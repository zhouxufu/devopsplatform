package server

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"logplatform/internal/types"
)

func TestMetricsAggregatorKeepsBucketsUntilBusAccepts(t *testing.T) {
	var accept atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !accept.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	a := NewMetricsAggregator(srv.URL)
	old := time.Now().Add(-2 * time.Minute).UnixMilli()
	a.Add([]types.Event{{Ts: old, Service: "orders", Environment: "prod", Level: "ERROR"}})
	a.publishClosed()
	if len(a.buckets) != 1 {
		t.Fatalf("failed publish removed buckets: %#v", a.buckets)
	}
	accept.Store(true)
	a.publishClosed()
	if len(a.buckets) != 0 {
		t.Fatalf("accepted publish retained buckets: %#v", a.buckets)
	}
}
