package deploy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type researchPreparationActions struct {
	Revision      int64  `json:"revision"`
	Digest        string `json:"digest"`
	NodeID        string `json:"node_id"`
	CanReview     bool   `json:"can_review"`
	ReviewBlocker string `json:"review_blocker,omitempty"`
	CanCancel     bool   `json:"can_cancel"`
	CancelBlocker string `json:"cancel_blocker,omitempty"`
}

// This narrowly cancels a preparation waiting at a human gate, before any
// executable work. Linked code jobs retain their own cancellation contract.
func (s *Store) researchPreparationCancelBlocker(v *DeliveryRun, actor string) string {
	if v == nil || v.Plan.PreparationRequest == nil || v.Plan.Template.Workbench != "research" {
		return "不是统一交付准备单"
	}
	if actor == "" || actor == "local" || v.Owner != actor {
		return "仅原发起人可以终止旧准备"
	}
	if v.Status != "waiting_confirmation" && v.Status != "pending_approval" {
		return "仅等待人工处理且尚未执行的准备可在此终止"
	}
	step, n := researchNextNode(v)
	if step == nil || n == nil || (n.Kind != "manual" && n.Kind != "approval") || (step.Status != "pending" && step.Status != "reviewing") {
		return "当前节点不是待处理的范围确认或审批"
	}
	control := s.DeliveryControl(v.ID)
	if control.State == "running" || control.State == "pausing" || control.State == "interrupted" {
		return "原执行协调器仍需核对，请先进入原流程"
	}
	if v.Plan.CodePreparationID != "" || v.Plan.PreparationRequest.CodePreparationID != "" {
		return "已关联代码准备，请在原代码节点核对并终止"
	}
	for _, st := range v.Steps {
		if st.ReleaseID != 0 || len(st.PreviousReleaseIDs) > 0 || st.LeaseUntil > 0 || st.ManualRecovery != nil || st.Evidence != nil {
			return "已有执行或恢复记录，请在原流程核对，不能直接终止"
		}
		if st.Kind == "code-prepare" || st.Kind == "code-verify" {
			if s.CodePreparation(st.Detail) != nil || (st.Status != "pending" && !(st.Status == "success" && st.Detail == "本次无需构建代码，已跳过")) {
				return "已有代码执行记录，请在原代码节点处理"
			}
		} else if st.Kind != "manual" && st.Kind != "approval" && st.Status != "pending" {
			return "已有执行记录，请在原流程核对，不能直接终止"
		}
	}
	var count int
	err := s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM sql_execution_ledger WHERE run_id=?) + (SELECT COUNT(*) FROM nacos_execution_ledger WHERE run_id=?) + (SELECT COUNT(*) FROM redis_execution_ledger WHERE run_id=?)`, v.ID, v.ID, v.ID).Scan(&count)
	if err != nil || count > 0 {
		return "已有资源执行记录或台账无法核验，请在原流程核对"
	}
	return ""
}

func (s *Store) researchPreparationActionContext(v *DeliveryRun, actor string) (*researchPreparationActions, error) {
	if v == nil || v.Plan.PreparationRequest == nil || v.Plan.Template.Workbench != "research" {
		return nil, fmt.Errorf("统一准备单不存在")
	}
	step, n := researchNextNode(v)
	if actor == "" || actor == "local" || (v.Owner != actor && (n == nil || !namedApprover(n, actor))) {
		return nil, fmt.Errorf("仅发起人或当前节点指定人员可以查看处理范围")
	}
	out := &researchPreparationActions{Revision: v.Revision, Digest: v.Digest}
	if step != nil {
		out.NodeID = step.NodeID
	}
	switch {
	case v.Status != "waiting_confirmation" && v.Status != "pending_approval":
		out.ReviewBlocker = "任务已流转或结束，请核对最新状态"
	case n == nil || !namedApprover(n, actor):
		out.ReviewBlocker = "仅当前节点指定人员可以处理"
	case n.Kind == "approval" && actor == v.Owner:
		out.ReviewBlocker = "发起人不能审批自己的发布"
	default:
		if err := s.checkResearchScope(v); err != nil {
			out.ReviewBlocker = err.Error()
		} else {
			out.CanReview = true
		}
	}
	out.CancelBlocker = s.researchPreparationCancelBlocker(v, actor)
	out.CanCancel = out.CancelBlocker == ""
	return out, nil
}

func (s *Store) cancelResearchPreparation(v *DeliveryRun, actor, digest, reason string, revision int64) (*DeliveryRun, error) {
	if v == nil || revision <= 0 || revision != v.Revision || digest == "" || digest != v.Digest {
		return nil, fmt.Errorf("准备单修订或摘要已变化，请核对最新状态")
	}
	if utf8.RuneCountInString(strings.TrimSpace(reason)) < 6 || len(reason) > 2000 {
		return nil, fmt.Errorf("请填写至少6字、最多2000字节的终止原因")
	}
	if blocker := s.researchPreparationCancelBlocker(v, actor); blocker != "" {
		return nil, fmt.Errorf("%s", blocker)
	}
	step, _ := researchNextNode(v)
	// Keep frozen scope, past reviews and skipped code intact. CAS competes with
	// a review of this same human gate; cancellation never advances a node.
	step.Status = "canceled"
	step.EndedAt = time.Now().Unix()
	step.Detail = strings.TrimSpace(step.Detail + "\n终止旧准备 · " + actor + "：" + strings.TrimSpace(reason))
	v.Status = "canceled"
	if err := s.saveDeliveryRun(v); err != nil {
		return nil, err
	}
	s.Audit(actor, "research-preparation-canceled", "delivery-run", fmt.Sprint(v.ID), map[string]any{"reason": strings.TrimSpace(reason), "digest": digest, "expected_revision": revision})
	return v, nil
}

func registerResearchPreparationActions(mux *http.ServeMux, s *Store) {
	mux.HandleFunc("GET /api/research/preparations/{id}/action-context", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		out, err := s.researchPreparationActionContext(s.deliveryRunLedger(id), deliveryActor(r))
		if err != nil {
			deliveryWriteError(w, 403, err)
			return
		}
		deliveryJSON(w, out)
	})
	mux.HandleFunc("POST /api/research/preparations/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Confirm          bool   `json:"confirm"`
			ExpectedRevision int64  `json:"expected_revision"`
			Digest           string `json:"digest"`
			Reason           string `json:"reason"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body) != nil || !body.Confirm {
			deliveryWriteError(w, 400, "请明确确认终止旧准备")
			return
		}
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		out, err := s.cancelResearchPreparation(s.deliveryRunLedger(id), deliveryActor(r), body.Digest, body.Reason, body.ExpectedRevision)
		if err != nil {
			deliveryWriteError(w, 409, err)
			return
		}
		deliveryJSON(w, out)
	})
}
