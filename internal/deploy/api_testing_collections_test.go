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
	"time"
)

func saveTeamCase(t *testing.T, h http.Handler, who, role string, c apiTestCase) apiTestCase {
	t.Helper()
	method, path := "POST", "/api/api-testing/cases"
	if c.ID > 0 {
		method = "PUT"
		path += fmt.Sprint("/", c.ID)
	}
	w := apiTestingRequest(h, method, path+"?service=orders", who, role, c)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if e := json.Unmarshal(w.Body.Bytes(), &c); e != nil {
		t.Fatal(e)
	}
	return c
}
func TestAPITeamSharingPermissionsCopyAndRevocation(t *testing.T) {
	st, h := apiTestingFixture(t)
	app := st.GetService("orders").ApplicationID
	c := saveTeamCase(t, h, "alice", "member", apiTestCase{Name: "团队订单", Visibility: "team", Request: apitest.Request{Path: "/"}})
	private := saveTeamCase(t, h, "alice", "member", apiTestCase{Name: "私人订单", Request: apitest.Request{Path: "/"}})
	w := apiTestingRequest(h, "GET", "/api/api-testing/cases?service=orders", "bob", "member", nil)
	var list []apiTestCase
	json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) != 1 || list[0].CanEdit || list[0].Owner != "alice" || list[0].ID != c.ID {
		t.Fatal(w.Body.String())
	}
	for _, method := range []string{"PUT", "DELETE"} {
		w = apiTestingRequest(h, method, fmt.Sprintf("/api/api-testing/cases/%d?service=orders", c.ID), "bob", "member", c)
		if w.Code == 200 {
			t.Fatal("member changed shared case", method)
		}
	}
	if _, e := st.apiTestCase(app, "bob", private.ID); e == nil {
		t.Fatal("private case leaked")
	}
	w = apiTestingRequest(h, "PUT", fmt.Sprintf("/api/api-testing/cases/%d?service=orders", private.ID), "root", "admin", private)
	if w.Code == 200 {
		t.Fatal("admin edited private case")
	}
	// Copy resets identity and remains independently owned, including data definitions.
	copied := c
	copied.ID = 0
	copied.Revision = 0
	copied.Visibility = "private"
	copied.Name += "副本"
	copied = saveTeamCase(t, h, "bob", "member", copied)
	if copied.Owner != "bob" || copied.ID == c.ID {
		t.Fatal(copied)
	}
	c.Name = "管理员修订"
	c = saveTeamCase(t, h, "root", "admin", c)
	if c.Owner != "alice" || c.Revision != 2 {
		t.Fatal(c)
	}
	stale := c
	stale.Revision = 1
	w = apiTestingRequest(h, "PUT", fmt.Sprintf("/api/api-testing/cases/%d?service=orders", c.ID), "alice", "member", stale)
	if w.Code != 409 {
		t.Fatal(w.Code)
	}
	c.Visibility = "private"
	saveTeamCase(t, h, "alice", "member", c)
	if _, e := st.apiTestCase(app, "bob", c.ID); e == nil {
		t.Fatal("revoked case accessible")
	}
	if _, e := st.apiTestCase(app, "bob", copied.ID); e != nil {
		t.Fatal("copy changed", e)
	}
}
func TestAPISuiteTypedDataFrozenVersionsAndHistory(t *testing.T) {
	st, h := apiTestingFixture(t)
	var mu sync.Mutex
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.RequestURI())
		mu.Unlock()
		fmt.Fprint(w, `{"id":9007199254740993123,"ok":true}`)
	}))
	defer server.Close()
	st.apiTests.client = apiTestingClient(server)
	c := saveTeamCase(t, h, "alice", "member", apiTestCase{Name: "数据查询", Visibility: "team", Steps: []apiTestStep{{Name: "取值", Request: apitest.Request{Path: "/first/{{data.id}}", Assertions: []apitest.Assertion{{Type: "status", Expected: "200"}}}, Extractions: []apitest.Extraction{{Name: "id", Source: "json", Path: "/id"}}}, {Name: "核对", Request: apitest.Request{Path: "/second/{{step.id}}?input={{data.id}}", Assertions: []apitest.Assertion{{Type: "json", Path: "/ok", Expected: "{{data.ok}}"}}}}}, DataRows: []apiTestDataRow{{Name: "大数字", Values: map[string]apiTestCell{"id": {Type: "number", Value: "9007199254740993123"}, "ok": {Type: "boolean", Value: "true"}}}, {Name: "编码文本", Values: map[string]apiTestCell{"id": {Type: "string", Value: "a/b?x=1&y=2"}, "ok": {Type: "boolean", Value: "true"}}}}})
	w := apiTestingRequest(h, "POST", "/api/api-testing/suites?service=orders", "alice", "member", apiTestSuite{Name: "订单回归", Visibility: "team", CaseIDs: []int64{c.ID}})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var suite apiTestSuite
	json.Unmarshal(w.Body.Bytes(), &suite)
	in := apiTestingInput(t, st, "suite-typed-data-0001")
	in.SuiteID = suite.ID
	in.SuiteRevision = suite.Revision
	in.CaseRevisions = map[string]int64{fmt.Sprint(c.ID): c.Revision}
	in.Request.Path = "/draft-not-sent"
	in.DocumentID = 999
	w = apiTestingRequest(h, "POST", "/api/api-testing/runs?service=orders", "bob", "member", in)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var queued apiTestRun
	json.Unmarshal(w.Body.Bytes(), &queued)
	run := waitAPITest(t, st, "bob", queued.ID)
	if run.Status != "success" || run.Source != "case" || run.SuiteID != suite.ID || len(run.Units) != 2 || len(run.Steps) != 4 || run.Units[1].Status != "success" || len(run.Units[0].CaseSnapshot.DataRows) != 0 {
		t.Fatalf("bad report %+v", run)
	}
	mu.Lock()
	paths := strings.Join(seen, "\n")
	mu.Unlock()
	if !strings.Contains(paths, "/first/9007199254740993123") || !strings.Contains(paths, "/first/a%2Fb%3Fx=1&y=2") || !strings.Contains(paths, "/second/9007199254740993123") {
		t.Fatal(paths)
	}
	raw, _ := json.Marshal(run)
	if strings.Contains(string(raw), `"value":"9007199254740993123"`) || strings.Contains(string(raw), "a/b?x=1&y=2") {
		t.Fatal("data values leaked", string(raw))
	}
	// Idempotency returns frozen original after case edits and never sends again.
	c.Name = "新版本"
	c = saveTeamCase(t, h, "alice", "member", c)
	w = apiTestingRequest(h, "POST", "/api/api-testing/runs?service=orders", "bob", "member", in)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var same apiTestRun
	json.Unmarshal(w.Body.Bytes(), &same)
	if same.ID != run.ID || same.Units[0].CaseSnapshot.Revision != 1 {
		t.Fatal(same)
	}
	in.RequestID = "suite-stale-revision-001"
	w = apiTestingRequest(h, "POST", "/api/api-testing/runs?service=orders", "bob", "member", in)
	if w.Code != 409 {
		t.Fatal("stale plan accepted")
	}
	w = apiTestingRequest(h, "GET", fmt.Sprintf("/api/api-testing/runs?service=orders&source=case&page=1&page_size=15&case_id=%d&suite_id=%d&status=success&environment=test&search=订单", c.ID, suite.ID), "bob", "member", nil)
	var page struct {
		Total int
		Items []apiTestRun
	}
	json.Unmarshal(w.Body.Bytes(), &page)
	if w.Code != 200 || page.Total != 1 || len(page.Items) != 1 {
		t.Fatal(w.Body.String())
	}
	w = apiTestingRequest(h, "GET", "/api/api-testing/runs?service=orders&source=case&page=1", "alice", "member", nil)
	json.Unmarshal(w.Body.Bytes(), &page)
	if page.Total != 0 {
		t.Fatal("team report leaked", w.Body.String())
	}
	w = apiTestingRequest(h, "GET", fmt.Sprintf("/api/api-testing/runs/%d?service=orders", run.ID), "alice", "admin", nil)
	if w.Code != 404 {
		t.Fatal("report leaked")
	}
	c.Visibility = "private"
	saveTeamCase(t, h, "alice", "member", c)
	in.CaseRevisions[fmt.Sprint(c.ID)] = c.Revision + 1
	w = apiTestingRequest(h, "POST", "/api/api-testing/runs?service=orders", "bob", "member", in)
	if w.Code != 409 {
		t.Fatal("revoked suite executed")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 4 {
		t.Fatal("unexpected replay", seen)
	}
}
func TestAPISuiteFailureSkipsDataAndCasesAndRejectsInvalidPlans(t *testing.T) {
	st, h := apiTestingFixture(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) }))
	defer server.Close()
	st.apiTests.client = apiTestingClient(server)
	c := saveTeamCase(t, h, "alice", "member", apiTestCase{Name: "失败用例", Steps: []apiTestStep{{Name: "失败即停止", Request: apitest.Request{Path: "/", Assertions: []apitest.Assertion{{Type: "status", Expected: "200"}}}}}, DataRows: []apiTestDataRow{{Name: "第一组", Values: map[string]apiTestCell{"id": {Type: "string", Value: "first"}}}, {Name: "第二组", Values: map[string]apiTestCell{"id": {Type: "string", Value: "second"}}}}})
	in := apiTestingInput(t, st, "data-stop-first-failure1")
	in.CaseID = c.ID
	in.CaseRevision = c.Revision
	w := apiTestingRequest(h, "POST", "/api/api-testing/runs?service=orders", "alice", "member", in)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var v apiTestRun
	json.Unmarshal(w.Body.Bytes(), &v)
	run := waitAPITest(t, st, "alice", v.ID)
	if run.Status != "failed" || run.Units[0].Status != "failed" || run.Units[1].Status != "skipped" || calls != 1 {
		t.Fatal(run, calls)
	}
	for _, bad := range []apiTestCase{{Name: "missing", Request: apitest.Request{Path: "/{{data.missing}}"}}, {Name: "bad type", Request: apitest.Request{Path: "/"}, DataRows: []apiTestDataRow{{Name: "row", Values: map[string]apiTestCell{"id": {Type: "number", Value: "NaN"}}}}}} {
		w = apiTestingRequest(h, "POST", "/api/api-testing/cases?service=orders", "alice", "member", bad)
		if w.Code != 400 {
			t.Fatal("invalid data accepted", w.Body.String())
		}
	}
	w = apiTestingRequest(h, "POST", "/api/api-testing/suites?service=orders", "alice", "member", apiTestSuite{Name: "shared private", Visibility: "team", CaseIDs: []int64{c.ID}})
	if w.Code != 400 {
		t.Fatal("private case shared by suite")
	}
}
func TestAPIHistoryBackfillsLegacyAndPaginatesBeforeDecryption(t *testing.T) {
	st, h := apiTestingFixture(t)
	app := st.GetService("orders").ApplicationID
	for i := 0; i < 35; i++ {
		v := apiTestRun{Name: fmt.Sprintf("历史%02d", i), Owner: "alice", Environment: "test", Status: "success", StartedAt: 100 + int64(i)}
		if i%2 == 0 {
			v.CaseID = 9
		}
		raw, _ := json.Marshal(v)
		enc, _ := st.encryptSecret(string(raw))
		if _, e := st.db.Exec(`INSERT INTO api_test_runs(application_id,service,owner,request_key,request_digest,document,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, app, "orders", "alice", fmt.Sprintf("old-%d", i), "digest", enc, "success", v.StartedAt, time.Now().Unix()); e != nil {
			t.Fatal(e)
		}
	}
	w := apiTestingRequest(h, "GET", "/api/api-testing/runs?service=orders&source=case&page=2&page_size=5&from=110&to=130", "alice", "member", nil)
	var page struct {
		Total int
		Items []apiTestRun
	}
	json.Unmarshal(w.Body.Bytes(), &page)
	if w.Code != 200 || page.Total != 11 || len(page.Items) != 5 || page.Items[0].Name != "历史20" {
		t.Fatal(w.Body.String())
	}
	w = apiTestingRequest(h, "GET", "/api/api-testing/runs?service=orders&page=0", "alice", "member", nil)
	if w.Code != 400 {
		t.Fatal("invalid page accepted")
	}
}

func TestAPICollectionPlanLimitsAndCancellation(t *testing.T) {
	st, h := apiTestingFixture(t)
	bad := apiTestingInput(t, st, "negative-suite-id-0001")
	bad.SuiteID = -1
	if w := apiTestingRequest(h, "POST", "/api/api-testing/runs?service=orders", "alice", "member", bad); w.Code != 409 {
		t.Fatal("negative ID accepted", w.Body.String())
	}
	entered := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { once.Do(func() { close(entered) }); <-r.Context().Done() }))
	defer server.Close()
	st.apiTests.client = apiTestingClient(server)
	c := apiTestCase{Name: "停止数据回归", Request: apitest.Request{Method: "POST", Path: "/"}, DataRows: []apiTestDataRow{{Name: "一", Values: map[string]apiTestCell{"id": {Type: "number", Value: "1"}}}, {Name: "二", Values: map[string]apiTestCell{"id": {Type: "number", Value: "2"}}}}}
	c = saveTeamCase(t, h, "alice", "member", c)
	in := apiTestingInput(t, st, "stop-data-combinations1")
	in.CaseID = c.ID
	in.CaseRevision = c.Revision
	w := apiTestingRequest(h, "POST", "/api/api-testing/runs?service=orders", "alice", "member", in)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var queued apiTestRun
	json.Unmarshal(w.Body.Bytes(), &queued)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("did not start")
	}
	apiTestingRequest(h, "POST", fmt.Sprintf("/api/api-testing/runs/%d/stop?service=orders", queued.ID), "alice", "member", nil)
	run := waitAPITest(t, st, "alice", queued.ID)
	if run.Status != "canceled" || run.Units[0].Status != "canceled" || run.Units[1].Status != "skipped" || !run.Steps[0].Result.OutcomeUnknown {
		t.Fatalf("unsafe stopped report %+v", run)
	}
	c.DataRows = append(c.DataRows, c.DataRows...)
	for len(c.DataRows) < 11 {
		c.DataRows = append(c.DataRows, apiTestDataRow{Name: fmt.Sprint(len(c.DataRows)), Values: map[string]apiTestCell{"id": {Type: "number", Value: "1"}}})
	}
	c.Steps = make([]apiTestStep, 10)
	if validateAPITestData(&c) == nil {
		t.Fatal("unbounded combinations")
	}
}
