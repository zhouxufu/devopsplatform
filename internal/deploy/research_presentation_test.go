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

func TestResearchCommentImagesAreAtomicAndOwned(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	item := &WorkItem{Source: "manual", Kind: "bug", Title: "test", Owner: "alice"}
	if err = s.CreateWorkItem(item); err != nil {
		t.Fatal(err)
	}
	own, foreign := strings.Repeat("a", 32), strings.Repeat("b", 32)
	for file, owner := range map[string]string{own: "alice", foreign: "bob"} {
		if _, err = s.db.Exec(`INSERT INTO work_item_attachments(id,owner,name,mime,size,created_at) VALUES(?,?,?,'image/png',100,1)`, file, owner, "test.png"); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.AddResearchComment(item.ID, "alice", "", []string{own, foreign}); err == nil {
		t.Fatal("foreign image accepted")
	}
	var bound int64
	s.db.QueryRow(`SELECT work_item_id FROM work_item_attachments WHERE id=?`, own).Scan(&bound)
	if bound != 0 {
		t.Fatal("failed comment bound image")
	}
	activity, _ := s.ResearchActivities(item.ID)
	if len(activity) != 1 || activity[0].Kind != "created" {
		t.Fatal("failed comment was recorded")
	}
	if err = s.AddResearchComment(item.ID, "alice", "", []string{own}); err != nil {
		t.Fatal("image-only comment failed", err)
	}
	activity, _ = s.ResearchActivities(item.ID)
	if len(activity) != 2 || activity[0].Kind != "comment" || !strings.Contains(activity[0].Content, "attachment:"+own) {
		t.Fatal("image reference missing", activity)
	}
	s.db.QueryRow(`SELECT work_item_id FROM work_item_attachments WHERE id=?`, own).Scan(&bound)
	if bound != item.ID {
		t.Fatal("comment image not bound")
	}
	if err = s.AddResearchComment(item.ID, "alice", "", nil); err == nil {
		t.Fatal("empty comment accepted")
	}
	if err = s.AddResearchComment(item.ID, "alice", "text", []string{own, own}); err == nil {
		t.Fatal("duplicate image accepted")
	}
}

func TestResearchDetailUsesNativeResolutionOptionsWithoutWrites(t *testing.T) {
	nativeWrites := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Query().Get("f")
		var data any
		switch method {
		case "getSessionID":
			data = map[string]string{"sessionName": "zentaosid", "sessionID": "test"}
		case "login":
			data = map[string]any{"user": map[string]string{"account": "alice"}}
		default:
			if r.Method != "GET" {
				nativeWrites++
				t.Error("detail triggered native write")
			}
			remote := map[string]any{"bug": map[string]any{"id": "7", "product": "1", "title": "test", "status": "resolved", "resolution": "fixed2"}, "actions": map[string]any{}}
			if method == "resolve" {
				remote["resolutionList"] = map[string]string{"fixed2": "前端代码修复"}
			}
			data = remote
		}
		raw, _ := json.Marshal(data)
		json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": string(raw)})
	}))
	defer server.Close()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.UpsertConnection(ResourceConnection{Name: "issues", Kind: "zentao", Endpoint: server.URL + "/zentao", Config: map[string]string{"product_ids": "1"}}); err != nil {
		t.Fatal(err)
	}
	item := &WorkItem{Source: "zentao:issues", Kind: "bug", Title: "test", ExternalID: "7", Owner: "alice"}
	if err = s.CreateWorkItem(item); err != nil {
		t.Fatal(err)
	}
	if err = s.AuthorizeResearch(context.Background(), "issues", "alice", ResearchCredential{Account: "alice", Password: "test"}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	registerResearchRoutes(mux, s, nil, nil)
	req := httptest.NewRequest("GET", fmt.Sprintf("/api/research/work-items/%d", item.ID), nil)
	req.Header.Set("X-User", "alice")
	out := httptest.NewRecorder()
	mux.ServeHTTP(out, req)
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
	var detail struct {
		Item   WorkItem
		Remote ResearchRemote
	}
	if err = json.Unmarshal(out.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Item.Resolution != "fixed2" || detail.Item.ResolutionLabel != "前端代码修复" || detail.Remote.Resolutions["fixed2"] != "前端代码修复" {
		t.Fatal("custom resolution lost", out.Body.String())
	}
	if nativeWrites != 0 {
		t.Fatal("source was modified")
	}
}
