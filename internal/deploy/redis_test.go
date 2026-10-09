package deploy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// A disposable RESP fixture exercises the real Go client (auth, DB selection,
// wire argv and dropped responses). It cannot reach user Redis instances.
type mockRedis struct {
	mu          sync.Mutex
	commands    [][]string
	keys        map[string]bool
	drop, fail  bool
	connections []net.Conn
	wg          sync.WaitGroup
}

func readRESPCommand(r *bufio.Reader) ([]string, error) {
	line, e := r.ReadString('\n')
	if e != nil {
		return nil, e
	}
	if !strings.HasPrefix(line, "*") {
		return nil, fmt.Errorf("not array")
	}
	n, e := strconv.Atoi(strings.TrimSpace(line[1:]))
	if e != nil || n < 1 || n > 100 {
		return nil, fmt.Errorf("bad array")
	}
	out := []string{}
	for i := 0; i < n; i++ {
		line, e = r.ReadString('\n')
		if e != nil || !strings.HasPrefix(line, "$") {
			return nil, fmt.Errorf("bad bulk")
		}
		size, e := strconv.Atoi(strings.TrimSpace(line[1:]))
		if e != nil || size < 0 || size > 40000 {
			return nil, fmt.Errorf("bad size")
		}
		b := make([]byte, size+2)
		if _, e = io.ReadFull(r, b); e != nil {
			return nil, e
		}
		out = append(out, string(b[:size]))
	}
	return out, nil
}
func mockRedisServer(t *testing.T) (*mockRedis, string) {
	t.Helper()
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	r := &mockRedis{keys: map[string]bool{"menu:tree": true}}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			r.mu.Lock()
			r.connections = append(r.connections, c)
			r.mu.Unlock()
			r.wg.Add(1)
			go func() {
				defer r.wg.Done()
				defer c.Close()
				reader := bufio.NewReader(c)
				authed, selected := false, false
				for {
					a, e := readRESPCommand(reader)
					if e != nil {
						return
					}
					a[0] = strings.ToUpper(a[0])
					reply := ""
					drop := false
					switch strings.ToUpper(a[0]) {
					case "HELLO":
						reply = "-ERR unknown command HELLO\r\n"
					case "AUTH":
						if len(a) == 3 && a[1] == "qa" && a[2] == "fixture-password" {
							authed = true
							reply = "+OK\r\n"
						} else {
							reply = "-WRONGPASS private-upstream-secret\r\n"
						}
					case "SELECT":
						if authed && len(a) == 2 && a[1] == "3" {
							selected = true
							reply = "+OK\r\n"
						} else {
							reply = "-ERR bad DB\r\n"
						}
					default:
						if !authed || !selected {
							reply = "-NOAUTH required\r\n"
							break
						}
						r.mu.Lock()
						r.commands = append(r.commands, append([]string{}, a...))
						if a[0] == "PING" {
							reply = "+PONG\r\n"
						} else if r.fail {
							reply = "-ERR private-upstream-secret\r\n"
						} else if a[0] == "TYPE" {
							reply = "+none\r\n"
							if r.keys[a[1]] {
								reply = "+string\r\n"
							}
						} else if a[0] == "GETRANGE" {
							value := strings.Repeat("v", 65537)
							reply = fmt.Sprintf("$%d\r\n%s\r\n", len(value), value)
						} else if a[0] == "UNLINK" {
							n := 0
							for _, k := range a[1:] {
								if r.keys[k] {
									n++
									delete(r.keys, k)
								}
							}
							reply = fmt.Sprintf(":%d\r\n", n)
							drop = r.drop
						} else {
							reply = ":1\r\n"
						}
						r.mu.Unlock()
					}
					if drop {
						return
					}
					if _, e = io.WriteString(c, reply); e != nil {
						return
					}
				}
			}()
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		r.mu.Lock()
		for _, c := range r.connections {
			c.Close()
		}
		r.mu.Unlock()
		r.wg.Wait()
	})
	return r, ln.Addr().String()
}
func (r *mockRedis) count(command string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.commands {
		if c[0] == command {
			n++
		}
	}
	return n
}
func redisFixture(t *testing.T) (*Store, *ReleaseManager, *mockRedis, *Change, DeliveryRequest) {
	t.Helper()
	s, m, _ := deliveryFixture(t)
	server, addr := mockRedisServer(t)
	env := *s.GetEnvironment("test")
	env.Approvers = "alice,reviewer"
	if e := s.UpsertEnvironment(env); e != nil {
		t.Fatal(e)
	}
	c := ResourceConnection{Name: "qa-redis", Kind: "redis", Endpoint: addr, Username: "qa", Password: "fixture-password", Config: map[string]string{"db": "3", "key_prefix": "menu:", "redis_auth": "password", "tls": "false"}}
	if e := s.UpsertConnection(c); e != nil {
		t.Fatal(e)
	}
	if e := s.SaveApplicationResources("orders", ApplicationResources{Requirements: []ApplicationResourceRequirement{{Alias: "cache", Kind: "redis"}}, Bindings: []ApplicationEnvironmentResource{{Alias: "cache", Environment: "test", Connection: c.Name}, {Alias: "cache", Environment: "prod", Connection: c.Name}}}); e != nil {
		t.Fatal(e)
	}
	change := &Change{Kind: "redis", Title: "Clear menu cache", Service: "orders", Owner: "alice", Redis: &RedisChange{ResourceAlias: "cache", Commands: "TYPE menu:tree\nUNLINK menu:tree\nTTL menu:session"}}
	if e := s.CreateChange(change); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SetChangeReady(change.ID, true); e != nil {
		t.Fatal(e)
	}
	tpl := &DeliveryTemplate{Name: "Redis only", Mode: "fixed", Owner: "alice", Nodes: []DeliveryNode{{ID: "cache", Kind: "redis", Name: "Clear cache", ChangeIDs: []int64{change.ID}}}}
	if e := s.SaveDeliveryTemplate(tpl); e != nil {
		t.Fatal(e)
	}
	return s, m, server, change, DeliveryRequest{TemplateID: tpl.ID, Environment: "test", Reason: "QA"}
}
func TestRedisReviewExecutionLedgerAndNoSecrets(t *testing.T) {
	s, m, server, c, req := redisFixture(t)
	if e := testIntegrationConnection(context.Background(), s.GetConnection("qa-redis")); e != nil {
		t.Fatal(e)
	}
	run, e := s.CreateDeliveryRun(req, "alice", m)
	if e != nil {
		t.Fatal(e)
	}
	if run.Status != "pending_approval" || len(run.Plan.Services) != 0 || server.count("UNLINK") != 0 {
		t.Fatal("preview executed or review bypassed")
	}
	data, _ := json.Marshal(run)
	if strings.Contains(string(data), "fixture-password") {
		t.Fatal("public credential leak")
	}
	var secret string
	_ = s.db.QueryRow(`SELECT secret FROM resource_connections WHERE name='qa-redis'`).Scan(&secret)
	if strings.Contains(secret, "fixture-password") {
		t.Fatal("plaintext credential")
	}
	if _, e = s.AdvanceDeliveryRun(run.ID, "alice", false, m); e == nil {
		t.Fatal("executed before review")
	}
	if _, e = s.ReviewDeliveryRun(run.ID, "alice", run.Digest, true); e == nil {
		t.Fatal("self review")
	}
	if _, e = s.ReviewDeliveryRun(run.ID, "reviewer", "wrong", true); e == nil {
		t.Fatal("changed digest reviewed")
	}
	if _, e = s.ReviewDeliveryRun(run.ID, "reviewer", run.Digest, true); e != nil {
		t.Fatal(e)
	}
	run, e = s.AdvanceDeliveryRun(run.ID, "alice", false, m)
	if e != nil || run.Steps[0].Status != "success" {
		t.Fatalf("advance: %#v %v", run, e)
	}
	if server.count("UNLINK") != 1 {
		t.Fatal("duplicate or absent command")
	}
	run, e = s.AdvanceDeliveryRun(run.ID, "alice", false, m)
	if e != nil || run.Status != "success" {
		t.Fatalf("completion: %v", e)
	}
	if _, e = s.CreateDeliveryRun(req, "alice", m); e == nil {
		t.Fatal("repeated change reserved again")
	}
	var success int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM redis_execution_ledger WHERE change_id=? AND status='success'`, c.ID).Scan(&success)
	if success != 3 {
		t.Fatalf("ledger successes %d", success)
	}
}
func TestRedisFailureUncertainAndNoRetry(t *testing.T) {
	for _, mode := range []string{"drop-after-write", "error"} {
		t.Run(mode, func(t *testing.T) {
			s, m, server, _, req := redisFixture(t)
			run, e := s.CreateDeliveryRun(req, "alice", m)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = s.ReviewDeliveryRun(run.ID, "reviewer", run.Digest, true); e != nil {
				t.Fatal(e)
			}
			server.mu.Lock()
			if mode == "drop-after-write" {
				server.drop = true
			} else {
				server.fail = true
			}
			server.mu.Unlock()
			run, e = s.AdvanceDeliveryRun(run.ID, "alice", false, m)
			if e != nil || run.Status != "failed" {
				t.Fatalf("not failed: %v %#v", e, run)
			}
			if server.count("TTL") != 0 || server.count("UNLINK") > 1 {
				t.Fatal("command retried or continued")
			}
			data, _ := json.Marshal(run)
			if strings.Contains(string(data), "private-upstream-secret") {
				t.Fatal("upstream response leak")
			}
			if _, e = s.AdvanceDeliveryRun(run.ID, "alice", false, m); e == nil {
				t.Fatal("failed run retry")
			}
			if _, e = s.CreateDeliveryRun(req, "alice", m); e == nil {
				t.Fatal("uncertain change retried")
			}
			var uncertain int
			_ = s.db.QueryRow(`SELECT COUNT(*) FROM redis_execution_ledger WHERE run_id=? AND status='uncertain'`, run.ID).Scan(&uncertain)
			if uncertain != 1 {
				t.Fatal("uncertainty not recorded")
			}
		})
	}
}
func TestRedisBindingApprovalAndScopeDrift(t *testing.T) {
	for _, mode := range []string{"missing-reviewer", "reviewer-removed", "prefix-drift", "db-drift", "change-status", "duplicate-node", "unscoped-key"} {
		t.Run(mode, func(t *testing.T) {
			s, m, server, c, req := redisFixture(t)
			if mode == "missing-reviewer" {
				env := *s.GetEnvironment("test")
				env.Approvers = ""
				_ = s.UpsertEnvironment(env)
				if _, e := s.CreateDeliveryRun(req, "alice", m); e == nil {
					t.Fatal("missing reviewer accepted")
				}
				return
			}
			if mode == "unscoped-key" {
				_, _ = s.SetChangeReady(c.ID, false)
				c = s.GetChange(c.ID)
				c.Redis.Commands = "UNLINK other:tree"
				if e := s.UpdateChange(c); e != nil {
					t.Fatal(e)
				}
				_, _ = s.SetChangeReady(c.ID, true)
				if _, e := s.CreateDeliveryRun(req, "alice", m); e == nil {
					t.Fatal("unscoped key accepted")
				}
				return
			}
			if mode == "duplicate-node" {
				tpl := s.DeliveryTemplate(req.TemplateID)
				node := tpl.Nodes[0]
				node.ID = "cache-two"
				tpl.Nodes = append(tpl.Nodes, node)
				_ = s.SaveDeliveryTemplate(tpl)
				if _, e := s.CreateDeliveryRun(req, "alice", m); e == nil {
					t.Fatal("duplicate node accepted")
				}
				return
			}
			run, e := s.CreateDeliveryRun(req, "alice", m)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = s.ReviewDeliveryRun(run.ID, "reviewer", run.Digest, true); e != nil {
				t.Fatal(e)
			}
			switch mode {
			case "reviewer-removed":
				env := *s.GetEnvironment("test")
				env.Approvers = "alice"
				_ = s.UpsertEnvironment(env)
			case "prefix-drift", "db-drift":
				conn := s.GetConnection("qa-redis")
				if mode == "prefix-drift" {
					conn.Config["key_prefix"] = "other:"
				} else {
					conn.Config["db"] = "2"
				}
				if e = s.UpsertConnection(*conn); e != nil {
					t.Fatal(e)
				}
			case "change-status":
				_, _ = s.SetChangeReady(c.ID, false)
			}
			run, e = s.AdvanceDeliveryRun(run.ID, "alice", false, m)
			if e == nil && run.Status != "failed" {
				t.Fatal("drift executed")
			}
			if server.count("UNLINK") != 0 {
				t.Fatal("write before drift check")
			}
		})
	}
}
func TestRedisConcurrencyAndInterruptedNode(t *testing.T) {
	s, m, server, _, req := redisFixture(t)
	run, e := s.CreateDeliveryRun(req, "alice", m)
	if e != nil {
		t.Fatal(e)
	}
	_, _ = s.ReviewDeliveryRun(run.ID, "reviewer", run.Digest, true)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = s.AdvanceDeliveryRun(run.ID, "alice", false, m) }()
	}
	wg.Wait()
	if server.count("UNLINK") != 1 {
		t.Fatal("concurrent duplicate")
	}
	s, m, server, _, req = redisFixture(t)
	run, e = s.CreateDeliveryRun(req, "alice", m)
	if e != nil {
		t.Fatal(e)
	}
	run, _ = s.ReviewDeliveryRun(run.ID, "reviewer", run.Digest, true)
	run.Status = "running"
	run.Steps[0].Status = "executing"
	_ = s.saveDeliveryRun(run)
	if _, e = s.AdvanceDeliveryRun(run.ID, "alice", false, m); e == nil || server.count("UNLINK") != 0 {
		t.Fatal("interrupted node retry")
	}
}
func TestRedisSyntaxAndConnectionValidation(t *testing.T) {
	for _, text := range []string{"", "FLUSHALL", "DEL menu:a", "SET menu:a value", "KEYS *", "EVAL 'return 1' 0", "CONFIG GET *", "SELECT 2", "GET menu:a", "PING extra", "EXPIRE menu:a 0", "PEXPIRE menu:a 999999999999", "UNLINK menu:a; PING", "UNLINK $(whoami)", "HDEL menu:a", "UNLINK 'unclosed", "PING\x00"} {
		if _, e := parseRedisCommands(text); e == nil {
			t.Errorf("accepted %q", text)
		}
	}
	a, e := parseRedisCommands("# comment\nunlink 'menu:key with space' menu:two\nHDEL menu:hash 'field one'\nEXPIRE menu:key 600\nPING")
	if e != nil || a[0][1] != "menu:key with space" || a[1][2] != "field one" {
		t.Fatalf("quotes %v %v", a, e)
	}
	s, _, _, _, _ := redisFixture(t)
	c := s.GetConnection("qa-redis")
	c.Password = ""
	if e = s.UpsertConnection(*c); e != nil {
		t.Fatal("password rotation blank retention", e)
	}
	c.Config["redis_auth"] = "none"
	if e = s.UpsertConnection(*c); e != nil {
		t.Fatal(e)
	}
	c = s.GetConnection(c.Name)
	if c.Password != "" || c.Username != "" {
		t.Fatal("none reused secret")
	}
	c.Config["redis_auth"] = "password"
	if e = s.UpsertConnection(*c); e == nil {
		t.Fatal("switched mode without password")
	}
	for _, config := range []map[string]string{{"db": "16", "key_prefix": "menu:"}, {"db": "0", "key_prefix": "*"}, {"db": "0", "key_prefix": "menu:", "tls": "insecure"}, {"db": "0", "key_prefix": ""}} {
		c.Config = config
		if e = validateRedisConnection(c); e == nil {
			t.Fatal("unsafe config")
		}
	}
}
func TestRedisPreviewAndLedgerRoutesAreReadOnly(t *testing.T) {
	s, _, server, c, _ := redisFixture(t)
	mux := Routes(s, nil, nil, "", nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", fmt.Sprintf("/api/changes/%d/redis-preview", c.ID), strings.NewReader(`{"environment":"test"}`))
	mux.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "UNLINK") || strings.Contains(w.Body.String(), "fixture-password") || server.count("UNLINK") != 0 {
		t.Fatalf("preview %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/redis-executions?run_id=-1", nil))
	if w.Code != 400 {
		t.Fatal("invalid run ID")
	}
}

func TestRedisVersionAllocationAndWorkDeliveryPayload(t *testing.T) {
	s, m, server, c, req := redisFixture(t)
	v := &ReleaseVersion{Version: "redis-v1", Title: "Redis release", Owner: "alice"}
	if e := s.CreateReleaseVersion(v); e != nil {
		t.Fatal(e)
	}
	if e := s.SetReleaseVersionChanges(v.ID, []int64{c.ID}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SetReleaseVersionReady(v.ID); e != nil {
		t.Fatal(e)
	}
	tpl := &DeliveryTemplate{Name: "Version Redis", Mode: "version", Owner: "alice", Nodes: []DeliveryNode{{ID: "cache", Kind: "redis", Name: "Cache"}}}
	if e := s.SaveDeliveryTemplate(tpl); e != nil {
		t.Fatal(e)
	}
	req.TemplateID, req.VersionID = tpl.ID, v.ID
	plan, _, e := s.ResolveDelivery(req, m)
	if e != nil || len(plan.Blockers) != 0 || len(plan.RedisPlans) != 1 || plan.RedisPlans[0].Changes[0].ChangeID != c.ID {
		t.Fatalf("version: %v %#v", e, plan)
	}
	other := &Change{Kind: "redis", Title: "other", Service: "orders", Owner: "alice", Redis: &RedisChange{ResourceAlias: "cache", Commands: "PING"}}
	if e = s.CreateChange(other); e != nil {
		t.Fatal(e)
	}
	_, _ = s.SetChangeReady(other.ID, true)
	req.Nodes = []DeliveryNode{{ID: "cache", Kind: "redis", Name: "Cache", ChangeIDs: []int64{other.ID}}}
	plan, _, e = s.ResolveDelivery(req, m)
	if e != nil || !strings.Contains(strings.Join(plan.Blockers, " "), "版本外") {
		t.Fatal("version-outside change accepted")
	}
	w := &WorkItem{Title: "menu bug", Kind: "bug", Source: "manual", Owner: "alice"}
	if e = s.CreateWorkItem(w); e != nil {
		t.Fatal(e)
	}
	content, e := s.WorkDelivery(w.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.SaveWorkDelivery(w.ID, &WorkDeliverySave{Digest: content.Digest, Changes: []Change{{Kind: "redis", Title: "cache", Service: "orders", Redis: &RedisChange{ResourceAlias: "cache", Commands: "UNLINK menu:tree"}}}}, "alice", false); e != nil {
		t.Fatal(e)
	}
	content, e = s.WorkDelivery(w.ID)
	if e != nil || len(content.Changes) != 1 || content.Changes[0].Redis == nil || content.Changes[0].Redis.Commands != "UNLINK menu:tree" {
		t.Fatalf("payload not loaded: %v %#v", e, content)
	}
	if server.count("UNLINK") != 0 {
		t.Fatal("version or work save executed command")
	}
}
