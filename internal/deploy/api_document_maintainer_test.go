package deploy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"logplatform/pkg/apidoc"
)

func TestDocumentKeyMaintainerSnapshotPrecedenceAndHistory(t *testing.T) {
	st, h := apiTestingFixture(t)
	request := func(path, user, key, raw string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(raw))
		r.Header.Set("X-User", user)
		r.Header.Set("X-Role", "admin")
		r.Header.Set("X-MCP-Users", fmt.Sprintf(`[{"username":%q,"role":"admin","enabled":true}]`, key))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		return w
	}
	raw := `{"openapi":"3.0.3","info":{"title":"Fixture","version":"1"},"paths":{"/explicit":{"get":{"x-maintainer":"负责人","responses":{"200":{"description":"ok"}}}},"/default":{"get":{"responses":{"200":{"description":"ok"}}}}}}`
	_, _, digest, _ := apidoc.Parse([]byte(raw))
	path := "/api/api-documents/import?service=orders&environment=test&expected_baseline_id=0"
	w := request(path, "alice", "alice", raw)
	var saved struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(w.Body.Bytes(), &saved)
	sv := st.GetService("orders")
	snap, e := st.apiDocumentSnapshot(sv.ApplicationID, saved.ID)
	if e != nil || snap.DefaultMaintainer != "alice" || string(snap.Document) != raw || snap.Digest != digest {
		t.Fatal("snapshot metadata/raw changed", snap, e)
	}
	read := documentReportRequest(h, "GET", fmt.Sprintf("/api/api-documents/%d?service=orders&environment=test", saved.ID), "bob", "admin", nil)
	var details struct {
		Operations []apidoc.Operation `json:"operations"`
	}
	json.Unmarshal(read.Body.Bytes(), &details)
	got := map[string]string{}
	for _, op := range details.Operations {
		got[op.Path] = op.Maintainer
	}
	if got["/explicit"] != "负责人" || got["/default"] != "alice" {
		t.Fatal(got)
	}
	search := documentReportRequest(h, "GET", "/api/api-documents/search?service=orders&environment=test", "bob", "admin", nil)
	var found documentSearchResult
	json.Unmarshal(search.Body.Bytes(), &found)
	for _, op := range found.Items {
		if op.Path == "/default" && op.Maintainer != "alice" {
			t.Fatal(op)
		}
	}
	preview := request("/api/api-documents/import-preview?service=orders&environment=test", "bob", "bob", raw)
	if !strings.Contains(preview.Body.String(), `"default_maintainer":"alice"`) || !strings.Contains(preview.Body.String(), `"duplicate":true`) {
		t.Fatal(preview.Body.String())
	}
	request(fmt.Sprintf("/api/api-documents/import?service=orders&environment=test&expected_baseline_id=%d", saved.ID), "bob", "bob", "\n"+raw)
	again, _ := st.apiDocumentSnapshot(sv.ApplicationID, saved.ID)
	if again.DefaultMaintainer != "alice" || string(again.Document) != raw || again.Digest != digest || again.CreatedAt != snap.CreatedAt {
		t.Fatal("duplicate rewrote history", again)
	}
	// Changing a document-wide default does not transfer existing operations.
	contact := strings.Replace(raw, `"version":"1"`, `"version":"2","contact":{"name":"文档负责人"}`, 1)
	request(fmt.Sprintf("/api/api-documents/import?service=orders&environment=test&expected_baseline_id=%d", saved.ID), "bob", "bob", contact)
	latest, _, err := st.documentBaseline(sv, "test")
	if err != nil {
		t.Fatal(err)
	}
	read = documentReportRequest(h, "GET", fmt.Sprintf("/api/api-documents/%v?service=orders&environment=test", latest["id"]), "alice", "admin", nil)
	json.Unmarshal(read.Body.Bytes(), &details)
	for _, op := range details.Operations {
		want := "alice"
		if op.Path == "/explicit" {
			want = "负责人"
		}
		if op.Maintainer != want {
			t.Fatal(op)
		}
	}
	// Upload without a verified MCP context cannot invent a default; legacy duplicate stays empty.
	legacyID, err := st.recordAPIDocument(sv, APIDocumentSnapshot{Environment: "prod", Provider: "legacy"}, []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	request(fmt.Sprintf("/api/api-documents/import?service=orders&environment=prod&expected_baseline_id=%d", legacyID), "bob", "bob", raw)
	legacy, _ := st.apiDocumentSnapshot(sv.ApplicationID, legacyID)
	if legacy.DefaultMaintainer != "" {
		t.Fatal("backfilled legacy", legacy)
	}
	var count int
	st.db.QueryRow(`SELECT COUNT(*) FROM api_document_maintainers`).Scan(&count)
	if count != 2 {
		t.Fatal("duplicate added owner metadata", count)
	}
}

func TestDocumentKeyMaintainerRejectsUntrustedOrUnboundIdentity(t *testing.T) {
	for _, tc := range []struct{ user, key, role string }{{"alice", "", "admin"}, {"alice", "mallory", "admin"}, {"local", "local", "admin"}, {"alice", "alice", "member"}, {" alice", " alice", "admin"}} {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.Header.Set("X-User", tc.user)
		r.Header.Set("X-MCP-Users", fmt.Sprintf(`[{"username":%q,"role":"admin","enabled":true}]`, tc.key))
		r.Header.Set("X-Role", tc.role)
		if documentKeyMaintainer(r) != "" {
			t.Fatal(tc)
		}
	}
}
