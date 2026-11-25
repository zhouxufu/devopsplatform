package devtools

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"logplatform/internal/deploy"
)

func TestPublicationFromMCPConfirmationToBackground(t *testing.T) {
	st, e := deploy.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	if e = st.UpsertEnvironment(deploy.Environment{Name: "test", DisplayName: "测试", Purpose: "test"}); e != nil {
		t.Fatal(e)
	}
	c := &deploy.Change{Title: "人工检查合成数据", Kind: "manual", Owner: "alice", Reference: "填写实际核对结果"}
	if e = st.CreateChange(c); e != nil {
		t.Fatal(e)
	}
	if _, e = st.SetChangeReady(c.ID, true); e != nil {
		t.Fatal(e)
	}
	hub := deploy.NewLogHub()
	up := httptest.NewServer(deploy.Routes(st, deploy.NewBuilder(st, t.TempDir(), hub), hub, "", deploy.NewStepLib(t.TempDir())))
	defer up.Close()
	store, e := OpenTokenStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	key, _, e := store.Create(CreateTokenRequest{Name: "release", Owner: "alice", Scopes: []string{"delivery:read", "delivery:write", "delivery:execute"}, Applications: []string{"*"}, Environments: []string{"test"}})
	if e != nil {
		t.Fatal(e)
	}
	key.verified, key.role, key.reviewers = true, "member", `["alice"]`
	svc := NewToolService(Upstreams{Deploy: up.URL})
	h := Routes(store, svc)
	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		v, _, _, err := svc.Call(context.Background(), &key, name, args)
		if err != nil {
			t.Fatal(name, err)
		}
		return mapValue(v)
	}
	req := map[string]any{"environment": "test", "source_change_ids": []any{float64(c.ID)}, "reason": "隔离验证", "order_confirmed": true}
	preview := call("publish_delivery", map[string]any{"request": req})
	id := stringArg(preview, "confirmation_id")
	if id == "" || len(st.DeliveryRuns()) != 0 {
		t.Fatal("preview created a run or missing confirmation")
	}
	confirm := map[string]any{"confirmation_id": id, "confirm": true, "reason": "同意本次冻结清单"}
	call("confirm_platform_action", confirm)
	call("confirm_platform_action", confirm)
	runs := st.DeliveryRuns()
	if len(runs) != 1 || st.DeliveryControl(runs[0].ID).State != "running" {
		t.Fatal("missing original coordinator", runs)
	}
	resp, e := http.NewRequest("GET", up.URL+"/api/delivery/publications", nil)
	if e != nil {
		t.Fatal(e)
	}
	resp.Header.Set("X-User", "alice")
	got, e := up.Client().Do(resp)
	if e != nil {
		t.Fatal(e)
	}
	defer got.Body.Close()
	var tasks []map[string]string
	if e = json.NewDecoder(got.Body).Decode(&tasks); e != nil || len(tasks) != 1 {
		t.Fatal("unlisted coordinator", e, tasks)
	}
	payload, _ := json.Marshal(map[string]string{"owner": "alice", "task_id": tasks[0]["id"]})
	r := httptest.NewRequest("POST", "/internal/delivery-publication-advance", bytes.NewReader(payload))
	r.Header.Set("X-MCP-Users", `[{"username":"alice","role":"member","enabled":true}]`)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	control := st.DeliveryControl(runs[0].ID)
	if w.Code != 200 || control.State != "paused" || st.DeliveryRun(runs[0].ID).Steps[0].StartedAt != 0 {
		t.Fatal(w.Code, w.Body.String(), control)
	}
}
