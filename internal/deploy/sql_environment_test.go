package deploy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSQLEnvironmentOverridesValidationAndPersistence(t *testing.T) {
	st, _, _ := deliveryFixture(t)
	base := func() *Change {
		return &Change{Kind: "mysql", Title: "环境例外", Service: "orders", SQL: &SQLChange{ResourceAlias: "db", Content: "UPDATE menus SET enabled=1 WHERE id=1", MaxAffectedRows: 100}}
	}
	cases := []struct {
		name    string
		entries []SQLEnvironmentOverride
	}{
		{"empty environment", []SQLEnvironmentOverride{{Content: "UPDATE menus SET enabled=1 WHERE id=2", Reason: "不同数据"}}},
		{"duplicate environment", []SQLEnvironmentOverride{{Environment: "prod", Skip: true, Reason: "随其他变更"}, {Environment: " prod ", Skip: true, Reason: "重复"}}},
		{"missing reason", []SQLEnvironmentOverride{{Environment: "prod", Content: "UPDATE menus SET enabled=1 WHERE id=2"}}},
		{"skip with script", []SQLEnvironmentOverride{{Environment: "prod", Skip: true, Content: "UPDATE menus SET enabled=1 WHERE id=2", Reason: "不执行"}}},
		{"empty script", []SQLEnvironmentOverride{{Environment: "prod", Reason: "没有脚本"}}},
		{"unknown environment", []SQLEnvironmentOverride{{Environment: "missing", Skip: true, Reason: "不存在"}}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			c := base()
			c.SQL.EnvironmentOverrides = tt.entries
			if err := st.CreateChange(c); err == nil {
				t.Fatal("invalid exception accepted")
			}
		})
	}
	unsupported := base()
	unsupported.SQL.EnvironmentOverrides = []SQLEnvironmentOverride{{Environment: "prod", Content: "DROP TABLE menus", Reason: "保存原文待核对"}}
	if err := st.CreateChange(unsupported); err != nil {
		t.Fatal("unsupported execution syntax should remain a draft", err)
	}
	if _, err := st.SetChangeReady(unsupported.ID, true); err == nil {
		t.Fatal("unsupported environment SQL became ready")
	}
	c := base()
	c.SQL.EnvironmentOverrides = []SQLEnvironmentOverride{{Environment: "prod", Content: "UPDATE menus SET enabled=1 WHERE id=856", Reason: "生产数据编号不同"}, {Environment: "test", Skip: true, Reason: "测试无需执行"}}
	if err := st.CreateChange(c); err != nil {
		t.Fatal(err)
	}
	got := st.GetChange(c.ID)
	if len(got.SQL.EnvironmentOverrides) != 2 {
		t.Fatal(got)
	}
	for _, tt := range []struct {
		env, sql       string
		skip, explicit bool
	}{{"prod", c.SQL.EnvironmentOverrides[0].Content, false, true}, {"test", "", true, true}, {"dev", c.SQL.Content, false, false}} {
		selected, explicit, err := selectEnvironmentSQL(got.SQL, tt.env)
		if err != nil || selected.Content != tt.sql || selected.Skip != tt.skip || explicit != tt.explicit {
			t.Fatal(tt, selected, explicit, err)
		}
	}
	legacy := base().SQL
	doc, _ := json.Marshal(legacy)
	if strings.Contains(string(doc), "environment_overrides") {
		t.Fatal("legacy digest changed", string(doc))
	}
	generated := base()
	generated.SQL.DataSync = &DataSyncSnapshot{}
	generated.SQL.EnvironmentOverrides = c.SQL.EnvironmentOverrides
	if err := normalizeChange(generated); err == nil || !strings.Contains(err.Error(), "冻结") {
		t.Fatal("frozen data sync edited", err)
	}
}

func TestSQLEnvironmentSkipFrozenPlanAndDrift(t *testing.T) {
	st, m, _ := deliveryFixture(t)
	env := *st.GetEnvironment("test")
	env.Approvers = "reviewer"
	if err := st.UpsertEnvironment(env); err != nil {
		t.Fatal(err)
	}
	// Intentionally no database binding or connection: an explicit skip must not open one.
	c := sqlChangeFixture(t, st, "UPDATE menus SET enabled=1 WHERE id=1")
	st.db.Exec(`UPDATE changes SET status='draft' WHERE id=?`, c.ID)
	c = st.GetChange(c.ID)
	c.SQL.EnvironmentOverrides = []SQLEnvironmentOverride{{Environment: "test", Skip: true, Reason: "测试已由基础数据覆盖"}}
	if err := st.UpdateChange(c); err != nil {
		t.Fatal(err)
	}
	st.SetChangeReady(c.ID, true)
	p, err := st.prepareSQLNode(context.Background(), "sql", []int64{c.ID}, "test")
	if err != nil || p.Mode != "skipped" || len(p.ChangeIDs) != 0 || len(p.Statements) != 0 || len(p.ScriptSelections) != 1 {
		t.Fatal(p, err)
	}
	target, err := st.deliveryChangeTarget(c, "test")
	if err != nil || !target.Skip || target.Connection != "" {
		t.Fatal(target, err)
	}
	run := sqlRunFixture(t, st, m, c, "test")
	if run.Plan.SQLPlans[0].ScriptSelections[0].Reason != "测试已由基础数据覆盖" {
		t.Fatal("skip not frozen")
	}
	advanced, err := st.AdvanceDeliveryRun(run.ID, "alice", false, m)
	if err != nil || advanced.Steps[0].Status != "success" || !strings.Contains(advanced.Steps[0].Detail, "未执行 SQL") {
		t.Fatal(advanced, err)
	}
	var ledger int
	st.db.QueryRow(`SELECT COUNT(*) FROM sql_execution_ledger WHERE change_id=?`, c.ID).Scan(&ledger)
	if ledger != 0 {
		t.Fatal("skip created execution ledger")
	}
	record := workDeliveryRecord(advanced)
	if len(record.SQLPlans) != 1 || !record.SQLPlans[0].ScriptSelections[0].Skip {
		t.Fatal("record omitted actual skip")
	}
	// Stored payload changes after approval cannot change a frozen skip into execution.
	c.SQL.EnvironmentOverrides[0].Reason = "改动后的原因"
	raw, _ := json.Marshal(map[string]any{"sql": c.SQL})
	st.db.Exec(`UPDATE change_payloads SET document=? WHERE change_id=?`, string(raw), c.ID)
	if _, err = st.executeSQLNode(context.Background(), run, p); err == nil {
		t.Fatal("approved exception drift accepted")
	}
	if _, err = st.prepareSQLNode(context.Background(), "sql", []int64{c.ID}, "prod"); err == nil {
		t.Fatal("default in unbound production silently skipped")
	}
}

func TestSQLReuseChecksSelectedEnvironmentScript(t *testing.T) {
	st, _, _ := deliveryFixture(t)
	conn := ResourceConnection{Name: "same-db", Kind: "mysql", Endpoint: "127.0.0.1:1", Username: "qa", Config: map[string]string{"tls": "false"}}
	if err := st.UpsertConnection(conn); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveApplicationResources("orders", ApplicationResources{Requirements: []ApplicationResourceRequirement{{Alias: "db", Kind: "mysql"}}, Bindings: []ApplicationEnvironmentResource{{Alias: "db", Environment: "test", Connection: conn.Name, Config: map[string]string{"database": "orders"}}, {Alias: "db", Environment: "prod", Connection: conn.Name, Config: map[string]string{"database": "orders"}}}}); err != nil {
		t.Fatal(err)
	}
	c := &Change{Kind: "mysql", Title: "exception", Service: "orders", SQL: &SQLChange{ResourceAlias: "db", Content: "UPDATE menus SET enabled=1 WHERE id=1", MaxAffectedRows: 100, EnvironmentOverrides: []SQLEnvironmentOverride{{Environment: "prod", Content: "UPDATE menus SET enabled=1 WHERE id=856", Reason: "生产不同数据"}}}}
	if err := st.CreateChange(c); err != nil {
		t.Fatal(err)
	}
	st.SetChangeReady(c.ID, true)
	ops, _ := parseChangeSQL(c.SQL.Content)
	ops[0].ChangeID = c.ID
	p := &SQLExecutionPlan{NodeID: "sql", ChangeIDs: []int64{c.ID}, Services: []string{c.Service}, Aliases: []string{"db"}, Connection: conn.Name, Database: "orders", DestinationDigest: func() string {
		target, err := st.sqlBinding("orders", "test", "db")
		if err != nil {
			t.Fatal(err)
		}
		return sqlDestination(target)
	}(), ChangeDigests: []string{digestJSON([]any{c.Service, c.SQL})}, Statements: ops}
	doc, _ := json.Marshal(DeliveryPlan{Environment: Environment{Name: "test"}, SQLPlans: []*SQLExecutionPlan{p}})
	r, err := st.db.Exec(`INSERT INTO delivery_runs(plan,digest,status,execution,env_secrets,owner,approver,created_at,revision) VALUES(?,'proof','success','[]','{}','alice','reviewer',1,1)`, string(doc))
	if err != nil {
		t.Fatal(err)
	}
	runID, _ := r.LastInsertId()
	if _, err = st.db.Exec(`INSERT INTO sql_execution_ledger(connection,database_name,change_id,digest,status,run_id,node_id,detail) VALUES(?,?,?,?,?,?,?,'proof')`, conn.Name, "orders", c.ID, sqlChangeDigest(p, c.ID), "success", runID, "sql"); err != nil {
		t.Fatal(err)
	}
	if _, err = st.prepareSQLNode(context.Background(), "sql", []int64{c.ID}, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err = st.prepareSQLNode(context.Background(), "sql", []int64{c.ID}, "prod"); err == nil || !strings.Contains(err.Error(), "当前环境不同") {
		t.Fatal("different script on same DB treated as already executed", err)
	}
}
