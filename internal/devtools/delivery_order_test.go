package devtools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeliveryOrderPreservesExecutableDefinitions(t *testing.T) {
	generated := []any{map[string]any{"id": "sql", "kind": "mysql", "service": "orders", "change_ids": []any{float64(1), float64(2)}}, map[string]any{"id": "deploy", "kind": "deploy", "service": "orders"}, map[string]any{"id": "review", "kind": "approval", "approvers": []any{"reviewer"}}}
	requested := []any{map[string]any{"id": "review"}, map[string]any{"id": "sql", "change_ids": []any{float64(1)}}, map[string]any{"id": "deploy"}, map[string]any{"id": "after", "sql_group": "sql", "change_ids": []any{float64(2)}}}
	actual, err := deliveryOrderedNodes(generated, requested)
	if err != nil || len(actual) != 4 || stringArg(mapValue(actual[3]), "kind") != "mysql" || stringArg(mapValue(actual[3]), "service") != "orders" {
		t.Fatal(actual, err)
	}
	if len(mapValue(generated[0])["change_ids"].([]any)) != 2 {
		t.Fatal("mutated draft")
	}
	for _, bad := range []map[string]any{{"id": "deploy", "kind": "script"}, {"id": "other"}, {"id": "new", "sql_group": "review", "change_ids": []any{1}}, {"id": "sql", "script": map[string]any{"content": "x"}}} {
		if _, err := deliveryOrderedNodes(generated, []any{bad}); err == nil {
			t.Fatal("accepted node rewrite", bad)
		}
	}
}

func TestDeliveryMCPOrderedPreviewUsesServerDraft(t *testing.T) {
	previews := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/environments":
			json.NewEncoder(w).Encode([]any{map[string]any{"name": "test", "purpose": "test"}})
		case "/api/environments/test/delivery-policy":
			json.NewEncoder(w).Encode(map[string]any{"revision": 1})
		case "/api/delivery/draft-plan":
			var req map[string]any
			json.NewDecoder(r.Body).Decode(&req)
			if req["nodes"] != nil {
				t.Error("untrusted definitions forwarded to draft")
			}
			json.NewEncoder(w).Encode(map[string]any{"nodes": []any{map[string]any{"id": "sql", "name": "Frozen SQL", "kind": "mysql", "service": "orders", "change_ids": []any{1, 2}}}})
		case "/api/delivery/preview":
			var req map[string]any
			json.NewDecoder(r.Body).Decode(&req)
			nodes, _ := req["nodes"].([]any)
			if intArg(req, "max_active_nodes", 0) != 2 || len(nodes) != 2 || stringArg(mapValue(nodes[1]), "kind") != "mysql" || stringArg(mapValue(nodes[1]), "service") != "orders" || stringArg(mapValue(nodes[1]), "sql_group") != "sql" {
				t.Error("incorrect resolved stages", req)
			}
			previews++
			json.NewEncoder(w).Encode(map[string]any{"initial_dispatch": []string{"sql"}, "preflight_token": "fixture-proof", "blockers": []any{}})
		default:
			t.Error("unexpected operation", r.URL.Path)
			http.Error(w, "unexpected", 403)
		}
	}))
	defer up.Close()
	store, _ := OpenTokenStore(t.TempDir())
	svc := NewToolService(Upstreams{Deploy: up.URL})
	svc.actions = store
	tok := workflowToken(t, store)
	tok.Scopes = append(tok.Scopes, "delivery:write")
	args := map[string]any{"phase": "release", "request": map[string]any{"environment": "test", "max_active_nodes": 2, "source_change_ids": []any{1, 2}, "nodes": []any{map[string]any{"id": "sql", "change_ids": []any{1}}, map[string]any{"id": "after", "sql_group": "sql", "change_ids": []any{2}}}}}
	v := mapValue(docCall(t, svc, tok, "preview_delivery", args))
	if ids, ok := v["initial_dispatch"].([]any); !ok || len(ids) != 1 || ids[0] != "sql" {
		t.Fatal(v)
	}
	action := docCall(t, svc, tok, "create_delivery_run", args)
	body := mapValue(mapValue(action)["request"])
	if previews != 2 || len(body["nodes"].([]any)) != 2 {
		t.Fatal("stages not frozen", action)
	}
}
