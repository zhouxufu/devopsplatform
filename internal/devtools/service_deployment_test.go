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

func TestServiceDeploymentMCPConfirmOnceAndNarrowApplication(t *testing.T) {
	var creates, advances atomic.Int32
	id := strings.Repeat("d", 32)
	digest := "frozen"
	spec := map[string]any{"service": "orders", "environment": "test", "mode": "build", "reason": "deploy", "branch": "shared-test"}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Work-Loop-Authority") != "mcp" || r.Header.Get("X-Work-Loop-Key-ID") == "" {
			t.Error("missing originating authority")
		}
		switch r.URL.Path {
		case "/api/service-deployments/preview":
			json.NewEncoder(w).Encode(map[string]any{"spec": spec, "digest": digest})
		case "/api/service-deployments":
			creates.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"id": id, "state": "running"})
		case "/api/service-deployments/" + id:
			json.NewEncoder(w).Encode(map[string]any{"id": id, "owner": "alice", "grant": map[string]any{"spec": spec}})
		case "/api/service-deployments/" + id + "/advance":
			advances.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"state": "running"})
		default:
			t.Error(r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer up.Close()
	store, _ := OpenTokenStore(t.TempDir())
	svc := NewToolService(Upstreams{Deploy: up.URL})
	svc.actions = store
	tok := workflowToken(t, store)
	tok.Applications = []string{"orders"}
	tok.Environments = []string{"test"}
	tok.Scopes = []string{"delivery:execute", "delivery:read", "builds:trigger", "builds:read"}
	p := docCall(t, svc, tok, "deploy_service", spec)
	if creates.Load() != 0 {
		t.Fatal("preview wrote")
	}
	docConfirm(t, svc, tok, p)
	docConfirm(t, svc, tok, p)
	if creates.Load() != 1 {
		t.Fatal("duplicate confirmation dispatched")
	}
	r := docCall(t, svc, tok, "advance_service_deployment", map[string]any{"task_id": id, "revision": 1})
	if advances.Load() != 1 || mapValue(r)["confirmation_id"] != nil {
		t.Fatal("repeated confirmation")
	}
	stale := docCall(t, svc, tok, "deploy_service", spec)
	digest = "changed"
	if _, _, _, e := svc.Call(context.Background(), tok, "confirm_platform_action", map[string]any{"confirmation_id": mapValue(stale)["confirmation_id"], "confirm": true, "reason": "confirmed"}); e == nil {
		t.Fatal("drift ignored")
	}
	if creates.Load() != 1 {
		t.Fatal("drift dispatched")
	}
	tok.Scopes = []string{"delivery:read", "delivery:execute"}
	if _, _, _, e := svc.Call(context.Background(), tok, "advance_service_deployment", map[string]any{"task_id": id, "revision": 2}); e == nil {
		t.Fatal("removed build scope ignored")
	}
	tok.Applications = []string{"other"}
	if _, _, _, e := svc.Call(context.Background(), tok, "get_service_deployment", map[string]any{"task_id": id}); e == nil {
		t.Fatal("application scope ignored")
	}
}
func TestServiceDeploymentBackgroundUsesFreshKey(t *testing.T) {
	store, _ := OpenTokenStore(t.TempDir())
	key, _, e := store.Create(CreateTokenRequest{Name: "worker", Owner: "alice", Scopes: []string{"delivery:read", "delivery:execute"}, Applications: []string{"orders"}, Environments: []string{"test"}})
	if e != nil {
		t.Fatal(e)
	}
	var writes, blocks atomic.Int32
	id := strings.Repeat("d", 32)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/advance") {
			writes.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"id": id})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/blocked") {
			blocks.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"id": id})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"id": id, "owner": "alice", "state": "running", "authority": "mcp", "authority_key_id": key.ID, "revision": 1, "grant": map[string]any{"spec": map[string]any{"service": "orders", "environment": "test", "mode": "artifact"}}})
	}))
	defer up.Close()
	h := Routes(store, NewToolService(Upstreams{Deploy: up.URL}))
	call := func(enabled bool) int {
		r := httptest.NewRequest("POST", "/internal/service-deployment-advance", strings.NewReader(`{"task_id":"`+id+`","owner":"alice"}`))
		users, _ := json.Marshal([]any{map[string]any{"username": "alice", "role": "member", "enabled": enabled}})
		r.Header.Set("X-MCP-Users", string(users))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if code := call(true); code != 200 || writes.Load() != 1 {
		t.Fatal(code, writes.Load())
	}
	if call(false) != 403 || writes.Load() != 1 {
		t.Fatal("disabled user dispatched")
	}
	if e = store.Revoke(key.ID); e != nil {
		t.Fatal(e)
	}
	if call(true) != 403 || writes.Load() != 1 || blocks.Load() != 1 {
		t.Fatal("revoked key dispatched")
	}
}
