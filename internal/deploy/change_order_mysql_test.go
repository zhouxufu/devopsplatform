package deploy

import (
	"os"
	"strings"
	"testing"
)

func TestReleaseBuildTriggerMySQLUpgrade(t *testing.T) {
	dsn := os.Getenv("DEVOPS_TEST_RELEASE_ORDER_MYSQL_DSN")
	if dsn == "" {
		t.Skip("requires dedicated disposable MySQL schema")
	}
	st, err := OpenDatabase(t.TempDir(), DatabaseConfig{Driver: "mysql", DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err = st.db.Exec(`ALTER TABLE pipeline_runs MODIFY COLUMN run_trigger VARCHAR(64) NOT NULL DEFAULT 'manual'`); err != nil {
		t.Fatal(err)
	}
	original := &PipelineRun{Service: "fixture", Trigger: "manual", Actor: "fixture"}
	if err = st.CreateRun(original); err != nil {
		t.Fatal(err)
	}
	if err = migrateMySQLDeploySchema(st.db); err != nil {
		t.Fatal(err)
	}
	trigger := "code-preparation:" + strings.Repeat("f", 32) + ":estate-gis-service"
	next := &PipelineRun{Service: "estate-gis-service", Trigger: trigger, Actor: "fixture"}
	if err = st.CreateRun(next); err != nil {
		t.Fatal(err)
	}
	if got := st.GetRun(next.ID); got == nil || got.Trigger != trigger {
		t.Fatalf("lost recovery identity %v", got)
	}
	if got := st.GetRun(original.ID); got == nil || got.Trigger != "manual" {
		t.Fatalf("old receipt changed %v", got)
	}
}
