package server

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"logplatform/internal/types"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func logFixtureEvent(id string) types.Event {
	return types.Event{ID: id, Ts: time.Now().Add(-time.Second).UnixMilli(), Service: "orders", Environment: "test", Host: "node-a", Level: "ERROR", Message: "request timeout 中文\n at com.demo.Order.run(Order.java:5)"}
}
func proxyLogStorage(t *testing.T, mode *atomic.Int32) (*Storage, string) {
	t.Helper()
	upstream, tenant := testVictoriaLogs(t)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode.Load() == 1 {
			http.Error(w, "unavailable", 503)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if mode.Load() == 3 && r.URL.Path == "/insert/jsonline" {
			var filtered bytes.Buffer
			for _, line := range bytes.Split(body, []byte("\n")) {
				if len(line) > 0 && !bytes.Contains(line, []byte("blocked-record")) {
					filtered.Write(line)
					filtered.WriteByte('\n')
				}
			}
			body = filtered.Bytes()
		}
		req, _ := http.NewRequestWithContext(r.Context(), r.Method, upstream+r.URL.RequestURI(), bytes.NewReader(body))
		req.Header = r.Header.Clone()
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			http.Error(w, "fixture backend unavailable", 503)
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if mode.Load() == 2 && r.URL.Path == "/insert/jsonline" {
			c, _, _ := w.(http.Hijacker).Hijack()
			c.Close()
			return
		}
		for key, values := range resp.Header {
			for _, v := range values {
				w.Header().Add(key, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		w.Write(b)
	}))
	t.Cleanup(proxy.Close)
	opts := DefaultStorageOptions()
	opts.URL = proxy.URL
	opts.Token = ""
	opts.ProjectID = tenant
	opts.RequestTimeout = time.Second
	opts.QueueMaxBytes = 64 << 20
	root := t.TempDir()
	s, err := OpenVictoriaLogs(root, opts)
	if err != nil {
		t.Fatal(err)
	}
	return s, root
}
func TestVictoriaLogsQueueOfflineRestartAndIdentityConflict(t *testing.T) {
	var mode atomic.Int32
	mode.Store(1)
	s, root := proxyLogStorage(t, &mode)
	opts := s.opts
	e := logFixtureEvent("offline")
	events := []types.Event{e}
	hash := hashLog(events)
	if out, _, err := s.AcceptBatch("agent", "batch", hash, events); err != nil || len(out) != 1 {
		t.Fatal(out, err)
	}
	if _, err := s.Search("orders", "test", "", "", e.Ts-1, e.Ts+1, "", "", "", 10); err == nil {
		t.Fatal("backend failure silently fell back to SQLite")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := OpenVictoriaLogs(root, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if out, _, err := s.AcceptBatch("agent", "batch", hash, events); err != nil || len(out) != 0 {
		t.Fatal("retry after reopen duplicated", out, err)
	}
	changed := e
	changed.Message = "different"
	if _, _, err = s.AcceptBatch("agent", "batch", hashLog([]types.Event{changed}), []types.Event{changed}); err != ErrLogIdentityConflict {
		t.Fatal("batch identity changed", err)
	}
	mode.Store(0)
	awaitTestLogDelivery(t, s)
	out, err := s.Search("orders", "test", "", "", e.Ts-1, e.Ts+1, "", "", "", 10)
	if err != nil || len(out.Hits) != 1 || out.Hits[0].Message != e.Message {
		t.Fatal(out, err)
	}
	// A pruned replay payload must leave its immutable batch receipt for retries.
	s.deliveryMu.Lock()
	_, err = s.db.Exec(`DELETE FROM log_batches`)
	s.deliveryMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.AcceptBatch("agent", "batch", hashLog([]types.Event{changed}), []types.Event{changed}); err != ErrLogIdentityConflict {
		t.Fatal("pruning erased immutable receipt", err)
	}
}
func TestVictoriaLogsLostResponseAndPhysicalDuplicatesDoNotInflateQueries(t *testing.T) {
	var mode atomic.Int32
	mode.Store(2)
	s, _ := proxyLogStorage(t, &mode)
	defer s.Close()
	e := logFixtureEvent("lost-response")
	if _, err := s.Insert([]types.Event{e}); err != nil {
		t.Fatal(err)
	}
	_, _ = s.deliverOne()
	mode.Store(0)
	awaitTestLogDelivery(t, s)
	var packed []byte
	if err := s.db.QueryRow(`SELECT payload FROM log_batches LIMIT 1`).Scan(&packed); err != nil {
		t.Fatal(err)
	}
	evs, err := unpackLogs(packed)
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately deliver the same immutable event twice to the actual backend.
	wire, _ := logsWire(evs)
	for i := 0; i < 2; i++ {
		resp, err := s.request(s.ctx, "POST", "/insert/jsonline", url.Values{"_stream_fields": {"application_id,environment,host"}}, wire, "application/x-ndjson")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	awaitTestLogDelivery(t, s)
	rows, err := s.query(s.ctx, "* | stats count() as n", 0, 0)
	if err != nil || len(rows) != 1 || rows[0]["n"] == "1" {
		t.Fatal("fixture did not create physical duplicates", rows, err)
	}
	out, err := s.Search("orders", "test", "", "ERROR", e.Ts-1, e.Ts+1, "timeout", "", "", 10)
	if err != nil || len(out.Hits) != 1 {
		t.Fatal(out, err)
	}
	counts, err := s.CountSeries("orders", "test", "ERROR", "timeout", e.Ts-1, e.Ts+1)
	if err != nil || len(counts) != 1 || counts[0].Count != 1 {
		t.Fatal(counts, err)
	}
	services, err := s.ListServices()
	if err != nil || services["orders"] != 1 {
		t.Fatal(services, err)
	}
}
func TestVictoriaLogsCursorTiesConcurrentInsertAndInclusiveEnd(t *testing.T) {
	s, err := openTestLogStorage(t)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	e := logFixtureEvent("tied")
	events := []types.Event{}
	for i := 0; i < 55; i++ {
		x := e
		x.ID = fmt.Sprint(i)
		events = append(events, x)
	}
	if _, err = s.Insert(events); err != nil {
		t.Fatal(err)
	}
	awaitTestLogDelivery(t, s)
	first, err := s.Search("orders", "test", "", "", e.Ts, e.Ts, "", "", "", 10)
	if err != nil || len(first.Hits) != 10 || !first.HasMore {
		t.Fatal(first, err)
	}
	late := e
	late.ID = "late"
	if _, err = s.Insert([]types.Event{late}); err != nil {
		t.Fatal(err)
	}
	awaitTestLogDelivery(t, s)
	seen := map[string]bool{}
	for _, h := range first.Hits {
		seen[h.EventID] = true
	}
	cursor := first.NextCursor
	for i := 0; cursor != "" && i < 10; i++ {
		p, err := s.Search("orders", "test", "", "", e.Ts, e.Ts, "", "", cursor, 10)
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range p.Hits {
			if seen[h.EventID] || h.EventID == "late" {
				t.Fatal("duplicate or concurrent insert crossed cursor", h)
			}
			seen[h.EventID] = true
		}
		cursor = p.NextCursor
	}
	if len(seen) != 55 {
		t.Fatal("cursor omitted tied records", len(seen))
	}
	if _, err = s.Search("orders", "prod", "", "", e.Ts, e.Ts, "", "", first.NextCursor, 10); err == nil {
		t.Fatal("foreign query cursor accepted")
	}
	if _, err = s.Search("orders", "test", "", "", e.Ts, e.Ts, "", "", "old_42", 10); err == nil {
		t.Fatal("obsolete rowid cursor accepted")
	}
}
func TestVictoriaLogsBadRecordsPartialAckAndExplicitRepair(t *testing.T) {
	var mode atomic.Int32
	s, _ := proxyLogStorage(t, &mode)
	defer s.Close()
	good := logFixtureEvent("good")
	bad := logFixtureEvent("bad")
	bad.Ts = 1
	api := NewAPI(s, "secret", "")
	ack := postIngest(t, api, types.IngestRequest{AgentID: "agent", BatchID: "mixed", Events: []types.Event{good, bad}})
	if ack.Accepted != 1 || ack.Acknowledged != 2 || ack.Quarantined != 1 {
		t.Fatal(ack)
	}
	q, err := s.quarantineList()
	if err != nil || len(q) != 1 {
		t.Fatal(q, err)
	}
	if _, err = s.repairQuarantine(q[0].ID, good); err != nil {
		t.Fatal(err)
	}
	if _, err = s.repairQuarantine(q[0].ID, good); err == nil {
		t.Fatal("duplicate repair executed")
	}
	awaitTestLogDelivery(t, s)
	var packed []byte
	if err = s.db.QueryRow(`SELECT payload FROM log_quarantine WHERE id=?`, q[0].ID).Scan(&packed); err != nil || len(packed) == 0 {
		t.Fatal("rejected original removed", err)
	}
	// The sink skips one otherwise valid JSON line with HTTP 200.
	mode.Store(3)
	blocked := logFixtureEvent("blocked")
	blocked.Message = "blocked-record"
	ok := logFixtureEvent("suffix")
	if _, err = s.Insert([]types.Event{blocked, ok}); err != nil {
		t.Fatal(err)
	}
	_, _ = s.deliverOne()
	resp, err := s.request(s.ctx, "POST", "/internal/force_flush", nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	s.deliveryMu.Lock()
	_, err = s.db.Exec(`UPDATE log_batches SET attempts=5,checked_at=0 WHERE visible_at=0`)
	s.deliveryMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		_, err = s.deliverOne()
		if err != nil {
			t.Fatal(err)
		}
	}
	q, err = s.quarantineList()
	if err != nil || len(q) != 2 {
		t.Fatal("missing record not quarantined", q, err)
	}
	if s.StorageStatus().Quarantined != 1 {
		t.Fatal("resolved item counted as pending quarantine")
	}
	mode.Store(0)
	awaitTestLogDelivery(t, s)
	hits, err := s.Search("orders", "test", "", "", good.Ts-10000, time.Now().UnixMilli(), "", "", "", 20)
	if err != nil || len(hits.Hits) != 3 {
		t.Fatal("valid suffix lost", hits, err)
	}
}
func TestVictoriaLogsRealSQLiteFullDoesNotLeaveAcknowledgedKeys(t *testing.T) {
	s, err := openTestLogStorage(t)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.deliveryMu.Lock()
	defer s.deliveryMu.Unlock()
	var pages int
	if err = s.db.QueryRow(`PRAGMA page_count`).Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("PRAGMA max_page_count=" + strconv.Itoa(pages)); err != nil {
		t.Fatal(err)
	}
	e := logFixtureEvent("disk-full")
	var random [64000]byte
	for i := range random {
		random[i] = byte((i*17 + i/37) % 251)
	}
	e.Message = fmt.Sprintf("%x", random)
	if _, err = s.Insert([]types.Event{e}); err == nil {
		t.Fatal("actual SQLite page quota did not reject batch")
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM log_receipts`).Scan(&n)
	if n != 0 {
		t.Fatal("failed transaction left dedupe keys", n)
	}
	_, _ = s.db.Exec(`PRAGMA max_page_count=100000`)
	if n, err := s.Insert([]types.Event{e}); err != nil || n != 1 {
		t.Fatal("retry after capacity restored", n, err)
	}
}

type logCrashWriter struct{ header http.Header }

func (w logCrashWriter) Header() http.Header { return w.header }
func (w logCrashWriter) WriteHeader(int)     {}
func (w logCrashWriter) Write([]byte) (int, error) {
	fmt.Println("crash-after-commit-before-ack")
	os.Exit(0)
	return 0, nil
}
func TestVictoriaLogsQueueCrashHelper(t *testing.T) {
	root := os.Getenv("VLOGS_CRASH_ROOT")
	if root == "" {
		t.Skip("subprocess-only crash helper")
	}
	o := DefaultStorageOptions()
	o.URL = "http://127.0.0.1:1"
	s, err := OpenVictoriaLogs(root, o)
	if err != nil {
		t.Fatal(err)
	}
	api := NewAPI(s, "secret", "") // Use the parent-frozen fixture bytes below.
	b, err := os.ReadFile(filepath.Join(root, "request.json"))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/ingest", bytes.NewReader(b))
	req.Header.Set("X-Token", "secret")
	api.Routes().ServeHTTP(logCrashWriter{make(http.Header)}, req)
	t.Fatal("crash hook not reached")
}
func TestVictoriaLogsQueueProcessCrashBeforeAckRetainsExactPayload(t *testing.T) {
	root := t.TempDir()
	e := logFixtureEvent("crash-event")
	req := types.IngestRequest{AgentID: "agent", BatchID: "crash-batch", Events: []types.Event{e}}
	b, _ := json.Marshal(req)
	if err := os.WriteFile(filepath.Join(root, "request.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestVictoriaLogsQueueCrashHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), "VLOGS_CRASH_ROOT="+root)
	out, err := cmd.CombinedOutput()
	if err != nil || !bytes.Contains(out, []byte("crash-after-commit-before-ack")) {
		t.Fatal(string(out), err)
	}
	u, tenant := testVictoriaLogs(t)
	o := DefaultStorageOptions()
	o.URL = u
	o.ProjectID = tenant
	s, err := OpenVictoriaLogs(root, o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	accepted, _, err := s.AcceptBatch(req.AgentID, req.BatchID, hashLog(req.Events), req.Events)
	if err != nil || len(accepted) != 0 {
		t.Fatal("committed batch was lost/repeated", accepted, err)
	}
	awaitTestLogDelivery(t, s)
	hits, err := s.Search("orders", "test", "", "", e.Ts-1, e.Ts+1, "", "", "", 10)
	if err != nil || len(hits.Hits) != 1 || hits.Hits[0].Message != e.Message {
		t.Fatal(hits, err)
	}
}
func TestVictoriaLogsQueuePruneKeepsRetryKeysAndReleasesPages(t *testing.T) {
	s, err := openTestLogStorage(t)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	e := logFixtureEvent("prune")
	if _, err = s.Insert([]types.Event{e}); err != nil {
		t.Fatal(err)
	}
	awaitTestLogDelivery(t, s)
	s.deliveryMu.Lock()
	_, err = s.db.Exec(`UPDATE log_batches SET sent_at=?,checked_at=0`, time.Now().Add(-2*time.Hour).UnixMilli())
	s.deliveryMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.deliverOne(); err != nil {
		t.Fatal(err)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM log_batches`).Scan(&n)
	if n != 0 {
		t.Fatal("expired verified replay was not pruned")
	}
	if accepted, err := s.InsertUnique([]types.Event{e}); err != nil || len(accepted) != 0 {
		t.Fatal("retry keys prematurely pruned", accepted, err)
	}
	if err = s.maintainQueue(); err != nil {
		t.Fatal(err)
	}
	var mode int
	s.db.QueryRow(`PRAGMA auto_vacuum`).Scan(&mode)
	if mode != 2 {
		t.Fatal("queue pages not configured for reclamation", mode)
	}
}
func TestVictoriaLogsMigratesOnlyAlertSettingsAndKeepsPrimaryKeys(t *testing.T) {
	root := t.TempDir()
	old, err := sqlOpenLegacyFixture(filepath.Join(root, "logs.db"))
	if err != nil {
		t.Fatal(err)
	}
	old.Close()
	u, tenant := testVictoriaLogs(t)
	o := DefaultStorageOptions()
	o.URL = u
	o.ProjectID = tenant
	s, err := OpenVictoriaLogs(root, o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name LIKE 'logs_%'`).Scan(&n)
	if n != 0 {
		t.Fatal("old searchable log schema copied")
	}
	if _, err = s.db.Exec(`INSERT INTO alert_rules(name,service,environment,level,threshold,duration_min,webhook) VALUES('new','orders','test','ERROR',1,1,'')`); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err = s.db.QueryRow(`SELECT id FROM alert_rules WHERE name='new'`).Scan(&id); err != nil || id <= 7 {
		t.Fatal("configuration identity damaged", id, err)
	}
}
func sqlOpenLegacyFixture(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		return nil, err
	}
	_, err = db.Exec(`CREATE TABLE alert_rules(id INTEGER PRIMARY KEY,name TEXT,service TEXT,level TEXT,threshold INTEGER,duration_min INTEGER,webhook TEXT);INSERT INTO alert_rules VALUES(7,'preserved','orders','ERROR',1,1,'');CREATE TABLE logs_20261105(message TEXT);INSERT INTO logs_20261105 VALUES('not migrated');`)
	return db, err
}

func TestVictoriaLogsConcurrentBatchReceiptsAreAtomic(t *testing.T) {
	var mode atomic.Int32
	mode.Store(1)
	s, _ := proxyLogStorage(t, &mode)
	defer s.Close()
	e := logFixtureEvent("concurrent")
	events := []types.Event{e}
	hash := hashLog(events)
	var wg sync.WaitGroup
	results := make(chan int, 32)
	errs := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a, _, err := s.AcceptBatch("agent", "same-batch", hash, events)
			if err != nil {
				errs <- err
			}
			results <- len(a)
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	total := 0
	for n := range results {
		total += n
	}
	if total != 1 {
		t.Fatal("concurrent requests accepted duplicates", total)
	}
	fresh := logFixtureEvent("new-event")
	changed := e
	changed.Message = "different"
	if _, _, err := s.AcceptBatch("agent", "other-batch", hashLog([]types.Event{fresh, changed}), []types.Event{fresh, changed}); err != ErrLogIdentityConflict {
		t.Fatal("changed event accepted under new batch", err)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM log_receipts`).Scan(&n)
	if n != 1 {
		t.Fatal("conflicting batch partly committed", n)
	}
}

type controlledLogBackend struct {
	mu                 sync.Mutex
	binary, addr, root string
	cmd                *exec.Cmd
	file               *os.File
}

func (c *controlledLogBackend) start(t *testing.T) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	var err error
	c.file, err = os.OpenFile(filepath.Join(c.root, "backend.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	c.cmd = exec.Command(c.binary, "-httpListenAddr="+c.addr, "-storageDataPath="+filepath.Join(c.root, "logs"), "-inmemoryDataFlushInterval=1s", "-memory.allowedBytes=64MiB")
	c.cmd.Env = append(os.Environ(), "GOMAXPROCS=2", "GOMEMLIMIT=256MiB")
	c.cmd.Stdout = c.file
	c.cmd.Stderr = c.file
	if err = c.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		r, e := http.Get("http://" + c.addr + "/health")
		if e == nil {
			r.Body.Close()
			if r.StatusCode == 200 {
				return
			}
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("controlled backend not ready")
}
func (c *controlledLogBackend) stop(kill bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cmd != nil {
		if kill {
			c.cmd.Process.Kill()
		} else {
			c.cmd.Process.Signal(os.Interrupt)
		}
		c.cmd.Wait()
		c.file.Close()
		c.cmd = nil
	}
}
func TestVictoriaLogsBackendSIGKILLAfterSuccessfulWriteReplaysQueue(t *testing.T) {
	binary := os.Getenv("VLOGS_TEST_BINARY")
	if binary == "" {
		t.Skip("actual backend integration requires VLOGS_TEST_BINARY")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	c := &controlledLogBackend{binary: binary, addr: addr, root: t.TempDir()}
	c.start(t)
	t.Cleanup(func() { c.stop(false) })
	var killed atomic.Bool
	var killOnce sync.Once
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		req, _ := http.NewRequestWithContext(r.Context(), r.Method, "http://"+addr+r.URL.RequestURI(), bytes.NewReader(b))
		req.Header = r.Header.Clone()
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			http.Error(w, "backend stopped", 503)
			return
		}
		defer resp.Body.Close()
		out, _ := io.ReadAll(resp.Body)
		if r.URL.Path == "/insert/jsonline" {
			killOnce.Do(func() { c.stop(true); killed.Store(true) })
		}
		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		w.Write(out)
	}))
	defer proxy.Close()
	opts := DefaultStorageOptions()
	opts.URL = proxy.URL
	opts.Token = ""
	opts.RequestTimeout = time.Second
	s, err := OpenVictoriaLogs(t.TempDir(), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	e := logFixtureEvent("backend-crash")
	if _, err = s.Insert([]types.Event{e}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !killed.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !killed.Load() {
		t.Fatal("fixture did not kill its backend")
	}
	// Queue acceptance already committed. Restart the same backend data directory.
	c.start(t)
	rows, err := s.query(s.ctx, "* | stats count() as n", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("physical records immediately after backend restart", rows)
	s.deliveryMu.Lock()
	_, err = s.db.Exec(`UPDATE log_batches SET sent_at=?,checked_at=0`, time.Now().Add(-2*time.Hour).UnixMilli())
	s.deliveryMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	awaitTestLogDelivery(t, s)
	hits, err := s.Search("orders", "test", "", "", e.Ts-1, e.Ts+1, "", "", "", 10)
	if err != nil || len(hits.Hits) != 1 || hits.Hits[0].Message != e.Message {
		t.Fatal("backend lost accepted event", hits, err)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM log_batches`).Scan(&n)
	if len(rows) > 0 && rows[0]["n"] == "0" && n != 1 {
		t.Fatal("freshly replayed tail pruned using original delivery age", n)
	}
}
func TestVictoriaLogsRedirectDoesNotForwardBackendCredentials(t *testing.T) {
	var leaked atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Store(true) }))
	defer other.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, http.StatusFound) }))
	defer redirect.Close()
	o := DefaultStorageOptions()
	o.URL = redirect.URL
	o.Token = "fixture-secret"
	s, err := OpenVictoriaLogs(t.TempDir(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.Search("orders", "test", "", "", time.Now().Add(-time.Hour).UnixMilli(), time.Now().UnixMilli(), "", "", "", 10); err == nil {
		t.Fatal("redirect followed")
	}
	if leaked.Load() {
		t.Fatal("backend credential forwarded to redirect")
	}
}

func TestVictoriaLogsMaximumAgentBatchBelowQueryLimits(t *testing.T) {
	s, err := openTestLogStorage(t)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	e := logFixtureEvent("large-batch")
	events := []types.Event{}
	for i := 0; i < 2000; i++ {
		x := e
		x.ID = fmt.Sprintf("max-%04d", i)
		events = append(events, x)
	}
	if n, err := s.Insert(events); err != nil || n != 2000 {
		t.Fatal(n, err)
	}
	awaitTestLogDelivery(t, s)
	counts, err := s.CountSeries("orders", "test", "ERROR", "timeout", e.Ts-1, e.Ts+1)
	if err != nil || len(counts) != 1 || counts[0].Count != 2000 {
		t.Fatal("maximum batch not fully visible", counts, err)
	}
}

func TestVictoriaLogsAPICursorFreezesImplicitTimeWindow(t *testing.T) {
	s, err := openTestLogStorage(t)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	e := logFixtureEvent("implicit-one")
	e2 := e
	e2.ID = "implicit-two"
	if _, err = s.Insert([]types.Event{e, e2}); err != nil {
		t.Fatal(err)
	}
	awaitTestLogDelivery(t, s)
	a := &APIServer{st: s}
	cursor := ""
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		params := url.Values{"service": {"orders"}, "environment": {"test"}, "limit": {"1"}}
		if cursor != "" {
			params.Set("cursor", cursor)
		}
		w := httptest.NewRecorder()
		a.handleSearch(w, httptest.NewRequest("GET", "/api/search?"+params.Encode(), nil))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var page types.SearchResponse
		if err = json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Hits) != 1 {
			t.Fatal(page, err)
		}
		if seen[page.Hits[0].EventID] {
			t.Fatal("implicit-time page repeated")
		}
		seen[page.Hits[0].EventID] = true
		cursor = page.NextCursor
		time.Sleep(2 * time.Millisecond)
	}
}

func TestVictoriaLogsQueueUsageExcludesLegacyLogsAndArchives(t *testing.T) {
	o := DefaultStorageOptions()
	o.URL = "http://127.0.0.1:1"
	s, err := OpenVictoriaLogs(t.TempDir(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	before := s.DiskUsage()
	if before == 0 {
		t.Fatal("queue database not counted")
	}
	if err = os.MkdirAll(filepath.Join(s.dir, "archive"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"logs.db", "archive/old.db"} {
		f, e := os.Create(filepath.Join(s.dir, name))
		if e != nil {
			t.Fatal(e)
		}
		if e = f.Truncate(8 << 20); e != nil {
			t.Fatal(e)
		}
		f.Close()
	}
	if after := s.DiskUsage(); after != before {
		t.Fatal("legacy files consume active queue allowance", before, after)
	}
}
