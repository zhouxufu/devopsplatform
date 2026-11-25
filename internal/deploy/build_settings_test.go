package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildSettingsCopyRemovedAfterContainerFailure(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(root, "mounts")
	// A local stub observes Docker arguments; it never runs a container or network.
	script := "#!/bin/sh\nif [ \"$1\" = image ]; then echo sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa; exit 0; fi\nif [ \"$1\" = run ]; then printf '%s\\n' \"$@\" > \"$CAPTURE_MOUNTS\"; exit 1; fi\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CAPTURE_MOUNTS", capture)
	source := filepath.Join(root, "settings.xml")
	if err := os.WriteFile(source, []byte("<settings/>"), 0600); err != nil {
		t.Fatal(err)
	}
	sv := &Service{Name: "orders", MavenSettingsPath: source, BuildEnvironment: &BuildEnvironment{Mode: "container", Preset: "maven-jdk8"}}
	def := &StepDef{Kind: "build", Dir: root}
	env := def.BuildEnv(StepInst{}, root, "orders", "v1", root)
	b := &Builder{workspaceDir: root}
	if code := b.execBuildStep(sv, def, env, func(string) {}); code == 0 {
		t.Fatal("expected local stub failure")
	}
	args, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	var staged string
	for _, arg := range strings.Split(string(args), "\n") {
		if strings.HasSuffix(arg, ":"+source+":ro,Z") {
			staged = strings.TrimSuffix(arg, ":"+source+":ro,Z")
		}
	}
	if staged == "" {
		t.Fatal("private mount not observed")
	}
	if _, err = os.Stat(staged); !os.IsNotExist(err) {
		t.Fatal("private copy leaked after failed build", err)
	}
	if _, err = os.Stat(source); err != nil {
		t.Fatal("original settings changed", err)
	}
}

func TestBuildSettingsPrivateCopyAndReadOnlyMount(t *testing.T) {
	root := t.TempDir()
	original := filepath.Join(root, "original-settings.xml")
	secret := "<settings><servers><server><password>private-test-value</password></server></servers></settings>"
	if err := os.WriteFile(original, []byte(secret), 0640); err != nil {
		t.Fatal(err)
	}
	staged, cleanup, err := stageBuildSettings(original)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if staged == original || strings.HasPrefix(staged, root+string(os.PathSeparator)) {
		t.Fatal("not isolated from source")
	}
	info, _ := os.Stat(staged)
	parent, _ := os.Stat(filepath.Dir(staged))
	if info.Mode().Perm() != 0600 || parent.Mode().Perm() != 0700 {
		t.Fatal("private permissions lost")
	}
	data, _ := os.ReadFile(staged)
	if string(data) != secret {
		t.Fatal("settings changed")
	}
	sv := &Service{MavenSettingsPath: original}
	def := &StepDef{Dir: root}
	env := def.BuildEnv(StepInst{}, root, "orders", "v1", root)
	args, _, err := containerBuildArgs(sv, def, env, root, "test", "sha256:test", staged)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(args, " ")
	if !strings.Contains(text, staged+":"+original+":ro,Z") || strings.Contains(text, secret) || strings.Contains(text, "label=disable") || strings.Contains(text, "--privileged") {
		t.Fatal("unsafe mount/secret leak")
	}
	if !strings.Contains(text, "--cap-drop=ALL") || !strings.Contains(text, "--user") {
		t.Fatal("container restrictions removed")
	}
	info, _ = os.Stat(original)
	if info.Mode().Perm() != 0640 {
		t.Fatal("original permissions changed")
	}
	cleanup()
	if _, err = os.Stat(staged); !os.IsNotExist(err) {
		t.Fatal("credential copy remains")
	}
	if _, err = os.Stat(original); err != nil {
		t.Fatal("original removed")
	}
}

func TestBuildSettingsRejectsMissingNonFileAndOversize(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"relative/settings.xml", filepath.Join(root, "missing"), root, "/tmp/a:b.xml"} {
		if _, cleanup, err := stageBuildSettings(path); err == nil {
			cleanup()
			t.Fatalf("accepted %s", path)
		}
	}
	large := filepath.Join(root, "large.xml")
	if err := os.WriteFile(large, make([]byte, maxBuildSettingsSize+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := stageBuildSettings(large); err == nil {
		t.Fatal("accepted oversized settings")
	}
	if path, cleanup, err := stageBuildSettings(""); err != nil || path != "" {
		t.Fatal(err, path)
	} else {
		cleanup()
	}
	if os.Geteuid() != 0 {
		unreadable := filepath.Join(root, "unreadable.xml")
		if err := os.WriteFile(unreadable, []byte("do-not-echo-this"), 0000); err != nil {
			t.Fatal(err)
		}
		if _, _, err := stageBuildSettings(unreadable); err == nil || strings.Contains(err.Error(), "do-not-echo-this") {
			t.Fatal("read denied not handled safely", err)
		}
	}
}

func TestBuildSettingsFailurePrecedesDockerPull(t *testing.T) {
	root := t.TempDir()
	b := &Builder{workspaceDir: root}
	sv := &Service{Name: "orders", MavenSettingsPath: filepath.Join(root, "missing.xml"), BuildEnvironment: &BuildEnvironment{Mode: "container", Preset: "maven-jdk8"}}
	var log strings.Builder
	if code := b.execBuildStep(sv, &StepDef{Kind: "build"}, nil, func(s string) { log.WriteString(s) }); code == 0 || !strings.Contains(log.String(), "settings.xml") {
		t.Fatal(code, log.String())
	}
	if strings.Contains(log.String(), "首次准备构建镜像") {
		t.Fatal("download preceded validation")
	}
}
