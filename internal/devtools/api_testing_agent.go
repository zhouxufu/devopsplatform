package devtools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

func testingPageSchema() map[string]any {
	return map[string]any{"q": map[string]any{"type": "string", "description": "按名称/编号/模块搜索，最多200字符"}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "默认20，最多100"}, "before_id": map[string]any{"type": "integer", "minimum": 1, "description": "上页next_before_id；has_more为true时继续，包括空页；新查询不传"}}
}
func testingRunPageSchema() map[string]any {
	out := testingPageSchema()
	for _, key := range []string{"case_id", "suite_id", "from", "to"} {
		minimum := 0
		if key == "case_id" || key == "suite_id" {
			minimum = 1
		}
		out[key] = map[string]any{"type": "integer", "minimum": minimum, "description": "用例/测试集编号或Unix秒时间范围"}
	}
	out["status"] = map[string]any{"type": "string", "enum": []string{"queued", "running", "success", "failed", "unknown", "canceled"}}
	out["source"] = map[string]any{"type": "string", "enum": []string{"manual", "case"}, "description": "case含用例及测试集，manual为临时请求"}
	return out
}
func (s *ToolService) callTestingCatalog(ctx context.Context, t *AccessToken, name string, args map[string]any) (any, error) {
	service, env := stringArg(args, "service"), stringArg(args, "environment")
	if service == "" {
		if name == "list_api_test_suites" {
			return nil, fmt.Errorf("测试集需要明确应用")
		}
		if err := scenarioAccess(t, env, nil); err != nil {
			return nil, err
		}
	} else if err := testingAccess(t, service, env); err != nil {
		return nil, err
	}
	kind := map[string]string{"list_api_test_cases": "cases", "list_api_test_suites": "suites", "list_api_test_runs": "runs"}[name]
	q := url.Values{"service": {service}, "environment": {env}, "kind": {kind}}
	// Only token-owned scope is forwarded. It is never accepted as a tool argument.
	if service == "" {
		raw, err := json.Marshal(t.Applications)
		if err != nil {
			return nil, err
		}
		q.Set("application_scope", string(raw))
	}
	for _, key := range []string{"q", "limit", "before_id", "case_id", "suite_id", "status", "source", "from", "to"} {
		if v, ok := args[key]; ok {
			q.Set(key, fmt.Sprint(v))
		}
	}
	v, err := s.platformRequest(ctx, t, "GET", "/api/api-testing/mcp/catalog", q, nil)
	if err != nil {
		return nil, err
	}
	if service == "" { // Defense in depth; upstream applies scope before paging.
		rows, _ := mapValue(v)["items"].([]any)
		for _, row := range rows {
			services, e := scenarioCaseServices(mapValue(row))
			if e != nil {
				return nil, e
			}
			if e = scenarioAccess(t, env, services); e != nil {
				return nil, e
			}
		}
	}
	return v, nil
}
func (s *ToolService) prepareTestingSuite(ctx context.Context, t *AccessToken, args map[string]any, a *PlatformAction) error {
	service, env := stringArg(args, "service"), stringArg(args, "environment")
	if err := testingAccess(t, service, env); err != nil {
		return err
	}
	suite := mapValue(cloneJSON(args["suite"]))
	raw, _ := json.Marshal(suite)
	if suite == nil || len(raw) > 384<<10 {
		return fmt.Errorf("缺少测试集或定义过大")
	}
	a.Query = url.Values{"service": {service}, "environment": {env}}
	checked, err := s.platformRequest(ctx, t, "POST", "/api/api-testing/mcp/suite-preview", a.Query, suite)
	if err != nil {
		return err
	}
	result := mapValue(checked)
	after := mapValue(result["after"])
	if after == nil || result["case_revisions"] == nil {
		return fmt.Errorf("测试集校验未返回规范化定义和用例修订")
	}
	ids, err := json.Marshal(after["case_ids"])
	if err != nil {
		return err
	}
	a.BaselinePath = "/api/api-testing/mcp/suite-context"
	a.BaselineQuery = url.Values{"service": {service}, "environment": {env}, "case_ids": {string(ids)}}
	a.Path = "/api/api-testing/mcp/suites"
	if intArg(after, "id", 0) > 0 {
		id, err := positiveID(after, "id")
		if err != nil {
			return err
		}
		a.BaselineQuery.Set("suite_id", id)
		a.Path += "/" + id
		a.Method = "PUT"
	}
	before, err := s.platformRequest(ctx, t, "GET", a.BaselinePath, a.BaselineQuery, nil)
	if err != nil {
		return err
	}
	if fingerprint(mapValue(before)["case_revisions"]) != fingerprint(result["case_revisions"]) {
		return fmt.Errorf("用例修订已变化，请重新预览")
	}
	if old := mapValue(mapValue(before)["suite"]); old != nil && fingerprint(old["revision"]) != fingerprint(after["revision"]) {
		return fmt.Errorf("测试集修订已变化，请重新预览")
	}
	a.Baseline = fingerprint(before)
	a.Body = map[string]any{"suite": after, "case_revisions": result["case_revisions"]}
	a.Preview = map[string]any{"service": service, "environment": env, "before": mapValue(before)["suite"], "after": after, "cases": result["cases"], "case_revisions": result["case_revisions"], "sends_requests": false}
	return nil
}
func (s *ToolService) prepareTestingStop(ctx context.Context, t *AccessToken, args map[string]any, a *PlatformAction) error {
	service, env := stringArg(args, "service"), stringArg(args, "environment")
	if service == "" {
		if err := scenarioAccess(t, env, nil); err != nil {
			return err
		}
	} else if err := testingAccess(t, service, env); err != nil {
		return err
	}
	id, err := positiveID(args, "run_id")
	if err != nil {
		return err
	}
	a.Query = url.Values{"service": {service}, "environment": {env}, "run_id": {id}}
	a.BaselinePath = "/api/api-testing/mcp/run-control"
	a.BaselineQuery = a.Query
	a.BaselineField = "identity"
	value, err := s.platformRequest(ctx, t, "GET", a.BaselinePath, a.Query, nil)
	if err != nil {
		return err
	}
	v := mapValue(value)
	identity := stringArg(v, "identity")
	if identity == "" || stringArg(v, "environment") != env || service != "" && stringArg(v, "service") != service {
		return fmt.Errorf("运行身份或环境不匹配")
	}
	if service == "" {
		services, e := scenarioCaseServices(v)
		if e != nil {
			return e
		}
		if e = scenarioAccess(t, env, services); e != nil {
			return e
		}
		a.TestingScenario = true
		a.TestingServices = services
	}
	a.Baseline = fingerprint(identity)
	a.Path = "/api/api-testing/mcp/stop"
	a.Body = map[string]any{"expected_identity": identity}
	a.Preview = map[string]any{"service": service, "environment": env, "services": a.TestingServices, "run": v["run"], "effect": "取消后续调度及在途接收；已发送的业务操作不回滚，确认后继续查询原run_id"}
	return nil
}
