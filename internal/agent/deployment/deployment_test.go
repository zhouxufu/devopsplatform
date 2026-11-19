package deployment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"logplatform/pkg/deploytask"
)

// Exercise the actual Agent worker process, not a mock goroutine. A killed
// controller must not terminate this process or replay an application's start.
func init() {
	if len(os.Args) == 3 && os.Args[1] == "-deployment-worker" {
		if e := RunWorker(os.Args[2]); e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(2)
		}
		os.Exit(0)
	}
	if len(os.Args) == 3 && os.Args[1] == "-deployment-manager-test" {
		var v struct {
			Policy     Config
			Credential string
		}
		if readJSON(os.Args[2], &v) != nil {
			os.Exit(2)
		}
		v.Policy.Token = v.Credential
		m, e := Open(v.Policy)
		if e != nil {
			os.Exit(2)
		}
		defer m.Close()
		for {
			_ = m.Tick(context.Background())
			time.Sleep(50 * time.Millisecond)
		}
	}
}

type brokerFixture struct {
	mu               sync.Mutex
	task             deploytask.Task
	offline, dropAck bool
	receipts         map[int64]string
	duplicates       int
	token            string
}

func (f *brokerFixture) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+f.token || r.Header.Get("X-Deployment-Agent-ID") != f.task.AgentID {
		http.Error(w, "auth", 401)
		return
	}
	if f.offline {
		http.Error(w, "offline", 503)
		return
	}
	switch {
	case r.URL.Path == "/tasks":
		json.NewEncoder(w).Encode([]deploytask.Task{f.task})
	case strings.HasSuffix(r.URL.Path, "/artifact"):
		w.Write([]byte("frozen artifact"))
	case r.URL.Path == "/reports":
		var v deploytask.Report
		if json.NewDecoder(r.Body).Decode(&v) != nil {
			http.Error(w, "bad", 400)
			return
		}
		digest := deploytask.Digest(v)
		if old, ok := f.receipts[v.Sequence]; ok {
			if old != digest {
				http.Error(w, "conflict", 409)
				return
			}
			f.duplicates++
		} else {
			f.receipts[v.Sequence] = digest
		}
		if f.dropAck {
			f.dropAck = false
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		json.NewEncoder(w).Encode(deploytask.Ack{TaskID: v.TaskID, Sequence: v.Sequence})
	default:
		http.NotFound(w, r)
	}
}
func setupDeployment(t *testing.T) (Config, *brokerFixture, *httptest.Server) {
	t.Helper()
	base := t.TempDir()
	allowed := filepath.Join(base, "apps")
	if e := os.Mkdir(allowed, 0700); e != nil {
		t.Fatal(e)
	}
	root := filepath.Join(allowed, "api")
	artifact := []byte("frozen artifact")
	f := &brokerFixture{receipts: map[int64]string{}, token: "independent-deployment-key"}
	// The synthetic application only sleeps. Its PID is the positive evidence
	// that expiry/disconnect/controller crashes never stopped it.
	start := `printf 'start\n' >> ` + quote(filepath.Join(root, "starts")) + `; nohup sleep 60 >/dev/null 2>&1 </dev/null & echo $! > ` + quote(filepath.Join(root, "app.pid")) + `; test -z "${UNRELATED_AGENT_SECRET-}"`
	probe := `root=` + quote(root) + `; if [ -f "$root/ready" ]; then echo verified; exit 0; fi; if [ -f "$root/dead" ]; then exit 20; fi; if [ -f "$root/app.pid" ]; then kill -0 "$(cat "$root/app.pid")" 2>/dev/null || exit 20; exit 10; fi; exit 30`
	f.task = deploytask.Task{Version: 1, ID: "deploy-17", DeployID: 17, AgentID: "agent_fixture", Host: "isolated", Environment: "qa", Service: "api", Root: root, ArtifactDigest: byteDigest(artifact), ArtifactSize: int64(len(artifact)), Steps: []deploytask.Step{{Name: "synthetic application", Script: start, UncertainOnError: true}}, Probe: probe, WarnAfterSeconds: 1}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	c := Config{Enabled: true, URL: srv.URL, Token: f.token, StateDir: filepath.Join(base, "tasks"), AllowedRoots: []string{allowed}, WorkerMode: "isolated-process", AgentID: f.task.AgentID, Host: f.task.Host, Environment: f.task.Environment}
	t.Cleanup(func() {
		var w workerReceipt
		_ = readJSON(filepath.Join(c.StateDir, f.task.ID, "worker.json"), &w)
		if alive(w, filepath.Join(c.StateDir, f.task.ID)) {
			_ = syscall.Kill(w.PID, syscall.SIGKILL)
		}
		if b, e := os.ReadFile(filepath.Join(root, "app.pid")); e == nil {
			pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
			if pid > 0 {
				_ = syscall.Kill(pid, syscall.SIGTERM)
			}
		}
	})
	return c, f, srv
}
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
func byteDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func waitFor(t *testing.T, f func() bool) {
	t.Helper()
	end := time.Now().Add(12 * time.Second)
	for time.Now().Before(end) {
		if f() {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("isolated deployment condition not reached")
}
func taskReport(c Config) deploytask.Report {
	r, _ := latest(filepath.Join(c.StateDir, "deploy-17"))
	return r
}
func appAlive(t *testing.T, root string) {
	t.Helper()
	b, e := os.ReadFile(filepath.Join(root, "app.pid"))
	if e != nil {
		t.Fatal(e)
	}
	pid, e := strconv.Atoi(strings.TrimSpace(string(b)))
	if e != nil || syscall.Kill(pid, 0) != nil {
		t.Fatal("synthetic application was stopped")
	}
}

func TestDeploymentOfflineOutboxLostAckAndDuplicateTask(t *testing.T) {
	c, f, _ := setupDeployment(t)
	t.Setenv("UNRELATED_AGENT_SECRET", "must-not-reach-child")
	m, e := Open(c)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	if e = m.Tick(context.Background()); e != nil {
		t.Fatal(e)
	}
	_ = m.Tick(context.Background())
	waitFor(t, func() bool { return taskReport(c).State == "needs_review" })
	appAlive(t, f.task.Root)
	f.mu.Lock()
	f.offline = true
	f.mu.Unlock()
	if e = m.Tick(context.Background()); e == nil {
		t.Fatal("disconnect was not reported")
	}
	if e = os.WriteFile(filepath.Join(f.task.Root, "ready"), nil, 0600); e != nil {
		t.Fatal(e)
	}
	waitFor(t, func() bool { return taskReport(c).State == "success" })
	appAlive(t, f.task.Root)
	if e = m.Tick(context.Background()); e == nil {
		t.Fatal("outbox unexpectedly ACKed while disconnected")
	}
	f.mu.Lock()
	f.offline = false
	f.dropAck = true
	f.mu.Unlock()
	_ = m.Tick(context.Background())
	_ = m.Tick(context.Background())
	_ = m.Tick(context.Background())
	var ack deploytask.Ack
	if e = readJSON(filepath.Join(c.StateDir, f.task.ID, "ack.json"), &ack); e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	duplicates, n := f.duplicates, len(f.receipts)
	f.mu.Unlock()
	if duplicates == 0 || ack.Sequence != taskReport(c).Sequence || n != int(ack.Sequence) {
		t.Fatalf("lost receipt/ACK: duplicates=%d receipts=%d ack=%+v", duplicates, n, ack)
	}
	b, _ := os.ReadFile(filepath.Join(f.task.Root, "starts"))
	if string(b) != "start\n" {
		t.Fatalf("replayed task: %q", b)
	}
	jobBytes, _ := os.ReadFile(filepath.Join(c.StateDir, f.task.ID, "job.json"))
	if strings.Contains(string(jobBytes), c.Token) {
		t.Fatal("network credential leaked into worker plan")
	}
}

func TestDeploymentControllerAndWorkerCrashRecovery(t *testing.T) {
	c, f, _ := setupDeployment(t)
	cfg := filepath.Join(t.TempDir(), "manager.json")
	if e := atomicJSON(cfg, struct {
		Policy     Config
		Credential string
	}{c, c.Token}); e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(os.Args[0], "-deployment-manager-test", cfg)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	if e := cmd.Start(); e != nil {
		t.Fatal(e)
	}
	waitFor(t, func() bool { return taskReport(c).State == "starting" })
	appAlive(t, f.task.Root)
	if e := cmd.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	_ = cmd.Wait()
	m, e := Open(c)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	_ = m.Tick(context.Background())
	appAlive(t, f.task.Root)
	var w workerReceipt
	if e = readJSON(filepath.Join(c.StateDir, f.task.ID, "worker.json"), &w); e != nil {
		t.Fatal(e)
	}
	if e = syscall.Kill(w.PID, syscall.SIGKILL); e != nil {
		t.Fatal(e)
	}
	waitFor(t, func() bool { _ = m.Tick(context.Background()); return taskReport(c).State == "needs_review" })
	appAlive(t, f.task.Root)
	if e = os.WriteFile(filepath.Join(f.task.Root, "ready"), nil, 0600); e != nil {
		t.Fatal(e)
	}
	waitFor(t, func() bool { _ = m.Tick(context.Background()); return taskReport(c).State == "success" })
	b, _ := os.ReadFile(filepath.Join(f.task.Root, "starts"))
	if string(b) != "start\n" {
		t.Fatal("crash recovery replayed an application start", string(b))
	}
}

func TestDeploymentDispatchGapAndPlanConflictNeverReplay(t *testing.T) {
	c, f, _ := setupDeployment(t)
	m, e := Open(c)
	if e != nil {
		t.Fatal(e)
	}
	m.launch = func(string, Config) error { return fmt.Errorf("simulated crash before worker receipt") }
	if e = m.accept(f.task); e != nil {
		t.Fatal(e)
	}
	_ = m.Tick(context.Background())
	if taskReport(c).State != "needs_review" {
		t.Fatal(taskReport(c))
	}
	m.Close()
	m, e = Open(c)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	m.launch = func(string, Config) error { t.Fatal("uncertain dispatch replayed"); return nil }
	_ = m.Tick(context.Background())
	if _, e = os.Stat(filepath.Join(f.task.Root, "starts")); !os.IsNotExist(e) {
		t.Fatal("uncertain task executed")
	}
	changed := f.task
	changed.Probe = "exit 0"
	if m.accept(changed) == nil {
		t.Fatal("accepted changed plan for same task")
	}
	if e = atomicJSON(filepath.Join(c.StateDir, f.task.ID, "job.json"), job{Task: changed, Digest: deploytask.Digest(f.task), Policy: c}); e != nil {
		t.Fatal(e)
	}
	if m.Tick(context.Background()) == nil {
		t.Fatal("corrupted task was not quarantined")
	}
}

func TestDeploymentScopeAndPathPolicy(t *testing.T) {
	c, f, _ := setupDeployment(t)
	m, e := Open(c)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	for _, kind := range []string{"host", "environment", "agent", "root", "symlink"} {
		task := f.task
		switch kind {
		case "host":
			task.Host = "other"
		case "environment":
			task.Environment = "prod"
		case "agent":
			task.AgentID = "another"
		case "root":
			task.Root = t.TempDir()
		case "symlink":
			link := filepath.Join(c.AllowedRoots[0], "link")
			if e = os.Symlink(t.TempDir(), link); e != nil {
				t.Fatal(e)
			}
			task.Root = filepath.Join(link, "app")
		}
		if m.accept(task) == nil {
			t.Fatal("accepted outside policy", kind)
		}
	}
}
