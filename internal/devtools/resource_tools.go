package devtools

import (
	"context"
	"fmt"
	"net/url"
)

func resourceTools() []Tool {
	str := func(desc string) any { return map[string]any{"type": "string", "description": desc, "maxLength": 512} }
	props := func() map[string]any {
		return map[string]any{"service": str("应用标识"), "environment": str("明确的目标环境")}
	}
	makeTool := func(name, title, scope string, p map[string]any, required ...string) Tool {
		annotations := readonly(title)
		if isWriteScope(scope) {
			annotations = map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": false, "openWorldHint": true}
		}
		return Tool{Name: name, Title: title, Description: title + "。仅管理员本人显式资源权限可用；不返回凭据，不执行 SQL、Redis 变更或发布配置。", Scope: scope, InputSchema: objectSchema(p, required...), Annotations: annotations}
	}
	config := map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "仅公开目标参数，不接受密码、Token、私钥"}
	declaration := objectSchema(map[string]any{"alias": str("逻辑资源别名"), "kind": map[string]any{"type": "string", "enum": []string{"mysql", "nacos", "redis"}}, "required": map[string]any{"type": "boolean"}, "description": str("资源用途")}, "alias", "kind")
	declaration["properties"].(map[string]any)["sources"] = map[string]any{"type": "object", "additionalProperties": objectSchema(map[string]any{"state": map[string]any{"type": "string", "enum": []string{"declared", "not_applicable", "pending_source", "missing_config"}}, "file": str("源文件相对路径"), "digest": str("来源摘要，不是执行凭证"), "runtime_name": str("源码运行名"), "profile": str("识别profile"), "repository": str("来源仓库，不含认证信息"), "artifact": str("配置的产物路径")}, "state")}
	binding := objectSchema(map[string]any{"alias": str("已有或本次声明的自有别名"), "environment": str("必须与所选环境相同"), "connection": str("已有连接名"), "config": config}, "alias", "environment", "connection")
	patch := props()
	patch["revision"] = map[string]any{"type": "integer", "minimum": 0}
	patch["requirements"] = map[string]any{"type": "array", "maxItems": 100, "items": declaration}
	patch["bindings"] = map[string]any{"type": "array", "minItems": 0, "maxItems": 100, "items": binding}
	discovery := props()
	discovery["connection"] = str("可选已保存 Nacos 连接名")
	discovery["profile"] = str("源码配置 profile")
	discovery["bootstrap_path"] = str("可选受管理工作区内 bootstrap 相对路径；不能读取任意文件")
	discovery["runtime_name"] = str("平台ID与源码运行名不同时显式指定；受管理源码仍须匹配本应用产物模块")
	discovery["source_format"] = map[string]any{"type": "string", "enum": []string{"yaml", "properties"}}
	discovery["adapter"] = map[string]any{"type": "string", "enum": []string{"spring", "custom", "flink"}}
	discovery["job"] = str("Flink配置中本次唯一作业名；未指定时只查询可选作业，不展开数据源")
	discovery["config_data_id"] = str("固定配置适配器的准确Data ID")
	discovery["config_group"] = str("固定配置适配器的准确Group")
	connection := map[string]any{"name": str("目标连接名称"), "kind": map[string]any{"type": "string", "enum": []string{"mysql", "nacos", "redis"}}, "endpoint": str("公开服务地址，不含凭据"), "config": config, "revision": str("当前连接 revision；新建填 missing"), "credential_connection": str("同类型已保存凭据来源；仅服务端复制当前凭据快照，不返回明文")}
	schema := props()
	schema["alias"] = str("当前环境已绑定的MySQL资源别名；不接收连接/库名/SQL")
	column := objectSchema(map[string]any{"name": str("字段名"), "type": str("期望MySQL完整类型，可选"), "nullable": map[string]any{"type": "boolean"}, "default_is_null": map[string]any{"type": "boolean"}, "default": str("期望默认值，仅返回匹配结果，不回传实际值"), "generated": map[string]any{"type": "boolean"}}, "name")
	index := objectSchema(map[string]any{"name": str("索引名"), "unique": map[string]any{"type": "boolean"}, "columns": map[string]any{"type": "array", "maxItems": 32, "items": str("按顺序的列名")}}, "name")
	schema["tables"] = map[string]any{"type": "array", "minItems": 1, "maxItems": 20, "items": objectSchema(map[string]any{"name": str("精确表名，不接受跨库/通配符"), "columns": map[string]any{"type": "array", "maxItems": 100, "items": column}, "indexes": map[string]any{"type": "array", "maxItems": 100, "items": index}}, "name")}
	keys := props()
	keys["alias"] = str("当前环境已绑定的Nacos别名，使用绑定的Data ID/Group")
	keys["format"] = map[string]any{"type": "string", "enum": []string{"yaml", "json", "properties"}}
	keys["keys"] = map[string]any{"type": "array", "minItems": 1, "maxItems": 50, "items": str("明确白名单配置键；不返回值、不枚举其他键")}
	return []Tool{
		makeTool("check_database_schema", "只读核对绑定数据库的指定表、字段、默认值和索引差异", "resources:read", schema, "service", "environment", "alias", "tables"),
		makeTool("check_configuration_keys", "只读核对绑定配置中的白名单键存在性及类型，不返回任何值", "resources:read", keys, "service", "environment", "alias", "format", "keys"),
		makeTool("list_resource_connections", "查询连接元数据；指定应用和环境时仅返回已登记到该环境的Nacos接入候选；全局列表仍需全部应用和环境授权", "resources:read", props()),
		makeTool("check_application_resources", "只读批量核验应用在指定环境的已保存资源", "resources:read", props(), "service", "environment"),
		makeTool("discover_application_resources", "从受管理源码和指定环境 Nacos 识别依赖候选，不自动保存", "resources:read", discovery, "service", "environment"),
		makeTool("configure_application_resources", "预览所选环境的自有资源绑定；保留其他环境和共享资源", "resources:write", patch, "service", "environment", "revision", "bindings"),
		makeTool("configure_resource_connection", "预览接入资源连接并复用已保存凭据（需全部应用和环境授权）", "resources:write", connection, "name", "kind", "endpoint", "revision", "credential_connection"),
	}
}
func resourceAccess(t *AccessToken, service, environment string, global bool) error {
	if !t.verified || t.role != "admin" || t.Owner == "" || t.Owner == "local" {
		return fmt.Errorf("资源接入需要管理员本人已验证的令牌")
	}
	if global {
		if !contains(t.Applications, "*") || !contains(t.Environments, "*") {
			return fmt.Errorf("共享连接需要显式授权全部应用和环境 *")
		}
	} else if service == "" || environment == "" || service == "*" || environment == "*" {
		return fmt.Errorf("资源操作必须明确一个应用和环境")
	}
	return t.Allows(service, environment)
}
func projectResourceEnvironment(v any, environment string) any {
	m := mapValue(v)
	for _, field := range []string{"requirements", "own_requirements"} {
		rows, _ := m[field].([]any)
		for _, row := range rows {
			req := mapValue(row)
			if sources, ok := req["sources"].(map[string]any); ok {
				filtered := map[string]any{}
				if source, exists := sources[environment]; exists {
					filtered[environment] = source
				}
				req["sources"] = filtered
			}
		}
	}
	for _, field := range []string{"bindings", "own_bindings", "nacos_configs", "checks"} {
		rows, _ := m[field].([]any)
		out := []any{}
		for _, row := range rows {
			if stringArg(mapValue(row), "environment") == environment {
				out = append(out, row)
			}
		}
		if _, ok := m[field]; ok {
			m[field] = out
		}
	}
	return m
}
func (s *ToolService) callResourceRead(ctx context.Context, t *AccessToken, name string, args map[string]any) (any, error) {
	service, environment := stringArg(args, "service"), stringArg(args, "environment")
	if err := resourceAccess(t, service, environment, name == "list_resource_connections" && service == "" && environment == ""); err != nil {
		return nil, err
	}
	path := "/api/services/" + url.PathEscape(service) + "/resources"
	switch name {
	case "list_resource_connections":
		listPath := "/api/connections"
		var listQuery url.Values
		if service != "" || environment != "" {
			listPath = path + "/discovery-connections"
			listQuery = url.Values{"environment": {environment}}
		}
		v, err := s.platformRequest(ctx, t, "GET", listPath, listQuery, nil)
		if err != nil {
			return nil, err
		}
		rows, _ := v.([]any)
		out := []any{}
		for _, row := range rows {
			m := mapValue(row)
			kind := stringArg(m, "kind")
			if kind != "mysql" && kind != "nacos" && kind != "redis" {
				continue
			}
			public := map[string]any{}
			for _, key := range []string{"name", "kind", "endpoint", "revision", "secret_configured", "last_check"} {
				public[key] = m[key]
			}
			config := map[string]any{}
			for _, key := range []string{"namespace", "nacos_auth", "nacos_version", "db", "tls", "redis_auth", "key_prefix", "key_prefixes"} {
				if value, ok := mapValue(m["config"])[key]; ok {
					config[key] = value
				}
			}
			public["config"] = config
			out = append(out, public)
		}
		return out, nil
	case "check_database_schema", "check_configuration_keys":
		body := mapValue(cloneJSON(args))
		delete(body, "service")
		suffix := "/schema-check"
		if name == "check_configuration_keys" {
			suffix = "/config-key-check"
		}
		return s.platformRequest(ctx, t, "POST", path+suffix, nil, body)
	case "check_application_resources":
		return s.platformRequest(ctx, t, "POST", path+"/check", nil, map[string]any{"environments": []string{environment}})
	case "discover_application_resources":
		initial := false
		// A token limited to one environment cannot use a different environment's
		// global connection just by supplying its name or a different source profile.
		if !contains(t.Applications, "*") || !contains(t.Environments, "*") {
			v, err := s.platformRequest(ctx, t, "GET", path, nil, nil)
			if err != nil {
				return nil, err
			}
			allowed := map[string]bool{}
			kinds := map[string]string{}
			reqs, _ := mapValue(v)["requirements"].([]any)
			for _, r := range reqs {
				kinds[stringArg(mapValue(r), "alias")] = stringArg(mapValue(r), "kind")
			}
			bindings, _ := mapValue(v)["bindings"].([]any)
			for _, b := range bindings {
				m := mapValue(b)
				if stringArg(m, "environment") == environment && kinds[stringArg(m, "alias")] == "nacos" {
					allowed[stringArg(m, "connection")] = true
				}
			}
			selected := stringArg(args, "connection")
			if selected == "" && len(allowed) == 1 {
				for name := range allowed {
					selected = name
				}
			}
			if !allowed[selected] {
				if selected == "" {
					return nil, fmt.Errorf("首次接入请先按应用与环境查询list_resource_connections，并明确选择Nacos")
				}
				v, err := s.platformRequest(ctx, t, "GET", path+"/discovery-connections", url.Values{"environment": {environment}}, nil)
				if err != nil {
					return nil, err
				}
				approved := false
				rows, _ := v.([]any)
				for _, row := range rows {
					m := mapValue(row)
					approved = approved || stringArg(m, "name") == selected && stringArg(m, "kind") == "nacos"
				}
				if !approved {
					return nil, fmt.Errorf("该Nacos未登记到目标环境，不能用于首次识别")
				}
				initial = true
			}
			args = mapValue(cloneJSON(args))
			args["connection"] = selected
		}
		body := map[string]any{"environments": []string{environment}, "bootstrap_path": stringArg(args, "bootstrap_path")}
		if initial {
			body["require_environment_scope"] = true
		}
		for _, key := range []string{"runtime_name", "source_format", "adapter", "job", "config_data_id", "config_group"} {
			if v := stringArg(args, key); v != "" {
				body[key] = v
			}
		}
		if connection := stringArg(args, "connection"); connection != "" {
			body["connections"] = map[string]string{environment: connection}
		}
		if profile := stringArg(args, "profile"); profile != "" {
			body["profiles"] = map[string]string{environment: profile}
		}
		return s.platformRequest(ctx, t, "POST", path+"/discover", nil, body)
	}
	return nil, fmt.Errorf("不支持的资源读取")
}
func (s *ToolService) prepareResourceAction(ctx context.Context, t *AccessToken, name string, args map[string]any, a *PlatformAction) error {
	service, environment := stringArg(args, "service"), stringArg(args, "environment")
	if err := resourceAccess(t, service, environment, name == "configure_resource_connection"); err != nil {
		return err
	}
	body := mapValue(cloneJSON(args))
	delete(body, "service")
	if name == "configure_resource_connection" {
		a.Path = "/api/connections/configure"
		a.BaselinePath = "/api/connections/" + url.PathEscape(stringArg(args, "name")) + "/metadata"
	} else {
		a.Path = "/api/services/" + url.PathEscape(service) + "/resources/bindings"
		a.BaselinePath = "/api/services/" + url.PathEscape(service) + "/resources"
	}
	a.BaselineField = "revision"
	preview, err := s.platformRequest(ctx, t, "POST", a.Path+"/preview", nil, body)
	if err != nil {
		return err
	}
	a.Preview = mapValue(preview)
	a.Body = mapValue(a.Preview["request"])
	if a.Body == nil {
		return fmt.Errorf("上游未返回冻结的资源请求")
	}
	// Use the revision actually reviewed, never a later GET which could hide a race.
	a.Baseline = fingerprint(a.Body["revision"])
	a.Query = url.Values{"service": {service}, "environment": {environment}}
	return nil
}
