package platform

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplicationDisplayNameDoesNotRenameCatalogIdentity(t *testing.T) {
	r, err := OpenRegistry(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	app, err := r.Observe("旧中文服务", "logs", "")
	if err != nil {
		t.Fatal(err)
	}
	r.Observe(app.Name, "deploy", app.ApplicationID)
	if err := r.RecordDeployment(DeploymentState{Service: app.Name, Environment: "prod", Version: "v1", Hosts: []string{"original"}}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	r.HandleRoutes(mux)
	call := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w
	}
	path := "/api/registry/applications/" + app.ApplicationID + "/display-name"
	if !requiresAdmin(httptest.NewRequest("PUT", path, nil)) {
		t.Fatal("rename lost administrator boundary")
	}
	if w := call("PUT", path, `{"display_name":"智能搜索","expected_display_name":"旧中文服务"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("PUT", path, `{"display_name":"误覆盖","expected_display_name":"旧中文服务"}`); w.Code != 409 {
		t.Fatal(w.Code)
	}
	if w := call("PUT", path, `{"display_name":"误覆盖","expected_display_name":"智能搜索","identifier":"changed"}`); w.Code != 400 {
		t.Fatal(w.Code)
	}
	// Both telemetry observations and stale upserts preserve the edited label.
	r.Upsert(Service{Name: app.Name, ApplicationID: app.ApplicationID, DisplayName: "stale", Version: "v2"})
	r.Observe(app.Name, "otel", app.ApplicationID)
	items, _ := r.ListActive()
	if len(items) != 1 || items[0].DisplayName != "智能搜索" || items[0].Name != app.Name || items[0].ApplicationID != app.ApplicationID || items[0].Identifier != app.Identifier {
		t.Fatal(items)
	}
	if d := r.DeploymentStates(); len(d) != 1 || d[0].Service != app.Name || d[0].Version != "v1" {
		t.Fatal(d)
	}
	var n int
	r.db.QueryRow(`SELECT COUNT(*) FROM application_name_events WHERE application_id=?`, app.ApplicationID).Scan(&n)
	if n != 1 {
		t.Fatal("audit missing", n)
	}
	snapshot, err := r.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	copy, err := OpenRegistry(filepath.Join(t.TempDir(), "copy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close()
	if err := copy.Restore(snapshot); err != nil {
		t.Fatal(err)
	}
	cloned, _ := copy.List()
	if len(cloned) != 1 || cloned[0].DisplayName != "智能搜索" || cloned[0].ApplicationID != app.ApplicationID {
		t.Fatal(cloned)
	}
}

func TestApplicationDisplayNameInitialLabelNeverRevertsRename(t *testing.T) {
	r, err := OpenRegistry(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	mux := http.NewServeMux()
	r.HandleRoutes(mux)
	observe := func(label string) Service {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/registry/applications/observe", strings.NewReader(`{"name":"orders","source":"deploy","display_name":"`+label+`"}`)))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var v Service
		json.Unmarshal(w.Body.Bytes(), &v)
		return v
	}
	a := observe("订单服务")
	if a.DisplayName != "订单服务" {
		t.Fatal(a)
	}
	if err := r.setApplicationDisplayName(a.ApplicationID, "新订单中心", a.DisplayName, "admin"); err != nil {
		t.Fatal(err)
	}
	if current := observe("订单服务"); current.DisplayName != "新订单中心" {
		t.Fatal(current)
	}
}
