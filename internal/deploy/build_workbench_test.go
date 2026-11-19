package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildWorkbenchPagingAndOldSummary(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.db.Close()
	if e = s.UpsertService(Service{Name: "old-app", GitURL: "https://example.invalid/a.git", ArtifactType: "jar"}); e != nil {
		t.Fatal(e)
	}
	if e = s.UpsertService(Service{Name: "busy-app", ArtifactType: "jar"}); e != nil {
		t.Fatal(e)
	}
	tx, e := s.db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	for i := 1; i <= 1301; i++ {
		name := "busy-app"
		if i == 1 {
			name = "old-app"
		}
		_, e = tx.Exec(`INSERT INTO pipeline_runs(service,status,branch,version,actor,started_at,ended_at) VALUES(?,?,?,?,?,?,?)`, name, "failed", "feature", fmt.Sprint(i), "alice", i, i+1)
		if e != nil {
			t.Fatal(e)
		}
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	if v := s.GetRun(1); v == nil || v.Service != "old-app" {
		t.Fatal("run beyond old 1000-record cache lost")
	}
	summary, e := s.BuildApplicationSummaries()
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, item := range summary {
		if item.Name == "old-app" {
			found = item.LatestRun != nil && item.LatestRun.ID == 1
		}
	}
	if !found {
		t.Fatal("old app latest status missing")
	}
	page, e := s.BuildList("runs", BuildListFilter{Service: "busy-app", Page: 2, PageSize: 20, Actor: "alice", Branch: "feature", Start: 1, End: 1400})
	if e != nil || page.Total != 1300 || len(page.Items) != 20 {
		t.Fatalf("pagination=%+v %v", page, e)
	}
	page, e = s.BuildList("runs", BuildListFilter{Query: "%_! OR 1=1", PageSize: 20})
	if e != nil || page.Total != 0 {
		t.Fatal("wildcard query was not literal", e)
	}
	if _, e = s.BuildList("runs", BuildListFilter{Start: 20, End: 10}); e == nil {
		t.Fatal("inverted date accepted")
	}
	if _, e = s.BuildList("runs", BuildListFilter{PageSize: 10001}); e == nil {
		t.Fatal("unbounded page accepted")
	}
}

func TestBuildControlAuthorizationAndCancelLock(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b := NewBuilder(s, t.TempDir(), NewLogHub())
	pr := NewPipelineRunner(s, b, NewLogHub(), nil, nil)
	run := &PipelineRun{Service: "fixture", Actor: "alice", StartedAt: time.Now().Unix()}
	if err = s.CreateRun(run); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pr.cancellations[run.ID] = cancel
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, v any) { json.NewEncoder(w).Encode(v) }
	fail := func(w http.ResponseWriter, n int, v any) {
		w.WriteHeader(n)
		json.NewEncoder(w).Encode(map[string]any{"error": fmt.Sprint(v)})
	}
	registerBuildControlRoutes(mux, s, pr, write, fail, func(r *http.Request) string { return r.Header.Get("X-User") })
	request := func(action, user, body string) int {
		req := httptest.NewRequest("POST", fmt.Sprintf("/api/pipeline/runs/%d/%s", run.ID, action), strings.NewReader(body))
		req.Header.Set("X-User", user)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w.Code
	}
	for _, action := range []string{"retry", "stop"} {
		if n := request(action, "bob", `{"reason":"fixture","confirm":true}`); n != 403 {
			t.Fatalf("unauthorized %s=%d", action, n)
		}
	}
	if n := request("stop", "alice", `{}`); n != 400 {
		t.Fatalf("missing reason %d", n)
	}
	if n := request("stop", "alice", `{"reason":"fixture cancellation"}`); n != 202 {
		t.Fatalf("cancel %d", n)
	}
	if ctx.Err() != context.Canceled {
		t.Fatal("executor not canceled")
	}
	if s.GetRun(run.ID).Status != "running" {
		t.Fatal("lock released before executor exits")
	}
	s.FinishRun(run.ID, "canceled")
	if err = s.CreateRun(&PipelineRun{Service: "fixture", Actor: "alice", StartedAt: time.Now().Unix()}); err != nil {
		t.Fatal("lock not released after executor exit", err)
	}
}

func TestBuildLineWriterBoundedAndComplete(t *testing.T) {
	var lines []string
	w := &buildLineWriter{emit: func(l string) { lines = append(lines, l) }}
	w.Write([]byte("sec"))
	w.Write([]byte("ret\npartial"))
	w.flush()
	if strings.Join(lines, "") != "secret\npartial\n" {
		t.Fatal(lines)
	}
	lines = nil
	w.Write([]byte(strings.Repeat("s", 300<<10) + "\nnext\n"))
	w.flush()
	if len(lines) != 2 || strings.Contains(lines[0], "ssss") || lines[1] != "next\n" {
		t.Fatal("oversized output split or blocked later logs")
	}
}

func TestCanceledPipelineNeverFinishesSuccessAndReleasesRunner(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.InitializeEnvironments([]Environment{{Name: "test", DisplayName: "测试"}}); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	dir := filepath.Join(root, "fixture")
	if err = os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "step.yaml"), []byte("id: fixture\nname: Fixture wait\nkind: build\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/bash\necho fixture-ready\nsleep 60 &\nwait\n"), 0700); err != nil {
		t.Fatal(err)
	}
	sv := Service{Name: "fixture-pipeline", ArtifactType: "jar", BuildTool: "command", BuildCmd: "true", Steps: []StepInst{{Step: "fixture"}}}
	if err = s.UpsertService(sv); err != nil {
		t.Fatal(err)
	}
	pr := NewPipelineRunner(s, NewBuilder(s, t.TempDir(), NewLogHub()), NewLogHub(), nil, NewStepLib(root))
	id, err := pr.StartWithOptions(s.GetService(sv.Name), true, PipelineRunOptions{DeployEnvironment: "test"}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pr.cancelBuild(id) }()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(s.RunLog(id), "fixture-ready") {
		if time.Now().After(deadline) {
			t.Fatal("fixture did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = pr.cancelBuild(id); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		v := s.GetRun(id)
		pr.mu.Lock()
		active := pr.activeByRunner["local"]
		pr.mu.Unlock()
		if v.Status == "canceled" && active == 0 {
			break
		}
		if v.Status == "success" {
			t.Fatal("canceled pipeline incorrectly succeeded")
		}
		if time.Now().After(deadline) {
			t.Fatal("executor or runner lock not released")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(s.Deploys(sv.Name, 10)) != 0 || len(s.ReleaseOrders(sv.Name, 10)) != 0 {
		t.Fatal("cancellation produced deployment")
	}
}
func TestBuildSnapshotEncryptedAndPinned(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.db.Close()
	sv := Service{Name: "orders", GitURL: "https://example.invalid/orders.git", ArtifactType: "jar", BuildTool: "maven", MavenExecutable: "mvn", MavenGoals: "clean package", MavenProperties: "custom.value=fixture-sensitive", Branch: "test"}
	if e = s.UpsertService(sv); e != nil {
		t.Fatal(e)
	}
	sv = *s.GetService(sv.Name)
	run := &PipelineRun{Service: sv.Name, Actor: "alice", StartedAt: 1}
	if e = s.CreateRun(run); e != nil {
		t.Fatal(e)
	}
	delivery := s.GetServiceDelivery(sv.Name)
	if e = s.saveBuildSnapshot(run.ID, &sv, delivery); e != nil {
		t.Fatal(e)
	}
	var cipher string
	s.db.QueryRow(`SELECT config_cipher FROM build_run_snapshots WHERE run_id=?`, run.ID).Scan(&cipher)
	if strings.Contains(cipher, "fixture-sensitive") {
		t.Fatal("plain snapshot")
	}
	if _, _, _, e = s.buildReplay(run.ID); e == nil {
		t.Fatal("retry before source resolution allowed")
	}
	commit := strings.Repeat("a", 40)
	s.db.Exec(`UPDATE build_run_snapshots SET commit_ref=? WHERE run_id=?`, commit, run.ID)
	changed := sv
	changed.MavenGoals = "deploy"
	changed.Branch = "prod"
	if e = s.UpsertService(changed); e != nil {
		t.Fatal(e)
	}
	replay, d, ref, e := s.buildReplay(run.ID)
	if e != nil {
		t.Fatal(e)
	}
	if ref != commit || replay.Branch != commit || replay.MavenGoals != "clean package" || d.AutoDeployEnvironment != "" {
		t.Fatal("retry follows current branch/config or deploys")
	}
	if s.GetService(sv.Name).Branch != "prod" {
		t.Fatal("retry changed default configuration")
	}
	b, _ := json.Marshal(replay)
	if strings.Contains(string(b), "runtime_secrets") {
		t.Fatal("runtime secrets escaped")
	}
}
func TestBuildCancellationStopsOwnedStep(t *testing.T) {
	if os.PathSeparator != '/' {
		t.Skip("Unix cancellation test")
	}
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.db.Close()
	dir := t.TempDir()
	marker := filepath.Join(dir, "fixture-material")
	if e = os.WriteFile(marker, []byte("fixture-only"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/bash\ntrap 'rm -f \"$LP_FIXTURE_MARKER\"' EXIT\necho fixture-ready\nsleep 60 &\nwait\n"), 0700); e != nil {
		t.Fatal(e)
	}
	b := NewBuilder(s, t.TempDir(), NewLogHub())
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{}, 1)
	done := make(chan int, 1)
	go func() {
		done <- b.execStepContext(ctx, &StepDef{Dir: dir}, append(os.Environ(), "LP_FIXTURE_MARKER="+marker), func(line string) {
			if strings.Contains(line, "fixture-ready") {
				select {
				case ready <- struct{}{}:
				default:
				}
			}
		})
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("fixture did not start")
	}
	cancel()
	select {
	case code := <-done:
		if code == 0 {
			t.Fatal("canceled command succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("owned descendant kept stdout alive")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("shell cleanup trap did not remove fixture material")
	}
	run := &PipelineRun{Service: "orders", Actor: "alice", StartedAt: 1}
	if e = s.CreateRun(run); e != nil {
		t.Fatal(e)
	}
	pr := NewPipelineRunner(s, b, NewLogHub(), nil, nil)
	if e = pr.cancelBuild(run.ID); e == nil {
		t.Fatal("detached/foreign executor cancellation accepted")
	}
}
