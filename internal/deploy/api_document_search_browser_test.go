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

// Explicit localhost fixture uses the real read routes and an isolated database.
// No business execution or application/document write endpoint is reachable.
func TestAPIDocumentSearchBrowserFixture(t *testing.T) {
	port := os.Getenv("DOCUMENT_SEARCH_FIXTURE_PORT")
	if port == "" {
		t.Skip("opt-in isolated document search browser fixture")
	}
	st, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	for _, env := range []string{"dev", "test"} {
		if e = st.UpsertEnvironment(Environment{Name: env, DisplayName: map[string]string{"dev": "开发", "test": "测试"}[env]}); e != nil {
			t.Fatal(e)
		}
	}
	for _, name := range []string{"orders", "payments"} {
		if e = st.UpsertService(Service{Name: name, DisplayName: map[string]string{"orders": "订单服务", "payments": "支付服务"}[name], ArtifactType: "jar", BuildTool: "maven"}); e != nil {
			t.Fatal(e)
		}
	}
	add := func(app, env, summary string) {
		doc := fmt.Sprintf(`{"openapi":"3.0.3","info":{"title":%q,"version":"1.0"},"paths":{"/api/orders":{"post":{"summary":%q,"description":"按业务功能创建订单并校验客户资料","tags":["订单"],"requestBody":{"content":{"application/json":{"schema":{"type":"object","properties":{"customerId":{"type":"integer","description":"客户编号"}}}}}},"responses":{"200":{"description":"ok"}}}},"/api/health":{"get":{"summary":"健康查询","tags":["基础功能"],"responses":{"200":{"description":"ok"}}}}}}`, app, summary)
		var candidate map[string]any
		if json.Unmarshal([]byte(doc), &candidate) != nil {
			t.Fatal("fixture JSON")
		}
		info := candidate["info"].(map[string]any)
		operation := candidate["paths"].(map[string]any)["/api/orders"].(map[string]any)["post"].(map[string]any)
		if app == "orders" {
			info["contact"] = map[string]any{"name": "文档维护人"}
		}
		if app == "orders" && env == "test" {
			operation["x-maintainer"] = "接口维护人"
		}
		schema := operation["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
		props := schema["properties"].(map[string]any)
		props["customerId"].(map[string]any)["description"] = strings.Repeat("客户编号与业务校验说明，保持完整显示，不得覆盖相邻列。", 8) + " Validation[Positive(value=2147483647)]"
		props["longIdentifierWithoutWhitespace"] = map[string]any{"type": "string", "pattern": strings.Repeat("[A-Za-z0-9_]", 24), "description": "长连续约束"}
		props["buyer"] = map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string", "description": "嵌套姓名字段"}}}
		props["history"] = map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "integer", "description": "历史项编号"}}}}
		operation["parameters"] = []any{map[string]any{"name": "page", "in": "query", "schema": map[string]any{"type": "integer", "default": 1}, "description": "分页查询参数"}}
		operation["responses"].(map[string]any)["200"] = map[string]any{"description": "成功", "content": map[string]any{"application/json": map[string]any{"schema": schema}}}
		encoded, _ := json.Marshal(candidate)
		doc = string(encoded)

		if _, e = st.recordAPIDocument(st.GetService(app), APIDocumentSnapshot{Environment: env, Provider: "fixture", DefaultMaintainer: "fixture-key-user"}, []byte(doc)); e != nil {
			t.Fatal(e)
		}
	}
	add("orders", "test", "历史旧功能")
	add("orders", "test", "创建订房订单")
	add("orders", "dev", "创建开发订单")
	add("payments", "test", "创建支付订单")
	routes := Routes(st, NewBuilder(st, t.TempDir(), NewLogHub()), NewLogHub(), "", NewStepLib("../../steps"))
	files := http.FileServer(http.Dir("../../web/dist"))
	var mu sync.Mutex
	calls := []string{}
	messages := []json.RawMessage{}
	blocked := 0
	expired := false
	done := make(chan struct{})
	var once sync.Once
	server := &http.Server{Addr: "127.0.0.1:" + port, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__fixture/embedded" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head><body style="margin:0"><iframe title="接口检索隔离验收" style="display:block;width:100%;height:100dvh;border:0" src="/api-center.html#/documents"></iframe><script>addEventListener('message',e=>{if(e.origin===location.origin&&e.data?.type==='workspace.open')fetch('/__fixture/message',{method:'POST',body:JSON.stringify(e.data)})})</script></body></html>`)
			return
		}
		if r.URL.Path == "/__fixture/message" {
			var msg json.RawMessage
			json.NewDecoder(r.Body).Decode(&msg)
			mu.Lock()
			messages = append(messages, msg)
			mu.Unlock()
			fmt.Fprint(w, `{}`)
			return
		}
		if r.URL.Path == "/__fixture/stop" {
			once.Do(func() { close(done) })
			fmt.Fprint(w, `{}`)
			return
		}
		if r.URL.Path == "/__fixture/expire" {
			mu.Lock()
			expired = !expired
			mu.Unlock()
			fmt.Fprint(w, `{}`)
			return
		}
		if r.URL.Path == "/__fixture/state" {
			mu.Lock()
			defer mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{"calls": calls, "messages": messages, "blocked": blocked})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			mu.Lock()
			calls = append(calls, r.Method+" "+r.URL.RequestURI())
			denied := expired && r.URL.Path == "/api/api-documents/search"
			if r.Method != "GET" {
				blocked++
			}
			mu.Unlock()
			if r.Method != "GET" {
				http.Error(w, `{"error":"fixture blocks writes and execution"}`, 403)
				return
			}
			if denied {
				http.Error(w, `{"error":"登录已过期，请重新登录"}`, 401)
				return
			}
		}
		if r.URL.Path == "/api/me" {
			json.NewEncoder(w).Encode(map[string]any{"username": "fixture", "role": "admin", "auth_enabled": true})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
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
	t.Log("document search fixture http://" + server.Addr + "/__fixture/embedded")
	select {
	case <-done:
	case <-time.After(30 * time.Minute):
	}
}
