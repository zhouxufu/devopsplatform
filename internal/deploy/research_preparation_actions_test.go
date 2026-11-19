package deploy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func waitingPreparationFixture(t *testing.T) (*Store, *DeliveryRun) {
	s, run, p := recoveryFlowFixture(t)
	// Synthetic fixture: no worker was launched, remove the fake job and model a
	// code-free preparation at its human gate, as in the reported stale task.
	if _, err := s.db.Exec(`DELETE FROM deploy_meta WHERE key=?`, "code_preparation:"+p.ID); err != nil {
		t.Fatal(err)
	}
	run = s.deliveryRunLedger(run.ID)
	run.Status = "waiting_confirmation"
	run.Steps[0].Status = "success"
	run.Steps[0].Detail = "本次无需构建代码，已跳过"
	run.Steps[1].Status = "pending"
	if err := s.saveDeliveryRun(run); err != nil {
		t.Fatal(err)
	}
	return s, run
}

func TestWaitingPreparationActionsAndCancelStaleScope(t *testing.T) {
	s, run := waitingPreparationFixture(t)
	ctx, err := s.researchPreparationActionContext(run, "alice")
	if err != nil || !ctx.CanReview || !ctx.CanCancel {
		t.Fatal(ctx, err)
	}
	// Introduce drift without rewriting the frozen plan.
	env := s.GetEnvironment("dev")
	env.DisplayName = "changed"
	if err = s.UpsertEnvironment(*env); err != nil {
		t.Fatal(err)
	}
	frozen := digestJSON(run.Plan)
	reviews := len(run.Steps[1].Reviews)
	before := digestJSON(s.deliveryRunLedger(run.ID))
	ctx, err = s.researchPreparationActionContext(run, "alice")
	if err != nil || ctx.CanReview || !ctx.CanCancel || !strings.Contains(ctx.ReviewBlocker, "已改变") {
		t.Fatal(ctx, err)
	}
	if before != digestJSON(s.deliveryRunLedger(run.ID)) {
		t.Fatal("context mutated execution")
	}
	mux := http.NewServeMux()
	registerResearchPreparationActions(mux, s)
	url := fmt.Sprintf("/api/research/preparations/%d", run.ID)
	if w := collaborationRequest(mux, "GET", url+"/action-context", "outsider", ""); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	payload, _ := json.Marshal(map[string]any{"confirm": true, "expected_revision": run.Revision, "digest": run.Digest, "reason": "策略改变重新准备"})
	if w := collaborationRequest(mux, "POST", url+"/cancel", "outsider", string(payload)); w.Code != 409 {
		t.Fatal(w.Code)
	}
	if w := collaborationRequest(mux, "POST", url+"/cancel", "alice", string(payload)); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	got := s.deliveryRunLedger(run.ID)
	if got.Status != "canceled" || got.Steps[1].Status != "canceled" || !strings.Contains(got.Steps[1].Detail, "策略改变重新准备") || got.Revision != run.Revision+1 || got.Digest != run.Digest || digestJSON(got.Plan) != frozen || len(got.Steps[1].Reviews) != reviews {
		t.Fatal("cancellation rewrote frozen state", got)
	}
	var audits int
	s.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='research-preparation-canceled'`).Scan(&audits)
	if audits != 1 {
		t.Fatal("missing audit", audits)
	}
	if w := collaborationRequest(mux, "POST", url+"/cancel", "alice", string(payload)); w.Code != 409 {
		t.Fatal("duplicate accepted", w.Code)
	}
	if err = s.saveDeliveryRun(run); err != ErrRunConflict {
		t.Fatal("stale review can overwrite cancel", err)
	}
}

func TestWaitingPreparationCancelGuards(t *testing.T) {
	for _, mode := range []string{"revision", "digest", "reason", "lease", "coordinator", "code", "release", "uncertain", "terminal"} {
		t.Run(mode, func(t *testing.T) {
			s, run := waitingPreparationFixture(t)
			revision, digest, reason := run.Revision, run.Digest, "按当前范围重新准备"
			switch mode {
			case "revision":
				revision--
			case "digest":
				digest = "other"
			case "reason":
				reason = "短"
			case "lease":
				run.Steps[1].LeaseUntil = time.Now().Unix() + 60
			case "coordinator":
				s.db.Exec(`INSERT INTO delivery_controls(run_id,state,actor,lease,lease_until,message) VALUES(?,'running','alice','fixture',?,'active')`, run.ID, time.Now().Unix()+60)
			case "code":
				run.Steps[0].Detail = "already-executed"
			case "release":
				run.Steps[0].ReleaseID = 20
			case "uncertain":
				run.Steps[0].Kind = "sql"
				run.Steps[0].Status = "uncertain"
			case "terminal":
				run.Status = "success"
			}
			if _, err := s.cancelResearchPreparation(run, "alice", digest, reason, revision); err == nil {
				t.Fatal("unsafe cancel allowed")
			}
			if s.deliveryRunLedger(run.ID).Status != "waiting_confirmation" {
				t.Fatal("rejected action wrote state")
			}
		})
	}
}

func TestReleaseRollbackFailurePersonalResolution(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mux := http.NewServeMux()
	registerResearchCollaboration(mux, s)
	for _, status := range []string{"rolled-back", "rollback-failed", "rolled_back", "failed", "running", "needs_review", "unknown"} {
		t.Run(status, func(t *testing.T) {
			d := &Deploy{Service: "orders", Host: "fixture-" + status, Status: status}
			if err := s.CreateDeploy(d); err != nil {
				t.Fatal(err)
			}
			if err := s.FinishDeploy(d.ID, status); err != nil {
				t.Fatal(err)
			}
			o := &ReleaseOrder{Service: "orders", Actor: "alice", Status: "failed", Environment: "dev", Total: 1, Failed: 1}
			if err := s.CreateReleaseOrder(o); err != nil {
				t.Fatal(err)
			}
			// CreateReleaseOrder inserts the immutable shell; update the execution link.
			o.DeployIDs = []int64{d.ID}
			if err := s.AttachReleaseDeploys(o.ID, 0, o.DeployIDs); err != nil {
				t.Fatal(err)
			}
			target, err := s.attentionTarget("alice", fmt.Sprint("release:", o.ID))
			if err != nil {
				t.Fatal(err)
			}
			settled := status == "rolled-back" || status == "rolled_back" || status == "rollback-failed" || status == "failed"
			if target.CanIgnore != settled {
				t.Fatal(status, target.CanIgnore)
			}
			rows, err := s.unresolvedResearchExceptions("alice")
			if err != nil {
				t.Fatal(err)
			}
			var task ResearchTask
			for _, row := range rows {
				if row.ID == fmt.Sprint("release:", o.ID) {
					task = row
				}
			}
			body, _ := json.Marshal(map[string]any{"id": task.ID, "digest": task.ResolutionDigest, "comment": "已核对原发布失败日志", "action": "review"})
			w := collaborationRequest(mux, "POST", "/api/research/exceptions/resolve", "alice", string(body))
			if (w.Code == 200) != settled {
				t.Fatal(status, w.Code, w.Body.String())
			}
			if s.GetReleaseOrder(o.ID).Status != "failed" || s.GetDeploy(d.ID).Status != status {
				t.Fatal("personal action changed execution")
			}
		})
	}
	if researchDeploySettled(&Deploy{Status: "failed", AutoRecheck: &JavaAutoRecheck{State: "checking"}}) {
		t.Fatal("active recheck considered settled")
	}
}

func TestReleaseRollbackFailureIgnoreKeepsFailureAndHistory(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d := &Deploy{Service: "orders", Host: "fixture"}
	if err = s.CreateDeploy(d); err != nil {
		t.Fatal(err)
	}
	if err = s.FinishDeploy(d.ID, "rollback-failed"); err != nil {
		t.Fatal(err)
	}
	o := &ReleaseOrder{Service: "orders", Actor: "alice", Status: "failed", Environment: "dev", Total: 1, Failed: 1}
	if err = s.CreateReleaseOrder(o); err != nil {
		t.Fatal(err)
	}
	if err = s.AttachReleaseDeploys(o.ID, 0, []int64{d.ID}); err != nil {
		t.Fatal(err)
	}
	item := attentionTestItem(t, s, "alice", fmt.Sprint("release:", o.ID))
	if _, err = s.setResearchAttention("alice", item, "ignore", "失败结果已核对暂不跟进", "rollback-ignore-fixture"); err != nil {
		t.Fatal(err)
	}
	raw, err := s.unresolvedResearchExceptions("alice")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.attentionTasks("alice", "todo", raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range rows {
		if task.ID == item.ID {
			t.Fatal("ignored task remains actionable", task)
		}
	}
	done, err := s.attentionTasks("alice", "done", nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, task := range done {
		if task.AttentionKey == item.ID && task.AttentionReason == "失败结果已核对暂不跟进" {
			found = true
		}
	}
	if !found {
		t.Fatal("ignore reason/history missing", done)
	}
	if s.GetDeploy(d.ID).Status != "rollback-failed" || s.GetReleaseOrder(o.ID).Status != "failed" {
		t.Fatal("ignore forged successful execution")
	}
}
