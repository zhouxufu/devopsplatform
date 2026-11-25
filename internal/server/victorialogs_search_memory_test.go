package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestLogSearchBoundedBatchesSkipReplayRuns(t *testing.T) {
	requests := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		r.ParseForm()
		q := r.Form.Get("query")
		if strings.Contains(q, "stats") || !strings.HasSuffix(q, "sort by (ts desc, ingest_sequence desc) limit 32") || !strings.Contains(q, `environment:="dev"`) {
			t.Errorf("unbounded or unscoped query: %s", q)
		}
		high := 91
		if match := regexp.MustCompile(`ingest_sequence:<(\d+)`).FindStringSubmatch(q); len(match) > 0 {
			high, _ = strconv.Atoi(match[1])
		}
		n := 0
		for seq := high - 1; seq > 0 && n < 32; seq-- {
			copies := 2
			if seq == 85 {
				copies = 100
			} // More duplicates than an entire physical page.
			for i := 0; i < copies && n < 32; i++ {
				json.NewEncoder(w).Encode(map[string]string{"event_key": fmt.Sprint(seq), "ts": "1000", "ingest_sequence": fmt.Sprint(seq)})
				n++
			}
		}
	}))
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	s := &Storage{base: u, client: upstream.Client()}
	rows, err := s.searchLogRows(context.Background(), `environment:="dev" AND ingest_sequence:<=90`, 1000, 1000, logCursor{High: 90}, 91)
	if err != nil || len(rows) != 90 || requests < 3 {
		t.Fatalf("rows=%d requests=%d error=%v", len(rows), requests, err)
	}
	for i, row := range rows {
		if row["event_key"] != fmt.Sprint(90-i) {
			t.Fatalf("missing/repeated/out-of-order row: %v", row)
		}
	}
}

func TestLogQueryMemoryErrorIsActionableAndRedacted(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "cannot execute query private-log-value: requires more than 102MB of memory", http.StatusBadRequest)
	}))
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	s := &Storage{base: u, client: upstream.Client()}
	_, err := s.query(context.Background(), "*", 1, 2)
	if err == nil || !strings.Contains(err.Error(), "内存限额") || strings.Contains(err.Error(), "private-log-value") {
		t.Fatal(err)
	}
}

func TestVictoriaLogsSearchLargeWindowBoundedMemory(t *testing.T) {
	s, err := openTestLogStorage(t)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	e := logFixtureEvent("large-window")
	const count = 6000
	message := strings.Repeat("synthetic isolated log content ", 1100)
	for first := 1; first <= count; first += 100 {
		var body bytes.Buffer
		for i := first; i < first+100; i++ {
			row := map[string]any{"_time": e.Ts, "ts": e.Ts, "event_id": fmt.Sprint(i), "event_key": fmt.Sprint(i), "ingest_sequence": i, "service": e.Service, "environment": e.Environment, "_msg": message}
			json.NewEncoder(&body).Encode(row)
			if i == count {
				for replay := 0; replay < 100; replay++ {
					json.NewEncoder(&body).Encode(row)
				}
			}
		}
		resp, err := s.request(s.ctx, "POST", "/insert/jsonline", nil, body.Bytes(), "application/x-ndjson")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	if _, err = s.db.Exec(`INSERT OR REPLACE INTO log_state(key,value) VALUES('sequence',?)`, count); err != nil {
		t.Fatal(err)
	}
	resp, err := s.request(s.ctx, "POST", "/internal/force_flush", nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	// Negative control: the old whole-window aggregation fails under the same
	// real engine memory budget where bounded search must succeed.
	_, oldErr := s.query(s.ctx, "* | stats by (event_key) row_any() as sample | unpack_json sample | delete sample | sort by (ts desc, ingest_sequence desc) limit 101", e.Ts, e.Ts)
	if oldErr == nil || !strings.Contains(oldErr.Error(), "内存限额") {
		t.Fatalf("fixture did not reproduce the old memory failure: %v", oldErr)
	}
	page, err := s.Search(e.Service, e.Environment, "", "", e.Ts, e.Ts, "", "", "", 100)
	if err != nil || len(page.Hits) != 100 || !page.HasMore {
		t.Fatalf("large-window first page: %#v %v", page, err)
	}
	if page.Hits[0].EventID != "6000" || page.Hits[99].EventID != "5901" {
		t.Fatal("wrong first page ordering")
	}
	next, err := s.Search(e.Service, e.Environment, "", "", e.Ts, e.Ts, "", "", page.NextCursor, 100)
	if err != nil || len(next.Hits) != 100 || next.Hits[0].EventID != "5900" {
		t.Fatalf("large-window continuation: %#v %v", next, err)
	}
}
