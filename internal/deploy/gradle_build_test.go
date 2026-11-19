package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareGradleBuildConfig(t *testing.T) {
	sv := Service{
		ArtifactType: "jar", BuildTool: "gradle", GradleExecutable: "./gradlew",
		GradleTasks: ":app:clean :app:bootJar", GradleExcludedTasks: "test,integrationTest",
		GradleProjectProperties: "profile=ci\nrevision=1.2.3", GradleSystemProperties: "org.gradle.jvmargs=-Xmx2g",
	}
	if err := prepareBuildConfig(&sv); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`'./gradlew'`, `'--no-daemon'`, `'-Pprofile=ci'`, `'-Dorg.gradle.jvmargs=-Xmx2g'`,
		`'-x' 'test'`, `'-x' 'integrationTest'`, `':app:clean' ':app:bootJar'`,
	} {
		if !strings.Contains(sv.BuildCmd, want) {
			t.Fatalf("build command %q does not contain %q", sv.BuildCmd, want)
		}
	}
	if sv.ArtifactGlob != "build/libs/*.jar" {
		t.Fatalf("unexpected default artifact glob: %q", sv.ArtifactGlob)
	}
}

func TestGradleRefreshDependencies(t *testing.T) {
	for _, kind := range []string{"jar", "image"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			gradle := filepath.Join(dir, "gradle")
			if err := os.WriteFile(gradle, []byte("#!/bin/sh\nprintf '<%s>\\n' \"$@\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			sv := Service{ArtifactType: kind, BuildTool: "gradle", GradleExecutable: gradle,
				GradleTasks: "clean :estate-ai-seek-bootstrap:bootJar --refresh-dependencies", GradleExcludedTasks: "test"}
			if err := prepareGradleBuildConfig(&sv); err != nil {
				t.Fatal(err)
			}
			// Run only a temporary argument-printing executable, never real Gradle.
			out, err := exec.Command("bash", "-c", sv.BuildCmd).CombinedOutput()
			if err != nil {
				t.Fatalf("generated command failed: %v %s", err, out)
			}
			if !strings.Contains(string(out), "<clean>\n<:estate-ai-seek-bootstrap:bootJar>\n<--refresh-dependencies>\n") || !strings.Contains(string(out), "<-x>\n<test>\n") {
				t.Fatalf("wrong task/option arguments: %s", out)
			}
		})
	}
}

func TestGradleBuildConfigRejectsUnsafeOrSensitiveParameters(t *testing.T) {
	base := Service{ArtifactType: "jar", BuildTool: "gradle", GradleExecutable: "./gradlew", GradleTasks: "clean build"}
	tests := []struct {
		name   string
		mutate func(*Service)
	}{
		{"task option injection", func(s *Service) { s.GradleTasks = "build --init-script=/tmp/evil" }},
		{"refresh does not allow init scripts", func(s *Service) { s.GradleTasks = "build --refresh-dependencies --init-script=/tmp/evil" }},
		{"refresh does not allow project paths", func(s *Service) { s.GradleTasks = "build --refresh-dependencies --project-dir=/tmp/other" }},
		{"refresh value is not supported", func(s *Service) { s.GradleTasks = "build --refresh-dependencies=true" }},
		{"refresh shell syntax", func(s *Service) { s.GradleTasks = "build --refresh-dependencies;echo injected" }},
		{"refresh cannot be an excluded task", func(s *Service) { s.GradleExcludedTasks = "--refresh-dependencies" }},
		{"task shell syntax", func(s *Service) { s.GradleTasks = "build;curl" }},
		{"exclude shell syntax", func(s *Service) { s.GradleExcludedTasks = "test;curl" }},
		{"plaintext secret", func(s *Service) { s.GradleProjectProperties = "repo.password=secret" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sv := base
			tt.mutate(&sv)
			if err := prepareBuildConfig(&sv); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestGeneratedGradleCommandKeepsArgumentsIsolated(t *testing.T) {
	dir := t.TempDir()
	gradle := filepath.Join(dir, "gradle")
	if err := os.WriteFile(gradle, []byte("#!/bin/sh\nprintf '<%s>\\n' \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	sv := Service{ArtifactType: "jar", BuildTool: "gradle", GradleExecutable: gradle, GradleTasks: "build",
		GradleProjectProperties: "message=hello world;not-a-command\nrepo.token=${env.YUNXIAO_TOKEN}"}
	if err := prepareBuildConfig(&sv); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", sv.BuildCmd)
	cmd.Env = append(os.Environ(), "YUNXIAO_TOKEN=abc def;still-one-argument")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generated command failed: %v\n%s", err, out)
	}
	got := string(out)
	for _, want := range []string{"<-Pmessage=hello world;not-a-command>", "<-Prepo.token=abc def;still-one-argument>", "<build>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated command split or changed an argument; output=%q want=%q", got, want)
		}
	}
}

func TestGradleAutoUsesWrapperThenRunner(t *testing.T) {
	t.Run("non executable wrapper", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "gradlew"), []byte("#!/bin/sh\necho wrapper:$*\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		sv := Service{ArtifactType: "jar", BuildTool: "gradle", GradleTasks: "build"}
		if err := prepareBuildConfig(&sv); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("bash", "-c", sv.BuildCmd)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "wrapper:--no-daemon --console=plain build") {
			t.Fatalf("auto wrapper failed: %v\n%s\ncommand=%s", err, out, sv.BuildCmd)
		}
	})

	t.Run("runner path fallback", func(t *testing.T) {
		dir := t.TempDir()
		bin := filepath.Join(dir, "bin")
		if err := os.Mkdir(bin, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, "gradle"), []byte("#!/bin/sh\necho runner:$*\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		sv := Service{ArtifactType: "jar", BuildTool: "gradle", GradleTasks: "build"}
		if err := prepareBuildConfig(&sv); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("bash", "-c", sv.BuildCmd)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "runner:--no-daemon --console=plain build") {
			t.Fatalf("runner fallback failed: %v\n%s\ncommand=%s", err, out, sv.BuildCmd)
		}
	})
}

func TestPipelineGradleOverridesAndRoundTrip(t *testing.T) {
	original := Service{
		Name: "orders", Branch: "main", ArtifactType: "jar", BuildTool: "gradle",
		GradleExecutable: "./gradlew", GradleTasks: "clean build", GradleExcludedTasks: "test",
		GradleProjectProperties: "profile=ci", GradleSystemProperties: "revision=1",
		Steps: []StepInst{{Step: "git-checkout", Params: map[string]string{"BRANCH": "main"}}, {Step: "java-build", Params: map[string]string{"COMMAND": "old"}}},
	}
	runService, err := serviceForPipelineRun(&original, PipelineRunOptions{
		Branch: "release", GradleTasks: ":app:bootJar", GradleExcludedTasks: "integrationTest",
		GradleProjectProperties: "profile=release", GradleSystemProperties: "revision=2", OverrideGradle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if original.Branch != "main" || original.GradleTasks != "clean build" || original.Steps[0].Params["BRANCH"] != "main" {
		t.Fatalf("run override mutated service defaults: %#v", original)
	}
	if !strings.Contains(runService.BuildCmd, "'-Pprofile=release'") || !strings.Contains(runService.BuildCmd, "':app:bootJar'") || runService.Steps[1].Params["COMMAND"] != runService.BuildCmd {
		t.Fatalf("Gradle override missing from command/step: %#v", runService)
	}

	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpsertService(*runService); err != nil {
		t.Fatal(err)
	}
	got := st.GetService("orders")
	if got == nil || got.BuildTool != "gradle" || got.GradleTasks != ":app:bootJar" || got.GradleExcludedTasks != "integrationTest" || got.GradleProjectProperties != "profile=release" || got.GradleSystemProperties != "revision=2" {
		t.Fatalf("Gradle build config did not round-trip: %#v", got)
	}
}

func TestServiceForPipelineRunRefreshesStructuredStepParameters(t *testing.T) {
	sv := &Service{
		Name:         "api",
		GitURL:       "https://git.example.test/team/api.git",
		Branch:       "main",
		ArtifactType: "jar",
		BuildTool:    "maven",
		MavenGoals:   "clean package",
		ArtifactGlob: "module/target/*.jar",
		Steps: []StepInst{
			{Step: "git-checkout", Params: map[string]string{"GIT_URL": "old", "BRANCH": "old"}},
			{Step: "java-build", Params: map[string]string{"COMMAND": "old"}},
			{Step: "package-jar", Params: map[string]string{"GLOB": "old/*.jar"}},
		},
	}
	prepared, err := serviceForPipelineRun(sv, PipelineRunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := prepared.Steps[0].Params["GIT_URL"]; got != sv.GitURL {
		t.Fatalf("GIT_URL = %q", got)
	}
	if got := prepared.Steps[0].Params["BRANCH"]; got != "main" {
		t.Fatalf("BRANCH = %q", got)
	}
	if got := prepared.Steps[2].Params["GLOB"]; got != sv.ArtifactGlob {
		t.Fatalf("GLOB = %q", got)
	}
}
