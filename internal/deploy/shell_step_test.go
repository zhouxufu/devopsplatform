package deploy

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestShellScriptBuildStep(t *testing.T) {
	lib := NewStepLib(filepath.Join("..", "..", "steps"))
	def := lib.Get("shell-script")
	if def == nil || def.Kind != "build" || def.Produces != "" {
		t.Fatal("script must be a build-only node")
	}
	for _, script := range []string{"", strings.Repeat("s", (64<<10)+1), "a\x00b"} {
		if err := lib.ValidateBuildPipeline([]StepInst{{Step: "shell-script", Params: map[string]string{"SCRIPT": script}}}); err == nil {
			t.Fatal("invalid script accepted")
		}
	}
	if err := lib.ValidateBuildPipeline([]StepInst{{Step: "shell-script"}}); err != nil {
		t.Fatal(err)
	}
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b := NewBuilder(s, t.TempDir(), NewLogHub())
	workspace := t.TempDir()
	output := t.TempDir()
	var log strings.Builder
	run := func(script string) int {
		return b.execBuildStepContext(context.Background(), &Service{Name: "fixture"}, def, append(def.BuildEnv(StepInst{Step: "shell-script", Params: map[string]string{"SCRIPT": script}}, workspace, "fixture", "v1", output), "FIXTURE_VAR=fixture-value"), func(line string) { log.WriteString(line) })
	}
	if code := run(`printf '%s\n' "$PWD" "$LP_SERVICE" "$FIXTURE_VAR"`); code != 0 {
		t.Fatalf("code=%d log=%s", code, log.String())
	}
	for _, value := range []string{workspace, "fixture", "fixture-value"} {
		if !strings.Contains(log.String(), value) {
			t.Fatalf("missing %s", value)
		}
	}
	log.Reset()
	if code := run("false\nprintf should-not-run\\n"); code == 0 || strings.Contains(log.String(), "should-not-run") {
		t.Fatal("failed script did not stop")
	}
	log.Reset()
	if code := run(`printf '%s' "$UNDEFINED_FIXTURE_REQUIRED"`); code == 0 {
		t.Fatal("undefined variable did not fail")
	}
	sv := &Service{Name: "fixture", ArtifactType: "jar", BuildEnvironment: &BuildEnvironment{Mode: "container", Preset: "gradle-jdk21"}}
	args, _, err := containerBuildArgs(sv, def, def.BuildEnv(StepInst{Step: "shell-script", Params: map[string]string{"SCRIPT": "pwd"}}, workspace, "fixture", "v1", output), t.TempDir(), "fixture-container", "gradle:8.14.3-jdk21")
	if err != nil || !strings.Contains(strings.Join(args, " "), "LP_SCRIPT") {
		t.Fatalf("script not forwarded into build container: %v", err)
	}
}
