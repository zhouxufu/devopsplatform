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

func TestIndependentChangeMCPReturnsOriginalInsteadOfAnotherConfirmation(t *testing.T) {
	var creates atomic.Int32
	spec := map[string]any{"service": "gis", "environment": "dev", "change_id": 66, "mode": "change", "reason": "release"}
	id := strings.Repeat("c", 32)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var result any
		switch r.URL.Path {
		case "/api/service-deployments/by-change":
			result = map[string]any{}
			if creates.Load() > 0 {
				result = map[string]any{"id": id, "state": "running"}
			}
		case "/api/service-deployments/preview":
			result = map[string]any{"spec": spec, "digest": "frozen", "change": map[string]any{"mark_ready_draft_digest": "fixture-digest"}}
		case "/api/service-deployments":
			creates.Add(1)
			result = map[string]any{"id": id, "state": "running"}
		default:
			t.Error(r.URL.Path)
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	}))
	defer up.Close()
	store, _ := OpenTokenStore(t.TempDir())
	svc := NewToolService(Upstreams{Deploy: up.URL})
	svc.actions = store
	tok := workflowToken(t, store)
	tok.Applications = []string{"gis"}
	tok.Environments = []string{"dev"}
	tok.Scopes = []string{"changes:read", "delivery:read", "delivery:write", "delivery:execute", "builds:read", "builds:trigger"}
	if _, _, _, err := svc.Call(context.Background(), tok, "deploy_service", spec); err == nil {
		t.Fatal("missing preparation authority accepted")
	}
	tok.Scopes = append(tok.Scopes, "delivery:prepare")
	if _, _, _, err := svc.Call(context.Background(), tok, "deploy_service", spec); err == nil {
		t.Fatal("draft readiness without changes:write")
	}
	tok.Scopes = append(tok.Scopes, "changes:write")
	stale := docCall(t, svc, tok, "deploy_service", spec)
	tok.Scopes = tok.Scopes[:len(tok.Scopes)-1]
	_, _, _, err := svc.Call(context.Background(), tok, "confirm_platform_action", map[string]any{"confirmation_id": mapValue(stale)["confirmation_id"], "confirm": true, "reason": "confirmed"})
	if err == nil || creates.Load() != 0 {
		t.Fatal("removed readiness permission did not stop confirmation")
	}
	tok.Scopes = append(tok.Scopes, "changes:write")
	preview := docCall(t, svc, tok, "deploy_service", spec)
	docConfirm(t, svc, tok, preview)
	again := mapValue(docCall(t, svc, tok, "deploy_service", spec))
	if creates.Load() != 1 || again["id"] != id || again["confirmation_id"] != nil {
		t.Fatal(again, creates.Load())
	}
}

// A normal request from chat (with no phase or artifact hints) must reach the
// same coordinator as an explicitly mixed direct/merge delivery.
func TestPublishDeliveryRoutesCodeAndRecoversOriginal(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "mixed"}[explicit], func(t *testing.T) {
			var creates atomic.Int32
			var grant map[string]any
			id := strings.Repeat("e", 32)
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var result any
				switch r.URL.Path {
				case "/api/environments":
					result = []any{map[string]any{"name": "test", "purpose": "test"}}
				case "/api/environments/test/delivery-policy":
					result = map[string]any{"workflow_template_id": 9}
				case "/api/delivery/draft-plan":
					result = map[string]any{"scope": map[string]any{"changes": []any{map[string]any{"service": "orders", "kind": "code"}}}}
				case "/api/service-deployments/preview":
					var spec map[string]any
					if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
						t.Error(err)
					}
					req := mapValue(spec["delivery"])
					if spec["mode"] != "change" || req["source_type"] != "work_items" || len(req["selected_change_ids"].([]any)) != map[bool]int{false: 1, true: 2}[explicit] {
						t.Error("incorrect publication scope", spec)
					}
					if explicit && mapValue(req["code_modes"])["billing"] != "direct" {
						t.Error("lost direct mode")
					}
					grant = map[string]any{"spec": spec, "digest": "frozen", "change": map[string]any{"publication_scope": map[string]any{"applications": []any{map[string]any{"service": "orders"}, map[string]any{"service": "billing"}}}}}
					if creates.Load() > 0 {
						grant["original_task_id"] = id
					}
					result = grant
				case "/api/service-deployments":
					creates.Add(1)
					result = map[string]any{"id": id, "state": "running"}
				case "/api/service-deployments/" + id:
					result = map[string]any{"id": id, "owner": "alice", "state": "running", "grant": grant}
				default:
					t.Errorf("unexpected route %s", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(result)
			}))
			defer up.Close()
			store, _ := OpenTokenStore(t.TempDir())
			svc := NewToolService(Upstreams{Deploy: up.URL})
			svc.actions = store
			tok := workflowToken(t, store)
			tok.Applications = []string{"*"}
			tok.Environments = []string{"test"}
			tok.Scopes = []string{"delivery:execute", "delivery:read", "delivery:prepare", "delivery:write", "builds:trigger", "builds:read", "changes:read"}
			req := map[string]any{"environment": "test", "reason": "fixture", "work_item_ids": []any{24}, "selected_change_ids": []any{13}}
			if explicit {
				req["selected_change_ids"] = []any{13, 14}
				req["code_modes"] = map[string]any{"orders": "merge", "billing": "direct"}
			}
			args := map[string]any{"request": req}
			preview := docCall(t, svc, tok, "publish_delivery", args)
			if creates.Load() != 0 {
				t.Fatal("preview dispatched")
			}
			docConfirm(t, svc, tok, preview)
			docConfirm(t, svc, tok, preview)
			original := mapValue(docCall(t, svc, tok, "publish_delivery", args))
			if creates.Load() != 1 || original["id"] != id || original["confirmation_id"] != nil {
				t.Fatal("original publication not recovered", original)
			}
		})
	}
}
