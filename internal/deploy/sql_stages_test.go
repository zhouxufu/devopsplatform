package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

func TestStandardSQLStagePartitionGuards(t *testing.T) {
	expected := []DeliveryNode{{ID: "sql", Kind: "mysql", Service: "orders", ChangeIDs: []int64{1, 2}}, {ID: "deploy", Kind: "deploy", Service: "orders"}, {ID: "review", Kind: "approval", Approvers: []string{"reviewer"}, ApprovalMode: "all"}}
	valid := []DeliveryNode{{ID: "review", Kind: "approval", Approvers: []string{"reviewer"}, ApprovalMode: "all"}, {ID: "sql", Kind: "mysql", Service: "orders", ChangeIDs: []int64{1}}, {ID: "deploy", Kind: "deploy", Service: "orders"}, {ID: "after", SQLGroup: "sql", Kind: "mysql", Service: "orders", ChangeIDs: []int64{2}}}
	if err := validateStandardOverrides(expected, valid); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []func([]DeliveryNode){
		func(n []DeliveryNode) { n[3].ChangeIDs = []int64{1} },
		func(n []DeliveryNode) { n[3].ChangeIDs = []int64{3} },
		func(n []DeliveryNode) { n[3].ChangeIDs = nil },
		func(n []DeliveryNode) { n[3].Service = "other" },
		func(n []DeliveryNode) { n[3].SQLGroup = "deploy" },
		func(n []DeliveryNode) { n[0].Approvers = []string{"alice"} },
	} {
		raw, _ := json.Marshal(valid)
		var bad []DeliveryNode
		_ = json.Unmarshal(raw, &bad)
		mutation(bad)
		if err := validateStandardOverrides(expected, bad); err == nil {
			t.Fatal("accepted invalid partition", bad)
		}
	}
	if err := validateStandardOverrides(expected, valid[1:]); err == nil {
		t.Fatal("approval removed")
	}
}

func TestSQLMySQLStagesAfterDDLAndDeployment(t *testing.T) {
	for _, scenario := range []string{"success", "schema-drift", "row-limit", "missing-receipt"} {
		t.Run(scenario, func(t *testing.T) {
			st, m, db, table := sqlTargetFixture(t)
			if _, err := db.Exec("CREATE TABLE `" + table + "` (id bigint NOT NULL, PRIMARY KEY(id)) ENGINE=InnoDB"); err != nil {
				t.Fatal(err)
			}
			before := sqlChangeFixture(t, st, "ALTER TABLE "+table+" ADD COLUMN enabled int NOT NULL DEFAULT 0")
			after := sqlChangeFixture(t, st, "UPDATE "+table+" SET enabled=1 WHERE id > 0")
			tpl := &DeliveryTemplate{Name: "SQL stages", Mode: "fixed", Nodes: []DeliveryNode{{ID: "before", Name: "Before deploy", Kind: "mysql", ChangeIDs: []int64{before.ID}}, {ID: "service", Name: "Synthetic service checkpoint", Kind: "manual", Instructions: "Local fixture deployment checkpoint"}, {ID: "after", Name: "After deploy", Kind: "mysql", ChangeIDs: []int64{after.ID}}}}
			if err := st.SaveDeliveryTemplate(tpl); err != nil {
				t.Fatal(err)
			}
			run, err := st.CreateDeliveryRun(DeliveryRequest{TemplateID: tpl.ID, Environment: "test", Reason: "Local SQL stage test"}, "alice", m)
			if err != nil {
				t.Fatal(err)
			}
			plan := run.Plan.SQLPlans[1]
			if !plan.DeferredData || plan.Statements[0].ExpectedRows != -1 || len(plan.PreviousNodes) != 1 {
				t.Fatal("missing staged contract", plan)
			}
			frozen := digestJSON(run.Plan)
			if _, err = st.executeSQLNode(context.Background(), run, plan); err == nil {
				t.Fatal("ran before predecessor")
			}
			run, err = st.ReviewDeliveryRun(run.ID, "reviewer", run.Digest, true)
			if err != nil {
				t.Fatal(err)
			}
			run, err = st.AdvanceDeliveryRun(run.ID, "alice", false, m)
			if err != nil || run.Steps[0].Status != "success" {
				t.Fatal(run, err)
			}
			// A service may write data between stages. No business service is deployed.
			if _, err = db.Exec("INSERT INTO `" + table + "` (id) VALUES (1)"); err != nil {
				t.Fatal(err)
			}
			run, err = st.AdvanceDeliveryRun(run.ID, "alice", true, m)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "schema-drift":
				_, err = db.Exec("ALTER TABLE `" + table + "` ADD COLUMN extra int")
			case "row-limit":
				for i := 2; i <= 101; i++ {
					if _, err = db.Exec("INSERT INTO `"+table+"` (id) VALUES (?)", i); err != nil {
						t.Fatal(err)
					}
				}
			case "missing-receipt":
				_, err = st.db.Exec(`UPDATE sql_execution_ledger SET status='uncertain' WHERE change_id=?`, before.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			run, err = st.AdvanceDeliveryRun(run.ID, "alice", false, m)
			if err != nil {
				t.Fatal(err)
			}
			if digestJSON(run.Plan) != frozen {
				t.Fatal("execution rewrote reviewed plan")
			}
			var count int
			if err = db.QueryRow("SELECT COUNT(*) FROM `" + table + "` WHERE enabled=1").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if scenario != "success" {
				if run.Status != "failed" || count != 0 {
					t.Fatal("failed precondition wrote data", run, count)
				}
				return
			}
			if count != 1 || run.Steps[2].Status != "success" {
				t.Fatal("stage did not execute", run, count)
			}
			actual := st.DeliveryRun(run.ID)
			if len(actual.SQLStageBaselines) != 1 || len(actual.SQLStageBaselines[0].Scopes) != 1 || actual.SQLStageBaselines[0].Scopes[0].Rows != 1 {
				t.Fatal("missing runtime evidence", actual.SQLStageBaselines)
			}
			if _, err = st.executeSQLNode(context.Background(), run, plan); err == nil {
				t.Fatal("successful stage replayed")
			}
		})
	}
}

func TestSQLMySQLStagesCreateThenInsert(t *testing.T) {
	st, m, db, table := sqlTargetFixture(t)
	a := sqlChangeFixture(t, st, fmt.Sprintf("CREATE TABLE %s (id bigint NOT NULL, name varchar(20) NOT NULL DEFAULT '', PRIMARY KEY(id))", table))
	b := sqlChangeFixture(t, st, fmt.Sprintf("INSERT INTO %s (id,name) VALUES (1,'fixture')", table))
	tpl := &DeliveryTemplate{Name: "Create then insert", Mode: "fixed", Nodes: []DeliveryNode{{ID: "create", Name: "Create", Kind: "mysql", ChangeIDs: []int64{a.ID}}, {ID: "insert", Name: "Insert", Kind: "mysql", ChangeIDs: []int64{b.ID}}}}
	if err := st.SaveDeliveryTemplate(tpl); err != nil {
		t.Fatal(err)
	}
	run, err := st.CreateDeliveryRun(DeliveryRequest{TemplateID: tpl.ID, Environment: "test", Reason: "Fixture"}, "alice", m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.ReviewDeliveryRun(run.ID, "reviewer", run.Digest, true); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		run, err = st.AdvanceDeliveryRun(run.ID, "alice", false, m)
		if err != nil || run.Status == "failed" {
			t.Fatal(run, err)
		}
	}
	var name string
	if err = db.QueryRow("SELECT name FROM `" + table + "` WHERE id=1").Scan(&name); err != nil || name != "fixture" {
		t.Fatal(name, err)
	}
	// An extra column must not pass projected-schema normalization.
	p := run.Plan.SQLPlans[1]
	catalog := sqlCatalog{}
	raw, _ := json.Marshal(p.ExpectedSchema)
	_ = json.Unmarshal(raw, &catalog)
	catalog[table].Columns["unexpected"] = sqlColumn{Type: "int"}
	if sqlStageSchemaMatches(p, catalog) {
		t.Fatal("schema drift accepted")
	}
}

func TestSQLMySQLStandardSplitAndOrder(t *testing.T) {
	st, m, db, table := sqlTargetFixture(t)
	before := sqlChangeFixture(t, st, fmt.Sprintf("CREATE TABLE %s (id bigint NOT NULL, PRIMARY KEY(id))", table))
	after := sqlChangeFixture(t, st, fmt.Sprintf("INSERT INTO %s (id) VALUES (1)", table))
	req := DeliveryRequest{StrategyKind: "standard", SourceType: "changes", SourceChangeIDs: []int64{before.ID, after.ID}, Environment: "test", Reason: "Standard SQL split"}
	draft, err := st.DraftDelivery(req)
	if err != nil {
		t.Fatal(err)
	}
	var grouped DeliveryNode
	for _, n := range draft.Nodes {
		if n.Kind == "mysql" {
			grouped = n
		} else {
			req.Nodes = append(req.Nodes, n)
		}
	}
	if len(grouped.ChangeIDs) != 2 {
		t.Fatal(draft.Nodes)
	}
	second := grouped
	second.ID = "sql-after"
	second.SQLGroup = grouped.ID
	second.ChangeIDs = []int64{after.ID}
	second.Name = "Initialize after structure"
	grouped.ChangeIDs = []int64{before.ID}
	req.Nodes = append(req.Nodes, grouped, second)
	req = standardSign(t, st, m, req)
	run, err := st.CreateDeliveryRun(req, "alice", m)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status == "pending_approval" {
		run, err = st.ReviewDeliveryRun(run.ID, "reviewer", run.Digest, true)
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		run, err = st.AdvanceDeliveryRun(run.ID, "alice", false, m)
		if err != nil || run.Status == "failed" {
			t.Fatal(run, err)
		}
	}
	var count int
	if err = db.QueryRow("SELECT COUNT(*) FROM `" + table + "`").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}
