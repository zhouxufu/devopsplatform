package devtools

import (
	"context"
	"encoding/json"
	"fmt"
	"logplatform/internal/deploy"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func testingAgentFixture(t *testing.T) (*ToolService, *AccessToken, *atomic.Int32) {
	t.Helper()
	st, e := deploy.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { st.Close() })
	for _, name := range []string{"orders", "other"} {
		if e = st.UpsertService(deploy.Service{Name: name, ArtifactType: "jar"}); e != nil {
			t.Fatal(e)
		}
	}
	if e = st.UpsertEnvironment(deploy.Environment{Name: "test", DisplayName: "测试"}); e != nil {
		t.Fatal(e)
	}
	if e = st.UpsertHost(deploy.Host{Name: "fixture", Environment: "test", Addr: "192.0.2.55:22", User: "fixture"}); e != nil {
		t.Fatal(e)
	}
	if e = st.UpsertHostGroup(deploy.HostGroup{Name: "fixture", Environment: "test", Hosts: []string{"fixture"}}); e != nil {
		t.Fatal(e)
	}
	if e = st.UpsertServiceEnvironment(deploy.ServiceEnvironment{Service: "orders", Environment: "test", HostGroup: "fixture", TargetType: "ssh-jar", RemoteDir: "/srv/fixture", StartCmd: "java -jar app.jar"}); e != nil {
		t.Fatal(e)
	}
	hub := deploy.NewLogHub()
	routes := deploy.Routes(st, deploy.NewBuilder(st, t.TempDir(), hub), hub, "", deploy.NewStepLib(t.TempDir()))
	writes := &atomic.Int32{}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			writes.Add(1)
		}
		routes.ServeHTTP(w, r)
	}))
	t.Cleanup(up.Close)
	store, e := OpenTokenStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	tok := workflowToken(t, store)
	tok.Applications = []string{"orders"}
	tok.Scopes = append(tok.Scopes, "apitests:read", "apitests:write", "apitests:execute")
	svc := NewToolService(Upstreams{Deploy: up.URL})
	svc.actions = store
	return svc, tok, writes
}

func TestAPITestCredentialTemplatesRoundTripThroughMCP(t *testing.T) {
	svc, token, _ := testingAgentFixture(t)
	consumer := map[string]any{"path": "/share?shareToken={{step.share}}&page=2", "headers": map[string]any{"Authorization": "Bearer {{step.share}}", "User-Agent": "house/2"}}
	args := map[string]any{"service": "orders", "environment": "test", "case": map[string]any{"name": "分享流程", "steps": []any{
		map[string]any{"name": "登录", "request": map[string]any{"path": "/login"}, "extractions": []any{map[string]any{"name": "share", "source": "json", "path": "/data/token"}}},
		map[string]any{"name": "读取分享", "request": consumer},
	}}}
	validated := mapValue(docCall(t, svc, token, "validate_api_test_case", args))
	args["case"] = validated["case"]
	preview := mapValue(docCall(t, svc, token, "save_api_test_case", args))
	shown := mapValue(mapValue(preview["preview"])["after"])["steps"].([]any)
	if mapValue(mapValue(shown[1])["request"])["path"] != consumer["path"] {
		t.Fatal("confirmation preview lost the query template")
	}
	saved := mapValue(docConfirm(t, svc, token, preview)["result"])
	read := mapValue(docCall(t, svc, token, "get_api_test_case", map[string]any{"service": "orders", "environment": "test", "case_id": saved["id"]}))
	steps := mapValue(read["case"])["steps"].([]any)
	request := mapValue(mapValue(steps[1])["request"])
	if request["path"] != consumer["path"] || mapValue(request["headers"])["Authorization"] != "Bearer {{step.share}}" {
		t.Fatal("saved case lost a credential template")
	}
}
func TestTestingAgentSuiteMCPRealRoutesFrozenSaveAndRevocation(t *testing.T) {
	svc, tok, writes := testingAgentFixture(t)
	args := map[string]any{"service": "orders", "environment": "test", "case": map[string]any{"id": 0, "revision": 0, "name": "agent contract", "steps": []any{map[string]any{"name": "status", "request": map[string]any{"path": "/contract", "assertions": []any{map[string]any{"type": "status", "expected": "200"}}}}}}}
	saved := mapValue(docConfirm(t, svc, tok, docCall(t, svc, tok, "save_api_test_case", args))["result"])
	id := intArg(saved, "id", 0)
	if id < 1 {
		t.Fatal(saved)
	}
	suite := map[string]any{"id": 0, "revision": 0, "name": "agent regression", "case_ids": []any{id}, "visibility": "private"}
	suiteArgs := map[string]any{"service": "orders", "environment": "test", "suite": suite}
	before := writes.Load()
	checked := mapValue(docCall(t, svc, tok, "validate_api_test_suite", suiteArgs))
	if checked["valid"] != true || writes.Load() != before+1 {
		t.Fatal("invalid static preview", checked)
	}
	preview := docCall(t, svc, tok, "save_api_test_suite", suiteArgs)
	page := mapValue(docCall(t, svc, tok, "list_api_test_suites", map[string]any{"service": "orders", "environment": "test"}))
	if len(page["items"].([]any)) != 0 {
		t.Fatal("preview persisted")
	}
	// Changing a member after the preview invalidates the full frozen plan.
	saved["name"] = "changed before confirm"
	args["case"] = saved
	saved = mapValue(docConfirm(t, svc, tok, docCall(t, svc, tok, "save_api_test_case", args))["result"])
	_, _, _, err := svc.Call(context.Background(), tok, "confirm_platform_action", map[string]any{"confirmation_id": mapValue(preview)["confirmation_id"], "confirm": true, "reason": "fixture"})
	if err == nil {
		t.Fatal("changed member accepted")
	}
	preview = docCall(t, svc, tok, "save_api_test_suite", suiteArgs)
	stored := mapValue(docConfirm(t, svc, tok, preview)["result"])
	suiteID := intArg(stored, "id", 0)
	if suiteID < 1 {
		t.Fatal(stored)
	}
	count := writes.Load()
	docConfirm(t, svc, tok, preview)
	if writes.Load() != count {
		t.Fatal("duplicate confirmation wrote again")
	}
	read := mapValue(docCall(t, svc, tok, "get_api_test_suite", map[string]any{"service": "orders", "environment": "test", "suite_id": suiteID}))
	if intArg(mapValue(read["suite"]), "revision", 0) != 1 || len(read["cases"].([]any)) != 1 || intArg(mapValue(read["case_revisions"]), fmt.Sprint(id), 0) != 2 {
		t.Fatal(read)
	}
	page = mapValue(docCall(t, svc, tok, "list_api_test_cases", map[string]any{"service": "orders", "environment": "test", "q": "changed", "limit": 1}))
	if len(page["items"].([]any)) != 1 {
		t.Fatal(page)
	}
	stored["name"] = "renamed"
	suiteArgs["suite"] = stored
	preview = docCall(t, svc, tok, "save_api_test_suite", suiteArgs)
	tok.Scopes = []string{"apitests:read"}
	count = writes.Load()
	_, _, _, err = svc.Call(context.Background(), tok, "confirm_platform_action", map[string]any{"confirmation_id": mapValue(preview)["confirmation_id"], "confirm": true, "reason": "fixture"})
	if err == nil || writes.Load() != count {
		t.Fatal("revoked write accepted", err)
	}
	for _, name := range []string{"save_api_test_suite", "stop_api_test_run", "execute_api_test"} {
		for _, tool := range svc.VisibleTools(tok) {
			if tool.Name == name {
				t.Fatal("unauthorized tool visible", name)
			}
		}
	}
}

func TestTestingAgentSuiteExecutionFreezesRevisionsAndDoesNotReplay(t *testing.T) {
	var writes atomic.Int32
	var changed atomic.Bool
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" {
			revision := 1
			if changed.Load() {
				revision = 2
			}
			json.NewEncoder(w).Encode(map[string]any{"enabled": true, "service": "orders", "environment": "test", "variables_digest": "vars", "targets": []any{map[string]any{"key": "direct", "fingerprint": "target", "base_url": "https://fixture.invalid"}}, "suite": map[string]any{"id": 9, "revision": 1, "name": "regression", "case_ids": []any{3}}, "cases": []any{map[string]any{"id": 3, "revision": revision}}, "case_revisions": map[string]any{"3": revision}})
			return
		}
		writes.Add(1)
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["suite_id"].(float64) != 9 || body["suite_revision"].(float64) != 1 || intArg(mapValue(body["case_revisions"]), "3", 0) != 1 || body["expected_variables_digest"] != "vars" || body["request"] != nil {
			t.Error("suite execution contract", body)
		}
		json.NewEncoder(w).Encode(map[string]any{"id": 17, "status": "queued", "service": "orders", "environment": "test"})
	}))
	defer up.Close()
	store, _ := OpenTokenStore(t.TempDir())
	svc := NewToolService(Upstreams{Deploy: up.URL})
	svc.actions = store
	tok := workflowToken(t, store)
	tok.Scopes = append(tok.Scopes, "apitests:read", "apitests:execute")
	args := map[string]any{"service": "orders", "environment": "test", "target_key": "direct", "target_fingerprint": "target", "suite_id": 9, "suite_revision": 1, "case_revisions": map[string]any{"3": 1}}
	preview := docCall(t, svc, tok, "execute_api_test", args)
	changed.Store(true)
	_, _, _, err := svc.Call(context.Background(), tok, "confirm_platform_action", map[string]any{"confirmation_id": mapValue(preview)["confirmation_id"], "confirm": true, "reason": "fixture"})
	if err == nil || writes.Load() != 0 {
		t.Fatal("member revision drift executed", err)
	}
	changed.Store(false)
	preview = docCall(t, svc, tok, "execute_api_test", args)
	result := docConfirm(t, svc, tok, preview)
	if intArg(mapValue(result["result"]), "id", 0) != 17 || writes.Load() != 1 {
		t.Fatal(result)
	}
	docConfirm(t, svc, tok, preview)
	if writes.Load() != 1 {
		t.Fatal("execution replayed")
	}
	args["request"] = map[string]any{"path": "/"}
	if _, _, _, err = svc.Call(context.Background(), tok, "execute_api_test", args); err == nil {
		t.Fatal("suite and inline request accepted")
	}
	delete(args, "request")
	delete(args, "service")
	if _, _, _, err = svc.Call(context.Background(), tok, "execute_api_test", args); err == nil {
		t.Fatal("scenario suite accepted")
	}
}

func TestTestingAgentStopConfirmationUsesImmutableIdentityAndScope(t *testing.T) {
	var writes atomic.Int32
	var terminal atomic.Bool
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" {
			if r.URL.Path == "/api/api-testing/mcp/catalog" {
				if r.URL.Query().Get("application_scope") != `["orders","payments"]` || r.URL.Query().Get("before_id") != "71" || r.URL.Query().Get("environment") != "test" {
					t.Error("catalog scope/cursor", r.URL.Query())
				}
				fmt.Fprint(w, `{"items":[{"id":70,"services":["orders","payments"]}],"has_more":false,"next_before_id":null}`)
				return
			}
			status := "running"
			if terminal.Load() {
				status = "success"
			}
			json.NewEncoder(w).Encode(map[string]any{"identity": "frozen-run-17", "service": "orders", "environment": "test", "services": []string{"orders", "payments"}, "run": map[string]any{"id": 17, "status": status}})
			return
		}
		writes.Add(1)
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["expected_identity"] != "frozen-run-17" || r.URL.Query().Get("run_id") != "17" {
			t.Error("wrong run stopped", body)
		}
		fmt.Fprint(w, `{"id":17,"stop_requested":false,"requires_result_check":true,"status":"success"}`)
	}))
	defer up.Close()
	store, _ := OpenTokenStore(t.TempDir())
	svc := NewToolService(Upstreams{Deploy: up.URL})
	svc.actions = store
	tok := workflowToken(t, store)
	tok.Applications = []string{"orders", "payments"}
	tok.Scopes = append(tok.Scopes, "apitests:read", "apitests:execute")
	args := map[string]any{"environment": "test", "run_id": 17}
	preview := docCall(t, svc, tok, "stop_api_test_run", args)
	tok.Applications = []string{"orders"}
	_, _, _, err := svc.Call(context.Background(), tok, "confirm_platform_action", map[string]any{"confirmation_id": mapValue(preview)["confirmation_id"], "confirm": true, "reason": "fixture"})
	if err == nil || writes.Load() != 0 {
		t.Fatal("revoked scenario stop accepted")
	}
	tok.Applications = []string{"orders", "payments"}
	preview = docCall(t, svc, tok, "stop_api_test_run", args)
	terminal.Store(true)
	docConfirm(t, svc, tok, preview)
	docConfirm(t, svc, tok, preview)
	if writes.Load() != 1 {
		t.Fatal("mutable status invalidated stop or replayed", writes.Load())
	}
	page := mapValue(docCall(t, svc, tok, "list_api_test_runs", map[string]any{"environment": "test", "before_id": 71, "limit": 10, "status": "success"}))
	if len(page["items"].([]any)) != 1 {
		t.Fatal(page)
	}
	// Caller cannot choose a broader application_scope.
	_, _, _, err = svc.Call(context.Background(), tok, "list_api_test_runs", map[string]any{"environment": "test", "application_scope": []string{"*"}})
	if err == nil || !strings.Contains(err.Error(), "application_scope") {
		t.Fatal("extra scope accepted", err)
	}
}

func TestTestingAgentUnknownStopNeverReplays(t *testing.T) {
	var writes atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" {
			fmt.Fprint(w, `{"identity":"original-run","service":"orders","environment":"test","run":{"id":17,"status":"running"}}`)
			return
		}
		writes.Add(1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		conn.Close()
	}))
	defer up.Close()
	store, _ := OpenTokenStore(t.TempDir())
	svc := NewToolService(Upstreams{Deploy: up.URL})
	svc.actions = store
	tok := workflowToken(t, store)
	tok.Scopes = append(tok.Scopes, "apitests:execute")
	preview := mapValue(docCall(t, svc, tok, "stop_api_test_run", map[string]any{"service": "orders", "environment": "test", "run_id": 17}))
	args := map[string]any{"confirmation_id": preview["confirmation_id"], "confirm": true, "reason": "fixture"}
	svc.Call(context.Background(), tok, "confirm_platform_action", args)
	status := mapValue(docCall(t, svc, tok, "get_platform_action", map[string]any{"confirmation_id": preview["confirmation_id"]}))
	if status["state"] != "uncertain" || writes.Load() != 1 {
		t.Fatal(status, writes.Load())
	}
	svc.Call(context.Background(), tok, "confirm_platform_action", args)
	if writes.Load() != 1 {
		t.Fatal("unknown stop replayed")
	}
}
