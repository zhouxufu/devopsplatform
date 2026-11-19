package devtools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTestingMCPScopesPreviewDriftConfirmationAndOnce(t *testing.T) {
	var writes atomic.Int32
	var drift atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-User") != "alice" {
			t.Error("identity lost")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" {
			revision := 1
			if drift.Load() {
				revision = 2
			}
			json.NewEncoder(w).Encode(map[string]any{"revision": revision, "enabled": true, "protected": false, "variables_digest": "private-digest", "targets": []any{map[string]any{"key": "endpoint:gateway", "fingerprint": "target-v1", "base_url": "https://api.invalid/gateway", "login_url": "https://login.invalid/login"}}, "profile": map[string]any{"revision": revision, "enabled": true, "auth": map[string]any{"mode": "login", "path": "/login", "body": `{"password":"{{var.PASS}}"}`, "token_pointer": "/token"}}})
			return
		}
		writes.Add(1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path == "/api/api-testing/mcp/runs" {
			if body["expected_variables_digest"] != "private-digest" || body["verify_auth"] != true {
				t.Error("missing frozen execution contract")
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"id": 17, "service": "orders", "environment": "test", "status": "queued"})
	}))
	defer upstream.Close()
	store, _ := OpenTokenStore(t.TempDir())
	svc := NewToolService(Upstreams{Deploy: upstream.URL})
	svc.actions = store
	tok := workflowToken(t, store)
	tok.Scopes = append(tok.Scopes, "apitests:read", "apitests:write", "apitests:execute")
	tok.role = "admin"
	call := func(name string, args map[string]any) any {
		t.Helper()
		v, _, _, e := svc.Call(context.Background(), tok, name, args)
		if e != nil {
			t.Fatal(name, e)
		}
		return v
	}
	scope := map[string]any{"service": "orders", "environment": "test"}
	profile := mapValue(call("get_api_test_profile", scope))
	if stringArg(mapValue(mapValue(profile["profile"])["auth"]), "token_pointer") != "/token" {
		t.Fatal("structural pointer redacted")
	}
	args := map[string]any{"service": "orders", "environment": "test", "target_key": "endpoint:gateway", "target_fingerprint": "target-v1"}
	preview := mapValue(call("verify_api_authentication", args))
	if writes.Load() != 0 {
		t.Fatal("preview performed login")
	}
	id := preview["confirmation_id"]
	for i := 0; i < 2; i++ {
		call("confirm_platform_action", map[string]any{"confirmation_id": id, "confirm": true, "reason": "verify login"})
	}
	if writes.Load() != 1 {
		t.Fatal("confirmation replayed")
	}
	preview = mapValue(call("verify_api_authentication", args))
	drift.Store(true)
	if _, _, _, e := svc.Call(context.Background(), tok, "confirm_platform_action", map[string]any{"confirmation_id": preview["confirmation_id"], "confirm": true, "reason": "verify"}); e == nil || writes.Load() != 1 {
		t.Fatal("drift permitted")
	}
	drift.Store(false)
	bad := map[string]any{"service": "orders", "environment": "test", "profile": map[string]any{"revision": 1, "enabled": true, "auth": map[string]any{"mode": "login", "path": "/login", "token_pointer": "/token", "body": `{"password":"do-not-store"}`}}}
	if _, _, _, e := svc.Call(context.Background(), tok, "configure_api_testing", bad); e == nil {
		t.Fatal("plaintext accepted")
	}
	raw, _ := json.Marshal(store.data.Actions)
	if strings.Contains(string(raw), "do-not-store") {
		t.Fatal("secret persisted before confirmation")
	}
	wrong := map[string]any{"service": "orders", "environment": "prod"}
	if _, _, _, e := svc.Call(context.Background(), tok, "get_api_test_context", wrong); e == nil {
		t.Fatal("environment scope widened")
	}
	tok.role = "member"
	if _, _, _, e := svc.Call(context.Background(), tok, "get_api_test_profile", scope); e == nil {
		t.Fatal("member profile access")
	}
	tok.Scopes = []string{"apitests:read"}
	if _, _, _, e := svc.Call(context.Background(), tok, "verify_api_authentication", args); e == nil {
		t.Fatal("read scope executed login")
	}
}

func TestTestingMCPUncertainExecutionIsNeverReplayed(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" {
			json.NewEncoder(w).Encode(map[string]any{"revision": 1, "enabled": true, "variables_digest": "bound-v1", "targets": []any{map[string]any{"key": "entry", "fingerprint": "frozen", "base_url": "https://business.invalid"}}})
			return
		}
		calls.Add(1)
		w.Write([]byte("incomplete-json"))
	}))
	defer upstream.Close()
	store, _ := OpenTokenStore(t.TempDir())
	svc := NewToolService(Upstreams{Deploy: upstream.URL})
	svc.actions = store
	tok := workflowToken(t, store)
	tok.Scopes = append(tok.Scopes, "apitests:execute")
	args := map[string]any{"service": "orders", "environment": "test", "target_key": "entry", "target_fingerprint": "frozen", "request": map[string]any{"method": "POST", "path": "/orders", "body_type": "json", "body": `{"title":"test"}`}}
	value, _, _, err := svc.Call(context.Background(), tok, "execute_api_test", args)
	if err != nil {
		t.Fatal(err)
	}
	preview := mapValue(value)
	if stringArg(mapValue(preview["preview"]), "request_url") != "https://business.invalid/orders" {
		t.Fatal("actual reviewed URL missing")
	}
	confirm := map[string]any{"confirmation_id": preview["confirmation_id"], "confirm": true, "reason": "synthetic execution"}
	if _, _, _, err = svc.Call(context.Background(), tok, "confirm_platform_action", confirm); err == nil {
		t.Fatal("unknown outcome shown as success")
	}
	if _, _, _, err = svc.Call(context.Background(), tok, "confirm_platform_action", confirm); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("unknown execution replayed")
	}
	status, _, _, err := svc.Call(context.Background(), tok, "get_platform_action", map[string]any{"confirmation_id": preview["confirmation_id"]})
	if err != nil || stringArg(mapValue(status), "state") != "uncertain" {
		t.Fatal(status, err)
	}
}
