package deploy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestApplicationNameCatalogSessionAndFailureConsistency(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.UpsertService(Service{Name: "orders", DisplayName: "订单", ArtifactType: "jar"})
	id := st.GetService("orders").ApplicationID
	var label atomic.Value
	label.Store("订单")
	var unavailable atomic.Bool
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "lp_session=fixture" {
			http.Error(w, "session missing", 401)
			return
		}
		if unavailable.Load() {
			http.Error(w, "offline", 503)
			return
		}
		switch r.Method {
		case "PUT":
			if r.URL.Path != "/api/registry/applications/"+id+"/display-name" {
				t.Error("identity changed", r.URL.Path)
			}
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if len(body) != 2 || body["expected_display_name"] != label.Load().(string) {
				http.Error(w, "stale", 409)
				return
			}
			label.Store(body["display_name"])
			json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		case "GET":
			json.NewEncoder(w).Encode([]map[string]string{{"name": "orders", "display_name": label.Load().(string), "application_id": id}})
		default:
			json.NewEncoder(w).Encode(map[string]string{"application_id": id})
		}
	}))
	defer catalog.Close()
	hub := NewLogHub()
	h := Routes(st, NewBuilder(st, t.TempDir(), hub), hub, catalog.URL, NewStepLib("../../steps"))
	call := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("X-User", "admin")
		r.Header.Set("X-Role", "admin")
		r.Header.Set("Cookie", "lp_session=fixture")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := call("PUT", "/api/services/orders/display-name", `{"display_name":"订单中心","expected_display_name":"订单"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if label.Load().(string) != "订单中心" || st.GetService("orders").ApplicationID != id || st.GetService("orders").DisplayName != "订单中心" {
		t.Fatal("catalog/local identity drift")
	}
	unavailable.Store(true)
	if w := call("PUT", "/api/services/orders/display-name", `{"display_name":"失败改名","expected_display_name":"订单中心"}`); w.Code != 409 {
		t.Fatal(w.Code)
	}
	if st.GetService("orders").DisplayName != "订单中心" {
		t.Fatal("claimed success while catalog offline")
	}
	unavailable.Store(false)
	label.Store("从应用中心改名")
	w := call("GET", "/api/services", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "从应用中心改名") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestApplicationNameKeepsLegacyIdentityAndHistory(t *testing.T) {
	for _, key := range []string{"estate-ai-seek", "旧中文应用"} {
		t.Run(key, func(t *testing.T) {
			st, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			if err := st.UpsertService(Service{Name: key, GitURL: "https://example.invalid/repo.git", ArtifactType: "image", ImageName: "image:original", Steps: []StepInst{{Step: "docker-build", Params: map[string]string{"IMAGE_NAME": "image:original"}}}}); err != nil {
				t.Fatal(err)
			}
			before := st.GetService(key)
			id := before.ApplicationID
			identifier := before.Identifier
			if err := st.saveBuildSnapshot(91, before, &ServiceDelivery{Service: key}); err != nil {
				t.Fatal(err)
			}
			if _, err := st.db.Exec(`INSERT INTO builds(service,version,status,artifact) VALUES(?,?,?,?)`, key, "abc", "success", "original.jar"); err != nil {
				t.Fatal(err)
			}
			if _, err := st.db.Exec(`INSERT INTO service_environments(service,environment,target_type,deploy_config) VALUES(?,?,?,?)`, key, "test", "docker", `{"container_name":"old-container"}`); err != nil {
				t.Fatal(err)
			}
			var frozen string
			st.db.QueryRow(`SELECT config_cipher FROM build_run_snapshots WHERE run_id=91`).Scan(&frozen)
			if err := st.SetApplicationDisplayName(key, "智能搜索服务", key); err != nil {
				t.Fatal(err)
			}
			after := st.GetService(key)
			if after.Name != key || after.ApplicationID != id || after.Identifier != identifier || after.DisplayName != "智能搜索服务" || after.ImageName != before.ImageName {
				t.Fatalf("identity/config changed: %+v", after)
			}
			var history, frozenAfter, config string
			st.db.QueryRow(`SELECT service FROM builds`).Scan(&history)
			st.db.QueryRow(`SELECT config_cipher FROM build_run_snapshots WHERE run_id=91`).Scan(&frozenAfter)
			st.db.QueryRow(`SELECT deploy_config FROM service_environments WHERE service=?`, key).Scan(&config)
			if history != key || frozen != frozenAfter || config != `{"container_name":"old-container"}` {
				t.Fatal("history, frozen snapshot or runtime target changed")
			}
			// Old clients and stale build editors cannot undo a presentation rename.
			if err := st.UpsertService(*before); err != nil {
				t.Fatal(err)
			}
			if st.GetService(key).DisplayName != "智能搜索服务" {
				t.Fatal("configuration submission reverted name")
			}
			if err := st.SetApplicationDisplayName(key, "stale", key); err == nil {
				t.Fatal("stale rename accepted")
			}
		})
	}
}

func TestApplicationNameRoutesProtectIdentityPermissionAndRevision(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.UpsertService(Service{Name: "orders", DisplayName: "订单", ArtifactType: "jar"})
	dir := t.TempDir()
	hub := NewLogHub()
	h := Routes(st, NewBuilder(st, dir, hub), hub, "", NewStepLib("../../steps"))
	call := func(body, role string) int {
		r := httptest.NewRequest("PUT", "/api/services/orders/display-name", strings.NewReader(body))
		r.Header.Set("X-User", "tester")
		r.Header.Set("X-Role", role)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if got := call(`{"display_name":"订单中心","expected_display_name":"订单"}`, "member"); got != 403 {
		t.Fatal(got)
	}
	if got := call(`{"display_name":"订单中心","expected_display_name":"订单","name":"other"}`, "admin"); got != 400 {
		t.Fatal(got)
	}
	if got := call(`{"display_name":"订单中心","expected_display_name":"订单"}`, "admin"); got != 200 {
		t.Fatal(got)
	}
	if got := call(`{"display_name":"覆盖","expected_display_name":"订单"}`, "admin"); got != 409 {
		t.Fatal(got)
	}
	if got := call(`{"display_name":" ","expected_display_name":"订单中心"}`, "admin"); got != 400 {
		t.Fatal(got)
	}
	var audits int
	st.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='rename_display_name'`).Scan(&audits)
	if audits != 1 {
		t.Fatal("missing rename audit", audits)
	}
	if st.GetService("other") != nil || st.GetService("orders").DisplayName != "订单中心" {
		t.Fatal("renamed source key")
	}
}

func TestApplicationNameOnboardingAndOldColumnMigration(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	st.UpsertService(Service{Name: "历史中文", ArtifactType: "jar"})
	if _, err := st.db.Exec(`ALTER TABLE services DROP COLUMN display_name`); err != nil {
		t.Fatal(err)
	}
	st.Close()
	st, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if app := st.GetService("历史中文"); app == nil || app.DisplayName != "历史中文" || !strings.HasPrefix(app.Identifier, "legacy-") {
		t.Fatal(app)
	}
	hub := NewLogHub()
	h := Routes(st, NewBuilder(st, dir, hub), hub, "", NewStepLib("../../steps"))
	post := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/api/services", strings.NewReader(body)))
		return w
	}
	body := `{"name":"orders","display_name":"订单服务","create_only":true,"artifact_type":"static","git_url":"https://example.invalid/repo.git","build_tool":"command","build_cmd":"npm run build","artifact_glob":"dist"}`
	if w := post(body); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := post(body); w.Code != 409 {
		t.Fatal("duplicate create overwrote configuration", w.Code)
	}
	if w := post(strings.Replace(body, `"orders"`, `"中文标识"`, 1)); w.Code != 400 {
		t.Fatal(w.Code)
	}
	old := st.GetService("历史中文")
	raw, _ := json.Marshal(old)
	// Existing Unicode routes are retained, never rewritten to their display identifier.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("PUT", "/api/services/"+url.PathEscape(old.Name)+"/display-name", strings.NewReader(`{"display_name":"历史服务新名称","expected_display_name":"历史中文"}`)))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String(), string(raw))
	}
}
