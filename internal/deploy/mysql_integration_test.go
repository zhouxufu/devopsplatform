package deploy

import (
	"encoding/json"
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
	if err := st.UpsertConnection(ResourceConnection{Name: "resource-db", Kind: "mysql", Endpoint: "127.0.0.1:1", Username: "qa", Config: map[string]string{"tls": "false"}}); err != nil {
		t.Fatal(err)
	}
	group := &ResourceGroup{Name: "mysql-group", Requirements: []ApplicationResourceRequirement{{Alias: "db", Kind: "mysql"}}, Bindings: []ApplicationEnvironmentResource{{Alias: "db", Environment: "test", Connection: "resource-db", Config: map[string]string{"database": "orders"}}}}
	if err := st.SaveResourceGroup(group); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveApplicationResources("orders", ApplicationResources{ResourceGroup: group.Name, GroupRevision: group.Revision}); err != nil {
		t.Fatal(err)
	}
	if c, err := st.sqlBinding("orders", "test", "db"); err != nil || c.Name != "resource-db" {
		t.Fatal("MySQL resource group binding lost", err)
	}
	group.Description = "CAS update"
	if err := st.SaveResourceGroup(group); err != nil {
		t.Fatal(err)
	}
	if len(st.ResourceGroups()) != 1 || st.ResourceGroup(group.Name).Revision != 2 {
		t.Fatal("MySQL resource group persistence missing")
	}
	if err := st.DeleteResourceGroup(group.Name); err == nil {
		t.Fatal("MySQL referenced group deleted")
	}
	prep := &CodePreparation{ID: "mysql-prep", Owner: "local", State: "preview"}
	if err := st.saveCodePreparation(prep); err != nil {
		t.Fatal(err)
	}
	prep.Message = "CAS update"
	if err := st.saveCodePreparation(prep); err != nil {
		t.Fatal("MySQL preparation CAS failed", err)
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
	// Runtime-variable patches lock and merge the current encrypted map inside
	// the same transaction as the environment configuration (including MySQL).
	target := *st.GetServiceEnvironment("orders", "test")
	target.RuntimeEnv = map[string]string{"ORIGINAL": "private-original"}
	if err := st.UpsertServiceEnvironment(target); err != nil {
		t.Fatal(err)
	}
	target.RuntimeEnv = nil
	value := "private-new"
	target.RuntimeEnvPatch = map[string]*string{"ADDED": &value}
	if err := st.UpsertServiceEnvironment(target); err != nil {
		t.Fatal(err)
	}
	values, err := st.decryptRuntimeEnvironment(st.GetServiceEnvironment("orders", "test").runtimeEnvEncrypted)
	if err != nil || values["ORIGINAL"] != "private-original" || values["ADDED"] != value {
		t.Fatal("MySQL runtime patch did not preserve saved keys")
	}
	for _, status := range []string{"success", "failed", "canceled", "timed_out"} {
		d := &Deploy{Service: "orders", Host: "test-a", Environment: "test", StartedAt: time.Now().Unix()}
		if err := st.CreateDeploy(d); err != nil {
			t.Fatal(err)
		}
		st.AppendDeployLog(d.ID, "MySQL terminal update regression")
		if err := st.FinishDeploy(d.ID, status); err != nil {
			t.Fatalf("MySQL deploy terminal update: %v", err)
		}
		if got := st.GetDeploy(d.ID); got.Status != status || got.EndedAt == 0 {
			t.Fatalf("MySQL instance still active: %+v", got)
		}
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
	resources := st.ApplicationResources("orders")
	resources.ResourceGroup = ""
	resources.GroupRevision = 0
	if err := st.SaveApplicationResources("orders", resources); err != nil {
		t.Fatal(err)
	}
	history, _ := json.Marshal(DeliveryPlan{ResourceTargets: []DeliveryResourceTarget{{GroupName: group.Name}}})
	if _, err := st.db.Exec(`UPDATE delivery_runs SET plan=? WHERE id=?`, string(history), delivery.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteResourceGroup(group.Name); err == nil || !strings.Contains(err.Error(), "交付记录") {
		t.Fatal("MySQL group history deletion guard failed", err)
	}
}

func TestMySQLStandardDeliveryPolicyAndPartialScope(t *testing.T) {
	dsn := os.Getenv("DEVOPS_TEST_STANDARD_MYSQL_DSN")
	if dsn == "" {
		t.Skip("requires empty disposable MySQL schema")
	}
	st, e := OpenDatabase(t.TempDir(), DatabaseConfig{Driver: "mysql", DSN: dsn})
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	if e = st.InitializeEnvironments([]Environment{{Name: "test", DisplayName: "QA", Approvers: "reviewer"}}); e != nil {
		t.Fatal(e)
	}
	w := &WorkItem{Kind: "bug", Title: "Partial work", Owner: "alice"}
	if e = st.CreateWorkItem(w); e != nil {
		t.Fatal(e)
	}
	ids := []int64{}
	for _, title := range []string{"First", "Later"} {
		c := &Change{Kind: "manual", Title: title, Reference: "Verify " + title, Owner: "alice"}
		st.CreateChange(c)
		if err := st.SetChangeWorkItems(c.ID, []int64{w.ID}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.SetChangeReady(c.ID, true); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, c.ID)
	}
	m := NewReleaseManager(st, nil)
	req := DeliveryRequest{StrategyKind: "standard", SourceType: "work_items", WorkItemIDs: []int64{w.ID}, SelectedChangeIDs: ids[:1], Environment: "test"}
	req = standardSign(t, st, m, req)
	run, e := st.CreateDeliveryRun(req, "alice", m)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = st.AdvanceDeliveryRun(run.ID, "alice", true, m); e != nil {
		t.Fatal(e)
	}
	if _, e = st.AdvanceDeliveryRun(run.ID, "alice", false, m); e != nil {
		t.Fatal(e)
	}
	page, e := st.WorkCandidates(WorkCandidateFilter{Environment: "test", IDs: []int64{w.ID}})
	if e != nil || len(page.Items) != 1 || !page.Items[0].PartialDelivery || page.Items[0].SuccessfulDelivery {
		t.Fatal("partial scope falsely marked fully delivered", e, page)
	}
	policy := DeliveryPolicy{Stages: []DeliveryNode{{Name: "Review", Approvers: []string{"reviewer"}, ApprovalMode: "any"}}}
	if e = st.saveDeliveryPolicy("test", policy); e != nil {
		t.Fatal(e)
	}
	if e = st.saveDeliveryPolicy("test", policy); e == nil {
		t.Fatal("stale policy revision accepted")
	}
	current, e := st.deliveryPolicy("test")
	if e != nil || current.Revision != 1 {
		t.Fatal(e, current)
	}
	if e = st.saveDeliveryPolicy("test", current); e != nil {
		t.Fatal("MySQL policy CAS failed", e)
	}
	req.PreflightToken = ""
	req.SelectedChangeIDs = ids
	d, e := st.DraftDelivery(req)
	if e != nil || len(d.Nodes) != 3 {
		t.Fatal("mandatory policy not inherited", e, d)
	}
}
