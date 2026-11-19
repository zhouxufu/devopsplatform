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

func TestScopedResourceOnboardingListsOnlyEnvironmentCandidatesAndForcesSourceProof(t *testing.T) {
	var discoveries atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/services/orders/resources":
			json.NewEncoder(w).Encode(map[string]any{"requirements": []any{}, "bindings": []any{}})
		case "/api/services/orders/resources/discovery-connections":
			if r.URL.Query().Get("environment") != "test" {
				t.Error("wrong environment")
			}
			json.NewEncoder(w).Encode([]any{map[string]any{"name": "same-environment", "kind": "nacos", "endpoint": "http://127.0.0.1:1", "password": "NEVER-EXPOSE", "config": map[string]any{"namespace": "test-space", "password": "NEVER-EXPOSE"}}})
		case "/api/services/orders/resources/discover":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["require_environment_scope"] != true || mapValue(body["connections"])["test"] != "same-environment" || body["runtime_name"] != "runtime-orders" || body["adapter"] != "flink" || body["job"] != "chosen" {
				t.Error("source guard or explicit selectors lost", body)
			}
			discoveries.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
		default:
			t.Errorf("unscoped request %s", r.URL.Path)
			http.Error(w, "forbidden", 403)
		}
	}))
	defer up.Close()
	s := NewToolService(Upstreams{Deploy: up.URL})
	tok := &AccessToken{Owner: "alice", role: "admin", verified: true, Scopes: []string{"resources:read"}, Applications: []string{"orders"}, Environments: []string{"test"}}
	listed, _, _, err := s.Call(context.Background(), tok, "list_resource_connections", map[string]any{"service": "orders", "environment": "test"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(listed)
	if string(raw) == "[]" || strings.Contains(string(raw), "NEVER-EXPOSE") {
		t.Fatal(string(raw))
	}
	args := map[string]any{"service": "orders", "environment": "test", "connection": "same-environment", "runtime_name": "runtime-orders", "adapter": "flink", "job": "chosen", "config_data_id": "fixed.yaml", "config_group": "DI_GROUP"}
	if _, _, _, err = s.Call(context.Background(), tok, "discover_application_resources", args); err != nil || discoveries.Load() != 1 {
		t.Fatal(err, discoveries.Load())
	}
	for _, other := range []map[string]any{{"service": "orders", "environment": "prod", "connection": "same-environment"}, {"service": "other", "environment": "test", "connection": "same-environment"}, {"service": "orders", "environment": "test", "connection": "foreign-environment"}, {"service": "orders", "environment": "test", "connection": "same-environment", "require_environment_scope": false}} {
		if _, _, _, err = s.Call(context.Background(), tok, "discover_application_resources", other); err == nil {
			t.Fatal("scope widened", other)
		}
	}
	if discoveries.Load() != 1 {
		t.Fatal("foreign discovery reached backend")
	}
	if _, _, _, err = s.Call(context.Background(), tok, "list_resource_connections", map[string]any{}); err == nil {
		t.Fatal("scoped token read global registry")
	}
	if _, _, _, err = s.Call(context.Background(), tok, "configure_resource_connection", map[string]any{}); err == nil {
		t.Fatal("read scope gained configuration")
	}
	visible := false
	for _, tool := range s.VisibleTools(tok) {
		visible = visible || tool.Name == "list_resource_connections"
	}
	if !visible {
		t.Fatal("scoped onboarding remains undiscoverable")
	}
}
