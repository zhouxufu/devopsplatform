package devtools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestResourceToolsRequireDedicatedScopeAndConfirmedFrozenRequest(t *testing.T) {
	var previews, writes atomic.Int32
	var revision atomic.Int64
	revision.Store(3)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" {
			json.NewEncoder(w).Encode(map[string]any{"revision": revision.Load()})
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if strings.HasSuffix(r.URL.Path, "/preview") {
			previews.Add(1)
			body["connection_revisions"] = map[string]string{"db": "frozen-target"}
			json.NewEncoder(w).Encode(map[string]any{"request": body, "no_execution": true})
			return
		}
		if mapValue(body["connection_revisions"])["db"] != "frozen-target" {
			t.Error("lost frozen connection identity")
		}
		writes.Add(1)
		json.NewEncoder(w).Encode(map[string]any{"saved": true, "no_execution": true})
	}))
	defer up.Close()
	store, _ := OpenTokenStore(t.TempDir())
	svc := NewToolService(Upstreams{Deploy: up.URL})
	svc.actions = store
	old := workflowToken(t, store)
	args := map[string]any{"service": "orders", "environment": "test", "revision": 3, "bindings": []any{map[string]any{"alias": "db", "environment": "test", "connection": "db", "config": map[string]any{"database": "orders"}}}}
	call := func(tok *AccessToken, name string, args map[string]any) (any, error) {
		v, _, _, err := svc.Call(context.Background(), tok, name, args)
		return v, err
	}
	if _, err := call(old, "configure_application_resources", args); err == nil {
		t.Fatal("legacy scope gained resource writes")
	}
	tok, _, err := store.Create(CreateTokenRequest{Owner: "alice", Name: "resources", Scopes: []string{"resources:read", "resources:write"}, Applications: []string{"orders"}, Environments: []string{"test"}})
	if err != nil {
		t.Fatal(err)
	}
	tok.verified = true
	tok.role = "member"
	if _, err = call(&tok, "configure_application_resources", args); err == nil {
		t.Fatal("member resource write accepted")
	}
	tok.role = "admin"
	preview, err := call(&tok, "configure_application_resources", args)
	if err != nil {
		t.Fatal(err)
	}
	if previews.Load() != 1 || writes.Load() != 0 {
		t.Fatal("preview caused a write")
	}
	id := mapValue(preview)["confirmation_id"]
	if _, err = call(&tok, "confirm_platform_action", map[string]any{"confirmation_id": id, "confirm": true, "reason": "reviewed test target"}); err != nil {
		t.Fatal(err)
	}
	if writes.Load() != 1 {
		t.Fatal(writes.Load())
	}
	if _, err = call(&tok, "confirm_platform_action", map[string]any{"confirmation_id": id, "confirm": true, "reason": "same"}); err != nil || writes.Load() != 1 {
		t.Fatal("replayed", err)
	}
	preview, err = call(&tok, "configure_application_resources", args)
	if err != nil {
		t.Fatal(err)
	}
	revision.Store(4)
	if _, err = call(&tok, "confirm_platform_action", map[string]any{"confirmation_id": mapValue(preview)["confirmation_id"], "confirm": true, "reason": "stale"}); err == nil || writes.Load() != 1 {
		t.Fatal("drift accepted")
	}
	if _, err = call(&tok, "list_resource_connections", map[string]any{}); err == nil {
		t.Fatal("scoped token leaked global connections")
	}
}
func TestApplicationResourceProjectionFiltersEveryEnvironmentArray(t *testing.T) {
	value := map[string]any{"revision": 7, "requirements": []any{map[string]any{"alias": "db","sources":map[string]any{"test":map[string]any{"file":"allowed"},"prod":map[string]any{"file":"must-not-leak"}}}}}
	for _, field := range []string{"bindings", "own_bindings", "nacos_configs", "checks"} {
		value[field] = []any{map[string]any{"environment": "test", "connection": "allowed"}, map[string]any{"environment": "prod", "connection": "must-not-leak"}}
	}
	projected := projectResourceEnvironment(value, "test")
	raw, _ := json.Marshal(projected)
	if strings.Contains(string(raw), "must-not-leak") {
		t.Fatal("cross-environment data leaked")
	}
	if len(mapValue(projected)["bindings"].([]any)) != 1 {
		t.Fatal(projected)
	}
}
func TestResourceConnectionToolDoesNotAcceptPasswords(t *testing.T) {
	svc := NewToolService(Upstreams{})
	tool, _ := svc.tool("configure_resource_connection")
	args := map[string]any{"name": "db", "kind": "mysql", "endpoint": "127.0.0.1:3306", "revision": "missing", "credential_connection": "saved", "password": "secret"}
	if validateToolArgs(tool.InputSchema, args) == nil {
		t.Fatal("plaintext password accepted")
	}
}
func TestResourceReadNeverLeaksCredentialsOrUsesForeignEnvironmentConnection(t *testing.T) {
	var probes atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/connections":
			json.NewEncoder(w).Encode([]any{map[string]any{"name": "test-config", "kind": "nacos", "endpoint": "http://127.0.0.1:1", "password": "DO-NOT-LEAK", "config": map[string]any{"namespace": "testing", "password": "DO-NOT-LEAK"}}})
		case "/api/services/orders/resources":
			json.NewEncoder(w).Encode(map[string]any{"requirements": []any{map[string]any{"alias": "config", "kind": "nacos"}}, "bindings": []any{map[string]any{"environment": "test", "alias": "config", "connection": "test-config"}, map[string]any{"environment": "prod", "alias": "config", "connection": "prod-config"}}})
		case "/api/services/orders/resources/discovery-connections":
			json.NewEncoder(w).Encode([]any{map[string]any{"name": "test-config", "kind": "nacos", "endpoint": "http://127.0.0.1:1", "config": map[string]any{"namespace": "testing"}}})
		default:
			probes.Add(1)
			fmt.Fprint(w, `{"items":[]}`)
		}
	}))
	defer up.Close()
	svc := NewToolService(Upstreams{Deploy: up.URL})
	tok := &AccessToken{Owner: "admin", role: "admin", verified: true, Scopes: []string{"resources:read"}, Applications: []string{"orders"}, Environments: []string{"test"}}
	if _, _, _, err := svc.Call(context.Background(), tok, "discover_application_resources", map[string]any{"service": "orders", "environment": "test", "connection": "prod-config"}); err == nil || probes.Load() != 0 {
		t.Fatal("foreign connection read allowed")
	}
	tok.Applications = []string{"*"}
	tok.Environments = []string{"*"}
	v, _, _, err := svc.Call(context.Background(), tok, "list_resource_connections", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(v)
	if strings.Contains(string(raw), "DO-NOT-LEAK") || !strings.Contains(string(raw), "testing") {
		t.Fatal("metadata is unsafe or unusable")
	}
}
