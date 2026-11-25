package deploy

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// An opt-in, disposable source and platform database. Only the mark endpoints
// may mutate metadata; all deployment and source mutation routes are blocked.
func TestResearchReleaseMarkBrowserFixture(t *testing.T) {
	port := os.Getenv("RELEASE_MARK_FIXTURE_PORT")
	if port == "" {
		t.Skip("opt-in release mark fixture")
	}
	s, _ := researchLiveFixture(t, 1)
	items := registerPendingFixture(t, s, 1)
	if _, err := s.db.Exec(`INSERT INTO research_work_relations(work_item_id,actor,kind,created_at) VALUES(?,'alice','participant',1)`, items[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AuthorizeResearch(context.Background(), "issues", "alice", ResearchCredential{Account: "alice", Password: "alice-secret"}); err != nil {
		t.Fatal(err)
	}
	hub := NewLogHub()
	routes := Routes(s, NewBuilder(s, s.dataDir, hub), hub, "", NewStepLib("../../steps"))
	files := http.FileServer(http.Dir("../../web/dist"))
	stop := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	fault := ""
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__fixture/stop" {
			once.Do(func() { close(stop) })
			w.Write([]byte("stopped"))
			return
		}
		if r.URL.Path == "/__fixture/fault" {
			mu.Lock()
			fault = r.URL.Query().Get("kind")
			mu.Unlock()
			return
		}
		if r.URL.Path == "/__fixture/state" {
			mark, _ := s.researchReleaseMark(items[0].ID)
			rows, _ := s.ResearchActivities(items[0].ID)
			deliveryJSON(w, map[string]any{"mark": mark, "activity": rows})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/plugins/research/api/") {
			r.URL.Path = strings.TrimPrefix(r.URL.Path, "/api/plugins/research")
		}
		if strings.HasPrefix(r.URL.Path, "/api/plugins/research/assets/") {
			r.URL.Path = "/assets/" + strings.TrimPrefix(r.URL.Path, "/api/plugins/research/assets/")
			files.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/api/plugins/research/ui" {
			r.URL.Path = "/research.html"
			files.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/api/me" {
			deliveryJSON(w, map[string]any{"username": "alice", "display_name": "验收成员", "role": "member", "auth_enabled": true})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			r.Header.Set("X-User", "alice")
			r.Header.Set("X-Role", "member")
			r.Header.Set("X-Reviewer-Directory", `["alice"]`)
			read := r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/research/")
			write := r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/release-mark")
			if write {
				mu.Lock()
				f := fault
				mu.Unlock()
				if f == "expired" {
					deliveryWriteError(w, 401, "登录已过期，说明已保留")
					return
				}
				if f == "conflict" {
					deliveryWriteError(w, 409, "上线标记已变化，请重新打开核对后操作")
					return
				}
			}
			if read || write {
				routes.ServeHTTP(w, r)
				return
			}
			if r.Method == "GET" {
				json.NewEncoder(w).Encode([]any{})
				return
			}
			http.Error(w, "fixture blocks business execution", 403)
			return
		}
		files.ServeHTTP(w, r)
	})
	listener, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go server.Serve(listener)
	t.Logf("Release mark fixture: http://127.0.0.1:%s/research.html#/work?quick=release&connection=issues", port)
	select {
	case <-stop:
	case <-time.After(20 * time.Minute):
		t.Error("fixture expired")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	server.Shutdown(ctx)
}
