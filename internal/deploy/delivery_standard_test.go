package deploy

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func standardManualFixture(t *testing.T) (*Store, *ReleaseManager, DeliveryRequest, *Change, *WorkItem) {
	t.Helper()
	st, m, _ := deliveryFixture(t)
	st.db.Exec(`DELETE FROM delivery_templates`)
	w := &WorkItem{Kind: "bug", Title: "A scoped fix", Owner: "alice"}
	if err := st.CreateWorkItem(w); err != nil {
		t.Fatal(err)
	}
	c := &Change{Kind: "manual", Title: "Business verification", Reference: "Check menu after deployment", Owner: "alice"}
	if err := st.CreateChange(c); err != nil {
		t.Fatal(err)
	}
	st.SetChangeWorkItems(c.ID, []int64{w.ID})
	st.SetChangeReady(c.ID, true)
	req := DeliveryRequest{StrategyKind: "standard", SourceType: "work_items", WorkItemIDs: []int64{w.ID}, Environment: "test", Reason: "QA", Artifacts: map[string]int64{}}
	return st, m, req, c, w
}
func standardSign(t *testing.T, st *Store, m *ReleaseManager, req DeliveryRequest) DeliveryRequest {
	t.Helper()
	p, secrets, err := st.ResolveDelivery(req, m)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Blockers) > 0 {
		t.Fatal(p.Blockers)
	}
	req.PreflightToken, _ = st.signDeliveryPreflight(req, p, secrets, "alice")
	return req
}
func TestStandardDeliveryWithoutTemplatesAllSourcesAndNoAutomaticExecution(t *testing.T) {
	st, m, req, c, w := standardManualFixture(t)
	d, err := st.DraftDelivery(req)
	if err != nil || len(d.Nodes) != 1 || d.StrategyVersion != 1 {
		t.Fatal(err, d)
	}
	for _, source := range []string{"work_items", "changes", "version"} {
		candidate := req
		candidate.SourceType = source
		if source == "changes" {
			candidate.WorkItemIDs = nil
			candidate.SelectedChangeIDs = []int64{c.ID}
		}
		if source == "version" {
			v := &ReleaseVersion{Version: "v1", Title: "Scoped release", Owner: "alice"}
			st.CreateReleaseVersion(v)
			st.SaveReleaseDraft(v, VersionScope{WorkItemIDs: []int64{w.ID}})
			if _, e := st.SetReleaseVersionReady(v.ID); e != nil {
				t.Fatal(e)
			}
			candidate.WorkItemIDs = nil
			candidate.VersionID = v.ID
		}
		candidate = standardSign(t, st, m, candidate)
		run, e := st.CreateDeliveryRun(candidate, "alice", m)
		if e != nil {
			t.Fatal(source, e)
		}
		if run.Status != "approved" || run.Plan.Template.ID != 0 || len(run.Plan.Services) != 0 || run.Steps[0].StartedAt != 0 {
			t.Fatal("standard plan executed or invented artifact/template", run)
		}
	}
	if len(st.DeliveryTemplates()) != 0 {
		t.Fatal("virtual strategy leaked into user templates")
	}
}
func TestStandardDedupResourceIsolationStableIDsAndMissingInputs(t *testing.T) {
	scope := &DeliveryScope{Changes: []Change{
		{ID: 1, Kind: "code", Service: "orders"}, {ID: 2, Kind: "code", Service: "orders"},
		{ID: 3, Kind: "mysql", Service: "orders", SQL: &SQLChange{ResourceAlias: "db"}},
		{ID: 4, Kind: "mysql", Service: "another", SQL: &SQLChange{ResourceAlias: "db"}},
		{ID: 5, Kind: "nacos", Service: "orders", Nacos: &NacosChange{ResourceAlias: "config", DataID: "a.yaml", Group: "A"}},
		{ID: 6, Kind: "nacos", Service: "orders", Nacos: &NacosChange{ResourceAlias: "config", DataID: "b.yaml", Group: "A"}},
	}}
	nodes, e := standardDeliveryNodes(scope)
	if e != nil || len(nodes) != 5 {
		t.Fatal(nodes, e)
	}
	count := 0
	for _, n := range nodes {
		if n.Kind == "deploy" {
			count++
			if len(n.ChangeIDs) != 2 {
				t.Fatal("lost code scope")
			}
		}
	}
	if count != 1 {
		t.Fatal("duplicate deploy")
	}
	single, _ := standardDeliveryNodes(&DeliveryScope{Changes: scope.Changes[2:3]})
	if len(single) != 1 || single[0].Kind != "mysql" || single[0].ID != nodes[0].ID {
		t.Fatal("config-only deployed or unstable IDs")
	}
	if e = validateStandardOverrides(nodes, nodes[1:]); e == nil {
		t.Fatal("removed node bypassed scope")
	}
	copied := append([]DeliveryNode{}, nodes...)
	copied[0].ChangeIDs = []int64{987}
	if e = validateStandardOverrides(nodes, copied); e == nil {
		t.Fatal("injected change")
	}
}
func TestStandardScopeExclusionsAndReadiness(t *testing.T) {
	st, m, req, c, w := standardManualFixture(t)
	next := &Change{Kind: "manual", Title: "Not ready", Reference: "Separate work", Owner: "alice"}
	st.CreateChange(next)
	st.SetChangeWorkItems(next.ID, []int64{w.ID})
	d, e := st.DraftDelivery(req)
	if e != nil || len(d.Blockers) == 0 {
		t.Fatal("draft readiness missing", e, d)
	}
	req.SelectedChangeIDs = []int64{c.ID}
	d, e = st.DraftDelivery(req)
	if e != nil || len(d.Scope.Changes) != 1 || len(d.Scope.ExcludedChangeIDs) != 1 {
		t.Fatal("explicit exclusion not frozen", e, d)
	}
	req = standardSign(t, st, m, req)
	run, e := st.CreateDeliveryRun(req, "alice", m)
	if e != nil || run.Plan.Scope.ExcludedChangeIDs[0] != next.ID {
		t.Fatal(e)
	}
	req.SelectedChangeIDs = []int64{next.ID}
	if _, e = st.CreateDeliveryRun(req, "alice", m); e == nil {
		t.Fatal("reused token with altered request")
	}
	req.PreflightToken = ""
	p, _, e := st.ResolveDelivery(req, m)
	if e != nil || len(p.Blockers) == 0 {
		t.Fatal("unready submitted", e)
	}
	req.SelectedChangeIDs = []int64{99999}
	if _, e = st.DraftDelivery(req); e == nil {
		t.Fatal("out of scope selected")
	}
}
func TestStandardMandatoryPolicyStagesCannotBeRemovedReorderedOrSelfApproved(t *testing.T) {
	st, m, req, _, _ := standardManualFixture(t)
	req.Environment = "prod"
	env := st.GetEnvironment("prod")
	env.Approvers = "alice,dev,test,pm"
	st.UpsertEnvironment(*env)
	policy := DeliveryPolicy{Stages: []DeliveryNode{{Name: "开发负责人", Approvers: []string{"dev"}, ApprovalMode: "any"}, {Name: "测试审批", Approvers: []string{"test", "pm"}, ApprovalMode: "all"}, {Name: "项目管理", Approvers: []string{"pm"}, ApprovalMode: "any"}}}
	if e := st.saveDeliveryPolicy("prod", policy); e != nil {
		t.Fatal(e)
	}
	d, e := st.DraftDelivery(req)
	if e != nil || len(d.Nodes) != 4 {
		t.Fatal(e, d)
	}
	req.Nodes = d.Nodes[1:]
	if _, _, e = st.ResolveDelivery(req, m); e == nil {
		t.Fatal("removed mandatory review")
	}
	req.Nodes = append([]DeliveryNode{}, d.Nodes...)
	req.Nodes[0], req.Nodes[1] = req.Nodes[1], req.Nodes[0]
	if _, _, e = st.ResolveDelivery(req, m); e == nil {
		t.Fatal("reordered mandatory review")
	}
	req.Nodes = d.Nodes
	req = standardSign(t, st, m, req)
	run, e := st.CreateDeliveryRun(req, "alice", m)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = st.ReviewDeliveryNode(run.ID, "pm", run.Digest, "env-review-3", "", true); e == nil {
		t.Fatal("skipped stage")
	}
	if _, e = st.ReviewDeliveryNode(run.ID, "alice", run.Digest, "env-review-1", "", true); e == nil {
		t.Fatal("self review")
	}
	for _, step := range []struct{ actor, id string }{{"dev", "env-review-1"}, {"test", "env-review-2"}, {"pm", "env-review-2"}, {"pm", "env-review-3"}} {
		run, e = st.ReviewDeliveryNode(run.ID, step.actor, run.Digest, step.id, "reviewed", true)
		if e != nil {
			t.Fatal(step, e)
		}
	}
	if run.Status != "approved" || run.Steps[3].StartedAt != 0 {
		t.Fatal("approval automatically executed")
	}
	current, _ := st.deliveryPolicy("prod")
	if e = st.saveDeliveryPolicy("prod", current); e != nil {
		t.Fatal(e)
	}
	if _, e = st.AdvanceDeliveryRun(run.ID, "alice", true, m); e == nil {
		t.Fatal("policy drift ignored")
	}
}
func TestStandardPreflightRecoveryAndUnusedTokenDrift(t *testing.T) {
	st, m, req, _, _ := standardManualFixture(t)
	req = standardSign(t, st, m, req)
	unused := standardSign(t, st, m, req)
	run, e := st.CreateDeliveryRun(req, "alice", m)
	if e != nil {
		t.Fatal(e)
	}
	env := st.GetEnvironment("test")
	env.RequireReason = true
	st.UpsertEnvironment(*env)
	recovered, e := st.CreateDeliveryRun(req, "alice", m)
	if e != nil || recovered.ID != run.ID {
		t.Fatal("lost submit not recovered", e)
	}
	if _, e = st.CreateDeliveryRun(unused, "alice", m); e == nil {
		t.Fatal("unused stale preflight accepted")
	}
	changed := req
	changed.Reason = "different"
	if _, e = st.CreateDeliveryRun(changed, "alice", m); e == nil {
		t.Fatal("recovery accepted changed request")
	}
}
func TestStandardDependencyAndReviewDirectory(t *testing.T) {
	nodes := []DeliveryNode{{ID: "deploy", Kind: "deploy", Name: "Deploy", DependsOn: []string{"sql"}}, {ID: "sql", Kind: "mysql", Name: "SQL"}}
	if e := validateNodeDependencies(nodes); e == nil {
		t.Fatal("dependency violated")
	}
	nodes[0], nodes[1] = nodes[1], nodes[0]
	if e := validateNodeDependencies(nodes); e != nil {
		t.Fatal(e)
	}
	p := &DeliveryPlan{Nodes: []DeliveryNode{{Kind: "approval", Name: "Review", ApprovalMode: "any", Approvers: []string{"alice", "disabled"}}}}
	if e := validateDeliveryReviewerDirectory(p, `["alice"]`, "alice"); e == nil {
		t.Fatal("only initiator or disabled reviewers")
	}
	p.Nodes[0].Approvers = append(p.Nodes[0].Approvers, "reviewer")
	if e := validateDeliveryReviewerDirectory(p, `["alice","reviewer"]`, "alice"); e != nil {
		t.Fatal(e)
	}
	p.Nodes[0].ApprovalMode = "all"
	if e := validateDeliveryReviewerDirectory(p, `["alice","reviewer"]`, "alice"); e == nil || !strings.Contains(fmt.Sprint(e), "停用") {
		t.Fatal("disabled cosigner accepted")
	}
}

func TestStandardCodeArtifactCoverageAndConfigurationOnly(t *testing.T) {
	st, m, req, _, w := standardManualFixture(t)
	for i := 0; i < 2; i++ {
		c := &Change{Kind: "code", Title: fmt.Sprintf("Code %d", i), Service: "orders", Reference: "feature/fix", Owner: "alice"}
		st.CreateChange(c)
		if err := st.SetChangeWorkItems(c.ID, []int64{w.ID}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.SetChangeReady(c.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	req.OrderConfirmed = true
	p, _, e := st.ResolveDelivery(req, m)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(strings.Join(p.Blockers, ";"), "制品") {
		t.Fatal("missing artifact did not block")
	}
	req.Artifacts = map[string]int64{"orders": st.Artifacts("orders", 1)[0].ID}
	req.CodeConfirmed = true
	p, _, e = st.ResolveDelivery(req, m)
	if e != nil || len(p.Blockers) != 0 || len(p.Services) != 1 {
		t.Fatal(e, p.Blockers)
	}
	count := 0
	for _, n := range p.Nodes {
		if n.Kind == "deploy" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("duplicate application deployment")
	}
	st.SaveApplicationResources("orders", ApplicationResources{Requirements: []ApplicationResourceRequirement{{Alias: "db", Kind: "mysql"}}})
	sqlChange := &Change{Kind: "mysql", Title: "Only SQL", Service: "orders", SQL: &SQLChange{ResourceAlias: "db", Content: "UPDATE menus SET name='new' WHERE id=1", MaxAffectedRows: 10}, Owner: "alice"}
	if e := st.CreateChange(sqlChange); e != nil {
		t.Fatal(e)
	}
	st.SetChangeReady(sqlChange.ID, true)
	sqlReq := DeliveryRequest{StrategyKind: "standard", SourceType: "changes", SelectedChangeIDs: []int64{sqlChange.ID}, Environment: "test"}
	draft, e := st.DraftDelivery(sqlReq)
	if e != nil {
		t.Fatal(e)
	}
	for _, n := range draft.Nodes {
		if n.Kind == "deploy" {
			t.Fatal("SQL-only generated deployment")
		}
	}
	p, _, e = st.ResolveDelivery(sqlReq, m)
	if e == nil && strings.Contains(strings.Join(p.Blockers, ";"), "请选择应用的有效制品") {
		t.Fatal("SQL-only requires artifact")
	}
}

func TestStandardApprovalJSONRoundTripAndMissingReviewers(t *testing.T) {
	st, m, req, _, _ := standardManualFixture(t)
	req.Environment = "prod"
	env := st.GetEnvironment("prod")
	env.Approvers = ""
	// Represent an upgraded environment that has no reviewers configured yet.
	if _, err := st.db.Exec(`UPDATE environments SET approvers='' WHERE name='prod'`); err != nil {
		t.Fatal(err)
	}
	draft, err := st.DraftDelivery(req)
	if err != nil || len(draft.Blockers) == 0 || !strings.Contains(strings.Join(draft.Blockers, " "), "环境审核设置") {
		t.Fatal(draft, err)
	}
	roundTrip := func(nodes []DeliveryNode) []DeliveryNode {
		raw, e := json.Marshal(nodes)
		if e != nil {
			t.Fatal(e)
		}
		var out []DeliveryNode
		if e = json.Unmarshal(raw, &out); e != nil {
			t.Fatal(e)
		}
		return out
	}
	req.Nodes = roundTrip(draft.Nodes)
	if err = validateStandardOverrides(draft.Nodes, req.Nodes); err != nil {
		t.Fatal("empty list changed meaning after JSON transport", err)
	}
	if _, _, err = st.ResolveDelivery(req, m); err == nil {
		t.Fatal("missing reviewers must still block preflight")
	}
	env.Approvers = "reviewer"
	st.UpsertEnvironment(*env)
	draft, err = st.DraftDelivery(req)
	if err != nil {
		t.Fatal(err)
	}
	req.Nodes = roundTrip(draft.Nodes)
	if err = validateStandardOverrides(draft.Nodes, req.Nodes); err != nil {
		t.Fatal(err)
	}
	req = standardSign(t, st, m, req)
	run, err := st.CreateDeliveryRun(req, "alice", m)
	if err != nil || run.Status != "pending_approval" {
		t.Fatal(run, err)
	}
	tampered := roundTrip(draft.Nodes)
	tampered[0].Approvers = []string{"outsider"}
	if err = validateStandardOverrides(draft.Nodes, tampered); err == nil {
		t.Fatal("approval policy tampering accepted")
	}
}
