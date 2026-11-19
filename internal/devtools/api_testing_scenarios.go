package devtools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
)

func supportsScenarioTool(name string) bool {
	switch name {
	case "list_api_test_cases", "get_api_test_case", "validate_api_test_case", "save_api_test_case", "get_api_test_layout", "save_api_test_layout", "get_api_test_context", "execute_api_test", "get_api_test_run", "list_api_test_runs", "stop_api_test_run":
		return true
	}
	return false
}
func scenarioAccess(t *AccessToken, environment string, services []string) error {
	if environment == "" || environment == "*" || !t.verified || t.Owner == "" || t.Owner == "local" {
		return fmt.Errorf("业务场景需要已验证用户和具体环境")
	}
	if err := t.Allows("", environment); err != nil {
		return err
	}
	for _, service := range services {
		if service == "" || service == "*" {
			return fmt.Errorf("接口节点必须明确实际应用")
		}
		if err := t.Allows(service, environment); err != nil {
			return err
		}
	}
	return nil
}
func scenarioCaseServices(c map[string]any) ([]string, error) {
	seen := map[string]bool{}
	if rows, ok := c["steps"].([]any); ok {
		for _, raw := range rows {
			step := mapValue(raw)
			kind := stringArg(step, "kind")
			if kind != "" && kind != "http" {
				continue
			}
			name := stringArg(step, "service")
			if name == "" || name == "*" {
				return nil, fmt.Errorf("每个接口节点必须选择实际应用")
			}
			seen[name] = true
		}
	}
	if rows, ok := c["services"].([]any); ok {
		for _, raw := range rows {
			if name, ok := raw.(string); ok && name != "" && name != "*" {
				seen[name] = true
			}
		}
	}
	services := []string{}
	for name := range seen {
		services = append(services, name)
	}
	sort.Strings(services)
	if len(services) == 0 {
		return nil, fmt.Errorf("场景没有有效应用节点")
	}
	return services, nil
}
func scenarioRunServices(v map[string]any) ([]string, error) {
	if c := mapValue(v["case_snapshot"]); c != nil {
		return scenarioCaseServices(c)
	}
	return nil, fmt.Errorf("场景记录缺少冻结用例身份")
}
func (s *ToolService) readScenarioCase(ctx context.Context, t *AccessToken, id, environment string) (map[string]any, []string, error) {
	v, err := s.platformRequest(ctx, t, "GET", "/api/api-testing/scenario-cases/"+id, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	c := mapValue(v)
	services, err := scenarioCaseServices(c)
	if err == nil {
		err = scenarioAccess(t, environment, services)
	}
	return c, services, err
}
func (s *ToolService) callScenarioRead(ctx context.Context, t *AccessToken, name string, args map[string]any) (any, error) {
	environment := stringArg(args, "environment")
	if err := scenarioAccess(t, environment, nil); err != nil {
		return nil, err
	}
	q := url.Values{"environment": {environment}}
	if name == "list_api_test_cases" {
		v, err := s.platformRequest(ctx, t, "GET", "/api/api-testing/scenario-cases", q, nil)
		if err != nil {
			return nil, err
		}
		out := mapValue(v)
		items := []any{}
		rows, _ := out["cases"].([]any)
		for _, raw := range rows {
			services, e := scenarioCaseServices(mapValue(raw))
			if e == nil && scenarioAccess(t, environment, services) == nil {
				items = append(items, raw)
			}
		}
		return map[string]any{"scope": "scenario", "cases": items, "truncated": out["truncated"]}, nil
	}
	if name == "validate_api_test_case" {
		c := mapValue(args["case"])
		services, err := scenarioCaseServices(c)
		if err != nil {
			return nil, err
		}
		if err = scenarioAccess(t, environment, services); err != nil {
			return nil, err
		}
		if intArg(c, "id", 0) > 0 {
			id, err := positiveID(c, "id")
			if err != nil {
				return nil, err
			}
			if _, _, err = s.readScenarioCase(ctx, t, id, environment); err != nil {
				return nil, err
			}
		}
		return s.platformRequest(ctx, t, "POST", "/api/api-testing/scenario-cases/validate", q, c)
	}
	if name == "get_api_test_run" {
		id, err := positiveID(args, "run_id")
		if err != nil {
			return nil, err
		}
		v, err := s.platformRequest(ctx, t, "GET", "/api/api-testing/scenario-runs/"+id, q, nil)
		if err != nil {
			return nil, err
		}
		run := mapValue(v)
		services, err := scenarioRunServices(run)
		if err != nil {
			return nil, err
		}
		if err = scenarioAccess(t, environment, services); err != nil {
			return nil, err
		}
		if stringArg(run, "environment") != environment {
			return nil, fmt.Errorf("记录不属于指定环境")
		}
		return v, nil
	}
	id, err := positiveID(args, "case_id")
	if err != nil {
		return nil, err
	}
	c, _, err := s.readScenarioCase(ctx, t, id, environment)
	if err != nil {
		return nil, err
	}
	switch name {
	case "get_api_test_case":
		return map[string]any{"scope": "scenario", "case": c}, nil
	case "get_api_test_layout":
		q.Set("workspace", stringArg(args, "workspace"))
		return s.platformRequest(ctx, t, "GET", "/api/api-testing/scenario-cases/"+id+"/layout", q, nil)
	case "get_api_test_context":
		return s.platformRequest(ctx, t, "GET", "/api/api-testing/scenario-cases/"+id+"/context", q, nil)
	}
	return nil, fmt.Errorf("不支持的业务场景读取")
}
func (s *ToolService) prepareScenarioAction(ctx context.Context, t *AccessToken, name string, args map[string]any, a *PlatformAction) error {
	environment := stringArg(args, "environment")
	if err := scenarioAccess(t, environment, nil); err != nil {
		return err
	}
	a.TestingScenario = true
	a.Query = url.Values{"environment": {environment}}
	if name == "save_api_test_case" {
		c := mapValue(cloneJSON(args["case"]))
		raw, _ := json.Marshal(c)
		if c == nil || len(raw) > 384<<10 {
			return fmt.Errorf("用例定义为空或过大")
		}
		services, err := scenarioCaseServices(c)
		if err != nil {
			return err
		}
		if err = scenarioAccess(t, environment, services); err != nil {
			return err
		}
		a.TestingServices = services
		a.BaselinePath = "/api/api-testing/scenario-cases"
		a.BaselineQuery = a.Query
		var before any
		if intArg(c, "id", 0) > 0 {
			id, err := positiveID(c, "id")
			if err != nil {
				return err
			}
			old, _, err := s.readScenarioCase(ctx, t, id, environment)
			if err != nil {
				return err
			}
			before = old
			a.BaselinePath += "/" + id
			a.Path = a.BaselinePath
			a.Method = "PUT"
		} else {
			a.Path = a.BaselinePath
		}
		baseline, err := s.platformRequest(ctx, t, "GET", a.BaselinePath, a.BaselineQuery, nil)
		if err != nil {
			return err
		}
		a.Baseline = fingerprint(baseline)
		checked, err := s.platformRequest(ctx, t, "POST", "/api/api-testing/scenario-cases/validate", a.Query, c)
		if err != nil {
			return err
		}
		normalized := mapValue(mapValue(checked)["case"])
		if normalized == nil {
			return fmt.Errorf("场景校验未返回定义")
		}
		a.Body = normalized
		a.Preview = map[string]any{"scope": "scenario", "services": services, "environment": environment, "before": before, "after": normalized, "sends_requests": false}
		return nil
	}
	id, err := positiveID(args, "case_id")
	if err != nil {
		return err
	}
	c, services, err := s.readScenarioCase(ctx, t, id, environment)
	if err != nil {
		return err
	}
	a.TestingServices = services
	if name == "save_api_test_layout" {
		a.Query.Set("workspace", stringArg(args, "workspace"))
		a.Path = "/api/api-testing/scenario-cases/" + id + "/layout"
		a.Method = "PUT"
		before, err := s.platformRequest(ctx, t, "GET", a.Path, a.Query, nil)
		if err != nil {
			return err
		}
		checked, err := s.platformRequest(ctx, t, "POST", a.Path+"/validate", a.Query, mapValue(args["layout"]))
		if err != nil {
			return err
		}
		a.Body = mapValue(mapValue(checked)["layout"])
		if a.Body == nil {
			return fmt.Errorf("布局校验未返回定义")
		}
		a.BaselinePath = "/api/api-testing/scenario-cases/" + id
		a.BaselineQuery = url.Values{"environment": {environment}}
		a.Baseline = fingerprint(c)
		a.Preview = map[string]any{"scope": "scenario", "services": services, "case_id": args["case_id"], "before": before, "after": a.Body, "sends_requests": false}
		return nil
	}
	if name != "execute_api_test" {
		return fmt.Errorf("不支持的场景操作")
	}
	if args["request"] != nil || args["case_revision"] == nil || fingerprint(args["case_revision"]) != fingerprint(c["revision"]) {
		return fmt.Errorf("执行场景必须携带准确case_revision，不同时提交request")
	}
	a.BaselinePath = "/api/api-testing/scenario-cases/" + id + "/context"
	a.BaselineQuery = a.Query
	value, err := s.platformRequest(ctx, t, "GET", a.BaselinePath, a.Query, nil)
	if err != nil {
		return err
	}
	context := mapValue(value)
	a.Baseline = fingerprint(value)
	if context["protected"] == true && (t.role != "admin" || stringArg(args, "reason") == "") {
		return fmt.Errorf("受保护环境必须管理员确认全部应用并填写原因")
	}
	selections := mapValue(cloneJSON(args["targets"]))
	if len(selections) != len(services) {
		return fmt.Errorf("请传入所有应用的targets{service:{key,fingerprint,variables_digest}}")
	}
	rows, _ := context["applications"].([]any)
	preview := []any{}
	for _, raw := range rows {
		app := mapValue(raw)
		name := stringArg(app, "service")
		selection := mapValue(selections[name])
		if app["enabled"] != true {
			return fmt.Errorf("应用 %s 的接口测试已停用", name)
		}
		if stringArg(selection, "variables_digest") == "" || selection["variables_digest"] != app["variables_digest"] {
			return fmt.Errorf("应用 %s 的变量已变化", name)
		}
		var target map[string]any
		targets, _ := app["targets"].([]any)
		for _, raw := range targets {
			item := mapValue(raw)
			if item["key"] == selection["key"] {
				target = item
				break
			}
		}
		if target == nil || target["credential_references_valid"] != true || target["fingerprint"] != selection["fingerprint"] {
			return fmt.Errorf("应用 %s 的目标或认证已变化", name)
		}
		preview = append(preview, map[string]any{"service": name, "application_id": app["application_id"], "target": target, "protected": app["protected"]})
	}
	a.Path = "/api/api-testing/scenario-runs"
	a.Body = map[string]any{"request_id": a.ID, "environment": environment, "case_id": args["case_id"], "case_revision": args["case_revision"], "targets": selections, "confirm_protected": context["protected"] == true, "reason": stringArg(args, "reason")}
	a.Preview = map[string]any{"scope": "scenario", "services": services, "environment": environment, "case": c, "targets": preview, "sends_requests": true, "automatic_retries": false}
	return nil
}
