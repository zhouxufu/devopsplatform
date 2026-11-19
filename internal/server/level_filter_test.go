package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"logplatform/internal/types"
)

func TestMultipleLevelsAgreeForSearchCountsAndLive(t *testing.T) {
	st, err := openTestLogStorage(t)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().Truncate(time.Minute).Add(10 * time.Second).UnixMilli()
	events := []types.Event{
		{Ts: now, Service: "orders", Environment: "test", Level: "ERROR", Message: "request timeout"},
		{Ts: now + 1, Service: "orders", Environment: "test", Level: "WARN", Message: "request slow"},
		{Ts: now + 2, Service: "orders", Environment: "test", Level: "INFO", Message: "request accepted"},
		{Ts: now + 3, Service: "orders", Environment: "test", Level: "DEBUG", Message: "request details"},
		{Ts: now + 4, Service: "orders", Environment: "test", Level: "TRACE", Message: "request trace"},
		{Ts: now + 5, Service: "orders", Environment: "test", Level: "FATAL", Message: "request fatal"},
		{Ts: now + 6, Service: "orders", Environment: "prod", Level: "ERROR", Message: "request timeout"},
		{Ts: now + 7, Service: "billing", Environment: "test", Level: "WARN", Message: "request timeout"},
	}
	if _, err := st.Insert(events); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, levels, query string
		want                int
	}{
		{"multiple", "ERROR,WARN", "", 2},
		{"single compatible", "INFO", "", 1},
		{"clear", "", "", 6},
		{"normalize and deduplicate", " error,WARNING,ERROR, ", "", 2},
		{"all six", "FATAL,ERROR,WARN,INFO,DEBUG,TRACE", "", 6},
		{"combine keyword", "ERROR,WARN", "timeout", 1},
		{"intersect query level", "ERROR,WARN", "level:INFO", 0},
		{"unknown literal", "ERROR') OR 1=1 --", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			awaitTestLogDelivery(t, st)
			resp, err := st.Search("orders", "test", "", tc.levels, now-1, now+100, tc.query, "", "", 50)
			if err != nil {
				t.Fatal(err)
			}
			if len(resp.Hits) != tc.want {
				t.Fatalf("search count=%d, want %d", len(resp.Hits), tc.want)
			}
			points, err := st.CountSeries("orders", "test", tc.levels, tc.query, now-1, now+100)
			if err != nil {
				t.Fatal(err)
			}
			var total int64
			for _, point := range points {
				total += point.Count
			}
			if total != int64(tc.want) {
				t.Fatalf("series count=%d, want %d", total, tc.want)
			}
			query, err := ParseLogQuery(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			filter := Filter{Service: "orders", Environment: "test", Levels: parseLevelFilter(tc.levels), Query: query}
			live := 0
			for _, event := range events {
				if filter.Match(types.Hit{Service: event.Service, Environment: event.Environment, Level: event.Level, Message: event.Message}) {
					live++
				}
			}
			if live != tc.want {
				t.Fatalf("live count=%d, want %d", live, tc.want)
			}
		})
	}

	api := &APIServer{st: st}
	params := url.Values{"service": {"orders"}, "environment": {"test"}, "level": {"ERROR", "WARN"}, "start": {strconv.FormatInt(now-1, 10)}, "end": {strconv.FormatInt(now+100, 10)}, "limit": {"1"}}
	var got []string
	for page := 0; page < 2; page++ {
		w := httptest.NewRecorder()
		api.handleSearch(w, httptest.NewRequest(http.MethodGet, "/api/search?"+params.Encode(), nil))
		if w.Code != http.StatusOK {
			t.Fatalf("search status=%d: %s", w.Code, w.Body.String())
		}
		var response types.SearchResponse
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if len(response.Hits) != 1 || response.HasMore != (page == 0) {
			t.Fatalf("page %d: %#v", page, response)
		}
		got = append(got, response.Hits[0].Level)
		params.Set("cursor", response.NextCursor)
	}
	if got[0] != "WARN" || got[1] != "ERROR" {
		t.Fatalf("multi-level pagination = %v", got)
	}
	w := httptest.NewRecorder()
	api.handleCount(w, httptest.NewRequest(http.MethodGet, "/api/count?"+params.Encode(), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("count status=%d: %s", w.Code, w.Body.String())
	}
	var counts struct {
		Points []MinuteCount `json:"points"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &counts); err != nil {
		t.Fatal(err)
	}
	if len(counts.Points) != 1 || counts.Points[0].Count != 2 {
		t.Fatalf("HTTP multi-level count: %#v", counts.Points)
	}
}
