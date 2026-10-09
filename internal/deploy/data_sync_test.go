package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestDataSyncQueryAndDiff(t *testing.T) {
	for _, query := range []string{"DELETE FROM menu WHERE id=1", "SELECT * FROM menu", "SELECT id, sleep(1) FROM menu", "SELECT id FROM menu JOIN other ON menu.id=other.id", "SELECT id FROM menu FOR UPDATE", "SELECT id FROM menu LIMIT 1", "SELECT id AS other FROM menu", "SELECT id FROM menu WHERE id IN (SELECT id FROM other)", "SELECT @secret FROM menu", "SELECT id FROM menu; DROP TABLE menu"} {
		if _, err := parseSyncQuery(query); err == nil {
			t.Fatalf("unsafe query accepted: %s", query)
		}
	}
	task := &DataSyncTask{ID: 1, Revision: 1, Name: "菜单", Service: "api", ResourceAlias: "db", SourceSQL: "SELECT id,name FROM menu WHERE id > 0", TargetSQL: "SELECT id,name FROM menu WHERE id > 0", Keys: []string{"id"}}
	if err := normalizeSyncTask(task); err != nil {
		t.Fatal(err)
	}
	ptr := func(s string) *string { return &s }
	source := []SyncRow{{"id": ptr("1"), "name": ptr("新菜单")}, {"id": ptr("2"), "name": nil}}
	target := []SyncRow{{"id": ptr("1"), "name": ptr("旧菜单")}, {"id": ptr("3"), "name": ptr("目标独有")}}
	scope := DataSyncScope{SourceEnvironment: "dev", TargetEnvironment: "test"}
	comp, err := compareSyncRows(task, scope, source, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(comp.Differences) != 2 || comp.TargetOnly != 1 {
		t.Fatal("wrong differences")
	}
	reordered, err := compareSyncRows(task, scope, []SyncRow{source[1], source[0]}, target)
	if err != nil || reordered.Digest != comp.Digest {
		t.Fatal("digest depended on query row order")
	}
	for _, changed := range []DataSyncScope{{SourceEnvironment: "test", TargetEnvironment: "prod"}, {SourceEnvironment: "other", TargetEnvironment: "test"}} {
		other, err := compareSyncRows(task, changed, source, target)
		if err != nil || other.Digest == comp.Digest || other.Task.Revision != task.Revision {
			t.Fatal("comparison scope not bound to digest, or reusable task changed")
		}
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
	if _, err = compareSyncRows(task, scope, []SyncRow{source[0], source[0]}, target); err == nil {
		t.Fatal("duplicate keys accepted")
	}
}

func TestDataSyncTaskIndependentOfEnvironments(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err = st.UpsertService(Service{Name: "api", ArtifactType: "jar", BuildTool: "command", BuildCmd: "true", ArtifactGlob: "*.jar"}); err != nil {
		t.Fatal(err)
	}
	if err = st.SaveApplicationResources("api", ApplicationResources{Requirements: []ApplicationResourceRequirement{{Alias: "db", Kind: "mysql"}, {Alias: "config", Kind: "nacos"}}}); err != nil {
		t.Fatal(err)
	}
	task := &DataSyncTask{Name: "菜单", Service: "api", ResourceAlias: "db", SourceSQL: "SELECT id,name FROM menu WHERE id>0", TargetSQL: "SELECT id,name FROM menu WHERE id>0", Keys: []string{"id"}, Owner: "local"}
	if err = st.SaveDataSyncTask(task); err != nil {
		t.Fatalf("Task without any environment/binding cannot be saved: %v", err)
	}
	old := *task
	task.Name = "菜单更新"
	if err = st.SaveDataSyncTask(task); err != nil {
		t.Fatal(err)
	}
	if err = st.SaveDataSyncTask(&old); err == nil {
		t.Fatal("stale revision update accepted")
	}
	for _, alias := range []string{"missing", "config"} {
		bad := *task
		bad.ID = 0
		bad.ResourceAlias = alias
		if err = st.SaveDataSyncTask(&bad); err == nil {
			t.Fatalf("Invalid MySQL declaration accepted: %s", alias)
		}
	}
	// Legacy task documents are read as environment-free definitions, without
	// silently reusing their old source or modifying existing frozen snapshots.
	raw, _ := json.Marshal(task)
	var document map[string]any
	_ = json.Unmarshal(raw, &document)
	document["source_environment"] = "deleted-legacy-environment"
	raw, _ = json.Marshal(document)
	if _, err = st.db.Exec(`UPDATE data_sync_tasks SET document=? WHERE id=?`, string(raw), task.ID); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.DataSyncTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := st.DataSyncTasks()
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{loaded, listed} {
		encoded, _ := json.Marshal(value)
		if bytes.Contains(encoded, []byte("source_environment")) {
			t.Fatal("Legacy environment leaked into task definition")
		}
	}
	if err = st.SaveDataSyncTask(loaded); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []DataSyncScope{{}, {SourceEnvironment: "test"}, {TargetEnvironment: "prod"}, {SourceEnvironment: "test", TargetEnvironment: "test"}} {
		if _, err = st.CompareDataSync(context.Background(), loaded, scope); err == nil {
			t.Fatal("Missing/equal comparison environments accepted")
		}
	}
	mux := http.NewServeMux()
	registerDataSyncRoutes(mux, st)
	for _, endpoint := range []string{"compare", "freeze"} {
		request := httptest.NewRequest("POST", "/api/data-sync/tasks/"+strconv.FormatInt(task.ID, 10)+"/"+endpoint, strings.NewReader(`{"environment":"prod"}`))
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != 409 || !strings.Contains(response.Body.String(), "请选择本次来源和目标环境") {
			t.Fatalf("Implicit legacy source accepted: %d %s", response.Code, response.Body.String())
		}
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
	task := &DataSyncTask{Name: "菜单同步", Service: "orders", ResourceAlias: "db", SourceSQL: "SELECT id,name FROM `" + table + "` WHERE id>0", TargetSQL: "SELECT id,name FROM `" + table + "` WHERE id>0", Keys: []string{"id"}, Owner: "alice"}
	if err = st.SaveDataSyncTask(task); err != nil {
		t.Fatal(err)
	}
	comp, err := st.CompareDataSync(ctx, task, DataSyncScope{SourceEnvironment: "test", TargetEnvironment: "prod"})
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
	inverse, err := st.CompareDataSync(ctx, task, DataSyncScope{SourceEnvironment: "prod", TargetEnvironment: "test"})
	if err != nil || inverse.Digest == comp.Digest || inverse.Task.Revision != comp.Task.Revision {
		t.Fatalf("Task cannot be reused with different comparison scope: %v", err)
	}
	mux := http.NewServeMux()
	registerDataSyncRoutes(mux, st)
	freeze := func(source, target string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"source_environment": source, "target_environment": target, "digest": comp.Digest, "rows": selection})
		r := httptest.NewRequest("POST", "/api/data-sync/tasks/"+strconv.FormatInt(task.ID, 10)+"/freeze", bytes.NewReader(body))
		r.Header.Set("X-User", "alice")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	if response := freeze("prod", "test"); response.Code != 409 || !strings.Contains(response.Body.String(), "请重新对比") {
		t.Fatalf("Comparison digest reused with changed scope: %d %s", response.Code, response.Body.String())
	}
	response := freeze("test", "prod")
	if response.Code != 200 {
		t.Fatalf("Freeze failed: %d %s", response.Code, response.Body.String())
	}
	var c *Change
	if err = json.Unmarshal(response.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	if c.Status != ChangeDraft || c.SQL.DataSync.SourceEnvironment != "test" || c.SQL.DataSync.TargetEnvironment != "prod" || c.SQL.DataSync.TaskRevision != task.Revision {
		t.Fatal("Snapshot lost explicit comparison scope/revision")
	}
	targetDB.QueryRow("SELECT count(*) FROM `" + table + "`").Scan(&count)
	if count != 2 {
		t.Fatal("Freezing wrote business data")
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
