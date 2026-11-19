package deploy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const successfulScriptFixture = `set -Eeuo pipefail
test -f "$LP_ARTIFACT_PATH"
test "${DEVOPSPLATFORM_AUTH+x}" != x
test "$DELIVERY_ENVIRONMENT" = test
test "$1" = 'literal; $(touch must-not-execute)'
printf '%s\n' '{"status":"success","external_id":"fixture-job","values":{"savepoint":"fixture://state"}}' > "$LP_RESULT_FILE"
`

func scriptFixture(t *testing.T, script string) (*Store, *ReleaseManager, *Artifact) {
	t.Helper()
	s, _, _ := deliveryFixture(t)
	if err := s.UpsertRunner(BuildRunner{Name: "local", Kind: "local", Enabled: true, MaxParallel: 4}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertServiceEnvironment(ServiceEnvironment{Service: "orders", Environment: "test", TargetType: TargetScript, DeployConfig: map[string]string{"script": script, "script_runner": "local", "script_arguments": `["literal; $(touch must-not-execute)"]`, "script_timeout_seconds": "5"}}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "fixture.jar")
	if err := os.WriteFile(file, []byte("synthetic immutable artifact\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := describeArtifact("orders", "fixture-version", ArtifactJar, file, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RegisterArtifact(&a); err != nil {
		t.Fatal(err)
	}
	hub := NewLogHub()
	dp := NewDeployer(s, hub, "")
	s.pipelineRunner = NewPipelineRunner(s, NewBuilder(s, t.TempDir(), hub), hub, dp, NewStepLib("../../steps"))
	return s, NewReleaseManager(s, dp), &a
}

func waitScriptOrder(t *testing.T, s *Store, id int64) *ReleaseOrder {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		o := s.GetReleaseOrder(id)
		if o.Status != "queued" && o.Status != "running" {
			return o
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("script publication did not finish")
	return nil
}

func TestScriptPublicationFrozenArtifactArgumentsAndEvidence(t *testing.T) {
	s, m, a := scriptFixture(t, successfulScriptFixture)
	t.Setenv("DEVOPSPLATFORM_AUTH", "must-not-inherit")
	o, err := m.CreateArtifact("orders", a.ID, "test", "isolated fixture", "alice")
	if err != nil {
		t.Fatal(err)
	}
	o = waitScriptOrder(t, s, o.ID)
	if o.Status != "success" || o.Total != 1 || o.Succeeded != 1 || len(o.DeployIDs) != 1 || o.Script == nil || o.Script.Result == nil || o.Script.Result.ExternalID != "fixture-job" || o.Script.NeedsReconciliation {
		t.Fatalf("%+v", o)
	}
	if s.GetDeploy(o.DeployIDs[0]).ArtifactDigest != a.Digest {
		t.Fatal("artifact identity lost")
	}
	if _, err = os.Stat(s.scriptWorkspace(o.DeployIDs[0])); !os.IsNotExist(err) {
		t.Fatal("successful workspace retained", err)
	}
	if m.Rollback(o.ID, "fixture rollback", "alice") == nil {
		t.Fatal("generic script auto-rollback accepted")
	}
	if s.scriptScopeBusy("orders", "test") != nil {
		t.Fatal("successful scope not unlocked")
	}
}

func TestScriptPublicationUncertainNoReplayAndManualReconciliation(t *testing.T) {
	for _, mode := range []string{"nonzero", "missing-result", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "executions")
			script := "printf x >> " + shellQuote(marker) + "\n"
			switch mode {
			case "nonzero":
				script += "exit 3\n"
			case "missing-result":
				script += "exit 0\n"
			case "timeout":
				script += "sleep 20\n"
			}
			s, m, a := scriptFixture(t, script)
			if mode == "timeout" {
				v := *s.GetServiceEnvironment("orders", "test")
				v.DeployConfig["script_timeout_seconds"] = "1"
				if err := s.UpsertServiceEnvironment(v); err != nil {
					t.Fatal(err)
				}
			}
			o, err := m.CreateArtifact("orders", a.ID, "test", "fixture", "alice")
			if err != nil {
				t.Fatal(err)
			}
			o = waitScriptOrder(t, s, o.ID)
			if o.Status != "failed" || o.Script == nil || !o.Script.NeedsReconciliation {
				t.Fatalf("%+v", o)
			}
			if _, err = m.CreateArtifact("orders", a.ID, "test", "must not replay", "alice"); err == nil {
				t.Fatal("uncertain publication replayed")
			}
			// Changing the target's concurrency key cannot bypass the fixed scope lock.
			v := *s.GetServiceEnvironment("orders", "test")
			v.DeployConfig["script_concurrency_key"] = "another-key"
			if err = s.UpsertServiceEnvironment(v); err != nil {
				t.Fatal(err)
			}
			if _, err = m.CreateArtifact("orders", a.ID, "test", "must not bypass", "alice"); err == nil {
				t.Fatal("scope lock bypassed")
			}
			if _, err = m.dp.StartArtifactRelease(v.Apply(s.GetService("orders")), v.ExecutionHosts()[0], *a, "test", "bare call", "alice"); err == nil {
				t.Fatal("lower-level replay accepted")
			}
			req := scriptReconciliation{DeployID: o.Script.DeployID, ArtifactID: a.ID, Digest: a.Digest, ConfigDigest: o.Script.ConfigDigest, Result: "failed", Comment: "Synthetic executor ended; external fixture state checked", ConfirmStopped: true}
			wrong := req
			wrong.Digest = "wrong"
			if m.reconcileScript(o.ID, wrong, "alice", false) == nil {
				t.Fatal("wrong identity reconciled")
			}
			if m.reconcileScript(o.ID, req, "other", false) == nil {
				t.Fatal("foreign owner reconciled")
			}
			if err = m.reconcileScript(o.ID, req, "alice", false); err != nil {
				t.Fatal(err)
			}
			if m.reconcileScript(o.ID, req, "alice", false) == nil {
				t.Fatal("duplicate reconciliation accepted")
			}
			got := s.GetReleaseOrder(o.ID)
			if got.Script.Result.ReconciledBy != "alice" || got.Script.NeedsReconciliation {
				t.Fatal(got.Script)
			}
			b, _ := os.ReadFile(marker)
			if string(b) != "x" {
				t.Fatal("script side effect repeated", string(b))
			}
		})
	}
}

func TestScriptPublicationApprovalSnapshotAndRunnerDrift(t *testing.T) {
	for _, drift := range []string{"script", "runner", "artifact"} {
		t.Run(drift, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "approval")
			s, m, a := scriptFixture(t, "printf original > "+shellQuote(marker)+"\nprintf '%s' '{\"status\":\"success\"}' > \"$LP_RESULT_FILE\"\n")
			v := *s.GetServiceEnvironment("orders", "test")
			v.Environment = "prod"
			if err := s.UpsertServiceEnvironment(v); err != nil {
				t.Fatal(err)
			}
			o, err := m.CreateArtifact("orders", a.ID, "prod", "fixture", "alice")
			if err != nil || o.Status != "pending_approval" {
				t.Fatal(o, err)
			}
			if _, err = os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("script ran before approval")
			}
			switch drift {
			case "script":
				v.DeployConfig["script"] = "exit 13"
				err = s.UpsertServiceEnvironment(v)
			case "runner":
				err = s.UpsertRunner(BuildRunner{Name: "local", Kind: "local", Enabled: false})
			case "artifact":
				err = os.WriteFile(a.URI, []byte("tampered"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if m.Approve(o.ID, "alice") == nil {
				t.Fatal("self approval bypassed")
			}
			if err = m.Approve(o.ID, "reviewer"); err != nil {
				t.Fatal(err)
			}
			o = waitScriptOrder(t, s, o.ID)
			if drift == "script" {
				b, _ := os.ReadFile(marker)
				if o.Status != "success" || string(b) != "original" {
					t.Fatal("frozen script changed", o.Status, string(b))
				}
			} else {
				if o.Status != "failed" {
					t.Fatal(o.Status)
				}
				if _, err = os.Stat(marker); !os.IsNotExist(err) {
					t.Fatal("drift allowed side effects")
				}
			}
		})
	}
}

func TestScriptPublicationConcurrentClaimsAndRestartProtection(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "once")
	s, m, a := scriptFixture(t, "printf x >> "+shellQuote(marker)+"\nsleep 0.5\nexit 1\n")
	var wg sync.WaitGroup
	results := make(chan *ReleaseOrder, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if o, e := m.CreateArtifact("orders", a.ID, "test", "fixture", "alice"); e == nil {
				results <- o
			}
		}()
	}
	wg.Wait()
	close(results)
	var only *ReleaseOrder
	count := 0
	for o := range results {
		only = o
		count++
	}
	if count != 1 {
		t.Fatal("duplicate publication", count)
	}
	waitScriptOrder(t, s, only.ID)
	reopened, err := Open(s.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recovery := NewReleaseManager(reopened, NewDeployer(reopened, NewLogHub(), ""))
	recovery.Recover()
	if _, err = recovery.ValidateTarget("orders", "test", "fixture"); err == nil {
		t.Fatal("restart lost durable claim")
	}
	b, _ := os.ReadFile(marker)
	if string(b) != "x" {
		t.Fatal("restart replayed", string(b))
	}
}

func TestScriptPublicationResultRedactionAndValidation(t *testing.T) {
	s, _, _ := scriptFixture(t, "exit 0")
	secret := "quoted\"secret\nvalue"
	result := &ScriptPublicationResult{Status: "success", ExternalID: secret, Values: map[string]string{"note": secret}}
	if err := s.saveScriptResult(91, result, map[string]string{"API_KEY": secret}); err != nil {
		t.Fatal(err)
	}
	got := s.scriptResult(91)
	if got == nil || got.ExternalID != "[REDACTED]" || got.Values["note"] != "[REDACTED]" {
		t.Fatal(got)
	}
	for _, raw := range []string{`{"status":"success","values":{"password":"bad"}}`, `{"status":"unknown"}`, `{"status":"success","external_id":"bad\nvalue"}`} {
		file := filepath.Join(t.TempDir(), "result.json")
		os.WriteFile(file, []byte(raw), 0600)
		if _, err := readScriptResult(file); err == nil {
			t.Fatal("invalid result accepted", raw)
		}
	}
}

func TestScriptPublicationSharedResourceAndRunnerCapacity(t *testing.T) {
	s, m, a := scriptFixture(t, "exit 3\n")
	v := *s.GetServiceEnvironment("orders", "test")
	v.DeployConfig["script_concurrency_key"] = "same-external-task"
	if err := s.UpsertServiceEnvironment(v); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertRunner(BuildRunner{Name: "other-runner", Kind: "local", Enabled: true, MaxParallel: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertService(Service{Name: "other", ArtifactType: "jar", BuildTool: "command", BuildCmd: "true"}); err != nil {
		t.Fatal(err)
	}
	other := v
	other.Service = "other"
	other.DeployConfig = map[string]string{"script": "exit 0", "script_runner": "other-runner", "script_timeout_seconds": "5", "script_concurrency_key": "same-external-task"}
	if err := s.UpsertServiceEnvironment(other); err != nil {
		t.Fatal(err)
	}
	otherArtifact := *a
	otherArtifact.ID = 0
	otherArtifact.Service = "other"
	if err := s.RegisterArtifact(&otherArtifact); err != nil {
		t.Fatal(err)
	}
	o, err := m.CreateArtifact("orders", a.ID, "test", "fixture", "alice")
	if err != nil {
		t.Fatal(err)
	}
	waitScriptOrder(t, s, o.ID)
	if _, err = m.CreateArtifact("other", otherArtifact.ID, "test", "same external task", "alice"); err == nil {
		t.Fatal("another runner bypassed external resource lock")
	}
	// Config validation and scope remain separate from Runner availability.
	other.DeployConfig["script_concurrency_key"] = "independent-task"
	if err = s.UpsertServiceEnvironment(other); err != nil {
		t.Fatal(err)
	}
	release, ok := s.pipelineRunner.claimRunner("other-runner", 1)
	if !ok {
		t.Fatal("fixture runner claim")
	}
	second, err := m.CreateArtifact("other", otherArtifact.ID, "test", "saturated runner", "alice")
	if err != nil {
		t.Fatal(err)
	}
	second = waitScriptOrder(t, s, second.ID)
	release()
	if second.Status != "failed" || s.scriptScopeBusy("other", "test") != nil {
		t.Fatal("no-side-effect capacity failure held execution lock")
	}
}

func TestScriptPublicationFlinkExamplesUseOnlyIsolatedREST(t *testing.T) {
	for _, binary := range []string{"curl", "jq", "sha256sum"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Fatalf("required isolated fixture tool unavailable: %s", binary)
		}
	}
	script, err := os.ReadFile("../../deploy/examples/flink-rest-publication.sh")
	if err != nil {
		t.Fatal(err)
	}
	jobs := map[string][2]string{"behavior": {"zfb-house-view-requirement-pipeline", "HouseViewBehaviorJob"}, "user-demand-cdc": {"zfb-user-demand-config-cdc", "UserDemandConfigCdcJob"}, "house-favorite-cdc": {"zfb-house-favorite-cdc", "HouseFavoriteCdcJob"}, "house-compare-cdc": {"zfb-house-compare-cdc", "HouseCompareCdcJob"}, "house-offline-view": {"zfb-house-offline-view-cdc", "HouseOfflineViewCdcJob"}}
	for key, identity := range jobs {
		t.Run(key, func(t *testing.T) {
			var mu sync.Mutex
			uploads, stops, starts := 0, 0, 0
			newID := ""
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.URL.Path == "/config":
					io.WriteString(w, `{"flink-version":"1.20.4"}`)
				case r.URL.Path == "/jobs/overview":
					fmt.Fprintf(w, `{"jobs":[{"name":%q,"jid":"old-job","state":"RUNNING"}]}`, identity[0])
				case r.URL.Path == "/jars/upload":
					if r.Method != "POST" {
						t.Error("wrong upload method")
					}
					f, _, err := r.FormFile("jarfile")
					if err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					b, _ := io.ReadAll(f)
					f.Close()
					if string(b) != "synthetic immutable artifact\n" {
						t.Error("wrong artifact uploaded")
					}
					uploads++
					io.WriteString(w, `{"filename":"/fixture/new.jar"}`)
				case r.URL.Path == "/jobs/old-job/stop":
					stops++
					io.WriteString(w, `{"request-id":"save-request"}`)
				case r.URL.Path == "/jobs/old-job/savepoints/save-request":
					io.WriteString(w, `{"status":{"id":"COMPLETED"},"operation":{"location":"fixture://savepoint"}}`)
				case r.URL.Path == "/jars/new.jar/run":
					var body map[string]string
					json.NewDecoder(r.Body).Decode(&body)
					if !strings.HasSuffix(body["entryClass"], identity[1]) || body["savepointPath"] != "fixture://savepoint" || len(body["jobId"]) != 32 {
						t.Error("wrong restored job", body)
					}
					starts++
					newID = body["jobId"]
					fmt.Fprintf(w, `{"jobid":%q}`, newID)
				case newID != "" && r.URL.Path == "/jobs/"+newID:
					io.WriteString(w, `{"state":"RUNNING","vertices":[{"status":"RUNNING"}]}`)
				default:
					t.Errorf("unexpected fixture request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			s, m, a := scriptFixture(t, string(script))
			v := *s.GetServiceEnvironment("orders", "test")
			args, _ := json.Marshal([]string{key})
			v.DeployConfig["script_arguments"] = string(args)
			v.RuntimeEnv = map[string]string{"FLINK_REST_URL": server.URL, "FLINK_SAVEPOINT_DIR": "fixture://savepoints"}
			if err := s.UpsertServiceEnvironment(v); err != nil {
				t.Fatal(err)
			}
			o, err := m.CreateArtifact("orders", a.ID, "test", "isolated REST fixture only", "alice")
			if err != nil {
				t.Fatal(err)
			}
			o = waitScriptOrder(t, s, o.ID)
			if o.Status != "success" {
				var log string
				s.db.QueryRow(`SELECT log FROM deploys WHERE id=?`, o.DeployIDs[0]).Scan(&log)
				t.Fatal(o.Status, log)
			}
			mu.Lock()
			defer mu.Unlock()
			if uploads != 1 || stops != 1 || starts != 1 || o.Script.Result.ExternalID != newID || o.Script.Result.Values["savepoint"] != "fixture://savepoint" {
				t.Fatal("publication evidence mismatch", uploads, stops, starts, o.Script)
			}
		})
	}
}

func TestScriptPublicationFlinkFailureBoundaries(t *testing.T) {
	script, err := os.ReadFile("../../deploy/examples/flink-rest-publication.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"wrong-version", "duplicate-job", "savepoint-failed", "submission-response-lost"} {
		t.Run(mode, func(t *testing.T) {
			var mu sync.Mutex
			uploads, stops, starts := 0, 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/config":
					version := "1.20.4"
					if mode == "wrong-version" {
						version = "2.2.1"
					}
					fmt.Fprintf(w, `{"flink-version":%q}`, version)
				case "/jobs/overview":
					jobs := []map[string]string{{"name": "zfb-house-view-requirement-pipeline", "jid": "old-job", "state": "RUNNING"}}
					if mode == "duplicate-job" {
						jobs = append(jobs, map[string]string{"name": jobs[0]["name"], "jid": "duplicate", "state": "RUNNING"})
					}
					json.NewEncoder(w).Encode(map[string]any{"jobs": jobs})
				case "/jars/upload":
					uploads++
					io.WriteString(w, `{"filename":"/fixture/new.jar"}`)
				case "/jobs/old-job/stop":
					stops++
					io.WriteString(w, `{"request-id":"save-request"}`)
				case "/jobs/old-job/savepoints/save-request":
					if mode == "savepoint-failed" {
						io.WriteString(w, `{"status":{"id":"COMPLETED"},"operation":{"failure-cause":{"class":"FixtureFailure"}}}`)
					} else {
						io.WriteString(w, `{"status":{"id":"COMPLETED"},"operation":{"location":"fixture://savepoint"}}`)
					}
				case "/jars/new.jar/run":
					starts++
					w.WriteHeader(500)
					io.WriteString(w, `{"error":"fixture accepted job, response lost"}`)
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			s, m, a := scriptFixture(t, string(script))
			v := *s.GetServiceEnvironment("orders", "test")
			v.DeployConfig["script_arguments"] = `["behavior"]`
			v.RuntimeEnv = map[string]string{"FLINK_REST_URL": server.URL, "FLINK_SAVEPOINT_DIR": "fixture://savepoints"}
			if err := s.UpsertServiceEnvironment(v); err != nil {
				t.Fatal(err)
			}
			o, err := m.CreateArtifact("orders", a.ID, "test", "isolated failure fixture", "alice")
			if err != nil {
				t.Fatal(err)
			}
			o = waitScriptOrder(t, s, o.ID)
			if o.Status != "failed" || o.Script == nil || !o.Script.NeedsReconciliation {
				t.Fatal("unknown result lost protection", o)
			}
			if _, err = m.CreateArtifact("orders", a.ID, "test", "no blind retry", "alice"); err == nil {
				t.Fatal("uncertain script replayed")
			}
			mu.Lock()
			defer mu.Unlock()
			switch mode {
			case "wrong-version", "duplicate-job":
				if uploads+stops+starts != 0 {
					t.Fatal("preflight did not prevent side effects")
				}
			case "savepoint-failed":
				if uploads != 1 || stops != 1 || starts != 0 {
					t.Fatal(uploads, stops, starts)
				}
			case "submission-response-lost":
				if starts != 1 || o.Script.Result == nil || len(o.Script.Result.ExternalID) != 32 || o.Script.Result.Values["phase"] != "submitting" || o.Script.Result.Values["savepoint"] != "fixture://savepoint" {
					t.Fatal("lost response not reconcilable", starts, o.Script)
				}
			}
		})
	}
}
