package platform

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestMCPGatewayReplacesSpoofedDirectoryWithCurrentUsers(t *testing.T) {
	reg, e := OpenRegistry(filepath.Join(t.TempDir(), "registry.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer reg.Close()
	if _, e = reg.SaveUser(User{Username: "alice", DisplayName: "Alice", Role: RoleMember, Enabled: true}, "member-password"); e != nil {
		t.Fatal(e)
	}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("X-MCP-Users"), "mallory") {
			t.Error("client forged MCP identity")
		}
		var rows []struct {
			Username, Role string
			Enabled        bool
		}
		if json.Unmarshal([]byte(r.Header.Get("X-MCP-Users")), &rows) != nil || len(rows) != 1 || rows[0].Username != "alice" || rows[0].Role != RoleMember {
			t.Error("directory not injected", r.Header.Get("X-MCP-Users"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{}`))
	}))
	defer up.Close()
	u, _ := url.Parse(up.URL)
	g := NewGateway("enabled")
	g.SetPlugins([]*Manifest{{Name: "devtools", AdminAddr: u.Host}})
	h := g.Routes(reg, NewBus())
	r := httptest.NewRequest("POST", "http://localhost/api/plugins/devtools/mcp", strings.NewReader(`{}`))
	r.Header.Set("X-MCP-Users", `[{"username":"mallory","role":"admin","enabled":true}]`)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestBrowserCannotSupplyMCPDirectoryToDocumentImport(t *testing.T) {
	reg, e := OpenRegistry(filepath.Join(t.TempDir(), "registry.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer reg.Close()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-MCP-Users") != "" || r.Header.Get("X-User") != "local" {
			t.Error("browser supplied trusted MCP identity")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{}`))
	}))
	defer up.Close()
	u, _ := url.Parse(up.URL)
	g := NewGateway("")
	g.SetPlugins([]*Manifest{{Name: "deploy", AdminAddr: u.Host}})
	r := httptest.NewRequest("POST", "http://localhost/api/plugins/deploy/api/api-documents/import", strings.NewReader(`{}`))
	r.Header.Set("X-User", "mallory")
	r.Header.Set("X-MCP-Users", `[{"username":"mallory","role":"admin","enabled":true}]`)
	w := httptest.NewRecorder()
	g.Routes(reg, NewBus()).ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}
