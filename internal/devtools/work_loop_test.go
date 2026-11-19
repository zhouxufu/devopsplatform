package devtools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkLoopMCPAuthorizesOnceAndRechecksScopes(t *testing.T) {
	var creates, advances atomic.Int32
	spec := map[string]any{"work_item_id": 1, "environment": "test", "applications": []any{"orders"}, "expires_at": time.Now().Add(time.Hour).Unix(), "max_iterations": 2, "acceptance": "接口回归通过", "complete_scope": true}
	id := strings.Repeat("a", 32)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/api/work-loops") && r.Header.Get("X-Reviewer-Directory") != `["alice"]` {
			t.Error("lost verified reviewer directory")
		}
		switch r.URL.Path {
		case "/api/environments":
			json.NewEncoder(w).Encode([]any{map[string]any{"name": "test", "purpose": "test"}})
		case "/api/work-loops/preview":
			json.NewEncoder(w).Encode(map[string]any{"spec": spec, "digest": "frozen-grant"})
		case "/api/work-loops":
			creates.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"id": id, "revision": 1, "grant": map[string]any{"spec": spec}})
		case "/api/work-loops/" + id:
			json.NewEncoder(w).Encode(map[string]any{"id": id, "owner": "alice", "revision": 1, "grant": map[string]any{"spec": spec}})
		case "/api/work-loops/" + id + "/advance":
			advances.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"id": id, "revision": 2, "state": "running"})
		default:
			t.Error(r.URL.Path)
			http.Error(w, "unexpected", 404)
		}
	}))
	defer up.Close()
	store, _ := OpenTokenStore(t.TempDir())
	svc := NewToolService(Upstreams{Deploy: up.URL})
	svc.actions = store
	tok := workflowToken(t, store)
	tok.Scopes = append([]string{}, workLoopScopes...)
	tok.reviewers = `["alice"]`
	p := docCall(t, svc, tok, "create_work_loop", map[string]any{"spec": spec})
	if creates.Load() != 0 {
		t.Fatal("authorization preview executed")
	}
	docConfirm(t, svc, tok, p)
	docConfirm(t, svc, tok, p)
	if creates.Load() != 1 {
		t.Fatal("duplicate authorization")
	}
	result := docCall(t, svc, tok, "advance_work_loop", map[string]any{"task_id": id, "revision": 1})
	if advances.Load() != 1 || mapValue(result)["confirmation_id"] != nil {
		t.Fatal("task step needs redundant confirmation", result)
	}
	tok.Scopes = slices.DeleteFunc(tok.Scopes, func(s string) bool { return s == "apitests:execute" })
	if _, _, _, e := svc.Call(context.Background(), tok, "advance_work_loop", map[string]any{"task_id": id, "revision": 2}); e == nil {
		t.Fatal("revoked execution scope ignored")
	}
	if advances.Load() != 1 {
		t.Fatal("dispatched after scope revocation")
	}
	tok.Environments = []string{"dev"}
	if _, _, _, e := svc.Call(context.Background(), tok, "get_work_loop", map[string]any{"task_id": id}); e == nil {
		t.Fatal("environment restriction ignored")
	}
}

func TestWorkLoopMCPReauthorizationRequiresNewPreview(t *testing.T) {
	var writes atomic.Int32
	spec := map[string]any{"work_item_id": 1, "environment": "test", "applications": []any{"orders"}}
	id := strings.Repeat("b", 32)
	digest := "preview-one"
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/environments":
			json.NewEncoder(w).Encode([]any{map[string]any{"name": "test", "purpose": "test"}})
		case "/api/work-loops/" + id:
			json.NewEncoder(w).Encode(map[string]any{"grant": map[string]any{"spec": spec}})
		case "/api/work-loops/" + id + "/authorization/preview":
			json.NewEncoder(w).Encode(map[string]any{"revision": 3, "grant": map[string]any{"spec": spec}, "digest": digest, "differences": []any{"expiry"}})
		case "/api/work-loops/" + id + "/authorization":
			writes.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"id": id, "revision": 4})
		default:
			t.Error(r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer up.Close()
	store, _ := OpenTokenStore(t.TempDir())
	svc := NewToolService(Upstreams{Deploy: up.URL})
	svc.actions = store
	token := workflowToken(t, store)
	token.Scopes = append([]string{}, workLoopScopes...)
	args := map[string]any{"task_id": id, "revision": 3, "spec": spec}
	p := docCall(t, svc, token, "reauthorize_work_loop", args)
	if writes.Load() != 0 {
		t.Fatal("preview wrote authority")
	}
	digest = "preview-two"
	if _, _, _, e := svc.Call(context.Background(), token, "confirm_platform_action", map[string]any{"confirmation_id": mapValue(p)["confirmation_id"], "confirm": true, "reason": "核对差异"}); e == nil || writes.Load() != 0 {
		t.Fatal("stale renewal confirmed")
	}
	p = docCall(t, svc, token, "reauthorize_work_loop", args)
	docConfirm(t, svc, token, p)
	if writes.Load() != 1 {
		t.Fatal(writes.Load())
	}
}

func TestWorkLoopRequirementQueryForwardsTypedCursor(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/research/work-items/page" || r.URL.Query().Get("kind") != "requirement" || r.URL.Query().Get("cursor") != "frozen-cursor" || r.URL.Query().Get("page") != "2" {
			t.Error("typed query lost", r.URL)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
	}))
	defer up.Close()
	store, _ := OpenTokenStore(t.TempDir())
	svc := NewToolService(Upstreams{Deploy: up.URL})
	svc.actions = store
	token := workflowToken(t, store)
	token.Scopes = append([]string{}, workLoopScopes...)
	docCall(t, svc, token, "list_work_items", map[string]any{"connection": "issues", "kind": "requirement", "page": 2, "cursor": "frozen-cursor"})
}
