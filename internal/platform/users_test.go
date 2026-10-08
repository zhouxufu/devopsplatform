package platform

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestLoginAndUserManagementAPI(t *testing.T) {
	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	if err := registry.EnsureBootstrapAdmin("initial-password"); err != nil {
		t.Fatal(err)
	}
	handler := NewGateway("enabled").Routes(registry, NewBus())

	loginBody, _ := json.Marshal(map[string]string{"username": "admin", "password": "initial-password"})
	login := httptest.NewRecorder()
	handler.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "http://localhost/api/login", bytes.NewReader(loginBody)))
	if login.Code != http.StatusOK || len(login.Result().Cookies()) != 1 {
		t.Fatalf("login status=%d body=%s", login.Code, login.Body.String())
	}
	cookie := login.Result().Cookies()[0]
	createBody, _ := json.Marshal(map[string]any{"username": "bob", "display_name": "Bob", "role": RoleMember, "password": "member-password", "enabled": true})
	createReq := httptest.NewRequest(http.MethodPost, "http://localhost/api/users", bytes.NewReader(createBody))
	createReq.AddCookie(cookie)
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, createReq)
	if created.Code != http.StatusOK {
		t.Fatalf("create user status=%d body=%s", created.Code, created.Body.String())
	}
	meReq := httptest.NewRequest(http.MethodGet, "http://localhost/api/me", nil)
	meReq.AddCookie(cookie)
	me := httptest.NewRecorder()
	handler.ServeHTTP(me, meReq)
	if me.Code != http.StatusOK || !bytes.Contains(me.Body.Bytes(), []byte(`"role":"admin"`)) {
		t.Fatalf("me status=%d body=%s", me.Code, me.Body.String())
	}
}

func TestBootstrapAdminAndTwoBuiltInRoles(t *testing.T) {
	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	if err := registry.EnsureBootstrapAdmin("initial-password"); err != nil {
		t.Fatal(err)
	}
	if err := registry.EnsureBootstrapAdmin("must-not-overwrite"); err != nil {
		t.Fatal(err)
	}
	admin, err := registry.Authenticate("admin", "initial-password")
	if err != nil || admin.Role != RoleAdmin {
		t.Fatalf("bootstrap admin = %#v, %v", admin, err)
	}
	if _, err := registry.SaveUser(User{Username: "alice", DisplayName: "Alice", Role: RoleMember, Enabled: true}, "member-password"); err != nil {
		t.Fatal(err)
	}
	member, err := registry.Authenticate("alice", "member-password")
	if err != nil || member.Role != RoleMember {
		t.Fatalf("member = %#v, %v", member, err)
	}
	if _, err := registry.SaveUser(User{Username: "bad-role", Role: "auditor", Enabled: true}, "member-password"); err == nil {
		t.Fatal("custom role unexpectedly accepted")
	}
}

func TestUserSnapshotPreservesPasswordHash(t *testing.T) {
	source, err := OpenRegistry(filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if _, err := source.SaveUser(User{Username: "alice", Role: RoleMember, Enabled: true}, "member-password"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := source.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	target, err := OpenRegistry(filepath.Join(t.TempDir(), "target.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if err := target.Restore(snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := target.Authenticate("alice", "member-password"); err != nil {
		t.Fatalf("migrated account cannot authenticate: %v", err)
	}
}

func TestMemberCanReleaseButCannotManageResources(t *testing.T) {
	g := NewGateway("enabled")
	h := g.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	token := "member-role-session"
	sessionsMu.Lock()
	sessions[token] = sessionIdentity{Username: "alice", Role: RoleMember, Expires: time.Now().Add(time.Minute)}
	sessionsMu.Unlock()
	t.Cleanup(func() {
		sessionsMu.Lock()
		delete(sessions, token)
		sessionsMu.Unlock()
	})

	call := func(method, path string) int {
		req := httptest.NewRequest(method, "http://localhost"+path, nil)
		req.AddCookie(&http.Cookie{Name: "lp_session", Value: token})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := call(http.MethodPost, "/api/resources/hosts"); got != http.StatusForbidden {
		t.Fatalf("resource mutation status = %d, want 403", got)
	}
	if got := call(http.MethodPost, "/api/plugins/deploy/api/releases"); got != http.StatusNoContent {
		t.Fatalf("release status = %d, want 204", got)
	}
	if got := call(http.MethodPost, "/api/plugins/deploy/api/builds"); got != http.StatusNoContent {
		t.Fatalf("build status = %d, want 204", got)
	}
}
