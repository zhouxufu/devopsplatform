package deploy

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Opt-in fixture uses the real rename/configuration routes and a disposable DB.
// No build/deploy/resource mutation route is reachable from this server.
func TestApplicationNamesBrowserFixture(t *testing.T) {
	port := os.Getenv("APPLICATION_NAMES_FIXTURE_PORT")
	if port == "" {
		t.Skip("opt-in isolated browser fixture")
	}
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, app := range []Service{{Name: "name-fixture", DisplayName: "智能搜索服务", ArtifactType: "static", BuildTool: "command", BuildCmd: "npm run build", GitURL: "https://example.invalid/repo.git", ArtifactGlob: "dist"}, {Name: "旧中文应用", ArtifactType: "jar", BuildTool: "maven", MavenExecutable: "auto", MavenGoals: "clean package"}} {
		if err := st.UpsertService(app); err != nil {
			t.Fatal(err)
		}
	}
	hub := NewLogHub()
	routes := Routes(st, NewBuilder(st, dir, hub), hub, "", NewStepLib("../../steps"))
	var writes, blocked atomic.Int32
	files := http.FileServer(http.Dir("../../web/dist"))
	server := &http.Server{Addr: "127.0.0.1:" + port, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__fixture/state" {
			json.NewEncoder(w).Encode(map[string]any{"writes": writes.Load(), "blocked": blocked.Load(), "applications": st.Services(), "audit": st.AuditEvents(20)})
			return
		}
		if r.Method != "GET" && !(r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/display-name")) {
			blocked.Add(1)
			http.Error(w, "fixture blocks execution", 403)
			return
		}
		if r.Method == "PUT" {
			writes.Add(1)
		}
		if r.URL.Path == "/api/me" {
			json.NewEncoder(w).Encode(map[string]any{"username": "admin", "role": "admin", "auth_enabled": false})
			return
		}
		if r.URL.Path == "/api/plugins" || r.URL.Path == "/api/registry/applications" {
			json.NewEncoder(w).Encode([]any{})
			return
		}
		// Exercise the same nested URL as the real platform plugin. /api/me is
		// platform-owned; a relative api/me request must not succeed here.
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/api/plugins/deploy")
		if r.URL.Path == "/api/me" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/ui" {
			r.URL.Path = "/deploy.html"
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			r.Header.Set("X-User", "admin")
			r.Header.Set("X-Role", "admin")
			routes.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/" {
			r.URL.Path = "/deploy.html"
		}
		files.ServeHTTP(w, r)
	})}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve(listener)
	defer server.Close()
	t.Log("isolated application names fixture http://" + server.Addr + "/deploy.html#/")
	time.Sleep(10 * time.Minute)
}
