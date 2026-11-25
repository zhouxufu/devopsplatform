package deploy

import (
	"slices"
	"strings"
	"testing"
)

func TestRetryPreparedBuildOnlyAfterVerifiedFailure(t *testing.T) {
	st, p, start := parallelPreparationFixture(t)
	p.State = "failed"
	b := &p.Builds[0]
	for _, spec := range p.Builds {
		p.Repositories = append(p.Repositories, PreparedRepository{Services: []string{spec.Service}, ResultCommit: spec.Commit, Status: "merged"})
	}
	id, err := start(b)
	if err != nil {
		t.Fatal(err)
	}
	b.RunID, b.Status = id, "failed"
	if err = st.prepareFrozenBuildRetry(p); err == nil {
		t.Fatal("active build accepted")
	}
	st.FinishRun(id, "failed")
	if err = st.prepareFrozenBuildRetry(p); err == nil {
		t.Fatal("missing snapshot accepted")
	}
	sv := st.GetService(b.Service)
	sv.GitURL = "https://fixture.invalid/orders.git"
	if err = st.saveBuildSnapshot(id, sv, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = st.db.Exec(`UPDATE build_run_snapshots SET commit_ref=? WHERE run_id=?`, b.Commit, id); err != nil {
		t.Fatal(err)
	}
	oldTrigger := buildAttemptTrigger(p, b)
	if err = st.prepareFrozenBuildRetry(p); err != nil {
		t.Fatal(err)
	}
	if b.RunID != 0 || !slices.Equal(b.PreviousRunIDs, []int64{id}) || buildAttemptTrigger(p, b) == oldTrigger || p.Repositories[0].Status != "merged" {
		t.Fatal("lost history or reused original trigger")
	}
}

func TestReleaseChecklistDistinguishesReceipts(t *testing.T) {
	p := DeliveryPlan{Scope: &DeliveryScope{Changes: []Change{{ID: 1, Kind: "mysql", Status: ChangeReady}, {ID: 2, Kind: "mysql", Status: ChangeReady}, {ID: 3, Kind: "nacos", Status: ChangeReady}}}, Nodes: []DeliveryNode{{ID: "sql", Kind: "mysql", ChangeIDs: []int64{1, 2}}, {ID: "cfg", Kind: "nacos"}}, SQLPlans: []*SQLExecutionPlan{{Applied: []AppliedSQLChange{{ChangeID: 1, RunID: 19}}, ScriptSelections: []SQLScriptSelection{{ChangeID: 2, Skip: true, Reason: "此环境无需回填"}}}}, NacosPlans: []*NacosExecutionPlan{{Configs: []NacosConfigPlan{{ChangeID: 3, Skip: true, PriorRunID: 8}}}}}
	steps := releaseChecklist(&p)
	if steps[0].Items[0].PriorRunID != 19 || steps[0].Items[1].Status != "not_applicable" || steps[1].Items[0].PriorRunID != 8 {
		t.Fatal(steps)
	}
}

func TestChangeOrderSQLDeploySQLWithGate(t *testing.T) {
	scope := &DeliveryScope{Changes: []Change{
		{ID: 1, Kind: "code", Service: "app", Prerequisites: []ChangePrerequisite{{2, "启动读取新增字段"}}},
		{ID: 2, Kind: "mysql", Service: "app"}, {ID: 3, Kind: "mysql", Service: "app", Prerequisites: []ChangePrerequisite{{1, "全量实例就绪后回填"}}},
	}}
	generated, err := standardDeliveryNodes(scope)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := orderChangeNodes(generated, scope, true)
	if err != nil || len(nodes) != 3 || nodes[0].ChangeIDs[0] != 2 || nodes[1].Kind != "deploy" || nodes[2].ChangeIDs[0] != 3 {
		t.Fatal(nodes, err)
	}
	if _, err = orderChangeNodes(nodes, scope, false); err != nil {
		t.Fatal(err)
	}
	nodes[0], nodes[1] = nodes[1], nodes[0]
	if _, err = orderChangeNodes(nodes, scope, false); err == nil {
		t.Fatal("accepted reversed dependency")
	}
	custom := []DeliveryNode{{ID: "sql", Kind: "mysql"}, {ID: "gate", Kind: "approval", Name: "审核"}, {ID: "deploy", Kind: "deploy"}}
	if _, err = orderChangeNodes(custom, scope, true); err == nil {
		t.Fatal("moved a stage across approval")
	}
	scope.Changes[1].Prerequisites = []ChangePrerequisite{{3, "循环"}}
	if _, err = orderChangeNodes(generated, scope, true); err == nil {
		t.Fatal("accepted cycle")
	}
	scope.Changes[1].Prerequisites = []ChangePrerequisite{{99, "不在范围"}}
	if _, err = orderChangeNodes(generated, scope, true); err == nil || !strings.Contains(err.Error(), "#99") {
		t.Fatal(err)
	}
}

func TestChangePrerequisiteSurvivesDraftAndScope(t *testing.T) {
	st, _, req, first, _ := standardManualFixture(t)
	next := &Change{Kind: "manual", Title: "后置核对", Summary: "核对业务", Owner: "alice", Prerequisites: []ChangePrerequisite{{first.ID, "先完成前置操作"}}}
	if err := st.CreateChange(next); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(st.GetChange(next.ID).Prerequisites, next.Prerequisites) {
		t.Fatal("lost prerequisite on reload")
	}
	req.SourceType, req.SourceChangeIDs, req.SelectedChangeIDs, req.WorkItemIDs = "changes", []int64{first.ID, next.ID}, nil, nil
	draft, err := st.DraftDelivery(req)
	if err != nil || len(draft.Blockers) == 0 {
		t.Fatal(draft, err)
	}
	tpl := &DeliveryTemplate{Name: "检查内容", Mode: "version", Owner: "alice", Preparation: []DeliveryNode{{ID: "check", Kind: "manual", Name: "确认", Instructions: "确认", Approvers: []string{"alice"}, AssigneeSource: "specified", ApprovalMode: "all"}}, Nodes: []DeliveryNode{{ID: "done", Kind: "manual", Name: "处理", Instructions: "处理"}}}
	if err = st.SaveDeliveryTemplate(tpl); err != nil {
		t.Fatal(err)
	}
	req.StrategyKind, req.TemplateID = "custom", tpl.ID
	p, err := st.ResearchFlowPlan(req, nil, []string{"alice"}, "alice")
	if err != nil || len(p.Blockers) == 0 {
		t.Fatal(p, err)
	}
	if _, err = st.CreateResearchFlow(req, nil, []string{"alice"}, "alice"); err == nil {
		t.Fatal("draft created preparation")
	}
}
