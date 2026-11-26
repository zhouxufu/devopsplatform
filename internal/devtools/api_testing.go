package devtools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"logplatform/pkg/apitest"
	"net/url"
	"strings"
)

func testingTools() []Tool {
	str := func(description string) any { return map[string]any{"type": "string", "description": description} }
	makeTool := func(name, title, scope string, extra map[string]any, required ...string) Tool {
		props := map[string]any{"service": str("应用稳定标识；用例/画布及执行工具省略时使用跨应用业务场景，每个HTTP或Dubbo节点必须指定service"), "environment": str("具体环境；必须在 Key 授权范围内")}
		for k, v := range extra {
			props[k] = v
		}
		keys := []string{"environment"}
		if !supportsScenarioTool(name) {
			keys = append(keys, "service")
		}
		for _, key := range required {
			if name == "execute_api_test" && (key == "target_key" || key == "target_fingerprint") {
				continue
			}
			keys = append(keys, key)
		}
		return Tool{Name: name, Title: title, Description: title + "。复用应用环境配置及受保护变量；不接收账号密码或 Token 明文。登录验证和执行须预览后明确确认；结果未知不能重放。", Scope: scope, InputSchema: objectSchema(props, keys...), Annotations: readonly(title)}
	}
	tools := []Tool{
		makeTool("list_api_test_suites", "分页搜索当前应用可见测试集", "apitests:read", testingPageSchema()),
		makeTool("get_api_test_suite", "读取测试集、完整用例及准确修订", "apitests:read", map[string]any{"suite_id": map[string]any{"type": "integer", "minimum": 1}}, "suite_id"),
		makeTool("validate_api_test_suite", "静态校验测试集顺序、共享和跨用例变量映射，不保存不执行", "apitests:read", map[string]any{"suite": map[string]any{"type": "object", "description": "id/revision/name/visibility/case_ids/bindings；新测试集id/revision为0；测试集属于明确service"}}, "suite"),
		makeTool("list_api_test_runs", "按游标和条件查询本人历史测试报告摘要", "apitests:read", testingRunPageSchema()),
		makeTool("get_api_test_layout", "读取本人指定用例和工作区的节点坐标、缩放和布局修订", "apitests:read", map[string]any{"case_id": map[string]any{"type": "integer", "minimum": 1}, "workspace": str("工作区标识，默认standalone")}, "case_id"),
		makeTool("list_api_test_cases", "分页搜索可见用例摘要及修订；按next_before_id继续，空页也须检查has_more", "apitests:read", testingPageSchema()),
		makeTool("get_api_test_case", "读取用例完整定义、稳定节点ID和修订；凭据必须使用变量引用", "apitests:read", map[string]any{"case_id": map[string]any{"type": "integer", "minimum": 1}}, "case_id"),
		makeTool("validate_api_test_case", "静态校验用例节点、变量、断言和接口引用，不保存不执行", "apitests:read", map[string]any{"case": map[string]any{"type": "object"}}, "case"),
		makeTool("get_api_test_context", "读取接口测试入口、目标指纹和可引用变量名（不返回变量值）", "apitests:read", map[string]any{"suite_id": map[string]any{"type": "integer", "minimum": 1, "description": "读取测试集及全部用例修订，必须指定service，不与case_id同传"}, "case_id": map[string]any{"type": "integer", "minimum": 1, "description": "service省略时必填，读取业务场景所有应用的环境入口"}}),
		makeTool("get_api_test_profile", "读取应用环境的认证与直连/网关入口配置（仅管理员，旧明文脱敏）", "apitests:read", nil),
		makeTool("get_api_test_run", "查询本人接口测试记录及脱敏身份和实际地址", "apitests:read", map[string]any{"run_id": map[string]any{"type": "integer", "minimum": 1}}, "run_id"),
	}
	for _, tool := range []Tool{
		makeTool("save_api_test_suite", "预览新增或修改测试集，冻结全部用例修订；明确确认后保存，不执行", "apitests:write", map[string]any{"suite": map[string]any{"type": "object", "description": "完整测试集id/revision/name/visibility/case_ids/bindings；保留读取的修订"}}, "suite"),
		makeTool("stop_api_test_run", "预览停止本人指定测试运行；确认后取消后续调度及在途接收，不代表业务回滚，继续读取原报告", "apitests:execute", map[string]any{"run_id": map[string]any{"type": "integer", "minimum": 1}}, "run_id"),
		makeTool("save_api_test_layout", "预览保存本人画布布局，不修改用例执行修订", "apitests:write", map[string]any{"case_id": map[string]any{"type": "integer", "minimum": 1}, "workspace": str("工作区标识，默认standalone"), "layout": map[string]any{"type": "object", "description": "get_api_test_layout返回的revision、positions{node_id:{x,y}}、zoom、pan{x,y}。仅允许布局字段。"}}, "case_id", "layout"),
		makeTool("save_api_test_case", "预览新增或修改用例；完整定义携带id/revision/steps，明确确认后保存，不执行测试", "apitests:write", map[string]any{"case": map[string]any{"type": "object", "description": "get_api_test_case返回的case，保留步骤id；图模式增加graph{entry,concurrency,edges:[{from,to,branch}]}；步骤kind支持http/dubbo/condition/parallel/join；condition配置variable(step.NAME/data.NAME/case.NAME)、operator(equals/not_equals/greater/less/exists)、expected{type,value}；条件连线branch=true/false；新用例id/revision为0。与页面共用步骤定义和CAS。"}}, "case"),
		makeTool("configure_api_testing", "预览保存测试入口与认证配置（管理员）；凭据只引用 {{var.NAME}}", "apitests:write", map[string]any{"profile": map[string]any{"type": "object", "description": "get_api_test_profile 返回的 profile，携带 revision；base_url/auth 为默认入口；entries 数组各项 key、label、kind(direct/gateway)、base_url、path_prefix、auth。auth 支持 mode(none/bearer/login)、base_url(独立登录地址)、path、body_type(json/form)、body、headers、token_pointer、token_variable、injection(header/query/cookie)、header/name、prefix、identity_pointer、expires_in_seconds、refresh_mode(per_request)。"}}, "profile"),
		makeTool("verify_api_authentication", "预览仅验证登录和 Token 提取，不发送业务请求；身份只以配置的登录响应字段为证据", "apitests:execute", map[string]any{"target_key": str("get_api_test_context 返回的入口 key"), "target_fingerprint": str("当前目标 fingerprint"), "reason": str("验证目的；受保护环境必填")}, "target_key", "target_fingerprint"),
		makeTool("execute_api_test", "预览执行 HTTP/SSE/Dubbo 调试请求、已保存用例或测试集（不自动重试）", "apitests:execute", map[string]any{"suite_id": map[string]any{"type": "integer", "minimum": 1, "description": "执行单应用测试集，与case_id和request互斥"}, "suite_revision": map[string]any{"type": "integer", "minimum": 1}, "case_revisions": map[string]any{"type": "object", "description": "get_api_test_context返回的case_revisions；测试集中每个用例ID映射准确修订"}, "targets": map[string]any{"type": "object", "description": "跨应用场景必填：按service映射get_api_test_context中的key/fingerprint/variables_digest；省略service，提供case_id/case_revision，不传request"}, "target_key": str("当前目标 key"), "target_fingerprint": str("当前目标 fingerprint"), "name": str("测试名称"), "request": map[string]any{"type": "object", "description": "Dubbo 使用 mode=dubbo、rpc{interface,method,group,version,parameter_types}、body=有序JSON数组、headers=Attachments；Dubbo 断言 rpc_success/exception/json/duration，目标选已配置 Dubbo 入口。HTTP/SSE Request：method、path(相对路径)、headers、body_type、body、form、mode、timeout_seconds、max_events、assertions；凭据使用受保护变量引用"}, "case_id": map[string]any{"type": "integer", "minimum": 1}, "case_revision": map[string]any{"type": "integer", "minimum": 1}, "reason": str("执行目的；受保护环境必填")}, "target_key", "target_fingerprint"),
	} {
		if tool.Name == "verify_api_authentication" || tool.Name == "execute_api_test" {
			tool.Description += "执行异步返回记录 ID；queued/running 仅表示受理，请使用 get_api_test_run 查询最终状态和 authentication/result，不能将确认成功当成验证通过。"
		}
		tool.Annotations = map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": false, "openWorldHint": true}
		tools = append(tools, tool)
	}
	return tools
}
func testingAccess(t *AccessToken, service, environment string) error {
	if service == "" || environment == "" || service == "*" || environment == "*" {
		return fmt.Errorf("必须指定应用和具体环境")
	}
	if !t.verified || t.Owner == "" || t.Owner == "local" {
		return fmt.Errorf("接口测试需要平台网关验证的 Key 绑定用户")
	}
	return t.Allows(service, environment)
}
func (s *ToolService) callTestingRead(ctx context.Context, t *AccessToken, name string, args map[string]any) (any, error) {
	service, env := stringArg(args, "service"), stringArg(args, "environment")
	if name == "list_api_test_cases" || name == "list_api_test_suites" || name == "list_api_test_runs" {
		return s.callTestingCatalog(ctx, t, name, args)
	}
	if service == "" && args["suite_id"] != nil {
		return nil, fmt.Errorf("测试集需要明确应用")
	}
	if service == "" && supportsScenarioTool(name) {
		return s.callScenarioRead(ctx, t, name, args)
	}
	if err := testingAccess(t, service, env); err != nil {
		return nil, err
	}
	q := url.Values{"service": {service}, "environment": {env}}
	if name == "get_api_test_suite" {
		id, err := positiveID(args, "suite_id")
		if err != nil {
			return nil, err
		}
		q.Set("suite_id", id)
		return s.platformRequest(ctx, t, "GET", "/api/api-testing/mcp/suite-context", q, nil)
	}
	if name == "validate_api_test_suite" {
		return s.platformRequest(ctx, t, "POST", "/api/api-testing/mcp/suite-preview", q, mapValue(args["suite"]))
	}
	if args["suite_id"] != nil && name == "get_api_test_context" {
		id, err := positiveID(args, "suite_id")
		if err != nil {
			return nil, err
		}
		q.Set("suite_id", id)
	}
	if name == "get_api_test_layout" {
		id, err := positiveID(args, "case_id")
		if err != nil {
			return nil, err
		}
		q.Set("workspace", stringArg(args, "workspace"))
		return s.platformRequest(ctx, t, "GET", "/api/api-testing/cases/"+id+"/layout", q, nil)
	}
	if name == "list_api_test_cases" || name == "get_api_test_case" {
		if name == "get_api_test_case" {
			id, err := positiveID(args, "case_id")
			if err != nil {
				return nil, err
			}
			q.Set("case_id", id)
		}
		return s.platformRequest(ctx, t, "GET", "/api/api-testing/mcp/cases", q, nil)
	}
	if name == "validate_api_test_case" {
		return s.platformRequest(ctx, t, "POST", "/api/api-testing/mcp/case-preview", q, mapValue(args["case"]))
	}
	if name == "get_api_test_run" {
		id, err := positiveID(args, "run_id")
		if err != nil {
			return nil, err
		}
		v, err := s.platformRequest(ctx, t, "GET", "/api/api-testing/runs/"+id, q, nil)
		if err != nil {
			return nil, err
		}
		if stringArg(mapValue(v), "service") != service || stringArg(mapValue(v), "environment") != env {
			return nil, fmt.Errorf("执行记录不属于指定应用环境")
		}
		return v, nil
	}
	if name == "get_api_test_profile" && t.role != "admin" {
		return nil, fmt.Errorf("仅管理员可读取认证配置")
	}
	v, err := s.platformRequest(ctx, t, "GET", "/api/api-testing/mcp/context", q, nil)
	if err != nil {
		return nil, err
	}
	if name == "get_api_test_context" {
		delete(mapValue(v), "profile")
	}
	return v, nil
}
func (s *ToolService) prepareTestingAction(ctx context.Context, t *AccessToken, name string, args map[string]any, a *PlatformAction) error {
	if name == "stop_api_test_run" {
		return s.prepareTestingStop(ctx, t, args, a)
	}
	if name == "save_api_test_suite" {
		return s.prepareTestingSuite(ctx, t, args, a)
	}
	if stringArg(args, "service") == "" && args["suite_id"] != nil {
		return fmt.Errorf("测试集需要明确应用")
	}
	service, env := stringArg(args, "service"), stringArg(args, "environment")
	if service == "" && supportsScenarioTool(name) {
		return s.prepareScenarioAction(ctx, t, name, args, a)
	}
	if err := testingAccess(t, service, env); err != nil {
		return err
	}
	a.Query = url.Values{"service": {service}, "environment": {env}}
	if name == "save_api_test_layout" {
		id, err := positiveID(args, "case_id")
		if err != nil {
			return err
		}
		a.Query.Set("workspace", stringArg(args, "workspace"))
		a.Path = "/api/api-testing/cases/" + id + "/layout"
		a.BaselinePath = a.Path
		a.BaselineQuery = a.Query
		previous, err := s.platformRequest(ctx, t, "GET", a.Path, a.Query, nil)
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
		a.Method = "PUT"
		a.Baseline = fingerprint(previous)
		a.Preview = map[string]any{"before": previous, "after": a.Body, "case_id": args["case_id"], "sends_requests": false}
		return nil
	}
	if name == "save_api_test_case" {
		c := mapValue(cloneJSON(args["case"]))
		if c == nil {
			return fmt.Errorf("缺少用例定义")
		}
		raw, _ := json.Marshal(c)
		if len(raw) > 384<<10 {
			return fmt.Errorf("用例定义过大")
		}
		a.BaselinePath = "/api/api-testing/mcp/cases"
		a.BaselineQuery = url.Values{"service": {service}, "environment": {env}}
		if intArg(c, "id", 0) > 0 {
			id, err := positiveID(c, "id")
			if err != nil {
				return err
			}
			a.BaselineQuery.Set("case_id", id)
		}
		previous, err := s.platformRequest(ctx, t, "GET", a.BaselinePath, a.BaselineQuery, nil)
		if err != nil {
			return err
		}
		checked, err := s.platformRequest(ctx, t, "POST", "/api/api-testing/mcp/case-preview", a.Query, c)
		if err != nil {
			return err
		}
		normalized := mapValue(mapValue(checked)["case"])
		if normalized == nil {
			return fmt.Errorf("未返回已校验用例")
		}
		a.Baseline = fingerprint(previous)
		a.Body = normalized
		a.Path = "/api/api-testing/cases"
		if id := a.BaselineQuery.Get("case_id"); id != "" {
			a.Path += "/" + id
			a.Method = "PUT"
		}
		a.Preview = map[string]any{"service": service, "environment": env, "before": previous, "after": normalized, "sends_requests": false}
		return nil
	}
	a.BaselinePath = "/api/api-testing/mcp/context"
	a.BaselineQuery = a.Query
	if name == "execute_api_test" && args["case_id"] != nil {
		id, err := positiveID(args, "case_id")
		if err != nil {
			return err
		}
		a.Query.Set("case_id", id)
	}
	if name == "execute_api_test" && args["suite_id"] != nil {
		if args["case_id"] != nil || args["request"] != nil {
			return fmt.Errorf("测试集、用例和临时请求只能选择一种")
		}
		id, err := positiveID(args, "suite_id")
		if err != nil {
			return err
		}
		a.Query.Set("suite_id", id)
	}
	value, err := s.platformRequest(ctx, t, "GET", a.BaselinePath, a.Query, nil)
	if err != nil {
		return err
	}
	context := mapValue(value)
	a.Baseline = fingerprint(value)
	if name == "configure_api_testing" {
		if t.role != "admin" {
			return fmt.Errorf("仅管理员可配置认证与测试入口")
		}
		p := mapValue(cloneJSON(args["profile"]))
		if p == nil || p["revision"] == nil {
			return fmt.Errorf("必须携带读取的 profile revision")
		}
		merged := mapValue(cloneJSON(context["profile"]))
		if merged == nil {
			return fmt.Errorf("上游未返回管理员配置")
		}
		for key, value := range p {
			merged[key] = value
		}
		p = merged
		raw, _ := json.Marshal(p)
		if len(raw) > 384<<10 {
			return fmt.Errorf("配置过大")
		}
		var profile apitest.Profile
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&profile); err != nil {
			return fmt.Errorf("profile 含未知字段或类型错误")
		}

		if err := apitest.ValidateProfile(profile); err != nil {
			return err
		}
		if p == nil {
			return fmt.Errorf("缺少完整 profile")
		}
		if fingerprint(p["revision"]) != fingerprint(context["revision"]) {
			return fmt.Errorf("配置版本已变化，请重新读取")
		}
		// Validate before persisting any preview, so action storage cannot contain
		// inline credentials even when the eventual platform write is rejected.
		checkAuth := func(value any) error {
			raw, _ := json.Marshal(value)
			var auth apitest.Auth
			if err := json.Unmarshal(raw, &auth); err != nil {
				return fmt.Errorf("认证格式无效")
			}
			if err := apitest.ValidateAuth(auth); err != nil {
				return err
			}
			_, err := apitest.ReferenceAuth(auth)
			return err
		}
		if err := checkAuth(p["auth"]); err != nil {
			return err
		}
		if rows, ok := p["entries"].([]any); ok {
			for _, row := range rows {
				if err := checkAuth(mapValue(row)["auth"]); err != nil {
					return err
				}
			}
		}
		a.Method = "PUT"
		a.Path = "/api/api-testing/mcp/profile"
		a.Body = p
		return nil
	}
	if context["enabled"] == false {
		return fmt.Errorf("该环境已停用接口测试")
	}
	if context["protected"] == true && (t.role != "admin" || stringArg(args, "reason") == "") {
		return fmt.Errorf("受保护环境必须管理员填写目的后预览")
	}
	var target map[string]any
	rows, _ := context["targets"].([]any)
	for _, row := range rows {
		r := mapValue(row)
		if stringArg(r, "key") == stringArg(args, "target_key") {
			target = r
			break
		}
	}
	if target == nil || stringArg(target, "fingerprint") != stringArg(args, "target_fingerprint") {
		return fmt.Errorf("测试入口或认证已变化，请重新读取目标")
	}
	if target["credential_references_valid"] == false {
		return fmt.Errorf("此入口有旧内联凭据或登录 URL 参数，请先改成受保护变量与 JSON/表单参数")
	}
	if name == "verify_api_authentication" && stringArg(target, "auth_mode") == "none" {
		return fmt.Errorf("此入口没有配置登录或 Token 变量方案")
	}
	body := map[string]any{"request_id": a.ID, "name": stringArg(args, "name"), "environment": env, "target_key": args["target_key"], "target_fingerprint": args["target_fingerprint"], "reason": stringArg(args, "reason"), "confirm_protected": context["protected"] == true, "expected_variables_digest": context["variables_digest"]}
	if name == "verify_api_authentication" {
		body["verify_auth"] = true
		body["name"] = "验证登录认证"
		body["request"] = map[string]any{"method": "GET", "path": "/", "mode": "http", "timeout_seconds": 30}
	} else {
		if args["suite_id"] != nil {
			suite := mapValue(context["suite"])
			if suite == nil || args["suite_revision"] == nil || fingerprint(suite["revision"]) != fingerprint(args["suite_revision"]) || args["case_revisions"] == nil || fingerprint(args["case_revisions"]) != fingerprint(context["case_revisions"]) {
				return fmt.Errorf("测试集或用例修订已变化，请重新读取上下文")
			}
			body["suite_id"], body["suite_revision"], body["case_revisions"] = args["suite_id"], args["suite_revision"], cloneJSON(args["case_revisions"])
		} else if args["case_id"] != nil {
			if args["request"] != nil || args["case_revision"] == nil {
				return fmt.Errorf("执行用例必须提供 case_revision，不能同时提交调试请求")
			}
			body["case_id"] = args["case_id"]
			body["case_revision"] = args["case_revision"]
		} else {
			raw, _ := json.Marshal(args["request"])
			var request apitest.Request
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&request); err != nil {
				return fmt.Errorf("请求格式无效")
			}
			if err := apitest.Validate(&request); err != nil {
				return err
			}
			if err := apitest.ValidateReferences(request); err != nil {
				return err
			}
			if err := apitest.ValidateStepReferences(request, nil); err != nil {
				return err
			}
			body["request"] = request
		}
	}
	if request, ok := body["request"].(apitest.Request); ok {
		body["request"] = cloneJSON(request)
	}

	a.Preview = map[string]any{"service": service, "environment": env, "target": target, "actor": t.Owner, "verify_auth_only": name == "verify_api_authentication"}
	if c := mapValue(context["case"]); c != nil {
		if fingerprint(c["revision"]) != fingerprint(args["case_revision"]) {
			return fmt.Errorf("用例版本已变化，请重新读取")
		}
		a.Preview["case"] = c
	}
	if request := mapValue(body["request"]); request != nil && name != "verify_api_authentication" {
		actual, err := apitest.Join(stringArg(target, "base_url"), stringArg(request, "path"))
		if err != nil {
			return err
		}
		a.Preview["request_url"] = actual
	}
	if context["suite"] != nil {
		a.Preview["suite"] = context["suite"]
		a.Preview["cases"] = context["cases"]
		a.Preview["case_revisions"] = context["case_revisions"]
	}
	a.Path = "/api/api-testing/mcp/runs"
	a.Body = body
	return nil
}

func scrubTestDefinitionRequest(request map[string]any) map[string]any {
	safe := mapValue(scrubAny(cloneJSON(request)))
	for key, value := range mapValue(request["headers"]) {
		if text, ok := value.(string); ok && apitest.IsCredentialReference(text) {
			mapValue(safe["headers"])[key] = text
		}
	}
	path, query, found := strings.Cut(stringArg(request, "path"), "?")
	if found {
		parts := strings.Split(query, "&")
		for index, part := range parts {
			key, value, _ := strings.Cut(part, "=")
			decodedKey, err := url.QueryUnescape(key)
			if err == nil && apitest.IsCredentialReference(value) {
				parts[index] = scrubString(key) + "=" + value
			} else if sensitiveOutputKey(decodedKey) {
				parts[index] = scrubString(key) + "=[REDACTED]"
			} else {
				parts[index] = scrubString(part)
			}
		}
		safe["path"] = scrubString(path) + "?" + strings.Join(parts, "&")
	}
	return safe
}

func restoreTestDefinitionTemplates(original, safe map[string]any) {
	if request := mapValue(original["request"]); request != nil {
		safe["request"] = scrubTestDefinitionRequest(request)
	}
	steps, _ := original["steps"].([]any)
	safeSteps, _ := safe["steps"].([]any)
	for index, step := range steps {
		if request := mapValue(mapValue(step)["request"]); request != nil {
			mapValue(safeSteps[index])["request"] = scrubTestDefinitionRequest(request)
		}
	}
}

func testingOutput(tool string, value any) any {
	v := mapValue(value)
	if v == nil {
		return scrubAny(value)
	}
	profile, has := v["profile"]
	delete(v, "profile")
	var request any
	preserve := stringArg(v, "tool") == "configure_api_testing" && stringArg(v, "state") == "preview"
	if preserve {
		request = v["request"]
		delete(v, "request")
	}
	safe := scrubAny(cloneJSON(v))
	if tool == "get_api_test_case" || tool == "validate_api_test_case" {
		restoreTestDefinitionTemplates(mapValue(v["case"]), mapValue(mapValue(safe)["case"]))
	}
	if tool == "save_api_test_case" && stringArg(v, "state") == "preview" {
		restoreTestDefinitionTemplates(mapValue(v["request"]), mapValue(mapValue(safe)["request"]))
		preview, safePreview := mapValue(v["preview"]), mapValue(mapValue(safe)["preview"])
		restoreTestDefinitionTemplates(mapValue(preview["after"]), mapValue(safePreview["after"]))
		before, safeBefore := mapValue(preview["before"]), mapValue(safePreview["before"])
		if before["case"] != nil {
			before, safeBefore = mapValue(before["case"]), mapValue(safeBefore["case"])
		}
		restoreTestDefinitionTemplates(before, safeBefore)
	}
	if preserve {
		mapValue(safe)["request"] = request
	}
	if has {
		mapValue(safe)["profile"] = profile
	}
	return safe
}
