package deploy

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func reportLinkWork(t *testing.T, s *Store, kind, title string) *WorkItem {
	t.Helper()
	w := &WorkItem{Kind: kind, Title: title, Owner: "alice", Source: "manual"}
	if e := s.CreateWorkItem(w); e != nil {
		t.Fatal(e)
	}
	return w
}
func submittedContext(raw string, id int64) string {
	return fmt.Sprintf(`{"document":%s,"change_context":{"work_item":{"id":%d}}}`, raw, id)
}

func TestReportLinksAutomaticContextAndDuplicateIsolation(t *testing.T) {
	s, h := apiTestingFixture(t)
	a := reportLinkWork(t, s, "requirement", "First request")
	b := reportLinkWork(t, s, "bug", "Follow-up bug")
	one := receiptSubmit(t, h, submittedContext(apiDocumentFixture, a.ID), strings.Repeat("a", 32), false, "")
	two := receiptSubmit(t, h, submittedContext(apiDocumentFixture, b.ID), strings.Repeat("b", 32), false, "")
	three := receiptSubmit(t, h, apiDocumentFixture, strings.Repeat("c", 32), false, "")
	if one.Association == nil || one.Association.Source != "reported" || one.Association.Items[0].ID != a.ID || two.Association.Items[0].ID != b.ID || len(three.Association.Items) != 0 {
		t.Fatal(one.Association, two.Association, three.Association)
	}
	if one.SnapshotID != two.SnapshotID || two.SnapshotID != three.SnapshotID {
		t.Fatal("association caused a new document")
	}
	context, e := s.documentChangeContext(one.SnapshotID)
	if e != nil || context.WorkItem.ID != a.ID {
		t.Fatal("duplicate rewrote original document provenance", context, e)
	}
	// MCP duplicate confirmation intentionally reuses the snapshot without old lineage.
	id := strings.Repeat("d", 32)
	preview := receiptSubmit(t, h, submittedContext(apiDocumentFixture, b.ID), id, true, "")
	confirmed := receiptSubmit(t, h, apiDocumentFixture, id, false, fmt.Sprintf("&expected_baseline_id=%d", one.SnapshotID))
	if preview.Association.Items[0].ID != b.ID || confirmed.Association.Items[0].ID != b.ID {
		t.Fatal("preview context lost during confirmation")
	}
}

func TestReportLinksManualSaveIsMetadataOnlyAndRequiresOwner(t *testing.T) {
	s, h := apiTestingFixture(t)
	a := reportLinkWork(t, s, "requirement", "Request")
	b := reportLinkWork(t, s, "bug", "Bug")
	r := receiptSubmit(t, h, submittedContext(apiDocumentFixture, a.ID), strings.Repeat("a", 32), false, "")
	before, _ := s.apiDocumentSnapshot(r.ApplicationID, r.SnapshotID)
	original, _ := json.Marshal(before)
	body := fmt.Sprintf(`{"revision":1,"work_item_ids":[%d]}`, b.ID)
	missing := documentReportRequest(h, "PUT", "/api/api-document-reports/"+r.ID+"/work-items", "alice", "member", []byte(`{"revision":1}`))
	if missing.Code != 400 {
		t.Fatal("omitted selection silently cleared associations", missing.Code)
	}
	for _, role := range []string{"member"} {
		w := documentReportRequest(h, "PUT", "/api/api-document-reports/"+r.ID+"/work-items", "mallory", role, []byte(body))
		if w.Code != 403 {
			t.Fatal("foreign member changed association", w.Code)
		}
	}
	w := documentReportRequest(h, "PUT", "/api/api-document-reports/"+r.ID+"/work-items", "alice", "member", []byte(body))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	links, _ := scanReportLinks(s.db.QueryRow(reportLinksSelect, r.ID))
	if links.Revision != 2 || links.Source != "manual" || links.Actor != "alice" || len(links.Items) != 1 || links.Items[0].ID != b.ID {
		t.Fatal(links)
	}
	if w = documentReportRequest(h, "PUT", "/api/api-document-reports/"+r.ID+"/work-items", "alice", "member", []byte(body)); w.Code != 409 {
		t.Fatal("duplicate write accepted")
	}
	after, _ := s.apiDocumentSnapshot(r.ApplicationID, r.SnapshotID)
	current, _ := json.Marshal(after)
	if string(original) != string(current) {
		t.Fatal("manual link modified immutable document")
	}
	context, _ := s.documentChangeContext(r.SnapshotID)
	if context.WorkItem.ID != a.ID {
		t.Fatal("manual report association rewrote field provenance")
	}
	var audits, docs, runs int
	s.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='link-work-items' AND resource_id=?`, r.ID).Scan(&audits)
	s.db.QueryRow(`SELECT COUNT(*) FROM api_document_versions`).Scan(&docs)
	s.db.QueryRow(`SELECT COUNT(*) FROM delivery_runs`).Scan(&runs)
	if audits != 1 || docs != 1 || runs != 0 {
		t.Fatal(audits, docs, runs)
	}
	if _, e := s.saveReportLinks(r.ID, "admin", true, 2, nil); e != nil {
		t.Fatal(e)
	}
	links, _ = scanReportLinks(s.db.QueryRow(reportLinksSelect, r.ID))
	if len(links.Items) != 0 || links.Revision != 3 {
		t.Fatal("explicit clear failed", links)
	}
}

func TestReportLinksValidationConflictAndAuditRollback(t *testing.T) {
	s, h := apiTestingFixture(t)
	w := reportLinkWork(t, s, "bug", "Bug")
	r := receiptSubmit(t, h, apiDocumentFixture, strings.Repeat("a", 32), false, "")
	for _, ids := range [][]int64{{w.ID, 999999}, {w.ID, w.ID}, {-1}} {
		if _, e := s.saveReportLinks(r.ID, "alice", false, 0, ids); e == nil {
			t.Fatal("invalid selection accepted", ids)
		}
	}
	links, _ := scanReportLinks(s.db.QueryRow(reportLinksSelect, r.ID))
	if links.Revision != 0 {
		t.Fatal("invalid selection partially saved")
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := s.saveReportLinks(r.ID, "alice", false, 0, []int64{w.ID})
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatal("CAS did not serialize association", success)
	}
	if _, e := s.db.Exec(`CREATE TRIGGER reject_link_audit BEFORE INSERT ON audit_events WHEN NEW.action='link-work-items' BEGIN SELECT RAISE(ABORT,'fixture audit failure'); END`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.saveReportLinks(r.ID, "alice", false, 1, nil); e == nil {
		t.Fatal("audit failure was ignored")
	}
	links, _ = scanReportLinks(s.db.QueryRow(reportLinksSelect, r.ID))
	if links.Revision != 1 || len(links.Items) != 1 {
		t.Fatal("association not rolled back", links)
	}
}

func TestReportLinksPatchPreviewRetainsExplicitContext(t *testing.T) {
	s, h := apiTestingFixture(t)
	w := reportLinkWork(t, s, "bug", "Patch request")
	original := receiptSubmit(t, h, apiDocumentFixture, strings.Repeat("a", 32), false, "")
	id := strings.Repeat("b", 32)
	body := fmt.Sprintf(`{"patches":[{"op":"add","path":"/paths/~1orders/get/summary","value":"Patched summary"}],"change_context":{"work_item":{"id":%d}}}`, w.ID)
	preview := documentReportRequest(h, "POST", fmt.Sprintf("/api/api-documents/%d/edit-preview?service=orders&environment=test&report_id=%s", original.SnapshotID, id), "alice", "admin", []byte(body))
	if preview.Code != 200 {
		t.Fatal(preview.Code, preview.Body)
	}
	var result struct {
		Report    DocumentReport         `json:"report"`
		Candidate json.RawMessage        `json:"candidate"`
		Context   *DocumentChangeContext `json:"change_context"`
		Digest    string                 `json:"digest"`
	}
	if e := json.Unmarshal(preview.Body.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	if result.Report.Association == nil || len(result.Report.Association.Items) != 1 || result.Report.Association.Items[0].ID != w.ID {
		t.Fatal(result.Report.Association)
	}
	candidate, _ := json.Marshal(map[string]any{"document": result.Candidate, "change_context": result.Context})
	saved := documentReportRequest(h, "POST", fmt.Sprintf("/api/api-documents/%d/edit?service=orders&environment=test&report_id=%s&expected_baseline_id=%d&expected_digest=%s", original.SnapshotID, id, original.SnapshotID, result.Digest), "alice", "admin", candidate)
	if saved.Code != 200 {
		t.Fatal(saved.Code, saved.Body)
	}
	links, _ := scanReportLinks(s.db.QueryRow(reportLinksSelect, id))
	if links.Items[0].ID != w.ID {
		t.Fatal(links)
	}
}
