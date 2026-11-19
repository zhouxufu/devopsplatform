package deploy

import (
	"fmt"
	"strings"
	"testing"
)

func parallelPreparationFixture(t *testing.T) (*Store, *CodePreparation, func(*PreparedBuild) (int64, error)) {
	t.Helper()
	st, _, _ := deliveryFixture(t)
	req := DeliveryRequest{StrategyKind: "standard", SourceType: "changes", Environment: "test"}
	p := &CodePreparation{ID: "parallel-fixture", Owner: "alice", State: "building", BuildConcurrency: 2}
	for _, name := range []string{"orders", "billing", "shipping"} {
		if name != "orders" {
			if err := st.UpsertService(Service{Name: name, ArtifactType: "jar", BuildTool: "command", BuildCmd: "true", ArtifactGlob: "*.jar"}); err != nil {
				t.Fatal(err)
			}
		}
		c := &Change{Title: name, Kind: "code", Service: name, Reference: "feature/test", Owner: "alice"}
		if err := st.CreateChange(c); err != nil {
			t.Fatal(err)
		}
		if _, err := st.SetChangeReady(c.ID, true); err != nil {
			t.Fatal(err)
		}
		req.SourceChangeIDs = append(req.SourceChangeIDs, c.ID)
		config, err := st.preparationEnvironmentConfiguration(st.GetService(name), "test")
		if err != nil {
			t.Fatal(err)
		}
		p.Builds = append(p.Builds, PreparedBuild{Service: name, Commit: strings.Repeat("a", 40), ConfigurationDigest: config, Status: "pending"})
	}
	p.Request = req
	tpl, err := st.deliveryStrategyTemplate(req)
	if err != nil {
		t.Fatal(err)
	}
	scope, _, err := st.resolveDeliveryScope(req, tpl)
	if err != nil {
		t.Fatal(err)
	}
	p.ScopeDigest = codeScopeDigest(scope)
	if err = st.saveCodePreparation(p); err != nil {
		t.Fatal(err)
	}
	start := func(b *PreparedBuild) (int64, error) {
		run := &PipelineRun{Service: b.Service, Actor: p.Owner, Branch: b.Commit, Trigger: "code-preparation:" + p.ID + ":" + b.Service}
		if err := st.CreateRun(run); err != nil {
			return 0, err
		}
		return run.ID, nil
	}
	return st, p, start
}
func completePreparedFixture(t *testing.T, s *Store, b PreparedBuild) {
	t.Helper()
	a := &Artifact{Service: b.Service, Version: b.Commit[:7], Kind: "jar", URI: "/fixture-not-executed.jar", Digest: "sha256:fixture", Source: "pipeline-run", SourceID: b.RunID}
	if err := s.RegisterArtifact(a); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE pipeline_runs SET artifact_id=? WHERE id=?`, a.ID, b.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO build_run_snapshots(run_id,config_cipher,commit_ref,created_at) VALUES(?,'fixture',?,1)`, b.RunID, b.Commit); err != nil {
		t.Fatal(err)
	}
	s.FinishRun(b.RunID, "success")
}
func TestPreparedBuildConcurrencyRecoveryAndDrain(t *testing.T) {
	s, p, start := parallelPreparationFixture(t)
	done, err := s.advancePreparedBuilds(p, start)
	if err != nil || done || p.Builds[0].RunID == 0 || p.Builds[1].RunID == 0 || p.Builds[2].RunID != 0 {
		t.Fatal("not bounded parallel", p, err)
	}
	// Lost returned ID after a crash: same trigger must recover, not start again.
	p.Builds[0].RunID = 0
	p.Builds[0].Status = "starting"
	if err = s.saveCodePreparation(p); err != nil {
		t.Fatal(err)
	}
	p = s.CodePreparation(p.ID)
	if _, err = s.advancePreparedBuilds(p, func(*PreparedBuild) (int64, error) { t.Fatal("replayed active start"); return 0, nil }); err != nil {
		t.Fatal(err)
	}
	completePreparedFixture(t, s, p.Builds[0])
	if _, err = s.advancePreparedBuilds(p, start); err != nil || p.Builds[2].RunID == 0 || p.Builds[0].Status != "success" {
		t.Fatal(p, err)
	}
	completePreparedFixture(t, s, p.Builds[1])
	completePreparedFixture(t, s, p.Builds[2])
	done, err = s.advancePreparedBuilds(p, start)
	if err != nil || !done {
		t.Fatal(p, err)
	}
	if _, err = s.advancePreparedBuilds(p, func(*PreparedBuild) (int64, error) { t.Fatal("restarted successful build"); return 0, nil }); err != nil {
		t.Fatal(err)
	}
}
func TestPreparedBuildFailureStopsQueuedButTracksRunning(t *testing.T) {
	s, p, start := parallelPreparationFixture(t)
	if _, err := s.advancePreparedBuilds(p, start); err != nil {
		t.Fatal(err)
	}
	s.FinishRun(p.Builds[0].RunID, "failed")
	if done, err := s.advancePreparedBuilds(p, start); err != nil || done || p.Builds[2].RunID != 0 || p.BuildError == "" {
		t.Fatal(p, err)
	}
	p = s.CodePreparation(p.ID)
	completePreparedFixture(t, s, p.Builds[1])
	if _, err := s.advancePreparedBuilds(p, start); err == nil || p.Builds[1].Status != "success" || p.Builds[2].RunID != 0 {
		t.Fatal("failure lost after reload", p, err)
	}
}
func TestPreparedBuildRunnerCapacityDoesNotBlockOtherRunner(t *testing.T) {
	s, p, start := parallelPreparationFixture(t)
	if _, err := s.advancePreparedBuilds(p, func(b *PreparedBuild) (int64, error) {
		if b.Service == "orders" {
			return 0, fmt.Errorf("busy: %w", ErrRunConflict)
		}
		return start(b)
	}); err != nil {
		t.Fatal(err)
	}
	if p.Builds[0].RunID != 0 || p.Builds[0].Status != "queued" || p.Builds[1].RunID == 0 || p.Builds[2].RunID == 0 {
		t.Fatal(p)
	}
}
