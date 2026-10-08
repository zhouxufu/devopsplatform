package monitor

import (
	"strconv"
	"testing"

	collectorv1 "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"
)

// TestOtlpProtoIngest 验证 protobuf 路径：gauge 覆盖、histogram 累计差分、heap 拆分
func TestOtlpProtoIngest(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	o := NewOtlpReceiver(st.DB())
	if o == nil {
		t.Fatal("receiver nil")
	}
	mk := func(environment, instance string, cnt uint64, sum, heap float64) []byte {
		req := &collectorv1.ExportMetricsServiceRequest{ResourceMetrics: []*metricsv1.ResourceMetrics{{
			Resource: &resourcev1.Resource{Attributes: []*commonv1.KeyValue{
				{Key: "service.name", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "svc-a"}}},
				{Key: "deployment.environment", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: environment}}},
				{Key: "service.instance.id", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: instance}}},
			}},
			ScopeMetrics: []*metricsv1.ScopeMetrics{{Metrics: []*metricsv1.Metric{
				{Name: "jvm_memory_used_bytes", Data: &metricsv1.Metric_Gauge{Gauge: &metricsv1.Gauge{DataPoints: []*metricsv1.NumberDataPoint{{
					Attributes: []*commonv1.KeyValue{{Key: "area", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "heap"}}}},
					Value:      &metricsv1.NumberDataPoint_AsDouble{AsDouble: heap},
				}}}}},
				{Name: "http_server_request_duration_seconds", Data: &metricsv1.Metric_Histogram{Histogram: &metricsv1.Histogram{DataPoints: []*metricsv1.HistogramDataPoint{{
					Count: cnt, Sum: &sum,
				}}}}},
			}}},
		}}}
		b, _ := proto.Marshal(req)
		return b
	}
	// 两次累计导出：120→305（增量 185）
	if err := o.IngestProto(mk("prod", "pod-a", 120, 5.4, 1.28e9)); err != nil {
		t.Fatal(err)
	}
	if err := o.IngestProto(mk("prod", "pod-a", 305, 14.1, 1.35e9)); err != nil {
		t.Fatal(err)
	}
	if err := o.IngestProto(mk("prod", "pod-b", 50, 2.5, 5e8)); err != nil {
		t.Fatal(err)
	}
	if err := o.IngestProto(mk("test", "pod-test", 7, 0.7, 2.4e8)); err != nil {
		t.Fatal(err)
	}
	qps := OtlpSeries(st.DB(), "svc-a", "prod", "pod-a", "http_server_request_duration_seconds", 0, 1<<62)
	if len(qps) != 1 || qps[0].Cnt != 305 {
		t.Fatalf("histogram delta wrong: %+v", qps)
	}
	if podB := OtlpSeries(st.DB(), "svc-a", "prod", "pod-b", "http_server_request_duration_seconds", 0, 1<<62); len(podB) != 1 || podB[0].Cnt != 50 {
		t.Fatalf("instance isolation wrong: %+v", podB)
	}
	if prod := OtlpSeries(st.DB(), "svc-a", "prod", "", "http_server_request_duration_seconds", 0, 1<<62); len(prod) != 1 || prod[0].Cnt != 355 {
		t.Fatalf("all-instance aggregation wrong: %+v", prod)
	}
	if test := OtlpSeries(st.DB(), "svc-a", "test", "", "http_server_request_duration_seconds", 0, 1<<62); len(test) != 1 || test[0].Cnt != 7 {
		t.Fatalf("environment isolation wrong: %+v", test)
	}
	if all := OtlpSeries(st.DB(), "svc-a", "", "", "http_server_request_duration_seconds", 0, 1<<62); len(all) != 1 || all[0].Cnt != 362 {
		t.Fatalf("all-environment aggregation wrong: %+v", all)
	}
	heap := OtlpSeries(st.DB(), "svc-a", "prod", "pod-a", "jvm_memory_used_bytes_heap", 0, 1<<62)
	if len(heap) != 1 || heap[0].Val != 1.35e9 {
		t.Fatalf("gauge overwrite wrong: %+v", heap)
	}
	if got := OtlpSeries(st.DB(), "svc-a", "prod", "pod-a", "jvm_memory_used_bytes", 0, 1<<62); len(got) != 0 {
		t.Fatalf("area split wrong: %+v", got)
	}
	minute := qps[0].Minute
	for _, row := range []struct {
		environment, status string
		count               int
	}{
		{"prod", "200", 90}, {"prod", "500", 10}, {"test", "500", 5},
	} {
		if _, err := st.DB().Exec(`INSERT INTO otlp_hist(minute,service,environment,instance,name,iface,meth,status,cnt,sum,bounds,buckets)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, minute, "svc-a", row.environment, "pod-a", "request_latency", "/orders", "GET", row.status, row.count, row.count, `[1]`, `[`+strconv.Itoa(row.count)+`,0]`); err != nil {
			t.Fatal(err)
		}
	}
	if got := Interfaces(st.DB(), "svc-a", "prod", "pod-a", "request_latency"); len(got) != 1 || got[0].Cnt != 100 {
		t.Fatalf("interface environment isolation wrong: %+v", got)
	}
	if got := Quantiles(st.DB(), "svc-a", "test", "pod-a", "request_latency", "/orders", "", "", 0, 1<<62); len(got) != 1 || got[0].Cnt != 5 {
		t.Fatalf("quantile environment isolation wrong: %+v", got)
	}
	if got := Availability(st.DB(), "svc-a", "prod", "pod-a", "request_latency", "/orders", "", 0, 1<<62, nil); len(got) != 1 || got[0].Rate != 0.9 {
		t.Fatalf("availability environment isolation wrong: %+v", got)
	}
	if got := ObservedEnvironments(st.DB(), "svc-a"); len(got) != 2 || got[0] != "prod" || got[1] != "test" {
		t.Fatalf("observed environments wrong: %+v", got)
	}
	if got := ObservedInstances(st.DB(), "svc-a", "prod"); len(got) != 2 || got[0] != "pod-a" || got[1] != "pod-b" {
		t.Fatalf("observed instances wrong: %+v", got)
	}
	alerts := &MetricAlertEngine{db: st.DB()}
	rule := &MetricRule{Service: "svc-a", Environment: "prod", Instance: "pod-a", Metric: "request_latency", Iface: "/orders", Threshold: 9, MinSample: 1}
	if fired, recent := alerts.evalErrorRate(rule, 0, 1<<62); !fired || recent["error_rate_pct"] != 10.0 {
		t.Fatalf("instance alert error-rate query wrong: fired=%v recent=%+v", fired, recent)
	}
	rule.Quantile, rule.Threshold = 0.99, 0.5
	if fired, recent := alerts.evalLatency(rule, 0, 1<<62); !fired || recent["sample"] != 100.0 {
		t.Fatalf("instance alert latency query wrong: fired=%v recent=%+v", fired, recent)
	}
	if got := alerts.windowCount(rule, 0, 1<<62); got != 100 {
		t.Fatalf("instance alert traffic query wrong: %v", got)
	}

}

func TestOtlpEnvironmentMigration(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	db := st.DB()
	statements := []string{
		`CREATE TABLE otlp_minute (minute INTEGER NOT NULL,service TEXT NOT NULL,name TEXT NOT NULL,val REAL,cnt INTEGER,PRIMARY KEY(minute,service,name))`,
		`INSERT INTO otlp_minute VALUES(60000,'legacy','gauge',2,0)`,
		`CREATE TABLE otlp_hist (minute INTEGER NOT NULL,service TEXT NOT NULL,name TEXT NOT NULL,iface TEXT NOT NULL DEFAULT '',meth TEXT NOT NULL DEFAULT '',status TEXT NOT NULL DEFAULT '',cnt REAL,sum REAL,bounds TEXT,buckets TEXT,PRIMARY KEY(minute,service,name,iface,meth,status))`,
		`INSERT INTO otlp_hist VALUES(60000,'legacy','latency','/x','GET','200',3,1,'[1]','[3,0]')`,
		`CREATE TABLE otlp_counter (minute INTEGER NOT NULL,service TEXT NOT NULL,name TEXT NOT NULL,iface TEXT NOT NULL DEFAULT '',meth TEXT NOT NULL DEFAULT '',status TEXT NOT NULL DEFAULT '',val REAL,cnt INTEGER,PRIMARY KEY(minute,service,name,iface,meth,status))`,
		`INSERT INTO otlp_counter VALUES(60000,'legacy','calls','/x','GET','0',4,1)`,
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if NewOtlpReceiver(db) == nil {
		t.Fatal("receiver nil after migration")
	}
	if got := OtlpSeries(db, "legacy", "", "", "gauge", 0, 120000); len(got) != 1 || got[0].Val != 2 {
		t.Fatalf("legacy row not preserved: %+v", got)
	}
	if got := OtlpSeries(db, "legacy", "prod", "", "gauge", 0, 120000); len(got) != 0 {
		t.Fatalf("legacy row incorrectly assigned to prod: %+v", got)
	}
	for _, table := range []string{"otlp_minute", "otlp_hist", "otlp_counter"} {
		cols, err := tableColumns(db, table)
		if err != nil || !cols["environment"] || !cols["instance"] {
			t.Fatalf("%s environment migration failed: %v %+v", table, err, cols)
		}
	}
}

func TestOtlpInstanceMigration(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	db := st.DB()
	statements := []string{
		`CREATE TABLE otlp_minute (minute INTEGER NOT NULL,service TEXT NOT NULL,environment TEXT NOT NULL DEFAULT '',name TEXT NOT NULL,val REAL,cnt INTEGER,PRIMARY KEY(minute,service,environment,name))`,
		`INSERT INTO otlp_minute VALUES(60000,'legacy','prod','gauge',3,0)`,
		`CREATE TABLE otlp_hist (minute INTEGER NOT NULL,service TEXT NOT NULL,environment TEXT NOT NULL DEFAULT '',name TEXT NOT NULL,iface TEXT NOT NULL DEFAULT '',meth TEXT NOT NULL DEFAULT '',status TEXT NOT NULL DEFAULT '',cnt REAL,sum REAL,bounds TEXT,buckets TEXT,PRIMARY KEY(minute,service,environment,name,iface,meth,status))`,
		`INSERT INTO otlp_hist VALUES(60000,'legacy','prod','latency','/x','GET','200',3,1,'[1]','[3,0]')`,
		`CREATE TABLE otlp_counter (minute INTEGER NOT NULL,service TEXT NOT NULL,environment TEXT NOT NULL DEFAULT '',name TEXT NOT NULL,iface TEXT NOT NULL DEFAULT '',meth TEXT NOT NULL DEFAULT '',status TEXT NOT NULL DEFAULT '',val REAL,cnt INTEGER,PRIMARY KEY(minute,service,environment,name,iface,meth,status))`,
		`INSERT INTO otlp_counter VALUES(60000,'legacy','prod','calls','/x','GET','0',4,1)`,
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if NewOtlpReceiver(db) == nil {
		t.Fatal("receiver nil after instance migration")
	}
	if got := OtlpSeries(db, "legacy", "prod", "", "gauge", 0, 120000); len(got) != 1 || got[0].Val != 3 {
		t.Fatalf("environment lost during instance migration: %+v", got)
	}
	if got := ObservedInstances(db, "legacy", "prod"); len(got) != 0 {
		t.Fatalf("legacy data should remain in empty instance: %+v", got)
	}
}
