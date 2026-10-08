package deploy

import (
	"context"
	"strings"
	"testing"
)

func TestDataSyncQueryAndDiff(t *testing.T) {
	for _, query := range []string{"DELETE FROM menu WHERE id=1", "SELECT * FROM menu", "SELECT id, sleep(1) FROM menu", "SELECT id FROM menu JOIN other ON menu.id=other.id", "SELECT id FROM menu FOR UPDATE", "SELECT id FROM menu LIMIT 1", "SELECT id AS other FROM menu", "SELECT id FROM menu WHERE id IN (SELECT id FROM other)", "SELECT @secret FROM menu", "SELECT id FROM menu; DROP TABLE menu"} {
		if _, err := parseSyncQuery(query); err == nil {
			t.Fatalf("unsafe query accepted: %s", query)
		}
	}
	task := &DataSyncTask{ID: 1, Revision: 1, Name: "菜单", Service: "api", ResourceAlias: "db", SourceEnvironment: "dev", SourceSQL: "SELECT id,name FROM menu WHERE id > 0", TargetSQL: "SELECT id,name FROM menu WHERE id > 0", Keys: []string{"id"}}
	if err := normalizeSyncTask(task); err != nil {
		t.Fatal(err)
	}
	ptr := func(s string) *string { return &s }
	source := []SyncRow{{"id": ptr("1"), "name": ptr("新菜单")}, {"id": ptr("2"), "name": nil}}
	target := []SyncRow{{"id": ptr("1"), "name": ptr("旧菜单")}, {"id": ptr("3"), "name": ptr("目标独有")}}
	comp, err := compareSyncRows(task, "test", source, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(comp.Differences) != 2 || comp.TargetOnly != 1 {
		t.Fatal("wrong differences")
	}
	reordered, err := compareSyncRows(task, "test", []SyncRow{source[1], source[0]}, target)
	if err != nil || reordered.Digest != comp.Digest {
		t.Fatal("digest depended on query row order")
	}
	selected := []DataSyncSelection{{Key: comp.Differences[0].Key, After: SyncRow{"id": ptr("1"), "name": ptr("编辑后的值'\n行")}}, {Key: comp.Differences[1].Key, After: source[1]}}
	script, err := syncSelectionSQL(comp, selected)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(script), "delete") || !strings.Contains(script, "null") {
		t.Fatal("unexpected delete or lost NULL")
	}
	selected[0].After["id"] = ptr("3")
	if _, err = syncSelectionSQL(comp, selected); err == nil {
		t.Fatal("matching key changed")
	}
	if _, err = compareSyncRows(task, "test", []SyncRow{source[0], source[0]}, target); err == nil {
		t.Fatal("duplicate keys accepted")
	}
}

func TestDataSyncMySQLCompareFreezeExecuteApprovalAndDrift(t *testing.T) {
	st, m, targetDB, table := sqlTargetFixture(t)
	if _, err := targetDB.Exec("CREATE DATABASE IF NOT EXISTS delivery_sync_source_qa"); err != nil {
		t.Fatal(err)
	}
	sourceConnection := *st.GetConnection("qa-mysql")
	sourceConnection.Name = "qa-sync-source"
	sourceConnection.Config = map[string]string{"database": "delivery_sync_source_qa", "tls": "false"}
	if err := st.UpsertConnection(sourceConnection); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveApplicationResources("orders", ApplicationResources{Requirements: []ApplicationResourceRequirement{{Alias: "db", Kind: "mysql"}}, Bindings: []ApplicationEnvironmentResource{{Alias: "db", Environment: "test", Connection: sourceConnection.Name}, {Alias: "db", Environment: "prod", Connection: "qa-mysql"}}}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	sourceDB, err := openChangeMySQL(ctx, &sourceConnection)
	if err != nil {
		t.Fatal(err)
	}
	defer sourceDB.Close()
	t.Cleanup(func() { sourceDB.Exec("DROP TABLE IF EXISTS `" + table + "`") })
	create := "CREATE TABLE `" + table + "` (id int NOT NULL PRIMARY KEY,name varchar(64) NULL) ENGINE=InnoDB"
	if _, err = sourceDB.Exec(create); err != nil {
		t.Fatal(err)
	}
	if _, err = targetDB.Exec(create); err != nil {
		t.Fatal(err)
	}
	sourceDB.Exec("INSERT INTO `" + table + "` VALUES(1,'新版'),(2,NULL)")
	targetDB.Exec("INSERT INTO `" + table + "` VALUES(1,'旧版'),(3,'保留')")
	task := &DataSyncTask{Name: "菜单同步", Service: "orders", ResourceAlias: "db", SourceEnvironment: "test", SourceSQL: "SELECT id,name FROM `" + table + "` WHERE id>0", TargetSQL: "SELECT id,name FROM `" + table + "` WHERE id>0", Keys: []string{"id"}, Owner: "alice"}
	if err = st.SaveDataSyncTask(task); err != nil {
		t.Fatal(err)
	}
	comp, err := st.CompareDataSync(ctx, task, "prod")
	if err != nil {
		t.Fatal(err)
	}
	if len(comp.Differences) != 2 || comp.TargetOnly != 1 {
		t.Fatalf("incorrect compare: %+v", comp)
	}
	var count int
	targetDB.QueryRow("SELECT count(*) FROM `" + table + "`").Scan(&count)
	if count != 2 {
		t.Fatal("comparison wrote database")
	}
	selection := []DataSyncSelection{}
	for _, d := range comp.Differences {
		selection = append(selection, DataSyncSelection{Key: d.Key, After: d.After})
	}
	script, err := syncSelectionSQL(comp, selection)
	if err != nil {
		t.Fatal(err)
	}
	c := sqlChangeFixture(t, st, script)
	c = st.GetChange(c.ID)
	c.SQL.DataSync = &DataSyncSnapshot{TaskID: task.ID, TaskRevision: task.Revision, TargetEnvironment: "prod", TargetSQL: task.TargetSQL, TargetDigest: comp.TargetDigest, TargetDestination: comp.TargetDestination, ComparisonDigest: comp.Digest, SourceEnvironment: "test"}
	c.SQL.DataSync.Keys = task.Keys
	c.SQL.DataSync.Differences = comp.Differences
	// Attach provenance before ready; updates otherwise reject ready changes.
	st.SetChangeReady(c.ID, false)
	if err = st.UpdateChange(c); err != nil {
		t.Fatal(err)
	}
	st.SetChangeReady(c.ID, true)
	if _, err = st.prepareSQLNode(ctx, "sql", []int64{c.ID}, "test"); err == nil {
		t.Fatal("environment-bound differences reused in source")
	}
	targetDB.Exec("UPDATE `" + table + "` SET name='并发编辑' WHERE id=1")
	if _, err = st.prepareSQLNode(ctx, "sql", []int64{c.ID}, "prod"); err == nil {
		t.Fatal("drift overwritten")
	}
	targetDB.Exec("UPDATE `" + table + "` SET name='旧版' WHERE id=1")
	run := sqlRunFixture(t, st, m, c, "prod")
	if run.Status != PromotionPendingApproval {
		t.Fatalf("production did not require approval: %s", run.Status)
	}
	if _, err = st.AdvanceDeliveryRun(run.ID, "alice", false, m); err == nil {
		t.Fatal("unapproved sync executed")
	}
	if _, err = st.ReviewDeliveryRun(run.ID, "reviewer", run.Digest, true); err != nil {
		t.Fatal(err)
	}
	got, err := st.AdvanceDeliveryRun(run.ID, "alice", false, m)
	if err != nil || got.Steps[0].Status != "success" {
		t.Fatalf("sync execution failed: %v %+v", err, got)
	}
	var name string
	targetDB.QueryRow("SELECT name FROM `" + table + "` WHERE id=1").Scan(&name)
	if name != "新版" {
		t.Fatal(name)
	}
	targetDB.QueryRow("SELECT count(*) FROM `" + table + "`").Scan(&count)
	if count != 3 {
		t.Fatal("target-only row lost")
	}
	if _, err = st.prepareSQLNode(ctx, "sql", []int64{c.ID}, "prod"); err == nil {
		t.Fatal("repeat execution allowed")
	}
	old := *task
	task.Name = "修改"
	if err = st.SaveDataSyncTask(task); err != nil {
		t.Fatal(err)
	}
	if err = st.SaveDataSyncTask(&old); err == nil {
		t.Fatal("stale task update allowed")
	}
}
