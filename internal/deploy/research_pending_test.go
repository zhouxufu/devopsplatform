package deploy

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"testing"
)

func registerPendingFixture(t *testing.T, s *Store, count int) []*WorkItem {
	t.Helper()
	out := []*WorkItem{}
	for i := 1; i <= count; i++ {
		item, err := s.AttachResearchBug(context.Background(), "alice", "issues", strconv.Itoa(i))
		if err != nil {
			t.Fatal(err)
		}
		yes := true
		if err = s.SetResearchMetadata(item.ID, ResearchMetadata{ReleaseRequired: &yes}, "alice"); err != nil {
			t.Fatal(err)
		}
		out = append(out, item)
	}
	return out
}

func TestResearchPendingBatchEmptyAvoidsSourceReads(t *testing.T) {
	s, state := researchLiveFixture(t, 12037)
	f := researchLiveFilter{Connection: "issues", Delivery: "pending_release", WorkItemFilter: WorkItemFilter{PageSize: 20}}
	for i := 0; i < 2; i++ {
		out, err := s.ResearchLivePage(context.Background(), "member", f)
		if err != nil || out.Total != 0 || !out.TotalExact || out.HasMore || len(out.Items) != 0 {
			t.Fatal("empty queue", out, err)
		}
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.routes["bug/browse"] != 0 || state.routes["api/getModel"] != 0 || state.routes["search/buildForm"] != 0 || state.businessWrites != 0 {
		t.Fatal("empty queue scanned source", state.routes)
	}
}

func TestResearchPendingBatchFreshFiltersPermissionsAndPagination(t *testing.T) {
	s, state := researchLiveFixture(t, 130)
	registerPendingFixture(t, s, 65)
	if err := s.AuthorizeResearch(context.Background(), "issues", "platform-alice", ResearchCredential{Account: "alice", Password: "alice-secret"}); err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	// All these fields differ from the registered snapshot; source data wins.
	for _, row := range state.rows {
		row["resolvedBy"] = "alice"
	}
	state.rows[0]["status"] = "active"
	state.rows[1]["product"] = "2"   // platform allows it, current source user does not.
	state.rows[2]["execution"] = "8" // inaccessible execution.
	state.rows[3]["product"] = "99"  // source allows it, platform does not.
	state.rows[4]["deleted"] = "1"
	state.rows[5]["title"] = "fresh pending title"
	state.rows[6]["resolvedBy"] = "bob"
	state.sourceProducts = "1,99"
	state.mu.Unlock()
	f := researchLiveFilter{Connection: "issues", Mine: true, Delivery: "pending_release", WorkItemFilter: WorkItemFilter{Person: "bob", PersonRole: "resolved_by", Status: "closed", PageSize: 20}}
	seen := map[string]bool{}
	for page := 1; page <= 3; page++ {
		f.Page = page
		out, err := s.ResearchLivePage(context.Background(), "platform-alice", f)
		if err != nil {
			t.Fatal(err)
		}
		if out.Total != 59 || !out.TotalExact || len(out.Items) != min(20, 59-(page-1)*20) {
			t.Fatal("wrong visible pending total", out)
		}
		for _, item := range out.Items {
			if seen[item.ExternalID] || item.ResolvedBy != "alice" || item.ExternalStatus != "closed" || item.Description != "" {
				t.Fatal("fresh scoped row", item)
			}
			seen[item.ExternalID] = true
			if item.ExternalID == "6" && item.Title != "fresh pending title" {
				t.Fatal("stale title")
			}
		}
		f.Cursor = out.NextCursor
	}
	if len(seen) != 59 {
		t.Fatal("pagination lost pending Bugs", len(seen))
	}
	state.mu.Lock()
	if state.routes["api/getModel"] != 3 || state.businessWrites != 0 {
		t.Fatal("not one model batch per query", state.routes)
	}
	state.sourceProducts = "" // A permission revocation must apply to the next read.
	state.mu.Unlock()
	f.Page, f.Cursor = 1, ""
	out, err := s.ResearchLivePage(context.Background(), "platform-alice", f)
	if err != nil || len(out.Items) != 0 || out.Total != 0 {
		t.Fatal("source permission cached", out, err)
	}
	if _, err = s.ResearchLivePage(context.Background(), "platform-alice", researchLiveFilter{Connection: "issues", Delivery: "pending_release", WorkItemFilter: WorkItemFilter{Page: 2}}); err == nil {
		t.Fatal("missing cursor accepted")
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.routes["bug/browse"] != 0 || state.routes["search/buildQuery"] != 0 {
		t.Fatal("pending still browsed native pages", state.routes)
	}
}

func TestResearchPendingBatchBoundsAndFailClosed(t *testing.T) {
	for _, mode := range []string{"extra", "duplicate", "source-failure", "no-browse", "no-qa"} {
		t.Run(mode, func(t *testing.T) {
			s, state := researchLiveFixture(t, 10)
			registerPendingFixture(t, s, 4)
			state.mu.Lock()
			switch mode {
			case "extra":
				state.modelExtra = true
			case "duplicate":
				state.modelDuplicate = true
			case "source-failure":
				state.fail = true
			case "no-browse":
				state.denyBrowse = true
			case "no-qa":
				state.denyQA = true
			}
			state.mu.Unlock()
			out, err := s.ResearchLivePage(context.Background(), "member", researchLiveFilter{Connection: "issues", Delivery: "pending_release"})
			if err == nil || len(out.Items) != 0 {
				t.Fatal("unsafe fallback/result", out, err)
			}
		})
	}
}

func TestResearchPendingBatchDisabledUsesFreshNativeFallback(t *testing.T) {
	s, state := researchLiveFixture(t, 10)
	registerPendingFixture(t, s, 4)
	state.mu.Lock()
	state.modelDisabled = true
	state.rows[0]["title"] = "source current title"
	state.mu.Unlock()
	out, err := s.ResearchLivePage(context.Background(), "member", researchLiveFilter{Connection: "issues", Delivery: "pending_release"})
	if err != nil || len(out.Items) != 4 {
		t.Fatal("disabled feature broke native query", out, err)
	}
	for _, item := range out.Items {
		if item.ExternalID == "1" && item.Title != "source current title" {
			t.Fatal("fallback used stored snapshot")
		}
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.routes["bug/browse"] == 0 || state.routes["api/getModel"] != 1 || state.businessWrites != 0 {
		t.Fatal("bad feature fallback", state.routes)
	}
}

func TestResearchPendingBatchChunkingAndChangedIntentInvalidatesCursor(t *testing.T) {
	s, state := researchLiveFixture(t, 130)
	items := registerPendingFixture(t, s, 105)
	f := researchLiveFilter{Connection: "issues", Delivery: "pending_release", WorkItemFilter: WorkItemFilter{PageSize: 20}}
	out, err := s.ResearchLivePage(context.Background(), "member", f)
	if err != nil || out.Total != 105 || !out.HasMore {
		t.Fatal("chunks lost candidates", out, err)
	}
	state.mu.Lock()
	calls := state.routes["api/getModel"]
	state.mu.Unlock()
	if calls != 2 {
		t.Fatal("unbounded batch", calls)
	}
	no := false
	if err = s.SetResearchMetadata(items[0].ID, ResearchMetadata{ReleaseRequired: &no, NoReleaseReason: "no release", Revision: 1}, "alice"); err != nil {
		t.Fatal(err)
	}
	f.Page, f.Cursor = 2, out.NextCursor
	if _, err = s.ResearchLivePage(context.Background(), "member", f); err == nil {
		t.Fatal("changed pending range cursor accepted")
	}
}

func TestBugListModelRouteWhitelist(t *testing.T) {
	params := "bugIDList=1,2,fields=" + bugListFields
	for _, pathInfo := range []bool{false, true} {
		z := &zentaoLegacy{root: "http://source/zentao", pathInfo: pathInfo}
		args := url.Values{"moduleName": {"bug"}, "methodName": {"getByList"}, "params": {params}}
		if _, err := z.requestURL("api", "getModel", args); err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"bugIDList=,fields=" + bugListFields, "bugIDList=0,fields=" + bugListFields, "bugIDList=1,1,fields=" + bugListFields, "bugIDList=1,fields=*", "bugIDList=1 OR 1=1,fields=" + bugListFields} {
			args.Set("params", bad)
			if _, err := z.requestURL("api", "getModel", args); err == nil {
				t.Fatal("unsafe args allowed", bad)
			}
		}
		args.Set("params", params)
		args.Set("methodName", "delete")
		if _, err := z.requestURL("api", "getModel", args); err == nil {
			t.Fatal("write model allowed")
		}
		ids := ""
		for i := 1; i <= 101; i++ {
			if i > 1 {
				ids += ","
			}
			ids += fmt.Sprint(i)
		}
		args.Set("methodName", "getByList")
		args.Set("params", "bugIDList="+ids+",fields="+bugListFields)
		if _, err := z.requestURL("api", "getModel", args); err == nil {
			t.Fatal("oversized batch allowed")
		}
	}
}
