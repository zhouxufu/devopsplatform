package deploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

const imageFixtureID = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func imageSourceFixture(t *testing.T) (*BuildEnvironment, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "deploy"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "deploy/Dockerfile.build"), []byte("FROM scratch\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return &BuildEnvironment{Mode: "container", Preset: "custom", Image: "fixture-" + strings.ToLower(t.Name()) + ":v1", ImageSource: "dockerfile", Dockerfile: "deploy/Dockerfile.build", Network: "host"}, root
}
func TestBuildImageSourceValidation(t *testing.T) {
	e, _ := imageSourceFixture(t)
	if err := validateBuildEnvironment(e, nil); err != nil {
		t.Fatal(err)
	}
	for _, patch := range []func(*BuildEnvironment){
		func(e *BuildEnvironment) { e.Dockerfile = "" }, func(e *BuildEnvironment) { e.Dockerfile = "../Dockerfile" }, func(e *BuildEnvironment) { e.Dockerfile = "/etc/Dockerfile" },
		func(e *BuildEnvironment) { e.Context = "https://elsewhere/source" }, func(e *BuildEnvironment) { e.Context = "../other" }, func(e *BuildEnvironment) { e.Dockerfile = ".git/config" },
		func(e *BuildEnvironment) { e.Context = ".git" }, func(e *BuildEnvironment) { e.Dockerfile = "file\nFROM" }, func(e *BuildEnvironment) { e.ImageSource = "other" },
		func(e *BuildEnvironment) { e.Image = "image@sha256:aaaa" }, func(e *BuildEnvironment) { e.Preset = "maven-jdk21" },
	} {
		bad := *e
		patch(&bad)
		if validateBuildEnvironment(&bad, nil) == nil {
			t.Errorf("accepted invalid config %+v", bad)
		}
	}
	legacy := BuildEnvironment{Mode: "container", Preset: "custom", Image: "old:v1", Dockerfile: "../inactive", Context: "/inactive"}
	if err := validateBuildEnvironment(&legacy, nil); err != nil {
		t.Fatal("legacy image-only config changed", err)
	}
}
func TestBuildImageDockerfileMissingBuildAndCache(t *testing.T) {
	e, root := imageSourceFixture(t)
	var commands [][]string
	ready := false
	inspect := func() (string, error) {
		if ready {
			return imageFixtureID, nil
		}
		return "", errors.New("missing")
	}
	run := func(args []string) error { commands = append(commands, args); ready = true; return nil }
	var log strings.Builder
	for i := 0; i < 2; i++ {
		id, err := prepareBuildImage(context.Background(), e, root, func(s string) { log.WriteString(s) }, inspect, run)
		if err != nil || id != imageFixtureID {
			t.Fatal(id, err)
		}
	}
	if len(commands) != 1 {
		t.Fatalf("expected one build, got %v", commands)
	}
	got := strings.Join(commands[0], "\n")
	for _, want := range []string{"build\n--file", filepath.Join(root, e.Dockerfile), "--tag\n" + e.Image, "--network\nhost", root} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q: %v", want, commands)
		}
	}
	for _, bad := range []string{"pull", "--build-arg", "--secret", "docker.sock", "--privileged"} {
		if strings.Contains(got, bad) {
			t.Fatal("unexpected command", commands)
		}
	}
	if !strings.Contains(log.String(), "按仓库 Dockerfile 构建") || !strings.Contains(log.String(), "复用本地构建镜像") {
		t.Fatal(log.String())
	}
}
func TestBuildImageFailureNoFallbackOrReplay(t *testing.T) {
	e, root := imageSourceFixture(t)
	attempts := 0
	inspections := 0
	_, err := prepareBuildImage(context.Background(), e, root, func(string) {}, func() (string, error) { inspections++; return "", errors.New("missing") }, func(args []string) error {
		attempts++
		if args[0] != "build" {
			t.Fatal(args)
		}
		return errors.New("network")
	})
	if err == nil || attempts != 1 || inspections != 1 {
		t.Fatal(err, attempts, inspections)
	}
	if !strings.Contains(err.Error(), "源码编译尚未开始") {
		t.Fatal(err)
	}
}
func TestBuildImageRegistryAndImmutableResolution(t *testing.T) {
	e, root := imageSourceFixture(t)
	e.ImageSource = ""
	ready := false
	id, err := prepareBuildImage(context.Background(), e, root, func(string) {}, func() (string, error) {
		if ready {
			return imageFixtureID, nil
		}
		return "", errors.New("missing")
	}, func(args []string) error {
		if strings.Join(args, " ") != "pull "+e.Image {
			t.Fatal(args)
		}
		ready = true
		return nil
	})
	if err != nil || id != imageFixtureID {
		t.Fatal(id, err)
	}
	_, err = prepareBuildImage(context.Background(), e, root, func(string) {}, func() (string, error) { return "sha256:invalid", nil }, func([]string) error { t.Fatal("invalid ID must not build/pull"); return nil })
	if err == nil {
		t.Fatal("invalid ID accepted")
	}
}
func TestBuildImageRejectsMissingAndEscapingInputs(t *testing.T) {
	e, root := imageSourceFixture(t)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "Dockerfile"), []byte("FROM scratch"), 0600)
	if err := os.Symlink(outside, filepath.Join(root, "outside")); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"missing", "."}, {"outside/Dockerfile", "."}, {e.Dockerfile, "outside"}, {"deploy", "."}, {e.Dockerfile, e.Dockerfile}} {
		c := *e
		c.Dockerfile = pair[0]
		c.Context = pair[1]
		if _, err := buildImageDockerfileArgs(&c, root); err == nil {
			t.Fatalf("accepted %#v", c)
		}
	}
}
func TestBuildImageConcurrentPreparationAndCancellation(t *testing.T) {
	e, root := imageSourceFixture(t)
	var ready atomic.Bool
	var builds atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	inspect := func() (string, error) {
		if ready.Load() {
			return imageFixtureID, nil
		}
		return "", errors.New("missing")
	}
	run := func([]string) error { builds.Add(1); close(entered); <-release; ready.Store(true); return nil }
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := prepareBuildImage(context.Background(), e, root, func(string) {}, inspect, run); err != nil {
			t.Error(err)
		}
	}()
	<-entered
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := prepareBuildImage(canceled, e, root, func(string) {}, inspect, run); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := prepareBuildImage(context.Background(), e, root, func(string) {}, inspect, run); err != nil {
			t.Error(err)
		}
	}()
	close(release)
	wg.Wait()
	if builds.Load() != 1 {
		t.Fatal(builds.Load())
	}
}
func TestBuildImageSourcePersistsInOriginalSnapshot(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	e, _ := imageSourceFixture(t)
	if err := st.UpsertService(Service{Name: "fixture", ArtifactType: "jar", GitURL: "https://example.invalid/repo.git", BuildTool: "gradle"}); err != nil {
		t.Fatal(err)
	}
	original := ServiceDelivery{Service: "fixture", BuildEnvironment: e}
	if err := st.UpsertServiceDelivery(original); err != nil {
		t.Fatal(err)
	}
	sv := st.GetService("fixture")
	run := &PipelineRun{Service: sv.Name, Actor: "alice", Status: "failed"}
	if err := st.CreateRun(run); err != nil {
		t.Fatal(err)
	}
	if err := st.saveBuildSnapshot(run.ID, sv, &original); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE build_run_snapshots SET commit_ref=? WHERE run_id=?`, strings.Repeat("a", 40), run.ID); err != nil {
		t.Fatal(err)
	}
	changed := *e
	changed.Dockerfile = "new/Dockerfile"
	changed.Context = "new"
	changed.Image = "new:v2"
	if err := st.UpsertServiceDelivery(ServiceDelivery{Service: sv.Name, BuildEnvironment: &changed}); err != nil {
		t.Fatal(err)
	}
	_, d, _, err := st.buildReplay(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.BuildEnvironment == nil || *d.BuildEnvironment != *e {
		t.Fatalf("snapshot changed: %+v", d.BuildEnvironment)
	}
	if got := st.GetServiceDelivery(sv.Name).BuildEnvironment; got.Dockerfile != changed.Dockerfile {
		t.Fatal("current configuration lost", got)
	}
}

func TestBuildImagePreparationPrecedesCompilation(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "success"
		if fail {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			e, root := imageSourceFixture(t)
			workspace := root
			bin := t.TempDir()
			step := t.TempDir()
			output := t.TempDir()
			capture := filepath.Join(root, "commands")
			script := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$DOCKER_CAPTURE"
case "$1" in
 image) test -f "$DOCKER_READY" || exit 1; printf '%s\n' "$DOCKER_IMAGE_ID";;
 build) test -z "${BUSINESS_SECRET:-}"; test "$DOCKER_FAIL" = 0 || exit 7; touch "$DOCKER_READY";;
 run) printf 'source-compile-started\n';;
 rm) :;;
 *) exit 9;;
esac
`
			if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("DOCKER_CAPTURE", capture)
			t.Setenv("DOCKER_READY", filepath.Join(root, "ready"))
			t.Setenv("DOCKER_IMAGE_ID", imageFixtureID)
			flag := "0"
			if fail {
				flag = "1"
			}
			t.Setenv("DOCKER_FAIL", flag)
			def := &StepDef{Kind: "build", Dir: step}
			sv := &Service{Name: "image-fixture", Workdir: workspace, BuildEnvironment: e, RuntimeEnv: map[string]string{"BUSINESS_SECRET": "not-for-image-layer"}}
			b := &Builder{workspaceDir: root}
			var log strings.Builder
			env := append(def.BuildEnv(StepInst{}, workspace, sv.Name, "v1", output), "BUSINESS_SECRET=not-for-image-layer")
			code := b.execBuildStepContext(context.Background(), sv, def, env, func(s string) { log.WriteString(s) })
			cmds, err := os.ReadFile(capture)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(cmds), "not-for-image-layer") || strings.Contains(string(cmds), "pull ") {
				t.Fatal(string(cmds))
			}
			if fail {
				if code == 0 || strings.Contains(log.String(), "source-compile-started") || strings.Contains(string(cmds), "run --") {
					t.Fatal(code, log.String(), string(cmds))
				}
			} else if code != 0 || !strings.Contains(log.String(), "source-compile-started") || !strings.Contains(string(cmds), imageFixtureID) {
				t.Fatal(code, log.String(), string(cmds))
			}
		})
	}
}
