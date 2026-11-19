package platform

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestCrossOriginMutationIsRejectedEvenWithoutLogin(t *testing.T) {
	g := NewGateway("")
	h := g.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	req := httptest.NewRequest(http.MethodPost, "http://localhost/api/test", nil)
	req.Host = "localhost"
	req.Header.Set("Origin", "https://attacker.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestSameOriginMutationPasses(t *testing.T) {
	g := NewGateway("")
	h := g.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	req := httptest.NewRequest(http.MethodPost, "http://localhost/api/test", nil)
	req.Host = "localhost"
	req.Header.Set("Origin", "http://localhost")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
}

func TestSecurePasswordEqual(t *testing.T) {
	if !securePasswordEqual("correct", "correct") || securePasswordEqual("wrong", "correct") {
		t.Fatal("password comparison result is incorrect")
	}
}

func TestMCPUsesPluginBearerAuthInsteadOfConsoleCookie(t *testing.T) {
	g := NewGateway("admin-password")
	h := g.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))

	req := httptest.NewRequest(http.MethodPost, "http://localhost/api/plugins/devtools/mcp", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("MCP endpoint blocked before plugin bearer auth: status=%d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "http://localhost/api/plugins/devtools/api/tokens", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("token admin endpoint bypassed console auth: status=%d", rec.Code)
	}
}

func TestDeployWebhookUsesItsOwnTokenWithoutConsoleCookie(t *testing.T) {
	g := NewGateway("admin-password")
	h := g.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	req := httptest.NewRequest(http.MethodPost, "http://localhost/api/plugins/deploy/api/hooks/orders/random-token", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("deploy webhook blocked before application token auth: status=%d", rec.Code)
	}
}

func TestAuthMiddlewareAllowsLoginShellAssets(t *testing.T) {
	g := NewGateway("secret")
	h := g.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, path := range []string{"/", "/login", "/assets/shell.js", "/static/theme.css", "/api/health"} {
		req := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("login shell resource %s blocked: status=%d", path, rec.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "http://localhost/api/plugins", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("protected API unexpectedly allowed: status=%d", rec.Code)
	}
}

func TestExpiredConsoleSessionSignalsPlatformLogin(t *testing.T) {
	g := NewGateway("enabled")
	h := g.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for _, path := range []string{"/api/plugins/research/api/research/tasks", "/api/plugins/deploy/api/changes", "/api/me"} {
		req := httptest.NewRequest("GET", "http://localhost"+path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 401 || rec.Header().Get("X-Platform-Auth-Required") != "1" || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(path, rec.Code, rec.Header())
		}
	}
	// Browsing a plugin directly enters the platform login; API calls remain JSON 401.
	req := httptest.NewRequest("GET", "http://localhost/api/plugins/research/ui?embedded=0", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login?return_to=%2Fapi%2Fplugins%2Fresearch%2Fui%3Fembedded%3D0" {
		t.Fatal(rec.Code, rec.Header())
	}
	for _, path := range []string{"/api/plugins/research/ui/api", "/api/plugins/research/api/ui", "/api/plugins/research/ui.js"} {
		if pluginLoginReturn(path) {
			t.Fatal("non UI route redirected", path)
		}
	}
}

func TestSourceUnauthorizedAndForbiddenAreNotPlatformExpiry(t *testing.T) {
	g := NewGateway("enabled")
	token := sessionToken(User{Username: "member", Role: RoleMember, Enabled: true})
	defer func() { sessionsMu.Lock(); delete(sessions, token); sessionsMu.Unlock() }()
	for _, status := range []int{401, 403} {
		h := g.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
		req := httptest.NewRequest("GET", "http://localhost/api/plugins/research/api/source", nil)
		req.AddCookie(&http.Cookie{Name: "lp_session", Value: token})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != status || rec.Header().Get("X-Platform-Auth-Required") != "" {
			t.Fatal("source error marked as expired login", rec.Code, rec.Header())
		}
	}
}

func TestLoginPageRouteServesShellWithoutOpeningAPIs(t *testing.T) {
	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	handler := NewGateway("enabled").Routes(registry, NewBus())
	for _, path := range []string{"/login", "/login?reason=expired&return_to=%2Fapi%2Fplugins%2Fresearch%2Fui"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
			t.Fatal(path, rec.Code, rec.Header())
		}
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/users", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatal("login page exposed API", rec.Code)
	}
}
