package devtools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCodeRecoveryHandoffAndFrozenConfirmation(t *testing.T) {
	var writes, revision, lease atomic.Int64
	revision.Store(7)
	var state atomic.Value
	state.Store("conflict")
	var protected atomic.Bool
	sha := strings.Repeat("a", 40)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/environments" {
			json.NewEncoder(w).Encode([]any{map[string]any{"name": "test", "protected": protected.Load()}})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/service-deployments/") {
			child := "fixture-a"
			if strings.HasSuffix(r.URL.Path, "other") {
				child = "other"
			}
			json.NewEncoder(w).Encode(map[string]any{"id": "publication", "owner": "alice", "state": "running", "stage": "waiting_conflict", "code_id": child, "grant": map[string]any{"spec": map[string]any{"service": "orders", "environment": "test", "mode": "change"}}})
			return
		}
		if r.Method == "GET" {
			json.NewEncoder(w).Encode(map[string]any{"id": "fixture-a", "owner": "alice", "state": state.Load(), "revision": revision.Load(), "lease_until": lease.Load(), "scope_digest": "frozen", "preparation_run_id": 101, "request": map[string]any{"environment": "test"}, "repositories": []any{map[string]any{"service": "orders", "url": "https://user:SECRET@example.invalid/orders.git?token=PRIVATE", "connection_digest": "PRIVATE", "base_commit": sha, "target_commit": sha, "target_branch": "dev", "sources": []any{map[string]any{"branch": "fix", "commit": sha}}, "conflicts": []any{"Order.java"}}}})
			return
		}
		if r.URL.Path != "/api/delivery/mcp/code-preparations/fixture-a/resume" {
			t.Error(r.URL.Path)
		}
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		if intArg(b, "expected_revision", 0) != 7 || b["confirm"] != true || b["target_commits"].([]any)[0] != sha {
			t.Error(b)
		}
		writes.Add(1)
		json.NewEncoder(w).Encode(map[string]any{"id": "fixture-a", "state": "merging"})
	}))
	defer up.Close()
	store, _ := OpenTokenStore(t.TempDir())
	s := NewToolService(Upstreams{Deploy: up.URL})
	s.actions = store
	tok := workflowToken(t, store)
	tok.Scopes = append(tok.Scopes, "delivery:prepare", "builds:read")
	ctx := context.Background()
	args := map[string]any{"preparation_id": "fixture-a", "environment": "test"}
	handoff := docCall(t, s, tok, "get_code_conflict", args)
	raw, _ := json.Marshal(handoff)
	if strings.Contains(string(raw), "SECRET") || strings.Contains(string(raw), "PRIVATE") || !strings.Contains(string(raw), "Order.java") || writes.Load() != 0 {
		t.Fatal(string(raw))
	}
	args["expected_revision"] = 7
	args["scope_digest"] = "frozen"
	args["target_commits"] = []any{sha}
	preview := docCall(t, s, tok, "resume_code_preparation", args)
	args["target_commits"] = []any{strings.Repeat("b", 40)} // caller mutation must not alter frozen action
	if writes.Load() != 0 {
		t.Fatal("preview wrote")
	}
	docConfirm(t, s, tok, preview)
	docConfirm(t, s, tok, preview)
	if writes.Load() != 1 {
		t.Fatal("not exactly once")
	}
	args["target_commits"] = []any{sha}
	preview = docCall(t, s, tok, "resume_code_preparation", args)
	revision.Store(8)
	if _, _, _, err := s.Call(ctx, tok, "confirm_platform_action", map[string]any{"confirmation_id": mapValue(preview)["confirmation_id"], "confirm": true, "reason": "fixture"}); err == nil {
		t.Fatal("stale task resumed")
	}
	revision.Store(7)
	for _, bad := range []string{"lease", "ready", "scope", "sha", "revision", "protected", "count"} {
		copy := mapValue(cloneJSON(args))
		lease.Store(0)
		state.Store("conflict")
		protected.Store(false)
		switch bad {
		case "lease":
			lease.Store(time.Now().Add(time.Minute).Unix())
		case "ready":
			state.Store("ready")
		case "scope":
			copy["scope_digest"] = "changed"
		case "sha":
			copy["target_commits"] = []any{"HEAD"}
		case "revision":
			copy["expected_revision"] = 8
		case "protected":
			protected.Store(true)
		case "count":
			copy["target_commits"] = []any{sha, sha}
		}
		if _, _, _, err := s.Call(ctx, tok, "resume_code_preparation", copy); err == nil {
			t.Fatal("accepted", bad)
		}
	}
	if writes.Load() != 1 {
		t.Fatal("invalid preview wrote")
	}
	for _, bad := range []string{"owner", "environment", "unverified", "scope"} {
		copy := *tok
		read := map[string]any{"preparation_id": "fixture-a", "environment": "test"}
		switch bad {
		case "owner":
			copy.Owner = "bob"
			copy.role = "member"
		case "environment":
			read["environment"] = "other"
		case "unverified":
			copy.verified = false
		case "scope":
			copy.Scopes = []string{"logs:read"}
		}
		if _, _, _, err := s.Call(ctx, &copy, "get_code_conflict", read); err == nil {
			t.Fatal("handoff disclosure", bad)
		}
	}
	// A scoped app key may recover the exact child of its own publication.
	tok.Applications = []string{"orders"}
	tok.Scopes = append(tok.Scopes, "delivery:read", "delivery:write", "delivery:execute", "changes:read", "builds:trigger")
	read := map[string]any{"preparation_id": "fixture-a", "environment": "test", "publication_task_id": "publication"}
	scoped := docCall(t, s, tok, "get_code_conflict", read)
	if mapValue(scoped)["can_resume"] != true {
		t.Fatal(scoped)
	}
	args["publication_task_id"] = "other"
	if _, _, _, err := s.Call(ctx, tok, "resume_code_preparation", args); err == nil {
		t.Fatal("accepted unrelated parent")
	}
	args["publication_task_id"] = "publication"
	scoped = docCall(t, s, tok, "resume_code_preparation", args)
	if mapValue(mapValue(scoped)["preview"])["continues_publication"] != true {
		t.Fatal("missing publication effect")
	}
	docConfirm(t, s, tok, scoped)
	if writes.Load() != 2 {
		t.Fatal("publication recovery failed")
	}

}
