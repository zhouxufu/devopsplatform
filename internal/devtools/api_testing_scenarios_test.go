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

	"logplatform/internal/deploy"
)

func TestScenarioMCPRealSaveReadLayoutScopeAndRevocation(t *testing.T) {
	st, err := deploy.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for _, name := range []string{"orders", "payments"} {
		if err = st.UpsertService(deploy.Service{Name: name, ArtifactType: "jar"}); err != nil {
			t.Fatal(err)
		}
	}
	hub := deploy.NewLogHub()
	routes := deploy.Routes(st, deploy.NewBuilder(st, t.TempDir(), hub), hub, "", deploy.NewStepLib(t.TempDir()))
	var saves atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/api/api-testing/scenario-cases" {
			saves.Add(1)
		}
		routes.ServeHTTP(w, r)
	}))
	t.Cleanup(up.Close)
	store, _ := OpenTokenStore(t.TempDir())
	svc := NewToolService(Upstreams{Deploy: up.URL})
	svc.actions = store
	token := workflowToken(t, store)
	token.Scopes = append(token.Scopes, "apitests:read", "apitests:write")
	token.Applications = []string{"orders", "payments"}
	args := map[string]any{"environment": "test", "case": map[string]any{"id": 0, "revision": 0, "name": "跨应用业务场景", "steps": []any{map[string]any{"id": "a", "service": "orders", "name": "查询订单", "request": map[string]any{"path": "/orders/7"}}, map[string]any{"id": "b", "service": "payments", "name": "核对付款", "request": map[string]any{"path": "/payments/7"}}}}}
	checked := mapValue(docCall(t, svc, token, "validate_api_test_case", args))
	args["case"] = checked["case"]
	preview := docCall(t, svc, token, "save_api_test_case", args)
	if saves.Load() != 0 {
		t.Fatal("preview saved")
	}
	saved := mapValue(docConfirm(t, svc, token, preview)["result"])
	id := intArg(saved, "id", 0)
	if id < 1 || saves.Load() != 1 {
		t.Fatal(saved)
	}
	read := mapValue(docCall(t, svc, token, "get_api_test_case", map[string]any{"case_id": id, "environment": "test"}))
	if stringArg(mapValue(read["case"]), "scope") != "scenario" {
		t.Fatal(read)
	}
	layout := map[string]any{"case_id": id, "environment": "test", "layout": map[string]any{"revision": 0, "positions": map[string]any{"a": map[string]any{"x": 50, "y": 20}}, "zoom": 1, "pan": map[string]any{"x": 0, "y": 0}}}
	docConfirm(t, svc, token, docCall(t, svc, token, "save_api_test_layout", layout))
	result := mapValue(docCall(t, svc, token, "get_api_test_layout", map[string]any{"case_id": id, "environment": "test"}))
	if intArg(result, "revision", 0) != 1 {
		t.Fatal(result)
	}
	preview = docCall(t, svc, token, "save_api_test_case", args)
	token.Applications = []string{"orders"}
	if _, _, _, err = svc.Call(context.Background(), token, "confirm_platform_action", map[string]any{"confirmation_id": mapValue(preview)["confirmation_id"], "confirm": true, "reason": "revocation"}); err == nil || saves.Load() != 1 {
		t.Fatal("revoked application saved", err)
	}
	if _, _, _, err = svc.Call(context.Background(), token, "get_api_test_case", map[string]any{"case_id": id, "environment": "test"}); err == nil {
		t.Fatal("unauthorized node read")
	}
	list := mapValue(docCall(t, svc, token, "list_api_test_cases", map[string]any{"environment": "test"}))
	if len(list["cases"].([]any)) != 0 {
		t.Fatal("unauthorized summary leaked")
	}
}

func TestScenarioMCPExecutionFreezesEveryTargetDriftAndUncertainty(t *testing.T) {
	var writes atomic.Int32
	var drift atomic.Bool
	c := map[string]any{"id": 1, "revision": 1, "scope": "scenario", "steps": []any{map[string]any{"service": "orders"}, map[string]any{"service": "payments"}}}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" {
			if strings.HasSuffix(r.URL.Path, "/context") {
				apps := []any{}
				for _, name := range []string{"orders", "payments"} {
					fp := "target-" + name
					if drift.Load() && name == "payments" {
						fp = "changed"
					}
					apps = append(apps, map[string]any{"service": name, "enabled": true, "variables_digest": "vars-" + name, "targets": []any{map[string]any{"key": "entry", "fingerprint": fp, "credential_references_valid": true}}})
				}
				json.NewEncoder(w).Encode(map[string]any{"case": c, "protected": false, "applications": apps})
				return
			}
			json.NewEncoder(w).Encode(c)
			return
		}
		if r.URL.Path != "/api/api-testing/scenario-runs" {
			t.Error("unexpected write", r.URL.Path)
		}
		var input map[string]any
		json.NewDecoder(r.Body).Decode(&input)
		if len(mapValue(input["targets"])) != 2 || input["request_id"] == "" {
			t.Error("lost frozen input")
		}
		writes.Add(1)
		fmt.Fprint(w, "invalid-json")
	}))
	defer up.Close()
	store, _ := OpenTokenStore(t.TempDir())
	svc := NewToolService(Upstreams{Deploy: up.URL})
	svc.actions = store
	token := workflowToken(t, store)
	token.Scopes = append(token.Scopes, "apitests:execute")
	token.Applications = []string{"orders", "payments"}
	args := map[string]any{"environment": "test", "case_id": 1, "case_revision": 1, "targets": map[string]any{"orders": map[string]any{"key": "entry", "fingerprint": "target-orders", "variables_digest": "vars-orders"}, "payments": map[string]any{"key": "entry", "fingerprint": "target-payments", "variables_digest": "vars-payments"}}}
	preview := mapValue(docCall(t, svc, token, "execute_api_test", args))
	confirm := map[string]any{"confirmation_id": preview["confirmation_id"], "confirm": true, "reason": "fixture"}
	drift.Store(true)
	if _, _, _, err := svc.Call(context.Background(), token, "confirm_platform_action", confirm); err == nil || writes.Load() != 0 {
		t.Fatal("second target drift executed")
	}
	drift.Store(false)
	preview = mapValue(docCall(t, svc, token, "execute_api_test", args))
	confirm["confirmation_id"] = preview["confirmation_id"]
	if _, _, _, err := svc.Call(context.Background(), token, "confirm_platform_action", confirm); err == nil {
		t.Fatal("unknown outcome accepted")
	}
	docCall(t, svc, token, "confirm_platform_action", confirm)
	if writes.Load() != 1 {
		t.Fatal("uncertain scenario replayed")
	}
	status := mapValue(docCall(t, svc, token, "get_platform_action", map[string]any{"confirmation_id": preview["confirmation_id"]}))
	if status["state"] != "uncertain" {
		t.Fatal(status)
	}
}
