package deploy

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDocumentLineagePersistsOriginsAndFrozenRequirementDeltas(t *testing.T) {
	st, h := apiTestingFixture(t)
	sv := st.GetService("orders")
	w := WorkItem{Source: "manual", Kind: "requirement", Title: "新增状态"}
	w2 := WorkItem{Source: "manual", Kind: "bug", Title: "修改状态"}
	st.CreateWorkItem(&w)
	st.CreateWorkItem(&w2)
	base := `{"openapi":"3.0.3","info":{"title":"订单","version":"1"},"paths":{"/a":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object","properties":{"id":{"type":"integer","example":9223372036854775807}}}}}}}}}}}`
	first, e := st.recordAPIDocument(sv, APIDocumentSnapshot{Environment: "test"}, []byte(base))
	if e != nil {
		t.Fatal(e)
	}
	next := strings.Replace(base, `"id":{"type":"integer","example":9223372036854775807}`, `"id":{"type":"integer","example":9223372036854775807},"status":{"type":"string"}`, 1)
	save := func(raw string, ref *WorkItem, baseline int64) int64 {
		t.Helper()
		var context *DocumentChangeContext
		if ref != nil {
			context, _ = st.resolveDocumentChanges(&DocumentChangeContext{WorkItem: &DocumentRequirement{ID: ref.ID}}, []byte(raw))
		}
		id, e := st.recordAPIDocument(sv, APIDocumentSnapshot{Environment: "test", Provider: "openapi-edit", ChangeContext: context}, []byte(raw), baseline)
		if e != nil {
			t.Fatal(e)
		}
		return id
	}
	second := save(next, &w, first)
	third := save(strings.Replace(next, `"status":{"type":"string"}`, `"status":{"type":"integer"}`, 1), &w2, second)
	line, e := loadDocumentLineage(st.db, third)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, o := range line.Origins {
		if o.Field != nil && o.Field.Name == "status" {
			found = true
			if o.FirstSnapshot != second || o.IntroducedBy.ID != w.ID || o.LastSnapshot != third || o.LastWorkItem.ID != w2.ID {
				t.Fatal(o)
			}
		}
	}
	if !found {
		t.Fatal("missing origin")
	}
	old, _ := loadDocumentLineage(st.db, second)
	for _, o := range old.Origins {
		if o.Field != nil && o.Field.Name == "status" && o.LastSnapshot != second {
			t.Fatal("rewrote immutable lineage")
		}
	}
	r := httptest.NewRequest("GET", fmt.Sprintf("/api/api-documents/work-item-changes?work_item_id=%d&service=orders&environment=test", w2.ID), nil)
	out := httptest.NewRecorder()
	h.ServeHTTP(out, r)
	if out.Code != 200 || !strings.Contains(out.Body.String(), `"change":"modified"`) || !strings.Contains(out.Body.String(), `"baseline_id":`+fmt.Sprint(second)) {
		t.Fatal(out.Code, out.Body.String())
	}
	r = httptest.NewRequest("GET", fmt.Sprintf("/api/api-documents/work-item-changes?work_item_id=%d&service=orders&environment=prod", w2.ID), nil)
	out = httptest.NewRecorder()
	h.ServeHTTP(out, r)
	if out.Code != 200 || !strings.Contains(out.Body.String(), `"items":[]`) {
		t.Fatal(out.Body.String())
	}
	fourth := save(base, &w2, third)
	line, _ = loadDocumentLineage(st.db, fourth)
	for _, o := range line.Origins {
		if o.Field != nil && o.Field.Name == "status" {
			t.Fatal("removed field is current")
		}
	}
	fifth := save(next, &w2, fourth)
	line, _ = loadDocumentLineage(st.db, fifth)
	for _, o := range line.Origins {
		if o.Field != nil && o.Field.Name == "status" && o.FirstSnapshot != fifth {
			t.Fatal("recreated field inherited deleted origin")
		}
	}
	// A legacy baseline without lineage stays unknown; no historical backfill.
	st.db.Exec(`DELETE FROM api_document_lineage WHERE snapshot_id=?`, fifth)
	sixth := save(strings.Replace(next, `"title":"订单"`, `"title":"订单2"`, 1), nil, fifth)
	line, _ = loadDocumentLineage(st.db, sixth)
	if !line.LegacyUnknown {
		t.Fatal("legacy unknown missing")
	}
	for _, o := range line.Origins {
		if o.Field != nil && o.Field.Name == "status" && (o.FirstSnapshot != 0 || o.IntroducedBy != nil) {
			t.Fatal("invented old origin")
		}
	}
	raw, _ := json.Marshal(line)
	if strings.Contains(string(raw), `9223372036854776000`) {
		t.Fatal("precision lost")
	}
}

func TestDocumentRevisionIdentityMigrationPreservesHistoryAndSequence(t *testing.T) {
	st, _ := apiTestingFixture(t)
	raw := `{"openapi":"3.0.3","info":{"title":"订单","version":"1"},"paths":{}}`
	sv := st.GetService("orders")
	id, e := st.recordAPIDocument(sv, APIDocumentSnapshot{Environment: "test"}, []byte(raw))
	if e != nil {
		t.Fatal(e)
	}
	var ddl string
	if e = st.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='api_document_versions'`).Scan(&ddl); e != nil {
		t.Fatal(e)
	}
	legacy := strings.Replace(ddl, "api_document_versions", "api_document_versions_old", 1)
	legacy = strings.Replace(legacy, ",digest,baseline_id)", ",digest)", 1)
	for _, q := range []string{legacy, `INSERT INTO api_document_versions_old SELECT * FROM api_document_versions`, `DROP TABLE api_document_versions`, `ALTER TABLE api_document_versions_old RENAME TO api_document_versions`, `UPDATE sqlite_sequence SET seq=90 WHERE name='api_document_versions'`} {
		if _, e = st.db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	if e = st.ensureDocumentRevisionIdentity(); e != nil {
		t.Fatal(e)
	}
	original, e := st.apiDocumentSnapshot(sv.ApplicationID, id)
	if e != nil || string(original.Document) != raw {
		t.Fatal("history changed", original, e)
	}
	// Reopening/migrating twice must be harmless.
	if e = st.ensureDocumentRevisionIdentity(); e != nil {
		t.Fatal(e)
	}
	next, e := st.recordAPIDocument(sv, APIDocumentSnapshot{Environment: "test", Provider: "openapi-edit"}, []byte(strings.Replace(raw, `"version":"1"`, `"version":"2"`, 1)), id)
	if e != nil || next <= 90 {
		t.Fatal("sequence reused", next, e)
	}
	restored, e := st.recordAPIDocument(sv, APIDocumentSnapshot{Environment: "test", Provider: "openapi-edit"}, []byte(raw), next)
	if e != nil || restored <= next {
		t.Fatal("restore failed", restored, e)
	}
	var count int
	st.db.QueryRow(`SELECT COUNT(*) FROM api_document_lineage WHERE snapshot_id=?`, id).Scan(&count)
	if count != 1 {
		t.Fatal("lost lineage")
	}
}
