package server

import (
	"strings"
	"testing"
	"time"

	"logplatform/internal/types"
)

func TestStorageSeparatesEnvironments(t *testing.T) {
	st, err := Open(t.TempDir(), 7, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().UnixMilli()
	events := []types.Event{
		{Ts: now - 2, Service: "orders", Environment: "test", Host: "test-a", Level: "ERROR", Message: "test failure"},
		{Ts: now - 1, Service: "orders", Environment: "prod", Host: "prod-a", Level: "INFO", Message: "prod healthy"},
	}
	if n, err := st.Insert(events); err != nil || n != 2 {
		t.Fatalf("insert = %d, %v", n, err)
	}
	resp, err := st.Search("orders", "test", "", "", now-1000, now+1000, "", "", "", 10)
	if err != nil || len(resp.Hits) != 1 || resp.Hits[0].Environment != "test" || resp.Hits[0].Host != "test-a" {
		t.Fatalf("environment search leaked data: %#v, %v", resp, err)
	}
	points, err := st.CountSeries("orders", "prod", "", "", now-1000, now+1000)
	if err != nil || len(points) != 1 || points[0].Count != 1 {
		t.Fatalf("environment count = %#v, %v", points, err)
	}
}

func TestStorageIdempotencyAndSourcePath(t *testing.T) {
	st, err := Open(t.TempDir(), 7, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().UnixMilli()
	event := types.Event{ID: "stable-event-id", Ts: now, ApplicationID: "app_orders", Service: "orders", Environment: "prod", Host: "prod-a", Path: "/var/log/orders.log", Message: "INFO paid"}
	if n, err := st.Insert([]types.Event{event}); err != nil || n != 1 {
		t.Fatalf("first insert = %d, %v", n, err)
	}
	if n, err := st.Insert([]types.Event{event}); err != nil || n != 0 {
		t.Fatalf("duplicate insert = %d, %v", n, err)
	}
	// event_id 去重表是全局的，即使重试时 agent 时间跨过午夜也不会重复。
	event.Ts = now - 24*60*60*1000
	if n, err := st.Insert([]types.Event{event}); err != nil || n != 0 {
		t.Fatalf("cross-day duplicate = %d, %v", n, err)
	}
	resp, err := st.Search("orders", "prod", "", "", now-1000, now+1000, "", "", "", 10)
	if err != nil || len(resp.Hits) != 1 {
		t.Fatalf("search = %#v, %v", resp, err)
	}
	if resp.Hits[0].EventID != event.ID || resp.Hits[0].Path != event.Path || resp.Hits[0].ApplicationID != event.ApplicationID {
		t.Fatalf("source metadata not retained: %#v", resp.Hits[0])
	}
}

func TestQuotedSearchRequiresLiteralPhrase(t *testing.T) {
	st, err := Open(t.TempDir(), 7, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().UnixMilli()
	events := []types.Event{
		{Ts: now - 1, Service: "orders", Environment: "test", Message: "at com.alibaba.nacos.common.remote.client.RpcClient"},
		// FTS tokenization treats punctuation as separators; this row must not
		// satisfy a quoted literal dotted Java package search.
		{Ts: now, Service: "orders", Environment: "test", Message: "at com alibaba nacos common remote client RpcClient"},
	}
	if n, err := st.Insert(events); err != nil || n != 2 {
		t.Fatalf("insert = %d, %v", n, err)
	}
	resp, err := st.Search("orders", "test", "", "", now-1000, now+1000, `"com.alibaba.nacos.common.remote.client"`, "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Hits) != 1 || !strings.Contains(resp.Hits[0].Message, "com.alibaba.nacos.common.remote.client") {
		t.Fatalf("quoted search was not literal: %#v", resp.Hits)
	}
}

func TestLogQueryBooleanFieldsWildcardsAndLiveMatch(t *testing.T) {
	st, err := Open(t.TempDir(), 7, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().UnixMilli()
	events := []types.Event{
		{ID: "q1", Ts: now - 3, Service: "orders", Environment: "test", Host: "node-a", Path: "/var/log/orders/error.log", Level: "ERROR", TraceID: "trace-a", Message: "request timeout while calling nacos"},
		{ID: "q2", Ts: now - 2, Service: "orders", Environment: "test", Host: "node-b", Path: "/var/log/orders/info.log", Level: "INFO", TraceID: "trace-b", Message: "heartbeat healthy"},
		{ID: "q3", Ts: now - 1, Service: "billing", Environment: "prod", Host: "node-c", Path: "/var/log/billing/app.log", Level: "WARN", TraceID: "trace-c", Message: "connection refused TimeoutException"},
	}
	if n, err := st.Insert(events); err != nil || n != len(events) {
		t.Fatalf("insert = %d, %v", n, err)
	}

	assertCount := func(query string, want int) {
		t.Helper()
		resp, err := st.Search("", "", "", "", now-1000, now+1000, query, "", "", 20)
		if err != nil {
			t.Fatalf("query %q: %v", query, err)
		}
		if len(resp.Hits) != want {
			t.Fatalf("query %q returned %d hits, want %d: %#v", query, len(resp.Hits), want, resp.Hits)
		}
	}
	assertCount("timeout nacos", 1)
	assertCount("timeout AND nacos", 1)
	assertCount("timeout OR refused", 2)
	assertCount("NOT heartbeat", 2)
	assertCount("(timeout OR refused) NOT heartbeat", 2)
	assertCount("timeout*", 2)
	assertCount("service:orders env:test level:(ERROR OR WARN) host:node-*", 1)
	assertCount("path:/var/log/orders/*", 2)
	assertCount(`message:"request timeout" trace:trace-a`, 1)

	for _, invalid := range []string{"*timeout", "*timeout*", "time?ut", "unknown:value", "(timeout OR refused"} {
		if _, err := ParseLogQuery(invalid); err == nil {
			t.Fatalf("query %q should be rejected", invalid)
		}
	}

	query, err := ParseLogQuery("(level:ERROR OR level:WARN) timeout*")
	if err != nil {
		t.Fatal(err)
	}
	if !query.Match(types.Hit{Level: "ERROR", Message: "request TimeoutException"}) || query.Match(types.Hit{Level: "INFO", Message: "request TimeoutException"}) {
		t.Fatal("Live matcher did not preserve parsed query semantics")
	}
}
