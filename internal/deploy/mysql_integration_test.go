package deploy

import (
	"os"
	"strings"
	"testing"
	"time"
)

// TestMySQLStoreSmoke is opt-in because CI and local development do not need a
// database server. Point it at an empty disposable schema; the test exercises
// schema creation plus representative configuration and runtime queries.
func TestMySQLStoreSmoke(t *testing.T) {
	dsn := os.Getenv("DEVOPS_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set DEVOPS_TEST_MYSQL_DSN to an empty disposable MySQL 8 schema")
	}
	st, err := OpenDatabase(t.TempDir(), DatabaseConfig{Driver: "mysql", DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if st.Driver() != "mysql" {
		t.Fatalf("driver = %q", st.Driver())
	}
	if err := st.InitializeEnvironments([]Environment{{Name: "test", DisplayName: "测试"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertService(Service{Name: "orders", ArtifactType: "jar", BuildTool: "maven", MavenGoals: "package"}); err != nil {
		t.Fatal(err)
	}
	batchWork := &WorkItem{Title: "MySQL 批量交付", Owner: "local"}
	if err = st.CreateWorkItem(batchWork); err != nil {
		t.Fatal(err)
	}
	base, err := st.WorkDelivery(batchWork.ID)
	if err != nil {
		t.Fatal(err)
	}
	batch := WorkDeliverySave{Digest: base.Digest, Changes: []Change{{Kind: "code", Title: "批量代码", Service: "orders", Reference: "feature/batch"}, {Kind: "manual", Title: "批量确认"}}}
	if err = st.SaveWorkDelivery(batchWork.ID, &batch, "local", true); err != nil {
		t.Fatal(err)
	}
	saved, err := st.WorkDelivery(batchWork.ID)
	if err != nil || len(saved.Changes) != 2 {
		t.Fatalf("batch roundtrip failed: %v", err)
	}
	edit := saved.Changes[0]
	edit.Reference = "feature/updated"
	if err = st.SaveWorkDelivery(batchWork.ID, &WorkDeliverySave{Digest: saved.Digest, Changes: []Change{edit}}, "local", true); err != nil {
		t.Fatal(err)
	}
	if err = st.SaveWorkDelivery(batchWork.ID, &WorkDeliverySave{Digest: saved.Digest, Changes: []Change{{Kind: "manual", Title: "stale"}}}, "local", true); err == nil {
		t.Fatal("stale MySQL batch accepted")
	}
	if got := st.GetService("orders"); got == nil || !strings.HasPrefix(got.ApplicationID, "app_") {
		t.Fatalf("service was not persisted: %#v", got)
	}
	if err := st.UpsertHost(Host{Name: "test-a", Environment: "test", Addr: "127.0.0.1", User: "deploy", Password: "secret", HostKey: "SHA256:test"}); err != nil {
		t.Fatal(err)
	}
	if got := st.GetHost("test-a"); got == nil || got.Password != "secret" {
		t.Fatalf("host secret round-trip failed: %#v", got)
	}
	if err := st.UpsertHostGroup(HostGroup{Name: "orders-test", Environment: "test", Hosts: []string{"test-a"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertServiceEnvironment(ServiceEnvironment{Service: "orders", Environment: "test", TargetType: "ssh-jar", HostGroup: "orders-test", RemoteDir: "/opt/orders", StartCmd: "bash /opt/orders/current/service.sh start"}); err != nil {
		t.Fatal(err)
	}
	if target := st.GetServiceEnvironment("orders", "test"); target == nil || len(target.Hosts) != 1 {
		t.Fatalf("deployment target was not persisted: %#v", target)
	}
	if err := st.UpsertConnection(ResourceConnection{Name: "git-main", Kind: "git", Endpoint: "https://git.example.test", Username: "builder", Password: "token"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertServiceDelivery(ServiceDelivery{Service: "orders", Runner: "local", GitConnection: "git-main"}); err != nil {
		t.Fatal(err)
	}
	run := &PipelineRun{Service: "orders", Trigger: "manual", Branch: "main", StartedAt: time.Now().Unix()}
	if err := st.CreateRun(run); err != nil {
		t.Fatal(err)
	}
	st.AppendRunLog(run.ID, "mysql smoke")
	st.SetRunStatus(run.ID, "waiting_deploy")
	if !st.CancelWaitingRun(run.ID) {
		t.Fatal("could not cancel waiting pipeline")
	}
	if got := st.GetRun(run.ID); got == nil || got.Trigger != "manual" || got.Status != "canceled" || !strings.Contains(st.RunLog(run.ID), "mysql smoke") {
		t.Fatalf("pipeline round-trip failed: %#v", got)
	}
	change := &Change{Kind: "manual", Title: "MySQL 中文交付验证", Owner: "local"}
	if err := st.CreateChange(change); err != nil {
		t.Fatal(err)
	}
	work := &WorkItem{Title: "MySQL 版本规划需求", Owner: "local"}
	if err := st.CreateWorkItem(work); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChangeWorkItems(change.ID, []int64{work.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetChangeReady(change.ID, true); err != nil {
		t.Fatal(err)
	}
	version := &ReleaseVersion{Version: "mysql-smoke", Title: "MySQL 编排版本", Owner: "local"}
	if err := st.SaveReleaseDraft(version, VersionScope{}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetReleaseVersionReady(version.ID); err == nil {
		t.Fatal("empty MySQL draft frozen")
	}
	if err := st.SaveReleaseDraft(version, VersionScope{WorkItemIDs: []int64{work.ID}}); err != nil {
		t.Fatal(err)
	}
	if got := st.GetReleaseVersion(version.ID); len(got.WorkItems) != 1 || len(got.Changes) != 1 || len(got.ManualChangeIDs) != 0 {
		t.Fatalf("MySQL scope not persisted: %#v", got)
	}
	if _, err := st.SetReleaseVersionReady(version.ID); err != nil {
		t.Fatal(err)
	}
	tpl := &DeliveryTemplate{Name: "MySQL 编排", Mode: "version", Owner: "local", Nodes: []DeliveryNode{{ID: "check", Kind: "manual", Name: "人工验收", Instructions: "确认数据库验证通过"}}}
	if err := st.SaveDeliveryTemplate(tpl); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveDeliveryTemplate(tpl); err != nil {
		t.Fatal(err)
	} // idempotent update
	manager := NewReleaseManager(st, nil)
	delivery, err := st.CreateDeliveryRun(DeliveryRequest{TemplateID: tpl.ID, VersionID: version.ID, Environment: "test"}, "local", manager)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.AdvanceDeliveryRun(delivery.ID, "local", true, manager); err != nil {
		t.Fatal(err)
	}
	if completed, err := st.AdvanceDeliveryRun(delivery.ID, "local", false, manager); err != nil || completed.Status != "success" {
		t.Fatalf("delivery completion: %#v %v", completed, err)
	}
	if len(st.ReleaseVersions("", 100)) != 1 || len(st.DeliveryRuns()) != 1 || len(st.DeliveryTemplates()) != 1 {
		t.Fatal("MySQL delivery persistence is incomplete")
	}
	sqlChange := &Change{Kind: "mysql", Title: "MySQL SQL payload", Service: "orders", SQL: &SQLChange{ResourceAlias: "db", Content: "CREATE TABLE menus (id int NOT NULL, PRIMARY KEY (id))", MaxAffectedRows: 100}}
	if err := st.CreateChange(sqlChange); err != nil {
		t.Fatal(err)
	}
	if got := st.GetChange(sqlChange.ID); got == nil || got.SQL == nil || got.SQL.Content != sqlChange.SQL.Content {
		t.Fatalf("MySQL SQL payload persistence failed: %+v", got)
	}
	sqlChange.SQL.Content = "ALTER TABLE menus ADD COLUMN enabled int NOT NULL DEFAULT 1"
	if err := st.UpdateChange(sqlChange); err != nil {
		t.Fatal(err)
	}
	if got := st.GetChange(sqlChange.ID); got.SQL == nil || got.SQL.Content != sqlChange.SQL.Content {
		t.Fatal("MySQL payload-only edit was lost")
	}
	if _, err := st.db.Exec(`INSERT INTO issue_sync_state(connection,last_at,status,detail) VALUES('qa',1,'success','中文同步')`); err != nil {
		t.Fatal(err)
	}
	if states := st.issueSyncStates(); len(states) != 1 || states[0].Detail != "中文同步" {
		t.Fatalf("MySQL sync state persistence failed: %+v", states)
	}
	if _, err := st.db.Exec(`INSERT INTO sql_execution_ledger(connection,database_name,change_id,digest,status,run_id,node_id,detail) VALUES('qa','qa',?,'sha256:qa','success',?,'sql','中文执行')`, sqlChange.ID, delivery.ID); err != nil {
		t.Fatal(err)
	}
	if ledger := st.SQLLedger(); len(ledger) != 1 || ledger[0]["status"] != "success" {
		t.Fatalf("MySQL SQL ledger persistence failed: %+v", ledger)
	}
}
