package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestResearchImageProxyRechecksSourceOwnershipAndProduct(t *testing.T) {
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	var product atomic.Int32
	product.Store(1)
	var downloads atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		var data any
		switch q.Get("f") {
		case "getSessionID":
			data = map[string]string{"sessionName": "zentaosid", "sessionID": "image-session"}
		case "login":
			data = map[string]any{"user": map[string]string{"account": "alice"}}
		case "getModel":
			owner := "8"
			if q.Get("params") == "fileID=42" {
				owner = "7"
			}
			data = map[string]string{"id": strings.TrimPrefix(q.Get("params"), "fileID="), "objectType": "bug", "objectID": owner, "deleted": "0"}
		case "read":
			downloads.Add(1)
			w.Header().Set("Content-Type", "image/png")
			w.Write(imageBytes.Bytes())
			return
		default:
			data = map[string]any{"bug": map[string]any{"id": "7", "title": "图片 Bug", "status": "active", "product": product.Load(), "steps": `<img src="{42.png}">`}, "actions": map[string]any{}, "users": map[string]string{"alice": "Alice"}}
		}
		raw, _ := json.Marshal(data)
		json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": string(raw)})
	}))
	defer source.Close()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.UpsertConnection(ResourceConnection{Name: "images", Kind: "zentao", Endpoint: source.URL + "/zentao", Username: "sync", Password: "fixture-only", Config: map[string]string{"product_ids": "1"}}); err != nil {
		t.Fatal(err)
	}
	item := &WorkItem{Source: "zentao:images", ExternalID: "7", Kind: "bug", Title: "图片 Bug", Status: "open", Owner: "alice"}
	if err = s.CreateWorkItem(item); err != nil {
		t.Fatal(err)
	}
	if err = s.AuthorizeResearch(context.Background(), "images", "platform-alice", ResearchCredential{Account: "alice", Password: "fixture-only"}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	registerResearchRoutes(mux, s, nil, nil)
	read := func(file, actor string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", fmt.Sprintf("/api/research/work-items/%d/images/%s", item.ID, file), nil)
		r.Header.Set("X-User", actor)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	if w := read("42", "platform-alice"); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), imageBytes.Bytes()) || w.Header().Get("Content-Type") != "image/png" {
		t.Fatal(w.Code, w.Body.String())
	}
	n := downloads.Load()
	if w := read("43", "platform-alice"); w.Code != 403 {
		t.Fatal("foreign file accepted", w.Code)
	}
	if downloads.Load() != n {
		t.Fatal("foreign file downloaded")
	}
	product.Store(2)
	if w := read("42", "platform-alice"); w.Code != 403 {
		t.Fatal("revoked product accepted", w.Code, w.Body.String())
	}
	if downloads.Load() != n {
		t.Fatal("revoked product downloaded")
	}
}
