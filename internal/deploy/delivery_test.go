package deploy

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestResourceSecretsAreEncryptedAndApplied(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpsertConnection(ResourceConnection{Name: "aliyun-maven", Kind: "maven", Endpoint: "https://packages.example.test", Username: "builder", Password: "top-secret", Config: map[string]string{"settings_path": "/tmp/settings.xml"}}); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := st.db.QueryRow(`SELECT secret FROM resource_connections WHERE name='aliyun-maven'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "top-secret") || !strings.HasPrefix(stored, "enc:v1:") {
		t.Fatalf("secret not encrypted: %q", stored)
	}
	list := st.Connections()
	if len(list) != 1 || list[0].Password != "" || !list[0].SecretConfigured {
		t.Fatalf("public connection leaked or lost state: %#v", list)
	}
	if err := st.UpsertService(Service{Name: "api", GitURL: "https://git.example.test/team/api.git", Workdir: t.TempDir(), ArtifactType: "jar", BuildTool: "maven", MavenExecutable: "mvn", MavenGoals: "package", ArtifactGlob: "target/*.jar"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertServiceDelivery(ServiceDelivery{Service: "api", Runner: "local", MavenConnection: "aliyun-maven"}); err != nil {
		t.Fatal(err)
	}
	prepared, err := st.PrepareServiceResources(st.GetService("api"))
	if err != nil {
		t.Fatal(err)
	}
	if prepared.RuntimeEnv["MAVEN_PASSWORD"] != "top-secret" || prepared.MavenSettingsPath != "/tmp/settings.xml" {
		t.Fatalf("resource not applied: %#v", prepared)
	}
	if err := st.UpsertConnection(ResourceConnection{Name: "generated-maven", Kind: "maven", Endpoint: "https://packages.example.test/repository/public", Username: "builder", Password: "top-secret", Config: map[string]string{"repository_id": "private"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertConnection(ResourceConnection{Name: "private-git", Kind: "git", Endpoint: "https://git.example.test", Username: "git-user", Password: "git-token"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertServiceDelivery(ServiceDelivery{Service: "api", Runner: "local", GitConnection: "private-git", MavenConnection: "generated-maven"}); err != nil {
		t.Fatal(err)
	}
	prepared, err = st.PrepareServiceResources(st.GetService("api"))
	if err != nil {
		t.Fatal(err)
	}
	settings, err := os.ReadFile(prepared.MavenSettingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(settings), "packages.example.test/repository/public") || !strings.Contains(string(settings), "${env.MAVEN_PASSWORD}") || strings.Contains(string(settings), "top-secret") {
		t.Fatalf("generated Maven settings is unsafe or incomplete: %s", settings)
	}
	if prepared.RuntimeEnv["GIT_PASSWORD"] != "git-token" {
		t.Fatalf("git credential was not injected into the build: %#v", prepared.RuntimeEnv)
	}
}

func TestProtectedReleaseCreatesApprovalOrder(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	artifactPath := dir + "/app.jar"
	if err := os.WriteFile(artifactPath, []byte("jar"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertService(Service{Name: "api", Workdir: dir, ArtifactType: "jar", BuildTool: "command", BuildCmd: "true", ArtifactGlob: "*.jar"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEnvironment(Environment{Name: "prod", DisplayName: "生产", Protected: true, RequireReason: true, SortOrder: 40, Approvers: "alice,bob"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertHost(Host{Name: "prod-1", Environment: "prod", Addr: "127.0.0.1", User: "root"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertHostGroup(HostGroup{Name: "api-prod", Environment: "prod", Hosts: []string{"prod-1"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertServiceEnvironment(ServiceEnvironment{Service: "api", Environment: "prod", TargetType: "ssh-jar", RemoteDir: "/opt/api", StartCmd: "java -jar /opt/api/app.jar", HostGroup: "api-prod", Strategy: "rolling", MaxParallel: 1, BatchSize: 1, RuntimeEnv: map[string]string{"SPRING_PROFILES_ACTIVE": "prod", "DB_PASSWORD": "release-secret"}}); err != nil {
		t.Fatal(err)
	}
	var encrypted string
	if err := st.db.QueryRow(`SELECT runtime_env_encrypted FROM service_environments WHERE service='api' AND environment='prod'`).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encrypted, "enc:v1:") || strings.Contains(encrypted, "release-secret") {
		t.Fatalf("application environment was not encrypted: %q", encrypted)
	}
	publicTarget := st.GetServiceEnvironment("api", "prod")
	if publicTarget.RuntimeEnv != nil || strings.Join(publicTarget.RuntimeEnvKeys, ",") != "DB_PASSWORD,SPRING_PROFILES_ACTIVE" {
		t.Fatalf("application environment values leaked or keys missing: %#v", publicTarget)
	}
	b := &Build{Service: "api", Version: "v1", Actor: "test", StartedAt: time.Now().Unix()}
	if err := st.CreateBuild(b); err != nil {
		t.Fatal(err)
	}
	st.FinishBuild(b.ID, "success", artifactPath, "", 0, "")
	rm := NewReleaseManager(st, NewDeployer(st, NewLogHub(), ""))
	order, err := rm.Create("api", "v1", "prod", "release test", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if order.Status != "pending_approval" || order.Total != 1 {
		t.Fatalf("unexpected order: %#v", order)
	}
	if err := rm.Approve(order.ID, "alice"); err == nil || !strings.Contains(err.Error(), "不能审批自己") {
		t.Fatalf("creator self approval was not rejected: %v", err)
	}
	if order.Target.HostGroup != "api-prod" || order.Target.StartCmd != "java -jar /opt/api/app.jar" {
		t.Fatalf("release did not snapshot the application environment: %#v", order.Target)
	}
	changed := *st.GetServiceEnvironment("api", "prod")
	changed.StartCmd = "java -jar /opt/api/new-command.jar"
	changed.RuntimeEnv = map[string]string{"SPRING_PROFILES_ACTIVE": "prod", "DB_PASSWORD": "changed-after-approval"}
	if err := st.UpsertServiceEnvironment(changed); err != nil {
		t.Fatal(err)
	}
	if got := st.GetReleaseOrder(order.ID); got.Target.StartCmd != "java -jar /opt/api/app.jar" {
		t.Fatalf("release snapshot drifted after environment edit: %#v", got.Target)
	} else if snap, err := st.decryptRuntimeEnvironment(got.runtimeEnvEncrypted); err != nil || snap["DB_PASSWORD"] != "release-secret" {
		t.Fatalf("release environment snapshot drifted or failed to decrypt: %#v, %v", snap, err)
	}
	if err := st.UpsertHost(Host{Name: "prod-1", Environment: "prod", Addr: "127.0.0.2", User: "root"}); err == nil {
		t.Fatal("host changed while an approval release was active")
	}
	if len(st.Deploys("api", 10)) != 0 {
		t.Fatal("protected release touched hosts before approval")
	}
}

func TestBackfillLegacyDeploysAsOneReleaseOrder(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, host := range []string{"test-1", "test-2"} {
		if _, err := st.db.Exec(`INSERT INTO deploys(service,version,host,environment,status,actor,started_at,ended_at) VALUES('web','v7',?,'test','success','alice',100,110)`, host); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.backfillReleaseOrders(); err != nil {
		t.Fatal(err)
	}
	orders := st.ReleaseOrders("web", 10)
	if len(orders) != 1 || orders[0].Total != 2 || orders[0].Succeeded != 2 || len(orders[0].DeployIDs) != 2 {
		t.Fatalf("legacy deploys were not grouped: %#v", orders)
	}
	if err := st.backfillReleaseOrders(); err != nil {
		t.Fatal(err)
	}
	if got := len(st.ReleaseOrders("web", 10)); got != 1 {
		t.Fatalf("backfill is not idempotent: got %d release orders", got)
	}
}

func TestRunnerCapacityIsEnforced(t *testing.T) {
	pr := &PipelineRunner{activeByRunner: map[string]int{}, secretValuesByRun: map[int64][]string{}}
	release, ok := pr.claimRunner("local", 1)
	if !ok {
		t.Fatal("first runner lease was rejected")
	}
	if _, ok := pr.claimRunner("local", 1); ok {
		t.Fatal("runner accepted work above its concurrency limit")
	}
	release()
	if releaseAgain, ok := pr.claimRunner("local", 1); !ok {
		t.Fatal("runner capacity was not released")
	} else {
		releaseAgain()
	}
}

func TestPipelineLogSecretRedaction(t *testing.T) {
	pr := &PipelineRunner{activeByRunner: map[string]int{}, secretValuesByRun: map[int64][]string{}}
	pr.registerRunSecrets(42, []string{"short", "longer-secret", "short"})
	got := pr.redactRunLog(42, "token=longer-secret value=short")
	if strings.Contains(got, "longer-secret") || strings.Contains(got, "short") || got != "token=****** value=******" {
		t.Fatalf("secret was not redacted: %q", got)
	}
	pr.registerRunSecrets(42, nil)
	if got := pr.redactRunLog(42, "short"); got != "short" {
		t.Fatalf("secret registry was not released: %q", got)
	}
}

func TestLegacyDeployStepsAreMigratedOnce(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	service := Service{Name: "legacy", Workdir: t.TempDir(), ArtifactType: "jar", BuildTool: "command", BuildCmd: "true", ArtifactGlob: "*.jar", Steps: []StepInst{{Step: "java-build"}, {Step: "package-jar"}, {Step: "ssh-jar-deploy"}}}
	if err := st.UpsertService(service); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`DELETE FROM deploy_meta WHERE key='build_only_pipeline_v1'`); err != nil {
		t.Fatal(err)
	}
	if err := st.migrateBuildOnlyPipelines(); err != nil {
		t.Fatal(err)
	}
	got := st.GetService("legacy")
	if got == nil || len(got.Steps) != 2 || got.Steps[0].Step != "java-build" || got.Steps[1].Step != "package-jar" {
		t.Fatalf("legacy deploy step was not removed: %#v", got)
	}
}

func TestHostGroupsAreEnvironmentIsolated(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, env := range []Environment{{Name: "dev", DisplayName: "开发"}, {Name: "test", DisplayName: "测试"}, {Name: "prod", DisplayName: "生产"}} {
		if err := st.UpsertEnvironment(env); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.UpsertHost(Host{Name: "dev-1", Environment: "dev", Addr: "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertHostGroup(HostGroup{Name: "prod-pool", Environment: "prod", Hosts: []string{"dev-1"}}); err == nil {
		t.Fatal("cross-environment host group was accepted")
	}
	if err := st.UpsertHostGroup(HostGroup{Name: "dev-pool", Environment: "dev", Hosts: []string{"dev-1"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertService(Service{Name: "api", ArtifactType: "jar"}); err != nil {
		t.Fatal(err)
	}
	base := ServiceEnvironment{Service: "api", Environment: "test", TargetType: "ssh-jar", RemoteDir: "/opt/api", StartCmd: "true", Strategy: "rolling"}
	base.HostGroup = "dev-pool"
	if err := st.UpsertServiceEnvironment(base); err == nil {
		t.Fatal("application environment accepted a host group from another environment")
	}
	base.HostGroup, base.Hosts = "", []string{"dev-1"}
	if err := st.UpsertServiceEnvironment(base); err == nil {
		t.Fatal("application environment accepted a direct host from another environment")
	}
}
