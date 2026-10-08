package monitor

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRewriteMonitorSQLLeavesSQLiteUnchanged(t *testing.T) {
	query := `INSERT INTO metrics_minute(minute,service,environment,level,count) VALUES(?,?,?,?,?) ON CONFLICT(minute,service,environment,level) DO UPDATE SET count=excluded.count`
	if got := rewriteMonitorSQL("sqlite", query); got != query {
		t.Fatalf("sqlite query was rewritten: %s", got)
	}
}

func TestRewriteMonitorSQLMySQLUpsert(t *testing.T) {
	query := `INSERT INTO otlp_minute(minute,service,environment,instance,name,val,cnt) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(minute,service,environment,instance,name) DO UPDATE SET val=val+excluded.val,cnt=cnt+excluded.cnt`
	got := rewriteMonitorSQL("mysql", query)
	for _, want := range []string{"ON DUPLICATE KEY UPDATE", "val=val+VALUES(val)", "cnt=cnt+VALUES(cnt)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("rewritten query %q does not contain %q", got, want)
		}
	}
}

func TestRewriteMonitorSQLMySQLSchemaAndMachine(t *testing.T) {
	if got := rewriteMonitorSQL("mysql", `CREATE TABLE IF NOT EXISTS spans (id INTEGER)`); got != "DO 1" {
		t.Fatalf("sqlite schema query was not suppressed: %q", got)
	}
	query := `INSERT INTO machine(ts,agent,environment,cpu,mem_used,mem_total,disk_used,disk_total,net_rx,net_tx) VALUES(?,?,?,?,?,?,?,?,?,?)`
	got := rewriteMonitorSQL("mysql", query)
	if !strings.Contains(got, "ON DUPLICATE KEY UPDATE") || !strings.Contains(got, "cpu=VALUES(cpu)") {
		t.Fatalf("machine upsert was not added: %q", got)
	}
}

func TestMonitorHealthReportsDatabase(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	traces, err := NewTraceStore(st.DB(), false)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	Routes(st, traces).ServeHTTP(recorder, httptest.NewRequest("GET", "/api/health", nil))
	if recorder.Code != 200 {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response["database"] != "sqlite" {
		t.Fatalf("health response = %#v err=%v", response, err)
	}
}
