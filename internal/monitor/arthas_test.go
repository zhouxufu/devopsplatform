package monitor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type arthasFixture struct {
	st           *Store
	h            http.Handler
	remote       *httptest.Server
	mu           sync.Mutex
	calls        []map[string]any
	mode, secret string
	leaked       bool
	execStarted  chan struct{}
	execRelease  chan struct{}
}

func newArthasFixture(t *testing.T) *arthasFixture {
	t.Helper()
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	f := &arthasFixture{st: s}
	f.remote = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		f.calls = append(f.calls, in)
		mode, secret := f.mode, f.secret
		started, release := f.execStarted, f.execRelease
		f.leaked = f.leaked || r.Header.Get("Cookie") != "" || r.Header.Get("X-User") != "" || r.Header.Get("X-Role") != ""
		n := len(f.calls)
		f.mu.Unlock()
		if secret != "" {
			user, pass, ok := r.BasicAuth()
			if !ok || user != "arthas" || pass != secret {
				w.WriteHeader(401)
				return
			}
		}
		if mode == "unauthorized" {
			w.WriteHeader(401)
			return
		}
		if mode == "redirect" {
			http.Redirect(w, r, "http://127.0.0.1:1/leak", 302)
			return
		}
		if mode == "malformed" {
			_, _ = w.Write([]byte("<html>private upstream details</html>"))
			return
		}
		if in["action"] == "init_session" {
			writeJSON(w, map[string]any{"state": "SUCCEEDED", "sessionId": fmt.Sprint("remote-", n), "consumerId": "private-consumer"})
			return
		}
		if in["action"] != "exec" {
			writeJSON(w, map[string]any{"state": "SUCCEEDED"})
			return
		}
		if mode == "blocked" {
			close(started)
			<-release
		}
		if mode == "structured" {
			writeJSON(w, map[string]any{"state": "SUCCEEDED", "body": map[string]any{"jobStatus": "TERMINATED", "results": []any{map[string]any{"type": "jvm", "jvmInfo": map[string]any{"RUNTIME": []any{map[string]any{"name": "JVM-VERSION", "value": "17.0"}, map[string]any{"name": "INPUT-ARGUMENTS", "value": "-Dpassword=hidden-value"}}}}, map[string]any{"type": "memory", "memoryInfo": map[string]any{"HEAP": []any{map[string]any{"name": "heap", "used": 128, "max": 256, "usage": "50%"}}}}, map[string]any{"type": "thread", "busyThreads": []any{map[string]any{"id": 42, "name": "worker", "state": "RUNNABLE", "cpu": 12}}}, map[string]any{"type": "status", "statusCode": 0}}}})
			return
		}
		if mode == "disconnect" {
			c, _, _ := w.(http.Hijacker).Hijack()
			_ = c.Close()
			return
		}
		if mode == "oversize" {
			_, _ = w.Write(bytes.Repeat([]byte("x"), (1<<20)+1))
			return
		}
		state, job := "SUCCEEDED", "TERMINATED"
		expired := false
		code := 0
		switch mode {
		case "failed":
			state = "FAILED"
			code = 1
		case "nonzero":
			code = 1
		case "timeout":
			expired = true
		case "running":
			job = "RUNNING"
		}
		writeJSON(w, map[string]any{"state": state, "body": map[string]any{"jobStatus": job, "timeExpired": expired, "results": []any{map[string]any{"type": "message", "message": "safe output <script>alert(1)</script> " + secret}, map[string]any{"type": "status", "statusCode": code}}}})
	}))
	f.h = s.arthasRoutes()
	t.Cleanup(func() { _ = s.Close(); f.remote.Close() })
	return f
}
func (f *arthasFixture) setMode(mode string) { f.mu.Lock(); f.mode = mode; f.mu.Unlock() }
func (f *arthasFixture) callCount() int      { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }
func (f *arthasFixture) request(t *testing.T, method, path, actor string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.Header.Set("X-User", actor)
	r.Header.Set("X-Role", "admin")
	r.Header.Set("Cookie", "lp_session=never-forward")
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	return w
}
func (f *arthasFixture) target(t *testing.T) arthasTarget {
	t.Helper()
	r := f.request(t, "POST", "/api/arthas/targets", "alice", arthasTarget{Name: "测试 JVM", Service: "orders", Environment: "test", Endpoint: f.remote.URL})
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	var target arthasTarget
	_ = json.Unmarshal(r.Body.Bytes(), &target)
	return target
}
func (f *arthasFixture) session(t *testing.T, target arthasTarget) string {
	t.Helper()
	r := f.request(t, "POST", "/api/arthas/sessions", "alice", map[string]any{"target_id": target.ID, "revision": target.Revision, "password": f.secret})
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	var s struct{ ID string }
	_ = json.Unmarshal(r.Body.Bytes(), &s)
	if strings.Contains(r.Body.String(), "remote-") || strings.Contains(r.Body.String(), "private-consumer") {
		t.Fatal("upstream session exposed")
	}
	return s.ID
}

func TestArthasAuthorizationAndFixedDestination(t *testing.T) {
	f := newArthasFixture(t)
	for _, role := range []string{"", "member"} {
		for _, method := range []string{"GET", "POST", "DELETE"} {
			r := httptest.NewRequest(method, "/api/arthas/targets", nil)
			r.Header.Set("X-Role", role)
			r.Header.Set("X-User", "member")
			w := httptest.NewRecorder()
			f.h.ServeHTTP(w, r)
			if w.Code != 403 {
				t.Fatal(role, method, w.Code)
			}
		}
	}
	r := httptest.NewRequest("GET", "/api/arthas/targets", nil)
	r.Header.Set("X-Role", "admin")
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("anonymous admin header accepted")
	}
	r = httptest.NewRequest("POST", "/api/arthas/sessions", strings.NewReader(`{}`))
	r.Header.Set("X-Role", "admin")
	r.Header.Set("X-User", "alice")
	r.Header.Set("Origin", "http://evil.invalid")
	w = httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross origin accepted")
	}
	for _, endpoint := range []string{"file:///tmp/private", "http://user:pass@localhost:8563", "http://localhost:8563/api", "http://localhost:8563/?password=x", "http://169.254.169.254", "http://[::]", "http://224.0.0.1", "http://localhost:99999", "http://localhost:", "http://localhost/#secret"} {
		target := arthasTarget{Name: "x", Service: "app", Environment: "test", Endpoint: endpoint}
		if validateArthasTarget(&target) == nil {
			t.Fatal("accepted", endpoint)
		}
	}
	for _, endpoint := range []string{"http://127.0.0.1:8563/", "https://private.example:8563", "http://192.168.0.2:8563"} {
		target := arthasTarget{Name: "x", Service: "app", Environment: "test", Endpoint: endpoint}
		if e := validateArthasTarget(&target); e != nil {
			t.Fatal(e)
		}
	}
	if arthasIPAllowed(net.ParseIP("fe80::1")) {
		t.Fatal("link local permitted")
	}
	if f.callCount() != 0 {
		t.Fatal("configuration or denied calls contacted remote")
	}
}
func TestArthasConfigurationCASAndAuditTransaction(t *testing.T) {
	f := newArthasFixture(t)
	target := f.target(t)
	if f.callCount() != 0 {
		t.Fatal("save connected automatically")
	}
	target.Name = "renamed"
	r := f.request(t, "PUT", "/api/arthas/targets/"+target.ID, "alice", target)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r = f.request(t, "PUT", "/api/arthas/targets/"+target.ID, "alice", target); r.Code != 409 {
		t.Fatal("stale write", r.Code)
	}
	if r = f.request(t, "GET", "/api/arthas/targets?service=elsewhere", "alice", nil); r.Body.String() != "[]\n" {
		t.Fatal("scope filter", r.Body.String())
	}
	if r = f.request(t, "POST", "/api/arthas/sessions", "alice", map[string]any{"target_id": target.ID, "revision": target.Revision}); r.Code != 409 {
		t.Fatal("stale connect", r.Code)
	}
	target.Revision++
	if r = f.request(t, "DELETE", "/api/arthas/targets/"+target.ID, "alice", map[string]any{"revision": target.Revision}); r.Code != 200 {
		t.Fatal(r.Code)
	}
	var deleted int
	if e := f.st.db.QueryRow(`SELECT deleted FROM arthas_targets WHERE id=?`, target.ID).Scan(&deleted); e != nil || deleted != 1 {
		t.Fatal("target not soft removed", e)
	}
	_, _ = f.st.db.Exec(`DROP TABLE arthas_audit`)
	if r = f.request(t, "POST", "/api/arthas/targets", "alice", arthasTarget{Name: "fail", Service: "app", Environment: "test", Endpoint: f.remote.URL}); r.Code != 500 {
		t.Fatal(r.Code)
	}
	var count int
	_ = f.st.db.QueryRow(`SELECT count(*) FROM arthas_targets`).Scan(&count)
	if count != 1 {
		t.Fatal("config committed without audit")
	}
}
func TestArthasSessionOwnershipCredentialsAndExecution(t *testing.T) {
	f := newArthasFixture(t)
	f.mu.Lock()
	f.secret = `p"ass\word`
	f.mu.Unlock()
	target := f.target(t)
	id := f.session(t, target)
	path := "/api/arthas/sessions/" + id
	if r := f.request(t, "POST", path+"/execute", "bob", map[string]any{"command": "version"}); r.Code != 404 {
		t.Fatal("cross-user session access", r.Code)
	}
	if r := f.request(t, "POST", path+"/execute", "alice", map[string]any{"command": "ognl sensitive"}); r.Code != 400 {
		t.Fatal("unconfirmed custom command", r.Code)
	}
	for _, command := range []string{"version", "ognl sensitive"} {
		r := f.request(t, "POST", path+"/execute", "alice", map[string]any{"command": command, "confirm": command != "version"})
		if r.Code != 200 {
			t.Fatal(r.Code, r.Body.String())
		}
		var out map[string]any
		_ = json.Unmarshal(r.Body.Bytes(), &out)
		if out["state"] != "success" || strings.Contains(fmt.Sprint(out["output"]), f.secret) || !strings.Contains(fmt.Sprint(out["output"]), "[REDACTED]") {
			t.Fatal("invalid result", out)
		}
	}
	if r := f.request(t, "PUT", "/api/arthas/targets/"+target.ID, "alice", target); r.Code != 409 {
		t.Fatal("active target changed", r.Code)
	}
	if r := f.request(t, "DELETE", "/api/arthas/targets/"+target.ID, "alice", map[string]any{"revision": target.Revision}); r.Code != 409 {
		t.Fatal("active target removed", r.Code)
	}
	audit := f.request(t, "GET", "/api/arthas/audit", "alice", nil).Body.String()
	if strings.Contains(audit, "sensitive") || strings.Contains(audit, f.secret) || strings.Contains(audit, "safe output") {
		t.Fatal("audit stored command/secret/output")
	}
	f.mu.Lock()
	leaked := f.leaked
	calls := append([]map[string]any{}, f.calls...)
	f.mu.Unlock()
	if leaked {
		t.Fatal("platform headers or cookies forwarded")
	}
	if len(calls) != 3 {
		t.Fatal("unexpected retries", len(calls))
	}
	for _, call := range calls {
		if call["action"] == "exec" && call["execTimeout"] != float64(10000) {
			t.Fatal("unbounded execution")
		}
	}
	r := f.request(t, "DELETE", path, "alice", nil)
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"remote_closed":true`) {
		t.Fatal(r.Code, r.Body.String())
	}
	if r = f.request(t, "POST", path+"/execute", "alice", map[string]any{"command": "version"}); r.Code != 404 {
		t.Fatal("closed session usable")
	}
}
func TestArthasFailuresDoNotReplayOrReportFalseSuccess(t *testing.T) {
	for _, mode := range []string{"failed", "nonzero", "timeout", "running", "disconnect", "oversize", "malformed", "unauthorized", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			f := newArthasFixture(t)
			id := f.session(t, f.target(t))
			f.setMode(mode)
			path := "/api/arthas/sessions/" + id
			r := f.request(t, "POST", path+"/execute", "alice", map[string]any{"command": "version"})
			var result map[string]any
			_ = json.Unmarshal(r.Body.Bytes(), &result)
			switch mode {
			case "failed", "nonzero":
				if r.Code != 200 || result["state"] != "failed" {
					t.Fatal(r.Code, result)
				}
			case "timeout":
				if result["state"] != "timeout" {
					t.Fatal(result)
				}
			case "running":
				if result["state"] != "unknown" {
					t.Fatal(result)
				}
			default:
				if r.Code != 502 || strings.Contains(r.Body.String(), "private upstream") {
					t.Fatal(r.Code, r.Body.String())
				}
			}
			if f.callCount() != 2 {
				t.Fatal("diagnostic retried", f.callCount())
			}
			if mode == "running" || r.Code == 502 {
				next := f.request(t, "POST", path+"/execute", "alice", map[string]any{"command": "version"})
				if next.Code != 409 || f.callCount() != 2 {
					t.Fatal("uncertain execution repeated", next.Code)
				}
				f.setMode("")
				if next = f.request(t, "POST", path+"/interrupt", "alice", nil); next.Code != 200 {
					t.Fatal(next.Code)
				}
				if next = f.request(t, "POST", path+"/execute", "alice", map[string]any{"command": "version"}); next.Code != 200 {
					t.Fatal("explicit recovery failed", next.Code)
				}
			}
		})
	}
}
func TestArthasLimitsExpiryAndCleanup(t *testing.T) {
	f := newArthasFixture(t)
	target := f.target(t)
	ids := []string{}
	for i := 0; i < 4; i++ {
		ids = append(ids, f.session(t, target))
	}
	if r := f.request(t, "POST", "/api/arthas/sessions", "alice", map[string]any{"target_id": target.ID, "revision": target.Revision}); r.Code != 429 {
		t.Fatal("session cap", r.Code)
	}
	m := f.st.arthas
	m.mu.Lock()
	s := m.sessions[ids[0]]
	s.mu.Lock()
	s.created = time.Now().Add(-16 * time.Minute)
	s.mu.Unlock()
	m.mu.Unlock()
	if r := f.request(t, "POST", "/api/arthas/sessions/"+ids[0]+"/execute", "alice", map[string]any{"command": "version"}); r.Code != 410 {
		t.Fatal("expired session", r.Code)
	}
	if r := f.request(t, "DELETE", "/api/arthas/sessions/"+ids[0], "alice", nil); r.Code != 200 {
		t.Fatal("cannot close expired session")
	}
	m.mu.Lock()
	for _, s := range m.sessions {
		s.mu.Lock()
		s.touched = time.Now().Add(-6 * time.Minute)
		s.mu.Unlock()
	}
	m.mu.Unlock()
	m.expire(time.Now())
	if len(m.sessions) != 0 {
		t.Fatal("idle session not evicted")
	}
	before := f.callCount()
	for i := 0; i < 2; i++ {
		r := f.request(t, "DELETE", "/api/arthas/sessions/"+ids[1], "alice", nil)
		if r.Code != 200 || !strings.Contains(r.Body.String(), `"remote_closed":false`) {
			t.Fatal("stale window cannot discard expired handle", r.Code, r.Body.String())
		}
	}
	if f.callCount() != before {
		t.Fatal("closing an expired handle must not contact another JVM")
	}
}
func TestArthasAuditRequiredBeforeCommand(t *testing.T) {
	f := newArthasFixture(t)
	id := f.session(t, f.target(t))
	before := f.callCount()
	_, _ = f.st.db.Exec(`DROP TABLE arthas_audit`)
	r := f.request(t, "POST", "/api/arthas/sessions/"+id+"/execute", "alice", map[string]any{"command": "version"})
	if r.Code != 500 || f.callCount() != before {
		t.Fatal("command sent without durable audit", r.Code)
	}
}
