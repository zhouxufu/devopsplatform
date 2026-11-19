package deploy

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestDocumentManualEditingSnapshotCASAndRequirements(t *testing.T) {
	st, h := apiTestingFixture(t)
	sv := st.GetService("orders")
	before := `{"openapi":"3.0.3","info":{"title":"Orders","version":"1"},"paths":{"/orders":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object","properties":{"id":{"type":"integer","example":9223372036854775807}}}}}}}}}}}`
	base, e := st.recordAPIDocument(sv, APIDocumentSnapshot{Environment: "test", DefaultMaintainer: "alice", RunID: 99, ArtifactID: 88}, []byte(before))
	if e != nil {
		t.Fatal(e)
	}
	w1, w2 := WorkItem{Source: "manual", Kind: "requirement", Title: "订单需求"}, WorkItem{Source: "manual", Kind: "requirement", Title: "状态需求"}
	if e = st.CreateWorkItem(&w1); e != nil {
		t.Fatal(e)
	}
	if e = st.CreateWorkItem(&w2); e != nil {
		t.Fatal(e)
	}
	after := strings.Replace(before, `"id":{"type":"integer","example":9223372036854775807}`, `"id":{"type":"integer","example":9223372036854775807},"name":{"type":"string"},"state":{"type":"string"}`, 1)
	pointer := "#/paths/~1orders/get/responses/200/content/application~1json/schema/properties/state"
	context := &DocumentChangeContext{WorkItem: &DocumentRequirement{ID: w1.ID}, Fields: map[string]DocumentRequirement{pointer: {ID: w2.ID}}}
	submit := func(doc string, c *DocumentChangeContext) string {
		raw, _ := json.Marshal(c)
		return `{"document":` + doc + `,"change_context":` + string(raw) + `}`
	}
	call := func(id int64, action, env, role, body, extra string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("POST", fmt.Sprintf("/api/api-documents/%d/%s?service=orders&environment=%s%s", id, action, env, extra), strings.NewReader(body))
		r.Header.Set("X-User", "alice")
		r.Header.Set("X-Role", role)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: %d %s", action, w.Code, w.Body.String())
		}
		return w
	}
	call(base, "edit-preview", "prod", "admin", after, "", 400)
	call(base, "edit-preview", "test", "member", after, "", 403)
	preview := call(base, "edit-preview", "test", "admin", submit(after, context), "", 200)
	var v struct {
		Digest  string                  `json:"digest"`
		Context *DocumentChangeContext  `json:"change_context"`
		Fields  []DocumentFieldAddition `json:"field_additions"`
	}
	json.Unmarshal(preview.Body.Bytes(), &v)
	if len(v.Fields) != 2 || v.Context.WorkItem.Title != w1.Title {
		t.Fatal(preview.Body.String())
	}
	for _, f := range v.Fields {
		if f.Name == "state" && f.WorkItem.ID != w2.ID || f.Name == "name" && f.WorkItem.ID != w1.ID {
			t.Fatal(f)
		}
	}
	extra := fmt.Sprintf("&expected_baseline_id=%d&expected_digest=%s", base, url.QueryEscape(v.Digest))
	call(base, "edit", "test", "admin", submit(after, v.Context), "", 409)
	saved := call(base, "edit", "test", "admin", submit(after, v.Context), extra, 200)
	var result struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(saved.Body.Bytes(), &result)
	snap, e := st.apiDocumentSnapshot(sv.ApplicationID, result.ID)
	if e != nil || snap.ID <= base || snap.BaselineID != base || snap.RunID != 0 || snap.ArtifactID != 0 || snap.Provider != "openapi-edit" || snap.DefaultMaintainer != "" {
		t.Fatal(snap, e)
	}
	old, _ := st.apiDocumentSnapshot(sv.ApplicationID, base)
	if string(old.Document) != before || !strings.Contains(string(snap.Document), "9223372036854775807") {
		t.Fatal("history or numeric example changed")
	}
	additions, meta, e := st.snapshotFieldAdditions(snap)
	if e != nil || len(additions) != 2 || meta.Fields[pointer].ID != w2.ID {
		t.Fatal(additions, meta, e)
	}
	call(base, "edit", "test", "admin", submit(after, v.Context), extra, 409)
	// Restoring historical content manually is a new snapshot, never promotion or mutation of the old one.
	p := call(result.ID, "edit-preview", "test", "admin", before, "", 200)
	json.Unmarshal(p.Body.Bytes(), &v)
	restored := call(result.ID, "edit", "test", "admin", before, fmt.Sprintf("&expected_baseline_id=%d&expected_digest=%s", result.ID, v.Digest), 200)
	var next struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(restored.Body.Bytes(), &next)
	if next.ID <= result.ID {
		t.Fatal(restored.Body.String())
	}
	// Refusing unknown or non-added fields cannot create a context record.
	bad := submit(before, &DocumentChangeContext{Fields: map[string]DocumentRequirement{"#/fake": {ID: w1.ID}}})
	call(next.ID, "edit-preview", "test", "admin", bad, "", 400)
	var count int
	st.db.QueryRow(`SELECT COUNT(*) FROM api_document_change_contexts`).Scan(&count)
	if count != 1 {
		t.Fatal(count)
	}
}
func TestDocumentSearchMaintainerAccountAndName(t *testing.T) {
	st, h := apiTestingFixture(t)
	raw := `{"openapi":"3.0.3","info":{"title":"Docs","version":"1"},"paths":{"/a":{"get":{"x-maintainer":"alice","responses":{"200":{"description":"ok"}}}}}}`
	id, e := st.recordAPIDocument(st.GetService("orders"), APIDocumentSnapshot{Environment: "test"}, []byte(raw))
	if e != nil {
		t.Fatal(e)
	}
	for _, query := range []string{"q=Alice", "maintainer=alice", "maintainer=" + url.QueryEscape("艾丽丝")} {
		r := httptest.NewRequest("GET", "/api/api-documents/search?"+query, nil)
		r.Header.Set("X-User-Display-Names", `{"alice":"艾丽丝"}`)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var v documentSearchResult
		json.Unmarshal(w.Body.Bytes(), &v)
		if w.Code != 200 || v.Total != 1 || v.Items[0].MaintainerName != "艾丽丝" || v.Items[0].Maintainer != "alice" || v.Items[0].SnapshotID != id {
			t.Fatal(query, w.Body.String())
		}
	}
}
