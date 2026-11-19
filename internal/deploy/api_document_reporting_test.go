package deploy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func documentReportRequest(h http.Handler, method, path, user, role string, raw []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
	req.Header.Set("X-User", user)
	req.Header.Set("X-Role", role)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestDocumentReportPreviewAndAtomicBaseline(t *testing.T) {
	st, h := apiTestingFixture(t)
	preview := documentReportRequest(h, "POST", "/api/api-documents/import-preview?service=orders&environment=test", "alice", "admin", []byte(apiDocumentFixture))
	if preview.Code != 200 || !strings.Contains(preview.Body.String(), `"added":1`) {
		t.Fatal(preview.Code, preview.Body.String())
	}
	var count int
	st.db.QueryRow(`SELECT COUNT(*) FROM api_document_versions`).Scan(&count)
	if count != 0 {
		t.Fatal("preview persisted document")
	}
	for _, path := range []string{"/api/api-documents/import-preview?service=orders&environment=missing", "/api/api-documents/import-preview?service=orders"} {
		w := documentReportRequest(h, "POST", path, "alice", "admin", []byte(apiDocumentFixture))
		if w.Code != 400 {
			t.Fatal(path, w.Code)
		}
	}
	if w := documentReportRequest(h, "POST", "/api/api-documents/import-preview?service=orders&environment=test", "alice", "member", []byte(apiDocumentFixture)); w.Code != 403 {
		t.Fatal(w.Code)
	}
	// Two independently confirmed requests with baseline 0 cannot both change the store.
	statuses := make(chan int, 2)
	var wg sync.WaitGroup
	for _, desc := range []string{"A", "B"} {
		wg.Add(1)
		go func(desc string) {
			defer wg.Done()
			w := documentReportRequest(h, "POST", "/api/api-documents/import?service=orders&environment=test&expected_baseline_id=0", "alice", "admin", []byte(strings.Replace(apiDocumentFixture, `"description":"ok"`, fmt.Sprintf(`"description":%q`, desc), 1)))
			statuses <- w.Code
		}(desc)
	}
	wg.Wait()
	close(statuses)
	counts := map[int]int{}
	for status := range statuses {
		counts[status]++
	}
	if counts[200] != 1 || counts[409] != 1 {
		t.Fatal(counts)
	}
	st.db.QueryRow(`SELECT COUNT(*) FROM api_document_versions`).Scan(&count)
	if count != 1 {
		t.Fatal(count)
	}
	// A generated snapshot is reusable without losing its build provenance.
	sv := st.GetService("orders")
	id, e := st.recordAPIDocument(sv, APIDocumentSnapshot{Environment: "test", RunID: 123, ArtifactID: 456, Commit: "fixture-commit", Provider: "smart-doc"}, []byte(apiDocumentFixture))
	if e != nil {
		t.Fatal(e)
	}
	w := documentReportRequest(h, "POST", fmt.Sprintf("/api/api-documents/import?service=orders&environment=test&expected_baseline_id=%d", id), "alice", "admin", []byte("\n"+apiDocumentFixture))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"duplicate":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	snap, _ := st.apiDocumentSnapshot(sv.ApplicationID, id)
	if snap.RunID != 123 || snap.ArtifactID != 456 || snap.Commit != "fixture-commit" {
		t.Fatal("provenance changed", snap)
	}
}
func TestDocumentReportInvalidBodyAndStaleNoInsert(t *testing.T) {
	st, h := apiTestingFixture(t)
	for _, raw := range []string{"{}", apiDocumentFixture + "{}", strings.Repeat("a", (8<<20)+1)} {
		for _, path := range []string{"/api/api-documents/import-preview?service=orders&environment=test", "/api/api-documents/import?service=orders&environment=test&expected_baseline_id=0"} {
			w := documentReportRequest(h, "POST", path, "alice", "admin", []byte(raw))
			if w.Code != 400 {
				t.Fatal(w.Code)
			}
		}
	}
	for _, base := range []string{"-1", "bad", "9223372036854775808", "7"} {
		req := httptest.NewRequest(http.MethodPost, "/api/api-documents/import?service=orders&environment=test&expected_baseline_id="+base, strings.NewReader(apiDocumentFixture))
		req.Header.Set("X-User", "alice")
		req.Header.Set("X-Role", "admin")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 400 && w.Code != 409 {
			t.Fatal(w.Code)
		}
	}
	var count int
	st.db.QueryRow(`SELECT COUNT(*) FROM api_document_versions`).Scan(&count)
	if count != 0 {
		t.Fatal("invalid report inserted")
	}
}
