package devtools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestMCPScopePreviewAndFrozenSelfConfirmation(t *testing.T) {
	for _, kind := range []string{"success", "stale", "scope-changed", "disabled-after-preview", "permission-revoked", "wrong-person-admin", "approval", "verify", "foreign-work-item", "already-confirmed", "missing-permission", "protected"} {
		t.Run(kind, func(t *testing.T) {
			var writes atomic.Int32
			revision := 1
			protected := kind == "protected"
			nodeKind := "manual"
			if kind == "approval" || kind == "verify" {
				nodeKind = kind
			}
			reviews := []any{}
			if kind == "already-confirmed" {
				reviews = []any{map[string]any{"actor": "alice", "approved": true, "work_item_ids": []any{24}}}
			}
			scopeTitle := "Frozen requirement"
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/environments":
					json.NewEncoder(w).Encode([]any{map[string]any{"name": "test", "purpose": "test", "protected": protected}})
				case "/api/delivery/runs/9":
					json.NewEncoder(w).Encode(map[string]any{"id": 9, "owner": "someone-else", "revision": revision, "digest": "scope-digest", "status": "waiting_confirmation", "plan": map[string]any{"environment": map[string]any{"name": "test"}, "template": map[string]any{"workbench": "research"}, "preparation_request": map[string]any{"environment": "test"}, "scope": map[string]any{"work_items": []any{map[string]any{"id": 24, "title": scopeTitle}}}, "nodes": []any{map[string]any{"id": "scope", "kind": nodeKind, "approvers": []any{"alice", "bob"}, "assigned_work_items": map[string]any{"alice": []any{24}, "bob": []any{25}}}}}, "steps": []any{map[string]any{"node_id": "scope", "kind": nodeKind, "status": "reviewing", "reviews": reviews}}})
				case "/api/delivery/mcp/runs/9/confirm-scope":
					var body map[string]any
					json.NewDecoder(r.Body).Decode(&body)
					ids, _ := body["work_item_ids"].([]any)
					if r.Header.Get("X-User") != "alice" || r.Header.Get("X-Reviewer-Directory") != `["alice","bob"]` || len(ids) != 1 || intArg(map[string]any{"id": ids[0]}, "id", 0) != 24 || body["node_id"] != "scope" || intArg(body, "revision", 0) != 1 || body["digest"] != "scope-digest" || body["comment"] != "I confirm this exact scope" || len(stringArg(body, "confirmation_id")) != 32 {
						t.Error("wrong frozen identity/scope", body)
					}
					writes.Add(1)
					json.NewEncoder(w).Encode(map[string]any{"id": 9, "status": "waiting_confirmation"})
				default:
					t.Error("unexpected route", r.URL.Path)
					http.Error(w, "unexpected", 404)
				}
			}))
			defer up.Close()
			store, _ := OpenTokenStore(t.TempDir())
			s := NewToolService(Upstreams{Deploy: up.URL})
			s.actions = store
			tok := workflowToken(t, store)
			tok.reviewers = `["alice","bob"]`
			if kind != "missing-permission" {
				tok.Scopes = append(tok.Scopes, "delivery:prepare")
			}
			if kind == "wrong-person-admin" {
				tok.Owner = "mallory"
				tok.role = "admin"
			}
			args := map[string]any{"run_id": 9, "environment": "test", "comment": "I confirm this exact scope"}
			if kind == "foreign-work-item" {
				args["work_item_ids"] = []any{25}
			}
			raw, _, _, err := s.Call(context.Background(), tok, "confirm_delivery_scope", args)
			rejected := kind == "wrong-person-admin" || kind == "approval" || kind == "verify" || kind == "foreign-work-item" || kind == "already-confirmed" || kind == "missing-permission" || kind == "protected"
			if rejected {
				if err == nil || writes.Load() != 0 {
					t.Fatal("boundary accepted", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			p := mapValue(raw)
			preview := mapValue(p["preview"])
			if preview["actor"] != "alice" || intArg(preview, "revision", 0) != 1 || mapValue(preview["scope"])["work_items"] == nil || writes.Load() != 0 {
				t.Fatal("missing visible frozen scope or preview wrote", p)
			}
			args["comment"] = "mutated client comment"
			switch kind {
			case "stale":
				revision = 2
			case "scope-changed":
				scopeTitle = "Changed scope"
			case "disabled-after-preview":
				tok.reviewers = `["bob"]`
			case "permission-revoked":
				tok.Scopes = []string{"delivery:read"}
			}
			// A trusted directory change is independently checked by the real backend;
			// this fake upstream models its refusal rather than pretending it succeeded.
			if kind == "disabled-after-preview" {
				// Pre-confirm validation must reject before sending the frozen write.
				tok.reviewers = `[]`
			}
			_, _, _, err = s.Call(context.Background(), tok, "confirm_platform_action", map[string]any{"confirmation_id": p["confirmation_id"], "confirm": true, "reason": "I confirm the shown scope"})
			if kind != "success" {
				if err == nil || writes.Load() != 0 {
					t.Fatal("stale/revoked scope executed", kind, err, writes.Load())
				}
				return
			}
			if err != nil || writes.Load() != 1 {
				t.Fatal(err, writes.Load())
			}
			docConfirm(t, s, tok, p)
			if writes.Load() != 1 {
				t.Fatal("duplicate confirmation replayed")
			}
		})
	}
}
