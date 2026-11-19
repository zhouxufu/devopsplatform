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

func TestCapabilityRegistryMatchesMCPListForToken(t *testing.T) {
	store, _ := OpenTokenStore(t.TempDir())
	svc := NewToolService(Upstreams{})
	token, secret, _ := store.Create(CreateTokenRequest{Owner: "alice", Name: "agent", Scopes: []string{"delivery:prepare", "changes:read", "changes:write", "resources:read"}, Applications: []string{"*"}, Environments: []string{"test"}})
	h := NewMCPHandler(store, svc)
	r := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	r.Header.Set("Authorization", "Bearer "+secret)
	r.Header.Set("X-MCP-Users", `[{"username":"alice","role":"admin","enabled":true}]`)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var rpc struct {
		Result struct {
			Tools []Tool `json:"tools"`
		} `json:"result"`
	}
	if json.Unmarshal(w.Body.Bytes(), &rpc) != nil || len(rpc.Result.Tools) == 0 {
		t.Fatal(w.Body.String())
	}
	token.verified = true
	token.role = "admin"
	caps := mapValue(svc.agentCapabilities(&token))
	visible := caps["tools"].([]any)
	if len(visible) != len(rpc.Result.Tools) {
		t.Fatal("manifest differs")
	}
	names := map[string]bool{}
	for i, tool := range rpc.Result.Tools {
		if tool.Name != mapValue(visible[i])["name"] {
			t.Fatal("mismatch")
		}
		names[tool.Name] = true
	}
	for _, n := range []string{"confirm_delivery_scope", "get_application_code_policy", "get_change_maintenance", "maintain_change", "check_database_schema", "check_configuration_keys"} {
		if !names[n] {
			t.Fatal("missing", n)
		}
	}
	token.Scopes = []string{"delivery:read"}
	caps = mapValue(svc.agentCapabilities(&token))
	if strings.Contains(mapValue(caps["delivery"])["scope_confirmation"].(string), "confirm_delivery_scope:") {
		t.Fatal("unsupported capability advertised")
	}
}
func TestInspectionMCPRequiresResourceScopeAndExactTarget(t *testing.T) {
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if r.URL.Path != "/api/services/orders/resources/config-key-check" || r.Method != "POST" || req["environment"] != "test" || req["alias"] != "app" || req["connection"] != nil {
			t.Error("wrong target", req, r.URL)
		}
		json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"key": "feature.enabled", "exists": true, "type": "bool"}}, "read_only": true})
	}))
	defer up.Close()
	svc := NewToolService(Upstreams{Deploy: up.URL})
	token := &AccessToken{Owner: "alice", verified: true, role: "admin", Scopes: []string{"resources:read"}, Applications: []string{"orders"}, Environments: []string{"test"}}
	args := map[string]any{"service": "orders", "environment": "test", "alias": "app", "format": "yaml", "keys": []any{"feature.enabled"}}
	for _, bad := range []string{"scope", "role", "environment", "application"} {
		copy := *token
		copyArgs := mapValue(cloneJSON(args))
		switch bad {
		case "scope":
			copy.Scopes = []string{"changes:read"}
		case "role":
			copy.role = "member"
		case "environment":
			copyArgs["environment"] = "prod"
		case "application":
			copyArgs["service"] = "other"
		}
		if _, _, _, err := svc.Call(context.Background(), &copy, "check_configuration_keys", copyArgs); err == nil {
			t.Fatal("denied context accepted", bad)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("denied token reached upstream")
	}
	if _, _, _, err := svc.Call(context.Background(), token, "check_configuration_keys", args); err != nil || calls.Load() != 1 {
		t.Fatal(err)
	}
	args["connection"] = "other"
	if _, _, _, err := svc.Call(context.Background(), token, "check_configuration_keys", args); err == nil || calls.Load() != 1 {
		t.Fatal("arbitrary connection accepted")
	}
}

func TestEnvironmentBuildPreviewPinsPolicyAndRejectsRequirementSource(t *testing.T) {
	var builds atomic.Int32
	var digest atomic.Value
	digest.Store("policy-1")
	var protected atomic.Bool
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			json.NewEncoder(w).Encode(map[string]any{"service": "orders", "environment": "test", "policy_digest": digest.Load(), "code_policy": map[string]any{"mode": "integration", "target_branch": "master-dev-optimize"}, "workflow_binding": map[string]any{"protected": protected.Load(), "purpose": "development"}})
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["build_environment"] != "test" || body["branch"] != "master-dev-optimize" || body["auto_deploy"] != false {
			t.Error(body)
		}
		builds.Add(1)
		json.NewEncoder(w).Encode(map[string]any{"id": 1})
	}))
	defer up.Close()
	store, _ := OpenTokenStore(t.TempDir())
	svc := NewToolService(Upstreams{Deploy: up.URL})
	svc.actions = store
	token := workflowToken(t, store)
	if _, _, _, err := svc.Call(context.Background(), token, "trigger_build", map[string]any{"service": "orders", "environment": "test", "branch": "master-house-claim"}); err == nil {
		t.Fatal("requirement source substituted for target")
	}
	args := map[string]any{"service": "orders", "environment": "test", "branch": "master-dev-optimize"}
	preview := docCall(t, svc, token, "trigger_build", args)
	if builds.Load() != 0 {
		t.Fatal("preview built")
	}
	docConfirm(t, svc, token, preview)
	if builds.Load() != 1 {
		t.Fatal("not built exactly once")
	}
	preview = docCall(t, svc, token, "trigger_build", args)
	digest.Store("policy-2")
	if _, _, _, err := svc.Call(context.Background(), token, "confirm_platform_action", map[string]any{"confirmation_id": mapValue(preview)["confirmation_id"], "confirm": true, "reason": "checked"}); err == nil || builds.Load() != 1 {
		t.Fatal("changed policy accepted")
	}
	preview = docCall(t, svc, token, "trigger_build", args)
	protected.Store(true)
	if _, _, _, err := svc.Call(context.Background(), token, "confirm_platform_action", map[string]any{"confirmation_id": mapValue(preview)["confirmation_id"], "confirm": true, "reason": "checked"}); err == nil || builds.Load() != 1 {
		t.Fatal("protected environment change accepted")
	}

}
