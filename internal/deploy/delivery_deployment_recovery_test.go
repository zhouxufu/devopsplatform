package deploy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func deploymentRecoveryFixture(t *testing.T) (*Store, *ReleaseManager, *DeliveryRun, deliveryDeploymentRecoveryRequest) {
	t.Helper()
	s, m, req := deliveryFixture(t)
	for _, name := range []string{"test-2", "test-3"} {
		if err := s.UpsertHost(Host{Name: name, Environment: "test", Addr: "127.0.0.1:22", User: "deploy"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UpsertHostGroup(HostGroup{Name: "test-group", Environment: "test", Hosts: []string{"test-1", "test-2", "test-3"}}); err != nil {
		t.Fatal(err)
	}
	req.Nodes = []DeliveryNode{{ID: "before", Kind: "manual", Name: "已完成的前置确认", Instructions: "核对"}, {ID: "deploy", Kind: "deploy", Name: "发布应用"}, {ID: "after", Kind: "manual", Name: "后续确认", Instructions: "核对"}}
	v, err := s.CreateDeliveryRun(req, "alice", m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.advanceDeliveryRun(v.ID, "alice", true, m, func(int64) { t.Fatal("manual launched deployment") }); err != nil {
		t.Fatal(err)
	}
	v, err = s.advanceDeliveryRun(v.ID, "alice", false, m, func(int64) {})
	if err != nil {
		t.Fatal(err)
	}
	child := s.GetReleaseOrder(v.Steps[1].ReleaseID)
	for i, host := range []string{"test-1", "test-2"} {
		d := &Deploy{Service: child.Service, Version: child.Version, Host: host, Environment: child.Environment, ArtifactID: child.ArtifactID, ArtifactDigest: v.Plan.Services[0].Artifact.Digest, StartedAt: time.Now().Unix(), Actor: "alice"}
		if err = s.CreateDeploy(d); err != nil {
			t.Fatal(err)
		}
		status := "failed"
		if i == 0 {
			status = "success"
		}
		if err = s.FinishDeploy(d.ID, status); err != nil {
			t.Fatal(err)
		}
		if err = s.AttachReleaseDeploys(child.ID, i+1, []int64{d.ID}); err != nil {
			t.Fatal(err)
		}
	}
	s.SetReleaseProgress(child.ID, 2)
	s.SetReleaseStatus(child.ID, "failed", "", true)
	v, err = s.advanceDeliveryRun(v.ID, "alice", false, m, func(int64) { t.Fatal("failure relaunched") })
	if err != nil {
		t.Fatal(err)
	}
	if v.Status != "running" || v.Steps[1].Status != "failed" {
		t.Fatalf("app failure ended whole run: %+v", v)
	}
	if decision, _ := deliveryControlDecision(s, v); decision != "pause" {
		t.Fatalf("failed app not paused: %s", decision)
	}
	return s, m, v, deliveryDeploymentRecoveryRequest{NodeID: v.Steps[1].NodeID, Service: "orders", ReleaseID: child.ID, Revision: v.Revision, Digest: v.Digest, Confirm: true}
}

func TestDeliveryDeploymentRetryPreservesSnapshotAndPriorSuccess(t *testing.T) {
	s, m, v, req := deploymentRecoveryFixture(t)
	plan, _ := json.Marshal(v.Plan)
	before := v.Steps[0]
	after := v.Steps[2]
	preview, err := s.previewDeploymentRetry(v, req)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(preview.Targets, []string{"test-2", "test-3"}) || preview.Skipped != 1 {
		t.Fatal("retry includes completed instance", preview)
	}
	var launches atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = s.retryDeliveryDeployment(s.DeliveryRun(v.ID), req, "alice", m, func(int64) { launches.Add(1) })
		}()
	}
	wg.Wait()
	if launches.Load() != 1 {
		t.Fatalf("retry launched %d times", launches.Load())
	}
	got := s.DeliveryRun(v.ID)
	child := s.GetReleaseOrder(got.Steps[1].ReleaseID)
	if child.ID == req.ReleaseID || !reflect.DeepEqual(child.Hosts, preview.Targets) || child.ArtifactID != preview.ArtifactID || child.Status != "queued" {
		t.Fatal("wrong retry child", child)
	}
	currentPlan, _ := json.Marshal(got.Plan)
	if string(plan) != string(currentPlan) || got.Digest != v.Digest || !reflect.DeepEqual(before, got.Steps[0]) || !reflect.DeepEqual(after, got.Steps[2]) {
		t.Fatal("retry changed approved snapshot or other steps")
	}
	if !reflect.DeepEqual(got.Steps[1].PreviousReleaseIDs, []int64{req.ReleaseID}) || s.GetReleaseOrder(req.ReleaseID).Status != "failed" {
		t.Fatal("original failure lost")
	}
	if _, err = s.resolveDeliveryDeployment(s.DeliveryRun(v.ID), req, "alice"); err == nil {
		t.Fatal("old manual form accepted after retry")
	}
}

func TestDeliveryDeploymentRecoveryRejectsUnknownOrChangedEvidence(t *testing.T) {
	for _, scenario := range []string{"timeout", "wrong-node", "old-revision", "configuration", "policy", "artifact", "active-control", "resource-node", "explicit-stop"} {
		t.Run(scenario, func(t *testing.T) {
			s, m, v, req := deploymentRecoveryFixture(t)
			switch scenario {
			case "timeout":
				ids := s.ReleaseDeployIDs(req.ReleaseID)
				s.db.Exec(`UPDATE deploys SET status='timed_out' WHERE id=?`, ids[1])
			case "wrong-node":
				req.NodeID = "after"
			case "old-revision":
				req.Revision--
			case "configuration":
				target := s.GetServiceEnvironment("orders", "test")
				target.RemoteDir = "/changed"
				if err := s.UpsertServiceEnvironment(*target); err != nil {
					t.Fatal(err)
				}
			case "policy":
				env := s.GetEnvironment("test")
				env.Protected = true
				env.Approvers = "reviewer"
				if err := s.UpsertEnvironment(*env); err != nil {
					t.Fatal(err)
				}
			case "artifact":
				if _, err := s.db.Exec(`UPDATE artifacts SET digest='changed' WHERE id=?`, v.Plan.Services[0].Artifact.ID); err != nil {
					t.Fatal(err)
				}
			case "resource-node":
				v.Steps[1].Kind = "mysql"
				if err := s.saveDeliveryRun(v); err != nil {
					t.Fatal(err)
				}
				req.Revision = v.Revision
			case "explicit-stop":
				if _, err := s.db.Exec(`INSERT INTO delivery_controls(run_id,state,actor,lease,lease_until,message) VALUES(?,'stopped','alice','',0,'操作人终止后续编排；不确定的写操作仍须核对')`, v.ID); err != nil {
					t.Fatal(err)
				}
			case "active-control":
				if _, err := s.claimDeliveryControl(v.ID, "alice"); err != nil {
					t.Fatal(err)
				}
			}
			var launches int
			if _, err := s.retryDeliveryDeployment(s.DeliveryRun(v.ID), req, "alice", m, func(int64) { launches++ }); err == nil {
				t.Fatal("unsafe retry accepted")
			}
			if launches != 0 || s.DeliveryRun(v.ID).Steps[1].ReleaseID != req.ReleaseID {
				t.Fatal("failed validation mutated run")
			}
		})
	}
}

func TestDeliveryManualRecoveryIsAttestationAndDoesNotRunNextStep(t *testing.T) {
	s, m, v, req := deploymentRecoveryFixture(t)
	// Existing failed runs remain recoverable without data migration.
	v.Status = "failed"
	if err := s.saveDeliveryRun(v); err != nil {
		t.Fatal(err)
	}
	req.Revision = v.Revision
	if _, err := s.resolveDeliveryDeployment(s.DeliveryRun(v.ID), req, "alice"); err == nil {
		t.Fatal("missing evidence accepted")
	}
	req.Comment = "已人工恢复本次制品，检查进程与健康接口正常"
	if _, err := s.resolveDeliveryDeployment(s.DeliveryRun(v.ID), req, "alice"); err == nil {
		t.Fatal("missing artifact acknowledgement accepted")
	}
	req.ConfirmArtifact = true
	got, err := s.resolveDeliveryDeployment(s.DeliveryRun(v.ID), req, "alice")
	if err != nil {
		t.Fatal(err)
	}
	evidence := got.Steps[1].ManualRecovery
	if got.Status != "running" || got.Steps[1].Status != "success" || evidence == nil || evidence.Actor != "alice" || evidence.Comment != req.Comment || evidence.ArtifactID != v.Plan.Services[0].Artifact.ID || got.Steps[2].Status != "pending" {
		t.Fatal("manual evidence/next step incorrect", got)
	}
	if s.GetReleaseOrder(req.ReleaseID).Status != "failed" {
		t.Fatal("manual attestation forged machine success")
	}
	if _, err := s.resolveDeliveryDeployment(s.DeliveryRun(v.ID), req, "alice"); err == nil {
		t.Fatal("duplicate manual confirmation accepted")
	}
	if _, err := s.advanceDeliveryRun(v.ID, "alice", false, m, func(int64) { t.Fatal("manual resolution repeated deploy") }); err == nil {
		t.Fatal("next manual gate bypassed")
	}
}

func TestDeliveryDeploymentRecoveryAPIOwnershipAndAttemptLogs(t *testing.T) {
	s, m, v, req := deploymentRecoveryFixture(t)
	mux := http.NewServeMux()
	registerDeliveryRoutes(mux, s, m)
	body, _ := json.Marshal(req)
	for _, route := range []string{"retry-deployment", "resolve-deployment", "retry-preview"} {
		method := "POST"
		if route == "retry-preview" {
			method = "GET"
		}
		r := httptest.NewRequest(method, "/api/delivery/runs/"+strconv.FormatInt(v.ID, 10)+"/"+route, strings.NewReader(string(body)))
		r.Header.Set("X-User", "other")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("other user allowed", route, w.Code)
		}
	}
	var childID int64
	if _, err := s.retryDeliveryDeployment(s.DeliveryRun(v.ID), req, "alice", m, func(id int64) { childID = id }); err != nil {
		t.Fatal(err)
	}
	if childID == 0 {
		t.Fatal("no retry child")
	}
	for _, id := range []int64{req.ReleaseID, childID, 9999} {
		r := httptest.NewRequest("GET", "/api/delivery/runs/"+strconv.FormatInt(v.ID, 10)+"/logs?release_id="+strconv.FormatInt(id, 10), nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		expected := 200
		if id == 9999 {
			expected = 403
		}
		if w.Code != expected {
			t.Fatal("attempt log ownership", id, w.Code, w.Body.String())
		}
	}
}

func TestDeliveryInterruptedOutcomeRequiresManualAttestation(t *testing.T) {
	s, _, v, req := deploymentRecoveryFixture(t)
	s.SetReleaseStatus(req.ReleaseID, "interrupted", "", true)
	ids := s.ReleaseDeployIDs(req.ReleaseID)
	if _, err := s.db.Exec(`UPDATE deploys SET status='interrupted' WHERE id=?`, ids[1]); err != nil {
		t.Fatal(err)
	}
	v.Status = "failed"
	if err := s.saveDeliveryRun(v); err != nil {
		t.Fatal(err)
	}
	req.Revision = v.Revision
	if _, err := s.previewDeploymentRetry(v, req); err == nil {
		t.Fatal("interrupted outcome became retryable")
	}
	req.ConfirmArtifact = true
	req.Comment = "已在实际环境核实并人工恢复本次制品，健康检查正常"
	got, err := s.resolveDeliveryDeployment(s.DeliveryRun(v.ID), req, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if got.Steps[1].ManualRecovery == nil || got.Steps[2].Status != "pending" || s.GetReleaseOrder(req.ReleaseID).Status != "interrupted" {
		t.Fatal("manual reconciliation lost uncertainty history or advanced another step")
	}
}

func TestDeliveryManualRecoveryEvidenceHasNoCharacterThreshold(t *testing.T) {
	for _, comment := range []string{"已完成", "好", strings.Repeat("核对", 6000)} {
		name := "long"
		if len(comment) < 100 {
			name = comment
		}
		t.Run(name, func(t *testing.T) {
			s, m, v, req := deploymentRecoveryFixture(t)
			req.ConfirmArtifact = true
			req.Comment = comment
			mux := http.NewServeMux()
			registerDeliveryRoutes(mux, s, m)
			body, err := json.Marshal(req)
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest("POST", "/api/delivery/runs/"+strconv.FormatInt(v.ID, 10)+"/resolve-deployment", strings.NewReader(string(body)))
			r.Header.Set("X-User", "alice")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("valid evidence rejected: %d %s", w.Code, w.Body.String())
			}
			got := s.DeliveryRun(v.ID)
			if got.Steps[1].ManualRecovery == nil || got.Steps[1].ManualRecovery.Comment != comment || got.Steps[2].Status != "pending" || s.GetReleaseOrder(req.ReleaseID).Status != "failed" {
				t.Fatal("attestation or original failure changed")
			}
			repeated := httptest.NewRequest("POST", r.URL.String(), strings.NewReader(string(body)))
			repeated.Header.Set("X-User", "alice")
			again := httptest.NewRecorder()
			mux.ServeHTTP(again, repeated)
			if again.Code != http.StatusConflict {
				t.Fatal("duplicate recovery accepted", again.Code)
			}
		})
	}
}

func TestDeliveryManualRecoveryStillRequiresNonemptyEvidenceAndAcknowledgement(t *testing.T) {
	s, _, v, req := deploymentRecoveryFixture(t)
	for _, input := range []deliveryDeploymentRecoveryRequest{
		func() deliveryDeploymentRecoveryRequest {
			q := req
			q.ConfirmArtifact = true
			q.Comment = " \n\t "
			return q
		}(),
		func() deliveryDeploymentRecoveryRequest { q := req; q.Comment = "已完成"; return q }(),
	} {
		if _, err := s.resolveDeliveryDeployment(s.DeliveryRun(v.ID), input, "alice"); err == nil {
			t.Fatal("required prerequisite bypassed")
		}
		if s.DeliveryRun(v.ID).Revision != v.Revision || s.GetReleaseOrder(req.ReleaseID).Status != "failed" {
			t.Fatal("invalid request mutated state")
		}
	}
}
