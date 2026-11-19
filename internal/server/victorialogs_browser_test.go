package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"logplatform/internal/types"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestVictoriaLogsBrowserFixture(t *testing.T) {
	port := os.Getenv("VLOGS_BROWSER_PORT")
	if port == "" {
		t.Skip("opt-in temporary actual-backend UI fixture")
	}
	upstream, tenant := testVictoriaLogs(t)
	var offline atomic.Bool
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if offline.Load() {
			http.Error(w, "temporary fixture outage", 503)
			return
		}
		body, _ := io.ReadAll(r.Body)
		req, _ := http.NewRequestWithContext(r.Context(), r.Method, upstream+r.URL.RequestURI(), bytes.NewReader(body))
		req.Header = r.Header.Clone()
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			http.Error(w, "fixture backend unavailable", 503)
			return
		}
		defer resp.Body.Close()
		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}))
	defer proxy.Close()
	opts := DefaultStorageOptions()
	opts.URL = proxy.URL
	opts.Token = ""
	opts.ProjectID = tenant
	opts.RequestTimeout = 2 * time.Second
	s, err := OpenVictoriaLogs(t.TempDir(), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	api := NewAPI(s, "secret", "")
	routes := api.Routes()
	events := []types.Event{}
	base := time.Now().Add(-3 * time.Minute).UnixMilli()
	for i := 0; i < 330; i++ {
		level := "INFO"
		if i%5 == 0 {
			level = "ERROR"
		}
		events = append(events, types.Event{ID: fmt.Sprintf("ui-%03d", i), Ts: base + int64(i), Service: "orders", Environment: "test", Host: "fixture-node", Level: level, TraceID: fmt.Sprintf("trace-fixture-%03d", i), Message: fmt.Sprintf("订单查询 %03d timeout retry fixture\n at com.demo.Order.run(Order.java:5)", i)})
	}
	events = append(events, types.Event{ID: "quarantine-demo", Ts: 1, Service: "orders", Environment: "test", Message: "invalid timestamp fixture"})
	postIngest(t, api, types.IngestRequest{AgentID: "fixture-agent", AgentName: "隔离采集节点", BatchID: "ui-seed", Events: events})
	var expired atomic.Bool
	var mu sync.Mutex
	calls := []string{}
	blocked := 0
	done := make(chan struct{})
	var once sync.Once
	emitLive := func() {
		e := logFixtureEvent("ui-live-" + newLogID())
		e.Message = "实时补发验证 fixture-live"
		postIngest(t, api, types.IngestRequest{AgentID: "fixture-agent", BatchID: e.ID, Events: []types.Event{e}})
	}
	files := http.FileServer(http.Dir("../../web/dist"))
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/__fixture/embedded":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<!doctype html><meta name="viewport" content="width=device-width,initial-scale=1"><body style="margin:0"><div style="display:flex;gap:8px;padding:4px"><button onclick="go('/search')">搜索</button><button onclick="go('/live')">实时</button><button onclick="go('/status')">状态</button><button onclick="theme('light')">浅色</button><button onclick="theme('dark')">深色</button></div><iframe title="VictoriaLogs 隔离验收" style="border:0;width:100%;height:calc(100dvh - 32px)" src="/ui#/search"></iframe><script>function go(v){document.querySelector('iframe').contentWindow.location.hash=v}function theme(v){document.querySelector('iframe').contentWindow.postMessage({type:'theme.sync',mode:v,dark:v==='dark'},location.origin)}</script>`)
			return
		case "/__fixture/offline":
			offline.Store(!offline.Load())
			fmt.Fprint(w, `{}`)
			return
		case "/__fixture/expire":
			expired.Store(!expired.Load())
			fmt.Fprint(w, `{}`)
			return
		case "/__fixture/live":
			emitLive()
			fmt.Fprint(w, `{}`)
			return
		case "/__fixture/state":
			mu.Lock()
			defer mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{"calls": calls, "blocked": blocked, "storage": s.StorageStatus()})
			return
		case "/__fixture/stop":
			once.Do(func() { close(done) })
			fmt.Fprint(w, `{}`)
			return
		case "/api/me":
			writeJSON(w, map[string]any{"username": "fixture-admin", "role": "admin", "auth_enabled": true})
			return
		case "/api/extensions", "/api/plugins":
			writeJSON(w, []any{})
			return
		case "/ui":
			http.ServeFile(w, r, "../../web/dist/logs.html")
			return
		}
		if len(r.URL.Path) > 5 && r.URL.Path[:5] == "/api/" {
			mu.Lock()
			calls = append(calls, r.Method+" "+r.URL.RequestURI())
			allowed := r.Method == "GET"
			if !allowed {
				blocked++
			}
			mu.Unlock()
			if !allowed {
				http.Error(w, `{"error":"fixture forbids remote operations and settings writes"}`, 403)
				return
			}
			if expired.Load() {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(401)
				fmt.Fprint(w, `{"error":"登录已过期，请重新登录"}`)
				return
			}
			r.Header.Set("X-Role", "admin")
			r.Header.Set("X-User", "fixture-admin")
			routes.ServeHTTP(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})
	ln, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: h}
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(ln) }()
	t.Log("actual-backend UI fixture http://" + ln.Addr().String() + "/__fixture/embedded")
	select {
	case <-done:
	case <-time.After(30 * time.Minute):
		t.Error("fixture expired")
	}
	api.hub.mu.RLock()
	subs := []*Subscriber{}
	for sub := range api.hub.subs {
		subs = append(subs, sub)
	}
	api.hub.mu.RUnlock()
	for _, sub := range subs {
		api.hub.Unsubscribe(sub)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	server.Shutdown(ctx)
	<-finished
}
