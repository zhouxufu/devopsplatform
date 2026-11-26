package devtools

import (
	"context"
	"encoding/json"
	"logplatform/internal/deploy"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func changesFixture(t *testing.T) (*ToolService, *AccessToken, *deploy.Store, int64, *atomic.Int32) {
	t.Helper()
	st, e := deploy.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { st.Close() })
	for _, name := range []string{"orders", "other"} {
		if e = st.UpsertService(deploy.Service{Name: name}); e != nil {
			t.Fatal(e)
		}
	}
	if e = st.SaveApplicationResources("orders", deploy.ApplicationResources{Requirements: []deploy.ApplicationResourceRequirement{{Alias: "db", Kind: "mysql"}, {Alias: "config", Kind: "nacos"}}}); e != nil {
		t.Fatal(e)
	}
	work := &deploy.WorkItem{Title: "开发变更", Owner: "alice"}
	if e = st.CreateWorkItem(work); e != nil {
		t.Fatal(e)
	}
	hub := deploy.NewLogHub()
	routes := deploy.Routes(st, deploy.NewBuilder(st, t.TempDir(), hub), hub, "", deploy.NewStepLib(t.TempDir()))
	writes := &atomic.Int32{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			if r.URL.Path != "/api/changes/batch" && !strings.HasSuffix(r.URL.Path, "/delivery-content") {
				t.Error("unexpected side effect path", r.URL.Path)
			}
			writes.Add(1)
		}
		routes.ServeHTTP(w, r)
	}))
	t.Cleanup(upstream.Close)
	store, e := OpenTokenStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	tok, _, e := store.Create(CreateTokenRequest{Owner: "alice", Name: "changes", Scopes: []string{"changes:read", "changes:write"}, Applications: []string{"orders"}, Environments: []string{"test"}})
	if e != nil {
		t.Fatal(e)
	}
	tok.verified = true
	tok.role = "member"
	svc := NewToolService(Upstreams{Deploy: upstream.URL})
	svc.actions = store
	return svc, &tok, st, work.ID, writes
}
func developmentChanges() []any {
	return []any{
		map[string]any{"kind": "code", "title": "订单字段适配", "reference": "feature/order-note", "summary": "增加备注字段的读写代码", "risk_level": "low", "rollback_plan": "回退原版本"},
		map[string]any{"kind": "mysql", "title": "订单表加备注", "sql": map[string]any{"resource_alias": "db", "content": "ALTER TABLE orders ADD COLUMN note VARCHAR(64);", "max_affected_rows": 100}},
		map[string]any{"kind": "nacos", "title": "调整超时", "nacos": map[string]any{"resource_alias": "config", "group": "DEFAULT_GROUP", "data_id": "orders.yaml", "format": "yaml", "entries": []any{map[string]any{"key": "timeout", "action": "set", "value": "30"}, map[string]any{"key": "datasource.password", "action": "set", "value": "fixture-sensitive"}}}},
	}
}
func TestDevelopmentChangeMCPIndependentRoundtripAndOnce(t *testing.T) {
	svc, tok, st, _, writes := changesFixture(t)
	p := docCall(t, svc, tok, "report_changes", map[string]any{"service": "orders", "changes": developmentChanges()})
	preview := mapValue(mapValue(p)["preview"])
	if mapValue(p)["service"] != "orders" || preview["service"] != "orders" || preview["result_status"] != "draft" || preview["association"] != "独立草稿" || intArg(preview, "count", 0) != int64(len(developmentChanges())) {
		t.Fatalf("incomplete draft preview: %+v", preview)
	}
	if writes.Load() != 0 || len(st.Changes("", "orders", "", 100)) != 0 {
		t.Fatal("preview saved")
	}
	id := mapValue(p)["confirmation_id"]
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _, e := svc.Call(context.Background(), tok, "confirm_platform_action", map[string]any{"confirmation_id": id, "confirm": true, "reason": "登记开发变更"})
			if e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if writes.Load() != 1 {
		t.Fatal("duplicate metadata request", writes.Load())
	}
	changes := st.Changes("", "orders", "", 100)
	if len(changes) != 3 {
		t.Fatal(changes)
	}
	for _, c := range changes {
		got := st.GetChange(c.ID)
		if got.Status != "draft" || got.Owner != "alice" || len(got.WorkItems) != 0 {
			t.Fatal("independent draft contract", got)
		}
		detail := mapValue(docCall(t, svc, tok, "get_change", map[string]any{"service": "orders", "change_id": c.ID}))
		if detail["kind"] != c.Kind {
			t.Fatal(detail)
		}
		switch c.Kind {
		case "code":
			if got.Reference != "feature/order-note" || got.RollbackPlan != "回退原版本" {
				t.Fatal(got)
			}
		case "mysql":
			if got.SQL == nil || got.SQL.ResourceAlias != "db" || !strings.Contains(got.SQL.Content, "ADD COLUMN") {
				t.Fatal(got)
			}
		case "nacos":
			entries := mapValue(detail["nacos"])["entries"].([]any)
			if mapValue(entries[1])["value"] != "[REDACTED]" {
				t.Fatal("secret leaked in read response")
			}
			if got.Nacos == nil || got.Nacos.Entries[0].Value != "30" || got.Nacos.Entries[1].Value != "fixture-sensitive" {
				t.Fatal(got)
			}
		}
	}
	other := &deploy.Change{Kind: "code", Title: "other", Service: "other", Reference: "main"}
	if e := st.CreateChange(other); e != nil {
		t.Fatal(e)
	}
	if _, _, _, e := svc.Call(context.Background(), tok, "get_change", map[string]any{"service": "orders", "change_id": other.ID}); e == nil {
		t.Fatal("wrong application data leaked")
	}
}
func TestDevelopmentChangeMCPAppendPreservesReadyAndOtherOwner(t *testing.T) {
	svc, tok, st, wid, writes := changesFixture(t)
	tok.Applications = []string{"*"}
	base, e := st.WorkDelivery(wid)
	if e != nil {
		t.Fatal(e)
	}
	req := &deploy.WorkDeliverySave{Digest: base.Digest, Changes: []deploy.Change{{Kind: "code", Title: "旧变更", Service: "orders", Reference: "main"}}}
	if e = st.SaveWorkDelivery(wid, req, "bob", true); e != nil {
		t.Fatal(e)
	}
	oldID := req.Changes[0].ID
	if _, e = st.SetChangeReady(oldID, true); e != nil {
		t.Fatal(e)
	}
	before := st.GetChange(oldID)
	for _, tool := range []string{"report_changes", "add_work_item_changes"} {
		args := map[string]any{"work_item_id": wid, "changes": developmentChanges()}
		if tool == "report_changes" {
			args["service"] = "orders"
		} else {
			for _, r := range args["changes"].([]any) {
				r.(map[string]any)["service"] = "orders"
			}
		}
		p := docCall(t, svc, tok, tool, args)
		docConfirm(t, svc, tok, p)
	}
	after := st.GetChange(oldID)
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if string(a) != string(b) {
		t.Fatal("ready/other owner record edited")
	}
	delivery, _ := st.WorkDelivery(wid)
	if len(delivery.Changes) != 7 || writes.Load() != 2 {
		t.Fatal("append lost data", len(delivery.Changes), writes.Load())
	}
}
func TestDevelopmentChangeMCPRejectInjectionAndAtomicInvalidBatch(t *testing.T) {
	svc, tok, st, wid, writes := changesFixture(t)
	for _, c := range []map[string]any{
		{"kind": "code", "title": "x", "reference": "main", "service": "other"},
		{"kind": "code", "title": "x", "reference": "main", "id": 9},
		{"kind": "mysql", "title": "x", "sql": map[string]any{"resource_alias": "db", "content": "ALTER TABLE orders ADD COLUMN note INT;", "max_affected_rows": 100, "data_sync": map[string]any{"task_id": 1}}},
		{"kind": "nacos", "title": "x", "nacos": map[string]any{"resource_alias": "config", "group": "DEFAULT_GROUP", "format": "yaml", "entries": []any{map[string]any{"key": "a", "action": "set", "value": "x", "unexpected": 1}}}},
	} {
		if _, _, _, e := svc.Call(context.Background(), tok, "report_changes", map[string]any{"service": "orders", "changes": []any{c}}); e == nil {
			t.Fatal("injected data accepted", c)
		}
	}
	if _, _, _, e := svc.Call(context.Background(), tok, "report_changes", map[string]any{"service": "orders", "work_item_id": wid, "changes": developmentChanges()}); e == nil {
		t.Fatal("global work scope bypass")
	}
	// Backend validation is authoritative and atomic, even if a malformed draft
	// passed structural preview. No first-row save is left behind.
	invalid := developmentChanges()
	invalid[2].(map[string]any)["nacos"].(map[string]any)["resource_alias"] = "missing"
	p := docCall(t, svc, tok, "report_changes", map[string]any{"service": "orders", "changes": invalid})
	if _, _, _, e := svc.Call(context.Background(), tok, "confirm_platform_action", map[string]any{"confirmation_id": mapValue(p)["confirmation_id"], "confirm": true, "reason": "invalid fixture"}); e == nil {
		t.Fatal("missing resource accepted")
	}
	if len(st.Changes("", "orders", "", 100)) != 0 {
		t.Fatal("partially persisted batch")
	}
	if writes.Load() != 1 {
		t.Fatal(writes.Load())
	}
}
