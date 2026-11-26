package deploy

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
)

type publicationApp struct {
	Service          string   `json:"service"`
	Mode             string   `json:"mode"`
	Config           string   `json:"configuration_digest"`
	Destination      string   `json:"destination_digest"`
	ArtifactID       int64    `json:"artifact_id,omitempty"`
	ArtifactDigest   string   `json:"artifact_digest,omitempty"`
	ArtifactSource   string   `json:"artifact_source,omitempty"`
	ArtifactSourceID int64    `json:"artifact_source_id,omitempty"`
	Targets          []string `json:"targets"`
}
type publicationChange struct {
	ID      int64  `json:"id"`
	Service string `json:"service"`
	Kind    string `json:"kind"`
	Title   string `json:"title"`
}
type publicationScope struct {
	RequiresReview       bool                     `json:"requires_review,omitempty"`
	EnvironmentReviewers []string                 `json:"environment_reviewers,omitempty"`
	Builds               []PreparedBuild          `json:"builds"`
	ResourcePreview      []publicationDifference  `json:"resource_preview,omitempty"`
	Applications         []publicationApp         `json:"applications"`
	Changes              []publicationChange      `json:"changes"`
	Repositories         []PreparedRepository     `json:"repositories"`
	CandidateDigest      string                   `json:"candidate_digest"`
	ResourceDigest       string                   `json:"resource_digest"`
	Resources            []DeliveryResourceTarget `json:"resources,omitempty"`
	Checklist            []ReleaseChecklistStep   `json:"checklist,omitempty"`
}

func candidateDigest(p *CodePreparation) string {
	repos := slices.Clone(p.Repositories)
	for i := range repos {
		repos[i].Status = ""
		repos[i].Conflicts = nil
		repos[i].ResultCommit = ""
		repos[i].ExpectedResultCommit = ""
	}
	builds := map[string]string{}
	for _, b := range p.Builds {
		builds[b.Service] = b.ConfigurationDigest
	}
	return digestJSON([]any{repos, builds, p.ScopeDigest, p.PolicyRevision, p.VerifyOnly, p.Request.CodeModes})
}
func publicationNodes(steps []DeliveryNode, sources map[string]string) []DeliveryNode {
	// Runtime artifact fields are filled by ResolveDelivery from the verified builds.
	// Everything else, including the suite, plugin version and target, stays frozen.
	nodes := slices.Clone(steps)
	for i, node := range nodes {
		if node.Extension == nil {
			continue
		}
		bind := *node.Extension
		nodes[i].Extension = &bind
		bind.Tests = slices.Clone(bind.Tests)
		for j := range bind.Tests {
			task := &bind.Tests[j]
			if sources[task.Service] == "build" {
				task.ArtifactID, task.ArtifactDigest = 0, ""
			}
		}
		bind.Targets = slices.Clone(bind.Targets)
		for j := range bind.Targets {
			target := &bind.Targets[j]
			if sources[target.Service] == "build" {
				target.ArtifactID, target.ArtifactDigest = 0, ""
			}
		}
		bind.Documents = slices.Clone(bind.Documents)
		for j := range bind.Documents {
			doc := &bind.Documents[j]
			if sources[doc.Service] == "build" {
				doc.ArtifactID, doc.SourceRunID, doc.ArtifactDigest, doc.Commit = 0, 0, "", ""
			}
		}
	}
	return nodes
}
func publicationResourceDigest(p *DeliveryPlan) string {
	nodes := publicationNodes(p.Nodes, p.ArtifactSources)
	existing := map[string]Artifact{}
	for _, svc := range p.Services {
		if p.ArtifactSources[svc.Service] == "existing" {
			existing[svc.Service] = svc.Artifact
		}
	}
	return digestJSON([]any{existing, p.PolicyDigest, nodes, p.ResourceTargets, p.SQLPlans, publicNacosPlans(p.NacosPlans), p.RedisPlans})
}
func validatePublicationOrder(draft *DeliveryDraftPlan, nodes []DeliveryNode) error {
	base := draft.Nodes
	if nodes == nil || digestJSON(base) == digestJSON(nodes) {
		return nil
	}
	if err := validateOrderBarriers(base, nodes); err != nil {
		return err
	}
	base = slices.Clone(base)
	for i := range base {
		n := &base[i]
		if n.Kind == "mysql" && len(n.ChangeIDs) == 0 {
			for _, c := range draft.Scope.Changes {
				if c.Kind == "mysql" && (n.Service == "" || c.Service == n.Service) {
					n.ChangeIDs = append(n.ChangeIDs, c.ID)
				}
			}
		}
	}
	byID := map[string]DeliveryNode{}
	for _, n := range base {
		byID[n.ID] = n
	}
	nodes = slices.Clone(nodes)
	for i, n := range nodes {
		k := n.ID
		if n.SQLGroup != "" {
			k = n.SQLGroup
		}
		o, ok := byID[k]
		if !ok {
			return fmt.Errorf("节点不在原计划")
		}
		if o.Kind == "mysql" && n.SQLGroup == "" && len(n.ChangeIDs) == 0 {
			n.ChangeIDs = o.ChangeIDs
			nodes[i] = n
		}
		if o.Kind == "mysql" && len(o.ChangeIDs) > 0 {
			o.ID, o.Name, o.SQLGroup, o.ChangeIDs = n.ID, n.Name, n.SQLGroup, n.ChangeIDs
		}
		if digestJSON(o) != digestJSON(n) {
			return fmt.Errorf("排序不能改写节点 %s", k)
		}
	}
	if e := validateStandardOverrides(base, nodes); e != nil {
		return e
	}
	return validateNodeDependencies(nodes)
}
func matchingPublicationPreparation(run *DeliveryRun, actor string, req DeliveryRequest, p *DeliveryPlan) bool {
	return run != nil && run.Owner == actor && run.Status != "failed" && run.Status != "rejected" && run.Status != "canceled" && run.Plan.PreparationRequest != nil && run.Plan.Scope != nil && run.Plan.Environment.Name == req.Environment && req.TemplateID == run.Plan.PreparationRequest.TemplateID && run.Plan.Scene == req.Scene && digestJSON(req.CodeModes) == digestJSON(run.Plan.PreparationRequest.CodeModes) && researchScopeDigest(run.Plan.Scope) == researchScopeDigest(p.Scope) && digestJSON(run.Plan.Nodes) == digestJSON(p.Nodes)
}
func (s *Store) pendingScopePreparation(actor string, req DeliveryRequest, p *DeliveryPlan) (int64, error) {
	rows, e := s.db.Query(`SELECT id FROM delivery_runs WHERE owner=? AND json_extract(plan,'$.preparation_request.environment') IS NOT NULL AND status IN ('waiting_confirmation','approved','pending_approval','running','blocked','success') AND NOT EXISTS (SELECT 1 FROM delivery_runs final WHERE json_extract(final.plan,'$.preparation_run_id')=delivery_runs.id) ORDER BY id DESC`, actor)
	if e != nil {
		return 0, e
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return 0, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return 0, e
	}
	var found int64
	for _, id := range ids {
		if matchingPublicationPreparation(s.deliveryRunLedger(id), actor, req, p) {
			if found != 0 {
				return 0, fmt.Errorf("存在多张同范围准备单，请指定原编号")
			}
			found = id
		}
	}
	return found, nil
}
func (s *Store) previewPublicationScope(r *http.Request, m *ReleaseManager, spec ServiceDeploymentSpec) (*ServiceDeploymentGrant, error) {
	env := s.GetEnvironment(spec.Environment)
	if env == nil || env.Protected || (env.Purpose != "development" && env.Purpose != "test") {
		return nil, fmt.Errorf("整次发布仅适用于非受保护开发／测试环境")
	}
	req := *spec.Delivery
	if req.Environment != spec.Environment || spec.Environment == "" || len(spec.Reason) > 2000 || req.PreflightToken != "" || req.CodePreparationID != "" || req.PreparationRunID != 0 || req.ClientRequestID != "" || spec.ArtifactID != 0 || spec.Branch != "" {
		return nil, fmt.Errorf("交付来源或环境不一致")
	}
	req.Reason = spec.Reason
	req.CodeConfirmed = true
	req.OrderConfirmed = true
	req.reviewerDirectory = r.Header.Get("X-Reviewer-Directory")
	policy, e := s.deliveryPolicy(req.Environment)
	if e != nil {
		return nil, e
	}
	if req.TemplateID == 0 {
		req.TemplateID = policy.WorkflowTemplateID
	}
	if req.StrategyKind == "" {
		req.StrategyKind = "custom"
	}
	draft, e := s.DraftDelivery(req)
	if e != nil {
		return nil, e
	}
	if draft.Scope == nil || len(draft.Scope.Changes) == 0 {
		return nil, fmt.Errorf("请选择本次变更")
	}
	if len(draft.Blockers) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(draft.Blockers, "；"))
	}
	if e = validatePublicationOrder(draft, req.Nodes); e != nil {
		return nil, e
	}
	batch := &publicationScope{}
	apps := map[string]bool{}
	all := map[string]bool{}
	for _, c := range draft.Scope.Changes {
		if draft.Scope.Type == "changes" && len(c.WorkItems) > 0 {
			return nil, fmt.Errorf("关联变更 #%d 请通过原工作项范围发布", c.ID)
		}
		batch.Changes = append(batch.Changes, publicationChange{c.ID, c.Service, c.Kind, c.Title})
		if c.Service != "" {
			all[c.Service] = true
		}
		if c.Kind == "code" || c.Kind == "artifact" {
			apps[c.Service] = true
		}
	}
	if len(apps) == 0 {
		return nil, fmt.Errorf("没有需要构建的服务")
	}
	if spec.ChangeID != 0 && (len(batch.Changes) != 1 || batch.Changes[0].ID != spec.ChangeID || batch.Changes[0].Service != spec.Service) {
		return nil, fmt.Errorf("指定变更与范围不一致")
	}
	if spec.Service != "" && (!apps[spec.Service] || len(all) != 1) {
		return nil, fmt.Errorf("发布应用与所选变更不一致")
	}
	for app, mode := range req.CodeModes {
		if !apps[app] || mode != "direct" && mode != "merge" {
			return nil, fmt.Errorf("无效的应用发布方式 %s", app)
		}
	}
	if req.ArtifactSources == nil {
		req.ArtifactSources = map[string]string{}
		for app := range apps {
			if req.Artifacts[app] > 0 {
				req.ArtifactSources[app] = "existing"
			} else {
				req.ArtifactSources[app] = "build"
			}
		}
	}
	for app, id := range req.Artifacts {
		if req.ArtifactSources[app] != "existing" || id <= 0 {
			return nil, fmt.Errorf("应用 %s 的制品不属于本次已有制品范围", app)
		}
	}
	for app := range req.CodeModes {
		if req.ArtifactSources[app] != "build" {
			return nil, fmt.Errorf("应用 %s 使用已有制品，不能指定构建方式", app)
		}
	}
	names := []string{}
	for app := range all {
		names = append(names, app)
	}
	slices.Sort(names)
	for _, app := range names {
		a := publicationApp{Service: app, Mode: "resource"}
		if apps[app] {
			cfg, dest, _, err := s.serviceDeploymentTerms(ServiceDeploymentSpec{Service: app, Environment: req.Environment, Reason: req.Reason})
			if err != nil {
				return nil, err
			}
			target, err := m.ValidateTarget(app, req.Environment, req.Reason)
			if err != nil {
				return nil, err
			}
			a.Config, a.Destination, a.Targets = cfg, dest, target.ExecutionTargets()
			a.Mode = req.CodeModes[app]
			if a.Mode == "" {
				a.Mode = "merge"
			}
			if req.ArtifactSources[app] == "existing" {
				a.Mode = "artifact"
				artifact := s.GetArtifact(req.Artifacts[app])
				if artifact == nil || artifact.Service != app || artifact.Digest == "" {
					return nil, fmt.Errorf("请为 %s 选择有效制品", app)
				}
				a.ArtifactID, a.ArtifactDigest, a.ArtifactSource, a.ArtifactSourceID = artifact.ID, artifact.Digest, artifact.Source, artifact.SourceID
			}
		}
		batch.Applications = append(batch.Applications, a)
	}
	accounts, enabled, e := s.researchPeople(r.Context(), r)
	if e != nil {
		return nil, e
	}
	actor := deliveryActor(r)
	if !slices.Contains(enabled, actor) {
		return nil, fmt.Errorf("当前人员目录不可用")
	}
	prep, e := s.ResearchFlowPlan(req, accounts, enabled, actor)
	if e != nil {
		return nil, e
	}
	if len(prep.Blockers) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(prep.Blockers, "；"))
	}
	var candidate *CodePreparation
	if spec.PreparationID == 0 {
		spec.PreparationID, e = s.pendingScopePreparation(actor, req, prep)
		if e != nil {
			return nil, e
		}
	}
	if spec.PreparationID != 0 {
		if err := checkPreparationUnpublished(s.db, spec.PreparationID); err != nil {
			return nil, err
		}
		old := s.deliveryRunLedger(spec.PreparationID)
		if !matchingPublicationPreparation(old, actor, req, prep) {
			return nil, fmt.Errorf("原准备单的范围、人员或方式不匹配")
		}
		for _, st := range old.Steps {
			if (st.Kind == "code-prepare" || st.Kind == "code-verify") && st.Detail != "" {
				candidate = s.CodePreparation(st.Detail)
				if candidate == nil || candidate.Owner != actor || candidate.PreparationRunID != old.ID {
					return nil, fmt.Errorf("原代码准备身份不匹配")
				}
			}
		}
	}
	count := 0
	for _, n := range prep.Nodes {
		if n.Kind != "code-prepare" && n.Kind != "code-verify" {
			continue
		}
		count++
		if candidate == nil {
			candidate, e = s.resolveCodePreparation(r.Context(), CodePreparationRequest{Delivery: req, Policies: n.Policies, BuildConcurrency: n.BuildConcurrency, VerifyOnly: n.Kind == "code-verify"}, actor)
			if e != nil {
				return nil, e
			}
		}
	}
	if count != 1 || candidate == nil {
		return nil, fmt.Errorf("发布需要一个代码准备节点")
	}
	if candidate.VerifyOnly {
		for i := range batch.Applications {
			app := &batch.Applications[i]
			if app.Mode == "merge" {
				if req.CodeModes[app.Service] == "merge" {
					return nil, fmt.Errorf("应用 %s 的模板只允许核对已合入代码", app.Service)
				}
				app.Mode = "verify"
			}
		}
	}
	batch.CandidateDigest = candidateDigest(candidate)
	batch.Repositories = candidate.Repositories
	batch.Builds = candidate.Builds
	plan, _, e := s.resolveDelivery(req, m, true)
	if e != nil {
		return nil, e
	}
	if len(plan.Blockers) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(plan.Blockers, "；"))
	}
	batch.RequiresReview = deliveryReviewRequired(plan)
	if batch.RequiresReview {
		batch.EnvironmentReviewers = researchApprovers(plan.Environment.Approvers)
	}
	batch.Resources, batch.Checklist, batch.ResourceDigest = plan.ResourceTargets, plan.Checklist, publicationResourceDigest(plan)
	batch.ResourcePreview = publicationDifferences(plan)
	spec.Mode = "change"
	g := &changeDeploymentGrant{Batch: batch, Request: req, ScopeDigest: researchScopeDigest(prep.Scope), TemplateDigest: digestJSON(draft.Template), PreparationSteps: prep.Nodes, Steps: plan.Nodes}
	grant := &ServiceDeploymentGrant{Spec: spec, Change: g}
	grant.Digest = digestJSON(grant)
	return grant, nil
}

type publicationDifference struct {
	Node     string `json:"node"`
	Target   string `json:"target"`
	ChangeID int64  `json:"change_id"`
	Content  string `json:"content"`
}

func publicationDifferences(p *DeliveryPlan) []publicationDifference {
	var result []publicationDifference
	for _, stage := range p.SQLPlans {
		for _, stmt := range stage.Statements {
			result = append(result, publicationDifference{stage.NodeID, stage.Database, stmt.ChangeID, stmt.SQL})
		}
	}
	for _, stage := range p.NacosPlans {
		for _, config := range stage.Configs {
			for _, d := range config.Diff {
				action := "修改"
				if d.Before == nil {
					action = "新增"
				} else if d.After == nil {
					action = "移除"
				}
				result = append(result, publicationDifference{stage.NodeID, config.Group + " / " + config.DataID, config.ChangeID, d.Key + " · " + action})
			}
		}
	}
	for _, stage := range p.RedisPlans {
		for _, c := range stage.Changes {
			for _, args := range c.Commands {
				if len(args) > 0 {
					result = append(result, publicationDifference{stage.NodeID, c.Alias, c.ChangeID, args[0]})
				}
			}
		}
	}
	return result
}

// Materialize only the repositories and build contracts in the original grant.
// In particular, a later push must not change a direct build's commit.
func (s *Store) createFrozenPublicationCandidate(run *DeliveryRun, node *DeliveryNode, actor string, b *publicationScope) (*CodePreparation, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	concurrency := node.BuildConcurrency
	if concurrency == 0 {
		concurrency = 2
	}
	p := &CodePreparation{ID: hex.EncodeToString(nonce[:]), State: "preview", Owner: actor, PreparationRunID: run.ID, Request: *run.Plan.PreparationRequest, ScopeDigest: codeScopeDigest(run.Plan.Scope), PolicyRevision: *run.Plan.PreparationPolicyRevision, BuildConcurrency: concurrency, VerifyOnly: node.Kind == "code-verify", Repositories: slices.Clone(b.Repositories), Builds: slices.Clone(b.Builds)}
	p.Request.Artifacts = nil
	p.Request.PreflightToken = ""
	p.Request.CodePreparationID = ""
	if candidateDigest(p) != b.CandidateDigest {
		return nil, fmt.Errorf("候选与本次授权不一致")
	}
	for _, repo := range p.Repositories {
		if repo.Status != "pending" || repo.ResultCommit != "" {
			return nil, fmt.Errorf("已有仓库执行回执，请返回原准备单")
		}
	}
	for _, build := range p.Builds {
		if build.RunID != 0 || build.ArtifactID != 0 {
			return nil, fmt.Errorf("已构建的制品不能启动新任务")
		}
	}
	return p, s.saveCodePreparation(p)
}

// Inspect historical manual executions as well as coordinated releases.
func checkPreparationUnpublished(db interface{ QueryRow(string, ...any) *sql.Row }, id int64) error {
	var runID int64
	err := db.QueryRow(`SELECT id FROM delivery_runs WHERE json_extract(plan,'$.preparation_run_id')=? ORDER BY id LIMIT 1`, id).Scan(&runID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("准备单 #%d 已生成执行单 #%d，请查看原单", id, runID)
}

// Reconnect to the same owner's active authorization before resolving moving branch heads.
func (s *Store) existingPublication(r *http.Request, spec ServiceDeploymentSpec) (*ServiceDeployment, error) {
	if spec.Delivery == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT value FROM deploy_meta WHERE key LIKE 'service_deployment:%' AND json_extract(value,'$.owner')=? AND json_extract(value,'$.state')='running'`, deliveryActor(r))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var task ServiceDeployment
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &task); err != nil {
			return nil, err
		}
		if task.Grant.Change == nil || task.Grant.Change.Batch == nil {
			continue
		}
		original := task.Grant.Spec
		if spec.PreparationID == 0 {
			original.PreparationID = 0
		}
		if digestJSON(original) == digestJSON(spec) {
			return &task, nil
		}
	}
	return nil, rows.Err()
}
