package deploy

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestResearchAllSourcesPageRechecksLiveFieldsWithoutImport(t *testing.T) {
	s, state := researchLiveFixture(t, 47)
	original := s.GetConnection("issues")
	second := *original
	second.Name = "another"
	second.Password = "sync-secret"
	if err := s.UpsertConnection(second); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	cursor := ""
	f := researchLiveFilter{WorkItemFilter: WorkItemFilter{Page: 1, PageSize: 20, Kind: "bug"}}
	for page := 1; page <= 5; page++ {
		f.Page, f.Cursor = page, cursor
		out, err := s.ResearchLivePage(context.Background(), "alice", f)
		if err != nil {
			t.Fatal(err)
		}
		if out.TotalExact || out.QueryMode != "live_sources" {
			t.Fatal("federation claims an exact count", out)
		}
		for _, w := range out.Items {
			key := w.Source + ":" + w.ExternalID
			if seen[key] {
				t.Fatal("duplicate across pages", key)
			}
			seen[key] = true
		}
		cursor = out.NextCursor
		if page < 5 && !out.HasMore {
			t.Fatal("premature end", page)
		}
	}
	if len(seen) != 94 {
		t.Fatal("source records omitted", len(seen))
	}
	if len(s.WorkItems("", "", 100)) != 0 {
		t.Fatal("read imported source records")
	}
	state.mu.Lock()
	state.rows[0]["title"] = "updated immediately"
	state.mu.Unlock()
	out, err := s.ResearchLivePage(context.Background(), "alice", researchLiveFilter{WorkItemFilter: WorkItemFilter{Page: 1, PageSize: 20, Query: "1"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 2 || out.Items[0].Title != "updated immediately" {
		t.Fatal("stored fields substituted for live source", out.Items)
	}
	state.mu.Lock()
	writes := state.businessWrites
	state.fail = true
	state.mu.Unlock()
	if writes != 0 {
		t.Fatal("upstream business write during query")
	}
	if _, err = s.ResearchLivePage(context.Background(), "alice", f); err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatal("source error hidden behind old data", err)
	}
}
func TestResearchAllSourcesCursorKeepsIdentityAndQueryScope(t *testing.T) {
	s, _ := researchLiveFixture(t, 45)
	f := researchLiveFilter{WorkItemFilter: WorkItemFilter{Page: 1, PageSize: 20}}
	first, err := s.ResearchLivePage(context.Background(), "alice", f)
	if err != nil {
		t.Fatal(err)
	}
	f.Page = 2
	f.Cursor = first.NextCursor
	for _, actor := range []string{"bob", "alice"} {
		q := f
		if actor == "alice" {
			q.Query = "different"
		}
		if _, err := s.ResearchLivePage(context.Background(), actor, q); err == nil {
			t.Fatal("cursor crossed actor or filter", actor)
		}
	}
	f.Cursor = ""
	if _, err := s.ResearchLivePage(context.Background(), "alice", f); err == nil {
		t.Fatal("offset jump silently accepted")
	}
}
func TestResearchAllSourcesKeepsPlatformCreatedRequirements(t *testing.T) {
	s, _ := researchLiveFixture(t, 3)
	w := &WorkItem{Source: "manual", Kind: "requirement", Title: "platform requirement", Owner: "alice"}
	err := s.CreateWorkItem(w)
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.ResearchLivePage(context.Background(), "alice", researchLiveFilter{WorkItemFilter: WorkItemFilter{Kind: "requirement", Page: 1, PageSize: 20}})
	if err != nil || len(out.Items) != 1 || out.Items[0].ID != w.ID {
		t.Fatal(fmt.Sprint(out, err))
	}
}
