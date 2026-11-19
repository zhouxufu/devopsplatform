package deploy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPreparationScopeReviewDiffAndReadOnly(t *testing.T) {
	s, run, p := recoveryFlowFixture(t)
	original := digestJSON(s.DeliveryRun(run.ID).Plan)
	review, err := s.preparationScopeReview(p)
	if err != nil || review.Changed {
		t.Fatal(review, err)
	}
	added := &Change{Kind: "manual", Service: "orders", Title: "新增任务登记", Owner: "alice"}
	if err = s.CreateChange(added); err != nil {
		t.Fatal(err)
	}
	if err = s.SetChangeWorkItems(added.ID, []int64{run.Plan.Scope.WorkItems[0].ID}); err != nil {
		t.Fatal(err)
	}
	review, err = s.preparationScopeReview(p)
	if err != nil || !review.Changed || review.FrozenCount != 1 || review.CurrentCount != 2 || len(review.Added) != 1 || !review.CanCancel {
		t.Fatal(review, err)
	}
	if review.Added[0].ID != added.ID || !strings.Contains(review.NewPreparationHash, "work_items=") {
		t.Fatal(review)
	}
	if digestJSON(s.DeliveryRun(run.ID).Plan) != original {
		t.Fatal("diff query rewrote frozen plan")
	}
	mux := http.NewServeMux()
	registerCodePreparationRoutes(mux, s, nil)
	url := "/api/delivery/code-preparations/" + p.ID + "/scope-review"
	if w := collaborationRequest(mux, "GET", url, "outsider", ""); w.Code != 403 {
		t.Fatal("foreign scope leak", w.Body.String())
	}
	if w := collaborationRequest(mux, "GET", url, "alice", ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	before := &DeliveryScope{Changes: []Change{{ID: 1, Title: "旧标题", Status: ChangeDraft}, {ID: 2, Title: "移出"}}}
	after := &DeliveryScope{Changes: []Change{{ID: 1, Title: "新标题", Status: ChangeReady}, {ID: 3, Title: "新增"}}}
	delta := scopeReviewDiff(before, after)
	if len(delta.Modified) != 1 || len(delta.Added) != 1 || len(delta.Removed) != 1 {
		t.Fatal(delta)
	}
	after.Changes = before.Changes
	after.Changes = append([]Change(nil), before.Changes...)
	after.Changes[0].Status = ChangeReady
	after.Changes[0].UpdatedAt = 999
	if d := scopeReviewDiff(before, after); d.Changed || len(d.Modified) != 0 {
		t.Fatal("display metadata/readiness became content drift", d)
	}
}
func TestCancelStalePreparationClosesParentWithoutScopeReplay(t *testing.T) {
	s, run, p := recoveryFlowFixture(t)
	before := s.DeliveryRun(run.ID)
	beforePlan := digestJSON(before.Plan)
	c := &Change{Kind: "manual", Service: "orders", Title: "当前范围新增", Owner: "alice"}
	if err := s.CreateChange(c); err != nil {
		t.Fatal(err)
	}
	if err := s.SetChangeWorkItems(c.ID, []int64{run.Plan.Scope.WorkItems[0].ID}); err != nil {
		t.Fatal(err)
	}
	if s.checkResearchScope(before) == nil {
		t.Fatal("fixture failed to create stale scope")
	}
	mux := http.NewServeMux()
	registerCodePreparationRoutes(mux, s, nil)
	url := "/api/delivery/code-preparations/" + p.ID + "/cancel"
	payload, _ := json.Marshal(map[string]any{"confirm": true, "expected_revision": p.Revision})
	w := collaborationRequest(mux, "POST", url, "alice", string(payload))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	got := s.DeliveryRun(run.ID)
	if got.Status != "canceled" || got.Steps[0].Status != "canceled" || got.Steps[0].EndedAt == 0 || digestJSON(got.Plan) != beforePlan || got.Digest != before.Digest {
		t.Fatal("old scope not safely closed", got)
	}
	if job := s.CodePreparation(p.ID); job.State != "canceled" || len(job.Builds) != 0 {
		t.Fatal("cancel started work", job)
	}
	var audits int
	s.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='prepare-code-cancel'`).Scan(&audits)
	if audits != 1 {
		t.Fatal("cancel missing audit", audits)
	}
	// Once terminal, neither a stale repeat nor the original task may be resumed.
	w = collaborationRequest(mux, "POST", url, "alice", string(payload))
	if w.Code != 409 {
		t.Fatal("stale cancel repeated", w.Code)
	}
}
func TestPreparationCancelGuardsUnknownLeaseAndCoordinator(t *testing.T) {
	for _, mode := range []string{"lease", "uncertain", "merging", "coordinator"} {
		t.Run(mode, func(t *testing.T) {
			s, run, p := recoveryFlowFixture(t)
			switch mode {
			case "lease":
				p.LeaseUntil = time.Now().Unix() + 60
			case "uncertain":
				p.State = "uncertain"
			case "merging":
				p.State = "merging"
			case "coordinator":
				_, err := s.db.Exec(`INSERT INTO delivery_controls(run_id,state,actor,lease,lease_until,message) VALUES(?,'running','alice','fixture',?,'active')`, run.ID, time.Now().Unix()+60)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := s.saveCodePreparation(p); err != nil {
				t.Fatal(err)
			}
			mux := http.NewServeMux()
			registerCodePreparationRoutes(mux, s, nil)
			w := collaborationRequest(mux, "POST", fmt.Sprintf("/api/delivery/code-preparations/%s/cancel", p.ID), "alice", fmt.Sprintf(`{"confirm":true,"expected_revision":%d}`, p.Revision))
			if w.Code != 409 {
				t.Fatal("unsafe cancel accepted", w.Code, w.Body.String())
			}
			if s.CodePreparation(p.ID).State == "canceled" {
				t.Fatal("refusal modified task")
			}
		})
	}
}
