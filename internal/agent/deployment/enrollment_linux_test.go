//go:build linux

package deployment

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func init() {
	if len(os.Args) == 3 && os.Args[1] == "-deployment-enrollment-probe-test" {
		if e := RunWorkerCheck(os.Args[2]); e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(2)
		}
		os.Exit(0)
	}
}

func TestDeploymentEnrollmentIndependentWorkerProof(t *testing.T) {
	if os.Getenv("DEVOPS_TEST_ENROLLMENT_SYSTEMD") != "1" {
		t.Skip("explicit isolated Linux systemd fixture only")
	}
	parent := t.TempDir()
	os.Chmod(parent, 0755)
	os.Chmod(filepath.Dir(parent), 0755)
	c := Config{StateDir: filepath.Join(parent, "state"), WorkerMode: "systemd"}
	dir := filepath.Join(c.StateDir, ".checks", "11223344556677881122334455667788")
	if e := os.MkdirAll(dir, 0755); e != nil {
		t.Fatal(e)
	}
	// Use an existing unprivileged account: systemd 219 requires a passwd
	// entry even when a numerical UID is specified. No account is created.
	account, e := user.Lookup("nobody")
	if e != nil {
		t.Fatal(e)
	}
	uid, e := strconv.Atoi(account.Uid)
	if e != nil || uid == 0 {
		t.Fatal("unprivileged test account missing")
	}
	gid, e := strconv.Atoi(account.Gid)
	if e != nil {
		t.Fatal(e)
	}
	// Exercise a non-root worker with no changes to an installed user/service.
	os.Chown(dir, uid, gid)
	if e := atomicJSON(filepath.Join(dir, "check.json"), workerCheck{c, filepath.Base(dir), uid}); e != nil {
		t.Fatal(e)
	}
	os.Chown(filepath.Join(dir, "check.json"), uid, gid)
	unit := unitName(dir, c)
	t.Cleanup(func() {
		exec.Command("systemctl", "stop", unit).Run()
		exec.Command("systemctl", "reset-failed", unit).Run()
	})
	b, e := exec.Command("systemd-run", "--service-type=simple", "--unit="+unit, "--property=KillMode=process", "--uid="+account.Uid, "--gid="+account.Gid, os.Args[0], "-deployment-enrollment-probe-test", dir).CombinedOutput()
	if e != nil {
		t.Fatalf("isolated worker %v %s", e, b)
	}
	end := time.Now().Add(10 * time.Second)
	for time.Now().Before(end) {
		var r workerCheckReceipt
		if readJSON(filepath.Join(dir, "receipt.json"), &r) == nil {
			if r.UID != uid || !r.Independent || r.Nonce != filepath.Base(dir) {
				t.Fatal(r)
			}
			t.Log("non-root worker proved unchanged UID and independent systemd cgroup; no app commands executed")
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("isolated systemd proof not received")
}
