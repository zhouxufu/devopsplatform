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

// Only document metadata is writable. No build, application/API execution or
// external resource can be called by this opt-in disposable browser fixture.
func TestAPIDocumentWorkflowBrowserFixture(t *testing.T) {
	port := os.Getenv("DOCUMENT_WORKFLOW_FIXTURE_PORT")
	if port == "" {
		t.Skip("opt-in document workflow browser fixture")
	}
	st, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	st.UpsertService(Service{Name: "orders", DisplayName: "订单服务", ArtifactType: "jar"})
	st.UpsertEnvironment(Environment{Name: "test", DisplayName: "测试"})
	item := WorkItem{Source: "manual", Kind: "requirement", Title: "订单状态查询 · 合成验收", Owner: "alice", AssignedTo: "alice"}
	if e = st.CreateWorkItem(&item); e != nil {
		t.Fatal(e)
	}
	raw := `{"openapi":"3.0.3","info":{"title":"订单文档","version":"1.0"},"paths":{"/orders":{"parameters":[{"name":"page","in":"query","schema":{"type":"integer"}}],"get":{"summary":"查询订单","tags":["订单"],"responses":{"200":{"description":"成功","content":{"application/json":{"schema":{"$ref":"#/components/schemas/Order"}}}}}},"post":{"summary":"创建订单","requestBody":{"content":{"application/json":{"schema":{"type":"object","properties":{"name":{"type":"string","description":"订单名"}}}}}},"responses":{"201":{"description":"成功"}}}}},"components":{"schemas":{"Order":{"type":"object","properties":{"id":{"type":"integer","example":9223372036854775807},"buyer":{"type":"object","properties":{"name":{"type":"string"}}},"history":{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"}}}}}}}}}`
	first, e := st.recordAPIDocument(st.GetService("orders"), APIDocumentSnapshot{Environment: "test"}, []byte(raw))
	if e != nil {
		t.Fatal(e)
	}
	hub := NewLogHub()
	routes := Routes(st, NewBuilder(st, t.TempDir(), hub), hub, "", NewStepLib("../../steps"))
	files := http.FileServer(http.Dir("../../web/dist"))
	done := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	fault := ""
	writes := 0
	blocked := 0
	messages := []json.RawMessage{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/__fixture/stop":
			once.Do(func() { close(done) })
			fmt.Fprint(w, "stopped")
			return
		case "/__fixture/fault":
			mu.Lock()
			fault = r.URL.Query().Get("kind")
			mu.Unlock()
			fmt.Fprint(w, "set")
			return
		case "/__fixture/message":
			var m json.RawMessage
			json.NewDecoder(r.Body).Decode(&m)
			mu.Lock()
			messages = append(messages, m)
			mu.Unlock()
			fmt.Fprint(w, "{}")
			return
		case "/__fixture/state":
			var count int
			st.db.QueryRow(`SELECT COUNT(*) FROM api_document_versions`).Scan(&count)
			mu.Lock()
			defer mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{"count": count, "writes": writes, "blocked": blocked, "messages": messages, "first": first, "work_item": item.ID})
			return
		case "/__fixture/conflict":
			_, b, e := st.documentBaseline(st.GetService("orders"), "test")
			if e != nil {
				t.Error(e)
				return
			}
			latest, _ := st.apiDocumentSnapshot(st.GetService("orders").ApplicationID, b.ID)
			next := strings.Replace(string(latest.Document), `"version":"1.0"`, `"version":"1.1"`, 1)
			next = strings.Replace(next, `"version": "1.0"`, `"version": "1.1"`, 1)
			_, e = st.recordAPIDocument(st.GetService("orders"), APIDocumentSnapshot{Environment: "test", Provider: "openapi-edit"}, []byte(next), b.ID)
			if e != nil {
				t.Error(e)
			}
			fmt.Fprint(w, "updated")
			return
		case "/__fixture/embedded":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1"></head><body style="margin:0"><iframe title="接口编辑验收" style="display:block;width:100%%;height:100dvh;border:0" src="/api-center.html#/documents?service=orders&environment=test&snapshot=%d&operation=get%%20%%2Forders&work_item=%d"></iframe><script>addEventListener('message',e=>{if(e.origin===location.origin&&e.data?.type==='workspace.open')fetch('/__fixture/message',{method:'POST',body:JSON.stringify(e.data)})})</script></body></html>`, first, item.ID)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/plugins/research/api/") {
			r.URL.Path = strings.TrimPrefix(r.URL.Path, "/api/plugins/research")
		}
		if strings.HasPrefix(r.URL.Path, "/api/plugins/api-center/api/") {
			r.URL.Path = strings.TrimPrefix(r.URL.Path, "/api/plugins/api-center")
		}
		if r.URL.Path == "/api/plugins/research/ui" {
			r.URL.Path = "/research.html"
			files.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/plugins/research/assets/") {
			r.URL.Path = "/assets/" + strings.TrimPrefix(r.URL.Path, "/api/plugins/research/assets/")
			files.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/api/me" {
			json.NewEncoder(w).Encode(map[string]any{"username": "alice", "display_name": "合成验收", "role": "admin", "auth_enabled": true})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			r.Header.Set("X-User", "alice")
			r.Header.Set("X-Role", "admin")
			allowed := r.Method == "GET" || r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/api/api-documents/") && (strings.HasSuffix(r.URL.Path, "/edit") || strings.HasSuffix(r.URL.Path, "/edit-preview"))
			mu.Lock()
			f := fault
			if !allowed {
				blocked++
			}
			if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/edit") {
				writes++
			}
			mu.Unlock()
			if !allowed {
				http.Error(w, `{"error":"fixture blocks execution"}`, 403)
				return
			}
			if f == "expired" && r.Method == "POST" {
				http.Error(w, `{"error":"登录已过期，草稿保留"}`, 401)
				return
			}
			routes.ServeHTTP(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})
	ln, e := net.Listen("tcp", "127.0.0.1:"+port)
	if e != nil {
		t.Fatal(e)
	}
	srv := &http.Server{Handler: handler}
	defer srv.Close()
	go srv.Serve(ln)
	t.Log("document workflow fixture http://" + ln.Addr().String() + "/__fixture/embedded")
	select {
	case <-done:
	case <-time.After(20 * time.Minute):
		t.Error("browser fixture timed out without stop")
	}
}
