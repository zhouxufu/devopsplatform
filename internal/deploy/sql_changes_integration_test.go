package deploy

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// Opt-in, LOCAL disposable database only. No production DSN is accepted.
func sqlTargetFixture(t *testing.T) (*Store, *ReleaseManager, *sql.DB, string) {
	t.Helper()
	addr := os.Getenv("DEVOPS_SQL_TEST_ADDR")
	if addr == "" {
		t.Skip("set DEVOPS_SQL_TEST_ADDR for disposable local MySQL integration tests")
	}
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Fatal("SQL tests only accept a loopback disposable MySQL")
	}
	st, m, _ := deliveryFixture(t)
	env := *st.GetEnvironment("test")
	env.Approvers = "reviewer"
	if err := st.UpsertEnvironment(env); err != nil {
		t.Fatal(err)
	}
	c := ResourceConnection{Name: "qa-mysql", Kind: "mysql", Endpoint: addr, Username: "root", Password: os.Getenv("DEVOPS_SQL_TEST_PASSWORD"), Config: map[string]string{"tls": "false"}}
	if e := st.UpsertConnection(c); e != nil {
		t.Fatal(e)
	}
	if e := st.SaveApplicationResources("orders", ApplicationResources{Requirements: []ApplicationResourceRequirement{{Alias: "db", Kind: "mysql"}}, Bindings: []ApplicationEnvironmentResource{{Alias: "db", Environment: "test", Connection: c.Name, Config: map[string]string{"database": "delivery_qa"}}, {Alias: "db", Environment: "prod", Connection: c.Name, Config: map[string]string{"database": "delivery_qa"}}}}); e != nil {
		t.Fatal(e)
	}
	db, e := func() (*sql.DB, error) {
		target, err := st.sqlBinding("orders", "test", "db")
		if err != nil {
			return nil, err
		}
		return openChangeMySQL(context.Background(), target)
	}()
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	table := fmt.Sprintf("sql_qa_%x", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = db.Exec("DROP TABLE IF EXISTS `" + table + "`")
		_, _ = db.Exec("DROP TABLE IF EXISTS `" + table + "_b`")
	})
	return st, m, db, table
}
func sqlChangeFixture(t *testing.T, st *Store, content string) *Change {
	t.Helper()
	c := &Change{Kind: "mysql", Title: "SQL QA", Service: "orders", Owner: "alice", SQL: &SQLChange{ResourceAlias: "db", Content: content, MaxAffectedRows: 100}}
	if e := st.CreateChange(c); e != nil {
		t.Fatal(e)
	}
	if _, e := st.SetChangeReady(c.ID, true); e != nil {
		t.Fatal(e)
	}
	return c
}
func sqlRunFixture(t *testing.T, st *Store, m *ReleaseManager, c *Change, environment string) *DeliveryRun {
	t.Helper()
	version := &ReleaseVersion{Version: fmt.Sprintf("sql-%d", c.ID), Title: "SQL version", Owner: "alice"}
	if e := st.CreateReleaseVersion(version); e != nil {
		t.Fatal(e)
	}
	if e := st.SetReleaseVersionChanges(version.ID, []int64{c.ID}); e != nil {
		t.Fatal(e)
	}
	if _, e := st.SetReleaseVersionReady(version.ID); e != nil {
		t.Fatal(e)
	}
	tpl := &DeliveryTemplate{Name: "SQL node", Mode: "version", Owner: "alice", Nodes: []DeliveryNode{{ID: "sql", Kind: "mysql", Name: "数据库变更"}}}
	if e := st.SaveDeliveryTemplate(tpl); e != nil {
		t.Fatal(e)
	}
	run, e := st.CreateDeliveryRun(DeliveryRequest{TemplateID: tpl.ID, VersionID: version.ID, Environment: environment, Reason: "SQL QA"}, "alice", m)
	if e != nil {
		t.Fatal(e)
	}
	if environment == "test" {
		if run.Status != "pending_approval" {
			t.Fatal("ordinary environment SQL bypassed review")
		}
		if _, err := st.AdvanceDeliveryRun(run.ID, "alice", false, m); err == nil {
			t.Fatal("unreviewed SQL advanced")
		}
		run, e = st.ReviewDeliveryRun(run.ID, "reviewer", run.Digest, true)
		if e != nil {
			t.Fatal(e)
		}
	}
	return run
}
func TestSQLMySQLDDLPreviewExecuteAndDedup(t *testing.T) {
	st, m, db, table := sqlTargetFixture(t)
	c := sqlChangeFixture(t, st, fmt.Sprintf("CREATE TABLE %s (id bigint NOT NULL, name varchar(64) NOT NULL DEFAULT '', PRIMARY KEY (id)); ALTER TABLE %s ADD COLUMN enabled int NOT NULL DEFAULT 1; CREATE UNIQUE INDEX idx_name ON %s (name)", table, table, table))
	run := sqlRunFixture(t, st, m, c, "test")
	var before int
	if e := db.QueryRow(`SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA='delivery_qa' AND TABLE_NAME=?`, table).Scan(&before); e != nil || before != 0 {
		t.Fatal("preview wrote external DDL")
	}
	got, e := st.AdvanceDeliveryRun(run.ID, "alice", false, m)
	if e != nil {
		t.Fatal(e)
	}
	if got.Steps[0].Status != "success" {
		t.Fatalf("execution failed: %+v", got.Steps)
	}
	got, e = st.AdvanceDeliveryRun(run.ID, "alice", false, m)
	if e != nil || got.Status != "success" {
		t.Fatalf("run not complete: %v %+v", e, got)
	}
	if repeat, err := st.prepareSQLNode(context.Background(), "sql", []int64{c.ID}, "test"); err != nil || len(repeat.ChangeIDs) != 0 || len(repeat.Applied) != 1 {
		t.Fatal("did not reuse successful execution", err)
	}
	// A new identical add-column change is safely skipped, not re-executed.
	skip := sqlChangeFixture(t, st, fmt.Sprintf("ALTER TABLE %s ADD COLUMN enabled int NOT NULL DEFAULT 1", table))
	p, e := st.prepareSQLNode(context.Background(), "preview", []int64{skip.ID}, "test")
	if e != nil || !p.Statements[0].Skip {
		t.Fatalf("identical column not skipped: %v %+v", e, p)
	}
	conflict := sqlChangeFixture(t, st, fmt.Sprintf("ALTER TABLE %s ADD COLUMN enabled varchar(32)", table))
	if _, e = st.prepareSQLNode(context.Background(), "preview", []int64{conflict.ID}, "test"); e == nil {
		t.Fatal("conflicting column passed preflight")
	}
}

func TestSQLMySQLResourceGroupMultipleDatabases(t *testing.T) {
	if os.Getenv("DEVOPS_RESOURCE_GROUP_SQL_TEST") != "1" {
		t.Skip("enable only on disposable delivery_qa + delivery_qa_groups schemas")
	}
	st, m, firstDB, table := sqlTargetFixture(t)
	g := &ResourceGroup{
		Name: "mysql-integration",
		Requirements: []ApplicationResourceRequirement{
			{Alias: "db", Kind: "mysql"}, {Alias: "other-db", Kind: "mysql"},
		},
		Bindings: []ApplicationEnvironmentResource{
			{Alias: "db", Environment: "test", Connection: "qa-mysql", Config: map[string]string{"database": "delivery_qa"}},
			{Alias: "other-db", Environment: "test", Connection: "qa-mysql", Config: map[string]string{"database": "delivery_qa_groups"}},
		},
	}
	if err := st.SaveResourceGroup(g); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveApplicationResources("orders", ApplicationResources{ResourceGroup: g.Name}); err != nil {
		t.Fatal(err)
	}
	target, err := st.sqlBinding("orders", "test", "other-db")
	if err != nil {
		t.Fatal(err)
	}
	secondDB, err := openChangeMySQL(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = secondDB.Exec("DROP TABLE IF EXISTS `" + table + "`")
		secondDB.Close()
	})
	ids := []int64{}
	for _, alias := range []string{"db", "other-db"} {
		ch := &Change{Kind: "mysql", Title: "Create in " + alias, Service: "orders", SQL: &SQLChange{ResourceAlias: alias, Content: fmt.Sprintf("CREATE TABLE %s (id bigint NOT NULL, PRIMARY KEY (id))", table), MaxAffectedRows: 100}}
		if err := st.CreateChange(ch); err != nil {
			t.Fatal(err)
		}
		st.SetChangeReady(ch.ID, true)
		ids = append(ids, ch.ID)
	}
	tpl := &DeliveryTemplate{Name: "Two databases", Mode: "fixed", Nodes: []DeliveryNode{{ID: "sql", Kind: "mysql", Name: "SQL", ChangeIDs: ids}}}
	if err := st.SaveDeliveryTemplate(tpl); err != nil {
		t.Fatal(err)
	}
	req := DeliveryRequest{TemplateID: tpl.ID, Environment: "test", Reason: "Disposable QA"}
	run, err := st.CreateDeliveryRun(req, "alice", m)
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Plan.SQLPlans) != 2 || len(run.Steps) != 2 || run.Status != "pending_approval" {
		t.Fatal("split / approval missing")
	}
	for _, db := range []*sql.DB{firstDB, secondDB} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=?`, table).Scan(&count); err != nil || count != 0 {
			t.Fatal("preview wrote DDL", err)
		}
	}
	if _, err := st.ReviewDeliveryRun(run.ID, "reviewer", run.Digest, true); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		got, err := st.AdvanceDeliveryRun(run.ID, "alice", false, m)
		if err != nil || got.Steps[i].Status != "success" {
			t.Fatal("approved split execution failed", err, got)
		}
	}
	for _, db := range []*sql.DB{firstDB, secondDB} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=?`, table).Scan(&count); err != nil || count != 1 {
			t.Fatal("DDL not applied to each database", err)
		}
	}
	again, _, err := st.ResolveDelivery(req, m)
	if err != nil || len(again.Blockers) > 0 || len(again.SQLPlans) != 2 {
		t.Fatal("repeat preflight failed", err, again)
	}
	for _, plan := range again.SQLPlans {
		if plan.Mode != "applied" || len(plan.Applied) != 1 {
			t.Fatal("successful split change not skipped", plan)
		}
	}
}

func TestSQLMySQLAliasedQualifiedUpdateUsesSameScope(t *testing.T) {
	st, m, db, table := sqlTargetFixture(t)
	if _, err := db.Exec("CREATE TABLE `" + table + "` (id int NOT NULL PRIMARY KEY, name varchar(64)) ENGINE=InnoDB"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO `" + table + "` (id,name) VALUES (1,'old'),(2,'keep')"); err != nil {
		t.Fatal(err)
	}
	c := sqlChangeFixture(t, st, "UPDATE delivery_qa.`"+table+"` AS t SET t.name='new' WHERE t.id=1")
	run := sqlRunFixture(t, st, m, c, "test")
	op := run.Plan.SQLPlans[0].Statements[0]
	if op.ExpectedRows != 1 || op.Scope != "id = 1" || op.Database != "delivery_qa" || strings.Contains(op.SQL, "t.") {
		t.Fatalf("incorrect approved scope: %+v", op)
	}
	got, err := st.AdvanceDeliveryRun(run.ID, "alice", false, m)
	if err != nil || got.Steps[0].Status != "success" {
		t.Fatalf("execution failed: %v %+v", err, got)
	}
	for id, expected := range map[int]string{1: "new", 2: "keep"} {
		var actual string
		if err := db.QueryRow("SELECT name FROM `"+table+"` WHERE id=?", id).Scan(&actual); err != nil || actual != expected {
			t.Fatalf("wrong row updated: %d %s %v", id, actual, err)
		}
	}
}
func TestSQLMySQLApprovalAndSchemaDrift(t *testing.T) {
	st, m, db, table := sqlTargetFixture(t)
	if _, e := db.Exec("CREATE TABLE " + table + " (id bigint NOT NULL PRIMARY KEY) ENGINE=InnoDB"); e != nil {
		t.Fatal(e)
	}
	c := sqlChangeFixture(t, st, "ALTER TABLE "+table+" ADD COLUMN enabled int NOT NULL DEFAULT 1")
	run := sqlRunFixture(t, st, m, c, "prod")
	if _, e := st.AdvanceDeliveryRun(run.ID, "alice", false, m); e == nil {
		t.Fatal("executed before approval")
	}
	if _, e := st.ReviewDeliveryRun(run.ID, "alice", run.Digest, true); e == nil {
		t.Fatal("self approval allowed")
	}
	if _, e := st.ReviewDeliveryRun(run.ID, "reviewer", run.Digest, true); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec("ALTER TABLE " + table + " ADD COLUMN external_edit int"); e != nil {
		t.Fatal(e)
	}
	got, e := st.AdvanceDeliveryRun(run.ID, "alice", false, m)
	if e != nil {
		t.Fatal(e)
	}
	if got.Status != "failed" || !strings.Contains(got.Steps[0].Detail, "结构发生变化") {
		t.Fatalf("drift not blocked: %+v", got.Steps)
	}
	var exists int
	_ = db.QueryRow(`SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA='delivery_qa' AND TABLE_NAME=? AND COLUMN_NAME='enabled'`, table).Scan(&exists)
	if exists != 0 {
		t.Fatal("DDL executed despite drift")
	}
}
func TestSQLMySQLDuplicateAdvanceAndInterruptedStep(t *testing.T) {
	st, m, db, table := sqlTargetFixture(t)
	if _, err := db.Exec("CREATE TABLE " + table + " (id int NOT NULL PRIMARY KEY, label varchar(64) NOT NULL) ENGINE=InnoDB"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO " + table + " (id,label) VALUES (1,'before')"); err != nil {
		t.Fatal(err)
	}
	c := sqlChangeFixture(t, st, "UPDATE "+table+" SET label='after' WHERE id=1")
	run := sqlRunFixture(t, st, m, c, "test")
	blocker, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	rows, err := blocker.Query("SELECT id FROM " + table + " WHERE id=1 FOR UPDATE")
	if err != nil {
		t.Fatal(err)
	}
	rows.Close()
	type result struct {
		run *DeliveryRun
		err error
	}
	done := make(chan result, 1)
	go func() {
		v, e := st.AdvanceDeliveryRun(run.ID, "alice", false, m)
		done <- result{v, e}
	}()
	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	waiting := true
	for waiting {
		select {
		case <-deadline:
			t.Fatal("SQL node did not reserve execution")
		case <-ticker.C:
			v := st.DeliveryRun(run.ID)
			waiting = v.Steps[0].Status != "executing"
		}
	}
	if _, e := st.AdvanceDeliveryRun(run.ID, "alice", false, m); e == nil {
		t.Fatal("duplicate click was accepted during SQL execution")
	}
	if err := blocker.Rollback(); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if r.err != nil || r.run.Steps[0].Status != "success" {
			t.Fatalf("first execution failed: %+v %v", r.run, r.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("SQL executor did not finish after row lock release")
	}
	if len(st.SQLLedger()) != 1 {
		t.Fatal("duplicate click created duplicate ledger")
	}
	interrupted := sqlChangeFixture(t, st, "UPDATE "+table+" SET label='unexpected' WHERE id=1")
	run = sqlRunFixture(t, st, m, interrupted, "test")
	run.Steps[0].Status, run.Status = "executing", "running"
	if e := st.saveDeliveryRun(run); e != nil {
		t.Fatal(e)
	}
	if _, e := st.AdvanceDeliveryRun(run.ID, "alice", false, m); e == nil {
		t.Fatal("interrupted SQL node automatically replayed")
	}
	var label string
	if err := db.QueryRow("SELECT label FROM " + table + " WHERE id=1").Scan(&label); err != nil || label != "after" {
		t.Fatal("interrupted SQL node modified data")
	}
}

func TestSQLMySQLDestinationAndContentDrift(t *testing.T) {
	st, m, db, table := sqlTargetFixture(t)
	c := sqlChangeFixture(t, st, "CREATE TABLE "+table+" (id int NOT NULL, PRIMARY KEY (id))")
	run := sqlRunFixture(t, st, m, c, "test")
	connection := st.GetConnection("qa-mysql")
	connection.Username = "changed-identity"
	if e := st.UpsertConnection(*connection); e != nil {
		t.Fatal(e)
	}
	got, e := st.AdvanceDeliveryRun(run.ID, "alice", false, m)
	if e != nil || got.Status != "failed" || !strings.Contains(got.Steps[0].Detail, "身份发生变化") {
		t.Fatalf("connection drift not stopped: %+v %v", got, e)
	}
	connection.Username = "root"
	if e := st.UpsertConnection(*connection); e != nil {
		t.Fatal(e)
	}
	run = sqlRunFixture(t, st, m, sqlChangeFixture(t, st, "CREATE TABLE "+table+" (id int NOT NULL, PRIMARY KEY (id))"), "test")
	id := run.Plan.SQLPlans[0].ChangeIDs[0]
	if _, e := st.SetChangeReady(id, false); e == nil {
		t.Fatal("version-contained SQL was editable through normal API")
	}
	changed := st.GetChange(id)
	changed.SQL.Content = "CREATE TABLE " + table + " (id bigint NOT NULL, PRIMARY KEY (id))"
	// Simulate an out-of-band metadata edit; normal APIs keep version SQL
	// immutable, but the executor must also reject a corrupted snapshot source.
	tx, e := st.db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if e := saveSQLPayload(tx, changed); e != nil {
		t.Fatal(e)
	}
	if e := tx.Commit(); e != nil {
		t.Fatal(e)
	}
	got, e = st.AdvanceDeliveryRun(run.ID, "alice", false, m)
	if e != nil || got.Status != "failed" || !strings.Contains(got.Steps[0].Detail, "变更内容或状态已变化") {
		t.Fatalf("SQL content drift not stopped: %+v %v", got, e)
	}
	var count int
	if e := db.QueryRow(`SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA='delivery_qa' AND TABLE_NAME=?`, table).Scan(&count); e != nil || count != 0 || len(st.SQLLedger()) != 0 {
		t.Fatal("drifted SQL performed external writes or reserved ledger")
	}
}

func TestSQLMySQLDMLCommitDataDriftAndRollback(t *testing.T) {
	st, m, db, table := sqlTargetFixture(t)
	for _, name := range []string{table, table + "_b"} {
		if _, e := db.Exec("CREATE TABLE " + name + " (id int NOT NULL PRIMARY KEY, label varchar(64)) ENGINE=InnoDB"); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := db.Exec("INSERT INTO " + table + " VALUES (1,'old')"); e != nil {
		t.Fatal(e)
	}
	c := sqlChangeFixture(t, st, "UPDATE "+table+" SET label='new' WHERE id=1")
	run := sqlRunFixture(t, st, m, c, "test")
	got, e := st.AdvanceDeliveryRun(run.ID, "alice", false, m)
	if e != nil || got.Steps[0].Status != "success" {
		t.Fatalf("DML failed: %v %+v", e, got)
	}
	var label string
	_ = db.QueryRow("SELECT label FROM " + table + " WHERE id=1").Scan(&label)
	if label != "new" {
		t.Fatal("update not committed")
	}
	drift := sqlChangeFixture(t, st, "UPDATE "+table+" SET label='approved' WHERE id=1")
	run = sqlRunFixture(t, st, m, drift, "test")
	_, _ = db.Exec("UPDATE " + table + " SET label='concurrent' WHERE id=1")
	got, e = st.AdvanceDeliveryRun(run.ID, "alice", false, m)
	if e != nil || got.Status != "failed" {
		t.Fatal("data drift not blocked")
	}
	_ = db.QueryRow("SELECT label FROM " + table + " WHERE id=1").Scan(&label)
	if label != "concurrent" {
		t.Fatal("overwrote concurrent edit")
	}
	rollback := sqlChangeFixture(t, st, "INSERT INTO "+table+"_b (id,label) VALUES (1,'first'); INSERT INTO "+table+" (id,label) VALUES (1,'duplicate')")
	run = sqlRunFixture(t, st, m, rollback, "test")
	got, e = st.AdvanceDeliveryRun(run.ID, "alice", false, m)
	if e != nil || got.Status != "failed" {
		t.Fatal("duplicate constraint not stopped")
	}
	var count int
	_ = db.QueryRow("SELECT COUNT(*) FROM " + table + "_b").Scan(&count)
	if count != 0 {
		t.Fatal("partly committed failed transaction")
	}
}

// Opt-in isolated MySQL coverage: the same change selects distinct scripts and
// executes them on separate disposable databases; no real application data.
func TestSQLMySQLEnvironmentSpecificExecution(t *testing.T) {
	st, m, testDB, table := sqlTargetFixture(t)
	database := "env_" + table
	if _, err := testDB.Exec("CREATE DATABASE `" + database + "` CHARACTER SET utf8mb4"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testDB.Exec("DROP DATABASE IF EXISTS `" + database + "`") })
	connection := *st.GetConnection("qa-mysql")
	connection.Name = "qa-mysql-environment"
	connection.Password = os.Getenv("DEVOPS_SQL_TEST_PASSWORD")
	connection.Config = map[string]string{"tls": "false"}
	if err := st.UpsertConnection(connection); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveApplicationResources("orders", ApplicationResources{Requirements: []ApplicationResourceRequirement{{Alias: "db", Kind: "mysql"}}, Bindings: []ApplicationEnvironmentResource{{Alias: "db", Environment: "test", Connection: "qa-mysql", Config: map[string]string{"database": "delivery_qa"}}, {Alias: "db", Environment: "prod", Connection: connection.Name, Config: map[string]string{"database": database}}}}); err != nil {
		t.Fatal(err)
	}
	target, err := st.sqlBinding("orders", "prod", "db")
	if err != nil {
		t.Fatal(err)
	}
	productionDB, err := openChangeMySQL(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { productionDB.Close() })
	env := *st.GetEnvironment("prod")
	env.Approvers = "reviewer"
	if err := st.UpsertEnvironment(env); err != nil {
		t.Fatal(err)
	}
	defaultSQL := fmt.Sprintf("CREATE TABLE %s (id bigint NOT NULL, name varchar(64) NOT NULL DEFAULT '', PRIMARY KEY (id))", table)
	productionSQL := fmt.Sprintf("CREATE TABLE %s (id bigint NOT NULL, name varchar(128) NOT NULL DEFAULT '', PRIMARY KEY (id))", table)
	c := &Change{Kind: "mysql", Title: "同一变更按环境执行", Service: "orders", Owner: "alice", SQL: &SQLChange{ResourceAlias: "db", Content: defaultSQL, MaxAffectedRows: 100, EnvironmentOverrides: []SQLEnvironmentOverride{{Environment: "prod", Content: productionSQL, Reason: "生产保留更长的字段"}}}}
	if err := st.CreateChange(c); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetChangeReady(c.ID, true); err != nil {
		t.Fatal(err)
	}
	var testRun *DeliveryRun
	for _, name := range []string{"test", "prod"} {
		var run *DeliveryRun
		if name == "test" {
			run = sqlRunFixture(t, st, m, c, name)
			testRun = run
		} else {
			run, err = st.CreateDeliveryRun(DeliveryRequest{TemplateID: testRun.Plan.Template.ID, VersionID: testRun.Plan.Version.ID, Environment: name, Reason: "environment exception QA"}, "alice", m)
			if err != nil {
				t.Fatal(err)
			}
		}
		if name == "prod" {
			if run.Status != "pending_approval" {
				t.Fatal("production exception bypassed approval")
			}
			run, err = st.ReviewDeliveryRun(run.ID, "reviewer", run.Digest, true)
			if err != nil {
				t.Fatal(err)
			}
		}
		plan := run.Plan.SQLPlans[0]
		if name == "prod" && (len(plan.ScriptSelections) != 1 || plan.ScriptSelections[0].Content != productionSQL || !strings.Contains(plan.Statements[0].SQL, "128")) {
			t.Fatal("production executed default", plan)
		}
		if name == "test" && (len(plan.ScriptSelections) != 0 || !strings.Contains(plan.Statements[0].SQL, "64")) {
			t.Fatal("default script replaced", plan)
		}
		next, err := st.AdvanceDeliveryRun(run.ID, "alice", false, m)
		if err != nil || next.Steps[0].Status != "success" {
			t.Fatal(next, err)
		}
	}
	for _, target := range []struct {
		db     *sql.DB
		schema string
		length int
	}{{testDB, "delivery_qa", 64}, {productionDB, database, 128}} {
		var length int
		if err := target.db.QueryRow(`SELECT CHARACTER_MAXIMUM_LENGTH FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=? AND TABLE_NAME=? AND COLUMN_NAME='name'`, target.schema, table).Scan(&length); err != nil || length != target.length {
			t.Fatal(length, err)
		}
	}
}
