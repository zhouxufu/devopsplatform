package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"logplatform/internal/smartdocplugin"
	"logplatform/pkg/apidoc"
	"logplatform/pkg/extensionclient"
	p "logplatform/pkg/extensionprotocol"
	"logplatform/pkg/workflownode"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const apiDocumentFixture = `{"openapi":"3.0.3","info":{"title":"Fixture orders","version":"v1"},"paths":{"/orders":{"get":{"responses":{"200":{"description":"ok"}}}}}}`

func documentBrokerFixture(t *testing.T) *atomic.Bool {
	t.Helper()
	enabled := &atomic.Bool{}
	enabled.Store(true)
	handler := smartdocplugin.Handler("fixture-token")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-broker-token-0123456789012345" {
			w.WriteHeader(401)
			return
		}
		var in struct {
			ID      string    `json:"id"`
			Request p.Request `json:"request"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			w.WriteHeader(400)
			return
		}
		if !enabled.Load() {
			json.NewEncoder(w).Encode(p.Response{Error: &p.Error{Code: "unavailable", Message: "fixture disabled"}})
			return
		}
		in.Request.Actor = p.Actor{User: "platform-build", Role: "admin"}
		raw, _ := json.Marshal(in.Request)
		q := httptest.NewRequest("POST", "/v1/invoke", strings.NewReader(string(raw)))
		q.Header.Set("Authorization", "Bearer fixture-token")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, q)
		var response p.Response
		_ = json.Unmarshal(rec.Body.Bytes(), &response)
		response.Provider = &p.ProviderInfo{ID: smartdocplugin.ID, Version: smartdocplugin.Version, Digest: strings.Repeat("f", 64)}
		_ = json.NewEncoder(w).Encode(response)
	}))
	t.Cleanup(srv.Close)
	file := filepath.Join(t.TempDir(), "broker.json")
	raw, _ := json.Marshal(extensionclient.Config{URL: srv.URL, Token: "fixture-broker-token-0123456789012345"})
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(extensionclient.ConfigEnv, file)
	return enabled
}
func TestAPIDocumentRoutesImmutableScopedAndRevisioned(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, name := range []string{"orders", "other"} {
		if err = st.UpsertService(Service{Name: name, ArtifactType: "jar", BuildTool: "maven"}); err != nil {
			t.Fatal(err)
		}
	}
	hub := NewLogHub()
	h := Routes(st, NewBuilder(st, st.dataDir, hub), hub, "", NewStepLib("../../steps"))
	request := func(method, path, body, role string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("X-User", "fixture")
		r.Header.Set("X-Role", role)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if r := request("POST", "/api/api-documents/import?service=orders", apiDocumentFixture, "member"); r.Code != 403 {
		t.Fatal(r.Code)
	}
	one := request("POST", "/api/api-documents/import?service=orders", apiDocumentFixture, "admin")
	two := request("POST", "/api/api-documents/import?service=orders", apiDocumentFixture, "admin")
	if one.Code != 200 || one.Body.String() != two.Body.String() {
		t.Fatalf("import idempotency: %s %s", one.Body.String(), two.Body.String())
	}
	var imported struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(one.Body.Bytes(), &imported)
	if r := request("GET", fmt.Sprintf("/api/api-documents/%d?service=other", imported.ID), "", "member"); r.Code != 404 {
		t.Fatal("cross-app snapshot accepted")
	}
	if r := request("PUT", fmt.Sprintf("/api/api-documents/%d?service=orders", imported.ID), apiDocumentFixture, "admin"); r.Code != 405 {
		t.Fatal("snapshot mutation accepted")
	}
	documentBrokerFixture(t)
	config := `{"provider":"smart-doc","revision":0,"settings":{"plugin_version":"2.7.3"}}`
	if r := request("PUT", "/api/api-documents/source?service=orders", config, "admin"); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if r := request("PUT", "/api/api-documents/source?service=orders", config, "admin"); r.Code != 409 {
		t.Fatal("stale update accepted")
	}
}
func TestWorkflowDocumentUsesFrozenArtifactInFreshWorkspace(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	enabled := documentBrokerFixture(t)
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init")
	git("config", "user.name", "Fixture")
	git("config", "user.email", "fixture@example.invalid")
	for name, body := range map[string]string{"pom.xml": "<project/>", "smart-doc.json": "{}", ".gitignore": "target/\n", "mvnw": "#!/bin/sh\nset -eu\nmkdir -p target/api-docs\ncat > target/api-docs/openapi.json <<'DOC'\n" + apiDocumentFixture + "\nDOC\n"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	git("add", ".")
	git("commit", "-m", "fixture")
	commit := git("rev-parse", "HEAD")
	if err := st.UpsertService(Service{Name: "orders", GitURL: repo, BuildTool: "maven", ArtifactType: "jar", MavenExecutable: "auto", MavenGoals: "package"}); err != nil {
		t.Fatal(err)
	}
	sv := st.GetService("orders")
	run := &PipelineRun{Service: sv.Name, Actor: "fixture", Branch: commit}
	if err := st.CreateRun(run); err != nil {
		t.Fatal(err)
	}
	if err := st.saveBuildSnapshot(run.ID, sv, st.GetServiceDelivery(sv.Name)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE build_run_snapshots SET commit_ref=? WHERE run_id=?`, commit, run.ID); err != nil {
		t.Fatal(err)
	}
	artifact, err := st.RecordPipelineArtifact(run.ID, "fixture", Artifact{Service: sv.Name, Kind: ArtifactJar, Version: commit, Digest: "sha256:fixture-original", URI: "/fixture/not-executed.jar"})
	if err != nil {
		t.Fatal(err)
	}
	st.FinishRun(run.ID, "success")
	source := apidoc.Source{Provider: "smart-doc", Settings: json.RawMessage(`{"plugin_version":"2.7.3","config_file":"smart-doc.json"}`)}
	if err := st.saveAPIDocumentSource(sv, source, "fixture"); err != nil {
		t.Fatal(err)
	}
	node := DeliveryNode{ID: "docs", Kind: "extension", Name: "生成文档", Extension: &workflownode.Binding{Provider: "smart-doc", NodeKey: "api-documentation", Documents: []workflownode.DocumentTask{{ArtifactID: 999}}}}
	binding, err := st.prepareWorkflowExtension(node, []DeliveryService{{Service: sv.Name, Artifact: *artifact}})
	if err != nil {
		t.Fatal(err)
	}
	if len(binding.Documents) != 1 || binding.Documents[0].ArtifactID != artifact.ID || binding.Documents[0].Commit != commit {
		t.Fatalf("untrusted node input survived: %+v", binding)
	}
	hub := NewLogHub()
	pr := NewPipelineRunner(st, NewBuilder(st, st.dataDir, hub), hub, nil, NewStepLib("../../steps"))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := pr.generateWorkflowDocument(ctx, 17, node.ID, binding.Documents[0]); err != nil {
		t.Fatal(err)
	}
	var raw, actualCommit, actualDigest string
	if err := st.db.QueryRow(`SELECT document,commit_ref,artifact_digest FROM api_document_versions WHERE artifact_id=?`, artifact.ID).Scan(&raw, &actualCommit, &actualDigest); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, "Fixture orders") || actualCommit != commit || actualDigest != artifact.Digest {
		t.Fatal("snapshot not linked to exact artifact")
	}
	if _, err := os.Stat(filepath.Join(repo, "target")); !os.IsNotExist(err) {
		t.Fatal("generation modified the original checkout")
	}
	if git("status", "--porcelain") != "" {
		t.Fatal("fixture source changed")
	}
	node.Extension = binding
	plan := DeliveryPlan{Nodes: []DeliveryNode{node}, Services: []DeliveryService{{Service: sv.Name, Artifact: *artifact}}}
	steps := []DeliveryStep{{NodeID: node.ID, Name: node.Name, Kind: "extension", Status: "pending"}}
	planRaw, _ := json.Marshal(plan)
	stepsRaw, _ := json.Marshal(steps)
	result, err := st.db.Exec(`INSERT INTO delivery_runs(plan,digest,status,execution,env_secrets,owner,approver,created_at,revision) VALUES(?,?,'approved',?,'{}','fixture','',?,1)`, string(planRaw), "sha256:fixture-node", string(stepsRaw), time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	deliveryRun := st.DeliveryRun(id)
	st.pipelineRunner = pr
	enabled.Store(false)
	if err := st.advanceWorkflowExtension(deliveryRun, &deliveryRun.Steps[0], node, "fixture"); err == nil || deliveryRun.Steps[0].Status != "pending" {
		t.Fatal("disabled plugin started execution")
	}
	enabled.Store(true)
	if err := st.advanceWorkflowExtension(deliveryRun, &deliveryRun.Steps[0], node, "fixture"); err != nil {
		t.Fatal(err)
	}
	if action, _ := deliveryControlDecision(st, deliveryRun); action != "wait" {
		t.Fatal("coordinator did not wait for active plugin task")
	}
	if err := st.advanceWorkflowExtension(deliveryRun, &deliveryRun.Steps[0], node, "fixture"); err == nil {
		t.Fatal("duplicate plugin task started")
	}
	for until := time.Now().Add(8 * time.Second); time.Now().Before(until); {
		latest := st.DeliveryRun(id)
		if latest.Steps[0].Status != "executing" {
			if latest.Steps[0].Status != "success" {
				t.Fatal(latest.Steps[0].Detail)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if latest := st.DeliveryRun(id); latest.Steps[0].Status != "success" {
		t.Fatal("plugin node did not finish")
	}
	enabled.Store(false)
	if _, err := st.prepareWorkflowExtension(node, []DeliveryService{{Service: sv.Name, Artifact: *artifact}}); err == nil {
		t.Fatal("disabled plugin allowed new plan")
	}
}

func TestDocumentGenerationRejectsStaleOutput(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	workspace := t.TempDir()
	output := filepath.Join(workspace, "target", "openapi.json")
	if err := os.MkdirAll(filepath.Dir(output), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte(apiDocumentFixture), 0600); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(t.TempDir(), "fixture-maven")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	sv := &Service{Name: "fixture", ArtifactType: "jar", BuildTool: "maven", MavenExecutable: executable, APIDocumentation: &apidoc.Frozen{Source: apidoc.Source{Required: true}, Plan: &apidoc.Plan{Version: 1, Provider: "fixture", ProviderVersion: "1", Tool: "maven", Tasks: []string{"docs:openapi"}, OutputFile: "target/openapi.json", Format: "openapi-json"}}}
	hub := NewLogHub()
	pr := NewPipelineRunner(st, NewBuilder(st, st.dataDir, hub), hub, nil, nil)
	if err := pr.generateAPIDocumentWithEmit(context.Background(), 99, sv, &Artifact{ID: 1}, workspace, t.TempDir(), func(string) {}); err == nil {
		t.Fatal("old document was mistaken for new output")
	}
	var count int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM api_document_versions`).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed generation archived stale output")
	}
}
func TestDocumentGenerationCannotArchiveOldOrSymlinkOutput(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "target"), 0700)
	outside := filepath.Join(t.TempDir(), "private.json")
	_ = os.WriteFile(outside, []byte(apiDocumentFixture), 0600)
	if err := os.Symlink(outside, filepath.Join(root, "target", "openapi.json")); err != nil {
		t.Skip(err)
	}
	if _, err := safeDocumentPath(root, "target/openapi.json"); err == nil {
		t.Fatal("symlink output accepted")
	}
}
