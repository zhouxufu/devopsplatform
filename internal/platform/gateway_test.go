package platform

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"
)

func testGatewayRoutes(t *testing.T, g *Gateway) http.Handler {
	t.Helper()
	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	return g.Routes(registry, NewBus())
}

func TestResourceCenterUsesStablePlatformRoute(t *testing.T) {
	var gotPath, gotUser string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotUser = r.Header.Get("X-User")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"name":"test"}]`))
	}))
	t.Cleanup(upstream.Close)
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}

	g := NewGateway("")
	g.SetPlugins([]*Manifest{{Name: "deploy", AdminAddr: target.Host}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://localhost/api/resources/environments", nil)
	sessionsMu.Lock()
	sessions["resource-test-session"] = sessionIdentity{Username: "admin", Role: RoleAdmin, Expires: time.Now().Add(time.Minute)}
	sessionsMu.Unlock()
	t.Cleanup(func() {
		sessionsMu.Lock()
		delete(sessions, "resource-test-session")
		sessionsMu.Unlock()
	})
	req.AddCookie(&http.Cookie{Name: "lp_session", Value: "resource-test-session"})
	testGatewayRoutes(t, g).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/api/environments" {
		t.Fatalf("upstream path = %q, want /api/environments", gotPath)
	}
	if gotUser == "" {
		t.Fatal("platform identity was not forwarded to resource service")
	}
}

func TestResourceCenterReportsUnavailableService(t *testing.T) {
	g := NewGateway("")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://localhost/api/resources/hosts", nil)
	testGatewayRoutes(t, g).ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}
