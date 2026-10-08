package agent

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"logplatform/internal/types"
)

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
