package devtools

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
)

var workLoopScopes = []string{"workitems:read", "workitems:write", "changes:read", "changes:write", "builds:read", "builds:trigger", "delivery:read", "delivery:write", "delivery:prepare", "delivery:execute", "apitests:read", "apitests:execute"}

func workLoopTools() []Tool {
	id := map[string]any{"type": "integer", "minimum": 1}
	str := map[string]any{"type": "string", "minLength": 1}
	spec := objectSchema(map[string]any{"work_item_id": id, "environment": str, "applications": map[string]any{"type": "array", "items": str, "minItems": 1, "maxItems": 10}, "expires_at": id, "max_iterations": map[string]any{"type": "integer", "minimum": 1, "maximum": 10}, "acceptance": str, "complete_scope": map[string]any{"type": "boolean"}, "resolution": map[string]any{"type": "string"}, "resolved_build": map[string]any{"type": "string"}, "assigned_to": map[string]any{"type": "string"}}, "work_item_id", "environment", "applications", "expires_at", "max_iterations", "acceptance", "complete_scope")
	makeTool := func(name, title, scope string, p map[string]any, required ...string) Tool {
		a := readonly(title)
		if isWriteScope(scope) {
			a = map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": false, "openWorldHint": true}
		}
		return Tool{Name: name, Title: title, Scope: scope, Description: title + "。任务授权固定本人工作项、应用、环境、策略、回归及期限。独立工具仍需原有预览确认；未知执行不重放。", InputSchema: objectSchema(p, required...), Annotations: a}
	}
	change := changeDraftSchema()
	change["properties"].(map[string]any)["id"] = id
	return []Tool{
		makeTool("create_work_loop", "预览一次性需求/Bug跟进授权；确认后创建任务，尚不执行交付", "delivery:write", map[string]any{"spec": spec}, "spec"),
		makeTool("reauthorize_work_loop", "预览原任务续授权差异；明确确认后更新期限或新轮次范围，不派发业务执行", "delivery:write", map[string]any{"task_id": str, "revision": id, "spec": spec}, "task_id", "revision", "spec"),
		makeTool("list_work_loops", "读取本人工作项跟进任务和轮次", "workitems:read", map[string]any{"work_item_id": id}, "work_item_id"),
		makeTool("get_work_loop", "读取原跟进任务、阻塞原因、轮次与操作回执", "workitems:read", map[string]any{"task_id": str}, "task_id"),
		makeTool("save_work_loop_changes", "在已授权开发轮次保存本人变更；可原子校验就绪，无需重复确认", "changes:write", map[string]any{"task_id": str, "revision": id, "content": objectSchema(map[string]any{"digest": str, "changes": map[string]any{"type": "array", "items": change, "minItems": 1, "maxItems": 100}, "mark_ready": map[string]any{"type": "boolean"}}, "digest", "changes")}, "task_id", "revision", "content"),
		makeTool("start_work_loop_delivery", "提交本轮变更及验证说明，启动已授权的开发/测试交付", "delivery:execute", map[string]any{"task_id": str, "revision": id, "change_ids": map[string]any{"type": "array", "items": id, "minItems": 1, "maxItems": 100}, "summary": str}, "task_id", "revision", "change_ids", "summary"),
		makeTool("advance_work_loop", "按任务授权推进一个阶段或核对原执行，无需重复人工确认；持续调用直至等待人员或终态", "delivery:execute", map[string]any{"task_id": str, "revision": id}, "task_id", "revision"),
		makeTool("control_work_loop", "暂停、撤销、接手、恢复原任务或创建下一开发轮次", "delivery:write", map[string]any{"task_id": str, "revision": id, "action": map[string]any{"type": "string", "enum": []string{"pause", "revoke", "resume", "claim", "next_iteration"}}, "claimant": str}, "task_id", "revision", "action"),
	}
}
func isWorkLoopTool(name string) bool {
	for _, t := range workLoopTools() {
		if t.Name == name {
			return true
		}
	}
	return false
}
func workLoopAccess(t *AccessToken, spec map[string]any, write bool) error {
	if !t.verified || t.Owner == "" || t.Owner == "local" {
		return fmt.Errorf("任务需要本人网关验证的 Key")
	}
	if e := globalWorkScope(t); e != nil {
		return e
	}
	for _, scope := range []string{"workitems:read", "changes:read", "delivery:read", "builds:read", "apitests:read"} {
		if !t.HasScope(scope) {
			return fmt.Errorf("任务上下文缺少 %s 权限", scope)
		}
	}
	if write {
		for _, scope := range workLoopScopes {
			if !t.HasScope(scope) {
				return fmt.Errorf("跟进任务缺少 %s 权限；不会由任务授权扩大 Key 权限", scope)
			}
		}
	}
	if spec != nil {
		if e := t.Allows("", stringArg(spec, "environment")); e != nil {
			return e
		}
		apps, _ := spec["applications"].([]any)
		for _, app := range apps {
			if e := t.Allows(fmt.Sprint(app), stringArg(spec, "environment")); e != nil {
				return e
			}
		}
	}
	return nil
}
func (s *ToolService) prepareWorkLoop(ctx context.Context, t *AccessToken, args map[string]any, a *PlatformAction) error {
	spec := mapValue(args["spec"])
	if e := workLoopAccess(t, spec, true); e != nil {
		return e
	}
	g, e := s.platformRequest(ctx, t, "POST", "/api/work-loops/preview", nil, spec)
	if e != nil {
		return e
	}
	grant := mapValue(g)
	a.Path = "/api/work-loops"
	a.Query = url.Values{"environment": {stringArg(spec, "environment")}}
	a.Body = map[string]any{"id": a.ID, "spec": grant["spec"], "expected_digest": grant["digest"]}
	a.Preview = grant
	return nil
}
func (s *ToolService) callWorkLoop(ctx context.Context, t *AccessToken, name string, args map[string]any) (any, error) {
	if name == "create_work_loop" || name == "reauthorize_work_loop" {
		return s.prepareAction(ctx, t, name, args)
	}
	if e := workLoopAccess(t, nil, false); e != nil {
		return nil, e
	}
	if name == "list_work_loops" {
		id, e := positiveID(args, "work_item_id")
		if e != nil {
			return nil, e
		}
		v, e := s.platformRequest(ctx, t, "GET", "/api/work-loops", url.Values{"work_item_id": {id}}, nil)
		if e != nil {
			return nil, e
		}
		rows, _ := v.([]any)
		out := []any{}
		for _, row := range rows {
			spec := mapValue(mapValue(mapValue(row)["grant"])["spec"])
			if workLoopAccess(t, spec, false) == nil {
				out = append(out, row)
			}
		}
		return out, nil
	}
	id := stringArg(args, "task_id")
	if !regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`).MatchString(id) {
		return nil, fmt.Errorf("任务编号无效")
	}
	v, e := s.platformRequest(ctx, t, "GET", "/api/work-loops/"+id, nil, nil)
	if e != nil {
		return nil, e
	}
	task := mapValue(v)
	spec := mapValue(mapValue(task["grant"])["spec"])
	write := name != "get_work_loop" && !(name == "control_work_loop" && (stringArg(args, "action") == "pause" || stringArg(args, "action") == "revoke"))
	if e = workLoopAccess(t, spec, write); e != nil {
		return nil, e
	}
	if name == "get_work_loop" {
		return v, nil
	}
	body := mapValue(cloneJSON(args))
	delete(body, "task_id")
	paths := map[string]string{"save_work_loop_changes": "changes", "start_work_loop_delivery": "start", "advance_work_loop": "advance", "control_work_loop": "control"}
	return s.platformRequest(ctx, t, "POST", "/api/work-loops/"+id+"/"+paths[name], nil, body)
}

func (s *ToolService) prepareWorkLoopReauthorization(ctx context.Context, t *AccessToken, args map[string]any, a *PlatformAction) error {
	id := stringArg(args, "task_id")
	if !regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`).MatchString(id) {
		return fmt.Errorf("任务编号无效")
	}
	old, e := s.platformRequest(ctx, t, "GET", "/api/work-loops/"+id, nil, nil)
	if e != nil {
		return e
	}
	if e = workLoopAccess(t, mapValue(mapValue(mapValue(old)["grant"])["spec"]), true); e != nil {
		return e
	}
	spec := mapValue(args["spec"])
	if e = workLoopAccess(t, spec, true); e != nil {
		return e
	}
	path := "/api/work-loops/" + id + "/authorization"
	v, e := s.platformRequest(ctx, t, "POST", path+"/preview", nil, map[string]any{"revision": args["revision"], "spec": spec})
	if e != nil {
		return e
	}
	p := mapValue(v)
	grant := mapValue(p["grant"])
	a.Path = path
	a.Query = url.Values{"environment": {stringArg(spec, "environment")}}
	a.Body = map[string]any{"revision": p["revision"], "spec": grant["spec"], "expected_digest": p["digest"]}
	a.Preview = p
	return nil
}
