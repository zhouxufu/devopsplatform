package devtools

import (
	"bytes"
	"context"
	"encoding/json"
	"logplatform/internal/deploy"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

const reportDocument = `{"openapi":"3.0.3","info":{"title":"订单接口","version":"1"},"paths":{"/orders":{"get":{"responses":{"200":{"description":"ok"}}}}}}`

func TestDocumentMCPReceiptReadScopeAndStableConfirmation(t *testing.T) {
	svc, tok, _, writes := documentMCPFixture(t)
	preview := docCall(t, svc, tok, "report_api_document", reportArgs(reportDocument))
	id := stringArg(mapValue(preview), "confirmation_id")
	if stringArg(mapValue(mapValue(preview)["preview"]), "report_id") != id {
		t.Fatal("preview does not carry stable receipt", preview)
	}
	args := map[string]any{"service": "orders", "environment": "test", "report_id": id}
	p := mapValue(docCall(t, svc, tok, "get_api_document_report", args))
	if stringArg(mapValue(p["report"]), "state") != "preview" {
		t.Fatal(p)
	}
	docConfirm(t, svc, tok, preview)
	p = mapValue(docCall(t, svc, tok, "get_api_document_report", args))
	if stringArg(mapValue(p["report"]), "state") != "saved" || writes.Load() != 1 {
		t.Fatal(p, writes.Load())
	}
	list := mapValue(docCall(t, svc, tok, "list_api_document_reports", map[string]any{"service": "orders", "environment": "test"}))
	if intArg(list, "total", 0) != 1 {
		t.Fatal(list)
	}
	args["service"] = "other"
	if _, _, _, e := svc.Call(context.Background(), tok, "get_api_document_report", args); e == nil {
		t.Fatal("receipt escaped token scope")
	}
}

func documentMCPFixture(t *testing.T) (*ToolService, *AccessToken, http.Handler, *atomic.Int32) {
	t.Helper()
	st, e := deploy.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { st.Close() })
	for _, name := range []string{"orders", "other"} {
		if e := st.UpsertService(deploy.Service{Name: name, ArtifactType: "jar"}); e != nil {
			t.Fatal(e)
		}
	}
	for _, name := range []string{"test", "prod"} {
		if e := st.UpsertEnvironment(deploy.Environment{Name: name, DisplayName: name}); e != nil {
			t.Fatal(e)
		}
	}
	hub := deploy.NewLogHub()
	routes := deploy.Routes(st, deploy.NewBuilder(st, t.TempDir(), hub), hub, "", deploy.NewStepLib(t.TempDir()))
	writes := &atomic.Int32{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/api/api-documents/import" {
			writes.Add(1)
		}
		routes.ServeHTTP(w, r)
	}))
	t.Cleanup(upstream.Close)
	store, e := OpenTokenStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	tok, _, e := store.Create(CreateTokenRequest{Owner: "alice", Name: "docs", Scopes: []string{"apidocs:read", "apidocs:write"}, Applications: []string{"orders"}, Environments: []string{"test"}})
	if e != nil {
		t.Fatal(e)
	}
	tok.verified = true
	tok.role = "admin"
	svc := NewToolService(Upstreams{Deploy: upstream.URL})
	svc.actions = store
	return svc, &tok, routes, writes
}
func reportArgs(raw string) map[string]any {
	var doc map[string]any
	d := json.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	_ = d.Decode(&doc)
	return map[string]any{"service": "orders", "environment": "test", "document": doc}
}
func docCall(t *testing.T, s *ToolService, tok *AccessToken, name string, args map[string]any) any {
	t.Helper()
	v, _, _, e := s.Call(context.Background(), tok, name, args)
	if e != nil {
		t.Fatal(name, e)
	}
	return v
}
func docConfirm(t *testing.T, s *ToolService, tok *AccessToken, p any) map[string]any {
	t.Helper()
	return mapValue(docCall(t, s, tok, "confirm_platform_action", map[string]any{"confirmation_id": mapValue(p)["confirmation_id"], "confirm": true, "reason": "验收文档上报"}))
}
func TestDocumentMCPReportCompareAndDedupEndToEnd(t *testing.T) {
	svc, tok, _, writes := documentMCPFixture(t)
	args := reportArgs(reportDocument)
	preview := docCall(t, svc, tok, "report_api_document", args)
	if writes.Load() != 0 || intArg(mapValue(mapValue(preview)["preview"])["summary"].(map[string]any), "added", 0) != 1 {
		t.Fatal("preview wrote or missed diff", preview)
	}
	// Freeze caller data before confirmation, and survive reopening the action store.
	args["document"].(map[string]any)["info"].(map[string]any)["title"] = "mutated"
	reopened, e := OpenTokenStore(filepath.Dir(svc.actions.path))
	if e != nil {
		t.Fatal(e)
	}
	svc.actions = reopened
	result := docConfirm(t, svc, tok, preview)
	first := intArg(mapValue(result["result"]), "id", 0)
	if first < 1 || writes.Load() != 1 {
		t.Fatal(result, writes.Load())
	}
	get := mapValue(docCall(t, svc, tok, "get_api_document", map[string]any{"service": "orders", "environment": "test", "document_id": first}))
	if stringArg(mapValue(get["info"]), "title") != "订单接口" {
		t.Fatal("frozen content changed", get["info"])
	}
	// Formatting / property order are irrelevant to duplicate identity.
	duplicate := docCall(t, svc, tok, "report_api_document", reportArgs(" \n"+reportDocument))
	if mapValue(mapValue(duplicate)["preview"])["duplicate"] != true {
		t.Fatal(duplicate)
	}
	dup := docConfirm(t, svc, tok, duplicate)
	if intArg(mapValue(dup["result"]), "id", 0) != first || mapValue(dup["result"])["duplicate"] != true {
		t.Fatal(dup)
	}
	changed := strings.Replace(reportDocument, `"description":"ok"`, `"description":"changed"`, 1)
	p2 := docCall(t, svc, tok, "report_api_document", reportArgs(changed))
	second := intArg(mapValue(docConfirm(t, svc, tok, p2)["result"]), "id", 0)
	comparison := mapValue(docCall(t, svc, tok, "compare_api_documents", map[string]any{"service": "orders", "environment": "test", "document_id": second, "base_id": first}))
	if intArg(mapValue(comparison["summary"]), "modified", 0) != 1 {
		t.Fatal(comparison)
	}
	rows := docCall(t, svc, tok, "list_api_documents", map[string]any{"service": "orders", "environment": "test"}).([]any)
	if len(rows) != 2 {
		t.Fatal("duplicate created a version", rows)
	}
	// Historic duplicate is reused explicitly, not silently promoted to latest.
	old := docCall(t, svc, tok, "report_api_document", reportArgs(reportDocument))
	oldResult := docConfirm(t, svc, tok, old)
	if intArg(mapValue(oldResult["result"]), "id", 0) != first {
		t.Fatal(oldResult)
	}
	rows = docCall(t, svc, tok, "list_api_documents", map[string]any{"service": "orders", "environment": "test"}).([]any)
	if len(rows) != 2 || intArg(mapValue(rows[0]), "id", 0) != second {
		t.Fatal("duplicate altered latest", rows)
	}
}
func TestDocumentMCPConcurrentConfirmStaleAndPermissions(t *testing.T) {
	svc, tok, routes, writes := documentMCPFixture(t)
	preview := docCall(t, svc, tok, "report_api_document", reportArgs(reportDocument))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); docConfirm(t, svc, tok, preview) }()
	}
	wg.Wait()
	if writes.Load() != 1 {
		t.Fatal("multiple writes", writes.Load())
	}
	changed := strings.Replace(reportDocument, `"description":"ok"`, `"description":"next"`, 1)
	stale := docCall(t, svc, tok, "report_api_document", reportArgs(changed))
	newer := strings.Replace(reportDocument, `"description":"ok"`, `"description":"newer"`, 1)
	req := httptest.NewRequest("POST", "/api/api-documents/import?service=orders&environment=test", strings.NewReader(newer))
	req.Header.Set("X-User", "alice")
	req.Header.Set("X-Role", "admin")
	w := httptest.NewRecorder()
	routes.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if _, _, _, e := svc.Call(context.Background(), tok, "confirm_platform_action", map[string]any{"confirmation_id": mapValue(stale)["confirmation_id"], "confirm": true, "reason": "stale"}); e == nil {
		t.Fatal("stale accepted")
	}
	if writes.Load() != 1 {
		t.Fatal("stale wrote")
	}
	for _, args := range []map[string]any{{"service": "other", "environment": "test"}, {"service": "orders", "environment": "prod"}, {"service": "orders"}, {"service": "orders", "environment": "test", "extra": true}} {
		if _, _, _, e := svc.Call(context.Background(), tok, "list_api_documents", args); e == nil {
			t.Fatal("invalid scope accepted", args)
		}
	}
	member := *tok
	member.role = "member"
	if _, _, _, e := svc.Call(context.Background(), &member, "report_api_document", reportArgs(reportDocument)); e == nil {
		t.Fatal("member report allowed")
	}
	legacy := *tok
	legacy.Scopes = []string{"builds:read", "changes:write"}
	if _, _, _, e := svc.Call(context.Background(), &legacy, "report_api_document", reportArgs(reportDocument)); e == nil {
		t.Fatal("legacy scope gained write")
	}
	// Revoked/downgraded permission and scope are checked again at confirmation.
	p := docCall(t, svc, tok, "report_api_document", reportArgs(changed))
	narrowed := *tok
	narrowed.Environments = []string{"prod"}
	if _, _, _, e := svc.Call(context.Background(), &narrowed, "confirm_platform_action", map[string]any{"confirmation_id": mapValue(p)["confirmation_id"], "confirm": true, "reason": "test"}); e == nil {
		t.Fatal("narrowed env accepted")
	}
	if writes.Load() != 1 {
		t.Fatal("permission failure wrote")
	}
	// The read tool validates actual document ownership through the backend too.
	wide := *tok
	wide.Applications = []string{"*"}
	wide.Environments = []string{"*"}
	rows := docCall(t, svc, tok, "list_api_documents", map[string]any{"service": "orders", "environment": "test"}).([]any)
	id := intArg(mapValue(rows[0]), "id", 0)
	for _, args := range []map[string]any{{"service": "other", "environment": "test", "document_id": id}, {"service": "orders", "environment": "prod", "document_id": id}} {
		if _, _, _, e := svc.Call(context.Background(), &wide, "get_api_document", args); e == nil {
			t.Fatal("cross document scope accepted")
		}
	}
}
func TestDocumentMCPTransportLargeInvalidAndUncertain(t *testing.T) {
	svc, tok, _, writes := documentMCPFixture(t)
	for _, doc := range []any{"/tmp/openapi.json", map[string]any{"openapi": "2.0"}, nil} {
		args := reportArgs(reportDocument)
		args["document"] = doc
		if _, _, _, e := svc.Call(context.Background(), tok, "report_api_document", args); e == nil {
			t.Fatal("invalid document accepted")
		}
	}
	tooLarge := reportArgs(reportDocument)
	tooLarge["document"].(map[string]any)["x-padding"] = strings.Repeat("a", 8<<20)
	if _, _, _, e := svc.Call(context.Background(), tok, "report_api_document", tooLarge); e == nil {
		t.Fatal("oversized accepted")
	}
	// Use real MCP HTTP framing (> former 1 MiB limit), account verification and audit.
	store := svc.actions
	_, raw, e := store.Create(CreateTokenRequest{Owner: "alice", Name: "transport", Scopes: []string{"apidocs:read", "apidocs:write"}, Applications: []string{"orders"}, Environments: []string{"test"}})
	if e != nil {
		t.Fatal(e)
	}
	args := reportArgs(reportDocument)
	args["document"].(map[string]any)["x-padding"] = strings.Repeat("a", 1100000)
	h := NewMCPHandler(store, svc)
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "report_api_document", "arguments": args}})
	req := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+raw)
	req.Header.Set("X-MCP-Users", `[{"username":"alice","role":"admin","enabled":true}]`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), `"isError":true`) || !strings.Contains(rec.Body.String(), "confirmation_id") {
		t.Fatal(rec.Body.String())
	}
	if writes.Load() != 0 {
		t.Fatal("transport preview wrote")
	}
	if len(store.AuditEvents(10)) != 1 {
		t.Fatal("missing audit")
	}
	// A result lost after the actual write stays uncertain; confirming again does not resend.
	p := docCall(t, svc, tok, "report_api_document", reportArgs(reportDocument))
	original := svc.up.Deploy
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/api/api-documents/import" {
			writes.Add(1)
			http.Error(w, "lost", 503)
			return
		}
		resp, err := http.Get(original + r.URL.RequestURI())
		if err != nil {
			t.Error(err)
			return
		}
		defer resp.Body.Close()
		var v any
		json.NewDecoder(resp.Body).Decode(&v)
		json.NewEncoder(w).Encode(v)
	}))
	defer proxy.Close()
	svc.up.Deploy = proxy.URL
	confirm := map[string]any{"confirmation_id": mapValue(p)["confirmation_id"], "confirm": true, "reason": "uncertain"}
	if _, _, _, e := svc.Call(context.Background(), tok, "confirm_platform_action", confirm); e == nil {
		t.Fatal("lost result accepted")
	}
	result := mapValue(docCall(t, svc, tok, "confirm_platform_action", confirm))
	if result["state"] != "uncertain" || writes.Load() != 1 {
		t.Fatal(result, writes.Load())
	}
}

func TestDocumentMCPKeyDefaultMaintainerAndCrossKeyDedup(t *testing.T) {
	svc, tok, _, _ := documentMCPFixture(t)
	p := docCall(t, svc, tok, "report_api_document", reportArgs(reportDocument))
	if stringArg(mapValue(mapValue(p)["preview"]), "default_maintainer") != "alice" {
		t.Fatal(p)
	}
	id := intArg(mapValue(docConfirm(t, svc, tok, p)["result"]), "id", 0)
	bob, _, err := svc.actions.Create(CreateTokenRequest{Owner: "bob", Name: "other Key", Scopes: []string{"apidocs:read", "apidocs:write"}, Applications: []string{"orders"}, Environments: []string{"test"}})
	if err != nil {
		t.Fatal(err)
	}
	bob.verified = true
	bob.role = "admin"
	p = docCall(t, svc, &bob, "report_api_document", reportArgs(reportDocument))
	if stringArg(mapValue(mapValue(p)["preview"]), "default_maintainer") != "alice" {
		t.Fatal("duplicate preview substituted Key owner", p)
	}
	if got := intArg(mapValue(docConfirm(t, svc, &bob, p)["result"]), "id", 0); got != id {
		t.Fatal(got, id)
	}
	get := mapValue(docCall(t, svc, &bob, "get_api_document", map[string]any{"service": "orders", "environment": "test", "document_id": id}))
	operations := get["operations"].([]any)
	if stringArg(mapValue(operations[0]), "maintainer") != "alice" || stringArg(mapValue(get["snapshot"]), "default_maintainer") != "alice" {
		t.Fatal(get)
	}
	// New content receives the new Key owner; the original raw document remains untouched.
	changed := strings.Replace(reportDocument, `"description":"ok"`, `"description":"new"`, 1)
	p = docCall(t, svc, &bob, "report_api_document", reportArgs(changed))
	next := intArg(mapValue(docConfirm(t, svc, &bob, p)["result"]), "id", 0)
	get = mapValue(docCall(t, svc, &bob, "get_api_document", map[string]any{"service": "orders", "environment": "test", "document_id": next}))
	if stringArg(mapValue(get["snapshot"]), "default_maintainer") != "bob" {
		t.Fatal(get)
	}
	if _, ok := mapValue(get["document"])["x-maintainer"]; ok {
		t.Fatal("source mutated")
	}
	p = docCall(t, svc, &bob, "report_api_document", reportArgs(reportDocument))
	if stringArg(mapValue(mapValue(p)["preview"]), "default_maintainer") != "alice" {
		t.Fatal(p)
	}
	if got := intArg(mapValue(docConfirm(t, svc, &bob, p)["result"]), "id", 0); got != id {
		t.Fatal(got, id)
	}
	rows := docCall(t, svc, &bob, "list_api_documents", map[string]any{"service": "orders", "environment": "test"}).([]any)
	if len(rows) != 2 || intArg(mapValue(rows[0]), "id", 0) != next {
		t.Fatal("historical duplicate became latest", rows)
	}

}

func TestDocumentMCPFreezesFieldRequirementsAndKeepsDuplicateMetadata(t *testing.T) {
	svc, tok, h, writes := documentMCPFixture(t)
	create := func(title string) int64 {
		r := httptest.NewRequest("POST", "/api/work-items", strings.NewReader(`{"source":"manual","kind":"requirement","title":"`+title+`"}`))
		r.Header.Set("X-User", "alice")
		r.Header.Set("X-Role", "admin")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var value map[string]any
		d := json.NewDecoder(w.Body)
		d.UseNumber()
		d.Decode(&value)
		id := intArg(value, "id", 0)
		if id < 1 {
			t.Fatal(w.Code, value)
		}
		return id
	}
	first, second := create("默认需求"), create("字段单独需求")
	raw := `{"openapi":"3.0.3","info":{"title":"完整文档","version":"1"},"paths":{"/orders":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object","properties":{"name":{"type":"string"},"state":{"type":"string"}}}}}}}}},"/other":{"get":{"responses":{"200":{"description":"also retained"}}}}},"components":{"schemas":{"Unused":{"type":"string"}}}}`
	args := reportArgs(raw)
	pointer := "#/paths/~1orders/get/responses/200/content/application~1json/schema/properties/state"
	context := map[string]any{"work_item": map[string]any{"id": first}, "fields": map[string]any{pointer: map[string]any{"id": second}}}
	args["change_context"] = context
	preview := docCall(t, svc, tok, "report_api_document", args)
	details := mapValue(mapValue(preview)["preview"])
	if writes.Load() != 0 || len(details["field_additions"].([]any)) != 2 {
		t.Fatal(preview)
	}
	// Mutating caller-owned metadata after preview cannot change the frozen request.
	context["work_item"].(map[string]any)["id"] = second
	result := docConfirm(t, svc, tok, preview)
	id := intArg(mapValue(result["result"]), "id", 0)
	get := mapValue(docCall(t, svc, tok, "get_api_document", map[string]any{"service": "orders", "environment": "test", "document_id": id}))
	fields := get["field_additions"].([]any)
	for _, field := range fields {
		f := mapValue(field)
		want := first
		if stringArg(f, "name") == "state" {
			want = second
		}
		if intArg(mapValue(f["work_item"]), "id", 0) != want {
			t.Fatal(f)
		}
	}
	if len(mapValue(mapValue(get["document"])["paths"])) != 2 || mapValue(get["document"])["components"] == nil {
		t.Fatal("complete document was truncated", get)
	}
	duplicate := docCall(t, svc, tok, "report_api_document", reportArgs(raw))
	if mapValue(mapValue(duplicate)["preview"])["duplicate"] != true {
		t.Fatal(duplicate)
	}
	docConfirm(t, svc, tok, duplicate)
	again := mapValue(docCall(t, svc, tok, "get_api_document", map[string]any{"service": "orders", "environment": "test", "document_id": id}))
	if intArg(mapValue(mapValue(again["change_context"])["work_item"]), "id", 0) != first {
		t.Fatal("duplicate changed requirement", again)
	}
}
