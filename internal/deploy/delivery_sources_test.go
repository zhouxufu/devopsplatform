package deploy

import (
	"strings"
	"testing"
)

func TestDeliveryArtifactSourcesMixedAndStrict(t *testing.T) {
	scope := &DeliveryScope{Changes: []Change{{ID: 1, Kind: "code", Service: "a"}, {ID: 2, Kind: "code", Service: "a"}, {ID: 3, Kind: "code", Service: "b"}}}
	req := DeliveryRequest{ArtifactSources: map[string]string{"a": "build", "b": "existing"}}
	modes, err := deliveryArtifactSources(req, scope, nil)
	if err != nil || len(modes) != 2 || modes["b"] != "existing" {
		t.Fatal(modes, err)
	}
	for _, bad := range []map[string]string{{"a": "build"}, {"a": "build", "b": "bad"}, {"a": "build", "c": "existing"}} {
		req.ArtifactSources = bad
		if _, err := deliveryArtifactSources(req, scope, nil); err == nil {
			t.Fatal("invalid sources accepted", bad)
		}
	}
	req.ArtifactSources = map[string]string{"a": "build", "b": "existing"}
	scope.Changes = append(scope.Changes, Change{ID: 4, Kind: "artifact", Service: "a"})
	if _, err := deliveryArtifactSources(req, scope, nil); err == nil {
		t.Fatal("artifact and build mixed for same app")
	}
}

func TestVersionArtifactCannotBeSlicedByBugCheckbox(t *testing.T) {
	a := Change{ID: 1, Kind: "code", Service: "a"}
	b := Change{ID: 2, Kind: "code", Service: "a"}
	c := Change{ID: 3, Kind: "code", Service: "b"}
	v := &ReleaseVersion{Changes: []Change{a, b, c}}
	pinned := VersionArtifacts{Bindings: []VersionArtifactBinding{{Service: "a", ArtifactID: 1}, {Service: "b", ArtifactID: 2}}}
	if validateVersionArtifactSubset(&DeliveryScope{Changes: []Change{a}}, v, pinned) == nil {
		t.Fatal("partial app scope silently reuses full image")
	}
	if err := validateVersionArtifactSubset(&DeliveryScope{Changes: []Change{a, b}}, v, pinned); err != nil {
		t.Fatal("excluding whole app must remain allowed", err)
	}
}

func TestDeliveryDefaultsLegacyCASAndPreflightRevision(t *testing.T) {
	s, _, req := deliveryFixture(t)
	// Existing installations stored the smaller JSON shape. Adding defaults must
	// not make every subsequent compare-and-swap fail on re-marshaled JSON.
	_, err := s.db.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?)`, "delivery_policy:"+req.Environment, `{"revision":1,"stages":[]}`)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := s.deliveryPolicy(req.Environment)
	p.Defaults = DeliveryDefaults{StrategyKind: "standard", ArtifactSource: "build", Applications: map[string]ApplicationDeliveryDefault{"orders": {CodePolicy: CodePolicy{Mode: "integration", TargetBranch: "dev"}}}}
	if err := s.saveDeliveryPolicy(req.Environment, p); err != nil {
		t.Fatal(err)
	}
	if err := s.saveDeliveryPolicy(req.Environment, p); err == nil {
		t.Fatal("stale revision accepted")
	}
	actual, _ := s.deliveryPolicy(req.Environment)
	if actual.Revision != 2 || actual.Defaults.Applications["orders"].CodePolicy.TargetBranch != "dev" {
		t.Fatal(actual)
	}
	plan := &DeliveryPlan{Environment: *s.GetEnvironment(req.Environment), PolicySnapshot: &DeliveryPolicySnapshot{Revision: 1, Environment: *s.GetEnvironment(req.Environment)}}
	if s.checkDeliveryPolicy(plan) == nil {
		t.Fatal("changed defaults did not invalidate approved snapshot")
	}
	actual.Defaults = DeliveryDefaults{StrategyKind: "custom", TemplateID: 99999}
	if s.saveDeliveryPolicy(req.Environment, actual) == nil {
		t.Fatal("missing default template accepted")
	}
}

func TestMixedPreparedArtifactsRequireExistingCoverage(t *testing.T) {
	s, manager, req := deliveryFixture(t)
	sv := *s.GetService("orders")
	sv.Name = "billing"
	sv.ApplicationID = ""
	if err := s.UpsertService(sv); err != nil {
		t.Fatal(err)
	}
	w := &WorkItem{Title: "Mixed delivery", Kind: "bug"}
	if err := s.CreateWorkItem(w); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"orders", "billing"} {
		c := &Change{Title: name, Kind: "code", Service: name, Reference: "feature/fix"}
		if err := s.CreateChange(c); err != nil {
			t.Fatal(err)
		}
		if err := s.SetChangeWorkItems(c.ID, []int64{w.ID}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SetChangeReady(c.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	req.StrategyKind = "standard"
	req.TemplateID = 0
	req.VersionID = 0
	req.SourceType = "work_items"
	req.WorkItemIDs = []int64{w.ID}
	req.Nodes = nil
	req.ArtifactSources = map[string]string{"orders": "build", "billing": "existing"}
	tpl, _ := s.deliveryStrategyTemplate(req)
	scope, _, err := s.resolveDeliveryScope(req, tpl)
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("a", 40)
	r := &PipelineRun{Service: "orders", Actor: "alice", Branch: sha}
	if err = s.CreateRun(r); err != nil {
		t.Fatal(err)
	}
	a := &Artifact{Service: "orders", Version: "built", Kind: "jar", URI: "/not-executed.jar", Digest: "sha256:built", Source: "pipeline-run", SourceID: r.ID}
	if err = s.RegisterArtifact(a); err != nil {
		t.Fatal(err)
	}
	s.db.Exec(`UPDATE pipeline_runs SET artifact_id=? WHERE id=?`, a.ID, r.ID)
	s.db.Exec(`INSERT INTO build_run_snapshots(run_id,config_cipher,commit_ref,created_at) VALUES(?,'fixture',?,1)`, r.ID, sha)
	s.FinishRun(r.ID, "success")
	b := &Artifact{Service: "billing", Version: "tested", Kind: "jar", URI: "/not-executed.jar", Digest: "sha256:tested"}
	if err = s.RegisterArtifact(b); err != nil {
		t.Fatal(err)
	}
	p := &CodePreparation{ID: "mixed-fixture", State: "ready", Owner: "alice", Request: req, Protected: s.GetEnvironment(req.Environment).Protected, ScopeDigest: codeScopeDigest(scope), Builds: []PreparedBuild{{Service: "orders", Commit: sha, RunID: r.ID, ArtifactID: a.ID, ArtifactDigest: a.Digest, ConfigurationDigest: preparationConfiguration(s.GetService("orders"), s.GetServiceDelivery("orders"))}}}
	if err = s.saveCodePreparation(p); err != nil {
		t.Fatal(err)
	}
	req.CodePreparationID = p.ID
	req.Artifacts = map[string]int64{"orders": a.ID, "billing": b.ID}
	req.CodeConfirmed = false
	if err = s.validatePreparedArtifacts(req, scope); err != nil {
		t.Fatal("mixed artifacts rejected", err)
	}
	plan, _, err := s.ResolveDelivery(req, manager)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(plan.Blockers, " "), "核对每个制品") {
		t.Fatal("existing artifact confirmation bypassed", plan.Blockers)
	}
	req.ArtifactSources = map[string]string{"orders": "existing", "billing": "build"}
	if s.validatePreparedArtifacts(req, scope) == nil {
		t.Fatal("prepared app swap accepted")
	}
	req.CodePreparationID = ""
	plan, _, err = s.ResolveDelivery(req, manager)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(plan.Blockers, " "), "本次构建尚未完成") {
		t.Fatal("unfinished build bypassed", plan.Blockers)
	}
}
