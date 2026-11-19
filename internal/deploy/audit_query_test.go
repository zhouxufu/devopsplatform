package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuditSearchFullHistoryPaginationAndPrivacy(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err = st.db.Exec("DELETE FROM audit_events"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 137; i++ {
		st.Audit("admin", "save", "connection", fmt.Sprintf("db-%03d", i), map[string]string{"password": "secret-should-never-be-searched"})
	}
	st.Audit("operator", "delete", "host", "literal_100%", nil)
	if _, err = st.db.Exec("UPDATE audit_events SET created_at=100 WHERE resource_id='db-000'"); err != nil {
		t.Fatal(err)
	}
	query := func(raw string) auditPage {
		t.Helper()
		q, e := parseAuditQuery(httptest.NewRequest("GET", "/api/audit/page?"+raw, nil))
		if e != nil {
			t.Fatal(e)
		}
		v, e := st.queryAudit(context.Background(), q)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	all := query("page=1&page_size=20")
	if all.Total != 138 || len(all.Items) != 20 || len(all.ResourceTypes) != 2 {
		t.Fatalf("bad page: %+v", all)
	}
	last := query("page=7&page_size=20")
	if len(last.Items) != 18 || last.Items[17].ResourceID != "db-000" {
		t.Fatalf("old history missing: %+v", last)
	}
	old := query("page=1&q=db-000&start=1970-01-01T00:01:00Z&end=1970-01-01T00:02:00Z")
	if old.Total != 1 || old.Items[0].Detail != nil {
		t.Fatalf("old filter/privacy: %+v", old)
	}
	if query("q=secret-should-never-be-searched").Total != 0 {
		t.Fatal("searched private details")
	}
	if query("q=literal_100%25").Total != 1 {
		t.Fatal("literal wildcards not escaped")
	}
	if query("resource_type=host&resource_id=literal_100%25").Total != 1 {
		t.Fatal("exact resource filter failed")
	}
	if query("q=%27%20OR%201%3D1%20--").Total != 0 {
		t.Fatal("query escaped SQL binding")
	}
	if query("page=999").Total != 138 || len(query("page=999").Items) != 0 {
		t.Fatal("out of range page")
	}
}
func TestAuditSearchRejectsInvalidFilters(t *testing.T) {
	for _, raw := range []string{"page=-1", "page_size=101", "page_size=no", "start=bad", "start=2026-10-29T00:00:00Z&end=2026-10-28T00:00:00Z"} {
		if _, err := parseAuditQuery(httptest.NewRequest("GET", "/api/audit/page?"+raw, nil)); err == nil {
			t.Fatal("accepted invalid filters", raw)
		}
	}
}

func TestAuditSearchHTTPContract(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.Audit("tester", "save", "host", "fixture", map[string]string{"secret": "hidden"})
	mux := http.NewServeMux()
	registerAuditSearch(mux, st, func(w http.ResponseWriter, v any) { json.NewEncoder(w).Encode(v) }, func(w http.ResponseWriter, status int, e any) {
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprint(e)})
	})
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest("GET", "/api/audit/page?resource_type=host&resource_id=fixture", nil))
	var result auditPage
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Total != 1 || result.Items[0].Detail != nil {
		t.Fatal(response.Code, response.Body.String())
	}
	invalid := httptest.NewRecorder()
	mux.ServeHTTP(invalid, httptest.NewRequest("GET", "/api/audit/page?page=0", nil))
	if invalid.Code != 400 {
		t.Fatal(invalid.Code)
	}
	if len(st.AuditEvents(100)) == 0 {
		t.Fatal("legacy history API changed")
	}
}
