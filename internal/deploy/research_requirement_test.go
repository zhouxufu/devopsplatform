package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestResearchRequirementTypedReadWriteReceiptAndNoReplay(t *testing.T) {
	for _, pathInfo := range []bool{false, true} {
		t.Run(fmt.Sprintf("PATHINFO=%t", pathInfo), func(t *testing.T) {
			testResearchRequirementTypedReadWriteReceiptAndNoReplay(t, pathInfo)
		})
	}
}

func testResearchRequirementTypedReadWriteReceiptAndNoReplay(t *testing.T, pathInfo bool) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	mu := sync.Mutex{}
	writes := 0
	marker := true
	allowed := true
	story := map[string]any{"id": "7", "title": "原需求", "product": "1", "status": "active", "stage": "developing", "type": "story", "spec": "<p>原需求内容</p>", "verify": "必须回归", "assignedTo": "alice", "pri": "2"}
	actions := map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		method, module := r.URL.Query().Get("f"), r.URL.Query().Get("m")
		product, storyID := r.URL.Query().Get("productID"), r.URL.Query().Get("storyID")
		if pathInfo {
			if r.URL.Path == "/zentao/index.php" {
				if method != "getSessionID" {
					t.Errorf("unexpected GET-mode fallback: %s", r.URL)
				}
				http.NotFound(w, r)
				return
			}
			if r.URL.RawQuery != "" {
				t.Errorf("named query in PATHINFO request: %s", r.URL)
			}
			switch r.URL.Path {
			case "/zentao/api-getsessionid.json":
				module, method = "api", "getSessionID"
			case "/zentao/user-login.json":
				module, method = "user", "login"
			case "/zentao/product-browse-1-all-all-0-story-lastEditedDate_desc-0-100-1.json":
				module, method, product = "product", "browse", "1"
			case "/zentao/search-buildform-bug.json":
				module, method = "search", "buildForm"
			case "/zentao/search-buildquery.json":
				module, method = "search", "buildQuery"
			case "/zentao/bug-browse-1-all-all-0-lastEditedDate_desc-0-20-1.json", "/zentao/bug-browse-0-all-bysearch-0-lastEditedDate_desc-0-20-1.json":
				module, method = "bug", "browse"
			case "/zentao/story-view-7.json":
				module, method, storyID = "story", "view", "7"
			case "/zentao/story-edit-7.json":
				module, method, storyID = "story", "edit", "7"
			default:
				t.Errorf("unexpected positional route: %s", r.URL)
				http.NotFound(w, r)
				return
			}
		}
		reply := func(v any) {
			b, _ := json.Marshal(v)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": string(b)})
		}
		if method == "getSessionID" {
			reply(map[string]any{"sessionName": "zentaosid", "sessionID": "fixture"})
			return
		}
		if method == "login" {
			reply(map[string]any{"user": map[string]any{"account": "alice"}})
			return
		}
		if module == "search" {
			reply(map[string]any{"groupItems": 5})
			return
		}
		if module == "bug" && method == "browse" {
			reply(map[string]any{"bugs": []any{}, "orderBy": "lastEditedDate_desc", "pager": map[string]any{"pageID": 1, "recTotal": 0, "recPerPage": 20}})
			return
		}
		if module == "product" && method == "browse" {
			rows := []any{}
			if product == "1" {
				rows = append(rows, story)
			}
			reply(map[string]any{"stories": rows, "pager": map[string]any{"pageID": 1, "recTotal": len(rows)}})
			return
		}
		if module != "story" || storyID != "7" {
			t.Errorf("wrong source identity %s", r.URL)
			http.NotFound(w, r)
			return
		}
		if r.Method == "POST" {
			writes++
			_ = r.ParseMultipartForm(1 << 20)
			if method != "edit" || r.PostForm.Get("status") != "active" || r.PostForm.Get("spec") != "<p>原需求内容</p>" || r.PostForm.Get("verify") != "必须回归" || r.PostForm.Get("resolution") != "" {
				t.Errorf("unsafe story mutation: %v", r.PostForm)
			}
			story["stage"] = r.PostForm.Get("stage")
			if marker {
				actions[fmt.Sprint(writes)] = map[string]any{"actor": "alice", "action": "edited", "comment": r.PostForm.Get("comment")}
			}
			reply(map[string]any{"ok": true})
			return
		}
		stages := map[string]string{}
		if allowed {
			stages["developed"] = "研发完毕"
		}
		reply(map[string]any{"story": story, "actions": actions, "stageList": stages, "users": map[string]string{"alice": "Alice"}})
	}))
	defer server.Close()
	e = s.UpsertConnection(ResourceConnection{Name: "issues", Kind: "zentao", Endpoint: server.URL + "/zentao", Username: "alice", Password: "secret", Config: map[string]string{"product_ids": "1"}})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.AuthorizeResearch(context.Background(), "issues", "alice", ResearchCredential{Account: "alice", Password: "secret"}); e != nil {
		t.Fatal(e)
	}
	page, e := s.ResearchLivePage(context.Background(), "alice", researchLiveFilter{Connection: "issues", WorkItemFilter: WorkItemFilter{Kind: "requirement", Page: 1, PageSize: 20}})
	if e != nil || len(page.Items) != 1 || page.Items[0].Kind != "requirement" || page.Items[0].ID != 0 {
		t.Fatal(page, e)
	}
	// Homepage federates Bugs and requirements across sources using the current user's identity.
	home, e := s.ResearchLivePage(context.Background(), "alice", researchLiveFilter{Mine: true, WorkItemFilter: WorkItemFilter{Page: 1, PageSize: 20}})
	if e != nil || len(home.Items) != 1 || home.Items[0].Kind != "requirement" || home.Items[0].ExternalID != "7" {
		t.Fatal("homepage federation", home, e)
	}
	if len(s.WorkItems("", "", 100)) != 0 {
		t.Fatal("homepage query imported source records")
	}
	item, e := s.AttachResearchRequirement(context.Background(), "alice", "issues", "7")
	if e != nil {
		t.Fatal(e)
	}
	item2, e := s.AttachResearchRequirement(context.Background(), "alice", "issues", "7")
	if e != nil || item.ID != item2.ID {
		t.Fatal(item2, e)
	}
	z, _, e := s.researchClient(context.Background(), item, "alice")
	if e != nil {
		t.Fatal(e)
	}
	before, e := z.researchReadItem(context.Background(), item, "edit")
	if e != nil || requirementDevelopedLabel(before) != "研发完毕" {
		t.Fatal(before, e)
	}
	req := ResearchAction{OperationID: strings.Repeat("d", 32), Action: "develop", ExpectedDigest: before.Digest, Comment: "交付和回归已通过"}
	op, e := s.ApplyResearchAction(context.Background(), item.ID, "alice", req)
	if e != nil || op.State != "success" {
		t.Fatal(op, e)
	}
	op, e = s.ApplyResearchAction(context.Background(), item.ID, "alice", req)
	if e != nil || op.State != "success" {
		t.Fatal(op, e)
	}
	mu.Lock()
	if writes != 1 || story["status"] != "active" {
		t.Error("replayed or closed", writes, story)
	}
	marker = false
	mu.Unlock()
	before, _ = z.researchReadItem(context.Background(), item, "edit")
	req.OperationID = strings.Repeat("e", 32)
	req.Action = "comment"
	req.ExpectedDigest = before.Digest
	op, e = s.ApplyResearchAction(context.Background(), item.ID, "alice", req)
	if e != nil || op.State != "uncertain" {
		t.Fatal(op, e)
	}
	_, _ = s.ApplyResearchAction(context.Background(), item.ID, "alice", req)
	mu.Lock()
	if writes != 2 {
		t.Error("uncertain write replayed", writes)
	}
	allowed = false
	mu.Unlock()
	before, _ = z.researchReadItem(context.Background(), item, "edit")
	req.OperationID = strings.Repeat("f", 32)
	req.Action = "develop"
	req.ExpectedDigest = before.Digest
	if _, e = s.ApplyResearchAction(context.Background(), item.ID, "alice", req); e == nil {
		t.Fatal("missing native option accepted")
	}
}

func TestResearchRequirementsMergeProductHeadsAndCursor(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := func(v any) {
			b, _ := json.Marshal(v)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": string(b)})
		}
		switch r.URL.Query().Get("f") {
		case "getSessionID":
			reply(map[string]any{"sessionName": "zentaosid", "sessionID": "fixture"})
		case "login":
			reply(map[string]any{"user": map[string]any{"account": "reader"}})
		case "browse":
			if r.Method != "GET" || r.URL.Query().Get("m") != "product" {
				t.Error("unexpected business request")
			}
			product := r.URL.Query().Get("productID")
			if product != "1" && product != "2" {
				t.Error("product whitelist widened")
			}
			rows := []any{}
			for i := 40; i >= 1; i-- {
				if fmt.Sprint(1+i%2) != product {
					continue
				}
				rows = append(rows, map[string]any{"id": fmt.Sprint(i), "title": "Requirement " + fmt.Sprint(i), "product": product, "status": "active", "lastEditedDate": fmt.Sprintf("2026-11-18 10:00:%02d", i)})
			}
			reply(map[string]any{"stories": rows, "pager": map[string]any{"pageID": 1, "recTotal": len(rows)}})
		default:
			t.Error("unexpected source method", r.URL.Query().Get("f"))
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	if e = s.UpsertConnection(ResourceConnection{Name: "issues", Kind: "zentao", Endpoint: server.URL + "/zentao", Username: "reader", Password: "fixture", Config: map[string]string{"product_ids": "1,2"}}); e != nil {
		t.Fatal(e)
	}
	f := researchLiveFilter{Connection: "issues", WorkItemFilter: WorkItemFilter{Kind: "requirement", Page: 1, PageSize: 20, Order: "updated_desc"}}
	first, e := s.ResearchLivePage(context.Background(), "alice", f)
	if e != nil || len(first.Items) != 20 || first.Items[0].ExternalID != "40" || first.Items[19].ExternalID != "21" || !first.HasMore {
		t.Fatal(first, e)
	}
	f.Page = 2
	f.Cursor = first.NextCursor
	second, e := s.ResearchLivePage(context.Background(), "alice", f)
	if e != nil || len(second.Items) != 20 || second.Items[0].ExternalID != "20" || second.Items[19].ExternalID != "1" || second.HasMore {
		t.Fatal(second, e)
	}
	if len(s.WorkItems("", "", 100)) != 0 {
		t.Fatal("query imported requirements")
	}
}
