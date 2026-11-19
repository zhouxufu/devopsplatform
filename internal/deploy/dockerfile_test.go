package deploy

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManagedDockerfileValidation(t *testing.T) {
	lib := NewStepLib("../../steps")
	for _, test := range []struct {
		source, content, file string
		valid                 bool
	}{
		{"repository", "", "Dockerfile", true}, {"platform", "FROM example\n", "", true},
		{"platform", " \n", "", false}, {"platform", strings.Repeat("a", (32<<10)+1), "", false},
		{"platform", "FROM x\x00", "", false}, {"unknown", "FROM x", "Dockerfile", false},
		{"repository", "FROM x", "-", false},
	} {
		err := lib.ValidateBuildPipeline([]StepInst{{Step: "docker-build", Params: map[string]string{"DOCKERFILE_SOURCE": test.source, "DOCKERFILE_CONTENT": test.content, "DOCKERFILE": test.file}}})
		if (err == nil) != test.valid {
			t.Fatalf("source=%q valid=%v err=%v", test.source, test.valid, err)
		}
	}
}

func TestDockerfileBaseTemplates(t *testing.T) {
	def := NewStepLib("../../steps").Get("docker-build")
	for _, name := range []string{"java21", "nginx"} {
		params := map[string]string{"DOCKERFILE_SOURCE": "template", "DOCKERFILE_TEMPLATE": name}
		if err := validateDockerfileParams(params); err != nil {
			t.Fatal(err)
		}
		resolved := resolvedDockerfileParams(params)
		if resolved["DOCKERFILE_CONTENT"] == "" || resolved["DOCKERFILE_TEMPLATE_VERSION"] != "1" {
			t.Fatal(resolved)
		}
		env := def.BuildEnv(StepInst{Params: params}, "/fixture", "fixture", "v1", "/fixture-output")
		found := false
		for _, v := range env {
			if v == "LP_DOCKERFILE_CONTENT="+resolved["DOCKERFILE_CONTENT"] {
				found = true
			}
		}
		if !found {
			t.Fatal("template not passed to build")
		}
		if dockerfileBuildMetadata(params)["dockerfile_source"] != "template" {
			t.Fatal("template identity missing")
		}
	}
	for _, params := range []map[string]string{
		{"DOCKERFILE_SOURCE": "template", "DOCKERFILE_TEMPLATE": "unknown"},
		{"DOCKERFILE_SOURCE": "template", "DOCKERFILE_ARTIFACT_PATH": "../outside.jar"},
		{"DOCKERFILE_SOURCE": "template", "DOCKERFILE_TEMPLATE_VERSION": "999"},
	} {
		if validateDockerfileParams(params) == nil {
			t.Fatal("accepted invalid template")
		}
	}
}

func TestDockerfileTemplateSnapshotFreezesParameters(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sv := Service{Name: "template-fixture", ArtifactType: "image", ImageName: "fixture", GitURL: "https://example.invalid/app.git", BuildTool: "command", Steps: []StepInst{{Step: "docker-build", Params: map[string]string{"DOCKERFILE_SOURCE": "template", "DOCKERFILE_TEMPLATE": "java21", "DOCKERFILE_ARTIFACT_PATH": "build/original.jar"}}}}
	if err = st.UpsertService(sv); err != nil {
		t.Fatal(err)
	}
	sv = *st.GetService(sv.Name)
	run := &PipelineRun{Service: sv.Name, StartedAt: time.Now().Unix()}
	if err = st.CreateRun(run); err != nil {
		t.Fatal(err)
	}
	if err = st.saveBuildSnapshot(run.ID, &sv, nil); err != nil {
		t.Fatal(err)
	}
	if sv.Steps[0].Params["DOCKERFILE_CONTENT"] != "" {
		t.Fatal("snapshot mutated app configuration")
	}
	st.db.Exec(`UPDATE build_run_snapshots SET commit_ref=? WHERE run_id=?`, strings.Repeat("b", 40), run.ID)
	sv.Steps[0].Params["DOCKERFILE_ARTIFACT_PATH"] = "build/new.jar"
	st.UpsertService(sv)
	replayed, _, _, err := st.buildReplay(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	p := replayed.Steps[0].Params
	if p["DOCKERFILE_TEMPLATE_VERSION"] != "1" || !strings.Contains(p["DOCKERFILE_CONTENT"], "build/original.jar") {
		t.Fatal("template snapshot not frozen")
	}
	if resolvedDockerfileParams(p)["DOCKERFILE_CONTENT"] != p["DOCKERFILE_CONTENT"] {
		t.Fatal("retry template content drift")
	}
}

func TestManagedDockerfileSnapshotReplay(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sv := Service{Name: "dockerfile-fixture", ArtifactType: "image", ImageName: "fixture", GitURL: "https://example.invalid/app.git", BuildTool: "command", Steps: []StepInst{{Step: "docker-build", Params: map[string]string{"DOCKERFILE_SOURCE": "platform", "DOCKERFILE_CONTENT": "FROM original\n"}}}}
	if err = st.UpsertService(sv); err != nil {
		t.Fatal(err)
	}
	sv = *st.GetService(sv.Name)
	run := &PipelineRun{Service: sv.Name, Actor: "fixture", StartedAt: time.Now().Unix()}
	if err = st.CreateRun(run); err != nil {
		t.Fatal(err)
	}
	if err = st.saveBuildSnapshot(run.ID, &sv, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = st.db.Exec(`UPDATE build_run_snapshots SET commit_ref=? WHERE run_id=?`, strings.Repeat("a", 40), run.ID); err != nil {
		t.Fatal(err)
	}
	sv.Steps[0].Params["DOCKERFILE_CONTENT"] = "FROM changed\n"
	if err = st.UpsertService(sv); err != nil {
		t.Fatal(err)
	}
	replayed, _, _, err := st.buildReplay(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	params := replayed.Steps[0].Params
	if params["DOCKERFILE_CONTENT"] != "FROM original\n" {
		t.Fatal("retry used current Dockerfile")
	}
	meta := dockerfileBuildMetadata(params)
	if meta["dockerfile_source"] != "platform" || meta["dockerfile_digest"] == dockerfileBuildMetadata(sv.Steps[0].Params)["dockerfile_digest"] {
		t.Fatal("missing immutable Dockerfile identity")
	}
}

func TestManagedDockerfileBuildUsesStdinAndPreservesCheckout(t *testing.T) {
	dir := t.TempDir()
	fixture := dockerArchiveFixture(t, "amd64")
	content := `FROM example
RUN echo "$MY_VALUE" && echo '$(not-executed-by-platform)'
COPY generated/app.jar /app/app.jar`
	// Include a real repository Dockerfile; selecting platform must not alter it.
	repoFile := filepath.Join(dir, "Dockerfile")
	os.WriteFile(repoFile, []byte("FROM repository\n"), 0600)
	bin := filepath.Join(dir, "docker")
	script := `#!/bin/bash
set -eu
printf '%s\n' "$*" >> "$TEST_COMMANDS"
case "$*" in
  version*|buildx\ version*|buildx\ inspect*) exit 0;;
  buildx\ build*) cat > "$TEST_CAPTURE";;
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
	cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "LP_WORKSPACE="+dir, "LP_IMAGE_NAME=fixture", "LP_VERSION=v1", "LP_PUSH=false", "LP_DOCKERFILE_SOURCE=platform", "LP_DOCKERFILE_CONTENT="+content, "LP_OUTPUT_DIR="+output, "LP_OUTPUTS_FILE="+filepath.Join(output, ".outputs"), "TEST_FIXTURE="+fixture, "TEST_COMMANDS="+filepath.Join(dir, "commands"), "TEST_CAPTURE="+filepath.Join(dir, "captured"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	captured, _ := os.ReadFile(filepath.Join(dir, "captured"))
	if string(captured) != content {
		t.Fatalf("Dockerfile changed: %q", captured)
	}
	repo, _ := os.ReadFile(repoFile)
	if string(repo) != "FROM repository\n" {
		t.Fatal("checkout overwritten")
	}
	commands, _ := os.ReadFile(filepath.Join(dir, "commands"))
	if !strings.Contains(string(commands), "--file -") || strings.Contains(string(out), "MY_VALUE") {
		t.Fatal(fmt.Sprintf("bad command or leaked contents: %s %s", commands, out))
	}
}

func TestTemplateDockerfileBuildUsesStdin(t *testing.T) {
	def := NewStepLib("../../steps").Get("docker-build")
	for _, template := range []string{"java21", "nginx"} {
		for _, push := range []string{"true", "false"} {
			t.Run(template+"/push="+push, func(t *testing.T) {
				dir := t.TempDir()
				params := map[string]string{"DOCKERFILE_SOURCE": "template", "DOCKERFILE_TEMPLATE": template, "IMAGE_NAME": "fixture", "PUSH": push, "CONTEXT": "."}
				content, err := dockerfileTemplate(params)
				if err != nil {
					t.Fatal(err)
				}
				// Fake Docker only: no daemon, registry, image build or publication.
				script := `#!/bin/bash
set -eu
printf '%s\n' "$*" >> "$TEST_COMMANDS"
case "$*" in
  version*|buildx\ version*|buildx\ inspect*) exit 0;;
  buildx\ build*)
    [[ " $* " == *" --file - "* ]] || exit 41
    cat > "$TEST_CAPTURE"
    [ -s "$TEST_CAPTURE" ] || { echo 'empty Dockerfile on stdin' >&2; exit 42; };;
  buildx\ imagetools\ inspect*) printf 'Digest: sha256:fixture\n';;
  image\ save*) printf 'fixture archive' > "$4";;
  *) echo unexpected-command >&2; exit 43;;
esac
`
				for name, body := range map[string]string{"docker": script, "Dockerfile": "FROM repository\n", ".dockerignore": "private/\n"} {
					if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0700); err != nil {
						t.Fatal(err)
					}
				}
				output := filepath.Join(dir, "outputs")
				if err := os.Mkdir(output, 0700); err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command("bash", "../../steps/docker-build/run.sh")
				cmd.Env = append(os.Environ(), def.BuildEnv(StepInst{Params: params}, dir, "fixture", "v1", output)...)
				cmd.Env = append(cmd.Env, "PATH="+dir+":"+os.Getenv("PATH"), "LP_OUTPUTS_FILE="+filepath.Join(output, ".outputs"), "TEST_COMMANDS="+filepath.Join(dir, "commands"), "TEST_CAPTURE="+filepath.Join(dir, "captured"), "DOCKER_REGISTRY=", "DOCKER_USERNAME=", "DOCKER_PASSWORD=")
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("%v %s", err, out)
				}
				captured, err := os.ReadFile(filepath.Join(dir, "captured"))
				if err != nil || string(captured) != content {
					t.Fatalf("template stdin mismatch: %v", err)
				}
				for name, want := range map[string]string{"Dockerfile": "FROM repository\n", ".dockerignore": "private/\n"} {
					got, err := os.ReadFile(filepath.Join(dir, name))
					if err != nil || string(got) != want {
						t.Fatalf("checkout file %s changed: %v", name, err)
					}
				}
				commands, _ := os.ReadFile(filepath.Join(dir, "commands"))
				flag := "--load ."
				if push == "true" {
					flag = "--push ."
				}
				if !strings.Contains(string(commands), flag) || strings.Contains(string(out), "FROM ") {
					t.Fatal("wrong output mode or leaked Dockerfile content")
				}
			})
		}
	}
}
