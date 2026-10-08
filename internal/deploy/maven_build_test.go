package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPrepareMavenBuildConfig(t *testing.T) {
	sv := Service{
		ArtifactType: "jar", BuildTool: "maven", MavenExecutable: "./mvnw",
		MavenSettingsPath: "/opt/logplatform/settings.xml", MavenGoals: "clean package",
		MavenProfiles: "ci,release", MavenProperties: "maven.test.skip=true\nrevision=1.2.3",
	}
	if err := prepareBuildConfig(&sv); err != nil {
		t.Fatal(err)
	}
	wantParts := []string{
		`'./mvnw'`, `'--settings' '/opt/logplatform/settings.xml'`, `'-Pci,release'`,
		`'-Dmaven.test.skip=true'`, `'-Drevision=1.2.3'`, `'clean' 'package'`,
	}
	for _, want := range wantParts {
		if !strings.Contains(sv.BuildCmd, want) {
			t.Fatalf("build command %q does not contain %q", sv.BuildCmd, want)
		}
	}
}

func TestMavenBuildConfigRejectsUnsafeOrSensitiveParameters(t *testing.T) {
	base := Service{ArtifactType: "jar", BuildTool: "maven", MavenExecutable: "./mvnw", MavenGoals: "clean package"}
	tests := []struct {
		name   string
		mutate func(*Service)
	}{
		{"profile shell syntax", func(s *Service) { s.MavenProfiles = "ci;curl" }},
		{"goal option injection", func(s *Service) { s.MavenGoals = "clean --settings=/tmp/other" }},
		{"relative settings", func(s *Service) { s.MavenSettingsPath = "conf/settings.xml" }},
		{"plaintext secret", func(s *Service) { s.MavenProperties = "repo.password=secret" }},
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

func TestMavenSensitivePropertyCanReferenceEnvironment(t *testing.T) {
	sv := Service{ArtifactType: "jar", BuildTool: "maven", MavenExecutable: "mvn", MavenGoals: "package", MavenProperties: "repo.token=${env.YUNXIAO_TOKEN}"}
	if err := prepareBuildConfig(&sv); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sv.BuildCmd, `'-Drepo.token='"${YUNXIAO_TOKEN}"`) || strings.Contains(sv.BuildCmd, "secret-value") {
		t.Fatalf("environment reference was not rendered safely: %q", sv.BuildCmd)
	}
}

func TestGeneratedMavenCommandKeepsArgumentsIsolated(t *testing.T) {
	dir := t.TempDir()
	maven := filepath.Join(dir, "mvn")
	if err := os.WriteFile(maven, []byte("#!/bin/sh\nprintf '<%s>\\n' \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	sv := Service{ArtifactType: "jar", BuildTool: "maven", MavenExecutable: maven, MavenGoals: "clean package",
		MavenProperties: "revision=1.2 beta;echo not-a-command\nrepo.token=${env.YUNXIAO_TOKEN}"}
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
	for _, want := range []string{"<-Drevision=1.2 beta;echo not-a-command>", "<-Drepo.token=abc def;still-one-argument>", "<clean>", "<package>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated command split or changed an argument; output=%q want=%q", got, want)
		}
	}
}

func TestMavenAutoFallsBackToRunnerPath(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "mvn"), []byte("#!/bin/sh\necho runner:$*\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	sv := Service{ArtifactType: "jar", BuildTool: "maven", MavenGoals: "package"}
	if err := prepareBuildConfig(&sv); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", sv.BuildCmd)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "runner:--batch-mode --no-transfer-progress package") {
		t.Fatalf("Maven runner fallback failed: %v\n%s\ncommand=%s", err, out, sv.BuildCmd)
	}
}

func TestPipelineMavenOverridesAreIsolatedAndAudited(t *testing.T) {
	original := Service{
		Name: "orders", Branch: "main", ArtifactType: "jar", BuildTool: "maven",
		MavenExecutable: "mvn", MavenGoals: "clean package", MavenProfiles: "ci", MavenProperties: "revision=1",
		Steps: []StepInst{
			{Step: "git-checkout", Params: map[string]string{"BRANCH": "main"}},
			{Step: "java-build", Params: map[string]string{"COMMAND": "old"}},
		},
	}
	runService, err := serviceForPipelineRun(&original, PipelineRunOptions{
		Branch: "release", MavenProfiles: "release", MavenProperties: "revision=2", OverrideMaven: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if original.Branch != "main" || original.MavenProfiles != "ci" || original.Steps[0].Params["BRANCH"] != "main" {
		t.Fatalf("run override mutated service defaults: %#v", original)
	}
	if runService.Branch != "release" || runService.Steps[0].Params["BRANCH"] != "release" {
		t.Fatalf("branch override missing: %#v", runService)
	}
	if !strings.Contains(runService.BuildCmd, "'-Prelease'") || !strings.Contains(runService.BuildCmd, "'-Drevision=2'") || runService.Steps[1].Params["COMMAND"] != runService.BuildCmd {
		t.Fatalf("Maven override missing from command/step: %#v", runService)
	}

	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	run := PipelineRun{Service: "orders", Branch: runService.Branch, BuildCommand: runService.BuildCmd, StartedAt: time.Now().Unix()}
	if err := st.CreateRun(&run); err != nil {
		t.Fatal(err)
	}
	got := st.GetRun(run.ID)
	if got == nil || got.BuildCommand != runService.BuildCmd {
		t.Fatalf("build command audit was not persisted: %#v", got)
	}
}

func TestMavenServiceConfigurationRoundTrip(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sv := Service{Name: "orders", ArtifactType: "jar", BuildTool: "maven", MavenExecutable: "./mvnw",
		MavenGoals: "clean verify", MavenProfiles: "ci", MavenProperties: "revision=1.2.3", MavenSettingsPath: "/opt/settings.xml"}
	if err := prepareBuildConfig(&sv); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertService(sv); err != nil {
		t.Fatal(err)
	}
	got := st.GetService("orders")
	if got == nil || got.BuildTool != "maven" || got.MavenGoals != "clean verify" || got.MavenProfiles != "ci" || got.MavenProperties != "revision=1.2.3" || got.MavenSettingsPath != "/opt/settings.xml" {
		t.Fatalf("Maven build config did not round-trip: %#v", got)
	}
}
