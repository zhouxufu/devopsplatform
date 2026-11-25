package devtools

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"logplatform/internal/deploy"
)

// Exercise the MCP contract with the real store and routes, without any delivery environment.
func TestDraftTrackingIncrementalMCP(t *testing.T) {
	svc, key, st, wid, _ := changesFixture(t)
	hub := deploy.NewLogHub()
	server := httptest.NewServer(deploy.Routes(st, deploy.NewBuilder(st, t.TempDir(), hub), hub, "", deploy.NewStepLib(t.TempDir())))
	defer server.Close()
	svc.up.Deploy = server.URL
	key.Applications = []string{"*"}
	key.Scopes = append(key.Scopes, "workitems:read")
	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		return mapValue(docCall(t, svc, key, name, args))
	}
	reject := func(name string, args map[string]any) {
		t.Helper()
		if _, _, _, err := svc.Call(context.Background(), key, name, args); err == nil {
			t.Fatal("unexpected success", name)
		}
	}
	work := map[string]any{"work_item_id": wid}
	preview := call("create_change_tracking", map[string]any{"work_item_id": wid, "applications": []any{"orders"}, "expires_at": time.Now().Add(time.Hour).Unix()})
	confirm := map[string]any{"confirmation_id": preview["confirmation_id"], "confirm": true, "reason": "登记本人草稿"}
	task := mapValue(call("confirm_platform_action", confirm)["result"])
	id := stringArg(task, "id")
	if id == "" {
		t.Fatal("missing confirmed tracking task")
	}
	call("confirm_platform_action", confirm)
	if all := docCall(t, svc, key, "list_work_loops", work).([]any); len(all) != 1 {
		t.Fatal("duplicate grant", len(all))
	}
	changes := developmentChanges()
	for _, c := range changes {
		mapValue(c)["service"] = "orders"
	}
	save := func(rows []any) map[string]any {
		t.Helper()
		return map[string]any{"task_id": id, "revision": task["revision"], "content": map[string]any{"digest": call("get_work_item_changes", work)["digest"], "changes": rows}}
	}
	read := func() *deploy.WorkDelivery {
		t.Helper()
		d, err := st.WorkDelivery(wid)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	initial := save(changes)
	task = call("save_work_loop_changes", initial)
	ids, ok := mapValue(task["draft_receipt"])["change_ids"].([]any)
	if !ok || len(ids) != 3 {
		t.Fatal("missing per-change IDs")
	}
	receipt := mapValue(task["draft_receipt"])
	items, ok := receipt["items"].([]any)
	if !ok || len(items) != len(changes) || receipt["saved_at"] == nil {
		t.Fatal("missing registration details", receipt)
	}
	for i, v := range items {
		row := mapValue(v)
		input := mapValue(changes[i])
		if row["created"] != true || row["id"] != ids[i] || row["kind"] != input["kind"] || row["title"] != input["title"] {
			t.Fatalf("incorrect receipt item %d: %v", i, row)
		}
	}
	before := mapValue(call("get_work_item_changes", work)["change_digests"])
	if len(before) != len(changes) {
		t.Fatal("missing per-change digests", before)
	}
	call("save_work_loop_changes", initial)
	records := read().Changes
	if len(records) != 3 || records[1].SQL == nil || records[2].Nacos == nil {
		t.Fatal("missing typed drafts or duplicate insert")
	}
	for _, c := range records {
		if c.Status != deploy.ChangeDraft || c.Owner != "alice" {
			t.Fatal("unexpected state or owner", c.ID)
		}
	}
	code := mapValue(changes[0])
	code["id"] = ids[0]
	code["summary"] = "使用新增备注列"
	code["prerequisites"] = []any{map[string]any{"change_id": ids[1], "reason": "先加列"}}
	update := save([]any{code})
	task = call("save_work_loop_changes", update)
	updatedItems, ok := mapValue(task["draft_receipt"])["items"].([]any)
	if !ok || len(updatedItems) != 1 || mapValue(updatedItems[0])["created"] != false || mapValue(updatedItems[0])["id"] != ids[0] {
		t.Fatal("incorrect update receipt", updatedItems)
	}
	after := mapValue(call("get_work_item_changes", work)["change_digests"])
	for i, id := range ids {
		k := fmt.Sprint(id)
		if after[k] == nil || before[k] == nil || (after[k] != before[k]) != (i == 0) {
			t.Fatalf("unexpected digest after a single edit: %v", id)
		}
	}
	records = read().Changes
	if len(records) != 3 || records[0].Summary != "使用新增备注列" || len(records[0].Prerequisites) != 1 || records[0].Prerequisites[0].ChangeID != records[1].ID {
		t.Fatal("in-place update lost identity or ordering")
	}
	reject("save_work_loop_changes", initial)
	// Omitting prerequisites preserves the chain. An explicit empty list clears it.
	delete(code, "prerequisites")
	preserve := save([]any{code})
	task = call("save_work_loop_changes", preserve)
	code["prerequisites"] = []any{}
	reject("save_work_loop_changes", preserve) // same revision, different intent
	if len(read().Changes[0].Prerequisites) != 1 {
		t.Fatal("lost existing dependency")
	}
	task = call("save_work_loop_changes", save([]any{code}))
	if len(read().Changes[0].Prerequisites) != 0 {
		t.Fatal("could not clear dependency")
	}
	guard := save([]any{code})
	guard["content"].(map[string]any)["mark_ready"] = true
	reject("save_work_loop_changes", guard)
	guard = save([]any{code})
	guard["content"].(map[string]any)["digest"] = "stale"
	reject("save_work_loop_changes", guard)
	foreign := *key
	foreign.Owner = "bob"
	if _, _, _, e := svc.Call(context.Background(), &foreign, "save_work_loop_changes", save([]any{code})); e == nil {
		t.Fatal("foreign owner wrote to grant")
	}
	reject("save_work_loop_changes", save([]any{map[string]any{"kind": "code", "service": "other", "reference": "main", "title": "越界"}}))
	control := func(action string) {
		t.Helper()
		task = call("control_change_tracking", map[string]any{"task_id": id, "revision": task["revision"], "action": action})
	}
	control("pause")
	reject("save_work_loop_changes", save([]any{code}))
	control("resume")
	code["summary"] = "已完成"
	task = call("save_work_loop_changes", save([]any{code}))
	control("revoke")
	reject("save_work_loop_changes", save([]any{code}))
	if d := read(); len(d.Changes) != 3 || d.Changes[0].Summary != "已完成" || d.Changes[0].Status != deploy.ChangeDraft || len(st.DeliveryRuns()) != 0 {
		t.Fatal("unexpected final state")
	}
}

func TestDraftTrackingTwoDevelopersOnOneWorkItem(t *testing.T) {
	svc, _, st, wid, _ := changesFixture(t)
	hub := deploy.NewLogHub()
	server := httptest.NewServer(deploy.Routes(st, deploy.NewBuilder(st, t.TempDir(), hub), hub, "", deploy.NewStepLib(t.TempDir())))
	defer server.Close()
	svc.up.Deploy = server.URL
	type developer struct {
		key  AccessToken
		task map[string]any
	}
	authorize := func(owner string) developer {
		t.Helper()
		k, _, err := svc.actions.Create(CreateTokenRequest{Owner: owner, Name: "drafts", Scopes: []string{"workitems:read", "changes:read", "changes:write"}, Applications: []string{"*"}, Environments: []string{"test"}})
		if err != nil {
			t.Fatal(err)
		}
		k.verified = true
		k.role = "member"
		p := docCall(t, svc, &k, "create_change_tracking", map[string]any{"work_item_id": wid, "applications": []any{"orders"}, "expires_at": time.Now().Add(time.Hour).Unix()})
		return developer{k, mapValue(docConfirm(t, svc, &k, p)["result"])}
	}
	alice, bob := authorize("alice"), authorize("bob")
	content := func() *deploy.WorkDelivery {
		t.Helper()
		d, e := st.WorkDelivery(wid)
		if e != nil {
			t.Fatal(e)
		}
		return d
	}
	// Both clients started from the same digest. Bob must re-read Alice's committed change.
	original := content().Digest
	request := func(dev developer, digest string, row any) map[string]any {
		return map[string]any{"task_id": dev.task["id"], "revision": dev.task["revision"], "content": map[string]any{"digest": digest, "changes": []any{row}}}
	}
	a := map[string]any{"kind": "code", "title": "读取新增字段", "service": "orders", "reference": "feature/a"}
	b := map[string]any{"kind": "code", "title": "调整查询条件", "service": "orders", "reference": "feature/b"}
	docCall(t, svc, &alice.key, "save_work_loop_changes", request(alice, original, a))
	if _, _, _, e := svc.Call(context.Background(), &bob.key, "save_work_loop_changes", request(bob, original, b)); e == nil {
		t.Fatal("stale work-item digest accepted")
	}
	d := content()
	if len(d.Changes) != 1 {
		t.Fatal("unexpected partial write")
	}
	b["id"] = d.Changes[0].ID
	if _, _, _, e := svc.Call(context.Background(), &bob.key, "save_work_loop_changes", request(bob, d.Digest, b)); e == nil {
		t.Fatal("overwrote colleague's draft")
	}
	delete(b, "id")
	docCall(t, svc, &bob.key, "save_work_loop_changes", request(bob, d.Digest, b))
	d = content()
	if len(d.Changes) != 2 || d.Changes[0].Owner != "alice" || d.Changes[0].Reference != "feature/a" || d.Changes[1].Owner != "bob" || d.Changes[1].Reference != "feature/b" {
		t.Fatal("lost branch or author")
	}
}
