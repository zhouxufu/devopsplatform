package deploy

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Explicit, isolated UI fixture: no repository, build executor or business write
// can run. Control URLs only advance synthetic log records in a temporary DB.
func TestBuildLogBrowserFixture(t *testing.T) {
	port := os.Getenv("BUILD_LOG_FIXTURE_PORT")
	if port == "" {
		t.Skip("opt-in isolated build log browser fixture")
	}
	st, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	if e = st.UpsertService(Service{Name: "fixture-orders", DisplayName: "日志验收应用", ArtifactType: "jar", BuildTool: "maven"}); e != nil {
		t.Fatal(e)
	}
	hub := NewLogHub()
	routes := Routes(st, NewBuilder(st, t.TempDir(), hub), hub, "", NewStepLib("../../steps"))
	run := &PipelineRun{Service: "fixture-orders", Actor: "fixture", StartedAt: time.Now().Unix(), Branch: "fixture"}
	if e = st.CreateRun(run); e != nil {
		t.Fatal(e)
	}
	for _, line := range []string{"▶ 流水线 #1 fixture-orders（记录 ID 1）", "\n━━ 拉取源码（git-checkout）━━", "⬇ fetch + reset origin/HEAD"} {
		st.AppendRunLog(run.ID, line)
	}
	key := int64(1000000) + run.ID
	done := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	blocked := 0
	files := http.FileServer(http.Dir("../../web/dist"))
	server := &http.Server{Addr: "127.0.0.1:" + port, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/plugins/deploy/") {
			r.URL.Path = strings.TrimPrefix(r.URL.Path, "/api/plugins/deploy")
		}
		if r.URL.Path == "/ui" {
			r.URL.Path = "/deploy.html"
		}
		if r.URL.Path == "/__fixture/stop" {
			once.Do(func() { close(done) })
			fmt.Fprint(w, `{}`)
			return
		}
		if r.URL.Path == "/__fixture/advance" {
			for _, line := range []string{"HEAD is now at fixture-commit\n", "正在下载依赖\n", "正在下载依赖\n"} {
				st.AppendRunLog(run.ID, strings.TrimRight(line, "\n"))
				hub.emit(key, line)
			}
			hub.Close(key)
			fmt.Fprint(w, `{}`)
			return
		}
		if r.URL.Path == "/__fixture/finish" {
			line := "✗ 平台运行用户无法读取 Maven settings.xml，请检查该文件和父目录的读取权限；未修改文件权限\n"
			st.AppendRunLog(run.ID, strings.TrimRight(line, "\n"))
			hub.emit(key, line)
			st.FinishRun(run.ID, "failed")
			hub.Close(key)
			fmt.Fprint(w, `{}`)
			return
		}
		if r.URL.Path == "/__fixture/state" {
			mu.Lock()
			defer mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{"blocked_writes": blocked, "run_id": run.ID, "status": st.GetRun(run.ID).Status})
			return
		}
		if r.URL.Path == "/api/me" {
			json.NewEncoder(w).Encode(map[string]any{"username": "fixture", "role": "admin", "auth_enabled": true})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if r.Method != "GET" {
				mu.Lock()
				blocked++
				mu.Unlock()
				http.Error(w, `{"error":"fixture blocks business execution"}`, 403)
				return
			}
			r.Header.Set("X-User", "fixture")
			r.Header.Set("X-Role", "admin")
			routes.ServeHTTP(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})}
	ln, e := net.Listen("tcp", server.Addr)
	if e != nil {
		t.Fatal(e)
	}
	defer server.Close()
	go server.Serve(ln)
	t.Log("build log fixture http://" + server.Addr + "/deploy.html#/run/1")
	select {
	case <-done:
	case <-time.After(30 * time.Minute):
	}
}
