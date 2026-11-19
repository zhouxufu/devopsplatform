package deploy

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkCandidateClosedDateIsIndependentOfPlatformUpdate(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i, closed := range []string{"2026-10-01 00:00:00", "2026-10-15 23:59:59", "2026-10-16 00:00:00", "", "0000-00-00 00:00:00", "invalid"} {
		w := WorkItem{Source: "zentao:qa", ExternalID: string(rune('1' + i)), Title: "fixture", Kind: "bug", Status: "closed"}
		if err = s.CreateWorkItem(&w); err != nil {
			t.Fatal(err)
		}
		s.db.Exec(`UPDATE work_items SET updated_at=100 WHERE id=?`, w.ID)
		tx, _ := s.db.Begin()
		if err = saveIssueFields(tx, w.ID, issueFields(nil, "", "", "", closed)); err != nil {
			t.Fatal(err)
		}
		tx.Commit()
	}
	for _, tc := range []struct {
		start, end string
		want       int
	}{{"2026-10-01", "2026-10-15", 2}, {"", "2026-10-15", 2}, {"2026-10-16", "", 1}, {"", "", 6}} {
		page, err := s.WorkCandidates(WorkCandidateFilter{WorkItemFilter: WorkItemFilter{TimeField: "closed", SourceStart: tc.start, SourceEnd: tc.end}})
		if err != nil || page.Total != tc.want {
			t.Fatal(tc, page.Total, err)
		}
		for _, item := range page.Items {
			if (tc.start != "" || tc.end != "") && item.SourceClosedAt == "" {
				t.Fatal("unknown closure replaced with update", item)
			}
		}
	}
	page, err := s.WorkCandidates(WorkCandidateFilter{WorkItemFilter: WorkItemFilter{TimeField: "updated"}, Start: 101})
	if err != nil || page.Total != 0 {
		t.Fatal("platform update filter changed", page, err)
	}
	for _, f := range []WorkCandidateFilter{{WorkItemFilter: WorkItemFilter{TimeField: "unsafe"}}, {WorkItemFilter: WorkItemFilter{TimeField: "closed", SourceStart: "bad"}}, {WorkItemFilter: WorkItemFilter{TimeField: "closed", SourceStart: "2026-10-15", SourceEnd: "2026-10-01"}}, {WorkItemFilter: WorkItemFilter{TimeField: "closed"}, Start: 100}} {
		if _, err = s.WorkCandidates(f); err == nil {
			t.Fatal("invalid time filter accepted", f)
		}
	}
	hub := NewLogHub()
	h := Routes(s, NewBuilder(s, s.dataDir, hub), hub, "", NewStepLib("../../steps"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/work-items/candidates?time_field=closed&source_start=2026-10-01&source_end=2026-10-15", nil))
	var response WorkCandidatePage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Total != 2 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestResearchLiveClosedDateQueriesAndAttachSnapshot(t *testing.T) {
	s, state := researchLiveFixture(t, 5)
	state.mu.Lock()
	for i, row := range state.rows {
		row["closedDate"] = []string{"2026-10-01 00:00:00", "2026-10-15 23:59:59", "2026-10-16 00:00:00", "0000-00-00 00:00:00", ""}[i]
	}
	state.mu.Unlock()
	f := researchLiveFilter{Connection: "issues", WorkItemFilter: WorkItemFilter{TimeField: "closed", SourceStart: "2026-10-01", SourceEnd: "2026-10-15"}}
	page, err := s.ResearchLivePage(context.Background(), "member", f)
	if err != nil || page.Total != 2 || len(page.Items) != 2 {
		t.Fatal(page, err)
	}
	for _, item := range page.Items {
		if item.SourceClosedAt == "" {
			t.Fatal("closing date not exposed", item)
		}
	}
	f.SourceStart = ""
	page, err = s.ResearchLivePage(context.Background(), "member", f)
	if err != nil || page.Total != 2 {
		t.Fatal("zero closure matched upper bound", page, err)
	}
	item, err := s.AttachResearchBug(context.Background(), "member", "issues", "1")
	if err != nil || item.SourceClosedAt != "2026-10-01 00:00:00" {
		t.Fatal("attached snapshot lost closure", item, err)
	}
	local, err := s.WorkCandidates(WorkCandidateFilter{WorkItemFilter: WorkItemFilter{TimeField: "closed", SourceStart: "2026-10-01", SourceEnd: "2026-10-01"}})
	if err != nil || local.Total != 1 {
		t.Fatal(local, err)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.businessWrites != 0 {
		t.Fatal("source mutated", state.businessWrites)
	}
	seen := false
	for _, group := range state.predicates {
		for _, p := range group {
			if p.Field == "closedDate" {
				seen = true
			}
			if p.Field == "lastEditedDate" {
				t.Fatal("queried edit date instead of closure")
			}
		}
	}
	if !seen {
		t.Fatal("source closure filter absent")
	}
}

func TestNormalizedSourceClosedDate(t *testing.T) {
	for _, raw := range []string{"", "0000-00-00 00:00:00", "2026-13-01", "garbage", "0"} {
		if got := normalizedSourceDate(raw); got != "" {
			t.Fatal(raw, got)
		}
	}
	for _, raw := range []string{"2026-10-26 08:00:00", "2026-10-26T08:00:00+08:00"} {
		if got := normalizedSourceDate(raw); got != "2026-10-26 08:00:00" {
			t.Fatal(raw, got)
		}
	}
	if predicates, err := livePredicates(WorkItemFilter{TimeField: "closed", SourceEnd: "2026-10-26"}); err != nil || len(predicates) != 2 || predicates[0].Field != "closedDate" || !strings.HasPrefix(predicates[1].Value, "2026-10-26") {
		t.Fatal(predicates, err)
	}
}
