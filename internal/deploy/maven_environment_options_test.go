package deploy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCommonMavenOptionsValidateAndQuoteArguments(t *testing.T) {
	for _, value := range []string{"0", "0C", "0.0C", "-1", "1.5", "1C -U", "1C;true", "$(true)", "NaNC", "1e2C"} {
		if _, err := (&MavenExecutionOptions{Threads: value}).arguments(); err == nil {
			t.Fatalf("invalid thread value accepted: %q", value)
		}
	}
	for _, value := range []string{"1", "4", "1C", "2.5C", "0.5C"} {
		if _, err := (&MavenExecutionOptions{Threads: value}).arguments(); err != nil {
			t.Fatal(value, err)
		}
	}
	dir := t.TempDir()
	compiler := filepath.Join(dir, "mvn")
	if err := os.WriteFile(compiler, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	base := Service{BuildTool: "maven", ArtifactType: "jar", MavenExecutable: compiler, MavenGoals: "clean package", MavenThreads: "1C", MavenUpdateSnapshots: true}
	for _, env := range []string{"dev", "test"} {
		resolved, err := applyBuildVariant(&base, BuildVariant{Name: env, MavenProfiles: env, MavenProperties: "maven.test.skip=true\nmaven.compile.fork=true"})
		if err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command("bash", "-c", resolved.BuildCmd).CombinedOutput()
		want := []string{"--batch-mode", "--no-transfer-progress", "-T", "1C", "-U", "-P" + env, "-Dmaven.compile.fork=true", "-Dmaven.test.skip=true", "clean", "package"}
		if err != nil || !reflect.DeepEqual(strings.Fields(string(out)), want) {
			t.Fatalf("arguments changed: %q, %v", out, err)
		}
	}
	if base.BuildCmd != "" || base.MavenProfiles != "" {
		t.Fatal("environment mutated common settings")
	}
}

func TestCommonMavenOptionsSurviveSnapshotRetryAndGateArtifacts(t *testing.T) {
	for _, kind := range []string{"jar", "image"} {
		t.Run(kind, func(t *testing.T) {
			s, _, _ := variantFixture(t, "maven", kind)
			sv := *s.GetService("orders")
			sv.MavenThreads = "1C"
			sv.MavenUpdateSnapshots = true
			if err := s.UpsertService(sv); err != nil {
				t.Fatal(err)
			}
			a := variantArtifact(t, s, "test")
			if err := s.validateArtifactBuildVariant(a, "test"); err != nil {
				t.Fatal(err)
			}
			for _, change := range []MavenExecutionOptions{{Threads: "2C", UpdateSnapshots: true}, {Threads: "1C", UpdateSnapshots: false}} {
				sv.MavenThreads = change.Threads
				sv.MavenUpdateSnapshots = change.UpdateSnapshots
				if err := s.UpsertService(sv); err != nil {
					t.Fatal(err)
				}
				if err := s.validateArtifactBuildVariant(a, "test"); err == nil {
					t.Fatal("changed common options accepted old artifact")
				}
			}
			replay, _, _, err := s.buildReplay(a.SourceID)
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := serviceForPipelineRun(replay, PipelineRunOptions{})
			if err != nil || prepared.BuildCmd != replay.BuildCmd || !strings.Contains(prepared.BuildCmd, "'-T' '1C' '-U'") {
				t.Fatal("retry lost original common options", err)
			}
			sv.MavenThreads = "1C"
			sv.MavenUpdateSnapshots = true
			if err := s.UpsertService(sv); err != nil {
				t.Fatal(err)
			}
			if err := s.validateArtifactBuildVariant(a, "test"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCommonMavenOptionsAPIPreservesOmittedAndAcceptsExplicitClear(t *testing.T) {
	s, _, _ := variantFixture(t, "maven", "jar")
	h := Routes(s, NewBuilder(s, t.TempDir(), NewLogHub()), NewLogHub(), "", NewStepLib(filepath.Join("..", "..", "steps")))
	request := func(input map[string]any) {
		t.Helper()
		raw, _ := json.Marshal(input)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/services", strings.NewReader(string(raw))))
		if rec.Code != 200 {
			t.Fatal(rec.Code, rec.Body.String())
		}
	}
	input := map[string]any{}
	raw, _ := json.Marshal(s.GetService("orders"))
	json.Unmarshal(raw, &input)
	input["maven_arguments"] = "-e"
	input["maven_threads"] = "1C"
	input["maven_update_snapshots"] = true
	request(input)
	delete(input, "maven_arguments")
	delete(input, "maven_threads")
	delete(input, "maven_update_snapshots")
	request(input)
	if got := s.GetService("orders"); got.MavenArguments != "-e" || got.MavenThreads != "1C" || !got.MavenUpdateSnapshots {
		t.Fatal("older client erased common options")
	}
	input["maven_arguments"] = ""
	input["maven_threads"] = ""
	input["maven_update_snapshots"] = false
	request(input)
	if got := s.GetService("orders"); got.MavenArguments != "" || got.MavenThreads != "" || got.MavenUpdateSnapshots {
		t.Fatal("explicit clear was ignored")
	}
}

func TestCommonMavenOptionsUpgradePreservesExistingApplication(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	sv := Service{Name: "legacy", BuildTool: "maven", ArtifactType: "jar", MavenGoals: "clean package", MavenProfiles: "ci"}
	if err := s.UpsertService(sv); err != nil {
		t.Fatal(err)
	}
	identity := s.GetService("legacy").ApplicationID
	for _, col := range []string{"maven_arguments", "maven_threads", "maven_update_snapshots"} {
		if _, err := s.db.Exec("ALTER TABLE services DROP COLUMN " + col); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := s.GetService("legacy")
	if got == nil || got.ApplicationID != identity || got.MavenProfiles != "ci" || got.MavenThreads != "" || got.MavenUpdateSnapshots {
		t.Fatal("legacy application changed during upgrade")
	}
	got.MavenArguments = "-e"
	got.MavenThreads = "1C"
	got.MavenUpdateSnapshots = true
	if err := s.UpsertService(*got); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got = s.GetService("legacy"); got == nil || got.MavenArguments != "-e" || got.MavenThreads != "1C" || !got.MavenUpdateSnapshots || got.ApplicationID != identity {
		t.Fatal("common options did not persist across reopen")
	}
}
