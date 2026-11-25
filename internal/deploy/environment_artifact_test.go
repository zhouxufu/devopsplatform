package deploy

import (
	"fmt"
	"strings"
	"testing"
)

func pairedEnvironmentArtifacts(t *testing.T) (*Store, *ReleaseManager, *Artifact, *Artifact) {
	t.Helper()
	s, m, _ := variantFixture(t, "maven", "image")
	image := variantArtifact(t, s, "test")
	jar, err := s.RecordPipelineArtifact(image.SourceID, "alice", Artifact{Service: image.Service, Version: image.Version, Kind: ArtifactJar, URI: "/isolated-fixture.jar", Digest: "sha256:jar-input"})
	if err != nil {
		t.Fatal(err)
	}
	image.Metadata["compiled_input_digest"] = jar.Digest
	image, err = s.RecordPipelineArtifact(image.SourceID, "alice", *image)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.linkCompilationArtifact(image.SourceID, jar, image); err != nil {
		t.Fatal(err)
	}
	target := s.GetServiceEnvironment("orders", "prod")
	target.TargetType = "docker-host"
	target.RemoteDir = "/"
	target.DeployConfig = map[string]string{}
	if err = s.UpsertServiceEnvironment(*target); err != nil {
		t.Fatal(err)
	}
	return s, m, jar, image
}

func TestEnvironmentDeploymentChoosesOutputWithoutChangingApplication(t *testing.T) {
	s, m, jar, image := pairedEnvironmentArtifacts(t)
	sv := s.GetService("orders")
	run := s.GetRun(image.SourceID)
	if !s.artifactMatchesEnvironment(jar, sv, "test") || s.artifactMatchesEnvironment(image, sv, "test") {
		t.Fatal("Java environment must require linked JAR")
	}
	if !s.artifactMatchesEnvironment(image, sv, "prod") || s.artifactMatchesEnvironment(jar, sv, "prod") {
		t.Fatal("Docker environment must require image")
	}
	if a := s.artifactForEnvironmentRun(run, "test"); a == nil || a.ID != jar.ID {
		t.Fatal("build-and-publish did not select JAR", a)
	}
	if a := s.artifactForEnvironmentRun(run, "prod"); a == nil || a.ID != image.ID {
		t.Fatal("image environment lost primary artifact", a)
	}
	if sv.ArtifactType != "image" || s.GetRun(run.ID).ArtifactID != image.ID {
		t.Fatal("environment changed common build configuration")
	}
	if err := s.validateArtifactBuildVariant(jar, "test"); err != nil {
		t.Fatal(err)
	}
	if err := s.validateArtifactBuildVariant(jar, "prod"); err == nil {
		t.Fatal("JAR output bypassed environment build variant")
	}
	o, err := m.CreateArtifact("orders", jar.ID, "test", "isolated selection only", "alice")
	if err != nil || o.ArtifactID != jar.ID || o.Target.TargetType != "ssh-jar" {
		t.Fatal("release did not freeze original JAR target", o, err)
	}
	if _, err = m.CreateArtifact("orders", image.ID, "test", "wrong kind", "alice"); err == nil {
		t.Fatal("release accepted Docker bytes for Java target")
	}
	if s.GetServiceEnvironment("orders", "prod").TargetType != "docker-host" {
		t.Fatal("other environment changed")
	}
}

func TestEnvironmentJarOutputRequiresImmutablePairAndCompletedBuild(t *testing.T) {
	s, _, jar, image := pairedEnvironmentArtifacts(t)
	sv := s.GetService("orders")
	for name, mutate := range map[string]func(*Artifact){"identity": func(a *Artifact) { a.ID += 10000 }, "digest": func(a *Artifact) { a.Digest = "sha256:forged" }, "service": func(a *Artifact) { a.Service = "other" }, "source": func(a *Artifact) { a.Source = "manual" }, "run": func(a *Artifact) { a.SourceID += 10000 }} {
		t.Run(name, func(t *testing.T) {
			copy := *jar
			mutate(&copy)
			if s.artifactMatchesEnvironment(&copy, sv, "test") {
				t.Fatal("forged pair accepted")
			}
		})
	}
	for _, status := range []string{"running", "failed", "canceled"} {
		s.SetRunStatus(image.SourceID, status)
		if s.artifactMatchesEnvironment(jar, sv, "test") {
			t.Fatal("unfinished/failed build accepted", status)
		}
	}
	// Existing build-ready proof permits the original waiting pipeline to resume;
	// metadata or a stale proof for a failed run does not.
	s.SetRunStatus(image.SourceID, "running")
	if _, err := s.db.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?)`, fmt.Sprintf("build-ready:%d", image.SourceID), fmt.Sprintf("%d:%s", image.ID, image.Digest)); err != nil {
		t.Fatal(err)
	}
	if !s.artifactMatchesEnvironment(jar, sv, "test") {
		t.Fatal("verified completed build rejected")
	}
	s.SetRunStatus(image.SourceID, "failed")
	if s.artifactMatchesEnvironment(jar, sv, "test") {
		t.Fatal("stale build-ready proof authorized failure")
	}
}

func TestEnvironmentDriverDoesNotConvertOrInventArtifacts(t *testing.T) {
	s, _, jar, image := pairedEnvironmentArtifacts(t)
	if _, err := s.db.Exec(`DELETE FROM build_artifact_outputs WHERE artifact_id=?`, jar.ID); err != nil {
		t.Fatal(err)
	}
	if s.artifactMatchesEnvironment(jar, s.GetService("orders"), "test") || s.artifactForEnvironmentRun(s.GetRun(image.SourceID), "test") != nil {
		t.Fatal("unlinked JAR became deployable")
	}
	sv := &Service{Name: "simple", ArtifactType: "jar"}
	if environmentArtifactType(sv, &ServiceEnvironment{TargetType: "ssh-jar"}) != "jar" || environmentArtifactType(sv, &ServiceEnvironment{TargetType: "script"}) != "jar" {
		t.Fatal("ordinary deployment default changed")
	}
	// A pipeline waiting for image deployment must reject a Java environment before
	// claiming its stage or touching any deployment target.
	run := s.GetRun(image.SourceID)
	s.SetRunStatus(run.ID, "waiting_deploy")
	s.UpdateStage(run.ID, 0, Stage{Name: "deploy", Status: "waiting"})
	pr := &PipelineRunner{st: s}
	if err := pr.ContinueDeployEnvironment(run.ID, s.GetServiceEnvironment("orders", "test"), "fixture", "alice"); err == nil {
		t.Fatal("missing output accepted")
	} else if !strings.Contains(err.Error(), "制品") {
		t.Fatal("unexpected blocker", err)
	}
	if s.GetRun(run.ID).Status != "waiting_deploy" {
		t.Fatal("rejected selection claimed run")
	}
}
