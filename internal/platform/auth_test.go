package platform

import (
	"net/http"
	"net/http/httptest"
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

	for _, path := range []string{"/", "/assets/shell.js", "/static/theme.css", "/api/health"} {
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
