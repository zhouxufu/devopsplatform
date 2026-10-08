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
	if err := st.UpsertServiceEnvironment(ServiceEnvironment{Service: "orders", Environment: "test", TargetType: "ssh-jar", HostGroup: "orders-test", RemoteDir: "/opt/orders"}); err != nil {
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
}
