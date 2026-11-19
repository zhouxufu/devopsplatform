package deploy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func variantFixture(t *testing.T, tool, kind string) (*Store, *ReleaseManager, DeliveryRequest) {
	t.Helper()
	s, m, req := deliveryFixture(t)
	sv := *s.GetService("orders")
	sv.BuildTool, sv.ArtifactType, sv.GitURL = tool, kind, "https://example.invalid/orders.git"
	sv.MavenGoals, sv.MavenExecutable, sv.MavenProperties = "clean package", "auto", "maven.test.skip=true"
	sv.GradleTasks, sv.GradleExecutable = "clean bootJar", "auto"
	if kind == "image" {
		sv.ImageName = "orders"
		sv.Steps = []StepInst{{Step: "git-checkout"}, {Step: "java-build"}, {Step: "docker-build", Params: map[string]string{"DOCKERFILE_SOURCE": "template", "DOCKERFILE_TEMPLATE": "java21-jar"}}}
	}
	if err := s.UpsertService(sv); err != nil {
		t.Fatal(err)
	}
	for _, env := range []string{"test", "prod"} {
		v := BuildVariant{Name: env}
		if tool == "maven" {
			v.MavenProfiles = env
			v.MavenProperties = "env=" + env
		} else {
			v.GradleProjectProperties = "env=" + env
			v.GradleSystemProperties = "app.stage=" + env
		}
		setVariant(t, s, env, v)
	}
	return s, m, req
}
func setVariant(t *testing.T, s *Store, env string, v BuildVariant) {
	t.Helper()
	p, e := s.deliveryPolicy(env)
	if e != nil {
		t.Fatal(e)
	}
	if p.Defaults.Applications == nil {
		p.Defaults.Applications = map[string]ApplicationDeliveryDefault{}
	}
	app := p.Defaults.Applications["orders"]
	app.BuildVariant = v
	p.Defaults.Applications["orders"] = app
	if e = s.saveDeliveryPolicy(env, p); e != nil {
		t.Fatal(e)
	}
}

// No compiler, Docker daemon, network or real release is invoked. Freeze only
// synthetic configuration and immutable fake artifact identities in a temp DB.
func variantArtifact(t *testing.T, s *Store, env string) *Artifact {
	t.Helper()
	sv, e := s.environmentBuildService(s.GetService("orders"), env)
	if e != nil {
		t.Fatal(e)
	}
	r := &PipelineRun{Service: "orders", Branch: "feature/fix", Actor: "alice"}
	if e = s.CreateRun(r); e != nil {
		t.Fatal(e)
	}
	if e = s.saveBuildSnapshot(r.ID, sv, s.GetServiceDelivery("orders")); e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec(`UPDATE build_run_snapshots SET commit_ref=? WHERE run_id=?`, strings.Repeat("a", 40), r.ID); e != nil {
		t.Fatal(e)
	}
	kind := ArtifactJar
	if sv.ArtifactType == "image" {
		kind = ArtifactDockerArchive
	}
	a, e := s.RecordPipelineArtifact(r.ID, "alice", Artifact{Service: "orders", Kind: kind, Version: "same-commit", Digest: fmt.Sprintf("sha256:fake%d", r.ID), URI: "/never-executed"})
	if e != nil {
		t.Fatal(e)
	}
	s.FinishRun(r.ID, "success")
	return a
}

func TestBuildVariantMavenGradleJarAndDockerProvenance(t *testing.T) {
	for _, tool := range []string{"maven", "gradle"} {
		for _, kind := range []string{"jar", "image"} {
			t.Run(tool+"/"+kind, func(t *testing.T) {
				s, _, _ := variantFixture(t, tool, kind)
				a := variantArtifact(t, s, "test")
				if a.Metadata["build_variant"] != "test" || a.Metadata["build_configuration_digest"] == "" {
					t.Fatal("missing frozen provenance", a.Metadata)
				}
				if e := s.validateArtifactBuildVariant(a, "test"); e != nil {
					t.Fatal(e)
				}
				if e := s.validateArtifactBuildVariant(a, "prod"); e == nil {
					t.Fatal("test artifact accepted in prod")
				}
				// Public labels, including matching names and fake digests, confer no rights.
				a.Metadata["build_variant"] = "prod"
				expected, _ := s.environmentBuildService(s.GetService("orders"), "prod")
				a.Metadata["build_configuration_digest"] = buildVariantDigest(expected, s.GetServiceDelivery("orders"))
				if e := s.validateArtifactBuildVariant(a, "prod"); e == nil {
					t.Fatal("metadata forged authorization")
				}
				// Current mutable settings cannot relabel an already-built package.
				sv := *s.GetService("orders")
				sv.ArtifactGlob = "changed/*.jar"
				if e := s.UpsertService(sv); e != nil {
					t.Fatal(e)
				}
				if e := s.validateArtifactBuildVariant(a, "test"); e == nil {
					t.Fatal("changed build config reused")
				}
			})
		}
	}
}

func TestBuildVariantCommonAndMissingProof(t *testing.T) {
	s, _, _ := variantFixture(t, "maven", "jar")
	common := BuildVariant{Name: "shared"}
	setVariant(t, s, "test", common)
	setVariant(t, s, "prod", common)
	a := variantArtifact(t, s, "test")
	if e := s.validateArtifactBuildVariant(a, "prod"); e != nil {
		t.Fatal("identical common build could not promote", e)
	}
	legacy := s.Artifacts("orders", 10)[1]
	legacy.Metadata = map[string]string{"build_variant": "shared"}
	if e := s.validateArtifactBuildVariant(&legacy, "test"); e == nil {
		t.Fatal("unknown legacy provenance allowed")
	}
	setVariant(t, s, "test", BuildVariant{})
	if e := s.validateArtifactBuildVariant(a, "test"); e == nil {
		t.Fatal("unconfigured destination bypassed variant")
	}
	setVariant(t, s, "prod", BuildVariant{})
	if e := s.validateArtifactBuildVariant(a, "test"); e == nil {
		t.Fatal("removing all bindings relabeled artifact")
	}
}

func TestBuildVariantAllDeliverySourcesFailBeforeAnyWrites(t *testing.T) {
	for _, tool := range []string{"maven", "gradle"} {
		t.Run(tool, func(t *testing.T) {
			s, m, req := variantFixture(t, tool, "jar")
			tpl := s.DeliveryTemplate(req.TemplateID)
			tpl.Mode = "version"
			tpl.Services = nil
			if err := s.SaveDeliveryTemplate(tpl); err != nil {
				t.Fatal(err)
			}
			a := variantArtifact(t, s, "test")
			req.Environment = "prod"
			req.Artifacts["orders"] = a.ID
			req.CodeConfirmed = true
			if _, e := m.CreateArtifact("orders", a.ID, "prod", "test", "alice"); e == nil {
				t.Fatal("ordinary release accepted test artifact")
			}
			c := &Change{Kind: "code", Title: "fixture fix", Service: "orders", Reference: "feature/fix", Owner: "alice"}
			if e := s.CreateChange(c); e != nil {
				t.Fatal(e)
			}
			w := &WorkItem{Kind: "bug", Title: "fixture bug", Owner: "alice"}
			if e := s.CreateWorkItem(w); e != nil {
				t.Fatal(e)
			}
			if e := s.SetChangeWorkItems(c.ID, []int64{w.ID}); e != nil {
				t.Fatal(e)
			}
			if _, e := s.SetChangeReady(c.ID, true); e != nil {
				t.Fatal(e)
			}
			v := &ReleaseVersion{Version: "v-fixture", Title: "fixture", Owner: "alice"}
			if e := s.CreateReleaseVersion(v); e != nil {
				t.Fatal(e)
			}
			if e := s.SetReleaseVersionChanges(v.ID, []int64{c.ID}); e != nil {
				t.Fatal(e)
			}
			if _, e := s.SetReleaseVersionReady(v.ID); e != nil {
				t.Fatal(e)
			}
			for _, source := range []string{"work_items", "version", "hotfix"} {
				r := req
				r.SourceType = "work_items"
				r.WorkItemIDs = []int64{w.ID}
				if source == "version" {
					r.SourceType = "version"
					r.VersionID = v.ID
					r.WorkItemIDs = nil
				}
				if source == "hotfix" {
					r.Scene = "hotfix"
				}
				p, _, e := s.ResolveDelivery(r, m)
				if e != nil {
					t.Fatal(source, e)
				}
				if !strings.Contains(strings.Join(p.Blockers, ";"), "不兼容") {
					t.Fatal("missing variant blocker", source, p.Blockers)
				}
				r.StrategyKind = "standard"
				r.TemplateID = 0
				p, _, e = s.ResolveDelivery(r, m)
				if e != nil || !strings.Contains(strings.Join(p.Blockers, ";"), "不兼容") {
					t.Fatal("standard delivery skipped variant gate", source, e, p)
				}
			}
			if len(s.ReleaseOrders("", 100)) != 0 || len(s.Deploys("orders", 100)) != 0 {
				t.Fatal("validation created release/deploy")
			}
		})
	}
}

func TestBuildVariantPreparationSnapshotAndOverrideRefusal(t *testing.T) {
	for _, tool := range []string{"maven", "gradle"} {
		t.Run(tool, func(t *testing.T) {
			s, _, _ := variantFixture(t, tool, "jar")
			original := s.GetService("orders")
			a, e := s.environmentBuildService(original, "test")
			if e != nil {
				t.Fatal(e)
			}
			b, e := s.environmentBuildService(original, "prod")
			if e != nil {
				t.Fatal(e)
			}
			if a.BuildVariant != "test" || b.BuildVariant != "prod" || original.BuildVariant != "" {
				t.Fatal("variant resolution changed app")
			}
			if tool == "maven" && (!strings.Contains(a.BuildCmd, "-Ptest") || !strings.Contains(a.BuildCmd, "-Denv=test") || !strings.Contains(a.BuildCmd, "-Dmaven.test.skip=true")) {
				t.Fatal(a.BuildCmd)
			}
			if tool == "gradle" && (!strings.Contains(a.BuildCmd, "-Penv=test") || !strings.Contains(a.BuildCmd, "-Dapp.stage=test")) {
				t.Fatal(a.BuildCmd)
			}
			digest, e := s.preparationEnvironmentConfiguration(original, "test")
			if e != nil {
				t.Fatal(e)
			}
			v := BuildVariant{Name: "test", MavenProfiles: "changed"}
			if tool == "gradle" {
				v = BuildVariant{Name: "test", GradleProjectProperties: "env=changed"}
			}
			setVariant(t, s, "test", v)
			changed, _ := s.preparationEnvironmentConfiguration(original, "test")
			if digest == changed {
				t.Fatal("environment config missing from preparation snapshot")
			}
			runner := NewPipelineRunner(s, NewBuilder(s, t.TempDir(), NewLogHub()), NewLogHub(), nil, NewStepLib(t.TempDir()))
			if _, e = runner.StartWithOptions(original, true, PipelineRunOptions{BuildEnvironment: "test", DeployEnvironment: "prod"}, "alice"); e == nil || !strings.Contains(e.Error(), "不兼容") {
				t.Fatal("incompatible automatic deployment started build", e)
			}
			opts := PipelineRunOptions{BuildEnvironment: "test", OverrideMaven: true, MavenProfiles: "prod", OverrideGradle: true, GradleProjectProperties: "env=prod"}
			if _, e = runner.StartWithOptions(original, false, opts, "alice"); e == nil || !strings.Contains(e.Error(), "覆盖参数") {
				t.Fatal("override not rejected before build", e)
			}
			if len(s.Runs("orders", 100)) != 0 {
				t.Fatal("invalid override started build")
			}
		})
	}
}

func TestBuildVariantCompatibilityAPIReadOnly(t *testing.T) {
	s, _, _ := variantFixture(t, "maven", "jar")
	a := variantArtifact(t, s, "test")
	h := Routes(s, NewBuilder(s, t.TempDir(), NewLogHub()), NewLogHub(), "", NewStepLib(t.TempDir()))
	for _, env := range []string{"test", "prod"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/artifacts/%d/build-compatibility?environment=%s", a.ID, env), nil))
		var result struct {
			Compatible bool   `json:"compatible"`
			Reason     string `json:"reason"`
		}
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &result) != nil || result.Compatible != (env == "test") {
			t.Fatal(rec.Code, rec.Body.String())
		}
	}
	if len(s.ReleaseOrders("", 100)) != 0 {
		t.Fatal("read-only compatibility check wrote release")
	}
}

func TestBuildVariantVersionMappingsAreFrozenPerEnvironment(t *testing.T) {
	for _, tool := range []string{"maven", "gradle"} {
		t.Run(tool, func(t *testing.T) {
			s, m, req := variantFixture(t, tool, "jar")
			v := versionForBinding(t, s, &req)
			testArtifact, prodArtifact := variantArtifact(t, s, "test"), variantArtifact(t, s, "prod")
			binding := func(a *Artifact) []VersionArtifactBinding {
				return []VersionArtifactBinding{{Service: "orders", ArtifactID: a.ID, CodeConfirmed: true}}
			}
			if _, e := s.SaveVersionArtifacts(v.ID, binding(testArtifact), "alice", "test"); e != nil {
				t.Fatal(e)
			}
			if _, e := s.SaveVersionArtifacts(v.ID, binding(testArtifact), "alice", "prod"); e == nil {
				t.Fatal("test package pinned to prod")
			}
			if _, e := s.SaveVersionArtifacts(v.ID, binding(prodArtifact), "alice", "prod"); e != nil {
				t.Fatal(e)
			}
			req.Artifacts["orders"] = testArtifact.ID
			req.CodeConfirmed = true
			p, _, e := s.ResolveDelivery(req, m)
			if e != nil || len(p.Blockers) > 0 {
				t.Fatal(e, p.Blockers)
			}
			req = preflightFor(t, s, m, req, "alice")
			if _, e = s.CreateDeliveryRun(req, "alice", m); e != nil {
				t.Fatal(e)
			}
			if !s.VersionArtifacts(v.ID, "test").Locked || s.VersionArtifacts(v.ID, "prod").Locked {
				t.Fatal("locking crossed environments")
			}
			if _, e = s.SaveVersionArtifacts(v.ID, binding(testArtifact), "alice", "test"); e == nil {
				t.Fatal("overwrote locked mapping")
			}
			if _, e = s.SaveVersionArtifacts(v.ID, binding(prodArtifact), "alice", "prod"); e != nil {
				t.Fatal("test lock prevented production build mapping", e)
			}
			req.PreflightToken = ""
			req.Environment = "prod"
			req.Artifacts["orders"] = prodArtifact.ID
			req = preflightFor(t, s, m, req, "alice")
			if _, e = s.CreateDeliveryRun(req, "alice", m); e != nil {
				t.Fatal(e)
			}
			if !s.VersionArtifacts(v.ID, "prod").Locked || s.VersionArtifacts(v.ID, "test").Bindings[0].ArtifactID != testArtifact.ID {
				t.Fatal("production overwrote test evidence")
			}
			if len(s.ReleaseOrders("", 100)) != 0 {
				t.Fatal("mapping/submission executed a deployment")
			}
		})
	}
}

func TestBuildVariantRechecksBeforeManualOrResourceNode(t *testing.T) {
	s, m, req := variantFixture(t, "gradle", "jar")
	a := variantArtifact(t, s, "test")
	req.Artifacts["orders"] = a.ID
	tpl := s.DeliveryTemplate(req.TemplateID)
	tpl.Nodes = append([]DeliveryNode{{ID: "check", Kind: "manual", Name: "Before SQL or deploy", Instructions: "Synthetic confirmation only"}}, tpl.Nodes...)
	if err := s.SaveDeliveryTemplate(tpl); err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateDeliveryRun(req, "alice", m)
	if err != nil {
		t.Fatal(err)
	}
	sv := *s.GetService("orders")
	sv.GradleExcludedTasks = "test"
	if err = s.UpsertService(sv); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AdvanceDeliveryRun(run.ID, "alice", true, m); err == nil {
		t.Fatal("configuration drift allowed side-effect node")
	}
	saved := s.DeliveryRun(run.ID)
	for _, step := range saved.Steps {
		if step.Status != "pending" {
			t.Fatal("node advanced before compatibility check", step)
		}
	}
}

func TestBuildVariantBareHostAndIncompleteBuildCannotBypass(t *testing.T) {
	s, _, _ := variantFixture(t, "maven", "jar")
	a := variantArtifact(t, s, "test")
	if e := s.validateArtifactBuildVariant(a, ""); e == nil {
		t.Fatal("bare host bypassed environment")
	}
	s.SetRunStatus(a.SourceID, "running")
	if e := s.validateArtifactBuildVariant(a, "test"); e == nil {
		t.Fatal("incomplete pipeline released")
	}
	if _, e := s.db.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?)`, fmt.Sprintf("build-ready:%d", a.SourceID), fmt.Sprintf("%d:%s", a.ID, a.Digest)); e != nil {
		t.Fatal(e)
	}
	if e := s.validateArtifactBuildVariant(a, "test"); e != nil {
		t.Fatal("completed build could not continue deploy", e)
	}
	s.SetRunStatus(a.SourceID, "canceled")
	if e := s.validateArtifactBuildVariant(a, "test"); e == nil {
		t.Fatal("canceled build released")
	}
}

func TestBuildVariantDigestDoesNotChangeCheckoutOrBuildParameters(t *testing.T) {
	sv := &Service{Name: "orders", Branch: "master-dev-optimize", BuildTool: "maven", MavenGoals: "clean package", Steps: []StepInst{
		{Step: "git-checkout", Params: map[string]string{"GIT_URL": "https://example.invalid/orders.git", "BRANCH": "master-dev-optimize"}},
		{Step: "java-build", Params: map[string]string{"COMMAND": "mvn clean package"}},
		{Step: "docker-build", Params: map[string]string{"DOCKERFILE_SOURCE": "template", "DOCKERFILE_TEMPLATE": "java21-jar", "DOCKERFILE_ARTIFACT_PATH": "target/app.jar"}},
	}}
	before, _ := json.Marshal(sv)
	first := buildVariantDigest(sv, nil)
	after, _ := json.Marshal(sv)
	if string(before) != string(after) {
		t.Fatal("computing compatibility evidence mutated the checkout/build configuration")
	}
	if sv.Steps[0].Params["BRANCH"] != "master-dev-optimize" {
		t.Fatal("checkout would fall back to origin/HEAD")
	}
	sv.Branch = "another-branch"
	sv.Steps[0].Params["BRANCH"] = "another-branch"
	if buildVariantDigest(sv, nil) != first {
		t.Fatal("source branch unexpectedly became a build-variant contract")
	}
	sv.Steps[1].Params["COMMAND"] = "mvn clean verify"
	if buildVariantDigest(sv, nil) == first {
		t.Fatal("build command change did not change the build contract")
	}
}
