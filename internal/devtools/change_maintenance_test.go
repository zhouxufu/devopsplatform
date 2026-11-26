package devtools

import (
	"context"
	"logplatform/internal/deploy"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAgentIndependentReadinessValidatesAndConfirmsWholeBatch(t *testing.T) {
	st, err := deploy.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpsertService(deploy.Service{Name: "orders"}); err != nil {
		t.Fatal(err)
	}
	code := &deploy.Change{Kind: "code", Title: "更新服务", Service: "orders", Reference: "feature/orders", Owner: "alice"}
	config := &deploy.Change{Kind: "nacos", Title: "服务配置", Service: "orders", Owner: "alice", Nacos: &deploy.NacosChange{
		ResourceAlias: "config", Group: "DEFAULT_GROUP", Format: "yaml", Entries: []deploy.NacosEntry{{Key: "feature.enabled", Action: "set", Value: "true"}},
	}}
	for _, change := range []*deploy.Change{code, config} {
		if err := st.CreateChange(change); err != nil {
			t.Fatal(err)
		}
	}
	hub := deploy.NewLogHub()
	up := httptest.NewServer(deploy.Routes(st, deploy.NewBuilder(st, t.TempDir(), hub), hub, "", deploy.NewStepLib(t.TempDir())))
	defer up.Close()
	store, _ := OpenTokenStore(t.TempDir())
	svc := NewToolService(Upstreams{Deploy: up.URL})
	svc.actions = store
	token := workflowToken(t, store)
	token.Applications = []string{"orders"}
	const tool = "mark_independent_changes_ready"
	args := map[string]any{"service": "orders", "change_ids": []any{code.ID, config.ID}}
	if _, _, _, err := svc.Call(context.Background(), token, tool, args); err == nil || !strings.Contains(err.Error(), "未声明") {
		t.Fatal("accepted undeclared Nacos resource", err)
	}
	if err := st.SaveApplicationResources("orders", deploy.ApplicationResources{Requirements: []deploy.ApplicationResourceRequirement{{Alias: "config", Kind: "nacos"}}}); err != nil {
		t.Fatal(err)
	}
	preview := docCall(t, svc, token, tool, args)
	code.Reference = "feature/revised"
	if err := st.UpdateChange(code); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.Call(context.Background(), token, "confirm_platform_action", map[string]any{"confirmation_id": mapValue(preview)["confirmation_id"], "confirm": true, "reason": "确认就绪"}); err == nil {
		t.Fatal("accepted stale batch")
	}
	preview = docCall(t, svc, token, tool, args)
	token.Applications = []string{"other"}
	if _, _, _, err := svc.Call(context.Background(), token, "confirm_platform_action", map[string]any{"confirmation_id": mapValue(preview)["confirmation_id"], "confirm": true, "reason": "确认就绪"}); err == nil {
		t.Fatal("confirmation ignored revoked application access")
	}
	token.Applications = []string{"orders"}
	preview = docCall(t, svc, token, tool, args)
	for _, change := range []*deploy.Change{code, config} {
		if st.GetChange(change.ID).Status != deploy.ChangeDraft {
			t.Fatal("preview or stale confirmation wrote", change.ID)
		}
	}
	docConfirm(t, svc, token, preview)
	docConfirm(t, svc, token, preview)
	for _, change := range []*deploy.Change{code, config} {
		if st.GetChange(change.ID).Status != deploy.ChangeReady {
			t.Fatal("missing ready receipt", change.ID)
		}
	}
}

func TestMaintainChangeMCPRealHandlersPreviewConfirmAndCAS(t *testing.T) {
	st, err := deploy.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err = st.UpsertService(deploy.Service{Name: "orders"}); err != nil {
		t.Fatal(err)
	}
	change := &deploy.Change{Kind: "code", Title: "old", Service: "orders", Reference: "feature/old", Owner: "alice"}
	if err = st.CreateChange(change); err != nil {
		t.Fatal(err)
	}
	hub := deploy.NewLogHub()
	routes := deploy.Routes(st, deploy.NewBuilder(st, t.TempDir(), hub), hub, "", deploy.NewStepLib(t.TempDir()))
	var writes atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.URL.Path[len(r.URL.Path)-8:] != "/preview" {
			writes.Add(1)
		}
		routes.ServeHTTP(w, r)
	}))
	defer up.Close()
	store, _ := OpenTokenStore(t.TempDir())
	svc := NewToolService(Upstreams{Deploy: up.URL})
	svc.actions = store
	token := workflowToken(t, store)
	state := mapValue(docCall(t, svc, token, "get_change_maintenance", map[string]any{"service": "orders", "change_id": change.ID}))
	args := map[string]any{"service": "orders", "change_id": change.ID, "digest": state["digest"], "action": "edit", "reason": "correct branch", "patch": map[string]any{"reference": "master-house-claim", "summary": "new description"}}
	preview := docCall(t, svc, token, "maintain_change", args)
	if writes.Load() != 0 || st.GetChange(change.ID).Reference != "feature/old" {
		t.Fatal("preview saved")
	}
	docConfirm(t, svc, token, preview)
	docConfirm(t, svc, token, preview)
	if writes.Load() != 1 || st.GetChange(change.ID).Reference != "master-house-claim" || st.GetChange(change.ID).Title != "old" {
		t.Fatal("not atomic or repeated")
	}
	if _, _, _, err = svc.Call(context.Background(), token, "maintain_change", args); err == nil {
		t.Fatal("stale preview accepted")
	}
	state = mapValue(docCall(t, svc, token, "get_change_maintenance", map[string]any{"service": "orders", "change_id": change.ID}))
	args = map[string]any{"service": "orders", "change_id": change.ID, "digest": state["digest"], "action": "archive", "reason": "withdrawn"}
	preview = docCall(t, svc, token, "maintain_change", args)
	docConfirm(t, svc, token, preview)
	if st.GetChange(change.ID).Status != "archived" || writes.Load() != 2 {
		t.Fatal("not archived")
	}
}

func TestMaintainReadyCodeMCPPreviewConfirm(t *testing.T) {
	st, err := deploy.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.UpsertService(deploy.Service{Name: "orders"})
	change := &deploy.Change{Kind: "code", Title: "ready original", Summary: "original explanation", Reference: "feature/original", Service: "orders", Owner: "alice"}
	if err = st.CreateChange(change); err != nil {
		t.Fatal(err)
	}

	hub := deploy.NewLogHub()
	up := httptest.NewServer(deploy.Routes(st, deploy.NewBuilder(st, t.TempDir(), hub), hub, "", deploy.NewStepLib(t.TempDir())))
	defer up.Close()
	store, _ := OpenTokenStore(t.TempDir())
	svc := NewToolService(Upstreams{Deploy: up.URL})
	svc.actions = store
	token := workflowToken(t, store)
	allApps := token.Applications
	token.Applications = []string{"orders"}
	state := mapValue(docCall(t, svc, token, "get_change_maintenance", map[string]any{"service": "orders", "change_id": change.ID}))
	ready := docCall(t, svc, token, "maintain_change", map[string]any{"service": "orders", "change_id": change.ID, "digest": state["digest"], "action": "ready", "reason": "validate independent code"})
	if st.GetChange(change.ID).Status != deploy.ChangeDraft {
		t.Fatal("preview marked ready")
	}
	docConfirm(t, svc, token, ready)
	docConfirm(t, svc, token, ready)
	state = mapValue(docCall(t, svc, token, "get_change_maintenance", map[string]any{"service": "orders", "change_id": change.ID}))
	if mapValue(state["maintenance"])["ready_code_description_keeps_ready"] != true {
		t.Fatal(state)
	}
	token.Applications = allApps
	args := map[string]any{"service": "orders", "change_id": change.ID, "digest": state["digest"], "action": "edit", "reason": "verified description correction", "patch": map[string]any{"summary": "corrected explanation"}}
	preview := docCall(t, svc, token, "maintain_change", args)
	if st.GetChange(change.ID).Summary != "original explanation" {
		t.Fatal("preview wrote")
	}
	docConfirm(t, svc, token, preview)
	docConfirm(t, svc, token, preview)
	got := st.GetChange(change.ID)
	if got.Status != deploy.ChangeReady || got.Reference != "feature/original" || got.Summary != "corrected explanation" {
		t.Fatal(got)
	}
}
