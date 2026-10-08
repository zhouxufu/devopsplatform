package deploy

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestStorePreventsConcurrentOperationsAndRecovers(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()

	build := &Build{Service: "orders", Version: "v1", Actor: "alice", StartedAt: now}
	if err := st.CreateBuild(build); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateBuild(&Build{Service: "orders", Version: "v2", StartedAt: now}); !errors.Is(err, ErrRunConflict) {
		t.Fatalf("duplicate build error = %v, want ErrRunConflict", err)
	}
	builds := st.Builds("orders", 10)
	if len(builds) != 1 || builds[0].Status != "running" || builds[0].Actor != "alice" {
		t.Fatalf("running build not returned correctly: %#v", builds)
	}

	dp := &Deploy{Service: "orders", Version: "v1", Host: "prod-a", Actor: "bob", StartedAt: now}
	if err := st.CreateDeploy(dp); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDeploy(&Deploy{Service: "orders", Version: "v2", Host: "prod-a", StartedAt: now}); !errors.Is(err, ErrRunConflict) {
		t.Fatalf("duplicate deploy error = %v, want ErrRunConflict", err)
	}
	if err := st.CreateDeploy(&Deploy{Service: "orders", Version: "v1", Host: "prod-b", StartedAt: now}); err != nil {
		t.Fatalf("different host should be allowed: %v", err)
	}

	run := &PipelineRun{Service: "orders", Actor: "carol", AutoDeploy: true, DeployEnvironment: "test", StartedAt: now}
	if err := st.CreateRun(run); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateRun(&PipelineRun{Service: "orders", StartedAt: now}); !errors.Is(err, ErrRunConflict) {
		t.Fatalf("duplicate pipeline error = %v, want ErrRunConflict", err)
	}
	waiting := &PipelineRun{Service: "billing", Actor: "dave", StartedAt: now}
	if err := st.CreateRun(waiting); err != nil {
		t.Fatal(err)
	}
	st.SetRunStatus(waiting.ID, "waiting_deploy")
	if err := st.CreateRun(&PipelineRun{Service: "billing", StartedAt: now}); !errors.Is(err, ErrRunConflict) {
		t.Fatalf("waiting pipeline should retain service lock: %v", err)
	}

	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if got := st.Builds("orders", 10)[0].Status; got != "interrupted" {
		t.Fatalf("recovered build status = %q, want interrupted", got)
	}
	for _, deploy := range st.Deploys("orders", 10) {
		if deploy.Status != "interrupted" {
			t.Fatalf("recovered deploy status = %q, want interrupted", deploy.Status)
		}
	}
	if got := st.GetRun(run.ID); got.Status != "interrupted" || !got.AutoDeploy || got.DeployEnvironment != "test" {
		t.Fatalf("recovered pipeline = %#v, want interrupted one-click deployment to test", got)
	}
	if got := st.GetRun(waiting.ID).Status; got != "waiting_deploy" {
		t.Fatalf("waiting pipeline status = %q, want waiting_deploy", got)
	}
}

func TestClaimRunDeployIsAtomic(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	run := &PipelineRun{Service: "orders", StartedAt: time.Now().Unix()}
	if err := st.CreateRun(run); err != nil {
		t.Fatal(err)
	}
	st.SetRunStatus(run.ID, "waiting_deploy")
	if !st.ClaimRunDeploy(run.ID, "prod-a") {
		t.Fatal("first deploy claim should succeed")
	}
	if st.ClaimRunDeploy(run.ID, "prod-b") {
		t.Fatal("second deploy claim should fail")
	}
	got := st.GetRun(run.ID)
	if got.Status != "running" || got.DeployHost != "prod-a" {
		t.Fatalf("claimed run = %#v", got)
	}
}

func TestCancelWaitingRunReleasesService(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	run := &PipelineRun{Service: "orders", StartedAt: time.Now().Unix(), Stages: []Stage{{Name: "release", Status: "waiting"}}}
	if err := st.CreateRun(run); err != nil {
		t.Fatal(err)
	}
	st.SetRunStatus(run.ID, "waiting_deploy")
	if !st.CancelWaitingRun(run.ID) {
		t.Fatal("cancel should succeed")
	}
	got := st.GetRun(run.ID)
	if got.Status != "canceled" || got.Stages[0].Status != "skipped" {
		t.Fatalf("canceled run = %#v", got)
	}
	if err := st.CreateRun(&PipelineRun{Service: "orders", StartedAt: time.Now().Unix()}); err != nil {
		t.Fatalf("service lock was not released: %v", err)
	}
}

func TestWaitingDeployStage(t *testing.T) {
	stages := []Stage{{Name: "build", Status: "success"}, {Name: "release", Status: "waiting"}}
	if got := waitingDeployStage(stages); got != 1 {
		t.Fatalf("waitingDeployStage = %d, want 1", got)
	}
}

func TestEnvironmentConfigurationAndCredentialProtection(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpsertEnvironment(Environment{Name: "secure", DisplayName: "安全", Protected: true}); err == nil {
		t.Fatal("protected environment without approvers was accepted")
	}
	if setup := st.EnvironmentSetup(); setup.Completed || len(setup.Environments) != 0 {
		t.Fatalf("fresh store must wait for environment setup: %#v", setup)
	}
	if err := st.InitializeEnvironments([]Environment{{Name: "prod", DisplayName: "生产", Protected: true, RequireReason: true, Approvers: "alice"}}); err != nil {
		t.Fatal(err)
	}
	if env := st.GetEnvironment("prod"); env == nil || !env.Protected || !env.RequireReason {
		t.Fatalf("prod environment protection missing: %#v", env)
	}
	if err := st.InitializeEnvironments([]Environment{{Name: "test", DisplayName: "测试"}}); err == nil {
		t.Fatal("completed environment setup was allowed to run twice")
	}
	if err := st.UpsertService(Service{Name: "orders", ArtifactType: "jar"}); err != nil {
		t.Fatal(err)
	}
	orders := st.GetService("orders")
	if orders == nil || !strings.HasPrefix(orders.ApplicationID, "app_") || st.GetServiceByApplicationID(orders.ApplicationID) == nil {
		t.Fatalf("stable application id missing: %#v", orders)
	}
	if err := st.UpsertHost(Host{Name: "prod-a", Environment: "prod", Addr: "10.0.0.10", User: "deploy", Password: "secret", HostKey: "SHA256:test"}); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := st.db.QueryRow(`SELECT password FROM hosts WHERE name='prod-a'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw == "secret" || !strings.HasPrefix(raw, "enc:v1:") {
		t.Fatalf("password is not encrypted at rest: %q", raw)
	}
	if got := st.GetHost("prod-a"); got == nil || got.Password != "secret" {
		t.Fatalf("password cannot be decrypted: %#v", got)
	}
	public := st.PublicHosts()
	if len(public) != 1 || public[0].Password != "" || !public[0].CredentialConfigured {
		t.Fatalf("public host leaks or loses credential state: %#v", public)
	}
	if err := st.UpsertHost(Host{Name: "prod-a", Environment: "prod", Addr: "10.0.0.11", User: "deploy", HostKey: "SHA256:test"}); err != nil {
		t.Fatal(err)
	}
	if got := st.GetHost("prod-a"); got.Password != "secret" || got.Addr != "10.0.0.11" {
		t.Fatalf("blank credential edit should preserve password: %#v", got)
	}

	if err := st.UpsertHostGroup(HostGroup{Name: "orders-prod", Environment: "prod", Hosts: []string{"prod-a"}}); err != nil {
		t.Fatal(err)
	}
	target := ServiceEnvironment{Service: "orders", Environment: "prod", TargetType: "ssh-jar", RemoteDir: "/opt/orders", StartCmd: "systemctl start orders", Strategy: "rolling", MaxParallel: 2, HostGroup: "orders-prod"}
	if err := st.UpsertServiceEnvironment(target); err != nil {
		t.Fatal(err)
	}
	got := st.GetServiceEnvironment("orders", "prod")
	if got == nil || len(got.Hosts) != 1 || got.Hosts[0] != "prod-a" || !got.Protected {
		t.Fatalf("service environment was not saved: %#v", got)
	}
	if env := st.ProtectedEnvironmentForHost("orders", "prod-a"); env == nil || env.Name != "prod" {
		t.Fatalf("protected host lookup failed: %#v", env)
	}
	if err := st.DeleteHost("prod-a"); err == nil {
		t.Fatal("host referenced by environment should not be deleted")
	}
}

func TestHostNameSupportsChineseAndReportsEnvironmentSeparately(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpsertEnvironment(Environment{Name: "test", DisplayName: "测试环境"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertHost(Host{Name: "住房宝测试", Environment: "test", Addr: "192.168.0.2", User: "root"}); err != nil {
		t.Fatalf("Chinese host name should be accepted: %v", err)
	}
	if got := st.GetHost("住房宝测试"); got == nil || got.Addr != "192.168.0.2" {
		t.Fatalf("saved host mismatch: %#v", got)
	}
	if err := st.UpsertHost(Host{Name: "bad/name", Environment: "test", Addr: "192.168.0.3"}); err == nil || !strings.Contains(err.Error(), "主机名称格式错误") {
		t.Fatalf("expected a precise host-name error, got %v", err)
	}
	if err := st.UpsertHost(Host{Name: "valid-host", Environment: "missing", Addr: "192.168.0.3"}); err == nil || !strings.Contains(err.Error(), "所属环境") {
		t.Fatalf("expected a precise environment error, got %v", err)
	}
}

func TestEnvironmentSetupIsAtomicAndEnvironmentsAreUserManaged(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.InitializeEnvironments([]Environment{{Name: "qa", DisplayName: "质量验证"}, {Name: "online", DisplayName: "线上", RequireReason: true}}); err != nil {
		t.Fatal(err)
	}
	if setup := st.EnvironmentSetup(); !setup.Completed || len(setup.Environments) != 2 || st.GetEnvironment("default") != nil {
		t.Fatalf("unexpected setup result: %#v", setup)
	}
	if err := st.DeleteEnvironment("qa"); err != nil {
		t.Fatalf("unused template environment should be deletable: %v", err)
	}
	if err := st.DeleteEnvironment("online"); err == nil || !strings.Contains(err.Error(), "至少保留") {
		t.Fatalf("last environment deletion should fail: %v", err)
	}
}

func TestClaimRunEnvironmentIsAtomic(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	run := &PipelineRun{Service: "orders", StartedAt: time.Now().Unix()}
	if err := st.CreateRun(run); err != nil {
		t.Fatal(err)
	}
	st.SetRunStatus(run.ID, "waiting_deploy")
	if !st.ClaimRunEnvironment(run.ID, "test") || st.ClaimRunEnvironment(run.ID, "prod") {
		t.Fatal("environment deploy claim must succeed exactly once")
	}
	got := st.GetRun(run.ID)
	if got.Status != "running" || got.DeployEnvironment != "test" {
		t.Fatalf("claimed environment run = %#v", got)
	}
}

func TestServiceTagsAreNormalizedAndPersisted(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpsertService(Service{Name: "orders", ArtifactType: "jar", Tags: []string{" 核心 ", "Java", "核心", ""}}); err != nil {
		t.Fatal(err)
	}
	got := st.GetService("orders")
	if got == nil || len(got.Tags) != 2 || got.Tags[0] != "核心" || got.Tags[1] != "Java" {
		t.Fatalf("tags = %#v", got)
	}
}

func TestArtifactMetadata(t *testing.T) {
	path := t.TempDir() + "/artifact.jar"
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, size, err := artifactMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if digest != "sha256:2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" || size != 5 {
		t.Fatalf("metadata = %q %d", digest, size)
	}
}
