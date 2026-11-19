package devtools

import (
	"context"
	"logplatform/internal/deploy"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

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
	if _, err = st.SetChangeReady(change.ID, true); err != nil {
		t.Fatal(err)
	}
	hub := deploy.NewLogHub()
	up := httptest.NewServer(deploy.Routes(st, deploy.NewBuilder(st, t.TempDir(), hub), hub, "", deploy.NewStepLib(t.TempDir())))
	defer up.Close()
	store, _ := OpenTokenStore(t.TempDir())
	svc := NewToolService(Upstreams{Deploy: up.URL})
	svc.actions = store
	token := workflowToken(t, store)
	state := mapValue(docCall(t, svc, token, "get_change_maintenance", map[string]any{"service": "orders", "change_id": change.ID}))
	if mapValue(state["maintenance"])["ready_code_description_keeps_ready"] != true {
		t.Fatal(state)
	}
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
