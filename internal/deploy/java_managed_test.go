package deploy

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func managedJavaFixture() *Service {
	return &Service{Name: "api", TargetType: "ssh-jar", RemoteDir: "/test/app", RuntimeEnvironment: "test", HealthURL: "http://127.0.0.1:19997/health", DeployConfig: map[string]string{"java_mode": "managed", "java_version": "21", "java_port": "19997", "java_jvm_options": "-Xms32m\n-Xmx64m"}, RuntimeEnv: map[string]string{"ENV_MARK": "release1"}}
}

type javaTestExit int

func (e javaTestExit) Error() string   { return fmt.Sprintf("synthetic Java exit %d", e) }
func (e javaTestExit) ExitStatus() int { return int(e) }

func TestManagedJavaValidationAndQuoting(t *testing.T) {
	sv := managedJavaFixture()
	sv.HealthURL = "http://{host}:19997/health"
	if err := validateManagedJava(sv); err != nil {
		t.Fatal(err)
	}
	sv.DeployConfig["java_jvm_options"] = "-Dtext=$(touch /tmp/SHOULD_NOT_EXIST)\n-Dvalue=with spaces and 'quote'"
	script := managedJavaScript(sv, "/test/app/releases/deploy-2")
	cmd := exec.Command("bash", "-n")
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("invalid generated Bash: %v %s", err, out)
	}
	if !strings.Contains(script, "'-Dtext=$(touch /tmp/SHOULD_NOT_EXIST)'") {
		t.Fatal("JVM argument is not literal")
	}
	if strings.Contains(script, "{host}") || !strings.Contains(script, "http://127.0.0.1:19997/health") || !strings.Contains(script, "application.log (last 60 lines)") {
		t.Fatal("health placeholder or readiness diagnostics were not rendered safely")
	}
	sv.DeployConfig["java_port"] = "bad"
	if validateManagedJava(sv) == nil {
		t.Fatal("accepted invalid port")
	}
	sv.DeployConfig["java_port"] = ""
	sv.HealthURL = ""
	if validateManagedJava(sv) == nil {
		t.Fatal("accepted unverified deployment")
	}
	sv.HealthURL = "http://127.0.0.1/health"
	sv.DeployConfig["health_failure_action"] = "unsafe"
	if validateManagedJava(sv) == nil {
		t.Fatal("accepted invalid health failure action")
	}
}

func TestManagedJavaLauncherEmptyArraysAndEarlyExitDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, jvm, args   string
		wantJVM, wantArgs []string
	}{
		{"empty", "", "", nil, nil},
		{"empty-app-args", "-Xms32m\n-Dliteral=a value *", "", []string{"-Xms32m", "-Dliteral=a value *"}, nil},
		{"empty-jvm", "", "--value=with spaces\n--literal=$(false)", nil, []string{"--value=with spaces", "--literal=$(false)"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			release := filepath.Join(root, "app/releases/deploy-1")
			for _, dir := range []string{release, filepath.Join(root, "app/logs"), filepath.Join(root, "jre/bin")} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			sv := managedJavaFixture()
			sv.RemoteDir = filepath.Join(root, "app")
			sv.HealthURL = ""
			sv.DeployConfig["java_home"] = filepath.Join(root, "jre")
			sv.DeployConfig["java_port"] = ""
			sv.DeployConfig["java_jvm_options"] = tc.jvm
			sv.DeployConfig["java_args"] = tc.args
			// A synthetic executable records argv then exits. No JVM, port or
			// business application is started; the generated launcher is exercised.
			fakeJava := "#!/bin/bash\nprintf 'ARG[%s]\\n' \"$@\"\necho synthetic-java-exit >&2\nexit 17\n"
			if err := os.WriteFile(filepath.Join(root, "jre/bin/java"), []byte(fakeJava), 0700); err != nil {
				t.Fatal(err)
			}
			tr, err := exec.LookPath("tr")
			if err != nil {
				t.Fatal(err)
			}
			// Hold the first /proc read until the synthetic process exits. This
			// exercises the start failure branch deterministically on Linux too,
			// instead of occasionally observing the stub before its planned exit.
			probe := "#!/bin/bash\nsleep .2\nexec " + shellQuote(tr) + " \"$@\"\n"
			if err := os.WriteFile(filepath.Join(root, "jre/bin/tr"), []byte(probe), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(release, "runtime.env"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(release, "service.sh")
			if err := os.WriteFile(file, []byte(managedJavaScript(sv, release)), 0700); err != nil {
				t.Fatal(err)
			}
			// /bin/bash is 3.2 on macOS, which reproduces the pre-4.4 nounset
			// behavior. The same fixture also runs with the Linux test host Bash.
			cmd := exec.Command("/bin/bash", file, "start")
			cmd.Env = append(os.Environ(), "LC_ALL=C", "PATH="+filepath.Join(root, "jre/bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
			out, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(out), "Java failed to start (exit status 17)") || !strings.Contains(string(out), "synthetic-java-exit") || strings.Contains(string(out), "unbound variable") || strings.Contains(string(out), "tr:") {
				t.Fatalf("early exit lost its diagnostics: %v\n%s", err, out)
			}
			log, err := os.ReadFile(filepath.Join(sv.RemoteDir, "logs/application.log"))
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(log)), "\n")
			want := append([]string{}, tc.wantJVM...)
			want = append(want, "-jar", filepath.Join(release, "app.jar"))
			want = append(want, tc.wantArgs...)
			wantLines := make([]string, 0, len(want)+1)
			for _, arg := range want {
				wantLines = append(wantLines, "ARG["+arg+"]")
			}
			wantLines = append(wantLines, "synthetic-java-exit")
			if strings.Join(lines, "\n") != strings.Join(wantLines, "\n") {
				t.Fatalf("arguments changed: got %q want %q", lines, wantLines)
			}
		})
	}
}

func TestManagedJavaOptionalWorkingDirectoryPreservesRelativePaths(t *testing.T) {
	sv := managedJavaFixture()
	release := "/test/app/releases/deploy-2"
	defaultScript := managedJavaScript(sv, release)
	if !strings.Contains(defaultScript, "readonly workdir='/test/app/releases/deploy-2'") {
		t.Fatal("default runtime directory changed")
	}
	for _, invalid := range []string{"relative/path", "../other", "/tmp/with\x00nul"} {
		sv.DeployConfig["java_workdir"] = invalid
		if validateManagedJava(sv) == nil {
			t.Fatalf("invalid workdir accepted: %q", invalid)
		}
	}
	dir := filepath.Join(t.TempDir(), "old app ' $(false)")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	sv.DeployConfig["java_workdir"] = dir
	if err := validateManagedJava(sv); err != nil {
		t.Fatal(err)
	}
	script := managedJavaScript(sv, release)
	if !strings.Contains(script, `(cd "$workdir";`) || !strings.Contains(script, `[[ -d "$workdir" && -x "$workdir" ]]`) || !strings.Contains(script, `-jar "$jar"`) {
		t.Fatal("working directory changed launcher ownership, JAR identity or preflight")
	}
	// Execute only generated constant declarations and cd, never a JVM or a
	// lifecycle action. Spaces, quotes and shell syntax must remain literal.
	preamble := script[:strings.Index(script, "owned_pid()")]
	cmd := exec.Command("bash", "-c", preamble+"\ncd \"$workdir\"\npwd -P\n")
	out, err := cmd.CombinedOutput()
	resolved, _ := filepath.EvalSymlinks(dir)
	if err != nil || strings.TrimSpace(string(out)) != resolved {
		t.Fatalf("working directory not preserved literally: %q, %v", out, err)
	}
}

func TestJavaBytecodeCompatibility(t *testing.T) {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	f, _ := z.Create("BOOT-INF/classes/Example.class")
	f.Write([]byte{0xca, 0xfe, 0xba, 0xbe, 0, 0, 0, 65})
	multi, _ := z.Create("META-INF/versions/25/Example.class")
	multi.Write([]byte{0xca, 0xfe, 0xba, 0xbe, 0, 0, 0, 69})
	z.Close()
	file := filepath.Join(t.TempDir(), "test.jar")
	os.WriteFile(file, buf.Bytes(), 0600)
	if err := validateJavaBytecode(file, 21); err != nil {
		t.Fatal(err)
	}
	if err := validateJavaBytecode(file, 17); err == nil || !strings.Contains(err.Error(), "Java 21") {
		t.Fatalf("wrong compatibility result: %v", err)
	}
}

func TestManagedJavaEnvironmentRoundTrip(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.InitializeEnvironments([]Environment{{Name: "qa", DisplayName: "QA"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertService(Service{Name: "api", ArtifactType: "jar", BuildCmd: "true"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertHost(Host{Name: "qa-host", Environment: "qa", Addr: "127.0.0.1:1", User: "qa"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertHostGroup(HostGroup{Name: "qa-group", Environment: "qa", Hosts: []string{"qa-host"}}); err != nil {
		t.Fatal(err)
	}
	sv := managedJavaFixture()
	v := ServiceEnvironment{Service: "api", Environment: "qa", HostGroup: "qa-group", TargetType: "ssh-jar", RemoteDir: sv.RemoteDir, DeployConfig: sv.DeployConfig, RuntimeEnv: map[string]string{"SPRING_PROFILES_ACTIVE": "qa"}}
	if err := st.UpsertServiceEnvironment(v); err != nil {
		t.Fatal(err)
	}
	got := st.GetServiceEnvironment("api", "qa")
	if got.DeployConfig["java_version"] != "21" || got.DeployConfig["health_failure_action"] != "rollback" || !strings.Contains(got.StartCmd, "current/service.sh") || !strings.Contains(got.StopCmd, " stop") {
		t.Fatalf("configuration not persisted: %#v", got)
	}
	if len(got.RuntimeEnv) != 0 {
		t.Fatal("public environment returned plaintext secrets")
	}
	v.DeployConfig = map[string]string{"java_mode": "custom"}
	v.StartCmd = "custom-start"
	v.StopCmd = "custom-stop"
	if err := st.UpsertServiceEnvironment(v); err != nil {
		t.Fatal(err)
	}
	if got := st.GetServiceEnvironment("api", "qa"); got.StartCmd != "custom-start" {
		t.Fatal("custom command was overwritten")
	}
}

func TestProtectedManagedJavaEnvironmentRequiresRollback(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.InitializeEnvironments([]Environment{{Name: "prod", DisplayName: "生产", Protected: true, Approvers: "reviewer"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertService(Service{Name: "api", ArtifactType: "jar", BuildCmd: "true"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertHost(Host{Name: "prod-host", Environment: "prod", Addr: "127.0.0.1:1", User: "deploy"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertHostGroup(HostGroup{Name: "prod-group", Environment: "prod", Hosts: []string{"prod-host"}}); err != nil {
		t.Fatal(err)
	}
	sv := managedJavaFixture()
	sv.DeployConfig["health_failure_action"] = "keep"
	v := ServiceEnvironment{Service: "api", Environment: "prod", HostGroup: "prod-group", TargetType: "ssh-jar", RemoteDir: sv.RemoteDir, HealthURL: sv.HealthURL, DeployConfig: sv.DeployConfig}
	if err := st.UpsertServiceEnvironment(v); err == nil || !strings.Contains(err.Error(), "受保护环境") {
		t.Fatalf("protected environment accepted keep-running policy: %v", err)
	}
}

func TestManagedJavaReleaseRollbackStates(t *testing.T) {
	for _, tc := range []struct{ name, previous, fail, action, want string }{
		{"success", "/test/app/releases/deploy-1", "", "", "success"},
		{"preflight", "/test/app/releases/deploy-1", "check", "", "failed"},
		{"rollback", "/test/app/releases/deploy-1", "ready", "rollback", "rolled-back"},
		{"stop-without-restore", "/test/app/releases/deploy-1", "ready", "stop", "failed"},
		{"keep-running", "/test/app/releases/deploy-1", "ready", "keep", "failed"},
		{"first-release", "", "ready", "", "failed"},
		{"rollback-failure", "/test/app/releases/deploy-1", "rollback", "", "rollback-failed"},
		{"foreign-current", "/other/application", "", "", "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sv := managedJavaFixture()
			if tc.action != "" {
				sv.DeployConfig["health_failure_action"] = tc.action
			}
			d := NewDeployer(nil, NewLogHub(), "")
			var commands []string
			files := map[string]string{}
			r := javaRemote{
				exec: func(_ Host, cmd string, _ time.Duration) (string, error) {
					commands = append(commands, cmd)
					if strings.Contains(cmd, "then readlink") {
						return tc.previous, nil
					}
					if tc.fail == "check" && strings.HasSuffix(cmd, " check") {
						return "", fmt.Errorf("JDK mismatch")
					}
					if (tc.fail == "ready" || tc.fail == "rollback") && strings.HasSuffix(cmd, "'deploy-2/service.sh' ready") {
						return "", javaTestExit(20)
					}
					// Full path is shell quoted, not just its basename.
					if (tc.fail == "ready" || tc.fail == "rollback") && strings.HasSuffix(cmd, "/deploy-2/service.sh' ready") {
						return "", javaTestExit(20)
					}
					if tc.fail == "rollback" && strings.Contains(cmd, "/deploy-1/service.sh' start") {
						return "", fmt.Errorf("rollback start failed")
					}
					return "", nil
				},
				upload: func(_ Host, _, _ string) error { return nil },
				write:  func(_ Host, content []byte, name string) error { files[name] = string(content); return nil },
			}
			if status := d.deployManagedJavaRemote(2, sv, Host{}, "test.jar", r); status != tc.want {
				t.Fatalf("got %s want %s\n%s", status, tc.want, strings.Join(commands, "\n"))
			}
			if tc.fail == "check" && strings.Contains(strings.Join(commands, "\n"), "/deploy-1/service.sh' stop") {
				t.Fatal("stopped old service before preflight passed")
			}
			for name := range files {
				if strings.Contains(name, "deploy-1") || strings.Contains(name, ".devopsplatform.env") {
					t.Fatal("overwrote old runtime configuration")
				}
			}
			if tc.want == "rolled-back" {
				joined := strings.Join(commands, "\n")
				if strings.Index(joined, "/deploy-2/service.sh' stop") > strings.Index(joined, "/deploy-1/service.sh' start") {
					t.Fatal("started old version before stopping failed version")
				}
			}
			joined := strings.Join(commands, "\n")
			if tc.action == "stop" && (!strings.Contains(joined, "/deploy-2/service.sh' stop") || strings.Contains(joined, "/deploy-1/service.sh' start")) {
				t.Fatal("stop policy did not preserve the failed release without restoring the old release")
			}
			if tc.action == "keep" && (strings.Contains(joined, "/deploy-2/service.sh' stop") || strings.Contains(joined, "/deploy-1/service.sh' start")) {
				t.Fatal("keep policy stopped the failed release or restored the old release")
			}
		})
	}
}

func TestManagedJavaInstanceInspectionAndControl(t *testing.T) {
	sv := managedJavaFixture()
	host := Host{Name: "qa-a", Addr: "192.0.2.10"}
	result := inspectManagedJavaInstance(sv, host, func(_ Host, command string, _ time.Duration) (string, error) {
		if !strings.Contains(command, ".devopsplatform.pid") || !strings.Contains(command, "/proc/$pid/cmdline") {
			t.Fatalf("unsafe or incomplete status command: %s", command)
		}
		return "release=deploy-42\npid=1234\nstate=running\n", nil
	})
	if result.State != "running" || result.PID != 1234 || result.DeployID != 42 || result.Host != host.Name {
		t.Fatalf("unexpected instance status: %#v", result)
	}

	var commands []string
	result, err := controlManagedJavaInstance(sv, host, "restart", func(_ Host, command string, _ time.Duration) (string, error) {
		commands = append(commands, command)
		if strings.Contains(command, ".deploy-lock") {
			return "", nil
		}
		return "release=deploy-42\npid=2345\nstate=running\n", nil
	})
	if err != nil || result.State != "running" || len(commands) != 2 {
		t.Fatalf("restart result=%#v err=%v commands=%v", result, err, commands)
	}
	if !strings.Contains(commands[0], `.deploy-lock`) || !strings.Contains(commands[0], `service.sh" stop`) || !strings.Contains(commands[0], `service.sh" start`) || !strings.Contains(commands[0], `service.sh" ready`) {
		t.Fatalf("restart is not serialized or readiness-gated: %s", commands[0])
	}
	if !strings.Contains(commands[0], `started_at`) || !strings.Contains(commands[0], `Recovered legacy empty deployment lock`) || !strings.Contains(commands[0], `requested_owner`) {
		t.Fatalf("instance lock has no ownership or stale-lock recovery: %s", commands[0])
	}
	if _, err := managedJavaControlCommand(sv.RemoteDir, "kill"); err == nil {
		t.Fatal("accepted unsupported instance action")
	}
}

func TestManagedJavaLockLeaseRecoversLegacyLockAndProtectsOwner(t *testing.T) {
	base := filepath.Join(t.TempDir(), "service")
	if err := os.MkdirAll(filepath.Join(base, ".deploy-lock"), 0700); err != nil {
		t.Fatal(err)
	}
	run := func(command string) (string, error) {
		cmd := exec.Command("bash", "-c", command)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := run(managedJavaLockAcquireCommand(base, "deploy-1")); err != nil {
		t.Fatalf("legacy lock was not recovered: %v\n%s", err, out)
	} else if !strings.Contains(out, "Recovered legacy empty deployment lock") {
		t.Fatalf("legacy recovery was not reported: %q", out)
	}
	owner, err := os.ReadFile(filepath.Join(base, ".deploy-lock", "owner"))
	if err != nil || strings.TrimSpace(string(owner)) != "deploy-1" {
		t.Fatalf("lock owner not persisted: %q %v", owner, err)
	}
	if out, err := run(managedJavaLockAcquireCommand(base, "deploy-2")); err == nil || !strings.Contains(out, "owner: deploy-1") {
		t.Fatalf("active lock was not protected: err=%v out=%q", err, out)
	}
	if out, err := run(managedJavaLockReleaseCommand(base, "deploy-2")); err == nil || !strings.Contains(out, "refusing to remove lock owned by deploy-1") {
		t.Fatalf("foreign owner released the lock: err=%v out=%q", err, out)
	}
	if out, err := run(managedJavaLockReleaseCommand(base, "deploy-1")); err != nil {
		t.Fatalf("owner could not release lock: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(base, ".deploy-lock")); !os.IsNotExist(err) {
		t.Fatalf("lock remains after release: %v", err)
	}
}

func TestManagedJavaRealLinuxLifecycle(t *testing.T) {
	image := os.Getenv("DEVOPS_TEST_BUILD_IMAGE")
	if image == "" {
		t.Skip("set DEVOPS_TEST_BUILD_IMAGE for Linux container lifecycle test")
	}
	root := t.TempDir()
	release := filepath.Join(root, "app/releases/deploy-1")
	for _, dir := range []string{release, filepath.Join(root, "app/logs")} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	sv := managedJavaFixture()
	sv.DeployConfig["java_jvm_options"] += "\n-Dliteral=$(touch /test/injected)"
	files := map[string]string{
		"app/releases/deploy-1/service.sh":  managedJavaScript(sv, "/test/app/releases/deploy-1"),
		"app/releases/deploy-1/runtime.env": "ENV_MARK='release1'\n",
		"Smoke.java": `import com.sun.net.httpserver.HttpServer; import java.net.InetSocketAddress;
public class Smoke { public static void main(String[] args) throws Exception {
  var server=HttpServer.create(new InetSocketAddress("127.0.0.1",19997),0);
  server.createContext("/health", e -> { byte[] b=System.getenv("ENV_MARK").getBytes(); e.sendResponseHeaders(200,b.length); e.getResponseBody().write(b); e.close(); });
  server.start();
}}`,
		"test.sh": `set -euo pipefail
cd /test
javac Smoke.java
jar cfe app/releases/deploy-1/app.jar Smoke Smoke.class
s=/test/app/releases/deploy-1/service.sh
bash "$s" check
bash "$s" start
bash "$s" ready
test "$(curl -fsS http://127.0.0.1:19997/health)" = release1
test ! -e /test/injected
bash "$s" start
bash "$s" stop
test ! -e app/.devopsplatform.pid
sleep 60 & foreign=$!
echo "$foreign" > app/.devopsplatform.pid
if bash "$s" stop; then echo 'Unsafe foreign-PID stop'; exit 1; fi
kill -0 "$foreign"
kill "$foreign"
rm app/.devopsplatform.pid
echo managed-java-lifecycle-ok
`,
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("docker", "run", "--rm", "--init", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), "--mount", "type=bind,source="+root+",target=/test", "--entrypoint", "bash", image, "/test/test.sh")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Linux lifecycle: %v\n%s", err, out)
	} else {
		t.Log(string(out))
	}
}
