package deploy

import (
	"encoding/json"
	"fmt"
	"logplatform/pkg/apitest"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func graphCaseFixture() apiTestCase {
	httpNode := func(id, path string, outputs ...apitest.Extraction) apiTestStep {
		return apiTestStep{ID: id, Name: id, Request: apitest.Request{Path: path, Assertions: []apitest.Assertion{{Type: "status", Expected: "200"}}}, Extractions: outputs}
	}
	return apiTestCase{Name: "条件与并行", Graph: &apiTestGraph{Entry: "seed", Concurrency: 4, Edges: []apiTestEdge{{From: "seed", To: "condition"}, {From: "condition", To: "fork", Branch: "true"}, {From: "condition", To: "other", Branch: "false"}, {From: "fork", To: "left"}, {From: "fork", To: "right"}, {From: "left", To: "joined"}, {From: "right", To: "joined"}, {From: "joined", To: "verify"}, {From: "verify", To: "end"}, {From: "other", To: "end"}}}, Steps: []apiTestStep{
		httpNode("seed", "/seed", apitest.Extraction{Name: "route", Source: "json", Path: "/route"}),
		{ID: "condition", Name: "判断", Kind: "condition", Condition: &apiTestCondition{Variable: "step.route", Operator: "equals", Expected: apiTestCell{Type: "boolean", Value: "true"}}},
		{ID: "fork", Name: "同时查询", Kind: "parallel"}, httpNode("left", "/left", apitest.Extraction{Name: "left_value", Source: "json", Path: "/value"}), httpNode("right", "/right", apitest.Extraction{Name: "right_value", Source: "json", Path: "/value"}),
		{ID: "joined", Name: "等待两项完成", Kind: "join"}, httpNode("verify", "/verify/{{step.left_value}}/{{step.right_value}}"), httpNode("other", "/other"), {ID: "end", Name: "分支结束", Kind: "join"},
	}}
}

func TestAPITestGraphConditionalParallelIsolationAndFrozenReport(t *testing.T) {
	st, h := apiTestingFixture(t)
	var route atomic.Bool
	route.Store(true)
	var parallel, maxParallel, others atomic.Int32
	barrier := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/seed":
			fmt.Fprintf(w, `{"route":%v}`, route.Load())
		case "/left", "/right":
			current := parallel.Add(1)
			defer parallel.Add(-1)
			for old := maxParallel.Load(); current > old && !maxParallel.CompareAndSwap(old, current); old = maxParallel.Load() {
			}
			if current == 2 {
				once.Do(func() { close(barrier) })
			}
			select {
			case <-barrier:
			case <-time.After(time.Second):
				t.Error("parallel paths ran serially")
			}
			fmt.Fprintf(w, `{"value":"%s-private"}`, strings.TrimPrefix(r.URL.Path, "/"))
		case "/verify/left-private/right-private":
			fmt.Fprint(w, `{"ok":true}`)
		case "/other":
			others.Add(1)
			fmt.Fprint(w, `{}`)
		default:
			t.Error("unexpected request", r.URL.Path)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer server.Close()
	st.apiTests.client = apiTestingClient(server)
	c := saveTeamCase(t, h, "alice", "member", graphCaseFixture())
	if c.DefinitionVersion != 2 {
		t.Fatal(c.DefinitionVersion)
	}
	runCase := func(key string) *apiTestRun {
		in := apiTestingInput(t, st, key)
		in.CaseID = c.ID
		in.CaseRevision = c.Revision
		in.Request = apitest.Request{}
		run, err := st.apiTests.start(st.GetService("orders"), "alice", false, in)
		if err != nil {
			t.Fatal(err)
		}
		return waitAPITest(t, st, "alice", run.ID)
	}
	v := runCase("graph-parallel-first-run")
	if v.Status != "success" || maxParallel.Load() != 2 || others.Load() != 0 {
		t.Fatal(v.Status, maxParallel.Load(), others.Load())
	}
	if v.Steps[7].Status != "skipped" || v.Steps[7].SkipReason != "branch_not_selected" {
		t.Fatal(v.Steps[7])
	}
	if v.Steps[1].BranchTaken == nil || !*v.Steps[1].BranchTaken {
		t.Fatal("branch result missing")
	}
	raw, _ := json.Marshal(v)
	if strings.Contains(string(raw), "left-private") || strings.Contains(string(raw), "right-private") {
		t.Fatal("private outputs leaked")
	}
	route.Store(false)
	v = runCase("graph-false-second-run")
	if v.Status != "success" || v.Steps[2].Status != "skipped" || v.Steps[7].Status != "success" || others.Load() != 1 {
		t.Fatal(v.Status, v.Steps)
	}
}

func TestAPITestGraphRejectsCyclesAndUnsafeVariableRoutes(t *testing.T) {
	c := graphCaseFixture()
	if err := validateAPITestCase(&c); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*apiTestCase){
		func(c *apiTestCase) { c.Graph.Edges = append(c.Graph.Edges, apiTestEdge{From: "end", To: "seed"}) },
		func(c *apiTestCase) { c.Steps[4].Request.Path = "/{{step.left_value}}" },
		func(c *apiTestCase) { c.Steps[7].Request.Path = "/{{step.left_value}}" },
		func(c *apiTestCase) { c.Graph.Concurrency = 5 },
		func(c *apiTestCase) { c.Graph.Edges[2].Branch = "true" },
	} {
		var copy apiTestCase
		raw, _ := json.Marshal(graphCaseFixture())
		json.Unmarshal(raw, &copy)
		change(&copy)
		if err := validateAPITestCase(&copy); err == nil {
			t.Fatal("unsafe graph accepted")
		}
	}
	actual := map[string]any{"amount": json.Number("9007199254740993123")}
	ok, err := compareCondition(apiTestCondition{Variable: "step.amount", Operator: "greater", Expected: apiTestCell{Type: "number", Value: "9007199254740993122"}}, actual)
	if err != nil || !ok {
		t.Fatal("number precision lost", err)
	}
}

func TestAPITestGraphUpgradeCanSaveNormalizedAgentPreview(t *testing.T) {
	_, h := apiTestingFixture(t)
	c := saveTeamCase(t, h, "alice", "member", apiTestCase{Name: "升级图定义", Request: apitest.Request{Path: "/"}})
	c.Graph = &apiTestGraph{Entry: c.Steps[0].ID, Concurrency: 1}
	w := apiTestingRequest(h, "POST", "/api/api-testing/mcp/case-preview?service=orders&environment=test", "alice", "member", c)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var checked struct {
		Case apiTestCase `json:"case"`
	}
	json.Unmarshal(w.Body.Bytes(), &checked)
	if checked.Case.DefinitionVersion != 2 {
		t.Fatal("preview did not upgrade")
	}
	w = apiTestingRequest(h, "POST", "/api/api-testing/mcp/case-preview?service=orders&environment=test", "alice", "member", checked.Case)
	if w.Code != 200 {
		t.Fatal("normalized preview cannot be submitted", w.Body.String())
	}
	c = saveTeamCase(t, h, "alice", "member", checked.Case)
	if c.DefinitionVersion != 2 || c.Revision != 2 {
		t.Fatal(c)
	}
}

func TestAPITestLayoutCASPrivacyAndCaseRevision(t *testing.T) {
	st, h := apiTestingFixture(t)
	c := saveTeamCase(t, h, "alice", "member", apiTestCase{Name: "布局", Visibility: "team", Request: apitest.Request{Path: "/"}})
	path := fmt.Sprintf("/api/api-testing/cases/%d/layout?service=orders&workspace=tab-a", c.ID)
	v := apiTestLayout{Positions: map[string]apiTestPoint{c.Steps[0].ID: {X: 320, Y: -40}}, Zoom: 1.2, Pan: apiTestPoint{X: 10, Y: 20}}
	w := apiTestingRequest(h, "PUT", path, "alice", "member", v)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w = apiTestingRequest(h, "PUT", path, "alice", "member", v); w.Code != 409 {
		t.Fatal("stale layout overwrote")
	}
	w = apiTestingRequest(h, "GET", path, "alice", "member", nil)
	var saved apiTestLayout
	json.Unmarshal(w.Body.Bytes(), &saved)
	if saved.Revision != 1 || saved.Positions[c.Steps[0].ID].X != 320 {
		t.Fatal(w.Body.String())
	}
	for _, query := range []struct{ who, path string }{{"bob", path}, {"alice", strings.Replace(path, "tab-a", "tab-b", 1)}} {
		w = apiTestingRequest(h, "GET", query.path, query.who, "member", nil)
		json.Unmarshal(w.Body.Bytes(), &saved)
		if saved.Revision != 0 {
			t.Fatal("personal layout crossed scope")
		}
	}
	current, _ := st.apiTestCase(st.GetService("orders").ApplicationID, "alice", c.ID)
	if current.Revision != 1 {
		t.Fatal("layout changed execution revision")
	}
}

func parallelFailureCase() apiTestCase {
	return apiTestCase{Name: "并行失败处理", Graph: &apiTestGraph{Entry: "fork", Concurrency: 2, Edges: []apiTestEdge{{From: "fork", To: "a"}, {From: "fork", To: "b"}, {From: "a", To: "join"}, {From: "b", To: "join"}, {From: "join", To: "after"}}}, Steps: []apiTestStep{{ID: "fork", Name: "并行", Kind: "parallel"}, {ID: "a", Name: "A", Request: apitest.Request{Path: "/a"}}, {ID: "b", Name: "B", Request: apitest.Request{Path: "/b"}}, {ID: "join", Name: "汇合", Kind: "join"}, {ID: "after", Name: "后续请求", Request: apitest.Request{Path: "/after"}}}}
}

func TestAPITestGraphParallelFailureAndCancellationNeverAdvance(t *testing.T) {
	for _, cancelRun := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelRun), func(t *testing.T) {
			st, h := apiTestingFixture(t)
			var after, started atomic.Int32
			both := make(chan struct{})
			var once sync.Once
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/after" {
					after.Add(1)
					fmt.Fprint(w, `{}`)
					return
				}
				if started.Add(1) == 2 {
					once.Do(func() { close(both) })
				}
				if cancelRun {
					<-r.Context().Done()
					return
				}
				select {
				case <-both:
				case <-time.After(time.Second):
					t.Error("paths did not overlap")
				}
				if r.URL.Path == "/a" {
					http.Error(w, "failed", 500)
				} else {
					time.Sleep(25 * time.Millisecond)
					fmt.Fprint(w, `{}`)
				}
			}))
			defer server.Close()
			st.apiTests.client = apiTestingClient(server)
			c := saveTeamCase(t, h, "alice", "member", parallelFailureCase())
			in := apiTestingInput(t, st, "parallel-stop-test-key")
			in.CaseID = c.ID
			in.CaseRevision = c.Revision
			in.Request = apitest.Request{}
			run, err := st.apiTests.start(st.GetService("orders"), "alice", false, in)
			if err != nil {
				t.Fatal(err)
			}
			if cancelRun {
				select {
				case <-both:
				case <-time.After(2 * time.Second):
					t.Fatal("parallel requests never started")
				}
				w := apiTestingRequest(h, "POST", fmt.Sprintf("/api/api-testing/runs/%d/stop?service=orders", run.ID), "alice", "member", nil)
				if w.Code != 200 {
					t.Fatal(w.Body.String())
				}
			}
			v := waitAPITest(t, st, "alice", run.ID)
			if v.Status == "success" || after.Load() != 0 || v.Steps[4].Status != "skipped" {
				t.Fatal(v.Status, v.Steps, after.Load())
			}
			if v.Steps[1].Status == "running" || v.Steps[2].Status == "running" {
				t.Fatal("in-flight result not recorded")
			}
			if !cancelRun && (v.Steps[1].Status != "failed" || v.Steps[2].Status != "success") {
				t.Fatal("failure lost sibling result", v.Steps)
			}
		})
	}
}
