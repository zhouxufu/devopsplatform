package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWorkbenchIndependentPoliciesAndReadApproval(t *testing.T) {
	s, _, server, _, _ := redisFixture(t)
	hub := NewLogHub()
	h := Routes(s, NewBuilder(s, t.TempDir(), hub), hub, "", NewStepLib(t.TempDir(), t.TempDir()))
	call := func(kind, method, path, actor, role string, body any) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "/api/workbench/"+kind+"/"+path, bytes.NewReader(data))
		r.Header.Set("X-User", actor)
		r.Header.Set("X-Role", role)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	defaults := call("redis", "GET", "policy?environment=test", "alice", "member", nil)
	if defaults.Code != 200 || !strings.Contains(defaults.Body.String(), `"change_approval":true`) || !strings.Contains(defaults.Body.String(), `"query_approval":false`) {
		t.Fatal(defaults.Body.String())
	}
	policy := WorkbenchPolicy{QueryApproval: true, ChangeApproval: false}
	if w := call("redis", "PUT", "policy?environment=test", "alice", "member", policy); w.Code != 403 {
		t.Fatal("member changed policy")
	}
	if w := call("redis", "PUT", "policy?environment=test", "root", "admin", policy); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for _, scope := range [][2]string{{"dbms", "test"}, {"redis", "prod"}} {
		p, e := s.workbenchPolicy(scope[0], scope[1])
		if e != nil || p.QueryApproval || !p.ChangeApproval {
			t.Fatal("policy leaked into other scope", p, e)
		}
	}
	q := WorkbenchQuery{Service: "orders", Environment: "test", Alias: "cache", Commands: "TYPE menu:tree\nTTL menu:tree"}
	if w := call("redis", "POST", "query", "alice", "member", q); w.Code == 200 || server.count("TYPE") != 0 {
		t.Fatal("direct read bypassed approval")
	}
	create := call("redis", "POST", "query-requests", "alice", "member", q)
	var ticket WorkbenchQueryTicket
	json.Unmarshal(create.Body.Bytes(), &ticket)
	if create.Code != 200 || ticket.Status != "pending_approval" || server.count("TYPE") != 0 || strings.Contains(create.Body.String(), "fixture-password") {
		t.Fatal(create.Body.String())
	}
	path := fmt.Sprintf("query-requests/%d/", ticket.ID)
	for _, actor := range []string{"alice", "outsider"} {
		if w := call("redis", "POST", path+"approve", actor, "member", map[string]any{}); w.Code == 200 {
			t.Fatal("invalid reviewer approved", actor)
		}
	}
	if w := call("redis", "POST", path+"approve", "reviewer", "member", map[string]any{}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := call("redis", "POST", path+"execute", "reviewer", "member", map[string]any{}); w.Code == 200 {
		t.Fatal("reviewer executed another owner's query")
	}
	w := call("redis", "POST", path+"execute", "alice", "member", map[string]any{})
	if w.Code != 200 || server.count("TYPE") != 1 || server.count("UNLINK") != 0 {
		t.Fatal(w.Body.String())
	}
	if w := call("redis", "POST", path+"execute", "alice", "member", map[string]any{}); w.Code == 200 || server.count("TYPE") != 1 {
		t.Fatal("query executed twice")
	}
	stored, _ := s.queryTicket(ticket.ID, "redis")
	if stored.Status != "completed" {
		t.Fatal(stored)
	}
	if _, err := s.queryTicket(ticket.ID, "dbms"); err == nil {
		t.Fatal("cross-plugin query ticket read")
	}
	for _, commands := range []string{"UNLINK menu:tree", "SET menu:tree value", "GET other:key", "TYPE menu:*", "EVAL x 0"} {
		q.Commands = commands
		// With approval disabled, whitelist and prefix remain mandatory.
		call("redis", "PUT", "policy?environment=test", "root", "admin", WorkbenchPolicy{})
		if w := call("redis", "POST", "query", "alice", "member", q); w.Code == 200 {
			t.Fatal("unsafe query accepted", commands)
		}
	}
	if server.count("UNLINK") != 0 || server.count("SET") != 0 {
		t.Fatal("query executed writes")
	}
	q.Commands = "GET menu:tree"
	w = call("redis", "POST", "query", "alice", "member", q)
	var result struct {
		Rows []struct {
			Result    string `json:"result"`
			Truncated bool   `json:"truncated"`
		} `json:"rows"`
	}
	json.Unmarshal(w.Body.Bytes(), &result)
	if w.Code != 200 || len(result.Rows) != 1 || len(result.Rows[0].Result) != 65536 || !result.Rows[0].Truncated {
		t.Fatal("GET reply not bounded", w.Code)
	}
	for _, event := range s.AuditEvents(100) {
		data, _ := json.Marshal(event.Detail)
		if strings.Contains(string(data), "vvvvvvvv") {
			t.Fatal("query results persisted in audit")
		}
	}
}

func TestWorkbenchComparisonCannotBypassReadApproval(t *testing.T) {
	s, _, _, _, _ := redisFixture(t)
	if e := s.SaveApplicationResources("orders", ApplicationResources{Requirements: []ApplicationResourceRequirement{{Alias: "db", Kind: "mysql"}}}); e != nil {
		t.Fatal(e)
	}
	task := DataSyncTask{Name: "Menu", Service: "orders", ResourceAlias: "db", SourceSQL: "SELECT id,name FROM menu WHERE id>0", TargetSQL: "SELECT id,name FROM menu WHERE id>0", Keys: []string{"id"}, Owner: "alice"}
	if e := s.SaveDataSyncTask(&task); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?)`, "workbench_policy:dbms:prod", `{"query_approval":true,"change_approval":true}`); e != nil {
		t.Fatal(e)
	}
	hub := NewLogHub()
	h := Routes(s, NewBuilder(s, t.TempDir(), hub), hub, "", NewStepLib(t.TempDir(), t.TempDir()))
	for _, prefix := range []string{"/api/", "/api/workbench/dbms/"} {
		for _, op := range []string{"compare", "freeze"} {
			r := httptest.NewRequest("POST", fmt.Sprintf("%sdata-sync/tasks/%d/%s", prefix, task.ID, op), strings.NewReader(`{"source_environment":"test","target_environment":"prod","digest":"fake","rows":[]}`))
			r.Header.Set("X-User", "alice")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code == 200 || (!strings.Contains(w.Body.String(), "审批") && !strings.Contains(w.Body.String(), "批准")) {
				t.Fatal("comparison bypass", prefix, op, w.Body.String())
			}
		}
	}
}

func TestWorkbenchChangePolicySnapshotsAndProtection(t *testing.T) {
	s, m, server, c, req := redisFixture(t)
	tpl := DeliveryTemplate{Workbench: "redis", Name: "Workbench", Mode: "fixed", Owner: "alice", Nodes: []DeliveryNode{{ID: "op", Kind: "redis", Name: "Cache", ChangeIDs: []int64{c.ID}}}}
	if err := s.SaveDeliveryTemplate(&tpl); err != nil {
		t.Fatal(err)
	}
	req.TemplateID = tpl.ID
	save := func(env string, p WorkbenchPolicy) {
		data, _ := json.Marshal(p)
		_, err := s.db.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, "workbench_policy:redis:"+env, string(data))
		if err != nil {
			t.Fatal(err)
		}
	}
	old, e := s.CreateDeliveryRun(req, "alice", m)
	if e != nil || old.Status != "pending_approval" {
		t.Fatal(old, e)
	}
	save("test", WorkbenchPolicy{QueryApproval: true, ChangeApproval: false})
	if _, e := s.AdvanceDeliveryRun(old.ID, "alice", false, m); e == nil {
		t.Fatal("old mandatory approval waived")
	}
	env := *s.GetEnvironment("test")
	env.Approvers = ""
	s.UpsertEnvironment(env)
	run, e := s.CreateDeliveryRun(req, "alice", m)
	if e != nil || run.Status != "approved" || server.count("UNLINK") != 0 {
		t.Fatal("disabled change approval should create, not execute", run, e)
	}
	save("test", WorkbenchPolicy{ChangeApproval: true})
	if _, e := s.AdvanceDeliveryRun(run.ID, "alice", false, m); e == nil || server.count("UNLINK") != 0 {
		t.Fatal("tightened policy ignored")
	}
	save("test", WorkbenchPolicy{ChangeApproval: false})
	env.Protected = true
	env.Approvers = "reviewer"
	if e := s.UpsertEnvironment(env); e != nil {
		t.Fatal(e)
	}
	if protected, e := s.CreateDeliveryRun(req, "alice", m); e != nil || protected.Status != "pending_approval" {
		t.Fatal("protected environment waived approval")
	}
	if _, e := s.AdvanceDeliveryRun(run.ID, "alice", false, m); e == nil {
		t.Fatal("changed environment protection ignored")
	}
}

func TestWorkbenchReadTicketRejectsBindingAndReviewerDrift(t *testing.T) {
	for _, mode := range []string{"binding", "reviewer", "expired"} {
		t.Run(mode, func(t *testing.T) {
			s, _, server, _, _ := redisFixture(t)
			q := WorkbenchQuery{Service: "orders", Environment: "test", Alias: "cache", Commands: "TYPE menu:tree"}
			approvers, digest, e := s.queryTargets("redis", q)
			if e != nil {
				t.Fatal(e)
			}
			ticket := WorkbenchQueryTicket{Kind: "redis", Query: q, Status: "approved", Owner: "alice", Approver: "reviewer", Approvers: approvers, TargetDigest: digest, CreatedAt: time.Now().Unix(), Revision: 1}
			if mode == "expired" {
				ticket.CreatedAt -= 1801
			}
			data, _ := json.Marshal(ticket)
			res, e := s.db.Exec(`INSERT INTO workbench_queries(kind,document,status,owner,revision) VALUES(?,?,?,?,1)`, "redis", string(data), ticket.Status, ticket.Owner)
			if e != nil {
				t.Fatal(e)
			}
			id, _ := res.LastInsertId()
			if mode == "binding" {
				conn := s.GetConnection("qa-redis")
				conn.Config["key_prefix"] = "new:"
				s.UpsertConnection(*conn)
			}
			if mode == "reviewer" {
				env := *s.GetEnvironment("test")
				env.Approvers = "alice"
				s.UpsertEnvironment(env)
			}
			r := httptest.NewRequest("POST", fmt.Sprintf("/api/workbench/redis/query-requests/%d/execute", id), strings.NewReader("{}"))
			r.Header.Set("X-User", "alice")
			w := httptest.NewRecorder()
			s.handleWorkbenchQueries(w, r, "redis", fmt.Sprintf("query-requests/%d/execute", id), []byte("{}"))
			if w.Code == 200 || server.count("TYPE") != 0 {
				t.Fatal("query drift ignored", mode, w.Body.String())
			}
		})
	}
}
