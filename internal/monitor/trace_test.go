package monitor

import (
	"testing"
	"time"

	collectortracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

func TestTraceEnvironmentIsolation(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ts, err := NewTraceStore(st.DB(), true)
	if err != nil {
		t.Fatal(err)
	}
	ingest := func(environment, instance string, traceByte, spanByte byte) string {
		traceID := make([]byte, 16)
		spanID := make([]byte, 8)
		for i := range traceID {
			traceID[i] = traceByte
		}
		for i := range spanID {
			spanID[i] = spanByte
		}
		now := uint64(time.Now().UnixNano())
		req := &collectortracev1.ExportTraceServiceRequest{ResourceSpans: []*tracev1.ResourceSpans{{
			Resource: &resourcev1.Resource{Attributes: []*commonv1.KeyValue{
				{Key: "service.name", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "svc-a"}}},
				{Key: "deployment.environment.name", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: environment}}},
				{Key: "service.instance.id", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: instance}}},
			}},
			ScopeSpans: []*tracev1.ScopeSpans{{Spans: []*tracev1.Span{{
				TraceId: traceID, SpanId: spanID, Name: "GET /health",
				StartTimeUnixNano: now, EndTimeUnixNano: now + uint64(time.Millisecond),
			}}}},
		}}}
		body, _ := proto.Marshal(req)
		if err := ts.IngestTraces(body); err != nil {
			t.Fatal(err)
		}
		return stringHex(traceID)
	}
	prodID := ingest("prod", "pod-a", 1, 2)
	ingest("prod", "pod-b", 1, 5)              // 同一调用链跨实例，按实例检索仍应返回完整 Trace
	testID := ingest("test", "pod-test", 1, 2) // 即使 trace/span id 相同，也不能互相覆盖
	if testID != prodID {
		t.Fatal("test setup expected identical trace ids")
	}
	spans, err := ts.ByTrace(prodID, "prod", "pod-a")
	if err != nil || len(spans) != 2 || spans[0].Environment != "prod" {
		t.Fatalf("prod trace mismatch: err=%v spans=%+v", err, spans)
	}
	if spans, err := ts.ByTrace(prodID, "test", "pod-test"); err != nil || len(spans) != 1 || spans[0].Environment != "test" {
		t.Fatalf("same-id trace was overwritten across environments: err=%v spans=%+v", err, spans)
	}
	if recent := ts.RecentTraces("svc-a", "test", "pod-test", 30); len(recent) != 1 {
		t.Fatalf("recent traces environment filter failed: %+v", recent)
	}
}

func TestTraceEnvironmentMigration(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`CREATE TABLE spans (
		trace_id TEXT NOT NULL, span_id TEXT NOT NULL, parent_id TEXT DEFAULT '',
		service TEXT, name TEXT, start_ms INTEGER, dur_ms INTEGER, status INTEGER DEFAULT 0,
		PRIMARY KEY(trace_id, span_id))`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`INSERT INTO spans(trace_id,span_id,service,name,start_ms,dur_ms) VALUES('legacy-trace','span-1','legacy','work',1,2)`); err != nil {
		t.Fatal(err)
	}
	ts, err := NewTraceStore(st.DB(), true)
	if err != nil {
		t.Fatal(err)
	}
	spans, err := ts.ByTrace("legacy-trace", "", "")
	if err != nil || len(spans) != 1 || spans[0].Environment != "" {
		t.Fatalf("legacy trace migration failed: err=%v spans=%+v", err, spans)
	}
}

func stringHex(b []byte) string {
	const chars = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = chars[v>>4]
		out[i*2+1] = chars[v&15]
	}
	return string(out)
}
