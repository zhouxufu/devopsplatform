package agent

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"logplatform/internal/types"
)

func TestSenderReusesCompressionWithoutLeakingPreviousBatch(t *testing.T) {
	requests := make(chan types.IngestRequest, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Errorf("gzip: %v", err)
			w.WriteHeader(400)
			return
		}
		defer zr.Close()
		var req types.IngestRequest
		if err := json.NewDecoder(zr).Decode(&req); err != nil {
			t.Errorf("JSON: %v", err)
			w.WriteHeader(400)
			return
		}
		requests <- req
		_ = json.NewEncoder(w).Encode(types.IngestResponse{BatchID: req.BatchID, Acknowledged: len(req.Events)})
	}))
	defer srv.Close()
	sender := NewSender(&Config{ServerURL: srv.URL}, nil, nil, nil, &atomic.Int64{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, message := range []string{strings.Repeat("中文异常\\n", 6000), "short", ""} {
		var events []types.Event
		if message != "" {
			events = []types.Event{{ID: "event", Message: message}}
		}
		req := types.IngestRequest{BatchID: makeBatchID(events), Events: events}
		if err := sender.post(ctx, &req); err != nil {
			t.Fatal(err)
		}
		got := <-requests
		if got.BatchID != req.BatchID || len(got.Events) != len(events) {
			t.Fatalf("wrong batch: %+v", got)
		}
		if len(events) > 0 && got.Events[0].Message != message {
			t.Fatal("reused encoder changed the message or retained an old payload")
		}
	}
}

func TestSenderDeletesSpoolOnlyAfterMatchingAck(t *testing.T) {
	received := make(chan types.IngestRequest, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Errorf("gzip reader: %v", err)
			return
		}
		defer zr.Close()
		var req types.IngestRequest
		if err := json.NewDecoder(zr).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
			return
		}
		received <- req
		_ = json.NewEncoder(w).Encode(types.IngestResponse{BatchID: req.BatchID, Accepted: len(req.Events), Acknowledged: len(req.Events)})
	}))
	defer srv.Close()

	spool, err := OpenSpool(filepath.Join(t.TempDir(), "spool.db"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer spool.Close()
	if err := spool.EnqueueBatch([]PendingEvent{pending("event-1", "INFO ok", 8)}); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{ServerURL: srv.URL, AgentID: "test/node-a", Host: "node-a"}
	if err := spool.BindIdentity(cfg); err != nil {
		t.Fatal(err)
	}
	reg := LoadRegistry(filepath.Join(t.TempDir(), "registry.json"))
	var dropped atomic.Int64
	sender := NewSender(cfg, make(chan PendingEvent), reg, spool, &dropped)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		sender.sendLoop(ctx)
		close(done)
	}()

	select {
	case req := <-received:
		if req.AgentID != cfg.AgentID || req.AgentName != "test/node-a" || len(req.AgentAliases) != 1 {
			t.Fatalf("identity missing: %+v", req)
		}
		if len(req.Events) != 1 || req.Events[0].ID != "event-1" || req.BatchID == "" {
			t.Fatalf("request = %#v", req)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("sender did not post spooled event")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		n, _ := spool.Count()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("spool still has %d records after ACK", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sender did not stop")
	}
}

func TestSenderDrainsPersistenceBatchLargerThanQueueCapacity(t *testing.T) {
	var received atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Errorf("gzip: %v", err)
			w.WriteHeader(400)
			return
		}
		defer zr.Close()
		var req types.IngestRequest
		if err := json.NewDecoder(zr).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
			w.WriteHeader(400)
			return
		}
		received.Add(int64(len(req.Events)))
		_ = json.NewEncoder(w).Encode(types.IngestResponse{BatchID: req.BatchID, Acknowledged: len(req.Events)})
	}))
	defer srv.Close()
	spool, err := OpenSpool(filepath.Join(t.TempDir(), "spool.db"), 1800)
	if err != nil {
		t.Fatal(err)
	}
	defer spool.Close()
	in := make(chan PendingEvent, 8)
	for i := 0; i < 8; i++ {
		in <- noisyPending(i, 600)
	}
	reg := LoadRegistry(filepath.Join(t.TempDir(), "registry.json"))
	sender := NewSender(&Config{ServerURL: srv.URL}, in, reg, spool, &atomic.Int64{})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { sender.Run(ctx); close(done) }()
	for received.Load() < 8 && ctx.Err() == nil {
		time.Sleep(20 * time.Millisecond)
	}
	if received.Load() != 8 {
		t.Error("large persistence batch blocked a queue that can hold individual events")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("sender did not stop")
	}
	if offset, _ := reg.ResolveFile("api|/tmp/api.log", 7, 1, "head"); offset != 8 {
		t.Fatalf("checkpoint=%d, want 8", offset)
	}
}

func TestSenderKeepsQueueOnMismatchedAck(t *testing.T) {
	posted := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		defer zr.Close()
		var req types.IngestRequest
		if err := json.NewDecoder(zr).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		_ = json.NewEncoder(w).Encode(types.IngestResponse{BatchID: "wrong-batch", Acknowledged: len(req.Events)})
		posted <- struct{}{}
	}))
	defer srv.Close()
	spool, err := OpenSpool(filepath.Join(t.TempDir(), "spool.db"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer spool.Close()
	if err := spool.EnqueueBatch([]PendingEvent{pending("retain-me", "ERROR 中文\nstack", 42)}); err != nil {
		t.Fatal(err)
	}
	sender := NewSender(&Config{ServerURL: srv.URL}, nil, nil, spool, &atomic.Int64{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { sender.sendLoop(ctx); close(done) }()
	select {
	case <-posted:
	case <-time.After(3 * time.Second):
		t.Fatal("no request")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("retry did not stop")
	}
	if n, err := spool.Count(); err != nil || n != 1 {
		t.Fatalf("invalid ACK cleared durable queue: n=%d err=%v", n, err)
	}
}
