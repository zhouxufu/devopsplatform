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
	spec := objectSchema(map[string]any{"work_item_id": id, "environment": str, "applications": map[string]any{"type": "array", "items": str, "minItems": 1, "maxItems": 10}, "acceptance": str, "complete_scope": map[string]any{"type": "boolean"}, "resolution": map[string]any{"type": "string"}, "resolved_build": map[string]any{"type": "string"}, "assigned_to": map[string]any{"type": "string"}}, "work_item_id", "environment", "applications", "acceptance", "complete_scope")
	makeTool := func(name, title, scope string, p map[string]any, required ...string) Tool {
		a := readonly(title)
		if isWriteScope(scope) {
			a = map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": false, "openWorldHint": true}
		}
		return Tool{Name: name, Title: title, Scope: scope, Description: title + "。任务授权固定本人工作项、应用、环境、策略和回归；默认跟进至完成，可暂停或撤销。独立工具仍需原有预览确认；未知执行不重放。", InputSchema: objectSchema(p, required...), Annotations: a}
	}
	change := changeDraftSchema()
	change["properties"].(map[string]any)["id"] = id
	return []Tool{
		{Name: "create_change_tracking", Title: "授权持续登记工作项草稿", Description: "预览本人在指定需求或 Bug、应用内持续维护草稿的授权。confirm_platform_action 确认一次后，通过 save_work_loop_changes 增量登记。", Scope: "changes:write", InputSchema: objectSchema(map[string]any{"work_item_id": id, "applications": map[string]any{"type": "array", "items": str, "minItems": 1, "maxItems": 10}}, "work_item_id", "applications")},
		{Name: "control_change_tracking", Title: "暂停、恢复或撤销草稿登记", Description: "按当前修订管理本人的草稿登记授权。", Scope: "changes:write", InputSchema: objectSchema(map[string]any{"task_id": str, "revision": id, "action": map[string]any{"type": "string", "enum": []string{"pause", "resume", "revoke"}}}, "task_id", "revision", "action")},
		makeTool("create_work_loop", "预览一次性需求/Bug跟进授权；确认后创建任务，尚不执行交付", "delivery:write", map[string]any{"spec": spec}, "spec"),
		makeTool("reauthorize_work_loop", "预览原任务续授权差异；明确确认后调整交付范围，不派发业务执行", "delivery:write", map[string]any{"task_id": str, "revision": id, "spec": spec}, "task_id", "revision", "spec"),
		makeTool("follow_work_item", "接手或找回本人需求／Bug 的开发记录，仅登记本人草稿；发布时单独确认交付范围", "changes:write", map[string]any{"work_item_id": id, "applications": map[string]any{"type": "array", "items": str, "minItems": 0, "maxItems": 10}}, "work_item_id"),
		makeTool("list_work_loops", "读取本人工作项跟进任务和轮次", "workitems:read", map[string]any{"work_item_id": id}, "work_item_id"),
		makeTool("get_work_loop", "读取原跟进任务、阻塞原因、轮次与操作回执", "workitems:read", map[string]any{"task_id": str}, "task_id"),
		makeTool("save_work_loop_changes", "在授权工作项增量保存本人变更。仅登记授权省略 mark_ready，返回 draft_receipt.items（新增或更新、标题、类型）及 change_ids；交付跟进授权可校验就绪", "changes:write", map[string]any{"task_id": str, "revision": id, "content": objectSchema(map[string]any{"digest": str, "changes": map[string]any{"type": "array", "items": change, "minItems": 1, "maxItems": 100}, "mark_ready": map[string]any{"type": "boolean"}}, "digest", "changes")}, "task_id", "revision", "content"),
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
func draftTrackingAccess(t *AccessToken, write bool) error {
	if !t.verified || t.Owner == "" || t.Owner == "local" {
		return fmt.Errorf("请使用本人验证的 Key")
	}
	if err := globalWorkScope(t); err != nil {
		return err
	}
	if !t.HasScope("workitems:read") || !t.HasScope("changes:read") || (write && !t.HasScope("changes:write")) {
		return fmt.Errorf("请核对工作项读取、变更读取与草稿写入权限")
	}
	return nil
}
func workLoopAccess(t *AccessToken, spec map[string]any, write bool) error {
	if spec["draft_only"] == true {
		return draftTrackingAccess(t, write)
	}
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
	if a.Tool == "create_change_tracking" {
		spec = mapValue(cloneJSON(args))
		spec["draft_only"] = true
	}
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
	if name == "follow_work_item" {
		if err := draftTrackingAccess(t, true); err != nil {
			return nil, err
		}
		v, err := s.platformRequest(ctx, t, "POST", "/api/work-loops/follow", nil, args)
		if err != nil {
			return nil, err
		}
		if err = workLoopAccess(t, mapValue(mapValue(mapValue(v)["grant"])["spec"]), false); err != nil {
			return nil, err
		}
		return v, nil
	}
	if name == "create_work_loop" || name == "reauthorize_work_loop" {
		tool, _ := s.tool(name)
		schema := mapValue(mapValue(tool.InputSchema["properties"])["spec"])
		if err := validateToolArgs(schema, mapValue(args["spec"])); err != nil {
			return nil, fmt.Errorf("spec: %w", err)
		}
	}
	if name == "create_change_tracking" || name == "create_work_loop" || name == "reauthorize_work_loop" {
		return s.prepareAction(ctx, t, name, args)
	}
	if e := draftTrackingAccess(t, false); e != nil {
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
	draft := spec["draft_only"] == true
	if name == "control_change_tracking" && !draft {
		return nil, fmt.Errorf("请选择草稿登记授权")
	}
	if draft && name != "get_work_loop" && name != "save_work_loop_changes" && name != "control_change_tracking" {
		return nil, fmt.Errorf("此授权仅可登记草稿")
	}
	if name == "get_work_loop" {
		return v, nil
	}
	body := mapValue(cloneJSON(args))
	delete(body, "task_id")
	paths := map[string]string{"save_work_loop_changes": "changes", "start_work_loop_delivery": "start", "advance_work_loop": "advance", "control_work_loop": "control", "control_change_tracking": "control"}
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
