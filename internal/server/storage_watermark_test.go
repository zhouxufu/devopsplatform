package server

import (
	"logplatform/internal/types"
	"testing"
	"time"
)

// Replaces obsolete SQLite day-table deletion assertions: pressure must never
// erase accepted pending or replay data, nor acknowledge uncommitted records.
func TestLogQueuePressurePreservesPendingAndReceipts(t *testing.T) {
	st, err := openTestLogStorage(t)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.opts.QueueMaxBytes = 1
	event := types.Event{ID: "full-event", Ts: time.Now().UnixMilli(), Service: "orders", Environment: "test", Message: "must retry"}
	if _, err = st.InsertUnique([]types.Event{event}); err != ErrLogQueueFull {
		t.Fatal("queue overflow accepted", err)
	}
	var n int
	st.db.QueryRow(`SELECT COUNT(*) FROM log_receipts`).Scan(&n)
	if n != 0 {
		t.Fatal("failed batch left receipt", n)
	}
	st.opts.QueueMaxBytes = 64 << 20
	if n, err := st.Insert([]types.Event{event}); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	awaitTestLogDelivery(t, st)
	st.opts.QueueMaxBytes = 1
	if err = st.maintainQueue(); err != nil {
		t.Fatal(err)
	}
	st.db.QueryRow(`SELECT COUNT(*) FROM log_batches`).Scan(&n)
	if n != 1 {
		t.Fatal("pressure removed replay copy", n)
	}
}
