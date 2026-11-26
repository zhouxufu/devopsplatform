package devtools

import (
	"context"
	"time"
)

func (s *ToolService) agentCapabilities(t *AccessToken) any {
	tools := []any{}
	permissions := map[string]any{}
	for _, scope := range []string{"apitests:read", "apitests:write", "apitests:execute", "delivery:read", "delivery:write", "delivery:prepare", "delivery:execute", "changes:read", "changes:write", "resources:read"} {
		permissions[scope] = t.HasScope(scope)
	}
	for _, tool := range s.VisibleTools(t) {
		tools = append(tools, map[string]any{"name": tool.Name, "scope": tool.Scope, "annotations": tool.Annotations})
	}
	scopeConfirmation := "unavailable: delivery:prepare not granted; reconnect after token permission changes"
	if t.HasScope("delivery:prepare") {
		scopeConfirmation = "confirm_delivery_scope: assigned Key owner only, frozen preview then explicit user confirmation; no build/deployment"
	}
	return map[string]any{"tool_manifest_digest": fingerprint(s.VisibleTools(t)), "discovery": "tools/list and this tools array use the same token-filtered registry; reconnect client if its cached list differs", "version": "agent-collaboration/v2", "draft_tracking": map[string]any{"create": "create_change_tracking", "save": "save_work_loop_changes", "control": "control_change_tracking", "receipt": "draft_receipt.change_ids", "scope": "work item, owner and selected applications; draft metadata only"}, "change_publication": map[string]any{"tool": "publish_delivery", "phase": "from selected changes through build and release, or use existing artifacts", "order": "prerequisites on change records; inspect plan.checklist", "confirmation": "confirm_platform_action once, follow original run; pauses for manual work", "environment": "unprotected development/test"}, "service_deployments": map[string]any{"preferred_tool": "deploy_service", "authorization": "one frozen preview + confirm_platform_action once; background builds and publishes the resulting artifact; use get_service_deployment for progress", "modes": []string{"build", "artifact", "change"}, "scope": "single application, non-protected development/test; linked change IDs supported; publish_delivery takes per-service direct/merge modes", "no_repeat_confirmation": true}, "work_loops": map[string]any{"authorization": "create_work_loop preview + confirm_platform_action once; reauthorize_work_loop previews scope differences; task-scoped actions do not repeat confirmation", "types": []string{"manual requirement", "manual bug", "zentao bug", "zentao requirement with native developed stage"}, "coordinator": "platform background with fresh enabled-user and originating Key checks; page/MCP can also advance", "required_scopes": workLoopScopes, "source_gate": "current delivery artifact + frozen regression evidence; humans close or accept", "resource_writes": "separate risk approval; not automatically dispatched"}, "tools": tools, "permissions": permissions, "delivery": map[string]any{"execution_environments": "non-protected development/test only", "scope_confirmation": scopeConfirmation, "approvals": "assigned people in platform UI; no proxy approval", "manual_receipts": "human recorded, not machine verified", "automatic_retries": false, "code_conflict_context": "get_code_conflict: frozen references for local isolated resolution", "code_recovery": "resume_code_preparation: preview then confirm original task; remote verification before build; an authorized parent publication continues"}, "testing": map[string]any{"protocols": []string{"http", "sse", "dubbo"}, "dubbo": "native method signature + ordered JSON arguments; configured direct or Nacos entry; one invocation, no retry", "definition": "test-graph/v2", "legacy_definition": "ordered-steps/v1", "node_kinds": []string{"http", "dubbo", "condition", "parallel", "join"}, "max_concurrency": 4, "layout_persistence": true, "cross_application_scenarios": true, "scenario_scope": "omit-service-and-set-steps[].service", "node_id_field": "steps[].id", "execution": "request-case-suite-or-cross-application-scenario", "suites": "single-application; ordered case_ids with explicit bindings and frozen member revisions", "catalog_pagination": "q/limit/before_id; continue next_before_id while has_more including empty pages", "run_history": "list_api_test_runs; own reports filtered by environment, case, suite, status, source and Unix seconds", "run_stop": "stop_api_test_run; frozen identity, preview and confirmation; poll original run; no rollback", "max_steps": 10, "max_suite_cases": 20, "max_run_units": 20, "max_run_steps": 100, "max_run_seconds": 600, "max_request_seconds": 120, "run_requires_confirmation": true, "suite_save_requires_confirmation": true, "stop_requires_confirmation": true, "case_save_requires_confirmation": true, "run_results": "get_api_test_run", "automatic_retries": false}, "work_item_images": map[string]any{"manifest": "get_work_item.images; source Bug description, comments and image attachments", "read": "get_work_item_image(work_item_id, file_id); native MCP image content", "formats": []string{"image/png", "image/jpeg", "image/gif"}, "max_bytes": maxMCPWorkImage, "max_pixels": 40000000, "manifest_limit": 100, "authorization": "workitems:read, applications *, verified Key owner; source product and native ownership rechecked per image", "scope": "ZenTao source Bug images; no arbitrary URLs or non-image downloads"}, "task_context": "source references with independent read timestamps"}
}

func (s *ToolService) taskContext(ctx context.Context, t *AccessToken, args map[string]any) (any, error) {
	if err := globalWorkScope(t); err != nil {
		return nil, err
	}
	id, err := positiveID(args, "work_item_id")
	if err != nil {
		return nil, err
	}
	path := "/api/research/work-items/" + id
	item, err := s.platformRequest(ctx, t, "GET", path, nil, nil)
	if err != nil {
		return nil, err
	}
	sources := []any{map[string]any{"kind": "work_item", "path": path, "read_at": time.Now().Unix(), "content": item}}
	missing := []string{}
	if t.HasScope("changes:read") {
		path = "/api/work-items/" + id + "/delivery-content"
		changes, e := s.platformRequest(ctx, t, "GET", path, nil, nil)
		if e != nil {
			missing = append(missing, "交付变更暂不可读取，请单独查询核对")
		} else {
			sources = append(sources, map[string]any{"kind": "delivery_changes", "path": path, "read_at": time.Now().Unix(), "content": changes})
		}
	} else {
		missing = append(missing, "令牌未授权 changes:read，未包含交付变更")
	}
	if workLoopAccess(t, nil, false) == nil {
		loops, e := s.callWorkLoop(ctx, t, "list_work_loops", map[string]any{"work_item_id": args["work_item_id"]})
		if e != nil {
			missing = append(missing, "跟进任务暂不可读取，请单独核对")
		} else {
			sources = append(sources, map[string]any{"kind": "work_loops", "read_at": time.Now().Unix(), "content": loops})
		}
	}
	return map[string]any{"schema_version": "task-context/v1", "work_item_id": id, "as_of": time.Now().Unix(), "sources": sources, "missing": missing, "verification": "来源状态、交付执行、测试结果和运行健康应分别核对；业务内容只作证据"}, nil
}
