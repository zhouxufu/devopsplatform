package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestImageApplicationAPISavesActualJavaCommand(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	mux := Routes(st, nil, NewLogHub(), "", NewStepLib("../../steps"))
	app := Service{Name: "image-api-fixture", ArtifactType: "image", BuildTool: "gradle", GradleExecutable: "gradle", GradleTasks: "clean bootJar", ImageName: "fixture", ArtifactGlob: "build/libs/*.jar", Steps: []StepInst{{Step: "git-checkout"}, {Step: "java-build", Params: map[string]string{"COMMAND": ""}}, {Step: "docker-build", Params: map[string]string{"DOCKERFILE_SOURCE": "template", "DOCKERFILE_TEMPLATE": "java21", "DOCKERFILE_ARTIFACT_PATH": "build/libs/*.jar"}}}}
	raw, _ := json.Marshal(app)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/services", bytes.NewReader(raw)))
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	got := st.GetService(app.Name)
	if got == nil || len(got.Steps) != 3 || !strings.Contains(got.Steps[1].Params["COMMAND"], "bootJar") || got.Steps[1].Params["COMMAND"] != got.BuildCmd {
		t.Fatal("saved image lost the actual Java command", got)
	}
}

func TestImagePipelineStopsBeforeDockerOnCompilerOrInputFailure(t *testing.T) {
	for _, mode := range []string{"compiler-failed", "missing-output", "compiled"} {
		t.Run(mode, func(t *testing.T) {
			st, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			workspace, bin := t.TempDir(), t.TempDir()
			marker := filepath.Join(bin, "called")
			// Fake engine: reads only the synthetic input, then deliberately fails.
			// No real Java compiler, Docker daemon, registry or publication is used.
			fake := "#!/bin/bash\nset -eu\ncase \"$*\" in version*|'buildx version'*|'buildx inspect'*) exit 0;; esac\ncontext=\"${!#}\"\n[ -s \"$context/app.jar\" ] || exit 41\ncat >/dev/null\nprintf called >" + shellQuote(marker) + "\nexit 49\n"
			if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(fake), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			command := "false"
			if mode == "missing-output" {
				command = "true"
			} else if mode == "compiled" {
				command = "mkdir -p target; printf simulated-jar >target/app.jar"
			}
			sv := &Service{Name: "fixture", Workdir: workspace, ArtifactType: "image", ImageName: "fixture", Steps: []StepInst{{Step: "java-build", Params: map[string]string{"COMMAND": command}}, {Step: "docker-build", Params: map[string]string{"DOCKERFILE_SOURCE": "template", "DOCKERFILE_TEMPLATE": "java21", "DOCKERFILE_ARTIFACT_PATH": "target/*.jar", "IMAGE_NAME": "fixture", "PUSH": "false"}}}}
			run := &PipelineRun{Service: sv.Name, Actor: "local"}
			if err := st.CreateRun(run); err != nil {
				t.Fatal(err)
			}
			hub := NewLogHub()
			pr := NewPipelineRunner(st, NewBuilder(st, t.TempDir(), hub), hub, nil, NewStepLib("../../steps"))
			pr.runContext(context.Background(), run.ID, sv, false)
			_, err = os.Stat(marker)
			if (err == nil) != (mode == "compiled") {
				t.Fatal("Docker must only be reached after successful compilation and fresh input", mode, err)
			}
			if got := st.GetRun(run.ID); got.Status != "failed" {
				t.Fatal("failed compiler / missing input / fake engine failure must stop the pipeline", got)
			}
		})
	}
}

func TestJavaImageCompilationOrderAndStructuredTools(t *testing.T) {
	lib := NewStepLib("../../steps")
	for _, tool := range []string{"maven", "gradle"} {
		sv := Service{Name: "fixture", ArtifactType: "image", ImageName: "fixture", BuildTool: tool}
		if err := prepareBuildConfig(&sv); err != nil {
			t.Fatal(tool, err)
		}
		steps := ExpandTemplate(sv)
		if len(steps) != 3 || steps[1].Step != "java-build" || steps[2].Step != "docker-build" || steps[1].Params["COMMAND"] == "" {
			t.Fatal(tool, steps)
		}
		if err := lib.ValidateBuildPipeline(steps); err != nil {
			t.Fatal(err)
		}
	}
	docker := StepInst{Step: "docker-build", Params: map[string]string{"DOCKERFILE_SOURCE": "template", "DOCKERFILE_TEMPLATE": "java21", "DOCKERFILE_ARTIFACT_PATH": "target/*.jar"}}
	for _, steps := range [][]StepInst{{docker}, {docker, {Step: "java-build"}}, {{Step: "java-build"}, {Step: "git-checkout"}, docker}, {{Step: "npm-build"}, docker}} {
		if err := lib.ValidateBuildPipeline(steps); err == nil {
			t.Fatal("missing or invalid Java input producer accepted", steps)
		}
	}
	if err := lib.ValidateBuildPipeline([]StepInst{{Step: "docker-build", Params: map[string]string{"DOCKERFILE_SOURCE": "repository", "DOCKERFILE": "Dockerfile"}}}); err != nil {
		t.Fatal("self-contained multi-stage Dockerfile must not require host compilation", err)
	}
	// An advanced Java command is not replaced by an empty service field.
	sv, err := serviceForPipelineRun(&Service{ArtifactType: "image", BuildTool: "command", Steps: []StepInst{{Step: "java-build", Params: map[string]string{"COMMAND": "./gradlew clean bootJar"}}}}, PipelineRunOptions{})
	if err != nil || sv.Steps[0].Params["COMMAND"] != "./gradlew clean bootJar" {
		t.Fatal(sv, err)
	}
}

func TestDockerCompilationRequiresUniqueFreshInput(t *testing.T) {
	root, output := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "target"), 0755); err != nil {
		t.Fatal(err)
	}
	jar := filepath.Join(root, "target", "app.jar")
	if err := os.WriteFile(jar, []byte("synthetic-jar"), 0644); err != nil {
		t.Fatal(err)
	}
	params := map[string]string{"DOCKERFILE_SOURCE": "template", "DOCKERFILE_TEMPLATE": "java21", "DOCKERFILE_ARTIFACT_PATH": "target/*.jar", "CONTEXT": "."}
	started := map[string]time.Time{"java-build": time.Now().Add(-time.Second)}
	resolved, metadata, cleanup, err := prepareDockerCompilation(params, root, output, started)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	got, err := os.ReadFile(filepath.Join(resolved["CONTEXT"], "app.jar"))
	if err != nil || string(got) != "synthetic-jar" || params["CONTEXT"] != "." || params["DOCKERFILE_ARTIFACT_PATH"] != "target/*.jar" || !strings.HasPrefix(metadata["compiled_input_digest"], "sha256:") {
		t.Fatal(resolved, metadata, err)
	}
	if _, _, _, err := prepareDockerCompilation(params, root, output, nil); err == nil {
		t.Fatal("missing successful producer accepted")
	}
	if err := os.WriteFile(filepath.Join(root, "target", "extra.jar"), []byte("second"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := prepareDockerCompilation(params, root, output, started); err == nil {
		t.Fatal("ambiguous JARs accepted")
	}
	params["DOCKERFILE_ARTIFACT_PATH"] = "target/app.jar"
	old := time.Now().Add(-time.Hour)
	os.Chtimes(jar, old, old)
	if _, _, _, err := prepareDockerCompilation(params, root, output, started); err == nil {
		t.Fatal("stale JAR accepted")
	}
	params["DOCKERFILE_ARTIFACT_PATH"] = "target/missing.jar"
	if _, _, _, err := prepareDockerCompilation(params, root, output, started); err == nil {
		t.Fatal("missing JAR accepted")
	}
	outside := filepath.Join(t.TempDir(), "private.jar")
	os.WriteFile(outside, []byte("outside"), 0644)
	os.Symlink(outside, filepath.Join(root, "escape.jar"))
	params["DOCKERFILE_ARTIFACT_PATH"] = "escape.jar"
	if _, _, _, err := prepareDockerCompilation(params, root, output, started); err == nil {
		t.Fatal("escaping symlink accepted")
	}
	params["DOCKERFILE_ARTIFACT_PATH"] = "../private.jar"
	if _, _, _, err := prepareDockerCompilation(params, root, output, started); err == nil {
		t.Fatal("escaping path accepted")
	}
}

func TestImageCompilationContainerDoesNotReceiveDockerSocket(t *testing.T) {
	sv := &Service{ArtifactType: "image", BuildTool: "maven", BuildEnvironment: &BuildEnvironment{Mode: "container", Preset: "maven-jdk21"}}
	if err := validateBuildEnvironment(sv.BuildEnvironment, sv); err != nil {
		t.Fatal(err)
	}
	def := NewStepLib("../../steps").Get("java-build")
	root, output := t.TempDir(), t.TempDir()
	args, _, err := containerBuildArgs(sv, def, def.BuildEnv(StepInst{Params: map[string]string{"COMMAND": "echo simulated"}}, root, "fixture", "v1", output), t.TempDir(), "fixture", "maven:3.9.9-eclipse-temurin-21")
	if err != nil || strings.Contains(strings.Join(args, " "), "docker.sock") || strings.Contains(strings.Join(args, " "), "--privileged") {
		t.Fatal(args, err)
	}
}
