package devtools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Persist before sending any write. An interrupted/uncertain request is never
// automatically replayed, even across a process restart or concurrent calls.
type PlatformAction struct {
	TestingScenario    bool           `json:"testing_scenario,omitempty"`
	TestingServices    []string       `json:"testing_services,omitempty"`
	Preview            map[string]any `json:"preview,omitempty"`
	ConfirmationReason string         `json:"confirmation_reason,omitempty"`
	ID                 string         `json:"confirmation_id"`
	TokenID            string         `json:"token_id"`
	Owner              string         `json:"owner"`
	Tool               string         `json:"tool"`
	Scope              string         `json:"scope"`
	State              string         `json:"state"`
	ExpiresAt          int64          `json:"expires_at"`
	Method             string         `json:"method"`
	Path               string         `json:"path"`
	Query              url.Values     `json:"query,omitempty"`
	Body               map[string]any `json:"body"`
	BaselinePath       string         `json:"baseline_path"`
	BaselineQuery      url.Values     `json:"baseline_query,omitempty"`
	BaselineService    string         `json:"baseline_service,omitempty"`
	BaselineField      string         `json:"baseline_field,omitempty"`
	Baseline           string         `json:"baseline"`
	Result             any            `json:"result,omitempty"`
	Error              string         `json:"error,omitempty"`
}

func validBugAction(a string) bool {
	switch a {
	case "resolve", "activate", "close", "assignTo", "comment":
		return true
	}
	return false
}
func (s *ToolService) actionBaseline(ctx context.Context, t *AccessToken, a *PlatformAction) (string, error) {
	if a.Tool == "mark_independent_changes_ready" {
		result, err := s.platformRequest(ctx, t, "POST", a.Path+"/preview", nil, a.Body)
		if err != nil {
			return "", err
		}
		return stringArg(mapValue(result), "digest"), nil
	}
	if a.Tool == "deploy_service" || a.Tool == "publish_delivery" && a.Path == "/api/service-deployments" {
		v, e := s.platformRequest(ctx, t, "POST", "/api/service-deployments/preview", nil, a.Body["spec"])
		if e != nil {
			return "", e
		}
		return fingerprint(mapValue(v)["digest"]), nil
	}
	if a.Tool == "reauthorize_work_loop" {
		v, e := s.platformRequest(ctx, t, "POST", a.Path+"/preview", nil, map[string]any{"revision": a.Body["revision"], "spec": a.Body["spec"]})
		if e != nil {
			return "", e
		}
		return fingerprint(mapValue(v)["digest"]), nil
	}
	if a.Tool == "create_work_loop" || a.Tool == "create_change_tracking" {
		v, e := s.platformRequest(ctx, t, "POST", "/api/work-loops/preview", nil, a.Body["spec"])
		if e != nil {
			return "", e
		}
		return fingerprint(mapValue(v)["digest"]), nil
	}
	v, e := s.platformRequest(ctx, t, "GET", a.BaselinePath, a.BaselineQuery, nil)
	if e != nil {
		return "", e
	}
	if a.BaselineService != "" {
		rows, _ := v.([]any)
		found := false
		for _, r := range rows {
			if stringArg(mapValue(r), "name") == a.BaselineService {
				v = r
				found = true
				break
			}
		}
		if !found {
			return "", fmt.Errorf("应用不存在")
		}
	}
	if a.Tool == "trigger_build" && a.Query.Get("environment") != "" {
		policy := mapValue(v)
		binding := mapValue(policy["workflow_binding"])
		if stringArg(policy, "service") != stringArg(a.Body, "service") || stringArg(policy, "environment") != a.Query.Get("environment") || binding["protected"] == true || stringArg(binding, "purpose") == "production" {
			return "", fmt.Errorf("构建环境身份或保护策略已变化，请重新核对")
		}
	}
	if a.BaselineField != "" {
		v = mapValue(v)[a.BaselineField]
		if v == nil || v == "" {
			return "", fmt.Errorf("上游未返回编辑基线摘要")
		}
	}
	return fingerprint(v), nil
}
func (s *ToolService) prepareAction(ctx context.Context, t *AccessToken, name string, args map[string]any) (any, error) {
	if s.actions == nil {
		return nil, fmt.Errorf("操作确认存储不可用")
	}
	tool, _ := s.tool(name)
	a := PlatformAction{Tool: name, Scope: tool.Scope, Owner: t.Owner, TokenID: t.ID, State: "preview", Method: "POST", ExpiresAt: time.Now().Add(10 * time.Minute).Unix(), Body: map[string]any{}}
	buf := make([]byte, 16)
	if _, e := rand.Read(buf); e != nil {
		return nil, e
	}
	a.ID = hex.EncodeToString(buf)
	switch name {
	case "mark_independent_changes_ready":
		a.Path = "/api/changes/readiness"
		a.Query = url.Values{"service": {stringArg(args, "service")}}
		a.Body = mapValue(cloneJSON(args))
		result, err := s.platformRequest(ctx, t, "POST", a.Path+"/preview", nil, a.Body)
		if err != nil {
			return nil, err
		}
		a.Preview = mapValue(result)
		a.Baseline = stringArg(a.Preview, "digest")
		if a.Baseline == "" {
			return nil, fmt.Errorf("未返回待就绪变更摘要")
		}
		a.Body["digest"] = a.Baseline
	case "deploy_service":
		if err := s.prepareServiceDeployment(ctx, t, args, &a); err != nil {
			return nil, err
		}
	case "reauthorize_work_loop":
		if err := s.prepareWorkLoopReauthorization(ctx, t, args, &a); err != nil {
			return nil, err
		}
	case "create_work_loop", "create_change_tracking":
		if err := s.prepareWorkLoop(ctx, t, args, &a); err != nil {
			return nil, err
		}
	case "create_delivery_run", "publish_delivery", "advance_delivery", "start_code_preparation", "resume_code_preparation", "retry_prepared_builds", "confirm_delivery_scope", "record_delivery_manual_step", "mark_work_item_changes_ready":
		if err := s.prepareDeliveryAction(ctx, t, name, args, &a); err != nil {
			return nil, err
		}
	case "maintain_change":
		if err := globalWorkScope(t); err != nil && stringArg(args, "action") != "ready" {
			return nil, err
		}
		cid, err := positiveID(args, "change_id")
		if err != nil {
			return nil, err
		}
		service := stringArg(args, "service")
		a.Path = "/api/changes/" + cid + "/maintenance"
		a.BaselinePath = a.Path
		a.BaselineQuery = url.Values{"service": {service}}
		a.BaselineField = "digest"
		body := mapValue(cloneJSON(args))
		delete(body, "change_id")
		preview, err := s.platformRequest(ctx, t, "POST", a.Path+"/preview", nil, body)
		if err != nil {
			return nil, err
		}
		a.Preview = mapValue(preview)
		a.Body = mapValue(a.Preview["request"])
		if a.Body == nil || a.Body["digest"] != args["digest"] {
			return nil, fmt.Errorf("未返回原摘要冻结的维护请求")
		}
		a.Baseline = fingerprint(args["digest"])
		a.Query = url.Values{"service": {service}}
	case "configure_application_resources", "configure_resource_connection":
		if err := s.prepareResourceAction(ctx, t, name, args, &a); err != nil {
			return nil, err
		}
	case "configure_api_testing", "verify_api_authentication", "execute_api_test", "save_api_test_case", "save_api_test_layout", "save_api_test_suite", "stop_api_test_run":
		if err := s.prepareTestingAction(ctx, t, name, args, &a); err != nil {
			return nil, err
		}
	case "report_api_document", "patch_api_document":
		var preview any
		var err error
		if name == "patch_api_document" {
			preview, err = s.prepareDocumentPatch(ctx, t, args, &a)
		} else {
			preview, err = s.prepareDocumentReport(ctx, t, args, &a)
		}
		if err != nil {
			return nil, err
		}
		if err = s.actions.addAction(a); err != nil {
			return nil, err
		}
		return map[string]any{"confirmation_id": a.ID, "tool": name, "state": "preview", "expires_at": a.ExpiresAt, "actor": a.Owner, "preview": preview, "warning": "尚未入库。展示目标应用、环境、差异和重复状态，明确同意后调用 confirm_platform_action；重复内容复用已有快照，不改变最新版本。"}, nil
	case "attach_source_work_item":
		if e := globalWorkScope(t); e != nil {
			return nil, e
		}
		id := stringArg(args, "external_id")
		for _, r := range id {
			if r < '0' || r > '9' {
				return nil, fmt.Errorf("来源 Bug 编号必须是数字")
			}
		}
		a.Path = "/api/research/work-items/attach"
		a.Body = map[string]any{"connection": stringArg(args, "connection"), "external_id": id, "kind": stringArg(args, "kind")}
		a.BaselinePath = "/api/research/work-items/sources"
	case "change_work_item_state":
		if e := globalWorkScope(t); e != nil {
			return nil, e
		}
		id, e := positiveID(args, "work_item_id")
		if e != nil {
			return nil, e
		}
		action := stringArg(args, "action")
		if !validBugAction(action) {
			return nil, fmt.Errorf("不支持的 Bug 操作")
		}
		a.Path = "/api/research/work-items/" + id + "/actions"
		a.BaselinePath = "/api/research/work-items/" + id + "/action-options"
		a.BaselineQuery = url.Values{"action": {action}}
		a.BaselineField = "digest"
		v, e := s.platformRequest(ctx, t, "GET", a.BaselinePath, a.BaselineQuery, nil)
		if e != nil {
			return nil, e
		}
		digest := stringArg(mapValue(v), "digest")
		if digest == "" {
			return nil, fmt.Errorf("来源未提供摘要，不能安全回写")
		}
		a.Baseline = fingerprint(digest)
		for k, v := range args {
			if k != "work_item_id" {
				a.Body[k] = v
			}
		}
		a.Body["operation_id"] = a.ID
		a.Body["expected_digest"] = digest
	case "add_work_item_changes", "report_changes":
		var rows []any
		digest := ""
		_, associated := args["work_item_id"]
		if name == "add_work_item_changes" || associated {
			if e := globalWorkScope(t); e != nil {
				return nil, e
			}
			id, e := positiveID(args, "work_item_id")
			if e != nil {
				return nil, e
			}
			a.Method = "PUT"
			a.Path = "/api/work-items/" + id + "/delivery-content"
			a.BaselinePath = a.Path
			a.BaselineField = "digest"
			v, e := s.platformRequest(ctx, t, "GET", a.BaselinePath, nil, nil)
			if e != nil {
				return nil, e
			}
			current := mapValue(v)
			digest = stringArg(current, "digest")
			if digest == "" {
				return nil, fmt.Errorf("交付草稿缺少摘要")
			}
			a.Baseline = fingerprint(digest)
			a.Preview, e = s.changeWorkItemPreview(ctx, t, id)
			if e != nil {
				return nil, e
			}
			// Omitted items are preserved by SaveWorkDelivery. Resubmitting them
			// would incorrectly edit ready items or another contributor's draft.
		} else {
			a.Path = "/api/changes/batch"
			a.BaselinePath = "/api/services"
			a.BaselineService = stringArg(args, "service")
		}
		newRows, _ := args["changes"].([]any)
		if len(newRows) < 1 || len(newRows) > 50 {
			return nil, fmt.Errorf("一次登记 1–50 项变更")
		}
		allowed := map[string]bool{"kind": true, "title": true, "service": true, "reference": true, "summary": true, "risk_level": true, "rollback_plan": true, "sql": true, "nacos": true, "redis": true, "delivery_ownership": true}
		allowed["prerequisites"] = true
		allowed["xxl_job"] = true
		for _, r := range newRows {
			c := mapValue(cloneJSON(r))
			if len(c) == 0 || stringArg(c, "title") == "" {
				return nil, fmt.Errorf("变更标题必填")
			}
			for k := range c {
				if !allowed[k] {
					return nil, fmt.Errorf("新增变更不允许字段 %s", k)
				}
			}
			if err := validateChangeShape(changeDraftSchema(), c, "changes"); err != nil {
				return nil, err
			}
			if name == "report_changes" {
				service := stringArg(args, "service")
				if actual := stringArg(c, "service"); actual != "" && actual != service {
					return nil, fmt.Errorf("变更应用与本次上报应用不一致")
				}
				c["service"] = service
			}
			switch stringArg(c, "kind") {
			case "code", "artifact", "mysql", "nacos", "redis", "manual", "xxl_job":
			default:
				return nil, fmt.Errorf("变更类型无效")
			}
			if service := stringArg(c, "service"); service != "" {
				if e := t.Allows(service, ""); e != nil {
					return nil, e
				}
			}
			c["status"] = "draft"
			if a.Method == "PUT" {
				if c["delivery_ownership"] == nil {
					c["delivery_ownership"] = map[string]any{"mode": "draft"}
				}
			} else if c["delivery_ownership"] != nil {
				return nil, fmt.Errorf("独立变更不能指定交付归属，请先关联工作项")
			}
			rows = append(rows, c)
		}
		a.Body = map[string]any{"changes": rows}
		if name == "report_changes" {
			if a.Preview == nil {
				a.Preview = map[string]any{"association": "独立草稿"}
			}
			a.Preview["service"] = stringArg(args, "service")
			a.Preview["count"] = len(rows)
			a.Preview["result_status"] = "draft"
		}
		if digest != "" {
			a.Body["digest"] = digest
		}
		if raw, err := json.Marshal(a.Body); err != nil || len(raw) > 2<<20 {
			return nil, fmt.Errorf("批量变更内容不能超过 2 MiB")
		}
	case "trigger_build":
		service, branch := stringArg(args, "service"), stringArg(args, "branch")
		if e := t.Allows(service, ""); e != nil {
			return nil, e
		}
		if strings.ContainsAny(branch, "\r\n\x00") || strings.HasPrefix(branch, "-") || len(branch) > 255 {
			return nil, fmt.Errorf("构建分支无效")
		}
		a.Path = "/api/pipeline/runs"
		a.Body = map[string]any{"service": service, "branch": branch, "auto_deploy": false}
		a.BaselinePath = "/api/services"
		a.BaselineService = service
		if environment := stringArg(args, "environment"); environment != "" {
			a.Query = url.Values{"service": {service}, "environment": {environment}}
			policy, err := s.platformRequest(ctx, t, "GET", "/api/services/"+url.PathEscape(service)+"/environments/"+url.PathEscape(environment)+"/code-policy", nil, nil)
			if err != nil {
				return nil, err
			}
			p := mapValue(policy)
			binding := mapValue(p["workflow_binding"])
			if binding["protected"] == true || stringArg(binding, "purpose") == "production" {
				return nil, fmt.Errorf("MCP构建仅支持非受保护开发/测试环境")
			}
			if stringArg(p, "service") != service || stringArg(p, "environment") != environment || stringArg(p, "policy_digest") == "" {
				return nil, fmt.Errorf("未返回准确环境的构建契约")
			}
			code := mapValue(p["code_policy"])
			target := stringArg(code, "target_branch")
			if p["requires_scope_resolution"] == true || strings.Contains(target, "{") {
				return nil, fmt.Errorf("环境分支依赖交付范围，请使用统一交付代码准备")
			}
			if target != "" && branch != target {
				return nil, fmt.Errorf("环境目标分支为 %s；需求源不能代替环境构建目标，请使用统一交付代码准备", target)
			}
			a.Body["build_environment"] = environment
			a.Preview = map[string]any{"environment_policy": p, "merges_requirement_sources": false}
			a.BaselinePath = "/api/services/" + url.PathEscape(service) + "/environments/" + url.PathEscape(environment) + "/code-policy"
			a.BaselineService = ""
			a.BaselineField = "policy_digest"
			a.Baseline = fingerprint(p["policy_digest"])
		}
	default:
		return nil, fmt.Errorf("不支持的写操作")
	}
	if a.Path == "/api/service-deployments" {
		if id := stringArg(a.Preview, "original_task_id"); id != "" {
			return s.callServiceDeployment(ctx, t, "get_service_deployment", map[string]any{"task_id": id})
		}
	}
	if a.Baseline == "" {
		var e error
		a.Baseline, e = s.actionBaseline(ctx, t, &a)
		if e != nil {
			return nil, e
		}
	}
	if e := s.actions.addAction(a); e != nil {
		return nil, e
	}
	service := a.Query.Get("service")
	if name == "report_changes" {
		service = stringArg(a.Preview, "service")
	}
	return map[string]any{"confirmation_id": a.ID, "tool": name, "state": "preview", "expires_at": a.ExpiresAt, "actor": a.Owner, "request": a.Body, "preview": a.Preview, "service": service, "environment": a.Query.Get("environment"), "warning": "尚未执行。请向用户展示此操作与目标，明确同意后调用 confirm_platform_action；不自动重放未知结果。"}, nil
}
func (s *TokenStore) addAction(a PlatformAction) error {
	a.Body = mapValue(cloneJSON(a.Body))
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := []PlatformAction{}
	for _, old := range s.data.Actions {
		if old.ExpiresAt > time.Now().Unix() || old.State == "executing" {
			rows = append(rows, old)
		}
	}
	if len(rows) >= 100 {
		return fmt.Errorf("待确认操作过多，请先处理已有操作")
	}
	s.data.Actions = append(rows, a)
	return s.saveLocked()
}
func (s *TokenStore) actionStatus(t *AccessToken, id string) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.data.Actions {
		if a.ID == id && a.TokenID == t.ID && a.Owner == t.Owner {
			return map[string]any{"confirmation_id": a.ID, "tool": a.Tool, "state": a.State, "expires_at": a.ExpiresAt, "result": a.Result, "error": a.Error, "reason": a.ConfirmationReason, "message": "executing / uncertain 表示须先核对平台记录，不得重新发送写入；Bug 操作可使用同 ID 查询来源回写记录。"}, nil
		}
	}
	return nil, fmt.Errorf("本令牌没有此操作")
}

// Inspect only the identity-bound frozen target, before claiming a write.
func (s *TokenStore) confirmationEnvironment(t *AccessToken, id string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.data.Actions {
		if a.ID == id && a.TokenID == t.ID && a.Owner == t.Owner {
			return a.Query.Get("environment"), a.Body["confirm_protected"] == true, nil
		}
	}
	return "", false, fmt.Errorf("本令牌没有此操作")
}

func (s *TokenStore) claimAction(t *AccessToken, id string, reason ...string) (PlatformAction, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Actions {
		a := &s.data.Actions[i]
		if a.ID != id || a.TokenID != t.ID || a.Owner != t.Owner {
			continue
		}
		if !t.HasScope(a.Scope) {
			return *a, false, fmt.Errorf("令牌没有此写操作权限")
		}
		if a.State != "preview" {
			return *a, false, nil
		}
		if a.ExpiresAt <= time.Now().Unix() {
			return *a, false, fmt.Errorf("确认已过期，请重新预览")
		}
		a.State = "executing"
		if len(reason) > 0 {
			a.ConfirmationReason = truncate(scrubString(reason[0]), 2000)
		}
		if e := s.saveLocked(); e != nil {
			a.State = "preview"
			return *a, false, e
		}
		return *a, true, nil
	}
	return PlatformAction{}, false, fmt.Errorf("本令牌没有此操作")
}
func (s *TokenStore) finishAction(id, state string, result any, err error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Actions {
		a := &s.data.Actions[i]
		if a.ID == id {
			a.State = state
			a.Result = result
			if err != nil {
				a.Error = truncate(scrubString(err.Error()), 500)
			}
			return s.saveLocked()
		}
	}
	return fmt.Errorf("操作不存在")
}
func (s *ToolService) checkReasonMayBeOmitted(ctx context.Context, t *AccessToken, env string) error {
	value, err := s.platformRequest(ctx, t, "GET", "/api/environments", nil, nil)
	if err != nil {
		return err
	}
	rows, _ := value.([]any)
	for _, v := range rows {
		row := mapValue(v)
		if stringArg(row, "name") == env && row["protected"] == false {
			return nil
		}
	}
	return fmt.Errorf("受保护或无法核实的环境须填写操作原因")
}
func (s *ToolService) confirmAction(ctx context.Context, t *AccessToken, args map[string]any) (any, error) {
	if s.actions == nil {
		return nil, fmt.Errorf("确认存储不可用")
	}
	if args["confirm"] != true {
		return nil, fmt.Errorf("请明确确认这份操作预览")
	}
	id := stringArg(args, "confirmation_id")
	if stringArg(args, "reason") == "" {
		env, protected, err := s.actions.confirmationEnvironment(t, id)
		if err != nil {
			return nil, err
		}
		if protected {
			return nil, fmt.Errorf("受保护环境须填写操作原因")
		}
		if env != "" {
			if err := s.checkReasonMayBeOmitted(ctx, t, env); err != nil {
				return nil, err
			}
		}
	}
	a, claimed, e := s.actions.claimAction(t, id, stringArg(args, "reason"))
	if e != nil {
		return nil, e
	}
	if !claimed {
		return s.actions.actionStatus(t, id)
	}
	if a.Tool == "publish_delivery" {
		if err := publicationAccess(t, a.Query.Get("environment")); err != nil {
			s.actions.finishAction(id, "stale", nil, err)
			return nil, err
		}
	}
	if strings.HasPrefix(a.Scope, "resources:") {
		if err := resourceAccess(t, a.Query.Get("service"), a.Query.Get("environment"), a.Tool == "configure_resource_connection"); err != nil {
			s.actions.finishAction(id, "stale", nil, err)
			return nil, err
		}
	}
	if strings.HasPrefix(a.Scope, "apitests:") {
		var accessErr error
		if a.TestingScenario {
			accessErr = scenarioAccess(t, a.Query.Get("environment"), a.TestingServices)
		} else {
			accessErr = testingAccess(t, a.Query.Get("service"), a.Query.Get("environment"))
		}
		if err := accessErr; err != nil {
			s.actions.finishAction(id, "stale", nil, err)
			return nil, err
		}
		if a.TestingScenario && a.Body["confirm_protected"] == true && t.role != "admin" {
			err := fmt.Errorf("受保护环境的管理员身份已变化")
			s.actions.finishAction(id, "stale", nil, err)
			return nil, err
		}
		if a.Tool == "configure_api_testing" && t.role != "admin" {
			err := fmt.Errorf("管理员身份已变化")
			s.actions.finishAction(id, "stale", nil, err)
			return nil, err
		}
	}
	if a.Tool == "report_api_document" || a.Tool == "patch_api_document" {
		if err := documentAccess(t, a.Query.Get("service"), a.Query.Get("environment"), true); err != nil {
			s.actions.finishAction(id, "stale", nil, err)
			return nil, err
		}
	}
	if a.Tool == "create_work_loop" || a.Tool == "create_change_tracking" || a.Tool == "reauthorize_work_loop" {
		if err := workLoopAccess(t, mapValue(a.Body["spec"]), true); err != nil {
			s.actions.finishAction(id, "stale", nil, err)
			return nil, err
		}
	}
	if a.Tool == "deploy_service" || a.Tool == "publish_delivery" && a.Path == "/api/service-deployments" {
		if err := deploymentGrantAccess(t, a.Preview); err != nil {
			s.actions.finishAction(id, "stale", nil, err)
			return nil, err
		}
	}
	if strings.HasPrefix(a.Scope, "delivery:") && a.Tool != "deploy_service" {
		var accessErr error
		if a.Tool == "resume_code_preparation" {
			accessErr = s.publicationRecoveryAccess(ctx, t, stringArg(a.Preview, "publication_task_id"), stringArg(mapValue(a.Preview["code_preparation"]), "id"), a.Query.Get("environment"))
		} else {
			accessErr = s.deliveryEnvironment(ctx, t, a.Query.Get("environment"))
		}
		if err := accessErr; err != nil {
			s.actions.finishAction(id, "stale", nil, err)
			return nil, err
		}
		if (a.Tool == "advance_delivery" && mapValue(a.Preview["plan"])["preparation_request"] != nil || a.Tool == "start_code_preparation" || a.Tool == "resume_code_preparation" || a.Tool == "retry_prepared_builds") && !t.HasScope("delivery:prepare") {
			err := fmt.Errorf("代码准备权限已变化")
			s.actions.finishAction(id, "stale", nil, err)
			return nil, err
		}
		if a.Tool == "confirm_delivery_scope" {
			if err := scopeConfirmationPeople(t, mapValue(a.Preview["confirmation_node"])); err != nil {
				s.actions.finishAction(id, "stale", nil, err)
				return nil, err
			}
		}
	}
	if a.Tool == "trigger_build" {
		if err := t.Allows(stringArg(a.Body, "service"), a.Query.Get("environment")); err != nil {
			s.actions.finishAction(id, "stale", nil, err)
			return nil, err
		}
	}
	if a.Tool == "maintain_change" || a.Tool == "mark_independent_changes_ready" {
		if err := globalWorkScope(t); err != nil && a.Tool == "maintain_change" && stringArg(a.Body, "action") != "ready" {
			s.actions.finishAction(id, "stale", nil, err)
			return nil, err
		}
		if err := t.Allows(a.Query.Get("service"), ""); err != nil {
			s.actions.finishAction(id, "stale", nil, err)
			return nil, err
		}
	}
	if a.Tool == "mark_work_item_changes_ready" {
		if err := globalWorkScope(t); err != nil {
			s.actions.finishAction(id, "stale", nil, err)
			return nil, err
		}
		rows, _ := a.Body["changes"].([]any)
		for _, row := range rows {
			if err := t.Allows(stringArg(mapValue(row), "service"), ""); err != nil {
				s.actions.finishAction(id, "stale", nil, err)
				return nil, err
			}
		}
	}
	if a.Tool == "report_changes" || a.Tool == "add_work_item_changes" {
		if strings.HasPrefix(a.Path, "/api/work-items/") {
			if err := globalWorkScope(t); err != nil {
				s.actions.finishAction(id, "stale", nil, err)
				return nil, err
			}
		}
		rows, _ := a.Body["changes"].([]any)
		for _, row := range rows {
			if err := t.Allows(stringArg(mapValue(row), "service"), ""); err != nil {
				s.actions.finishAction(id, "stale", nil, err)
				return nil, err
			}
		}
	}
	baseline, e := s.actionBaseline(ctx, t, &a)
	if e != nil || baseline != a.Baseline {
		if e == nil {
			e = fmt.Errorf("目标状态或配置已变化，请重新预览")
		}
		s.actions.finishAction(id, "stale", nil, e)
		return nil, e
	}
	v, e := s.platformRequest(ctx, t, a.Method, a.Path, a.Query, a.Body)
	state := "success"
	if e != nil {
		state = "uncertain"
	}
	if saveErr := s.actions.finishAction(id, state, scrubAny(v), e); saveErr != nil {
		return nil, fmt.Errorf("结果记录失败，请核对平台记录，不得自动重试")
	}
	if e != nil {
		return nil, fmt.Errorf("结果待核对，勿自动重放；confirmation_id=%s: %w", id, e)
	}
	return s.actions.actionStatus(t, id)
}
