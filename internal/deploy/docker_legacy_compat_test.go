package deploy

import (
	"strings"
	"testing"
	"time"
)

func TestDockerLegacyRunAndLogMountPermissions(t *testing.T) {
	sv := &Service{Name: "estate-ai-seek", RuntimeEnvFile: "/tmp/private.env", DeployConfig: map[string]string{
		"network": "host", "host_ports": "8889,20881",
		"mounts": `[{"source":"/srv/models","target":"/models/bge","read_only":true},{"source":"/srv/dockerlog","target":"/app/logs/estate-ai-seek","read_only":false}]`,
	}}
	if _, err := parseDockerRuntime(sv.DeployConfig); err != nil {
		t.Fatal(err)
	}
	cmd := dockerRunCommand(sv.Name, "sha256:"+strings.Repeat("b", 64), sv)
	if strings.Contains(cmd, "--pull") {
		t.Fatal("unsupported legacy flag", cmd)
	}
	for _, want := range []string{"--network 'host'", "src=/srv/models,dst=/models/bge,readonly'", "src=/srv/dockerlog,dst=/app/logs/estate-ai-seek'", "--env-file '/tmp/private.env'", "'sha256:" + strings.Repeat("b", 64) + "'"} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("missing %s: %s", want, cmd)
		}
	}
	for _, cfg := range []map[string]string{
		{"mounts": `[{"source":"/srv/logs","target":"/app/logs","read_only":true}]`},
		{"run_args": "--mount type=bind,src=/srv/logs,dst=/app/logs/app,readonly"},
		{"run_args": "-v /srv/logs:/app/logs/app:ro"},
	} {
		var commands []string
		status := deployDockerContainer(1, &Service{Name: "app", DeployConfig: cfg}, Artifact{Kind: ArtifactDockerArchive}, dockerHostIO{
			emit: func(string) {}, exec: func(cmd string, _ time.Duration) (string, error) { commands = append(commands, cmd); return "", nil },
		})
		if status != "failed" || len(commands) != 0 {
			t.Fatalf("readonly logs reached remote side effects: %s %v", status, commands)
		}
	}
}
