package deploy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkItemSourcesRoutesAndAdapter(t *testing.T) {
	st, name := zentaoFixture(t, func(r *http.Request) (any, string) {
		return map[string]any{"bugs": map[string]any{"7": map[string]any{"id": "7", "title": "Generic source", "status": "closed", "closedDate": "2026-10-01 12:00:00"}}}, ""
	})
	mux := http.NewServeMux()
	registerIntegrationRoutes(mux, st)
	for _, path := range []string{"/api/work-item-sources", "/api/work-item-sync", "/api/issue-sync"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	for _, prefix := range []string{"/api/work-item-sync/", "/api/issue-sync/"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", prefix+name, strings.NewReader(`{"baseline":"2026-09-30","status":"closed"}`)))
		if w.Code != 400 {
			t.Fatalf("%s: %d %s", prefix, w.Code, w.Body.String())
		}
	}
	items := st.WorkItems("", "", 100)
	if len(items) != 0 {
		t.Fatalf("live source unexpectedly re-imported bugs: %+v", items)
	}
	if err := st.UpsertConnection(ResourceConnection{Name: "git-only", Kind: "git", Endpoint: "https://example.invalid"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.syncWorkItems(context.Background(), "git-only", "", "local", ""); err == nil || !strings.Contains(err.Error(), "尚未提供") {
		t.Fatalf("unsupported provider: %v", err)
	}
	if _, err := st.syncWorkItems(context.Background(), "missing", "", "local", ""); err == nil {
		t.Fatal("missing connection accepted")
	}
}
