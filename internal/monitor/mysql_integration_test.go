package monitor

import (
	"os"
	"testing"
	"time"
)

// TestMySQLMonitorSmoke is opt-in and must point to an empty disposable
// MySQL 8 schema.
func TestMySQLMonitorSmoke(t *testing.T) {
	dsn := os.Getenv("DEVOPS_TEST_MONITOR_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set DEVOPS_TEST_MONITOR_MYSQL_DSN to an empty disposable MySQL 8 schema")
	}
	st, err := OpenDatabase(t.TempDir(), DatabaseConfig{Driver: "mysql", DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if st.Driver() != "mysql" {
		t.Fatalf("driver = %q", st.Driver())
	}
	now := time.Now().UnixMilli() / 60000 * 60000
	if err := st.UpsertPoints([]Point{{Minute: now, Service: "orders", Environment: "test", Counts: map[string]int64{"ERROR": 2}}}); err != nil {
		t.Fatal(err)
	}
	if points, err := st.Query("orders", "test", now, now); err != nil || len(points) != 1 || points[0].Counts["ERROR"] != 2 {
		t.Fatalf("metrics round-trip: points=%+v err=%v", points, err)
	}
	ingest, err := IngestMachine(st.DB())
	if err != nil {
		t.Fatal(err)
	}
	if err := ingest([]MachineSample{{AgentID: "test-a", Environment: "test", Ts: now, CPU: 12.5}}); err != nil {
		t.Fatal(err)
	}
	if latest := queryMachineLatest(st.DB(), "test"); len(latest) != 1 || latest[0].AgentID != "test-a" {
		t.Fatalf("machine round-trip: %+v", latest)
	}
	if _, err := NewTraceStore(st.DB(), true); err != nil {
		t.Fatal(err)
	}
	if NewOtlpReceiver(st.DB()) == nil {
		t.Fatal("OTLP receiver schema initialization failed")
	}
	if _, err := st.DB().Exec(`INSERT INTO avail_rules(service,environment,name,ok_codes,ignore_codes) VALUES(?,?,?,?,?)
		ON CONFLICT(service,environment,name) DO UPDATE SET ok_codes=excluded.ok_codes,ignore_codes=excluded.ignore_codes`, "orders", "test", "http.server.duration", `["200"]`, `[]`); err != nil {
		t.Fatal(err)
	}
	if rule := loadAvailRule(st.DB(), "orders", "test", "http.server.duration"); rule == nil || len(rule.OkCodes) != 1 {
		t.Fatalf("availability rule round-trip: %#v", rule)
	}
}
