package deploy

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIndependentChangeBatchAtomic(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.UpsertService(Service{Name: "api"})
	st.UpsertService(Service{Name: "web"})
	w := &WorkItem{Title: "Bug", Owner: "alice"}
	st.CreateWorkItem(w)
	bad := &WorkDeliverySave{Changes: []Change{{Kind: "manual", Title: "must rollback"}, {Kind: "code", Title: "bad", Service: "missing", Reference: "main"}}}
	if st.CreateChangeBatch(bad, "alice") == nil {
		t.Fatal("invalid application accepted")
	}
	if len(st.Changes("", "", "", 100)) != 0 {
		t.Fatal("partial batch retained")
	}
	req := &WorkDeliverySave{AlsoWorkItemIDs: []int64{w.ID}, Changes: []Change{{Kind: "code", Title: "api", Service: "api", Reference: "feature/menu"}, {Kind: "code", Title: "web", Service: "web", Reference: "feature/menu"}, {Kind: "manual", Title: "check"}}}
	if err = st.CreateChangeBatch(req, "alice"); err != nil {
		t.Fatal(err)
	}
	for _, c := range req.Changes {
		got := st.GetChange(c.ID)
		if got.Status != "draft" || got.Owner != "alice" || len(got.WorkItems) != 1 {
			t.Fatal(got)
		}
	}
	if st.CreateChangeBatch(req, "alice") == nil {
		t.Fatal("existing changes accepted as new")
	}
	frozen := &WorkDeliverySave{Changes: []Change{{Kind: "data-sync", Title: "spoof"}}}
	if st.CreateChangeBatch(frozen, "alice") == nil {
		t.Fatal("unfrozen sync accepted")
	}
	broken := &WorkDeliverySave{AlsoWorkItemIDs: []int64{999999}, Changes: []Change{{Kind: "manual", Title: "no link"}}}
	if st.CreateChangeBatch(broken, "alice") == nil {
		t.Fatal("missing link accepted")
	}
	if len(st.Changes("", "", "", 100)) != 3 {
		t.Fatal("invalid request changed records")
	}
}

func TestWorkImagesBindingAndFormats(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { st.Close() }()
	mux := Routes(st, NewBuilder(st, dir, NewLogHub()), NewLogHub(), "", NewStepLib(dir+"/steps", dir+"/custom"))
	upload := func(data []byte, owner string) *httptest.ResponseRecorder {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		f, _ := writer.CreateFormFile("file", "test.png")
		f.Write(data)
		writer.Close()
		r := httptest.NewRequest("POST", "/api/work-item-attachments", &body)
		r.Header.Set("Content-Type", writer.FormDataContentType())
		r.Header.Set("X-User", owner)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	var img bytes.Buffer
	png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	if w := upload([]byte(`<svg onload="alert(1)"></svg>`), "alice"); w.Code != 400 {
		t.Fatal("active file accepted", w.Code)
	}
	refs := []string{}
	for i := 0; i < 2; i++ {
		w := upload(img.Bytes(), "alice")
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		var v map[string]any
		json.Unmarshal(w.Body.Bytes(), &v)
		refs = append(refs, v["reference"].(string))
	}
	get := func(id, user string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/work-item-attachments/"+id, nil)
		r.Header.Set("X-User", user)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	id := strings.TrimPrefix(refs[0], "attachment:")
	if get(id, "bob").Code != 403 {
		t.Fatal("draft exposed")
	}
	bad := &WorkItem{Title: "steal", Owner: "bob", Description: refs[0], DescriptionFormat: "markdown"}
	if st.CreateWorkItem(bad) == nil {
		t.Fatal("foreign attachment linked")
	}
	if len(st.WorkItems("", "", 100)) != 0 {
		t.Fatal("invalid description partly saved")
	}
	item := &WorkItem{Title: "images", Owner: "alice", DescriptionFormat: "markdown", Description: "# Heading\n![one](" + refs[0] + ")\n![two](" + refs[1] + ")"}
	if err = st.CreateWorkItem(item); err != nil {
		t.Fatal(err)
	}
	got := st.GetWorkItem(item.ID)
	if got.DescriptionFormat != "markdown" || got.Description != item.Description {
		t.Fatal("format or description lost")
	}
	w := get(id, "bob")
	if w.Code != 200 || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Content-Type") != "image/png" {
		t.Fatal(w.Code, w.Header())
	}
	bad.Owner = "alice"
	if st.CreateWorkItem(bad) == nil {
		t.Fatal("attachment moved to another item")
	}
	legacy := &WorkItem{Title: "legacy", Description: "<p>Rich text</p>"}
	if st.CreateWorkItem(legacy) != nil || st.GetWorkItem(legacy.ID).DescriptionFormat != "" {
		t.Fatal("legacy format altered")
	}
	item.DescriptionFormat = "script"
	if st.UpdateWorkItem(item) == nil {
		t.Fatal("unknown format accepted")
	}
	st.Close()
	st, err = Open(dir)
	if err != nil {
		t.Fatal("reopening store", err)
	}
	if st.GetWorkItem(item.ID).DescriptionFormat != "markdown" {
		t.Fatal("migration not persistent")
	}
}
