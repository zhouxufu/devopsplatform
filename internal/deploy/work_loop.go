package deploy

// Work loops own authorization and coordination, never a second execution engine.
// Each dispatch is driven by a freshly authenticated page/MCP request. In-flight
// engine jobs continue independently; absence of a client cannot extend authority.
import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"logplatform/pkg/workflownode"
)

type WorkLoopSpec struct {
	WorkItemID    int64    `json:"work_item_id"`
	Environment   string   `json:"environment"`
	Applications  []string `json:"applications"`
	ExpiresAt     int64    `json:"expires_at"`
	MaxIterations int      `json:"max_iterations"`
	Acceptance    string   `json:"acceptance"`
	CompleteScope bool     `json:"complete_scope"`
	Resolution    string   `json:"resolution,omitempty"`
	ResolvedBuild string   `json:"resolved_build,omitempty"`
	AssignedTo    string   `json:"assigned_to,omitempty"`
}
type WorkLoopGrant struct {
	Spec           WorkLoopSpec            `json:"spec"`
	WorkflowDigest string                  `json:"workflow_digest"`
	TemplateID     int64                   `json:"template_id"`
	Policies       map[string]any          `json:"policies"`
	Regression     []workflownode.TestTask `json:"regression"`
	Source         map[string]any          `json:"source"`
	Digest         string                  `json:"digest"`
}
type WorkLoopEvent struct {
	At      int64  `json:"at"`
	State   string `json:"state"`
	Message string `json:"message"`
}
type WorkLoopIteration struct {
	GrantDigest     string              `json:"grant_digest"`
	Number          int                 `json:"number"`
	ChangeIDs       []int64             `json:"change_ids"`
	ChangesDigest   string              `json:"changes_digest"`
	PreparationID   int64               `json:"preparation_id,omitempty"`
	DeliveryID      int64               `json:"delivery_id,omitempty"`
	CodeID          string              `json:"code_id,omitempty"`
	Summary         string              `json:"summary"`
	Reports         []workflowAPIReport `json:"reports,omitempty"`
	SourceOperation string              `json:"source_operation,omitempty"`
	State           string              `json:"state"`
}
type WorkLoopIntent struct {
	Name   string          `json:"name"`
	Path   string          `json:"path"`
	Body   json.RawMessage `json:"body"`
	State  string          `json:"state"`
	Result json.RawMessage `json:"result,omitempty"`
}
type WorkLoop struct {
	Authority      string              `json:"authority,omitempty"`
	AuthorityKeyID string              `json:"authority_key_id,omitempty"`
	Authorizations []WorkLoopGrant     `json:"authorizations,omitempty"`
	ID             string              `json:"id"`
	Owner          string              `json:"owner"`
	Revision       int64               `json:"revision"`
	Grant          WorkLoopGrant       `json:"grant"`
	State          string              `json:"state"`
	Stage          string              `json:"stage"`
	Message        string              `json:"message"`
	Paused         bool                `json:"paused"`
	Revoked        bool                `json:"revoked"`
	Claimant       string              `json:"claimant,omitempty"`
	Iterations     []WorkLoopIteration `json:"iterations"`
	Events         []WorkLoopEvent     `json:"events"`
	Intent         *WorkLoopIntent     `json:"intent,omitempty"`
	UpdatedAt      int64               `json:"updated_at"`
	raw            string
}

func (s *Store) workLoop(id string) (*WorkLoop, error) {
	if !attachmentIDRE.MatchString(id) {
		return nil, fmt.Errorf("跟进任务编号无效")
	}
	var raw string
	if e := s.db.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, "work_loop:"+id).Scan(&raw); e != nil {
		return nil, fmt.Errorf("跟进任务不存在")
	}
	var t WorkLoop
	if e := json.Unmarshal([]byte(raw), &t); e != nil {
		return nil, e
	}
	t.raw = raw
	return &t, nil
}
func (s *Store) saveWorkLoop(t *WorkLoop) error {
	t.Revision++
	t.UpdatedAt = time.Now().Unix()
	b, e := json.Marshal(t)
	if e != nil {
		return e
	}
	if t.raw == "" {
		_, e = s.db.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?)`, "work_loop:"+t.ID, string(b))
	} else {
		r, err := s.db.Exec(`UPDATE deploy_meta SET value=? WHERE key=? AND value=?`, string(b), "work_loop:"+t.ID, t.raw)
		e = err
		if e == nil {
			n, _ := r.RowsAffected()
			if n != 1 {
				e = ErrRunConflict
			}
		}
	}
	if e == nil {
		t.raw = string(b)
	}
	return e
}
func (t *WorkLoop) current() *WorkLoopIteration { return &t.Iterations[len(t.Iterations)-1] }
func (t *WorkLoop) setState(state, stage, message string) {
	if t.State != state || t.Stage != stage || t.Message != message {
		t.Events = append(t.Events, WorkLoopEvent{time.Now().Unix(), state, message})
	}
	t.State, t.Stage, t.Message = state, stage, message
}
func (s *Store) workLoopSource(ctx context.Context, w *WorkItem, actor string, spec WorkLoopSpec) (map[string]any, error) {
	if w == nil || (w.Kind != "bug" && w.Kind != "requirement") {
		return nil, fmt.Errorf("仅支持需求和 Bug")
	}
	if e := s.canReadResearchItem(ctx, actor, w); e != nil {
		return nil, e
	}
	if w.Source == "manual" {
		if actor != w.Owner && actor != w.AssignedTo {
			return nil, fmt.Errorf("仅工作项创建人或负责人可授权自动跟进")
		}
		label := "已解决 · 待测试关闭"
		if w.Kind == "requirement" {
			label = "开发完成 · 待验收"
		}
		return map[string]any{"kind": w.Kind, "source": w.Source, "action": "resolved", "label": label}, nil
	}
	z, _, e := s.researchClient(ctx, w, actor)
	if e != nil {
		return nil, e
	}
	if w.Kind == "requirement" {
		remote, e := z.researchReadItem(ctx, w, "edit")
		if e != nil {
			return nil, e
		}
		label := requirementDevelopedLabel(remote)
		if label == "" {
			return nil, fmt.Errorf("来源需求编辑页未提供开发完成阶段，不能自动回写；请核对来源版本与权限")
		}
		return map[string]any{"kind": w.Kind, "source": w.Source, "action": "develop", "stage": "developed", "label": label + " · 待人工验收"}, nil
	}
	remote, e := z.researchRead(ctx, w.ExternalID, "resolve")
	if e != nil {
		return nil, e
	}
	z.enrichResearchResolution(ctx, s.GetConnection(strings.TrimPrefix(w.Source, "zentao:")), remote)
	options := remote.Resolutions
	if len(options) == 0 {
		options = remote.ResolutionList
	}
	if spec.Resolution == "" || options[spec.Resolution] == "" {
		return nil, fmt.Errorf("请选择来源提供的解决方案；可选项：%v", options)
	}
	if e := validateResearchAction(ResearchAction{OperationID: strings.Repeat("a", 32), Action: "resolve", Resolution: spec.Resolution, ResolvedBuild: spec.ResolvedBuild}); e != nil {
		return nil, e
	}
	if spec.ResolvedBuild != "" && remote.Builds[spec.ResolvedBuild] == "" {
		return nil, fmt.Errorf("请选择来源允许的解决版本")
	}
	if spec.AssignedTo != "" && remote.Users[spec.AssignedTo] == "" {
		return nil, fmt.Errorf("请选择来源允许的接收人")
	}
	return map[string]any{"kind": w.Kind, "source": w.Source, "action": "resolve", "resolution": spec.Resolution, "label": "已解决 · 待测试关闭"}, nil
}
func (s *Store) previewWorkLoop(ctx context.Context, spec WorkLoopSpec, actor string) (*WorkLoopGrant, error) {
	if actor == "" || actor == "local" {
		return nil, fmt.Errorf("请登录本人账号后授权任务")
	}
	if spec.ExpiresAt <= time.Now().Unix() || spec.ExpiresAt > time.Now().Add(7*24*time.Hour).Unix() || spec.MaxIterations < 1 || spec.MaxIterations > 10 {
		return nil, fmt.Errorf("授权期限最多 7 天，开发轮次为 1–10")
	}
	if len(spec.Applications) == 0 || len(spec.Applications) > 10 || strings.TrimSpace(spec.Acceptance) == "" || len(spec.Acceptance) > 4000 {
		return nil, fmt.Errorf("请选择 1–10 个应用并填写验收条件")
	}
	slices.Sort(spec.Applications)
	spec.Applications = slices.Compact(spec.Applications)
	if e := mcpDeliveryEnvironment(s, spec.Environment); e != nil {
		return nil, e
	}
	env := s.GetEnvironment(spec.Environment)
	if env.Purpose != "development" && env.Purpose != "test" {
		return nil, fmt.Errorf("仅开发或测试用途环境可自动跟进")
	}
	w := s.GetWorkItem(spec.WorkItemID)
	source, e := s.workLoopSource(ctx, w, actor, spec)
	if e != nil {
		return nil, e
	}
	p, e := s.deliveryPolicy(spec.Environment)
	if e != nil {
		return nil, e
	}
	template := s.DeliveryTemplate(p.WorkflowTemplateID)
	if template == nil || template.Workbench != "" || template.Mode != "version" || len(template.Preparation) == 0 {
		return nil, fmt.Errorf("环境须绑定包含准备阶段的统一交付流程")
	}
	// Regression must run after deployment, and must cover every authorized app.
	deployed := false
	covered := map[string]bool{}
	for _, n := range template.Nodes {
		if n.Kind == "deploy" {
			deployed = true
		}
		if deployed && n.Extension != nil && n.Extension.Provider == workflownode.APITestProvider {
			for _, app := range spec.Applications {
				if n.Service == "" || n.Service == app {
					covered[app] = true
				}
			}
		}
	}
	g := &WorkLoopGrant{Spec: spec, TemplateID: template.ID, WorkflowDigest: digestJSON(template), Policies: map[string]any{}, Source: source}
	apps := []DeliveryService{}
	for _, app := range spec.Applications {
		if !covered[app] {
			return nil, fmt.Errorf("应用 %s 缺少部署后的接口回归节点", app)
		}
		policy, e := s.applicationCodePolicy(app, spec.Environment)
		if e != nil {
			return nil, e
		}
		g.Policies[app] = map[string]any{"policy_digest": policy["policy_digest"], "code_policy": policy["code_policy"], "build_variant": policy["build_variant"]}
		apps = append(apps, DeliveryService{Service: app})
	}
	b, e := s.prepareAPITestWorkflow(&workflownode.Binding{Provider: workflownode.APITestProvider, Environment: spec.Environment}, DeliveryNode{}, apps)
	if e != nil {
		return nil, e
	}
	g.Regression = b.Tests
	g.Digest = digestJSON(g)
	return g, nil
}
func (s *Store) validateWorkLoop(ctx context.Context, t *WorkLoop, actor string) error {
	if actor != t.Owner {
		return fmt.Errorf("仅任务授权人可操作；切换页面和 Agent 不改变归属")
	}
	if t.Revoked || t.Paused {
		return fmt.Errorf("任务已暂停或撤销授权，在途执行仍保留原回执")
	}
	g, e := s.previewWorkLoop(ctx, t.Grant.Spec, actor)
	if e != nil {
		return e
	}
	if g.Digest != t.Grant.Digest {
		return fmt.Errorf("应用、目标分支策略、流程或回归要求已变化；请核对差异并重新授权")
	}
	return nil
}

func (s *Store) workLoopMayIterate(t *WorkLoop) bool {
	if t.Intent != nil && t.Intent.State == "dispatching" {
		return false
	}
	if t.State == "awaiting_acceptance" || t.State == "completed" {
		return true
	}
	if t.State != "blocked" {
		return false
	}
	it := t.current()
	if it.DeliveryID > 0 {
		r := s.DeliveryRun(it.DeliveryID)
		if r == nil || r.Status != "failed" && r.Status != "canceled" {
			return false
		}
		for _, step := range r.Steps {
			if step.Status == "executing" {
				return false
			}
			if step.ReleaseID > 0 {
				child := s.GetReleaseOrder(step.ReleaseID)
				if child == nil || releaseStatusActive(child.Status) || child.Status == "needs_review" {
					return false
				}
			}
		}
		return true
	}
	if it.PreparationID > 0 {
		r := s.DeliveryRun(it.PreparationID)
		if r == nil || r.Status != "failed" && r.Status != "canceled" {
			return false
		}
		for _, step := range r.Steps {
			if p := s.CodePreparation(step.Detail); p != nil && s.preparationCancelBlocker(p) != "" {
				return false
			}
		}
		return true
	}
	return false
}
func (s *Store) workLoopChanges(t *WorkLoop, ids []int64) ([]Change, error) {
	content, e := s.WorkDelivery(t.Grant.Spec.WorkItemID)
	if e != nil {
		return nil, e
	}
	if len(ids) == 0 || len(ids) > 100 {
		return nil, fmt.Errorf("请选择本轮变更")
	}
	out := []Change{}
	seen := map[int64]bool{}
	for _, id := range ids {
		if seen[id] {
			return nil, fmt.Errorf("重复变更编号")
		}
		seen[id] = true
		var selected *Change
		for _, c := range content.Changes {
			if c.ID == id {
				copy := c
				selected = &copy
				break
			}
		}
		if selected == nil || selected.Owner != t.Owner || !slices.Contains(t.Grant.Spec.Applications, selected.Service) {
			return nil, fmt.Errorf("变更 #%d 不属于本人工作项或超出授权应用", id)
		}
		if selected.Kind != "code" && selected.Kind != "artifact" {
			return nil, fmt.Errorf("变更 #%d 需要独立资源风险审核，本任务不会自动执行 SQL、配置或脚本", id)
		}
		if selected.Status != ChangeReady {
			return nil, fmt.Errorf("变更 #%d 尚未校验就绪", id)
		}
		out = append(out, *selected)
	}
	return out, nil
}

func (s *Store) saveWorkLoopChanges(t *WorkLoop, content *WorkDeliverySave) error {
	if t.Intent != nil && t.Intent.State == "dispatching" {
		return fmt.Errorf("上次保存结果待核对，请先读取原任务回执")
	}
	for _, c := range content.Changes {
		if c.ID > 0 {
			old := s.GetChange(c.ID)
			if old == nil || old.Owner != t.Owner {
				return fmt.Errorf("不能维护他人的变更")
			}
		}
	}
	b, e := json.Marshal(content)
	if e != nil {
		return e
	}
	key := fmt.Sprintf("work_loop_save:%s:%d", t.ID, t.Revision)
	t.Intent = &WorkLoopIntent{Name: "save_changes", Path: key, State: "dispatching", Body: b}
	if e = s.saveWorkLoop(t); e != nil {
		return e
	}
	content.loopReceiptKey, content.loopReceiptValue = key, digestJSON(json.RawMessage(b))
	if e = s.SaveWorkDelivery(t.Grant.Spec.WorkItemID, content, t.Owner, false); e != nil {
		if !content.loopCommitAttempted {
			t.Intent.State = "rejected"
		}
		_ = s.saveWorkLoop(t)
		return e
	}
	t.Intent.State = "received"
	return s.saveWorkLoop(t)
}

func (s *Store) workLoopCompleteScope(t *WorkLoop, ids []int64) error {
	if !t.Grant.Spec.CompleteScope {
		return nil
	}
	d, e := s.WorkDelivery(t.Grant.Spec.WorkItemID)
	if e != nil {
		return e
	}
	for _, c := range d.Changes {
		if slices.Contains(ids, c.ID) {
			continue
		}
		proven := false
		for _, it := range t.Iterations {
			r := s.DeliveryRun(it.DeliveryID)
			if r == nil || r.Status != "success" || r.Plan.Environment.Name != t.Grant.Spec.Environment || r.Plan.Scope == nil {
				continue
			}
			for _, past := range r.Plan.Scope.Changes {
				if past.ID == c.ID && deliveryWorkProof(past) == deliveryWorkProof(c) {
					proven = true
				}
			}
		}
		if !proven {
			return fmt.Errorf("完整验收范围还包含未交付变更 #%d（%s）；请纳入或改为部分范围授权，不能自动完成工作项", c.ID, c.Title)
		}
	}
	return nil
}
