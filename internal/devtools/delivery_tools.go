package devtools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

var recoveryCommitRE = regexp.MustCompile(`^(?:[a-f0-9]{40}|[a-f0-9]{64})$`)

// These tools expose the existing unified workflow, never a second executor.
func deliveryTools() []Tool {
	str := func(d string) any { return map[string]any{"type": "string", "description": d} }
	id := map[string]any{"type": "integer", "minimum": 1}
	request := objectSchema(map[string]any{
		"environment": str("明确的非受保护开发/测试环境"), "template_id": id,
		"strategy_kind": map[string]any{"type": "string", "enum": []string{"custom", "standard"}},
		"source_type":   map[string]any{"type": "string", "enum": []string{"work_items", "version", "changes", "fixed"}},
		"work_item_ids": map[string]any{"type": "array", "items": id, "maxItems": 100}, "version_id": id,
		"selected_change_ids": map[string]any{"type": "array", "items": id, "maxItems": 200},
		"source_change_ids":   map[string]any{"type": "array", "items": id, "maxItems": 200},
		"artifacts":           map[string]any{"type": "object", "additionalProperties": id},
		"code_modes":          map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string", "enum": []string{"direct", "merge"}}, "description": "逐应用选择直接构建公共分支或合并选定代码后构建；发布从准备阶段一次执行"},
		"artifact_sources":    map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string", "enum": []string{"build", "existing"}}},
		"preparation_run_id":  id, "code_preparation_id": str("已有代码准备任务ID"),
		"nodes":  map[string]any{"type": "array", "maxItems": 30, "items": objectSchema(map[string]any{"id": str("原节点 ID；拆分 SQL 阶段使用新的唯一 ID"), "sql_group": str("拆分 SQL 时引用预览中的原 SQL 节点 ID"), "change_ids": map[string]any{"type": "array", "items": id, "minItems": 1, "maxItems": 50}}, "id"), "description": "可选的执行顺序：仅提交原节点 id；SQL 拆分可附 sql_group 和 change_ids。服务端保留原节点类型、目标、脚本和审核，不能增添其他节点或改写执行内容。"},
		"reason": str("此次交付目的"), "order_confirmed": map[string]any{"type": "boolean"}, "code_confirmed": map[string]any{"type": "boolean"},
	}, "environment")
	phase := map[string]any{"type": "string", "enum": []string{"preparation", "release"}, "description": "preparation=环境绑定流程的范围确认/代码准备阶段；release=完成准备后的环境预检/发布阶段"}
	makeTool := func(name, title, scope string, props map[string]any, required ...string) Tool {
		t := Tool{Name: name, Title: title, Description: title + "。仅开发/测试；保留现有审批、资源预检和制品来源校验。写入只生成冻结预览，展示实际目标并明确同意后确认；未知结果查询原操作/执行单，不自动重试。", Scope: scope, InputSchema: objectSchema(props, required...), Annotations: readonly(title)}
		if isWriteScope(scope) {
			t.Annotations = map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": false, "openWorldHint": true}
		}
		return t
	}
	return []Tool{
		makeTool("preview_delivery", "解析交付准备范围或执行环境预检，不创建执行单、不合并构建或发布", "delivery:read", map[string]any{"phase": phase, "request": request}, "phase", "request"),
		makeTool("create_delivery_run", "仅用户明确要求单独建单时使用。要完成发布请用 publish_delivery 或 deploy_service", "delivery:write", map[string]any{"phase": phase, "request": request}, "phase", "request"),
		makeTool("publish_delivery", "单服务或多服务一次确认发布。代码范围可自动识别待构建应用，code_modes 按应用选 direct 或 merge；关联需求用 work_item_ids 加 selected_change_ids。此入口从准备推进到部署，不应转为逐步确认。已有制品时展示目标环境、不可变制品、SQL、配置及步骤顺序；确认后后台推进一小时，独立审批和人工步骤由指定人员处理。仅非受保护开发/测试环境。", "delivery:execute", map[string]any{"request": request}, "request"),
		makeTool("advance_delivery", "仅用于人工逐步维护原执行单。普通发布由 publish_delivery 后台推进", "delivery:execute", map[string]any{"run_id": id, "environment": str("执行单实际环境")}, "run_id", "environment"),
		makeTool("resume_code_preparation", "按解决提交预览原任务恢复。确认后校验远端并构建，已授权的整次发布继续执行", "delivery:prepare", map[string]any{"preparation_id": str("原代码准备任务ID"), "environment": str("任务实际环境"), "publication_task_id": str("如来自整次发布，填等待冲突处理的原发布任务ID"), "expected_revision": id, "scope_digest": str("读取冲突上下文返回的冻结范围摘要"), "target_commits": map[string]any{"type": "array", "items": str("按repository_index顺序，解决后远端目标分支完整SHA"), "minItems": 1, "maxItems": 100}}, "preparation_id", "environment", "expected_revision", "scope_digest", "target_commits"),
		makeTool("start_code_preparation", "预览开始已冻结的统一流程代码任务；确认后可能合并、推送和构建，不执行部署", "delivery:prepare", map[string]any{"preparation_id": str("由统一交付代码节点生成的原任务ID"), "environment": str("代码任务实际环境")}, "preparation_id", "environment"),
		makeTool("retry_prepared_builds", "从原任务已合并的提交和原构建配置续跑失败构建，保留成功制品，不重新合并或发布", "delivery:prepare", map[string]any{"preparation_id": str("原代码准备任务ID"), "environment": str("原开发或测试环境")}, "preparation_id", "environment"),
		makeTool("confirm_delivery_scope", "仅维护未由整次发布接管的范围节点；展示冻结范围并获用户明确同意后调用confirm_platform_action；不代他人确认、不审批、不执行构建或部署", "delivery:prepare", map[string]any{"run_id": id, "environment": str("准备单实际环境"), "comment": str("本人确认的范围说明"), "work_item_ids": map[string]any{"type": "array", "items": id, "minItems": 1, "maxItems": 100}}, "run_id", "environment", "comment"),
		makeTool("record_delivery_manual_step", "预览登记当前人工步骤的实际回执；不是机器验证、不允许代审范围确认或审批", "delivery:execute", map[string]any{"run_id": id, "environment": str("执行单实际环境"), "comment": str("已人工处理的内容、结果和凭据/记录地址；不填密码或Token")}, "run_id", "environment", "comment"),
		makeTool("mark_work_item_changes_ready", "预览将本人指定变更校验并标记就绪；原文/摘要冻结，不执行环境预检或发布", "changes:write", map[string]any{"work_item_id": id, "change_ids": map[string]any{"type": "array", "items": id, "minItems": 1, "maxItems": 50}}, "work_item_id", "change_ids"),
	}
}

func isDeliveryMCPTool(name string) bool {
	for _, tool := range deliveryTools() {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func deliveryAccess(t *AccessToken, env string) error {
	if !t.verified || t.Owner == "" || t.Owner == "local" {
		return fmt.Errorf("交付需要网关验证的本人 Key")
	}
	if env == "" || env == "*" {
		return fmt.Errorf("必须指定具体环境")
	}
	if err := t.Allows("", env); err != nil {
		return err
	}
	// Scope selection is cross-application. Do not disclose unauthorized scope
	// while resolving work-item/version membership.
	return globalWorkScope(t)
}

func (s *ToolService) deliveryEnvironment(ctx context.Context, t *AccessToken, env string) error {
	if err := deliveryAccess(t, env); err != nil {
		return err
	}
	return s.unprotectedDeliveryEnvironment(ctx, t, env)
}
func (s *ToolService) unprotectedDeliveryEnvironment(ctx context.Context, t *AccessToken, env string) error {
	v, err := s.platformRequest(ctx, t, "GET", "/api/environments", nil, nil)
	if err != nil {
		return err
	}
	rows, _ := v.([]any)
	for _, row := range rows {
		e := mapValue(row)
		if stringArg(e, "name") != env {
			continue
		}
		if e["protected"] == true || stringArg(e, "purpose") == "production" {
			return fmt.Errorf("本轮 MCP 执行仅支持非受保护开发/测试环境")
		}
		return nil
	}
	return fmt.Errorf("目标环境不存在")
}

func (s *ToolService) resolvePublicationOrder(ctx context.Context, t *AccessToken, phase string, req map[string]any) error {
	if requested, ok := req["nodes"].([]any); ok {
		if phase != "release" {
			return fmt.Errorf("执行顺序仅能在发布预检阶段调整")
		}
		draftRequest := mapValue(cloneJSON(req))
		delete(draftRequest, "nodes")
		draft, err := s.platformRequest(ctx, t, "POST", "/api/delivery/draft-plan", nil, draftRequest)
		if err != nil {
			return err
		}
		generated, _ := mapValue(draft)["nodes"].([]any)
		nodes, err := deliveryOrderedNodes(generated, requested)
		if err != nil {
			return err
		}
		req["nodes"] = nodes
	}
	return nil
}
func deliveryPreviewPath(phase string) string {
	if phase == "preparation" {
		return "/api/delivery/preparations/preview"
	}
	return "/api/delivery/preview"
}

func (s *ToolService) deliveryPreview(ctx context.Context, t *AccessToken, phase string, req map[string]any) (any, error) {
	props := deliveryTools()[0].InputSchema["properties"].(map[string]any)
	if err := validateToolArgs(props["request"].(map[string]any), req); err != nil {
		return nil, err
	}
	if phase != "preparation" && phase != "release" {
		return nil, fmt.Errorf("交付阶段无效")
	}
	if err := s.deliveryEnvironment(ctx, t, stringArg(req, "environment")); err != nil {
		return nil, err
	}
	if err := s.resolveDeliveryMCPRequest(ctx, t, phase, req); err != nil {
		return nil, err
	}
	if err := s.resolvePublicationOrder(ctx, t, phase, req); err != nil {
		return nil, err
	}
	v, err := s.platformRequest(ctx, t, "POST", deliveryPreviewPath(phase), nil, req)
	if err != nil {
		return nil, err
	}
	plan := mapValue(v)
	if phase == "preparation" {
		plan = mapValue(plan["plan"])
	}
	if plan == nil {
		return nil, fmt.Errorf("未返回交付计划")
	}
	if err := checkPlanApps(t, plan); err != nil {
		return nil, err
	}
	return v, nil
}

// Normalize before BOTH preview and freezing its request. Missing scope must
// never silently select the template's version mode. Never guess a template
// from its name/list order or revive standalone preparation records.
func (s *ToolService) resolveDeliveryMCPRequest(ctx context.Context, t *AccessToken, phase string, req map[string]any) error {
	env := stringArg(req, "environment")
	v, err := s.platformRequest(ctx, t, "GET", "/api/environments/"+url.PathEscape(env)+"/delivery-policy", nil, nil)
	if err != nil {
		return err
	}
	bound := intArg(mapValue(v), "workflow_template_id", 0)
	if bound > 0 {
		if supplied := intArg(req, "template_id", 0); supplied > 0 && supplied != bound {
			return fmt.Errorf("目标环境绑定流程 #%d，与指定模板不一致", bound)
		}
		if strategy := stringArg(req, "strategy_kind"); strategy != "" && strategy != "custom" {
			return fmt.Errorf("环境已绑定流程，不能切换标准交付绕过准备阶段")
		}
		req["template_id"], req["strategy_kind"] = bound, "custom"
	} else if phase == "preparation" {
		return fmt.Errorf("目标环境 %s 尚未绑定部署编排（environment=%s, policy_revision=%v, workflow_template_id=%d, missing_phase=preparation）；请调用 diagnose_delivery_binding 核对实际绑定", env, env, mapValue(v)["revision"], bound)
	} else if stringArg(req, "strategy_kind") == "" {
		req["strategy_kind"] = "standard"
	}
	work, _ := req["work_item_ids"].([]any)
	changes, _ := req["source_change_ids"].([]any)
	selected, _ := req["selected_change_ids"].([]any)
	source := ""
	count := 0
	if len(work) > 0 {
		source = "work_items"
		count++
	}
	if intArg(req, "version_id", 0) > 0 {
		source = "version"
		count++
	}
	if len(changes) > 0 {
		source = "changes"
		count++
	} else if len(selected) > 0 && source == "" {
		source = "changes"
		count++
	}
	if count != 1 {
		return fmt.Errorf("明确选择一种交付来源：work_item_ids、version_id 或 source_change_ids；工作项交付不要求创建版本")
	}
	if explicit := stringArg(req, "source_type"); explicit != "" && explicit != source {
		return fmt.Errorf("source_type 与实际交付来源不一致，请核对工作项/版本/变更参数")
	}
	req["source_type"] = source
	return nil
}

func (s *ToolService) callDeliveryRead(ctx context.Context, t *AccessToken, name string, args map[string]any) (any, error) {
	if name == "preview_delivery" {
		return s.deliveryPreview(ctx, t, stringArg(args, "phase"), mapValue(cloneJSON(args["request"])))
	}
	return nil, fmt.Errorf("未知交付查询")
}

func (s *ToolService) prepareDeliveryAction(ctx context.Context, t *AccessToken, name string, args map[string]any, a *PlatformAction) error {
	if name == "mark_work_item_changes_ready" {
		return s.prepareChangesReady(ctx, t, args, a)
	}
	if name == "create_delivery_run" || name == "publish_delivery" {
		req := mapValue(cloneJSON(args["request"]))
		phase := stringArg(args, "phase")
		if name == "publish_delivery" {
			phase = "release"
			modes := mapValue(req["artifact_sources"])
			needsBuild := len(mapValue(req["code_modes"])) > 0
			for _, mode := range modes {
				if mode == "build" {
					needsBuild = true
				}
			}
			if !needsBuild && len(modes) == 0 && stringArg(req, "code_preparation_id") == "" {
				if err := publicationAccess(t, stringArg(req, "environment")); err != nil {
					return err
				}
				if err := s.resolveDeliveryMCPRequest(ctx, t, "release", req); err != nil {
					return err
				}
				input := mapValue(cloneJSON(req))
				delete(input, "nodes")
				draft, err := s.platformRequest(ctx, t, "POST", "/api/delivery/draft-plan", nil, input)
				if err != nil {
					return err
				}
				changes, _ := mapValue(mapValue(draft)["scope"])["changes"].([]any)
				for _, row := range changes {
					c := mapValue(row)
					if stringArg(c, "kind") == "code" && intArg(mapValue(req["artifacts"]), stringArg(c, "service"), 0) == 0 {
						needsBuild = true
						break
					}
				}
			}
			if needsBuild && stringArg(req, "code_preparation_id") == "" {
				env := stringArg(req, "environment")
				if err := publicationAccess(t, env); err != nil {
					return err
				}
				if err := s.resolveDeliveryMCPRequest(ctx, t, "preparation", req); err != nil {
					return err
				}
				if err := s.resolvePublicationOrder(ctx, t, "release", req); err != nil {
					return err
				}
				spec := map[string]any{"environment": env, "mode": "change", "reason": stringArg(req, "reason"), "delivery": req}
				if id := intArg(req, "preparation_run_id", 0); id > 0 {
					spec["preparation_id"] = id
					delete(req, "preparation_run_id")
				}
				return s.prepareServiceDeployment(ctx, t, spec, a)
			}
			if err := publicationAccess(t, stringArg(req, "environment")); err != nil {
				return err
			}
		}
		v, err := s.deliveryPreview(ctx, t, phase, req)
		if err != nil {
			return err
		}
		env := stringArg(req, "environment")
		a.Query = url.Values{"environment": {env}}
		a.Body = req
		a.Preview = map[string]any{"phase": phase, "environment": env, "plan": v, "executes_resources": false}
		a.BaselinePath = "/api/environments/" + url.PathEscape(env) + "/delivery-policy"
		if phase == "preparation" {
			if blockers, _ := mapValue(mapValue(v)["plan"])["blockers"].([]any); len(blockers) > 0 {
				return fmt.Errorf("准备预检阻塞：%v", blockers)
			}
			digest := stringArg(mapValue(v), "digest")
			if digest == "" {
				return fmt.Errorf("准备计划缺少摘要")
			}
			a.Body["expected_digest"] = digest
			a.Body["client_request_id"] = a.ID
			a.Path = "/api/delivery/mcp/preparations/runs"
		} else {
			plan := mapValue(v)
			if blockers, _ := plan["blockers"].([]any); len(blockers) > 0 {
				return fmt.Errorf("预检阻塞：%v", blockers)
			}
			token := stringArg(plan, "preflight_token")
			if token == "" {
				return fmt.Errorf("预检未通过或未返回凭证")
			}
			a.Body["preflight_token"] = token
			a.Path = "/api/delivery/mcp/runs"
			if name == "publish_delivery" {
				envInfo := mapValue(plan["environment"])
				if envInfo["protected"] == true || (envInfo["purpose"] != "development" && envInfo["purpose"] != "test") {
					return fmt.Errorf("仅开发/测试环境支持一次确认发布")
				}
				a.Path = "/api/delivery/publications"
				a.Preview["executes_resources"] = true
				a.Preview["effect"] = "确认后按冻结清单推进一小时，含 SQL、配置和服务发布，人工节点暂停"
			}
		}
		return nil
	}
	env := stringArg(args, "environment")
	var accessErr error
	if name == "resume_code_preparation" {
		accessErr = s.publicationRecoveryAccess(ctx, t, stringArg(args, "publication_task_id"), stringArg(args, "preparation_id"), env)
	} else {
		accessErr = s.deliveryEnvironment(ctx, t, env)
	}
	if err := accessErr; err != nil {
		return err
	}
	a.Query = url.Values{"environment": {env}}
	if name == "start_code_preparation" || name == "resume_code_preparation" || name == "retry_prepared_builds" {
		id := stringArg(args, "preparation_id")
		if !safeDeliveryID(id) {
			return fmt.Errorf("代码任务ID无效")
		}
		a.BaselinePath = "/api/delivery/code-preparations/" + id
		v, err := s.platformRequest(ctx, t, "GET", a.BaselinePath, nil, nil)
		if err != nil {
			return err
		}
		p := mapValue(v)
		if stringArg(mapValue(p["request"]), "environment") != env || p["protected"] == true || intArg(p, "preparation_run_id", 0) <= 0 {
			return fmt.Errorf("只能开始本环境统一流程中未执行的代码任务")
		}
		if stringArg(p, "owner") != t.Owner && t.role != "admin" {
			return fmt.Errorf("代码任务不属于本人")
		}
		action := "start"
		if name == "resume_code_preparation" {
			action = "resume"
			if intArg(args, "expected_revision", 0) != intArg(p, "revision", 0) || stringArg(args, "scope_digest") == "" || stringArg(args, "scope_digest") != stringArg(p, "scope_digest") {
				return fmt.Errorf("代码任务修订或冻结范围已变化，请重新读取上下文")
			}
			commits, _ := args["target_commits"].([]any)
			repos, _ := p["repositories"].([]any)
			if len(commits) == 0 || len(commits) != len(repos) {
				return fmt.Errorf("必须逐仓库冻结解决后的目标提交")
			}
			for _, sha := range commits {
				text, ok := sha.(string)
				if !ok || !recoveryCommitRE.MatchString(text) {
					return fmt.Errorf("目标提交必须是完整SHA")
				}
			}
			state := stringArg(p, "state")
			if state != "conflict" && state != "uncertain" && state != "failed" && state != "merging" && state != "building" {
				return fmt.Errorf("当前任务无需恢复")
			}
			if intArg(p, "lease_until", 0) > time.Now().Unix() {
				return fmt.Errorf("执行器仍在工作或安全等待期内，请查询原任务")
			}
		} else if name == "retry_prepared_builds" {
			action = "retry-builds"
			if stringArg(p, "state") != "failed" || intArg(p, "lease_until", 0) > time.Now().Unix() {
				return fmt.Errorf("只有已结束的失败构建可重试")
			}
			rows, _ := p["builds"].([]any)
			failed := false
			for _, entry := range rows {
				b := mapValue(entry)
				if err := t.Allows(stringArg(b, "service"), env); err != nil {
					return err
				}
				if stringArg(b, "status") == "failed" {
					if intArg(b, "run_id", 0) <= 0 {
						return fmt.Errorf("失败构建没有执行回执，请先核对原操作")
					}
					failed = true
				}
			}
			if !failed {
				return fmt.Errorf("没有已记录的失败构建")
			}
		} else if stringArg(p, "state") != "preview" {
			return fmt.Errorf("只能开始未执行的代码任务")
		}
		a.Baseline = fingerprint(v)
		a.Path = "/api/delivery/mcp/code-preparations/" + id + "/" + action
		a.Body = map[string]any{"confirm": true, "expected_revision": p["revision"]}
		if action == "resume" {
			a.Body["target_commits"] = cloneJSON(args["target_commits"])
		}
		a.Preview = map[string]any{"environment": env, "code_preparation": v, "merges_and_builds": true, "publishes": false, "action": action, "target_commits": a.Body["target_commits"], "verification": "平台重新读取远端并校验冻结范围；不信任Agent自行声明成功"}
		if action == "resume" && stringArg(args, "publication_task_id") != "" {
			a.Preview["publication_task_id"] = args["publication_task_id"]
			a.Preview["publishes"] = true
			a.Preview["continues_publication"] = true
		}
		if action == "resume" {
			a.Preview["continuation"] = "恢复原代码任务；若隶属已授权整次发布，构建就绪后按原范围继续部署。"
		}
		if action == "retry-builds" {
			a.Preview["merges_and_builds"] = false
			a.Preview["build_only"] = true
			a.Preview["verification"] = "按失败构建的原快照重试；保留成功制品，不再合并。"
		}
		return nil
	}
	id, err := positiveID(args, "run_id")
	if err != nil {
		return err
	}
	a.BaselinePath = "/api/delivery/runs/" + id
	v, err := s.platformRequest(ctx, t, "GET", a.BaselinePath, nil, nil)
	if err != nil {
		return err
	}
	run := mapValue(v)
	plan := mapValue(run["plan"])
	if stringArg(mapValue(plan["environment"]), "name") != env {
		return fmt.Errorf("执行单环境不匹配")
	}
	if err := checkPlanApps(t, plan); err != nil {
		return err
	}
	if name == "confirm_delivery_scope" {
		return prepareScopeConfirmation(t, args, a, run, plan, v, id)
	}
	if stringArg(run, "owner") != t.Owner && t.role != "admin" {
		return fmt.Errorf("仅发起人或管理员可推进")
	}
	if state := stringArg(run, "status"); state != "running" && state != "approved" {
		return fmt.Errorf("流程尚需人员确认/审批，或已结束；不能代审")
	}
	var next map[string]any
	steps, _ := run["steps"].([]any)
	for _, row := range steps {
		step := mapValue(row)
		if stringArg(step, "status") != "success" {
			next = step
			break
		}
	}
	if next == nil || stringArg(next, "status") == "executing" {
		return fmt.Errorf("没有可推进节点，或原操作结果待核对")
	}
	manual := name == "record_delivery_manual_step"
	preparation := plan["preparation_request"] != nil
	if preparation && (!t.HasScope("delivery:prepare") || manual) {
		return fmt.Errorf("准备阶段需 delivery:prepare；人工确认/审核须由指定人员在页面处理")
	}
	if manual != (stringArg(next, "kind") == "manual") {
		return fmt.Errorf("人工步骤需独立回执；自动节点不能登记为人工结果")
	}
	if kind := stringArg(next, "kind"); kind == "approval" {
		return fmt.Errorf("不能通过MCP代审")
	}
	comment := strings.TrimSpace(stringArg(args, "comment"))
	if manual && comment == "" {
		return fmt.Errorf("必须填写实际人工回执")
	}
	a.Baseline = fingerprint(v)
	a.Path = "/api/delivery/mcp/runs/" + id + "/advance"
	a.Body = map[string]any{"revision": run["revision"], "digest": run["digest"], "node_id": next["node_id"], "service": next["service"], "manual": manual, "comment": comment}
	a.Preview = map[string]any{"environment": env, "run_id": run["id"], "revision": run["revision"], "digest": run["digest"], "current_step": next, "plan": plan, "manual_receipt": manual, "comment": comment, "verification": map[bool]string{true: "人工记录，不是机器验证", false: "仅推进冻结的当前节点，不自动推进后续节点"}[manual]}
	return nil
}

func safeDeliveryID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			continue
		}
		return false
	}
	return true
}

func prepareScopeConfirmation(t *AccessToken, args map[string]any, a *PlatformAction, run, plan map[string]any, raw any, id string) error {
	if plan["preparation_request"] == nil || stringArg(mapValue(plan["template"]), "workbench") != "research" || stringArg(run, "status") != "waiting_confirmation" {
		return fmt.Errorf("仅支持统一准备流程当前的范围确认；正式审批和人工验证仍须在页面处理")
	}
	var step map[string]any
	steps, _ := run["steps"].([]any)
	for _, row := range steps {
		if stringArg(mapValue(row), "status") != "success" {
			step = mapValue(row)
			break
		}
	}
	var node map[string]any
	nodes, _ := plan["nodes"].([]any)
	for _, row := range nodes {
		if stringArg(mapValue(row), "id") == stringArg(step, "node_id") {
			node = mapValue(row)
			break
		}
	}
	if node == nil || stringArg(node, "kind") != "manual" || stringArg(step, "kind") != "manual" {
		return fmt.Errorf("当前不是范围确认节点，不能确认审批或验证节点")
	}
	if err := scopeConfirmationPeople(t, node); err != nil {
		return err
	}
	comment := strings.TrimSpace(stringArg(args, "comment"))
	if comment == "" || len(comment) > 8000 {
		return fmt.Errorf("请填写不超过8000字节的本人范围确认说明")
	}
	done := map[int64]bool{}
	reviews, _ := step["reviews"].([]any)
	already := false
	for _, row := range reviews {
		r := mapValue(row)
		if stringArg(r, "actor") == t.Owner {
			already = true
			if r["approved"] == true {
				ids, _ := r["work_item_ids"].([]any)
				for _, wid := range ids {
					done[intArg(map[string]any{"id": wid}, "id", 0)] = true
				}
			}
		}
	}
	assigned, _ := mapValue(node["assigned_work_items"])[t.Owner].([]any)
	ids, specified := args["work_item_ids"].([]any)
	if !specified {
		ids = []any{}
		for _, wid := range assigned {
			if !done[intArg(map[string]any{"id": wid}, "id", 0)] {
				ids = append(ids, wid)
			}
		}
	}
	if len(assigned) == 0 && (already || len(ids) > 0) || len(assigned) > 0 && len(ids) == 0 {
		return fmt.Errorf("本人已确认，或没有分配给本人的待确认工作项")
	}
	seen := map[int64]bool{}
	for _, wid := range ids {
		id := intArg(map[string]any{"id": wid}, "id", 0)
		allowed := false
		for _, other := range assigned {
			allowed = allowed || intArg(map[string]any{"id": other}, "id", 0) == id
		}
		if !allowed || done[id] || seen[id] {
			return fmt.Errorf("只能确认分配给本人的未确认工作项")
		}
		seen[id] = true
	}
	a.Baseline = fingerprint(raw)
	a.Path = "/api/delivery/mcp/runs/" + id + "/confirm-scope"
	a.Body = map[string]any{"revision": run["revision"], "digest": run["digest"], "node_id": step["node_id"], "work_item_ids": ids, "comment": comment, "confirmation_id": a.ID}
	a.Preview = map[string]any{"actor": t.Owner, "environment": mapValue(plan["environment"])["name"], "run_id": run["id"], "revision": run["revision"], "digest": run["digest"], "current_step": step, "confirmation_node": node, "scope": plan["scope"], "plan": plan, "work_item_ids": ids, "comment": comment, "effect": "仅记录本人范围确认，不代审、不合并、不构建、不部署；后续节点须独立操作"}
	return nil
}

func scopeConfirmationPeople(t *AccessToken, node map[string]any) error {
	var enabled []string
	if json.Unmarshal([]byte(t.reviewers), &enabled) != nil {
		return fmt.Errorf("可信人员目录不可用")
	}
	approvers, _ := node["approvers"].([]any)
	self := false
	for _, name := range approvers {
		person, _ := name.(string)
		if !slices.Contains(enabled, person) {
			return fmt.Errorf("节点人员已停用，请重新准备")
		}
		self = self || person == t.Owner
	}
	if !self {
		return fmt.Errorf("仅Key绑定人本人被指定为当前确认人时可确认；管理员不能代签")
	}
	return nil
}

func (s *ToolService) changeWorkItemPreview(ctx context.Context, t *AccessToken, id string) (map[string]any, error) {
	v, err := s.platformRequest(ctx, t, "GET", "/api/research/work-items/"+id, nil, nil)
	if err != nil {
		return nil, err
	}
	item := mapValue(mapValue(v)["item"])
	if item == nil {
		item = mapValue(v)
	}
	if intArg(item, "id", 0) <= 0 || stringArg(item, "title") == "" {
		return nil, fmt.Errorf("未返回实际工作项身份，不能确认关联")
	}
	identity := map[string]any{}
	for _, key := range []string{"id", "title", "source", "external_id", "kind"} {
		identity[key] = item[key]
	}
	return map[string]any{"work_item": identity, "association": "新增变更关联此工作项，仅保存草稿，不执行发布"}, nil
}

func (s *ToolService) prepareChangesReady(ctx context.Context, t *AccessToken, args map[string]any, a *PlatformAction) error {
	if err := globalWorkScope(t); err != nil {
		return err
	}
	id, err := positiveID(args, "work_item_id")
	if err != nil {
		return err
	}
	a.BaselinePath = "/api/work-items/" + id + "/delivery-content"
	a.BaselineField = "digest"
	v, err := s.platformRequest(ctx, t, "GET", a.BaselinePath, nil, nil)
	if err != nil {
		return err
	}
	current := mapValue(v)
	digest := stringArg(current, "digest")
	if digest == "" {
		return fmt.Errorf("草稿缺少摘要")
	}
	wanted := map[int64]bool{}
	ids, _ := args["change_ids"].([]any)
	for _, raw := range ids {
		n := intArg(map[string]any{"id": raw}, "id", 0)
		if n <= 0 || wanted[n] {
			return fmt.Errorf("变更编号无效或重复")
		}
		wanted[n] = true
	}
	rows := []any{}
	all, _ := current["changes"].([]any)
	for _, raw := range all {
		row := mapValue(raw)
		n := intArg(row, "id", 0)
		if !wanted[n] {
			continue
		}
		if stringArg(row, "owner") != t.Owner {
			return fmt.Errorf("只能就绪本人变更")
		}
		if stringArg(row, "status") != "draft" {
			return fmt.Errorf("变更不是可维护草稿")
		}
		if err := t.Allows(stringArg(row, "service"), ""); err != nil {
			return err
		}
		rows = append(rows, cloneJSON(row))
		delete(wanted, n)
	}
	if len(wanted) > 0 || len(rows) == 0 {
		return fmt.Errorf("指定变更不在工作项范围")
	}
	a.Path = a.BaselinePath
	a.Method = "PUT"
	a.Baseline = fingerprint(digest)
	a.Body = map[string]any{"digest": digest, "changes": rows, "mark_ready": true}
	a.Preview = map[string]any{"work_item_id": id, "changes": rows, "state_after": "ready", "executes_resources": false}
	return nil
}

func publicationAccess(t *AccessToken, env string) error {
	if err := deliveryAccess(t, env); err != nil {
		return err
	}
	for _, scope := range []string{"delivery:read", "delivery:write", "delivery:execute"} {
		if !t.HasScope(scope) {
			return fmt.Errorf("发布需要 %s 权限", scope)
		}
	}
	return nil
}
