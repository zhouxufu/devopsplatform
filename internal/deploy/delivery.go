package deploy

// Delivery plans are immutable approval snapshots. The first executor is
// deliberately stepwise: deploy uses the existing batch release engine and
// manual gates require an explicit confirmation. Unimplemented executors fail
// closed instead of marking configuration/database changes as successful.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"logplatform/pkg/workflownode"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type DeliveryNode struct {
	BuildConcurrency  int                   `json:"build_concurrency,omitempty"`
	SQLGroup          string                `json:"sql_group,omitempty"`
	Extension         *workflownode.Binding `json:"extension,omitempty"`
	Verification      *VerificationSpec     `json:"verification,omitempty"`
	Script            *DeliveryScript       `json:"script,omitempty"`
	Monitor           *MonitorGate          `json:"monitor,omitempty"`
	ApplicationScope  string                `json:"application_scope,omitempty"`
	DefaultCodePolicy *CodePolicy           `json:"default_code_policy,omitempty"`
	AssignedWorkItems map[string][]int64    `json:"assigned_work_items,omitempty"`
	AssigneeSource    string                `json:"assignee_source,omitempty"`
	Policies          map[string]CodePolicy `json:"policies,omitempty"`
	DependsOn         []string              `json:"depends_on,omitempty"`
	ID                string                `json:"id"`
	Kind              string                `json:"kind"`
	Name              string                `json:"name"`
	Service           string                `json:"service,omitempty"`
	Instructions      string                `json:"instructions,omitempty"`
	Approvers         []string              `json:"approvers,omitempty"`
	ApprovalMode      string                `json:"approval_mode,omitempty"`
	ChangeIDs         []int64               `json:"change_ids,omitempty"`
}
type DeliveryTemplate struct {
	Preparation []DeliveryNode `json:"preparation,omitempty"`
	Workbench   string         `json:"workbench,omitempty"`
	ID          int64          `json:"id"`
	Name        string         `json:"name"`
	Mode        string         `json:"mode"`
	Services    []string       `json:"services"`
	Nodes       []DeliveryNode `json:"nodes"`
	Owner       string         `json:"owner"`
}
type DeliveryRequest struct {
	CodeModes         map[string]string `json:"code_modes,omitempty"`
	Scene             string            `json:"scene,omitempty"`
	PreparationRunID  int64             `json:"preparation_run_id,omitempty"`
	ArtifactSources   map[string]string `json:"artifact_sources,omitempty"`
	ClientRequestID   string            `json:"client_request_id,omitempty"`
	StrategyKind      string            `json:"strategy_kind,omitempty"`
	SourceChangeIDs   []int64           `json:"source_change_ids,omitempty"`
	SelectedChangeIDs []int64           `json:"selected_change_ids"`
	OrderConfirmed    bool              `json:"order_confirmed,omitempty"`
	CodePreparationID string            `json:"code_preparation_id,omitempty"`
	SourceType        string            `json:"source_type,omitempty"`
	WorkItemIDs       []int64           `json:"work_item_ids,omitempty"`
	CodeConfirmed     bool              `json:"code_confirmed,omitempty"`
	PreflightToken    string            `json:"preflight_token,omitempty"`
	TemplateID        int64             `json:"template_id"`
	VersionID         int64             `json:"version_id"`
	Environment       string            `json:"environment"`
	Reason            string            `json:"reason"`
	Artifacts         map[string]int64  `json:"artifacts"`
	// A run may override/add/remove nodes without changing the reusable template.
	Nodes              []DeliveryNode `json:"nodes,omitempty"`
	changeDeploymentID string
	publication        *deliveryPublication
	reviewerDirectory  string
	readyDraftDigest   string // Internal one-change deployment preview. Never accepted from JSON.
	previewDrafts      bool   // Set only by the read-only preview route, never from JSON.
}
type DeliveryService struct {
	Service           string             `json:"service"`
	Artifact          Artifact           `json:"artifact"`
	Target            ServiceEnvironment `json:"target"`
	DestinationDigest string             `json:"destination_digest"`
}
type DeliveryPlan struct {
	Checklist                 []ReleaseChecklistStep   `json:"checklist,omitempty"`
	Scene                     string                   `json:"scene,omitempty"`
	PreparationRunID          int64                    `json:"preparation_run_id,omitempty"`
	PreparationRequest        *DeliveryRequest         `json:"preparation_request,omitempty"`
	PreparationPolicyRevision *int64                   `json:"preparation_policy_revision,omitempty"`
	ArtifactSources           map[string]string        `json:"artifact_sources,omitempty"`
	RequestDigest             string                   `json:"request_digest,omitempty"`
	StrategyKind              string                   `json:"strategy_kind,omitempty"`
	StrategyVersion           int                      `json:"strategy_version,omitempty"`
	PolicySnapshot            *DeliveryPolicySnapshot  `json:"policy_snapshot,omitempty"`
	PolicyDigest              string                   `json:"policy_digest,omitempty"`
	ResourceTargets           []DeliveryResourceTarget `json:"resource_targets,omitempty"`
	CodePreparationID         string                   `json:"code_preparation_id,omitempty"`
	Scope                     *DeliveryScope           `json:"scope,omitempty"`
	PreflightToken            string                   `json:"preflight_token,omitempty"`
	PreflightExpires          int64                    `json:"preflight_expires,omitempty"`
	WorkbenchPolicy           *WorkbenchPolicy         `json:"workbench_policy,omitempty"`
	Template                  DeliveryTemplate         `json:"template"`
	Version                   *ReleaseVersion          `json:"version,omitempty"`
	Environment               Environment              `json:"environment"`
	Reason                    string                   `json:"reason"`
	Services                  []DeliveryService        `json:"services"`
	Nodes                     []DeliveryNode           `json:"nodes"`
	Blockers                  []string                 `json:"blockers"`
	Warnings                  []string                 `json:"warnings"`
	SQLPlans                  []*SQLExecutionPlan      `json:"sql_plans,omitempty"`
	NacosPlans                []*NacosExecutionPlan    `json:"nacos_plans,omitempty"`
	RedisPlans                []*RedisExecutionPlan    `json:"redis_plans,omitempty"`
}
type DeliveryManualRecovery struct {
	Actor          string `json:"actor"`
	Comment        string `json:"comment"`
	At             int64  `json:"at"`
	ArtifactID     int64  `json:"artifact_id"`
	ArtifactDigest string `json:"artifact_digest"`
}

type DeliveryStep struct {
	APIReports         []workflowAPIReport     `json:"api_reports,omitempty"`
	PreviousReleaseIDs []int64                 `json:"previous_release_ids,omitempty"`
	ManualRecovery     *DeliveryManualRecovery `json:"manual_recovery,omitempty"`
	LeaseUntil         int64                   `json:"lease_until,omitempty"`
	Evidence           *NodeEvidence           `json:"evidence,omitempty"`
	StartedAt          int64                   `json:"started_at,omitempty"`
	EndedAt            int64                   `json:"ended_at,omitempty"`
	NodeID             string                  `json:"node_id"`
	Name               string                  `json:"name"`
	Kind               string                  `json:"kind"`
	Service            string                  `json:"service,omitempty"`
	Status             string                  `json:"status"`
	ReleaseID          int64                   `json:"release_id,omitempty"`
	Detail             string                  `json:"detail,omitempty"`
	Reviews            []ApprovalDecision      `json:"reviews,omitempty"`
}
type DeliveryRun struct {
	SQLStageBaselines []SQLStageBaseline `json:"sql_stage_baselines,omitempty"`
	ID                int64              `json:"id"`
	Plan              DeliveryPlan       `json:"plan"`
	Digest            string             `json:"digest"`
	Status            string             `json:"status"`
	Steps             []DeliveryStep     `json:"steps"`
	Owner             string             `json:"owner"`
	Approver          string             `json:"approver,omitempty"`
	CreatedAt         int64              `json:"created_at"`
	Revision          int64              `json:"revision"`
	envSecrets        map[string]string
}

func (v DeliveryRun) MarshalJSON() ([]byte, error) {
	type publicRun DeliveryRun
	p := publicRun(v)
	p.Plan.NacosPlans = publicNacosPlans(v.Plan.NacosPlans)
	return json.Marshal(p)
}

func deliverySchema(dialect string) []string {
	identity, doc := "INTEGER PRIMARY KEY AUTOINCREMENT", "TEXT"
	if dialect == "mysql" {
		identity, doc = "BIGINT PRIMARY KEY AUTO_INCREMENT", "LONGTEXT"
	}
	queries := []string{
		instanceOperationSchema,
		`CREATE TABLE IF NOT EXISTS delivery_publications (id VARCHAR(64) PRIMARY KEY,run_id BIGINT NOT NULL UNIQUE,owner VARCHAR(191) NOT NULL,authority VARCHAR(8) NOT NULL,key_id VARCHAR(191) NOT NULL,environment VARCHAR(64) NOT NULL,digest VARCHAR(80) NOT NULL,expires_at BIGINT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS delivery_script_locks (host VARCHAR(191) PRIMARY KEY, run_id BIGINT NOT NULL, node_id VARCHAR(191) NOT NULL)`,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS feature_branch_plans (id VARCHAR(64) PRIMARY KEY, document %s NOT NULL, revision BIGINT NOT NULL)`, doc),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS delivery_observations (observation_key VARCHAR(80) PRIMARY KEY, run_id BIGINT NOT NULL, document %s NOT NULL, revision BIGINT NOT NULL)`, doc),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS release_notices (record_kind VARCHAR(16) NOT NULL, record_id BIGINT NOT NULL, document %s NOT NULL, revision BIGINT NOT NULL, actor VARCHAR(191) NOT NULL, updated_at BIGINT NOT NULL, PRIMARY KEY(record_kind,record_id))`, doc),
		`CREATE TABLE IF NOT EXISTS delivery_preflight_uses (nonce VARCHAR(64) PRIMARY KEY, run_id BIGINT NOT NULL)`,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS delivery_version_artifacts (version_id BIGINT PRIMARY KEY, document %s NOT NULL, used INTEGER NOT NULL DEFAULT 0)`, doc),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS delivery_environment_artifacts (version_id BIGINT NOT NULL, environment VARCHAR(32) NOT NULL, document %s NOT NULL, used INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(version_id,environment))`, doc),
		`CREATE TABLE IF NOT EXISTS delivery_controls (run_id BIGINT PRIMARY KEY, state VARCHAR(32) NOT NULL, actor VARCHAR(191) NOT NULL, lease VARCHAR(64) NOT NULL, lease_until BIGINT NOT NULL, message TEXT NOT NULL)`,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS delivery_templates (id %s, document %s NOT NULL, owner VARCHAR(191) NOT NULL)`, identity, doc),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS delivery_runs (id %s, plan %s NOT NULL, digest VARCHAR(80) NOT NULL, status VARCHAR(32) NOT NULL, execution %s NOT NULL, env_secrets %s NOT NULL, owner VARCHAR(191) NOT NULL, approver VARCHAR(191) NOT NULL, created_at BIGINT NOT NULL, revision BIGINT NOT NULL)`, identity, doc, doc, doc),
	}
	queries = append(queries, documentReportSchema(doc), documentReportLinksSchema(doc), fmt.Sprintf(`CREATE TABLE IF NOT EXISTS api_document_operation_updates (snapshot_id BIGINT PRIMARY KEY, document %s NOT NULL)`, doc))
	queries = append(queries, artifactRetentionSchema()...)
	queries = append(queries, integrationSchema(identity, doc)...)
	queries = append(queries, researchSchema(identity, doc)...)
	if dialect == "mysql" {
		for i := range queries {
			queries[i] += ` ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`
		}
	}
	return queries
}
func (s *Store) initDeliverySchema() error {
	for _, q := range deliverySchema(s.Driver()) {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}
	if err := s.ensureDocumentReportIndexes(); err != nil {
		return err
	}
	if err := s.ensureDocumentRevisionIdentity(); err != nil {
		return err
	}
	// Retain legacy immutable IDs. Unknown historical environments remain unassigned.
	if _, err := s.db.Exec(`INSERT INTO api_document_versions(id,application_id,environment,service,run_id,artifact_id,artifact_digest,commit_ref,provider,provider_version,digest,document,created_at,baseline_id,comparison)
        SELECT p.id,p.application_id,'',p.service,p.run_id,p.artifact_id,p.artifact_digest,p.commit_ref,p.provider,p.provider_version,p.digest,p.document,p.created_at,COALESCE((SELECT MAX(b.id) FROM api_document_snapshots b WHERE b.application_id=p.application_id AND b.id<p.id),0),'{}'
        FROM api_document_snapshots p WHERE NOT EXISTS (SELECT 1 FROM api_document_versions v WHERE v.id=p.id)`); err != nil {
		return err
	}
	_, err := s.db.Exec(`INSERT OR IGNORE INTO resource_applications(name) SELECT DISTINCT service FROM application_resource_requirements`)
	return err
}

func validateDeliveryNodes(nodes []DeliveryNode) error {
	if len(nodes) == 0 || len(nodes) > 30 {
		return fmt.Errorf("编排需要 1–30 个顺序节点")
	}
	seen := map[string]bool{}
	executionSeen := false
	for _, n := range nodes {
		if err := validateOperationNode(n); err != nil {
			return err
		}
		if !environmentNameRE.MatchString(n.ID) || seen[n.ID] || strings.TrimSpace(n.Name) == "" {
			return fmt.Errorf("节点需要不重复的英文标识和名称")
		}
		seen[n.ID] = true
		if n.Kind == "approval" {
			if executionSeen {
				return fmt.Errorf("人工审核节点必须放在所有执行/确认节点之前")
			}
			if err := validateApprovalNode(n); err != nil {
				return err
			}
		} else {
			executionSeen = true
		}
		switch n.Kind {
		case "deploy", "manual", "mysql", "nacos", "data-sync", "redis", "approval", "verify", "script", "extension":
		default:
			return fmt.Errorf("不支持的节点类型：%s", n.Kind)
		}
		if n.Kind == "extension" && (n.Extension == nil || n.Extension.Provider == "" || n.Extension.NodeKey == "") {
			return fmt.Errorf("请选择已安装的插件节点")
		}
		if n.Kind == "manual" && strings.TrimSpace(n.Instructions) == "" {
			return fmt.Errorf("人工节点需要填写操作与确认说明")
		}
	}
	return nil
}
func (s *Store) SaveDeliveryTemplate(v *DeliveryTemplate) error {
	v.Name = strings.TrimSpace(v.Name)
	if v.Name == "" || (v.Mode != "fixed" && v.Mode != "version") {
		return fmt.Errorf("模板名称必填；范围支持 fixed 或 version")
	}
	var nodesErr error
	if v.Workbench == "research" {
		return fmt.Errorf("独立准备模板已移除，请使用部署编排的准备阶段")
	}
	nodesErr = validateDeliveryNodes(v.Nodes)
	if err := nodesErr; err != nil {
		return err
	}
	if len(v.Preparation) > 0 {
		if v.Workbench != "" || v.Mode != "version" {
			return fmt.Errorf("组合交付流程的准备阶段仅支持工作项 / 版本动态范围")
		}
		if err := validateResearchNodes(v.Preparation); err != nil {
			return err
		}
		codeNodes := 0
		for _, n := range v.Preparation {
			if n.Kind == "code-prepare" || n.Kind == "code-verify" {
				codeNodes++
				if n.ApplicationScope != "delivery" {
					return fmt.Errorf("组合流程的代码节点须跟随本次交付范围")
				}
			}
		}
		if codeNodes > 1 {
			return fmt.Errorf("组合流程目前支持一个按范围合并构建节点，请勿重复构建应用")
		}
	}
	if v.Workbench != "" && v.Workbench != "research" {
		if err := validateWorkbenchTemplate(*v); err != nil {
			return err
		}
	}
	if v.Mode == "fixed" && len(v.Services) == 0 {
		for _, node := range v.Nodes {
			if node.Kind == "deploy" {
				return fmt.Errorf("包含部署节点的固定服务模式至少选择一个应用")
			}
		}
	}
	seen := map[string]bool{}
	for _, name := range v.Services {
		if seen[name] || s.GetService(name) == nil {
			return fmt.Errorf("固定服务不存在或重复：%s", name)
		}
		seen[name] = true
	}
	if v.Mode == "version" {
		v.Services = []string{}
	}
	data, _ := json.Marshal(v)
	if v.ID == 0 {
		r, err := s.db.Exec(`INSERT INTO delivery_templates(document,owner) VALUES(?,?)`, string(data), v.Owner)
		if err != nil {
			return err
		}
		v.ID, _ = r.LastInsertId()
		return nil
	}
	r, err := s.db.Exec(`UPDATE delivery_templates SET document=? WHERE id=? AND owner=?`, string(data), v.ID, v.Owner)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		current := s.DeliveryTemplate(v.ID)
		if current == nil || current.Owner != v.Owner {
			return fmt.Errorf("模板不存在或已改变")
		}
	}
	return nil
}
func (s *Store) DeliveryTemplates() []DeliveryTemplate {
	out := []DeliveryTemplate{}
	rows, err := s.db.Query(`SELECT id,document,owner FROM delivery_templates ORDER BY id DESC`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var doc, owner string
		if rows.Scan(&id, &doc, &owner) == nil {
			var v DeliveryTemplate
			if json.Unmarshal([]byte(doc), &v) == nil {
				v.ID, v.Owner = id, owner
				out = append(out, v)
			}
		}
	}
	return out
}
func (s *Store) DeliveryTemplate(id int64) *DeliveryTemplate {
	for _, v := range s.DeliveryTemplates() {
		if v.ID == id {
			return &v
		}
	}
	return nil
}

// Pin destination identities, not SSH/cloud credential plaintext. Rotating a
// credential does not change the host identity, changing its endpoint does.
func (s *Store) deliveryDestinationDigest(target ServiceEnvironment) (string, error) {
	identities := []any{}
	if target.ScriptTarget() {
		r, err := s.validateScriptRunner(target.DeployConfig)
		if err != nil {
			return "", err
		}
		return scriptRunnerDigest(r), nil
	}
	if target.ManagedTarget() {
		connection := s.GetConnection(target.DeployConfig["connection"])
		if connection == nil {
			return "", fmt.Errorf("云资源连接不存在")
		}
		identities = append(identities, map[string]any{"name": connection.Name, "kind": connection.Kind, "endpoint": connection.Endpoint, "config": connection.Config})
	} else {
		for _, name := range target.Hosts {
			host := s.GetHost(name)
			if host == nil || host.Environment != target.Environment {
				return "", fmt.Errorf("目标主机不存在或环境不一致：%s", name)
			}
			identities = append(identities, map[string]string{"name": host.Name, "environment": host.Environment, "addr": host.Addr, "user": host.User, "key_path": host.KeyPath, "host_key": host.HostKey})
		}
	}
	data, _ := json.Marshal(identities)
	hash := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(hash[:]), nil
}

func (s *Store) ResolveDelivery(req DeliveryRequest, manager *ReleaseManager) (*DeliveryPlan, map[string]string, error) {

	return s.resolveDelivery(req, manager, false)
}

// beforeBuild resolves read-only resource preflight before the authorized build produces artifacts.
func (s *Store) resolveDelivery(req DeliveryRequest, manager *ReleaseManager, beforeBuild bool) (*DeliveryPlan, map[string]string, error) {
	draft, err := s.DraftDelivery(req)
	if err != nil {
		return nil, nil, err
	}
	if !beforeBuild {
		if err := s.validateWorkflowPreparation(req, draft); err != nil {
			return nil, nil, err
		}
	}
	tpl, env := &draft.Template, s.GetEnvironment(req.Environment)
	nodes := draft.Nodes
	if req.Nodes != nil {
		if err := validateChangeOrderGates(draft, req.Nodes); err != nil {
			return nil, nil, err
		}
		if err := validateManualRecordOverrides(nodes, req.Nodes); err != nil {
			return nil, nil, err
		}
		if req.StrategyKind == "standard" {
			if err := validateStandardOverrides(nodes, req.Nodes); err != nil {
				return nil, nil, err
			}
		}
		nodes = req.Nodes
	}
	if err := validateDeliveryNodes(nodes); err != nil {
		return nil, nil, err
	}
	for _, n := range draft.Nodes {
		if len(n.DependsOn) > 0 {
			found := false
			for _, a := range nodes {
				if a.ID == n.ID && digestJSON(a.DependsOn) == digestJSON(n.DependsOn) {
					found = true
				}
			}
			if !found {
				return nil, nil, fmt.Errorf("不能删减节点 %s 的声明依赖", n.Name)
			}
		}
	}
	if err := validateNodeDependencies(nodes); err != nil {
		return nil, nil, err
	}
	requiredReviews, actualReviews := []DeliveryNode{}, []DeliveryNode{}
	for _, n := range draft.Nodes {
		if n.Kind == "approval" {
			requiredReviews = append(requiredReviews, n)
		}
	}
	for _, n := range nodes {
		if n.Kind == "approval" {
			actualReviews = append(actualReviews, n)
		}
	}
	if len(requiredReviews) > 0 {
		a, _ := json.Marshal(requiredReviews)
		b, _ := json.Marshal(actualReviews)
		if string(a) != string(b) {
			return nil, nil, fmt.Errorf("本次交付不能删除、跳过或改动模板中的审核阶段；请修改模板后重新预检")
		}
	}
	p := &DeliveryPlan{RequestDigest: deliveryRequestDigest(req), StrategyKind: draft.StrategyKind, StrategyVersion: draft.StrategyVersion, PolicySnapshot: &draft.PolicySnapshot, PolicyDigest: draft.PolicyDigest, CodePreparationID: req.CodePreparationID, Template: *tpl, Environment: *env, Reason: strings.TrimSpace(req.Reason), Nodes: nodes, Services: []DeliveryService{}, Blockers: []string{}, Warnings: []string{}}
	p.Scene, p.PreparationRunID = req.Scene, req.PreparationRunID
	defer func() {
		p.Checklist = releaseChecklist(p)
		for _, step := range p.Checklist {
			for _, item := range step.Items {
				if item.Status == "blocked" && len(item.Prerequisites) > 0 {
					p.Blockers = append(p.Blockers, fmt.Sprintf("变更 #%d：%s", item.ID, item.Reason))
				}
			}
		}
	}()
	if tpl.Workbench != "" {
		policy, err := s.workbenchPolicy(tpl.Workbench, env.Name)
		if err != nil {
			return nil, nil, err
		}
		p.WorkbenchPolicy = &policy
		candidate := *tpl
		candidate.Nodes = nodes
		if err := validateWorkbenchTemplate(candidate); err != nil {
			return nil, nil, err
		}
		if deliveryReviewRequired(p) && strings.TrimSpace(env.Approvers) == "" {
			p.Blockers = append(p.Blockers, "工作台操作必须审核，请先为环境配置审核人")
		}
	}
	secrets := map[string]string{}
	services := map[string]bool{}
	scope, version, scopeErr := s.resolveDeliveryScope(req, tpl)
	if scopeErr != nil {
		return nil, nil, scopeErr
	}
	if e := validateDeliveryReviewerDirectory(p, req.reviewerDirectory, ""); e != nil {
		p.Blockers = append(p.Blockers, e.Error())
	}
	p.Scope, p.Version = scope, version
	modes, modeErr := deliveryArtifactSources(req, scope, tpl.Services)
	if modeErr != nil {
		return nil, nil, modeErr
	}
	p.ArtifactSources = req.ArtifactSources
	needsBuild, needsExisting := false, false
	for _, mode := range modes {
		needsBuild = needsBuild || mode == "build"
		needsExisting = needsExisting || mode == "existing"
	}
	if len(req.ArtifactSources) == 0 && req.CodePreparationID != "" {
		needsExisting = false
	}
	if !beforeBuild && needsBuild && req.CodePreparationID == "" {
		p.Blockers = append(p.Blockers, "本次构建尚未完成，请先准备代码与制品，再预检环境变更")
	}
	if scope != nil && env.Purpose == "production" {
		for _, w := range scope.WorkItems {
			if w.ReleaseRequired != nil && !*w.ReleaseRequired {
				p.Blockers = append(p.Blockers, fmt.Sprintf("工作项 #%d 不独立生产交付，请关联主项、调整判定或在版本中明确选入变更", w.ID))
			}
		}
		selected := map[int64]bool{}
		for _, item := range scope.WorkItems {
			selected[item.ID] = true
		}
		explicit := map[int64]bool{}
		if version != nil {
			for _, id := range version.ManualChangeIDs {
				explicit[id] = true
			}
		}
		for _, c := range scope.Changes {
			if own := c.DeliveryOwnership; own != nil {
				if own.Mode == "draft" && !explicit[c.ID] {
					p.Blockers = append(p.Blockers, fmt.Sprintf("变更 #%d 尚未确定生产交付归属，请关联主工作项或明确纳入版本", c.ID))
				}
				if own.Mode == "follow" && !selected[own.WorkItemID] && !explicit[c.ID] {
					p.Blockers = append(p.Blockers, fmt.Sprintf("变更 #%d 随工作项 #%d 交付，请调整本次上线范围", c.ID, own.WorkItemID))
				}
			}
		}
	}
	if version != nil && version.Status != ReleaseVersionReady && version.Status != ReleaseVersionCompleted {
		p.Blockers = append(p.Blockers, "来源版本尚未就绪")
	}
	if req.CodePreparationID != "" {
		if err := s.validatePreparedArtifacts(req, scope); err != nil {
			return nil, nil, err
		}
		if !needsExisting {
			req.CodeConfirmed = true
		}
	}
	if scope == nil {
		for _, name := range tpl.Services {
			services[name] = true
		}
	} else {
		if version != nil {
			pinned := s.VersionArtifacts(version.ID, env.Name)
			if err := validateVersionArtifactSubset(scope, version, pinned); err != nil {
				p.Blockers = append(p.Blockers, err.Error())
			}
			if len(pinned.Bindings) > 0 {
				if pinned.ScopeDigest != versionCodeScope(version) {
					p.Blockers = append(p.Blockers, "版本代码范围发生变化，请重新关联制品")
				}
				for _, binding := range pinned.Bindings {
					selectedApp := false
					for _, c := range scope.Changes {
						if (c.Kind == "code" || c.Kind == "artifact") && c.Service == binding.Service {
							selectedApp = true
						}
					}
					if !selectedApp {
						continue
					}
					a := s.GetArtifact(binding.ArtifactID)
					if req.Artifacts[binding.Service] != binding.ArtifactID || a == nil || a.Digest != binding.Digest {
						p.Blockers = append(p.Blockers, "制品与版本锁定映射不一致："+binding.Service)
					}
				}
			}
		}
		for _, c := range scope.Changes {
			if c.Status != ChangeReady {
				p.Blockers = append(p.Blockers, fmt.Sprintf("变更 #%d 尚未就绪，请先完成交付内容", c.ID))
			}
			if c.Kind == "code" || c.Kind == "artifact" {
				if c.Service == "" {
					p.Blockers = append(p.Blockers, "代码/制品变更未关联应用："+c.Title)
				} else {
					services[c.Service] = true
				}
			} else if c.Kind != "manual" && c.Kind != "mysql" && c.Kind != "nacos" && c.Kind != "redis" {
				p.Blockers = append(p.Blockers, "变更执行器尚未接通："+c.Title+"（"+c.Kind+"）")
			}
		}
		if version != nil && req.StrategyKind == "standard" {
			pinned := s.VersionArtifacts(version.ID, env.Name)
			covered := len(pinned.Bindings) > 0 && pinned.ScopeDigest == versionCodeScope(version)
			for name := range services {
				found := false
				for _, b := range pinned.Bindings {
					a := s.GetArtifact(b.ArtifactID)
					if b.Service == name && b.CodeConfirmed && req.Artifacts[name] == b.ArtifactID && a != nil && a.Digest == b.Digest {
						found = true
					}
				}
				covered = covered && found
			}
			req.CodeConfirmed = req.CodeConfirmed || covered
		}
		if (scope.Type == "work_items" || req.StrategyKind == "standard") && len(services) > 0 && !req.CodeConfirmed {
			p.Blockers = append(p.Blockers, "请核对每个制品包含所选工作项代码；生产热修复不能使用混入其他未上线需求的 dev 制品")
		}
		if req.CodePreparationID == "" && len(services) > 0 {
			p.Warnings = append(p.Warnings, "尚未自动准备代码；请确认每个候选制品包含所选代码，不能用测试分支的全部代码替代。")
		}
		if len(scope.Changes) == 0 {
			p.Blockers = append(p.Blockers, "交付范围未包含变更")
		}
	}
	if req.StrategyKind == "standard" {
		kinds := map[string]bool{}
		for _, n := range nodes {
			if n.Kind != "approval" {
				kinds[n.Kind] = true
			}
		}
		if len(kinds) > 1 && !req.OrderConfirmed {
			p.Blockers = append(p.Blockers, "请明确核对跨类型步骤的业务依赖和执行顺序")
		}
	}
	if env.RequireReason && p.Reason == "" {
		p.Blockers = append(p.Blockers, "该环境必须填写发布原因")
	}
	if env.Protected && strings.TrimSpace(env.Approvers) == "" {
		p.Blockers = append(p.Blockers, "受保护环境尚未配置发布审核人")
	}
	names := []string{}
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		sv, artifact := s.GetService(name), s.GetArtifact(req.Artifacts[name])
		if beforeBuild && modes[name] == "build" && artifact == nil {
			target, e := manager.ValidateTarget(name, env.Name, p.Reason)
			if e != nil {
				p.Blockers = append(p.Blockers, e.Error())
				continue
			}
			target.RuntimeEnv = nil
			dest, e := s.deliveryDestinationDigest(*target)
			if e != nil {
				return nil, nil, e
			}
			p.Services = append(p.Services, DeliveryService{Service: name, Target: *target, DestinationDigest: dest})
			continue
		}
		if sv == nil || artifact == nil || artifact.Service != name || !s.artifactMatchesEnvironment(artifact, sv, env.Name) {
			p.Blockers = append(p.Blockers, "请选择应用的有效制品："+name)
			continue
		}
		target, err := manager.ValidateTarget(name, env.Name, p.Reason)
		if err != nil {
			p.Blockers = append(p.Blockers, name+"："+err.Error())
			continue
		}
		if e := s.artifactBuildComplete(artifact); e != nil {
			p.Blockers = append(p.Blockers, name+"："+e.Error())
		}
		if artifact.Digest == "" {
			p.Blockers = append(p.Blockers, name+"：制品缺少摘要")
		}
		if err := s.validateArtifactBuildVariant(artifact, env.Name); err != nil {
			p.Blockers = append(p.Blockers, name+"："+err.Error())
		}
		secrets[name] = target.runtimeEnvEncrypted
		target.RuntimeEnv = nil // never persist/reveal plaintext application secrets
		destination, err := s.deliveryDestinationDigest(*target)
		if err != nil {
			p.Blockers = append(p.Blockers, name+"："+err.Error())
			continue
		}
		p.Services = append(p.Services, DeliveryService{Service: name, Artifact: *artifact, Target: *target, DestinationDigest: destination})
	}
	var skippedWarnings []string
	nodes, skippedWarnings = omitEmptyDeliveryResourceNodes(nodes, p.Scope)
	p.Warnings = append(p.Warnings, skippedWarnings...)
	if len(nodes) == 0 {
		p.Blockers = append(p.Blockers, "本次交付没有可执行节点，请核对交付范围和流程")
		return p, secrets, nil
	}
	expanded, targets, expandErr := s.expandDeliveryResourceNodes(nodes, p.Scope, env.Name)
	if expandErr != nil {
		p.Blockers = append(p.Blockers, expandErr.Error())
		return p, secrets, nil
	}
	if len(expanded) > len(nodes) {
		p.Warnings = append(p.Warnings, fmt.Sprintf("按目标环境将 %d 个编排节点拆分为 %d 个执行节点；顺序执行，不保证跨资源原子性。", len(nodes), len(expanded)))
	}
	nodes, p.Nodes, p.ResourceTargets = expanded, expanded, targets
	if _, err := orderChangeNodes(nodes, p.Scope, false); err != nil {
		p.Blockers = append(p.Blockers, err.Error())
		return p, secrets, nil
	}
	allocated := map[string]int{}
	sqlAllocated := map[int64]int{}
	manualCount := 0
	sqlCount := 0
	nacosAllocated := map[int64]int{}
	nacosTargets := map[string]bool{}
	nacosCount := 0
	redisAllocated := map[int64]int{}
	redisCount := 0
	for nodeIndex, node := range nodes {
		switch node.Kind {
		case "extension":
			binding, err := s.resolveWorkflowExtension(node, p.Services, env.Name, beforeBuild)
			if err != nil {
				p.Blockers = append(p.Blockers, node.Name+"："+err.Error())
			} else {
				p.Nodes[nodeIndex].Extension = binding
				if binding.Executor == workflownode.APITestExecutor {
					for _, task := range binding.Tests {
						deployed := false
						for _, earlier := range nodes[:nodeIndex] {
							if earlier.Kind == "deploy" && (earlier.Service == "" || earlier.Service == task.Service) {
								deployed = true
							}
						}
						if !deployed {
							p.Blockers = append(p.Blockers, node.Name+"：请放在 "+task.Service+" 的部署节点之后")
						}
					}
				}
			}

		case "approval":
			p.Warnings = append(p.Warnings, node.Name+"：人工审核（"+node.ApprovalMode+"）；通过不会自动执行")
		case "redis":
			if deliveryReviewRequired(p) && strings.TrimSpace(env.Approvers) == "" {
				p.Blockers = append(p.Blockers, "Redis 操作必须审核，请先为该环境配置审核人（开发/测试也需要）")
			}
			ids := append([]int64{}, node.ChangeIDs...)
			if p.Scope != nil {
				allowed := map[int64]bool{}
				for _, c := range p.Scope.Changes {
					if c.Kind == "redis" {
						allowed[c.ID] = true
						if len(node.ChangeIDs) == 0 && (node.Service == "" || node.Service == c.Service) {
							ids = append(ids, c.ID)
						}
					}
				}
				for _, id := range ids {
					if !allowed[id] {
						p.Blockers = append(p.Blockers, fmt.Sprintf("Redis 节点引用版本外变更 #%d", id))
					}
				}
			}
			ready := true
			for _, id := range ids {
				redisAllocated[id]++
				c := s.GetChange(id)
				if c == nil || (c.Status != ChangeReady && !(req.previewDrafts && tpl.Workbench != "" && c.Status == ChangeDraft)) {
					p.Blockers = append(p.Blockers, fmt.Sprintf("Redis 变更 #%d 尚未就绪", id))
					ready = false
				}
			}
			if !ready {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			plan, err := s.prepareRedisNode(ctx, node.ID, ids, env.Name)
			cancel()
			if err != nil {
				p.Blockers = append(p.Blockers, node.Name+"："+err.Error())
				continue
			}
			p.RedisPlans = append(p.RedisPlans, plan)
			redisCount++
			review := "提交后仍须手动确认执行；"
			if deliveryReviewRequired(p) || hasApprovalNodes(p) {
				review = "Redis 命令必须由另一名环境审核人审核；"
			}
			p.Warnings = append(p.Warnings, review+"精确 Key 的当前数据不冻结、不校验内容，命令顺序执行、非事务、无自动回滚；超时不得重试。缓存清理无法恢复，请确认业务可重建缓存。")
		case "nacos":
			ids := append([]int64{}, node.ChangeIDs...)
			if p.Scope != nil {
				allowed := map[int64]bool{}
				for _, c := range p.Scope.Changes {
					if c.Kind == "nacos" {
						allowed[c.ID] = true
						if len(node.ChangeIDs) == 0 && (node.Service == "" || node.Service == c.Service) {
							ids = append(ids, c.ID)
						}
					}
				}
				for _, id := range ids {
					if !allowed[id] {
						p.Blockers = append(p.Blockers, fmt.Sprintf("Nacos 节点引用版本外变更 #%d", id))
					}
				}
			}
			ready := true
			for _, id := range ids {
				nacosAllocated[id]++
				c := s.GetChange(id)
				if c == nil || (c.Status != ChangeReady && !(req.previewDrafts && tpl.Workbench != "" && c.Status == ChangeDraft)) {
					p.Blockers = append(p.Blockers, fmt.Sprintf("Nacos 变更 #%d 尚未就绪", id))
					ready = false
				}
			}
			if !ready {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			plan, err := s.prepareNacosNode(ctx, node.ID, ids, env.Name)
			cancel()
			if err != nil {
				p.Blockers = append(p.Blockers, node.Name+"："+err.Error())
				continue
			}
			for _, item := range plan.Configs {
				key := nacosTargetKey(item)
				if nacosTargets[key] {
					p.Blockers = append(p.Blockers, "同一 Nacos 配置不能在多个节点重复修改")
				}
				nacosTargets[key] = true
			}
			p.NacosPlans = append(p.NacosPlans, plan)
			nacosCount++
			p.Warnings = append(p.Warnings, "Nacos 多文档顺序发布，不保证跨配置或跨服务原子性；失败停止，已成功项不自动恢复。差异值可能包含敏感信息，请限制执行单访问。")
		case "deploy":
			if node.Service != "" && !services[node.Service] {
				p.Blockers = append(p.Blockers, "部署节点应用不在交付范围："+node.Service)
			}
			for _, name := range names {
				if node.Service == "" || node.Service == name {
					allocated[name]++
				}
			}
		case "manual":
			manualCount++
		case "mysql":
			if deliveryReviewRequired(p) && strings.TrimSpace(env.Approvers) == "" {
				p.Blockers = append(p.Blockers, "SQL 写操作必须审核，请先为该环境配置审核人")
			}
			ids := append([]int64{}, node.ChangeIDs...)
			if p.Scope != nil {
				allowed := map[int64]bool{}
				for _, c := range p.Scope.Changes {
					if c.Kind == "mysql" {
						allowed[c.ID] = true
						if len(node.ChangeIDs) == 0 && (node.Service == "" || node.Service == c.Service) {
							ids = append(ids, c.ID)
						}
					}
				}
				for _, id := range ids {
					if !allowed[id] {
						p.Blockers = append(p.Blockers, fmt.Sprintf("SQL 节点引用版本外变更 #%d", id))
					}
				}
			}
			ready := true
			for _, id := range ids {
				sqlAllocated[id]++
				c := s.GetChange(id)
				if c == nil || (c.Status != ChangeReady && !(req.previewDrafts && tpl.Workbench != "" && c.Status == ChangeDraft)) {
					p.Blockers = append(p.Blockers, fmt.Sprintf("SQL 变更 #%d 尚未就绪", id))
					ready = false
				}
			}
			if !ready {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			plan, err := s.prepareSQLStage(ctx, node.ID, ids, env.Name, p.SQLPlans)
			cancel()
			if err != nil {
				p.Blockers = append(p.Blockers, node.Name+"："+err.Error())
				continue
			}
			p.SQLPlans = append(p.SQLPlans, plan)
			p.Warnings = append(p.Warnings, plan.Warnings...)
			sqlCount++
		default:
			p.Blockers = append(p.Blockers, "节点仅支持设计与保存，执行器尚未接通："+node.Name)
		}
	}
	for _, name := range names {
		if allocated[name] != 1 {
			p.Blockers = append(p.Blockers, name+" 必须且只能安排在一个部署节点中")
		}
	}
	if p.Scope != nil {
		for _, c := range p.Scope.Changes {
			if c.Kind == "redis" && redisAllocated[c.ID] != 1 {
				p.Blockers = append(p.Blockers, fmt.Sprintf("Redis 变更 #%d 必须且只能安排到一个 Redis 节点", c.ID))
			}
			if c.Kind == "nacos" && nacosAllocated[c.ID] != 1 {
				p.Blockers = append(p.Blockers, fmt.Sprintf("Nacos 变更 #%d 必须且只能安排到一个 Nacos 节点", c.ID))
			}
			if c.Kind == "mysql" && sqlAllocated[c.ID] != 1 {
				p.Blockers = append(p.Blockers, fmt.Sprintf("SQL 变更 #%d 必须且只能安排到一个 SQL 节点", c.ID))
			}
			if c.Kind == "manual" && manualCount == 0 {
				p.Blockers = append(p.Blockers, "人工变更需要配置人工确认节点："+c.Title)
			}
		}
	}
	for id, count := range sqlAllocated {
		if count != 1 {
			p.Blockers = append(p.Blockers, fmt.Sprintf("SQL 变更 #%d 在多个节点中重复", id))
		}
	}
	for id, count := range nacosAllocated {
		if count != 1 {
			p.Blockers = append(p.Blockers, fmt.Sprintf("Nacos 变更 #%d 在多个节点重复", id))
		}
	}
	for id, count := range redisAllocated {
		if count != 1 {
			p.Blockers = append(p.Blockers, fmt.Sprintf("Redis 变更 #%d 在多个节点重复", id))
		}
	}
	if len(names) == 0 && manualCount == 0 && sqlCount == 0 && nacosCount == 0 && redisCount == 0 {
		hasScript := false
		for _, n := range nodes {
			hasScript = hasScript || n.Kind == "script"
		}
		if !hasScript {
			p.Blockers = append(p.Blockers, "交付范围没有可执行应用或人工确认节点")
		}
	}
	if err := s.freezeOperations(p, tpl); err != nil {
		p.Blockers = append(p.Blockers, err.Error())
	}
	return p, secrets, nil
}

func (s *Store) CreateDeliveryRun(req DeliveryRequest, actor string, manager *ReleaseManager) (*DeliveryRun, error) {
	req.previewDrafts = false
	if old, err := s.submittedPreflightRun(req, actor); err != nil {
		return nil, err
	} else if old != nil {
		return old, nil
	}
	if req.StrategyKind == "standard" && req.PreflightToken == "" {
		return nil, fmt.Errorf("标准交付必须先预检并使用有效凭证")
	}
	p, secrets, err := s.ResolveDelivery(req, manager)
	if err != nil {
		return nil, err
	}
	if err := validateDeliveryReviewerDirectory(p, req.reviewerDirectory, actor); err != nil {
		return nil, err
	}
	if err := validateRunApprovals(p, actor); err != nil {
		return nil, err
	}
	if len(p.Blockers) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(p.Blockers, "；"))
	}
	var nonce string
	if req.PreflightToken != "" {
		nonce, err = s.verifyDeliveryPreflight(req, p, secrets, actor)
		if err != nil {
			return nil, err
		}
		var used int64
		if s.db.QueryRow(`SELECT run_id FROM delivery_preflight_uses WHERE nonce=?`, nonce).Scan(&used) == nil {
			return s.DeliveryRun(used), nil
		}
	}
	planJSON, _ := json.Marshal(p)
	hash := sha256.Sum256(planJSON)
	run := &DeliveryRun{Plan: *p, Digest: "sha256:" + hex.EncodeToString(hash[:]), Status: "approved", Owner: actor, CreatedAt: time.Now().Unix(), Revision: 1, Steps: []DeliveryStep{}, envSecrets: secrets}
	if deliveryReviewRequired(p) || hasApprovalNodes(p) {
		run.Status = "pending_approval"
	}
	for _, node := range p.Nodes {
		if node.Kind == "deploy" {
			for _, app := range p.Services {
				if node.Service == "" || node.Service == app.Service {
					run.Steps = append(run.Steps, DeliveryStep{NodeID: node.ID, Name: node.Name + " · " + app.Service, Kind: "deploy", Service: app.Service, Status: "pending"})
				}
			}
		} else {
			run.Steps = append(run.Steps, DeliveryStep{NodeID: node.ID, Name: node.Name, Kind: node.Kind, Status: "pending"})
		}
	}
	execution, _ := json.Marshal(run.Steps)
	secretJSON, _ := json.Marshal(secrets)
	versionEnvironment := ""
	if p.Version != nil {
		versionEnvironment = s.VersionArtifacts(p.Version.ID, p.Environment.Name).Environment
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := checkChangeDeliveryOwner(tx, req, p); err != nil {
		return nil, err
	}
	if err := lockArtifactReferences(tx, planArtifactIDs(p)...); err != nil {
		return nil, err
	}
	result, err := tx.Exec(`INSERT INTO delivery_runs(plan,digest,status,execution,env_secrets,owner,approver,created_at,revision) VALUES(?,?,?,?,?,?,?, ?,1)`, string(planJSON), run.Digest, run.Status, string(execution), string(secretJSON), actor, "", run.CreatedAt)
	if err != nil {
		return nil, err
	}
	run.ID, _ = result.LastInsertId()
	if nonce != "" {
		if _, err = tx.Exec(`INSERT INTO delivery_preflight_uses(nonce,run_id) VALUES(?,?)`, nonce, run.ID); err != nil {
			tx.Rollback()
			var used int64
			if s.db.QueryRow(`SELECT run_id FROM delivery_preflight_uses WHERE nonce=?`, nonce).Scan(&used) == nil {
				return s.DeliveryRun(used), nil
			}
			return nil, err
		}
	}
	if p.Version != nil {
		table, where, args := versionArtifactStorage(p.Version.ID, versionEnvironment)
		var document string
		if tx.QueryRow(`SELECT document FROM `+table+` WHERE `+where, args...).Scan(&document) == nil {
			var pinned VersionArtifacts
			if json.Unmarshal([]byte(document), &pinned) != nil || pinned.ScopeDigest != versionCodeScope(p.Version) {
				return nil, fmt.Errorf("版本制品范围已变化，请重新预检")
			}
			for _, b := range pinned.Bindings {
				matches := false
				for _, app := range p.Services {
					matches = matches || (app.Service == b.Service && app.Artifact.ID == b.ArtifactID && app.Artifact.Digest == b.Digest)
				}
				if !matches {
					return nil, fmt.Errorf("版本制品关联已变化，请重新预检")
				}
			}
			if _, err = tx.Exec(`UPDATE `+table+` SET used=1 WHERE `+where+` AND document=?`, append(args, document)...); err != nil {
				return nil, err
			}
			var current string
			var locked int
			if err = tx.QueryRow(`SELECT document,used FROM `+table+` WHERE `+where, args...).Scan(&current, &locked); err != nil || current != document || locked != 1 {
				return nil, fmt.Errorf("版本制品关联已变化，请重新预检")
			}
		} else if nonce != "" && len(p.Services) > 0 {
			return nil, fmt.Errorf("请先关联版本制品，再重新预检")
		}
		if err != nil {
			return nil, err
		}
	}
	if req.changeDeploymentID != "" {
		if _, err = tx.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?)`, "change_delivery:"+req.changeDeploymentID, fmt.Sprint(run.ID)); err != nil {
			return nil, err
		}
	}
	if req.publication != nil {
		if err = req.publication.insert(tx, run, nonce); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.Audit(actor, "create", "delivery-run", fmt.Sprint(run.ID), map[string]any{"digest": run.Digest, "environment": req.Environment})
	return run, nil
}

const deliveryRunSelect = `SELECT id,plan,digest,status,execution,env_secrets,owner,approver,created_at,revision FROM delivery_runs`

func scanDeliveryRun(row rowScanner) (*DeliveryRun, error) {
	v := &DeliveryRun{}
	var plan, steps, secrets string
	if err := row.Scan(&v.ID, &plan, &v.Digest, &v.Status, &steps, &secrets, &v.Owner, &v.Approver, &v.CreatedAt, &v.Revision); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(plan), &v.Plan); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(steps), &v.Steps); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(secrets), &v.envSecrets); err != nil {
		return nil, err
	}
	return v, nil
}

// Execution uses the persisted revision; presentation may derive child/completed state.
func (s *Store) deliveryRunLedger(id int64) *DeliveryRun {
	v, err := scanDeliveryRun(s.db.QueryRow(deliveryRunSelect+` WHERE id=?`, id))
	if err != nil {
		return nil
	}
	return v
}
func (s *Store) DeliveryRun(id int64) *DeliveryRun {
	v := s.deliveryRunLedger(id)
	s.loadSQLStageBaselines(v)
	s.projectResearchCodeState(v)
	return v
}
func (s *Store) DeliveryRuns() []DeliveryRun {
	rows, err := s.db.Query(deliveryRunSelect + ` ORDER BY id DESC LIMIT 100`)
	if err != nil {
		return []DeliveryRun{}
	}
	out := []DeliveryRun{}
	for rows.Next() {
		if v, e := scanDeliveryRun(rows); e == nil {
			out = append(out, *v)
		}
	}
	rows.Close()
	for i := range out {
		s.projectResearchCodeState(&out[i])
	}
	return out
}
func (s *Store) saveDeliveryRun(v *DeliveryRun) error {
	execution, _ := json.Marshal(v.Steps)
	res, err := s.db.Exec(`UPDATE delivery_runs SET status=?,execution=?,approver=?,revision=revision+1 WHERE id=? AND revision=?`, v.Status, string(execution), v.Approver, v.ID, v.Revision)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrRunConflict
	}
	v.Revision++
	return nil
}
func (s *Store) ReviewDeliveryRun(id int64, actor, digest string, approve bool) (*DeliveryRun, error) {
	v := s.DeliveryRun(id)
	if v != nil && v.Plan.Template.Workbench == "research" {
		return nil, fmt.Errorf("请通过版本准备流程的当前任务处理")
	}
	if v != nil && hasApprovalNodes(&v.Plan) {
		return nil, fmt.Errorf("分阶段审批必须指定当前审核节点")
	}
	if v == nil || v.Status != "pending_approval" || digest != v.Digest {
		return nil, fmt.Errorf("审核单状态或内容摘要已改变")
	}
	env := s.GetEnvironment(v.Plan.Environment.Name)
	if env == nil || actor == v.Owner || !allowedApprover(v.Plan.Environment.Approvers, actor) || !allowedApprover(env.Approvers, actor) {
		return nil, fmt.Errorf("仅指定发布审核人可审核，且不能审核自己的发布")
	}
	v.Approver, v.Status = actor, "rejected"
	if approve {
		v.Status = "approved"
	}
	if err := s.saveDeliveryRun(v); err != nil {
		return nil, err
	}
	s.Audit(actor, v.Status, "delivery-run", fmt.Sprint(id), map[string]any{"digest": v.Digest})
	return v, nil
}

func (s *Store) AdvanceDeliveryRun(id int64, actor string, confirmManual bool, manager *ReleaseManager, comments ...string) (*DeliveryRun, error) {
	return s.advanceDeliveryRun(id, actor, confirmManual, manager, func(childID int64) { go manager.startNextBatch(childID) }, comments...)
}

func (s *Store) advanceDeliveryRun(id int64, actor string, confirmManual bool, manager *ReleaseManager, launch func(int64), comments ...string) (*DeliveryRun, error) {
	return s.advanceDeliveryRunState(s.deliveryRunLedger(id), actor, confirmManual, manager, launch, nil, comments...)
}

func (s *Store) validateDeliveryExecution(v *DeliveryRun) (*Environment, error) {
	env := s.GetEnvironment(v.Plan.Environment.Name)
	if err := s.checkDeliveryPolicy(&v.Plan); err != nil {
		return nil, err
	}
	if hasApprovalNodes(&v.Plan) {
		if env == nil {
			return nil, fmt.Errorf("发布环境不存在")
		}
		if err := s.checkRunApprovals(v, env); err != nil {
			return nil, err
		}
	}
	if env == nil || (env.Protected && (v.Approver == "" || !allowedApprover(env.Approvers, v.Approver))) {
		return nil, fmt.Errorf("环境保护策略已变化，请重新提交审核")
	}
	requiresReview := deliveryReviewRequired(&v.Plan)
	if v.Plan.Template.Workbench != "" {
		policy, err := s.workbenchPolicy(v.Plan.Template.Workbench, env.Name)
		if err != nil {
			return nil, err
		}
		requiresReview = requiresReview || policy.ChangeApproval || env.Protected
	}
	if requiresReview && !hasApprovalNodes(&v.Plan) && (v.Approver == "" || v.Approver == v.Owner || !allowedApprover(env.Approvers, v.Approver) || !allowedApprover(v.Plan.Environment.Approvers, v.Approver)) {
		return nil, fmt.Errorf("SQL/Redis 工作台操作必须经另一名环境审核人审核，且审核人仍须有权限")
	}
	if env.RequireReason && v.Plan.Reason == "" {
		return nil, fmt.Errorf("环境已要求发布原因，请重新提交执行单")
	}
	// Check ALL application artifacts before any SQL/config/script side effect,
	// not merely when eventually reaching the application's deploy node.
	for _, app := range v.Plan.Services {
		if err := s.artifactBuildComplete(s.GetArtifact(app.Artifact.ID)); err != nil {
			return nil, err
		}
		if err := s.validateArtifactBuildVariant(s.GetArtifact(app.Artifact.ID), env.Name); err != nil {
			return nil, err
		}
	}
	return env, nil
}

func (s *Store) advanceDeliveryRunState(v *DeliveryRun, actor string, confirmManual bool, manager *ReleaseManager, launch func(int64), retry *deliveryDeploymentRetry, comments ...string) (*DeliveryRun, error) {
	if v != nil && v.Plan.Template.Workbench == "research" {
		return nil, fmt.Errorf("版本准备通过研发工作台推进，不使用部署执行器")
	}
	if v == nil || (v.Status != "approved" && v.Status != "running") {
		return nil, fmt.Errorf("仅已审核或执行中的编排可以继续")
	}
	env, err := s.validateDeliveryExecution(v)
	if err != nil {
		return nil, err
	}
	id := v.ID
	for i := range v.Steps {
		step := &v.Steps[i]
		if step.Status == "success" || step.Status == "skipped" {
			continue
		}
		if step.Kind == "deploy" && step.Status == "failed" && step.ReleaseID > 0 {
			return nil, fmt.Errorf("当前应用发布失败，请先重试发布或记录人工处理结果，再继续后续步骤")
		}
		if step.StartedAt == 0 {
			step.StartedAt = time.Now().Unix()
		}
		if step.Kind == "extension" {
			for _, node := range v.Plan.Nodes {
				if node.ID == step.NodeID {
					return v, s.advanceWorkflowExtension(v, step, node, actor)
				}
			}
			return nil, fmt.Errorf("插件节点快照不存在")
		}
		if step.Kind == "script" || step.Kind == "verify" {
			for _, n := range v.Plan.Nodes {
				if n.ID == step.NodeID {
					err := s.advanceOperation(v, step, n, actor)
					return v, err
				}
			}
			return nil, fmt.Errorf("执行节点快照不存在")
		}
		if step.Kind == "redis" {
			if step.Status == "executing" {
				return nil, fmt.Errorf("Redis 节点正在执行或中断待核对，禁止自动重试")
			}
			var plan *RedisExecutionPlan
			for _, candidate := range v.Plan.RedisPlans {
				if candidate.NodeID == step.NodeID {
					plan = candidate
					break
				}
			}
			if plan == nil {
				return nil, fmt.Errorf("Redis 节点缺少已审核快照")
			}
			step.Status, v.Status = "executing", "running"
			if err := s.saveDeliveryRun(v); err != nil {
				return nil, err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			detail, err := s.executeRedisNode(ctx, v, plan)
			cancel()
			step.Detail = detail
			step.EndedAt = time.Now().Unix()
			if err != nil {
				step.Status, v.Status = "failed", "failed"
				if detail == "" {
					step.Detail = err.Error()
				}
			} else {
				step.Status = "success"
			}
			if err := s.saveDeliveryRun(v); err != nil {
				return nil, err
			}
			s.Audit(actor, "execute-redis", "delivery-run", fmt.Sprint(id), map[string]any{"node": step.NodeID, "status": step.Status, "digest": v.Digest})
			return v, nil
		}
		if step.Kind == "nacos" {
			if step.Status == "executing" {
				return nil, fmt.Errorf("Nacos 节点正在执行或中断待核对，禁止自动重试")
			}
			var plan *NacosExecutionPlan
			for _, candidate := range v.Plan.NacosPlans {
				if candidate.NodeID == step.NodeID {
					plan = candidate
					break
				}
			}
			if plan == nil {
				return nil, fmt.Errorf("Nacos 节点缺少已审核快照")
			}
			step.Status, v.Status = "executing", "running"
			if err := s.saveDeliveryRun(v); err != nil {
				return nil, err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			detail, err := s.executeNacosNode(ctx, v, plan)
			cancel()
			step.Detail = detail
			step.EndedAt = time.Now().Unix()
			if err != nil {
				step.Status, v.Status = "failed", "failed"
				if detail == "" {
					step.Detail = err.Error()
				}
			} else {
				step.Status = "success"
			}
			if err := s.saveDeliveryRun(v); err != nil {
				return nil, err
			}
			s.Audit(actor, "execute-nacos", "delivery-run", fmt.Sprint(id), map[string]any{"node": step.NodeID, "status": step.Status, "digest": v.Digest})
			return v, nil
		}
		if step.Kind == "mysql" {
			if step.Status == "executing" {
				return nil, fmt.Errorf("SQL 节点正在执行或上次执行中断，请检查执行台账；禁止自动重复执行")
			}
			var plan *SQLExecutionPlan
			for _, candidate := range v.Plan.SQLPlans {
				if candidate.NodeID == step.NodeID {
					plan = candidate
					break
				}
			}
			if plan == nil {
				return nil, fmt.Errorf("SQL 节点缺少已审核执行计划")
			}
			step.Status, v.Status = "executing", "running"
			if err := s.saveDeliveryRun(v); err != nil {
				return nil, err
			}
			ctx, cancel := sqlExecutionContext()
			detail, err := s.executeSQLNode(ctx, v, plan)
			cancel()
			step.Detail = detail
			step.EndedAt = time.Now().Unix()
			if err != nil {
				step.Status, v.Status = "failed", "failed"
				if step.Detail == "" {
					step.Detail = err.Error()
				}
			} else {
				step.Status = "success"
			}
			if saveErr := s.saveDeliveryRun(v); saveErr != nil {
				return nil, saveErr
			}
			s.Audit(actor, "execute-sql", "delivery-run", fmt.Sprint(id), map[string]any{"node": step.NodeID, "status": step.Status, "digest": v.Digest})
			return v, nil
		}
		if step.ReleaseID > 0 {
			child := s.GetReleaseOrder(step.ReleaseID)
			if child == nil {
				return nil, fmt.Errorf("子发布单不存在")
			}
			if child.Status == "success" {
				step.Status = "success"
				step.EndedAt = time.Now().Unix()
			} else if !releaseStatusActive(child.Status) {
				step.Status, v.Status = "failed", "failed"
				if child.Status == "failed" {
					v.Status = "running"
				}
				step.EndedAt = time.Now().Unix()
			} else {
				return nil, fmt.Errorf("当前应用尚未发布完成；分批暂停请在子发布单中继续")
			}
			if err := s.saveDeliveryRun(v); err != nil {
				return nil, err
			}
			return v, nil
		}
		if step.Kind == "manual" {
			if !confirmManual {
				return nil, fmt.Errorf("请完成说明中的操作后显式确认人工节点")
			}
			comment := ""
			if len(comments) > 0 {
				comment = strings.TrimSpace(comments[0])
			}
			if len(comment) > 8000 {
				return nil, fmt.Errorf("处理说明不能超过 8000 字节")
			}
			step.Status, v.Status = "success", "running"
			step.EndedAt = time.Now().Unix()
			step.Reviews = append(step.Reviews, ApprovalDecision{Actor: actor, Approved: true, Comment: comment, At: step.EndedAt})
			if err := s.saveDeliveryRun(v); err != nil {
				return nil, err
			}
			s.Audit(actor, "confirm-manual", "delivery-run", fmt.Sprint(id), map[string]any{"node": step.NodeID, "digest": v.Digest})
			return v, nil
		}
		var app *DeliveryService
		for j := range v.Plan.Services {
			if v.Plan.Services[j].Service == step.Service {
				app = &v.Plan.Services[j]
				break
			}
		}
		if step.Kind != "deploy" || app == nil {
			return nil, fmt.Errorf("节点没有可用执行器")
		}
		currentArtifact := s.GetArtifact(app.Artifact.ID)
		currentService := s.GetService(app.Service)
		if currentArtifact == nil || currentService == nil || currentArtifact.Digest != app.Artifact.Digest || currentArtifact.Service != app.Service || !s.artifactMatchesEnvironment(currentArtifact, currentService, env.Name) {
			return nil, fmt.Errorf("已审核制品不存在或摘要发生变化，请重新提交")
		}
		currentTarget, err := manager.ValidateTarget(app.Service, env.Name, v.Plan.Reason)
		if e := s.validateArtifactBuildVariant(currentArtifact, env.Name); e != nil {
			return nil, e
		}
		if err != nil {
			return nil, err
		}
		currentJSON, _ := json.Marshal(currentTarget)
		approvedJSON, _ := json.Marshal(app.Target)
		if string(currentJSON) != string(approvedJSON) || currentTarget.runtimeEnvEncrypted != v.envSecrets[app.Service] {
			return nil, fmt.Errorf("应用环境配置或环境变量已变化，请重新生成执行单并审核")
		}
		destination, err := s.deliveryDestinationDigest(*currentTarget)
		if err != nil || destination != app.DestinationDigest {
			return nil, fmt.Errorf("主机或云资源的执行地址已变化，请重新生成执行单并审核")
		}
		// Reserve the child and update the run in ONE transaction. A competing
		// request loses the revision CAS and cannot launch a duplicate release.
		target := app.Target
		batch := target.BatchSize
		if batch < 1 {
			batch = target.MaxParallel
		}
		if batch < 1 {
			batch = 1
		}
		targets := target.ExecutionTargets()
		if retry != nil {
			targets = retry.Targets
		}
		hosts, _ := json.Marshal(targets)
		snapshot, _ := json.Marshal(target)
		tx, err := s.db.Begin()
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		if err := lockReleaseCounter(tx, app.Service); err != nil {
			return nil, err
		}
		if err := lockReleaseTarget(tx, app.Service, env.Name); err != nil {
			return nil, err
		}
		if retry != nil {
			if err := validateRetryReleaseTx(tx, retry.ReleaseID, "failed"); err != nil {
				return nil, err
			}
		}
		result, err := tx.Exec(`INSERT INTO release_orders(service,artifact_id,version,environment,reason,strategy,status,batch_size,pause_between_batches,failure_threshold,hosts,target_snapshot,runtime_env_encrypted,next_index,total,succeeded,failed,actor,approver,started_at) VALUES(?,?,?,?,?,?,'queued',?,?,?,?,?,?,0,?,0,0,?,?,?)`, app.Service, app.Artifact.ID, app.Artifact.Version, env.Name, v.Plan.Reason, target.Strategy, batch, target.PauseBetweenBatches, target.FailureThreshold, string(hosts), string(snapshot), v.envSecrets[app.Service], len(targets), v.Owner, v.Approver, time.Now().Unix())
		if err != nil {
			return nil, err
		}
		childID, err := result.LastInsertId()
		if err != nil {
			return nil, err
		}
		number, err := advanceReleaseCounter(tx, app.Service)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`INSERT INTO release_order_numbers(release_id,service,number) VALUES(?,?,?)`, childID, app.Service, number); err != nil {
			return nil, err
		}
		if target.ManualTarget() {
			target.runtimeEnvEncrypted = v.envSecrets[app.Service]
			child := &ReleaseOrder{ID: childID, Target: target, Actor: v.Owner}
			if err := initManualPublication(tx, child, &app.Artifact); err != nil {
				return nil, err
			}
			if _, err := tx.Exec(`UPDATE release_orders SET status='waiting_manual',total=1 WHERE id=?`, childID); err != nil {
				return nil, err
			}
		}
		if retry != nil {
			step.PreviousReleaseIDs = append(step.PreviousReleaseIDs, retry.ReleaseID)
			step.StartedAt = time.Now().Unix()
			step.EndedAt = 0
			step.Detail = ""
		}
		step.ReleaseID, step.Status, v.Status = childID, "waiting", "running"
		execution, _ := json.Marshal(v.Steps)
		result, err = tx.Exec(`UPDATE delivery_runs SET status=?,execution=?,revision=revision+1 WHERE id=? AND revision=?`, v.Status, string(execution), id, v.Revision)
		if err != nil {
			return nil, err
		}
		if count, _ := result.RowsAffected(); count != 1 {
			return nil, ErrRunConflict
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		v.Revision++
		s.Audit(actor, "execute", "delivery-run", fmt.Sprint(id), map[string]any{"release_id": childID, "digest": v.Digest})
		if !target.ManualTarget() {
			launch(childID)
		}
		return v, nil
	}
	v.Status = "success"
	if err := s.saveDeliveryRun(v); err != nil {
		return nil, err
	}
	return v, nil
}

func registerDeliveryRoutes(mux *http.ServeMux, st *Store, manager *ReleaseManager) {
	registerPublicationRoutes(mux, st, manager)
	registerDeliveryDeploymentRecoveryRoutes(mux, st, manager)
	registerOperationRoutes(mux, st)
	registerDeliveryExperienceRoutes(mux, st, manager)
	mux.HandleFunc("POST /api/delivery/work-item-scope", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			IDs []int64 `json:"work_item_ids"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
			deliveryWriteError(w, 400, err)
			return
		}
		p, err := st.resolveWorkItemScope(req.IDs)
		if err != nil {
			deliveryWriteError(w, 400, err)
			return
		}
		deliveryJSON(w, p)
	})
	actor := func(r *http.Request) string {
		if v := strings.TrimSpace(r.Header.Get("X-User")); v != "" {
			return v
		}
		return "local"
	}
	manage := func(r *http.Request, owner string) bool {
		return actor(r) == "local" || actor(r) == owner || r.Header.Get("X-Role") == "admin"
	}
	id := func(r *http.Request) int64 { v, _ := strconv.ParseInt(r.PathValue("id"), 10, 64); return v }
	reply := func(w http.ResponseWriter, v any, err error) {
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(v)
	}
	decode := func(w http.ResponseWriter, r *http.Request, v any) error {
		return json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(v)
	}
	st.registerStandardDeliveryAPI(mux, reply, actor)
	mux.HandleFunc("GET /api/delivery/templates", func(w http.ResponseWriter, r *http.Request) {
		out := []DeliveryTemplate{}
		for _, t := range st.DeliveryTemplates() {
			if t.Workbench == "" {
				out = append(out, t)
			}
		}
		reply(w, out, nil)
	})
	saveTemplate := func(w http.ResponseWriter, r *http.Request) {
		var v DeliveryTemplate
		if err := decode(w, r, &v); err != nil {
			reply(w, nil, err)
			return
		}
		v.ID, v.Owner = id(r), actor(r)
		if v.Workbench == "research" {
			reply(w, nil, fmt.Errorf("独立准备流程已移除，请在部署编排配置准备阶段"))
			return
		}
		if hasOperations(&v) && r.Header.Get("X-Role") != "admin" {
			reply(w, nil, fmt.Errorf("验证、脚本和监控地址须由管理员维护"))
			return
		}
		if v.ID > 0 {
			old := st.DeliveryTemplate(v.ID)
			if old == nil || !manage(r, old.Owner) {
				reply(w, nil, fmt.Errorf("仅创建人或管理员可修改模板"))
				return
			}
			v.Owner = old.Owner
		}
		err := st.SaveDeliveryTemplate(&v)
		reply(w, v, err)
	}
	mux.HandleFunc("POST /api/delivery/templates", saveTemplate)
	mux.HandleFunc("PUT /api/delivery/templates/{id}", saveTemplate)
	mux.HandleFunc("POST /api/delivery/preview", func(w http.ResponseWriter, r *http.Request) {
		var req DeliveryRequest
		if err := decode(w, r, &req); err != nil {
			reply(w, nil, err)
			return
		}
		req.previewDrafts = true
		req.reviewerDirectory = r.Header.Get("X-Reviewer-Directory")
		p, secrets, err := st.ResolveDelivery(req, manager)
		if p != nil {
			if e := validateDeliveryReviewerDirectory(p, req.reviewerDirectory, actor(r)); e != nil {
				p.Blockers = append(p.Blockers, e.Error())
			}
			if e := validateRunApprovals(p, actor(r)); e != nil {
				p.Blockers = append(p.Blockers, e.Error())
			}
			if len(p.Blockers) == 0 {
				p.PreflightToken, p.PreflightExpires = st.signDeliveryPreflight(req, p, secrets, actor(r))
			}
			p.NacosPlans = publicNacosPlans(p.NacosPlans)
		}
		reply(w, p, err)
	})
	mux.HandleFunc("GET /api/delivery/runs", func(w http.ResponseWriter, r *http.Request) { reply(w, st.DeliveryRuns(), nil) })
	mux.HandleFunc("POST /api/delivery/runs", func(w http.ResponseWriter, r *http.Request) {
		var req DeliveryRequest
		if err := decode(w, r, &req); err != nil {
			reply(w, nil, err)
			return
		}
		if tpl := st.DeliveryTemplate(req.TemplateID); req.PreflightToken == "" && (req.StrategyKind == "standard" || tpl != nil && tpl.Workbench == "") {
			reply(w, nil, fmt.Errorf("请先完成本次预检，提交必须使用预检凭证"))
			return
		}
		req.reviewerDirectory = r.Header.Get("X-Reviewer-Directory")
		v, err := st.CreateDeliveryRun(req, actor(r), manager)
		reply(w, v, err)
	})
	mux.HandleFunc("GET /api/delivery/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		v := st.DeliveryRun(id(r))
		if v == nil {
			reply(w, nil, fmt.Errorf("编排执行单不存在"))
			return
		}
		reply(w, v, nil)
	})
	for _, action := range []string{"approve", "reject", "advance", "cancel"} {
		action := action
		mux.HandleFunc("POST /api/delivery/runs/{id}/"+action, func(w http.ResponseWriter, r *http.Request) {
			v := st.DeliveryRun(id(r))
			if v == nil {
				reply(w, nil, fmt.Errorf("编排执行单不存在"))
				return
			}
			if action == "approve" || action == "advance" {
				if err := validateDeliveryReviewerDirectory(&v.Plan, r.Header.Get("X-Reviewer-Directory"), v.Owner); err != nil {
					reply(w, nil, err)
					return
				}
			}
			var req struct {
				Digest        string `json:"digest"`
				ConfirmManual bool   `json:"confirm_manual"`
				NodeID        string `json:"node_id"`
				Comment       string `json:"comment"`
			}
			if err := decode(w, r, &req); err != nil {
				reply(w, nil, err)
				return
			}
			var err error
			if action == "approve" || action == "reject" {
				if hasApprovalNodes(&v.Plan) {
					v, err = st.ReviewDeliveryNode(v.ID, actor(r), req.Digest, req.NodeID, req.Comment, action == "approve")
				} else {
					v, err = st.ReviewDeliveryRun(v.ID, actor(r), req.Digest, action == "approve")
				}
			} else {
				if !manage(r, v.Owner) {
					reply(w, nil, fmt.Errorf("仅发起人或管理员可执行或取消"))
					return
				}
				if action == "advance" {
					if step, node := researchNextNode(v); node != nil && step.Kind == "manual" && (req.Digest != v.Digest || req.NodeID != node.ID) {
						reply(w, nil, fmt.Errorf("人工处理节点已变化，请刷新后核对；不会代替确认下一项"))
						return
					}
					control := st.DeliveryControl(v.ID)
					if control.State == "running" || control.State == "pausing" {
						reply(w, nil, fmt.Errorf("自动协调器运行中，请先暂停后续步骤"))
						return
					}
					v, err = st.AdvanceDeliveryRun(v.ID, actor(r), req.ConfirmManual, manager, req.Comment)
				} else {
					if v.Status != "pending_approval" && v.Status != "approved" {
						reply(w, nil, fmt.Errorf("仅未执行的编排可取消；运行中的子发布单请单独停止"))
						return
					}
					v.Status = "canceled"
					err = st.saveDeliveryRun(v)
				}
			}
			reply(w, v, err)
		})
	}
}
