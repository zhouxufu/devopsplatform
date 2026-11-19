package deploy

import (
	"encoding/json"
	"fmt"
	"logplatform/pkg/apitest"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func agentPage(t *testing.T, h http.Handler, suffix, user string) map[string]any {
	t.Helper()
	w := apiTestingRequest(h, "GET", "/api/api-testing/mcp/catalog?environment=test&"+suffix, user, "member", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out map[string]any
	if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
		t.Fatal(e)
	}
	return out
}
func TestAgentCaseCatalogResumesSparseSearchAndVisibility(t *testing.T) {
	st, h := apiTestingFixture(t)
	needle := saveTeamCase(t, h, "alice", "member", apiTestCase{Name: "needle target", Module: "payments", Request: apitest.Request{Path: "/"}})
	template := saveTeamCase(t, h, "alice", "member", apiTestCase{Name: "ordinary", Request: apitest.Request{Path: "/"}})
	var enc string
	if e := st.db.QueryRow("SELECT document FROM api_test_cases WHERE id=?", template.ID).Scan(&enc); e != nil {
		t.Fatal(e)
	}
	app := st.GetService("orders").ApplicationID
	tx, e := st.db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 1005; i++ {
		if _, e = tx.Exec(`INSERT INTO api_test_cases(application_id,owner,document,revision,deleted,updated_at) VALUES(?,?,?,1,0,0)`, app, "alice", enc); e != nil {
			t.Fatal(e)
		}
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	saveTeamCase(t, h, "bob", "member", apiTestCase{Name: "needle private", Request: apitest.Request{Path: "/"}})
	shared := saveTeamCase(t, h, "bob", "member", apiTestCase{Name: "needle shared", Visibility: "team", Request: apitest.Request{Path: "/"}})
	first := agentPage(t, h, "service=orders&kind=cases&q=needle&limit=1", "alice")
	rows := first["items"].([]any)
	if len(rows) != 1 || int64(rows[0].(map[string]any)["id"].(float64)) != shared.ID || first["has_more"] != true {
		t.Fatal(first)
	}
	middle := agentPage(t, h, fmt.Sprintf("service=orders&kind=cases&q=needle&limit=1&before_id=%.0f", first["next_before_id"]), "alice")
	if len(middle["items"].([]any)) != 0 || middle["has_more"] != true {
		t.Fatal("sparse continuation lost", middle)
	}
	last := agentPage(t, h, fmt.Sprintf("service=orders&kind=cases&q=needle&limit=1&before_id=%.0f", middle["next_before_id"]), "alice")
	if rows = last["items"].([]any); len(rows) != 1 || int64(rows[0].(map[string]any)["id"].(float64)) != needle.ID {
		t.Fatal(last)
	}
	byModule := agentPage(t, h, "service=orders&kind=cases&q=payments&limit=10&before_id=2", "alice")
	if len(byModule["items"].([]any)) != 1 {
		t.Fatal(byModule)
	}
	for _, suffix := range []string{"limit=0", "limit=101", "before_id=-1", "before_id=bad", "q=" + strings.Repeat("x", 201), "kind=unknown"} {
		w := apiTestingRequest(h, "GET", "/api/api-testing/mcp/catalog?service=orders&environment=test&kind=cases&"+suffix, "alice", "member", nil)
		if suffix == "kind=unknown" {
			w = apiTestingRequest(h, "GET", "/api/api-testing/mcp/catalog?service=orders&environment=test&kind=unknown", "alice", "member", nil)
		}
		if w.Code != 400 {
			t.Fatal(suffix, w.Code, w.Body.String())
		}
	}
}

func TestAgentSuitePreviewSaveFreezeAndExecution(t *testing.T) {
	st, h := apiTestingFixture(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, `{"ok":true}`) }))
	defer server.Close()
	st.apiTests.client = apiTestingClient(server)
	c := saveTeamCase(t, h, "alice", "member", apiTestCase{Name: "contract", Visibility: "team", Request: apitest.Request{Path: "/contract", Assertions: []apitest.Assertion{{Type: "json", Path: "/ok", Expected: "true"}}}})
	candidate := apiTestSuite{Name: "agent suite", Visibility: "team", CaseIDs: []int64{c.ID}}
	w := apiTestingRequest(h, "POST", "/api/api-testing/mcp/suite-preview?service=orders&environment=test", "alice", "member", candidate)
	if w.Code != 200 || calls.Load() != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	var count int
	st.db.QueryRow("SELECT COUNT(*) FROM api_test_suites").Scan(&count)
	if count != 0 {
		t.Fatal("preview persisted")
	}
	mutation := apiTestSuiteMutation{Suite: candidate, CaseRevisions: map[string]int64{fmt.Sprint(c.ID): c.Revision}}
	w = apiTestingRequest(h, "POST", "/api/api-testing/mcp/suites?service=orders&environment=test", "alice", "member", mutation)
	var saved apiTestSuite
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &saved) != nil || saved.ID < 1 || saved.Revision != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = apiTestingRequest(h, "GET", fmt.Sprintf("/api/api-testing/mcp/context?service=orders&environment=test&suite_id=%d", saved.ID), "bob", "member", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"case_revisions"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	in := apiTestingInput(t, st, "agent-suite-execution-001")
	vars, _ := st.apiTestVariables(st.GetService("orders"), st.GetServiceEnvironment("orders", "test"))
	in.ExpectedVariablesDigest = st.privateOperationDigest(vars)
	in.Request = apitest.Request{}
	in.SuiteID = saved.ID
	in.SuiteRevision = saved.Revision
	in.CaseRevisions = mutation.CaseRevisions
	w = apiTestingRequest(h, "POST", "/api/api-testing/mcp/runs?service=orders&environment=test", "bob", "member", in)
	var run apiTestRun
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &run) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	finished := waitAPITest(t, st, "bob", run.ID)
	if finished.Status != "success" || calls.Load() != 1 || finished.Units[0].CaseSnapshot.Revision != 1 {
		t.Fatal(finished.Status, calls.Load())
	}
	c.Name = "changed"
	c = saveTeamCase(t, h, "alice", "member", c)
	mutation.Suite = saved
	w = apiTestingRequest(h, "PUT", fmt.Sprintf("/api/api-testing/mcp/suites/%d?service=orders&environment=test", saved.ID), "alice", "member", mutation)
	if w.Code != 409 {
		t.Fatal("changed member accepted", w.Code, w.Body.String())
	}
	in.RequestID = "agent-suite-execution-002"
	w = apiTestingRequest(h, "POST", "/api/api-testing/mcp/runs?service=orders&environment=test", "bob", "member", in)
	if w.Code != 409 || calls.Load() != 1 {
		t.Fatal("stale suite execution", w.Code, calls.Load())
	}
	mutation.CaseRevisions[fmt.Sprint(c.ID)] = c.Revision
	w = apiTestingRequest(h, "PUT", fmt.Sprintf("/api/api-testing/mcp/suites/%d?service=orders&environment=test", saved.ID), "alice", "member", mutation)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = apiTestingRequest(h, "PUT", fmt.Sprintf("/api/api-testing/mcp/suites/%d?service=orders&environment=test", saved.ID), "alice", "member", mutation)
	if w.Code != 409 {
		t.Fatal("suite CAS lost", w.Code)
	}
	frozen, e := st.apiTests.load(st.GetService("orders").ApplicationID, "bob", run.ID)
	if e != nil || frozen.Units[0].CaseSnapshot.Name != "contract" || frozen.SuiteRevision != 1 {
		t.Fatal("historical snapshot mutated", e)
	}
	page := agentPage(t, h, fmt.Sprintf("service=orders&kind=runs&suite_id=%d&case_id=%d&source=case&status=success&q=agent", saved.ID, c.ID), "bob")
	if len(page["items"].([]any)) != 1 {
		t.Fatal(page)
	}
	if len(agentPage(t, h, "service=orders&kind=runs", "alice")["items"].([]any)) != 0 {
		t.Fatal("other owner report leaked")
	}
	w = apiTestingRequest(h, "GET", "/api/api-testing/mcp/catalog?service=orders&environment=prod&kind=runs", "bob", "member", nil)
	var prod map[string]any
	json.Unmarshal(w.Body.Bytes(), &prod)
	if w.Code != 200 || len(prod["items"].([]any)) != 0 {
		t.Fatal("environment leak", w.Body.String())
	}
}

func TestAgentRunStopBindsIdentityAndPreservesUnknownOutcome(t *testing.T) {
	st, h := apiTestingFixture(t)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	st.apiTests.client = apiTestingClient(server)
	in := apiTestingInput(t, st, "agent-stop-original-001")
	in.Request.Path = "/orders"
	vars, _ := st.apiTestVariables(st.GetService("orders"), st.GetServiceEnvironment("orders", "test"))
	in.ExpectedVariablesDigest = st.privateOperationDigest(vars)
	w := apiTestingRequest(h, "POST", "/api/api-testing/mcp/runs?service=orders&environment=test", "alice", "member", in)
	var run apiTestRun
	json.Unmarshal(w.Body.Bytes(), &run)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("not started")
	}
	path := fmt.Sprintf("/api/api-testing/mcp/run-control?service=orders&environment=test&run_id=%d", run.ID)
	w = apiTestingRequest(h, "GET", path, "alice", "member", nil)
	var control map[string]any
	json.Unmarshal(w.Body.Bytes(), &control)
	if w.Code != 200 || control["identity"] == "" {
		t.Fatal(w.Body.String())
	}
	for _, bad := range []string{strings.Replace(path, "environment=test", "environment=prod", 1), strings.Replace(path, "service=orders", "service=other", 1)} {
		if v := apiTestingRequest(h, "GET", bad, "alice", "member", nil); v.Code == 200 {
			t.Fatal("wrong scope accepted", bad)
		}
	}
	if v := apiTestingRequest(h, "GET", path, "bob", "member", nil); v.Code == 200 {
		t.Fatal("foreign run accepted")
	}
	stop := strings.Replace(path, "run-control", "stop", 1)
	w = apiTestingRequest(h, "POST", stop, "alice", "member", map[string]any{"expected_identity": "spoof"})
	if w.Code != 409 {
		t.Fatal(w.Code)
	}
	w = apiTestingRequest(h, "POST", stop, "alice", "member", map[string]any{"expected_identity": control["identity"]})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	finished := waitAPITest(t, st, "alice", run.ID)
	if finished.Status != "canceled" || !finished.Result.OutcomeUnknown {
		t.Fatal("cancel outcome", finished.Status, finished.Result)
	}
	w = apiTestingRequest(h, "GET", path, "alice", "member", nil)
	var after map[string]any
	json.Unmarshal(w.Body.Bytes(), &after)
	if after["identity"] != control["identity"] {
		t.Fatal("progress changed immutable identity")
	}
	w = apiTestingRequest(h, "POST", stop, "alice", "member", map[string]any{"expected_identity": control["identity"]})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"stop_requested":false`) {
		t.Fatal("terminal stop", w.Code, w.Body.String())
	}
}

func TestAgentScenarioCatalogFiltersAllApplicationsBeforePaging(t *testing.T) {
	_, h := scenarioFixture(t)
	c := crossCase()
	c = saveScenario(t, h, c)
	restricted := agentPage(t, h, "kind=cases&application_scope="+url.QueryEscape(`["orders"]`), "alice")
	if len(restricted["items"].([]any)) != 0 {
		t.Fatal("partial scope leaked", restricted)
	}
	full := agentPage(t, h, "kind=cases&limit=1&application_scope="+url.QueryEscape(`["orders","other"]`), "alice")
	if len(full["items"].([]any)) != 1 {
		t.Fatal(full)
	}
}
