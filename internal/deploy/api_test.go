package deploy

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestScanSSHHostKeyAPI(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for i := 0; i < 2; i++ {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			cfg := &ssh.ServerConfig{NoClientAuth: true}
			cfg.AddHostKey(signer)
			_, _, _, _ = ssh.NewServerConn(conn, cfg)
			conn.Close()
		}
	}()

	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h := Routes(st, NewBuilder(st, dir, NewLogHub()), NewLogHub(), "", NewStepLib(dir+"/steps", dir+"/custom"))
	body, _ := json.Marshal(map[string]string{"addr": listener.Addr().String()})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/hosts/scan-key", bytes.NewReader(body)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), ssh.FingerprintSHA256(signer.PublicKey())) || !strings.Contains(rec.Body.String(), `"known_hosts"`) {
		t.Fatalf("scan host key status=%d body=%s", rec.Code, rec.Body.String())
	}
	connectionScan := httptest.NewRecorder()
	connectionBody, _ := json.Marshal(map[string]string{"endpoint": listener.Addr().String()})
	h.ServeHTTP(connectionScan, httptest.NewRequest(http.MethodPost, "/api/connections/scan-key", bytes.NewReader(connectionBody)))
	if connectionScan.Code != http.StatusOK || !strings.Contains(connectionScan.Body.String(), ssh.FingerprintSHA256(signer.PublicKey())) || !strings.Contains(connectionScan.Body.String(), `"known_hosts"`) {
		t.Fatalf("scan Git host key status=%d body=%s", connectionScan.Code, connectionScan.Body.String())
	}

	bad := httptest.NewRecorder()
	h.ServeHTTP(bad, httptest.NewRequest(http.MethodPost, "/api/hosts/scan-key", strings.NewReader(`{"addr":"bad host"}`)))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid scan address status=%d body=%s", bad.Code, bad.Body.String())
	}
}

func TestEnvironmentSetupAPI(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h := Routes(st, NewBuilder(st, dir, NewLogHub()), NewLogHub(), "", NewStepLib(dir+"/steps", dir+"/custom"))

	get := httptest.NewRecorder()
	h.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/setup", nil))
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), `"completed":false`) {
		t.Fatalf("fresh setup status=%d body=%s", get.Code, get.Body.String())
	}
	body := `{"environments":[{"name":"qa","display_name":"质量验证"},{"name":"online","display_name":"线上","require_reason":true}]}`
	create := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/setup/environments", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(create, req)
	if create.Code != http.StatusOK || !strings.Contains(create.Body.String(), `"completed":true`) || strings.Contains(create.Body.String(), `"default"`) {
		t.Fatalf("setup status=%d body=%s", create.Code, create.Body.String())
	}

	repeat := httptest.NewRecorder()
	h.ServeHTTP(repeat, httptest.NewRequest(http.MethodPost, "/api/setup/environments", strings.NewReader(body)))
	if repeat.Code != http.StatusConflict {
		t.Fatalf("repeated setup status=%d body=%s", repeat.Code, repeat.Body.String())
	}
}

func TestApplicationEnvironmentAPIOnlyReturnsVariableKeys(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpsertService(Service{Name: "api", ArtifactType: "jar"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEnvironment(Environment{Name: "test", DisplayName: "测试"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertHost(Host{Name: "test-a", Environment: "test", Addr: "127.0.0.1", User: "deploy"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertHostGroup(HostGroup{Name: "api-test", Environment: "test", Hosts: []string{"test-a"}}); err != nil {
		t.Fatal(err)
	}
	h := Routes(st, NewBuilder(st, t.TempDir(), NewLogHub()), NewLogHub(), "", NewStepLib(t.TempDir()+"/steps", t.TempDir()+"/custom"))
	body := `{"target_type":"ssh-jar","remote_dir":"/opt/api","start_cmd":"java -jar /opt/api/app.jar","host_group":"api-test","runtime_env":{"SPRING_PROFILES_ACTIVE":"test","DB_PASSWORD":"api-secret"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/services/api/environments/test", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "api-secret") || strings.Contains(rec.Body.String(), "enc:v1:") || !strings.Contains(rec.Body.String(), `"runtime_env_keys":["DB_PASSWORD","SPRING_PROFILES_ACTIVE"]`) {
		t.Fatalf("environment response leaked values or omitted keys: status=%d body=%s", rec.Code, rec.Body.String())
	}
	get := httptest.NewRecorder()
	h.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/services/api/environments", nil))
	if get.Code != http.StatusOK || strings.Contains(get.Body.String(), "api-secret") || !strings.Contains(get.Body.String(), `"runtime_env_keys":["DB_PASSWORD","SPRING_PROFILES_ACTIVE"]`) {
		t.Fatalf("environment list leaked values or omitted keys: status=%d body=%s", get.Code, get.Body.String())
	}
}

func TestProtectedManagedJavaInstanceActionRequiresConfirmation(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.InitializeEnvironments([]Environment{{Name: "prod", DisplayName: "生产", Protected: true, RequireReason: true, Approvers: "reviewer"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertService(Service{Name: "api", ArtifactType: "jar", BuildCmd: "true"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertHost(Host{Name: "prod-a", Environment: "prod", Addr: "127.0.0.1:1", User: "deploy"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertHostGroup(HostGroup{Name: "api-prod", Environment: "prod", Hosts: []string{"prod-a"}}); err != nil {
		t.Fatal(err)
	}
	config := map[string]string{"java_mode": "managed", "java_version": "21", "java_port": "8080"}
	if err := st.UpsertServiceEnvironment(ServiceEnvironment{Service: "api", Environment: "prod", TargetType: "ssh-jar", RemoteDir: "/opt/api", HostGroup: "api-prod", DeployConfig: config}); err != nil {
		t.Fatal(err)
	}
	h := Routes(st, NewBuilder(st, dir, NewLogHub()), NewLogHub(), "", NewStepLib(dir+"/steps", dir+"/custom"))

	req := httptest.NewRequest(http.MethodPost, "/api/services/api/environments/prod/instances/prod-a/stop", strings.NewReader(`{"reason":"maintenance"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusPreconditionRequired || !strings.Contains(rec.Body.String(), "二次确认") {
		t.Fatalf("protected instance action status=%d body=%s", rec.Code, rec.Body.String())
	}

	unknown := httptest.NewRecorder()
	h.ServeHTTP(unknown, httptest.NewRequest(http.MethodPost, "/api/services/api/environments/prod/instances/not-in-group/stop", strings.NewReader(`{}`)))
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("unknown instance action status=%d body=%s", unknown.Code, unknown.Body.String())
	}
}

func TestReleaseEndpointSelectsExactArtifactID(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpsertService(Service{Name: "api", ArtifactType: "jar"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEnvironment(Environment{Name: "staging", DisplayName: "预发", Protected: true, RequireReason: true, SortOrder: 30, Approvers: "reviewer"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertHost(Host{Name: "staging-a", Environment: "staging", Addr: "127.0.0.1", User: "deploy"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertHostGroup(HostGroup{Name: "api-staging", Environment: "staging", Hosts: []string{"staging-a"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertServiceEnvironment(ServiceEnvironment{Service: "api", Environment: "staging", TargetType: "ssh-jar", RemoteDir: "/opt/api", StartCmd: "java -jar /opt/api/app.jar", HostGroup: "api-staging", MaxParallel: 1}); err != nil {
		t.Fatal(err)
	}
	var selected Artifact
	for i, content := range []string{"first", "second"} {
		path := fmt.Sprintf("%s/api-%d.jar", dir, i)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		a, err := describeArtifact("api", "same-version", ArtifactJar, path, "")
		if err != nil {
			t.Fatal(err)
		}
		a.Source, a.SourceID = "test", int64(i+1)
		if err := st.RegisterArtifact(&a); err != nil {
			t.Fatal(err)
		}
		selected = a
	}
	if _, err := NewReleaseManager(st, NewDeployer(st, NewLogHub(), "")).Create("api", "same-version", "staging", "ambiguous", "local"); err == nil || !strings.Contains(err.Error(), "artifact_id") {
		t.Fatalf("ambiguous version selector was not rejected: %v", err)
	}
	h := Routes(st, NewBuilder(st, dir, NewLogHub()), NewLogHub(), "", NewStepLib(dir+"/steps", dir+"/custom"))
	body := fmt.Sprintf(`{"service":"api","artifact_id":%d,"environment":"staging","reason":"verify exact artifact"}`, selected.ID)
	req := httptest.NewRequest(http.MethodPost, "/api/releases", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), fmt.Sprintf(`"artifact_id":%d`, selected.ID)) {
		t.Fatalf("release did not bind selected artifact: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestProtectedEnvironmentCannotBeBypassed(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpsertService(Service{Name: "orders", ArtifactType: "jar", TargetType: "ssh-jar"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEnvironment(Environment{Name: "prod", DisplayName: "生产", Protected: true, RequireReason: true, SortOrder: 40, Approvers: "reviewer"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertHost(Host{Name: "prod-a", Environment: "prod", Addr: "127.0.0.1", User: "deploy", Password: "secret", HostKey: "SHA256:test"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertHostGroup(HostGroup{Name: "orders-prod", Environment: "prod", Hosts: []string{"prod-a"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertServiceEnvironment(ServiceEnvironment{
		Service: "orders", Environment: "prod", TargetType: "ssh-jar", RemoteDir: "/opt/orders",
		StartCmd: "systemctl start orders", Strategy: "rolling", MaxParallel: 1, HostGroup: "orders-prod",
	}); err != nil {
		t.Fatal(err)
	}
	artifact := dir + "/orders.jar"
	if err := os.WriteFile(artifact, []byte("artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, size, err := artifactMetadata(artifact)
	if err != nil {
		t.Fatal(err)
	}
	build := &Build{Service: "orders", Version: "v1", StartedAt: time.Now().Unix()}
	if err := st.CreateBuild(build); err != nil {
		t.Fatal(err)
	}
	st.FinishBuild(build.ID, "success", artifact, digest, size, "ok")
	hub := NewLogHub()
	h := Routes(st, NewBuilder(st, dir, hub), hub, "", NewStepLib(dir+"/steps", dir+"/custom"))

	request := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/deploys", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := request(`{"service":"orders","version":"v1","environment":"prod"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing reason status = %d body=%s", rec.Code, rec.Body.String())
	}
	run := &PipelineRun{Service: "orders", StartedAt: time.Now().Unix()}
	if err := st.CreateRun(run); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE pipeline_runs SET status='waiting_deploy',artifact=?,artifact_id=? WHERE id=?`, artifact, st.GetBuildByVersion("orders", "v1").ArtifactID, run.ID); err != nil {
		t.Fatal(err)
	}
	directReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/pipeline/runs/%d/deploy", run.ID), bytes.NewBufferString(`{"environment":"prod","reason":"release v1","confirm_protected":true}`))
	directReq.Header.Set("Content-Type", "application/json")
	directRec := httptest.NewRecorder()
	h.ServeHTTP(directRec, directReq)
	if directRec.Code != http.StatusPreconditionRequired || !strings.Contains(directRec.Body.String(), "发布单审批") {
		t.Fatalf("pipeline bypassed protected release approval: status=%d body=%s", directRec.Code, directRec.Body.String())
	}
	st.CancelWaitingRun(run.ID)
	if rec := request(`{"service":"orders","version":"v1","environment":"prod","reason":"release v1"}`); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "pending_approval") {
		t.Fatalf("protected release did not create approval order: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := request(`{"service":"orders","version":"v1","environment":"prod","host":"prod-a","reason":"release v1"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("direct protected-host deploy status = %d body=%s", rec.Code, rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/hosts", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "enc:v1:") {
		t.Fatalf("host response leaks credential: status=%d body=%s", rec.Code, rec.Body.String())
	}
}
