package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkbenchDirectDatabaseQueryTargetsAndApproval(t *testing.T) {
	s, _, _, _, _ := redisFixture(t)
	c := ResourceConnection{Name: "unbound-db", Kind: "mysql", Endpoint: "127.0.0.1:1", Username: "reader", Password: "query-secret", Config: map[string]string{}}
	if err := s.UpsertConnection(c); err != nil {
		t.Fatal(err)
	}
	q := WorkbenchQuery{Connection: c.Name, Environment: "test", Database: "reporting", SQL: "SELECT a.id,b.name FROM orders a LEFT JOIN customers b ON a.customer_id=b.id"}
	_, digest, err := s.queryTargets("dbms", q)
	if err != nil {
		t.Fatal("unbound database rejected", err)
	}
	for _, bad := range []WorkbenchQuery{
		{Connection: c.Name, Environment: "missing", SQL: q.SQL},
		{Connection: "missing", Environment: "test", SQL: q.SQL},
		{Connection: c.Name, Service: "orders", Environment: "test", SQL: q.SQL},
		{Connection: c.Name, Alias: "db", Environment: "test", SQL: q.SQL},
		{Connection: c.Name, TaskID: 1, Environment: "test", SQL: q.SQL},
		{Connection: c.Name, Environment: "test", SQL: "DELETE FROM orders"},
	} {
		if _, _, err := s.queryTargets("dbms", bad); err == nil {
			t.Fatal("invalid direct target accepted", bad)
		}
	}
	if _, _, err := s.queryTargets("redis", q); err == nil {
		t.Fatal("Redis accepted direct database target")
	}
	if err := s.UpsertConnection(ResourceConnection{Name: "not-db", Kind: "git", Endpoint: "https://example.com/repo"}); err != nil {
		t.Fatal(err)
	}
	wrong := q
	wrong.Connection = "not-db"
	if _, _, err := s.queryTargets("dbms", wrong); err == nil {
		t.Fatal("non-MySQL connection accepted")
	}
	hub := NewLogHub()
	h := Routes(s, NewBuilder(s, t.TempDir(), hub), hub, "", NewStepLib(t.TempDir(), t.TempDir()))
	call := func(method, path, actor string, body any) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "/api/workbench/dbms/"+path, bytes.NewReader(data))
		r.Header.Set("X-User", actor)
		r.Header.Set("X-Role", "admin")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := call("GET", "query-target?environment=test&connection=unbound-db&database=reporting", "alice", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"database":"reporting"`) || strings.Contains(w.Body.String(), "query-secret") {
		t.Fatal("target invalid or leaked credentials", w.Body.String())
	}
	if w := call("PUT", "policy?environment=test", "root", WorkbenchPolicy{QueryApproval: true, ChangeApproval: true}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = call("POST", "query", "alice", q)
	if w.Code == 200 || !strings.Contains(w.Body.String(), "审批") {
		t.Fatal("direct query bypassed approval", w.Body.String())
	}
	w = call("POST", "query-requests", "alice", q)
	var ticket WorkbenchQueryTicket
	if json.Unmarshal(w.Body.Bytes(), &ticket) != nil || w.Code != 200 || ticket.Query.Connection != c.Name || ticket.TargetDigest != digest || strings.Contains(w.Body.String(), "query-secret") {
		t.Fatal("direct query request failed", w.Body.String())
	}
	path := fmt.Sprintf("query-requests/%d/", ticket.ID)
	if w := call("POST", path+"approve", "alice", map[string]any{}); w.Code == 200 {
		t.Fatal("self approval accepted")
	}
	if w := call("POST", path+"approve", "reviewer", map[string]any{}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	c.Password = "rotated-secret"
	if err := s.UpsertConnection(c); err != nil {
		t.Fatal(err)
	}
	w = call("POST", path+"execute", "alice", map[string]any{})
	if w.Code == 200 || !strings.Contains(w.Body.String(), "目标连接或任务已变化") {
		t.Fatal("approved direct query ignored connection drift", w.Body.String())
	}
}
