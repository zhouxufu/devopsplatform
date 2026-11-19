package deploy

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestManualWorkItemLifecycleAndStatusGuards(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w := &WorkItem{Title: "本地 Bug", Kind: "bug", Owner: "alice", AssignedTo: "bob", ResearchIssueFields: ResearchIssueFields{Priority: "2", Severity: "1"}}
	if err = s.CreateWorkItem(w); err != nil {
		t.Fatal(err)
	}
	saved := s.GetWorkItem(w.ID)
	if saved.Priority != "2" || saved.Severity != "1" || saved.OpenedBy != "alice" {
		t.Fatal(saved)
	}
	if err = s.SetManualWorkItemStatus(w.ID, saved.UpdatedAt, saved.Status, WorkItemActive, "eve", false); err == nil {
		t.Fatal("unrelated member changed status")
	}
	if err = s.SetManualWorkItemStatus(w.ID, saved.UpdatedAt, saved.Status, "invalid", "alice", false); err == nil {
		t.Fatal("invalid state accepted")
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- s.SetManualWorkItemStatus(w.ID, saved.UpdatedAt, saved.Status, WorkItemActive, "bob", false)
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("same snapshot accepted %d times", success)
	}
	// A stale general edit must never restore the old status.
	saved.Title = "已编辑"
	saved.Priority = "3"
	if err = s.UpdateWorkItemAs(saved, "alice"); err != nil {
		t.Fatal(err)
	}
	current := s.GetWorkItem(w.ID)
	if current.Status != WorkItemActive || current.Priority != "3" {
		t.Fatal(current)
	}
	for _, status := range []string{WorkItemResolved, WorkItemClosed, WorkItemOpen} {
		if err = s.SetManualWorkItemStatus(w.ID, current.UpdatedAt, current.Status, status, "bob", false); err != nil {
			t.Fatal(err)
		}
		current = s.GetWorkItem(w.ID)
	}
	page, err := s.WorkItemsPage(WorkItemFilter{Source: "manual", Priority: "3", Severity: "1", Query: "已编辑"})
	if err != nil || page.Total != 1 {
		t.Fatal(page, err)
	}
	rows, err := s.ResearchActivities(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, r := range rows {
		counts[r.Kind]++
	}
	if counts["created"] != 1 || counts["work-item-status"] != 4 || counts["work-item-updated"] != 1 {
		t.Fatal(counts)
	}
	for _, fields := range []ResearchIssueFields{{Priority: "5"}, {Severity: "bad"}} {
		bad := &WorkItem{Title: "无效级别", ResearchIssueFields: fields}
		if s.CreateWorkItem(bad) == nil {
			t.Fatal("invalid fields saved")
		}
	}
}

func TestManualWorkItemStatusAPI(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w := &WorkItem{Title: "API Bug", Kind: "bug", Owner: "alice", AssignedTo: "bob"}
	if err = s.CreateWorkItem(w); err != nil {
		t.Fatal(err)
	}
	hub := NewLogHub()
	h := Routes(s, NewBuilder(s, s.dataDir, hub), hub, "", NewStepLib(s.dataDir+"/steps"))
	send := func(actor, role, method, path, body string) int {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("X-User", actor)
		r.Header.Set("X-Role", role)
		r.Header.Set("X-Reviewer-Directory", `["alice","bob"]`)
		out := httptest.NewRecorder()
		h.ServeHTTP(out, r)
		return out.Code
	}
	path := fmt.Sprintf("/api/work-items/%d/status", w.ID)
	body := fmt.Sprintf(`{"status":"active","expected_status":"open","expected_updated_at":%d}`, w.UpdatedAt)
	if code := send("eve", "member", "PATCH", path, body); code != 403 {
		t.Fatal(code)
	}
	if code := send("bob", "member", "PATCH", path, body); code != 200 {
		t.Fatal(code)
	}
	if code := send("bob", "member", "PATCH", path, body); code != 409 {
		t.Fatal(code)
	}
	edit := s.GetWorkItem(w.ID)
	edit.Status = "closed"
	edit.Title = "编辑不能改状态"
	raw, _ := json.Marshal(edit)
	if code := send("bob", "member", "PUT", fmt.Sprintf("/api/work-items/%d", w.ID), string(raw)); code != 403 {
		t.Fatal(code)
	}
	if code := send("alice", "member", "PUT", fmt.Sprintf("/api/work-items/%d", w.ID), string(raw)); code != 200 {
		t.Fatal(code)
	}
	if s.GetWorkItem(w.ID).Status != WorkItemActive {
		t.Fatal("PUT bypassed transition endpoint")
	}
	for _, status := range []string{WorkItemActive, WorkItemResolved} {
		current := s.GetWorkItem(w.ID)
		if status != current.Status {
			if err = s.SetManualWorkItemStatus(w.ID, current.UpdatedAt, current.Status, status, "admin", true); err != nil {
				t.Fatal(err)
			}
		}
		page, e := s.WorkItemsPage(WorkItemFilter{Status: status})
		if e != nil || page.Total != 1 {
			t.Fatal(page, e)
		}
		candidates, e := s.WorkCandidates(WorkCandidateFilter{Source: "manual", WorkItemFilter: WorkItemFilter{Status: status}})
		if e != nil || candidates.Total != 1 {
			t.Fatal(candidates, e)
		}
	}
	external := &WorkItem{Title: "来源 Bug", Source: "zentao:qa", ExternalID: "7", Kind: "bug", Owner: "alice"}
	if err = s.CreateWorkItem(external); err != nil {
		t.Fatal(err)
	}
	if code := send("admin", "admin", "PATCH", fmt.Sprintf("/api/work-items/%d/status", external.ID), body); code != 403 {
		t.Fatal(code)
	}
}
