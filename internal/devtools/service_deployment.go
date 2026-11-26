package devtools

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
)

func serviceDeploymentTools() []Tool {
	str := func(d string) any { return map[string]any{"type": "string", "minLength": 1, "description": d} }
	props := map[string]any{"service": str("应用稳定标识"), "environment": str("明确开发/测试环境"), "mode": map[string]any{"type": "string", "enum": []string{"auto", "build", "artifact", "change"}}, "branch": str("build 可选分支；默认环境公共目标分支。需求源合并使用已有工作项跟进任务"), "artifact_id": map[string]any{"type": "integer", "minimum": 1}, "reason": str("本次部署目的")}
	props["change_id"] = map[string]any{"type": "integer", "minimum": 1, "description": "独立代码变更 ID；使用 change 模式，平台从变更解析功能分支"}
	props["preparation_id"] = map[string]any{"type": "integer", "minimum": 1, "description": "已有准备单时传原编号，接续该单"}
	return []Tool{
		{Name: "deploy_service", Title: "一次确认部署服务", Description: "适用于仅部署一个服务制品、分支或单条独立代码变更。多项变更（含同应用的代码与 Nacos）使用 preview_delivery 保留整批编号；reason 仅为说明。省略 mode 时按变更编号、制品编号或分支自动选择。展示应用、环境、冻结提交/制品及目标后，只确认一次 confirm_platform_action；后台自动构建、发布并核对原结果。单个已关联需求或 Bug 的代码变更也可按 change_id 一次确认合并、构建和部署。需求/Bug已有 work_loop 授权时继续原任务。仅非受保护开发/测试，范围变化停止；独立代码变更走 mode=change + change_id，本人独立草稿在同一次确认中校验就绪、合入公共分支、构建并仅发布本次制品，无需工作项编号或网页登录；已有准备单附 preparation_id。不得拆成逐步确认。", Scope: "delivery:execute", InputSchema: objectSchema(props, "service", "environment", "reason"), Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": false}},
		{Name: "get_service_deployment", Title: "查询原服务部署任务", Description: "只读查询原任务的阶段、编号和阻塞原因。发布失败时用 release_id 调用 get_release_diagnostics；构建失败时用 run_id 调用 get_build_log。", Scope: "delivery:read", InputSchema: objectSchema(map[string]any{"task_id": str("原任务ID"), "service": str("按变更找回时的应用"), "environment": str("按变更找回时的环境"), "change_id": map[string]any{"type": "integer", "minimum": 1}}), Annotations: readonly("查询原服务部署任务")},
		{Name: "advance_service_deployment", Title: "继续已授权服务部署", Description: "在原任务冻结授权内推进或核对，不重新询问用户确认；后台通常自动调用。范围变化/授权失效停止，不重放未知执行。", Scope: "delivery:execute", InputSchema: objectSchema(map[string]any{"task_id": str("原任务ID"), "revision": map[string]any{"type": "integer", "minimum": 1}}, "task_id", "revision"), Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": false}},
	}
}
func serviceDeploymentAccess(t *AccessToken, spec map[string]any, write bool) error {
	if !t.verified || t.Owner == "" || t.Owner == "local" {
		return fmt.Errorf("需要本人已验证的平台Key")
	}
	if spec["delivery"] != nil && stringArg(spec, "service") == "" {
		if e := globalWorkScope(t); e != nil {
			return e
		}
	}
	if e := t.Allows(stringArg(spec, "service"), stringArg(spec, "environment")); e != nil {
		return e
	}
	scopes := []string{"delivery:read"}
	if write {
		scopes = append(scopes, "delivery:execute")
		if stringArg(spec, "mode") == "change" {
			scopes = append(scopes, "changes:read", "delivery:write", "delivery:prepare")
		}
		if stringArg(spec, "mode") == "build" || stringArg(spec, "mode") == "change" {
			scopes = append(scopes, "builds:trigger", "builds:read")
		}
	}
	for _, scope := range scopes {
		if !t.HasScope(scope) {
			return fmt.Errorf("缺少 %s 权限；部署任务不扩大Key权限", scope)
		}
	}
	return nil
}
func checkPublicationApps(t *AccessToken, grant map[string]any) error {
	apps, _ := mapValue(mapValue(grant["change"])["publication_scope"])["applications"].([]any)
	for _, raw := range apps {
		if err := t.Allows(stringArg(mapValue(raw), "service"), stringArg(mapValue(grant["spec"]), "environment")); err != nil {
			return err
		}
	}
	return nil
}
func deploymentGrantAccess(t *AccessToken, grant map[string]any) error {
	if err := checkPublicationApps(t, grant); err != nil {
		return err
	}
	if stringArg(mapValue(grant["change"]), "mark_ready_draft_digest") != "" && !t.HasScope("changes:write") {
		return fmt.Errorf("独立草稿就绪需要 changes:write 权限")
	}
	return serviceDeploymentAccess(t, mapValue(grant["spec"]), true)
}
func (s *ToolService) prepareServiceDeployment(ctx context.Context, t *AccessToken, args map[string]any, a *PlatformAction) error {
	if e := serviceDeploymentAccess(t, args, true); e != nil {
		return e
	}
	v, e := s.platformRequest(ctx, t, "POST", "/api/service-deployments/preview", nil, args)
	if e != nil {
		return e
	}
	grant := mapValue(v)
	spec := mapValue(grant["spec"])
	if err := deploymentGrantAccess(t, grant); err != nil {
		return err
	}
	if stringArg(spec, "service") != stringArg(args, "service") || stringArg(spec, "environment") != stringArg(args, "environment") || stringArg(grant, "digest") == "" {
		return fmt.Errorf("预览目标身份或摘要不完整")
	}
	a.Path = "/api/service-deployments"
	a.Query = url.Values{"service": {stringArg(spec, "service")}, "environment": {stringArg(spec, "environment")}}
	a.Body = map[string]any{"id": a.ID, "spec": spec, "expected_digest": grant["digest"]}
	a.Preview = grant
	a.Baseline = fingerprint(grant["digest"])
	return nil
}
func (s *ToolService) findChangeDeployment(ctx context.Context, t *AccessToken, args map[string]any) (map[string]any, error) {
	service, env, id := stringArg(args, "service"), stringArg(args, "environment"), intArg(args, "change_id", 0)
	if service == "" || env == "" || id <= 0 {
		return nil, fmt.Errorf("按变更查找需要应用、环境和变更编号")
	}
	if err := serviceDeploymentAccess(t, args, false); err != nil {
		return nil, err
	}
	if !t.HasScope("changes:read") {
		return nil, fmt.Errorf("缺少 changes:read 权限")
	}
	v, err := s.platformRequest(ctx, t, "GET", "/api/service-deployments/by-change", url.Values{"service": {service}, "environment": {env}, "change_id": {strconv.FormatInt(id, 10)}}, nil)
	if err != nil {
		return nil, err
	}
	task := mapValue(v)
	if err = checkPublicationApps(t, mapValue(task["grant"])); err != nil {
		return nil, err
	}
	return task, nil
}
func (s *ToolService) callServiceDeployment(ctx context.Context, t *AccessToken, name string, args map[string]any) (any, error) {
	id := stringArg(args, "task_id")
	if id == "" && name == "get_service_deployment" {
		return s.findChangeDeployment(ctx, t, args)
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`).MatchString(id) {
		return nil, fmt.Errorf("任务ID无效")
	}
	v, e := s.platformRequest(ctx, t, "GET", "/api/service-deployments/"+id, nil, nil)
	if e != nil {
		return nil, e
	}
	task := mapValue(v)
	spec := mapValue(mapValue(task["grant"])["spec"])
	if stringArg(task, "owner") != t.Owner {
		return nil, fmt.Errorf("仅原授权人可查看任务")
	}
	if e = serviceDeploymentAccess(t, spec, name != "get_service_deployment"); e != nil {
		return nil, e
	}
	if err := checkPublicationApps(t, mapValue(task["grant"])); err != nil {
		return nil, err
	}
	if name == "get_service_deployment" {
		if stringArg(task, "stage") == "waiting_conflict" && stringArg(task, "code_id") != "" {
			task["next_action"] = map[string]any{"tool": "get_code_conflict", "arguments": map[string]any{"publication_task_id": id, "preparation_id": task["code_id"], "environment": spec["environment"]}}
		}
		state := stringArg(task, "state")
		if state == "failed" || state == "blocked" || state == "needs_review" || state == "rollback-failed" {
			service := stringArg(spec, "service")
			if releaseID := intArg(task, "release_id", 0); releaseID > 0 && t.HasScope("releases:read") {
				task["diagnostics"] = map[string]any{"tool": "get_release_diagnostics", "arguments": map[string]any{"service": service, "environment": stringArg(spec, "environment"), "release_id": releaseID}}
			} else if runID := intArg(task, "run_id", 0); runID > 0 && releaseID == 0 && t.HasScope("builds:read") {
				task["diagnostics"] = map[string]any{"tool": "get_build_log", "arguments": map[string]any{"service": service, "build_id": runID}}
			}
		}
		return v, nil
	}
	return s.platformRequest(ctx, t, "POST", "/api/service-deployments/"+id+"/advance", nil, map[string]any{"revision": args["revision"]})
}
