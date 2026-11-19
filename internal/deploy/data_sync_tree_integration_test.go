package deploy

import (
	"context"
	"math/big"
	"strconv"
	"testing"
)

// Opt-in local disposable MySQL: real reads, frozen approval, transactional
// migration and topology drift. Never uses a configured business database.
func TestDataSyncTreeMySQLMigrationAndTopologyDrift(t *testing.T) {
	st, m, db, table := sqlTargetFixture(t)
	ctx := context.Background()
	if _, err := db.Exec("CREATE DATABASE IF NOT EXISTS delivery_sync_source_qa"); err != nil {
		t.Fatal(err)
	}
	connection := *st.GetConnection("qa-mysql")
	connection.Name = "tree-source"
	connection.Config = map[string]string{"tls": "false"}
	if err := st.UpsertConnection(connection); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveApplicationResources("orders", ApplicationResources{Requirements: []ApplicationResourceRequirement{{Alias: "db", Kind: "mysql"}}, Bindings: []ApplicationEnvironmentResource{{Alias: "db", Environment: "test", Connection: "tree-source", Config: map[string]string{"database": "delivery_sync_source_qa"}}, {Alias: "db", Environment: "prod", Connection: "qa-mysql", Config: map[string]string{"database": "delivery_qa"}}}}); err != nil {
		t.Fatal(err)
	}
	resolvedSource, err := st.sqlBinding("orders", "test", "db")
	if err != nil {
		t.Fatal(err)
	}
	source, err := openChangeMySQL(ctx, resolvedSource)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	defer source.Exec("DROP TABLE IF EXISTS `" + table + "`")
	ddl := "CREATE TABLE `" + table + "` (id bigint NOT NULL PRIMARY KEY,parent_id bigint NOT NULL,code varchar(100) NOT NULL,name varchar(100) NOT NULL) ENGINE=InnoDB"
	// Schema is deliberately unique by ID only.
	if _, err = source.Exec(ddl); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ddl); err != nil {
		t.Fatal(err)
	}
	if _, err = source.Exec("INSERT INTO `" + table + "` VALUES(1,0,'root','目录'),(2,1,'users','用户'),(3,2,'add','新增')"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO `" + table + "` VALUES(100,0,'root','目录'),(900,100,'keep','保留'),(5000,0,'outside','查询外记录')"); err != nil {
		t.Fatal(err)
	}
	task := treeTestTask()
	task.ID = 0
	task.Revision = 0
	task.Service = "orders"
	task.Owner = "alice"
	task.SourceSQL = "SELECT id,parent_id,code,name FROM `" + table + "` WHERE code != 'outside'"
	task.TargetSQL = task.SourceSQL
	if err = st.SaveDataSyncTask(task); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.DataSyncTask(task.ID)
	if err != nil || loaded.Tree == nil || loaded.Tree.ParentField != "parent_id" {
		t.Fatal("lost tree configuration", err)
	}
	comp, err := st.CompareDataSync(ctx, task, DataSyncScope{SourceEnvironment: "test", TargetEnvironment: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	if len(comp.Differences) != 2 || comp.Unchanged != 1 || comp.TargetOnly != 1 {
		t.Fatal("wrong tree comparison")
	}
	selected := []DataSyncSelection{}
	for _, d := range comp.Differences {
		n, _ := new(big.Int).SetString(*d.After["id"], 10)
		if n.Cmp(big.NewInt(5000)) <= 0 {
			t.Fatal("allocation ignored IDs outside WHERE")
		}
		selected = append(selected, DataSyncSelection{Key: d.Key, After: d.After})
	}
	script, err := syncSelectionSQL(comp, selected)
	if err != nil {
		t.Fatal(err)
	}
	change := &Change{Kind: "mysql", Title: "菜单迁移", Service: "orders", Owner: "alice", RiskLevel: "medium", SQL: &SQLChange{ResourceAlias: "db", Content: script, MaxAffectedRows: 2, DataSync: &DataSyncSnapshot{TaskID: task.ID, TaskRevision: task.Revision, SourceEnvironment: "test", TargetEnvironment: "prod", TargetSQL: task.TargetSQL, Keys: task.Keys, Tree: task.Tree, TreeBaseline: comp.TreeBaseline, Differences: comp.Differences, TargetDigest: comp.TargetDigest, TargetDestination: comp.TargetDestination, ComparisonDigest: comp.Digest}}}
	if err = st.CreateChange(change); err != nil {
		t.Fatal(err)
	}
	if _, err = st.SetChangeReady(change.ID, true); err != nil {
		t.Fatal(err)
	}
	run := sqlRunFixture(t, st, m, change, "prod")
	if run.Status != PromotionPendingApproval {
		t.Fatal("missing approval")
	}
	if _, err = st.ReviewDeliveryRun(run.ID, "reviewer", run.Digest, true); err != nil {
		t.Fatal(err)
	}
	// An unselected parent changed after approval; full tree baseline must block.
	if _, err = db.Exec("UPDATE `" + table + "` SET name='并发修改' WHERE id=100"); err != nil {
		t.Fatal(err)
	}
	plan := run.Plan.SQLPlans[0]
	if _, err = st.executeSQLNode(ctx, run, plan); err == nil {
		t.Fatal("unselected parent drift was ignored")
	}
	var count int
	db.QueryRow("SELECT count(*) FROM `" + table + "`").Scan(&count)
	if count != 3 {
		t.Fatal("drift attempt wrote rows")
	}
	// Create a fresh immutable comparison/change after drift; a failed execution
	// ledger is never silently reset or reused.
	comp, err = st.CompareDataSync(ctx, task, DataSyncScope{SourceEnvironment: "test", TargetEnvironment: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	selected = nil
	for _, d := range comp.Differences {
		selected = append(selected, DataSyncSelection{Key: d.Key, After: d.After})
	}
	script, err = syncSelectionSQL(comp, selected)
	if err != nil {
		t.Fatal(err)
	}
	change.ID = 0
	change.Status = ""
	change.SQL.Content = script
	change.SQL.MaxAffectedRows = len(selected)
	change.SQL.DataSync.Differences = comp.Differences
	change.SQL.DataSync.TargetDigest = comp.TargetDigest
	change.SQL.DataSync.ComparisonDigest = comp.Digest
	change.SQL.DataSync.TreeBaseline = comp.TreeBaseline
	if err = st.CreateChange(change); err != nil {
		t.Fatal(err)
	}
	st.SetChangeReady(change.ID, true)
	run = sqlRunFixture(t, st, m, change, "prod")
	if _, err = st.ReviewDeliveryRun(run.ID, "reviewer", run.Digest, true); err != nil {
		t.Fatal(err)
	}
	done, err := st.AdvanceDeliveryRun(run.ID, "alice", false, m)
	if err != nil || done.Steps[0].Status != "success" {
		t.Fatalf("tree execution failed: %v %+v", err, done)
	}
	var userID, parentID string
	if err = db.QueryRow("SELECT id,parent_id FROM `"+table+"` WHERE code='users'").Scan(&userID, &parentID); err != nil || parentID != "100" {
		t.Fatal("parent mapping lost", err)
	}
	if err = db.QueryRow("SELECT parent_id FROM `" + table + "` WHERE code='add'").Scan(&parentID); err != nil || parentID != userID {
		t.Fatal("child mapping lost", err)
	}
	db.QueryRow("SELECT count(*) FROM `" + table + "` WHERE id IN (900,5000)").Scan(&count)
	if count != 2 {
		t.Fatal("target-only data deleted")
	}
	if id, _ := strconv.ParseInt(userID, 10, 64); id <= 5000 {
		t.Fatal("unsafe allocated ID")
	}
}
