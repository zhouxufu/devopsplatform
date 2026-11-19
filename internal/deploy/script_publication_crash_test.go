package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// This helper deliberately exits without cleanup, simulating loss of the server
// after an external side effect. Its only side effect is a marker in t.TempDir.
func TestScriptPublicationCrashHelper(t *testing.T) {
	root := os.Getenv("SCRIPT_CRASH_FIXTURE_ROOT")
	if root == "" {
		t.Skip("subprocess-only crash fixture")
	}
	if !strings.HasPrefix(root, os.TempDir()) {
		t.Fatal("fixture must use temporary directory")
	}
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.UpsertEnvironment(Environment{Name: "test", DisplayName: "fixture"}); err != nil {
		t.Fatal(err)
	}
	if err = s.UpsertRunner(BuildRunner{Name: "local", Kind: "local", Enabled: true, MaxParallel: 1}); err != nil {
		t.Fatal(err)
	}
	if err = s.UpsertService(Service{Name: "fixture", ArtifactType: "jar", BuildTool: "command", BuildCmd: "true"}); err != nil {
		t.Fatal(err)
	}
	script := "printf x >> " + shellQuote(filepath.Join(root, "external-marker")) + "\nprintf '%s' '{\"status\":\"pending\",\"external_id\":\"crash-fixture-job\",\"values\":{\"phase\":\"submitted\"}}' > \"$LP_RESULT_FILE\"\nsleep 30\n"
	if err = s.UpsertServiceEnvironment(ServiceEnvironment{Service: "fixture", Environment: "test", TargetType: TargetScript, DeployConfig: map[string]string{"script": script, "script_runner": "local", "script_timeout_seconds": "40"}}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "artifact.jar")
	if err = os.WriteFile(file, []byte("synthetic crash artifact"), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := describeArtifact("fixture", "crash-fixture", ArtifactJar, file, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RegisterArtifact(&a); err != nil {
		t.Fatal(err)
	}
	hub := NewLogHub()
	dp := NewDeployer(s, hub, "")
	s.pipelineRunner = NewPipelineRunner(s, NewBuilder(s, root, hub), hub, dp, NewStepLib("../../steps"))
	m := NewReleaseManager(s, dp)
	if _, err = m.CreateArtifact("fixture", a.ID, "test", "isolated crash fixture", "fixture"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err = os.Stat(filepath.Join(root, "external-marker")); err == nil {
			if _, err = readScriptResult(filepath.Join(root, "script-publications/1/result.json")); err == nil {
				os.Exit(0)
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("crash helper did not reach external side effect")
}

func TestScriptPublicationProcessCrashRetainsClaimAndProgress(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX Bash fixture")
	}
	root := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestScriptPublicationCrashHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), "SCRIPT_CRASH_FIXTURE_ROOT="+root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	pidFile := filepath.Join(root, "script-publications/1/process.pid")
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	killFixture := func() {
		_ = exec.Command("bash", "-c", `kill -TERM -- -"$1" 2>/dev/null || true`, "isolated-fixture", strconv.Itoa(pid)).Run()
	}
	t.Cleanup(killFixture)
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m := NewReleaseManager(s, NewDeployer(s, NewLogHub(), ""))
	m.Recover()
	o := s.GetReleaseOrder(1)
	if o.Status != "interrupted" || o.Script == nil || !o.Script.NeedsReconciliation || o.Script.Result == nil || o.Script.Result.ExternalID != "crash-fixture-job" {
		t.Fatal("crash evidence lost", o)
	}
	if _, err = m.ValidateTarget("fixture", "test", "must not replay"); err == nil {
		t.Fatal("crash resumed external write")
	}
	req := scriptReconciliation{DeployID: 1, ArtifactID: o.ArtifactID, Digest: o.Script.ArtifactDigest, ConfigDigest: o.Script.ConfigDigest, Result: "failed", Comment: "isolated external fixture checked", ConfirmStopped: true}
	if m.reconcileScript(1, req, "fixture", false) == nil {
		t.Fatal("live process group unlocked")
	}
	killFixture()
	deadline := time.Now().Add(5 * time.Second)
	for scriptProcessGroupAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if err = m.reconcileScript(1, req, "fixture", false); err != nil {
		t.Fatal(err)
	}
	recorded := s.GetReleaseOrder(1)
	if recorded.Script == nil || recorded.Script.Result == nil || recorded.Script.Result.ExternalID != "crash-fixture-job" || recorded.Script.Result.Values["phase"] != "submitted" || recorded.Script.Result.ReconciledBy != "fixture" {
		t.Fatal("manual reconciliation lost recovered evidence", recorded)
	}
	b, _ := os.ReadFile(filepath.Join(root, "external-marker"))
	if string(b) != "x" {
		t.Fatal("crash repeated side effect", string(b))
	}
}
