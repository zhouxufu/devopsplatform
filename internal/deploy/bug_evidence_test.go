package deploy

import (
	"encoding/json"
	"fmt"
	"io"
	"logplatform/pkg/apitest"
	"net/http"
	"strings"
	"testing"
	"time"
)

func seedBugEvidenceRun(t *testing.T, s *Store, owner string, c apiTestCase) apiTestRun {
	t.Helper()
	v := apiTestRun{Owner: owner, Service: "orders", Status: "failed", Name: "订单详情验证", Environment: "test", StartedAt: time.Now().Unix() - 5, EndedAt: time.Now().Unix(), CaseID: c.ID, CaseRevision: c.Revision, Target: apiTestTarget{Key: "profile"}, Result: apitest.Result{Status: 500, Body: `{"error":"order missing","token":"secret-fixture-token"}`, Checks: []apitest.Check{{Passed: false, Type: "status", Expected: "200", Actual: "500"}}}, Units: []apiTestUnit{{CaseSnapshot: c, Status: "failed"}}}
	raw, _ := json.Marshal(v)
	enc, e := s.encryptSecret(string(raw))
	if e != nil {
		t.Fatal(e)
	}
	res, e := s.db.Exec(`INSERT INTO api_test_runs(application_id,service,owner,request_key,request_digest,document,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, s.GetService("orders").ApplicationID, "orders", owner, fmt.Sprint("fixture-", time.Now().UnixNano()), "fixture", enc, v.Status, v.StartedAt, v.EndedAt)
	if e != nil {
		t.Fatal(e)
	}
	v.ID, _ = res.LastInsertId()
	return v
}
func evidenceCase(t *testing.T, h http.Handler) apiTestCase {
	t.Helper()
	return saveTeamCase(t, h, "alice", "member", apiTestCase{Name: "订单回归", Visibility: "team", Steps: []apiTestStep{{Name: "查询订单", Request: apitest.Request{Path: "/orders/7", Assertions: []apitest.Assertion{{Type: "status", Expected: "200"}}}}}})
}

func TestBugEvidenceImmutablePrivateIdempotent(t *testing.T) {
	s, h := apiTestingFixture(t)
	c := evidenceCase(t, h)
	run := seedBugEvidenceRun(t, s, "alice", c)
	in := bugEvidenceInput{Kind: "test", Service: "orders", RunID: run.ID, RequestKey: "create-evidence-0001", Title: "订单详情返回错误", Note: "password=fixture-password"}
	w := apiTestingRequest(h, "POST", "/api/bug-evidence/preview", "bob", "admin", in)
	if w.Code == 200 {
		t.Fatal("admin must not acquire another reader's private report")
	}
	w = apiTestingRequest(h, "POST", "/api/bug-evidence/link", "alice", "member", in)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var first bugEvidence
	json.Unmarshal(w.Body.Bytes(), &first)
	if first.WorkItemID < 1 || first.RunID != run.ID || len(first.Cases) != 1 || first.Cases[0].Revision != c.Revision || strings.Contains(w.Body.String(), "secret-fixture-token") || strings.Contains(w.Body.String(), "fixture-password") {
		t.Fatal(w.Body.String())
	}
	for i := 0; i < 2; i++ {
		again, e := s.saveBugEvidence(in, "alice")
		if e != nil || again.ID != first.ID || again.WorkItemID != first.WorkItemID {
			t.Fatalf("retry: %+v %v", again, e)
		}
	}
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM work_items WHERE kind='bug'`).Scan(&count)
	if count != 1 {
		t.Fatal("duplicate bug", count)
	}
	changed := in
	changed.Title = "different"
	if _, e := s.saveBugEvidence(changed, "alice"); e == nil {
		t.Fatal("request key reused for changed payload")
	}
	if e := s.DeleteWorkItem(first.WorkItemID); e == nil {
		t.Fatal("evidence-bearing Bug deleted")
	}
	public, e := s.bugEvidenceList(first.WorkItemID, "bob")
	if e != nil || len(public) != 1 || public[0].CanOpen || public[0].RunID != 0 || public[0].Snapshot != "" || public[0].Note != "" || public[0].ApplicationID != "" {
		t.Fatalf("private evidence leaked: %+v %v", public, e)
	}
	var encrypted string
	s.db.QueryRow(`SELECT document FROM bug_evidence WHERE id=?`, first.ID).Scan(&encrypted)
	if !strings.HasPrefix(encrypted, "enc:v1:") || strings.Contains(encrypted, "order missing") {
		t.Fatal("evidence not encrypted")
	}
	// Updating or deleting a case never rewrites the version in old evidence.
	if _, e = s.db.Exec(`UPDATE api_test_cases SET revision=revision+1 WHERE id=?`, c.ID); e != nil {
		t.Fatal(e)
	}
	w = apiTestingRequest(h, "GET", fmt.Sprintf("/api/bug-evidence/%d/regression", first.ID), "alice", "member", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "evidence_revision=1") || !strings.Contains(w.Body.String(), fmt.Sprint("bug=", first.WorkItemID)) {
		t.Fatal(w.Body.String())
	}
	w = apiTestingRequest(h, "GET", fmt.Sprintf("/api/bug-evidence/%d/regression", first.ID), "bob", "admin", nil)
	if w.Code != 404 {
		t.Fatal("regression permission", w.Code)
	}
	if _, e = s.db.Exec(`UPDATE api_test_cases SET deleted=1 WHERE id=?`, c.ID); e != nil {
		t.Fatal(e)
	}
	w = apiTestingRequest(h, "GET", fmt.Sprintf("/api/bug-evidence/%d/regression", first.ID), "alice", "member", nil)
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	private, e := s.bugEvidenceList(first.WorkItemID, "alice")
	if e != nil || private[0].Cases[0].Revision != 1 || !strings.Contains(private[0].Snapshot, "order missing") {
		t.Fatal(private, e)
	}
}

func TestBugEvidenceLogSnapshotAndAtomicAssociation(t *testing.T) {
	s, h := apiTestingFixture(t)
	in := bugEvidenceInput{Kind: "log", RequestKey: "log-evidence-000001", Title: "订单日志异常", Log: &bugLogEvidence{Service: "orders", Environment: "test", Timestamp: time.Now().UnixMilli(), TraceID: "trace-fixture-123", Excerpt: "ERROR order missing\nAuthorization: Bearer super-secret\npassword=fixture-pass&access_token=secret-abc\nCookie: session=foo"}}
	v, e := s.saveBugEvidence(in, "alice")
	if e != nil {
		t.Fatal(e)
	}
	if v.ApplicationID != s.GetService("orders").ApplicationID || v.Log.TraceID != "trace-fixture-123" || strings.Contains(v.Log.Excerpt, "super-secret") || strings.Contains(v.Log.Excerpt, "fixture-pass") || strings.Contains(v.Log.Excerpt, "secret-abc") || strings.Contains(v.Log.Excerpt, "session=foo") {
		t.Fatal(v)
	}
	in.WorkItemID = v.WorkItemID
	in.RequestKey = "log-evidence-000002"
	again, e := s.saveBugEvidence(in, "alice")
	if e != nil || again.ID != v.ID {
		t.Fatal(again, e)
	}
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM bug_evidence WHERE work_item_id=?`, v.WorkItemID).Scan(&count)
	if count != 1 {
		t.Fatal("duplicate log association", count)
	}
	in.WorkItemID = 999999
	in.RequestKey = "log-evidence-000003"
	if _, e = s.saveBugEvidence(in, "alice"); e == nil {
		t.Fatal("missing Bug accepted")
	}
	if w := apiTestingRequest(h, "POST", "/api/bug-evidence/link", "alice", "member", map[string]any{"kind": "log", "log": in.Log, "unexpected": true}); w.Code != 400 {
		t.Fatal("unknown field accepted")
	}
	// Logs can expire without deleting the retained encrypted snapshot.
	if _, e = s.db.Exec(`DELETE FROM api_test_runs`); e != nil {
		t.Fatal(e)
	}
	items, e := s.bugEvidenceList(v.WorkItemID, "alice")
	if e != nil || !strings.Contains(items[0].Log.Excerpt, "order missing") {
		t.Fatal(items, e)
	}
	if got := bugSafeText(strings.Repeat("x", 3000)+" token=secret", 2048); strings.Contains(got, "secret") || len([]rune(got)) > 2100 {
		t.Fatal("redaction must precede bounding")
	}
}

func TestBugEvidenceRegressionAssociatesWithoutClosing(t *testing.T) {
	s, h := apiTestingFixture(t)
	c := evidenceCase(t, h)
	run := seedBugEvidenceRun(t, s, "alice", c)
	v, e := s.saveBugEvidence(bugEvidenceInput{Kind: "test", Service: "orders", RunID: run.ID, Title: "回归验收", RequestKey: "regression-link-001"}, "alice")
	if e != nil {
		t.Fatal(e)
	}
	s.apiTests.client.Transport = apiTestRoundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
	})
	in := apiTestingInput(t, s, "regression-execution-0001")
	in.BugID = v.WorkItemID
	in.CaseID = c.ID
	in.CaseRevision = c.Revision
	next, e := s.apiTests.start(s.GetService("orders"), "alice", false, in)
	if e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		list, e := s.bugEvidenceList(v.WorkItemID, "alice")
		if e != nil {
			t.Fatal(e)
		}
		if len(list) == 2 {
			if list[0].RunID != next.ID || list[0].Status != "success" {
				t.Fatal(list)
			}
			if s.GetWorkItem(v.WorkItemID).Status != WorkItemOpen {
				t.Fatal("passing regression closed Bug")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("regression evidence not associated")
}

func TestBugEvidenceSSESnapshotBoundedAndRedacted(t *testing.T) {
	run := apiTestRun{Status: "failed", Owner: "alice", Result: apitest.Result{Error: "观察时限结束", Events: []apitest.Event{{Data: `{"token":"sse-secret","error":"fixture stream error"}`}}}}
	v := bugTestEvidence(&run, "app-fixture")
	if !strings.Contains(v.Snapshot, "SSE") || !strings.Contains(v.Snapshot, "fixture stream error") || strings.Contains(v.Snapshot, "sse-secret") {
		t.Fatal(v.Snapshot)
	}
	run.Result.Error = strings.Repeat("x", 500)
	run.Result.Events[0].Data = strings.Repeat("y", 5000)
	if len([]rune(bugTestEvidence(&run, "app-fixture").Snapshot)) > 2060 {
		t.Fatal("snapshot exceeded bound")
	}
}
