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

func policyRequest(t *testing.T, h http.Handler, method, path, actor, role string, v any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(v)
	r := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
	r.Header.Set("X-User", actor)
	r.Header.Set("X-Role", role)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func savePolicyRules(t *testing.T, s *Store, v ResearchReleaseRules) {
	t.Helper()
	mux := http.NewServeMux()
	registerResearchDeliveryPolicy(mux, s)
	w := policyRequest(t, mux, "PUT", "/api/research/connections/issues/release-rules", "admin", "admin", v)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestResearchPolicyRulesFreshClassificationOverrideAndCAS(t *testing.T) {
	s, state := researchLiveFixture(t, 4)
	ctx := context.Background()
	state.mu.Lock()
	for i, row := range state.rows {
		if i%2 == 0 {
			row["project"] = "75"
		} else {
			row["project"] = "165"
		}
	}
	state.mu.Unlock()
	rules := ResearchReleaseRules{Enabled: true, Default: "unknown", Projects: []ResearchProjectRule{{ProjectID: "75", Name: "生产候选", Decision: "independent"}, {ProjectID: "165", Name: "测试修复", Decision: "not_independent"}}}
	savePolicyRules(t, s, rules)
	f := researchLiveFilter{Connection: "issues", Product: 1, WorkItemFilter: WorkItemFilter{ReleaseRequired: "yes"}}
	page, err := s.ResearchLivePage(ctx, "member", f)
	if err != nil || len(page.Items) != 2 {
		t.Fatal(page, err)
	}
	for _, w := range page.Items {
		if w.ID != 0 || w.ReleaseOrigin != "rule" || w.ProjectID != "75" {
			t.Fatal("list imported Bug or lost rule provenance", w)
		}
	}
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM work_items`).Scan(&count)
	if count != 0 {
		t.Fatal("rule query copied source Bugs")
	}
	item, err := s.AttachResearchBug(ctx, "member", "issues", "1")
	if err != nil || item.ReleaseRequired == nil || !*item.ReleaseRequired {
		t.Fatal(item, err)
	}
	candidates, err := s.WorkCandidates(WorkCandidateFilter{Source: "zentao:issues", WorkItemFilter: WorkItemFilter{ReleaseRequired: "yes"}})
	if err != nil || candidates.Total != 1 || candidates.Items[0].ReleaseRequired == nil || !*candidates.Items[0].ReleaseRequired {
		t.Fatal("version candidates ignored effective rules", candidates, err)
	}
	no := false
	if err = s.SetResearchMetadata(item.ID, ResearchMetadata{ReleaseRequired: &no, NoReleaseReason: "人工不独立交付"}, "alice"); err != nil {
		t.Fatal(err)
	}
	rules.Revision = 1
	rules.Projects[0].Decision = "independent"
	savePolicyRules(t, s, rules)
	page, err = s.ResearchLivePage(ctx, "member", researchLiveFilter{Connection: "issues", Product: 1, WorkItemFilter: WorkItemFilter{Query: "1"}})
	if err != nil || len(page.Items) != 1 || page.Items[0].ReleaseRequired == nil || *page.Items[0].ReleaseRequired || page.Items[0].ReleaseOrigin != "manual" {
		t.Fatal("rule overwrote manual override", page, err)
	}
	if err = s.SetResearchMetadata(item.ID, ResearchMetadata{UseRule: true, Revision: 1}, "alice"); err != nil {
		t.Fatal(err)
	}
	page, err = s.ResearchLivePage(ctx, "member", f)
	if err != nil || len(page.Items) != 2 {
		t.Fatal(page, err)
	}
	state.mu.Lock()
	state.rows[0]["project"] = "165"
	state.mu.Unlock()
	page, err = s.ResearchLivePage(ctx, "member", f)
	if err != nil || len(page.Items) != 1 || page.Items[0].ExternalID != "3" {
		t.Fatal("used stale project classification", page, err)
	}
	mux := http.NewServeMux()
	registerResearchDeliveryPolicy(mux, s)
	if w := policyRequest(t, mux, "PUT", "/api/research/connections/issues/release-rules", "admin", "admin", rules); w.Code != 409 {
		t.Fatal("stale rule edit accepted", w.Code)
	}
	if w := policyRequest(t, mux, "PUT", "/api/research/connections/issues/release-rules", "alice", "member", rules); w.Code != 403 {
		t.Fatal("member changed rules", w.Code)
	}
	state.mu.Lock()
	state.fail = true
	state.mu.Unlock()
	if _, err = s.ResearchLivePage(ctx, "member", f); err == nil {
		t.Fatal("source failure returned cached Bug classification")
	}
}
func TestResearchPolicyMissingProjectIsUnknownAndOverrideReasonRetained(t *testing.T) {
	s, _ := researchLiveFixture(t, 1)
	savePolicyRules(t, s, ResearchReleaseRules{Enabled: true, Default: "not_independent"})
	page, err := s.ResearchLivePage(context.Background(), "member", researchLiveFilter{Connection: "issues", Product: 1})
	if err != nil || page.Items[0].ReleaseRequired != nil {
		t.Fatal("missing source field treated as negative", page, err)
	}
	w := &WorkItem{Title: "manual", Owner: "alice"}
	s.CreateWorkItem(w)
	yes := true
	if err = s.SetResearchMetadata(w.ID, ResearchMetadata{ReleaseRequired: &yes, NoReleaseReason: "紧急独立交付"}, "alice"); err != nil {
		t.Fatal(err)
	}
	if got := s.GetWorkItem(w.ID); got.NoReleaseReason != "紧急独立交付" {
		t.Fatal("positive override reason lost", got)
	}
}
func TestResearchPolicyOwnershipPerChangeAggregationAndFrozenScope(t *testing.T) {
	s, m, _ := deliveryFixture(t)
	main := &WorkItem{Title: "主需求", Owner: "bob"}
	origin := &WorkItem{Title: "测试 Bug", Owner: "alice"}
	other := &WorkItem{Title: "另一需求", Owner: "bob"}
	for _, w := range []*WorkItem{main, origin, other} {
		if err := s.CreateWorkItem(w); err != nil {
			t.Fatal(err)
		}
	}
	d, _ := s.WorkDelivery(origin.ID)
	req := &WorkDeliverySave{Digest: d.Digest, Changes: []Change{{Kind: "manual", Title: "随主项", Reference: "人工核对", DeliveryOwnership: &ChangeDeliveryOwnership{Mode: "follow", WorkItemID: main.ID}}, {Kind: "manual", Title: "仅测试", Reference: "测试事项", DeliveryOwnership: &ChangeDeliveryOwnership{Mode: "draft"}}}}
	if err := s.SaveWorkDelivery(origin.ID, req, "alice", false); err != nil {
		t.Fatal(err)
	}
	d, _ = s.WorkDelivery(origin.ID)
	if len(d.Changes) != 2 || len(s.ChangeWorkItems(d.Changes[0].ID)) != 2 {
		t.Fatal(d)
	}
	for _, c := range d.Changes {
		s.SetChangeReady(c.ID, true)
	}
	v := &ReleaseVersion{Version: "v1", Title: "仅主项", Owner: "bob"}
	if err := s.SaveReleaseDraft(v, VersionScope{WorkItemIDs: []int64{main.ID}}); err != nil {
		t.Fatal(err)
	}
	got := s.GetReleaseVersion(v.ID)
	if len(got.Changes) != 1 || got.Changes[0].ID != d.Changes[0].ID || got.Changes[0].DeliveryOwnership.OriginWorkItemID != origin.ID {
		t.Fatal("unrelated source change leaked", got)
	}
	if _, err := s.SetReleaseVersionReady(v.ID); err != nil {
		t.Fatal(err)
	}
	before := s.GetReleaseVersion(v.ID)
	if err := s.SetChangeWorkItems(d.Changes[0].ID, []int64{other.ID}); err == nil {
		t.Fatal("generic associations replaced origin")
	}
	snapshot := DeliveryPlan{Environment: Environment{Name: "prod"}, Scope: &DeliveryScope{WorkItems: []WorkItem{*main}, Changes: before.Changes}}
	raw, _ := json.Marshal(snapshot)
	s.db.Exec(`INSERT INTO delivery_runs(plan,digest,status,execution,env_secrets,owner,approver,created_at,revision) VALUES(?,'frozen','success','[]','{}','bob','',1,1)`, string(raw))
	tracking, err := s.researchWorkTracking(origin.ID)
	if err != nil || len(tracking["deliveries"].([]ResearchTask)) != 1 {
		t.Fatal("participant cannot trace source change through main item", tracking, err)
	}
	f := WorkItemFilter{Source: "manual", Relation: "participating", RelationActor: "alice"}
	page, err := s.WorkItemsPage(f)
	if err != nil || page.Total != 2 {
		t.Fatal("change participation not recorded for origin/main", page, err)
	}
	// Drafts are usable in testing, not implicitly releasable in production.
	for _, env := range []string{"test", "prod"} {
		plan, _, err := s.ResolveDelivery(DeliveryRequest{StrategyKind: "standard", SourceType: "changes", SourceChangeIDs: []int64{d.Changes[1].ID}, Environment: env, Reason: "fixture"}, m)
		if err != nil {
			t.Fatal(err)
		}
		blocked := strings.Contains(strings.Join(plan.Blockers, ";"), "尚未确定生产交付归属")
		if blocked != (env == "prod") {
			t.Fatal(env, plan.Blockers)
		}
	}
	if len(s.GetReleaseVersion(v.ID).Changes) != 1 {
		t.Fatal("frozen scope expanded")
	}
}
func TestResearchPolicyFollowIdentityAndRelatedPendingNotResolver(t *testing.T) {
	s, state := researchLiveFixture(t, 4)
	ctx := context.Background()
	state.mu.Lock()
	for _, row := range state.rows {
		row["project"] = "75"
		row["resolvedBy"] = "bob"
	}
	state.mu.Unlock()
	savePolicyRules(t, s, ResearchReleaseRules{Enabled: true, Default: "unknown", Projects: []ResearchProjectRule{{ProjectID: "75", Decision: "independent"}}})
	item, err := s.AttachResearchBug(ctx, "alice", "issues", "1")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	registerResearchDeliveryPolicy(mux, s)
	path := fmt.Sprintf("/api/research/work-items/%d/follow", item.ID)
	for _, actor := range []string{"alice", "bob"} {
		if w := policyRequest(t, mux, "PUT", path, actor, "member", map[string]any{"following": true, "actor": "mallory"}); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := policyRequest(t, mux, "PUT", path, "alice", "member", map[string]bool{"following": false}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM research_work_relations WHERE actor='mallory'`).Scan(&n)
	if n != 0 {
		t.Fatal("client spoofed relation actor")
	}
	out, err := s.ResearchLivePage(ctx, "bob", researchLiveFilter{Connection: "issues", WorkItemFilter: WorkItemFilter{Relation: "following", RelationActor: "bob"}})
	if err != nil || len(out.Items) != 1 || !out.Items[0].Following {
		t.Fatal("per-user follow lost", out, err)
	}
	out, err = s.ResearchLivePage(ctx, "alice", researchLiveFilter{Connection: "issues", WorkItemFilter: WorkItemFilter{Relation: "following", RelationActor: "alice"}})
	if err != nil || len(out.Items) != 0 {
		t.Fatal("unfollow affected another actor or retained Alice follow", out, err)
	}
	d, _ := s.WorkDelivery(item.ID)
	if err = s.SaveWorkDelivery(item.ID, &WorkDeliverySave{Digest: d.Digest, Changes: []Change{{Kind: "manual", Title: "Alice 的修改", Reference: "检查", DeliveryOwnership: &ChangeDeliveryOwnership{Mode: "self"}}}}, "alice", false); err != nil {
		t.Fatal(err)
	}
	out, err = s.ResearchLivePage(ctx, "alice", researchLiveFilter{Connection: "issues", Delivery: "pending_release", WorkItemFilter: WorkItemFilter{Relation: "related", RelationActor: "alice"}})
	if err != nil || len(out.Items) != 1 || out.Items[0].ResolvedBy != "bob" || !out.Items[0].Participating {
		t.Fatal("pending query still depends on final resolver", out, err)
	}
	if w := policyRequest(t, mux, "PUT", path, "local", "member", map[string]bool{"following": true}); w.Code != 403 {
		t.Fatal("anonymous follow allowed", w.Code)
	}
	if tasks, err := s.ResearchTasks("bob", "todo"); err != nil || len(tasks) != 0 {
		t.Fatal("following generated approval tasks", tasks, err)
	}
}

func TestResearchPolicyOwnershipAtomicRollbackAndConfirmationSources(t *testing.T) {
	s, _, _ := deliveryFixture(t)
	w := &WorkItem{Title: "参与事项", Owner: "alice"}
	s.CreateWorkItem(w)
	d, _ := s.WorkDelivery(w.ID)
	bad := &WorkDeliverySave{Digest: d.Digest, Changes: []Change{{Kind: "manual", Title: "first", Reference: "check", DeliveryOwnership: &ChangeDeliveryOwnership{Mode: "self"}}, {Kind: "manual", Title: "bad", Reference: "check", DeliveryOwnership: &ChangeDeliveryOwnership{Mode: "follow", WorkItemID: 999999}}}}
	if err := s.SaveWorkDelivery(w.ID, bad, "mallory", false); err == nil {
		t.Fatal("invalid target accepted")
	}
	after, _ := s.WorkDelivery(w.ID)
	if len(after.Changes) != 0 || after.Digest != d.Digest {
		t.Fatal("partial draft/ownership committed", after)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM research_work_relations WHERE actor='mallory'`).Scan(&n)
	if n != 0 {
		t.Fatal("rolled-back edit created participant")
	}
	request := &WorkDeliverySave{Digest: after.Digest, Changes: []Change{{Kind: "manual", Title: "code proof", Reference: "check", DeliveryOwnership: &ChangeDeliveryOwnership{Mode: "self"}}}}
	if err := s.SaveWorkDelivery(w.ID, request, "bob", false); err != nil {
		t.Fatal(err)
	}
	contents, _ := s.WorkDelivery(w.ID)
	scope := &DeliveryScope{WorkItems: []WorkItem{*w}, Changes: contents.Changes}
	for mode, expected := range map[string]int{"work_item_participants": 2, "change_submitters": 1, "work_item_assignees": 1} {
		item := *w
		item.AssignedTo = "qa"
		item.Source = "zentao:issues"
		people, err := s.researchNodePeople(DeliveryNode{AssigneeSource: mode}, item, scope, map[string]string{"zentao:issues\x00qa": "tester"})
		if err != nil || len(people) != expected {
			t.Fatal(mode, people, err)
		}
	}
	// A reader who only follows is never a configured participant confirmer.
	tx, _ := s.db.Begin()
	recordResearchRelation(tx, w.ID, "watcher", "follower")
	tx.Commit()
	people, err := s.researchNodePeople(DeliveryNode{AssigneeSource: "work_item_participants"}, *w, scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, person := range people {
		if person == "watcher" {
			t.Fatal("follower became confirmer")
		}
	}
}

func TestResearchPolicyResolveDoesNotTurnRuleIntoOverrideAndTransferRetainsParticipant(t *testing.T) {
	s, w, writes, mu := researchFixture(t)
	ctx := context.Background()
	z, _, err := s.researchClient(ctx, w, "platform-alice")
	if err != nil {
		t.Fatal(err)
	}
	before, err := z.researchRead(ctx, "7", "view")
	if err != nil {
		t.Fatal(err)
	}
	yes, no := true, false
	request := ResearchAction{OperationID: strings.Repeat("e", 32), Action: "resolve", ExpectedDigest: before.Digest, Resolution: "fixed", ResolvedBuild: "trunk", AssignedTo: "qa", KeepFollowing: &yes, Metadata: ResearchMetadata{Revision: 0}}
	op, err := s.ApplyResearchAction(ctx, w.ID, "platform-alice", request)
	if err != nil || op.State != "success" {
		t.Fatal(op, err)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM research_work_metadata WHERE work_item_id=?`, w.ID).Scan(&n)
	if n != 0 {
		t.Fatal("resolving pinned automatic classification as manual override")
	}
	s.db.QueryRow(`SELECT COUNT(*) FROM research_work_relations WHERE work_item_id=? AND actor='platform-alice'`, w.ID).Scan(&n)
	if n != 2 {
		t.Fatal("missing actual participant or requested follow")
	}
	before, err = z.researchRead(ctx, "7", "view")
	if err != nil {
		t.Fatal(err)
	}
	assign := ResearchAction{OperationID: strings.Repeat("f", 32), Action: "assignTo", ExpectedDigest: before.Digest, AssignedTo: "qa", KeepFollowing: &no, Comment: "交接"}
	op, err = s.ApplyResearchAction(ctx, w.ID, "platform-alice", assign)
	if err != nil || op.State != "success" {
		t.Fatal(op, err)
	}
	s.db.QueryRow(`SELECT COUNT(*) FROM research_work_relations WHERE work_item_id=? AND actor='platform-alice' AND kind='participant'`, w.ID).Scan(&n)
	if n != 1 {
		t.Fatal("transfer erased original participation")
	}
	s.db.QueryRow(`SELECT COUNT(*) FROM research_work_relations WHERE work_item_id=? AND actor='platform-alice' AND kind='follower'`, w.ID).Scan(&n)
	if n != 0 {
		t.Fatal("unchecked continue-following ignored")
	}
	mu.Lock()
	count := *writes
	mu.Unlock()
	if count != 2 {
		t.Fatal("unexpected/replayed source operations", count)
	}
}
