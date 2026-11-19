//go:build linux

package deploy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"logplatform/internal/agent/deployment"
	"logplatform/pkg/deploytask"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func init() {
	if len(os.Args) == 3 && os.Args[1] == "-deployment-worker" {
		if e := deployment.RunWorker(os.Args[2]); e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(2)
		}
		os.Exit(0)
	}
}

// Only a newly compiled tiny fixture JAR is allowed. No configured business
// machine, source repository, installed Agent or running application is used.
func TestDeploymentAgentGeneratedJavaPlanEndToEnd(t *testing.T) {
	if os.Getenv("DEVOPS_TEST_AGENT_JAVA") != "1" {
		t.Skip("opt-in synthetic Linux JDK8/systemd fixture")
	}
	base := t.TempDir()
	source := filepath.Join(base, "Fixture.java")
	java := `import com.sun.net.httpserver.HttpServer;import java.net.InetSocketAddress;import java.nio.file.*;
public class Fixture{public static void main(String[] a)throws Exception{Files.write(Paths.get(a[1]),"start\n".getBytes(),StandardOpenOption.CREATE,StandardOpenOption.APPEND);while(!Files.exists(Paths.get(a[0]))){Thread.sleep(50);}HttpServer s=HttpServer.create(new InetSocketAddress("127.0.0.1",Integer.parseInt(a[2])),0);s.createContext("/health",e->{byte[] b="ready".getBytes();e.sendResponseHeaders(200,b.length);e.getResponseBody().write(b);e.close();});s.start();}}`
	if e := os.WriteFile(source, []byte(java), 0600); e != nil {
		t.Fatal(e)
	}
	for _, args := range [][]string{{"javac", source}, {"jar", "cfe", filepath.Join(base, "fixture.jar"), "Fixture", "-C", base, "Fixture.class"}} {
		b, e := exec.Command(args[0], args[1:]...).CombinedOutput()
		if e != nil {
			t.Fatalf("synthetic fixture compile: %v %s", e, b)
		}
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	s, m, _, token, _ := deploymentBrokerFixture(t)
	target := s.GetServiceEnvironment("api", "qa")
	root := filepath.Join(base, "apps", "api")
	ready, starts := filepath.Join(base, "ready"), filepath.Join(base, "starts")
	target.RemoteDir = root
	target.HealthURL = fmt.Sprintf("http://127.0.0.1:%d/health", port)
	target.DeployConfig["java_port"] = strconv.Itoa(port)
	target.DeployConfig["java_jvm_options"] = "-Xmx32m"
	target.DeployConfig["java_args"] = ready + "\n" + starts + "\n" + strconv.Itoa(port)
	if e = s.UpsertServiceEnvironment(*target); e != nil {
		t.Fatal(e)
	}
	a, e := describeArtifact("api", "synthetic-java", ArtifactJar, filepath.Join(base, "fixture.jar"), "")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.RegisterArtifact(&a); e != nil {
		t.Fatal(e)
	}
	o, e := m.CreateArtifact("api", a.ID, "qa", "synthetic JDK8 fixture", "tester")
	if e != nil {
		t.Fatal(e)
	}
	var task deploytask.Task
	awaitBroker(t, func() bool {
		tasks, e := s.pendingAgentTasks("agent_fixture")
		if e == nil && len(tasks) == 1 {
			task = tasks[0]
			return true
		}
		return false
	})
	// Shorten only this disposable task's warning window; scripts and artifact
	// identity remain exactly the generated managed-JAR plan.
	task.WarnAfterSeconds = 1
	raw, _ := jsonMarshalTask(task)
	cipher, e := s.encryptSecret(raw)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec(`UPDATE agent_deployment_tasks SET plan_cipher=?,plan_digest=? WHERE task_id=?`, cipher, deploytask.Digest(task), task.ID); e != nil {
		t.Fatal(e)
	}
	mux := http.NewServeMux()
	registerDeploymentAgentRoutes(mux, m)
	broker := httptest.NewServer(mux)
	defer broker.Close()
	policy := deployment.Config{Enabled: true, URL: broker.URL + "/api/deployment-agent", Token: token, StateDir: filepath.Join(base, "tasks"), AllowedRoots: []string{filepath.Join(base, "apps")}, AgentID: "agent_fixture", Host: "fixture", Environment: "qa"}
	mgr, e := deployment.Open(policy)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { mgr.Close() }()
	release := filepath.Join(root, "releases", fmt.Sprintf("deploy-%d", task.DeployID))
	t.Cleanup(func() {
		// The fixture launcher checks exact JAR ownership before stopping its JVM.
		exec.Command("bash", filepath.Join(release, "service.sh"), "stop").Run()
		sum := sha256.Sum256([]byte(policy.StateDir))
		unit := "devops-deploy-" + hex.EncodeToString(sum[:])[:12] + "-" + task.ID + ".service"
		exec.Command("systemctl", "stop", unit).Run()
		exec.Command("systemctl", "reset-failed", unit).Run()
	})
	wait := func(f func() bool) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if e := mgr.Tick(context.Background()); e != nil {
				t.Log(e)
			}
			if f() {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("synthetic Java did not reach expected state; deploy=%+v", s.GetDeploy(task.DeployID))
	}
	wait(func() bool { return s.GetDeploy(task.DeployID).Status == "needs_review" })
	b, e := os.ReadFile(filepath.Join(root, ".devopsplatform.pid"))
	if e != nil {
		t.Fatal(e)
	}
	pid, e := strconv.Atoi(strings.TrimSpace(string(b)))
	if e != nil {
		t.Fatal(e)
	}
	cmdline, e := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if e != nil || !strings.Contains(string(cmdline), release+"/app.jar") {
		t.Fatal("JVM was stopped or process identity changed")
	}
	if e = mgr.Close(); e != nil {
		t.Fatal(e)
	}
	mgr, e = deployment.Open(policy)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(ready, []byte("ready"), 0600); e != nil {
		t.Fatal(e)
	}
	wait(func() bool { return s.GetReleaseOrder(o.ID).Status == "success" })
	b, e = os.ReadFile(starts)
	if e != nil || string(b) != "start\n" {
		t.Fatalf("JVM start replayed: %q %v", b, e)
	}
	cmdline, e = os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if e != nil || !strings.Contains(string(cmdline), release+"/app.jar") {
		t.Fatal("late readiness stopped or replaced original JVM")
	}
	t.Log("generated managed Java plan: real JDK8 fixture remained running past warning; Agent reopen + verified late health completed original release; JVM launched once")
}
func jsonMarshalTask(t deploytask.Task) (string, error) { b, e := json.Marshal(t); return string(b), e }
