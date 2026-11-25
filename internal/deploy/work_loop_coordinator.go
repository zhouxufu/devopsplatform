package deploy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

func loopDeliveryRequest(t *WorkLoop) DeliveryRequest {
	return DeliveryRequest{TemplateID: t.Grant.TemplateID, SourceType: "work_items", WorkItemIDs: []int64{t.Grant.Spec.WorkItemID}, SelectedChangeIDs: t.current().ChangeIDs, Environment: t.Grant.Spec.Environment, Reason: t.current().Summary, StrategyKind: "custom", OrderConfirmed: true, CodeConfirmed: true, Artifacts: map[string]int64{}}
}
func (s *Store) advanceWorkLoop(mux *http.ServeMux, r *http.Request, t *WorkLoop, pr *PipelineRunner, m *ReleaseManager) error {
	it := t.current()
	if t.State == "completed" {
		return nil
	}
	if t.Stage == "acceptance" {
		w := s.GetWorkItem(t.Grant.Spec.WorkItemID)
		closed := w != nil && w.Source == "manual" && w.Status == WorkItemClosed
		if w != nil && w.Source != "manual" {
			z, _, err := s.researchClient(r.Context(), w, t.Owner)
			if err != nil {
				return err
			}
			remote, err := z.researchReadItem(r.Context(), w, "view")
			if err != nil {
				return err
			}
			closed = researchValue(remote.Bug, "status") == "closed"
			if w.Kind == "requirement" {
				closed = researchValue(remote.Story, "stage") == "verified" || closed && researchValue(remote.Story, "closedReason") == "done"
			}
		}
		if closed {
			t.setState("completed", "acceptance", "已由人员完成验收／关闭")
			return s.saveWorkLoop(t)
		}
		return nil
	}
	if t.Intent != nil && t.Intent.State == "dispatching" {
		if t.Intent.Name == "write_source" && t.current().SourceOperation != "" {
			var op ResearchOperation
			if e := loopCall(mux, r, "/api/research/operations/"+t.current().SourceOperation+"/reconcile", map[string]any{}, &op); e != nil {
				return e
			}
		}
		if e := s.reconcileWorkLoopIntent(t); e != nil {
			return e
		}
		if t.Intent.State == "dispatching" {
			return fmt.Errorf("上次 %s 结果待核对；保留原操作，不重复执行", t.Intent.Name)
		}
	}
	if t.Stage == "development" {
		return nil
	}
	// Recover references after receipt persistence but before iteration update.
	if t.Intent != nil && t.Intent.State == "received" {
		var receipt DeliveryRun
		if json.Unmarshal(t.Intent.Result, &receipt) == nil && receipt.ID > 0 {
			if t.Intent.Name == "create_preparation" && it.PreparationID == 0 {
				it.PreparationID = receipt.ID
			}
			if t.Intent.Name == "create_delivery" && it.DeliveryID == 0 {
				it.DeliveryID = receipt.ID
			}
		}
	}
	if t.Stage == "writeback" {
		// A work item may gain additional changes while its delivery is running.
		// Recheck the whole completion scope before changing its source status.
		if e := s.workLoopCompleteScope(t, it.ChangeIDs); e != nil {
			return e
		}
		if e := s.workLoopEvidence(t); e != nil {
			return e
		}
		if e := s.writeWorkLoopSource(r.Context(), t); e != nil {
			return e
		}
		it.State = "success"
		t.setState("awaiting_acceptance", "acceptance", "交付和回归已通过，状态及备注已回写；最终验收／关闭由人员决定")
		return s.saveWorkLoop(t)
	}
	changes, e := s.workLoopChanges(t, it.ChangeIDs)
	if e != nil {
		return e
	}
	if digestJSON(changes) != it.ChangesDigest {
		return fmt.Errorf("本轮变更内容或关联已变化；原冻结记录保留，请核对差异")
	}
	req := loopDeliveryRequest(t)
	if it.PreparationID == 0 {
		var preview struct {
			Plan   DeliveryPlan `json:"plan"`
			Digest string       `json:"digest"`
		}
		if e = loopCall(mux, r, "/api/delivery/preparations/preview", req, &preview); e != nil {
			return e
		}
		if e = loopPlanScope(t, &preview.Plan); e != nil {
			return e
		}
		body := map[string]any{}
		b, _ := json.Marshal(req)
		_ = json.Unmarshal(b, &body)
		body["client_request_id"] = strings.TrimPrefix(digestJSON([]any{t.ID, it.Number, "preparation"}), "sha256:")[:32]
		body["expected_digest"] = preview.Digest
		var run DeliveryRun
		if e = s.dispatchWorkLoop(mux, r, t, "create_preparation", "/api/delivery/preparations/runs", body, &run); e != nil {
			return e
		}
		it.PreparationID = run.ID
		t.setState("running", "preparation", "范围已冻结，自动核对本人范围并准备代码制品")
		return s.saveWorkLoop(t)
	}
	prep := s.DeliveryRun(it.PreparationID)
	if prep == nil {
		return fmt.Errorf("原准备执行单不存在，不创建替代记录")
	}
	if prep.Status != "success" {
		step, node := researchNextNode(prep)
		if step == nil || node == nil {
			return fmt.Errorf("准备阶段缺少可核对的节点")
		}
		if node.Kind == "approval" || node.Kind == "verify" && node.Verification != nil && node.Verification.Mode == "manual" {
			t.setState("waiting_human", "preparation", fmt.Sprintf("等待 %s：%s；任务授权不代替他人的审批或人工验证", strings.Join(node.Approvers, "、"), node.Name))
			return s.saveWorkLoop(t)
		}
		if node.Kind == "manual" {
			if (node.AssigneeSource != "change_submitters" && node.AssigneeSource != "delivery_initiator") || !slices.Contains(node.Approvers, t.Owner) {
				t.setState("waiting_human", "preparation", fmt.Sprintf("等待指定人员处理：%s", node.Name))
				return s.saveWorkLoop(t)
			}
			for _, review := range step.Reviews {
				if review.Actor == t.Owner {
					t.setState("waiting_human", "preparation", "本人范围已确认，等待其他范围提交人")
					return s.saveWorkLoop(t)
				}
			}
			body := map[string]any{"revision": prep.Revision, "digest": prep.Digest, "node_id": node.ID, "comment": "依据跟进任务 " + t.ID + " 的本人范围授权确认；不代签其他人员", "work_item_ids": node.AssignedWorkItems[t.Owner], "confirmation_id": strings.TrimPrefix(digestJSON([]any{t.ID, it.Number, node.ID}), "sha256:")[:32]}
			return s.dispatchWorkLoop(mux, r, t, "confirm_scope", fmt.Sprintf("/api/delivery/mcp/runs/%d/confirm-scope", prep.ID), body, nil)
		}
		if (node.Kind == "code-prepare" || node.Kind == "code-verify") && step.Detail != "" {
			p := s.CodePreparation(step.Detail)
			if p == nil {
				return fmt.Errorf("原代码任务不存在，不能重建")
			}
			it.CodeID = p.ID
			switch p.State {
			case "preview":
				if e = loopPreparedScope(t, p); e != nil {
					return e
				}
				return s.dispatchWorkLoop(mux, r, t, "start_code", "/api/delivery/mcp/code-preparations/"+p.ID+"/start", map[string]any{"confirm": true, "expected_revision": p.Revision}, nil)
			case "ready": // reconcile the original node below
			case "merging", "building":
				if p.LeaseUntil < time.Now().Unix() {
					return fmt.Errorf("代码任务 %s 执行租约已过期，请在原任务核对，不能自动重试", p.ID)
				}
				t.setState("running", "preparation", p.Message)
				return s.saveWorkLoop(t)
			default:
				return fmt.Errorf("代码任务 %s：%s；%s", p.ID, p.State, p.Message)
			}
		}
		if prep.Status == "failed" || prep.Status == "canceled" {
			return fmt.Errorf("准备执行单 #%d 已%s：%s", prep.ID, prep.Status, step.Detail)
		}
		return s.dispatchWorkLoop(mux, r, t, "advance_preparation", fmt.Sprintf("/api/delivery/mcp/runs/%d/advance", prep.ID), map[string]any{"revision": prep.Revision, "digest": prep.Digest, "node_id": node.ID, "service": step.Service, "manual": false}, nil)
	}
	if it.DeliveryID == 0 {
		req.PreparationRunID = prep.ID
		for _, step := range prep.Steps {
			if step.Kind != "code-prepare" && step.Kind != "code-verify" {
				continue
			}
			if p := s.CodePreparation(step.Detail); p != nil {
				for _, b := range p.Builds {
					req.Artifacts[b.Service] = b.ArtifactID
				}
			}
		}
		// Artifact-only changes still use the original resolver's exact identity.
		var plan DeliveryPlan
		if e = loopCall(mux, r, "/api/delivery/preview", req, &plan); e != nil {
			return e
		}
		if len(plan.Blockers) > 0 {
			return fmt.Errorf("交付预检未通过：%s", strings.Join(plan.Blockers, "；"))
		}
		if e = loopPlanScope(t, &plan); e != nil {
			return e
		}
		req.PreflightToken = plan.PreflightToken
		var run DeliveryRun
		if e = s.dispatchWorkLoop(mux, r, t, "create_delivery", "/api/delivery/mcp/runs", req, &run); e != nil {
			return e
		}
		it.DeliveryID = run.ID
		t.setState("running", "delivery", "环境预检通过，按冻结执行单交付及回归")
		return s.saveWorkLoop(t)
	}
	run := s.DeliveryRun(it.DeliveryID)
	if run == nil {
		return fmt.Errorf("原交付执行单不存在")
	}
	if run.Status == "success" {
		if e = s.workLoopEvidence(t); e != nil {
			return e
		}
		t.setState("running", "writeback", "交付及绑定制品的回归已通过，准备回写来源")
		return s.saveWorkLoop(t)
	}
	if run.Status == "pending_approval" {
		t.setState("waiting_human", "delivery", "等待执行单指定人员审批，不重复要求发起人确认")
		return s.saveWorkLoop(t)
	}
	if run.Status == "failed" || run.Status == "canceled" {
		return fmt.Errorf("交付执行单 #%d 为 %s，请在本任务核对具体节点", run.ID, run.Status)
	}
	for _, step := range run.Steps {
		if step.Status == "success" {
			continue
		}
		manualVerify := false
		for _, node := range run.Plan.Nodes {
			if node.ID == step.NodeID && node.Kind == "verify" && node.Verification != nil && node.Verification.Mode == "manual" {
				manualVerify = true
			}
		}
		if step.Kind == "manual" || step.Kind == "approval" || manualVerify {
			t.setState("waiting_human", "delivery", "等待人工节点："+step.Name)
			return s.saveWorkLoop(t)
		}
		if step.Status == "executing" {
			if step.Kind == "extension" && step.LeaseUntil > time.Now().Unix() {
				t.setState("running", "regression", "正在执行当前制品的回归测试")
				return s.saveWorkLoop(t)
			}
			if step.Kind == "deploy" && step.ReleaseID > 0 {
				// Existing executor reconciles the release; never dispatch the deploy again.
				updated, err := s.advanceDeliveryRunState(s.deliveryRunLedger(run.ID), t.Owner, false, m, func(child int64) { go m.startNextBatch(child) }, nil, "")
				if err != nil {
					return err
				}
				t.setState("running", "delivery", fmt.Sprintf("核对原发布执行单 #%d：%s", step.ReleaseID, updated.Status))
				return s.saveWorkLoop(t)
			}
			return fmt.Errorf("节点 %s 执行结果待核对，不能自动重放", step.Name)
		}
		return s.dispatchWorkLoop(mux, r, t, "advance_delivery", fmt.Sprintf("/api/delivery/mcp/runs/%d/advance", run.ID), map[string]any{"revision": run.Revision, "digest": run.Digest, "node_id": step.NodeID, "service": step.Service, "manual": false}, nil)
	}
	return fmt.Errorf("执行单暂无可推进节点，请核对状态")
}
func loopPlanScope(t *WorkLoop, p *DeliveryPlan) error {
	if p.Environment.Name != t.Grant.Spec.Environment || p.Environment.Protected {
		return fmt.Errorf("冻结计划环境超出任务授权")
	}
	if p.Scope == nil || len(p.Scope.WorkItems) != 1 {
		return fmt.Errorf("计划缺少工作项范围")
	}
	for _, w := range p.Scope.WorkItems {
		if w.ID != t.Grant.Spec.WorkItemID {
			return fmt.Errorf("计划扩大了工作项范围")
		}
	}
	if len(p.Scope.Changes) != len(t.current().ChangeIDs) {
		return fmt.Errorf("计划缺少本轮选定变更，请核对原范围")
	}
	seen := map[int64]bool{}
	for _, c := range p.Scope.Changes {
		if seen[c.ID] {
			return fmt.Errorf("计划重复变更")
		}
		seen[c.ID] = true
		if !slices.Contains(t.current().ChangeIDs, c.ID) || !slices.Contains(t.Grant.Spec.Applications, c.Service) {
			return fmt.Errorf("计划扩大了本轮变更范围")
		}
	}
	for _, app := range p.Services {
		if !slices.Contains(t.Grant.Spec.Applications, app.Service) {
			return fmt.Errorf("计划应用超出授权")
		}
	}
	for _, n := range p.Nodes {
		if n.Kind == "script" {
			return fmt.Errorf("任意脚本不在自动任务授权内，请走独立审核")
		}
	}
	return nil
}
func loopPreparedScope(t *WorkLoop, p *CodePreparation) error {
	if p.Protected || p.Request.Environment != t.Grant.Spec.Environment || p.PreparationRunID != t.current().PreparationID {
		return fmt.Errorf("代码任务不属于本轮授权")
	}
	return nil
}

func (s *Store) workLoopEvidence(t *WorkLoop) error {
	it := t.current()
	run := s.DeliveryRun(it.DeliveryID)
	if run == nil || run.Status != "success" || run.Owner != t.Owner || run.Plan.PreparationRunID != it.PreparationID {
		return fmt.Errorf("缺少本轮成功交付记录")
	}
	if e := loopPlanScope(t, &run.Plan); e != nil {
		return e
	}
	covered := map[string]bool{}
	suites := map[string]bool{}
	reports := []workflowAPIReport{}
	for _, step := range run.Steps {
		if step.Status != "success" {
			return fmt.Errorf("存在未成功节点")
		}
		for _, ref := range step.APIReports {
			sv := s.GetService(ref.Service)
			if sv == nil || ref.Owner != t.Owner {
				return fmt.Errorf("报告归属不匹配")
			}
			if s.apiTests == nil {
				return fmt.Errorf("测试报告服务不可用")
			}
			report, e := s.apiTests.load(sv.ApplicationID, ref.Owner, ref.RunID)
			if e != nil {
				return e
			}
			if report.Status != "success" || report.DeliveryRunID != run.ID || report.DeliveryNodeID != step.NodeID || report.Environment != t.Grant.Spec.Environment || report.StartedAt < run.CreatedAt {
				return fmt.Errorf("回归报告不是本轮执行证据")
			}
			matched := false
			for _, app := range run.Plan.Services {
				if app.Service == ref.Service && app.Artifact.ID == report.Target.ArtifactID && app.Artifact.Digest == report.Target.ArtifactDigest {
					matched = true
				}
			}
			if !matched {
				return fmt.Errorf("回归目标制品与本轮冻结制品不一致")
			}
			validSuite := false
			for _, test := range t.Grant.Regression {
				if test.Service == ref.Service && test.SuiteID == report.SuiteID && test.SuiteRevision == report.SuiteRevision {
					validSuite = true
				}
			}
			if !validSuite {
				return fmt.Errorf("报告不属于授权的回归测试集修订")
			}
			covered[ref.Service] = true
			suites[fmt.Sprintf("%s:%d:%d", ref.Service, report.SuiteID, report.SuiteRevision)] = true
			reports = append(reports, ref)
		}
	}
	for _, app := range run.Plan.Services {
		if !covered[app.Service] {
			return fmt.Errorf("应用 %s 缺少本轮匹配制品的回归报告", app.Service)
		}
		for _, test := range t.Grant.Regression {
			if test.Service == app.Service && !suites[fmt.Sprintf("%s:%d:%d", test.Service, test.SuiteID, test.SuiteRevision)] {
				return fmt.Errorf("应用 %s 缺少授权测试集的本轮报告", app.Service)
			}
		}
	}
	if len(reports) == 0 {
		return fmt.Errorf("缺少本轮回归报告")
	}
	it.Reports = reports
	return nil
}

func (s *Store) reconcileWorkLoopIntent(t *WorkLoop) error {
	i := t.Intent
	it := t.current()
	if i.Name == "save_changes" {
		var receipt string
		if s.db.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, i.Path).Scan(&receipt) == nil && receipt == digestJSON(i.Body) {
			i.State = "received"
			return s.saveWorkLoop(t)
		}
		return fmt.Errorf("保存回执尚不可确认，原草稿保留；不会重复新增")
	}
	if i.Name == "write_source" {
		op, e := s.ResearchOperation(it.SourceOperation, t.Owner)
		if e != nil {
			return e
		}
		if op.State == "success" {
			i.State = "received"
			return s.saveWorkLoop(t)
		}
		return fmt.Errorf("来源回写 %s 尚未确认，仅核对原操作", op.ID)
	}
	var run *DeliveryRun
	var body struct {
		ClientRequestID  string `json:"client_request_id"`
		Revision         int64  `json:"revision"`
		ExpectedRevision int64  `json:"expected_revision"`
	}
	_ = json.Unmarshal(i.Body, &body)
	switch i.Name {
	case "create_preparation":
		var id int64
		if s.db.QueryRow(`SELECT run_id FROM research_flow_uses WHERE owner=? AND request_id=?`, t.Owner, body.ClientRequestID).Scan(&id) == nil {
			it.PreparationID = id
			run = s.DeliveryRun(id)
		}
	case "create_delivery":
		var req DeliveryRequest
		_ = json.Unmarshal(i.Body, &req)
		run, _ = s.submittedPreflightRun(req, t.Owner)
		if run != nil {
			it.DeliveryID = run.ID
		}
	case "advance_preparation", "confirm_scope":
		run = s.DeliveryRun(it.PreparationID)
		if run != nil && run.Revision <= body.Revision {
			run = nil
		}
	case "advance_delivery":
		run = s.DeliveryRun(it.DeliveryID)
		if run != nil && run.Revision <= body.Revision {
			run = nil
		}
	case "start_code":
		if p := s.CodePreparation(it.CodeID); p != nil && p.Revision > body.ExpectedRevision && p.State != "preview" {
			i.State = "received"
			return s.saveWorkLoop(t)
		}
	}
	if run != nil {
		i.State = "received"
		i.Result, _ = json.Marshal(run)
		return s.saveWorkLoop(t)
	}
	return nil
}
