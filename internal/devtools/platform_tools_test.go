package devtools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func workflowToken(t *testing.T, store *TokenStore) *AccessToken {
	t.Helper()
	v, _, e := store.Create(CreateTokenRequest{Owner: "alice", Name: "workflow", Scopes: []string{"workitems:read", "workitems:write", "changes:read", "changes:write", "builds:read", "builds:trigger", "delivery:read"}, Applications: []string{"*"}, Environments: []string{"test"}})
	if e != nil {
		t.Fatal(e)
	}
	v.verified = true
	v.role = "member"
	v.identities = `[{"source":"zentao:bugs","external_id":"alice-z","username":"alice"}]`
	return &v
}
func TestWorkflowPreviewConfirmationCASIdentityAndOnce(t *testing.T) {
	var writes atomic.Int32
	var drift atomic.Bool
	var mu sync.Mutex
	var bodies []map[string]any
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-User") != "alice" || r.Header.Get("X-Role") != "member" || !strings.Contains(r.Header.Get("X-Research-Identities"), "alice-z") {
			t.Error("bound identity missing")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" {
			switch r.URL.Path {
			case "/api/services":
				name := "orders"
				if drift.Load() {
					name = "changed"
				}
				json.NewEncoder(w).Encode([]any{map[string]any{"name": name, "branch": "dev"}})
			case "/api/research/work-items/7":
				json.NewEncoder(w).Encode(map[string]any{"item": map[string]any{"id": 7, "title": "fixture work item", "source": "manual"}})
			case "/api/work-items/7/delivery-content":
				json.NewEncoder(w).Encode(map[string]any{"digest": "draft-v1", "changes": []any{map[string]any{"id": 1, "kind": "manual", "title": "keep"}}})
			default:
				json.NewEncoder(w).Encode(map[string]any{"digest": "source-v1", "resolutions": map[string]any{"fixed": "已修复"}})
			}
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		writes.Add(1)
		json.NewEncoder(w).Encode(map[string]any{"id": 9, "state": "success"})
	}))
	defer up.Close()
	store, _ := OpenTokenStore(t.TempDir())
	svc := NewToolService(Upstreams{Deploy: up.URL})
	svc.actions = store
	tok := workflowToken(t, store)
	call := func(name string, args map[string]any) any {
		t.Helper()
		v, _, _, e := svc.Call(context.Background(), tok, name, args)
		if e != nil {
			t.Fatal(name, e)
		}
		return v
	}
	preview := mapValue(call("trigger_build", map[string]any{"service": "orders", "branch": "feature/fix"}))
	if writes.Load() != 0 {
		t.Fatal("preview executed")
	}
	id := stringArg(preview, "confirmation_id")
	// Concurrent confirmation never sends the same write twice.
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _, e := svc.Call(context.Background(), tok, "confirm_platform_action", map[string]any{"confirmation_id": id, "confirm": true, "reason": "test"})
			if e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if writes.Load() != 1 {
		t.Fatal("duplicate build", writes.Load())
	}
	if bodies[0]["auto_deploy"] != false || bodies[0]["branch"] != "feature/fix" {
		t.Fatal(bodies)
	}
	// A caller cannot replace parameters in a frozen confirmation.
	if _, _, _, e := svc.Call(context.Background(), tok, "confirm_platform_action", map[string]any{"confirmation_id": id, "confirm": true, "reason": "test", "branch": "prod"}); e == nil {
		t.Fatal("parameter smuggling")
	}
	changed := mapValue(call("trigger_build", map[string]any{"service": "orders", "branch": "feature/fix"}))
	drift.Store(true)
	if _, _, _, e := svc.Call(context.Background(), tok, "confirm_platform_action", map[string]any{"confirmation_id": changed["confirmation_id"], "confirm": true, "reason": "test"}); e == nil {
		t.Fatal("drift accepted")
	}
	if writes.Load() != 1 {
		t.Fatal("stale configuration built")
	}
	drift.Store(false)
	changes := mapValue(call("add_work_item_changes", map[string]any{"work_item_id": 7, "changes": []any{map[string]any{"kind": "manual", "title": "new", "reference": "password=private"}}}))
	cid := stringArg(changes, "confirmation_id")
	if writes.Load() != 1 {
		t.Fatal("draft preview saved")
	}
	call("confirm_platform_action", map[string]any{"confirmation_id": cid, "confirm": true, "reason": "append"})
	rows := bodies[1]["changes"].([]any)
	if len(rows) != 1 || mapValue(rows[0])["title"] != "new" || mapValue(rows[0])["status"] != "draft" {
		t.Fatal("append must only submit new drafts; existing content is preserved server-side", rows)
	}
	if mapValue(rows[0])["reference"] != "password=private" {
		t.Fatal("response redaction mutated frozen request")
	}
	if mapValue(mapValue(rows[0])["delivery_ownership"])["mode"] != "draft" {
		t.Fatal("production ownership silently assigned")
	}
	bug := mapValue(call("change_work_item_state", map[string]any{"work_item_id": 7, "action": "resolve", "resolution": "fixed", "resolved_build": "trunk", "comment": "fixed"}))
	call("confirm_platform_action", map[string]any{"confirmation_id": bug["confirmation_id"], "confirm": true, "reason": "resolve"})
	if bodies[2]["expected_digest"] != "source-v1" || bodies[2]["operation_id"] != bug["confirmation_id"] {
		t.Fatal("source optimistic lock/idempotency missing")
	}
	// A second token from the same user cannot consume another token's preview.
	other := workflowToken(t, store)
	if _, e := store.actionStatus(other, id); e == nil {
		t.Fatal("cross-token action leak")
	}
}

func TestWorkflowUnknownOutcomeNeverReplayedAfterRestart(t *testing.T) {
	var n atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			w.Write([]byte(`[{"name":"orders"}]`))
			return
		}
		n.Add(1)
		w.WriteHeader(502)
		w.Write([]byte(`{"error":"unknown"}`))
	}))
	defer up.Close()
	dir := t.TempDir()
	store, _ := OpenTokenStore(dir)
	tok := workflowToken(t, store)
	svc := NewToolService(Upstreams{Deploy: up.URL})
	svc.actions = store
	v, _, _, e := svc.Call(context.Background(), tok, "trigger_build", map[string]any{"service": "orders", "branch": "dev"})
	if e != nil {
		t.Fatal(e)
	}
	args := map[string]any{"confirmation_id": mapValue(v)["confirmation_id"], "confirm": true, "reason": "test"}
	if _, _, _, e = svc.Call(context.Background(), tok, "confirm_platform_action", args); e == nil {
		t.Fatal("uncertain result reported success")
	}
	reopened, e := OpenTokenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	svc.actions = reopened
	v, _, _, e = svc.Call(context.Background(), tok, "confirm_platform_action", args)
	if e != nil || mapValue(v)["state"] != "uncertain" || n.Load() != 1 {
		t.Fatal("write replayed", v, e, n.Load())
	}
}

func TestWorkflowScopesSchemaAndDisabledUser(t *testing.T) {
	store, _ := OpenTokenStore(t.TempDir())
	svc := NewToolService(Upstreams{})
	tok := workflowToken(t, store)
	for _, owner := range []string{"", "local"} {
		if _, _, e := store.Create(CreateTokenRequest{Owner: owner, Name: "bad", Scopes: []string{"builds:trigger"}, Applications: []string{"*"}, Environments: []string{"*"}}); e == nil {
			t.Fatal("unbound write token")
		}
	}
	tok.Applications = []string{"orders"}
	if _, _, _, e := svc.Call(context.Background(), tok, "get_work_item", map[string]any{"work_item_id": 7}); e == nil {
		t.Fatal("cross-application work item read")
	}
	for _, id := range []any{1.5, "7", 0, -1} {
		tool, _ := svc.tool("get_work_item")
		if validateToolArgs(tool.InputSchema, map[string]any{"work_item_id": id}) == nil {
			t.Fatal("invalid id", id)
		}
	}
	legacy := &AccessToken{Scopes: []string{"builds:read"}, Applications: []string{"*"}, Environments: []string{"*"}}
	for _, tool := range svc.VisibleTools(legacy) {
		if isWriteScope(tool.Scope) || tool.Name == "confirm_platform_action" {
			t.Fatal("legacy token upgraded")
		}
	}
	_, raw, e := store.Create(CreateTokenRequest{Owner: "alice", Name: "bound", Scopes: []string{"builds:trigger"}, Applications: []string{"orders"}, Environments: []string{"test"}})
	if e != nil {
		t.Fatal(e)
	}
	h := NewMCPHandler(store, svc)
	request := func(directory string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
		r.Header.Set("Authorization", "Bearer "+raw)
		r.Header.Set("X-MCP-Users", directory)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if request(`[{"username":"alice","role":"member","enabled":true}]`).Code != 200 {
		t.Fatal("enabled owner rejected")
	}
	if request(`[{"username":"alice","role":"admin","enabled":false}]`).Code != 403 || request("").Code != 403 {
		t.Fatal("disabled/missing account accepted")
	}
	// Expiry is checked again when consuming an otherwise valid preview.
	a := PlatformAction{ID: "expired", TokenID: tok.ID, Owner: tok.Owner, Scope: "builds:trigger", State: "preview", ExpiresAt: time.Now().Add(-time.Minute).Unix()}
	store.addAction(a)
	if _, _, e = store.claimAction(tok, "expired"); e == nil {
		t.Fatal("expired preview consumed")
	}
}

func TestWorkflowReadsFilterActualApplicationAndEnvironment(t *testing.T) {
	var logs atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/changes":
			w.Write([]byte(`[{"service":"orders","id":1},{"service":"secret","id":2}]`))
		case "/api/services/orders/resources":
			w.Write([]byte(`{"bindings":[{"environment":"test"},{"environment":"prod"}],"requirements":[]}`))
		case "/api/pipeline/runs/9":
			w.Write([]byte(`{"service":"secret","id":9}`))
		case "/api/pipeline/runs/9/log":
			logs.Add(1)
			w.Write([]byte(`{"log":"private"}`))
		case "/api/delivery/runs/7":
			w.Write([]byte(`{"plan":{"environment":{"name":"test"},"services":[{"service":"orders"},{"service":"secret"}],"scope":{"changes":[]}}}`))
		default:
			w.Write([]byte(`[]`))
		}
	}))
	defer up.Close()
	store, _ := OpenTokenStore(t.TempDir())
	tok := workflowToken(t, store)
	tok.Applications = []string{"orders"}
	svc := NewToolService(Upstreams{Deploy: up.URL})
	svc.actions = store
	v, _, _, e := svc.Call(context.Background(), tok, "list_changes", map[string]any{"service": "orders"})
	if e != nil || len(mapValue(v)["items"].([]any)) != 1 {
		t.Fatal(v, e)
	}
	v, _, _, e = svc.Call(context.Background(), tok, "get_application_resources", map[string]any{"service": "orders", "environment": "test"})
	if e != nil || len(mapValue(v)["bindings"].([]any)) != 1 {
		t.Fatal(v, e)
	}
	if _, _, _, e = svc.Call(context.Background(), tok, "get_pipeline_run", map[string]any{"service": "orders", "run_id": 9}); e == nil || logs.Load() != 0 {
		t.Fatal("ID-based build log leak")
	}
	if _, _, _, e = svc.Call(context.Background(), tok, "get_delivery_run", map[string]any{"run_id": 7, "environment": "test"}); e == nil {
		t.Fatal("multi-app delivery leak")
	}
	if _, _, _, e = svc.Call(context.Background(), tok, "get_application_resources", map[string]any{"service": "orders", "environment": "prod"}); e == nil {
		t.Fatal("production scope leak")
	}
}

func TestTokenOwnerCannotBeChosenInRequestOrRevokedByAnotherUser(t *testing.T) {
	store, _ := OpenTokenStore(t.TempDir())
	h := Routes(store, NewToolService(Upstreams{}))
	r := httptest.NewRequest("POST", "/api/tokens", strings.NewReader(`{"name":"alice-token","owner":"admin","scopes":["builds:trigger"],"applications":["orders"],"environments":["test"]}`))
	r.Header.Set("X-User", "alice")
	r.Header.Set("X-Role", "member")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	rows := store.List()
	if len(rows) != 1 || rows[0].Owner != "alice" {
		t.Fatal("forged token owner", rows)
	}
	r = httptest.NewRequest("DELETE", "/api/tokens/"+rows[0].ID, nil)
	r.Header.Set("X-User", "bob")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 || store.List()[0].Revoked {
		t.Fatal("cross-user revocation")
	}
}
