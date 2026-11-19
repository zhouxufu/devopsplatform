package platform

import (
	"encoding/json"
	"logplatform/internal/extensions"
	"logplatform/pkg/workflownode"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExtensionManagementRequiresRealAdminIdentity(t *testing.T) {
	m, err := extensions.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	g := NewGateway("enabled")
	g.SetExtensions(m)
	handler := testGatewayRoutes(t, g)
	cookie := sessionToken(User{Username: "member", Role: RoleMember, Enabled: true})
	for _, endpoint := range []struct{ method, path, body string }{{"POST", "/api/extensions", ""}, {"PUT", "/api/extensions/missing/config", `{}`}, {"POST", "/api/extensions/missing/enable", ""}, {"POST", "/api/extensions/missing/disable", ""}, {"POST", "/api/extensions/missing/test", ""}, {"DELETE", "/api/extensions/missing", ""}} {
		r := httptest.NewRequest(endpoint.method, endpoint.path, strings.NewReader(endpoint.body))
		r.AddCookie(&http.Cookie{Name: "lp_session", Value: cookie})
		r.Header.Set("X-Role", "admin")
		r.Header.Set("X-User", "admin")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("spoofed admin: %s %d", endpoint.path, w.Code)
		}
	}
	r := httptest.NewRequest("GET", "/api/extensions", nil)
	r.AddCookie(&http.Cookie{Name: "lp_session", Value: cookie})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatal("member discovery failed")
	}
	r = httptest.NewRequest("GET", "/api/extensions", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("anonymous discovery allowed")
	}
}

func TestWorkflowSharedProviderTracksInstalledWorkspace(t *testing.T) {
	m, e := extensions.New(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	g := NewGateway("")
	g.SetExtensions(m)
	h := testGatewayRoutes(t, g)
	read := func() []map[string]any {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/api/workflow-node-providers", nil))
		var v []map[string]any
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		return v
	}
	if len(read()) != 0 {
		t.Fatal("uninstalled provider visible")
	}
	g.SetPlugins([]*Manifest{{Name: "api-center", AdminAddr: "127.0.0.1:1"}})
	v := read()
	if len(v) != 1 || v[0]["id"] != workflownode.APITestProvider || v[0]["runtime"] != "shared" {
		t.Fatal(v)
	}
	g.SetPlugins(nil)
	if len(read()) != 0 {
		t.Fatal("removed provider visible")
	}
}
