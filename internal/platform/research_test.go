package platform

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
)

func TestResearchAliasIsolationAndTrustedDirectory(t *testing.T) {
	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	if _, err = registry.SaveUser(User{Username: "reviewer", DisplayName: "审批人", Role: RoleMember, Enabled: true}, "qa-password"); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-User") != "local" || r.Header.Get("X-Role") != RoleAdmin {
			t.Error("spoofed platform actor")
		}
		var enabled []string
		if json.Unmarshal([]byte(r.Header.Get("X-Reviewer-Directory")), &enabled) != nil || len(enabled) != 1 || enabled[0] != "reviewer" {
			t.Error("untrusted reviewer directory", enabled)
		}
		if r.Header.Get("X-Research-Identities") == "forged" {
			t.Error("spoofed external identities")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	g := NewGateway("")
	g.SetPlugins([]*Manifest{{Name: "research", Backend: "delivery-core", UIPath: "/ui/research", AdminAddr: u.Host}})
	handler := g.Routes(registry, NewBus())
	for _, p := range []string{"/api/plugins/research/api/research/tasks", "/api/plugins/research/api/deploy", "/api/plugins/research/api/connections"} {
		r := httptest.NewRequest("GET", p, nil)
		r.Header.Set("X-User", "forged")
		r.Header.Set("X-Role", "admin")
		r.Header.Set("X-Reviewer-Directory", `["forged"]`)
		r.Header.Set("X-Research-Identities", "forged")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		want := 404
		if p == "/api/plugins/research/api/research/tasks" {
			want = 200
		}
		if w.Code != want {
			t.Fatalf("%s: %d %s", p, w.Code, w.Body.String())
		}
	}
}

func TestResearchVersionPlanningDoesNotOpenExecutionEndpoints(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		allowed      bool
	}{
		{"GET", "/api/release-versions/page", true},
		{"POST", "/api/release-versions/scope-preview", true},
		{"POST", "/api/release-notices/version/21/preview", true},
		{"GET", "/api/delivery/runs", true},
		{"POST", "/api/delivery/runs", false},
		{"POST", "/api/delivery/preview", false},
		{"POST", "/api/deploy", false},
		{"PUT", "/api/connections/mysql", false},
	} {
		if got := researchPluginPathAllowed(httptest.NewRequest(tc.method, tc.path, nil)); got != tc.allowed {
			t.Errorf("%s %s allowed=%v, want %v", tc.method, tc.path, got, tc.allowed)
		}
	}
}
