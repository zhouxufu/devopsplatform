package deploy

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeSAEProvider struct {
	mu     sync.Mutex
	inputs []saeDeployInput
	state  saeChangeState
}

func (f *fakeSAEProvider) Test(context.Context) error { return nil }
func (f *fakeSAEProvider) Deploy(_ context.Context, in saeDeployInput) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inputs = append(f.inputs, in)
	return "change-123", nil
}
func (f *fakeSAEProvider) Change(context.Context, string) (saeChangeState, error) {
	return f.state, nil
}

func TestSAEEnvironmentIsManagedAndEnvironmentIsolated(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, env := range []Environment{{Name: "test", DisplayName: "测试"}, {Name: "dev", DisplayName: "开发"}} {
		if err := st.UpsertEnvironment(env); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.UpsertService(Service{Name: "orders", ArtifactType: "jar", BuildTool: "command", BuildCmd: "true"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertConnection(ResourceConnection{Name: "sae-main", Kind: TargetAliyunSAE, Username: "ak", Password: "secret", Config: map[string]string{"region": "cn-hangzhou", "oss_bucket": "release-bucket", "security_token": "sts-secret"}}); err != nil {
		t.Fatal(err)
	}
	target := ServiceEnvironment{Service: "orders", Environment: "test", TargetType: TargetAliyunSAE, DeployConfig: map[string]string{"connection": "sae-main", "app_id": "app-test", "namespace_id": "cn-hangzhou:test"}}
	if err := st.UpsertServiceEnvironment(target); err != nil {
		t.Fatal(err)
	}
	stored := st.GetServiceEnvironment("orders", "test")
	if stored == nil || stored.HostGroup != "" || len(stored.Hosts) != 0 || stored.DeployConfig["region"] != "cn-hangzhou" {
		t.Fatalf("unexpected managed target: %#v", stored)
	}
	if got := stored.ExecutionTargets(); len(got) != 1 || got[0] != "sae:app-test" {
		t.Fatalf("execution targets = %#v", got)
	}
	target.Environment = "dev"
	if err := st.UpsertServiceEnvironment(target); err == nil || !strings.Contains(err.Error(), "不同环境") {
		t.Fatalf("same SAE app was allowed across environments: %v", err)
	}
	public := st.Connections()[0]
	if public.Config["security_token"] != "" || public.Username != "" || public.Password != "" {
		t.Fatalf("secret leaked through list API: %#v", public)
	}
	private := st.GetConnection("sae-main")
	if private.Config["security_token"] != "sts-secret" {
		t.Fatal("encrypted STS token was not restored")
	}
}

func TestMergeSAEEnvironmentUsesApplicationValues(t *testing.T) {
	merged, err := mergeSAEEnvironment(`[{"name":"EXISTING","value":"old"},{"name":"KEEP","value":"yes"}]`, map[string]string{"EXISTING": "new", "ADDED": "value"})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"name":"EXISTING","value":"new"`, `"name":"KEEP","value":"yes"`, `"name":"ADDED","value":"value"`} {
		if !strings.Contains(merged, expected) {
			t.Fatalf("merged SAE environment %q does not contain %q", merged, expected)
		}
	}
}

func TestSAEDriverDeploysJarAndImmutableImage(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpsertConnection(ResourceConnection{Name: "sae-main", Kind: TargetAliyunSAE, Username: "ak", Password: "secret", Config: map[string]string{"region": "cn-hangzhou", "oss_bucket": "release-bucket"}}); err != nil {
		t.Fatal(err)
	}
	fake := &fakeSAEProvider{state: saeChangeState{Status: 2}}
	d := NewDeployer(st, NewLogHub(), "")
	d.saeFactory = func(*ResourceConnection) (saeProvider, error) { return fake, nil }
	d.saePollInterval = time.Millisecond
	sv := &Service{Name: "orders", ArtifactType: "jar", TargetType: TargetAliyunSAE, DeployConfig: map[string]string{"connection": "sae-main", "app_id": "app-test", "namespace_id": "cn-hangzhou:test", "region": "cn-hangzhou"}}
	if err := st.InitializeEnvironments([]Environment{{Name: "test", DisplayName: "测试"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertService(*sv); err != nil {
		t.Fatal(err)
	}
	jarPath := t.TempDir() + "/orders.jar"
	if err := os.WriteFile(jarPath, []byte("jar"), 0o600); err != nil {
		t.Fatal(err)
	}
	jar, err := describeArtifact("orders", "v1", ArtifactJar, jarPath, "")
	if err != nil {
		t.Fatal(err)
	}
	id, err := d.StartArtifactRelease(sv, Host{Name: "sae:app-test", Addr: "SAE / cn-hangzhou"}, jar, "test", "", "alice")
	if err != nil {
		t.Fatal(err)
	}
	waitDeploy(t, st, id)
	if got := st.GetDeploy(id); got.ExternalID != "change-123" {
		t.Fatalf("external change order id = %q", got.ExternalID)
	}
	image := Artifact{Service: "orders", Version: "v2", Kind: ArtifactOCI, URI: "registry.cn-hangzhou.aliyuncs.com/acme/orders@sha256:abcdef", Digest: "sha256:abcdef"}
	id, err = d.StartArtifactRelease(sv, Host{Name: "sae:app-test", Addr: "SAE / cn-hangzhou"}, image, "test", "", "alice")
	if err != nil {
		t.Fatal(err)
	}
	waitDeploy(t, st, id)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.inputs) != 2 || fake.inputs[0].Artifact.Kind != ArtifactJar || fake.inputs[1].Artifact.URI != image.URI {
		t.Fatalf("provider inputs = %#v", fake.inputs)
	}
}

func TestSAEReleaseOrderUsesLogicalTargetSnapshot(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpsertEnvironment(Environment{Name: "test", DisplayName: "测试"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertService(Service{Name: "billing", ArtifactType: "jar", BuildTool: "command", BuildCmd: "true"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertConnection(ResourceConnection{Name: "sae-main", Kind: TargetAliyunSAE, Username: "ak", Password: "secret", Config: map[string]string{"region": "cn-hangzhou", "oss_bucket": "release-bucket"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertServiceEnvironment(ServiceEnvironment{Service: "billing", Environment: "test", TargetType: TargetAliyunSAE, DeployConfig: map[string]string{"connection": "sae-main", "app_id": "billing-test", "namespace_id": "cn-hangzhou:test"}}); err != nil {
		t.Fatal(err)
	}
	jarPath := t.TempDir() + "/billing.jar"
	if err := os.WriteFile(jarPath, []byte("jar"), 0o600); err != nil {
		t.Fatal(err)
	}
	artifact, err := describeArtifact("billing", "v1", ArtifactJar, jarPath, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RegisterArtifact(&artifact); err != nil {
		t.Fatal(err)
	}
	fake := &fakeSAEProvider{state: saeChangeState{Status: 2}}
	d := NewDeployer(st, NewLogHub(), "")
	d.saeFactory = func(*ResourceConnection) (saeProvider, error) { return fake, nil }
	d.saePollInterval = time.Millisecond
	manager := NewReleaseManager(st, d)
	order, err := manager.CreateArtifact("billing", artifact.ID, "test", "smoke", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if order.Total != 1 || len(order.Hosts) != 1 || order.Hosts[0] != "sae:billing-test" || !order.Target.ManagedTarget() {
		t.Fatalf("release snapshot = %#v", order)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got := st.GetReleaseOrder(order.ID)
		if got != nil && got.Status == "success" {
			if len(got.DeployIDs) != 1 || st.GetDeploy(got.DeployIDs[0]).ExternalID != "change-123" {
				t.Fatalf("managed deploy not attached: %#v", got)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("release did not finish: %#v", st.GetReleaseOrder(order.ID))
}

func waitDeploy(t *testing.T, st *Store, id int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := st.GetDeploy(id); got != nil && got.Status != "running" {
			if got.Status != "success" {
				t.Fatalf("deploy status = %s", got.Status)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("deploy did not finish")
}
