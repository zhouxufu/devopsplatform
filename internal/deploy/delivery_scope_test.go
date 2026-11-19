package deploy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestWorkItemDeliveryScopeAndFrozenPlan(t *testing.T) {
	st, m, _ := deliveryFixture(t)
	w := &WorkItem{Title: "Hotfix", Kind: "bug", Owner: "alice"}
	if err := st.CreateWorkItem(w); err != nil {
		t.Fatal(err)
	}
	c := &Change{Kind: "manual", Title: "Verify", Owner: "alice", Reference: "Verify business"}
	if err := st.CreateChange(c); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChangeWorkItems(c.ID, []int64{w.ID}); err != nil {
		t.Fatal(err)
	}
	st.SetChangeReady(c.ID, true)
	tpl := &DeliveryTemplate{Name: "Dynamic", Mode: "version", Owner: "alice", Nodes: []DeliveryNode{{ID: "verify", Kind: "manual", Name: "Verify", Instructions: "Check"}}}
	if err := st.SaveDeliveryTemplate(tpl); err != nil {
		t.Fatal(err)
	}
	req := DeliveryRequest{TemplateID: tpl.ID, SourceType: "work_items", WorkItemIDs: []int64{w.ID}, Environment: "test", Reason: "QA"}
	run, err := st.CreateDeliveryRun(req, "alice", m)
	if err != nil {
		t.Fatal(err)
	}
	if run.Plan.Version != nil || run.Plan.Scope.Type != "work_items" || len(run.Plan.Scope.Changes) != 1 {
		t.Fatalf("wrong scope: %+v", run.Plan)
	}
	// Later changes remain in the Bug but cannot enter its existing snapshot.
	next := &Change{Kind: "manual", Title: "Follow-up", Owner: "alice", Reference: "New check"}
	st.CreateChange(next)
	st.SetChangeWorkItems(next.ID, []int64{w.ID})
	st.SetChangeReady(next.ID, true)
	if len(st.DeliveryRun(run.ID).Plan.Scope.Changes) != 1 {
		t.Fatal("snapshot expanded after submission")
	}
	p, _, err := st.ResolveDelivery(req, m)
	if err != nil || len(p.Scope.Changes) != 2 {
		t.Fatal("next delivery did not include follow-up", err)
	}
	req.VersionID = 123
	if _, _, err := st.ResolveDelivery(req, m); err == nil {
		t.Fatal("mixed source accepted")
	}
	if _, err := st.resolveWorkItemScope([]int64{w.ID, w.ID}); err == nil {
		t.Fatal("duplicate work IDs accepted")
	}
}

func TestSQLReuseProofAndImmutability(t *testing.T) {
	st, _, _ := deliveryFixture(t)
	connection := ResourceConnection{Name: "qa-db", Kind: "mysql", Endpoint: "127.0.0.1:1", Username: "qa", Config: map[string]string{"tls": "false"}}
	if err := st.UpsertConnection(connection); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveApplicationResources("orders", ApplicationResources{Requirements: []ApplicationResourceRequirement{{Alias: "db", Kind: "mysql"}}, Bindings: []ApplicationEnvironmentResource{{Alias: "db", Environment: "test", Connection: connection.Name, Config: map[string]string{"database": "orders"}}}}); err != nil {
		t.Fatal(err)
	}
	c := sqlChangeFixture(t, st, "UPDATE menus SET name='new' WHERE id=1")
	ops, err := parseChangeSQL(c.SQL.Content)
	if err != nil {
		t.Fatal(err)
	}
	ops[0].ChangeID = c.ID
	p := &SQLExecutionPlan{NodeID: "sql", ChangeIDs: []int64{c.ID}, Services: []string{c.Service}, Aliases: []string{"db"}, Connection: connection.Name, Database: "orders", DestinationDigest: func() string {
		target, err := st.sqlBinding("orders", "test", "db")
		if err != nil {
			t.Fatal(err)
		}
		return sqlDestination(target)
	}(), ChangeDigests: []string{digestJSON([]any{c.Service, c.SQL})}, Statements: ops}
	// Simulated immutable success evidence; no MySQL connection is opened.
	doc, _ := json.Marshal(DeliveryPlan{Environment: Environment{Name: "test"}, SQLPlans: []*SQLExecutionPlan{p}})
	r, err := st.db.Exec(`INSERT INTO delivery_runs(plan,digest,status,execution,env_secrets,owner,approver,created_at,revision) VALUES(?,'proof','success','[]','{}','alice','reviewer',1,1)`, string(doc))
	if err != nil {
		t.Fatal(err)
	}
	runID, _ := r.LastInsertId()
	_, err = st.db.Exec(`INSERT INTO sql_execution_ledger(connection,database_name,change_id,digest,status,run_id,node_id,detail) VALUES(?,?,?,?,?,?,?,'fixture proof')`, connection.Name, "orders", c.ID, sqlChangeDigest(p, c.ID), "success", runID, "sql")
	if err != nil {
		t.Fatal(err)
	}
	repeat, err := st.prepareSQLNode(context.Background(), "repeat", []int64{c.ID}, "test")
	if err != nil || len(repeat.Applied) != 1 || len(repeat.ChangeIDs) != 0 {
		t.Fatalf("skip failed: %v %+v", err, repeat)
	}
	if _, err := st.executeSQLNode(context.Background(), &DeliveryRun{Plan: DeliveryPlan{Environment: Environment{Name: "test"}}}, repeat); err != nil {
		t.Fatal(err)
	}
	alias := connection
	alias.Name = "same-physical-db"
	if err := st.UpsertConnection(alias); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveApplicationResources("orders", ApplicationResources{Requirements: []ApplicationResourceRequirement{{Alias: "db", Kind: "mysql"}}, Bindings: []ApplicationEnvironmentResource{{Alias: "db", Environment: "test", Connection: alias.Name, Config: map[string]string{"database": "orders"}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.executeSQLNode(context.Background(), &DeliveryRun{Plan: DeliveryPlan{Environment: Environment{Name: "test"}}}, repeat); err == nil {
		t.Fatal("binding drift after preview accepted")
	}
	if again, err := st.prepareSQLNode(context.Background(), "alias", []int64{c.ID}, "test"); err != nil || len(again.Applied) != 1 {
		t.Fatal("equivalent physical target did not share success evidence", err)
	}
	if _, err := st.SetChangeReady(c.ID, false); err == nil {
		t.Fatal("applied change unlocked")
	}
	st.db.Exec(`UPDATE sql_execution_ledger SET status='uncertain' WHERE change_id=?`, c.ID)
	if _, err := st.prepareSQLNode(context.Background(), "uncertain", []int64{c.ID}, "test"); err == nil || !strings.Contains(err.Error(), "核对") {
		t.Fatal("uncertain operation reused", err)
	}
	st.db.Exec(`UPDATE sql_execution_ledger SET status='success' WHERE change_id=?`, c.ID)
	st.db.Exec(`UPDATE change_payloads SET document=? WHERE change_id=?`, `{"sql":{"resource_alias":"db","content":"UPDATE menus SET name='tampered' WHERE id=1","max_affected_rows":100}}`, c.ID)
	if _, err := st.prepareSQLNode(context.Background(), "changed", []int64{c.ID}, "test"); err == nil {
		t.Fatal("changed payload silently skipped")
	}
}

func TestWorkItemCodeAggregationAndDraftGuard(t *testing.T) {
	st, m, artifacts := deliveryFixture(t)
	ids := []int64{}
	for _, title := range []string{"Bug A", "Bug B"} {
		w := &WorkItem{Title: title, Kind: "bug"}
		if err := st.CreateWorkItem(w); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, w.ID)
	}
	c := &Change{Kind: "code", Title: "Shared fix", Service: "orders", Reference: "hotfix/shared"}
	if err := st.CreateChange(c); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChangeWorkItems(c.ID, ids); err != nil {
		t.Fatal(err)
	}
	tpl := &DeliveryTemplate{Name: "Scoped code", Mode: "version", Nodes: []DeliveryNode{{ID: "deploy", Kind: "deploy", Name: "Deploy"}}}
	if err := st.SaveDeliveryTemplate(tpl); err != nil {
		t.Fatal(err)
	}
	req := DeliveryRequest{TemplateID: tpl.ID, SourceType: "work_items", WorkItemIDs: ids, Environment: "test", Artifacts: map[string]int64{"orders": artifacts.Artifacts["orders"]}, CodeConfirmed: true}
	p, _, err := st.ResolveDelivery(req, m)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Blockers) == 0 {
		t.Fatal("draft code accepted")
	}
	st.SetChangeReady(c.ID, true)
	p, _, err = st.ResolveDelivery(req, m)
	if err != nil || len(p.Blockers) > 0 || len(p.Services) != 1 || len(p.Scope.Changes) != 1 {
		t.Fatalf("shared application not deduplicated: %v %+v", err, p)
	}
	req.CodeConfirmed = false
	p, _, err = st.ResolveDelivery(req, m)
	if err != nil || len(p.Blockers) == 0 {
		t.Fatal("unverified code coverage accepted", err)
	}
}

func TestNacosReuseNoPublishAndDriftBlocks(t *testing.T) {
	st, m, mock, c := nacosFixture(t)
	original := nacosRun(t, st, m, c, "test")
	if _, err := st.AdvanceDeliveryRun(original.ID, "alice", false, m); err != nil {
		t.Fatal(err)
	}
	_, posts := mock.snapshot()
	repeat := nacosRun(t, st, m, c, "test")
	if !repeat.Plan.NacosPlans[0].Configs[0].Skip {
		t.Fatal("not marked skip")
	}
	if _, err := st.AdvanceDeliveryRun(repeat.ID, "alice", false, m); err != nil {
		t.Fatal(err)
	}
	if _, n := mock.snapshot(); n != posts {
		t.Fatal("skip republished config")
	}
	mock.change("feature:\n  enabled: false\n")
	if _, err := st.prepareNacosNode(context.Background(), "drift", []int64{c.ID}, "test"); err == nil || !strings.Contains(err.Error(), "漂移") {
		t.Fatal("drift overwritten", err)
	}
}
