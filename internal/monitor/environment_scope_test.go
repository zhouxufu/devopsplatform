package monitor

import (
	"testing"
	"time"
)

func TestMetricRulesRequireExplicitEnvironment(t *testing.T) {
	rule := &MetricRule{Name: "errors", Service: "orders", Metric: "http.server.duration", Webhook: "http://example.test"}
	if err := validateMetricRule(rule); err == nil {
		t.Fatal("empty environment must not be accepted")
	}
	rule.Environment = "*"
	if err := validateMetricRule(rule); err != nil {
		t.Fatalf("explicit all-environment scope rejected: %v", err)
	}
}

func TestMachineMetricsAreEnvironmentIsolated(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ingest, err := IngestMachine(st.DB())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	if err := ingest([]MachineSample{
		{AgentID: "node-1", Environment: "test", Ts: now, CPU: 10},
		{AgentID: "node-1", Environment: "prod", Ts: now + 1, CPU: 90},
	}); err != nil {
		t.Fatal(err)
	}
	latest := queryMachineLatest(st.DB(), "prod")
	if len(latest) != 1 || latest[0].Environment != "prod" || latest[0].CPU != 90 {
		t.Fatalf("machine environment leaked: %+v", latest)
	}
	series := queryMachineSeries(st.DB(), "node-1", "test", now-1, now+2)
	if len(series) != 1 || series[0].CPU != 10 {
		t.Fatalf("machine series environment leaked: %+v", series)
	}
}
