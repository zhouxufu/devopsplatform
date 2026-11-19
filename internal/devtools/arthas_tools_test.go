package devtools

import (
	"context"
	"encoding/json"
	"logplatform/internal/monitor"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestArthasMCPBoundRoleScopeAndTarget(t *testing.T) {
	const instance = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	var gets, posts atomic.Int32
	var mismatch atomic.Bool
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-User") != "alice" || r.Header.Get("X-Role") != "admin" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("invalid bound identity or leaked credentials")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" {
			gets.Add(1)
			if r.URL.Query().Get("service") != "orders" || r.URL.Query().Get("environment") != "test" {
				t.Error("unscoped instance query")
			}
			json.NewEncoder(w).Encode([]any{map[string]any{"id": instance, "name": "worker", "service": "orders", "environment": "test", "revision": 2, "has_credential": true, "endpoint": "http://private-host:8563", "password": "must-not-return"}, map[string]any{"id": "out-of-scope", "service": "orders", "environment": "prod", "revision": 2}})
			return
		}
		posts.Add(1)
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b["target_id"] != instance || b["service"] != "orders" || b["environment"] != "test" || b["diagnostic"] != "memory" || b["command"] != nil || b["password"] != nil {
			t.Error("unexpected diagnostic request", b)
		}
		env := "test"
		if mismatch.Load() {
			env = "prod"
		}
		json.NewEncoder(w).Encode(map[string]any{"state": "success", "target": map[string]any{"id": instance, "service": "orders", "environment": env, "revision": 2}, "rows": []any{map[string]any{"label": "used", "value": "128"}}})
	}))
	defer up.Close()
	svc := NewToolService(Upstreams{Monitor: up.URL})
	token := &AccessToken{Owner: "alice", verified: true, role: "admin", Scopes: []string{"arthas:read"}, Applications: []string{"orders"}, Environments: []string{"test"}}
	args := map[string]any{"service": "orders", "environment": "test"}
	v, _, _, err := svc.Call(context.Background(), token, "list_arthas_instances", args)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(v)
	if strings.Contains(string(raw), "private-host") || strings.Contains(string(raw), "must-not-return") || strings.Contains(string(raw), "out-of-scope") {
		t.Fatal("instance metadata leaked", string(raw))
	}
	full := map[string]any{"service": "orders", "environment": "test", "instance_id": instance, "revision": 2, "diagnostic": "memory"}
	if _, _, _, err = svc.Call(context.Background(), token, "diagnose_jvm", full); err != nil {
		t.Fatal(err)
	}
	if posts.Load() != 1 {
		t.Fatal("diagnosis replayed")
	}
	for _, change := range []map[string]any{{"service": "billing"}, {"environment": "prod"}, {"instance_id": "out-of-scope"}, {"revision": 1}, {"diagnostic": "ognl"}, {"command": "sysenv"}, {"password": "unexpected"}} {
		input := map[string]any{}
		for k, v := range full {
			input[k] = v
		}
		for k, v := range change {
			input[k] = v
		}
		before := posts.Load()
		if _, _, _, err := svc.Call(context.Background(), token, "diagnose_jvm", input); err == nil {
			t.Fatal("invalid diagnostic allowed", change)
		}
		if posts.Load() != before {
			t.Fatal("invalid diagnostic sent", change)
		}
	}
	for _, bad := range []AccessToken{
		{Owner: "alice", role: "admin", Scopes: []string{"arthas:read"}},
		{Owner: "alice", verified: true, role: "member", Scopes: []string{"arthas:read"}},
		{Owner: "", verified: true, role: "admin", Scopes: []string{"arthas:read"}},
		{Owner: "alice", verified: true, role: "admin", Scopes: []string{"metrics:read"}},
		{Owner: "alice", verified: true, role: "admin", Scopes: []string{"*"}},
	} {
		bad.Applications = []string{"orders"}
		bad.Environments = []string{"test"}
		before := gets.Load()
		if _, _, _, err := svc.Call(context.Background(), &bad, "list_arthas_instances", args); err == nil {
			t.Fatal("unbound, member or old token obtained diagnostic access")
		}
		if gets.Load() != before {
			t.Fatal("unauthorized request reached monitor")
		}
		for _, tool := range svc.VisibleTools(&bad) {
			if isArthasTool(tool.Name) {
				t.Fatal("diagnostics advertised without authorization")
			}
		}
	}
	mismatch.Store(true)
	if _, _, _, err = svc.Call(context.Background(), token, "diagnose_jvm", full); err == nil {
		t.Fatal("mismatched result accepted")
	}
}

func TestArthasTokenExplicitAdminAuthorization(t *testing.T) {
	store, _ := OpenTokenStore(t.TempDir())
	if _, _, err := store.Create(CreateTokenRequest{Name: "legacy", Scopes: []string{"arthas:read"}, Applications: []string{"*"}, Environments: []string{"*"}}); err == nil {
		t.Fatal("unbound diagnostic token created")
	}
	h := Routes(store, NewToolService(Upstreams{}))
	for _, role := range []string{"member", "admin"} {
		r := httptest.NewRequest("POST", "/api/tokens", strings.NewReader(`{"name":"diagnostics","scopes":[" arthas:read "],"applications":["orders"],"environments":["test"]}`))
		r.Header.Set("X-User", "alice")
		r.Header.Set("X-Role", role)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := 403
		if role == "admin" {
			want = 201
		}
		if w.Code != want {
			t.Fatal(role, w.Code, w.Body.String())
		}
	}
}

func TestArthasMCPDoesNotReplayUnknownDiagnostic(t *testing.T) {
	var executions atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			json.NewEncoder(w).Encode([]any{map[string]any{"id": "one", "service": "orders", "environment": "test", "revision": 1}})
			return
		}
		executions.Add(1)
		c, _, _ := w.(http.Hijacker).Hijack()
		c.Close()
	}))
	defer up.Close()
	svc := NewToolService(Upstreams{Monitor: up.URL})
	token := &AccessToken{Owner: "alice", verified: true, role: "admin", Scopes: []string{"arthas:read"}, Applications: []string{"orders"}, Environments: []string{"test"}}
	_, _, _, err := svc.Call(context.Background(), token, "diagnose_jvm", map[string]any{"service": "orders", "environment": "test", "instance_id": "one", "revision": 1, "diagnostic": "memory"})
	if err == nil || executions.Load() != 1 {
		t.Fatal("unknown result replayed or accepted", executions.Load(), err)
	}
}

func TestArthasMCPToRealMonitorWithSavedAuthentication(t *testing.T) {
	var executions atomic.Int32
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "arthas" || password != "fixture-password" || r.Header.Get("X-User") != "" || r.Header.Get("Cookie") != "" {
			t.Error("incorrect agent authentication or leaked platform identity")
			w.WriteHeader(401)
			return
		}
		var in map[string]any
		json.NewDecoder(r.Body).Decode(&in)
		out := map[string]any{"state": "SUCCEEDED"}
		if in["action"] == "init_session" {
			out["sessionId"] = "private-session"
			out["consumerId"] = "private-consumer"
		}
		if in["action"] == "exec" {
			executions.Add(1)
			if in["command"] != "memory" {
				t.Error("unexpected command")
			}
			out["body"] = map[string]any{"jobStatus": "TERMINATED", "results": []any{map[string]any{"type": "memory", "memoryInfo": map[string]any{"heap": []any{map[string]any{"name": "heap", "used": 128, "max": 256}}}}, map[string]any{"type": "status", "statusCode": 0}}}
		}
		json.NewEncoder(w).Encode(out)
	}))
	defer agent.Close()
	st, err := monitor.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	up := httptest.NewServer(monitor.Routes(st, nil))
	defer up.Close()
	body, _ := json.Marshal(map[string]any{"name": "worker", "service": "orders", "environment": "test", "endpoint": agent.URL, "credential": map[string]any{"username": "arthas", "password": "fixture-password"}})
	req, _ := http.NewRequest("POST", up.URL+"/api/arthas/targets", strings.NewReader(string(body)))
	req.Header.Set("X-User", "alice")
	req.Header.Set("X-Role", "admin")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var target map[string]any
	json.NewDecoder(res.Body).Decode(&target)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal(target)
	}
	store, _ := OpenTokenStore(t.TempDir())
	token, secret, err := store.Create(CreateTokenRequest{Owner: "alice", Name: "diagnostic", Scopes: []string{"arthas:read"}, Applications: []string{"orders"}, Environments: []string{"test"}})
	if err != nil {
		t.Fatal(err)
	}
	h := NewMCPHandler(store, NewToolService(Upstreams{Monitor: up.URL}))
	rpc := func(role string) *httptest.ResponseRecorder {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "diagnose_jvm", "arguments": map[string]any{"service": "orders", "environment": "test", "instance_id": target["id"], "revision": target["revision"], "diagnostic": "memory"}}})
		r := httptest.NewRequest("POST", "/mcp", strings.NewReader(string(b)))
		r.Header.Set("Authorization", "Bearer "+secret)
		r.Header.Set("X-MCP-Users", `[{"username":"alice","enabled":true,"role":"`+role+`"}]`)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := rpc("admin")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"isError":false`) || executions.Load() != 1 || !strings.Contains(w.Body.String(), "128.00 B") {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, value := range []string{"fixture-password", agent.URL, "private-session", "private-consumer"} {
		if strings.Contains(w.Body.String(), value) {
			t.Fatal("MCP exposed credential or agent destination")
		}
	}
	w = rpc("member")
	if executions.Load() != 1 || !strings.Contains(w.Body.String(), `"isError":true`) {
		t.Fatal("role downgrade did not revoke access", w.Body.String())
	}
	if err := store.Revoke(token.ID); err != nil {
		t.Fatal(err)
	}
	if w = rpc("admin"); w.Code != 401 || executions.Load() != 1 {
		t.Fatal("revoked token executed diagnosis")
	}
}
