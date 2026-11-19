package server

import (
	"logplatform/internal/types"
	"testing"
)

func TestEnrichColoredSpringAndP6Spy(t *testing.T) {
	raw := "\x1b[32m2026-11-18 08:21:59 943\x1b[0;39m  \x1b[34mINFO \x1b[0;39m \x1b[32m[io-4010-exec-10]\x1b[0;39m \x1b[1;35mp6spy                                   \x1b[0;39m \x1b[1;35m:\x1b[0;39m select 1 traceId=abc123456789"
	want := "2026-11-18 08:21:59 943  INFO  [io-4010-exec-10] p6spy                                    : select 1 traceId=abc123456789"
	e := types.Event{ID: "stable", Ts: 1234, Message: raw}
	Enrich(&e, 256<<10)
	if e.Message != want || e.Level != "INFO" || e.TraceID != "abc123456789" || e.ID != "stable" || e.Ts != 1234 {
		t.Fatalf("unexpected normalized event: %+v", e)
	}
	for _, level := range []string{"DEBUG", "WARN", "ERROR"} {
		e = types.Event{Message: "\x1b[31m" + level + "\x1b[0m failed\n\tat Example.run"}
		Enrich(&e, 256<<10)
		if e.Level != level || e.Message != level+" failed\n\tat Example.run" {
			t.Fatal(e)
		}
	}
	e = types.Event{Level: "warning", TraceID: "trusted-trace", Message: raw}
	Enrich(&e, 256<<10)
	if e.Level != "WARN" || e.TraceID != "trusted-trace" {
		t.Fatal("explicit metadata overwritten", e)
	}
	e = types.Event{Message: "\x1b[31mERROR\x1b[0m failure"}
	Enrich(&e, 5)
	if e.Message != "ERROR... [truncated]" || e.Level != "ERROR" {
		t.Fatal("color bytes consumed content limit", e)
	}
}
