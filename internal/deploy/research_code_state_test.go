package deploy

import (
	"context"
	"testing"
	"time"
)

func recoveryFlowFixture(t *testing.T) (*Store, *DeliveryRun, *CodePreparation) {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err = s.UpsertEnvironment(Environment{Name: "dev", DisplayName: "开发"}); err != nil {
		t.Fatal(err)
	}
	w := &WorkItem{Title: "冲突隔离测试", Source: "manual", Kind: "bug", Owner: "alice"}
	if err = s.CreateWorkItem(w); err != nil {
		t.Fatal(err)
	}
	c := &Change{Title: "代码", Kind: "code", Service: "orders", Reference: "feature/fix", Owner: "alice"}
	if err = s.CreateChange(c); err != nil {
		t.Fatal(err)
	}
	if err = s.SetChangeWorkItems(c.ID, []int64{w.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetChangeReady(c.ID, true); err != nil {
		t.Fatal(err)
	}
	tpl := &DeliveryTemplate{Name: "冲突恢复", Mode: "version", Workbench: "research", Owner: "alice", Nodes: []DeliveryNode{{ID: "code", Kind: "code-prepare", Name: "代码准备", Policies: map[string]CodePolicy{"orders": {Mode: "integration", TargetBranch: "dev"}}}, {ID: "confirm", Kind: "manual", Name: "后续确认", AssigneeSource: "work_item_resolvers", ApprovalMode: "all", Instructions: "人工核对"}}}
	if err = saveUnifiedPreparationFixture(s, tpl); err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateResearchFlow(DeliveryRequest{TemplateID: tpl.ID, SourceType: "work_items", WorkItemIDs: []int64{w.ID}, Environment: "dev"}, nil, []string{"alice"}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := s.deliveryPolicy("dev")
	if err != nil {
		t.Fatal(err)
	}
	p := &CodePreparation{ID: "conflict-recovery", Owner: "alice", State: "conflict", Message: "fixture conflict", PreparationRunID: run.ID, Request: *run.Plan.PreparationRequest, PolicyRevision: policy.Revision, ScopeDigest: codeScopeDigest(run.Plan.Scope)}
	if err = s.saveCodePreparation(p); err != nil {
		t.Fatal(err)
	}
	run.Status = "running"
	run.Steps[0].Status = "running"
	run.Steps[0].Detail = p.ID
	if err = s.saveDeliveryRun(run); err != nil {
		t.Fatal(err)
	}
	return s, run, p
}

func TestResearchCodeBlockedProjectionAndRecovery(t *testing.T) {
	s, run, p := recoveryFlowFixture(t)
	originalRevision, digest := run.Revision, run.Digest
	// Reading an old running ledger reflects the child without any ledger write.
	got := s.DeliveryRun(run.ID)
	if got.Status != "blocked" || got.Steps[0].Status != "blocked" || got.Steps[0].Detail != p.ID {
		t.Fatalf("missing conflict projection: %+v", got)
	}
	raw, err := scanDeliveryRun(s.db.QueryRow(deliveryRunSelect+` WHERE id=?`, run.ID))
	if err != nil || raw.Status != "running" || raw.Revision != originalRevision {
		t.Fatal("read mutated ledger", raw, err)
	}
	summary := s.researchSummary(raw)
	if summary.Status != "blocked" || summary.Kind != "code-conflict" || summary.PreparationID != p.ID || len(summary.Assignees) != 1 {
		t.Fatal("owned inbox lost recovery", summary)
	}
	tasks, err := s.ResearchTasks("alice", "todo")
	if err != nil || len(tasks) != 1 || tasks[0].Status != "blocked" || tasks[0].PreparationID != p.ID {
		t.Fatal("recovery task missing", tasks, err)
	}
	s.reconcileResearchPreparation(p.ID)
	persisted, err := scanDeliveryRun(s.db.QueryRow(deliveryRunSelect+` WHERE id=?`, run.ID))
	if err != nil || persisted.Status != "blocked" {
		t.Fatal("worker state not persisted", persisted, err)
	}
	if err = s.preparationSourceValid(p); err != nil {
		t.Fatal("blocked parent prevents original task recovery", err)
	}
	rev := persisted.Revision
	s.reconcileResearchPreparation(p.ID)
	if s.DeliveryRun(run.ID).Revision != rev {
		t.Fatal("duplicate callback changed revision")
	}
	p.State = "merging"
	p.LeaseUntil = time.Now().Add(time.Minute).Unix()
	if err = s.saveCodePreparation(p); err != nil {
		t.Fatal(err)
	}
	s.reconcileResearchPreparation(p.ID)
	got = s.DeliveryRun(run.ID)
	if got.Status != "running" || got.Steps[0].Detail != p.ID || got.Digest != digest || len(got.Steps[0].Reviews) != len(run.Steps[0].Reviews) {
		t.Fatal("recovery replaced scope/task", got)
	}
	p.State = "ready"
	p.LeaseUntil = 0
	if err = s.saveCodePreparation(p); err != nil {
		t.Fatal(err)
	}
	s.reconcileResearchPreparation(p.ID)
	got = s.DeliveryRun(run.ID)
	if got.Status != "waiting_confirmation" || got.Steps[0].Status != "success" || got.Steps[1].Status == "success" {
		t.Fatal("recovery bypassed next human gate", got)
	}
	// A stale parent cannot overwrite the recovered ledger.
	if err = s.saveDeliveryRun(run); err != ErrRunConflict {
		t.Fatal("stale revision accepted", err)
	}
}

func TestResearchCodeRecoveryStatesAndBoundaries(t *testing.T) {
	for _, state := range []string{"conflict", "uncertain", "failed", "merging", "building"} {
		t.Run(state, func(t *testing.T) {
			s, run, p := recoveryFlowFixture(t)
			p.State = state
			p.LeaseUntil = 0
			if err := s.saveCodePreparation(p); err != nil {
				t.Fatal(err)
			}
			s.reconcileResearchPreparation(p.ID)
			if got := s.DeliveryRun(run.ID); got.Status != "blocked" {
				t.Fatal("blocked code reported running", got)
			}
			// Frozen source drift still blocks resumption.
			p.ScopeDigest = "changed"
			if err := s.preparationSourceValid(p); err == nil {
				t.Fatal("scope validation bypassed")
			}
			for _, terminal := range []string{"canceled", "rejected", "failed", "success"} {
				raw, err := scanDeliveryRun(s.db.QueryRow(deliveryRunSelect+` WHERE id=?`, run.ID))
				if err != nil {
					t.Fatal(err)
				}
				raw.Status = terminal
				if err = s.saveDeliveryRun(raw); err != nil {
					t.Fatal(err)
				}
				s.reconcileResearchPreparation(p.ID)
				if got := s.DeliveryRun(run.ID); got.Status != terminal {
					t.Fatal("terminal workflow resurrected", got)
				}
			}
		})
	}
}

func TestResearchWorkerFailurePersistsBlockedParent(t *testing.T) {
	s, run, p := recoveryFlowFixture(t)
	p.State = "merging"
	p.ScopeDigest = "drift"
	p.LeaseUntil = time.Now().Add(time.Minute).Unix()
	if err := s.saveCodePreparation(p); err != nil {
		t.Fatal(err)
	}
	s.performCodePreparation(p, nil, false) // Fails frozen scope before Git or build.
	raw, err := scanDeliveryRun(s.db.QueryRow(deliveryRunSelect+` WHERE id=?`, run.ID))
	if err != nil || raw.Status != "blocked" || s.CodePreparation(p.ID).State != "failed" {
		t.Fatal("failure callback lost", raw, err)
	}
	if _, err = s.AdvanceResearchFlow(context.Background(), run.ID, "alice", nil); err != nil {
		t.Fatal("blocked task cannot be inspected", err)
	}
}
