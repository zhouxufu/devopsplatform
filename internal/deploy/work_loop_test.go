package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func workLoopFixture(t *testing.T, kind string) (*Store, http.Handler, *WorkLoop, DeliveryRequest) {
	t.Helper()
	s, _, req, _ := apiWorkflowFixture(t, "test")
	tpl := s.DeliveryTemplate(req.TemplateID)
	tpl.Mode = "version"
	tpl.Services = nil
	tpl.Preparation = []DeliveryNode{{ID: "scope", Kind: "manual", Name: "本人变更范围", AssigneeSource: "change_submitters", ApprovalMode: "all", Instructions: "核对本人范围"}, {ID: "code", Kind: "code-prepare", Name: "代码准备", ApplicationScope: "delivery", DefaultCodePolicy: &CodePolicy{Mode: "integration", TargetBranch: "shared-test"}}}
	if e := s.SaveDeliveryTemplate(tpl); e != nil {
		t.Fatal(e)
	}
	if e := s.saveDeliveryPolicy("test", DeliveryPolicy{WorkflowTemplateID: tpl.ID}); e != nil {
		t.Fatal(e)
	}
	w := &WorkItem{Source: "manual", Kind: kind, Title: "隔离跟进", Owner: "alice", AssignedTo: "alice"}
	if e := s.CreateWorkItem(w); e != nil {
		t.Fatal(e)
	}
	c := &Change{Kind: "code", Title: "本轮代码", Service: "orders", Reference: "feature/fix", Owner: "alice"}
	if e := s.CreateChange(c); e != nil {
		t.Fatal(e)
	}
	if e := s.SetChangeWorkItems(c.ID, []int64{w.ID}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SetChangeReady(c.ID, true); e != nil {
		t.Fatal(e)
	}
	spec := WorkLoopSpec{WorkItemID: w.ID, Environment: "test", Applications: []string{"orders"}, ExpiresAt: time.Now().Add(time.Hour).Unix(), MaxIterations: 3, Acceptance: "回归通过并保留人工验收", CompleteScope: true}
	g, e := s.previewWorkLoop(context.Background(), spec, "alice")
	if e != nil {
		t.Fatal(e)
	}
	loop := &WorkLoop{ID: strings.Repeat("a", 32), Owner: "alice", Grant: *g, State: "developing", Stage: "development", Iterations: []WorkLoopIteration{{Number: 1, State: "developing", ChangeIDs: []int64{c.ID}}}}
	if e = s.saveWorkLoop(loop); e != nil {
		t.Fatal(e)
	}
	h := Routes(s, NewBuilder(s, s.dataDir, NewLogHub()), NewLogHub(), "", NewStepLib("../../steps"))
	return s, h, loop, req
}
func loopRequest(h http.Handler, method, path, actor string, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.Header.Set("X-User", actor)
	r.Header.Set("X-Role", "member")
	r.Header.Set("X-Reviewer-Directory", `["alice","reviewer"]`)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestWorkLoopAuthorizationRevisionAndScope(t *testing.T) {
	s, h, task, _ := workLoopFixture(t, "requirement")
	foreign := loopRequest(h, "GET", "/api/work-loops/"+task.ID, "bob", nil)
	if foreign.Code != 403 {
		t.Fatal(foreign.Code, foreign.Body.String())
	}
	for _, mutation := range []func(*WorkLoopSpec){func(s *WorkLoopSpec) { s.Environment = "prod" }, func(s *WorkLoopSpec) { s.Applications = []string{"not-authorized"} }} {
		spec := task.Grant.Spec
		mutation(&spec)
		if _, e := s.previewWorkLoop(context.Background(), spec, "alice"); e == nil {
			t.Fatal("invalid authorization accepted", spec)
		}
	}
	w := loopRequest(h, "POST", "/api/work-loops/"+task.ID+"/start", "alice", map[string]any{"revision": task.Revision + 1, "change_ids": task.current().ChangeIDs, "summary": "本地验证"})
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = loopRequest(h, "POST", "/api/work-loops/"+task.ID+"/start", "alice", map[string]any{"revision": task.Revision, "change_ids": task.current().ChangeIDs, "summary": "本地验证"})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	current, _ := s.workLoop(task.ID)
	w = loopRequest(h, "POST", "/api/work-loops/"+task.ID+"/advance", "alice", map[string]any{"revision": current.Revision})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	current, _ = s.workLoop(task.ID)
	if current.current().PreparationID == 0 {
		t.Fatal("no preparation", w.Body.String())
	}
	w = loopRequest(h, "POST", "/api/work-loops/"+task.ID+"/advance", "alice", map[string]any{"revision": current.Revision})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	current, _ = s.workLoop(task.ID)
	run := s.DeliveryRun(current.current().PreparationID)
	if run.Steps[0].Status != "success" {
		t.Fatal("self scope not completed", w.Body.String(), run.Steps[0])
	}
	if run.Steps[1].Status != "pending" {
		t.Fatal("scope confirmation executed code")
	}
	stale := *current
	w = loopRequest(h, "POST", "/api/work-loops/"+task.ID+"/control", "alice", map[string]any{"revision": current.Revision, "action": "revoke"})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if e := s.saveWorkLoop(&stale); e == nil {
		t.Fatal("stale coordinator overwrote revocation")
	}
	current, _ = s.workLoop(task.ID)
	w = loopRequest(h, "POST", "/api/work-loops/"+task.ID+"/advance", "alice", map[string]any{"revision": current.Revision})
	if w.Code != 403 {
		t.Fatal("revoked task dispatched", w.Body.String())
	}
}
func TestWorkLoopWritebackIsAtomicForBugAndRequirement(t *testing.T) {
	for _, kind := range []string{"bug", "requirement"} {
		t.Run(kind, func(t *testing.T) {
			s, _, task, _ := workLoopFixture(t, kind)
			task.current().DeliveryID = 42
			task.current().Summary = "合成回执测试，不代表执行过部署"
			for n := 0; n < 2; n++ {
				if e := s.writeWorkLoopSource(context.Background(), task); e != nil {
					t.Fatal(e)
				}
			}
			w := s.GetWorkItem(task.Grant.Spec.WorkItemID)
			if w.Status != WorkItemResolved {
				t.Fatal(w.Status)
			}
			var count int
			s.db.QueryRow(`SELECT COUNT(*) FROM research_activity WHERE work_item_id=? AND kind='comment'`, w.ID).Scan(&count)
			if count != 1 {
				t.Fatal("replayed source comment", count)
			}
			if w.Status == WorkItemClosed {
				t.Fatal("closed without human acceptance")
			}
			if e := s.workLoopEvidence(task); e == nil {
				t.Fatal("fabricated report accepted")
			}
		})
	}
}
func TestWorkLoopLostDispatchDoesNotReplay(t *testing.T) {
	s, _, task, _ := workLoopFixture(t, "bug")
	task.Stage = "preparation"
	task.Intent = &WorkLoopIntent{Name: "start_code", State: "dispatching", Path: "/never", Body: json.RawMessage(`{"expected_revision":1}`)}
	if e := s.saveWorkLoop(task); e != nil {
		t.Fatal(e)
	}
	calls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/never", func(w http.ResponseWriter, r *http.Request) { calls++ })
	r := httptest.NewRequest("POST", "/", nil)
	if e := s.advanceWorkLoop(mux, r, task, nil, nil); e == nil {
		t.Fatal("unknown dispatch allowed")
	}
	if calls != 0 {
		t.Fatal("replayed")
	}
}
func TestWorkLoopPolicyChangeAndPartialDelivery(t *testing.T) {
	s, _, task, _ := workLoopFixture(t, "requirement")
	task.Grant.Spec.CompleteScope = false
	if e := s.writeWorkLoopSource(context.Background(), task); e != nil {
		t.Fatal(e)
	}
	if s.GetWorkItem(task.Grant.Spec.WorkItemID).Status == WorkItemResolved {
		t.Fatal("partial iteration completed requirement")
	}
	task.Grant.Spec.CompleteScope = true
	sv := s.GetService("orders")
	sv.Branch = "unexpected"
	if e := s.UpsertService(*sv); e != nil {
		t.Fatal(e)
	}
	if e := s.validateWorkLoop(context.Background(), task, "alice"); e == nil {
		t.Fatal("changed target policy accepted")
	}
}
func TestWorkLoopCreateIdempotency(t *testing.T) {
	s, h, task, _ := workLoopFixture(t, "bug")
	id := strings.Repeat("b", 32)
	body := map[string]any{"id": id, "spec": task.Grant.Spec, "expected_digest": task.Grant.Digest}
	for i := 0; i < 2; i++ {
		w := loopRequest(h, "POST", "/api/work-loops", "alice", body)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	created, _ := s.workLoop(id)
	if created.Revision != 1 {
		t.Fatal("duplicate changed revision")
	}
	w := loopRequest(h, "GET", fmt.Sprintf("/api/work-loops?work_item_id=%d", task.Grant.Spec.WorkItemID), "bob", nil)
	if strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatal("foreign list leak", w.Body.String())
	}
}

func TestWorkLoopRegressionRejectsOldOrDifferentArtifact(t *testing.T) {
	s, m, req, _ := apiWorkflowFixture(t, "test")
	run, e := s.CreateDeliveryRun(req, "alice", m)
	if e != nil {
		t.Fatal(e)
	}
	run.Steps[0].Status = "success"
	if e = s.saveDeliveryRun(run); e != nil {
		t.Fatal(e)
	}
	workflowDeployObservation(t, s, req)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"ok":true}`) }))
	defer server.Close()
	s.apiTests.client = apiTestingClient(server)
	if _, e = s.AdvanceDeliveryRun(run.ID, "alice", false, m); e != nil {
		t.Fatal(e)
	}
	run = waitWorkflowAPI(t, s, run.ID)
	if run.Status != "success" {
		t.Fatal(run.Status, run.Steps)
	}
	// Attach the already verified synthetic engine result to a task fixture.
	// No real release is executed; the regression above uses the real engine.
	w := &WorkItem{Source: "manual", Kind: "requirement", Title: "回归门禁", Owner: "alice"}
	if e = s.CreateWorkItem(w); e != nil {
		t.Fatal(e)
	}
	run.Plan.Scope = &DeliveryScope{WorkItems: []WorkItem{*w}, Changes: []Change{}}
	run.Plan.PreparationRunID = 7
	// Only this synthetic fixture writes a plan; production snapshots remain immutable.
	planJSON, _ := json.Marshal(run.Plan)
	if _, e = s.db.Exec(`UPDATE delivery_runs SET plan=? WHERE id=?`, string(planJSON), run.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.saveDeliveryRun(run); e != nil {
		t.Fatal(e)
	}
	task := &WorkLoop{ID: strings.Repeat("e", 32), Owner: "alice", Grant: WorkLoopGrant{Spec: WorkLoopSpec{WorkItemID: w.ID, Applications: []string{"orders"}, Environment: "test", CompleteScope: true}, Regression: run.Plan.Nodes[1].Extension.Tests}, Iterations: []WorkLoopIteration{{Number: 1, PreparationID: 7, DeliveryID: run.ID}}, Stage: "writeback"}
	if e = s.workLoopEvidence(task); e != nil {
		t.Fatal(e)
	}
	ref := task.current().Reports[0]
	report, e := s.apiTests.load(s.GetService("orders").ApplicationID, "alice", ref.RunID)
	if e != nil {
		t.Fatal(e)
	}
	original := *report
	for _, mutation := range []func(*apiTestRun){func(r *apiTestRun) { r.Target.ArtifactID++ }, func(r *apiTestRun) { r.DeliveryRunID++ }, func(r *apiTestRun) { r.SuiteRevision++ }, func(r *apiTestRun) { r.Status = "failed" }, func(r *apiTestRun) { r.StartedAt = 1 }} {
		copy := original
		mutation(&copy)
		if e = s.apiTests.persist(copy); e != nil {
			t.Fatal(e)
		}
		if e = s.workLoopEvidence(task); e == nil {
			t.Fatal("accepted mismatched regression", copy)
		}
		if s.GetWorkItem(w.ID).Status == WorkItemResolved {
			t.Fatal("failed gate wrote source")
		}
	}
	if e = s.apiTests.persist(original); e != nil {
		t.Fatal(e)
	}
	if e = s.workLoopEvidence(task); e != nil {
		t.Fatal(e)
	}
	if e = s.writeWorkLoopSource(context.Background(), task); e != nil {
		t.Fatal(e)
	}
	if s.GetWorkItem(w.ID).Status != WorkItemResolved {
		t.Fatal("matching regression did not allow completion")
	}
}

func TestWorkLoopSourceBugUsesNativeOperationExactlyOnce(t *testing.T) {
	s, w, writes, mu := researchFixture(t)
	task := &WorkLoop{ID: strings.Repeat("f", 32), Owner: "platform-alice", Grant: WorkLoopGrant{Spec: WorkLoopSpec{WorkItemID: w.ID, Environment: "test", CompleteScope: true, Resolution: "fixed", ResolvedBuild: "trunk"}}, Iterations: []WorkLoopIteration{{Number: 1, DeliveryID: 42, Summary: "合成已验证轮次"}}, Stage: "writeback"}
	if e := s.saveWorkLoop(task); e != nil {
		t.Fatal(e)
	}
	if e := s.writeWorkLoopSource(context.Background(), task); e != nil {
		t.Fatal(e)
	}
	if e := s.writeWorkLoopSource(context.Background(), task); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	count := *writes
	mu.Unlock()
	if count != 1 {
		t.Fatal("source operation replayed", count)
	}
	if s.GetWorkItem(w.ID).ExternalStatus != "resolved" {
		t.Fatal("native source result not read back")
	}
}

func TestWorkLoopChangeSaveReceiptRecoversWithoutDuplicate(t *testing.T) {
	s, _, task, _ := workLoopFixture(t, "bug")
	d, _ := s.WorkDelivery(task.Grant.Spec.WorkItemID)
	content := &WorkDeliverySave{Digest: d.Digest, Changes: []Change{{Kind: "code", Title: "后续修复", Service: "orders", Reference: "feature/follow-up"}}}
	if e := s.saveWorkLoopChanges(task, content); e != nil {
		t.Fatal(e)
	}
	task.Intent.State = "dispatching"
	if e := s.saveWorkLoop(task); e != nil {
		t.Fatal(e)
	}
	if e := s.reconcileWorkLoopIntent(task); e != nil {
		t.Fatal(e)
	}
	if task.Intent.State != "received" {
		t.Fatal("receipt not reconciled")
	}
	current, _ := s.WorkDelivery(task.Grant.Spec.WorkItemID)
	if len(current.Changes) != 2 {
		t.Fatal("duplicate change", current)
	}
}

func TestWorkLoopHumanAcceptanceAndTemplateDrift(t *testing.T) {
	for _, kind := range []string{"bug", "requirement"} {
		t.Run(kind, func(t *testing.T) {
			s, h, task, _ := workLoopFixture(t, kind)
			task.Stage = "acceptance"
			task.State = "awaiting_acceptance"
			task.Grant.Spec.ExpiresAt = 1
			if e := s.saveWorkLoop(task); e != nil {
				t.Fatal(e)
			}
			if _, e := s.db.Exec(`UPDATE work_items SET status='closed' WHERE id=?`, task.Grant.Spec.WorkItemID); e != nil {
				t.Fatal(e)
			}
			w := loopRequest(h, "POST", "/api/work-loops/"+task.ID+"/advance", "alice", map[string]any{"revision": task.Revision})
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			got, _ := s.workLoop(task.ID)
			if got.State != "completed" {
				t.Fatal(got.State, got.Message)
			}
		})
	}
	s, _, task, _ := workLoopFixture(t, "bug")
	tpl := s.DeliveryTemplate(task.Grant.TemplateID)
	tpl.Preparation[0].Instructions = "changed authorization"
	if e := s.SaveDeliveryTemplate(tpl); e != nil {
		t.Fatal(e)
	}
	if e := s.validateWorkLoop(context.Background(), task, "alice"); e == nil {
		t.Fatal("workflow drift ignored")
	}
}
func TestWorkLoopSourcePrewriteFailureDoesNotFreeze(t *testing.T) {
	s, w, writes, mu := researchFixture(t)
	task := &WorkLoop{ID: strings.Repeat("d", 32), Owner: "platform-alice", Grant: WorkLoopGrant{Spec: WorkLoopSpec{WorkItemID: w.ID, CompleteScope: true, Resolution: "fixed"}}, Iterations: []WorkLoopIteration{{Number: 1}}, Stage: "writeback"}
	if e := s.saveWorkLoop(task); e != nil {
		t.Fatal(e)
	}
	if e := s.writeWorkLoopSource(context.Background(), task); e == nil {
		t.Fatal("missing required source build accepted")
	}
	got, _ := s.workLoop(task.ID)
	if got.Intent.State != "rejected" || got.current().SourceOperation != "" {
		t.Fatal("prewrite rejection froze task", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if *writes != 0 {
		t.Fatal("prewrite failure sent remote write")
	}
}
func TestWorkLoopPlanCannotOmitSelectedChanges(t *testing.T) {
	_, _, task, _ := workLoopFixture(t, "bug")
	p := &DeliveryPlan{Scope: &DeliveryScope{WorkItems: []WorkItem{{ID: task.Grant.Spec.WorkItemID}}}}
	p.Environment.Name = task.Grant.Spec.Environment
	if e := loopPlanScope(task, p); e == nil {
		t.Fatal("missing selected change accepted")
	}
}

func TestWorkLoopNewChangeBlocksWholeItemWriteback(t *testing.T) {
	s, _, task, _ := workLoopFixture(t, "requirement")
	c := &Change{Kind: "code", Title: "Added during delivery", Service: "orders", Reference: "feature/next", Owner: "alice"}
	if e := s.CreateChange(c); e != nil {
		t.Fatal(e)
	}
	if e := s.SetChangeWorkItems(c.ID, []int64{task.Grant.Spec.WorkItemID}); e != nil {
		t.Fatal(e)
	}
	task.Stage = "writeback"
	e := s.advanceWorkLoop(http.NewServeMux(), httptest.NewRequest("POST", "/", nil), task, nil, nil)
	if e == nil || !strings.Contains(e.Error(), fmt.Sprintf("#%d", c.ID)) {
		t.Fatal("new scope was not checked before writeback", e)
	}
	if s.GetWorkItem(task.Grant.Spec.WorkItemID).Status == WorkItemResolved {
		t.Fatal("expanded work item marked resolved")
	}
}
