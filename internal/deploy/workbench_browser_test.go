package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkbenchBrowserMetadataGrammar(t *testing.T) {
	for input, metadata := range map[string]string{
		"DESCRIBE `MixedCase`": "schema", "desc MixedCase;": "schema",
		"SHOW INDEX FROM `MixedCase`": "indexes", "show keys from MixedCase;": "indexes",
	} {
		p, err := parseWorkbenchSQL(input)
		if err != nil || p.Metadata != metadata || p.Table != "MixedCase" || p.Limit != 1000 {
			t.Fatalf("metadata or case lost for %q: %#v %v", input, p, err)
		}
	}
	for _, input := range []string{"SHOW INDEX FROM otherdb.menu", "SHOW INDEX FROM menu; DELETE FROM menu", "SHOW INDEX FROM 'menu'", "SHOW INDEX FROM menu WHERE 1=1", "SHOW INDEX FROM menu /* comment */", "DESCRIBE menu; SELECT * FROM menu"} {
		if _, err := parseWorkbenchSQL(input); err == nil {
			t.Fatal("unsafe metadata query accepted", input)
		}
	}
	for _, input := range []string{"ZRANGE menu:zset 0 49 WITHSCORES", "ZRANGE menu:zset 50 99 withscores"} {
		if _, err := parseRedisCommandText(input, redisReadKeys); err != nil {
			t.Fatal(input, err)
		}
	}
	for _, input := range []string{"ZRANGE menu:zset 0 100 WITHSCORES", "LRANGE menu:list 0 49 WITHSCORES", "ZRANGE menu:zset 0 -1 WITHSCORES"} {
		if _, err := parseRedisCommandText(input, redisReadKeys); err == nil {
			t.Fatal("unbounded range accepted", input)
		}
	}
}

func TestWorkbenchDirectRedisBrowserScopeAndApproval(t *testing.T) {
	s, _, server, _, _ := redisFixture(t)
	c := *s.GetConnection("qa-redis")
	c.Name = "unbound-cache"
	if err := s.UpsertConnection(c); err != nil {
		t.Fatal(err)
	}
	q := WorkbenchQuery{Connection: c.Name, Environment: "test", Commands: "SCAN 0 MATCH menu:* COUNT 50"}
	if _, _, err := s.queryTargets("redis", q); err != nil {
		t.Fatal("unbound Redis query rejected", err)
	}
	for _, bad := range []WorkbenchQuery{
		{Connection: c.Name, Environment: "missing", Commands: q.Commands},
		{Connection: c.Name, Environment: "test", Service: "orders", Commands: q.Commands},
		{Connection: c.Name, Environment: "test", Alias: "cache", Commands: q.Commands},
		{Connection: c.Name, Environment: "test", Commands: "SCAN 0 MATCH * COUNT 50"},
		{Connection: c.Name, Environment: "test", Commands: "GET other:secret"},
		{Connection: c.Name, Environment: "test", Commands: "KEYS menu:*"},
		{Connection: c.Name, Environment: "test", Commands: "UNLINK menu:tree"},
	} {
		if _, _, err := s.queryTargets("redis", bad); err == nil {
			t.Fatal("direct connection bypassed scope", bad)
		}
	}
	hub := NewLogHub()
	h := Routes(s, NewBuilder(s, t.TempDir(), hub), hub, "", NewStepLib(t.TempDir(), t.TempDir()))
	call := func(method, path, actor string, body any) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "/api/workbench/redis/"+path, bytes.NewReader(data))
		r.Header.Set("X-User", actor)
		r.Header.Set("X-Role", "admin")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := call("GET", "query-target?environment=test&connection="+c.Name, "alice", nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"key_prefix":"menu:"`) {
		t.Fatal("direct target unavailable", w.Body.String())
	}
	w := call("POST", "query", "alice", q)
	if w.Code != 200 || server.count("SCAN") != 1 || server.count("UNLINK") != 0 {
		t.Fatal("browser query did not perform exactly one read", w.Body.String())
	}
	if w := call("PUT", "policy?environment=test", "root", WorkbenchPolicy{QueryApproval: true, ChangeApproval: true}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := call("POST", "query", "alice", q); w.Code == 200 || server.count("SCAN") != 1 {
		t.Fatal("direct browser bypassed approval", w.Body.String())
	}
	w = call("POST", "query-requests", "alice", q)
	var ticket WorkbenchQueryTicket
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &ticket) != nil {
		t.Fatal(w.Body.String())
	}
	path := fmt.Sprintf("query-requests/%d/", ticket.ID)
	if w := call("POST", path+"approve", "alice", map[string]any{}); w.Code == 200 {
		t.Fatal("self approval accepted")
	}
	if w := call("POST", path+"approve", "reviewer", map[string]any{}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	c.Config["key_prefix"] = "other:"
	if err := s.UpsertConnection(c); err != nil {
		t.Fatal(err)
	}
	if w := call("POST", path+"execute", "alice", map[string]any{}); w.Code == 200 || server.count("SCAN") != 1 {
		t.Fatal("approved query ignored changed scope", w.Body.String())
	}
}
