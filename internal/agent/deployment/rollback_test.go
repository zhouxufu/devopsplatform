package deployment

import (
	"context"
	"logplatform/pkg/deploytask"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func setupRollbackTask(t *testing.T, block bool) (Config, *brokerFixture) {
	t.Helper()
	c, f, _ := setupDeployment(t)
	root := f.task.Root
	old := filepath.Join(root, "releases", "deploy-1")
	next := filepath.Join(root, "releases", "deploy-17")
	os.MkdirAll(old, 0700)
	os.WriteFile(filepath.Join(root, ".devopsplatform-owner"), []byte("api/qa"), 0600)
	os.WriteFile(filepath.Join(old, "service.sh"), []byte("frozen-old-launcher"), 0600)
	os.WriteFile(filepath.Join(old, "runtime.env"), []byte("MODE=old"), 0600)
	os.WriteFile(filepath.Join(old, "app.jar"), []byte("old-artifact"), 0600)
	os.Symlink(old, filepath.Join(root, "current"))
	f.task.Version = 2
	f.task.Steps[0].Script += "; mkdir -p " + quote(next) + "; ln -sfn " + quote(next) + " " + quote(filepath.Join(root, "current"))
	f.task.Steps[0].RollbackOnExit = true
	rollback := `root=` + quote(root) + `; previous=$(cat "$DEPLOY_TASK_DIR/previous"); test -n "$previous"; ln -sfn "$previous" "$root/current"; printf 'rollback\n' >> "$root/rollback-starts"; nohup sleep 60 >/dev/null 2>&1 </dev/null & echo $! > "$root/old.pid"`
	if block {
		rollback += `; while [ ! -f "$root/release-step" ]; do sleep .05; done`
	}
	probe := `root=` + quote(root) + `; previous=$(cat "$DEPLOY_TASK_DIR/previous"); test "$(readlink "$root/current")" = "$previous" || exit 30; [ -f "$root/old.pid" ] || exit 30; kill -0 "$(cat "$root/old.pid")" 2>/dev/null || exit 20; echo old-ready`
	f.task.Rollback = &deploytask.RollbackPlan{Guard: "test -f " + quote(filepath.Join(root, "dead")), Steps: []deploytask.Step{{Name: "synthetic rollback", Script: rollback, UncertainOnError: true}}, Probe: probe}
	t.Cleanup(func() {
		os.WriteFile(filepath.Join(root, "release-step"), nil, 0600)
		b, _ := os.ReadFile(filepath.Join(root, "old.pid"))
		pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
		if pid > 0 {
			syscall.Kill(pid, syscall.SIGTERM)
		}
	})
	return c, f
}
func TestDeploymentRollbackOnlyOnKnownExitAndDeduplicates(t *testing.T) {
	c, f := setupRollbackTask(t, false)
	m, e := Open(c)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { m.Close() }()
	m.Tick(context.Background())
	m.Tick(context.Background())
	waitFor(t, func() bool { return taskReport(c).State == "needs_review" })
	appAlive(t, f.task.Root)
	if _, e = os.Stat(filepath.Join(f.task.Root, "rollback-starts")); !os.IsNotExist(e) {
		t.Fatal("slow startup automatically rolled back")
	}
	pidBytes, _ := os.ReadFile(filepath.Join(f.task.Root, "app.pid"))
	pid, _ := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if e = syscall.Kill(pid, syscall.SIGTERM); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(f.task.Root, "dead"), nil, 0600)
	waitFor(t, func() bool { return taskReport(c).State == "rolled-back" })
	for i := 0; i < 3; i++ {
		m.Tick(context.Background())
	}
	b, _ := os.ReadFile(filepath.Join(f.task.Root, "rollback-starts"))
	if string(b) != "rollback\n" {
		t.Fatalf("rollback replayed: %q", b)
	}
	if e = m.Close(); e != nil {
		t.Fatal(e)
	}
	m, e = Open(c)
	if e != nil {
		t.Fatal(e)
	}
	m.Tick(context.Background())
	b, _ = os.ReadFile(filepath.Join(f.task.Root, "rollback-starts"))
	if string(b) != "rollback\n" {
		t.Fatal("terminal rollback replayed after Agent reopen")
	}
}
func TestDeploymentRollbackWorkerCrashObservesWithoutReplay(t *testing.T) {
	c, f := setupRollbackTask(t, true)
	m, e := Open(c)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	m.Tick(context.Background())
	m.Tick(context.Background())
	waitFor(t, func() bool { return taskReport(c).State == "needs_review" })
	b, _ := os.ReadFile(filepath.Join(f.task.Root, "app.pid"))
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	syscall.Kill(pid, syscall.SIGTERM)
	os.WriteFile(filepath.Join(f.task.Root, "dead"), nil, 0600)
	waitFor(t, func() bool { _, e := os.Stat(filepath.Join(f.task.Root, "old.pid")); return e == nil })
	dir := filepath.Join(c.StateDir, f.task.ID)
	var w workerReceipt
	if e = readJSON(filepath.Join(dir, "worker.json"), &w); e != nil || !alive(w, dir) {
		t.Fatal("synthetic worker missing", e)
	}
	syscall.Kill(w.PID, syscall.SIGKILL)
	waitFor(t, func() bool { m.Tick(context.Background()); return taskReport(c).State == "rolled-back" })
	b, _ = os.ReadFile(filepath.Join(f.task.Root, "rollback-starts"))
	if string(b) != "rollback\n" {
		t.Fatalf("rollback was replayed after worker crash: %q", b)
	}
}
func TestDeploymentRollbackRejectsChangedPreviousVersion(t *testing.T) {
	c, f := setupRollbackTask(t, false)
	m, e := Open(c)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	m.Tick(context.Background())
	m.Tick(context.Background())
	waitFor(t, func() bool { return taskReport(c).State == "needs_review" })
	os.WriteFile(filepath.Join(f.task.Root, "releases", "deploy-1", "runtime.env"), []byte("tampered"), 0600)
	b, _ := os.ReadFile(filepath.Join(f.task.Root, "app.pid"))
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	syscall.Kill(pid, syscall.SIGTERM)
	os.WriteFile(filepath.Join(f.task.Root, "dead"), nil, 0600)
	waitFor(t, func() bool { return taskReport(c).Phase == "rollback-guard" })
	if _, e = os.Stat(filepath.Join(f.task.Root, "rollback-starts")); !os.IsNotExist(e) {
		t.Fatal("changed previous configuration was started")
	}
}

func TestDeploymentRollbackLogsRedactPreviousCredentials(t *testing.T) {
	c, f := setupRollbackTask(t, false)
	os.WriteFile(filepath.Join(f.task.Root, "releases", "deploy-1", "runtime.env"), []byte("PASSWORD='private'\\''old'\\''value'\n"), 0600)
	f.task.Rollback.Steps[0].Script += `; previous=$(cat "$DEPLOY_TASK_DIR/previous"); set -a; source "$previous/runtime.env"; set +a; printf '%s\n' "$PASSWORD"; exit 30`
	m, e := Open(c)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	m.Tick(context.Background())
	m.Tick(context.Background())
	waitFor(t, func() bool { return taskReport(c).State == "needs_review" })
	b, _ := os.ReadFile(filepath.Join(f.task.Root, "app.pid"))
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	syscall.Kill(pid, syscall.SIGTERM)
	os.WriteFile(filepath.Join(f.task.Root, "dead"), nil, 0600)
	waitFor(t, func() bool { return taskReport(c).Phase == "rollback-step-0" && taskReport(c).State == "needs_review" })
	all, e := reports(filepath.Join(c.StateDir, f.task.ID))
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range all {
		if strings.Contains(r.Message, "private'old'value") {
			t.Fatal("old credential leaked in rollback report")
		}
	}
}
