package deploy

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func validAgentDesiredConfig() AgentDesiredConfig {
	return AgentDesiredConfig{
		ServerURL: "http://logs.internal:9801/", MonitorURL: "https://monitor.internal:9802/",
		Collect: []AgentCollectRequest{{ApplicationID: "app_test", Service: "orders", Paths: []string{"/opt/orders/logs/*.log"}, Multiline: `^\d{4}-\d{2}-\d{2}`}},
	}
}

func TestNormalizeAgentDesiredConfig(t *testing.T) {
	req := validAgentDesiredConfig()
	if err := normalizeAgentDesiredConfig(&req); err != nil {
		t.Fatal(err)
	}
	if req.ServerURL != "http://logs.internal:9801" || req.RunUser != "logagent" || req.RunGroup != "logagent" || req.SpoolMaxMB != 1024 {
		t.Fatalf("defaults not applied: %+v", req)
	}

	bad := validAgentDesiredConfig()
	bad.Collect[0].Paths = []string{"relative/app.log"}
	if err := normalizeAgentDesiredConfig(&bad); err == nil || !strings.Contains(err.Error(), "绝对路径") {
		t.Fatalf("relative path accepted: %v", err)
	}
	bad = validAgentDesiredConfig()
	bad.ServerURL = "file:///tmp/socket"
	if err := normalizeAgentDesiredConfig(&bad); err == nil {
		t.Fatal("non-http URL accepted")
	}
}

func TestParseAgentPlatform(t *testing.T) {
	for raw, want := range map[string]string{
		"Linux\nx86_64\nsystemd\n":                                        "linux-amd64",
		"notice from shell\nLOGPLATFORM_PLATFORM Linux aarch64 systemd\n": "linux-arm64",
	} {
		got, err := parseAgentPlatform(raw)
		if err != nil || got != want {
			t.Fatalf("parse %q = %q, %v; want %q", raw, got, err, want)
		}
	}
	if _, err := parseAgentPlatform("Darwin\narm64\nno-systemd\n"); err == nil {
		t.Fatal("unsupported OS accepted")
	}
}

func TestAgentInstallCommandDoesNotContainToken(t *testing.T) {
	cmd := renderAgentInstallCommand("/tmp/bin", "/tmp/config", "/tmp/unit", "logagent", "logagent")
	if strings.Contains(cmd, "top-secret") {
		t.Fatal("token leaked into shell command")
	}
	for _, required := range []string{"systemctl enable logagent", "systemctl restart logagent", "install -m 0600", "systemctl is-active"} {
		if !strings.Contains(cmd, required) {
			t.Fatalf("install command missing %q", required)
		}
	}
}

func TestAgentConfigAPIAndInstallRequiresDesiredState(t *testing.T) {
	t.Setenv("DEVOPSPLATFORM_DATA_TOKEN", "top-secret")
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpsertEnvironment(Environment{Name: "test", DisplayName: "测试"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertHost(Host{Name: "node-a", Environment: "test", Addr: "192.0.2.1", User: "root"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertService(Service{Name: "orders", ArtifactType: "jar"}); err != nil {
		t.Fatal(err)
	}
	h := Routes(st, NewBuilder(st, dir, NewLogHub()), NewLogHub(), "", NewStepLib(dir+"/steps", dir+"/custom"))

	req := httptest.NewRequest(http.MethodPost, "/api/hosts/node-a/agent/install", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "采集配置") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	app := st.GetService("orders")
	body := `{"server_url":"http://logs.internal:9801","monitor_url":"http://monitor.internal:9802","collect":[{"application_id":"` + app.ApplicationID + `","paths":["/var/log/orders.log"]}]}`
	put := httptest.NewRequest(http.MethodPut, "/api/hosts/node-a/agent/config", bytes.NewBufferString(body))
	put.Header.Set("Content-Type", "application/json")
	saved := httptest.NewRecorder()
	h.ServeHTTP(saved, put)
	if saved.Code != http.StatusOK || !strings.Contains(saved.Body.String(), `"service":"orders"`) || !strings.Contains(saved.Body.String(), `"token_available":true`) {
		t.Fatalf("save config status=%d body=%s", saved.Code, saved.Body.String())
	}
	if strings.Contains(saved.Body.String(), "top-secret") {
		t.Fatalf("platform token leaked in response: %s", saved.Body.String())
	}

	get := httptest.NewRecorder()
	h.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/hosts/node-a/agent/config", nil))
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), app.ApplicationID) {
		t.Fatalf("get config status=%d body=%s", get.Code, get.Body.String())
	}
	if err := st.RebindApplicationID("orders", "app_catalog"); err != nil {
		t.Fatal(err)
	}
	if cfg := st.AgentDesiredConfig("node-a"); cfg == nil || cfg.Collect[0].ApplicationID != "app_catalog" {
		t.Fatalf("agent desired config was not rebound: %#v", cfg)
	}
}

func TestLegacyNullAgentCollectionLoadsAsEmptyArray(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpsertEnvironment(Environment{Name: "test", DisplayName: "测试"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertHost(Host{Name: "node-a", Environment: "test", Addr: "192.0.2.1", User: "root"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`INSERT INTO agent_configs(host_name,server_url,monitor_url,run_user,run_group,spool_max_mb,collect,updated_at,applied_at,last_apply_error) VALUES(?,?,?,?,?,?,?,?,0,'')`,
		"node-a", "http://logs.internal:9801", "", "logagent", "logagent", 1024, "null", 1); err != nil {
		t.Fatal(err)
	}
	cfg := st.AgentDesiredConfig("node-a")
	if cfg == nil || cfg.Collect == nil || len(cfg.Collect) != 0 {
		t.Fatalf("legacy null collection must load as []: %#v", cfg)
	}
}

func TestLogOnlyApplicationConfigSurvivesDeployApplicationDeletion(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpsertEnvironment(Environment{Name: "test", DisplayName: "测试"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertHost(Host{Name: "node-a", Environment: "test", Addr: "192.0.2.1", User: "root"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertService(Service{Name: "orders", ApplicationID: "app_orders", ArtifactType: "jar"}); err != nil {
		t.Fatal(err)
	}
	cfg := validAgentDesiredConfig()
	cfg.Collect[0].ApplicationID = "app_orders"
	if _, err := st.UpsertAgentDesiredConfig("node-a", cfg); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteService("orders"); err != nil {
		t.Fatalf("deployment configuration should be removable while logs keep the identity: %v", err)
	}
	got := st.AgentDesiredConfig("node-a")
	if got == nil || len(got.Collect) != 1 || got.Collect[0].ApplicationID != "app_orders" || got.Collect[0].Service != "orders" {
		t.Fatalf("log collection identity was lost: %#v", got)
	}
	standalone := validAgentDesiredConfig()
	standalone.Collect[0] = AgentCollectRequest{ApplicationID: "app_logs_only", Service: "logs-only", Paths: []string{"/var/log/logs-only.log"}}
	if _, err := st.UpsertAgentDesiredConfig("node-a", standalone); err != nil {
		t.Fatalf("log-only application should not require a deployment application: %v", err)
	}
}
