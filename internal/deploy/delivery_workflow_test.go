package deploy

import (
	"context"
	"strings"
	"testing"
)

func TestWorkflowDynamicCodePoliciesAndLegacyScope(t *testing.T) {
	scope := &DeliveryScope{WorkItems: []WorkItem{{ID: 7}}, Changes: []Change{
		{ID: 1, Kind: "code", Service: "orders"}, {ID: 2, Kind: "code", Service: "orders"},
		{ID: 3, Kind: "code", Service: "billing"}, {ID: 4, Kind: "mysql", Service: "config-only"},
	}}
	common := CodePolicy{Mode: "integration", TargetBranch: "test"}
	nodes := []DeliveryNode{{ID: "code", Kind: "code-prepare", ApplicationScope: "delivery", DefaultCodePolicy: &common}}
	defaults := DeliveryDefaults{Applications: map[string]ApplicationDeliveryDefault{"orders": {CodePolicy: CodePolicy{Mode: "integration", TargetBranch: "qa"}}}}
	if err := resolvePreparationPolicies(nodes, scope, nil, defaults, nil); err != nil {
		t.Fatal(err)
	}
	if len(nodes[0].Policies) != 2 || nodes[0].Policies["orders"].TargetBranch != "qa" || nodes[0].Policies["billing"].TargetBranch != "test" {
		t.Fatalf("not deduplicated or defaults ignored: %+v", nodes)
	}
	if _, exists := nodes[0].Policies["config-only"]; exists {
		t.Fatal("SQL-only app became a build")
	}
	filtered := []DeliveryNode{{ID: "code", Kind: "code-prepare", ApplicationScope: "delivery", DefaultCodePolicy: &common}}
	if err := resolvePreparationPolicies(filtered, scope, nil, defaults, map[string]string{"orders": "build", "billing": "existing"}); err != nil || len(filtered[0].Policies) != 1 {
		t.Fatal("existing artifact caused a build", err)
	}
	missing := []DeliveryNode{{ID: "code", Kind: "code-prepare", ApplicationScope: "delivery"}}
	if err := resolvePreparationPolicies(missing, scope, nil, DeliveryDefaults{}, nil); err == nil || !strings.Contains(err.Error(), "orders") {
		t.Fatal("missing policy was guessed", err)
	}
	legacy := []DeliveryNode{{ID: "legacy", Kind: "code-prepare", Policies: map[string]CodePolicy{"orders": common}}}
	if err := resolvePreparationPolicies(legacy, scope, nil, defaults, nil); err == nil {
		t.Fatal("legacy fixed preparation accepted")
	}

	duplicate := []DeliveryNode{nodes[0], nodes[0]}
	if err := resolvePreparationPolicies(duplicate, scope, nil, defaults, nil); err == nil {
		t.Fatal("duplicate app across code nodes accepted")
	}
}

func TestWorkflowBranchPlaceholders(t *testing.T) {
	scope := &DeliveryScope{WorkItems: []WorkItem{{ID: 7}}}
	p, err := expandCodePolicy(CodePolicy{Mode: "isolated", BaseBranch: "main", TargetBranch: "hotfix/{work_item}"}, scope, nil)
	if err != nil || p.TargetBranch != "hotfix/7" {
		t.Fatal(p, err)
	}
	scope.WorkItems = append(scope.WorkItems, WorkItem{ID: 8})
	if _, err := expandCodePolicy(CodePolicy{Mode: "isolated", BaseBranch: "main", TargetBranch: "hotfix/{work_item}"}, scope, nil); err == nil {
		t.Fatal("ambiguous work item branch accepted")
	}
	if _, err := expandCodePolicy(CodePolicy{Mode: "isolated", BaseBranch: "main", TargetBranch: "release/{version}"}, scope, nil); err == nil {
		t.Fatal("missing version accepted")
	}
}

func TestWorkflowBindingCannotBeBypassed(t *testing.T) {
	p := DeliveryPolicy{WorkflowTemplateID: 10, HotfixTemplateID: 11}
	env := Environment{Name: "prod", Purpose: "production"}
	for _, req := range []DeliveryRequest{{StrategyKind: "standard"}, {StrategyKind: "custom", TemplateID: 11}, {StrategyKind: "custom", TemplateID: 10, Scene: "hotfix"}} {
		if err := validateWorkflowBinding(req, env, p); err == nil {
			t.Fatal("bound workflow bypassed", req)
		}
	}
	if err := validateWorkflowBinding(DeliveryRequest{StrategyKind: "custom", TemplateID: 11, Scene: "hotfix"}, env, p); err != nil {
		t.Fatal(err)
	}
	if err := validateWorkflowBinding(DeliveryRequest{Scene: "hotfix"}, Environment{Purpose: "test"}, DeliveryPolicy{}); err == nil {
		t.Fatal("nonproduction hotfix accepted")
	}
}

func TestWorkflowPreparationGateNoRemoteSideEffects(t *testing.T) {
	st, manager, req, _, _ := standardManualFixture(t)
	tpl := &DeliveryTemplate{Name: "End to end", Mode: "version", Owner: "alice",
		Preparation: []DeliveryNode{{ID: "scope", Kind: "manual", Name: "Scope", Instructions: "Confirm scope", AssigneeSource: "specified", Approvers: []string{"alice"}, ApprovalMode: "all"}},
		Nodes:       []DeliveryNode{{ID: "verify", Kind: "manual", Name: "Observe", Instructions: "Verify business"}},
	}
	if err := st.SaveDeliveryTemplate(tpl); err != nil {
		t.Fatal(err)
	}
	req.StrategyKind, req.TemplateID = "custom", tpl.ID
	if _, _, err := st.ResolveDelivery(req, manager); err == nil {
		t.Fatal("preparation could be bypassed")
	}
	run, err := st.CreateResearchFlow(req, nil, []string{"alice"}, "alice")
	if err != nil || run.Status != "waiting_confirmation" {
		t.Fatal(run, err)
	}
	if len(st.DeliveryRuns()) != 1 {
		t.Fatal("creating preparation launched deployment")
	}
	run, err = st.ReviewResearchTask(run.ID, "scope", "alice", run.Digest, "Checked", true, []string{"alice"})
	if err != nil || run.Status != "success" {
		t.Fatal(run, err)
	}
	req.PreparationRunID = run.ID
	plan, _, err := st.ResolveDelivery(req, manager)
	if err != nil || len(plan.Blockers) > 0 || plan.PreparationRunID != run.ID {
		t.Fatal(plan, err)
	}
	req.Environment = "missing"
	if _, _, err := st.ResolveDelivery(req, manager); err == nil {
		t.Fatal("preparation reused in different environment")
	}
	req.Environment = "test"
	tpl.Preparation[0].Instructions = "Changed"
	if err := st.SaveDeliveryTemplate(tpl); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.ResolveDelivery(req, manager); err == nil {
		t.Fatal("stale preparation reused after workflow edit")
	}
}

func TestWorkflowDynamicCodeSkipsConfigurationOnlyScope(t *testing.T) {
	st, _, req, _, _ := standardManualFixture(t)
	tpl := &DeliveryTemplate{Name: "Config workflow", Mode: "version", Owner: "alice", Preparation: []DeliveryNode{{ID: "code", Kind: "code-prepare", Name: "Code", ApplicationScope: "delivery"}}, Nodes: []DeliveryNode{{ID: "verify", Kind: "manual", Name: "Verify", Instructions: "Check"}}}
	if err := st.SaveDeliveryTemplate(tpl); err != nil {
		t.Fatal(err)
	}
	req.StrategyKind, req.TemplateID = "custom", tpl.ID
	run, err := st.CreateResearchFlow(req, nil, []string{"alice"}, "alice")
	if err != nil || run.Status != "success" || run.Steps[0].Status != "success" || len(run.Plan.Nodes[0].Policies) != 0 {
		t.Fatal("no-op code phase invented execution", run, err)
	}
}

func TestWorkflowPolicyPersistenceAndScopeInvalidation(t *testing.T) {
	st, _, req, c, _ := standardManualFixture(t)
	tpl := &DeliveryTemplate{Name: "Bound flow", Mode: "version", Owner: "alice", Preparation: []DeliveryNode{{ID: "scope", Kind: "manual", Name: "Scope", Instructions: "Check", AssigneeSource: "specified", Approvers: []string{"alice"}, ApprovalMode: "all"}}, Nodes: []DeliveryNode{{ID: "verify", Kind: "manual", Name: "Verify", Instructions: "Check"}}}
	if err := st.SaveDeliveryTemplate(tpl); err != nil {
		t.Fatal(err)
	}
	if err := st.saveDeliveryPolicy("test", DeliveryPolicy{WorkflowTemplateID: tpl.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DraftDelivery(req); err == nil {
		t.Fatal("standard request bypassed persisted environment binding")
	}
	req.StrategyKind, req.TemplateID = "custom", tpl.ID
	run, err := st.CreateResearchFlow(req, nil, []string{"alice"}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetChangeReady(c.ID, false); err != nil {
		t.Fatal(err)
	}
	c = st.GetChange(c.ID)
	c.Summary = "A changed business operation"
	if err := st.UpdateChange(c); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReviewResearchTask(run.ID, "scope", "alice", run.Digest, "", true, []string{"alice"}); err == nil {
		t.Fatal("scope change retained stale confirmation")
	}
	policy, err := st.deliveryPolicy("test")
	if err != nil || policy.WorkflowTemplateID != tpl.ID || policy.Revision != 1 {
		t.Fatal(policy, err)
	}
	policy.Defaults.Applications = map[string]ApplicationDeliveryDefault{"orders": {CodePolicy: CodePolicy{Mode: "isolated", BaseBranch: "main", TargetBranch: "release/{version}"}}}
	if err := st.saveDeliveryPolicy("test", policy); err != nil {
		t.Fatal("reusable branch policy rejected", err)
	}
}

func TestWorkflowIndependentChangesConfirmSubmitters(t *testing.T) {
	st, _, _, c, _ := standardManualFixture(t)
	tpl := &DeliveryTemplate{Name: "Independent change flow", Mode: "version", Owner: "alice", Preparation: []DeliveryNode{{ID: "scope", Kind: "manual", Name: "Confirm", Instructions: "Check", AssigneeSource: "change_submitters", ApprovalMode: "all"}}, Nodes: []DeliveryNode{{ID: "verify", Kind: "manual", Name: "Verify", Instructions: "Check"}}}
	if err := st.SaveDeliveryTemplate(tpl); err != nil {
		t.Fatal(err)
	}
	req := DeliveryRequest{StrategyKind: "custom", TemplateID: tpl.ID, Environment: "test", SourceType: "changes", SourceChangeIDs: []int64{c.ID}}
	run, err := st.CreateResearchFlow(req, nil, []string{"alice"}, "alice")
	if err != nil || len(run.Plan.Nodes[0].Approvers) != 1 || run.Plan.Nodes[0].Approvers[0] != "alice" {
		t.Fatal(run, err)
	}
	if run, err = st.ReviewResearchTask(run.ID, "scope", "alice", run.Digest, "checked", true, []string{"alice"}); err != nil || run.Status != "success" {
		t.Fatal(run, err)
	}
}

func TestWorkflowVersionEvidenceAssociationDoesNotInventCodeProof(t *testing.T) {
	st, _, req, _, w := standardManualFixture(t)
	v := &ReleaseVersion{Version: "v-workflow", Title: "A workflow release", Owner: "alice"}
	if err := st.CreateReleaseVersion(v); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveReleaseDraft(v, VersionScope{WorkItemIDs: []int64{w.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetReleaseVersionReady(v.ID); err != nil {
		t.Fatal(err)
	}
	tpl := &DeliveryTemplate{Name: "Version workflow", Mode: "version", Owner: "alice", Preparation: []DeliveryNode{{ID: "scope", Kind: "manual", Name: "Confirm", Instructions: "Check", AssigneeSource: "specified", Approvers: []string{"alice"}, ApprovalMode: "all"}}, Nodes: []DeliveryNode{{ID: "verify", Kind: "manual", Name: "Verify", Instructions: "Check"}}}
	if err := st.SaveDeliveryTemplate(tpl); err != nil {
		t.Fatal(err)
	}
	req.StrategyKind, req.TemplateID, req.SourceType, req.VersionID, req.WorkItemIDs = "custom", tpl.ID, "version", v.ID, nil
	run, err := st.CreateResearchFlow(req, nil, []string{"alice"}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(st.readinessPreparationRuns(st.GetReleaseVersion(v.ID))) != 0 {
		t.Fatal("unconfirmed run became evidence")
	}
	if _, err = st.ReviewResearchTask(run.ID, "scope", "alice", run.Digest, "checked", true, []string{"alice"}); err != nil {
		t.Fatal(err)
	}
	fresh := st.GetReleaseVersion(v.ID)
	if len(st.readinessPreparationRuns(fresh)) != 1 || len(st.readinessPreparations(fresh)) != 0 {
		t.Fatal("association overwrote legacy configuration or invented code proof")
	}
}

func TestWorkflowChildCodeCannotBypassConfirmationAndCancellation(t *testing.T) {
	st, _, req, _, w := standardManualFixture(t)
	c := &Change{Kind: "code", Title: "Code fixture", Service: "orders", Reference: "feature/scoped", Owner: "alice"}
	if err := st.CreateChange(c); err != nil {
		t.Fatal(err)
	}
	st.SetChangeWorkItems(c.ID, []int64{w.ID})
	st.SetChangeReady(c.ID, true)
	policy := CodePolicy{Mode: "integration", TargetBranch: "test"}
	tpl := &DeliveryTemplate{Name: "Guarded code", Mode: "version", Owner: "alice", Preparation: []DeliveryNode{
		{ID: "scope", Kind: "manual", Name: "Confirm", Instructions: "Check", AssigneeSource: "specified", Approvers: []string{"alice"}, ApprovalMode: "all"},
		{ID: "code", Kind: "code-prepare", Name: "Code", ApplicationScope: "delivery", DefaultCodePolicy: &policy},
	}, Nodes: []DeliveryNode{{ID: "deploy", Kind: "deploy", Name: "Deploy"}}}
	if err := st.SaveDeliveryTemplate(tpl); err != nil {
		t.Fatal(err)
	}
	req.StrategyKind, req.TemplateID, req.ArtifactSources = "custom", tpl.ID, map[string]string{"orders": "build"}
	run, err := st.CreateResearchFlow(req, nil, []string{"alice"}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	// Synthetic metadata only; no Git session or runner is created.
	p := &CodePreparation{ID: "workflow-child", Owner: "alice", State: "preview", Request: req, PreparationRunID: run.ID, ScopeDigest: codeScopeDigest(run.Plan.Scope)}
	if err := st.preparationSourceValid(p); err == nil {
		t.Fatal("code start bypassed scope confirmation")
	}
	run, err = st.ReviewResearchTask(run.ID, "scope", "alice", run.Digest, "checked", true, []string{"alice"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.preparationSourceValid(p); err == nil {
		t.Fatal("unlinked code job was accepted")
	}
	run.Status, run.Steps[1].Status, run.Steps[1].Detail = "running", "running", p.ID
	if err := st.saveDeliveryRun(run); err != nil {
		t.Fatal(err)
	}
	if err := st.preparationSourceValid(p); err != nil {
		t.Fatal(err)
	}
	p.State = "canceled"
	if err := st.saveCodePreparation(p); err != nil {
		t.Fatal(err)
	}
	run, err = st.AdvanceResearchFlow(context.Background(), run.ID, "alice", nil)
	if err != nil || run.Status != "canceled" || run.Steps[1].Status != "canceled" {
		t.Fatal("canceled child left workflow running", run, err)
	}
	p.State = "preview"
	if err := st.preparationSourceValid(p); err == nil {
		t.Fatal("canceled workflow allowed a child restart")
	}
}
