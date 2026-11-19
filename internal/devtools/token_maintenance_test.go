package devtools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func tokenRequest(t *testing.T, h http.Handler, method, path, actor, role string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
	r.Header.Set("X-User", actor)
	r.Header.Set("X-Role", role)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestTokenMaintenanceKeyUnchangedCASPermissionsDeleteAudit(t *testing.T) {
	store, _ := OpenTokenStore(t.TempDir())
	token, secret, err := store.Create(CreateTokenRequest{Owner: "alice", Name: "old", Scopes: []string{"changes:read"}, Applications: []string{"orders"}, Environments: []string{"test"}})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewToolService(Upstreams{})
	h := Routes(store, svc)
	store.addAction(PlatformAction{ID: "preview", TokenID: token.ID, Owner: "alice", State: "preview", ExpiresAt: time.Now().Add(time.Hour).Unix()})
	revision := int64(0)
	req := UpdateTokenRequest{Revision: &revision, Name: "edited", Scopes: []string{"changes:read", "changes:write"}, Applications: []string{"orders"}, Environments: []string{"test"}}
	path := "/api/tokens/" + token.ID
	if w := tokenRequest(t, h, "PUT", path, "bob", "member", req); w.Code == 200 {
		t.Fatal("foreign token edited")
	}
	req.Scopes = append(req.Scopes, "resources:read")
	if w := tokenRequest(t, h, "PUT", path, "alice", "member", req); w.Code == 200 {
		t.Fatal("member escalation")
	}
	req.Scopes = req.Scopes[:2]
	w := tokenRequest(t, h, "PUT", path, "alice", "member", req)
	if w.Code != 200 || strings.Contains(w.Body.String(), secret) || strings.Contains(w.Body.String(), "hash") {
		t.Fatal(w.Code, w.Body.String())
	}
	fresh, err := store.Authenticate(secret)
	if err != nil || fresh.Owner != "alice" || fresh.Name != "edited" || fresh.Revision != 1 || !fresh.HasScope("changes:write") || fresh.ExpiresAt != token.ExpiresAt {
		t.Fatal(fresh, err)
	}
	if w = tokenRequest(t, h, "PUT", path, "alice", "member", req); w.Code == 200 {
		t.Fatal("stale overwrite")
	}
	state, _ := store.actionStatus(&token, "preview")
	if mapValue(state)["state"] != "stale" {
		t.Fatal(state)
	}
	if w = tokenRequest(t, h, "DELETE", path+"/record", "alice", "member", map[string]any{"revision": 1}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err = store.Authenticate(secret); err == nil || len(store.List()) != 0 {
		t.Fatal("deleted token usable")
	}
	w = tokenRequest(t, h, "GET", "/api/audit", "alice", "member", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "delete_token") || !strings.Contains(w.Body.String(), "update_token") {
		t.Fatal("audit lost", w.Body.String())
	}
	reopened, err := OpenTokenStore(strings.TrimSuffix(store.path, "/devtools-tokens.json"))
	if err != nil || len(reopened.List()) != 0 {
		t.Fatal("delete not persisted", err)
	}
}
func TestTokenMaintenanceExecutingProtectionAndRevokedNotRestored(t *testing.T) {
	store, _ := OpenTokenStore(t.TempDir())
	token, _, _ := store.Create(CreateTokenRequest{Owner: "alice", Name: "key", Scopes: []string{"changes:write"}, Applications: []string{"*"}, Environments: []string{"test"}})
	revision := int64(0)
	req := &UpdateTokenRequest{Revision: &revision, Name: "x", Scopes: token.Scopes, Applications: token.Applications, Environments: token.Environments}
	store.addAction(PlatformAction{ID: "executing", TokenID: token.ID, State: "executing"})
	if _, err := store.maintainToken(token.ID, "alice", false, req, false); err == nil {
		t.Fatal("changed executing token")
	}
	if _, err := store.maintainToken(token.ID, "alice", false, req, true); err == nil {
		t.Fatal("deleted executing token")
	}
	store.finishAction("executing", "success", nil, nil)
	store.Revoke(token.ID)
	revision = 1
	if _, err := store.maintainToken(token.ID, "alice", false, req, false); err == nil {
		t.Fatal("revoked restored")
	}
	if _, err := store.maintainToken(token.ID, "alice", false, req, true); err != nil {
		t.Fatal(err)
	}
}
