package devtools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestDeliveryMCPFrozenNodeAndManualReceipt(t *testing.T) {
	var writes atomic.Int32
	var revision atomic.Int32
	revision.Store(1)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/environments" && r.Header.Get("X-Reviewer-Directory") != `["alice"]` {
			t.Error("missing trusted people")
		}
		switch r.URL.Path {
		case "/api/environments":
			json.NewEncoder(w).Encode([]any{map[string]any{"name": "test", "purpose": "test"}, map[string]any{"name": "prod", "purpose": "production"}})
		case "/api/delivery/runs/9":
			json.NewEncoder(w).Encode(map[string]any{"id": 9, "owner": "alice", "revision": revision.Load(), "digest": "immutable", "status": "approved", "plan": map[string]any{"environment": map[string]any{"name": "test"}}, "steps": []any{map[string]any{"node_id": "es-check", "kind": "manual", "status": "pending", "service": "orders"}}})
		case "/api/delivery/mcp/runs/9/advance":
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			if b["manual"] != true || b["node_id"] != "es-check" || b["comment"] != "已人工核对索引；记录 fixture-1" {
				t.Error(b)
			}
			writes.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"id": 9})
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
	tok.Scopes = append(tok.Scopes, "delivery:execute")
	tok.reviewers = `["alice"]`
	args := map[string]any{"run_id": 9, "environment": "test", "comment": "已人工核对索引；记录 fixture-1"}
	p := docCall(t, svc, tok, "record_delivery_manual_step", args)
	if writes.Load() != 0 || mapValue(mapValue(p)["preview"])["manual_receipt"] != true {
		t.Fatal("preview executed or lost manual identity")
	}
	docConfirm(t, svc, tok, p)
	docConfirm(t, svc, tok, p)
	if writes.Load() != 1 {
		t.Fatal("duplicate confirmation replayed")
	}
	p = docCall(t, svc, tok, "record_delivery_manual_step", args)
	revision.Store(2)
	if _, _, _, err := svc.Call(context.Background(), tok, "confirm_platform_action", map[string]any{"confirmation_id": mapValue(p)["confirmation_id"], "confirm": true, "reason": "fixture"}); err == nil {
		t.Fatal("stale node executed")
	}
	if writes.Load() != 1 {
		t.Fatal("stale write")
	}
	if _, _, _, err := svc.Call(context.Background(), tok, "advance_delivery", map[string]any{"run_id": 9, "environment": "test"}); err == nil {
		t.Fatal("manual masqueraded as machine")
	}
	if _, _, _, err := svc.Call(context.Background(), tok, "record_delivery_manual_step", map[string]any{"run_id": 9, "environment": "prod", "comment": "x"}); err == nil {
		t.Fatal("production allowed")
	}
}

func TestChangeReportPreviewShowsActualRequirement(t *testing.T) {
	svc, tok, _, wid, writes := changesFixture(t)
	tok.Applications = []string{"*"}
	p := docCall(t, svc, tok, "report_changes", map[string]any{"service": "orders", "work_item_id": wid, "changes": developmentChanges()})
	item := mapValue(mapValue(mapValue(p)["preview"])["work_item"])
	if intArg(item, "id", 0) != wid || stringArg(item, "title") != "开发变更" || writes.Load() != 0 {
		t.Fatal(item, writes.Load())
	}
}

func TestDeliveryMCPEnvironmentWorkflowAndWorkItemSource(t *testing.T) {
	var posts atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/environments":
			json.NewEncoder(w).Encode([]any{map[string]any{"name": "test", "purpose": "test"}})
		case "/api/environments/test/delivery-policy":
			json.NewEncoder(w).Encode(map[string]any{"workflow_template_id": 7, "revision": 3})
		case "/api/delivery/preparations/preview", "/api/delivery/preview":
			var req map[string]any
			json.NewDecoder(r.Body).Decode(&req)
			if intArg(req, "template_id", 0) != 7 || stringArg(req, "strategy_kind") != "custom" || stringArg(req, "source_type") != "work_items" || req["version_id"] != nil {
				t.Error("wrong normalized flow", req)
			}
			posts.Add(1)
			if r.URL.Path == "/api/delivery/preparations/preview" {
				json.NewEncoder(w).Encode(map[string]any{"plan": map[string]any{"scope": map[string]any{"type": "work_items"}}, "digest": "scope-digest"})
			} else {
				json.NewEncoder(w).Encode(map[string]any{"preflight_token": "frozen-proof", "blockers": []any{}})
			}
		default:
			t.Error("unexpected request", r.Method, r.URL.Path)
			http.Error(w, "unexpected", 404)
		}
	}))
	defer up.Close()
	store, _ := OpenTokenStore(t.TempDir())
	s := NewToolService(Upstreams{Deploy: up.URL})
	s.actions = store
	tok := workflowToken(t, store)
	tok.Scopes = append(tok.Scopes, "delivery:write")
	for _, phase := range []string{"preparation", "release"} {
		args := map[string]any{"phase": phase, "request": map[string]any{"environment": "test", "work_item_ids": []any{24}}}
		docCall(t, s, tok, "preview_delivery", args)
		p := docCall(t, s, tok, "create_delivery_run", args)
		body := mapValue(mapValue(p)["request"])
		if intArg(body, "template_id", 0) != 7 || stringArg(body, "source_type") != "work_items" {
			t.Fatal("frozen request not normalized", body)
		}
		if mapValue(args["request"])["template_id"] != nil {
			t.Fatal("caller candidate mutated")
		}
	}
	if posts.Load() != 4 {
		t.Fatal(posts.Load())
	}
	for _, req := range []map[string]any{
		{"environment": "test"},
		{"environment": "test", "work_item_ids": []any{24}, "version_id": 9},
		{"environment": "test", "work_item_ids": []any{24}, "source_type": "version"},
		{"environment": "test", "work_item_ids": []any{24}, "template_id": 8},
		{"environment": "test", "work_item_ids": []any{24}, "strategy_kind": "standard"},
	} {
		if _, _, _, err := s.Call(context.Background(), tok, "preview_delivery", map[string]any{"phase": "preparation", "request": req}); err == nil {
			t.Fatal("ambiguous scope/binding accepted", req)
		}
	}
	if posts.Load() != 4 {
		t.Fatal("invalid requests reached executor")
	}
}
