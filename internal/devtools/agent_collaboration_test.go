package devtools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestAgentCaseSaveConfirmationDriftAndPermissions(t *testing.T) {
	var writes atomic.Int32
	var revision atomic.Int32
	revision.Store(1)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("X-User") != "alice" {
			t.Error("lost user")
		}
		switch r.URL.Path {
		case "/api/api-testing/mcp/cases":
			json.NewEncoder(w).Encode(map[string]any{"case": map[string]any{"id": 4, "revision": revision.Load()}})
		case "/api/api-testing/mcp/case-preview":
			var c map[string]any
			json.NewDecoder(r.Body).Decode(&c)
			json.NewEncoder(w).Encode(map[string]any{"valid": true, "case": c})
		case "/api/api-testing/cases/4":
			if r.Method != "PUT" {
				t.Error("wrong method")
			}
			writes.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"id": 4, "revision": 2})
		default:
			t.Error(r.URL.Path)
			http.Error(w, "unexpected", 404)
		}
	}))
	defer up.Close()
	store, _ := OpenTokenStore(t.TempDir())
	s := NewToolService(Upstreams{Deploy: up.URL})
	s.actions = store
	tok := workflowToken(t, store)
	tok.Scopes = append(tok.Scopes, "apitests:read", "apitests:write")
	args := map[string]any{"service": "orders", "environment": "test", "case": map[string]any{"id": 4, "revision": 1, "name": "回归", "steps": []any{map[string]any{"id": "verify", "name": "校验", "request": map[string]any{"path": "/orders"}}}}}
	v, _, _, err := s.Call(context.Background(), tok, "save_api_test_case", args)
	if err != nil {
		t.Fatal(err)
	}
	if writes.Load() != 0 {
		t.Fatal("preview wrote")
	}
	confirm := map[string]any{"confirmation_id": mapValue(v)["confirmation_id"], "confirm": true, "reason": "save synthetic case"}
	for i := 0; i < 2; i++ {
		if _, _, _, err = s.Call(context.Background(), tok, "confirm_platform_action", confirm); err != nil {
			t.Fatal(err)
		}
	}
	if writes.Load() != 1 {
		t.Fatal("replayed save")
	}
	v, _, _, err = s.Call(context.Background(), tok, "save_api_test_case", args)
	if err != nil {
		t.Fatal(err)
	}
	revision.Store(2)
	confirm["confirmation_id"] = mapValue(v)["confirmation_id"]
	if _, _, _, err = s.Call(context.Background(), tok, "confirm_platform_action", confirm); err == nil {
		t.Fatal("stale save")
	}
	if writes.Load() != 1 {
		t.Fatal("stale save wrote")
	}
	tok.Scopes = []string{"apitests:read"}
	if _, _, _, err = s.Call(context.Background(), tok, "save_api_test_case", args); err == nil {
		t.Fatal("read key saved")
	}
	wrong := map[string]any{"service": "orders", "environment": "prod", "case_id": 4}
	if _, _, _, err = s.Call(context.Background(), tok, "get_api_test_case", wrong); err == nil {
		t.Fatal("scope widened")
	}
}

func TestAgentContextHonorsIndependentChangesScope(t *testing.T) {
	var changeReads atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/work-items/9/delivery-content" {
			changeReads.Add(1)
		}
		w.Write([]byte(`{"id":9,"title":"修复订单"}`))
	}))
	defer up.Close()
	store, _ := OpenTokenStore(t.TempDir())
	tok := workflowToken(t, store)
	tok.Applications = []string{"*"}
	tok.Scopes = []string{"workitems:read"}
	s := NewToolService(Upstreams{Deploy: up.URL})
	v, _, _, err := s.Call(context.Background(), tok, "get_task_context", map[string]any{"work_item_id": 9})
	if err != nil {
		t.Fatal(err)
	}
	if changeReads.Load() != 0 || len(mapValue(v)["missing"].([]any)) == 0 {
		t.Fatal("independent scope not respected", v)
	}
	tok.Scopes = append(tok.Scopes, "changes:read")
	if _, _, _, err = s.Call(context.Background(), tok, "get_task_context", map[string]any{"work_item_id": 9}); err != nil {
		t.Fatal(err)
	}
	if changeReads.Load() != 1 {
		t.Fatal("authorized changes omitted")
	}
	tok.Applications = []string{"orders"}
	if _, _, _, err = s.Call(context.Background(), tok, "get_task_context", map[string]any{"work_item_id": 9}); err == nil {
		t.Fatal("cross app task exposed")
	}
}

func TestAgentLayoutPreviewAndConfirmation(t *testing.T) {
	var writes atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET":
			w.Write([]byte(`{"revision":0,"positions":{},"zoom":1,"pan":{"x":0,"y":0}}`))
		case r.URL.Path == "/api/api-testing/cases/4/layout/validate":
			var v map[string]any
			json.NewDecoder(r.Body).Decode(&v)
			json.NewEncoder(w).Encode(map[string]any{"valid": true, "layout": v})
		case r.Method == "PUT":
			writes.Add(1)
			w.Write([]byte(`{"revision":1}`))
		default:
			t.Error("unexpected operation", r.Method, r.URL.Path)
		}
	}))
	defer up.Close()
	store, _ := OpenTokenStore(t.TempDir())
	s := NewToolService(Upstreams{Deploy: up.URL})
	s.actions = store
	tok := workflowToken(t, store)
	tok.Scopes = append(tok.Scopes, "apitests:write")
	args := map[string]any{"service": "orders", "environment": "test", "case_id": 4, "layout": map[string]any{"revision": 0, "positions": map[string]any{"node": map[string]any{"x": 100, "y": 200}}, "zoom": 1, "pan": map[string]any{"x": 0, "y": 0}}}
	v, _, _, err := s.Call(context.Background(), tok, "save_api_test_layout", args)
	if err != nil {
		t.Fatal(err)
	}
	if writes.Load() != 0 {
		t.Fatal("layout preview wrote")
	}
	confirm := map[string]any{"confirmation_id": mapValue(v)["confirmation_id"], "confirm": true, "reason": "save fixture layout"}
	for i := 0; i < 2; i++ {
		if _, _, _, err = s.Call(context.Background(), tok, "confirm_platform_action", confirm); err != nil {
			t.Fatal(err)
		}
	}
	if writes.Load() != 1 {
		t.Fatal("layout confirmation replayed")
	}
}
