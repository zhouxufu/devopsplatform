package platform

import (
	"context"
	"encoding/json"
	"logplatform/internal/appcatalog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Disposable catalog, real lifecycle APIs; no deployment or source mutation
// route is exposed. Browser tests cancel deletion or archive only these seeds.
func TestApplicationLifecycleBrowserFixture(t *testing.T) {
	port := os.Getenv("APPLICATION_LIFECYCLE_FIXTURE_PORT")
	if port == "" {
		t.Skip("opt-in isolated browser fixture")
	}
	r, err := OpenRegistry(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	refs := []appcatalog.ApplicationReference{}
	for _, seed := range []struct{ name, source, label string }{{"manual-fixture", "manual", "手动目录应用"}, {"deployed-fixture", "deploy", "部署关联应用"}, {"logs-fixture", "logs", "日志发现应用"}, {"stale-fixture", "deploy", "历史已删除应用"}} {
		a, err := r.Observe(seed.name, seed.source, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := r.setApplicationDisplayName(a.ApplicationID, seed.label, a.Name, "fixture"); err != nil {
			t.Fatal(err)
		}
		if seed.name == "deployed-fixture" {
			refs = append(refs, appcatalog.ApplicationReference{ApplicationID: a.ApplicationID, Name: a.Name})
		}
	}
	r.deploymentReferences = func(context.Context) ([]appcatalog.ApplicationReference, error) { return refs, nil }
	mux := http.NewServeMux()
	r.HandleRoutes(mux)
	files := http.FileServer(http.Dir("../../web/dist"))
	var deletes, blocked atomic.Int32
	server := &http.Server{Addr: "127.0.0.1:" + port, Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/__fixture/state" {
			all, _ := r.List()
			var n int
			r.db.QueryRow(`SELECT COUNT(*) FROM application_lifecycle_events`).Scan(&n)
			json.NewEncoder(w).Encode(map[string]any{"applications": all, "deletes": deletes.Load(), "blocked": blocked.Load(), "audit": n})
			return
		}
		if req.Method != "GET" && !(req.Method == "DELETE" && strings.HasPrefix(req.URL.Path, "/api/registry/applications/")) {
			blocked.Add(1)
			http.Error(w, "fixture blocks execution", 403)
			return
		}
		if req.Method == "DELETE" {
			deletes.Add(1)
		}
		if req.URL.Path == "/api/me" {
			writeJSON(w, map[string]any{"username": "admin", "role": "admin", "auth_enabled": false})
			return
		}
		if req.URL.Path == "/api/plugins/deploy/api/application-references" {
			writeJSON(w, map[string]any{"complete": true, "applications": refs})
			return
		}
		if req.URL.Path == "/api/plugins" {
			writeJSON(w, []any{})
			return
		}
		if strings.HasPrefix(req.URL.Path, "/api/") {
			mux.ServeHTTP(w, req)
			return
		}
		if req.URL.Path == "/" {
			req.URL.Path = "/shell.html"
		}
		files.ServeHTTP(w, req)
	})}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve(listener)
	defer server.Close()
	t.Log("isolated catalog fixture http://" + server.Addr + "/?platform-page=1#/apps")
	time.Sleep(10 * time.Minute)
}
