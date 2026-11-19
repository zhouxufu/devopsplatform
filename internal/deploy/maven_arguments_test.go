package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMavenArgumentsValidateBeforeExecution(t *testing.T) {
	for _, raw := range []string{"-e", "--errors -X -fae", "-pl :service-bus,estate-common -am -e", "--projects=:service-bus --also-make", "-o\n--strict-checksums"} {
		if _, err := parseMavenArguments(raw); err != nil {
			t.Fatalf("valid options rejected: %q: %v", raw, err)
		}
	}
	for _, raw := range []string{"-Pprod", "-Denv=prod", "--settings /tmp/other.xml", "-T 4", "-U", "--fail-never", "-fn", "package", "-f other/pom.xml", "-e;touch /tmp/injected", "$(id)", "--projects=", "-pl --settings", "-pl :app;id", "-e\x00", strings.Repeat("x", 4097)} {
		if _, err := parseMavenArguments(raw); err == nil {
			t.Fatalf("unsafe or ambiguous argument accepted: %q", raw)
		}
	}
	if _, err := parseMavenArguments(strings.Repeat("-e ", 65)); err == nil {
		t.Fatal("unbounded arguments accepted")
	}
}

func TestMavenArgumentsReachProcessAsSeparateArguments(t *testing.T) {
	dir := t.TempDir()
	compiler := filepath.Join(dir, "mvn")
	if err := os.WriteFile(compiler, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	sv := Service{ArtifactType: "jar", BuildTool: "maven", MavenExecutable: compiler, MavenGoals: "clean package", MavenThreads: "1", MavenArguments: "-e\n-pl :service-bus -am", MavenProfiles: "dev"}
	if err := prepareBuildConfig(&sv); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("bash", "-c", sv.BuildCmd).CombinedOutput()
	want := []string{"--batch-mode", "--no-transfer-progress", "-T", "1", "-e", "-pl", ":service-bus", "-am", "-Pdev", "clean", "package"}
	if err != nil || !reflect.DeepEqual(strings.Fields(string(out)), want) {
		t.Fatalf("unexpected actual argv: %s, %v", out, err)
	}
}

func TestMavenArgumentsPersistAndFreezeWithBuildVariant(t *testing.T) {
	for _, kind := range []string{"jar", "image"} {
		t.Run(kind, func(t *testing.T) {
			s, _, _ := variantFixture(t, "maven", kind)
			sv := *s.GetService("orders")
			sv.MavenArguments = "-e -fae"
			if err := s.UpsertService(sv); err != nil {
				t.Fatal(err)
			}
			if got := s.GetService("orders"); got == nil || got.MavenArguments != sv.MavenArguments {
				t.Fatal("arguments did not persist")
			}
			a := variantArtifact(t, s, "test")
			if err := s.validateArtifactBuildVariant(a, "test"); err != nil {
				t.Fatal(err)
			}
			if err := s.validateArtifactBuildVariant(a, "prod"); err == nil {
				t.Fatal("extra options bypassed environment contract")
			}
			sv.MavenArguments = "-X"
			if err := s.UpsertService(sv); err != nil {
				t.Fatal(err)
			}
			replay, _, _, err := s.buildReplay(a.SourceID)
			if err != nil || replay.MavenArguments != "-e -fae" {
				t.Fatal("retry lost frozen arguments", err)
			}
			prepared, err := serviceForPipelineRun(replay, PipelineRunOptions{})
			if err != nil || !strings.Contains(prepared.BuildCmd, "'-e' '-fae'") || strings.Contains(prepared.BuildCmd, "'-X'") {
				t.Fatal("retry used current rather than frozen options", err)
			}
		})
	}
}
