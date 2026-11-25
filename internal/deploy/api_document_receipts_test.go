package deploy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func receiptSubmit(t *testing.T, h http.Handler, raw, reportID string, preview bool, extra string) DocumentReport {
	t.Helper()
	action := "import"
	if preview {
		action += "-preview"
	}
	w := documentReportRequest(h, "POST", "/api/api-documents/"+action+"?service=orders&environment=test&report_id="+reportID+extra, "alice", "admin", []byte(raw))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result struct {
		Report DocumentReport `json:"report"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	return result.Report
}
func TestDocumentReceiptsIndependentAttemptsAndFrozenComparison(t *testing.T) {
	s, h := apiTestingFixture(t)
	a := receiptSubmit(t, h, apiDocumentFixture, strings.Repeat("a", 32), false, "")
	changed := strings.Replace(apiDocumentFixture, `"description":"ok"`, `"description":"updated"`, 1)
	b := receiptSubmit(t, h, changed, strings.Repeat("b", 32), false, "")
	c := receiptSubmit(t, h, changed, strings.Repeat("c", 32), false, "")
	d := receiptSubmit(t, h, apiDocumentFixture, strings.Repeat("d", 32), false, "")
	if a.State != "saved" || b.State != "saved" || c.State != "unchanged" || d.State != "historical" || c.SnapshotID != b.SnapshotID || d.SnapshotID != a.SnapshotID || d.CurrentID != b.SnapshotID || b.BaselineID != a.SnapshotID {
		t.Fatal(a, b, c, d)
	}
	if c.Changes.Modified != 0 || d.Changes.Modified != 1 {
		t.Fatal("no-op/history summary incorrect", c, d)
	}
	var docs, receipts int
	s.db.QueryRow(`SELECT COUNT(*) FROM api_document_versions`).Scan(&docs)
	s.db.QueryRow(`SELECT COUNT(*) FROM api_document_reports`).Scan(&receipts)
	if docs != 2 || receipts != 4 {
		t.Fatal(docs, receipts)
	}
	w := documentReportRequest(h, "GET", "/api/api-document-reports/"+b.ID+"?service=orders&environment=test", "alice", "member", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"modified":1`) {
		t.Fatal(w.Code, w.Body)
	}
	w = documentReportRequest(h, "GET", "/api/api-document-reports/"+b.ID+"?service=other&environment=test", "alice", "member", nil)
	if w.Code != 404 {
		t.Fatal("cross application receipt", w.Code)
	}
	_, e := s.db.Exec(`UPDATE api_document_reports SET actor='bob' WHERE id=?`, d.ID)
	if e != nil {
		t.Fatal(e)
	}
	w = documentReportRequest(h, "GET", "/api/api-document-reports?mine=1&service=orders&environment=test", "alice", "member", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"total":3`) {
		t.Fatal(w.Code, w.Body)
	}
	other, e := Open(s.dataDir)
	if e != nil {
		t.Fatal(e)
	}
	defer other.Close()
	persisted, e := other.documentReport(c.ID)
	if e != nil || persisted.State != "unchanged" {
		t.Fatal(persisted, e)
	}
}
func TestDocumentReceiptsPreviewConfirmationReplayAndUnknown(t *testing.T) {
	s, h := apiTestingFixture(t)
	id := strings.Repeat("a", 32)
	p := receiptSubmit(t, h, apiDocumentFixture, id, true, "")
	if p.State != "preview" {
		t.Fatal(p)
	}
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM api_document_versions`).Scan(&count)
	if count != 0 {
		t.Fatal("preview saved document")
	}
	r := receiptSubmit(t, h, apiDocumentFixture, id, false, "&expected_baseline_id=0")
	if r.State != "saved" {
		t.Fatal(r)
	}
	replay := receiptSubmit(t, h, apiDocumentFixture, id, false, "&expected_baseline_id=0")
	if replay.SnapshotID != r.SnapshotID {
		t.Fatal("replayed write")
	}
	for _, tc := range []struct{ actor, raw, query string }{{"bob", apiDocumentFixture, ""}, {"alice", strings.Replace(apiDocumentFixture, "v1", "v2", 1), ""}, {"alice", apiDocumentFixture, "&environment=prod"}} {
		w := documentReportRequest(h, "POST", "/api/api-documents/import?service=orders&environment=test&report_id="+id+tc.query, tc.actor, "admin", []byte(tc.raw))
		if tc.query == "" && w.Code != 409 {
			t.Fatal("reused receipt accepted", w.Code)
		}
	}
	u := DocumentReport{ID: strings.Repeat("e", 32), ApplicationID: s.GetService("orders").ApplicationID, Service: "orders", Environment: "test", Protocol: "http", Actor: "alice", Source: "file", State: "processing", CreatedAt: 1, UpdatedAt: 1}
	if e := s.insertDocumentReport(u); e != nil {
		t.Fatal(e)
	}
	w := documentReportRequest(h, "GET", "/api/api-document-reports?state=uncertain", "alice", "member", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"total":1`) {
		t.Fatal(w.Code, w.Body)
	}
	w = documentReportRequest(h, "POST", "/api/api-document-reports/"+u.ID+"/cancel", "alice", "admin", nil)
	if w.Code != 409 {
		t.Fatal("uncertain write cancelled", w.Code)
	}
}
func TestDocumentReceiptsFailureExpiryCancellationAndConcurrency(t *testing.T) {
	s, h := apiTestingFixture(t)
	w := documentReportRequest(h, "POST", "/api/api-documents/import?service=orders&environment=test&report_id="+strings.Repeat("f", 32), "alice", "admin", []byte(`{"password":"do-not-store"}`))
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
	f, _ := s.documentReport(strings.Repeat("f", 32))
	if f.State != "failed" || strings.Contains(f.Error, "do-not-store") {
		t.Fatal(f)
	}
	id := strings.Repeat("a", 32)
	receiptSubmit(t, h, apiDocumentFixture, id, true, "")
	w = documentReportRequest(h, "POST", "/api/api-document-reports/"+id+"/cancel", "bob", "admin", nil)
	if w.Code != 403 {
		t.Fatal("administrator cancelled another person's preview")
	}
	w = documentReportRequest(h, "POST", "/api/api-document-reports/"+id+"/cancel", "alice", "admin", nil)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	id = strings.Repeat("b", 32)
	receiptSubmit(t, h, apiDocumentFixture, id, true, "")
	s.db.Exec(`UPDATE api_document_reports SET expires_at=? WHERE id=?`, time.Now().Unix()-1, id)
	w = documentReportRequest(h, "GET", "/api/api-document-reports?state=expired", "alice", "member", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"total":1`) {
		t.Fatal(w.Code, w.Body)
	}
	id = strings.Repeat("c", 32)
	statuses := make(chan int, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := documentReportRequest(h, "POST", "/api/api-documents/import?service=orders&environment=test&report_id="+id, "alice", "admin", []byte(apiDocumentFixture))
			statuses <- w.Code
		}()
	}
	wg.Wait()
	close(statuses)
	for status := range statuses {
		if status != 200 && status != 409 {
			t.Fatal(status)
		}
	}
	var docs, receipts int
	s.db.QueryRow(`SELECT COUNT(*) FROM api_document_versions`).Scan(&docs)
	s.db.QueryRow(`SELECT COUNT(*) FROM api_document_reports WHERE id=?`, id).Scan(&receipts)
	if docs != 1 || receipts != 1 {
		t.Fatal(docs, receipts)
	}
}
func TestDocumentOperationUpdateDoesNotUseWholeDocumentTimestamp(t *testing.T) {
	s, h := apiTestingFixture(t)
	sv := s.GetService("orders")
	raw := `{"openapi":"3.0.3","info":{"title":"Two endpoints","version":"1"},"paths":{"/a":{"get":{"responses":{"200":{"description":"unchanged"}}}},"/b":{"get":{"responses":{"200":{"description":"before"}}}}}}`
	a := receiptSubmit(t, h, raw, strings.Repeat("a", 32), false, "")
	s.db.Exec(`UPDATE api_document_versions SET created_at=100 WHERE id=?`, a.SnapshotID)
	b := receiptSubmit(t, h, strings.Replace(raw, "before", "after", 1), strings.Repeat("b", 32), false, "")
	s.db.Exec(`UPDATE api_document_versions SET created_at=200 WHERE id=?`, b.SnapshotID)
	snap, e := s.apiDocumentSnapshot(sv.ApplicationID, b.SnapshotID)
	if e != nil {
		t.Fatal(e)
	}
	info := s.documentUpdateInfo(snap)
	updates := info["operations"].(map[string]DocumentOperationUpdate)
	if updates["get /a"].ChangedAt != 100 || updates["get /b"].ChangedAt != 200 {
		t.Fatal(updates)
	}
	w := documentReportRequest(h, "GET", "/api/api-documents/search?service=orders&environment=test&sort=updated", "alice", "member", nil)
	var result documentSearchResult
	if e := json.Unmarshal(w.Body.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	if len(result.Items) != 2 || result.Items[0].Path != "/b" || result.Items[1].Update.ChangedAt != 100 {
		t.Fatal(w.Code, w.Body)
	}
	w = documentReportRequest(h, "GET", "/api/api-documents/operation-history?service=orders&environment=test&operation=get%20%2Fa", "alice", "member", nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), fmt.Sprintf(`"id":%d,"`, b.SnapshotID)) {
		t.Fatal(w.Code, w.Body)
	}
	s.db.Exec(`DELETE FROM api_document_lineage WHERE snapshot_id=?`, b.SnapshotID)
	s.db.Exec(`DELETE FROM api_document_operation_updates WHERE snapshot_id=?`, b.SnapshotID)
	if len(s.documentUpdateInfo(snap)["operations"].(map[string]DocumentOperationUpdate)) != 0 {
		t.Fatal("missing history invented update time")
	}
}

func TestDocumentReceiptKeepsLargeExamplesAndAtomicFailure(t *testing.T) {
	s, h := apiTestingFixture(t)
	raw := strings.Replace(apiDocumentFixture, `"description":"ok"`, `"description":"ok","content":{"application/json":{"schema":{"type":"integer"},"example":9007199254740993}}`, 1)
	id := strings.Repeat("a", 32)
	p := receiptSubmit(t, h, raw, id, true, "")
	if p.State != "preview" {
		t.Fatal(p)
	}
	saved := receiptSubmit(t, h, raw, id, false, "&expected_baseline_id=0")
	editID := strings.Repeat("b", 32)
	edited := strings.Replace(raw, "v1", "v2", 1)
	w := documentReportRequest(h, "POST", fmt.Sprintf("/api/api-documents/%d/edit-preview?service=orders&environment=test&report_id=%s", saved.SnapshotID, editID), "alice", "admin", []byte(edited))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "9007199254740993") {
		t.Fatal("receipt wrapper rounded example", w.Code, w.Body)
	}
	// Receipt write failure must roll back the snapshot, maintainer and lineage writes too.
	if _, e := s.db.Exec(`CREATE TRIGGER fail_receipt BEFORE UPDATE ON api_document_reports WHEN NEW.state='saved' BEGIN SELECT RAISE(ABORT,'fixture receipt failure'); END`); e != nil {
		t.Fatal(e)
	}
	w = documentReportRequest(h, "POST", "/api/api-documents/import?service=orders&environment=test&report_id="+strings.Repeat("c", 32), "alice", "admin", []byte(edited))
	if w.Code == 200 {
		t.Fatal("receipt failure reported success")
	}
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM api_document_versions`).Scan(&count)
	if count != 1 {
		t.Fatal("document saved without receipt", count)
	}
}

func TestDocumentReceiptFileImportReusesBuildSnapshotWithoutRelabeling(t *testing.T) {
	s, h := apiTestingFixture(t)
	sv := s.GetService("orders")
	id, e := s.recordAPIDocument(sv, APIDocumentSnapshot{Environment: "test", Provider: "smart-doc", RunID: 123, Commit: "original"}, []byte(apiDocumentFixture))
	if e != nil {
		t.Fatal(e)
	}
	v := receiptSubmit(t, h, apiDocumentFixture, strings.Repeat("a", 32), false, "")
	if v.State != "unchanged" || v.SnapshotID != id {
		t.Fatal(v)
	}
	stored, e := s.apiDocumentSnapshot(sv.ApplicationID, id)
	if e != nil || stored.Commit != "original" || stored.RunID != 123 {
		t.Fatal("file import changed original provenance", stored, e)
	}
}
