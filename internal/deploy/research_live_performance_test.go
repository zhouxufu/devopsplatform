package deploy

import (
	"context"
	"testing"
	"time"
)

func TestResearchLiveSearchReuseAlwaysReadsFreshRows(t *testing.T) {
	s, state := researchLiveFixture(t, 80)
	ctx := context.Background()
	// Optional enum enrichment reads a native Bug form and deliberately resets
	// native search state. Prime that independent metadata lookup before measuring
	// search reuse; dictionary caching/isolation has its own regression tests.
	dictionaryClient, _, _, dictionaryErr := s.liveReadClient(ctx, "issues", "member", false)
	if dictionaryErr != nil {
		t.Fatal(dictionaryErr)
	}
	dictionaryClient.researchResolutionLabels(ctx, "1", "1", nil)
	counts := func() map[string]int {
		state.mu.Lock()
		defer state.mu.Unlock()
		out := map[string]int{}
		for k, v := range state.routes {
			out[k] = v
		}
		return out
	}
	f := researchLiveFilter{Connection: "issues", WorkItemFilter: WorkItemFilter{PageSize: 20}}
	first, err := s.ResearchLivePage(ctx, "member", f)
	if err != nil {
		t.Fatal(err)
	}
	cold := counts()
	if cold["bug/browse"] != 2 || cold["search/buildForm"] != 1 || cold["search/buildQuery"] != 1 {
		t.Fatal("cold setup incorrect", cold)
	}
	f.Page = 2
	f.Cursor = first.NextCursor
	if _, err = s.ResearchLivePage(ctx, "member", f); err != nil {
		t.Fatal(err)
	}
	warm := counts()
	if warm["bug/browse"] != cold["bug/browse"]+1 || warm["search/buildForm"] != cold["search/buildForm"] || warm["search/buildQuery"] != cold["search/buildQuery"] {
		t.Fatal("page rebuilt search", warm)
	}
	state.mu.Lock()
	state.rows[79]["title"] = "实时修改标题"
	state.mu.Unlock()
	f.Page = 1
	f.Cursor = ""
	fresh, err := s.ResearchLivePage(ctx, "member", f)
	if err != nil || fresh.Items[0].Title != "实时修改标题" {
		t.Fatal("cached Bug rows", err)
	}
	after := counts()
	if after["bug/browse"] != warm["bug/browse"]+1 {
		t.Fatal("refresh skipped live read")
	}
	f.Person = "alice"
	f.PersonRole = "resolved_by"
	out, err := s.ResearchLivePage(ctx, "member", f)
	if err != nil {
		t.Fatal(err)
	}
	changed := counts()
	if changed["search/buildForm"] != after["search/buildForm"] || changed["search/buildQuery"] != after["search/buildQuery"]+1 || changed["bug/browse"] != after["bug/browse"]+1 {
		t.Fatal("changed filters repeated initialization", changed)
	}
	for _, row := range out.Items {
		if row.ResolvedBy != "alice" {
			t.Fatal("filter lost")
		}
	}
	f.Order = "priority_asc"
	if _, err = s.ResearchLivePage(ctx, "member", f); err != nil {
		t.Fatal(err)
	}
	sorted := counts()
	if sorted["search/buildQuery"] != changed["search/buildQuery"] || sorted["bug/browse"] != changed["bug/browse"]+1 {
		t.Fatal("sort rebuilt WHERE", sorted)
	}
	f.Person = "bob"
	out, err = s.ResearchLivePage(ctx, "member", f)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range out.Items {
		if row.ResolvedBy != "bob" {
			t.Fatal("previous window predicate leaked")
		}
	}
	last := counts()
	if last["search/buildQuery"] != sorted["search/buildQuery"]+1 {
		t.Fatal("changed identity predicate not replaced")
	}
	state.mu.Lock()
	state.fail = true
	state.mu.Unlock()
	if _, err = s.ResearchLivePage(ctx, "member", f); err == nil {
		t.Fatal("returned cached data on failure")
	}
	failed := counts()
	state.mu.Lock()
	state.fail = false
	state.mu.Unlock()
	if _, err = s.ResearchLivePage(ctx, "member", f); err != nil {
		t.Fatal(err)
	}
	recovered := counts()
	if recovered["bug/browse"] != failed["bug/browse"]+2 || recovered["search/buildForm"] != failed["search/buildForm"]+1 || recovered["search/buildQuery"] != failed["search/buildQuery"]+1 {
		t.Fatal("failed search state reused", recovered)
	}
	z, _, _, err := s.liveReadClient(ctx, "issues", "member", false)
	if err != nil {
		t.Fatal(err)
	}
	z.queryMu.Lock()
	z.searchInitializedAt = time.Now().Add(-liveSearchSetupTTL - time.Second)
	z.queryMu.Unlock()
	if _, err = s.ResearchLivePage(ctx, "member", f); err != nil {
		t.Fatal(err)
	}
	expired := counts()
	if expired["search/buildForm"] != recovered["search/buildForm"]+1 {
		t.Fatal("setup TTL ignored")
	}
	if _, err = z.researchRead(ctx, "7", "view"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResearchLivePage(ctx, "member", f); err != nil {
		t.Fatal(err)
	}
	detail := counts()
	if detail["search/buildForm"] != expired["search/buildForm"]+1 {
		t.Fatal("other Bug page did not invalidate search context")
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.businessWrites != 0 {
		t.Fatal("query optimization changed source Bugs")
	}
}

func TestResearchLiveBatchReferencesKeepMetadataAndFailClosed(t *testing.T) {
	s, _ := researchLiveFixture(t, 30)
	ctx := context.Background()
	item, err := s.AttachResearchBug(ctx, "member", "issues", "30")
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	if err = s.SetResearchMetadata(item.ID, ResearchMetadata{ReleaseRequired: &yes}, "member"); err != nil {
		t.Fatal(err)
	}
	refs, err := s.liveLocalReferences(ctx, "issues", []string{"30", "29"})
	if err != nil || len(refs) != 1 || refs["30"].ID != item.ID || refs["30"].ReleaseRequired == nil || !*refs["30"].ReleaseRequired {
		t.Fatal("batch metadata incorrect", err)
	}
	f := researchLiveFilter{Connection: "issues"}
	out, err := s.ResearchLivePage(ctx, "member", f)
	if err != nil || out.Items[0].ID != item.ID || out.Items[0].ReleaseRequired == nil || !*out.Items[0].ReleaseRequired {
		t.Fatal("projection lost metadata", err)
	}
	no := false
	if err = s.SetResearchMetadata(item.ID, ResearchMetadata{ReleaseRequired: &no, NoReleaseReason: "无需发布", Revision: 1}, "member"); err != nil {
		t.Fatal(err)
	}
	out, err = s.ResearchLivePage(ctx, "member", f)
	if err != nil || out.Items[0].ReleaseRequired == nil || *out.Items[0].ReleaseRequired || out.Items[0].NoReleaseReason != "无需发布" {
		t.Fatal("metadata cached across reads", err)
	}
	if _, err = s.db.Exec(`UPDATE external_work_items SET work_item_id=999999 WHERE external_id='30'`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResearchLivePage(ctx, "member", f); err == nil {
		t.Fatal("dangling association silently ignored")
	}
}
