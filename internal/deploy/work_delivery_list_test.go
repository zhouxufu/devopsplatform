package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func deliveryListWork(t *testing.T, s *Store, title string, intent *bool) *WorkItem {
	t.Helper()
	w := &WorkItem{Title: title, Source: "manual", Kind: "bug", Owner: "alice"}
	if err := s.CreateWorkItem(w); err != nil {
		t.Fatal(err)
	}
	if intent != nil {
		reason := ""
		if !*intent {
			reason = "无需独立上线"
		}
		if err := s.SetResearchMetadata(w.ID, ResearchMetadata{ReleaseRequired: intent, NoReleaseReason: reason}, "alice"); err != nil {
			t.Fatal(err)
		}
	}
	return s.GetWorkItem(w.ID)
}
func deliveryListChange(t *testing.T, s *Store, w *WorkItem, mode string, target int64) *Change {
	t.Helper()
	d, err := s.WorkDelivery(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	v := Change{Kind: "manual", Title: w.Title + "变更", Reference: "人工检查", DeliveryOwnership: &ChangeDeliveryOwnership{Mode: mode, WorkItemID: target}}
	if err = s.SaveWorkDelivery(w.ID, &WorkDeliverySave{Digest: d.Digest, Changes: []Change{v}}, "alice", true); err != nil {
		t.Fatal(err)
	}
	d, err = s.WorkDelivery(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s.GetChange(d.Changes[len(d.Changes)-1].ID)
}
func deliveryListRun(t *testing.T, s *Store, w *WorkItem, changes []Change, env Environment, status, workbench, owner string) int64 {
	t.Helper()
	plan := DeliveryPlan{Scope: &DeliveryScope{WorkItems: []WorkItem{*w}, Changes: changes}, Environment: env, Template: DeliveryTemplate{Workbench: workbench}}
	raw, _ := json.Marshal(plan)
	result, err := s.db.Exec(`INSERT INTO delivery_runs(plan,digest,status,execution,env_secrets,owner,approver,created_at,revision) VALUES(?,'digest',?,'[]','{}',?,'',1,1)`, string(raw), status, owner)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	return id
}
func TestDeliveryWorkListMembershipAggregationAndFilters(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	yes, no := true, false
	main := deliveryListWork(t, s, "生产主 Bug", &yes)
	child := deliveryListWork(t, s, "随主项子 Bug", &no)
	draft := deliveryListWork(t, s, "待归属", &yes)
	testOnly := deliveryListWork(t, s, "仅测试", &no)
	deliveryListWork(t, s, "只看过没有变更", &yes)
	deliveryListChange(t, s, main, "self", 0)
	deliveryListChange(t, s, child, "follow", main.ID)
	deliveryListChange(t, s, draft, "draft", 0)
	deliveryListChange(t, s, testOnly, "self", 0)
	f := deliveryWorkFilter{}
	out, err := s.DeliveryWorkPage(context.Background(), "alice", true, f)
	if err != nil || out.Total != 1 || out.Items[0].ID != main.ID || out.Items[0].ChangeCount != 2 || len(out.Items[0].Children) != 1 || out.Items[0].Children[0].ID != child.ID {
		t.Fatal("bad production scope", out, err)
	}
	if out.Items[0].Preparation != "draft" || out.Items[0].DeliveryStatus != "not_started" {
		t.Fatal("draft confused with delivered", out.Items[0])
	}
	f.Scope = "unassigned"
	out, err = s.DeliveryWorkPage(context.Background(), "alice", true, f)
	if err != nil || out.Total != 1 || out.Items[0].ID != draft.ID {
		t.Fatal("draft missing", out, err)
	}
	f.Scope = "nonproduction"
	out, err = s.DeliveryWorkPage(context.Background(), "alice", true, f)
	if err != nil || out.Total != 3 {
		t.Fatal("test changes hidden", out, err)
	}
	f.Scope = "production"
	f.Query = "随主项子"
	out, err = s.DeliveryWorkPage(context.Background(), "alice", true, f)
	if err != nil || out.Total != 1 || out.Items[0].ID != main.ID {
		t.Fatal("child search duplicates or misses parent", out, err)
	}
}
func TestDeliveryWorkListEvidenceLatestScopeAndEnvironment(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	yes := true
	prod := Environment{Name: "customer-east", DisplayName: "客户正式区", Purpose: "production"}
	testEnv := Environment{Name: "verify", DisplayName: "验证区", Purpose: "test", Protected: true}
	s.UpsertEnvironment(prod)
	s.UpsertEnvironment(testEnv)
	w := deliveryListWork(t, s, "发布范围", &yes)
	c := deliveryListChange(t, s, w, "self", 0)
	c, err = s.SetChangeReady(c.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	read := func() deliveryWorkRow {
		t.Helper()
		p, e := s.DeliveryWorkPage(context.Background(), "alice", true, deliveryWorkFilter{})
		if e != nil || len(p.Items) != 1 {
			t.Fatal(p, e)
		}
		return p.Items[0]
	}
	deliveryListRun(t, s, w, []Change{*c}, testEnv, "success", "", "alice")
	deliveryListRun(t, s, w, []Change{*c}, prod, "success", "research", "alice")
	if row := read(); row.DeliveryStatus != "not_started" {
		t.Fatal("test/preparation incorrectly proves production", row)
	}
	success := deliveryListRun(t, s, w, []Change{*c}, prod, "success", "", "alice")
	if row := read(); row.DeliveryStatus != "success" || row.RunID != success {
		t.Fatal(row)
	}
	for _, state := range []string{"failed", "canceled", "running", "pending_approval"} {
		id := deliveryListRun(t, s, w, []Change{*c}, prod, state, "", "alice")
		if row := read(); row.DeliveryStatus != state || row.RunID != id {
			t.Fatal("older success hides latest attempt", row)
		}
	}
	newChange := deliveryListChange(t, s, w, "self", 0)
	newChange, err = s.SetChangeReady(newChange.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	deliveryListRun(t, s, w, []Change{*c}, prod, "success", "", "alice")
	if row := read(); row.DeliveryStatus != "partial_success" || row.ChangeCount != 2 {
		t.Fatal("partial marked full", row)
	}
	// Private execution details cannot be exposed through the list.
	id := deliveryListRun(t, s, w, []Change{*c, *newChange}, prod, "failed", "", "bob")
	out, err := s.DeliveryWorkPage(context.Background(), "alice", false, deliveryWorkFilter{})
	if err != nil || out.Items[0].RunID == id {
		t.Fatal("foreign execution leaked", out, err)
	}
	if _, err = s.DeliveryWorkPage(context.Background(), "alice", true, deliveryWorkFilter{Scope: "nonproduction", Environment: prod.Name}); err == nil {
		t.Fatal("production accepted as testing")
	}
}
func TestDeliveryWorkListReadsFreshSourceFieldsAndScope(t *testing.T) {
	s, state := researchLiveFixture(t, 6)
	items := registerPendingFixture(t, s, 3)
	for _, w := range items {
		deliveryListChange(t, s, w, "self", 0)
	}
	state.mu.Lock()
	state.rows[0]["title"] = "新的来源标题"
	state.rows[0]["status"] = "active"
	state.rows[1]["product"] = "99"
	before := state.routes["api/getModel"]
	state.mu.Unlock()
	out, err := s.DeliveryWorkPage(context.Background(), "member", true, deliveryWorkFilter{})
	if err != nil || out.Total != 2 || len(out.SourceErrors) != 0 {
		t.Fatal(out, err)
	}
	for _, row := range out.Items {
		if row.ExternalID == "1" && (row.Title != "新的来源标题" || row.ExternalStatus != "active") {
			t.Fatal("stale source fields", row)
		}
	}
	state.mu.Lock()
	count := state.routes["api/getModel"] - before
	writes := state.businessWrites
	state.fail = true
	state.mu.Unlock()
	if count != 1 || writes != 0 {
		t.Fatal("not batched read-only", count, writes)
	}
	out, err = s.DeliveryWorkPage(context.Background(), "member", true, deliveryWorkFilter{})
	if err != nil || out.Total != 0 || len(out.SourceErrors) != 1 {
		t.Fatal("stale fallback after source error", out, err)
	}
}
func TestDeliveryWorkListAPIPaginationAndReadOnly(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	yes := true
	for i := 0; i < 21; i++ {
		w := deliveryListWork(t, s, fmt.Sprint("Bug", i), &yes)
		deliveryListChange(t, s, w, "self", 0)
	}
	mux := http.NewServeMux()
	registerDeliveryWorkList(mux, s)
	for _, query := range []string{"page=0", "page_size=500", "scope=arbitrary", "scope=production&environment=prod"} {
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, httptest.NewRequest("GET", "/api/work-items/delivery-page?"+query, nil))
		if res.Code != 400 {
			t.Fatal(query, res.Code)
		}
	}
	res := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/work-items/delivery-page?page=2", nil)
	req.Header.Set("X-User", "alice")
	req.Header.Set("X-Role", "admin")
	mux.ServeHTTP(res, req)
	var page deliveryWorkPage
	if err = json.Unmarshal(res.Body.Bytes(), &page); err != nil || res.Code != 200 || page.Total != 21 || len(page.Items) != 1 || page.Page != 2 {
		t.Fatal(res.Body.String(), err)
	}
	if strings.Contains(res.Body.String(), "人工检查") {
		t.Fatal("private change body included in list")
	}
}

func TestDeliveryWorkListPolicyUsesFreshProjectNotSnapshot(t *testing.T) {
	s, state := researchLiveFixture(t, 2)
	state.mu.Lock()
	state.rows[0]["project"] = "75"
	state.mu.Unlock()
	savePolicyRules(t, s, ResearchReleaseRules{Enabled: true, Default: "not_independent", Projects: []ResearchProjectRule{{ProjectID: "75", Decision: "independent"}}})
	w, err := s.AttachResearchBug(context.Background(), "alice", "issues", "1")
	if err != nil {
		t.Fatal(err)
	}
	deliveryListChange(t, s, w, "self", 0)
	p, err := s.DeliveryWorkPage(context.Background(), "alice", true, deliveryWorkFilter{})
	if err != nil || p.Total != 1 {
		t.Fatal(p, err)
	}
	state.mu.Lock()
	state.rows[0]["project"] = "165"
	state.mu.Unlock()
	p, err = s.DeliveryWorkPage(context.Background(), "alice", true, deliveryWorkFilter{})
	if err != nil || p.Total != 0 {
		t.Fatal("old project kept in production", p, err)
	}
	p, err = s.DeliveryWorkPage(context.Background(), "alice", true, deliveryWorkFilter{Scope: "nonproduction"})
	if err != nil || p.Total != 1 || p.Items[0].ReleaseRequired == nil || *p.Items[0].ReleaseRequired {
		t.Fatal("test change removed", p, err)
	}
}

func TestDeliveryWorkListHiddenChildCannotProveCompleteScope(t *testing.T) {
	s, state := researchLiveFixture(t, 2)
	items := registerPendingFixture(t, s, 2)
	deliveryListChange(t, s, items[0], "self", 0)
	deliveryListChange(t, s, items[1], "follow", items[0].ID)
	state.mu.Lock()
	state.rows[1]["product"] = "99"
	state.mu.Unlock()
	p, err := s.DeliveryWorkPage(context.Background(), "alice", true, deliveryWorkFilter{})
	if err != nil || p.Total != 1 || len(p.Items[0].Children) != 0 || p.Items[0].Preparation != "blocked" || p.Items[0].DeliveryStatus != "scope_unverified" {
		t.Fatal("hidden source linkage leaked/completed", p, err)
	}
}

func TestDeliveryWorkListChangedBodyCannotReuseOldSuccess(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	yes := true
	prod := Environment{Name: "east", Purpose: "production"}
	s.UpsertEnvironment(prod)
	w := deliveryListWork(t, s, "正文校验", &yes)
	c := deliveryListChange(t, s, w, "self", 0)
	c, err = s.SetChangeReady(c.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	deliveryListRun(t, s, w, []Change{*c}, prod, "success", "", "alice")
	// Same ID and second-level timestamp are not sufficient coverage evidence.
	if _, err = s.db.Exec(`UPDATE changes SET reference='修改后的文本' WHERE id=?`, c.ID); err != nil {
		t.Fatal(err)
	}
	p, err := s.DeliveryWorkPage(context.Background(), "alice", true, deliveryWorkFilter{})
	if err != nil || p.Items[0].DeliveryStatus != "scope_changed" {
		t.Fatal("old contents marked delivered", p, err)
	}
}

func TestDeliveryWorkListMixedDraftCannotAppearReadyForProduction(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	yes := true
	w := deliveryListWork(t, s, "混合归属", &yes)
	c := deliveryListChange(t, s, w, "self", 0)
	if _, err = s.SetChangeReady(c.ID, true); err != nil {
		t.Fatal(err)
	}
	deliveryListChange(t, s, w, "draft", 0)
	p, err := s.DeliveryWorkPage(context.Background(), "alice", true, deliveryWorkFilter{})
	if err != nil || len(p.Items) != 1 || p.Items[0].Preparation != "blocked" || p.Items[0].DeliveryStatus != "scope_unverified" {
		t.Fatal("unassigned change silently dropped", p, err)
	}
	p, err = s.DeliveryWorkPage(context.Background(), "alice", true, deliveryWorkFilter{Scope: "unassigned"})
	if err != nil || len(p.Items) != 1 || p.Items[0].ChangeCount != 1 {
		t.Fatal("unassigned part unavailable", p, err)
	}
}
