package deploy

import (
	"context"
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

// Only temporary configuration saves and synthetic reconciliation are reachable.
// Pipeline creation, actual publication and target operations are blocked.
func TestScriptPublicationBrowserFixture(t *testing.T) {
	port := os.Getenv("SCRIPT_PUBLICATION_FIXTURE_PORT")
	if port == "" {
		t.Skip("opt-in isolated script publication browser fixture")
	}
	s, _, a := scriptFixture(t, successfulScriptFixture)
	sv := *s.GetService("orders")
	sv.DisplayName = "订单服务"
	if err := s.UpsertService(sv); err != nil {
		t.Fatal(err)
	}
	routes := Routes(s, NewBuilder(s, t.TempDir(), NewLogHub()), NewLogHub(), "", NewStepLib("../../steps"))
	target := *s.GetServiceEnvironment("orders", "test")
	o := &ReleaseOrder{Service: "orders", ArtifactID: a.ID, Version: a.Version, Environment: "test", Reason: "Synthetic interrupted script", Strategy: "rolling", Status: "failed", BatchSize: 1, Hosts: target.ExecutionTargets(), Target: target, Actor: "fixture", StartedAt: time.Now().Unix()}
	if err := s.CreateReleaseOrder(o); err != nil {
		t.Fatal(err)
	}
	d := &Deploy{Service: "orders", ArtifactID: a.ID, ArtifactDigest: a.Digest, Version: a.Version, Environment: "test", Host: target.ExecutionTargets()[0], Actor: "fixture", StartedAt: time.Now().Unix()}
	if err := s.CreateDeploy(d); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishDeploy(d.ID, "interrupted"); err != nil {
		t.Fatal(err)
	}
	if err := s.AttachReleaseDeploys(o.ID, 1, []int64{d.ID}); err != nil {
		t.Fatal(err)
	}
	claim, _ := json.Marshal(scriptPublicationClaim{d.ID, "orders", "test", a.ID, a.Digest, digestJSON(target.DeployConfig)})
	for _, k := range []string{scriptScopeKey("orders", "test"), scriptPublicationLock("orders", "test", target.DeployConfig)} {
		if _, err := s.db.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?)`, k, string(claim)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.saveScriptResult(d.ID, &ScriptPublicationResult{Status: "pending", ExternalID: "fixture-job-17", Values: map[string]string{"phase": "submitting", "savepoint": "fixture://savepoint/test"}}, nil); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	calls := []string{}
	writes, blocked := 0, 0
	expired := false
	done := make(chan struct{})
	var once sync.Once
	files := http.FileServer(http.Dir("../../web/dist"))
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/__fixture/embedded":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<!doctype html><html><meta name="viewport" content="width=device-width,initial-scale=1"><body style="margin:0"><div style="display:flex;gap:8px;padding:4px"><button onclick="theme('light')">浅色</button><button onclick="theme('dark')">深色</button></div><iframe title="脚本发布隔离验收" style="display:block;width:100%;height:calc(100dvh - 32px);border:0" src="/deploy.html#/svc/orders/environment"></iframe><script>function theme(v){document.querySelector('iframe').contentWindow.postMessage({type:'theme.sync',dark:v==='dark',mode:v},location.origin)}</script></body></html>`)
			return
		case "/__fixture/expire":
			mu.Lock()
			expired = !expired
			mu.Unlock()
			fmt.Fprint(w, `{}`)
			return
		case "/__fixture/state":
			mu.Lock()
			defer mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{"calls": calls, "configuration_writes": writes, "blocked_execution_requests": blocked, "release_id": o.ID})
			return
		case "/__fixture/stop":
			once.Do(func() { close(done) })
			fmt.Fprint(w, `{}`)
			return
		case "/api/me":
			json.NewEncoder(w).Encode(map[string]any{"username": "fixture", "role": "admin", "auth_enabled": true})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			allowed := r.Method == "GET" || (r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/api/services/orders/environments/") && strings.Count(r.URL.Path, "/") == 5) || (r.Method == "POST" && r.URL.Path == fmt.Sprintf("/api/releases/%d/script-result", o.ID))
			mu.Lock()
			calls = append(calls, r.Method+" "+r.URL.RequestURI())
			deny := expired && r.Method != "GET"
			if !allowed {
				blocked++
			} else if r.Method != "GET" && !deny {
				writes++
			}
			mu.Unlock()
			if !allowed {
				http.Error(w, `{"error":"fixture blocks all real execution"}`, 403)
				return
			}
			if deny {
				http.Error(w, `{"error":"登录已过期，请重新登录"}`, 401)
				return
			}
			r.Header.Set("X-User", "fixture")
			r.Header.Set("X-Role", "admin")
			routes.ServeHTTP(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(ln) }()
	t.Log("isolated fixture http://" + ln.Addr().String() + "/__fixture/embedded")
	select {
	case <-done:
	case <-time.After(30 * time.Minute):
		t.Error("fixture expired")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	server.Shutdown(ctx)
	<-finished
}
