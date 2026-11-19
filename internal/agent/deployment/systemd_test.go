//go:build linux

package deployment

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// Explicit opt-in only: creates temporary synthetic units, never an installed
// Agent or application unit. The controller has the real log Agent's 128 MiB cap.
func TestDeploymentSystemdCgroupAndCrashRecovery(t *testing.T) {
	if os.Getenv("DEVOPS_TEST_DEPLOYMENT_SYSTEMD") != "1" {
		t.Skip("requires explicitly enabled isolated Linux systemd fixture")
	}
	c, f, _ := setupDeployment(t)
	c.WorkerMode = "systemd"
	cfg := filepath.Join(filepath.Dir(c.StateDir), "controller.json")
	if e := atomicJSON(cfg, struct {
		Policy     Config
		Credential string
	}{c, c.Token}); e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(c.StateDir, f.task.ID)
	workerUnit := unitName(dir, c)
	controllerUnit := strings.TrimSuffix(workerUnit, ".service") + "-controller.service"
	systemd := func(args ...string) string {
		t.Helper()
		b, e := exec.Command("systemctl", args...).CombinedOutput()
		if e != nil {
			t.Fatalf("synthetic unit: %v %s", e, b)
		}
		return string(b)
	}
	t.Cleanup(func() {
		if t.Failed() {
			b, _ := exec.Command("journalctl", "-u", controllerUnit, "-u", workerUnit, "-n", "30", "--no-pager").CombinedOutput()
			t.Logf("isolated unit diagnostics: %s", b)
			all, _ := reports(dir)
			t.Logf("isolated receipt diagnostics: %+v", all)
		}
		exec.Command("systemctl", "stop", controllerUnit).Run()
		exec.Command("systemctl", "stop", workerUnit).Run()
		exec.Command("systemctl", "reset-failed", controllerUnit, workerUnit).Run()
	})
	b, e := exec.Command("systemd-run", "--unit="+controllerUnit, "--property=MemoryLimit=134217728", os.Args[0], "-deployment-manager-test", cfg).CombinedOutput()
	if e != nil {
		t.Fatalf("synthetic controller: %v %s", e, b)
	}
	waitFor(t, func() bool { _, e := os.Stat(filepath.Join(c.StateDir, f.task.ID, "worker.json")); return e == nil })
	waitFor(t, func() bool { return taskReport(c).State == "needs_review" })
	var w workerReceipt
	if e = readJSON(filepath.Join(dir, "worker.json"), &w); e != nil {
		t.Fatal(e)
	}
	pidBytes, e := os.ReadFile(filepath.Join(f.task.Root, "app.pid"))
	if e != nil {
		t.Fatal(e)
	}
	pid, e := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if e != nil {
		t.Fatal(e)
	}
	workerGroup, _ := os.ReadFile("/proc/" + strconv.Itoa(w.PID) + "/cgroup")
	appGroup, _ := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cgroup")
	if !strings.Contains(string(workerGroup), workerUnit) || !strings.Contains(string(appGroup), workerUnit) || strings.Contains(string(appGroup), controllerUnit) {
		t.Fatalf("application inherited controller cgroup: worker=%s app=%s", workerGroup, appGroup)
	}
	if strings.Contains(systemd("show", workerUnit, "--property=MemoryLimit"), "=134217728") {
		t.Fatal("application inherited log Agent memory cap")
	}
	systemd("stop", controllerUnit)
	appAlive(t, f.task.Root)
	if !alive(w, dir) {
		t.Fatal("stopping controller stopped deployment worker")
	}
	systemd("kill", "--kill-who=main", "--signal=SIGKILL", workerUnit)
	waitFor(t, func() bool { return !alive(w, dir) })
	appAlive(t, f.task.Root)
	m, e := Open(c)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	if e = m.Tick(context.Background()); e != nil {
		t.Fatal(e)
	}
	if taskReport(c).State != "needs_review" {
		t.Fatal("worker crash was not retained for observation")
	}
	if e = os.WriteFile(filepath.Join(f.task.Root, "ready"), []byte("ready"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = m.Tick(context.Background()); e != nil {
		t.Fatal(e)
	}
	if taskReport(c).State != "success" {
		t.Fatal("late readiness did not finish recovered task")
	}
	starts, e := os.ReadFile(filepath.Join(f.task.Root, "starts"))
	if e != nil || string(starts) != "start\n" {
		t.Fatalf("start replayed: %q %v", starts, e)
	}
	appAlive(t, f.task.Root)
	if syscall.Kill(pid, 0) != nil {
		t.Fatal("recovery stopped application")
	}
	t.Log("128 MiB controller -> separate worker/application cgroup; controller stop + worker SIGKILL preserved application; recovery verified success; exactly one start")
}
