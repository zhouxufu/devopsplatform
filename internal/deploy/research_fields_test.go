package deploy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResearchSourceFieldsCodesFilteringAndUnknownValues(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i, values := range []ResearchIssueFields{{Severity: "1", Priority: "1", Resolution: "backend_custom", ResolutionLabel: "后端代码修复", ReadAt: 1}, {Severity: "2", Priority: "3", Resolution: "frontend_custom", ReadAt: 1}, {Severity: "4", Priority: "2", Resolution: "mystery", ReadAt: 1}} {
		w := &WorkItem{Source: "manual", Title: []string{"a", "b", "c"}[i], Kind: "bug", Owner: "alice"}
		if err = s.CreateWorkItem(w); err != nil {
			t.Fatal(err)
		}
		tx, _ := s.db.Begin()
		if err = saveIssueFields(tx, w.ID, values); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.WorkItemsPage(WorkItemFilter{Severity: "1", Priority: "1", Resolution: "backend_custom"})
	if err != nil || page.Total != 1 || page.Items[0].ResolutionLabel != "后端代码修复" {
		t.Fatalf("field filter %+v %v", page, err)
	}
	page, err = s.WorkItemsPage(WorkItemFilter{Order: "priority_asc"})
	if err != nil || page.Items[1].Resolution != "mystery" {
		t.Fatalf("source-code sorting %+v %v", page, err)
	}
	if _, err = s.WorkItemsPage(WorkItemFilter{Order: "severity_asc;drop table work_items"}); err == nil {
		t.Fatal("SQL order injection accepted")
	}
	var bug zentaoBug
	if err = json.Unmarshal([]byte(`{"id":"7","severity":"1","pri":2,"resolution":"frontend_custom"}`), &bug); err != nil || bug.Priority != "2" || bug.Resolution != "frontend_custom" {
		t.Fatal("source fields lost", err)
	}
}

func TestResearchFieldRefreshIsBoundedReadAndKeepsBugMetadata(t *testing.T) {
	s, w, writes, mu := researchFixture(t)
	if _, err := s.db.Exec(`INSERT INTO external_work_items(connection,kind,external_id,work_item_id) VALUES('issues','bug','7',?)`, w.ID); err != nil {
		t.Fatal(err)
	}
	c := s.GetConnection("issues")
	c.Config["resolution_labels"] = `{"fixed":"后端代码修复"}`
	if err := s.UpsertConnection(*c); err != nil {
		t.Fatal(err)
	}
	// Populate a verified remote result through the fixture, then the read-only
	// refresh itself must not post another native Bug action.
	z, _, _ := s.researchClient(context.Background(), w, "platform-alice")
	remote, _ := z.researchRead(context.Background(), "7", "view")
	yes := true
	op, err := s.ApplyResearchAction(context.Background(), w.ID, "platform-alice", ResearchAction{OperationID: strings.Repeat("f", 32), Action: "resolve", ExpectedDigest: remote.Digest, Resolution: "fixed", ResolvedBuild: "trunk", Metadata: ResearchMetadata{ReleaseRequired: &yes}})
	if err != nil || op.State != "success" {
		t.Fatal(err)
	}
	// Refresh uses sync reader credentials, not native business writes. The
	// fixture intentionally rejects the sync login: all previous fields remain.
	mux := http.NewServeMux()
	registerResearchFieldRoutes(mux, s)
	request := httptest.NewRequest("POST", "/api/research/refresh-fields", strings.NewReader(`{"ids":[`+jsonNumber(w.ID)+`]}`))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != 502 {
		t.Fatal("failed read not reported", response.Code)
	}
	item := s.GetWorkItem(w.ID)
	if item.Resolution != "fixed" || item.ResolutionLabel != "后端代码修复" || item.MetadataRevision != 1 {
		t.Fatal("failed refresh overwrote data", item)
	}
	mu.Lock()
	count := *writes
	mu.Unlock()
	if count != 1 {
		t.Fatal("refresh wrote native Bug")
	}
	request = httptest.NewRequest("POST", "/api/research/refresh-fields", strings.NewReader(`{"ids":[1,1]}`))
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code < 400 {
		t.Fatal("duplicate IDs accepted")
	}
}

func TestResearchCustomResolutionRequiresExplicitSourceDictionary(t *testing.T) {
	s, w, _, _ := researchFixture(t)
	z, _, _ := s.researchClient(context.Background(), w, "platform-alice")
	remote, _ := z.researchRead(context.Background(), "7", "view")
	yes := true
	req := ResearchAction{OperationID: strings.Repeat("c", 32), Action: "resolve", ExpectedDigest: remote.Digest, Resolution: "backend_custom", ResolvedBuild: "trunk", Metadata: ResearchMetadata{ReleaseRequired: &yes}}
	if _, err := s.ApplyResearchAction(context.Background(), w.ID, "platform-alice", req); err == nil {
		t.Fatal("unconfigured custom enum accepted")
	}
	c := s.GetConnection("issues")
	c.Config["resolution_labels"] = `{"backend_custom":"后端代码修复"}`
	if err := s.UpsertConnection(*c); err != nil {
		t.Fatal(err)
	}
	op, err := s.ApplyResearchAction(context.Background(), w.ID, "platform-alice", req)
	if err != nil || op.State != "success" {
		t.Fatal("source-defined custom resolution not accepted", err)
	}
	item := s.GetWorkItem(w.ID)
	if item.Resolution != "backend_custom" || item.ResolutionLabel != "后端代码修复" {
		t.Fatal("custom code transformed", item)
	}
}

func TestResearchSourceDictionaryAdminOnlyAndPreservesCredentials(t *testing.T) {
	s, _, _, _ := researchFixture(t)
	mux := http.NewServeMux()
	registerResearchDictionaryRoutes(mux, s)
	call := func(role, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PUT", "/api/research/connections/issues/field-labels", strings.NewReader(body))
		r.Header.Set("X-User", "alice")
		r.Header.Set("X-Role", role)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	body := `{"severity":{"1":"致命"},"priority":{"1":"紧急"},"resolution":{"backend_custom":"后端代码修复"}}`
	if call("member", body).Code != 403 {
		t.Fatal("member edited provider dictionary")
	}
	if w := call("admin", body); w.Code != 200 {
		t.Fatal("admin dictionary failed", w.Body.String())
	}
	c := s.GetConnection("issues")
	if c.Username != "sync" || c.Password != "unused-sync-secret" || sourceLabels(c, "resolution")["backend_custom"] != "后端代码修复" {
		t.Fatal("dictionary edit changed source credentials")
	}
	if call("admin", `{"resolution":{"":"bad"}}`).Code != 400 {
		t.Fatal("invalid dictionary code accepted")
	}
}
