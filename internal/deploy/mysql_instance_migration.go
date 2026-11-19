package deploy

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Convert editable legacy configuration atomically. Approved plans, query
// tickets and execution ledgers are evidence and must never be rewritten.
func (s *Store) migrateMySQLInstanceConnections() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	type connection struct {
		name   string
		config map[string]string
	}
	connections := []connection{}
	defaults := map[string]string{}
	rows, err := tx.Query(`SELECT name,config FROM resource_connections WHERE kind='mysql' ORDER BY name`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var name, raw string
		if err = rows.Scan(&name, &raw); err != nil {
			break
		}
		var config map[string]string
		if err = json.Unmarshal([]byte(raw), &config); err != nil {
			break
		}
		if database, exists := config["database"]; exists {
			if database != "" && !sqlIdentifierRE.MatchString(database) {
				err = fmt.Errorf("MySQL 连接 %s 的旧数据库名称无效", name)
				break
			}
			defaults[name] = database
			delete(config, "database")
			connections = append(connections, connection{name, config})
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return fmt.Errorf("MySQL 实例迁移读取失败: %w", err)
	}
	if len(connections) == 0 {
		return tx.Commit()
	}
	// Only the database selection moves; preserve every other binding field.
	fill := func(bindings []ApplicationEnvironmentResource) (bool, error) {
		changed := false
		for i := range bindings {
			b := &bindings[i]
			database := defaults[b.Connection]
			if database == "" || strings.TrimSpace(b.Config["database"]) != "" {
				continue
			}
			for key := range b.Config {
				if key != "database" {
					return false, fmt.Errorf("连接 %s 存在非法绑定覆盖，请先修复资源绑定", b.Connection)
				}
			}
			b.Config = map[string]string{"database": database}
			changed = true
		}
		return changed, nil
	}
	type bindingRow struct{ service, environment, alias, config string }
	updates := []bindingRow{}
	rows, err = tx.Query(`SELECT service,environment,alias,connection,COALESCE(config,'{}') FROM application_environment_resources`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var b ApplicationEnvironmentResource
		var raw string
		if err = rows.Scan(&b.Service, &b.Environment, &b.Alias, &b.Connection, &raw); err != nil {
			break
		}
		if defaults[b.Connection] == "" {
			continue
		}
		if err = json.Unmarshal([]byte(raw), &b.Config); err != nil {
			break
		}
		var changed bool
		if changed, err = fill([]ApplicationEnvironmentResource{b}); err != nil {
			break
		}
		if changed {
			config, _ := json.Marshal(map[string]string{"database": defaults[b.Connection]})
			updates = append(updates, bindingRow{b.Service, b.Environment, b.Alias, string(config)})
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return fmt.Errorf("MySQL 绑定迁移失败: %w", err)
	}
	// Raw JSON members retain unknown fields and group revisions verbatim.
	type document struct{ key, value string }
	documents := []document{}
	rows, err = tx.Query(`SELECT key,value FROM deploy_meta WHERE key LIKE 'resource_group:%' OR key LIKE 'application_resources_v2:%'`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var key, raw string
		if err = rows.Scan(&key, &raw); err != nil {
			break
		}
		var doc map[string]json.RawMessage
		if err = json.Unmarshal([]byte(raw), &doc); err != nil {
			break
		}
		field := "bindings"
		if strings.HasPrefix(key, "application_resources_v2:") {
			field = "own_bindings"
		}
		if len(doc[field]) == 0 {
			continue
		}
		var bindings []map[string]json.RawMessage
		if err = json.Unmarshal(doc[field], &bindings); err != nil {
			break
		}
		changed := false
		for _, binding := range bindings {
			var b ApplicationEnvironmentResource
			encoded, _ := json.Marshal(binding)
			if err = json.Unmarshal(encoded, &b); err != nil {
				break
			}
			selected := []ApplicationEnvironmentResource{b}
			var updated bool
			if updated, err = fill(selected); err != nil {
				break
			}
			if updated {
				binding["config"], _ = json.Marshal(selected[0].Config)
				changed = true
			}
		}
		if err != nil {
			break
		}
		if changed {
			doc[field], _ = json.Marshal(bindings)
			encoded, _ := json.Marshal(doc)
			documents = append(documents, document{key, string(encoded)})
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return fmt.Errorf("MySQL 资源文档迁移失败: %w", err)
	}
	for _, b := range updates {
		if _, err = tx.Exec(`UPDATE application_environment_resources SET config=? WHERE service=? AND environment=? AND alias=?`, b.config, b.service, b.environment, b.alias); err != nil {
			return err
		}
	}
	for _, doc := range documents {
		if _, err = tx.Exec(`UPDATE deploy_meta SET value=? WHERE key=?`, doc.value, doc.key); err != nil {
			return err
		}
	}
	for _, c := range connections {
		// This is an authorization restriction, never a default database. Direct
		// queries must now select a database explicitly, including migrated ones.
		if database := defaults[c.name]; database != "" {
			if _, err = tx.Exec(`INSERT OR IGNORE INTO deploy_meta(key,value) VALUES(?,?)`, "mysql_direct_query_scope:"+c.name, database); err != nil {
				return err
			}
		}
		raw, _ := json.Marshal(c.config)
		if _, err = tx.Exec(`UPDATE resource_connections SET config=? WHERE name=?`, string(raw), c.name); err != nil {
			return err
		}
	}
	return tx.Commit()
}
