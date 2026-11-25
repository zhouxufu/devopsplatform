package deploy

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
)

const standardDeliveryVersion = 1

// Stored in deploy_meta so SQLite and MySQL share one representation. Legacy
// templates and submitted plans retain their original interpretation.
type DeliveryPolicy struct {
	WorkflowTemplateID int64            `json:"workflow_template_id,omitempty"`
	HotfixTemplateID   int64            `json:"hotfix_template_id,omitempty"`
	Defaults           DeliveryDefaults `json:"defaults,omitempty"`
	Revision           int64            `json:"revision"`
	Stages             []DeliveryNode   `json:"stages"`
}
type DeliveryPolicySnapshot struct {
	Version     int            `json:"version"`
	Revision    int64          `json:"revision"`
	Environment Environment    `json:"environment"`
	Stages      []DeliveryNode `json:"stages"`
}
type DeliveryDraftPlan struct {
	StepsDigest     string                 `json:"steps_digest"`
	Defaults        DeliveryDefaults       `json:"defaults"`
	StrategyKind    string                 `json:"strategy_kind"`
	StrategyVersion int                    `json:"strategy_version"`
	Template        DeliveryTemplate       `json:"template"`
	Scope           *DeliveryScope         `json:"scope,omitempty"`
	Nodes           []DeliveryNode         `json:"nodes"`
	PolicySnapshot  DeliveryPolicySnapshot `json:"policy_snapshot"`
	PolicyDigest    string                 `json:"policy_digest"`
	Blockers        []string               `json:"blockers"`
	Warnings        []string               `json:"warnings"`
}

func (s *Store) deliveryPolicy(environment string) (DeliveryPolicy, error) {
	p := DeliveryPolicy{Stages: []DeliveryNode{}}
	var raw string
	err := s.db.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, "delivery_policy:"+environment).Scan(&raw)
	if err == nil && json.Unmarshal([]byte(raw), &p) != nil {
		return p, fmt.Errorf("环境交付策略损坏，请联系管理员")
	}
	if err != nil && err != sql.ErrNoRows {
		return p, err
	}
	return p, nil
}
func (s *Store) saveDeliveryPolicy(environment string, p DeliveryPolicy) error {
	env := s.GetEnvironment(environment)
	if env == nil {
		return fmt.Errorf("环境不存在")
	}
	for _, id := range []int64{p.WorkflowTemplateID, p.HotfixTemplateID} {
		if id < 0 {
			return fmt.Errorf("环境绑定的交付流程无效")
		}
		if id > 0 {
			t := s.DeliveryTemplate(id)
			if t == nil || t.Workbench != "" || t.Mode != "version" {
				return fmt.Errorf("环境只能绑定部署编排中的动态范围交付流程")
			}
		}
	}
	if p.HotfixTemplateID > 0 && env.Purpose != "production" {
		return fmt.Errorf("热修复流程只绑定生产用途环境")
	}
	if err := s.validateDeliveryDefaults(p.Defaults); err != nil {
		return err
	}
	if len(p.Stages) > 10 {
		return fmt.Errorf("环境最多配置 10 个审核阶段")
	}
	for i := range p.Stages {
		p.Stages[i].ID = fmt.Sprintf("env-review-%d", i+1)
		p.Stages[i].Kind = "approval"
		p.Stages[i].Service = ""
		p.Stages[i].ChangeIDs = nil
		p.Stages[i].DependsOn = nil
		if strings.TrimSpace(p.Stages[i].Name) == "" {
			return fmt.Errorf("审核阶段名称必填")
		}
		if err := validateApprovalNode(p.Stages[i]); err != nil {
			return err
		}
		for _, a := range p.Stages[i].Approvers {
			if !allowedApprover(env.Approvers, a) {
				return fmt.Errorf("审核人员 %s 不在环境审核名单中", a)
			}
		}
	}
	current, err := s.deliveryPolicy(environment)
	if err != nil {
		return err
	}
	if current.Revision != p.Revision {
		return fmt.Errorf("环境策略已改变，请重新载入")
	}
	p.Revision++
	raw, _ := json.Marshal(p)
	if current.Revision == 0 {
		_, err = s.db.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?)`, "delivery_policy:"+environment, string(raw))
	} else {
		var before string
		if err = s.db.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, "delivery_policy:"+environment).Scan(&before); err != nil {
			return err
		}
		var previous DeliveryPolicy
		if json.Unmarshal([]byte(before), &previous) != nil || previous.Revision != current.Revision {
			return fmt.Errorf("环境策略已改变，请重新载入")
		}
		r, e := s.db.Exec(`UPDATE deploy_meta SET value=? WHERE key=? AND value=?`, string(raw), "delivery_policy:"+environment, before)
		err = e
		if e == nil {
			n, _ := r.RowsAffected()
			if n != 1 {
				return fmt.Errorf("环境策略已改变，请重新载入")
			}
		}
	}
	return err
}
func (s *Store) deliveryStrategyTemplate(req DeliveryRequest) (*DeliveryTemplate, error) {
	if req.StrategyKind == "standard" {
		if req.TemplateID != 0 {
			return nil, fmt.Errorf("标准交付不能同时指定自定义模板")
		}
		return &DeliveryTemplate{Name: "标准交付", Mode: "version", Services: []string{}, Nodes: []DeliveryNode{}}, nil
	}
	if req.StrategyKind != "" && req.StrategyKind != "custom" {
		return nil, fmt.Errorf("交付方式仅支持 standard 或 custom")
	}
	tpl := s.DeliveryTemplate(req.TemplateID)
	if tpl == nil {
		return nil, fmt.Errorf("请选择有效自定义编排")
	}
	return tpl, nil
}
func standardNodeID(parts ...string) string {
	return "std-" + strings.TrimPrefix(digestJSON(parts), "sha256:")[:24]
}
func standardDeliveryNodes(scope *DeliveryScope) ([]DeliveryNode, error) {
	nodes := []DeliveryNode{}
	groups := map[string]int{}
	if scope == nil {
		return nil, fmt.Errorf("标准交付需要需求、版本或明确选择的变更")
	}
	// This is a suggestion, not proof of business dependency safety.
	for _, kind := range []string{"mysql", "nacos", "deploy", "redis", "manual"} {
		for _, c := range scope.Changes {
			match := c.Kind == kind || kind == "deploy" && (c.Kind == "code" || c.Kind == "artifact")
			if !match {
				continue
			}
			alias, document := "", ""
			switch kind {
			case "mysql":
				if c.SQL != nil {
					alias = c.SQL.ResourceAlias
				}
			case "nacos":
				if c.Nacos != nil {
					alias = c.Nacos.ResourceAlias
					document = c.Nacos.Group + "\x00" + c.Nacos.DataID
				}
			case "redis":
				if c.Redis != nil {
					alias = c.Redis.ResourceAlias
				}
			case "manual":
				document = fmt.Sprint(c.ID)
			}
			key := kind + "\x00" + c.Service + "\x00" + alias + "\x00" + document
			idx, ok := groups[key]
			if !ok {
				name := map[string]string{"mysql": "SQL 变更", "nacos": "Nacos 配置", "deploy": "发布应用", "redis": "Redis 命令", "manual": "人工确认"}[kind]
				if c.Service != "" {
					name += " · " + c.Service
				}
				if alias != "" {
					name += " / " + alias
				}
				n := DeliveryNode{ID: standardNodeID(kind, c.Service, alias, document), Kind: kind, Name: name, Service: c.Service, ChangeIDs: []int64{}}
				if kind == "manual" {
					n.Name = c.Title
					n.Instructions = manualChangeInstructions(c)
				}
				idx = len(nodes)
				groups[key] = idx
				nodes = append(nodes, n)
			}
			nodes[idx].ChangeIDs = append(nodes[idx].ChangeIDs, c.ID)
		}
	}
	return nodes, nil
}

// A memo is frozen release content, never interpreted as executable code.
func manualChangeInstructions(c Change) string {
	if c.Reference == "" || c.Reference == c.Summary {
		return c.Summary
	}
	if c.Summary == "" {
		return c.Reference
	}
	return c.Summary + "\n\n" + c.Reference
}

func includeManualRecords(nodes []DeliveryNode, scope *DeliveryScope) []DeliveryNode {
	if scope == nil {
		return nodes
	}
	selected := map[int64]bool{}
	for _, c := range scope.Changes {
		if c.Kind == "manual" {
			selected[c.ID] = true
		}
	}
	filtered := []DeliveryNode{}
	for _, n := range nodes {
		if n.Kind == "manual" && len(n.ChangeIDs) > 0 {
			ids := []int64{}
			for _, id := range n.ChangeIDs {
				if selected[id] {
					ids = append(ids, id)
				}
			}
			if len(ids) == 0 {
				continue
			}
			n.ChangeIDs = ids
			// Generated records saved in a template are rebound to this scope,
			// rather than accumulating stale text from an earlier release.
			if strings.HasPrefix(n.ID, "std-") {
				n.Instructions = ""
			}
		}
		filtered = append(filtered, n)
	}
	nodes = filtered
	used := map[string]bool{}
	for _, n := range nodes {
		used[n.ID] = true
	}
	for _, c := range scope.Changes {
		if c.Kind != "manual" {
			continue
		}
		covered := false
		for i := range nodes {
			if nodes[i].Kind == "manual" && slices.Contains(nodes[i].ChangeIDs, c.ID) {
				nodes[i].Instructions += "\n\n" + c.Title + "\n" + manualChangeInstructions(c)
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		id := standardNodeID("manual", c.Service, "", fmt.Sprint(c.ID))
		for used[id] {
			id += "-record"
		}
		used[id] = true
		nodes = append(nodes, DeliveryNode{ID: id, Kind: "manual", Name: c.Title, Service: c.Service, ChangeIDs: []int64{c.ID}, Instructions: manualChangeInstructions(c)})
	}
	return nodes
}

func validateManualRecordOverrides(expected, actual []DeliveryNode) error {
	for _, e := range expected {
		if e.Kind != "manual" || len(e.ChangeIDs) == 0 {
			continue
		}
		found := false
		for _, n := range actual {
			if n.ID == e.ID && n.Kind == e.Kind && n.Service == e.Service && slices.Equal(n.ChangeIDs, e.ChangeIDs) && n.Instructions == e.Instructions && slices.Equal(n.Approvers, e.Approvers) && n.ApprovalMode == e.ApprovalMode {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("自定义记录 %s 不能从处理清单删除或改写；请回到范围选择调整变更", e.Name)
		}
	}
	return nil
}
func (s *Store) DraftDelivery(req DeliveryRequest) (*DeliveryDraftPlan, error) {
	tpl, err := s.deliveryStrategyTemplate(req)
	if err != nil {
		return nil, err
	}
	env := s.GetEnvironment(req.Environment)
	if env == nil {
		return nil, fmt.Errorf("请选择有效目标环境")
	}
	scope, version, err := s.resolveDeliveryScope(req, tpl)
	if err != nil {
		return nil, err
	}
	nodes := append([]DeliveryNode{}, tpl.Nodes...)
	if req.StrategyKind == "standard" {
		nodes, err = standardDeliveryNodes(scope)
		if err != nil {
			return nil, err
		}
	} else if tpl.Workbench == "" {
		nodes = includeManualRecords(nodes, scope)
	}
	if tpl.Workbench == "" {
		nodes, err = orderChangeNodes(nodes, scope, true)
		if err != nil {
			return nil, err
		}
	}
	policy, err := s.deliveryPolicy(env.Name)
	if err != nil {
		return nil, err
	}
	if err := validateWorkflowBinding(req, *env, policy); err != nil {
		return nil, err
	}
	stages := append([]DeliveryNode{}, policy.Stages...)
	if req.StrategyKind == "standard" && len(stages) == 0 && deliveryReviewRequired(&DeliveryPlan{StrategyKind: req.StrategyKind, Template: *tpl, Environment: *env, Nodes: nodes}) {
		approvers := []string{}
		for _, a := range strings.Split(env.Approvers, ",") {
			if a = strings.TrimSpace(a); a != "" && a != "local" {
				approvers = append(approvers, a)
			}
		}
		stages = append(stages, DeliveryNode{ID: "env-review-1", Name: "环境发布审核", Kind: "approval", Approvers: approvers, ApprovalMode: "any"})
	}
	// A saved custom template can legitimately use an environment node's legacy
	// identifier. Preserve both gates under a deterministic, collision-free ID.
	usedIDs := map[string]bool{}
	for _, n := range nodes {
		usedIDs[n.ID] = true
	}
	for i := range stages {
		if usedIDs[stages[i].ID] {
			stages[i].ID = standardNodeID("environment-review", fmt.Sprint(i), stages[i].ID)
		}
	}
	// Workbench maintains its separately configured query/change approval policy.
	if tpl.Workbench == "" {
		nodes = append(stages, nodes...)
	} else {
		stages = []DeliveryNode{}
	}
	snapshot := DeliveryPolicySnapshot{Version: 1, Revision: policy.Revision, Environment: *env, Stages: stages}
	out := &DeliveryDraftPlan{StepsDigest: digestJSON(nodes), StrategyKind: "custom", Template: *tpl, Scope: scope, Nodes: nodes, PolicySnapshot: snapshot, PolicyDigest: digestJSON(snapshot), Blockers: []string{}, Warnings: []string{}}
	out.Defaults = policy.Defaults
	if req.StrategyKind == "standard" {
		out.StrategyKind = "standard"
		out.StrategyVersion = standardDeliveryVersion
	}
	if version != nil && version.Status != ReleaseVersionReady && version.Status != ReleaseVersionCompleted {
		out.Blockers = append(out.Blockers, "来源版本尚未就绪")
	}
	if scope != nil {
		for _, c := range scope.Changes {
			if c.Status != ChangeReady {
				out.Blockers = append(out.Blockers, fmt.Sprintf("变更 #%d · %s 尚未就绪", c.ID, c.Title))
			}
		}
	}
	for _, n := range stages {
		if err := validateApprovalNode(n); err != nil {
			if len(n.Approvers) == 0 {
				out.Blockers = append(out.Blockers, fmt.Sprintf("环境 %s 未配置审核人员，请在环境审核设置中配置", env.DisplayName))
			} else {
				out.Blockers = append(out.Blockers, err.Error())
			}
		}
	}
	if len(nodes) == 0 {
		out.Blockers = append(out.Blockers, "本次范围没有可执行内容")
	}
	return out, nil
}

// Only reorder generated business nodes. Never remove scope or mandatory gates
// by editing client nodes. Scope exclusions are independently frozen instead.
func validateStandardOverrides(expected, actual []DeliveryNode) error {
	byID := map[string]DeliveryNode{}
	sqlChanges := map[int64]string{}
	for _, n := range expected {
		byID[n.ID] = n
		if n.Kind == "mysql" {
			for _, id := range n.ChangeIDs {
				sqlChanges[id] = n.ID
			}
		}
	}
	seen := map[string]bool{}
	for _, n := range actual {
		if seen[n.ID] {
			return fmt.Errorf("步骤标识重复")
		}
		seen[n.ID] = true
		group := n.ID
		if n.SQLGroup != "" {
			group = n.SQLGroup
		}
		e, ok := byID[group]
		if !ok {
			return fmt.Errorf("节点 %s 不符合服务端生成的范围", n.Name)
		}
		if e.Kind == "mysql" {
			if n.Kind != "mysql" || n.Service != e.Service || len(n.ChangeIDs) == 0 || len(n.Approvers) > 0 || n.ApprovalMode != "" {
				return fmt.Errorf("SQL 阶段只能拆分原组内的变更")
			}
			for _, id := range n.ChangeIDs {
				if sqlChanges[id] != group {
					return fmt.Errorf("SQL 变更 #%d 重复或不属于原分组", id)
				}
				delete(sqlChanges, id)
			}
			continue
		}
		if n.SQLGroup != "" || e.Kind != n.Kind || e.Service != n.Service || !slices.Equal(e.ChangeIDs, n.ChangeIDs) || !slices.Equal(e.Approvers, n.Approvers) || e.ApprovalMode != n.ApprovalMode {
			return fmt.Errorf("节点 %s 不符合服务端生成的范围或审核策略", n.Name)
		}
		delete(byID, group)
	}
	if len(sqlChanges) > 0 {
		return fmt.Errorf("SQL 阶段遗漏已选择的变更")
	}
	for _, n := range byID {
		if n.Kind != "mysql" {
			return fmt.Errorf("步骤不能删减已确认范围或审核节点")
		}
	}
	return nil
}

func validateNodeDependencies(nodes []DeliveryNode) error {
	positions := map[string]int{}
	for i, n := range nodes {
		positions[n.ID] = i
	}
	for i, n := range nodes {
		for _, dependency := range n.DependsOn {
			p, ok := positions[dependency]
			if !ok || p >= i {
				return fmt.Errorf("节点 %s 的前置依赖 %s 未满足，不能使用该顺序", n.Name, dependency)
			}
		}
	}
	return nil
}
func (s *Store) checkDeliveryPolicy(p *DeliveryPlan) error {
	if p.PolicySnapshot == nil {
		return nil
	}
	env := s.GetEnvironment(p.Environment.Name)
	if env == nil {
		return fmt.Errorf("环境已删除")
	}
	policy, err := s.deliveryPolicy(env.Name)
	if err != nil {
		return err
	}
	if policy.Revision != p.PolicySnapshot.Revision || digestJSON(*env) != digestJSON(p.PolicySnapshot.Environment) {
		return fmt.Errorf("环境交付策略已变化，请新建执行单并重新预检审核")
	}
	return nil
}
func (s *Store) registerStandardDeliveryAPI(mux *http.ServeMux, reply func(http.ResponseWriter, any, error), actor func(*http.Request) string) {
	mux.HandleFunc("POST /api/delivery/draft-plan", func(w http.ResponseWriter, r *http.Request) {
		var req DeliveryRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&req); err != nil {
			reply(w, nil, err)
			return
		}
		req.previewDrafts = true
		p, err := s.DraftDelivery(req)
		reply(w, p, err)
	})
	mux.HandleFunc("GET /api/environments/{name}/delivery-policy", func(w http.ResponseWriter, r *http.Request) {
		p, err := s.deliveryPolicy(r.PathValue("name"))
		reply(w, p, err)
	})
	mux.HandleFunc("PUT /api/environments/{name}/delivery-policy", func(w http.ResponseWriter, r *http.Request) {
		if actor(r) != "local" && r.Header.Get("X-Role") != "admin" {
			reply(w, nil, fmt.Errorf("仅管理员可以配置环境交付策略"))
			return
		}
		var p DeliveryPolicy
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&p); err != nil {
			reply(w, nil, err)
			return
		}
		if err := validateDeliveryReviewerDirectory(&DeliveryPlan{Nodes: p.Stages}, r.Header.Get("X-Reviewer-Directory"), ""); err != nil {
			reply(w, nil, err)
			return
		}
		err := s.saveDeliveryPolicy(r.PathValue("name"), p)
		if err == nil {
			s.Audit(actor(r), "configure", "delivery-policy", r.PathValue("name"), nil)
			p, _ = s.deliveryPolicy(r.PathValue("name"))
		}
		reply(w, p, err)
	})
}

func selectDeliveryChanges(scope *DeliveryScope, ids []int64) (*DeliveryScope, error) {
	if scope == nil || ids == nil {
		return scope, nil
	}
	allowed := map[int64]Change{}
	for _, c := range scope.Changes {
		allowed[c.ID] = c
	}
	selected := []Change{}
	seen := map[int64]bool{}
	for _, id := range ids {
		c, ok := allowed[id]
		if !ok || seen[id] {
			return nil, fmt.Errorf("变更 #%d 不在来源范围中或重复", id)
		}
		seen[id] = true
		selected = append(selected, c)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("至少选择一项交付变更")
	}
	copy := *scope
	copy.SourceDigest = scope.Digest
	copy.Changes = selected
	copy.ExcludedChangeIDs = []int64{}
	for _, c := range scope.Changes {
		if !seen[c.ID] {
			copy.ExcludedChangeIDs = append(copy.ExcludedChangeIDs, c.ID)
		}
	}
	sort.Slice(copy.Changes, func(i, j int) bool { return copy.Changes[i].ID < copy.Changes[j].ID })
	copy.Digest = ""
	copy.Digest = digestJSON(copy)
	return &copy, nil
}

func validateDeliveryReviewerDirectory(p *DeliveryPlan, raw, owner string) error {
	hasReview := hasApprovalNodes(p)
	for _, n := range p.Nodes {
		hasReview = hasReview || n.Kind == "verify" && n.Verification != nil && n.Verification.Mode == "manual"
	}
	if raw == "" || !hasReview {
		return nil
	} // standalone backend has no user catalog
	var names []string
	if json.Unmarshal([]byte(raw), &names) != nil || names == nil {
		return fmt.Errorf("平台审核人员目录不可用，请重新检查")
	}
	enabled := map[string]bool{}
	for _, n := range names {
		enabled[n] = true
	}
	for _, stage := range p.Nodes {
		verification := stage.Kind == "verify" && stage.Verification != nil && stage.Verification.Mode == "manual"
		if stage.Kind != "approval" && !verification {
			continue
		}
		count := 0
		for _, a := range stage.Approvers {
			if !enabled[a] {
				if stage.ApprovalMode == "all" {
					return fmt.Errorf("会签阶段 %s 的 %s 已停用或不存在", stage.Name, a)
				}
				continue
			}
			if a != owner || verification {
				count++
			}
		}
		if count == 0 {
			return fmt.Errorf("审核阶段 %s 没有可用且非发起人的审核人员", stage.Name)
		}
	}
	return nil
}
