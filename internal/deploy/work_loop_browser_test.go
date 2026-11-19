package deploy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWorkLoopBrowserFixture(t *testing.T) {
	port := os.Getenv("WORK_LOOP_FIXTURE_PORT")
	if port == "" {
		t.Skip("opt-in local task fixture")
	}
	s, routes, task, _ := workLoopFixture(t, "requirement")
	bug := &WorkItem{Source: "manual", Kind: "bug", Owner: "alice", AssignedTo: "alice", Title: "合成 Bug · 跟进界面"}
	if e := s.CreateWorkItem(bug); e != nil {
		t.Fatal(e)
	}
	files := http.FileServer(http.Dir("../../web/dist"))
	stop := make(chan struct{})
	var once sync.Once
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__fixture/stop" {
			once.Do(func() { close(stop) })
			fmt.Fprint(w, "stopped")
			return
		}
		r.Header.Set("X-User", "alice")
		r.Header.Set("X-Role", "member")
		r.Header.Set("X-Work-Loop-Authority", "page")
		r.Header.Set("X-Reviewer-Directory", `["alice","reviewer"]`)
		for _, plugin := range []string{"research", "deploy"} {
			if strings.HasPrefix(r.URL.Path, "/api/plugins/"+plugin+"/api/") {
				r.URL.Path = strings.TrimPrefix(r.URL.Path, "/api/plugins/"+plugin)
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/plugins/research/assets/") {
			r.URL.Path = "/assets/" + strings.TrimPrefix(r.URL.Path, "/api/plugins/research/assets/")
		}
		if r.URL.Path == "/api/plugins/deploy/ui" {
			r.URL.Path = "/deploy.html"
		}
		if r.URL.Path == "/api/plugins/research/ui" {
			r.URL.Path = "/research.html"
		}
		if strings.HasPrefix(r.URL.Path, "/api/resources/services/") {
			r.URL.Path = "/api/" + strings.TrimPrefix(r.URL.Path, "/api/resources/")
		}
		if r.URL.Path == "/api/me" {
			deliveryJSON(w, map[string]any{"username": "alice", "role": "member", "display_name": "本地验收", "auth_enabled": true})
			return
		}
		// Metadata-only fixture. Prevent any nested dispatch by blocking entry to
		// advance; the real coordinator is covered with disposable engine tests.
		if r.Method != "GET" {
			allowed := r.URL.Path == "/api/research/attention" || r.URL.Path == "/api/work-loops" || r.URL.Path == "/api/work-loops/preview" || strings.HasPrefix(r.URL.Path, "/api/work-loops/") && (strings.HasSuffix(r.URL.Path, "/control") || strings.HasSuffix(r.URL.Path, "/authorization") || strings.HasSuffix(r.URL.Path, "/authorization/preview"))
			if !allowed {
				deliveryWriteError(w, 403, "隔离界面验收不执行交付")
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			routes.ServeHTTP(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})
	l, e := net.Listen("tcp", "127.0.0.1:"+port)
	if e != nil {
		t.Fatal(e)
	}
	server := &http.Server{Handler: h}
	go server.Serve(l)
	t.Logf("http://127.0.0.1:%s/research.html#/work/%d ; bug=%d", port, task.Grant.Spec.WorkItemID, bug.ID)
	select {
	case <-stop:
	case <-time.After(30 * time.Minute):
		t.Error("fixture expired")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if e = server.Shutdown(ctx); e != nil {
		t.Error(e)
	}
}
