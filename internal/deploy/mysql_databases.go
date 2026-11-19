package deploy

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// A connection owns credentials and the instance. An application/environment
// binding owns its database. Never mutate the shared connection while resolving.
func mysqlDatabaseTarget(connection *ResourceConnection, binding map[string]string) (*ResourceConnection, error) {
	if connection == nil || connection.Kind != "mysql" {
		return nil, fmt.Errorf("MySQL 连接不存在")
	}
	for key := range binding {
		if key != "database" {
			return nil, fmt.Errorf("MySQL 资源绑定只允许选择数据库，地址和认证请在连接中配置")
		}
	}
	database := strings.TrimSpace(binding["database"])
	if !sqlIdentifierRE.MatchString(database) {
		return nil, fmt.Errorf("请选择数据库（schema），名称仅支持字母、数字、下划线")
	}
	copy := *connection
	copy.Config = map[string]string{}
	for key, value := range connection.Config {
		copy.Config[key] = value
	}
	copy.Config["database"] = database
	return &copy, nil
}

const mysqlDatabaseCatalogSQL = `SELECT SCHEMA_NAME FROM information_schema.SCHEMATA ORDER BY SCHEMA_NAME LIMIT 1001`

func readMySQLDatabases(ctx context.Context, db *sql.DB) ([]string, bool, error) {
	rows, err := db.QueryContext(ctx, mysqlDatabaseCatalogSQL)
	if err != nil {
		return nil, false, fmt.Errorf("数据库列表读取失败，请核对账号可见范围")
	}
	defer rows.Close()
	names := []string{}
	truncated := false
	scanned := 0
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			return nil, false, fmt.Errorf("数据库列表格式无效")
		}
		scanned++
		if scanned > 1000 {
			truncated = true
			break
		}
		if name == "information_schema" || name == "mysql" || name == "performance_schema" || name == "sys" || !sqlIdentifierRE.MatchString(name) {
			continue
		}
		names = append(names, name)
	}
	if rows.Err() != nil {
		return nil, false, fmt.Errorf("数据库列表读取中断，请重新读取")
	}
	sort.Strings(names)
	return names, truncated, nil
}

func registerMySQLDatabaseRoutes(mux *http.ServeMux, s *Store) {
	mux.HandleFunc("GET /api/connections/{name}/databases", func(w http.ResponseWriter, r *http.Request) {
		if role := r.Header.Get("X-Role"); role != "admin" && role != "" {
			deliveryWriteError(w, 403, "数据库目录读取需要管理员权限；可手动输入已知库名后按原查询审批执行")
			return
		}
		c := s.GetConnection(r.PathValue("name"))
		if c == nil || c.Kind != "mysql" {
			deliveryWriteError(w, 404, "MySQL 连接不存在")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		instance := *c
		instance.Config = map[string]string{}
		for key, value := range c.Config {
			if key != "database" {
				instance.Config[key] = value
			}
		}
		db, err := openChangeMySQL(ctx, &instance)
		if err != nil {
			check := resourceFailure(err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(502)
			deliveryJSON(w, map[string]any{"error": check.Message, "code": check.Code, "suggestion": check.Suggestion})
			return
		}
		defer db.Close()
		names, truncated, err := readMySQLDatabases(ctx, db)
		if err != nil {
			deliveryWriteError(w, 502, err)
			return
		}
		deliveryJSON(w, map[string]any{"databases": names, "truncated": truncated})
	})
}
