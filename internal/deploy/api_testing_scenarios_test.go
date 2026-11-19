package deploy

import (
	"encoding/json"
	"fmt"
	"io"
	"logplatform/pkg/apitest"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func scenarioFixture(t *testing.T) (*Store, http.Handler) {
	t.Helper()
	st, h := apiTestingFixture(t)
	for _, service := range []string{"orders", "other"} {
		for _, environment := range []string{"test", "prod"} {
			binding := *st.GetServiceEnvironment("orders", environment)
			binding.Service = service
			binding.RuntimeEnv = map[string]string{"API_PASSWORD": service + "-private-password"}
			if err := st.UpsertServiceEnvironment(binding); err != nil {
				t.Fatal(err)
			}
			port := "8080"
			if service == "other" {
				port = "9090"
			}
			p, err := st.apiTestingProfile(st.GetService(service).ApplicationID, environment)
			if err != nil {
				t.Fatal(err)
			}
			p.Enabled = true
			p.BaseURL = "http://192.0.2.55:" + port
			p.Auth = apitest.Auth{Mode: "bearer", TokenVariable: "API_PASSWORD", Header: "Authorization", Prefix: "Bearer "}
			if err = st.saveAPITestingProfile(st.GetService(service).ApplicationID, environment, p); err != nil {
				t.Fatal(err)
			}
		}
	}
	return st, h
}
func saveScenario(t *testing.T, h http.Handler, c apiTestCase) apiTestCase {
	t.Helper()
	w := apiTestingRequest(h, "POST", "/api/api-testing/scenario-cases", "alice", "admin", c)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var saved apiTestCase
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	return saved
}
func scenarioInput(t *testing.T, st *Store, c apiTestCase, key, environment string) apiScenarioInput {
	t.Helper()
	ctx, err := st.scenarioContext(&c, environment)
	if err != nil {
		t.Fatal(err)
	}
	in := apiScenarioInput{CaseID: c.ID, CaseRevision: c.Revision, Environment: environment, RequestID: key, Targets: map[string]apiScenarioSelection{}}
	for _, raw := range ctx["applications"].([]any) {
		app := raw.(map[string]any)
		targets := app["targets"].([]apiTestTarget)
		if len(targets) == 0 {
			t.Fatal("no target")
		}
		target := targets[0]
		in.Targets[app["service"].(string)] = apiScenarioSelection{Key: target.Key, Fingerprint: target.Fingerprint, VariablesDigest: app["variables_digest"].(string)}
	}
	return in
}
func waitScenario(t *testing.T, st *Store, id int64) *apiTestRun {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		v, err := st.apiTests.load("", "alice", id)
		if err != nil {
			t.Fatal(err)
		}
		if v.Status != "queued" && v.Status != "running" {
			return v
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("scenario did not finish")
	return nil
}
func crossCase() apiTestCase {
	return apiTestCase{Name: "订单跨应用付款", Steps: []apiTestStep{{Name: "创建订单", Service: "orders", Request: apitest.Request{Method: "POST", Path: "/create", Assertions: []apitest.Assertion{{Type: "status", Expected: "200"}}}, Extractions: []apitest.Extraction{{Name: "order_id", Source: "json", Path: "/id"}}}, {Name: "付款", Service: "other", Request: apitest.Request{Method: "POST", Path: "/pay/{{step.order_id}}", Headers: map[string]string{"X-Local": "{{var.API_PASSWORD}}"}, Assertions: []apitest.Assertion{{Type: "json", Path: "/ok", Expected: "true"}}}}}}
}

func TestScenarioCrossApplicationCredentialsOutputsFrozenReportsAndNoReplay(t *testing.T) {
	st, h := scenarioFixture(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		name := "orders"
		if r.Header.Get("X-Fixture-Application") == "other" {
			name = "other"
		}
		if r.Header.Get("Authorization") != "Bearer "+name+"-private-password" {
			t.Errorf("credential crossed application: %s", name)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/create" {
			io.WriteString(w, `{"id":"order-42"}`)
		} else {
			if r.URL.Path != "/pay/order-42" || r.Header.Get("X-Local") != "other-private-password" {
				t.Error("output or local variable lost")
			}
			io.WriteString(w, `{"ok":true,"echo":"orders-private-password"}`)
		}
	}))
	defer server.Close()
	st.apiTests.client = &http.Client{Transport: apiTestRoundTrip(func(r *http.Request) (*http.Response, error) {
		copy := r.Clone(r.Context())
		name := "orders"
		if r.URL.Port() == "9090" {
			name = "other"
		}
		copy.Header.Set("X-Fixture-Application", name)
		copy.URL, _ = url.Parse(server.URL + r.URL.RequestURI())
		return http.DefaultTransport.RoundTrip(copy)
	})}
	c := saveScenario(t, h, crossCase())
	if c.Scope != "scenario" || c.Steps[0].ApplicationID == "" {
		t.Fatal(c)
	}
	in := scenarioInput(t, st, c, "cross-application-run-001", "test")
	v, err := st.apiTests.startScenario("alice", true, in)
	if err != nil {
		t.Fatal(err)
	}
	report := waitScenario(t, st, v.ID)
	if report.Status != "success" || len(report.Targets) != 2 || report.Steps[1].Service != "other" || report.Steps[1].Target.BaseURL != "http://192.0.2.55:9090" {
		t.Fatalf("%+v", report)
	}
	raw, _ := json.Marshal(report)
	if strings.Contains(string(raw), "private-password") {
		t.Fatal("credential persisted in report")
	}
	if report.Units[0].CaseSnapshot.Steps[0].Service != "orders" {
		t.Fatal("snapshot lost ownership")
	}
	repeat, err := st.apiTests.startScenario("alice", true, in)
	if err != nil || repeat.ID != v.ID || calls.Load() != 2 {
		t.Fatal(repeat, err, calls.Load())
	}
	if _, err = st.apiTestCase(st.GetService("orders").ApplicationID, "alice", c.ID); err == nil {
		t.Fatal("global case leaked into application scope")
	}
	if _, err = st.apiTests.load("", "bob", v.ID); err == nil {
		t.Fatal("another user read report")
	}
}

func TestScenarioPreflightAllTargetsBeforeAnyRequestAndProtectedEnvironment(t *testing.T) {
	st, h := scenarioFixture(t)
	var calls atomic.Int32
	st.apiTests.client = &http.Client{Transport: apiTestRoundTrip(func(r *http.Request) (*http.Response, error) { calls.Add(1); return nil, fmt.Errorf("must not send") })}
	c := saveScenario(t, h, crossCase())
	in := scenarioInput(t, st, c, "cross-application-run-002", "test")
	changed := in.Targets["other"]
	changed.Fingerprint = "stale"
	in.Targets["other"] = changed
	if _, err := st.apiTests.startScenario("alice", true, in); err == nil {
		t.Fatal("accepted stale second target")
	}
	in = scenarioInput(t, st, c, "cross-application-run-003", "prod")
	in.ConfirmProtected = true
	in.Reason = "protected test"
	if _, err := st.apiTests.startScenario("alice", false, in); err == nil {
		t.Fatal("member bypassed protection")
	}
	in = scenarioInput(t, st, c, "cross-application-run-004", "test")
	selection := in.Targets["other"]
	selection.VariablesDigest = "stale"
	in.Targets["other"] = selection
	if _, err := st.apiTests.startScenario("alice", true, in); err == nil {
		t.Fatal("accepted changed variables")
	}
	if calls.Load() != 0 {
		t.Fatal("partial side effects before complete preflight")
	}
	c.Steps[1].ApplicationID = "replaced-application"
	if err := st.validateScenario(&c); err == nil {
		t.Fatal("rebound application identity")
	}
}

func TestScenarioCASSharingDocumentScopeAndPersonalLayout(t *testing.T) {
	st, h := scenarioFixture(t)
	c := saveScenario(t, h, crossCase())
	url := fmt.Sprintf("/api/api-testing/scenario-cases/%d", c.ID)
	if w := apiTestingRequest(h, "GET", url, "bob", "member", nil); w.Code != 404 {
		t.Fatal(w.Code)
	}
	c.Visibility = "team"
	w := apiTestingRequest(h, "PUT", url, "alice", "admin", c)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if apiTestingRequest(h, "PUT", url, "alice", "admin", c).Code != 409 {
		t.Fatal("stale overwrite")
	}
	if w = apiTestingRequest(h, "GET", url, "bob", "member", nil); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w = apiTestingRequest(h, "DELETE", url, "bob", "member", nil); w.Code != 403 {
		t.Fatal("member deleted shared case")
	}
	layout := apiTestLayout{Revision: 0, Positions: map[string]apiTestPoint{c.Steps[0].ID: {X: 80, Y: 20}}, Zoom: 1, Pan: apiTestPoint{20, 28}}
	if w = apiTestingRequest(h, "PUT", url+"/layout", "bob", "member", layout); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var own apiTestLayout
	json.Unmarshal(apiTestingRequest(h, "GET", url+"/layout", "alice", "admin", nil).Body.Bytes(), &own)
	if own.Revision != 0 {
		t.Fatal("layout leaked between owners")
	}
	c.Steps[0].DocumentID = 999
	if err := st.validateScenario(&c); err == nil {
		t.Fatal("accepted foreign document")
	}
}

func TestScenarioMissingSecondApplicationVariablesPreventsFirstRequest(t *testing.T) {
	st, h := scenarioFixture(t)
	c := saveScenario(t, h, crossCase())
	in := scenarioInput(t, st, c, "missing-variable-scenario-001", "test")
	binding := *st.GetServiceEnvironment("other", "test")
	binding.RuntimeEnv = map[string]string{}
	if err := st.UpsertServiceEnvironment(binding); err != nil {
		t.Fatal(err)
	}
	variables, _ := st.apiTestVariables(st.GetService("other"), st.GetServiceEnvironment("other", "test"))
	selection := in.Targets["other"]
	selection.VariablesDigest = st.privateOperationDigest(variables)
	in.Targets["other"] = selection
	var calls atomic.Int32
	st.apiTests.client = &http.Client{Transport: apiTestRoundTrip(func(r *http.Request) (*http.Response, error) { calls.Add(1); return nil, fmt.Errorf("forbidden") })}
	if _, err := st.apiTests.startScenario("alice", true, in); err == nil || calls.Load() != 0 {
		t.Fatal("missing second credential sent first request", err)
	}
	context, err := st.scenarioContext(&c, "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range context["applications"].([]any) {
		app := row.(map[string]any)
		if app["service"] == "other" {
			for _, target := range app["targets"].([]apiTestTarget) {
				if target.CredentialReferencesValid {
					t.Fatal("missing credential marked available")
				}
			}
		}
	}
}
