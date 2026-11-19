package deploy

import (
	"encoding/json"
	"fmt"
	"logplatform/pkg/apitest"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestAPISuiteCrossCaseBindingsFrozenAndIsolated(t *testing.T) {
	st, h := apiTestingFixture(t)
	var mu sync.Mutex
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.RequestURI())
		mu.Unlock()
		if strings.HasPrefix(r.URL.Path, "/source/") {
			fmt.Fprintf(w, `{"id":"private-%s"}`, strings.TrimPrefix(r.URL.Path, "/source/"))
			return
		}
		if r.URL.Path != "/target/private-b" {
			t.Error("wrong cross-case value", r.URL)
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()
	st.apiTests.client = apiTestingClient(server)
	source := saveTeamCase(t, h, "alice", "member", apiTestCase{Name: "创建", Visibility: "team", Steps: []apiTestStep{{Name: "创建并提取", Request: apitest.Request{Path: "/source/{{data.row}}"}, Extractions: []apitest.Extraction{{Name: "id", Source: "json", Path: "/id"}}}}, DataRows: []apiTestDataRow{{Name: "first", Values: map[string]apiTestCell{"row": {Type: "string", Value: "a"}}}, {Name: "second", Values: map[string]apiTestCell{"row": {Type: "string", Value: "b"}}}}})
	target := saveTeamCase(t, h, "alice", "member", apiTestCase{Name: "查询", Visibility: "team", Steps: []apiTestStep{{Name: "查询来源用例", Request: apitest.Request{Path: "/target/{{case.order_id}}", Assertions: []apitest.Assertion{{Type: "status", Expected: "200"}}}}}})
	bindings := []apiTestCaseBinding{{TargetCaseID: target.ID, Name: "order_id", SourceCaseID: source.ID, SourceName: "id", SourceRow: "second"}}
	suite := apiTestSuite{Name: "创建查询", Visibility: "team", CaseIDs: []int64{source.ID, target.ID}, Bindings: bindings}
	for _, change := range []func(*apiTestSuite){func(s *apiTestSuite) { s.Bindings = nil }, func(s *apiTestSuite) { s.CaseIDs = []int64{target.ID, source.ID} }, func(s *apiTestSuite) {
		s.Bindings = append([]apiTestCaseBinding(nil), bindings...)
		s.Bindings[0].SourceRow = ""
	}, func(s *apiTestSuite) {
		s.Bindings = append([]apiTestCaseBinding(nil), bindings...)
		s.Bindings[0].SourceName = "missing"
	}} {
		invalid := suite
		change(&invalid)
		w := apiTestingRequest(h, "POST", "/api/api-testing/suites?service=orders", "alice", "member", invalid)
		if w.Code == 200 {
			t.Fatal("invalid mapping accepted", invalid)
		}
	}
	w := apiTestingRequest(h, "POST", "/api/api-testing/suites?service=orders", "alice", "member", suite)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &suite)
	in := apiTestingInput(t, st, "case-cross-bindings-0001")
	in.SuiteID = suite.ID
	in.SuiteRevision = suite.Revision
	in.CaseRevisions = map[string]int64{fmt.Sprint(source.ID): source.Revision, fmt.Sprint(target.ID): target.Revision}
	w = apiTestingRequest(h, "POST", "/api/api-testing/runs?service=orders", "bob", "member", in)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var queued apiTestRun
	json.Unmarshal(w.Body.Bytes(), &queued)
	report := waitAPITest(t, st, "bob", queued.ID)
	if report.Status != "success" || len(report.Bindings) != 1 || len(report.Units) != 3 {
		raw, _ := json.Marshal(report)
		t.Fatal(string(raw))
	}
	raw, _ := json.Marshal(report)
	if strings.Contains(string(raw), "private-a") || strings.Contains(string(raw), "private-b") {
		t.Fatal("cross-case values in report", string(raw))
	}
	mu.Lock()
	count := len(paths)
	mu.Unlock()
	if count != 3 {
		t.Fatal(count)
	}
	// A standalone run cannot silently reuse the suite's prior values.
	in = apiTestingInput(t, st, "case-cross-bindings-0002")
	in.CaseID = target.ID
	in.CaseRevision = target.Revision
	w = apiTestingRequest(h, "POST", "/api/api-testing/runs?service=orders", "bob", "member", in)
	if w.Code == 200 {
		t.Fatal("unbound standalone accepted")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != count {
		t.Fatal("business request sent without binding")
	}
}
func TestAPISuiteCrossCaseFailureDoesNotPublish(t *testing.T) {
	st, h := apiTestingFixture(t)
	var mu sync.Mutex
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		fmt.Fprint(w, `{"id":"private-id"}`)
	}))
	defer server.Close()
	st.apiTests.client = apiTestingClient(server)
	source := saveTeamCase(t, h, "alice", "member", apiTestCase{Name: "来源失败", Visibility: "team", Steps: []apiTestStep{{Name: "提取", Request: apitest.Request{Path: "/source"}, Extractions: []apitest.Extraction{{Name: "id", Source: "json", Path: "/id"}}}, {Name: "核对失败", Request: apitest.Request{Path: "/fail", Assertions: []apitest.Assertion{{Type: "status", Expected: "201"}}}}}})
	target := saveTeamCase(t, h, "alice", "member", apiTestCase{Name: "不得执行", Visibility: "team", Request: apitest.Request{Path: "/{{case.id}}"}})
	suite := apiTestSuite{Name: "失败停止", Visibility: "team", CaseIDs: []int64{source.ID, target.ID}, Bindings: []apiTestCaseBinding{{SourceCaseID: source.ID, SourceName: "id", TargetCaseID: target.ID, Name: "id"}}}
	w := apiTestingRequest(h, "POST", "/api/api-testing/suites?service=orders", "alice", "member", suite)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &suite)
	in := apiTestingInput(t, st, "case-cross-failed-0001")
	in.SuiteID = suite.ID
	in.SuiteRevision = suite.Revision
	in.CaseRevisions = map[string]int64{fmt.Sprint(source.ID): source.Revision, fmt.Sprint(target.ID): target.Revision}
	w = apiTestingRequest(h, "POST", "/api/api-testing/runs?service=orders", "bob", "member", in)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var queued apiTestRun
	json.Unmarshal(w.Body.Bytes(), &queued)
	report := waitAPITest(t, st, "bob", queued.ID)
	if report.Status != "failed" || report.Units[1].Status != "skipped" {
		t.Fatal(report.Status, report.Units)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatal(calls)
	}
}
