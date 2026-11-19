package deploy

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func manualFixture(t *testing.T) (*Store, *ReleaseManager, DeliveryRequest) {
	s, m, req := deliveryFixture(t)
	target := ServiceEnvironment{Service: "orders", Environment: "test", TargetType: TargetManual, DeployConfig: map[string]string{"manual_artifact_kind": ArtifactJar, "manual_assignee": "operator", "manual_instructions": "下载审核制品，核对 SHA-256 后发布，检查接口与日志"}}
	if err := s.UpsertServiceEnvironment(target); err != nil {
		t.Fatal(err)
	}
	return s, m, req
}

func TestManualPublicationApprovalResultAndNoAutomaticDeployment(t *testing.T) {
	s, m, req := manualFixture(t)
	order, err := m.CreateArtifact("orders", req.Artifacts["orders"], "test", "synthetic", "alice")
	if err != nil || order.Status != "waiting_manual" || order.Total != 1 || order.Manual == nil {
		t.Fatal(order, err)
	}
	if len(s.Deploys("orders", 10)) != 0 {
		t.Fatal("manual publication launched an executor")
	}
	if _, err = m.CreateArtifact("orders", req.Artifacts["orders"], "test", "another", "alice"); err == nil {
		t.Fatal("waiting manual publication did not reserve target")
	}
	result := ManualPublicationResult{ArtifactID: order.ArtifactID, Digest: order.Manual.Digest, Result: "success", PerformedAt: time.Now().Unix(), Comment: "合成数据：已核对摘要，发布后接口检查通过"}
	if err = m.CompleteManual(order.ID, result, "alice", false); err == nil {
		t.Fatal("non-assignee confirmed")
	}
	changed := result
	changed.Digest = "wrong"
	if m.CompleteManual(order.ID, changed, "operator", false) == nil {
		t.Fatal("wrong artifact accepted")
	}
	changed = result
	changed.Comment = ""
	if m.CompleteManual(order.ID, changed, "operator", false) == nil {
		t.Fatal("empty evidence accepted")
	}
	changed = result
	changed.PerformedAt = order.StartedAt - 1
	if m.CompleteManual(order.ID, changed, "operator", false) == nil {
		t.Fatal("invalid publication time accepted")
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- m.CompleteManual(order.ID, result, "operator", false) }()
	}
	wg.Wait()
	close(results)
	passes := 0
	for e := range results {
		if e == nil {
			passes++
		}
	}
	if passes != 1 {
		t.Fatal("duplicate confirmation", passes)
	}
	got := s.GetReleaseOrder(order.ID)
	if got.Status != "success" || got.Succeeded != 1 || got.Manual.Actor != "operator" || got.Manual.PerformedAt != result.PerformedAt || len(s.Deploys("orders", 10)) != 0 {
		t.Fatal(got)
	}
	if m.Rollback(order.ID, "rollback", "alice") == nil {
		t.Fatal("manual release attempted automatic rollback")
	}
}

func TestManualPublicationProtectedApprovalAndConfigurationDrift(t *testing.T) {
	s, m, req := manualFixture(t)
	target := *s.GetServiceEnvironment("orders", "test")
	target.Environment = "prod"
	if err := s.UpsertServiceEnvironment(target); err != nil {
		t.Fatal(err)
	}
	o, err := m.CreateArtifact("orders", req.Artifacts["orders"], "prod", "controlled fixture", "alice")
	if err != nil || o.Status != "pending_approval" {
		t.Fatal(o, err)
	}
	result := ManualPublicationResult{ArtifactID: o.ArtifactID, Digest: o.Manual.Digest, Result: "success", PerformedAt: time.Now().Unix(), Comment: "synthetic evidence"}
	if m.CompleteManual(o.ID, result, "operator", false) == nil {
		t.Fatal("approval bypassed")
	}
	if m.Approve(o.ID, "alice") == nil {
		t.Fatal("self approval")
	}
	if err = m.Approve(o.ID, "reviewer"); err != nil {
		t.Fatal(err)
	}
	if s.GetReleaseOrder(o.ID).Status != "waiting_manual" {
		t.Fatal("approval launched deployment")
	}
	target.DeployConfig["manual_instructions"] = "different reviewed instructions"
	if err = s.UpsertServiceEnvironment(target); err != nil {
		t.Fatal(err)
	}
	if m.CompleteManual(o.ID, result, "operator", false) == nil {
		t.Fatal("configuration drift accepted")
	}
	if err = m.Cancel(o.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	if s.GetReleaseOrder(o.ID).Status != "canceled" {
		t.Fatal("manual cancel failed")
	}
}

func TestManualPublicationDeliveryPausesAndContinues(t *testing.T) {
	s, m, req := manualFixture(t)
	run, err := s.CreateDeliveryRun(req, "alice", m)
	if err != nil {
		t.Fatal(err)
	}
	launched := 0
	run, err = s.advanceDeliveryRun(run.ID, "alice", false, m, func(int64) { launched++ })
	if err != nil {
		t.Fatal(err)
	}
	child := s.GetReleaseOrder(run.Steps[0].ReleaseID)
	if child == nil || child.Status != "waiting_manual" || launched != 0 {
		t.Fatal(child, launched)
	}
	if child.Number != 1 || s.ReleaseOrderNumber(child.ID, "orders") != 1 {
		t.Fatalf("orchestrated release lost application number: %+v", child)
	}
	control, _ := deliveryControlDecision(s, run)
	if control != "pause" {
		t.Fatal("coordinator did not pause", control)
	}
	result := ManualPublicationResult{ArtifactID: child.ArtifactID, Digest: child.Manual.Digest, Result: "success", PerformedAt: time.Now().Unix(), Comment: "fixture publication evidence"}
	if err = m.CompleteManual(child.ID, result, "operator", false); err != nil {
		t.Fatal(err)
	}
	run, err = s.advanceDeliveryRun(run.ID, "alice", false, m, func(int64) { launched++ })
	if err != nil || run.Steps[0].Status != "success" {
		t.Fatal(run, err)
	}
	run, err = s.advanceDeliveryRun(run.ID, "alice", false, m, func(int64) { launched++ })
	if err != nil || run.Status != "success" || launched != 0 {
		t.Fatal(run, err, launched)
	}
}

func TestManualPublicationFailureRetriesOnlyCurrentApplication(t *testing.T) {
	s, m, req := manualFixture(t)
	run, err := s.CreateDeliveryRun(req, "alice", m)
	if err != nil {
		t.Fatal(err)
	}
	launched := 0
	launch := func(int64) { launched++ }
	run, err = s.advanceDeliveryRun(run.ID, "alice", false, m, launch)
	if err != nil {
		t.Fatal(err)
	}
	original := run.Steps[0].ReleaseID
	child := s.GetReleaseOrder(original)
	if err = m.CompleteManual(original, ManualPublicationResult{ArtifactID: child.ArtifactID, Digest: child.Manual.Digest, Result: "failed", PerformedAt: time.Now().Unix(), Comment: "合成失败，需重新安排发布"}, "operator", false); err != nil {
		t.Fatal(err)
	}
	run, err = s.advanceDeliveryRun(run.ID, "alice", false, m, launch)
	if err != nil {
		t.Fatal(err)
	}
	step := run.Steps[0]
	request := deliveryDeploymentRecoveryRequest{NodeID: step.NodeID, Service: step.Service, ReleaseID: original, Digest: run.Digest, Revision: run.Revision}
	retry, err := s.previewDeploymentRetry(run, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(retry.Targets) != 0 {
		t.Fatal("manual retry invented machine targets", retry.Targets)
	}
	run, err = s.retryDeliveryDeployment(run, request, "alice", m, launch)
	if err != nil {
		t.Fatal(err)
	}
	next := s.GetReleaseOrder(run.Steps[0].ReleaseID)
	if next == nil || next.ID == original || next.Status != "waiting_manual" || launched != 0 || len(run.Steps[0].PreviousReleaseIDs) != 1 || s.GetReleaseOrder(original).Manual.Result != "failed" {
		t.Fatal(run, next, launched)
	}
}

func TestMultiArtifactIdentityMigratesWithoutRewritingLegacyOutput(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := Artifact{Service: "fixture", Kind: ArtifactOCI, URI: "registry.example.invalid/app@sha256:old", Digest: "sha256:old", Version: "same", Source: "pipeline-run", SourceID: 7}
	if err = s.RegisterArtifact(&a); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`CREATE UNIQUE INDEX idx_artifacts_source ON artifacts(source,source_id) WHERE source<>'' AND source_id>0`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	jar := Artifact{Service: a.Service, Kind: ArtifactJar, URI: "/synthetic.jar", Digest: "sha256:jar", Version: a.Version, Source: a.Source, SourceID: a.SourceID}
	if err = s.RegisterArtifact(&jar); err != nil {
		t.Fatal(err)
	}
	original := s.GetArtifact(a.ID)
	if jar.ID == a.ID || original == nil || original.Digest != a.Digest || original.Kind != a.Kind {
		t.Fatal("legacy identity overwritten", original, jar)
	}
	again := jar
	if err = s.RegisterArtifact(&again); err != nil || again.ID != jar.ID {
		t.Fatal("identical retry duplicated output", again, err)
	}
}

func TestCompiledJarPairPreservesEnvironmentContract(t *testing.T) {
	s, m, _ := variantFixture(t, "gradle", "image")
	image := variantArtifact(t, s, "test")
	jar, err := s.RecordPipelineArtifact(image.SourceID, "alice", Artifact{Service: image.Service, Version: image.Version, Kind: ArtifactJar, URI: "/fixture-only.jar", Digest: "sha256:jar-input", Metadata: map[string]string{"artifact_role": "compilation", "build_command": "private-fixture-command"}})
	if err != nil {
		t.Fatal(err)
	}
	image.Metadata["compiled_input_digest"] = jar.Digest
	paired, err := s.RecordPipelineArtifact(image.SourceID, "alice", *image)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.linkCompilationArtifact(image.SourceID, jar, paired); err != nil {
		t.Fatal(err)
	}
	if err = s.validateArtifactBuildVariant(jar, "test"); err != nil {
		t.Fatal("paired JAR lost immutable snapshot", err)
	}
	if s.validateArtifactBuildVariant(jar, "prod") == nil {
		t.Fatal("paired JAR bypassed environment variant")
	}
	target := ServiceEnvironment{Service: "orders", Environment: "test", TargetType: TargetManual, DeployConfig: map[string]string{"manual_artifact_kind": ArtifactJar, "manual_instructions": "fixture"}}
	if err = s.UpsertServiceEnvironment(target); err != nil {
		t.Fatal(err)
	}
	run := s.GetRun(image.SourceID)
	for _, output := range run.Artifacts {
		if output.Metadata["build_command"] != "" {
			t.Fatal("run output collection disclosed private build command")
		}
	}
	if run.ArtifactID != paired.ID || len(run.Artifacts) != 2 || s.artifactForEnvironmentRun(run, "test").ID != jar.ID {
		t.Fatal("primary / multiple outputs / environment choice changed", run)
	}
	if !s.artifactMatchesEnvironment(jar, s.GetService("orders"), "test") {
		t.Fatal("paired JAR cannot be selected")
	}
	o, err := m.Create("orders", image.Version, "test", "same build, choose target kind", "alice")
	if err != nil || o.ArtifactID != jar.ID || o.Status != "waiting_manual" {
		t.Fatal("version selector confused JAR and image", o, err)
	}
	forged := *jar
	forged.ID = 12345
	if s.artifactMatchesEnvironment(&forged, s.GetService("orders"), "test") {
		t.Fatal("display metadata impersonated pair")
	}
	s.FinishRun(run.ID, "failed")
	if s.artifactMatchesEnvironment(jar, s.GetService("orders"), "test") || s.artifactBuildComplete(jar) == nil {
		t.Fatal("failed build exposed compilation output")
	}
}

func TestArtifactDownloadUsesVerifiedManagedBytes(t *testing.T) {
	s, _, _ := manualFixture(t)
	root := filepath.Join(s.dataDir, "artifacts")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(root, "fixture.jar")
	os.WriteFile(name, []byte("fixture-only-jar"), 0600)
	a, err := describeArtifact("orders", "fixture", ArtifactJar, name, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RegisterArtifact(&a); err != nil {
		t.Fatal(err)
	}
	b := NewBuilder(s, s.dataDir, NewLogHub())
	mux := Routes(s, b, NewLogHub(), "", NewStepLib(t.TempDir()))
	request := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", fmt.Sprintf("/api/artifacts/%d/download", a.ID), nil))
		return w
	}
	w := request()
	if w.Code != 200 || w.Body.String() != "fixture-only-jar" || w.Header().Get("X-Artifact-Digest") != a.Digest {
		t.Fatal(w.Code, w.Body.String())
	}
	os.WriteFile(name, []byte("changed-after-review"), 0600)
	if w = request(); w.Code != 409 {
		t.Fatal("changed bytes downloaded", w.Code)
	}
	outside := filepath.Join(t.TempDir(), "secret.jar")
	os.WriteFile(outside, []byte("not-an-artifact"), 0600)
	os.Remove(name)
	os.Symlink(outside, name)
	if w = request(); w.Code != 409 || strings.Contains(w.Body.String(), "not-an-artifact") {
		t.Fatal("storage escape", w.Code, w.Body.String())
	}
	if s.GetReleaseOrder(1) != nil {
		t.Fatal("download created publication")
	}
}

func TestRetainDockerJarCopiesExactCompilationInput(t *testing.T) {
	workspace, output := t.TempDir(), t.TempDir()
	started := time.Now().Add(-time.Second)
	os.MkdirAll(filepath.Join(workspace, "target"), 0755)
	os.WriteFile(filepath.Join(workspace, "target/app.jar"), []byte("this-run"), 0600)
	p, metadata, cleanup, err := prepareDockerCompilation(map[string]string{"DOCKERFILE_SOURCE": "template", "DOCKERFILE_TEMPLATE": "java21", "DOCKERFILE_ARTIFACT_PATH": "target/*.jar"}, workspace, output, map[string]time.Time{"java-build": started})
	if err != nil {
		t.Fatal(err)
	}
	jar, err := retainDockerJar(p, metadata, output)
	cleanup()
	if err != nil {
		t.Fatal(err)
	}
	digest, _, err := artifactMetadata(jar)
	if err != nil || digest != metadata["compiled_input_digest"] {
		t.Fatal(digest, err)
	}
	os.WriteFile(filepath.Join(workspace, "target/app.jar"), []byte("workspace-changed"), 0600)
	bytes, _ := os.ReadFile(jar)
	if string(bytes) != "this-run" {
		t.Fatal("workspace mutation changed archived JAR")
	}
}

func TestImageBuildRegistersJarAndImageWithoutDocker(t *testing.T) {
	for _, mode := range []string{"template", "export", "image-only"} {
		t.Run(mode, func(t *testing.T) { testImageArtifactSet(t, mode) })
	}
}
func testImageArtifactSet(t *testing.T, mode string) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	library, workspace := t.TempDir(), t.TempDir()
	for id, body := range map[string]string{
		"java-build":   "#!/bin/bash\nset -eu\nmkdir -p \"$LP_WORKSPACE/target\"\nprintf this-run-jar >\"$LP_WORKSPACE/target/app.jar\"\n",
		"docker-build": "#!/bin/bash\nset -eu\n[ -s \"$LP_WORKSPACE/target/app.jar\" ]\nprintf 'ARTIFACT=registry.example.invalid/orders@sha256:fixture\\nARTIFACT_KIND=oci-image\\nARTIFACT_DIGEST=sha256:fixture\\n' >\"$LP_OUTPUTS_FILE\"\n",
	} {
		dir := filepath.Join(library, id)
		os.MkdirAll(dir, 0755)
		kind := "build"
		if id == "docker-build" {
			kind = "package"
		}
		yaml := "apiVersion: deploy.logplatform.io/v1\nid: " + id + "\nname: " + id + "\nkind: " + kind + "\n"
		if id == "docker-build" {
			yaml += "produces: oci-image\nparams:\n  - {name: CONTEXT, type: input}\n  - {name: DOCKERFILE_SOURCE, type: input}\n  - {name: DOCKERFILE_TEMPLATE, type: input}\n  - {name: DOCKERFILE_ARTIFACT_PATH, type: input}\n  - {name: EXPORT_JAR_GLOB, type: input}\n"
		}
		os.WriteFile(filepath.Join(dir, "step.yaml"), []byte(yaml), 0600)
		os.WriteFile(filepath.Join(dir, "run.sh"), []byte(body), 0700)
	}
	sv := &Service{Name: "fixture", Workdir: workspace, ArtifactType: "image", Steps: []StepInst{{Step: "java-build"}, {Step: "docker-build", Params: map[string]string{"DOCKERFILE_SOURCE": "template", "DOCKERFILE_TEMPLATE": "java21", "DOCKERFILE_ARTIFACT_PATH": "target/*.jar"}}}}
	if mode != "template" {
		sv.Steps[1].Params["DOCKERFILE_SOURCE"] = "repository"
		if mode == "export" {
			sv.Steps[1].Params["EXPORT_JAR_GLOB"] = "target/*.jar"
		}
	}
	if err = s.UpsertService(*sv); err != nil {
		t.Fatal(err)
	}
	r := &PipelineRun{Service: sv.Name, Actor: "alice"}
	if err = s.CreateRun(r); err != nil {
		t.Fatal(err)
	}
	hub := NewLogHub()
	pr := NewPipelineRunner(s, NewBuilder(s, t.TempDir(), hub), hub, nil, NewStepLib(library))
	pr.runContext(context.Background(), r.ID, sv, false)
	got := s.GetRun(r.ID)
	expected := 2
	if mode == "image-only" {
		expected = 1
	}
	if got.Status != "success" || len(got.Artifacts) != expected || got.ArtifactKind != ArtifactOCI {
		t.Fatal("two-output pipeline failed", got, s.RunLog(r.ID))
	}
	if mode == "image-only" {
		return
	}
	var jar, image *Artifact
	for _, a := range got.Artifacts {
		copy := a
		if a.Kind == ArtifactJar {
			jar = &copy
		} else if a.Kind == ArtifactOCI {
			image = &copy
		}
	}
	if jar == nil || image == nil || jar.ID == image.ID || (image.Metadata["compiled_input_digest"] != jar.Digest && image.Metadata["related_output_digest"] != jar.Digest) || !s.pairedCompilation(jar, image.ID) {
		t.Fatal(jar, image)
	}
	if bytes, e := os.ReadFile(jar.URI); e != nil || string(bytes) != "this-run-jar" {
		t.Fatal("JAR bytes unavailable", e)
	}
}
