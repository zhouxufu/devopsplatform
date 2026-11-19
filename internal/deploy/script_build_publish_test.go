package deploy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The public build-and-publish API must carry a newly built artifact through the
// normal release manager to the generic script driver. No business repository,
// Maven invocation or external Flink endpoint is used by this fixture.
func TestScriptBuildAndPublishFlinkJobs(t *testing.T) {
	scriptPath := os.Getenv("FLINK_SCRIPT_CANDIDATE")
	if scriptPath == "" {
		scriptPath = "../../deploy/examples/flink-rest-publication.sh"
	}
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	jobs := map[string]string{
		"behavior":           "zfb-house-view-requirement-pipeline",
		"user-demand-cdc":    "zfb-user-demand-config-cdc",
		"house-favorite-cdc": "zfb-house-favorite-cdc",
		"house-compare-cdc":  "zfb-house-compare-cdc",
		"house-offline-view": "zfb-house-offline-view-cdc",
	}
	for key, jobName := range jobs {
		t.Run(key, func(t *testing.T) {
			var mu sync.Mutex
			var calls []string
			newID := ""
			flink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				calls = append(calls, r.Method+" "+r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				switch r.Method + " " + r.URL.Path {
				case "GET /config":
					io.WriteString(w, `{"flink-version":"1.20.4"}`)
				case "GET /jobs/overview":
					fmt.Fprintf(w, `{"jobs":[{"jid":"old-job","name":%q,"state":"RUNNING"}]}`, jobName)
				case "POST /jars/upload":
					file, _, e := r.FormFile("jarfile")
					if e != nil {
						t.Error(e)
						w.WriteHeader(400)
						return
					}
					body, _ := io.ReadAll(file)
					file.Close()
					if string(body) != "new synthetic build artifact\n" {
						t.Errorf("published stale or wrong build: %q", body)
					}
					io.WriteString(w, `{"filename":"/fixture/new-build.jar"}`)
				case "POST /jobs/old-job/stop":
					var body map[string]any
					json.NewDecoder(r.Body).Decode(&body)
					if body["targetDirectory"] != "fixture://savepoints/"+key || body["drain"] != false {
						t.Errorf("wrong savepoint target: %+v", body)
					}
					io.WriteString(w, `{"request-id":"isolated-stop"}`)
				case "GET /jobs/old-job/savepoints/isolated-stop":
					io.WriteString(w, `{"status":{"id":"COMPLETED"},"operation":{"location":"fixture://saved-state"}}`)
				case "POST /jars/new-build.jar/run":
					var body map[string]string
					json.NewDecoder(r.Body).Decode(&body)
					newID = body["jobId"]
					if len(newID) != 32 || body["savepointPath"] != "fixture://saved-state" {
						t.Errorf("restore identity lost: %+v", body)
					}
					fmt.Fprintf(w, `{"jobid":%q}`, newID)
				default:
					if newID != "" && r.Method == "GET" && r.URL.Path == "/jobs/"+newID {
						io.WriteString(w, `{"state":"RUNNING","vertices":[{"status":"RUNNING"}]}`)
					} else {
						t.Errorf("unexpected isolated request: %s %s", r.Method, r.URL.Path)
						w.WriteHeader(404)
					}
				}
			}))
			defer flink.Close()
			s, _, _ := scriptFixture(t, string(script))
			if err := s.UpsertRunner(BuildRunner{Name: "local", Kind: "local", Enabled: true, MaxParallel: 1}); err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			workspace := filepath.Join(root, "source")
			lib := filepath.Join(root, "steps", "fixture-package")
			os.MkdirAll(workspace, 0700)
			os.MkdirAll(lib, 0700)
			os.WriteFile(filepath.Join(lib, "step.yaml"), []byte("id: fixture-package\nname: Synthetic build\nkind: package\nproduces: jar\n"), 0600)
			os.WriteFile(filepath.Join(lib, "run.sh"), []byte("set -eu\nprintf 'new synthetic build artifact\\n' > \"$LP_OUTPUT_DIR/new.jar\"\nprintf 'ARTIFACT=%s\\nARTIFACT_KIND=jar\\n' \"$LP_OUTPUT_DIR/new.jar\" > \"$LP_OUTPUTS_FILE\"\n"), 0700)
			name := "zfb-flink-" + key
			if err := s.UpsertService(Service{Name: name, ArtifactType: "jar", BuildTool: "command", BuildCmd: "true", Workdir: workspace, Steps: []StepInst{{Step: "fixture-package"}}}); err != nil {
				t.Fatal(err)
			}
			if err := s.UpsertServiceDelivery(ServiceDelivery{Service: name, Runner: "local"}); err != nil {
				t.Fatal(err)
			}
			target := *s.GetServiceEnvironment("orders", "test")
			target.Service = name
			args, _ := json.Marshal([]string{key})
			target.DeployConfig["script_arguments"] = string(args)
			target.DeployConfig["script_timeout_seconds"] = "15"
			target.RuntimeEnv = map[string]string{"FLINK_REST_URL": flink.URL, "FLINK_SAVEPOINT_DIR": "fixture://savepoints"}
			if tools := os.Getenv("FLINK_SCRIPT_TOOLS_DIR"); tools != "" {
				target.RuntimeEnv["FLINK_TOOLS_DIR"] = tools
			}
			if err := s.UpsertServiceEnvironment(target); err != nil {
				t.Fatal(err)
			}
			hub := NewLogHub()
			routes := Routes(s, NewBuilder(s, root, hub), hub, "", NewStepLib(filepath.Dir(lib)))
			start := func(publish bool) int64 {
				body := map[string]string{"service": name, "build_environment": "test"}
				if publish {
					body["deploy_environment"] = "test"
				}
				raw, _ := json.Marshal(body)
				req := httptest.NewRequest("POST", "/api/pipeline/runs", strings.NewReader(string(raw)))
				req.Header.Set("X-User", "fixture")
				w := httptest.NewRecorder()
				routes.ServeHTTP(w, req)
				if w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
				var result struct{ ID int64 }
				json.Unmarshal(w.Body.Bytes(), &result)
				return result.ID
			}
			waitBuild := func(id int64) *PipelineRun {
				deadline := time.Now().Add(10 * time.Second)
				for time.Now().Before(deadline) {
					v := s.GetRun(id)
					if v.Status != "running" {
						if v.Status != "success" {
							t.Fatal(v.Status, s.RunLog(id))
						}
						return v
					}
					time.Sleep(20 * time.Millisecond)
				}
				t.Fatal("isolated build did not finish")
				return nil
			}
			// An ordinary build must not invoke the script or create a release.
			waitBuild(start(false))
			mu.Lock()
			if len(calls) != 0 {
				t.Error("build-only touched a target", calls)
			}
			mu.Unlock()
			built := waitBuild(start(true))
			var order *ReleaseOrder
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				orders := s.ReleaseOrders(name, 10)
				if len(orders) > 0 {
					if len(orders) != 1 {
						t.Fatal("duplicate automatic release", len(orders))
					}
					order = s.GetReleaseOrder(orders[0].ID)
					if order.Status != "running" && order.Status != "queued" {
						break
					}
				}
				time.Sleep(20 * time.Millisecond)
			}
			if order == nil || order.Status != "success" || order.ArtifactID != built.ArtifactID || order.Script == nil || order.Script.NeedsReconciliation {
				t.Fatalf("build did not automatically publish its artifact: %+v; log=%s", order, s.RunLog(built.ID))
			}
			mu.Lock()
			defer mu.Unlock()
			for _, endpoint := range []string{"POST /jars/upload", "POST /jobs/old-job/stop", "POST /jars/new-build.jar/run"} {
				n := 0
				for _, call := range calls {
					if call == endpoint {
						n++
					}
				}
				if n != 1 {
					t.Errorf("expected one %s, got %d", endpoint, n)
				}
			}
		})
	}
}
