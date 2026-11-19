package deploy

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDockerRuntimeConfiguration(t *testing.T) {
	config := map[string]string{"network": "app-net", "ports": "8889:8889,20881:20881/tcp", "mounts": `[{"source":"/data/models/bge-v1","target":"/models/bge","read_only":true}]`, "run_args": `--memory 2g --cpus=2`}
	sv := &Service{DeployConfig: config, RuntimeEnvFile: "/tmp/private.env"}
	if _, err := parseDockerRuntime(config); err != nil {
		t.Fatal(err)
	}
	command := dockerRunCommand("app", "registry/app@sha256:fixed", sv)
	for _, want := range []string{"--network 'app-net'", "--mount 'type=bind,src=/data/models/bge-v1,dst=/models/bge,readonly'", "-p '8889:8889'", "--env-file '/tmp/private.env'", "'--memory' '2g'"} {
		if !strings.Contains(command, want) {
			t.Fatalf("missing %s: %s", want, command)
		}
	}
	for _, raw := range []string{`--mount type=bind,src=/data/models,dst=/models,readonly`, `-v '/data/my models:/models:ro'`} {
		if _, err := parseDockerRuntime(map[string]string{"run_args": raw}); err != nil {
			t.Fatal(err)
		}
	}
	if err := validateDockerHealthURL(&Service{HealthURL: "http://127.0.0.1:8889/ready", DeployConfig: map[string]string{}}); err == nil {
		t.Fatal("unpublished old-process health accepted")
	}
	if err := validateDockerHealthURL(&Service{HealthURL: "http://127.0.0.1:8889/ready", DeployConfig: config}); err != nil {
		t.Fatal(err)
	}
	for _, cfg := range []map[string]string{
		{"run_args": "--env DASHSCOPE_API_KEY=secret"}, {"run_args": "--privileged"}, {"run_args": "--network host; id"},
		{"run_args": "--mount type=volume,src=x,dst=/models"}, {"run_args": "--volume /:/host"},
		{"mounts": `[{"source":"relative","target":"/models"}]`}, {"mounts": `[{"source":"/data","target":"/models"},{"source":"/other","target":"/models"}]`},
		{"network": "host", "ports": "8889:8889"}, {"network": "host"}, {"network": "host", "host_ports": "99999"},
		{"ports": "70000:8889"}, {"ports": "8889:8889,8889:8080"}, {"startup_timeout_seconds": "0"},
	} {
		if _, err := parseDockerRuntime(cfg); err == nil {
			t.Fatalf("unsafe configuration accepted: %v", cfg)
		}
	}
}

func TestDockerPortCheckShellUsesListenersAndContainerOwnership(t *testing.T) {
	for _, scenario := range []string{"free", "other-java", "owned-bridge", "owned-host", "other-host-pid"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			docker := `#!/bin/sh
case "$*" in
  'container ls -a'*) if [ "$CASE" = owned-bridge ] || [ "$CASE" = owned-host ] || [ "$CASE" = other-host-pid ]; then echo abc123; fi ;;
  port*) if [ "$CASE" = owned-bridge ]; then echo 0.0.0.0:8889; fi ;;
  'container ls -q'*) if [ "$CASE" = owned-bridge ]; then echo abc123; fi ;;
  inspect*) echo host;;
  top*) printf 'PID\n100\n';;
  *) exit 9;;
esac
`
			ss := `#!/bin/sh
case "$CASE" in
free) ;;
owned-host) echo 'LISTEN 0 128 0.0.0.0:8889 0.0.0.0:* users:(("java",pid=100,fd=9))';;
*) echo 'LISTEN 0 128 0.0.0.0:8889 0.0.0.0:* users:(("java",pid=999,fd=9))';;
esac
`
			for name, body := range map[string]string{"docker": docker, "ss": ss} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0700); err != nil {
					t.Fatal(err)
				}
			}
			cfg := map[string]string{"ports": "8889:8889"}
			if strings.Contains(scenario, "host") {
				cfg = map[string]string{"network": "host", "host_ports": "8889"}
			}
			io := dockerHostIO{emit: func(string) {}, exec: func(command string, _ time.Duration) (string, error) {
				cmd := exec.Command("bash", "-c", command)
				cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "CASE="+scenario)
				out, err := cmd.CombinedOutput()
				return string(out), err
			}}
			ok := dockerPreflight("app", &Service{DeployConfig: cfg}, io)
			if want := scenario != "other-java" && scenario != "other-host-pid"; ok != want {
				t.Fatalf("%s preflight=%t", scenario, ok)
			}
		})
	}
}

func TestDockerPreflightStopsBeforeMutations(t *testing.T) {
	for _, scenario := range []string{"missing-mount", "port-conflict", "network-missing"} {
		t.Run(scenario, func(t *testing.T) {
			commands := []string{}
			io := dockerHostIO{emit: func(string) {}, exec: func(cmd string, _ time.Duration) (string, error) {
				commands = append(commands, cmd)
				if scenario == "missing-mount" && strings.Contains(cmd, "test -e") || scenario == "port-conflict" && strings.Contains(cmd, "command -v ss") || scenario == "network-missing" && strings.Contains(cmd, "network inspect") {
					return "validation failed", errors.New("preflight")
				}
				return "", nil
			}}
			sv := &Service{Name: "app", DeployConfig: map[string]string{"network": "app-net", "ports": "8889:8889", "mounts": `[{"source":"/models/bge","target":"/models","read_only":true}]`}}
			if status := deployDockerContainer(1, sv, Artifact{Kind: ArtifactOCI, URI: "image@sha256:fixture"}, io); status != "failed" {
				t.Fatal(status)
			}
			for _, cmd := range commands {
				for _, mutate := range []string{"docker pull", "docker stop", "docker rename", "docker run", "docker rm"} {
					if strings.Contains(cmd, mutate) {
						t.Fatal("mutation before failed preflight", cmd)
					}
				}
			}
		})
	}
}

func TestDockerStartupDetectsExitRestartOOMAndTimeout(t *testing.T) {
	for _, scenario := range []string{"success", "exit", "restart", "oom", "unhealthy", "created-timeout", "http-timeout", "late-exit"} {
		t.Run(scenario, func(t *testing.T) {
			clock := time.Now()
			stateReads := 0
			logs := strings.Builder{}
			sv := &Service{DeployConfig: map[string]string{"startup_timeout_seconds": "5", "startup_observe_seconds": "1"}}
			if scenario == "http-timeout" || scenario == "late-exit" {
				sv.HealthURL = "http://host:8889/ready"
			}
			io := dockerHostIO{now: func() time.Time { return clock }, pause: func(d time.Duration) { clock = clock.Add(d) }, emit: func(s string) { logs.WriteString(s) }, health: func() bool { return scenario != "http-timeout" }, exec: func(cmd string, _ time.Duration) (string, error) {
				if strings.HasPrefix(cmd, "docker logs") {
					return "DashScope embedding is enabled but DASHSCOPE_API_KEY is not configured", nil
				}
				if strings.Contains(cmd, "json .State") {
					stateReads++
					switch scenario {
					case "exit":
						return `{"Status":"exited","ExitCode":1}|0`, nil
					case "restart":
						return `{"Status":"running","Running":true}|1`, nil
					case "oom":
						return `{"Status":"exited","OOMKilled":true,"ExitCode":137}|0`, nil
					case "unhealthy":
						return `{"Status":"running","Running":true,"Health":{"Status":"unhealthy"}}|0`, nil
					case "created-timeout":
						return `{"Status":"created"}|0`, nil
					case "late-exit":
						if stateReads > 1 {
							return `{"Status":"restarting","Restarting":true}|1`, nil
						}
					}
					return `{"Status":"running","Running":true}|0`, nil
				}
				if scenario == "late-exit" {
					return "false 1", nil
				}
				return "true 0", nil
			}}
			ok := dockerStartupCheck("app", sv, io)
			if ok != (scenario == "success") {
				t.Fatalf("%s %t: %s", scenario, ok, logs.String())
			}
			if !ok && !strings.Contains(logs.String(), "DASHSCOPE_API_KEY is not configured") {
				t.Fatal("startup cause missing", logs.String())
			}
			if scenario == "restart" && stateReads != 1 {
				t.Fatal("restart loop not detected immediately")
			}
		})
	}
}

func TestDeployCompletionCountsAndTimeout(t *testing.T) {
	for _, status := range []string{"success", "failed", "canceled", "timed_out", "rolled-back"} {
		t.Run(status, func(t *testing.T) {
			s, _, req := deliveryFixture(t)
			o := &ReleaseOrder{Service: "orders", Environment: "test", Status: "running", Hosts: []string{"test-1"}, Target: *s.GetServiceEnvironment("orders", "test")}
			if err := s.CreateReleaseOrder(o); err != nil {
				t.Fatal(err)
			}
			d := &Deploy{Service: "orders", Host: "test-1", Environment: "test", Version: fmt.Sprint(req.VersionID)}
			if err := s.CreateDeploy(d); err != nil {
				t.Fatal(err)
			}
			if err := s.AttachReleaseDeploys(o.ID, 1, []int64{d.ID}); err != nil {
				t.Fatal(err)
			}
			s.AppendDeployLog(d.ID, "retained diagnostic")
			if err := s.FinishDeploy(d.ID, status); err != nil {
				t.Fatal(err)
			}
			s.SetReleaseStatus(o.ID, map[bool]string{true: "success", false: "failed"}[status == "success"], "", true)
			got := s.GetReleaseOrder(o.ID)
			dp := s.GetDeploy(d.ID)
			if got.Succeeded+got.Failed != 1 || got.Total != 1 || dp.Status != status || dp.EndedAt == 0 || got.EndedAt == 0 {
				t.Fatalf("inconsistent order=%+v deploy=%+v", got, dp)
			}
			if status == "success" && got.Succeeded != 1 || status != "success" && got.Failed != 1 {
				t.Fatal(got)
			}
			if err := s.FinishDeploy(d.ID, "success"); !errors.Is(err, ErrRunConflict) {
				t.Fatal("late worker overwrote terminal result", err)
			}
			var log string
			s.db.QueryRow(`SELECT log FROM deploys WHERE id=?`, d.ID).Scan(&log)
			if !strings.Contains(log, "retained diagnostic") {
				t.Fatal("diagnostic lost")
			}
		})
	}
	t.Run("monitor-timeout", func(t *testing.T) {
		s, m, _ := deliveryFixture(t)
		m.monitorTimeout = time.Nanosecond
		o := &ReleaseOrder{Service: "orders", Environment: "test", Status: "running", Hosts: []string{"test-1"}, Target: *s.GetServiceEnvironment("orders", "test")}
		if err := s.CreateReleaseOrder(o); err != nil {
			t.Fatal(err)
		}
		d := &Deploy{Service: "orders", Host: "test-1"}
		if err := s.CreateDeploy(d); err != nil {
			t.Fatal(err)
		}
		if err := s.AttachReleaseDeploys(o.ID, 1, []int64{d.ID}); err != nil {
			t.Fatal(err)
		}
		m.monitorBatch(o.ID, []int64{d.ID}, 1)
		if got := s.GetReleaseOrder(o.ID); got.Status != "failed" || got.Failed != 1 || got.EndedAt == 0 {
			t.Fatalf("timeout lost counts: %+v", got)
		}
		if got := s.GetDeploy(d.ID); got.Status != "timed_out" || got.EndedAt == 0 {
			t.Fatal(got)
		}
	})
	t.Run("unknown-result-cannot-pass-threshold", func(t *testing.T) {
		s, m, _ := deliveryFixture(t)
		o := &ReleaseOrder{Service: "orders", Environment: "test", Status: "running", Hosts: []string{"test-1"}, FailureThreshold: 100, Target: *s.GetServiceEnvironment("orders", "test")}
		if err := s.CreateReleaseOrder(o); err != nil {
			t.Fatal(err)
		}
		d := &Deploy{Service: "orders", Host: "test-1"}
		if err := s.CreateDeploy(d); err != nil {
			t.Fatal(err)
		}
		if err := s.AttachReleaseDeploys(o.ID, 1, []int64{d.ID}); err != nil {
			t.Fatal(err)
		}
		if err := s.FinishDeploy(d.ID, "timed_out"); err != nil {
			t.Fatal(err)
		}
		m.monitorBatch(o.ID, []int64{d.ID}, 1)
		if got := s.GetReleaseOrder(o.ID); got.Status != "failed" || got.Failed != 1 {
			t.Fatal("unknown outcome passed failure threshold", got)
		}
	})
}

func TestDeployLogRedactsBeforeStreamingAndPersistence(t *testing.T) {
	s, _, _ := deliveryFixture(t)
	d := NewDeployer(s, NewLogHub(), "")
	d.logSecrets = map[int64][]string{99: {"actual-private-value"}}
	d.emit(99, "DASHSCOPE_API_KEY=actual-private-value token=unexpected-secret\nmissing DASHSCOPE_API_KEY is not configured")
	text := d.logs[99].String()
	if strings.Contains(text, "actual-private-value") || strings.Contains(text, "unexpected-secret") || !strings.Contains(text, "not configured") {
		t.Fatal(text)
	}
}

// Execute generated arguments against a fake docker, never a real daemon.
func TestDockerRuntimeActualArgv(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "docker")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	sv := &Service{DeployConfig: map[string]string{"network": "app-net", "mounts": `[{"source":"/data/my models","target":"/models","read_only":true}]`}, RuntimeEnvFile: "/tmp/private.env"}
	cmd := exec.Command("bash", "-c", dockerRunCommand("app", "image@sha256:fixed", sv))
	cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "--mount\ntype=bind,src=/data/my models,dst=/models,readonly\n") {
		t.Fatal(string(out))
	}
	// Public configuration carries only runtime names, not values.
	s, _, _ := deliveryFixture(t)
	if err = s.UpsertService(Service{Name: "orders", ArtifactType: "image", BuildTool: "command", BuildCmd: "true"}); err != nil {
		t.Fatal(err)
	}
	target := s.GetServiceEnvironment("orders", "test")
	target.TargetType = "docker-host"
	target.DeployConfig = map[string]string{"network": "app-net", "ports": "18889:8889", "mounts": `[{"source":"/data/my models","target":"/models","read_only":true}]`}
	target.RuntimeEnv = map[string]string{"DASHSCOPE_API_KEY": "private-key", "JAVA_TOOL_OPTIONS": "-Xmx1g"}
	if err = s.UpsertServiceEnvironment(*target); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(s.GetServiceEnvironment("orders", "test"))
	if strings.Contains(string(b), "private-key") || strings.Contains(string(b), "-Xmx1g") {
		t.Fatal("runtime values returned")
	}
	saved := s.GetServiceEnvironment("orders", "test")
	values, err := s.decryptRuntimeEnvironment(saved.runtimeEnvEncrypted)
	if err != nil {
		t.Fatal(err)
	}
	if values["DASHSCOPE_API_KEY"] != "private-key" || values["JAVA_TOOL_OPTIONS"] != "-Xmx1g" {
		t.Fatal("runtime values not preserved")
	}
	applied := saved.Apply(s.GetService("orders"))
	applied.RuntimeEnvFile = "/tmp/private.env"
	command := dockerRunCommand("orders", "image@sha256:fixed", applied)
	for _, expected := range []string{"--network 'app-net'", "-p '18889:8889'", "--mount 'type=bind,src=/data/my models,dst=/models,readonly'", "--env-file '/tmp/private.env'"} {
		if !strings.Contains(command, expected) {
			t.Fatal("saved target not applied", command)
		}
	}
	if strings.Contains(command, "private-key") || strings.Contains(command, "-Xmx1g") {
		t.Fatal("runtime values placed in command")
	}
}
