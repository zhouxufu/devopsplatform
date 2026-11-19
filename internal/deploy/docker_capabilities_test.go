package deploy

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const dockerCompatibleVersion = `{"Client":{"Version":"19.03.15","ApiVersion":"1.40"},"Server":{"Version":"19.03.15","ApiVersion":"1.40","MinAPIVersion":"1.12","Os":"linux"}}`
const dockerCompatibleHelp = "--detach --restart --name --env-file --mount --network --publish --time --format --input --memory --cpus --init --read-only -m, --user -u, --hostname"

func dockerCapabilityFixture(cmd string) (string, bool) {
	if cmd == dockerVersionProbe {
		return dockerCompatibleVersion, true
	}
	if strings.HasSuffix(cmd, " --help") {
		return dockerCompatibleHelp, true
	}
	return "", false
}

func TestDockerCapabilitiesVersionsAndHelp(t *testing.T) {
	for _, tc := range []struct {
		name, version, missing string
		connectionError, want  bool
	}{
		{name: "old-with-required-features", version: dockerCompatibleVersion, want: true},
		{name: "new", version: strings.ReplaceAll(strings.ReplaceAll(dockerCompatibleVersion, "19.03.15", "29.0.0"), "1.40", "1.52"), want: true},
		{name: "old-client-new-server", version: strings.Replace(dockerCompatibleVersion, `"ApiVersion":"1.40"`, `"ApiVersion":"1.24"`, 1)},
		{name: "old-server", version: strings.ReplaceAll(dockerCompatibleVersion, "1.40", "1.24")},
		{name: "new-server-minimum", version: strings.Replace(dockerCompatibleVersion, `"MinAPIVersion":"1.12"`, `"MinAPIVersion":"1.44"`, 1)},
		{name: "non-linux", version: strings.ReplaceAll(dockerCompatibleVersion, `"linux"`, `"windows"`)},
		{name: "malformed", version: `{"Client":{}}`},
		{name: "missing-mount", version: dockerCompatibleVersion, missing: "--mount"},
		{name: "missing-env-file", version: dockerCompatibleVersion, missing: "--env-file"},
		{name: "missing-load", version: dockerCompatibleVersion, missing: "--input"},
		{name: "missing-stop-time", version: dockerCompatibleVersion, missing: "--time"},
		{name: "daemon-down", connectionError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var commands []string
			io := dockerHostIO{emit: func(string) {}, exec: func(cmd string, _ time.Duration) (string, error) {
				commands = append(commands, cmd)
				if cmd == dockerVersionProbe {
					if tc.connectionError {
						return "", errors.New("daemon unavailable")
					}
					return tc.version, nil
				}
				if strings.HasSuffix(cmd, " --help") {
					if tc.missing != "" {
						return strings.ReplaceAll(dockerCompatibleHelp, tc.missing+" ", ""), nil
					}
					return dockerCompatibleHelp, nil
				}
				t.Fatalf("unexpected side effect %s", cmd)
				return "", nil
			}}
			sv := &Service{DeployConfig: map[string]string{"mounts": `[{"source":"/srv/models","target":"/models","read_only":true}]`}}
			if got := dockerCapabilityPreflight(sv, true, io); got != tc.want {
				t.Fatalf("got %t want %t; %v", got, tc.want, commands)
			}
		})
	}
	if dockerHasFlag("--mount-point", "--mount") {
		t.Fatal("accepted partial flag")
	}
}

func TestDockerCapabilityFailureBeforeOldContainerOrImageMutation(t *testing.T) {
	for _, kind := range []string{ArtifactOCI, ArtifactDockerArchive} {
		calls := []string{}
		logs := strings.Builder{}
		status := deployDockerContainer(1, &Service{Name: "app", DeployConfig: map[string]string{}}, Artifact{Kind: kind, URI: "registry/app@sha256:fixture"}, dockerHostIO{
			emit: func(s string) { logs.WriteString(s) }, upload: func(string, string) error { t.Fatal("uploaded before capability check"); return nil },
			exec: func(cmd string, _ time.Duration) (string, error) {
				calls = append(calls, cmd)
				if cmd == dockerVersionProbe {
					return dockerCompatibleVersion, nil
				}
				if cmd == "docker run --help" {
					return "--detach --restart --name", nil
				}
				t.Fatal("side effect before rejection", cmd)
				return "", nil
			},
		})
		if status != "failed" || len(calls) != 2 || !strings.Contains(logs.String(), "--env-file") || !strings.Contains(logs.String(), "旧容器未停止") {
			t.Fatal(status, calls, logs.String())
		}
	}
}

func TestDockerBuildCapabilityFailureBeforeLoginOrBuild(t *testing.T) {
	for _, stage := range []string{"daemon", "buildx", "builder"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			commands := filepath.Join(dir, "commands")
			script := `#!/bin/bash
printf '%s\n' "$*" >> "$TEST_COMMANDS"
case "$*" in
 version*) [ "$FAIL_STAGE" != daemon ];;
 'buildx version'*) [ "$FAIL_STAGE" != buildx ];;
 'buildx inspect'*) [ "$FAIL_STAGE" != builder ];;
 *) exit 97;;
esac
`
			if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", "../../steps/docker-build/run.sh")
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "TEST_COMMANDS="+commands, "FAIL_STAGE="+stage, "LP_WORKSPACE="+dir, "LP_IMAGE_NAME=fixture", "LP_VERSION=one", "LP_PUSH=true")
			out, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(out), "✗") {
				t.Fatal("capability failure not reported", err, string(out))
			}
			data, err := os.ReadFile(commands)
			if err != nil {
				t.Fatal(err)
			}
			for _, mutation := range []string{"login", "buildx build", "imagetools", "image save"} {
				if strings.Contains(string(data), mutation) {
					t.Fatal("mutation before capability check", string(data))
				}
			}
		})
	}
}
