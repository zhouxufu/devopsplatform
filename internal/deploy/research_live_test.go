package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type liveFixtureState struct {
	mu                                                        sync.Mutex
	rows                                                      []map[string]any
	predicates                                                map[string][]livePredicate
	calls, businessWrites                                     int
	routes                                                    map[string]int
	fail, ignoreFilters                                       bool
	modelDisabled, modelExtra, modelDuplicate, profileMissing bool
	sourceProducts, sourceSprints                             string
	sourceAdmin, denyBrowse, denyQA                           bool
	resolutionOptions                                         map[string]string
}

func researchLiveFixture(t *testing.T, n int) (*Store, *liveFixtureState) {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	state := &liveFixtureState{predicates: map[string][]livePredicate{}, routes: map[string]int{}, sourceProducts: "1,2,99", sourceSprints: "7"}
	for i := 1; i <= n; i++ {
		account := "alice"
		if i%2 == 0 {
			account = "bob"
		}
		state.rows = append(state.rows, map[string]any{"id": strconv.Itoa(i), "product": "1", "execution": "0", "deleted": "0", "title": fmt.Sprint("菜单 ", i), "status": "closed", "severity": "1", "pri": "2", "resolution": "backend_custom", "steps": "PRIVATE-LONG-BODY", "assignedTo": account, "resolvedBy": account, "openedBy": "qa", "closedBy": "qa", "lastEditedDate": "2026-10-14 10:00:00"})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		defer state.mu.Unlock()
		state.calls++
		reply := func(v any) {
			w.Header().Set("Content-Type", "application/json")
			raw, _ := json.Marshal(v)
			json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": string(raw)})
		}
		method := r.URL.Query().Get("f")
		module := r.URL.Query().Get("m")
		state.routes[module+"/"+method]++
		if method == "getSessionID" {
			reply(map[string]string{"sessionName": "zentaosid", "sessionID": "anonymous"})
			return
		}
		if method == "login" && r.Method == http.MethodPost {
			r.ParseForm()
			account := r.Form.Get("account")
			if r.Form.Get("password") != account+"-secret" {
				json.NewEncoder(w).Encode(map[string]string{"status": "fail"})
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "zentaosid", Value: account, Path: "/"})
			reply(map[string]any{"user": map[string]string{"account": account}})
			return
		}
		if state.fail {
			w.WriteHeader(503)
			return
		}
		cookie, _ := r.Cookie("zentaosid")
		if cookie == nil {
			w.WriteHeader(401)
			return
		}
		session := cookie.Value
		if module == "user" && method == "login" {
			if state.profileMissing {
				reply(map[string]any{"user": map[string]string{"account": session}})
				return
			}
			bugRights := map[string]int{"browse": 1}
			if state.denyBrowse {
				delete(bugRights, "browse")
			}
			views := map[string]int{"qa": 1}
			if state.denyQA {
				views = map[string]int{"my": 1}
			}
			reply(map[string]any{"user": map[string]any{"account": session, "admin": state.sourceAdmin, "view": map[string]string{"products": state.sourceProducts, "sprints": state.sourceSprints}, "rights": map[string]any{"rights": map[string]any{"bug": bugRights}, "acls": map[string]any{"views": views}}}})
			return
		}
		if module == "api" && method == "getModel" {
			if state.modelDisabled {
				fmt.Fprint(w, "apiGetModel disabled")
				return
			}
			if err := validateBugListModelArgs(url.Values{"moduleName": {r.URL.Query().Get("moduleName")}, "methodName": {r.URL.Query().Get("methodName")}, "params": {r.URL.Query().Get("params")}}); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			params := r.URL.Query().Get("params")
			ids := strings.Split(strings.TrimSuffix(strings.TrimPrefix(params, "bugIDList="), ",fields="+bugListFields), ",")
			rows := []map[string]any{}
			for _, row := range state.rows {
				for _, id := range ids {
					if researchValue(row, "id") == id {
						rows = append(rows, row)
					}
				}
			}
			if state.modelExtra {
				rows = append(rows, map[string]any{"id": "999999", "product": "1", "execution": "0", "title": "must not escape", "deleted": "0"})
			}
			if state.modelDuplicate && len(rows) > 0 {
				rows = append(rows, rows[0])
			}
			reply(rows)
			return
		}
		if module == "search" && method == "buildForm" {
			reply(map[string]any{"groupItems": 3, "params": map[string]any{"project": map[string]any{"values": map[string]string{"75": "生产缺陷", "165": "测试修复"}}, "resolution": map[string]any{"values": map[string]string{"fixed2": "后端代码修复", "bydesign2": "设计如此"}}}})
			return
		}
		if module == "search" && method == "buildQuery" {
			r.ParseForm()
			action := r.Form.Get("actionURL")
			if !strings.HasPrefix(action, "/zentao/index.php?m=bug&f=browse") {
				t.Error("unsafe or incorrectly ordered action URI", action)
			}
			predicates := []livePredicate{}
			for i := 1; i <= 6; i++ {
				k := strconv.Itoa(i)
				if r.Form.Get("value"+k) != "" {
					predicates = append(predicates, livePredicate{r.Form.Get("field" + k), r.Form.Get("operator" + k), r.Form.Get("value" + k)})
				}
			}
			state.predicates[session] = predicates
			fmt.Fprint(w, "<script>parent.location='"+action+"';</script>")
			return
		}
		if r.Method != http.MethodGet {
			state.businessWrites++
			w.WriteHeader(400)
			return
		}
		if method == "resolve" && state.resolutionOptions != nil {
			for _, row := range state.rows {
				if row["id"] == r.URL.Query().Get("bugID") {
					reply(map[string]any{"bug": row, "resolutionList": state.resolutionOptions})
					return
				}
			}
		}
		if method == "view" {
			for _, row := range state.rows {
				if row["id"] == r.URL.Query().Get("bugID") {
					reply(map[string]any{"bug": row, "actions": map[string]any{}})
					return
				}
			}
			w.WriteHeader(404)
			return
		}
		if module == "product" && method == "browse" {
			reply(map[string]any{"stories": []any{}, "pager": map[string]any{"pageID": r.URL.Query().Get("pageID"), "recTotal": 0}})
			return
		}
		if method != "browse" {
			w.WriteHeader(404)
			return
		}
		rows := []map[string]any{}
		requestedProduct := r.URL.Query().Get("productID")
		if requestedProduct == "0" {
			requestedProduct = "1"
		}
		allProducts := false
		if r.URL.Query().Get("browseType") == "bysearch" && !state.ignoreFilters {
			for _, p := range state.predicates[session] {
				if p.Field == "product" && p.Value == "all" {
					allProducts = true
				}
			}
		}
		for _, row := range state.rows {
			if !allProducts && row["product"] != requestedProduct {
				continue
			}
			matches := true
			if r.URL.Query().Get("browseType") == "bysearch" && !state.ignoreFilters {
				for _, p := range state.predicates[session] {
					if p.Field == "product" && p.Value == "all" {
						continue
					}
					value := researchValue(row, p.Field)
					if p.Operator == "=" && value != p.Value || p.Operator == "!=" && value == p.Value || p.Operator == ">=" && value < p.Value || p.Operator == "<=" && value > p.Value || p.Operator == "include" && !strings.Contains(value, p.Value) {
						matches = false
					}
				}
			}
			if matches {
				rows = append(rows, row)
			}
		}
		order := r.URL.Query().Get("orderBy")
		sortLiveBugs(rows, order)
		size, _ := strconv.Atoi(r.URL.Query().Get("recPerPage"))
		page, _ := strconv.Atoi(r.URL.Query().Get("pageID"))
		if size < 1 || page < 1 {
			w.WriteHeader(400)
			return
		}
		total := len(rows)
		start := min((page-1)*size, total)
		rows = rows[start:min(start+size, total)]
		// PHP associative keys are intentionally unordered; frontend still receives
		// source order rather than Go map iteration order.
		indexed := map[string]any{}
		for _, row := range rows {
			indexed[researchValue(row, "id")] = row
		}
		reply(map[string]any{"bugs": indexed, "orderBy": order, "products": map[string]string{"1": "产品 A", "2": "产品 B", "99": "不允许的产品"}, "users": map[string]string{"alice": "Alice", "bob": "Bob"}, "pager": map[string]any{"recTotal": total, "recPerPage": size, "pageID": page}})
	}))
	t.Cleanup(server.Close)
	if err = s.UpsertConnection(ResourceConnection{Name: "issues", Kind: "zentao", Endpoint: server.URL + "/zentao", Username: "sync", Password: "sync-secret", Config: map[string]string{"product_ids": "1,2", "sync_minutes": "1"}}); err != nil {
		t.Fatal(err)
	}
	s.UpsertEnvironment(Environment{Name: "prod", DisplayName: "生产"})
	return s, state
}

func TestResearchLivePaginationAndFiltersDoNotImport(t *testing.T) {
	s, state := researchLiveFixture(t, 12037)
	ctx := context.Background()
	seen := map[string]bool{}
	cursor := ""
	for page := 1; page <= 2; page++ {
		out, err := s.ResearchLivePage(ctx, "platform-alice", researchLiveFilter{Connection: "issues", Cursor: cursor, WorkItemFilter: WorkItemFilter{Page: page, PageSize: 20}})
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Items) != 20 || out.Total != 12037 || out.TotalExact || out.ReadIdentity != "public" || len(out.Products) != 2 {
			t.Fatalf("bad page: %+v", out)
		}
		for _, row := range out.Items {
			if seen[row.ExternalID] || row.ID != 0 || row.Description != "" || row.Resolution != "backend_custom" {
				t.Fatal("repeat/import/description/field loss")
			}
			seen[row.ExternalID] = true
		}
		cursor = out.NextCursor
	}
	if err := s.AuthorizeResearch(ctx, "issues", "platform-alice", ResearchCredential{Account: "alice", Password: "alice-secret"}); err != nil {
		t.Fatal(err)
	}
	out, err := s.ResearchLivePage(ctx, "platform-alice", researchLiveFilter{Connection: "issues", Product: 1, Mine: true, WorkItemFilter: WorkItemFilter{Person: "bob", PersonRole: "resolved_by", Status: "closed", Severity: "1", Priority: "2", Resolution: "backend_custom", PageSize: 20, Order: "priority_asc"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range out.Items {
		if row.ResolvedBy != "alice" {
			t.Fatal("client spoofed my account")
		}
	}
	if out.Total != 6019 || out.ReadIdentity != "personal" {
		t.Fatal("source filters not applied", out.Total)
	}
	state.mu.Lock()
	state.rows[0]["title"] = "实时改名"
	state.mu.Unlock()
	out, err = s.ResearchLivePage(ctx, "platform-alice", researchLiveFilter{Connection: "issues", WorkItemFilter: WorkItemFilter{Query: "1", Status: "closed", PageSize: 20}})
	if err != nil || len(out.Items) != 1 || out.Items[0].Title != "实时改名" {
		t.Fatal("local copy returned", err)
	}
	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM work_items").Scan(&count)
	if count != 0 {
		t.Fatal("GET imported bugs", count)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.businessWrites != 0 {
		t.Fatal("query wrote Bug")
	}
}

func TestResearchLiveReferencePreservesMetadataAndHistory(t *testing.T) {
	s, _ := researchLiveFixture(t, 60)
	ctx := context.Background()
	item, err := s.AttachResearchBug(ctx, "alice", "issues", "7")
	if err != nil {
		t.Fatal(err)
	}
	no := false
	if err = s.SetResearchMetadata(item.ID, ResearchMetadata{ReleaseRequired: &no, NoReleaseReason: "重复问题"}, "alice"); err != nil {
		t.Fatal(err)
	}
	change := &Change{Title: "代码", Kind: "code", Service: "orders", Owner: "alice"}
	if err = s.CreateChange(change); err != nil {
		t.Fatal(err)
	}
	if err = s.SetChangeWorkItems(change.ID, []int64{item.ID}); err != nil {
		t.Fatal(err)
	}
	version := &ReleaseVersion{Version: "v1", Title: "v1", Owner: "alice"}
	yes := true
	s.SetResearchMetadata(item.ID, ResearchMetadata{ReleaseRequired: &yes, Revision: 1}, "alice")
	if err = s.SaveReleaseDraft(version, VersionScope{WorkItemIDs: []int64{item.ID}}); err != nil {
		t.Fatal(err)
	}
	again, err := s.AttachResearchBug(ctx, "alice", "issues", "7")
	if err != nil || again.ID != item.ID || again.MetadataRevision != 2 || again.ReleaseRequired == nil || !*again.ReleaseRequired {
		t.Fatal("metadata overwritten", err)
	}
	if len(s.ChangeWorkItems(change.ID)) != 1 || len(s.GetReleaseVersion(version.ID).WorkItems) != 1 {
		t.Fatal("delivery links lost")
	}
	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM work_items").Scan(&count)
	if count != 1 {
		t.Fatal("whole list imported")
	}
}

func TestResearchLiveLocalFiltersUseCursorAndFrozenEvidence(t *testing.T) {
	s, _ := researchLiveFixture(t, 60)
	ctx := context.Background()
	no := false
	for id := 60; id >= 56; id-- {
		item, err := s.AttachResearchBug(ctx, "alice", "issues", strconv.Itoa(id))
		if err != nil {
			t.Fatal(err)
		}
		if err = s.SetResearchMetadata(item.ID, ResearchMetadata{ReleaseRequired: &no, NoReleaseReason: "无需发布"}, "alice"); err != nil {
			t.Fatal(err)
		}
	}
	published, err := s.AttachResearchBug(ctx, "alice", "issues", "55")
	if err != nil {
		t.Fatal(err)
	}
	plan, _ := json.Marshal(DeliveryPlan{Environment: Environment{Name: "prod"}, Scope: &DeliveryScope{WorkItems: []WorkItem{{ID: published.ID}}}})
	if _, err = s.db.Exec(`INSERT INTO delivery_runs(plan,digest,status,execution,env_secrets,owner,approver,created_at,revision) VALUES(?,'frozen','success','[]','{}','alice','',1,1)`, string(plan)); err != nil {
		t.Fatal(err)
	}
	f := researchLiveFilter{Connection: "issues", Environment: "prod", Delivery: "not_recorded", WorkItemFilter: WorkItemFilter{ReleaseRequired: "eligible", Status: "closed", PageSize: 20}}
	first, err := s.ResearchLivePage(ctx, "alice", f)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 20 || first.TotalExact || first.NextCursor == "" || first.Total != 60 || first.Items[0].ExternalID != "54" {
		t.Fatalf("local metadata filter wrong %+v", first)
	}
	seen := map[string]bool{}
	for _, row := range first.Items {
		seen[row.ExternalID] = true
	}
	f.Page = 2
	f.Cursor = first.NextCursor
	second, err := s.ResearchLivePage(ctx, "alice", f)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range second.Items {
		if seen[row.ExternalID] {
			t.Fatal("filtered page repeated")
		}
	}
	f.Priority = "1"
	if _, err = s.ResearchLivePage(ctx, "alice", f); err == nil {
		t.Fatal("old cursor accepted after filter change")
	}
}

func TestResearchLiveConcurrentTabsAndFailClosed(t *testing.T) {
	s, state := researchLiveFixture(t, 40)
	ctx := context.Background()
	var wg sync.WaitGroup
	for _, person := range []string{"alice", "bob", "alice", "bob"} {
		wg.Add(1)
		go func(person string) {
			defer wg.Done()
			out, err := s.ResearchLivePage(ctx, "member", researchLiveFilter{Connection: "issues", WorkItemFilter: WorkItemFilter{Person: person, PersonRole: "resolved_by", PageSize: 20}})
			if err != nil {
				t.Error(err)
				return
			}
			for _, w := range out.Items {
				if w.ResolvedBy != person {
					t.Error("other tab filters leaked")
				}
			}
		}(person)
	}
	wg.Wait()
	if _, err := s.ResearchLivePage(ctx, "member", researchLiveFilter{Connection: "issues", Product: 99}); err == nil {
		t.Fatal("out-of-scope product allowed")
	}
	if _, err := s.ResearchLivePage(ctx, "unverified", researchLiveFilter{Connection: "issues", Mine: true}); err == nil {
		t.Fatal("public account treated as mine")
	}
	state.mu.Lock()
	state.ignoreFilters = true
	state.mu.Unlock()
	if _, err := s.ResearchLivePage(ctx, "member", researchLiveFilter{Connection: "issues", WorkItemFilter: WorkItemFilter{Query: "not-present"}}); err == nil {
		t.Fatal("unfiltered source data accepted")
	}
	state.mu.Lock()
	state.fail = true
	state.mu.Unlock()
	if _, err := s.ResearchLivePage(ctx, "member", researchLiveFilter{Connection: "issues"}); err == nil {
		t.Fatal("source failure hidden by local fallback")
	}
}
