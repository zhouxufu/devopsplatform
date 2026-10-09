package deploy

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkbenchResourceOnlyApplicationAndReview(t *testing.T) {
	st, m, server, c, req := redisFixture(t)
	if _, err := st.db.Exec(`INSERT INTO resource_applications(name) VALUES('resource-only')`); err != nil {
		t.Fatal(err)
	}
	if st.GetService("resource-only") != nil {
		t.Fatal("created fake deployment service")
	}
	if err := st.SaveApplicationResources("resource-only", ApplicationResources{Requirements: []ApplicationResourceRequirement{{Alias: "cache", Kind: "redis"}}, Bindings: []ApplicationEnvironmentResource{{Alias: "cache", Environment: "test", Connection: "qa-redis"}}}); err != nil {
		t.Fatal(err)
	}
	c.ID = 0
	c.Service = "resource-only"
	if err := st.CreateChange(c); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetChangeReady(c.ID, true); err != nil {
		t.Fatal(err)
	}
	tpl := DeliveryTemplate{Workbench: "redis", Name: "Redis workbench", Mode: "fixed", Owner: "alice", Nodes: []DeliveryNode{{ID: "cache", Kind: "redis", Name: "Cache", ChangeIDs: []int64{c.ID}}}}
	if err := st.SaveDeliveryTemplate(&tpl); err != nil {
		t.Fatal(err)
	}
	req.TemplateID = tpl.ID
	run, err := st.CreateDeliveryRun(req, "alice", m)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "pending_approval" || len(run.Plan.Services) != 0 || server.count("UNLINK") != 0 {
		t.Fatal("resource-only preview executed or bypassed review")
	}
	if _, err := st.AdvanceDeliveryRun(run.ID, "alice", false, m); err == nil {
		t.Fatal("unreviewed run advanced")
	}
	if _, err := st.ReviewDeliveryRun(run.ID, "alice", run.Digest, true); err == nil {
		t.Fatal("self-review allowed")
	}
	if _, err := st.ReviewDeliveryRun(run.ID, "reviewer", run.Digest, true); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AdvanceDeliveryRun(run.ID, "alice", false, m); err != nil {
		t.Fatal(err)
	}
	if server.count("UNLINK") != 1 {
		t.Fatal("reviewed operation not executed once")
	}
	if err := st.DeleteService("orders"); err != nil {
		t.Fatal(err)
	}
	if !st.hasResourceApplication("orders") {
		t.Fatal("deleted build lost resource identity")
	}
	if _, err := st.redisBinding("orders", "test", "cache"); err != nil {
		t.Fatal("deleted build broke independent resource binding", err)
	}
	// A DBMS workbench must retain review even when the environment is ordinary.
	run = st.DeliveryRun(run.ID)
	run.Plan.Template.Workbench = "dbms"
	run.Plan.RedisPlans = nil
	run.Approver = ""
	run.Status = "approved"
	if err := st.saveDeliveryRun(run); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AdvanceDeliveryRun(run.ID, "alice", false, m); err == nil || !strings.Contains(err.Error(), "审核") {
		t.Fatal("DBMS review bypass", err)
	}
}

func TestWorkbenchCapabilityRoutes(t *testing.T) {
	st, _, _, c, req := redisFixture(t)
	hub := NewLogHub()
	h := Routes(st, NewBuilder(st, t.TempDir(), hub), hub, "", NewStepLib(t.TempDir(), t.TempDir()))
	call := func(method, path, actor, role, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.Header.Set("X-User", actor)
		r.Header.Set("X-Role", role)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := call("POST", "/api/workbench/redis/applications", "member", "member", `{"name":"unmanaged"}`); w.Code != 403 {
		t.Fatal("member registered application", w.Code)
	}
	if w := call("POST", "/api/workbench/redis/applications", "local", "admin", `{"name":"cache-only"}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if st.GetService("cache-only") != nil || !st.hasResourceApplication("cache-only") {
		t.Fatal("resource identity coupled to build")
	}
	if w := call("GET", "/api/workbench/dbms/changes", "alice", "member", ""); w.Body.String() != "[]\n" {
		t.Fatal("foreign changes exposed", w.Body.String())
	}
	if w := call("POST", "/api/workbench/dbms/changes", "alice", "member", `{"kind":"redis","title":"wrong"}`); w.Code != 400 {
		t.Fatal("foreign change accepted")
	}
	if w := call("POST", "/api/workbench/redis/delivery/runs", "alice", "member", `{"template_id":`+jsonNumber(req.TemplateID)+`,"environment":"test"}`); w.Code != 400 {
		t.Fatal("non-workbench template accepted")
	}
	if w := call("POST", "/api/workbench/dbms/changes/"+jsonNumber(c.ID)+"/ready", "alice", "member", `{}`); w.Code != 400 {
		t.Fatal("foreign change modified")
	}
	if w := call("POST", "/api/workbench/dbms/query", "alice", "member", `{"sql":"DELETE FROM users WHERE id=1"}`); w.Code != 400 {
		t.Fatal("non-select query accepted")
	}
	if w := call("POST", "/api/workbench/redis/builds", "alice", "member", `{}`); w.Code != 404 {
		t.Fatal("build endpoint exposed")
	}
	templateBody := `{"workbench":"redis","mode":"fixed","name":"QA","services":[],"nodes":[{"id":"operation","kind":"redis","change_ids":[` + jsonNumber(c.ID) + `]}]}`
	a := call("POST", "/api/workbench/redis/delivery/templates", "alice", "member", templateBody)
	b := call("POST", "/api/workbench/redis/delivery/templates", "alice", "member", templateBody)
	var first, second DeliveryTemplate
	if a.Code != 200 || b.Code != 200 || json.Unmarshal(a.Body.Bytes(), &first) != nil || json.Unmarshal(b.Body.Bytes(), &second) != nil || first.ID != second.ID || len(first.Nodes[0].ChangeIDs) != 0 {
		t.Fatal("workbench previews created duplicate or scope-bound templates")
	}
	if w := call("POST", "/api/workbench/redis/delivery/runs", "alice", "member", `{"template_id":`+jsonNumber(first.ID)+`,"environment":"test","nodes":[{"id":"operation","kind":"deploy"}]}`); w.Code != 400 {
		t.Fatal("overridden deploy node accepted")
	}
	if w := call("PUT", "/api/workbench/dbms/services/orders/resources", "member", "member", `{"requirements":[],"bindings":[]}`); w.Code != 403 {
		t.Fatal("member modified binding")
	}
	tpl := DeliveryTemplate{Workbench: "dbms", Mode: "fixed", Nodes: []DeliveryNode{{ID: "sql", Kind: "deploy"}}}
	if validateWorkbenchTemplate(tpl) == nil {
		t.Fatal("deployment node accepted in DBMS")
	}
	if w := call("POST", "/api/workbench/dbms/delivery/templates", "alice", "member", `{"workbench":"dbms","name":"DBMS QA","mode":"fixed","nodes":[{"id":"operation","name":"SQL","kind":"mysql"}]}`); w.Code != 200 {
		t.Fatal("DBMS template creation failed", w.Body.String())
	}
}

func jsonNumber(v int64) string { data, _ := json.Marshal(v); return string(data) }
