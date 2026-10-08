package deploy

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRecordPipelineArtifactCreatesDeployableBuild(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpsertService(Service{Name: "web", ArtifactType: "static", BuildTool: "command", BuildCmd: "true"}); err != nil {
		t.Fatal(err)
	}
	run := &PipelineRun{Service: "web", Actor: "alice", StartedAt: time.Now().Unix()}
	if err := st.CreateRun(run); err != nil {
		t.Fatal(err)
	}
	file := t.TempDir() + "/web.tar.gz"
	if err := os.WriteFile(file, []byte("asset"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := describeArtifact("web", "abc123", ArtifactStatic, file, "")
	if err != nil {
		t.Fatal(err)
	}
	stored, err := st.RecordPipelineArtifact(run.ID, "alice", a)
	if err != nil {
		t.Fatal(err)
	}
	build := st.GetBuildByVersion("web", "abc123")
	if build == nil || build.ArtifactID != stored.ID || build.PipelineRunID != run.ID || build.ArtifactKind != ArtifactStatic {
		t.Fatalf("pipeline build not linked to artifact: artifact=%#v build=%#v", stored, build)
	}
	got := st.GetRun(run.ID)
	if got.ArtifactID != stored.ID || got.ArtifactDigest == "" || got.ArtifactKind != ArtifactStatic {
		t.Fatalf("pipeline run not linked to artifact: %#v", got)
	}
}

func TestDeployDriverRejectsMismatchedAndMutableArtifacts(t *testing.T) {
	d := NewDeployer(nil, NewLogHub(), "")
	jar := Artifact{Kind: ArtifactJar, URI: "/tmp/app.jar", Digest: "sha256:x"}
	if err := d.drivers["docker-host"].Validate(&Service{}, Host{}, jar); err == nil {
		t.Fatal("docker driver accepted jar")
	}
	image := Artifact{Kind: ArtifactOCI, URI: "registry/acme/app:latest", Digest: "sha256:abcdef"}
	if err := d.drivers["docker-host"].Validate(&Service{}, Host{}, image); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("docker driver accepted mutable tag: %v", err)
	}
	image.URI = "registry/acme/app@sha256:abcdef"
	if err := d.drivers["docker-host"].Validate(&Service{}, Host{}, image); err != nil {
		t.Fatal(err)
	}
}

func TestStepLibraryValidatesArtifactCompatibility(t *testing.T) {
	lib := NewStepLib("../../steps")
	if err := lib.ValidatePipeline([]StepInst{{Step: "package-static"}, {Step: "ssh-jar-deploy"}}); err == nil {
		t.Fatal("pipeline accepted static artifact for jar deploy driver")
	}
	if err := lib.ValidatePipeline([]StepInst{{Step: "docker-build"}, {Step: "docker-host-deploy"}}); err != nil {
		t.Fatal(err)
	}
	if err := lib.ValidateBuildPipeline([]StepInst{{Step: "docker-build"}, {Step: "docker-host-deploy"}}); err == nil {
		t.Fatal("build pipeline accepted a deployment step")
	}
}

func TestImageTemplateAndManifestJSON(t *testing.T) {
	steps := ExpandTemplate(Service{Name: "api", ArtifactType: "image", ImageName: "registry/acme/api"})
	if len(steps) != 2 || steps[1].Step != "docker-build" {
		t.Fatalf("image template = %#v", steps)
	}
	lib := NewStepLib("../../steps")
	if err := lib.ValidatePipeline(steps); err != nil {
		t.Fatal(err)
	}
	definition := lib.Get("docker-build")
	raw, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"value":"true"`) {
		t.Fatalf("step options are not JSON compatible: %s", raw)
	}
}

func TestLegacyPipelineArtifactIsBackfilled(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertService(Service{Name: "web", ArtifactType: "static", BuildTool: "command", BuildCmd: "true"}); err != nil {
		t.Fatal(err)
	}
	file := dir + "/legacy.tar.gz"
	if err := os.WriteFile(file, []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := &PipelineRun{Service: "web", Actor: "legacy", StartedAt: time.Now().Unix()}
	if err := st.CreateRun(run); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE pipeline_runs SET version='old1',artifact=?,status='success' WHERE id=?`, file, run.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	got := st.GetRun(run.ID)
	if got.ArtifactID == 0 || got.ArtifactKind != ArtifactStatic {
		t.Fatalf("legacy run not backfilled: %#v", got)
	}
	if build := st.GetBuildByVersion("web", "old1"); build == nil || build.PipelineRunID != run.ID {
		t.Fatalf("legacy pipeline build missing: %#v", build)
	}
}
