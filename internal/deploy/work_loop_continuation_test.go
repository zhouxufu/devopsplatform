package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWorkLoopReauthorizationPreservesFrozenHistory(t *testing.T) {
	s, h, task, _ := workLoopFixture(t, "requirement")
	task.current().ChangeIDs = nil
	if e := s.saveWorkLoop(task); e != nil {
		t.Fatal(e)
	}
	task, _ = s.workLoop(task.ID) // exercise persisted map fields, not only in-memory structs
	original := task.Grant
	spec := original.Spec
	spec.ExpiresAt = time.Now().Add(2 * time.Hour).Unix()
	path := "/api/work-loops/" + task.ID + "/authorization"
	req := workLoopAuthorizationRequest{Revision: task.Revision, Spec: spec}
	foreign := loopRequest(h, "POST", path+"/preview", "bob", req)
	if foreign.Code != 403 {
		t.Fatal("foreign renewal", foreign.Code)
	}
	p, e := s.previewWorkLoopAuthorization(context.Background(), task, req)
	if e != nil {
		t.Fatal(e)
	}
	req.ExpectedDigest = p.Digest
	w := loopRequest(h, "POST", path, "alice", req)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	renewed, _ := s.workLoop(task.ID)
	if len(renewed.Authorizations) != 2 || renewed.Authorizations[0].Digest != original.Digest || len(renewed.Iterations) != 1 {
		t.Fatal("original authorization lost", renewed)
	}
	if loopRequest(h, "POST", path, "alice", req).Code != 409 {
		t.Fatal("stale preview accepted")
	}
	renewed.setState("running", "preparation", "frozen")
	if e = s.saveWorkLoop(renewed); e != nil {
		t.Fatal(e)
	}
	spec.Acceptance = "新增验收范围"
	req = workLoopAuthorizationRequest{Revision: renewed.Revision, Spec: spec}
	if _, e = s.previewWorkLoopAuthorization(context.Background(), renewed, req); e == nil {
		t.Fatal("changed frozen scope accepted")
	}
	renewed.setState("awaiting_acceptance", "acceptance", "original regression passed")
	if e = s.saveWorkLoop(renewed); e != nil {
		t.Fatal(e)
	}
	req.Revision = renewed.Revision
	p, e = s.previewWorkLoopAuthorization(context.Background(), renewed, req)
	if e != nil || !p.NewIteration {
		t.Fatal(p, e)
	}
	oldIteration := renewed.current().GrantDigest
	if e = s.applyWorkLoopAuthorization(renewed, p); e != nil {
		t.Fatal(e)
	}
	if len(renewed.Iterations) != 2 || renewed.Iterations[0].GrantDigest != oldIteration || renewed.current().GrantDigest != p.Grant.Digest {
		t.Fatal("iteration authority rewritten")
	}
	renewed.Intent = &WorkLoopIntent{State: "dispatching"}
	req.Revision = renewed.Revision
	if _, e = s.previewWorkLoopAuthorization(context.Background(), renewed, req); e == nil {
		t.Fatal("uncertain dispatch bypassed")
	}
}
func TestWorkLoopHomeAttentionStableEventAndIsolation(t *testing.T) {
	s, h, task, _ := workLoopFixture(t, "bug")
	task.current().ChangeIDs = nil
	task.setState("blocked", "development", "需要修复代码")
	if e := s.saveWorkLoop(task); e != nil {
		t.Fatal(e)
	}
	target, e := s.attentionTarget("alice", "work-loop:"+task.ID)
	if e != nil || !target.CanIgnore {
		t.Fatal(target, e)
	}
	if _, e = s.attentionTarget("bob", target.Task.ID); e == nil {
		t.Fatal("foreign attention")
	}
	state, e := s.setResearchAttention("alice", researchAttentionItem{ID: target.Task.ID, Event: target.Task.AttentionEvent}, "ignore", "此次开发暂不继续", strings.Repeat("c", 32))
	if e != nil {
		t.Fatal(e)
	}
	if e = s.saveWorkLoop(task); e != nil {
		t.Fatal(e)
	} // polling/CAS change alone
	again, e := s.attentionTarget("alice", target.Task.ID)
	if e != nil || again.Task.AttentionEvent != target.Task.AttentionEvent {
		t.Fatal("refresh resurrected ignored task")
	}
	rows, e := s.attentionDeliveries("alice", "active")
	if e != nil || len(rows) != 0 {
		t.Fatal(rows, e)
	}
	if _, e = s.setResearchAttention("alice", researchAttentionItem{ID: target.Task.ID, Event: target.Task.AttentionEvent, Revision: state.Revision}, "restore", "恢复此任务继续处理", strings.Repeat("d", 32)); e != nil {
		t.Fatal(e)
	}
	rows, e = s.attentionDeliveries("alice", "active")
	if e != nil || len(rows) != 1 || rows[0].WorkLoopID != task.ID {
		t.Fatal(rows, e)
	}
	task.setState("running", "preparation", "新轮次推进")
	task.setState("blocked", "development", "需要修复代码")
	_ = s.saveWorkLoop(task)
	again, _ = s.attentionTarget("alice", target.Task.ID)
	if again.Task.AttentionEvent == target.Task.AttentionEvent {
		t.Fatal("new event hidden")
	}
	task.setState("completed", "acceptance", "验收完成")
	_ = s.saveWorkLoop(task)
	rows, e = s.attentionDeliveries("alice", "active")
	if e != nil || len(rows) != 0 {
		t.Fatal("terminal task still active", rows, e)
	}
	w := loopRequest(h, "GET", "/api/research/tasks?view=done", "alice", nil)
	var page struct {
		Items []ResearchTask `json:"items"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &page)
	found := false
	for _, row := range page.Items {
		if row.WorkLoopID == task.ID {
			found = true
		}
	}
	if w.Code != 200 || !found {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestWorkLoopAuthorizationBindsPreviewAuthority(t *testing.T) {
	_, h, task, _ := workLoopFixture(t, "bug")
	path := "/api/work-loops/" + task.ID + "/authorization"
	req := workLoopAuthorizationRequest{Revision: task.Revision, Spec: task.Grant.Spec}
	call := func(path, authority, key string) *httptest.ResponseRecorder {
		b, _ := json.Marshal(req)
		r := httptest.NewRequest("POST", path, bytes.NewReader(b))
		r.Header.Set("X-User", "alice")
		r.Header.Set("X-Work-Loop-Authority", authority)
		r.Header.Set("X-Work-Loop-Key-ID", key)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := call(path+"/preview", "mcp", "key-a")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var preview workLoopAuthorizationPreview
	if e := json.Unmarshal(w.Body.Bytes(), &preview); e != nil {
		t.Fatal(e)
	}
	req.ExpectedDigest = preview.Digest
	for _, provenance := range [][2]string{{"page", ""}, {"mcp", "key-b"}} {
		w = call(path, provenance[0], provenance[1])
		if w.Code != 409 {
			t.Fatal("changed authority accepted", w.Code)
		}
	}
	w = call(path, "mcp", "key-a")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestWorkLoopResolvedBugNextIterationOnlyComments(t *testing.T) {
	s, w, writes, mu := researchFixture(t)
	task := &WorkLoop{ID: strings.Repeat("e", 32), Owner: "platform-alice", Grant: WorkLoopGrant{Spec: WorkLoopSpec{WorkItemID: w.ID, Environment: "test", CompleteScope: true, Resolution: "fixed", ResolvedBuild: "trunk"}}, Iterations: []WorkLoopIteration{{Number: 1, DeliveryID: 42, Summary: "first verified"}}, Stage: "writeback"}
	if e := s.saveWorkLoop(task); e != nil {
		t.Fatal(e)
	}
	if e := s.writeWorkLoopSource(context.Background(), task); e != nil {
		t.Fatal(e)
	}
	task.Iterations = append(task.Iterations, WorkLoopIteration{Number: 2, DeliveryID: 43, Summary: "followup verified"})
	task.Intent = nil
	if e := s.saveWorkLoop(task); e != nil {
		t.Fatal(e)
	}
	if e := s.writeWorkLoopSource(context.Background(), task); e != nil {
		t.Fatal(e)
	}
	op, e := s.ResearchOperation(task.current().SourceOperation, task.Owner)
	if e != nil || op.State != "success" || op.Request.Action != "comment" {
		t.Fatal(op, e)
	}
	if e := s.writeWorkLoopSource(context.Background(), task); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	count := *writes
	mu.Unlock()
	if count != 2 {
		t.Fatal("followup replayed", count)
	}
}
