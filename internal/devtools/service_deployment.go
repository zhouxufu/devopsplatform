package devtools

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
)

func serviceDeploymentTools() []Tool {
	str := func(d string) any { return map[string]any{"type": "string", "minLength": 1, "description": d} }
	props := map[string]any{"service": str("应用稳定标识"), "environment": str("明确开发/测试环境"), "mode": map[string]any{"type": "string", "enum": []string{"build", "artifact"}}, "branch": str("build 可选分支；默认环境公共目标分支。需求源合并使用已有工作项跟进任务"), "artifact_id": map[string]any{"type": "integer", "minimum": 1}, "reason": str("本次部署目的")}
	return []Tool{
		{Name: "deploy_service", Title: "一次确认部署服务", Description: "普通单应用部署首选此工具：预览已有制品发布或构建并发布。展示应用、环境、冻结提交/制品及目标后，只确认一次 confirm_platform_action；后台自动构建、发布并核对原结果，不再逐步确认。需求/Bug已有 work_loop 授权时继续原任务。仅非受保护开发/测试，范围变化停止；不自动合并需求源、不代审、不重放未知执行。", Scope: "delivery:execute", InputSchema: objectSchema(props, "service", "environment", "mode", "reason"), Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": false}},
		{Name: "get_service_deployment", Title: "查询原服务部署任务", Description: "只读查询一次授权部署的构建、发布编号、阶段及阻塞原因，不需要再次确认；后台自动推进。", Scope: "delivery:read", InputSchema: objectSchema(map[string]any{"task_id": str("原任务ID")}, "task_id"), Annotations: readonly("查询原服务部署任务")},
		{Name: "advance_service_deployment", Title: "继续已授权服务部署", Description: "在原任务冻结授权内推进或核对，不重新询问用户确认；后台通常自动调用。范围变化/授权失效停止，不重放未知执行。", Scope: "delivery:execute", InputSchema: objectSchema(map[string]any{"task_id": str("原任务ID"), "revision": map[string]any{"type": "integer", "minimum": 1}}, "task_id", "revision"), Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": false}},
	}
}
func serviceDeploymentAccess(t *AccessToken, spec map[string]any, write bool) error {
	if !t.verified || t.Owner == "" || t.Owner == "local" {
		return fmt.Errorf("需要本人已验证的平台Key")
	}
	if e := t.Allows(stringArg(spec, "service"), stringArg(spec, "environment")); e != nil {
		return e
	}
	scopes := []string{"delivery:read"}
	if write {
		scopes = append(scopes, "delivery:execute")
		if stringArg(spec, "mode") == "build" {
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
	if stringArg(spec, "service") != stringArg(args, "service") || stringArg(spec, "environment") != stringArg(args, "environment") || stringArg(grant, "digest") == "" {
		return fmt.Errorf("预览目标身份或摘要不完整")
	}
	a.Path = "/api/service-deployments"
	a.Query = url.Values{"service": {stringArg(spec, "service")}, "environment": {stringArg(spec, "environment")}}
	a.Body = map[string]any{"id": a.ID, "spec": spec, "expected_digest": grant["digest"]}
	a.Preview = grant
	return nil
}
func (s *ToolService) callServiceDeployment(ctx context.Context, t *AccessToken, name string, args map[string]any) (any, error) {
	id := stringArg(args, "task_id")
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
	if name == "get_service_deployment" {
		return v, nil
	}
	return s.platformRequest(ctx, t, "POST", "/api/service-deployments/"+id+"/advance", nil, map[string]any{"revision": args["revision"]})
}
