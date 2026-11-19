package deploy

import (
	"encoding/json"
	"fmt"
	"logplatform/pkg/apitest"
	"logplatform/pkg/workflownode"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func apiWorkflowFixture(t *testing.T, env string) (*Store, *ReleaseManager, DeliveryRequest, apiTestCase) {
	t.Helper()
	t.Setenv(workflownode.SharedWorkspacesEnv, "api-center,deploy")
	st, m, req := deliveryFixture(t)
	req.Environment = env
	h := Routes(st, NewBuilder(st, st.dataDir, NewLogHub()), NewLogHub(), "", NewStepLib("../../steps"))
	c := saveTeamCase(t, h, "alice", "member", apiTestCase{Name: "团队回归", Visibility: "team", Steps: []apiTestStep{{Name: "校验", Request: apitest.Request{Method: "POST", Path: "/orders", Assertions: []apitest.Assertion{{Type: "status", Expected: "200"}}}}}})
	w := apiTestingRequest(h, "POST", "/api/api-testing/suites?service=orders", "alice", "member", apiTestSuite{Name: "团队集", Visibility: "team", CaseIDs: []int64{c.ID}})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var suite apiTestSuite
	json.Unmarshal(w.Body.Bytes(), &suite)
	p := apiTestProfile{Enabled: true, BaseURL: "http://192.0.2.55:8080", Auth: apitest.Auth{Mode: "none"}, RegressionSuiteID: suite.ID, RegressionTargetKey: "entry"}
	if e := st.saveAPITestingProfile(st.GetService("orders").ApplicationID, env, p); e != nil {
		t.Fatal(e)
	}
	tpl := &DeliveryTemplate{Name: "接口回归", Mode: "fixed", Services: []string{"orders"}, Nodes: []DeliveryNode{{ID: "deploy", Kind: "deploy", Name: "合成部署占位（不执行）"}, {ID: "test", Kind: "extension", Name: "接口回归", Extension: &workflownode.Binding{Provider: workflownode.APITestProvider, NodeKey: "regression"}}}}
	if e := st.SaveDeliveryTemplate(tpl); e != nil {
		t.Fatal(e)
	}
	req.TemplateID = tpl.ID
	return st, m, req, c
}
func workflowDeployObservation(t *testing.T, st *Store, req DeliveryRequest) {
	t.Helper()
	a := st.GetArtifact(req.Artifacts["orders"])
	_, e := st.db.Exec(`INSERT INTO deploys(service,version,host,environment,artifact_id,artifact_digest,status,actor,started_at,ended_at) VALUES(?,?,?,?,?,?,'success','fixture',100,110)`, "orders", a.Version, req.Environment+"-1", req.Environment, a.ID, a.Digest)
	if e != nil {
		t.Fatal(e)
	}
}
func waitWorkflowAPI(t *testing.T, st *Store, id int64) *DeliveryRun {
	t.Helper()
	until := time.Now().Add(4 * time.Second)
	for time.Now().Before(until) {
		r := st.DeliveryRun(id)
		if r != nil && len(r.Steps) > 0 && r.Steps[len(r.Steps)-1].Status != "pending" && (r.Steps[len(r.Steps)-1].Status != "executing" || r.Steps[len(r.Steps)-1].LeaseUntil == 0) {
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("workflow timeout")
	return nil
}
func TestAPIWorkflowFrozenPlanReportAndNoReplay(t *testing.T) {
	st, m, req, c := apiWorkflowFixture(t, "test")
	run, e := st.CreateDeliveryRun(req, "alice", m)
	if e != nil {
		t.Fatal(e)
	}
	run.Steps[0].Status = "success"
	if e = st.saveDeliveryRun(run); e != nil {
		t.Fatal(e)
	}
	task := run.Plan.Nodes[1].Extension.Tests[0]
	if task.CaseRevisions[fmt.Sprint(c.ID)] != c.Revision {
		t.Fatal("unfrozen case")
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, `{"ok":true}`) }))
	defer server.Close()
	st.apiTests.client = apiTestingClient(server)
	if _, e = st.AdvanceDeliveryRun(run.ID, "alice", false, m); e == nil {
		t.Fatal("ran without reviewed artifact observation")
	}
	if calls.Load() != 0 {
		t.Fatal("unexpected HTTP")
	}
	workflowDeployObservation(t, st, req)
	if _, e = st.AdvanceDeliveryRun(run.ID, "alice", false, m); e != nil {
		t.Fatal(e)
	}
	result := waitWorkflowAPI(t, st, run.ID)
	if result.Steps[len(result.Steps)-1].Status != "success" || len(result.Steps[len(result.Steps)-1].APIReports) != 1 {
		t.Fatalf("%+v", result)
	}
	ref := result.Steps[len(result.Steps)-1].APIReports[0]
	report, e := st.apiTests.load(st.GetService("orders").ApplicationID, "alice", ref.RunID)
	if e != nil || report.SuiteID != task.SuiteID || report.DeliveryRunID != run.ID || report.DeliveryNodeID != "test" || report.Status != "success" {
		t.Fatal(report, e)
	}
	if _, e = st.apiTests.load(st.GetService("orders").ApplicationID, "bob", ref.RunID); e == nil {
		t.Fatal("workflow report leaked")
	}
	_, _ = st.AdvanceDeliveryRun(run.ID, "alice", false, m)
	if calls.Load() != 1 {
		t.Fatal("replayed", calls.Load())
	}
}
func TestAPIWorkflowChangedCaseAndProfileBlockBeforeHTTP(t *testing.T) {
	for _, kind := range []string{"case", "profile", "variables", "plugin"} {
		t.Run(kind, func(t *testing.T) {
			st, m, req, c := apiWorkflowFixture(t, "test")
			run, e := st.CreateDeliveryRun(req, "alice", m)
			if e != nil {
				t.Fatal(e)
			}
			run.Steps[0].Status = "success"
			if e = st.saveDeliveryRun(run); e != nil {
				t.Fatal(e)
			}
			workflowDeployObservation(t, st, req)
			switch kind {
			case "case":
				h := Routes(st, NewBuilder(st, st.dataDir, NewLogHub()), NewLogHub(), "", NewStepLib("../../steps"))
				c.Name = "修订"
				saveTeamCase(t, h, "alice", "member", c)
			case "profile":
				p, _ := st.apiTestingProfile(st.GetService("orders").ApplicationID, "test")
				p.BaseURL = "http://192.0.2.99"
				if e = st.saveAPITestingProfile(st.GetService("orders").ApplicationID, "test", p); e != nil {
					t.Fatal(e)
				}
			case "variables":
				env := st.GetServiceEnvironment("orders", "test")
				env.RuntimeEnv = map[string]string{"API_KEY": "changed-fixture"}
				if e = st.UpsertServiceEnvironment(*env); e != nil {
					t.Fatal(e)
				}
			case "plugin":
				t.Setenv(workflownode.SharedWorkspacesEnv, "deploy")
			}
			var calls atomic.Int32
			st.apiTests.client = &http.Client{Transport: apiTestRoundTrip(func(r *http.Request) (*http.Response, error) { calls.Add(1); return nil, fmt.Errorf("should not send") })}
			if _, e = st.AdvanceDeliveryRun(run.ID, "alice", false, m); e == nil {
				t.Fatal("stale plan executed")
			}
			if calls.Load() != 0 {
				t.Fatal("unexpected HTTP")
			}
		})
	}
}
func TestAPIWorkflowProtectedApprovalFailureAndUnknown(t *testing.T) {
	for _, status := range []string{"failed", "unknown", "success"} {
		t.Run(status, func(t *testing.T) {
			st, m, req, _ := apiWorkflowFixture(t, "prod")
			run, e := st.CreateDeliveryRun(req, "alice", m)
			if e != nil {
				t.Fatal(e)
			}
			run.Steps[0].Status = "success"
			if e = st.saveDeliveryRun(run); e != nil {
				t.Fatal(e)
			}
			workflowDeployObservation(t, st, req)
			if _, e = st.AdvanceDeliveryRun(run.ID, "alice", false, m); e == nil {
				t.Fatal("protected workflow bypassed approval")
			}
			run, e = st.ReviewDeliveryRun(run.ID, "reviewer", run.Digest, true)
			if e != nil {
				t.Fatal(e)
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if status == "failed" {
					w.WriteHeader(500)
				}
				fmt.Fprint(w, `{}`)
			}))
			defer server.Close()
			st.apiTests.client = apiTestingClient(server)
			if status == "unknown" {
				st.apiTests.client = &http.Client{Transport: apiTestRoundTrip(func(r *http.Request) (*http.Response, error) {
					calls.Add(1)
					return nil, fmt.Errorf("network disconnected")
				})}
			}
			if _, e = st.AdvanceDeliveryRun(run.ID, "alice", false, m); e != nil {
				t.Fatal(e)
			}
			result := waitWorkflowAPI(t, st, run.ID)
			expected := status
			if status == "unknown" {
				expected = "executing"
			}
			if result.Steps[len(result.Steps)-1].Status != expected {
				t.Fatalf("expected %s got %+v", expected, result.Steps)
			}
			_, _ = st.AdvanceDeliveryRun(run.ID, "alice", false, m)
			if calls.Load() != 1 {
				t.Fatal("uncertain/failed replay", calls.Load())
			}
			if status == "unknown" && !strings.Contains(result.Steps[len(result.Steps)-1].Detail, "待") {
				t.Fatal(result.Steps[len(result.Steps)-1])
			}
		})
	}
}

func TestAPIWorkflowStopAndInterruptedPlanDoNotResume(t *testing.T) {
	st, m, req, _ := apiWorkflowFixture(t, "test")
	run, e := st.CreateDeliveryRun(req, "alice", m)
	if e != nil {
		t.Fatal(e)
	}
	run.Steps[0].Status = "success"
	if e = st.saveDeliveryRun(run); e != nil {
		t.Fatal(e)
	}
	workflowDeployObservation(t, st, req)
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }))
	defer server.Close()
	st.apiTests.client = apiTestingClient(server)
	if _, e = st.AdvanceDeliveryRun(run.ID, "alice", false, m); e != nil {
		t.Fatal(e)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("not started")
	}
	var ref workflowAPIReport
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		v := st.DeliveryRun(run.ID)
		if len(v.Steps[1].APIReports) > 0 {
			ref = v.Steps[1].APIReports[0]
			break
		}
		time.Sleep(time.Millisecond)
	}
	if ref.RunID == 0 {
		t.Fatal("no report")
	}
	st.apiTests.stop(ref.RunID)
	v := waitWorkflowAPI(t, st, run.ID)
	if v.Steps[1].Status != "failed" {
		t.Fatal(v.Steps)
	}
	// Simulate an interrupted durable marker with no report. Continuing may read,
	// but must never launch an HTTP request or complete the missing work.
	v.Status = "running"
	v.Steps[1].Status = "executing"
	v.Steps[1].APIReports = nil
	v.Steps[1].LeaseUntil = 0
	if e = st.saveDeliveryRun(v); e != nil {
		t.Fatal(e)
	}
	if _, e = st.AdvanceDeliveryRun(run.ID, "alice", false, m); e == nil {
		t.Fatal("interrupted plan resumed")
	}
	v = st.DeliveryRun(run.ID)
	if v.Steps[1].Status != "executing" || len(v.Steps[1].APIReports) > 0 {
		t.Fatal("invented report")
	}
}

func TestAPIWorkflowPreviewRejectsNodeBeforeDeploy(t *testing.T) {
	st, m, req, _ := apiWorkflowFixture(t, "test") // Read the saved definition through the same resolver, then swap only order.
	plan, _, e := st.ResolveDelivery(req, m)
	if e != nil {
		t.Fatal(e)
	}
	req.Nodes = append([]DeliveryNode(nil), plan.Nodes...)
	req.Nodes[0], req.Nodes[1] = req.Nodes[1], req.Nodes[0]
	p, _, e := st.ResolveDelivery(req, m)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(strings.Join(p.Blockers, " "), "部署节点之后") {
		t.Fatal(p.Blockers)
	}
}

func TestAPIWorkflowCannotAdoptEarlierManualReport(t *testing.T) {
	st, m, req, _ := apiWorkflowFixture(t, "test")
	run, e := st.CreateDeliveryRun(req, "alice", m)
	if e != nil {
		t.Fatal(e)
	}
	run.Steps[0].Status = "success"
	if e = st.saveDeliveryRun(run); e != nil {
		t.Fatal(e)
	}
	workflowDeployObservation(t, st, req)
	task := run.Plan.Nodes[1].Extension.Tests[0]
	sv, in, e := st.validateAPITestWorkflowTask(task, true)
	if e != nil {
		t.Fatal(e)
	}
	in.RequestID = workflowExecutionID(run.ID, "test:0")
	in.Reason += " #" + fmt.Sprint(run.ID)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, `{}`) }))
	defer server.Close()
	st.apiTests.client = apiTestingClient(server)
	earlier, e := st.apiTests.start(sv, "alice", false, in)
	if e != nil {
		t.Fatal(e)
	}
	waitAPITest(t, st, "alice", earlier.ID)
	if _, e = st.AdvanceDeliveryRun(run.ID, "alice", false, m); e != nil {
		t.Fatal(e)
	}
	latest := waitWorkflowAPI(t, st, run.ID)
	if latest.Steps[1].Status != "failed" || len(latest.Steps[1].APIReports) > 0 || calls.Load() != 1 {
		t.Fatalf("adopted manual report: %+v calls=%d", latest.Steps, calls.Load())
	}
}
