package devtools

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func createTestToken(t *testing.T, store *TokenStore) string {
	t.Helper()
	_, raw, err := store.Create(CreateTokenRequest{
		Name: "codex-test", Scopes: []string{"logs:read", "metrics:read", "traces:read", "builds:read", "releases:read"},
		Applications: []string{"orders"}, Environments: []string{"test"}, ExpiresInDays: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestTokenIsHashedScopedAndPersistent(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenTokenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	raw := createTestToken(t, store)
	file, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(file, []byte(raw)) {
		t.Fatal("raw token persisted to disk")
	}
	if got := store.List(); len(got) != 1 || got[0].Hash != "" {
		t.Fatalf("public token list leaked hash: %+v", got)
	}
	reopened, err := OpenTokenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	token, err := reopened.Authenticate(raw)
	if err != nil {
		t.Fatal(err)
	}
	if token.Allows("orders", "test") != nil || token.Allows("orders", "prod") == nil || token.Allows("users", "test") == nil {
		t.Fatal("application/environment scope enforcement is incorrect")
	}
}

func TestMCPInitializeListAndScopeDenial(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/services":
			w.Write([]byte(`[{"name":"orders","artifact_type":"jar","build_tool":"maven"},{"name":"secret-service"}]`))
		default:
			w.Write([]byte(`[]`))
		}
	}))
	defer upstream.Close()
	store, _ := OpenTokenStore(t.TempDir())
	raw := createTestToken(t, store)
	h := NewMCPHandler(store, NewToolService(Upstreams{Logs: upstream.URL, Monitor: upstream.URL, Deploy: upstream.URL}))

	call := func(body string, bearer bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if bearer {
			req.Header.Set("Authorization", "Bearer "+raw)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := call(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`, false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d", rec.Code)
	}
	initRec := call(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`, true)
	if initRec.Code != http.StatusOK || !strings.Contains(initRec.Body.String(), "logplatform-devtools") || !strings.Contains(initRec.Body.String(), "instructions") {
		t.Fatalf("initialize status=%d body=%s", initRec.Code, initRec.Body.String())
	}
	listRec := call(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`, true)
	if !strings.Contains(listRec.Body.String(), `"search_logs"`) || !strings.Contains(listRec.Body.String(), `"readOnlyHint":true`) {
		t.Fatalf("tool list missing metadata: %s", listRec.Body.String())
	}
	appsRec := call(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_applications","arguments":{}}}`, true)
	if !strings.Contains(appsRec.Body.String(), `\"orders\"`) || strings.Contains(appsRec.Body.String(), "secret-service") {
		t.Fatalf("application scope leak: %s", appsRec.Body.String())
	}
	denied := call(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"search_logs","arguments":{"service":"orders","environment":"prod"}}}`, true)
	if !strings.Contains(denied.Body.String(), `"isError":true`) || !strings.Contains(denied.Body.String(), "无权访问环境") {
		t.Fatalf("scope denial missing: %s", denied.Body.String())
	}
	if events := store.AuditEvents(10); len(events) != 2 || events[0].Success {
		t.Fatalf("tool calls not audited: %+v", events)
	}
}

func TestAdminCreateResponseNeverReturnsHash(t *testing.T) {
	store, _ := OpenTokenStore(t.TempDir())
	h := Routes(store, NewToolService(Upstreams{}))
	body, _ := json.Marshal(CreateTokenRequest{Name: "codex", Scopes: []string{"logs:read"}, Applications: []string{"orders"}, Environments: []string{"test"}, ExpiresInDays: 7})
	req := httptest.NewRequest(http.MethodPost, "/api/tokens", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), "lp_dev_") || strings.Contains(rec.Body.String(), `"hash"`) {
		t.Fatalf("unsafe create response: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestTokenDialogCancelDoesNotSubmitRequiredForm(t *testing.T) {
	store, _ := OpenTokenStore(t.TempDir())
	h := Routes(store, NewToolService(Upstreams{}))
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `id="cancelBtn"`) || !strings.Contains(body, `type="button"`) {
		t.Fatalf("cancel button may still trigger form validation: status=%d", rec.Code)
	}
	if strings.Contains(body, `<form method="dialog"`) || !strings.Contains(body, `$('cancelBtn').onclick=()=>$('dialog').close()`) {
		t.Fatal("token dialog cancel is not wired to an unconditional close")
	}
}

func TestToolOutputSecretRedaction(t *testing.T) {
	input := map[string]any{
		"message": "Authorization: Bearer-abc password=hunter2 https://user:pass@example.com/path",
		"api_key": "plain-secret",
	}
	out := scrubAny(input).(map[string]any)
	message := out["message"].(string)
	if strings.Contains(message, "hunter2") || strings.Contains(message, "pass@") || out["api_key"] != "[REDACTED]" {
		t.Fatalf("secret was not redacted: %+v", out)
	}
}
