package deploy

import (
	"context"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Real temporary Git discovery/checkout, build runner and original release
// engine; publication is a harmless local fixture script, never a business host.
func TestServiceDeploymentBuildPublishEndToEnd(t *testing.T) {
	s, m, pr, git, work, bare, commit := serviceBuildFixture(t, false)
	spec := ServiceDeploymentSpec{Service: "orders", Environment: "test", Mode: "build", Reason: "isolated end-to-end deployment"}
	bad := spec
	bad.Branch = "feature/bug"
	if _, e := s.previewServiceDeployment(context.Background(), m, bad); e == nil {
		t.Fatal("requirement source replaced public target")
	}
	g, e := s.previewServiceDeployment(context.Background(), m, spec)
	if e != nil {
		t.Fatal(e)
	}
	if g.Commit != commit || g.Spec.Branch != "shared-test" {
		t.Fatal(g)
	}
	task := &ServiceDeployment{ID: strings.Repeat("f", 32), Owner: "alice", Authority: "mcp", AuthorityKeyID: "fixture", Grant: *g, State: "running", Stage: "authorized", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	if e = s.saveServiceDeployment(task); e != nil {
		t.Fatal(e)
	}
	// Remote branch advances after authorization: keep building the approved SHA.
	os.WriteFile(filepath.Join(work, "README"), []byte("later unapproved source"), 0600)
	git("-C", work, "add", "README")
	git("-C", work, "-c", "user.email=fixture@example.test", "-c", "user.name=fixture", "commit", "-m", "later")
	git("-C", work, "push", bare, "shared-test")
	git("--git-dir="+bare, "update-server-info")
	next, e := s.previewServiceDeployment(context.Background(), m, spec)
	if e != nil || next.Digest == g.Digest {
		t.Fatal("new preview did not expose source change", e)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		task, _ = s.serviceDeployment(task.ID)
		if e = s.advanceServiceDeployment(task, pr, m); e != nil {
			t.Fatal(e)
		}
		if task.State == "success" {
			break
		}
		if task.State != "running" {
			t.Fatalf("%+v; log=%s", task, s.RunLog(task.RunID))
		}
		time.Sleep(50 * time.Millisecond)
	}
	if task.State != "success" || task.RunID == 0 || task.ReleaseID == 0 {
		t.Fatalf("%+v; log=%s", task, s.RunLog(task.RunID))
	}
	run := s.GetRun(task.RunID)
	order := s.GetReleaseOrder(task.ReleaseID)
	if run.Branch != commit || order.ArtifactID != run.ArtifactID || order.Number != 1 || order.Script == nil || order.Script.Result == nil {
		t.Fatal(run, order)
	}
	if len(s.ReleaseOrders("orders", 10)) != 1 {
		t.Fatal("duplicate release")
	}
}

func serviceBuildFixture(t *testing.T, smart bool) (*Store, *ReleaseManager, *PipelineRunner, func(...string) string, string, string, string) {
	t.Helper()
	root := t.TempDir()
	work, bare := filepath.Join(root, "work"), filepath.Join(root, "repo.git")
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
		out, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("git %v: %s %v", args, out, e)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", work)
	os.WriteFile(filepath.Join(work, "README"), []byte("isolated source"), 0600)
	git("-C", work, "add", "README")
	git("-C", work, "-c", "user.email=fixture@example.test", "-c", "user.name=fixture", "commit", "-m", "fixture")
	git("-C", work, "branch", "-M", "shared-test")
	commit := git("-C", work, "rev-parse", "HEAD")
	git("clone", "--bare", work, bare)
	git("--git-dir="+bare, "update-server-info")
	var handler http.Handler = http.FileServer(http.Dir(root))
	if smart {
		git("--git-dir="+bare, "config", "http.receivepack", "true")
		handler = &cgi.Handler{Path: filepath.Join(git("--exec-path"), "git-http-backend"), Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1"}}
	}
	remote := httptest.NewServer(handler)
	t.Cleanup(remote.Close)
	s, m, _ := scriptFixture(t, successfulScriptFixture)
	lib := filepath.Join(root, "steps")
	os.MkdirAll(filepath.Join(lib, "git-checkout"), 0700)
	os.MkdirAll(filepath.Join(lib, "fixture-package"), 0700)
	for _, f := range []string{"step.yaml", "run.sh"} {
		b, e := os.ReadFile(filepath.Join("../../steps/git-checkout", f))
		if e != nil {
			t.Fatal(e)
		}
		os.WriteFile(filepath.Join(lib, "git-checkout", f), b, 0700)
	}
	os.WriteFile(filepath.Join(lib, "fixture-package", "step.yaml"), []byte("id: fixture-package\nname: Synthetic artifact\nkind: package\nproduces: jar\n"), 0600)
	os.WriteFile(filepath.Join(lib, "fixture-package", "run.sh"), []byte("set -eu\nprintf 'synthetic fresh output' > \"$LP_OUTPUT_DIR/new.jar\"\nprintf 'ARTIFACT=%s\\nARTIFACT_KIND=jar\\n' \"$LP_OUTPUT_DIR/new.jar\" > \"$LP_OUTPUTS_FILE\"\n"), 0700)
	sv := *s.GetService("orders")
	sv.GitURL = remote.URL + "/repo.git"
	sv.Branch = "shared-test"
	sv.Steps = []StepInst{{Step: "git-checkout"}, {Step: "fixture-package"}}
	if e := s.UpsertService(sv); e != nil {
		t.Fatal(e)
	}
	policy, e := s.deliveryPolicy("test")
	if e != nil {
		t.Fatal(e)
	}
	if policy.Defaults.Applications == nil {
		policy.Defaults.Applications = map[string]ApplicationDeliveryDefault{}
	}
	app := policy.Defaults.Applications["orders"]
	app.CodePolicy = CodePolicy{Mode: "integration", TargetBranch: "shared-test"}
	policy.Defaults.Applications["orders"] = app
	if e = s.saveDeliveryPolicy("test", policy); e != nil {
		t.Fatal(e)
	}
	hub := NewLogHub()
	pr := NewPipelineRunner(s, NewBuilder(s, filepath.Join(root, "builds"), hub), hub, m.dp, NewStepLib(lib))

	return s, m, pr, git, work, bare, commit
}
