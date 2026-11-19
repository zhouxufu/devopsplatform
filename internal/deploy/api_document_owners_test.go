package deploy

import (
	"encoding/json"
	"fmt"
	"logplatform/pkg/apidoc"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestNewDocumentMaintainerUsesDeclarationThenVerifiedKey(t *testing.T) {
	// The reporting client resolves an evidenced code author into x-maintainer.
	// Global contacts and arbitrary author prose must not override the Key owner.
	raw := []byte(`{"openapi":"3.0.3","info":{"title":"Owners","version":"1","contact":{"name":"Global contact"}},"paths":{"/declared":{"get":{"x-maintainer":"Code author","responses":{"200":{"description":"ok"}}}},"/fallback":{"get":{"description":"author: someone else","responses":{"200":{"description":"ok"}}}}}}`)
	_, ops, _, err := apidoc.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	withKey := nextDocumentMaintainers(ops, nil, "key-user")
	if withKey["get /declared"] != (DocumentMaintainer{"Code author", "explicit"}) || withKey["get /fallback"] != (DocumentMaintainer{"key-user", "mcp-key"}) {
		t.Fatal(withKey)
	}
	// Non-MCP imports without a verified Key retain their contact compatibility.
	withoutKey := nextDocumentMaintainers(ops, nil, "")
	if withoutKey["get /fallback"] != (DocumentMaintainer{"Global contact", "document"}) {
		t.Fatal(withoutKey)
	}
	previous := []apidoc.Operation{{ID: "get /fallback", Maintainer: "Original owner"}}
	inherited := nextDocumentMaintainers(ops, previous, "new-key-user")
	if inherited["get /fallback"] != (DocumentMaintainer{"Original owner", "inherited"}) {
		t.Fatal(inherited)
	}
}

func TestDocumentMaintainersInheritPerOperationAcrossUsersAndDefaults(t *testing.T) {
	st, h := apiTestingFixture(t)
	raw := `{"openapi":"3.0.3","info":{"title":"Owners","version":"1"},"paths":{"/old":{"get":{"responses":{"200":{"description":"ok"}}}}}}`
	request := func(path, user, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Header.Set("X-User", user)
		r.Header.Set("X-Role", "admin")
		r.Header.Set("X-MCP-Users", fmt.Sprintf(`[{"username":%q,"role":"admin","enabled":true}]`, user))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatal(w.Code, w.Body.String())
		}
		return w
	}
	save := func(env, user, body string) int64 {
		t.Helper()
		w := request("/api/api-documents/import?service=orders&environment="+env, user, body, 200)
		var v struct {
			ID int64 `json:"id"`
		}
		json.Unmarshal(w.Body.Bytes(), &v)
		return v.ID
	}
	owners := func(id int64) map[string]string {
		t.Helper()
		v, e := st.apiDocumentSnapshot(st.GetService("orders").ApplicationID, id)
		if e != nil {
			t.Fatal(e)
		}
		_, ops, _, e := apidoc.Parse(v.Document)
		if e != nil {
			t.Fatal(e)
		}
		m, e := loadDocumentMaintainers(st.db, id)
		if e != nil {
			t.Fatal(e)
		}
		if e = applyFrozenDocumentMaintainers(ops, m, v.DefaultMaintainer); e != nil {
			t.Fatal(e)
		}
		out := map[string]string{}
		for _, op := range ops {
			out[op.ID] = op.Maintainer
		}
		return out
	}
	first := save("test", "alice", raw)
	second := strings.Replace(raw, `"version":"1"`, `"version":"2","contact":{"name":"Document default"}`, 1)
	second = strings.Replace(second, `"paths":{`, `"paths":{"/new":{"get":{"responses":{"200":{"description":"ok"}}}},`, 1)
	preview := request("/api/api-documents/import-preview?service=orders&environment=test", "bob", second, 200)
	var p struct {
		Maintainers map[string]string          `json:"maintainers"`
		Sources     map[string]string          `json:"maintainer_sources"`
		Changes     []DocumentMaintainerChange `json:"maintainer_changes"`
		Digest      string                     `json:"maintainer_digest"`
	}
	json.Unmarshal(preview.Body.Bytes(), &p)
	if p.Maintainers["get /old"] != "alice" || p.Maintainers["get /new"] != "bob" || p.Sources["get /new"] != "mcp-key" || p.Sources["get /old"] != "inherited" || len(p.Changes) != 1 {
		t.Fatal(preview.Body.String())
	}
	next := save("test", "bob", second)
	if got := owners(next); got["get /old"] != "alice" || got["get /new"] != "bob" {
		t.Fatal(got)
	}
	// Explicit per-operation changes are intentional, then remain stable when omitted later.
	explicit := strings.Replace(second, `"/old":{"get":{`, `"/old":{"get":{"x-maintainer":"yuanliwei",`, 1)
	assigned := save("test", "bob", explicit)
	if owners(assigned)["get /old"] != "yuanliwei" {
		t.Fatal(owners(assigned))
	}
	omitted := strings.Replace(second, `"version":"2"`, `"version":"3"`, 1)
	kept := save("test", "carol", omitted)
	if owners(kept)["get /old"] != "yuanliwei" {
		t.Fatal(owners(kept))
	}
	// Duplicate lookup never rewrites the original owner map or promotes it.
	before, _ := loadDocumentMaintainers(st.db, first)
	same := save("test", "carol", raw)
	after, _ := loadDocumentMaintainers(st.db, first)
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if same != first || string(a) != string(b) {
		t.Fatal("duplicate reassigned old owner")
	}
	// Same method/path in another environment has independent ownership.
	production := save("prod", "bob", raw)
	if owners(production)["get /old"] != "bob" {
		t.Fatal(owners(production))
	}
	// Reuploading a legacy snapshot must retain its old contact-first rule.
	legacyContact := strings.Replace(raw, `"version":"1"`, `"version":"legacy","contact":{"name":"Legacy contact"}`, 1)
	legacyContact = strings.Replace(legacyContact, `"/old"`, `"/legacy-contact"`, 1)
	legacyContactID := save("prod", "alice", legacyContact)
	if _, err := st.db.Exec(`DELETE FROM api_document_maintainer_maps WHERE snapshot_id=?`, legacyContactID); err != nil {
		t.Fatal(err)
	}
	legacyPreview := request("/api/api-documents/import-preview?service=orders&environment=prod", "bob", legacyContact, 200)
	var legacyOwners struct {
		Maintainers map[string]string `json:"maintainers"`
	}
	if err := json.Unmarshal(legacyPreview.Body.Bytes(), &legacyOwners); err != nil || legacyOwners.Maintainers["get /legacy-contact"] != "Legacy contact" {
		t.Fatal(legacyPreview.Body.String())
	}
	if duplicate := save("prod", "bob", legacyContact); duplicate != legacyContactID || owners(duplicate)["get /legacy-contact"] != "Legacy contact" {
		t.Fatal("legacy duplicate changed ownership", duplicate)
	}
	// A legacy unknown owner stays unknown; uploading is not ownership evidence.
	legacyRaw := strings.Replace(raw, `"/old"`, `"/unknown"`, 1)
	legacy, e := st.recordAPIDocument(st.GetService("orders"), APIDocumentSnapshot{Environment: "dev", Provider: "legacy"}, []byte(legacyRaw))
	if e != nil {
		if err := st.UpsertEnvironment(Environment{Name: "dev", DisplayName: "开发"}); err != nil {
			t.Fatal(err)
		}
		legacy, e = st.recordAPIDocument(st.GetService("orders"), APIDocumentSnapshot{Environment: "dev", Provider: "legacy"}, []byte(legacyRaw))
		if e != nil {
			t.Fatal(e)
		}
	}
	st.db.Exec(`DELETE FROM api_document_maintainer_maps WHERE snapshot_id=?`, legacy)
	rewritten := save("dev", "bob", strings.Replace(legacyRaw, `"version":"1"`, `"version":"2"`, 1))
	if owners(rewritten)["get /unknown"] != "" {
		t.Fatal("guessed legacy owner", owners(rewritten))
	}
	// The persisted map matches preview; adding a new interface cannot switch owner between preview and commit.
	withoutContact := strings.Replace(omitted, `,"contact":{"name":"Document default"}`, "", 1)
	third := strings.Replace(withoutContact, `"paths":{`, `"paths":{"/new-key":{"get":{"responses":{"200":{"description":"ok"}}}},`, 1)
	w := request("/api/api-documents/import-preview?service=orders&environment=test", "bob", third, 200)
	json.Unmarshal(w.Body.Bytes(), &p)
	var latest int64
	st.db.QueryRow(`SELECT MAX(id) FROM api_document_versions WHERE service='orders' AND environment='test'`).Scan(&latest)
	path := fmt.Sprintf("/api/api-documents/import?service=orders&environment=test&expected_baseline_id=%d&expected_maintainer_digest=%s", latest, url.QueryEscape(p.Digest))
	request(path, "carol", third, 409)
	request(path, "bob", third, 200)
	// Search reads the same frozen map as detail and does not attribute old ops to current reporter.
	r := httptest.NewRequest("GET", "/api/api-documents/search?service=orders&environment=test&maintainer=yuanliwei", nil)
	out := httptest.NewRecorder()
	h.ServeHTTP(out, r)
	var result documentSearchResult
	json.Unmarshal(out.Body.Bytes(), &result)
	if result.Total != 1 || result.Items[0].Path != "/old" {
		t.Fatal(out.Body.String())
	}
}
