package devtools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"logplatform/internal/deploy"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestWorkItemImageMCPRealDeployAndSourceRoutes(t *testing.T) {
	data := workImagePNG(t)
	var downloads, mutations atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		var result any
		switch q.Get("f") {
		case "getSessionID":
			result = map[string]string{"sessionName": "zentaosid", "sessionID": "fixture-session"}
		case "login":
			result = map[string]any{"user": map[string]string{"account": "alice"}}
		case "getModel":
			result = map[string]string{"id": "42", "objectType": "bug", "objectID": "7", "deleted": "0"}
		case "read":
			downloads.Add(1)
			w.Header().Set("Content-Type", "image/png")
			w.Write(data)
			return
		default:
			if r.Method != "GET" {
				mutations.Add(1)
			}
			result = map[string]any{"bug": map[string]any{"id": "7", "title": "图片隔离样例", "status": "active", "product": "1", "steps": `<p>说明</p><img src="{42.png}">`}, "actions": map[string]any{}, "users": map[string]string{"alice": "Alice"}}
		}
		raw, _ := json.Marshal(result)
		json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": string(raw)})
	}))
	defer source.Close()
	st, err := deploy.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err = st.UpsertConnection(deploy.ResourceConnection{Name: "fixture-images", Kind: "zentao", Endpoint: source.URL + "/zentao", Username: "sync", Password: "fixture-only", Config: map[string]string{"product_ids": "1"}}); err != nil {
		t.Fatal(err)
	}
	item := &deploy.WorkItem{Source: "zentao:fixture-images", ExternalID: "7", Kind: "bug", Title: "图片隔离样例", Status: "open", Owner: "alice"}
	if err = st.CreateWorkItem(item); err != nil {
		t.Fatal(err)
	}
	if err = st.AuthorizeResearch(context.Background(), "fixture-images", "alice", deploy.ResearchCredential{Account: "alice", Password: "fixture-only"}); err != nil {
		t.Fatal(err)
	}
	hub := deploy.NewLogHub()
	routes := deploy.Routes(st, deploy.NewBuilder(st, t.TempDir(), hub), hub, "", deploy.NewStepLib(t.TempDir()))
	var platformWrites atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			platformWrites.Add(1)
		}
		routes.ServeHTTP(w, r)
	}))
	defer up.Close()
	store, _ := OpenTokenStore(t.TempDir())
	token := workflowToken(t, store)
	svc := NewToolService(Upstreams{Deploy: up.URL})
	detail, _, _, err := svc.Call(context.Background(), token, "get_work_item", map[string]any{"work_item_id": item.ID})
	if err != nil {
		t.Fatal(err)
	}
	manifest := mapValue(mapValue(detail)["images"])
	items := manifest["items"].([]any)
	if len(items) != 1 || fmt.Sprint(mapValue(items[0])["file_id"]) != "42" || downloads.Load() != 0 {
		t.Fatal("discovery downloaded images", manifest)
	}
	img, _, _, err := svc.Call(context.Background(), token, "get_work_item_image", map[string]any{"work_item_id": item.ID, "file_id": 42})
	if err != nil {
		t.Fatal(err)
	}
	value, ok := img.(*workItemImageResult)
	if !ok || !bytes.Equal(value.Data, data) || value.WorkItemID != fmt.Sprint(item.ID) || value.FileID != "42" {
		t.Fatal("incorrect image", value)
	}
	if _, _, _, err = svc.Call(context.Background(), token, "get_work_item_image", map[string]any{"work_item_id": item.ID, "file_id": 43}); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatal("foreign image accepted", err)
	}
	if downloads.Load() != 1 || mutations.Load() != 0 || platformWrites.Load() != 0 {
		t.Fatal("unexpected side effects", downloads.Load(), mutations.Load(), platformWrites.Load())
	}
}
