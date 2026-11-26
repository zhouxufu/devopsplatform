//go:build linux || darwin

package deploy

import (
	"archive/zip"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func jpomFixture(dir string) *Service {
	return &Service{Name: "api", TargetType: "ssh-jar", RemoteDir: dir, RuntimeEnvironment: "test", DeployConfig: map[string]string{"java_mode": "jpom", "java_version": "8", "jpom_project_id": "old-project-id", "jpom_jar_name": "original-service.jar"}}
}

func TestJpomOverwriteUsesOriginalFilenameAndNeverCrossPlatformLocksOrRollback(t *testing.T) {
	for _, failure := range []int{0, 1, 2, 3, 4, 5} {
		t.Run(fmt.Sprint(failure), func(t *testing.T) {
			sv := jpomFixture("/old jpom/app/jar")
			sv.StartCmd = "must-not-use-stale-command"
			sv.StopCmd = "must-not-use-stale-stop"
			d := NewDeployer(nil, NewLogHub(), "")
			calls := []string{}
			uploaded := ""
			remote := javaRemote{exec: func(_ Host, cmd string, _ time.Duration) (string, error) {
				calls = append(calls, cmd)
				if len(calls) == failure {
					return "known script failure", javaTestExit(21)
				}
				return "", nil
			}, upload: func(_ Host, _, dest string) error {
				uploaded = dest
				if failure == 5 {
					return fmt.Errorf("upload failed")
				}
				return nil
			}}
			got := d.deployJarRemote(51, sv, Host{}, "artifact.jar", "commit", remote)
			want := "success"
			if failure != 0 {
				want = "failed"
			}
			if got != want {
				t.Fatalf("got %s want %s", got, want)
			}
			if failure != 1 && uploaded != "/old jpom/app/jar/original-service.jar.new-51" {
				t.Fatalf("wrong artifact destination %q", uploaded)
			}
			text := strings.Join(calls, "\n")
			for _, bad := range []string{".deploy-lock", "flock ", ".bak-", "must-not-use-stale", "开始回滚"} {
				if strings.Contains(text, bad) {
					t.Fatal("unexpected operation", bad)
				}
			}
			if failure >= 1 && failure <= 4 && len(calls) != failure {
				t.Fatal("failed step was replayed or rollback dispatched")
			}
			if failure == 5 && len(calls) != 1 {
				t.Fatal("upload failure changed runtime")
			}
			if failure == 0 && (!strings.Contains(calls[1], "-DJpom.application=old-project-id") || !strings.Contains(calls[2], "original-service.jar.new-51") || !strings.Contains(calls[3], "nohup")) {
				t.Fatal("stop/copy/start did not preserve shared project identity")
			}
		})
	}
}

func TestJpomGeneratedCommandsRecognizeNativeProcessAndPreserveForeignInstances(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "library with spaces")
	proc := filepath.Join(root, "proc")
	os.MkdirAll(dir, 0700)
	os.MkdirAll(proc, 0700)
	sv := jpomFixture(dir)
	sv.DeployConfig["jpom_log_file"] = filepath.Join(root, "console/project.log")
	startProcess := func(project, base string) *exec.Cmd {
		t.Helper()
		c := exec.Command("sleep", "60")
		if e := c.Start(); e != nil {
			t.Fatal(e)
		}
		done := make(chan struct{})
		go func() { _ = c.Wait(); close(done) }()
		t.Cleanup(func() { _ = c.Process.Kill(); <-done })
		p := filepath.Join(proc, strconv.Itoa(c.Process.Pid))
		os.Mkdir(p, 0700)
		args := []string{"java", "-DJpom.application=" + project, "-DJpom.basedir=" + base, "-jar", filepath.Join(base, "original-service.jar")}
		os.WriteFile(p+"/cmdline", []byte(strings.Join(args, "\x00")+"\x00"), 0600)
		return c
	}
	original := startProcess("old-project-id", dir)
	foreign := startProcess("old-project-id", root+"/different-app")
	start, stop := jpomJavaCommandsFor(sv, proc)
	out, e := exec.Command("bash", "-c", start).CombinedOutput()
	if exit, ok := e.(*exec.ExitError); !ok || exit.ExitCode() != 21 {
		t.Fatalf("existing Jpom instance was duplicated: %v %s", e, out)
	}
	out, e = exec.Command("bash", "-c", stop).CombinedOutput()
	if e != nil {
		t.Fatalf("could not stop owned original: %v %s", e, out)
	}
	if syscall.Kill(original.Process.Pid, 0) == nil {
		t.Fatal("original process not stopped")
	}
	if syscall.Kill(foreign.Process.Pid, 0) != nil {
		t.Fatal("different application was stopped")
	}
	// A synthetic Java executable records the JVM arguments exactly, then sleeps.
	// It is isolated under this test's directory, never a business JVM.
	javaDir := filepath.Join(root, "jre/bin")
	os.MkdirAll(javaDir, 0700)
	argsFile := filepath.Join(root, "argv")
	pidFile := filepath.Join(root, "new-pid")
	fake := "#!/bin/bash\nmkdir -p " + shellQuote(proc) + "/$$\nprintf '%s\\0' \"$0\" \"$@\" > " + shellQuote(proc) + "/$$/cmdline\nprintf '%s\\n' \"$@\" > " + shellQuote(argsFile) + "\nprintf '%s' \"$$\" > " + shellQuote(pidFile) + "\nexec sleep 60\n"
	os.WriteFile(filepath.Join(javaDir, "java"), []byte(fake), 0700)
	sv.DeployConfig["java_home"] = filepath.Dir(javaDir)
	sv.DeployConfig["java_jvm_options"] = "-Dliteral=$(touch SHOULD_NOT_EXIST)"
	start, _ = jpomJavaCommandsFor(sv, proc)
	out, e = exec.Command("bash", "-c", start).CombinedOutput()
	if e != nil {
		t.Fatalf("launch failed: %v %s", e, out)
	}
	b, e := os.ReadFile(pidFile)
	if e != nil {
		t.Fatal(e)
	}
	pid, _ := strconv.Atoi(string(b))
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGTERM) })
	b, e = os.ReadFile(argsFile)
	if e != nil {
		t.Fatal(e)
	}
	args := string(b)
	for _, v := range []string{"-DJpom.application=old-project-id", "-DJpom.basedir=" + dir, filepath.Join(dir, "original-service.jar"), "-Dliteral=$(touch SHOULD_NOT_EXIST)"} {
		if !strings.Contains(args, v) {
			t.Fatal("Jpom-compatible launch lost literal argument", v)
		}
	}
	if _, e := os.Stat(filepath.Join(dir, "SHOULD_NOT_EXIST")); !os.IsNotExist(e) {
		t.Fatal("JVM argument executed shell substitution")
	}
	if syscall.Kill(foreign.Process.Pid, 0) != nil {
		t.Fatal("foreign process changed during launch")
	}
	extra := startProcess("old-project-id", dir)
	out, e = exec.Command("bash", "-c", stop).CombinedOutput()
	if exit, ok := e.(*exec.ExitError); !ok || exit.ExitCode() != 21 {
		t.Fatalf("multiple matching processes were not rejected: %v %s", e, out)
	}
	if syscall.Kill(extra.Process.Pid, 0) != nil || syscall.Kill(pid, 0) != nil {
		t.Fatal("ambiguous process was stopped")
	}
}

func TestJpomCompatibilityScopeValidation(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	envs := []Environment{{Name: "dev", Purpose: "development"}, {Name: "test", Purpose: "test"}, {Name: "prod", Purpose: "production"}, {Name: "stage", Purpose: "staging"}, {Name: "protected", Purpose: "test", Protected: true, Approvers: "admin"}}
	for i := range envs {
		envs[i].DisplayName = envs[i].Name
	}
	if e = s.InitializeEnvironments(envs); e != nil {
		t.Fatal(e)
	}
	if e = s.UpsertService(Service{Name: "api", ArtifactType: "jar", BuildCmd: "true"}); e != nil {
		t.Fatal(e)
	}
	for _, env := range envs {
		host := env.Name + "-host"
		if e = s.UpsertHost(Host{Name: host, Environment: env.Name, Addr: "192.0.2.1", User: "test"}); e != nil {
			t.Fatal(e)
		}
		sv := jpomFixture("/old/jpom/jar")
		v := ServiceEnvironment{Service: "api", Environment: env.Name, TargetType: "ssh-jar", RemoteDir: sv.RemoteDir, DeployConfig: sv.DeployConfig, Hosts: []string{host}}
		e = s.UpsertServiceEnvironment(v)
		allowed := env.Name == "dev" || env.Name == "test"
		if (e == nil) != allowed {
			t.Fatalf("%s: %v", env.Name, e)
		}
		if allowed {
			saved := s.GetServiceEnvironment("api", env.Name)
			if !strings.Contains(saved.StartCmd, "-DJpom.application=old-project-id") || saved.RemoteDir != "/old/jpom/jar" {
				t.Fatal("saved native identity changed")
			}
		}
	}
	sv := jpomFixture("/old/jpom/jar")
	for _, jar := range []string{"../other.jar", "/absolute.jar", ".jar", "file\n.jar"} {
		sv.DeployConfig["jpom_jar_name"] = jar
		if validateJpomJava(sv) == nil {
			t.Fatal("unsafe jar accepted", jar)
		}
	}
	sv = jpomFixture("/old/jpom/jar")
	sv.DeployConfig["deployment_transport"] = "agent"
	if validateJpomJava(sv) == nil {
		t.Fatal("unsupported Agent transport silently accepted")
	}
	// The frozen execution path rechecks the current environment purpose.
	f := filepath.Join(t.TempDir(), "app.jar")
	w, e := os.Create(f)
	if e != nil {
		t.Fatal(e)
	}
	z := zip.NewWriter(w)
	entry, _ := z.Create("META-INF/MANIFEST.MF")
	entry.Write([]byte("Manifest-Version: 1.0\n"))
	z.Close()
	w.Close()
	a, e := describeArtifact("api", "v", ArtifactJar, f, "")
	if e != nil {
		t.Fatal(e)
	}
	sv = jpomFixture("/old/jpom/jar")
	if _, e = s.db.Exec(`UPDATE environments SET purpose='production' WHERE name='test'`); e != nil {
		t.Fatal(e)
	}
	sv.RuntimeEnvironment = "test"
	d := NewDeployer(s, NewLogHub(), "")
	if d.validateRelease(sv, Host{Name: "prod-host", Addr: "192.0.2.1"}, a) == nil {
		t.Fatal("direct execution bypassed production restriction")
	}
}
