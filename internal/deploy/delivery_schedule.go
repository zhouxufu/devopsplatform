package deploy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

const dependencyExecution = "dependencies"

type DeliveryDisposition struct {
	Action  string `json:"action"`
	Stage   string `json:"stage"`
	Actor   string `json:"actor"`
	At      int64  `json:"at"`
	Comment string `json:"comment,omitempty"`
}

func nodeByID(nodes []DeliveryNode, id string) *DeliveryNode {
	for i := range nodes {
		if nodes[i].ID == id {
			return &nodes[i]
		}
	}
	return nil
}
func disposableNode(n *DeliveryNode) bool {
	return n != nil && n.Kind != "approval" && n.Kind != xxlJobKind && !n.XXLJobVerification && len(n.Approvers) == 0 && n.Monitor == nil && !(n.Kind == "verify" && n.Verification != nil && n.Verification.Mode == "manual")
}

func xxlJobReleaseDependency(nodes []DeliveryNode, id string) bool {
	return slices.ContainsFunc(nodes, func(n DeliveryNode) bool {
		return n.XXLJobVerification && slices.Contains(n.DependsOn, id)
	})
}
func expandScheduleApplications(nodes []DeliveryNode, tpl *DeliveryTemplate, scope *DeliveryScope) []DeliveryNode {
	names := slices.Clone(tpl.Services)
	if scope != nil {
		names = nil
		for _, change := range scope.Changes {
			if (change.Kind == "code" || change.Kind == "artifact") && !slices.Contains(names, change.Service) {
				names = append(names, change.Service)
			}
		}
	}
	if scope != nil && len(names) == 0 {
		empty := map[string]DeliveryNode{}
		for _, n := range nodes {
			if n.Kind == "deploy" && n.Service == "" && len(n.ChangeIDs) == 0 && disposableNode(&n) {
				empty[n.ID] = n
			}
		}
		return withoutInapplicableNodes(nodes, empty)
	}
	if len(names) < 2 {
		return nodes
	}
	slices.Sort(names)
	replacements := map[string][]string{}
	var result []DeliveryNode
	for _, n := range nodes {
		if n.Kind != "deploy" || n.Service != "" || n.Monitor != nil {
			result = append(result, n)
			continue
		}
		for _, name := range names {
			app := n
			app.ID = standardNodeID("deploy", n.ID, name)
			app.Service = name
			app.Name = n.Name + " · " + name
			replacements[n.ID] = append(replacements[n.ID], app.ID)
			result = append(result, app)
		}
	}
	for i, n := range result {
		var deps []string
		for _, id := range n.DependsOn {
			if expanded, ok := replacements[id]; ok {
				deps = append(deps, expanded...)
			} else {
				deps = append(deps, id)
			}
		}
		result[i].DependsOn = deps
	}
	return result
}
func configureDeliverySchedule(p *DeliveryPlan, r DeliveryRequest) error {
	if r.MaxActiveNodes < 0 || (r.MaxActiveNodes > 0 && r.ExecutionMode != dependencyExecution) {
		return fmt.Errorf("节点并发上限仅支持按依赖执行的正整数")
	}
	p.MaxActiveNodes = r.MaxActiveNodes
	if r.ExecutionMode == "" && len(r.SkippedNodes) == 0 {
		return nil
	}
	if r.ExecutionMode != dependencyExecution || p.Template.Workbench != "" {
		return fmt.Errorf("此执行计划不支持按依赖调度")
	}
	p.ExecutionMode = r.ExecutionMode
	for id, note := range r.SkippedNodes {
		if !disposableNode(nodeByID(p.Nodes, id)) {
			return fmt.Errorf("不能跳过未选节点、审批或指定人员验证：%s", id)
		}
		if xxlJobReleaseDependency(p.Nodes, id) {
			return fmt.Errorf("定时任务启用前不能跳过关联发布节点：%s", id)
		}
		note = strings.TrimSpace(note)
		if len(note) > 8000 || (p.Environment.Protected && note == "") {
			return fmt.Errorf("请核对节点 %s 的跳过原因", id)
		}
		if p.SkippedNodes == nil {
			p.SkippedNodes = map[string]string{}
		}
		p.SkippedNodes[id] = note
	}
	return nil
}
func freezeScheduleDependencies(p *DeliveryPlan) error {
	if p.ExecutionMode != dependencyExecution {
		return nil
	}
	changes := map[int64]string{}
	if p.Scope != nil {
		for _, c := range p.Scope.Changes {
			for _, n := range p.Nodes {
				if changeNodeMatches(n, c) {
					changes[c.ID] = n.ID
					break
				}
			}
		}
	}
	for i := range p.Nodes {
		n := &p.Nodes[i]
		n.DependsOn = slices.Clone(n.DependsOn)
		add := func(id string) {
			if id != "" && id != n.ID && !slices.Contains(n.DependsOn, id) {
				n.DependsOn = append(n.DependsOn, id)
			}
		}
		if p.Scope != nil {
			for _, c := range p.Scope.Changes {
				if changes[c.ID] == n.ID {
					for _, pre := range c.Prerequisites {
						add(changes[pre.ChangeID])
					}
				}
			}
		}
		for _, prior := range p.Nodes[:i] {
			// Keep workflow gates and SQL stages in their reviewed order. Business
			// deployments are independent unless an explicit prerequisite says otherwise.
			if prior.Kind == "verify" && prior.Verification != nil && prior.Verification.Mode == "manual" || prior.Kind == "approval" || (prior.Kind != "verify" && releaseOrderBarrier(prior)) || (n.Kind != "verify" && releaseOrderBarrier(*n)) || (n.Kind == "verify" && prior.Kind == "deploy" && (prior.Service == "" || prior.Service == n.Service)) {
				add(prior.ID)
			}
			if n.Kind == "mysql" && prior.Kind == "mysql" {
				add(prior.ID)
			}
		}
	}
	return validateNodeDependencies(p.Nodes)
}
func scheduleStepDone(s DeliveryStep) bool {
	return s.Status == "success" || s.Status == "skipped" || s.Status == "manual_done"
}

// A confirmed failure blocks dependents. Other branches in a dependency run
// can still be dispatched; legacy serial runs stop on the first failure.
func deliveryFailureStatus(v *DeliveryRun) string {
	if v.Plan.ExecutionMode == dependencyExecution {
		return "running"
	}
	return "failed"
}
func deliveryCompletedStatus(v *DeliveryRun) string {
	if v.Plan.ExecutionMode == dependencyExecution && slices.ContainsFunc(v.Steps, func(st DeliveryStep) bool { return st.Status == "failed" }) {
		return "failed"
	}
	for _, s := range v.Steps {
		if s.Disposition != nil || (v.Plan.ExecutionMode == dependencyExecution && s.ManualRecovery != nil) {
			return "completed_with_exceptions"
		}
	}
	return "success"
}
func blockedPredecessor(v *DeliveryRun, n *DeliveryNode) string {
	for _, id := range n.DependsOn {
		found := false
		for _, st := range v.Steps {
			if st.NodeID != id {
				continue
			}
			found = true
			if st.Status != "success" && st.Status != "manual_done" {
				if st.Name != "" {
					return st.Name
				}
				return id
			}
		}
		if !found {
			return id
		}
	}
	return ""
}

// The same writer rule applies to preflight and to each live dispatch decision.
func canScheduleAlongsideResources(kind string, writers map[string]bool) bool {
	return len(writers) == 0 || kind == "deploy" || deliveryResourceKind(kind) && !writers[kind]
}

// First dispatches after approvals, before any business step reports completion.
// Node IDs refer to the resolved (and possibly split) nodes in the preview.
func initialDeliveryDispatch(p *DeliveryPlan) []string {
	ids := []string{}
	if len(p.Blockers) != 0 {
		return ids
	}
	approved := map[string]bool{}
	missingTarget := map[string]bool{}
	for _, app := range p.Services {
		if app.PendingTarget {
			missingTarget[app.Service] = true
		}
	}
	writers := map[string]bool{}
	for _, node := range p.Nodes {
		if node.Kind == "approval" {
			approved[node.ID] = true
			continue
		}
		if _, skipped := p.SkippedNodes[node.ID]; skipped {
			continue
		}
		if node.Kind == "manual" || node.Kind == "verify" && node.Verification != nil && node.Verification.Mode == "manual" {
			continue
		}
		if node.Kind == "deploy" && (missingTarget[node.Service] || node.Service == "" && len(missingTarget) > 0) {
			continue
		}
		if slices.ContainsFunc(node.DependsOn, func(id string) bool { return !approved[id] }) || !canScheduleAlongsideResources(node.Kind, writers) {
			continue
		}
		ids = append(ids, node.ID)
		if p.MaxActiveNodes > 0 && len(ids) == p.MaxActiveNodes {
			break
		}
		if deliveryResourceKind(node.Kind) {
			writers[node.Kind] = true
		} else if node.Kind != "deploy" {
			break
		}
	}
	return ids
}

// Count unresolved receipts, including child releases that are paused between batches.
func deliveryNodeLimitReached(v *DeliveryRun) bool {
	if v.Plan.MaxActiveNodes == 0 {
		return false
	}
	active := 0
	for _, st := range v.Steps {
		if st.Status == "executing" || st.ReleaseID != 0 && !scheduleStepDone(st) && st.Status != "failed" {
			active++
		}
	}
	return active >= v.Plan.MaxActiveNodes
}

func (s *Store) deliveryGraphNext(v *DeliveryRun) (int, string, string) {
	// An executor that owns a ledger revision must finish writing its receipt
	// before another step can change the revision. Child releases have their own ledger.
	resourceWriters := map[string]bool{}
	limitReached := deliveryNodeLimitReached(v)
	for _, st := range v.Steps {
		if st.Status == "executing" && deliveryResourceKind(st.Kind) && st.LeaseUntil > time.Now().Unix() {
			resourceWriters[st.Kind] = true
			continue
		}
		if st.Status == "executing" || st.Status == "uncertain" {
			if st.LeaseUntil > time.Now().Unix() {
				return -1, "wait", "等待执行回执：" + st.Name
			}
			return -1, "pause", "需核对原执行回执：" + st.Name
		}
	}
	pending, live := false, len(resourceWriters) > 0
	message := "等待前置节点"
	human := -1
	for i, st := range v.Steps {
		if scheduleStepDone(st) {
			continue
		}
		pending = true
		n := nodeByID(v.Plan.Nodes, st.NodeID)
		if n == nil {
			return -1, "pause", "节点快照缺失"
		}
		if dep := blockedPredecessor(v, n); dep != "" {
			message = "等待前置节点：" + dep
			continue
		}
		if st.Status == "failed" {
			message = "需处理失败节点：" + st.Name
			continue
		}
		if st.Status == "pending_target" {
			message = st.Name + "：部署目标待配置"
			continue
		}
		if st.ReleaseID != 0 {
			child := s.GetReleaseOrder(st.ReleaseID)
			if child == nil {
				message = "需核对子发布单"
				continue
			}
			if releaseStatusActive(child.Status) {
				if child.Status == "waiting_next_batch" || child.Status == "waiting_manual" {
					message = "等待批次观察或人工回执"
				} else {
					live = true
				}
				continue
			}
			return i, "advance", ""
		}
		if !canScheduleAlongsideResources(st.Kind, resourceWriters) {
			continue
		}
		if st.Status != "pending" {
			message = "请核对节点状态：" + st.Name
			continue
		}
		if st.Kind == "approval" {
			message = "等待审批"
			continue
		}
		if st.Kind == "manual" || (n.Kind == "verify" && n.Verification != nil && n.Verification.Mode == "manual") {
			if human < 0 {
				human = i
			}
			message = "等待人工处理：" + st.Name
			continue
		}
		if limitReached {
			continue
		}
		return i, "advance", ""
	}
	if live {
		return -1, "wait", "等待已派发节点的执行回执"
	}
	if pending && !slices.ContainsFunc(v.Steps, func(st DeliveryStep) bool { return st.Status != "failed" && !scheduleStepDone(st) }) {
		return -1, "advance", ""
	}
	if pending {
		return human, "pause", message
	}
	return -1, "advance", ""
}

type dispositionRequest struct {
	NodeID   string `json:"node_id"`
	Service  string `json:"service"`
	Action   string `json:"action"`
	Digest   string `json:"digest"`
	Revision int64  `json:"revision"`
	Comment  string `json:"comment"`
	Confirm  bool   `json:"confirm"`
}

// Resource workers merge their own receipts. Other active writers still need
// exclusive access to the run until their outcome has been recorded.
func blocksDisposition(st DeliveryStep) bool {
	return st.Status == "uncertain" || st.Status == "executing" && !(deliveryResourceKind(st.Kind) && st.LeaseUntil > time.Now().Unix())
}

func (s *Store) disposeDeliveryStep(v *DeliveryRun, r dispositionRequest, actor string) error {
	if v == nil || v.Plan.ExecutionMode != dependencyExecution || v.Plan.PreparationRequest != nil || v.Plan.Template.Workbench != "" || !r.Confirm || v.Digest != r.Digest || v.Revision != r.Revision || (v.Status != "approved" && v.Status != "running") {
		return ErrRunConflict
	}
	env, err := s.validateDeliveryExecution(v)
	if err != nil {
		return err
	}
	n := nodeByID(v.Plan.Nodes, r.NodeID)
	if !disposableNode(n) {
		return fmt.Errorf("审批或指定人员验收请从原入口处理")
	}
	if xxlJobReleaseDependency(v.Plan.Nodes, r.NodeID) {
		return fmt.Errorf("定时任务启用依赖本次应用发布成功，不能跳过或手动标记完成")
	}
	if r.Action != "skip" && r.Action != "manual_done" {
		return fmt.Errorf("未知节点操作")
	}
	r.Comment = strings.TrimSpace(r.Comment)
	if len(r.Comment) > 8000 || ((env.Protected || v.Plan.Environment.Protected) && r.Comment == "") {
		return fmt.Errorf("受保护环境须填写说明，最长 8000 字节")
	}
	if slices.ContainsFunc(v.Steps, blocksDisposition) {
		return fmt.Errorf("请先核对在途执行回执")
	}
	for i := range v.Steps {
		st := &v.Steps[i]
		if st.NodeID != r.NodeID || st.Service != r.Service {
			continue
		}
		if st.StartedAt != 0 || st.ReleaseID != 0 || (st.Status != "pending" && st.Status != "pending_target") {
			return fmt.Errorf("只能处置尚未启动的节点，已启动操作请核对原结果")
		}
		if r.Action == "manual_done" {
			if dep := blockedPredecessor(v, n); dep != "" {
				return fmt.Errorf("请先完成前置节点：%s", dep)
			}
		}
		st.Status = "skipped"
		if r.Action == "manual_done" {
			st.Status = "manual_done"
		}
		st.EndedAt = time.Now().Unix()
		st.Disposition = &DeliveryDisposition{Action: r.Action, Actor: actor, At: st.EndedAt, Stage: "execution", Comment: r.Comment}
		v.Status = "running"
		if !slices.ContainsFunc(v.Steps, func(st DeliveryStep) bool { return st.Status != "failed" && !scheduleStepDone(st) }) {
			v.Status = deliveryCompletedStatus(v)
		}
		if err := s.saveDeliveryRun(v); err != nil {
			return err
		}
		s.Audit(actor, "node-"+r.Action, "delivery-run", fmt.Sprint(v.ID), map[string]any{"node": st.NodeID, "service": st.Service, "digest": v.Digest})
		return nil
	}
	return fmt.Errorf("执行单中没有该节点")
}
func (s *Store) registerDispositionRoute(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/delivery/runs/{id}/node-disposition", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		v := s.deliveryRunLedger(id)
		if v == nil || !deliveryManage(r, v.Owner) {
			deliveryWriteError(w, 403, "仅发起人或管理员可处理此执行单")
			return
		}
		var req dispositionRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&req); err != nil {
			deliveryWriteError(w, 400, err)
			return
		}
		if err := validateDeliveryReviewerDirectory(&v.Plan, r.Header.Get("X-Reviewer-Directory"), v.Owner); err != nil {
			deliveryWriteError(w, 409, err)
			return
		}
		if err := s.disposeDeliveryStep(v, req, deliveryActor(r)); err != nil {
			deliveryWriteError(w, 409, err)
			return
		}
		deliveryJSON(w, v)
	})
}
