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
	c := ResourceConnection{Name: "qa-mysql", Kind: "mysql", Endpoint: addr, Username: "root", Password: os.Getenv("DEVOPS_SQL_TEST_PASSWORD"), Config: map[string]string{"database": "delivery_qa", "tls": "false"}}
	if e := st.UpsertConnection(c); e != nil {
		t.Fatal(e)
	}
	if e := st.SaveApplicationResources("orders", ApplicationResources{Requirements: []ApplicationResourceRequirement{{Alias: "db", Kind: "mysql"}}, Bindings: []ApplicationEnvironmentResource{{Alias: "db", Environment: "test", Connection: c.Name}, {Alias: "db", Environment: "prod", Connection: c.Name}}}); e != nil {
		t.Fatal(e)
	}
	db, e := openChangeMySQL(context.Background(), &c)
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
	if _, e = st.prepareSQLNode(context.Background(), "sql", []int64{c.ID}, "test"); e == nil {
		t.Fatal("did not block repeated execution")
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
