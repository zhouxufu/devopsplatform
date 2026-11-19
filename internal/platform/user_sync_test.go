package platform

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func directoryFixture() UserDirectory {
	return UserDirectory{Source: "zentao:" + strings.Repeat("a", 64), Users: []DirectoryUser{{ID: "1", Account: "alice", Name: "张三"}, {ID: "2", Account: "admin", Name: "远端管理员"}, {ID: "3", Account: "bob", Name: "远端 Bob"}}}
}

func TestDirectoryManualAssociationIsAtomicAndPreservesAccounts(t *testing.T) {
	r, err := OpenRegistry(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.SaveUser(User{Username: "disabled", DisplayName: "本地停用成员", Role: RoleMember, Enabled: false}, "local-password"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"existing", "another", "admin"} {
		if _, err := r.SaveUser(User{Username: name, DisplayName: "本地姓名", Role: RoleAdmin, Enabled: true}, "local-password"); err != nil {
			t.Fatal(err)
		}
	}
	d := directoryFixture()
	before, _ := r.Snapshot()
	for _, links := range []map[string]string{{"1": "missing"}, {"unknown": "existing"}, {"1": "existing", "3": "existing"}, {"3": "another"}} {
		if _, err := r.SyncDirectory(d, []string{"1"}, true, links); err == nil {
			t.Fatal("invalid association accepted", links)
		}
		after, _ := r.Snapshot()
		if len(after.Users) != len(before.Users) || len(after.UserIdentities) != 0 {
			t.Fatal("invalid association partially wrote users")
		}
	}
	preview, err := r.SyncDirectory(d, nil, false, map[string]string{"1": "existing"})
	if err != nil || preview.Users[0].Action != "link" || preview.Users[0].Bound {
		t.Fatal(preview, err)
	}
	if snapshot, _ := r.Snapshot(); len(snapshot.UserIdentities) != 0 {
		t.Fatal("preview persisted association")
	}
	for attempt := 0; attempt < 2; attempt++ {
		result, err := r.SyncDirectory(d, []string{"1"}, true, map[string]string{"1": "existing"})
		if err != nil || result.Users[0].Action != "link" || !result.Users[0].Bound || result.Users[0].Username != "existing" {
			t.Fatal(result, err)
		}
	}
	user, err := r.Authenticate("existing", "local-password")
	if err != nil || user.DisplayName != "本地姓名" || user.Role != RoleAdmin || !user.Enabled {
		t.Fatal("association changed login or profile", user, err)
	}
	if _, err := r.SyncDirectory(d, []string{"1"}, true, map[string]string{"1": "another"}); err == nil {
		t.Fatal("rebound external identity")
	}
	if _, err := r.SyncDirectory(d, []string{"3"}, true, map[string]string{"3": "existing"}); err == nil {
		t.Fatal("two source users share one account")
	}
	other := d
	other.Source = "zentao:" + strings.Repeat("b", 64)
	if _, err := r.SyncDirectory(other, []string{"1"}, true, map[string]string{"1": "existing"}); err == nil {
		t.Fatal("cross-source identity stolen")
	}
	// Linking to an existing administrator is an explicit admin operation, not
	// an external role import. Password and authorization remain local.
	if _, err := r.SyncDirectory(d, []string{"2"}, true, map[string]string{"2": "admin"}); err != nil {
		t.Fatal(err)
	}
	if user, err := r.Authenticate("admin", "local-password"); err != nil || user.DisplayName != "本地姓名" || user.Role != RoleAdmin {
		t.Fatal(user, err)
	}
	d.Users[0].Account = "renamed"
	d.Users[0].Name = "来源新姓名"
	result, err := r.SyncDirectory(d, []string{"1"}, true)
	if err != nil || result.Users[0].Username != "existing" || !result.Users[0].Bound || result.Users[0].Action != "update" {
		t.Fatal(result, err)
	}
	if user, err := r.Authenticate("existing", "local-password"); err != nil || user.DisplayName != "来源新姓名" || user.Role != RoleAdmin {
		t.Fatal(user, err)
	}
	snapshot, _ := r.Snapshot()
	if _, err := r.SyncDirectory(d, []string{"3"}, true, map[string]string{"3": "disabled"}); err != nil {
		t.Fatal(err)
	}
	after, _ := r.Snapshot()
	for _, user := range after.Users {
		if user.Username == "disabled" {
			user.Source = "" // Only the identity mapping is expected to change.
			for _, original := range snapshot.Users {
				if original.Username == "disabled" && !reflect.DeepEqual(user, original) {
					t.Fatal("association mutated disabled account")
				}
			}
		}
	}
	snapshot = after
	if len(snapshot.Users) != 4 || len(snapshot.UserIdentities) != 3 {
		t.Fatal("duplicate account created", snapshot.Users)
	}
	target, err := OpenRegistry(filepath.Join(t.TempDir(), "target.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if err := target.Restore(snapshot); err != nil {
		t.Fatal(err)
	}
	if p, err := target.SyncDirectory(d, nil, false); err != nil || p.Users[0].Username != "existing" || !p.Users[0].Bound {
		t.Fatal("migration lost link", p, err)
	}
}
func TestDirectorySyncPreservesLocalPermissionsAndPasswords(t *testing.T) {
	r, err := OpenRegistry(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err = r.SaveUser(User{Username: "bob", DisplayName: "Local Bob", Role: RoleAdmin, Enabled: true}, "local-password"); err != nil {
		t.Fatal(err)
	}
	d := directoryFixture()
	preview, err := r.SyncDirectory(d, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Users[0].Action != "create" || preview.Users[1].Action != "skip" || preview.Users[2].Action != "skip" {
		t.Fatalf("preview: %+v", preview)
	}
	if u, _ := r.Users(); len(u) != 1 {
		t.Fatal("preview mutated users")
	}
	if _, err = r.SyncDirectory(d, []string{"1", "2", "3"}, true); err != nil {
		t.Fatal(err)
	}
	users, _ := r.Users()
	if len(users) != 2 {
		t.Fatalf("users: %+v", users)
	}
	for _, u := range users {
		if u.Username == "alice" && (u.Enabled || u.Role != RoleMember || u.PasswordHash != "" || u.PasswordConfigured || u.Source != d.Source) {
			t.Fatal("new user received login/privileges")
		}
	}
	if _, err = r.SaveUser(User{Username: "alice", Role: RoleMember, Enabled: true}, ""); err == nil {
		t.Fatal("enabled user without password")
	}
	if _, err = r.SaveUser(User{Username: "alice", Role: RoleAdmin, Enabled: true}, "alice-password"); err != nil {
		t.Fatal(err)
	}
	d.Users[0].Account = "renamed-in-zentao"
	d.Users[0].Name = "李四"
	if _, err = r.SyncDirectory(d, []string{"1"}, true); err != nil {
		t.Fatal(err)
	}
	u, err := r.Authenticate("alice", "alice-password")
	if err != nil || u.Role != RoleAdmin || u.DisplayName != "李四" {
		t.Fatalf("resync changed permissions/password: %v %+v", err, u)
	}
	if u, err := r.Authenticate("bob", "local-password"); err != nil || u.DisplayName != "Local Bob" {
		t.Fatal("overwrote same-name local account")
	}
	if _, err = r.SyncDirectory(d, []string{"forged-id"}, true); err == nil {
		t.Fatal("accepted unknown external id")
	}
	snapshot, err := r.Snapshot()
	if err != nil || len(snapshot.UserIdentities) != 1 {
		t.Fatalf("snapshot identities: %v", err)
	}
	target, err := OpenRegistry(filepath.Join(t.TempDir(), "target.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if err = target.Restore(snapshot); err != nil {
		t.Fatal(err)
	}
	if preview, err = target.SyncDirectory(d, nil, false); err != nil || preview.Users[0].Action != "update" {
		t.Fatal("migration lost identity")
	}
	if _, err = target.Authenticate("alice", "alice-password"); err != nil {
		t.Fatal("migration lost password")
	}
	conflict := snapshot
	conflict.UserIdentities = []UserIdentity{{Source: "zentao:" + strings.Repeat("b", 64), ExternalID: "1", Username: "alice"}}
	if err = target.Restore(conflict); err == nil {
		t.Fatal("migration replaced an existing external identity")
	}
	if err = target.DeleteUser("alice", "bob"); err != nil {
		t.Fatal(err)
	}
	if preview, err = target.SyncDirectory(d, nil, false); err != nil || preview.Users[0].Action != "skip" {
		t.Fatal("resync resurrected deleted user")
	}
}

func TestBootstrapAfterDirectoryImport(t *testing.T) {
	r, err := OpenRegistry(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err = r.SyncDirectory(directoryFixture(), []string{"1"}, true); err != nil {
		t.Fatal(err)
	}
	if err = r.EnsureBootstrapAdmin("bootstrap-password"); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Authenticate("admin", "bootstrap-password"); err != nil {
		t.Fatal("import prevented bootstrap admin")
	}
}

func TestDirectorySyncMySQLMigration(t *testing.T) {
	dsn := os.Getenv("DEVOPS_USER_SYNC_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set DEVOPS_USER_SYNC_MYSQL_DSN to an empty disposable MySQL schema")
	}
	r, err := OpenRegistryDatabase(RegistryConfig{Driver: "mysql", DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	d := directoryFixture()
	if _, err = r.SyncDirectory(d, []string{"1"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err = r.SaveUser(User{Username: "alice", Role: RoleAdmin, Enabled: true}, "mysql-qa-password"); err != nil {
		t.Fatal(err)
	}
	d.Users[0].Name = "新姓名"
	if result, err := r.SyncDirectory(d, []string{"1"}, true); err != nil || result.Users[0].Action != "update" {
		t.Fatalf("mysql sync: %v", err)
	}
	if u, err := r.Authenticate("alice", "mysql-qa-password"); err != nil || u.Role != RoleAdmin || u.DisplayName != "新姓名" {
		t.Fatal("MySQL sync changed auth")
	}
	snapshot, err := r.Snapshot()
	if err != nil || len(snapshot.UserIdentities) != 1 {
		t.Fatalf("mysql snapshot: %v", err)
	}
	target, err := OpenRegistry(filepath.Join(t.TempDir(), "target.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if err = target.Restore(snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err = target.SyncDirectory(d, []string{"1"}, true); err != nil {
		t.Fatal(err)
	}
	if err = r.Restore(snapshot); err != nil {
		t.Fatal(err)
	}
}

func TestDirectorySyncGatewayAuthorizationAndAuthoritativeFetch(t *testing.T) {
	r, err := OpenRegistry(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.SaveUser(User{Username: "existing", DisplayName: "本地姓名", Role: RoleAdmin, Enabled: true}, "local-password"); err != nil {
		t.Fatal(err)
	}
	reads := 0
	plugin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		reads++
		if req.Method != "POST" || req.URL.Path != "/api/connections/zentao/user-directory" || req.Header.Get("X-Role") != "admin" {
			t.Error("invalid provider request")
		}
		_ = json.NewEncoder(w).Encode(directoryFixture())
	}))
	defer plugin.Close()
	g := NewGateway("enabled")
	g.SetPlugins([]*Manifest{{Name: "deploy", AdminAddr: strings.TrimPrefix(plugin.URL, "http://")}})
	h := g.Routes(r, NewBus())
	for _, role := range []string{RoleAdmin, RoleMember} {
		token := "sync-test-" + role
		sessionsMu.Lock()
		sessions[token] = sessionIdentity{Username: role, Role: role, Expires: time.Now().Add(time.Minute)}
		sessionsMu.Unlock()
		t.Cleanup(func() { sessionsMu.Lock(); delete(sessions, token); sessionsMu.Unlock() })
		for _, path := range []string{"/api/users/sync-preview", "/api/users/sync"} {
			payload, _ := json.Marshal(map[string]any{"connection": "zentao", "ids": []string{"1"}, "role": "member", "name": "forged", "password": "forged-password", "source": "forged", "expected_source": directoryFixture().Source, "links": map[string]string{"1": "existing"}})
			req := httptest.NewRequest("POST", path, bytes.NewReader(payload))
			req.AddCookie(&http.Cookie{Name: "lp_session", Value: token})
			out := httptest.NewRecorder()
			h.ServeHTTP(out, req)
			want := 200
			if role == RoleMember {
				want = 403
			}
			if out.Code != want {
				t.Fatalf("%s %s: %d %s", role, path, out.Code, out.Body.String())
			}
		}
	}
	if reads != 2 {
		t.Fatalf("member reached provider or confirmation skipped refetch: %d", reads)
	}
	req := httptest.NewRequest("POST", "/api/users/sync", strings.NewReader(`{"connection":"zentao","ids":["1"],"expected_source":"changed"}`))
	req.AddCookie(&http.Cookie{Name: "lp_session", Value: "sync-test-admin"})
	output := httptest.NewRecorder()
	h.ServeHTTP(output, req)
	if output.Code != 400 || !strings.Contains(output.Body.String(), "重新读取") {
		t.Fatal("accepted changed source")
	}
	users, _ := r.Users()
	if len(users) != 1 || users[0].Username != "existing" || users[0].DisplayName != "本地姓名" || users[0].Role != RoleAdmin || !users[0].Enabled || users[0].Source != directoryFixture().Source {
		t.Fatalf("trusted forged payload %+v", users)
	}
	if _, err := r.Authenticate("existing", "local-password"); err != nil {
		t.Fatal("gateway association changed password", err)
	}
	encoded, _ := json.Marshal(users)
	if bytes.Contains(encoded, []byte("password_hash")) {
		t.Fatal("password hash leaked")
	}
}
