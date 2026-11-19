package deploy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkbenchSQLIndependentGrammar(t *testing.T) {
	for _, q := range []string{"SELECT * FROM menu", "SELECT id,name FROM menu ORDER BY id DESC LIMIT 20 OFFSET 40", "SELECT COUNT(*) AS total FROM menu WHERE id > 0", "SELECT kind,COUNT(id) AS total FROM menu GROUP BY kind ORDER BY total DESC LIMIT 10", "SELECT MAX(id) AS last_id FROM menu", "SELECT m.id,o.name FROM menu m JOIN other o ON m.id=o.id", "SELECT m.* FROM menu AS m LEFT JOIN other AS o USING (id)", "SELECT m.id,COUNT(o.id) AS total FROM menu m RIGHT JOIN other o ON m.id=o.id GROUP BY m.id ORDER BY total DESC LIMIT 10", "SELECT a.id,b.id AS parent_id FROM menu a JOIN menu b ON a.parent_id=b.id", "SELECT menu.id FROM menu", "SELECT m.id FROM (menu m JOIN other o ON m.id=o.id)", "SHOW TABLES", "DESCRIBE menu"} {
		if _, e := parseWorkbenchSQL(q); e != nil {
			t.Error(q, e)
		}
	}
	for _, q := range []string{"SELECT SLEEP(1) FROM menu", "SELECT m.id FROM menu m JOIN otherdb.other o ON m.id=o.id", "SELECT otherdb.m.id FROM menu m", "SELECT missing.* FROM menu m", "SELECT x.id FROM menu m", "SELECT m.id FROM menu m JOIN other m ON m.id=m.id", "SELECT m.id FROM menu m JOIN (SELECT id FROM other) o ON m.id=o.id", "SELECT m.id FROM menu m JOIN other o ON SLEEP(1)=0", "SELECT m.id FROM menu m JOIN other o ON m.id=o.id FOR UPDATE", "SELECT LOAD_FILE('/etc/passwd') FROM menu", "SELECT id FROM menu INTO OUTFILE '/tmp/out'", "SELECT id FROM menu FOR UPDATE", "SELECT @x FROM menu", "SELECT id FROM other.menu", "SELECT id FROM menu;DELETE FROM menu", "SELECT id FROM menu LIMIT 1001", "SELECT (SELECT id FROM secrets) FROM menu", "SELECT id FROM menu /*! INTO OUTFILE '/tmp/out' */", "DELETE FROM menu", "SELECT next 1 values FROM sequence", "SELECT GROUP_CONCAT(name) FROM menu"} {
		if _, e := parseWorkbenchSQL(q); e == nil {
			t.Error("accepted unsafe SQL", q)
		}
	}
	p, e := parseWorkbenchSQL("SELECT id FROM menu")
	if e != nil || p.Limit != 200 || !strings.HasSuffix(p.SQL, "limit 200") {
		t.Fatal(p, e)
	}
	if _, e := parseSyncQuery("SELECT * FROM menu ORDER BY id LIMIT 10"); e == nil {
		t.Fatal("interactive grammar leaked into sync")
	}
}

func TestWorkbenchTargetAndLedgerScope(t *testing.T) {
	s, manager, server, _, req := redisFixture(t)
	hub := NewLogHub()
	h := Routes(s, NewBuilder(s, t.TempDir(), hub), hub, "", NewStepLib(t.TempDir(), t.TempDir()))
	get := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("X-User", "alice")
		r.Header.Set("X-Role", "member")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := get("/api/workbench/redis/query-target?service=orders&environment=test&resource_alias=cache")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"key_prefix":"menu:"`) || strings.Contains(w.Body.String(), "password") || strings.Contains(w.Body.String(), "fixture-password") || server.count("PING") != 0 {
		t.Fatal("target metadata leaked secrets or queried Redis", w.Code, w.Body.String())
	}
	if w := get("/api/workbench/redis/query-target?service=orders&environment=missing&resource_alias=cache"); w.Code == 200 {
		t.Fatal("missing target accepted")
	}
	run, err := s.CreateDeliveryRun(req, "alice", manager)
	if err != nil {
		t.Fatal(err)
	}
	if w := get("/api/workbench/redis/redis-executions?run_id=" + jsonNumber(run.ID)); w.Code == 200 {
		t.Fatal("ordinary deployment ledger leaked into workbench")
	}
	tpl := run.Plan.Template
	tpl.ID = 0
	tpl.Workbench = "redis"
	if err := s.SaveDeliveryTemplate(&tpl); err != nil {
		t.Fatal(err)
	}
	req.TemplateID = tpl.ID
	run, err = s.CreateDeliveryRun(req, "alice", manager)
	if err != nil {
		t.Fatal(err)
	}
	if w := get("/api/workbench/redis/redis-executions?run_id=" + jsonNumber(run.ID)); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := get("/api/workbench/redis/redis-executions"); w.Code == 200 {
		t.Fatal("unscoped ledger exposed")
	}
	if w := get("/api/workbench/dbms/redis-executions?run_id=" + jsonNumber(run.ID)); w.Code == 200 {
		t.Fatal("foreign plugin ledger exposed")
	}
}
func TestWorkbenchRedisReadBoundsAndScope(t *testing.T) {
	c := &ResourceConnection{Config: map[string]string{"key_prefix": "menu:"}}
	for _, q := range []string{"SCAN 0 MATCH menu:* COUNT 50", "HSCAN menu:hash 0 COUNT 50", "SSCAN menu:set 0 COUNT 20", "ZSCAN menu:zset 0 COUNT 10", "HGET menu:hash name", "LRANGE menu:list 0 99", "ZRANGE menu:zset 0 50"} {
		a, e := parseRedisCommandText(q, redisReadKeys)
		if e != nil {
			t.Fatal(q, e)
		}
		if e = validateRedisReadScope(a, c); e != nil {
			t.Fatal(q, e)
		}
	}
	for _, q := range []string{"SCAN 0 MATCH * COUNT 10", "SCAN 0 MATCH menu:* COUNT 1000", "HGET other:hash name", "LRANGE menu:list 0 -1", "LRANGE menu:list 0 100", "KEYS menu:*", "HGETALL menu:hash", "SET menu:a value", "SCAN 0 MATCH menu:* COUNT 00", "HSCAN menu:hash 0 COUNT 00"} {
		a, e := parseRedisCommandText(q, redisReadKeys)
		if e == nil {
			e = validateRedisReadScope(a, c)
		}
		if e == nil {
			t.Fatal("unsafe Redis read", q)
		}
	}
	for _, reply := range []string{"$999999999\r\n", "*1000000\r\n"} {
		var b bytes.Buffer
		if e := readBoundedRESP(bufio.NewReader(strings.NewReader(reply)), &b, 0); e == nil {
			t.Fatal("oversized response accepted")
		}
		if b.Len() > 30 {
			t.Fatal("response allocated")
		}
	}
	var b bytes.Buffer
	reply := "*2\r\n$1\r\n0\r\n*1\r\n$6\r\nmenu:a\r\n"
	if e := readBoundedRESP(bufio.NewReader(strings.NewReader(reply)), &b, 0); e != nil || b.String() != reply {
		t.Fatal(b.String(), e)
	}
}

func TestWorkbenchBoundedRedisScanProtocol(t *testing.T) {
	s, _, server, _, _ := redisFixture(t)
	q := WorkbenchQuery{Service: "orders", Environment: "test", Alias: "cache", Commands: "SCAN 0 MATCH menu:* COUNT 50"}
	v, err := s.executeWorkbenchQuery(context.Background(), "redis", q, "alice")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(v)
	if !bytes.Contains(b, []byte(`"result":["0",["menu:tree"]]`)) || server.count("SCAN") != 1 || server.count("UNLINK") != 0 {
		t.Fatal(string(b))
	}
	if err := validateRedisReadScope([][]string{{"SCAN", "0", "MATCH", `menu\:*`, "COUNT", "10"}}, &ResourceConnection{Config: map[string]string{"key_prefix": `menu\:`}}); err == nil {
		t.Fatal("glob escape prefix bypass")
	}
}
func TestWorkbenchDraftPreviewNeverCreatesExecutionPermit(t *testing.T) {
	s, m, server, c, req := redisFixture(t)
	if _, e := s.SetChangeReady(c.ID, false); e != nil {
		t.Fatal(e)
	}
	tpl := DeliveryTemplate{Workbench: "redis", Name: "Draft", Mode: "fixed", Owner: "alice", Nodes: []DeliveryNode{{ID: "op", Kind: "redis", Name: "Cache", ChangeIDs: []int64{c.ID}}}}
	if e := s.SaveDeliveryTemplate(&tpl); e != nil {
		t.Fatal(e)
	}
	req.TemplateID = tpl.ID
	req.previewDrafts = true
	p, _, e := s.ResolveDelivery(req, m)
	if e != nil || len(p.Blockers) > 0 {
		t.Fatal(p, e)
	}
	if s.GetChange(c.ID).Status != ChangeDraft || server.count("UNLINK") != 0 {
		t.Fatal("preview mutated or executed")
	}
	if _, e = s.CreateDeliveryRun(req, "alice", m); e == nil {
		t.Fatal("draft preview created permit")
	}
	// A JSON field cannot enable preview-only behavior.
	var decoded DeliveryRequest
	json.Unmarshal([]byte(`{"previewDrafts":true,"preview_drafts":true}`), &decoded)
	if decoded.previewDrafts {
		t.Fatal("client enabled preview flag")
	}
}
