package platform

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestWorkLoopSchedulerAndPublicBoundary(t *testing.T) {
	registry, e := OpenRegistry(filepath.Join(t.TempDir(), "registry.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer registry.Close()
	_, e = registry.SaveUser(User{Username: "alice", Role: RoleMember, Enabled: true}, "qa-password")
	if e != nil {
		t.Fatal(e)
	}
	var ticks atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/work-loops" || r.URL.Path == "/api/service-deployments" {
			if r.Header.Get("X-User") != "alice" {
				t.Error("wrong actor")
			}
			_ = json.NewEncoder(w).Encode([]any{map[string]any{"id": strings.Repeat("a", 32), "state": "running"}, map[string]any{"id": strings.Repeat("b", 32), "state": "blocked"}})
			return
		}
		if r.URL.Path == "/internal/work-loop-advance" || r.URL.Path == "/internal/service-deployment-advance" {
			ticks.Add(1)
			if !strings.Contains(r.Header.Get("X-MCP-Users"), `"username":"alice"`) {
				t.Error("missing current directory")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
			return
		}
		t.Error("unexpected path", r.URL.Path)
		w.WriteHeader(404)
	}))
	defer up.Close()
	u, _ := url.Parse(up.URL)
	g := NewGateway("enabled")
	g.SetPlugins([]*Manifest{{Name: "deploy", AdminAddr: u.Host}, {Name: "devtools", AdminAddr: u.Host}})
	g.workLoopTick(context.Background(), registry)
	if ticks.Load() != 2 {
		t.Fatal(ticks.Load())
	}
	// No authentication bypass for the internal dispatcher, even on auth-off test gateways.
	public := NewGateway("")
	public.SetPlugins([]*Manifest{{Name: "devtools", AdminAddr: u.Host}})
	h := public.Routes(registry, NewBus())
	r := httptest.NewRequest("POST", "/api/plugins/devtools/internal/work-loop-advance", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 404 || ticks.Load() != 2 {
		t.Fatal("public internal endpoint exposed", w.Code)
	}
	if !researchPluginPathAllowed(httptest.NewRequest("POST", "/api/work-loops/abc/authorization", nil)) {
		t.Fatal("page task renewal blocked by gateway")
	}
}
