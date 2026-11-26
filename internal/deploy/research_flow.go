package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

func validateResearchNodes(nodes []DeliveryNode) error {
	if len(nodes) < 1 || len(nodes) > 30 {
		return fmt.Errorf("准备流程需要 1–30 个节点")
	}
	seen := map[string]bool{}
	codeSeen := false
	for _, n := range nodes {
		if !environmentNameRE.MatchString(n.ID) || seen[n.ID] || strings.TrimSpace(n.Name) == "" {
			return fmt.Errorf("节点标识或名称无效")
		}
		for _, id := range n.DependsOn {
			if !seen[id] {
				return fmt.Errorf("顺序流程依赖必须在当前节点之前")
			}
		}
		seen[n.ID] = true
		switch n.Kind {
		case "verify":
			if !codeSeen {
				return fmt.Errorf("准备阶段的独立验证须放在代码构建之后；已有制品请在执行阶段验证")
			}
			if err := validateOperationNode(n); err != nil {
				return err
			}
		case "approval":
			if err := validateApprovalNode(n); err != nil {
				return err
			}
		case "manual":
			if n.Instructions == "" {
				return fmt.Errorf("人工确认须填写确认说明")
			}
			if n.ApprovalMode != "any" && n.ApprovalMode != "all" {
				return fmt.Errorf("人工确认须选择或签或会签")
			}
			if n.AssigneeSource != "work_item_resolvers" && n.AssigneeSource != "specified" && n.AssigneeSource != "work_item_assignees" && n.AssigneeSource != "work_item_participants" && n.AssigneeSource != "change_submitters" && n.AssigneeSource != "delivery_initiator" {
				return fmt.Errorf("请选择确认人来源")
			}
			if n.AssigneeSource == "specified" {
				if err := validateApprovalNode(n); err != nil {
					return err
				}
			}
		case "code-prepare", "code-verify":
			if n.BuildConcurrency < 0 || n.BuildConcurrency > 8 {
				return fmt.Errorf("构建并发数须为 1–8")
			}
			codeSeen = true
			if n.ApplicationScope != "delivery" {
				return fmt.Errorf("代码节点须跟随交付范围，不再兼容旧固定应用准备节点")
			}
			if (len(n.Policies) == 0 && n.ApplicationScope != "delivery") || len(n.Policies) > 20 {
				return fmt.Errorf("代码节点须配置应用的基线和目标分支")
			}
			if n.DefaultCodePolicy != nil && *n.DefaultCodePolicy != (CodePolicy{}) && !validateCodePolicy(*n.DefaultCodePolicy) {
				return fmt.Errorf("通用代码策略不完整")
			}
			for service, p := range n.Policies {
				if service == "" || !validateCodePolicy(p) {
					return fmt.Errorf("代码策略不完整")
				}
			}
		default:
			return fmt.Errorf("准备流程仅支持代码准备、人工合入核对、人工确认、人工审批和独立验证")
		}
	}
	return nil
}

func researchScopeDigest(scope *DeliveryScope) string {
	items := []any{}
	for _, w := range scope.WorkItems {
		items = append(items, []any{w.ID, w.Source, w.ExternalID, w.ResolvedBy, w.ReleaseRequired, w.NoReleaseReason})
	}
	changes := []any{}
	for _, c := range scope.Changes {
		copy := c
		copy.CreatedAt, copy.UpdatedAt = 0, 0
		copy.Status = ""
		copy.WorkItems = nil
		changes = append(changes, copy)
	}
	return digestJSON([]any{items, changes})
}

func (s *Store) researchPeople(ctx context.Context, r *http.Request) (map[string]string, []string, error) {
	var identities []struct {
		Source string `json:"source"`
		ID     string `json:"external_id"`
		User   string `json:"username"`
	}
	if json.Unmarshal([]byte(r.Header.Get("X-Research-Identities")), &identities) != nil {
		identities = nil
	}
	var enabled []string
	if json.Unmarshal([]byte(r.Header.Get("X-Reviewer-Directory")), &enabled) != nil {
		return nil, nil, fmt.Errorf("人员目录不可用，请通过平台壳操作")
	}
	allow := map[string]bool{}
	for _, name := range enabled {
		allow[name] = true
	}
	accounts := map[string]string{}
	endpoints := map[string]string{}
	for _, c := range s.Connections() {
		if c.Kind != "zentao" {
			continue
		}
		endpoints[c.Name] = digestJSON(c.Endpoint)
		source := researchSourceIdentity(c.Endpoint)
		needed := false
		for _, i := range identities {
			if i.Source == source {
				needed = true
			}
		}
		if !needed {
			continue
		}
		directory, err := s.zentaoUsers(ctx, c.Name)
		if err != nil {
			return nil, nil, err
		}
		for _, u := range directory.Users {
			for _, i := range identities {
				if i.Source == directory.Source && i.ID == u.ID && allow[i.User] {
					accounts["zentao:"+c.Name+"\x00"+u.Account] = i.User
				}
			}
		}
	}
	// Self-verified source credentials are another unambiguous actor mapping.
	rows, err := s.db.Query(`SELECT actor,connection_name,account,endpoint_digest FROM research_credentials`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var actor, connection, account, endpoint string
		if err = rows.Scan(&actor, &connection, &account, &endpoint); err != nil {
			return nil, nil, err
		}
		key := "zentao:" + connection + "\x00" + account
		if allow[actor] && endpoints[connection] != "" && endpoints[connection] == endpoint {
			if previous := accounts[key]; previous != "" && previous != actor {
				return nil, nil, fmt.Errorf("来源账号关联多个平台身份，请核对关联")
			}
			accounts[key] = actor
		}
	}
	return accounts, enabled, rows.Err()
}

func (s *Store) ResearchFlowPlan(req DeliveryRequest, accounts map[string]string, enabled []string, actor string) (*DeliveryPlan, error) {
	original := s.DeliveryTemplate(req.TemplateID)
	unified := original != nil && original.Workbench == "" && len(original.Preparation) > 0
	if !unified {
		diagnostic, err := s.workflowBindingDiagnostic(req.Environment)
		if err != nil {
			return nil, fmt.Errorf("准备阶段不可用（environment=%s, requested_template_id=%d）：%w", req.Environment, req.TemplateID, err)
		}
		return nil, fmt.Errorf("独立准备流程已移除，请使用环境绑定的部署编排准备阶段（environment=%s, policy_revision=%v, workflow_template_id=%v, requested_template_id=%d, binding_status=%v, missing_phases=%v）", req.Environment, diagnostic["policy_revision"], diagnostic["workflow_template_id"], req.TemplateID, diagnostic["status"], diagnostic["missing_phases"])
	}
	t := preparationTemplate(original)
	if t == nil || t.Workbench != "research" {
		return nil, fmt.Errorf("请选择版本准备流程")
	}
	if err := validateResearchNodes(t.Nodes); err != nil {
		return nil, err
	}
	env := s.GetEnvironment(req.Environment)
	if env == nil {
		return nil, fmt.Errorf("请选择目标环境")
	}
	scopeTemplate := t
	if unified {
		scopeTemplate = original
	}
	scope, v, err := s.resolveDeliveryScope(req, scopeTemplate)
	if err != nil || scope == nil {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("准备流程需要版本或工作项来源")
	}
	for _, w := range scope.WorkItems {
		if w.ReleaseRequired != nil && !*w.ReleaseRequired && (!unified || env.Purpose == "production") {
			return nil, fmt.Errorf("工作项 #%d 不需要上线，请调整范围", w.ID)
		}
	}
	allow := map[string]bool{}
	for _, name := range enabled {
		allow[name] = true
	}
	raw, _ := json.Marshal(t.Nodes)
	nodes := []DeliveryNode{}
	_ = json.Unmarshal(raw, &nodes)
	policy, err := s.deliveryPolicy(env.Name)
	if err != nil {
		return nil, err
	}
	if unified {
		if err := validateWorkflowBinding(req, *env, policy); err != nil {
			return nil, err
		}
		if _, err := deliveryArtifactSources(req, scope, nil); err != nil {
			return nil, err
		}
	}
	if err := resolvePreparationPolicies(nodes, scope, v, policy.Defaults, req.ArtifactSources); err != nil {
		return nil, fmt.Errorf("环境 %s 的交付准备受阻：%w", env.Name, err)
	}
	for i := range nodes {
		n := &nodes[i]
		n.AssignedWorkItems = nil
		if n.Kind == "manual" && n.AssigneeSource == "delivery_initiator" {
			// Resolve the trusted caller at preview time, never a client-supplied
			// approver or the author of a reused team change. The resulting plan
			// freezes this identity just like the other assignment policies.
			if env.Protected || (env.Purpose != "development" && env.Purpose != "test") {
				return nil, fmt.Errorf("本次交付发起人确认仅适用于非受保护的开发／测试环境；请调整流程确认人来源")
			}
			if actor == "" || !allow[actor] {
				return nil, fmt.Errorf("交付发起人不存在或已停用，请核对人员配置")
			}
			n.Approvers = []string{actor}
			n.AssignedWorkItems = map[string][]int64{}
			for _, w := range scope.WorkItems {
				matches := n.Service == ""
				for _, c := range scope.Changes {
					if c.Service == n.Service {
						for _, cw := range c.WorkItems {
							matches = matches || cw.ID == w.ID
						}
					}
				}
				if matches {
					n.AssignedWorkItems[actor] = append(n.AssignedWorkItems[actor], w.ID)
				}
			}
		} else if n.Kind == "manual" && n.AssigneeSource != "specified" {
			seen := map[string]bool{}
			n.Approvers = []string{}
			n.AssignedWorkItems = map[string][]int64{}
			for _, w := range scope.WorkItems {
				if n.Service != "" {
					matches := false
					for _, c := range scope.Changes {
						if c.Service == n.Service {
							for _, cw := range c.WorkItems {
								matches = matches || cw.ID == w.ID
							}
						}
					}
					if !matches {
						continue
					}
				}
				people, e := s.researchNodePeople(*n, w, scope, accounts)
				if e != nil {
					return nil, e
				}
				if len(people) == 0 {
					return nil, fmt.Errorf("工作项 #%d 没有匹配的确认人员，请关联人员或改用指定人员", w.ID)
				}
				for _, person := range people {
					if person == "" || !allow[person] {
						return nil, fmt.Errorf("工作项 #%d 的确认人未关联或已停用，请核对人员配置", w.ID)
					}
					if !seen[person] {
						seen[person] = true
						n.Approvers = append(n.Approvers, person)
					}
					n.AssignedWorkItems[person] = append(n.AssignedWorkItems[person], w.ID)
				}
			}
			sort.Strings(n.Approvers)
			if unified && len(scope.WorkItems) == 0 && n.AssigneeSource == "change_submitters" {
				for _, c := range scope.Changes {
					if n.Service != "" && n.Service != c.Service {
						continue
					}
					if c.Owner == "" || !allow[c.Owner] {
						return nil, fmt.Errorf("变更 #%d 的提交人未关联或已停用，请核对人员", c.ID)
					}
					if !seen[c.Owner] {
						seen[c.Owner] = true
						n.Approvers = append(n.Approvers, c.Owner)
					}
				}
				sort.Strings(n.Approvers)
			}
			if len(n.Approvers) == 0 {
				return nil, fmt.Errorf("确认节点没有匹配的工作项")
			}
		}
		if n.Kind == "manual" || n.Kind == "approval" || n.Kind == "verify" && n.Verification.Mode == "manual" {
			for _, name := range n.Approvers {
				if !allow[name] {
					return nil, fmt.Errorf("节点人员 %s 不存在或已停用", name)
				}
				if n.Kind == "approval" && name == actor && n.ApprovalMode == "all" {
					return nil, fmt.Errorf("会签审核不能包含发起人")
				}
			}
			if n.Kind == "approval" {
				eligible := false
				for _, a := range n.Approvers {
					eligible = eligible || a != actor
				}
				if !eligible {
					return nil, fmt.Errorf("审批节点须有发起人之外的审批人")
				}
			}
		}
	}
	p := &DeliveryPlan{Template: *t, Scope: scope, Version: v, Environment: *env, Nodes: nodes, Reason: req.Reason, Blockers: []string{}, Warnings: []string{}, RequestDigest: researchScopeDigest(scope), StrategyKind: "research"}
	if _, err := orderChangeNodes(includeManualRecords(append([]DeliveryNode(nil), original.Nodes...), scope), scope, true); err != nil {
		p.Blockers = append(p.Blockers, err.Error())
	}
	var pendingDraft bool
	for _, change := range scope.Changes {
		if change.Status != ChangeReady {
			p.Blockers = append(p.Blockers, fmt.Sprintf("变更 #%d · %s 尚未就绪，请先校验并就绪", change.ID, change.Title))
			pendingDraft = pendingDraft || change.Status == ChangeDraft
		}
	}
	if pendingDraft && scope.Type == "changes" && len(scope.Changes) > 1 {
		ids := make([]string, 0, len(scope.Changes))
		for _, change := range scope.Changes {
			ids = append(ids, fmt.Sprintf("#%d", change.ID))
		}
		p.Warnings = append(p.Warnings, fmt.Sprintf("请让 Agent 或变更清单校验待就绪项，然后保留本次全部编号 %s，在 %s 重新预览同一次交付。仅在用户明确选择拆分发布时调整范围。", strings.Join(ids, "、"), env.Name))
	}
	if unified {
		req.PreflightToken, req.CodePreparationID, req.ClientRequestID = "", "", ""
		req.Nodes, req.Artifacts, req.PreparationRunID = nil, nil, 0
		p.PreparationRequest, p.PreparationPolicyRevision, p.Scene = &req, &policy.Revision, req.Scene
	}
	return p, nil
}

func (s *Store) CreateResearchFlow(req DeliveryRequest, accounts map[string]string, enabled []string, actor string) (*DeliveryRun, error) {
	requestDigest := digestJSON(req)
	if req.ClientRequestID != "" {
		if !attachmentIDRE.MatchString(req.ClientRequestID) {
			return nil, fmt.Errorf("流程请求编号无效")
		}
		var previous int64
		var digest string
		if s.db.QueryRow(`SELECT run_id,request_digest FROM research_flow_uses WHERE owner=? AND request_id=?`, actor, req.ClientRequestID).Scan(&previous, &digest) == nil {
			if digest != requestDigest {
				return nil, fmt.Errorf("请求编号已用于其他范围")
			}
			return s.DeliveryRun(previous), nil
		}
	}
	p, err := s.ResearchFlowPlan(req, accounts, enabled, actor)
	if err != nil {
		return nil, err
	}
	if len(p.Blockers) != 0 {
		return nil, fmt.Errorf("%s", strings.Join(p.Blockers, "；"))
	}
	v := &DeliveryRun{Plan: *p, Digest: digestJSON(p), Owner: actor, CreatedAt: time.Now().Unix(), Revision: 1, Steps: []DeliveryStep{}, envSecrets: map[string]string{}}
	for _, n := range p.Nodes {
		step := DeliveryStep{NodeID: n.ID, Name: n.Name, Kind: n.Kind, Service: n.Service, Status: "pending"}
		if n.ApplicationScope == "delivery" && (n.Kind == "code-prepare" || n.Kind == "code-verify") && len(n.Policies) == 0 {
			step.Status, step.Detail = "success", "本次无需构建代码，已跳过"
		}
		v.Steps = append(v.Steps, step)
	}
	researchNextState(v)
	plan, _ := json.Marshal(v.Plan)
	steps, _ := json.Marshal(v.Steps)
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := lockArtifactReferences(tx, planArtifactIDs(&v.Plan)...); err != nil {
		return nil, err
	}
	r, err := tx.Exec(`INSERT INTO delivery_runs(plan,digest,status,execution,env_secrets,owner,approver,created_at,revision) VALUES(?,?,?,?,? ,?,'',?,1)`, string(plan), v.Digest, v.Status, string(steps), "{}", actor, v.CreatedAt)
	if err != nil {
		return nil, err
	}
	v.ID, _ = r.LastInsertId()
	if req.ClientRequestID != "" {
		if _, err = tx.Exec(`INSERT INTO research_flow_uses(owner,request_id,run_id,request_digest) VALUES(?,?,?,?)`, actor, req.ClientRequestID, v.ID, requestDigest); err != nil {
			tx.Rollback()
			var previous int64
			var digest string
			if s.db.QueryRow(`SELECT run_id,request_digest FROM research_flow_uses WHERE owner=? AND request_id=?`, actor, req.ClientRequestID).Scan(&previous, &digest) == nil && digest == requestDigest {
				return s.DeliveryRun(previous), nil
			}
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.Audit(actor, "create-preparation-flow", "delivery-run", fmt.Sprint(v.ID), map[string]any{"digest": v.Digest})
	return v, nil
}

func researchNextState(v *DeliveryRun) {
	v.Status = "success"
	for _, step := range v.Steps {
		if step.Status == "success" {
			continue
		}
		switch step.Kind {
		case "verify":
			v.Status = "approved"
			for _, n := range v.Plan.Nodes {
				if n.ID == step.NodeID && n.Verification != nil && n.Verification.Mode == "manual" {
					v.Status = "waiting_confirmation"
				}
			}
		case "approval":
			v.Status = "pending_approval"
		case "manual":
			v.Status = "waiting_confirmation"
		default:
			v.Status = "approved"
		}
		return
	}
}
func researchNextNode(v *DeliveryRun) (*DeliveryStep, *DeliveryNode) {
	for i := range v.Steps {
		if v.Steps[i].Status != "success" {
			for j := range v.Plan.Nodes {
				if v.Plan.Nodes[j].ID == v.Steps[i].NodeID {
					return &v.Steps[i], &v.Plan.Nodes[j]
				}
			}
		}
	}
	return nil, nil
}
func (s *Store) checkResearchScope(v *DeliveryRun) error {
	if v.Plan.PreparationRequest != nil {
		req := *v.Plan.PreparationRequest
		t := s.DeliveryTemplate(req.TemplateID)
		if t == nil || digestJSON(preparationTemplate(t)) != digestJSON(v.Plan.Template) {
			return fmt.Errorf("交付流程已改变，请重新生成准备计划")
		}
		policy, err := s.deliveryPolicy(req.Environment)
		if err != nil {
			return err
		}
		env := s.GetEnvironment(req.Environment)
		if env == nil || v.Plan.PreparationPolicyRevision == nil || *v.Plan.PreparationPolicyRevision != policy.Revision || digestJSON(*env) != digestJSON(v.Plan.Environment) {
			return fmt.Errorf("目标环境或绑定策略已改变，请重新生成准备计划")
		}
		current, _, err := s.resolveDeliveryScope(req, t)
		if err != nil {
			return err
		}
		if current == nil || researchScopeDigest(current) != v.Plan.RequestDigest {
			return fmt.Errorf("本次交付范围已改变，请重新确认")
		}
		return nil
	}
	return fmt.Errorf("旧版准备记录不再支持，请重新生成本次交付")
}

func (s *Store) ReviewResearchTask(id int64, node, actor, digest, comment string, approve bool, enabled []string, selections ...[]int64) (*DeliveryRun, error) {
	v := s.DeliveryRun(id)
	return s.reviewResearchTaskState(v, node, actor, digest, comment, approve, enabled, selections...)
}

// Keep the checked revision through the final CAS. MCP freezes this state in
// its preview; reloading here could confirm a newer scope or another decision.
func (s *Store) reviewResearchTaskState(v *DeliveryRun, node, actor, digest, comment string, approve bool, enabled []string, selections ...[]int64) (*DeliveryRun, error) {
	if v == nil || v.Plan.PreparationRequest == nil || v.Plan.Template.Workbench != "research" || v.Digest != digest || (v.Status != "pending_approval" && v.Status != "waiting_confirmation") {
		return nil, fmt.Errorf("任务状态或摘要已变化")
	}
	if err := s.checkResearchScope(v); err != nil {
		return nil, err
	}
	step, n := researchNextNode(v)
	if n == nil || n.ID != node || (n.Kind != "manual" && n.Kind != "approval" && !(n.Kind == "verify" && n.Verification.Mode == "manual")) || !namedApprover(n, actor) {
		return nil, fmt.Errorf("仅当前节点指定人员可以处理")
	}
	for _, name := range n.Approvers {
		found := false
		for _, a := range enabled {
			found = found || a == name
		}
		if !found {
			return nil, fmt.Errorf("节点人员已停用，请重新准备")
		}
	}
	if n.Kind == "approval" && actor == v.Owner {
		return nil, fmt.Errorf("发起人不能审批自己的发布")
	}
	if len(comment) > 8000 || (!approve && strings.TrimSpace(comment) == "") {
		return nil, fmt.Errorf("拒绝须填写原因，意见不能过长")
	}
	if n.Kind == "verify" && strings.TrimSpace(comment) == "" {
		return nil, fmt.Errorf("请填写验证记录或证据链接")
	}
	var verificationRun *DeliveryRun
	if n.Kind == "verify" {
		var err error
		verificationRun, err = s.preparationVerification(v, *n)
		if err != nil {
			return nil, err
		}
	}
	for _, decision := range step.Reviews {
		if decision.Actor == actor && (n.Kind != "manual" || len(n.AssignedWorkItems[actor]) == 0 || researchPersonComplete(step, n, actor)) {
			return nil, fmt.Errorf("已经处理过本阶段")
		}
	}
	ids := []int64{}
	if approve && n.Kind == "manual" && len(n.AssignedWorkItems[actor]) > 0 {
		allowed, done := map[int64]bool{}, map[int64]bool{}
		for _, wid := range n.AssignedWorkItems[actor] {
			allowed[wid] = true
		}
		for _, review := range step.Reviews {
			if review.Actor == actor && review.Approved {
				for _, wid := range review.WorkItemIDs {
					done[wid] = true
				}
			}
		}
		if len(selections) > 0 && selections[0] != nil {
			ids = selections[0]
		} else {
			for _, wid := range n.AssignedWorkItems[actor] {
				if !done[wid] {
					ids = append(ids, wid)
				}
			}
		}
		if len(ids) == 0 {
			return nil, fmt.Errorf("请至少选择一项尚未确认的工作项")
		}
		seen := map[int64]bool{}
		for _, wid := range ids {
			if !allowed[wid] || done[wid] || seen[wid] {
				return nil, fmt.Errorf("仅能确认分配给自己的未确认工作项")
			}
			seen[wid] = true
		}
	}
	now := time.Now().Unix()
	step.Reviews = append(step.Reviews, ApprovalDecision{Actor: actor, Approved: approve, Comment: comment, At: now, WorkItemIDs: ids})
	if step.StartedAt == 0 {
		step.StartedAt = now
	}
	if !approve {
		step.Status = "rejected"
		step.EndedAt = now
		v.Status = "rejected"
	} else {
		count := 0
		for _, person := range n.Approvers {
			if researchPersonComplete(step, n, person) {
				count++
			}
		}
		if (n.ApprovalMode == "any" && count > 0) || count == len(n.Approvers) {
			step.Status = "success"
			step.EndedAt = now
			if verificationRun != nil {
				step.Evidence = evidenceFor(verificationRun, *n, "manual-verification", actor, "人工验证，不代表机器测试证明")
			}
			researchNextState(v)
			if err := s.bindResearchPreparedArtifacts(v); err != nil {
				return nil, err
			}
		} else {
			step.Status = "reviewing"
		}
	}
	if err := s.saveDeliveryRun(v); err != nil {
		return nil, err
	}
	s.Audit(actor, "research-node-decision", "delivery-run", fmt.Sprint(v.ID), map[string]any{"node": node, "approved": approve, "digest": digest})
	return v, nil
}

// Preparation nodes reuse the frozen Git merge/build implementation. The node
// CAS is persisted BEFORE any remote action; lost responses are reconciled via
// the existing preparation id, never by issuing another merge/build.
func (s *Store) AdvanceResearchFlow(ctx context.Context, id int64, actor string, pr *PipelineRunner) (*DeliveryRun, error) {
	v := s.DeliveryRun(id)
	return s.advanceResearchFlowState(ctx, v, actor, pr)
}

func (s *Store) advanceResearchFlowState(ctx context.Context, v *DeliveryRun, actor string, pr *PipelineRunner) (*DeliveryRun, error) {
	return s.advanceFrozenResearchFlow(ctx, v, actor, pr, nil)
}
func (s *Store) advanceFrozenResearchFlow(ctx context.Context, v *DeliveryRun, actor string, pr *PipelineRunner, batch *publicationScope) (*DeliveryRun, error) {
	if v == nil || v.Plan.PreparationRequest == nil || v.Plan.Template.Workbench != "research" || (v.Status != "approved" && v.Status != "running" && v.Status != "blocked") {
		return nil, fmt.Errorf("流程不在待执行状态")
	}
	if err := s.checkResearchScope(v); err != nil {
		return nil, err
	}
	step, n := researchNextNode(v)
	if n == nil {
		return v, nil
	}
	if n.Kind == "verify" {
		proof, err := s.preparationVerification(v, *n)
		if err != nil {
			return nil, err
		}
		if err = s.advanceOperation(proof, &proof.Steps[indexDeliveryStep(proof, n.ID)], *n, actor); err != nil {
			return nil, err
		}
		if proof.Status != "failed" {
			researchNextState(proof)
			if err = s.bindResearchPreparedArtifacts(proof); err != nil {
				return nil, err
			}
			if err = s.saveDeliveryRun(proof); err != nil {
				return nil, err
			}
		}
		return proof, nil
	}
	if n.Kind != "code-prepare" && n.Kind != "code-verify" {
		return nil, fmt.Errorf("当前节点须由指定人员处理")
	}
	if step.Detail != "" {
		p := s.CodePreparation(step.Detail)
		if p == nil {
			return nil, fmt.Errorf("原代码任务不存在，禁止重新执行")
		}
		if p.State == "canceled" {
			step.Status, v.Status, step.EndedAt = "canceled", "canceled", time.Now().Unix()
			return v, s.saveDeliveryRun(v)
		}
		if p.State == "ready" {
			step.Status = "success"
			step.EndedAt = time.Now().Unix()
			researchNextState(v)
			if err := s.bindResearchPreparedArtifacts(v); err != nil {
				return nil, err
			}
			if err := s.saveDeliveryRun(v); err != nil {
				return nil, err
			}
			return v, nil
		}
		return v, nil
	}
	if step.Status == "executing" {
		return nil, fmt.Errorf("代码节点曾中断，请核对准备任务，禁止重复执行")
	}
	step.Status = "executing"
	step.StartedAt = time.Now().Unix()
	v.Status = "running"
	if err := s.saveDeliveryRun(v); err != nil {
		return nil, err
	}
	ids := []int64{}
	for _, c := range v.Plan.Scope.Changes {
		if c.Kind == "code" {
			if _, ok := n.Policies[c.Service]; ok {
				ids = append(ids, c.ID)
			}
		}
	}
	if len(ids) == 0 {
		step.Status = "failed"
		v.Status = "failed"
		step.Detail = "代码节点没有匹配的代码变更"
		s.saveDeliveryRun(v)
		return nil, fmt.Errorf("代码节点没有匹配变更")
	}
	req := *v.Plan.PreparationRequest
	var p *CodePreparation
	var err error
	if batch == nil {
		p, err = s.previewCodePreparation(ctx, CodePreparationRequest{BuildConcurrency: n.BuildConcurrency, Delivery: req, Policies: n.Policies, VerifyOnly: n.Kind == "code-verify"}, actor)
	} else {
		p, err = s.createFrozenPublicationCandidate(v, n, actor, batch)
	}
	if err != nil {
		step.Status = "failed"
		step.Detail = err.Error()
		v.Status = "failed"
		s.saveDeliveryRun(v)
		return nil, err
	}
	if v.Plan.PreparationRequest != nil {
		p.PreparationRunID = v.ID
		if err := s.saveCodePreparation(p); err != nil {
			return nil, err
		}
	}
	step.Detail = p.ID
	step.Status = "running"
	if err = s.saveDeliveryRun(v); err != nil {
		return nil, err
	}
	// Frozen sources must be explicitly confirmed using the code preparation API.
	return v, nil
}

type ResearchTask struct {
	WorkLoopID        string `json:"work_loop_id,omitempty"`
	AttentionKey      string `json:"attention_key,omitempty"`
	AttentionEvent    string `json:"attention_event,omitempty"`
	AttentionState    string `json:"attention_state,omitempty"`
	AttentionRevision int64  `json:"attention_revision,omitempty"`
	AttentionReason   string `json:"attention_reason,omitempty"`
	AttentionAt       int64  `json:"attention_at,omitempty"`
	CanIgnore         bool   `json:"can_ignore,omitempty"`
	CanArchive        bool   `json:"can_archive,omitempty"`
	CanRestore        bool   `json:"can_restore,omitempty"`

	ResolutionAction string   `json:"resolution_action,omitempty"`
	ResolutionDigest string   `json:"resolution_digest,omitempty"`
	Revision         int64    `json:"revision,omitempty"`
	EventDigest      string   `json:"event_digest,omitempty"`
	Stage            string   `json:"stage,omitempty"`
	Assignees        []string `json:"assignees,omitempty"`
	NextAction       string   `json:"next_action,omitempty"`
	BlockedReason    string   `json:"blocked_reason,omitempty"`
	UpdatedAt        int64    `json:"updated_at,omitempty"`
	Services         []string `json:"services,omitempty"`
	Progress         string   `json:"progress,omitempty"`
	ReadOnly         bool     `json:"read_only,omitempty"`
	WorkItemID       int64    `json:"work_item_id,omitempty"`
	OperationID      string   `json:"operation_id,omitempty"`
	SourceKind       string   `json:"source_kind,omitempty"`
	Service          string   `json:"service,omitempty"`
	Workbench        string   `json:"workbench,omitempty"`
	ID               string   `json:"id"`
	RunID            int64    `json:"run_id"`
	RunNumber        int64    `json:"run_number,omitempty"`
	NodeID           string   `json:"node_id"`
	Kind             string   `json:"kind"`
	Title            string   `json:"title"`
	Version          string   `json:"version"`
	Environment      string   `json:"environment"`
	Digest           string   `json:"digest"`
	Status           string   `json:"status"`
	Owner            string   `json:"owner"`
	At               int64    `json:"at"`
	Instructions     string   `json:"instructions"`
	PreparationID    string   `json:"preparation_id,omitempty"`
}

// Finishing an already-started code job may complete its workflow node and
// generate the next human task. It never starts the next code/deployment node.
func (s *Store) reconcileResearchPreparation(preparationID string) {
	rows, err := s.db.Query(deliveryRunSelect + ` WHERE status IN ('running','blocked')`)
	if err != nil {
		return
	}
	runs := []*DeliveryRun{}
	for rows.Next() {
		v, e := scanDeliveryRun(rows)
		if e == nil && v.Plan.Template.Workbench == "research" {
			step, node := researchNextNode(v)
			if step != nil && node != nil && step.Detail == preparationID && (node.Kind == "code-prepare" || node.Kind == "code-verify") {
				runs = append(runs, v)
			}
		}
	}
	rows.Close()
	for _, run := range runs {
		// Cancellation closes the old preparation even after its source scope drifted.
		// This branch changes only metadata and never advances or replays a node.
		if p := s.CodePreparation(preparationID); p != nil && p.State == "canceled" && p.Owner == run.Owner && p.PreparationRunID == run.ID {
			step, _ := researchNextNode(run)
			if step != nil && step.Detail == p.ID {
				step.Status = "canceled"
				step.EndedAt = time.Now().Unix()
				run.Status = "canceled"
				if e := s.saveDeliveryRun(run); e != nil {
					s.Audit(run.Owner, "preparation-cancel-pending", "delivery-run", fmt.Sprint(run.ID), map[string]string{"error": e.Error()})
				}
				continue
			}
		}
		// Persist only the linked status projection, retaining scope, reviews,
		// task ID and revision CAS. Reading the inbox never invokes this hook.
		if s.projectResearchCodeState(run) {
			if err = s.saveDeliveryRun(run); err != nil {
				continue
			}
		}
		if run.Status == "blocked" {
			continue
		}
		if _, err = s.AdvanceResearchFlow(context.Background(), run.ID, run.Owner, nil); err != nil {
			s.Audit(run.Owner, "research-code-result-pending", "delivery-run", fmt.Sprint(run.ID), map[string]string{"preparation": preparationID, "error": err.Error()})
		}
	}
}

func (s *Store) ResearchTasks(actor, view string) ([]ResearchTask, error) {
	rows, err := s.db.Query(deliveryRunSelect + ` ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	runs := []*DeliveryRun{}
	for rows.Next() {
		v, e := scanDeliveryRun(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		runs = append(runs, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []ResearchTask{}
	for _, v := range runs {
		s.projectResearchCodeState(v)
		var current string
		if v.Plan.Template.Workbench == "research" && v.Plan.PreparationRequest == nil {
			continue
		}
		if v.Plan.Template.Workbench == "research" {
			_, n := researchNextNode(v)
			if n != nil {
				current = n.ID
			}
		} else if v.Status == "pending_approval" {
			for _, step := range v.Steps {
				if step.Kind == "approval" && step.Status != "success" {
					current = step.NodeID
					break
				}
			}
		} else if v.Status == "approved" || v.Status == "running" {
			for _, step := range v.Steps {
				if step.Status != "success" {
					current = step.NodeID
					break
				}
			}
		}
		for _, step := range v.Steps {
			var n *DeliveryNode
			for i := range v.Plan.Nodes {
				if v.Plan.Nodes[i].ID == step.NodeID {
					n = &v.Plan.Nodes[i]
					break
				}
			}
			if n == nil {
				continue
			}
			mine := namedApprover(n, actor)
			if n.Kind == "manual" && len(n.Approvers) == 0 {
				mine = v.Owner == actor
			}
			handled := false
			for _, review := range step.Reviews {
				handled = handled || review.Actor == actor
			}
			complete := handled
			if v.Plan.Template.Workbench == "research" && n.Kind == "manual" {
				complete = researchPersonComplete(&step, n, actor)
			}
			blockedCode := (n.Kind == "code-prepare" || n.Kind == "code-verify") && v.Owner == actor && step.NodeID == current && step.Detail != ""
			kind := n.Kind
			if blockedCode {
				p := s.CodePreparation(step.Detail)
				blockedCode = p != nil && (p.State == "conflict" || p.State == "failed" || p.State == "uncertain" || ((p.State == "merging" || p.State == "building") && p.LeaseUntil < time.Now().Unix()))
				kind = "code-conflict"
			}
			if view == "done" {
				if !handled {
					continue
				}
			} else {
				if v.Status == "rejected" || v.Status == "success" || v.Status == "canceled" || step.NodeID != current || complete || (!blockedCode && (!mine || (n.Kind != "manual" && n.Kind != "approval" && !(n.Kind == "verify" && n.Verification != nil && n.Verification.Mode == "manual")))) {
					continue
				}
				if n.Kind == "approval" && actor == v.Owner {
					continue
				}
			}
			version := ""
			if v.Plan.Version != nil {
				version = v.Plan.Version.Version
			}
			out = append(out, ResearchTask{ID: fmt.Sprintf("%d:%s", v.ID, step.NodeID), RunID: v.ID, NodeID: step.NodeID, Kind: kind, Title: step.Name, Version: version, Environment: v.Plan.Environment.DisplayName, Digest: v.Digest, Status: step.Status, Owner: v.Owner, At: v.CreatedAt, Instructions: n.Instructions, PreparationID: step.Detail})
		}
		if len(v.Steps) > 0 && !hasApprovalNodes(&v.Plan) && v.Status == "pending_approval" && actor != v.Owner && allowedApprover(v.Plan.Environment.Approvers, actor) && view != "done" {
			out = append(out, ResearchTask{ID: fmt.Sprintf("%d:environment", v.ID), RunID: v.ID, NodeID: "environment", Kind: "approval", Title: "环境发布审批", Digest: v.Digest, Owner: v.Owner, At: v.CreatedAt, Environment: v.Plan.Environment.DisplayName})
		}
	}
	return out, nil
}

func researchPersonComplete(step *DeliveryStep, n *DeliveryNode, actor string) bool {
	done := map[int64]bool{}
	signed := false
	for _, review := range step.Reviews {
		if review.Actor == actor && review.Approved {
			signed = true
			for _, id := range review.WorkItemIDs {
				done[id] = true
			}
		}
	}
	ids := n.AssignedWorkItems[actor]
	if n.Kind != "manual" || len(ids) == 0 {
		return signed
	}
	for _, id := range ids {
		if !done[id] {
			return false
		}
	}
	return true
}
