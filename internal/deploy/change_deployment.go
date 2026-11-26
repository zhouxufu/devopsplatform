package deploy

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Both entry points bind the user's confirmation to these values. A future
// artifact is identified by the original preparation's verified build receipt.
type changeDeploymentGrant struct {
	Batch                *publicationScope `json:"publication_scope,omitempty"`
	MarkReadyDraftDigest string            `json:"mark_ready_draft_digest,omitempty"`
	Title                string            `json:"title"`
	Request              DeliveryRequest   `json:"request"`
	SourceBranch         string            `json:"source_branch"`
	SourceCommit         string            `json:"source_commit"`
	TargetBranch         string            `json:"target_branch"`
	TargetCommit         string            `json:"target_commit"`
	GitIdentity          string            `json:"git_identity"`
	ScopeDigest          string            `json:"scope_digest"`
	TemplateDigest       string            `json:"template_digest"`
	PreparationSteps     []DeliveryNode    `json:"preparation_steps"`
	Steps                []DeliveryNode    `json:"release_steps"`
}

func (s *Store) previewServiceDeploymentRequest(r *http.Request, m *ReleaseManager, spec ServiceDeploymentSpec) (*ServiceDeploymentGrant, error) {
	if spec.Delivery != nil {
		return s.previewPublicationScope(r, m, spec)
	}
	// Older MCP clients always send build plus a branch. Resolve that input before
	// presenting the grant, so a feature branch gets the same complete preview.
	resolveBuildBranch := spec.Mode == "build" && spec.Branch != "" && spec.ArtifactID == 0 && spec.ChangeID == 0 && spec.PreparationID == 0
	if spec.Mode == "" || spec.Mode == "auto" || resolveBuildBranch {
		switch {
		case spec.ChangeID > 0 || spec.PreparationID > 0:
			spec.Mode = "change"
		case spec.ArtifactID > 0:
			spec.Mode = "artifact"
		default:
			spec.Mode = "build"
			if spec.Branch != "" {
				_, _, policy, err := s.serviceDeploymentTerms(spec)
				if err != nil {
					return nil, err
				}
				target := policy["code_policy"].(CodePolicy).TargetBranch
				if target != "" && target != spec.Branch {
					var id int64
					var count int
					err = s.db.QueryRow(`SELECT COUNT(*),COALESCE(MIN(id),0) FROM changes WHERE kind='code' AND status=? AND service=? AND reference=? AND NOT EXISTS (SELECT 1 FROM work_item_changes WHERE change_id=changes.id)`, ChangeReady, spec.Service, spec.Branch).Scan(&count, &id)
					if err != nil {
						return nil, err
					}
					if count != 1 {
						return nil, fmt.Errorf("功能分支 %s 匹配 %d 项已就绪的独立代码变更，请按变更编号选择本次范围", spec.Branch, count)
					}
					spec.Mode, spec.ChangeID, spec.Branch = "change", id, ""
				}
			}
		}
	}
	if spec.Mode != "change" {
		return s.previewServiceDeployment(r.Context(), m, spec)
	}
	if spec.ChangeID <= 0 || spec.PreparationID < 0 || spec.Branch != "" || spec.ArtifactID != 0 || len(spec.Reason) > 2000 {
		return nil, fmt.Errorf("请选择独立代码变更（原因最多 2000 字节），分支和制品由本次变更确定")
	}
	env := s.GetEnvironment(spec.Environment)
	if env == nil || (env.Purpose != "development" && env.Purpose != "test") {
		return nil, fmt.Errorf("整次变更发布仅适用于开发／测试环境")
	}
	if env.Protected && strings.TrimSpace(spec.Reason) == "" {
		return nil, fmt.Errorf("受保护环境须填写发布原因")
	}
	c := s.GetChange(spec.ChangeID)
	if c != nil && c.Service == spec.Service && c.Kind == "code" && len(c.WorkItems) > 0 {
		req := DeliveryRequest{SourceType: "work_items", SelectedChangeIDs: []int64{c.ID}, Environment: spec.Environment, Reason: spec.Reason}
		for _, w := range c.WorkItems {
			req.WorkItemIDs = append(req.WorkItemIDs, w.ID)
		}
		slices.Sort(req.WorkItemIDs)
		spec.Delivery = &req
		return s.previewPublicationScope(r, m, spec)
	}
	if c == nil || c.Service != spec.Service || c.Kind != "code" || (c.Status != ChangeReady && c.Status != ChangeDraft) {
		return nil, fmt.Errorf("需要当前应用的独立代码草稿或就绪变更；关联需求或 Bug 请从原工作项发布")
	}
	config, targetDigest, policy, e := s.serviceDeploymentTerms(spec)
	if e != nil {
		return nil, e
	}
	target, e := m.ValidateTarget(spec.Service, spec.Environment, spec.Reason)
	if e != nil {
		return nil, e
	}
	ep, e := s.deliveryPolicy(spec.Environment)
	if e != nil {
		return nil, e
	}
	req := DeliveryRequest{StrategyKind: "custom", TemplateID: ep.WorkflowTemplateID, SourceType: "changes", SourceChangeIDs: []int64{c.ID}, Environment: spec.Environment, Reason: spec.Reason, ArtifactSources: map[string]string{c.Service: "build"}}
	if spec.PreparationID == 0 {
		id, err := s.pendingChangePreparation(deliveryActor(r), spec)
		if err != nil {
			return nil, err
		}
		spec.PreparationID = id
	}
	var old *DeliveryRun
	if spec.PreparationID > 0 {
		old = s.DeliveryRun(spec.PreparationID)
		if old == nil || old.Owner != deliveryActor(r) || old.Plan.PreparationRequest == nil || old.Plan.Scope == nil || old.Plan.Scope.Type != "changes" || len(old.Plan.Scope.Changes) != 1 || old.Plan.Scope.Changes[0].ID != c.ID || old.Plan.Environment.Name != spec.Environment || old.Plan.PreparationRequest.TemplateID != ep.WorkflowTemplateID {
			return nil, fmt.Errorf("原准备单身份、环境或范围不匹配")
		}
		if old.Status == "failed" || old.Status == "rejected" || old.Status == "canceled" {
			return nil, fmt.Errorf("原准备单已经终止")
		}
		req = *old.Plan.PreparationRequest
	}
	previewReq := req
	if c.Status == ChangeDraft {
		if old != nil {
			return nil, fmt.Errorf("变更已回到草稿，请先核对原准备单")
		}
		previewReq.readyDraftDigest = digestJSON(c)
		if _, e = s.maintainChange(c.ID, changeMaintenanceRequest{Action: "ready", Service: c.Service, Digest: previewReq.readyDraftDigest, Reason: spec.Reason}, deliveryActor(r), false); e != nil {
			return nil, e
		}
	}
	draft, e := s.DraftDelivery(previewReq)
	if e != nil {
		return nil, e
	}
	if len(draft.Blockers) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(draft.Blockers, "；"))
	}
	var enabled []string
	if json.Unmarshal([]byte(r.Header.Get("X-Reviewer-Directory")), &enabled) != nil || !slices.Contains(enabled, deliveryActor(r)) {
		return nil, fmt.Errorf("当前发布人员目录不可用")
	}
	// Independent changes have no upstream work-item identities to resolve.
	prep, e := s.ResearchFlowPlan(previewReq, nil, enabled, deliveryActor(r))
	if e != nil {
		return nil, e
	}
	if len(prep.Blockers) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(prep.Blockers, "；"))
	}
	if old != nil && (researchScopeDigest(old.Plan.Scope) != researchScopeDigest(prep.Scope) || digestJSON(old.Plan.Nodes) != digestJSON(prep.Nodes)) {
		return nil, fmt.Errorf("原准备单的变更或人员范围已经改变")
	}
	var cp CodePolicy
	codeNodes := 0
	for _, n := range prep.Nodes {
		if n.Kind == "code-prepare" || n.Kind == "code-verify" {
			codeNodes++
			cp = n.Policies[c.Service]
		}
	}
	if codeNodes != 1 || cp.Mode != "integration" || !validPreparationBranch(cp.TargetBranch) || !validPreparationBranch(c.Reference) {
		return nil, fmt.Errorf("当前绑定流程需包含一个使用公共分支的代码准备节点")
	}
	steps, e := changeReleaseSteps(draft.Nodes, c.Service)
	if e != nil {
		return nil, e
	}
	repository, conn, e := s.gitDiscoverySource(GitBranchesRequest{Service: c.Service})
	if e != nil {
		return nil, e
	}
	refs, e := discoverGitBranches(r.Context(), repository, conn)
	if e != nil {
		return nil, e
	}
	g := &changeDeploymentGrant{MarkReadyDraftDigest: previewReq.readyDraftDigest, Title: c.Title, Request: req, SourceBranch: c.Reference, TargetBranch: cp.TargetBranch, GitIdentity: preparationGitIdentity(repository, conn), ScopeDigest: researchScopeDigest(prep.Scope), TemplateDigest: digestJSON(draft.Template), PreparationSteps: prep.Nodes, Steps: steps}
	for _, ref := range refs.Branches {
		if ref.Name == g.SourceBranch {
			g.SourceCommit = ref.Commit
		}
		if ref.Name == g.TargetBranch {
			g.TargetCommit = ref.Commit
		}
	}
	if !fullBuildCommit.MatchString(g.SourceCommit) || !fullBuildCommit.MatchString(g.TargetCommit) {
		return nil, fmt.Errorf("无法冻结源分支或公共分支完整提交")
	}
	if old != nil {
		for _, st := range old.Steps {
			if (st.Kind == "code-prepare" || st.Kind == "code-verify") && st.Detail != "" {
				p := s.CodePreparation(st.Detail)
				if p == nil || p.State != "preview" || !g.matchesCandidate(p, spec, old.ID) {
					return nil, fmt.Errorf("原候选与当前分支不一致或已启动，请核对原执行记录")
				}
			}
		}
	}
	grant := &ServiceDeploymentGrant{Spec: spec, Change: g, ConfigurationDigest: config, TargetDigest: targetDigest, Policy: policy, Targets: target.ExecutionTargets(), TargetType: target.TargetType, Strategy: target.Strategy, PauseBetweenBatches: target.PauseBetweenBatches}
	grant.Digest = digestJSON(grant)
	return grant, nil
}

// Recover an existing preparation when the caller knows only the change ID.
func (s *Store) pendingChangePreparation(owner string, spec ServiceDeploymentSpec) (int64, error) {
	rows, err := s.db.Query(`SELECT id,plan FROM delivery_runs WHERE owner=? AND status IN ('waiting_confirmation','approved','pending_approval','running','blocked') ORDER BY id DESC`, owner)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var matched int64
	for rows.Next() {
		var id int64
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return 0, err
		}
		var p DeliveryPlan
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			return 0, err
		}
		if p.PreparationRequest == nil || p.Environment.Name != spec.Environment || p.Scope == nil || p.Scope.Type != "changes" || len(p.Scope.Changes) != 1 || p.Scope.Changes[0].ID != spec.ChangeID {
			continue
		}
		if matched != 0 {
			return 0, fmt.Errorf("变更已有多张准备单，请明确接续 #%d 或 #%d", matched, id)
		}
		matched = id
	}
	return matched, rows.Err()
}

func changeReleaseSteps(nodes []DeliveryNode, service string) ([]DeliveryNode, error) {
	var steps []DeliveryNode
	deploys := 0
	for _, n := range nodes {
		if n.Service != "" && n.Service != service {
			return nil, fmt.Errorf("发布节点 %s 包含其他应用", n.Name)
		}
		switch n.Kind {
		case "deploy":
			deploys++
		case "approval", "manual", "verify":
		case "mysql", "nacos", "redis", "data-sync":
			if len(n.ChangeIDs) == 0 {
				continue
			}
			fallthrough
		default:
			return nil, fmt.Errorf("步骤 %s 包含独立代码变更以外的操作，请使用完整交付流程", n.Name)
		}
		steps = append(steps, n)
	}
	if deploys != 1 {
		return nil, fmt.Errorf("发布流程需要一个匹配当前应用的部署节点")
	}
	return steps, validateNodeDependencies(steps)
}
func (g *changeDeploymentGrant) matchesCandidate(p *CodePreparation, spec ServiceDeploymentSpec, prepID int64) bool {
	if g.Batch != nil {
		return p != nil && p.PreparationRunID == prepID && p.Request.Environment == spec.Environment && !p.Protected && candidateDigest(p) == g.Batch.CandidateDigest
	}
	if p == nil || p.PreparationRunID != prepID || p.Request.Environment != spec.Environment || p.Protected || len(p.Repositories) != 1 || len(p.Builds) != 1 || p.Builds[0].Service != spec.Service {
		return false
	}
	repo := p.Repositories[0]
	return repo.Service == spec.Service && repo.ConnectionDigest == g.GitIdentity && repo.TargetBranch == g.TargetBranch && repo.TargetCommit == g.TargetCommit && repo.BaseBranch == g.TargetBranch && repo.BaseCommit == g.TargetCommit && len(repo.Sources) == 1 && repo.Sources[0].Branch == g.SourceBranch && repo.Sources[0].Commit == g.SourceCommit && slices.Equal(repo.Sources[0].Changes, []int64{spec.ChangeID})
}

func changeDeploymentClaim(spec ServiceDeploymentSpec) string {
	return "change_deployment_claim:" + digestJSON([]any{spec.Service, spec.Environment, spec.ChangeID})
}

// An operator can still handle approval and verification on the original run.
// Creating another final execution from that preparation must not race its grant.
func checkChangeDeliveryOwner(tx *deployTx, req DeliveryRequest, p *DeliveryPlan) error {
	if req.PreparationRunID == 0 || p.Scope == nil {
		return nil
	}
	if req.changeDeploymentID != "" {
		if err := checkPreparationUnpublished(tx, req.PreparationRunID); err != nil {
			return err
		}
	}
	for _, c := range p.Scope.Changes {
		if err := checkPublicationClaim(tx, req, c); err != nil {
			return err
		}
	}
	return nil
}
func checkPublicationClaim(tx *deployTx, req DeliveryRequest, c Change) error {
	key := changeDeploymentClaim(ServiceDeploymentSpec{Service: c.Service, Environment: req.Environment, ChangeID: c.ID})
	var raw, id string
	err := tx.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) && req.changeDeploymentID == "" {
		return nil
	}
	if err != nil {
		return err
	}
	if req.changeDeploymentID != "" {
		if id != req.changeDeploymentID {
			return fmt.Errorf("整次发布授权归属已变化")
		}
		return nil
	}
	if err = tx.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, "service_deployment:"+id).Scan(&raw); err != nil {
		return err
	}
	var task ServiceDeployment
	if err = json.Unmarshal([]byte(raw), &task); err != nil {
		return err
	}
	if task.State == "running" || task.PreparationID == req.PreparationRunID && (task.DeliveryID > 0 || task.Stage == "create_delivery") {
		return fmt.Errorf("当前范围已由整次发布任务 %s 接管，请查看原执行单", id)
	}
	return nil
}

// Atomically claim this source/target pair across pages and MCP sessions.
func (s *Store) createChangeDeployment(t *ServiceDeployment) error {
	spec := t.Grant.Spec
	keys := []string{changeDeploymentClaim(spec)}
	if b := t.Grant.Change.Batch; b != nil {
		keys = nil
		for _, c := range b.Changes {
			keys = append(keys, changeDeploymentClaim(ServiceDeploymentSpec{Environment: spec.Environment, ChangeID: c.ID, Service: c.Service}))
		}
		slices.Sort(keys)
	}
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for _, key := range keys {
		prior, err := claimPublicationChange(tx, key, t)
		if err != nil {
			return err
		}
		if prior != nil {
			*t = *prior
			return nil
		}
	}
	if digest := t.Grant.Change.MarkReadyDraftDigest; digest != "" {
		_, e = s.maintainChangeTx(tx, spec.ChangeID, changeMaintenanceRequest{Action: "ready", Service: spec.Service, Digest: digest, Reason: spec.Reason}, t.Owner, true)
		if e != nil {
			return e
		}
	}
	t.Revision = 1
	t.UpdatedAt = time.Now().Unix()
	b, e := json.Marshal(t)
	if e != nil {
		return e
	}
	if _, e = tx.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?)`, "service_deployment:"+t.ID, string(b)); e != nil {
		return e
	}
	return tx.Commit()
}

func claimPublicationChange(tx *deployTx, key string, t *ServiceDeployment) (*ServiceDeployment, error) {
	spec := t.Grant.Spec
	var e error
	var previous string
	err := tx.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, key).Scan(&previous)
	switch {
	case err == nil:
		var raw string
		var prior ServiceDeployment
		if e = tx.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, "service_deployment:"+previous).Scan(&raw); e != nil {
			return nil, e
		}
		if e = json.Unmarshal([]byte(raw), &prior); e != nil {
			return nil, e
		}
		if prior.Owner != t.Owner && prior.State == "running" {
			return nil, fmt.Errorf("本次变更有正在进行的发布，请在原执行单查看进度")
		}
		same := prior.Owner == t.Owner && prior.Authority == t.Authority && prior.AuthorityKeyID == t.AuthorityKeyID && prior.Grant.Digest == t.Grant.Digest
		if same && (prior.State == "running" || prior.State == "success") {
			return &prior, nil
		}
		if prior.State == "running" || prior.LeaseUntil != 0 || prior.State == "blocked" && prior.DeliveryID != 0 {
			return nil, fmt.Errorf("已有发布任务 %s，请核对原执行结果", previous)
		}
		if prior.PreparationID != 0 && prior.State != "success" && spec.PreparationID != prior.PreparationID {
			var status string
			if e = tx.QueryRow(`SELECT status FROM delivery_runs WHERE id=?`, prior.PreparationID).Scan(&status); e != nil {
				return nil, e
			}
			if status != "canceled" && status != "rejected" {
				return nil, fmt.Errorf("请核对原准备单 #%d，不能另起流程", prior.PreparationID)
			}
		}
		if prior.CodeID != "" && prior.State != "success" && spec.PreparationID == prior.PreparationID {
			var data string
			var p CodePreparation
			if e = tx.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, "code_preparation:"+prior.CodeID).Scan(&data); e != nil {
				return nil, e
			}
			if json.Unmarshal([]byte(data), &p) != nil || p.State != "preview" {
				return nil, fmt.Errorf("原代码任务已有执行记录，请先核对")
			}
		}
		updated, err := tx.Exec(`UPDATE deploy_meta SET value=? WHERE key=? AND value=?`, t.ID, key, previous)
		if err != nil {
			return nil, err
		}
		n, err := updated.RowsAffected()
		if err != nil || n != 1 {
			return nil, ErrRunConflict
		}
	case errors.Is(err, sql.ErrNoRows):
		if _, e = tx.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?)`, key, t.ID); e != nil {
			return nil, fmt.Errorf("同范围发布已被提交，请查看原任务：%w", e)
		}
	default:
		return nil, err
	}
	return nil, nil
}

func (s *Store) advanceChangeDeployment(mux *http.ServeMux, r *http.Request, t *ServiceDeployment, pr *PipelineRunner, m *ReleaseManager) error {
	if t.State != "running" {
		return nil
	}
	now := time.Now().Unix()
	if t.LeaseUntil > now {
		return nil
	}
	report := func(state, stage, msg string) error {
		t.State, t.Stage, t.Message, t.LeaseUntil = state, stage, msg, 0
		snapshot, err := json.Marshal(t)
		if err != nil {
			return err
		}
		if string(snapshot) == t.raw {
			return nil
		}
		return s.saveServiceDeployment(t)
	}
	block := func(e error) error { return report("blocked", t.Stage, e.Error()) }
	// A coordinator may die after a child accepted a write. Only reconcile the
	// original receipt; a missing receipt must never trigger a second business write.
	if t.LeaseUntil != 0 {
		return s.recoverChangeDispatch(t)
	}
	spec, g := t.Grant.Spec, t.Grant.Change
	if t.ExpiresAt > 0 && t.ExpiresAt <= now {
		return block(fmt.Errorf("发布授权已过期，请核对原任务"))
	}
	if g.Batch != nil {
		for _, app := range g.Batch.Applications {
			if app.Mode == "resource" {
				continue
			}
			appSpec := spec
			appSpec.Service = app.Service
			config, dest, _, err := s.serviceDeploymentTerms(appSpec)
			if err != nil {
				return block(err)
			}
			if config != app.Config || dest != app.Destination {
				return block(fmt.Errorf("应用 %s 配置或部署目标已变化", app.Service))
			}
		}
	} else {
		config, dest, _, e := s.serviceDeploymentTerms(spec)
		if e != nil {
			return block(e)
		}
		if config != t.Grant.ConfigurationDigest || dest != t.Grant.TargetDigest {
			return block(fmt.Errorf("应用构建配置、环境策略或部署目标与确认时不同"))
		}
	}
	draft, e := s.DraftDelivery(g.Request)
	if e != nil {
		return block(e)
	}
	if len(draft.Blockers) > 0 {
		return block(fmt.Errorf("%s", strings.Join(draft.Blockers, "；")))
	}
	if researchScopeDigest(draft.Scope) != g.ScopeDigest || digestJSON(draft.Template) != g.TemplateDigest {
		return block(fmt.Errorf("变更内容或绑定流程与确认时不同"))
	}
	dispatch := func(stage string, rev int64, fn func() error) error {
		t.Stage, t.Message, t.LeaseUntil, t.ChildRevision = stage, "正在推进原发布", time.Now().Add(3*time.Minute).Unix(), rev
		if err := s.saveServiceDeployment(t); err != nil {
			return err
		}
		if err := fn(); err != nil {
			return block(err)
		}
		return report("running", stage, "已取得执行回执，正在核对进度")
	}
	if t.PreparationID == 0 {
		return dispatch("create_preparation", 0, func() error {
			req := g.Request
			req.ClientRequestID = t.ID
			var enabled []string
			if json.Unmarshal([]byte(r.Header.Get("X-Reviewer-Directory")), &enabled) != nil {
				return fmt.Errorf("人员目录不可用")
			}
			var accounts map[string]string
			if g.Batch != nil {
				var err error
				accounts, enabled, err = s.researchPeople(r.Context(), r)
				if err != nil {
					return err
				}
			}
			prep, e := s.ResearchFlowPlan(req, accounts, enabled, t.Owner)
			if e != nil {
				return e
			}
			if digestJSON(prep.Nodes) != digestJSON(g.PreparationSteps) || researchScopeDigest(prep.Scope) != g.ScopeDigest {
				return fmt.Errorf("准备范围或人员已变化")
			}
			run, e := s.CreateResearchFlow(req, accounts, enabled, t.Owner)
			if e == nil {
				t.PreparationID = run.ID
			}
			return e
		})
	}
	prep := s.deliveryRunLedger(t.PreparationID)
	if prep == nil || prep.Owner != t.Owner || prep.Plan.Scope == nil || prep.Plan.Environment.Name != spec.Environment || digestJSON(prep.Plan.Nodes) != digestJSON(g.PreparationSteps) || researchScopeDigest(prep.Plan.Scope) != g.ScopeDigest {
		return block(fmt.Errorf("原准备单与冻结范围不一致"))
	}
	if prep.Status != "success" {
		step, node := researchNextNode(prep)
		if step == nil || node == nil || prep.Status == "failed" || prep.Status == "canceled" || prep.Status == "rejected" {
			return block(fmt.Errorf("准备单 #%d：%s", prep.ID, prep.Status))
		}
		if node.Kind == "manual" {
			if (node.AssigneeSource != "delivery_initiator" && node.AssigneeSource != "change_submitters") || !slices.Contains(node.Approvers, t.Owner) || researchPersonComplete(step, node, t.Owner) {
				return report("running", "waiting_review", "等待指定人员处理："+node.Name)
			}
			return dispatch("confirm_scope", prep.Revision, func() error {
				return loopCall(mux, r, fmt.Sprintf("/api/delivery/mcp/runs/%d/confirm-scope", prep.ID), map[string]any{"revision": prep.Revision, "digest": prep.Digest, "node_id": node.ID, "confirmation_id": t.ID, "comment": "整次发布授权 " + t.ID, "work_item_ids": node.AssignedWorkItems[t.Owner]}, nil)
			})
		}
		if node.Kind == "approval" || node.Kind == "verify" && node.Verification != nil && node.Verification.Mode == "manual" {
			return report("running", "waiting_review", "等待指定人员处理："+node.Name)
		}
		if (node.Kind == "code-prepare" || node.Kind == "code-verify") && step.Detail != "" {
			p := s.CodePreparation(step.Detail)
			if !g.matchesCandidate(p, spec, prep.ID) || p.Owner != t.Owner {
				if p != nil && p.Owner == t.Owner && len(p.Repositories) == 1 && len(p.Repositories[0].Sources) == 1 {
					repo := p.Repositories[0]
					return block(fmt.Errorf("候选与授权不一致：源 %s %s → %s；公共分支 %s %s → %s。请核对准备单 #%d", g.SourceBranch, g.SourceCommit, repo.Sources[0].Commit, g.TargetBranch, g.TargetCommit, repo.TargetCommit, prep.ID))
				}
				return block(fmt.Errorf("原候选的应用、仓库或变更范围已变化，请核对准备单 #%d", prep.ID))
			}
			t.CodeID = p.ID
			switch p.State {
			case "preview":
				return dispatch("merge_build", p.Revision, func() error {
					return loopCall(mux, r, "/api/delivery/mcp/code-preparations/"+p.ID+"/start", map[string]any{"confirm": true, "expected_revision": p.Revision}, nil)
				})
			case "merging", "building":
				if p.LeaseUntil <= now {
					return block(fmt.Errorf("原合并／构建租约已过期，请核对任务 %s", p.ID))
				}
				t.RunID = p.Builds[0].RunID
				return report("running", p.State, p.Message)
			case "conflict":
				return report("running", "waiting_conflict", "请由 Agent 解决冲突；审核后通过原代码任务恢复："+p.ID)
			case "ready":
			default:
				return block(fmt.Errorf("代码准备 %s：%s", p.State, p.Message))
			}
		}
		// A finished build also reconciles its preparation in the runner. If it
		// wins the revision race, accept only the receipt for this exact code node.
		childID, nodeID := step.Detail, node.ID
		return dispatch("prepare", prep.Revision, func() error {
			_, err := s.advanceFrozenResearchFlow(r.Context(), prep, t.Owner, pr, g.Batch)
			if errors.Is(err, ErrRunConflict) && childID != "" && (node.Kind == "code-prepare" || node.Kind == "code-verify") {
				fresh := s.deliveryRunLedger(prep.ID)
				if fresh != nil && fresh.Owner == t.Owner && fresh.Digest == prep.Digest {
					for _, receipt := range fresh.Steps {
						if receipt.NodeID == nodeID && receipt.Detail == childID && receipt.Status == "success" {
							return nil
						}
					}
				}
			}
			return err
		})
	}
	if t.DeliveryID == 0 {
		var p *CodePreparation
		for _, st := range prep.Steps {
			if st.Kind == "code-prepare" || st.Kind == "code-verify" {
				p = s.CodePreparation(st.Detail)
			}
		}
		if !g.matchesCandidate(p, spec, prep.ID) || p.Owner != t.Owner || p.State != "ready" {
			return block(fmt.Errorf("缺少本次候选的构建回执"))
		}
		b := p.Builds[0]
		t.CodeID, t.RunID, t.ArtifactID = p.ID, b.RunID, b.ArtifactID
		t.BuiltCommit, t.BuiltArtifactDigest = b.Commit, b.ArtifactDigest
		req := g.Request
		req.PreparationRunID, req.CodePreparationID, req.Artifacts, req.Nodes = prep.ID, p.ID, map[string]int64{spec.Service: b.ArtifactID}, g.Steps
		if g.Batch != nil {
			req.Nodes = g.Request.Nodes
			req.Artifacts = map[string]int64{}
			for app, id := range g.Request.Artifacts {
				req.Artifacts[app] = id
			}
			for _, build := range p.Builds {
				req.Artifacts[build.Service] = build.ArtifactID
			}
			t.Artifacts = req.Artifacts
		}
		req.OrderConfirmed, req.CodeConfirmed = true, true
		req.reviewerDirectory = r.Header.Get("X-Reviewer-Directory")
		plan, secrets, err := s.ResolveDelivery(req, m)
		if err != nil {
			return block(err)
		}
		if len(plan.Blockers) > 0 {
			return block(fmt.Errorf("%s", strings.Join(plan.Blockers, "；")))
		}
		if g.Batch != nil {
			if publicationResourceDigest(plan) != g.Batch.ResourceDigest || !matchesPublicationArtifacts(plan, t.Artifacts) {
				return block(fmt.Errorf("资源内容、执行顺序或制品与确认范围不一致"))
			}
		} else if len(plan.Services) != 1 || plan.Services[0].Service != spec.Service || plan.Services[0].Artifact.ID != b.ArtifactID || digestJSON(plan.Nodes) != digestJSON(g.Steps) {
			return block(fmt.Errorf("最终发布计划与授权内容不一致"))
		}
		req.PreflightToken, _ = s.signDeliveryPreflight(req, plan, secrets, t.Owner)
		req.changeDeploymentID = t.ID
		return dispatch("create_delivery", 0, func() error {
			run, err := s.CreateDeliveryRun(req, t.Owner, m)
			if err == nil {
				t.DeliveryID = run.ID
			}
			return err
		})
	}
	run := s.deliveryRunLedger(t.DeliveryID)
	if run == nil || run.Owner != t.Owner || run.Plan.PreparationRunID != prep.ID || (g.Batch == nil && digestJSON(run.Plan.Nodes) != digestJSON(g.Steps)) || (g.Batch != nil && (digestJSON(publicationNodes(run.Plan.Nodes, run.Plan.ArtifactSources)) != digestJSON(publicationNodes(g.Steps, g.Request.ArtifactSources)) || !matchesPublicationArtifacts(&run.Plan, t.Artifacts))) || (g.Batch == nil && (len(run.Plan.Services) != 1 || run.Plan.Services[0].Artifact.ID != t.ArtifactID)) {
		return block(fmt.Errorf("发布执行单与本次制品不一致"))
	}
	for _, step := range run.Steps {
		if step.Kind == "deploy" && step.ReleaseID > 0 {
			t.ReleaseID = step.ReleaseID
			if g.Batch != nil {
				if t.Releases == nil {
					t.Releases = map[string]int64{}
				}
				t.Releases[step.Service] = step.ReleaseID
			}
		}
	}
	if run.Status == "pending_approval" {
		return report("running", "waiting_review", "等待发布单指定人员审批")
	}
	decision, message := deliveryControlDecision(s, run)
	switch decision {
	case "complete":
		if g.Batch != nil {
			for app, id := range t.Artifacts {
				o := s.GetReleaseOrder(t.Releases[app])
				if o == nil || o.Status != "success" || o.Environment != spec.Environment || o.Service != app || o.ArtifactID != id {
					return block(fmt.Errorf("应用 %s 缺少本次发布回执", app))
				}
			}
			return report("success", "complete", message)
		}
		order := s.GetReleaseOrder(t.ReleaseID)
		if order == nil || order.Service != spec.Service || order.Environment != spec.Environment || order.ArtifactID != t.ArtifactID || order.Status != "success" {
			return block(fmt.Errorf("缺少本次制品的发布回执"))
		}
		return report("success", "complete", message)
	case "finish":
		return report(run.Status, "delivery", message)
	case "wait", "pause":
		return report("running", "delivery", message)
	}
	if err := validateDeliveryReviewerDirectory(&run.Plan, r.Header.Get("X-Reviewer-Directory"), t.Owner); err != nil {
		return block(err)
	}
	if err := s.checkDeliveryPolicy(&run.Plan); err != nil {
		return block(err)
	}
	if control := s.DeliveryControl(run.ID); control.State != "idle" {
		return block(fmt.Errorf("执行单已有独立控制记录：%s", control.Message))
	}
	return dispatch("deliver", run.Revision, func() error {
		_, err := s.advanceDeliveryRunState(run, t.Owner, false, m, func(id int64) { go m.startNextBatch(id) }, nil)
		return err
	})
}

func (s *Store) recoverChangeDispatch(t *ServiceDeployment) error {
	recovered := false
	switch t.Stage {
	case "create_preparation":
		recovered = s.db.QueryRow(`SELECT run_id FROM research_flow_uses WHERE owner=? AND request_id=?`, t.Owner, t.ID).Scan(&t.PreparationID) == nil
	case "create_delivery":
		recovered = s.db.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, "change_delivery:"+t.ID).Scan(&t.DeliveryID) == nil
	case "merge_build":
		p := s.CodePreparation(t.CodeID)
		recovered = p != nil && p.Owner == t.Owner && p.Revision > t.ChildRevision && p.State != "preview"
	case "confirm_scope", "prepare":
		run := s.deliveryRunLedger(t.PreparationID)
		recovered = run != nil && run.Owner == t.Owner && run.Revision > t.ChildRevision
	case "deliver":
		run := s.deliveryRunLedger(t.DeliveryID)
		recovered = run != nil && run.Owner == t.Owner && run.Revision > t.ChildRevision
	}
	t.LeaseUntil = 0
	if !recovered {
		t.State = "blocked"
		t.Message = "上次操作的回执尚未核实，请核对原执行记录"
	} else {
		t.Message = "已找回原执行回执"
	}
	return s.saveServiceDeployment(t)
}

func matchesPublicationArtifacts(plan *DeliveryPlan, artifacts map[string]int64) bool {
	if len(plan.Services) == 0 || len(plan.Services) != len(artifacts) {
		return false
	}
	for _, app := range plan.Services {
		if app.Artifact.ID <= 0 || artifacts[app.Service] != app.Artifact.ID {
			return false
		}
	}
	return true
}
