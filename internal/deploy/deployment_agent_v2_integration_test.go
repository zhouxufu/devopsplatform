//go:build linux

package deploy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"logplatform/internal/agent/deployment"
	"logplatform/pkg/deploytask"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type containerAgentPolicy struct {
	Policy               deployment.Config
	Credential, Upstream string
}

func init() {
	if len(os.Args) != 3 || os.Args[1] != "-deployment-fixture-manager" {
		return
	}
	debug.SetMemoryLimit(50 << 20)
	var cfg containerAgentPolicy
	b, e := os.ReadFile(os.Args[2])
	if e != nil || json.Unmarshal(b, &cfg) != nil {
		os.Exit(2)
	}
	upstream, e := url.Parse(cfg.Upstream)
	if e != nil {
		os.Exit(2)
	}
	proxy := httptest.NewServer(httputil.NewSingleHostReverseProxy(upstream))
	defer proxy.Close()
	cfg.Policy.URL = proxy.URL + "/api/deployment-agent"
	cfg.Policy.Token = cfg.Credential
	manager, e := deployment.Open(cfg.Policy)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(2)
	}
	defer manager.Close()
	os.WriteFile("/qa/manager.pid", []byte(strconv.Itoa(os.Getpid())), 0600)
	log, _ := os.OpenFile("/qa/manager.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if log != nil {
		defer log.Close()
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	manager.Run(ctx, func(e error) {
		if log != nil {
			fmt.Fprintln(log, e)
		}
	})
	os.Exit(0)
}
func compileAgentJavaFixture(t *testing.T, base string) string {
	t.Helper()
	source := filepath.Join(base, "Fixture.java")
	body := `import com.sun.net.httpserver.HttpServer;import java.net.InetSocketAddress;import java.nio.file.*;
public class Fixture{public static void main(String[] a)throws Exception{String mark=System.getenv("MODE");if(mark==null)mark="normal";Files.write(Paths.get(a[1]),(mark+"\n").getBytes(),StandardOpenOption.CREATE,StandardOpenOption.APPEND);if(mark.equals("bad"))System.exit(17);while(!Files.exists(Paths.get(a[0]))){Thread.sleep(50);}final String value=mark;HttpServer s=HttpServer.create(new InetSocketAddress("127.0.0.1",Integer.parseInt(a[2])),0);s.createContext("/health",e->{byte[] b=value.getBytes();e.sendResponseHeaders(200,b.length);e.getResponseBody().write(b);e.close();});s.start();}}`
	if e := os.WriteFile(source, []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
	jar := filepath.Join(base, "fixture.jar")
	for _, args := range [][]string{{"javac", source}, {"jar", "cfe", jar, "Fixture", "-C", base, "Fixture.class"}} {
		if b, e := exec.Command(args[0], args[1:]...).CombinedOutput(); e != nil {
			t.Fatalf("synthetic Java compile: %v %s", e, b)
		}
	}
	return jar
}
func fixtureCopy(t *testing.T, src, dest string) {
	t.Helper()
	in, e := os.Open(src)
	if e != nil {
		t.Fatal(e)
	}
	defer in.Close()
	out, e := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if e != nil {
		t.Fatal(e)
	}
	_, e = io.Copy(out, in)
	out.Close()
	if e != nil {
		t.Fatal(e)
	}
}
func realAgentArtifact(t *testing.T, s *Store, jar, kind, version string) *Artifact {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "app.jar")
	fixtureCopy(t, jar, file)
	if kind == ArtifactJavaBundle {
		root := filepath.Join(dir, "build", "jpom-release")
		for _, d := range []string{"lib", "bin", "tools/changchun-traffic/scripts", "tools/changchun-traffic/data", "tools/changchun-traffic/config"} {
			os.MkdirAll(filepath.Join(root, d), 0755)
		}
		fixtureCopy(t, jar, filepath.Join(root, "lib", "estate-gis-service.jar"))
		for name, body := range map[string]string{"bin/estate-gis-service.sh": "#!/bin/sh\necho BUNDLED_SCRIPT_MUST_NOT_RUN\nexit 99\n", "tools/changchun-traffic/scripts/replay.sh": "#!/bin/sh\necho fixture-only\n", "tools/changchun-traffic/data/model.csv": "GIS-DATA-" + version, "tools/changchun-traffic/config/default.json": "{\"fixture\":true}"} {
			if e := os.WriteFile(filepath.Join(root, name), []byte(body), 0755); e != nil {
				t.Fatal(e)
			}
		}
		var e error
		file, e = packageJavaBundle(context.Background(), dir, t.TempDir(), "api", map[string]string{"ROOT": "build/jpom-release", "MAIN_JAR": "lib/estate-gis-service.jar", "REQUIRED_PATHS": "bin/estate-gis-service.sh\ntools/changchun-traffic/data/model.csv"}, time.Now().Add(-time.Second))
		if e != nil {
			t.Fatal(e)
		}
	}
	a, e := describeArtifact("api", version, kind, file, "")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.RegisterArtifact(&a); e != nil {
		t.Fatal(e)
	}
	return &a
}
func tickAgentUntil(t *testing.T, m *deployment.Manager, condition func() bool, diagnostic func() any) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if e := m.Tick(context.Background()); e != nil {
			t.Log(e)
		}
		if condition() {
			return
		}
		time.Sleep(60 * time.Millisecond)
	}
	t.Fatalf("isolated Agent condition not reached: %+v", diagnostic())
}
func TestDeploymentAgentRealJarAndGISBundleRollback(t *testing.T) {
	if os.Getenv("DEVOPS_TEST_AGENT_JAVA") != "1" {
		t.Skip("opt-in private Linux JDK8/systemd fixture")
	}
	compiled := compileAgentJavaFixture(t, t.TempDir())
	for _, kind := range []string{ArtifactJar, ArtifactJavaBundle} {
		t.Run(kind, func(t *testing.T) {
			s, m, _, token, _ := deploymentBrokerFixture(t)
			base := t.TempDir()
			root := filepath.Join(base, "apps", "api")
			ready, starts := filepath.Join(base, "ready"), filepath.Join(base, "starts")
			os.WriteFile(ready, nil, 0600)
			listener, e := net.Listen("tcp", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			port := listener.Addr().(*net.TCPAddr).Port
			listener.Close()
			if kind == ArtifactJavaBundle {
				sv := *s.GetService("api")
				sv.ArtifactType = ArtifactJavaBundle
				if e = s.UpsertService(sv); e != nil {
					t.Fatal(e)
				}
			}
			target := s.GetServiceEnvironment("api", "qa")
			target.RemoteDir = root
			target.HealthURL = fmt.Sprintf("http://127.0.0.1:%d/health", port)
			target.DeployConfig["java_port"] = strconv.Itoa(port)
			target.DeployConfig["java_jvm_options"] = "-Xmx32m"
			target.DeployConfig["java_args"] = ready + "\n" + starts + "\n" + strconv.Itoa(port)
			target.DeployConfig["health_failure_action"] = "rollback"
			target.RuntimeEnv = map[string]string{"MODE": "old"}
			if kind == ArtifactJavaBundle {
				target.TargetType = TargetJavaBundle
				target.DeployConfig["java_bundle_jar"] = "lib/estate-gis-service.jar"
			}
			if e = s.UpsertServiceEnvironment(*target); e != nil {
				t.Fatal(e)
			}
			mux := http.NewServeMux()
			registerDeploymentAgentRoutes(mux, m)
			broker := httptest.NewServer(mux)
			defer broker.Close()
			policy := deployment.Config{Enabled: true, URL: broker.URL + "/api/deployment-agent", Token: token, StateDir: filepath.Join(base, "tasks"), AllowedRoots: []string{filepath.Join(base, "apps")}, AgentID: "agent_fixture", Host: "fixture", Environment: "qa"}
			manager, e := deployment.Open(policy)
			if e != nil {
				t.Fatal(e)
			}
			defer manager.Close()
			t.Cleanup(func() {
				current, e := os.Readlink(filepath.Join(root, "current"))
				if e == nil && filepath.Dir(current) == filepath.Join(root, "releases") {
					exec.Command("bash", filepath.Join(current, "service.sh"), "stop").Run()
				}
				sum := sha256.Sum256([]byte(policy.StateDir))
				for _, task := range allAgentTasks(t, s) {
					unit := "devops-deploy-" + hex.EncodeToString(sum[:])[:12] + "-" + task.ID + ".service"
					exec.Command("systemctl", "stop", unit).Run()
					exec.Command("systemctl", "reset-failed", unit).Run()
				}
			})
			old := realAgentArtifact(t, s, compiled, kind, "old")
			first, e := m.CreateArtifact("api", old.ID, "qa", "synthetic old version", "tester")
			if e != nil {
				t.Fatal(e)
			}
			tickAgentUntil(t, manager, func() bool { return s.GetReleaseOrder(first.ID).Status == "success" }, func() any { return s.GetReleaseOrder(first.ID) })
			previous, e := os.Readlink(filepath.Join(root, "current"))
			if e != nil {
				t.Fatal(e)
			}
			oldEnv := mustRead(t, filepath.Join(previous, "runtime.env"))
			target.RuntimeEnv = map[string]string{"MODE": "bad"}
			if e = s.UpsertServiceEnvironment(*target); e != nil {
				t.Fatal(e)
			}
			bad := realAgentArtifact(t, s, compiled, kind, "bad")
			second, e := m.CreateArtifact("api", bad.ID, "qa", "synthetic known exit triggers rollback", "tester")
			if e != nil {
				t.Fatal(e)
			}
			tickAgentUntil(t, manager, func() bool { return s.GetReleaseOrder(second.ID).Status == "rolled-back" }, func() any {
				ids := s.ReleaseDeployIDs(second.ID)
				if len(ids) > 0 {
					return s.GetDeploy(ids[0])
				}
				return s.GetReleaseOrder(second.ID)
			})
			current, e := os.Readlink(filepath.Join(root, "current"))
			if e != nil || current != previous {
				t.Fatal("previous complete directory not restored", current, e)
			}
			if string(mustRead(t, filepath.Join(current, "runtime.env"))) != string(oldEnv) {
				t.Fatal("old runtime snapshot lost")
			}
			if b := string(mustRead(t, starts)); b != "old\nbad\nold\n" {
				t.Fatalf("unexpected start count %q", b)
			}
			if kind == ArtifactJavaBundle {
				if string(mustRead(t, filepath.Join(current, "bundle/tools/changchun-traffic/data/model.csv"))) != "GIS-DATA-old" {
					t.Fatal("whole package data was not restored")
				}
				info, e := os.Stat(filepath.Join(current, "bundle/bin/estate-gis-service.sh"))
				if e != nil || info.Mode().Perm()&0111 == 0 {
					t.Fatal("bundled executable permissions lost")
				}
			}
			if strings.Contains(string(mustRead(t, filepath.Join(root, "logs/application.log"))), "BUNDLED_SCRIPT_MUST_NOT_RUN") {
				t.Fatal("source bundled script was executed")
			}
			t.Log("real JDK8: new process exit -> original directory, artifact and old environment restored; exactly old/bad/old starts")
		})
	}
}

func TestDeploymentAgentThreeIsolatedContainers(t *testing.T) {
	image := os.Getenv("DEVOPS_TEST_AGENT_IMAGE")
	if image == "" {
		t.Skip("requires explicit cached JDK8 image and private Docker fixtures")
	}
	base := t.TempDir()
	compiled := compileAgentJavaFixture(t, base)
	s, m, _, _, _ := deploymentBrokerFixture(t)
	gatewayBytes, e := exec.Command("docker", "network", "inspect", "bridge", "--format", "{{(index .IPAM.Config 0).Gateway}}").Output()
	if e != nil {
		t.Fatal(e)
	}
	gateway := strings.TrimSpace(string(gatewayBytes))
	if net.ParseIP(gateway) == nil {
		t.Fatal("private bridge gateway missing")
	}
	listener, e := net.Listen("tcp", net.JoinHostPort(gateway, "0"))
	if e != nil {
		t.Fatal(e)
	}
	mux := http.NewServeMux()
	registerDeploymentAgentRoutes(mux, m)
	var drop atomic.Bool
	drop.Store(true)
	var duplicates atomic.Int32
	var callsMu sync.Mutex
	seen := map[string]bool{}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/deployment-agent/reports" {
			body, e := io.ReadAll(r.Body)
			if e != nil {
				http.Error(w, "read", 400)
				return
			}
			r.Body = io.NopCloser(strings.NewReader(string(body)))
			var report deploytask.Report
			json.Unmarshal(body, &report)
			key := report.TaskID + ":" + strconv.FormatInt(report.Sequence, 10)
			callsMu.Lock()
			if seen[key] {
				duplicates.Add(1)
			}
			seen[key] = true
			callsMu.Unlock()
			recorded := httptest.NewRecorder()
			mux.ServeHTTP(recorded, r)
			if report.State == "success" && recorded.Code == 200 && drop.CompareAndSwap(true, false) {
				conn, _, _ := w.(http.Hijacker).Hijack()
				conn.Close()
				return
			}
			for k, values := range recorded.Header() {
				for _, v := range values {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(recorded.Code)
			w.Write(recorded.Body.Bytes())
			return
		}
		mux.ServeHTTP(w, r)
	})}
	go server.Serve(listener)
	defer server.Close()
	upstream := "http://" + listener.Addr().String()
	hash := sha256.Sum256([]byte(base))
	prefix := "devops-agent-v2-" + hex.EncodeToString(hash[:])[:8]
	names := []string{}
	dirs := map[string]string{}
	tokens := map[string]string{}
	cleanup := func() {
		for _, name := range names {
			label, _ := exec.Command("docker", "inspect", name, "--format", "{{index .Config.Labels \"devops.fixture\"}}").Output()
			if strings.TrimSpace(string(label)) == prefix {
				exec.Command("docker", "rm", "-f", name).Run()
			}
		}
	}
	defer cleanup()
	hosts := []string{"fixture", "fixture2", "fixture3"}
	for i, host := range hosts {
		if i > 0 {
			if e = s.UpsertHost(Host{Name: host, Environment: "qa", Addr: fmt.Sprintf("192.0.2.%d:1", i+1), User: "never-connect"}); e != nil {
				t.Fatal(e)
			}
		}
		token, e := s.registerDeploymentAgent(deploymentAgentRegistration{AgentID: "agent_" + host, Host: host, Environment: "qa", Services: []string{"api"}})
		if e != nil {
			t.Fatal(e)
		}
		tokens[host] = token
		dir := filepath.Join(base, host)
		os.Mkdir(dir, 0700)
		dirs[host] = dir
		fixtureCopy(t, os.Args[0], filepath.Join(dir, "deploy.test"))
		policy := containerAgentPolicy{Policy: deployment.Config{Enabled: true, StateDir: "/qa/tasks", AllowedRoots: []string{"/isolated"}, AgentID: "agent_" + host, Host: host, Environment: "qa", WorkerMode: "isolated-process"}, Credential: token, Upstream: upstream}
		b, _ := json.Marshal(policy)
		os.WriteFile(filepath.Join(dir, "policy.json"), b, 0600)
		name := fmt.Sprintf("%s-%d", prefix, i+1)
		args := []string{"run", "-d", "--name", name, "--label", "devops.fixture=" + prefix, "--memory", "256m", "--cpus", "0.5", "--pids-limit", "128", "--cap-drop=ALL", "--security-opt=no-new-privileges", "-v", dir + ":/qa:Z", "--entrypoint", "/bin/bash", image, "-c", "exec sleep 600"}
		if b, e := exec.Command("docker", args...).CombinedOutput(); e != nil {
			t.Fatalf("private container start: %v %s", e, b)
		}
		names = append(names, name)
	}
	target := s.GetServiceEnvironment("api", "qa")
	target.Hosts = hosts
	target.RemoteDir = "/isolated/api"
	target.HealthURL = "http://127.0.0.1:19997/health"
	target.DeployConfig = map[string]string{"deployment_transport": "agent", "java_mode": "managed", "java_version": "8", "java_port": "19997", "java_jvm_options": "-Xmx32m", "java_args": "/qa/ready\n/qa/starts\n19997", "health_failure_action": "rollback"}
	target.BatchSize = 2
	target.MaxParallel = 1
	target.PauseBetweenBatches = true
	target.RuntimeEnv = map[string]string{"MODE": "normal"}
	if e = s.UpsertServiceEnvironment(*target); e != nil {
		t.Fatal(e)
	}
	a := realAgentArtifact(t, s, compiled, ArtifactJar, "multi")
	o, e := m.CreateArtifact("api", a.ID, "qa", "three independent container hosts", "tester")
	if e != nil {
		t.Fatal(e)
	}
	awaitBroker(t, func() bool { return len(allAgentTasks(t, s)) == 1 })
	// Only the synthetic warning duration changes before any container receives it.
	task := allAgentTasks(t, s)[0]
	task.WarnAfterSeconds = 1
	raw, _ := json.Marshal(task)
	cipher, _ := s.encryptSecret(string(raw))
	s.db.Exec(`UPDATE agent_deployment_tasks SET plan_cipher=?,plan_digest=? WHERE task_id=?`, cipher, deploytask.Digest(task), task.ID)
	for i, name := range names {
		if i > 0 {
			os.WriteFile(filepath.Join(dirs[hosts[i]], "ready"), nil, 0600)
		}
		if b, e := exec.Command("docker", "exec", "-d", name, "/qa/deploy.test", "-deployment-fixture-manager", "/qa/policy.json").CombinedOutput(); e != nil {
			t.Fatalf("fixture Agent start: %v %s", e, b)
		}
	}
	wait := func(condition func() bool) {
		t.Helper()
		deadline := time.Now().Add(50 * time.Second)
		for time.Now().Before(deadline) {
			if condition() {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		for _, host := range hosts {
			if b, e := os.ReadFile(filepath.Join(dirs[host], "manager.log")); e == nil {
				t.Logf("%s: %s", host, b)
			}
		}
		for _, id := range s.ReleaseDeployIDs(o.ID) {
			t.Logf("deploy: %+v", s.GetDeploy(id))
		}
		t.Fatalf("three-container fixture condition missing, release=%+v", s.GetReleaseOrder(o.ID))
	}
	wait(func() bool { return s.GetReleaseOrder(o.ID).Status == "needs_review" })
	if len(allAgentTasks(t, s)) != 1 {
		t.Fatal("unknown result dispatched another container")
	}
	pidBefore, e := exec.Command("docker", "exec", names[0], "cat", "/isolated/api/.devopsplatform.pid").Output()
	if e != nil {
		t.Fatal(e)
	}
	if b, e := exec.Command("docker", "exec", names[0], "/bin/bash", "-c", `pid=$(cat /qa/manager.pid); tr '\0' '\n' < /proc/$pid/cmdline | grep -Fxq -- '-deployment-fixture-manager' && kill -KILL "$pid"`).CombinedOutput(); e != nil {
		t.Fatalf("kill synthetic Agent: %v %s", e, b)
	}
	exec.Command("docker", "exec", "-d", names[0], "/qa/deploy.test", "-deployment-fixture-manager", "/qa/policy.json").Run()
	os.WriteFile(filepath.Join(dirs[hosts[0]], "ready"), nil, 0600)
	wait(func() bool { return s.GetReleaseOrder(o.ID).Status == "waiting_next_batch" && duplicates.Load() > 0 })
	pidAfter, e := exec.Command("docker", "exec", names[0], "cat", "/isolated/api/.devopsplatform.pid").Output()
	if e != nil || string(pidBefore) != string(pidAfter) {
		t.Fatal("Agent restart replaced application process")
	}
	if len(allAgentTasks(t, s)) != 2 {
		t.Fatal("batch pause bypassed")
	}
	if e = m.Continue(o.ID, "tester"); e != nil {
		t.Fatal(e)
	}
	wait(func() bool { return s.GetReleaseOrder(o.ID).Status == "success" })
	for _, host := range hosts {
		b, e := os.ReadFile(filepath.Join(dirs[host], "starts"))
		if e != nil || string(b) != "normal\n" {
			t.Fatalf("application start replayed on %s: %q %v", host, b, e)
		}
	}
	t.Logf("three private container filesystems: concurrency=1, pause after two, Agent SIGKILL preserved JVM, lost ACK deduplicated (%d retries), each host started once", duplicates.Load())
}
