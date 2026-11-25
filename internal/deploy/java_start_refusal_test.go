package deploy

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManagedJavaPortRefusalNeverStartsAndCanBeRechecked(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	root := t.TempDir()
	release := filepath.Join(root, "releases/deploy-7")
	if err := os.MkdirAll(release, 0700); err != nil {
		t.Fatal(err)
	}
	sv := managedJavaFixture()
	sv.RemoteDir = root
	sv.DeployConfig["java_port"] = fmt.Sprint(listener.Addr().(*net.TCPAddr).Port)
	file := filepath.Join(release, "service.sh")
	if err := os.WriteFile(file, []byte(managedJavaScript(sv, release)), 0700); err != nil {
		t.Fatal(err)
	}
	run := func(action string, want int) string {
		t.Helper()
		out, e := exec.Command("bash", file, action).CombinedOutput()
		exit, ok := e.(*exec.ExitError)
		if !ok || exit.ExitCode() != want {
			t.Fatalf("%s: want %d, got %v: %s", action, want, e, out)
		}
		return string(out)
	}
	run("prestart", 21)
	if _, e := os.Stat(filepath.Join(release, ".start-state")); !os.IsNotExist(e) {
		t.Fatal("read-only preflight wrote a start receipt")
	}
	run("start", 21)
	if _, e := os.Stat(filepath.Join(root, ".devopsplatform.pid")); !os.IsNotExist(e) {
		t.Fatal("rejected start produced a PID")
	}
	state, e := os.ReadFile(filepath.Join(release, ".start-state"))
	if e != nil || string(state) != "rejected-port\n" {
		t.Fatalf("lost rejection receipt: %q %v", state, e)
	}
	if !strings.Contains(run("agent-probe", 21), "no new process was launched") {
		t.Fatal("lost explicit refusal evidence")
	}
	if e := os.WriteFile(filepath.Join(root, ".devopsplatform.pid"), []byte("123\n"), 0600); e != nil {
		t.Fatal(e)
	}
	run("agent-probe", 31)
	// The occupied listener still accepts connections; the launcher did not stop it.
	c, e := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if e != nil {
		t.Fatal(e)
	}
	c.Close()
}

func TestManagedJavaRefusalEndsWithoutStopRollbackOrRetainedLock(t *testing.T) {
	for _, phase := range []string{"prestart", "start"} {
		for _, code := range []int{2, 21, 75} {
			t.Run(fmt.Sprintf("%s-%d", phase, code), func(t *testing.T) {
				sv := managedJavaFixture()
				d := NewDeployer(nil, NewLogHub(), "")
				var commands []string
				remote := javaRemote{exec: func(_ Host, cmd string, _ time.Duration) (string, error) {
					commands = append(commands, cmd)
					if strings.Contains(cmd, "then readlink") {
						return "", nil
					}
					if strings.HasSuffix(cmd, " "+phase) {
						return "synthetic refusal", javaTestExit(code)
					}
					return "", nil
				}, upload: func(Host, string, string) error { return nil }, write: func(Host, []byte, string) error { return nil }}
				got := d.deployManagedJavaRemote(7, sv, Host{}, "fixture.jar", remote)
				want := "failed"
				if phase == "start" && code != 21 {
					want = "needs_review"
				}
				if got != want {
					t.Fatalf("want %s got %s", want, got)
				}
				unlocked := false
				for _, cmd := range commands {
					if strings.Contains(cmd, "requested_owner='deploy-7'") && strings.Contains(cmd, "refusing to remove lock owned") {
						unlocked = true
					}
					if strings.HasSuffix(cmd, " stop") {
						t.Fatal("refused launch stopped a process")
					}
					if phase == "prestart" && strings.HasSuffix(cmd, " start") {
						t.Fatal("failed preflight dispatched start")
					}
				}
				if unlocked != (want == "failed") {
					t.Fatal("terminal/unknown deployment lock protection changed")
				}
			})
		}
	}
}
