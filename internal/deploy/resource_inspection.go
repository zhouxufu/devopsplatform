package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type schemaColumnExpectation struct {
	Name          string  `json:"name"`
	Type          string  `json:"type,omitempty"`
	Nullable      *bool   `json:"nullable,omitempty"`
	Default       *string `json:"default,omitempty"`
	Generated     *bool   `json:"generated,omitempty"`
	DefaultIsNull *bool   `json:"default_is_null,omitempty"`
}
type schemaIndexExpectation struct {
	Name    string   `json:"name"`
	Unique  *bool    `json:"unique,omitempty"`
	Columns []string `json:"columns,omitempty"`
}
type schemaTableExpectation struct {
	Name    string                    `json:"name"`
	Columns []schemaColumnExpectation `json:"columns,omitempty"`
	Indexes []schemaIndexExpectation  `json:"indexes,omitempty"`
}
type resourceInspectionRequest struct {
	Environment string                   `json:"environment"`
	Alias       string                   `json:"alias"`
	Tables      []schemaTableExpectation `json:"tables,omitempty"`
	Keys        []string                 `json:"keys,omitempty"`
	Format      string                   `json:"format,omitempty"`
}

var inspectionNameRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_$-]{0,63}$`)

func (s *Store) inspectionBinding(service, environment, alias, kind string) (ApplicationEnvironmentResource, *ResourceConnection, error) {
	if !s.hasResourceApplication(service) || s.GetEnvironment(environment) == nil || !validResourceName(alias) {
		return ApplicationEnvironmentResource{}, nil, fmt.Errorf("请指定已有应用、环境和资源别名")
	}
	resources := s.ApplicationResources(service)
	if resources.Error != "" {
		return ApplicationEnvironmentResource{}, nil, fmt.Errorf("资源解析失败，请重新读取绑定")
	}
	declared := false
	for _, r := range resources.Requirements {
		if r.Alias == alias && r.Kind == kind {
			declared = true
		}
	}
	if declared {
		for _, b := range resources.Bindings {
			if b.Environment == environment && b.Alias == alias {
				c := s.GetConnection(b.Connection)
				if c != nil && c.Kind == kind {
					if kind == "nacos" && (strings.TrimSpace(b.Config["data_id"]) == "" || strings.TrimSpace(b.Config["group"]) == "") {
						return ApplicationEnvironmentResource{}, nil, fmt.Errorf("配置检查需要此环境明确绑定Data ID和Group；不使用连接默认值或其他环境")
					}
					return b, c, nil
				}
			}
		}
	}
	return ApplicationEnvironmentResource{}, nil, fmt.Errorf("指定环境的资源尚未绑定或类型不匹配；不会查找其他环境/连接")
}
func validateInspection(req resourceInspectionRequest, kind string) error {
	if kind == "mysql" {
		if len(req.Tables) < 1 || len(req.Tables) > 20 || len(req.Keys) > 0 || req.Format != "" {
			return fmt.Errorf("结构检查需显式指定1–20张表，不接受SQL、跨库名称或配置键")
		}
		seen := map[string]bool{}
		for _, t := range req.Tables {
			if !inspectionNameRE.MatchString(t.Name) || seen[t.Name] || len(t.Columns) > 100 || len(t.Indexes) > 100 {
				return fmt.Errorf("表名重复、无效或期望字段/索引超过100项")
			}
			seen[t.Name] = true
			for _, c := range t.Columns {
				if !inspectionNameRE.MatchString(c.Name) || len(c.Type) > 128 || c.Default != nil && len(*c.Default) > 1024 {
					return fmt.Errorf("字段期望无效")
				}
			}
			for _, i := range t.Indexes {
				if !inspectionNameRE.MatchString(i.Name) || len(i.Columns) > 32 {
					return fmt.Errorf("索引期望无效")
				}
				for _, c := range i.Columns {
					if !inspectionNameRE.MatchString(c) {
						return fmt.Errorf("索引列名无效")
					}
				}
			}
		}
	} else {
		if len(req.Keys) < 1 || len(req.Keys) > 50 || len(req.Tables) > 0 {
			return fmt.Errorf("仅检查显式列出的1–50个白名单配置键，不接受通配/内容查询")
		}
		if req.Format != "yaml" && req.Format != "json" && req.Format != "properties" {
			return fmt.Errorf("配置格式须明确为yaml/json/properties")
		}
		seen := map[string]bool{}
		for _, k := range req.Keys {
			if len(k) > 256 || !nacosKeyRE.MatchString(k) || seen[k] {
				return fmt.Errorf("配置键重复、无效或包含通配符")
			}
			seen[k] = true
		}
	}
	return nil
}

// Every query is hard-coded, parameterized and limited to one bound database
// and one explicitly named table. No business table SELECT, SHOW CREATE or SQL input.
func readInspectionTable(ctx context.Context, db interface {
	sqlQueryer
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, database string, expect schemaTableExpectation) (map[string]any, error) {
	out := map[string]any{"name": expect.Name, "exists": false, "columns": []any{}, "indexes": []any{}, "differences": []string{}}
	var engine, collation sql.NullString
	err := db.QueryRowContext(ctx, `SELECT ENGINE,TABLE_COLLATION FROM information_schema.TABLES WHERE TABLE_SCHEMA=? AND TABLE_NAME=? AND TABLE_TYPE='BASE TABLE'`, database, expect.Name).Scan(&engine, &collation)
	if err == sql.ErrNoRows {
		out["differences"] = []string{"table_missing"}
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	out["exists"] = true
	out["engine"] = engine.String
	out["collation"] = collation.String
	cols := map[string]map[string]any{}
	defaults := map[string]sql.NullString{}
	colout := []any{}
	diff := []string{}
	rows, err := db.QueryContext(ctx, `SELECT COLUMN_NAME,COLUMN_TYPE,IS_NULLABLE,COLUMN_DEFAULT,EXTRA,GENERATION_EXPRESSION FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=? AND TABLE_NAME=? ORDER BY ORDINAL_POSITION LIMIT 501`, database, expect.Name)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		if len(colout) >= 500 {
			rows.Close()
			return nil, fmt.Errorf("表字段超过500项，请缩小检查范围")
		}
		var n, typ, nullable, extra, generation string
		var def sql.NullString
		if err = rows.Scan(&n, &typ, &nullable, &def, &extra, &generation); err != nil {
			rows.Close()
			return nil, err
		}
		if len(typ) > 4096 {
			rows.Close()
			return nil, fmt.Errorf("字段类型元数据过大")
		}
		c := map[string]any{"name": n, "type": typ, "nullable": nullable == "YES", "default_is_null": !def.Valid, "has_default": def.Valid, "generated": generation != "", "auto_increment": strings.Contains(extra, "auto_increment")}
		cols[n] = c
		defaults[n] = def
		colout = append(colout, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, e := range expect.Columns {
		c := cols[e.Name]
		if c == nil {
			diff = append(diff, "column_missing:"+e.Name)
			continue
		}
		if e.Type != "" && !strings.EqualFold(e.Type, c["type"].(string)) {
			diff = append(diff, "column_type:"+e.Name)
		}
		if e.Nullable != nil && *e.Nullable != c["nullable"] {
			diff = append(diff, "column_nullable:"+e.Name)
		}
		if e.Generated != nil && *e.Generated != c["generated"] {
			diff = append(diff, "column_generated:"+e.Name)
		}
		if e.DefaultIsNull != nil && *e.DefaultIsNull != c["default_is_null"] {
			diff = append(diff, "column_default_null:"+e.Name)
		}
		if e.Default != nil {
			v := defaults[e.Name]
			matches := v.Valid && v.String == *e.Default
			c["default_matches"] = matches
			if !matches {
				diff = append(diff, "column_default:"+e.Name)
			}
		}
	}
	indexes := map[string]map[string]any{}
	idxout := []any{}
	rows, err = db.QueryContext(ctx, `SELECT INDEX_NAME,NON_UNIQUE,COLUMN_NAME,SUB_PART,INDEX_TYPE FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=? AND TABLE_NAME=? ORDER BY INDEX_NAME,SEQ_IN_INDEX LIMIT 501`, database, expect.Name)
	if err != nil {
		return nil, err
	}
	count := 0
	for rows.Next() {
		count++
		if count > 500 {
			rows.Close()
			return nil, fmt.Errorf("索引元数据超过500项")
		}
		var n, typ string
		var nonunique int
		var column sql.NullString
		var prefix sql.NullInt64
		if err = rows.Scan(&n, &nonunique, &column, &prefix, &typ); err != nil {
			rows.Close()
			return nil, err
		}
		idx := indexes[n]
		if idx == nil {
			idx = map[string]any{"name": n, "unique": nonunique == 0, "type": typ, "columns": []string{}, "complex": false}
			indexes[n] = idx
			idxout = append(idxout, idx)
		}
		if !column.Valid || prefix.Valid {
			idx["complex"] = true
		}
		idx["columns"] = append(idx["columns"].([]string), column.String)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, e := range expect.Indexes {
		i := indexes[e.Name]
		if i == nil {
			diff = append(diff, "index_missing:"+e.Name)
			continue
		}
		if e.Unique != nil && *e.Unique != i["unique"] {
			diff = append(diff, "index_unique:"+e.Name)
		}
		if len(e.Columns) > 0 && (strings.Join(e.Columns, "\x00") != strings.Join(i["columns"].([]string), "\x00") || i["complex"] == true) {
			diff = append(diff, "index_columns:"+e.Name)
		}
	}
	out["columns"], out["indexes"], out["differences"] = colout, idxout, diff
	encoded, _ := json.Marshal(out)
	if len(encoded) > 128<<10 {
		return nil, fmt.Errorf("表结构超过128KiB检查上限")
	}
	return out, nil
}
func inspectionConfigNode(root *yaml.Node, parts []string) (*yaml.Node, error) {
	if root == nil || root.Kind != yaml.MappingNode {
		return nil, nil
	}
	var direct, child *yaml.Node
	for i := 0; i < len(root.Content); i += 2 {
		key := root.Content[i].Value
		if key == strings.Join(parts, ".") {
			direct = root.Content[i+1]
		}
		if key == parts[0] {
			child = root.Content[i+1]
		}
	}
	if len(parts) == 1 {
		return direct, nil
	}
	nested, err := inspectionConfigNode(child, parts[1:])
	if err != nil {
		return nil, err
	}
	if direct != nil && nested != nil {
		return nil, fmt.Errorf("配置存在点键/层级歧义，结果未判定")
	}
	if direct != nil {
		return direct, nil
	}
	return nested, nil
}

func inspectConfigKeys(content, format string, keys []string) ([]any, error) {
	if len(content) > nacosMaxContent {
		return nil, fmt.Errorf("配置超过检查上限")
	}
	result := []any{}
	values := map[string]string{}
	if format == "properties" {
		for _, line := range strings.Split(content, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
				continue
			}
			if strings.Contains(line, "\\") {
				return nil, fmt.Errorf("properties含转义或续行，当前检查不支持；结果未判定")
			}
			m := nacosPropertyRE.FindStringSubmatch(line)
			if m == nil {
				return nil, fmt.Errorf("properties格式不支持，结果未判定")
			}
			if _, ok := values[m[1]]; ok {
				return nil, fmt.Errorf("properties含重复键，结果未判定")
			}
			values[m[1]] = "string"
		}
		for _, k := range keys {
			typ, ok := values[k]
			result = append(result, map[string]any{"key": k, "exists": ok, "type": typ})
		}
		return result, nil
	}
	if format == "json" && !json.Valid([]byte(content)) {
		return nil, fmt.Errorf("配置不是有效JSON，结果未判定")
	}
	var doc yaml.Node
	decoder := yaml.NewDecoder(strings.NewReader(content))
	if decoder.Decode(&doc) != nil || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("配置不是单个对象，结果未判定")
	}
	var second yaml.Node
	if decoder.Decode(&second) != io.EOF {
		return nil, fmt.Errorf("不支持多文档配置")
	}
	if err := validateNacosTree(&doc); err != nil {
		return nil, fmt.Errorf("配置含歧义、别名或重复键，结果未判定")
	}
	for _, k := range keys {
		node, err := inspectionConfigNode(doc.Content[0], strings.Split(k, "."))
		if err != nil {
			return nil, err
		}
		typ := ""
		if node != nil {
			typ = strings.TrimPrefix(node.Tag, "!!")
		}
		result = append(result, map[string]any{"key": k, "exists": node != nil, "type": typ})
	}
	return result, nil
}
func (s *Store) inspectResource(ctx context.Context, service, kind string, req resourceInspectionRequest) (map[string]any, error) {
	if err := validateInspection(req, kind); err != nil {
		return nil, err
	}
	b, c, err := s.inspectionBinding(service, req.Environment, req.Alias, kind)
	if err != nil {
		return nil, err
	}
	identity := s.bindingCheckFingerprint(b)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out := map[string]any{"service": service, "environment": req.Environment, "alias": req.Alias, "checked_at": time.Now().Unix(), "binding_digest": identity, "read_only": true, "status": "checked", "values_returned": false, "visibility": "bound credential metadata only; missing objects may be absent or not visible; this observation is not execution approval"}
	if kind == "mysql" {
		target, e := mysqlDatabaseTarget(c, b.Config)
		if e != nil {
			return nil, e
		}
		db, e := openChangeMySQL(ctx, target)
		if e != nil {
			return nil, fmt.Errorf("结构读取失败：%s", resourceFailure(e).Message)
		}
		defer db.Close()
		var actual string
		if e = db.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&actual); e != nil || actual != target.Config["database"] {
			return nil, fmt.Errorf("目标库身份无法核验")
		}
		tables := []any{}
		different := false
		for _, t := range req.Tables {
			v, e := readInspectionTable(ctx, db, actual, t)
			if e != nil {
				return nil, fmt.Errorf("表结构读取失败，结果未判定")
			}
			tables = append(tables, v)
			if len(v["differences"].([]string)) > 0 {
				different = true
			}
		}
		out["tables"] = tables
		if different {
			out["status"] = "differences"
		}
	} else {
		client, e := newNacosClient(ctx, c)
		if e != nil {
			return nil, fmt.Errorf("配置连接失败：%s", resourceFailure(e).Message)
		}
		content, e := client.read(ctx, b.Config["data_id"], b.Config["group"])
		if e != nil {
			return nil, fmt.Errorf("配置读取失败：%s", resourceFailure(e).Message)
		}
		keys, e := inspectConfigKeys(content, req.Format, req.Keys)
		if e != nil {
			return nil, e
		}
		out["keys"] = keys
		out["data_id"] = b.Config["data_id"]
		out["group"] = b.Config["group"]
		for _, v := range keys {
			if !v.(map[string]any)["exists"].(bool) {
				out["status"] = "differences"
			}
		}
	}
	// A changed target/credential cannot be presented as current evidence.
	current, _, e := s.inspectionBinding(service, req.Environment, req.Alias, kind)
	if e != nil || s.bindingCheckFingerprint(current) != identity {
		return nil, fmt.Errorf("读取期间资源或凭据变化，请重新核对")
	}
	return out, nil
}
func registerResourceInspectionRoutes(mux *http.ServeMux, s *Store) {
	for path, kind := range map[string]string{"schema-check": "mysql", "config-key-check": "nacos"} {
		mux.HandleFunc("POST /api/services/{name}/resources/"+path, func(w http.ResponseWriter, r *http.Request) {
			if deliveryActor(r) != "local" && r.Header.Get("X-Role") != "admin" {
				deliveryWriteError(w, 403, "资源结构检查需要管理员权限")
				return
			}
			var req resourceInspectionRequest
			d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
			d.DisallowUnknownFields()
			if d.Decode(&req) != nil {
				deliveryWriteError(w, 400, "检查参数无效")
				return
			}
			v, err := s.inspectResource(r.Context(), r.PathValue("name"), kind, req)
			if err != nil {
				deliveryWriteError(w, 400, err)
				return
			}
			s.Audit(deliveryActor(r), "inspect-"+path, "application-resources", r.PathValue("name"), map[string]any{"environment": req.Environment, "alias": req.Alias, "status": v["status"]})
			deliveryJSON(w, v)
		})
	}
}
