package deploy

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func serviceDeploymentFixture(t *testing.T) (*Store, *ReleaseManager, *ServiceDeployment) {
	t.Helper()
	s, m, req := manualFixture(t)
	spec := ServiceDeploymentSpec{Service: "orders", Environment: "test", Mode: "artifact", ArtifactID: req.Artifacts["orders"], Reason: "isolated once-authorized publication"}
	g, e := s.previewServiceDeployment(context.Background(), m, spec)
	if e != nil {
		t.Fatal(e)
	}
	task := &ServiceDeployment{ID: strings.Repeat("d", 32), Owner: "alice", Authority: "mcp", AuthorityKeyID: "fixture-key", State: "running", Stage: "authorized", Grant: *g, ExpiresAt: time.Now().Add(time.Hour).Unix()}
	if e = s.saveServiceDeployment(task); e != nil {
		t.Fatal(e)
	}
	return s, m, task
}
func TestServiceDeploymentConcurrentDispatchAtomicLinkAndObservation(t *testing.T) {
	s, m, task := serviceDeploymentFixture(t)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			copy, e := s.serviceDeployment(task.ID)
			if e == nil {
				_ = s.advanceServiceDeployment(copy, nil, m)
			}
		}()
	}
	wg.Wait()
	got, e := s.serviceDeployment(task.ID)
	if e != nil || got.ReleaseID == 0 {
		t.Fatal(got, e)
	}
	orders := s.ReleaseOrders("orders", 20)
	if len(orders) != 1 || orders[0].ID != got.ReleaseID || orders[0].Number != 1 || orders[0].Status != "waiting_manual" {
		t.Fatal(orders)
	}
	// Original result can be observed after restart/reload, with no new dispatch.
	for i := 0; i < 3; i++ {
		got, _ = s.serviceDeployment(task.ID)
		if e = s.advanceServiceDeployment(got, nil, m); e != nil {
			t.Fatal(e)
		}
	}
	if len(s.ReleaseOrders("orders", 20)) != 1 {
		t.Fatal("duplicate release")
	}
	o := s.GetReleaseOrder(got.ReleaseID)
	result := ManualPublicationResult{ArtifactID: o.ArtifactID, Digest: o.Manual.Digest, Result: "success", PerformedAt: time.Now().Unix(), Comment: "isolated human receipt"}
	if e = m.CompleteManual(o.ID, result, "operator", false); e != nil {
		t.Fatal(e)
	}
	got, _ = s.serviceDeployment(task.ID)
	got.ExpiresAt = 1
	if e = s.advanceServiceDeployment(got, nil, m); e != nil || got.State != "success" {
		t.Fatal(got, e)
	}
}
func TestServiceDeploymentDriftAndUnknownIntentNeverDispatch(t *testing.T) {
	for _, scenario := range []string{"target", "expiry", "intent", "artifact", "protected"} {
		t.Run(scenario, func(t *testing.T) {
			s, m, task := serviceDeploymentFixture(t)
			switch scenario {
			case "target":
				target := s.GetServiceEnvironment("orders", "test")
				target.DeployConfig["manual_instructions"] = "changed destination instructions"
				if e := s.UpsertServiceEnvironment(*target); e != nil {
					t.Fatal(e)
				}
			case "expiry":
				task.ExpiresAt = 1
			case "intent":
				task.Stage = "release_dispatching"
			case "artifact":
				task.Grant.ArtifactDigest = "different"
			case "protected":
				env := s.GetEnvironment("test")
				env.Protected = true
				env.Approvers = "reviewer"
				if e := s.UpsertEnvironment(*env); e != nil {
					t.Fatal(e)
				}
			}
			if e := s.advanceServiceDeployment(task, nil, m); e != nil {
				t.Fatal(e)
			}
			if task.State != "blocked" || len(s.ReleaseOrders("orders", 20)) != 0 {
				t.Fatal(task)
			}
		})
	}
}
func TestServiceDeploymentPreviewCreateOwnerAndFrozenGrant(t *testing.T) {
	s, m, task := serviceDeploymentFixture(t)
	h := Routes(s, NewBuilder(s, t.TempDir(), NewLogHub()), NewLogHub(), "", NewStepLib("../../steps"))
	if w := loopRequest(h, "GET", "/api/service-deployments/"+task.ID, "bob", nil); w.Code != 403 {
		t.Fatal(w.Code)
	}
	req := map[string]any{"id": strings.Repeat("e", 32), "spec": task.Grant.Spec, "expected_digest": task.Grant.Digest}
	post := func() *httptest.ResponseRecorder {
		b, _ := json.Marshal(req)
		r := httptest.NewRequest("POST", "/api/service-deployments", strings.NewReader(string(b)))
		r.Header.Set("X-User", "alice")
		r.Header.Set("X-Work-Loop-Authority", "mcp")
		r.Header.Set("X-Work-Loop-Key-ID", "fixture-key")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for i := 0; i < 2; i++ {
		w := post()
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if len(s.ReleaseOrders("orders", 20)) != 0 {
		t.Fatal("creation dispatched before coordinator")
	}
	req["expected_digest"] = "changed"
	if w := post(); w.Code != 409 {
		t.Fatal(w.Code)
	}
	spec := task.Grant.Spec
	spec.Branch = "unexpected"
	if _, e := s.previewServiceDeployment(context.Background(), m, spec); e == nil {
		t.Fatal("mixed scope")
	}
	spec = task.Grant.Spec
	spec.Mode = "build"
	if _, e := s.previewServiceDeployment(context.Background(), m, spec); e == nil {
		t.Fatal("build accepted existing artifact")
	}
}

func TestServiceDeploymentBuildReceiptHandoff(t *testing.T) {
	for _, scenario := range []string{"success", "wrong_commit", "failed", "lost_id", "recover_running", "active_lease"} {
		t.Run(scenario, func(t *testing.T) {
			s, m, task := serviceDeploymentFixture(t)
			a := variantArtifact(t, s, "test")
			task.Grant.Spec.Mode = "build"
			task.Grant.Spec.ArtifactID = 0
			task.Grant.ArtifactDigest = ""
			task.Grant.Commit = strings.Repeat("a", 40)
			task.Grant.Spec.Branch = "shared-test"
			task.Stage = "building"
			task.RunID = a.SourceID
			if _, e := s.db.Exec(`UPDATE pipeline_runs SET branch=?,trigger=? WHERE id=?`, task.Grant.Commit, "service-deployment:"+task.ID, a.SourceID); e != nil {
				t.Fatal(e)
			}
			switch scenario {
			case "wrong_commit":
				s.db.Exec(`UPDATE build_run_snapshots SET commit_ref=? WHERE run_id=?`, strings.Repeat("b", 40), a.SourceID)
			case "failed":
				s.FinishRun(a.SourceID, "failed")
			case "recover_running":
				task.RunID = 0
				task.Stage = "build_dispatching"
				s.SetRunStatus(a.SourceID, "running")
			case "lost_id":
				task.RunID = 0
				task.Stage = "build_dispatching"
			case "active_lease":
				task.RunID = 0
				task.Stage = "build_dispatching"
				task.LeaseUntil = time.Now().Add(time.Minute).Unix()
			}
			if e := s.saveServiceDeployment(task); e != nil {
				t.Fatal(e)
			}
			task, _ = s.serviceDeployment(task.ID)
			if e := s.advanceServiceDeployment(task, nil, m); e != nil {
				t.Fatal(e)
			}
			switch scenario {
			case "success", "lost_id":
				got, _ := s.serviceDeployment(task.ID)
				if got.ReleaseID == 0 || got.ArtifactID != a.ID || got.RunID != a.SourceID {
					t.Fatal(got)
				}
			case "recover_running":
				got, _ := s.serviceDeployment(task.ID)
				if got.RunID != a.SourceID || got.Stage != "building" {
					t.Fatal("recovered running ID not persisted", got)
				}
			case "active_lease":
				if task.State != "running" || task.RunID != 0 {
					t.Fatal("took over active dispatcher")
				}
			default:
				if task.State != "blocked" && task.State != "failed" {
					t.Fatal(task)
				}
			}
			if scenario != "success" && scenario != "lost_id" && len(s.ReleaseOrders("orders", 10)) > 0 {
				t.Fatal("invalid build published")
			}
		})
	}
}
