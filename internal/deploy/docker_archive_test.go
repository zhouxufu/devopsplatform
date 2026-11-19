package deploy

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func dockerArchiveFixture(t *testing.T, arch string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "image.tar")
	config := []byte(fmt.Sprintf(`{"os":"linux","architecture":%q,"rootfs":{"type":"layers","diff_ids":[]}}`, arch))
	h := sha256.Sum256(config)
	name := hex.EncodeToString(h[:]) + ".json"
	manifest, _ := json.Marshal([]map[string]any{{"Config": name, "RepoTags": []string{"app:example"}, "Layers": []string{"layer/layer.tar"}}})
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	w := tar.NewWriter(f)
	for _, entry := range []struct {
		name string
		data []byte
	}{{name, config}, {"manifest.json", manifest}, {"layer/layer.tar", []byte("layer")}} {
		if err := w.WriteHeader(&tar.Header{Name: entry.name, Mode: 0600, Size: int64(len(entry.data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestDockerArchiveIdentityAndIntegrity(t *testing.T) {
	a, err := describeArtifact("app", "v1", ArtifactDockerArchive, dockerArchiveFixture(t, "amd64"), "")
	if err != nil {
		t.Fatal(err)
	}
	if a.Size == 0 || !strings.HasPrefix(a.Digest, "sha256:") || a.Metadata["architecture"] != "amd64" || a.Metadata["image_id"] == a.Digest {
		t.Fatalf("bad identity: %#v", a)
	}
	if !artifactMatchesService(a.Kind, "image") || artifactMatchesService(a.Kind, "jar") {
		t.Fatal("bad service compatibility")
	}
	if err := (dockerHostDriver{}).Validate(&Service{}, Host{}, a); err != nil {
		t.Fatal(err)
	}
	if err := (aliyunSAEDriver{}).Validate(&Service{}, Host{}, a); err == nil {
		t.Fatal("SAE accepted offline archive")
	}
	copy := a
	copy.Metadata = map[string]string{"image_id": "sha256:wrong"}
	if validateDockerArchive(copy) == nil {
		t.Fatal("accepted forged metadata")
	}
	if err := os.WriteFile(a.URI, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if validateDockerArchive(a) == nil {
		t.Fatal("accepted modified archive")
	}
}

func TestDockerArchiveRejectsInvalidFiles(t *testing.T) {
	for _, data := range []string{"not a tar", ""} {
		file := filepath.Join(t.TempDir(), "bad.tar")
		os.WriteFile(file, []byte(data), 0600)
		if _, err := describeArtifact("app", "v1", ArtifactDockerArchive, file, ""); err == nil {
			t.Fatal("accepted invalid archive")
		}
	}
}

func TestDockerArchiveDeploymentSafety(t *testing.T) {
	for _, scenario := range []string{"success", "upload", "checksum", "load", "architecture", "identity", "start", "health", "no-old"} {
		t.Run(scenario, func(t *testing.T) {
			a, err := describeArtifact("app", "v1", ArtifactDockerArchive, dockerArchiveFixture(t, "amd64"), "")
			if err != nil {
				t.Fatal(err)
			}
			var commands []string
			clock := time.Now()
			io := dockerHostIO{
				now:    func() time.Time { return clock },
				pause:  func(d time.Duration) { clock = clock.Add(d) },
				emit:   func(string) {},
				health: func() bool { return scenario != "health" },
				upload: func(local, remote string) error {
					if local != a.URI || remote != "/tmp/devops-image-abcdefghij/image.tar" {
						t.Fatal("bad upload")
					}
					if scenario == "upload" {
						return fmt.Errorf("upload failed")
					}
					return nil
				},
				exec: func(cmd string, _ time.Duration) (string, error) {
					commands = append(commands, cmd)
					if out, ok := dockerCapabilityFixture(cmd); ok {
						return out, nil
					}
					if strings.Contains(cmd, "docker pull") {
						t.Fatal("offline path pulled a registry")
					}
					switch {
					case strings.HasPrefix(cmd, "docker version"):
						if scenario == "architecture" {
							return "linux/arm64", nil
						}
						return "linux/amd64", nil
					case strings.HasPrefix(cmd, "mktemp"):
						return "/tmp/devops-image-abcdefghij", nil
					case strings.Contains(cmd, "sha256sum"):
						if scenario == "checksum" {
							return "", fmt.Errorf("checksum")
						}
					case strings.HasPrefix(cmd, "docker image load"):
						if scenario == "load" {
							return "", fmt.Errorf("load")
						}
					case strings.HasPrefix(cmd, "docker image inspect"):
						if scenario == "identity" {
							return "wrong", nil
						}
						return a.Metadata["image_id"] + " linux/amd64", nil
					case strings.HasPrefix(cmd, "docker container ls"):
						if scenario == "no-old" {
							return "", nil
						}
						return "old-container", nil
					case strings.HasPrefix(cmd, "docker inspect --format"):
						if strings.Contains(cmd, "json .State") {
							return `{"Status":"running","Running":true}|0`, nil
						}
						if strings.Contains(cmd, ".RestartCount") {
							return "true 0", nil
						}
						return "true", nil
					case strings.HasPrefix(cmd, "docker run"):
						if !strings.Contains(cmd, a.Metadata["image_id"]) || strings.Contains(cmd, "--pull") {
							t.Fatal("mutable run identity")
						}
						if scenario == "start" {
							return "", fmt.Errorf("start")
						}
					}
					return "", nil
				},
			}
			sv := &Service{Name: "app", HealthURL: "http://host/ready", DeployConfig: map[string]string{"ports": "8888:8888", "run_args": "--mount type=bind,src=/models,dst=/models,readonly"}}
			status := deployDockerContainer(42, sv, a, io)
			all := strings.Join(commands, "\n")
			if scenario == "success" || scenario == "no-old" {
				if status != "success" {
					t.Fatal(status)
				}
			} else if scenario == "start" || scenario == "health" {
				if status != "rolled-back" || !strings.Contains(all, "docker start 'app'") || !strings.Contains(all, "docker rename 'app.previous-42' 'app'") {
					t.Fatalf("no config-preserving rollback: %s\n%s", status, all)
				}
			} else if status != "failed" || strings.Contains(all, "docker stop --time") || strings.Contains(all, "docker run -d") {
				t.Fatalf("unsafe failure: %s\n%s", status, all)
			}
			if scenario != "architecture" && !strings.Contains(all, "rmdir -- '/tmp/devops-image-abcdefghij'") {
				t.Fatal("temp dir not cleaned")
			}
		})
	}
}

func TestDockerArchiveBuildStepExportsWithoutRegistry(t *testing.T) {
	dir := t.TempDir()
	fixture := dockerArchiveFixture(t, "amd64")
	bin := filepath.Join(dir, "docker")
	script := `#!/bin/bash
set -eu
printf '%s\n' "$*" >> "$TEST_COMMANDS"
case "$*" in
  version*|buildx\ version*|buildx\ inspect*) exit 0;;
  buildx\ build*) exit 0;;
  image\ save*) cp "$TEST_FIXTURE" "$4";;
  *) echo unexpected-command >&2; exit 1;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "outputs")
	os.Mkdir(output, 0700)
	cmd := exec.Command("bash", "../../steps/docker-build/run.sh")
	cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "LP_WORKSPACE="+dir, "LP_IMAGE_NAME=app", "LP_VERSION=v1", "LP_PUSH=false", "LP_PLATFORMS=linux/amd64", "LP_OUTPUT_DIR="+output, "LP_OUTPUTS_FILE="+filepath.Join(output, ".outputs"), "TEST_FIXTURE="+fixture, "TEST_COMMANDS="+filepath.Join(dir, "commands"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	commands, _ := os.ReadFile(filepath.Join(dir, "commands"))
	if strings.Contains(string(commands), "--push") || strings.Contains(string(commands), "login") || !strings.Contains(string(commands), "--load") {
		t.Fatal(string(commands))
	}
	outs := readOutputs(output)
	if outs["ARTIFACT_KIND"] != ArtifactDockerArchive {
		t.Fatal(outs)
	}
	if _, err := describeArtifact("app", "v1", outs["ARTIFACT_KIND"], outs["ARTIFACT"], outs["ARTIFACT_DIGEST"]); err != nil {
		t.Fatal(err)
	}
}

func TestDockerArchiveStoredAndReleased(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a, err := describeArtifact("app", "v1", ArtifactDockerArchive, dockerArchiveFixture(t, "amd64"), "")
	if err != nil {
		t.Fatal(err)
	}
	run := &PipelineRun{Service: "app", Actor: "tester", StartedAt: time.Now().Unix()}
	if err = st.CreateRun(run); err != nil {
		t.Fatal(err)
	}
	saved, err := st.RecordPipelineArtifact(run.ID, "tester", a)
	if err != nil {
		t.Fatal(err)
	}
	loaded := st.GetArtifact(saved.ID)
	if loaded == nil || loaded.Metadata["image_id"] != a.Metadata["image_id"] || loaded.Kind != ArtifactDockerArchive {
		t.Fatal(loaded)
	}
	if err = validateDockerArchive(*loaded); err != nil {
		t.Fatal(err)
	}
}

// Opt-in read-only smoke check: export an already available local image, never
// pull, build, run, load or remove anything in the operator's Docker daemon.
func TestDockerArchiveRealLocalImage(t *testing.T) {
	image := os.Getenv("DEVOPS_DOCKER_ARCHIVE_TEST_IMAGE")
	if image == "" {
		t.Skip("set DEVOPS_DOCKER_ARCHIVE_TEST_IMAGE to an existing local image")
	}
	file := filepath.Join(t.TempDir(), "real-image.tar")
	if out, err := exec.Command("docker", "image", "save", "--output", file, image).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	a, err := describeArtifact("fixture", "v1", ArtifactDockerArchive, file, "")
	if err != nil {
		t.Fatal(err)
	}
	id, err := exec.Command("docker", "image", "inspect", "--format", "{{.Id}}", image).Output()
	if err != nil {
		t.Fatal(err)
	}
	if a.Metadata["image_id"] != strings.TrimSpace(string(id)) {
		t.Fatalf("wrong image ID: %#v", a.Metadata)
	}
}
