package devtools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReleaseDiagnosticsMCP(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/releases/51/diagnostics" || r.URL.Query().Get("environment") != "dev" {
			t.Error("unexpected upstream request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"service": "orders", "environment": "dev", "release_id": 51, "targets": []any{map[string]any{"log_tail": "old line\nSelected Java is not executable\ntoken=hidden-value"}}})
	}))
	defer backend.Close()
	svc := NewToolService(Upstreams{Deploy: backend.URL})
	token := &AccessToken{Applications: []string{"orders", "other"}, Environments: []string{"dev"}, Scopes: []string{"releases:read"}}
	args := map[string]any{"service": "orders", "environment": "dev", "release_id": 51, "tail_lines": 2}
	call := func() (any, error) {
		result, _, _, err := svc.Call(context.Background(), token, "get_release_diagnostics", args)
		return result, err
	}
	result, err := call()
	raw, _ := json.Marshal(result)
	if err != nil || !strings.Contains(string(raw), "Selected Java is not executable") || strings.Contains(string(raw), "old line") || strings.Contains(string(raw), "hidden-value") {
		t.Fatalf("unexpected diagnostic: %s %v", raw, err)
	}
	args["service"] = "other"
	if _, err := call(); err == nil {
		t.Fatal("accepted a different application's response")
	}
	args["environment"] = "test"
	if _, err := call(); err == nil {
		t.Fatal("accepted an unauthorized environment")
	}
	args["service"], args["environment"], token.Scopes = "orders", "dev", nil
	if _, err := call(); err == nil {
		t.Fatal("release read scope was not enforced")
	}
}

func TestFailedServiceDeploymentPointsToOriginalEvidence(t *testing.T) {
	for _, scenario := range []struct {
		id    int
		scope string
		tool  string
	}{
		{0, "builds:read", "get_build_log"},
		{51, "releases:read", "get_release_diagnostics"},
		{51, "builds:read", ""},
	} {
		backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "GET" || r.URL.Path != "/api/service-deployments/example" {
				t.Error("unexpected upstream request")
				w.WriteHeader(400)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"owner": "alice", "state": "failed", "run_id": 62, "release_id": scenario.id, "grant": map[string]any{"spec": map[string]any{"service": "orders", "environment": "dev"}}})
		}))
		svc := NewToolService(Upstreams{Deploy: backend.URL})
		token := &AccessToken{Owner: "alice", verified: true, Applications: []string{"orders"}, Environments: []string{"dev"}, Scopes: []string{"delivery:read", scenario.scope}}
		result, err := svc.callServiceDeployment(context.Background(), token, "get_service_deployment", map[string]any{"task_id": "example"})
		backend.Close()
		if err != nil || stringArg(mapValue(mapValue(result)["diagnostics"]), "tool") != scenario.tool {
			t.Fatalf("expected %q for release %d, got %v %v", scenario.tool, scenario.id, result, err)
		}
	}
}
