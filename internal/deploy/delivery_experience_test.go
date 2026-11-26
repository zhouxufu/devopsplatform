package deploy

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func preflightFor(t *testing.T, s *Store, m *ReleaseManager, req DeliveryRequest, actor string) DeliveryRequest {
	t.Helper()
	p, secrets, err := s.ResolveDelivery(req, m)
	if err != nil || len(p.Blockers) > 0 {
		t.Fatalf("resolve: %v %+v", err, p)
	}
	req.PreflightToken, _ = s.signDeliveryPreflight(req, p, secrets, actor)
	return req
}
func TestDeliveryPreflightBindsInputTargetsSecretsAndActor(t *testing.T) {
	for _, mutation := range []string{"actor", "reason", "environment", "artifact", "secret", "target", "signature", "expiry"} {
		t.Run(mutation, func(t *testing.T) {
			s, m, req := deliveryFixture(t)
			req = preflightFor(t, s, m, req, "alice")
			actor := "alice"
			switch mutation {
			case "actor":
				actor = "bob"
			case "reason":
				req.Reason = "different"
			case "environment":
				req.Environment = "prod"
			case "artifact":
				a := s.GetArtifact(req.Artifacts["orders"])
				if _, e := s.db.Exec("UPDATE artifacts SET digest='sha256:changed' WHERE id=?", a.ID); e != nil {
					t.Fatal(e)
				}
			case "secret", "target":
				target := *s.GetServiceEnvironment("orders", "test")
				if mutation == "secret" {
					target.RuntimeEnv = map[string]string{"API_KEY": "changed"}
				} else {
					target.StartCmd = "different"
				}
				if e := s.UpsertServiceEnvironment(target); e != nil {
					t.Fatal(e)
				}
			case "signature":
				req.PreflightToken += "x"
			case "expiry":
				parts := strings.Split(req.PreflightToken, ".")
				raw, _ := base64.RawURLEncoding.DecodeString(parts[0])
				var c deliveryPreflight
				json.Unmarshal(raw, &c)
				c.Expires = time.Now().Unix() - 1
				raw, _ = json.Marshal(c)
				mac := hmac.New(sha256.New, s.secretKey)
				mac.Write(raw)
				req.PreflightToken = base64.RawURLEncoding.EncodeToString(raw) + "." + hex.EncodeToString(mac.Sum(nil))
			}
			if _, e := s.CreateDeliveryRun(req, actor, m); e == nil {
				t.Fatal("stale preflight accepted")
			}
			if len(s.DeliveryRuns()) != 0 {
				t.Fatal("invalid preflight inserted run")
			}
		})
	}
}
func TestDeliveryPreflightSingleUseIsIdempotentAndNoDeployment(t *testing.T) {
	s, m, req := deliveryFixture(t)
	req = preflightFor(t, s, m, req, "alice")
	a, e := s.CreateDeliveryRun(req, "alice", m)
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.CreateDeliveryRun(req, "alice", m)
	if e != nil || a.ID != b.ID || len(s.DeliveryRuns()) != 1 {
		t.Fatalf("duplicate: %+v %v", b, e)
	}
	if len(s.ReleaseOrders("", 100)) != 0 {
		t.Fatal("submission started release")
	}
	mux := http.NewServeMux()
	registerDeliveryRoutes(mux, s, m)
	body, _ := json.Marshal(DeliveryRequest{TemplateID: req.TemplateID, Environment: req.Environment, Artifacts: req.Artifacts})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/delivery/runs", bytes.NewReader(body)))
	if w.Code == 200 {
		t.Fatal("API allowed missing preflight")
	}
}
func versionForBinding(t *testing.T, s *Store, req *DeliveryRequest) *ReleaseVersion {
	t.Helper()
	v := &ReleaseVersion{Version: "release-1", Title: "QA", Owner: "alice"}
	if e := s.CreateReleaseVersion(v); e != nil {
		t.Fatal(e)
	}
	c := &Change{Kind: "code", Title: "QA code", Service: "orders", Reference: "feature/qa", Owner: "alice"}
	if e := s.CreateChange(c); e != nil {
		t.Fatal(e)
	}
	s.SetChangeReady(c.ID, true)
	if e := s.SetReleaseVersionChanges(v.ID, []int64{c.ID}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SetReleaseVersionReady(v.ID); e != nil {
		t.Fatal(e)
	}
	tpl := &DeliveryTemplate{Name: "version", Mode: "version", Owner: "alice", Nodes: []DeliveryNode{{ID: "deploy", Name: "deploy", Kind: "deploy"}}}
	if e := s.SaveDeliveryTemplate(tpl); e != nil {
		t.Fatal(e)
	}
	req.TemplateID = tpl.ID
	req.VersionID = v.ID
	return s.GetReleaseVersion(v.ID)
}
func TestDeliveryVersionArtifactMappingRequiresConfirmationAndLocksAcrossEnvironments(t *testing.T) {
	s, m, req := deliveryFixture(t)
	v := versionForBinding(t, s, &req)
	b := []VersionArtifactBinding{{Service: "orders", ArtifactID: req.Artifacts["orders"]}}
	if _, e := s.SaveVersionArtifacts(v.ID, b, "alice"); e == nil {
		t.Fatal("unconfirmed coverage accepted")
	}
	b[0].CodeConfirmed = true
	pinned, e := s.SaveVersionArtifacts(v.ID, b, "alice")
	if e != nil {
		t.Fatal(e)
	}
	if pinned.Bindings[0].Digest == "" || pinned.Locked {
		t.Fatal("mapping missing digest")
	}
	req = preflightFor(t, s, m, req, "alice")
	if _, e := s.CreateDeliveryRun(req, "alice", m); e != nil {
		t.Fatal(e)
	}
	if !s.VersionArtifacts(v.ID).Locked {
		t.Fatal("mapping not locked on first submission")
	}
	if _, e := s.SaveVersionArtifacts(v.ID, b, "alice"); e == nil {
		t.Fatal("overwrote locked mapping")
	}
	req.Environment = "prod"
	req.PreflightToken = ""
	req = preflightFor(t, s, m, req, "alice")
	run, e := s.CreateDeliveryRun(req, "alice", m)
	if e != nil || run.Status != "pending_approval" {
		t.Fatalf("cross-env same artifact: %+v %v", run, e)
	}
	if len(s.ReleaseOrders("", 100)) != 0 {
		t.Fatal("approval submission started deployment")
	}
}
func TestDeliveryVersionMissingMappingCannotSubmitWithPreflight(t *testing.T) {
	s, m, req := deliveryFixture(t)
	versionForBinding(t, s, &req)
	req = preflightFor(t, s, m, req, "alice")
	if _, e := s.CreateDeliveryRun(req, "alice", m); e == nil {
		t.Fatal("new version delivery lacks pinned artifacts")
	}
}
func TestDeliveryRunPaginationIncludesHistoryBeyond100AndReturnsOnlySummary(t *testing.T) {
	s, m, req := deliveryFixture(t)
	p, _, e := s.ResolveDelivery(req, m)
	if e != nil {
		t.Fatal(e)
	}
	p.Version = &ReleaseVersion{ID: 99, Version: "v99"}
	raw, _ := json.Marshal(p)
	for i := 0; i < 151; i++ {
		if _, e := s.db.Exec("INSERT INTO delivery_runs(plan,digest,status,execution,env_secrets,owner,approver,created_at,revision) VALUES(?,?,'approved','[]',?,'alice','',?,1)", string(raw), "digest", `{"secret":"DO-NOT-RETURN"}`, int64(i+100)); e != nil {
			t.Fatal(e)
		}
	}
	page, e := s.DeliveryRunPage("发布订单", "approved", "test", "", "alice", 0, 0, 8, 20)
	if e != nil || page.Total != 151 || len(page.Items) != 11 {
		t.Fatalf("history: %+v %v", page, e)
	}
	encoded, _ := json.Marshal(page)
	if strings.Contains(string(encoded), "DO-NOT-RETURN") || strings.Contains(string(encoded), "/not-executed.jar") {
		t.Fatal("summary leaked snapshot")
	}
	allVersions, e := s.DeliveryRunPage("", "approved", "test", "0", "alice", 0, 0, 1, 20)
	if e != nil || allVersions.Total != 151 {
		t.Fatalf("zero version means all versions: %+v %v", allVersions, e)
	}
	for _, invalid := range []string{"-1", "invalid", "99 OR 1=1"} {
		if _, e := s.DeliveryRunPage("", "", "", invalid, "", 0, 0, 1, 20); e == nil {
			t.Fatalf("invalid version filter accepted: %q", invalid)
		}
	}
	filtered, e := s.DeliveryRunPage("", "approved", "test", "", "alice", 200, 210, 1, 20)
	if e != nil || filtered.Total != 11 {
		t.Fatalf("date filter %+v %v", filtered, e)
	}
	filtered, e = s.DeliveryRunPage("", "approved", "test", "99", "alice", 0, 0, 1, 20)
	if e != nil || filtered.Total != 151 {
		t.Fatalf("numeric version filter: %+v %v", filtered, e)
	}
	if _, e := s.DeliveryRunPage("", "", "", "", "", 0, 0, 0, 20); e == nil {
		t.Fatal("invalid pagination accepted")
	}
	filtered, e = s.DeliveryRunPage("' OR 1=1 --", "", "", "", "", 0, 0, 1, 20)
	if e != nil || filtered.Total != 0 {
		t.Fatal("search SQL injection")
	}
}
func TestDeliveryCoordinatorPausesAtManualAndNeverStartsFromApproval(t *testing.T) {
	s, m, req := deliveryFixture(t)
	req.Nodes = []DeliveryNode{{ID: "observe", Name: "观察", Kind: "manual", Instructions: "确认就绪"}, {ID: "deploy", Name: "发布", Kind: "deploy"}}
	run, e := s.CreateDeliveryRun(req, "alice", m)
	if e != nil {
		t.Fatal(e)
	}
	if s.DeliveryControl(run.ID).State != "idle" {
		t.Fatal("submission started coordinator")
	}
	lease, e := s.claimDeliveryControl(run.ID, "alice")
	if e != nil {
		t.Fatal(e)
	}
	if _, e := s.claimDeliveryControl(run.ID, "alice"); e == nil {
		t.Fatal("duplicate coordinator")
	}
	s.runDeliveryControl(run.ID, "alice", lease, m)
	if s.DeliveryControl(run.ID).State != "paused" || s.DeliveryRun(run.ID).Steps[0].Status != "pending" {
		t.Fatal("manual node auto-confirmed")
	}
	if len(s.ReleaseOrders("", 100)) != 0 {
		t.Fatal("manual pause started deployment")
	}
	run.Status = "running"
	run.Steps[0].Kind = "mysql"
	run.Steps[0].Status = "executing"
	s.saveDeliveryRun(run)
	if d, _ := deliveryControlDecision(s, run); d != "pause" {
		t.Fatal("uncertain SQL repeated")
	}
	run.Status = "failed"
	if d, _ := deliveryControlDecision(s, run); d != "finish" {
		t.Fatal("failed write repeated")
	}
}
func TestDeliveryCoordinatorWaitsForReleaseAndBatchObservation(t *testing.T) {
	s, m, req := deliveryFixture(t)
	run, e := s.CreateDeliveryRun(req, "alice", m)
	if e != nil {
		t.Fatal(e)
	}
	run, e = s.advanceDeliveryRun(run.ID, "alice", false, m, func(int64) {})
	if e != nil {
		t.Fatal(e)
	}
	id := run.Steps[0].ReleaseID
	if d, _ := deliveryControlDecision(s, run); d != "wait" {
		t.Fatal("did not wait for release")
	}
	s.SetReleaseStatus(id, "waiting_next_batch", "", false)
	if d, _ := deliveryControlDecision(s, run); d != "pause" {
		t.Fatal("batch auto-confirmed")
	}
	s.SetReleaseStatus(id, "success", "", true)
	if d, _ := deliveryControlDecision(s, run); d != "advance" {
		t.Fatal("success not reconciled")
	}
}
func TestDeliveryCoordinatorLeaseExpiryAndAuthorization(t *testing.T) {
	s, m, req := deliveryFixture(t)
	run, e := s.CreateDeliveryRun(req, "alice", m)
	if e != nil {
		t.Fatal(e)
	}
	lease, e := s.claimDeliveryControl(run.ID, "alice")
	if e != nil {
		t.Fatal(e)
	}
	s.db.Exec("UPDATE delivery_controls SET lease_until=1 WHERE run_id=?", run.ID)
	if s.DeliveryControl(run.ID).State != "interrupted" {
		t.Fatal("expired worker shown active")
	}
	next, e := s.claimDeliveryControl(run.ID, "alice")
	if e != nil || next == lease {
		t.Fatal("lease not reclaimed")
	}
	s.finishDeliveryControl(run.ID, lease, "completed", "stale worker")
	if s.DeliveryControl(run.ID).State != "running" {
		t.Fatal("stale worker overwrote lease")
	}
	mux := http.NewServeMux()
	registerDeliveryRoutes(mux, s, m)
	for _, user := range []string{"outsider", "alice"} {
		body := `{"digest":"wrong","confirm":true}`
		r := httptest.NewRequest("POST", fmt.Sprintf("/api/delivery/runs/%d/start", run.ID), strings.NewReader(body))
		r.Header.Set("X-User", user)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code == 200 {
			t.Fatal("invalid actor or snapshot started worker")
		}
	}
}
func TestDeliveryLogsAndRollbackCannotAccessOtherRelease(t *testing.T) {
	s, m, req := deliveryFixture(t)
	run, e := s.CreateDeliveryRun(req, "alice", m)
	if e != nil {
		t.Fatal(e)
	}
	mux := http.NewServeMux()
	registerDeliveryRoutes(mux, s, m)
	for _, path := range []string{"logs", "rollback-candidate"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", fmt.Sprintf("/api/delivery/runs/%d/%s?release_id=99999", run.ID, path), nil))
		if w.Code == 200 {
			t.Fatal("cross-run release read")
		}
	}
}

func TestDeliveryLogsUseLiveTailAndPersistedLogWithSecretRedaction(t *testing.T) {
	s, m, req := deliveryFixture(t)
	run, e := s.CreateDeliveryRun(req, "alice", m)
	if e != nil {
		t.Fatal(e)
	}
	run, e = s.advanceDeliveryRun(run.ID, "alice", false, m, func(int64) {})
	if e != nil {
		t.Fatal(e)
	}
	d := &Deploy{Service: "orders", Version: "new", Host: "test-1", Environment: "test", Actor: "alice"}
	if e := s.CreateDeploy(d); e != nil {
		t.Fatal(e)
	}
	if e := s.AttachReleaseDeploys(run.Steps[0].ReleaseID, 0, []int64{d.ID}); e != nil {
		t.Fatal(e)
	}
	s.AppendDeployLog(d.ID, "persisted never-serialize-me")
	dp := NewDeployer(s, NewLogHub(), "")
	m.dp = dp
	dp.emit(d.ID, "live never-serialize-me")
	mux := http.NewServeMux()
	registerDeliveryRoutes(mux, s, m)
	read := func() string {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", fmt.Sprintf("/api/delivery/runs/%d/logs?release_id=%d", run.ID, run.Steps[0].ReleaseID), nil))
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		text := w.Body.String()
		if strings.Contains(text, "never-serialize-me") {
			t.Fatal("runtime secret in logs")
		}
		return text
	}
	if !strings.Contains(read(), "live [REDACTED]") {
		t.Fatal("live tail missing")
	}
	s.FinishDeploy(d.ID, "success")
	if !strings.Contains(read(), "persisted [REDACTED]") {
		t.Fatal("persisted log missing")
	}
}

func TestDeliveryCoordinatorAdvancesCompletedReleaseExactlyOnce(t *testing.T) {
	s, m, req := deliveryFixture(t)
	run, e := s.CreateDeliveryRun(req, "alice", m)
	if e != nil {
		t.Fatal(e)
	}
	lease, e := s.claimDeliveryControl(run.ID, "alice")
	if e != nil {
		t.Fatal(e)
	}
	launches := 0
	s.runDeliveryControlWithAdvance(run.ID, lease, func() error {
		_, err := s.advanceDeliveryRun(run.ID, "alice", false, m, func(childID int64) { launches++; s.SetReleaseStatus(childID, "success", "", true) })
		return err
	})
	if launches != 1 || s.DeliveryRun(run.ID).Status != "success" || s.DeliveryControl(run.ID).State != "completed" {
		t.Fatal("ordinary nodes failed to complete exactly once")
	}
}
func TestDeliveryRollbackCandidateRequiresSamePreviousArtifactOnEveryTarget(t *testing.T) {
	s, m, req := deliveryFixture(t)
	run, e := s.CreateDeliveryRun(req, "alice", m)
	if e != nil {
		t.Fatal(e)
	}
	run, e = s.advanceDeliveryRun(run.ID, "alice", false, m, func(int64) {})
	if e != nil {
		t.Fatal(e)
	}
	old := &Deploy{Service: "orders", Version: "old", Host: "test-1", Environment: "test", Actor: "alice", ArtifactID: req.Artifacts["orders"]}
	if e := s.CreateDeploy(old); e != nil {
		t.Fatal(e)
	}
	s.FinishDeploy(old.ID, "success")
	current := &Deploy{Service: "orders", Version: "new", Host: "test-1", Environment: "test", Actor: "alice", ArtifactID: req.Artifacts["orders"]}
	if e := s.CreateDeploy(current); e != nil {
		t.Fatal(e)
	}
	s.FinishDeploy(current.ID, "success")
	s.AttachReleaseDeploys(run.Steps[0].ReleaseID, 0, []int64{current.ID})
	s.SetReleaseStatus(run.Steps[0].ReleaseID, "success", "", true)
	mux := http.NewServeMux()
	registerDeliveryRoutes(mux, s, m)
	call := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", fmt.Sprintf("/api/delivery/runs/%d/rollback-candidate?release_id=%d", run.ID, run.Steps[0].ReleaseID), nil))
		return w
	}
	if w := call(); w.Code != 200 || !strings.Contains(w.Body.String(), "artifact_id") {
		t.Fatal("valid old artifact candidate missing")
	}
	if len(s.ReleaseOrders("", 100)) != 1 {
		t.Fatal("candidate query started rollback")
	}
	s.db.Exec("UPDATE deploys SET artifact_id=0 WHERE id=?", old.ID)
	if w := call(); w.Code == 200 {
		t.Fatal("unknown old artifact accepted")
	}
}

func TestDeliveryNacosPreflightIgnoresRandomBackupNonceButDetectsRemoteDrift(t *testing.T) {
	s, m, n, c := nacosFixture(t)
	tpl := &DeliveryTemplate{Name: "config only", Mode: "fixed", Owner: "alice", Nodes: []DeliveryNode{{ID: "config", Name: "配置变更", Kind: "nacos", ChangeIDs: []int64{c.ID}}}}
	if e := s.SaveDeliveryTemplate(tpl); e != nil {
		t.Fatal(e)
	}
	req := preflightFor(t, s, m, DeliveryRequest{TemplateID: tpl.ID, Environment: "test", Reason: "config QA"}, "alice")
	if _, e := s.CreateDeliveryRun(req, "alice", m); e != nil {
		t.Fatal("stable remote content invalidated by random backup nonce", e)
	}
	req = preflightFor(t, s, m, DeliveryRequest{TemplateID: tpl.ID, Environment: "test", Reason: "config QA"}, "alice")
	n.change("feature:\n  enabled: true\n")
	if _, e := s.CreateDeliveryRun(req, "alice", m); e == nil {
		t.Fatal("changed Nacos baseline accepted")
	}
	_, posts := n.snapshot()
	if posts != 0 {
		t.Fatal("preflight/submission changed remote configuration")
	}
}

func TestDeliveryStopUncertainWriteRequiresPauseAndPreservesLedger(t *testing.T) {
	s, m, req := deliveryFixture(t)
	run, e := s.CreateDeliveryRun(req, "alice", m)
	if e != nil {
		t.Fatal(e)
	}
	run.Status = "running"
	run.Steps = []DeliveryStep{{NodeID: "sql", Name: "SQL", Kind: "mysql", Status: "executing", StartedAt: 1}}
	if e := s.saveDeliveryRun(run); e != nil {
		t.Fatal(e)
	}
	mux := http.NewServeMux()
	registerDeliveryRoutes(mux, s, m)
	call := func(user, reason string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(map[string]any{"digest": run.Digest, "confirm": true, "reason": reason})
		r := httptest.NewRequest("POST", fmt.Sprintf("/api/delivery/runs/%d/stop", run.ID), bytes.NewReader(raw))
		r.Header.Set("X-User", user)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	if w := call("outsider", "核对中断"); w.Code == 200 {
		t.Fatal("outsider stopped run")
	}
	lease, e := s.claimDeliveryControl(run.ID, "alice")
	if e != nil {
		t.Fatal(e)
	}
	if w := call("alice", "核对中断"); w.Code == 200 {
		t.Fatal("active coordinator stopped unsafely")
	}
	s.finishDeliveryControl(run.ID, lease, "paused", "operator paused")
	if w := call("alice", ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	got := s.DeliveryRun(run.ID)
	if got.Status != "failed" || got.Steps[0].Status != "executing" {
		t.Fatal("uncertain SQL result falsified")
	}
	if _, e := s.AdvanceDeliveryRun(run.ID, "alice", false, m); e == nil {
		t.Fatal("stopped write restarted")
	}
}
