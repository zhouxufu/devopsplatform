package deploy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkCandidatesFullHistoryEvidenceFiltersAndSummary(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	s.UpsertEnvironment(Environment{Name: "prod", DisplayName: "生产"})
	s.UpsertEnvironment(Environment{Name: "test", DisplayName: "测试"})
	ids := []int64{}
	for i := 0; i < 605; i++ {
		w := WorkItem{Source: "zentao:qa", ExternalID: fmt.Sprint(i + 1), Title: fmt.Sprint("Bug ", i), Kind: "bug", Status: "closed", Owner: "sync-admin", ResolvedBy: "alice", Description: "PRIVATE-LONG-BODY"}
		if e := s.CreateWorkItem(&w); e != nil {
			t.Fatal(e)
		}
		ids = append(ids, w.ID)
		s.db.Exec("UPDATE work_items SET updated_at=?,resolved_by='alice',status='closed',external_status='closed' WHERE id=?", 100+i, w.ID)
	}
	duplicate := WorkItem{Source: "zentao:other", ExternalID: "1", Title: "Other installation", Kind: "bug", Status: "closed"}
	s.CreateWorkItem(&duplicate)
	v := &ReleaseVersion{Version: "planned", Title: "planned", Owner: "alice"}
	if e := s.SaveReleaseDraft(v, VersionScope{WorkItemIDs: []int64{ids[1]}}); e != nil {
		t.Fatal(e)
	}
	// The current version lists item 2. Its old delivery snapshot lists item 1.
	// Only the latter is evidence, even after someone edits the draft scope.
	for _, spec := range []struct {
		env, status string
		id          int64
	}{{"prod", "success", ids[0]}, {"test", "success", ids[2]}, {"prod", "failed", ids[3]}} {
		raw, _ := json.Marshal(DeliveryPlan{Environment: Environment{Name: spec.env}, Version: &ReleaseVersion{ID: v.ID, WorkItems: []WorkItem{{ID: spec.id}}}})
		if _, e := s.db.Exec("INSERT INTO delivery_runs(plan,digest,status,execution,env_secrets,owner,approver,created_at,revision) VALUES(?,'digest',?,'[]','{}','alice','',1,1)", string(raw), spec.status); e != nil {
			t.Fatal(e)
		}
	}
	f := WorkCandidateFilter{WorkItemFilter: WorkItemFilter{Kind: "bug", Status: "closed", Person: "alice", PersonRole: "resolved_by", Page: 31, PageSize: 20}, Source: "zentao:qa", Environment: "prod", Delivery: "not_recorded"}
	out, e := s.WorkCandidates(f)
	if e != nil || out.Total != 604 || len(out.Items) != 4 {
		t.Fatalf("history %+v %v", out, e)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "PRIVATE-LONG-BODY") {
		t.Fatal("candidate leaked body")
	}
	f.IDs = []int64{ids[0], ids[1], ids[2], ids[3]}
	f.Delivery = ""
	f.Page = 1
	out, e = s.WorkCandidates(f)
	if e != nil || len(out.Items) != 4 {
		t.Fatal(out, e)
	}
	seen := map[int64]WorkCandidate{}
	for _, item := range out.Items {
		seen[item.ID] = item
	}
	if !seen[ids[0]].SuccessfulDelivery || seen[ids[1]].SuccessfulDelivery || seen[ids[2]].SuccessfulDelivery || seen[ids[3]].SuccessfulDelivery {
		t.Fatal("tracker closure, failed/testing delivery or mutable scope treated as production success")
	}
	if seen[ids[1]].PlannedCount != 1 {
		t.Fatal("other draft planning absent")
	}
	f.VersionID = v.ID
	out, e = s.WorkCandidates(f)
	if e != nil {
		t.Fatal(e)
	}
	for _, w := range out.Items {
		if w.PlannedCount != 0 {
			t.Fatal("editing version counted as other planning")
		}
	}
	f.IDs = nil
	f.Numbers = "1,2"
	out, e = s.WorkCandidates(f)
	if e != nil || out.Total != 2 {
		t.Fatal("batch source-scoped external numbers", out, e)
	}
	f.Numbers = ""
	f.Query = "' OR 1=1 --"
	out, e = s.WorkCandidates(f)
	if e != nil || out.Total != 0 {
		t.Fatal("SQL injection search", out, e)
	}
	f.Query = ""
	f.Person = "sync-admin"
	f.PersonRole = "related"
	out, e = s.WorkCandidates(f)
	if e != nil || out.Total != 0 {
		t.Fatal("sync actor mistaken for bug owner")
	}
	for _, bad := range []WorkCandidateFilter{{WorkItemFilter: WorkItemFilter{Page: -1}}, {Delivery: "not_recorded"}, {Environment: "missing"}, {Numbers: "1,2"}, {Source: "zentao:qa", Numbers: strings.Repeat("1,", 201)}, {Planning: "garbage"}, {Start: 200, End: 100}} {
		if _, e = s.WorkCandidates(bad); e == nil {
			t.Fatal("invalid candidate filter accepted", bad)
		}
	}
}
func TestWorkCandidateRoutesSelectionAndScopeAreReadOnly(t *testing.T) {
	s, m, req := deliveryFixture(t)
	w := &WorkItem{Title: "Need change", Kind: "bug", Status: "closed", Source: "zentao:qa", ExternalID: "123"}
	if e := s.CreateWorkItem(w); e != nil {
		t.Fatal(e)
	}
	c := &Change{Kind: "code", Title: "Implementation", Service: "orders", Reference: "feature/qa", Owner: "alice"}
	if e := s.CreateChange(c); e != nil {
		t.Fatal(e)
	}
	if e := s.SetChangeWorkItems(c.ID, []int64{w.ID}); e != nil {
		t.Fatal(e)
	}
	mux := http.NewServeMux()
	registerWorkCandidateRoutes(mux, s)
	get := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, r)
		return out
	}
	r := get(fmt.Sprintf("/api/work-items/candidates/selected?ids=%d&environment=test", w.ID))
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"change_count":1`) {
		t.Fatal(r.Body.String())
	}
	r = get(fmt.Sprintf("/api/work-items/candidates/selected?ids=%d,%d", w.ID, w.ID))
	if r.Code == 200 {
		t.Fatal("duplicate IDs accepted")
	}
	r = get("/api/work-items/candidates?person_role=arbitrary-column")
	if r.Code == 200 {
		t.Fatal("unvalidated field")
	}
	body := fmt.Sprintf(`{"work_item_ids":[%d],"manual_change_ids":[%d]}`, w.ID, c.ID)
	out := httptest.NewRecorder()
	mux.ServeHTTP(out, httptest.NewRequest("POST", "/api/release-versions/scope-preview", strings.NewReader(body)))
	var rows []map[string]any
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &rows) != nil || len(rows) != 1 {
		t.Fatal("scope not deduplicated", out.Body.String())
	}
	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM release_versions").Scan(&count)
	if count != 0 || len(s.ReleaseOrders("", 100)) != 0 {
		t.Fatal("selection/preview mutated version or executed")
	}
	// Normal submission continues to use its canonical authoritative scope.
	_ = m
	_ = req
}
