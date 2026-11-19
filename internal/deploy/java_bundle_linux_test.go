package deploy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Explicit opt-in: this test runs only synthetic Java code, in its own Linux
// process/filesystem/network namespace. It never opens a platform database,
// business repository, registered SSH host or deployment execution endpoint.
func TestJavaBundleLinuxGISLifecycle(t *testing.T) {
	if runtime.GOOS != "linux" || os.Getenv("DEVOPS_TEST_JAVA_BUNDLE_LINUX") != "1" {
		t.Skip("opt in inside an isolated Linux JDK21 fixture")
	}
	base := t.TempDir()
	javaHome := os.Getenv("JAVA_HOME")
	if javaHome == "" {
		t.Fatal("fixture requires JAVA_HOME")
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	source := `import com.sun.net.httpserver.HttpServer; import java.net.InetSocketAddress; import java.nio.file.*;
public class BundleFixture {
 public static void main(String[] args) throws Exception {
  String marker=System.getenv("FIXTURE_MARK");
  if("fail".equals(marker)){Thread.sleep(600); System.exit(3);}
  String data=Files.readString(Path.of("tools/changchun-traffic/data/edge_features_changchun_full.csv")).trim();
  var server=HttpServer.create(new InetSocketAddress("127.0.0.1",Integer.parseInt(args[0])),0);
  server.createContext("/ready",e->{byte[] body=(marker+"|"+data+"|"+Path.of("").toAbsolutePath()).getBytes();e.sendResponseHeaders(200,body.length);e.getResponseBody().write(body);e.close();});server.start();
 }
}`
	javaSource := filepath.Join(base, "BundleFixture.java")
	os.WriteFile(javaSource, []byte(source), 0600)
	compile := exec.Command(filepath.Join(javaHome, "bin/javac"), javaSource)
	if out, e := compile.CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	jar := filepath.Join(base, "fixture.jar")
	makeJar := exec.Command(filepath.Join(javaHome, "bin/jar"), "--create", "--file", jar, "--main-class", "BundleFixture", "-C", base, "BundleFixture.class")
	if out, e := makeJar.CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	newArtifact := func(label string) Artifact {
		ws, root, p := gisBundleFixture(t)
		b, e := os.ReadFile(jar)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(root, p["MAIN_JAR"]), b, 0600); e != nil {
			t.Fatal(e)
		}
		os.WriteFile(filepath.Join(root, "tools/changchun-traffic/data/edge_features_changchun_full.csv"), []byte(label), 0600)
		file, e := packageJavaBundle(context.Background(), ws, t.TempDir(), "gis", p, time.Now().Add(-time.Second))
		if e != nil {
			t.Fatal(e)
		}
		a, e := describeArtifact("gis", label, ArtifactJavaBundle, file, "")
		if e != nil {
			t.Fatal(e)
		}
		return a
	}
	sv := &Service{Name: "gis", ArtifactType: ArtifactJavaBundle, TargetType: TargetJavaBundle, RemoteDir: filepath.Join(base, "managed"), RuntimeEnvironment: "test", HealthURL: fmt.Sprintf("http://127.0.0.1:%d/ready", port), DeployConfig: map[string]string{"java_mode": "managed", "java_version": "21", "java_home": javaHome, "java_bundle_jar": "lib/estate-gis-service.jar", "java_port": strconv.Itoa(port), "java_args": strconv.Itoa(port), "java_jvm_options": "-Xms16m\n-Xmx64m\n-Dliteral=$(touch " + filepath.Join(base, "injected") + ")"}, RuntimeEnv: map[string]string{"FIXTURE_MARK": "one"}}
	var commands []string
	corruptUpload := false
	within := func(file string) {
		t.Helper()
		rel, e := filepath.Rel(base, file)
		if e != nil || rel == ".." || strings.HasPrefix(rel, "../") {
			t.Fatal("fixture attempted outside its directory", file)
		}
	}
	r := javaRemote{
		exec: func(_ Host, c string, timeout time.Duration) (string, error) {
			commands = append(commands, c)
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			cmd := exec.CommandContext(ctx, "bash", "-c", c)
			out, e := cmd.CombinedOutput()
			if e != nil {
				t.Logf("isolated command failure: %s", out)
			}
			return string(out), e
		},
		upload: func(_ Host, source, dest string) error {
			within(dest)
			b, e := os.ReadFile(source)
			if e != nil {
				return e
			}
			if corruptUpload {
				b = append(b, []byte("transport corruption")...)
			}
			return os.WriteFile(dest, b, 0600)
		},
		write: func(_ Host, b []byte, dest string) error { within(dest); return os.WriteFile(dest, b, 0600) },
	}
	d := NewDeployer(nil, NewLogHub(), "")
	t.Cleanup(func() {
		current := filepath.Join(sv.RemoteDir, "current/service.sh")
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		out, e := exec.CommandContext(ctx, "bash", current, "stop").CombinedOutput()
		if e != nil {
			t.Log("fixture cleanup", e, string(out))
		}
	})
	assertReady := func(marker, data string) {
		t.Helper()
		client := &http.Client{Timeout: 2 * time.Second}
		resp, e := client.Get(sv.HealthURL)
		if e != nil {
			t.Fatal(e)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		want := marker + "|" + data + "|"
		if resp.StatusCode != 200 || !strings.HasPrefix(string(b), want) {
			t.Fatal("wrong restored code/data/env", string(b), want)
		}
	}
	first := newArtifact("dataset-one")
	if got := d.deployJavaBundleRemote(1, sv, Host{}, first, r); got != "success" {
		t.Fatal(got, strings.Join(commands, "\n"))
	}
	assertReady("one", "dataset-one")
	if _, e = os.Stat(filepath.Join(base, "injected")); !os.IsNotExist(e) {
		t.Fatal("JVM argument was executed by a shell")
	}
	oldCurrent, _ := os.Readlink(filepath.Join(sv.RemoteDir, "current"))
	second := newArtifact("dataset-two")
	sv.RuntimeEnv["FIXTURE_MARK"] = "two"
	corruptUpload = true
	before := len(commands)
	if got := d.deployJavaBundleRemote(2, sv, Host{}, second, r); got != "failed" {
		t.Fatal("corrupt transfer succeeded", got)
	}
	if strings.Contains(strings.Join(commands[before:], "\n"), "/deploy-1/service.sh' stop") {
		t.Fatal("corrupt transfer stopped old process")
	}
	assertReady("one", "dataset-one")
	corruptUpload = false
	// A duplicate/uncertain execution ID cannot overwrite a prepared directory.
	if got := d.deployJavaBundleRemote(2, sv, Host{}, second, r); got != "failed" {
		t.Fatal("replayed deployment id was allowed", got)
	}
	assertReady("one", "dataset-one")
	if got := d.deployJavaBundleRemote(3, sv, Host{}, second, r); got != "success" {
		t.Fatal(got)
	}
	assertReady("two", "dataset-two")
	if b, e := os.ReadFile(filepath.Join(oldCurrent, "bundle/tools/changchun-traffic/data/edge_features_changchun_full.csv")); e != nil || string(b) != "dataset-one" {
		t.Fatal("old bundle changed", e, string(b))
	}
	// Start fails after the PID has acquired the immutable JAR identity. Rollback
	// must restore the previous directory AND its environment, never today's values.
	sv.RuntimeEnv["FIXTURE_MARK"] = "fail"
	third := newArtifact("dataset-failed")
	if got := d.deployJavaBundleRemote(4, sv, Host{}, third, r); got != "rolled-back" {
		t.Fatal(got)
	}
	assertReady("two", "dataset-two")
	sv.RuntimeEnv["FIXTURE_MARK"] = "after-crash"
	lock := filepath.Join(sv.RemoteDir, ".deploy-lock")
	os.Mkdir(lock, 0700)
	os.WriteFile(filepath.Join(lock, "owner"), []byte("deploy-crashed"), 0600)
	os.WriteFile(filepath.Join(lock, "started_at"), []byte(strconv.FormatInt(time.Now().Unix(), 10)), 0600)
	if got := d.deployJavaBundleRemote(5, sv, Host{}, third, r); got != "failed" {
		t.Fatal("active crash lease was stolen", got)
	}
	assertReady("two", "dataset-two")
	os.WriteFile(filepath.Join(lock, "started_at"), []byte("1"), 0600)
	if got := d.deployJavaBundleRemote(6, sv, Host{}, third, r); got != "success" {
		t.Fatal("stale lease not recovered", got)
	}
	assertReady("after-crash", "dataset-failed")
	instance := inspectManagedJavaInstance(sv, Host{}, r.exec)
	if instance.State != "running" || instance.DeployID != 6 {
		t.Fatal("bundle instance identity lost", instance)
	}
	if _, e = controlManagedJavaInstance(sv, Host{}, "restart", r.exec); e != nil {
		t.Fatal(e)
	}
	assertReady("after-crash", "dataset-failed")
	// A foreign Jpom/unmanaged PID is never stopped, even if it appears in the
	// platform PID file. Restore our own PID before fixture cleanup.
	pidfile := filepath.Join(sv.RemoteDir, ".devopsplatform.pid")
	originalPID, _ := os.ReadFile(pidfile)
	foreign := exec.Command("sleep", "60")
	if e = foreign.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { foreign.Process.Kill(); foreign.Wait() }()
	os.WriteFile(pidfile, []byte(strconv.Itoa(foreign.Process.Pid)), 0600)
	current := filepath.Join(sv.RemoteDir, "current/service.sh")
	out, stopError := exec.Command("bash", current, "stop").CombinedOutput()
	os.WriteFile(pidfile, originalPID, 0600)
	if stopError == nil || !strings.Contains(string(out), "belongs to another process") {
		t.Fatal("unmanaged PID was accepted", stopError, string(out))
	}
	if out, e := exec.Command("bash", "-c", "kill -0 "+strconv.Itoa(foreign.Process.Pid)).CombinedOutput(); e != nil {
		t.Fatal("foreign process was stopped", string(out))
	}
	assertReady("after-crash", "dataset-failed")
	t.Log("Linux JDK21 GIS-layout lifecycle passed: full files, JAR identity, version env, tampered transport, duplicate ID, rollback, crash lease, restart and foreign PID protection")
}
