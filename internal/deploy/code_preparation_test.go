package deploy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// All Git writes in these tests are confined to temporary repositories.
func preparationTestSession(t *testing.T) *gitSession {
	t.Helper()
	g, err := newGitSession("", nil)
	if err != nil {
		t.Fatal(err)
	}
	g.args = append(g.args, "-c", "protocol.file.allow=always", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgSign=false")
	t.Cleanup(g.close)
	return g
}
func preparationGitCommand(t *testing.T, g *gitSession, args ...string) string {
	t.Helper()
	out, err := g.repo(context.Background(), args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}
func preparationRemote(t *testing.T) (*gitSession, string, string) {
	t.Helper()
	g := preparationTestSession(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	if _, err := g.run(context.Background(), "init", "--bare", remote); err != nil {
		t.Fatal(err)
	}
	preparationGitCommand(t, func() *gitSession {
		if _, err := g.run(context.Background(), "init", "--initial-branch=dev", filepath.Join(g.dir, "repo")); err != nil {
			t.Fatal(err)
		}
		return g
	}(), "remote", "add", "origin", remote)
	preparationFile(t, g, "shared.txt", "base\n")
	preparationGitCommand(t, g, "add", ".")
	preparationGitCommand(t, g, "commit", "-m", "base")
	base := preparationGitCommand(t, g, "rev-parse", "HEAD")
	preparationGitCommand(t, g, "branch", "main")
	preparationGitCommand(t, g, "push", "origin", "dev", "main")
	return g, remote, base
}
func preparationFile(t *testing.T, g *gitSession, name, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(g.dir, "repo", name), []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}
func preparationSource(t *testing.T, g *gitSession, branch, file, value string) string {
	t.Helper()
	preparationGitCommand(t, g, "checkout", "-B", branch, "main")
	preparationFile(t, g, file, value)
	preparationGitCommand(t, g, "add", ".")
	preparationGitCommand(t, g, "commit", "-m", branch)
	preparationGitCommand(t, g, "push", "origin", branch)
	return preparationGitCommand(t, g, "rev-parse", "HEAD")
}
func TestCodePreparationMergeAndConcurrentTipGuard(t *testing.T) {
	seed, url, base := preparationRemote(t)
	a := preparationSource(t, seed, "feature/a", "a.txt", "A\n")
	b := preparationSource(t, seed, "feature/b", "b.txt", "B\n")
	r := PreparedRepository{URL: url, Policy: CodePolicy{Mode: "integration"}, BaseBranch: "dev", BaseCommit: base, TargetBranch: "dev", TargetCommit: base, Sources: []PreparedSource{{Branch: "feature/a", Commit: a}, {Branch: "feature/b", Commit: b}}}
	g := preparationTestSession(t)
	if err := mergePreparationRepository(context.Background(), g, &r, false); err != nil {
		t.Fatal(err)
	}
	if !g.ancestor(context.Background(), a, r.ResultCommit) || !g.ancestor(context.Background(), b, r.ResultCommit) {
		t.Fatal("not all sources merged")
	}
	if err := verifyPreparedWorkspace(context.Background(), filepath.Join(g.dir, "repo"), r.ResultCommit); err != nil {
		t.Fatal(err)
	}
	if err := verifyPreparedWorkspace(context.Background(), filepath.Join(g.dir, "repo"), base); err == nil {
		t.Fatal("build workspace drift ignored")
	}
	// Preparing the merge alone must not write the remote.
	tip, _ := g.run(context.Background(), "ls-remote", url, "refs/heads/dev")
	if !strings.HasPrefix(tip, base+"\t") {
		t.Fatal("preparation pushed without confirmation")
	}
	preparationGitCommand(t, g, "push", "origin", r.ResultCommit+":refs/heads/dev")
	resume := preparationTestSession(t)
	if err := mergePreparationRepository(context.Background(), resume, &r, true); err != nil {
		t.Fatal(err)
	}
	if r.Status != "merged" {
		t.Fatal(r.Status)
	}
	stale := preparationTestSession(t)
	if err := mergePreparationRepository(context.Background(), stale, &r, false); err == nil {
		t.Fatal("changed target tip accepted")
	}
}
func TestCodePreparationConflictAndVerifiedManualResume(t *testing.T) {
	seed, url, base := preparationRemote(t)
	a := preparationSource(t, seed, "feature/a", "shared.txt", "A\n")
	b := preparationSource(t, seed, "feature/b", "shared.txt", "B\n")
	r := PreparedRepository{URL: url, Policy: CodePolicy{Mode: "integration"}, BaseBranch: "dev", BaseCommit: base, TargetBranch: "dev", TargetCommit: base, Sources: []PreparedSource{{Branch: "feature/a", Commit: a}, {Branch: "feature/b", Commit: b}}}
	g := preparationTestSession(t)
	if err := mergePreparationRepository(context.Background(), g, &r, false); err == nil || r.Status != "conflict" || len(r.Conflicts) != 1 || r.Conflicts[0] != "shared.txt" {
		t.Fatalf("missing conflict: %+v %v", r, err)
	}
	if err := mergePreparationRepository(context.Background(), preparationTestSession(t), &r, true); err == nil {
		t.Fatal("manual confirmation without a real merge accepted")
	}
	// A developer resolves the conflict in a separate temporary checkout.
	preparationGitCommand(t, seed, "checkout", "dev")
	preparationGitCommand(t, seed, "merge", "--no-ff", "--no-edit", a)
	if _, err := seed.repo(context.Background(), "merge", "--no-ff", "--no-edit", b); err == nil {
		t.Fatal("fixture did not conflict")
	}
	preparationFile(t, seed, "shared.txt", "A+B\n")
	preparationGitCommand(t, seed, "add", ".")
	preparationGitCommand(t, seed, "commit", "-m", "Resolve selected fixes")
	preparationGitCommand(t, seed, "push", "origin", "dev")
	if err := mergePreparationRepository(context.Background(), preparationTestSession(t), &r, true); err != nil {
		t.Fatal(err)
	}
	if len(r.Conflicts) != 0 || r.Status != "merged" {
		t.Fatal("conflict not cleared")
	}
}
func TestCodePreparationIsolatedScopeAndSourceDrift(t *testing.T) {
	seed, url, base := preparationRemote(t)
	a := preparationSource(t, seed, "feature/a", "a.txt", "A\n")
	r := PreparedRepository{URL: url, Policy: CodePolicy{Mode: "isolated"}, BaseBranch: "main", BaseCommit: base, TargetBranch: "release/v1", Sources: []PreparedSource{{Branch: "feature/a", Commit: a}}}
	g := preparationTestSession(t)
	if err := mergePreparationRepository(context.Background(), g, &r, false); err != nil {
		t.Fatal(err)
	}
	preparationGitCommand(t, g, "push", "origin", r.ResultCommit+":refs/heads/release/v1")
	if err := mergePreparationRepository(context.Background(), preparationTestSession(t), &r, true); err != nil {
		t.Fatal(err)
	}
	preparationGitCommand(t, seed, "fetch", "origin")
	preparationGitCommand(t, seed, "checkout", "-B", "release/v1", "origin/release/v1")
	preparationFile(t, seed, "extra.txt", "Unselected\n")
	preparationGitCommand(t, seed, "add", ".")
	preparationGitCommand(t, seed, "commit", "-m", "Unselected")
	preparationGitCommand(t, seed, "push", "origin", "release/v1")
	if err := mergePreparationRepository(context.Background(), preparationTestSession(t), &r, true); err == nil {
		t.Fatal("isolated scope accepted unselected commit")
	}
	preparationGitCommand(t, seed, "checkout", "feature/a")
	preparationFile(t, seed, "a.txt", "A2\n")
	preparationGitCommand(t, seed, "add", ".")
	preparationGitCommand(t, seed, "commit", "-m", "Followup")
	preparationGitCommand(t, seed, "push", "origin", "feature/a")
	r.TargetBranch = "release/v2"
	if err := mergePreparationRepository(context.Background(), preparationTestSession(t), &r, false); err == nil {
		t.Fatal("changed source tip accepted")
	}
}
func TestCodePreparationPersistenceLocksAndAPI(t *testing.T) {
	st, _, _ := deliveryFixture(t)
	p := &CodePreparation{ID: "fixture-a", Owner: "local", State: "conflict", Protected: true, Repositories: []PreparedRepository{{URL: "https://example.invalid/repo.git", TargetBranch: "dev"}}}
	if err := st.saveCodePreparation(p); err != nil {
		t.Fatal(err)
	}
	stale := st.CodePreparation(p.ID)
	p.Message = "new revision"
	if err := st.saveCodePreparation(p); err != nil {
		t.Fatal(err)
	}
	if err := st.saveCodePreparation(stale); !errors.Is(err, ErrRunConflict) {
		t.Fatal("stale update accepted", err)
	}
	if err := st.acquirePreparationLocks(p); err != nil {
		t.Fatal(err)
	}
	other := *p
	other.ID = "fixture-b"
	if err := st.acquirePreparationLocks(&other); err == nil {
		t.Fatal("branch lock shared")
	}
	st.releasePreparationLocks(&other)
	if err := st.acquirePreparationLocks(&other); err == nil {
		t.Fatal("other task removed lock")
	}
	mux := http.NewServeMux()
	registerCodePreparationRoutes(mux, st, nil)
	call := func(action, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/delivery/code-preparations/"+p.ID+"/"+action, strings.NewReader(body)))
		return w
	}
	if w := call("resume", `{"confirm":true}`); w.Code != 400 {
		t.Fatal("production scope not confirmed", w.Code, w.Body.String())
	}
	if w := call("cancel", `{"confirm":true}`); w.Code != 200 {
		t.Fatal("cancel requires unnecessary production attestation", w.Code, w.Body.String())
	}
	if err := st.acquirePreparationLocks(&other); err != nil {
		t.Fatal("cancel did not release branch lock", err)
	}
	st.releasePreparationLocks(&other)
	if w := call("start", `bad json`); w.Code != 400 || strings.Count(w.Body.String(), "error") != 1 {
		t.Fatal("malformed body double-written", w.Body.String())
	}
}
func TestCodePreparationPinsBuildWithoutEditingService(t *testing.T) {
	st, _, _ := deliveryFixture(t)
	sv := st.GetService("orders")
	sv.GitURL = "https://example.invalid/orders.git"
	sv.Branch = "dev"
	sv.Steps = []StepInst{{Step: "git-checkout", Params: map[string]string{"GIT_URL": sv.GitURL, "BRANCH": "dev"}}, {Step: "java-build"}}
	if err := st.UpsertService(*sv); err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("a", 40)
	prepared, err := st.preparedBuildService(&PreparedBuild{Service: sv.Name, Commit: sha})
	if err != nil || prepared.Branch != sha || prepared.Steps[0].Params["BRANCH"] != sha {
		t.Fatal("build not pinned", prepared, err)
	}
	if st.GetService("orders").Branch != "dev" || st.GetService("orders").Steps[0].Params["BRANCH"] != "dev" {
		t.Fatal("saved application configuration mutated")
	}
	sv.Steps = append(sv.Steps, sv.Steps[0])
	st.UpsertService(*sv)
	if _, err := st.preparedBuildService(&PreparedBuild{Service: sv.Name, Commit: sha}); err == nil {
		t.Fatal("ambiguous multiple source steps accepted")
	}
	for _, branch := range []string{"--help", "a..b", "a//b", "release/../main", "release/x.lock", "refs/@{x}"} {
		if validPreparationBranch(branch) {
			t.Fatal("unsafe branch accepted", branch)
		}
	}
}

func TestCodePreparationRecoveredBuildAndArtifactProvenance(t *testing.T) {
	st, _, req := deliveryFixture(t)
	w := &WorkItem{Title: "Fix", Kind: "bug"}
	if err := st.CreateWorkItem(w); err != nil {
		t.Fatal(err)
	}
	c := &Change{Title: "Code fix", Kind: "code", Service: "orders", Reference: "feature/fix"}
	if err := st.CreateChange(c); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChangeWorkItems(c.ID, []int64{w.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetChangeReady(c.ID, true); err != nil {
		t.Fatal(err)
	}
	tpl := &DeliveryTemplate{Name: "Dynamic", Mode: "version", Nodes: []DeliveryNode{{ID: "deploy", Kind: "deploy", Name: "Deploy"}}}
	if err := st.SaveDeliveryTemplate(tpl); err != nil {
		t.Fatal(err)
	}
	req.TemplateID, req.SourceType, req.WorkItemIDs = tpl.ID, "work_items", []int64{w.ID}
	scope, _, err := st.resolveDeliveryScope(req, tpl)
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("a", 40)
	p := &CodePreparation{ID: "recovered-build", Owner: "alice", State: "building", Request: req, ScopeDigest: codeScopeDigest(scope), Builds: []PreparedBuild{{Service: "orders", Commit: sha, ConfigurationDigest: preparationConfiguration(st.GetService("orders"), st.GetServiceDelivery("orders")), Status: "starting"}}}
	if err := st.saveCodePreparation(p); err != nil {
		t.Fatal(err)
	}
	r := &PipelineRun{Service: "orders", Actor: "alice", Branch: sha, Trigger: "code-preparation:" + p.ID + ":orders"}
	if err := st.CreateRun(r); err != nil {
		t.Fatal(err)
	}
	a := &Artifact{Service: "orders", Version: sha[:7], Kind: "jar", URI: "/never-executed.jar", Digest: "sha256:fixture", Source: "pipeline-run", SourceID: r.ID}
	if err := st.RegisterArtifact(a); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE pipeline_runs SET artifact_id=? WHERE id=?`, a.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`INSERT INTO build_run_snapshots(run_id,config_cipher,commit_ref,created_at) VALUES(?,'fixture',?,1)`, r.ID, sha); err != nil {
		t.Fatal(err)
	}
	st.FinishRun(r.ID, "success")
	// No runner is provided: recovery must find the already-created run, not rebuild.
	st.performPreparedBuilds(p, nil)
	if p.State != "ready" || p.Builds[0].RunID != r.ID || p.Builds[0].ArtifactID != a.ID {
		t.Fatal("recovered build was not associated", p)
	}
	req.CodePreparationID = p.ID
	req.Artifacts = map[string]int64{"orders": a.ID}
	if err := st.validatePreparedArtifacts(req, scope); err != nil {
		t.Fatal("valid artifact rejected", err)
	}
	if _, err := st.db.Exec(`UPDATE build_run_snapshots SET commit_ref=? WHERE run_id=?`, strings.Repeat("b", 40), r.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.validatePreparedArtifacts(req, scope); err == nil {
		t.Fatal("wrong actual commit accepted")
	}
	st.db.Exec(`UPDATE build_run_snapshots SET commit_ref=? WHERE run_id=?`, sha, r.ID)
	st.db.Exec(`UPDATE artifacts SET digest='sha256:tampered' WHERE id=?`, a.ID)
	if err := st.validatePreparedArtifacts(req, scope); err == nil {
		t.Fatal("changed artifact digest accepted")
	}
	var count int
	st.db.QueryRow(`SELECT COUNT(*) FROM pipeline_runs`).Scan(&count)
	if count != 1 {
		t.Fatal("lost start produced duplicate build", count)
	}
}
