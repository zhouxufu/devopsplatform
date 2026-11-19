package deploy

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// Existing behavior tests exercise the unified model; no runtime adapter
// accepts a standalone research template.
func saveUnifiedPreparationFixture(s *Store, t *DeliveryTemplate) error {
	t.Preparation = t.Nodes
	t.Workbench = ""
	t.Nodes = []DeliveryNode{{ID: "publish", Name: "Finish delivery", Kind: "manual", Instructions: "fixture only"}}
	for i := range t.Preparation {
		if t.Preparation[i].Kind == "code-prepare" || t.Preparation[i].Kind == "code-verify" {
			t.Preparation[i].ApplicationScope = "delivery"
		}
	}
	return s.SaveDeliveryTemplate(t)
}

func TestFeatureRefCreateOnlyAndNoOverwrite(t *testing.T) {
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	work := filepath.Join(root, "work")
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatal(err, string(out))
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "--bare", origin)
	git("init", work)
	git("-C", work, "-c", "user.name=Fixture", "-c", "user.email=fixture@local", "commit", "--allow-empty", "-m", "base")
	git("-C", work, "branch", "-M", "main")
	git("-C", work, "push", origin, "main")
	sha := git("-C", work, "rev-parse", "HEAD")
	newSession := func() *gitSession {
		g, err := newGitSession(origin, nil)
		if err != nil {
			t.Fatal(err)
		}
		g.args = append(g.args, "-c", "protocol.file.allow=always")
		t.Cleanup(g.close)
		return g
	}
	repo := FeatureRepository{URL: origin, BaseBranch: "main", BaseCommit: sha}
	if err := createFeatureRef(context.Background(), newSession(), repo, "feature/task"); err != nil {
		t.Fatal(err)
	}
	if got := git("--git-dir", origin, "rev-parse", "refs/heads/feature/task"); got != sha {
		t.Fatal("wrong baseline", got)
	}
	git("-C", work, "-c", "user.name=Fixture", "-c", "user.email=fixture@local", "commit", "--allow-empty", "-m", "other")
	other := git("-C", work, "rev-parse", "HEAD")
	git("-C", work, "push", origin, other+":refs/heads/feature/task")
	if err := createFeatureRef(context.Background(), newSession(), repo, "feature/task"); err == nil {
		t.Fatal("overwrote competing ref")
	}
	if got := git("--git-dir", origin, "rev-parse", "refs/heads/feature/task"); got != other {
		t.Fatal("remote changed")
	}
}
func TestFeaturePlanCASAndNoReplay(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := FeatureBranchPlan{ID: "test", Owner: "alice", State: "preview", Revision: 1}
	raw, _ := json.Marshal(p)
	if _, err = s.db.Exec(`INSERT INTO feature_branch_plans(id,document,revision) VALUES(?,?,1)`, p.ID, string(raw)); err != nil {
		t.Fatal(err)
	}
	a, b := s.featureBranchPlan(p.ID), s.featureBranchPlan(p.ID)
	a.State = "running"
	if err = s.saveFeatureBranchPlan(a); err != nil {
		t.Fatal(err)
	}
	if err = s.saveFeatureBranchPlan(b); err == nil {
		t.Fatal("competing writer accepted")
	}
	if _, err = s.executeFeatureBranch(context.Background(), a, false); err == nil {
		t.Fatal("running plan replayed")
	}
}
func TestFeaturePreviewDeduplicatesRepositoriesAndFreezesBaseline(t *testing.T) {
	s, m, _, _, w := standardManualFixture(t)
	_ = m
	for _, name := range []string{"one", "two"} {
		if err := s.UpsertService(Service{Name: name, GitURL: "https://example.invalid/shared.git", ArtifactType: "jar"}); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	sha := strings.Repeat("a", 40)
	discover := func(context.Context, string, *ResourceConnection) (*GitBranches, error) {
		calls++
		return &GitBranches{DefaultBranch: "main", Branches: []GitBranch{{Name: "main", Commit: sha}}}, nil
	}
	req := FeatureBranchRequest{WorkItemID: w.ID, Services: []string{"one", "two", "one"}}
	p, err := s.previewFeatureBranchWithDiscovery(context.Background(), req, "alice", discover)
	if err != nil || calls != 1 || len(p.Repositories) != 1 || len(p.Repositories[0].Services) != 2 || p.Repositories[0].BaseCommit != sha || p.State != "preview" {
		t.Fatal("bad grouping", p, err, calls)
	}
	second, err := s.previewFeatureBranchWithDiscovery(context.Background(), req, "alice", discover)
	if err != nil || second.Branch == p.Branch {
		t.Fatal("repeated task branch collided", err)
	}
	req.BaseBranch = "missing"
	req.BaseCommit = sha
	if _, err = s.previewFeatureBranchWithDiscovery(context.Background(), req, "alice", discover); err == nil {
		t.Fatal("missing named base accepted")
	}
	if len(s.DeliveryRuns()) != 0 {
		t.Fatal("branch preview launched delivery")
	}
}
func verificationFixture() *DeliveryRun {
	return &DeliveryRun{ID: 7, Digest: "frozen", Plan: DeliveryPlan{Environment: Environment{Name: "test"}, Services: []DeliveryService{{Service: "orders", Artifact: Artifact{Digest: "sha256:a"}}}}}
}
func TestHTTPVerificationRequiresArtifactIdentity(t *testing.T) {
	for _, kind := range []string{"valid", "wrong-artifact", "missing-result", "failed", "wrong-environment", "status"} {
		t.Run(kind, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Query().Get("artifact_digest") != "sha256:a" {
					t.Error("unscoped request")
				}
				result := map[string]any{"service": "orders", "environment": "test", "artifact_digest": "sha256:a", "passed": true}
				switch kind {
				case "wrong-artifact":
					result["artifact_digest"] = "sha256:b"
				case "missing-result":
					delete(result, "passed")
				case "failed":
					result["passed"] = false
				case "wrong-environment":
					result["environment"] = "prod"
				case "status":
					w.WriteHeader(500)
				}
				json.NewEncoder(w).Encode(result)
			}))
			defer server.Close()
			n := DeliveryNode{Service: "orders", Verification: &VerificationSpec{URL: server.URL, Mode: "http", ExpectedStatus: 200, TimeoutSeconds: 1}}
			_, err := verifyHTTP(context.Background(), server.Client(), verificationFixture(), n)
			if (err == nil) != (kind == "valid") {
				t.Fatal(kind, err)
			}
		})
	}
}
func TestOperationHTTPRejectsMetadataAndRedirect(t *testing.T) {
	for _, u := range []string{"http://user:secret@host/check", "file:///tmp/check", "http://host/check?token=secret", "http://host/check#x"} {
		if validateCheckURL(u) == nil {
			t.Fatal("unsafe URL accepted", u)
		}
	}
	client := operationHTTPClient(time.Second)
	if _, _, err := readOperationHTTP(context.Background(), client, "http://127.0.0.1:1/check", nil); err == nil {
		t.Fatal("loopback permitted")
	}
	if client.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
		t.Fatal("redirect allowed")
	}
}
func TestBatchMetricFailsClosed(t *testing.T) {
	now := time.Now().Unix()
	rate, p95 := 0.001, 20.0
	o := &ReleaseOrder{ID: 1, Service: "orders", Environment: "test", observationArtifactDigest: "sha256:a"}
	p := MonitorGate{WarmupSeconds: 5, WindowSeconds: 10, MinRequests: 100, MaxErrorRate: .01, MaxP95Millis: 100}
	valid := BatchMetric{Service: "orders", Environment: "test", ReleaseID: 1, ArtifactDigest: "sha256:a", Batch: "pipeline-1", WindowStart: now - 15, WindowEnd: now, Requests: 120, ErrorRate: &rate, P95Millis: &p95}
	for _, kind := range []string{"valid", "no-traffic", "stale", "short-window", "wrong-batch", "wrong-revision", "missing-error-rate", "high-error", "high-latency", "future"} {
		t.Run(kind, func(t *testing.T) {
			m := valid
			switch kind {
			case "no-traffic":
				m.Requests = 0
			case "stale":
				m.WindowEnd = now - 30
			case "short-window":
				m.WindowStart = now - 1
			case "wrong-batch":
				m.Batch = "other"
			case "wrong-revision":
				m.ArtifactDigest = "sha256:b"
			case "missing-error-rate":
				m.ErrorRate = nil
			case "high-error":
				r := .5
				m.ErrorRate = &r
			case "high-latency":
				v := 1000.0
				m.P95Millis = &v
			case "future":
				m.WindowEnd = now + 20
			}
			err := evaluateBatchMetric(m, p, o, "pipeline-1", now-20, now)
			if (err == nil) != (kind == "valid") {
				t.Fatal(kind, err)
			}
		})
	}
}
func TestMonitoringContinuesOnceAndNeverReplaysUnknownResult(t *testing.T) {
	for _, scenario := range []string{"healthy", "no-data", "uncertain"} {
		t.Run(scenario, func(t *testing.T) {
			s, m, req := deliveryFixture(t)
			a := s.GetArtifact(req.Artifacts["orders"])
			o := &ReleaseOrder{Service: "orders", Environment: "test", ArtifactID: a.ID, Status: "waiting_next_batch", Hosts: []string{"test-1", "test-2"}, NextIndex: 1, Target: *s.GetServiceEnvironment("orders", "test")}
			if err := s.CreateReleaseOrder(o); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				if q.Get("batch") != "hosts-1" || q.Get("artifact_digest") != a.Digest {
					t.Error("unscoped metrics")
				}
				now := time.Now().Unix()
				requests := 200
				if scenario == "no-data" {
					requests = 0
				}
				json.NewEncoder(w).Encode(map[string]any{"service": "orders", "environment": "test", "artifact_digest": a.Digest, "release_id": o.ID, "batch": "hosts-1", "window_start": now - 10, "window_end": now, "requests": requests, "error_rate": 0.001, "p95_ms": 20})
			}))
			defer server.Close()
			n := DeliveryNode{ID: "deploy", Kind: "deploy", Service: "orders", Monitor: &MonitorGate{URL: server.URL, WindowSeconds: 10, MinRequests: 100, MaxErrorRate: .01, MaxP95Millis: 100, Consecutive: 1, TimeoutSeconds: 600}}
			run := &DeliveryRun{ID: 42, Owner: "alice", Digest: "frozen", Plan: DeliveryPlan{Environment: *s.GetEnvironment("test"), Nodes: []DeliveryNode{n}, Services: []DeliveryService{{Service: "orders", Artifact: *a}}}}
			step := DeliveryStep{NodeID: "deploy", Kind: "deploy", ReleaseID: o.ID}
			continues := 0
			next := func(_ *ReleaseOrder, batch string) error {
				continues++
				if batch != "hosts-1" {
					t.Fatal("wrong batch")
				}
				if scenario == "uncertain" {
					return fmt.Errorf("fixture transport lost after accept")
				}
				return nil
			}
			if decision, err := s.observeDeliveryBatchWithClient(run, step, m, server.Client(), next); err != nil || decision != "wait" || continues != 0 {
				t.Fatal("skipped warmup", err, decision)
			}
			var key, raw string
			var rev int64
			if err := s.db.QueryRow(`SELECT observation_key,document,revision FROM delivery_observations WHERE run_id=42`).Scan(&key, &raw, &rev); err != nil {
				t.Fatal(err)
			}
			var saved BatchObservation
			json.Unmarshal([]byte(raw), &saved)
			saved.Revision = rev
			saved.StartedAt = time.Now().Unix() - 20
			if err := s.saveBatchObservation(key, &saved); err != nil {
				t.Fatal(err)
			}
			decision, err := s.observeDeliveryBatchWithClient(run, step, m, server.Client(), next)
			if scenario == "healthy" {
				if err != nil || decision != "wait" || continues != 1 {
					t.Fatal("did not release healthy batch", decision, err, continues)
				}
			} else if err == nil {
				t.Fatal("failed metrics or uncertain continue passed", scenario)
			}
			_, _ = s.observeDeliveryBatchWithClient(run, step, m, server.Client(), next)
			if continues > 1 || scenario == "no-data" && continues != 0 {
				t.Fatal("unsafe auto retry", scenario, continues)
			}
		})
	}
}
func TestScriptArgumentsNotShellSyntax(t *testing.T) {
	value := "a' ; $(touch bad)\nnext"
	cmd := exec.Command("sh", "-c", "printf '%s' "+shellArgument(value))
	out, err := cmd.Output()
	if err != nil || string(out) != value {
		t.Fatal(string(out), err)
	}
}
func TestScriptSSHUsesStdinAndDeadline(t *testing.T) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, e := listener.Accept()
		if e != nil {
			return
		}
		defer conn.Close()
		cfg := &ssh.ServerConfig{PasswordCallback: func(_ ssh.ConnMetadata, _ []byte) (*ssh.Permissions, error) { return nil, nil }}
		cfg.AddHostKey(signer)
		server, chans, requests, e := ssh.NewServerConn(conn, cfg)
		if e != nil {
			return
		}
		defer server.Close()
		go ssh.DiscardRequests(requests)
		for ch := range chans {
			channel, reqs, e := ch.Accept()
			if e != nil {
				return
			}
			for req := range reqs {
				if req.Type != "exec" {
					req.Reply(false, nil)
					continue
				}
				var command struct{ Command string }
				ssh.Unmarshal(req.Payload, &command)
				req.Reply(true, nil)
				data, _ := io.ReadAll(channel)
				io.WriteString(channel, command.Command+"\n"+string(data))
				channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
				channel.Close()
				return
			}
		}
	}()
	h := Host{Addr: listener.Addr().String(), User: "worker", Password: "fixture-only", HostKey: ssh.FingerprintSHA256(signer.PublicKey())}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	text, err := executeDeliverySSH(ctx, h, DeliveryScript{Content: "echo fixture\n", Arguments: []string{"x; unsafe"}, TimeoutSeconds: 3}, verificationFixture())
	if err != nil || !strings.Contains(text, "'x; unsafe'") || !strings.HasSuffix(text, "echo fixture\n") || !strings.Contains(text, "env -i") {
		t.Fatal("bad SSH contract", text, err)
	}
	<-serverDone
	slow, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Close()
	go func() {
		conn, e := slow.Accept()
		if e == nil {
			defer conn.Close()
			var b [1]byte
			for {
				if _, e = conn.Read(b[:]); e != nil {
					return
				}
			}
		}
	}()
	h.Addr = slow.Addr().String()
	short, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer stop()
	started := time.Now()
	if _, err = executeDeliverySSH(short, h, DeliveryScript{TimeoutSeconds: 1}, verificationFixture()); err == nil || time.Since(started) > time.Second {
		t.Fatal("SSH handshake ignored deadline", err)
	}
}
func TestOperationValidationAndTemplateScope(t *testing.T) {
	n := DeliveryNode{ID: "s", Name: "script", Kind: "script", Script: &DeliveryScript{Host: "worker", Content: "echo hello", TimeoutSeconds: 30}}
	if err := validateOperationNode(n); err != nil {
		t.Fatal(err)
	}
	if err := validateDeliveryNodes([]DeliveryNode{n}); err != nil {
		t.Fatal(err)
	}
	if operationSignature([]DeliveryNode{n}) == operationSignature(nil) {
		t.Fatal("operation missing from signature")
	}
	copy := n
	spec := *n.Script
	copy.Script = &spec
	copy.Script.Content = "echo changed"
	if operationSignature([]DeliveryNode{n}) == operationSignature([]DeliveryNode{copy}) {
		t.Fatal("script change not detected")
	}
	n.Script.TimeoutSeconds = 0
	if err := validateOperationNode(n); err == nil {
		t.Fatal("unbounded script accepted")
	}
	if validateResearchNodes([]DeliveryNode{{ID: "v", Name: "test", Kind: "verify", Service: "orders", Verification: &VerificationSpec{Mode: "http", URL: "https://example.invalid/check", ExpectedStatus: 200, TimeoutSeconds: 10}}}) == nil {
		t.Fatal("pre-build verification accepted")
	}
}
func TestLegacyStandalonePreparationRejected(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.SaveDeliveryTemplate(&DeliveryTemplate{Name: "old", Mode: "version", Workbench: "research", Nodes: []DeliveryNode{{ID: "m", Kind: "manual", Name: "old", Instructions: "old"}}}); err == nil {
		t.Fatal("legacy template accepted")
	}
	mux := http.NewServeMux()
	registerResearchRoutes(mux, s, nil, nil)
	for _, method := range []string{"GET", "POST"} {
		r := httptest.NewRequest(method, "/api/research/flows", strings.NewReader("{}"))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 404 {
			t.Fatal("old endpoint active", method, w.Code)
		}
	}
	if len(s.DeliveryTemplates()) != 0 {
		t.Fatal("legacy template persisted")
	}
}
func TestScriptTargetAndManualVerification(t *testing.T) {
	s, m, req, _, _ := standardManualFixture(t)
	run, err := s.CreateDeliveryRun(standardSign(t, s, m, req), "alice", m)
	if err != nil {
		t.Fatal(err)
	}
	n := DeliveryNode{ID: "verify", Kind: "verify", Name: "test", Service: "orders", Approvers: []string{"qa1", "qa2"}, ApprovalMode: "all", Verification: &VerificationSpec{Mode: "manual"}}
	run.Plan.Nodes = []DeliveryNode{n}
	run.Plan.Services = []DeliveryService{{Service: "orders", Artifact: Artifact{ID: 1, Digest: "sha256:a"}}}
	run.Digest = digestJSON(run.Plan)
	run.Steps = []DeliveryStep{{NodeID: n.ID, Kind: n.Kind, Status: "pending"}}
	plan, _ := json.Marshal(run.Plan)
	steps, _ := json.Marshal(run.Steps)
	s.db.Exec(`UPDATE delivery_runs SET plan=?,digest=?,execution=? WHERE id=?`, string(plan), run.Digest, string(steps), run.ID)
	if _, err = s.reviewVerification(run.ID, n.ID, run.Digest, "alice", "checked", true); err == nil {
		t.Fatal("unassigned verifier accepted")
	}
	if _, err = s.reviewVerification(run.ID, n.ID, run.Digest, "qa1", "", true); err == nil {
		t.Fatal("empty evidence accepted")
	}
	run, err = s.reviewVerification(run.ID, n.ID, run.Digest, "qa1", "test run 1", true)
	if err != nil || run.Steps[0].Status == "success" {
		t.Fatal("all-sign passed early", err)
	}
	if _, err = s.reviewVerification(run.ID, n.ID, run.Digest, "qa1", "again", true); err == nil {
		t.Fatal("duplicate confirmation accepted")
	}
	run, err = s.reviewVerification(run.ID, n.ID, run.Digest, "qa2", "test run 2", true)
	if err != nil || run.Steps[0].Status != "success" || run.Steps[0].Evidence.Kind != "manual-verification" || run.Steps[0].Evidence.Artifacts["orders"] != "sha256:a" {
		t.Fatal("missing artifact proof", err, run)
	}
	tasks, err := s.ResearchTasks("qa1", "done")
	if err != nil || len(tasks) != 1 {
		t.Fatal("verification missing from personal history", err, tasks)
	}
}
func TestScriptHostReservationKeepsUncertainWritesLocked(t *testing.T) {
	s, m, req, _, _ := standardManualFixture(t)
	first, err := s.CreateDeliveryRun(standardSign(t, s, m, req), "alice", m)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateDeliveryRun(standardSign(t, s, m, req), "alice", m)
	if err != nil {
		t.Fatal(err)
	}
	n := DeliveryNode{ID: "script", Kind: "script", Script: &DeliveryScript{Host: "test-worker"}}
	for _, run := range []*DeliveryRun{first, second} {
		run.Status = "running"
		run.Steps = []DeliveryStep{{NodeID: "script", Kind: "script", Status: "executing"}}
	}
	if err = s.claimScriptOperation(first, n); err != nil {
		t.Fatal(err)
	}
	if err = s.claimScriptOperation(second, n); err == nil {
		t.Fatal("concurrent script accepted")
	}
	first.Steps[0].Status = "uncertain"
	first.Status = "failed"
	if err = s.saveDeliveryRun(first); err != nil {
		t.Fatal(err)
	}
	if err = s.claimScriptOperation(second, n); err == nil {
		t.Fatal("uncertain lock expired")
	}
	first.Steps[0].Status = "success"
	first.Steps[0].Evidence = &NodeEvidence{Kind: "manual-script-reconciliation"}
	if err = s.saveDeliveryRun(first); err != nil {
		t.Fatal(err)
	}
	if err = s.claimScriptOperation(second, n); err != nil {
		t.Fatal("completed reservation not recovered", err)
	}
}
func TestOperationTargetSnapshotAndApprovalCannotBeReplaced(t *testing.T) {
	s, _, _, _, _ := standardManualFixture(t)
	h := Host{Name: "worker", Environment: "test", Addr: "192.0.2.10:22", User: "worker", Password: "fixture-secret"}
	if err := s.UpsertHost(h); err != nil {
		t.Fatal(err)
	}
	n := DeliveryNode{ID: "script", Kind: "script", Name: "Prepare", Script: &DeliveryScript{Host: h.Name, Content: "echo fixture", TimeoutSeconds: 30}}
	tpl := &DeliveryTemplate{Nodes: []DeliveryNode{n}}
	p := &DeliveryPlan{Environment: *s.GetEnvironment("test"), Nodes: []DeliveryNode{n}}
	if err := s.freezeOperations(p, tpl); err != nil {
		t.Fatal(err)
	}
	if p.Nodes[0].Script.Target != "worker@192.0.2.10:22" || p.Nodes[0].Script.TargetDigest == "" || strings.Contains(p.Nodes[0].Script.TargetDigest, h.Password) {
		t.Fatal("missing safe destination snapshot")
	}
	h.Password = "rotated"
	if err := s.UpsertHost(h); err != nil {
		t.Fatal(err)
	}
	if s.scriptTargetDigest(s.GetHost(h.Name)) == p.Nodes[0].Script.TargetDigest {
		t.Fatal("credential rotation not detected")
	}
	copy := *p.Nodes[0].Script
	copy.Content = "echo changed"
	p.Nodes[0].Script = &copy
	if err := s.freezeOperations(p, tpl); err == nil {
		t.Fatal("single-run script replacement accepted")
	}
	h.User = "root"
	if err := s.UpsertHost(h); err != nil {
		t.Fatal(err)
	}
	p.Nodes = []DeliveryNode{n}
	if err := s.freezeOperations(p, tpl); err == nil {
		t.Fatal("root script execution accepted")
	}
}
func TestPreparationVerificationPinsBuiltArtifact(t *testing.T) {
	s, _, req := deliveryFixture(t)
	a := s.GetArtifact(req.Artifacts["orders"])
	p := &CodePreparation{ID: "verify-fixture", State: "ready", Owner: "alice", Builds: []PreparedBuild{{Service: "orders", ArtifactID: a.ID, ArtifactDigest: a.Digest}}}
	if err := s.saveCodePreparation(p); err != nil {
		t.Fatal(err)
	}
	n := DeliveryNode{ID: "test", Kind: "verify", Service: "orders", Verification: &VerificationSpec{Mode: "manual"}}
	run := &DeliveryRun{Digest: "frozen", Steps: []DeliveryStep{{NodeID: "code", Kind: "code-prepare", Status: "success", Detail: p.ID}, {NodeID: n.ID, Kind: n.Kind, Status: "pending"}}}
	proof, err := s.preparationVerification(run, n)
	if err != nil || len(proof.Plan.Services) != 1 || proof.Plan.Services[0].Artifact.Digest != a.Digest {
		t.Fatal("built artifact not resolved", err)
	}
	if len(run.Plan.Services) != 0 {
		t.Fatal("verification modified immutable plan")
	}
	if _, err = s.db.Exec(`UPDATE artifacts SET digest='sha256:changed' WHERE id=?`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.preparationVerification(run, n); err == nil {
		t.Fatal("changed artifact remained validated")
	}
}
