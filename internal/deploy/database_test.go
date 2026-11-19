package deploy

import (
	"strings"
	"testing"
)

func TestRewriteDeploySQLLeavesSQLiteUnchanged(t *testing.T) {
	query := `INSERT INTO deploy_meta(key,value) VALUES('x','1') ON CONFLICT(key) DO UPDATE SET value='1'`
	if got := rewriteDeploySQL("sqlite", query); got != query {
		t.Fatalf("sqlite query was rewritten: %s", got)
	}
}

func TestRewriteDeploySQLUpsertAndReservedColumns(t *testing.T) {
	query := `INSERT INTO pipeline_runs(service,trigger,branch) VALUES(?,?,?)
		ON CONFLICT(service) DO UPDATE SET branch=excluded.branch`
	got := rewriteDeploySQL("mysql", query)
	for _, want := range []string{
		"pipeline_runs(service,run_trigger,branch)",
		"ON DUPLICATE KEY UPDATE",
		"branch=VALUES(branch)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rewritten query %q does not contain %q", got, want)
		}
	}
	if strings.Contains(got, "ON CONFLICT") || strings.Contains(got, "excluded.") {
		t.Fatalf("sqlite upsert syntax remains in %q", got)
	}

	meta := rewriteDeploySQL("mysql", `INSERT INTO deploy_meta(key,value) VALUES('x','1') ON CONFLICT(key) DO UPDATE SET value='1'`)
	if !strings.Contains(meta, "deploy_meta(meta_key,value)") || strings.Contains(meta, "ON CONFLICT") {
		t.Fatalf("deploy_meta query was not translated: %s", meta)
	}
	lookup := rewriteDeploySQL("mysql", `SELECT value FROM deploy_meta WHERE key='x'`)
	if !strings.Contains(lookup, "WHERE meta_key='x'") {
		t.Fatalf("deploy_meta lookup was not translated: %s", lookup)
	}
}

func TestRewriteDeploySQLFunctionsAndLogAppend(t *testing.T) {
	query := `UPDATE pipeline_runs SET ended_at=strftime('%s','now'),log=substr(COALESCE(log,'') || char(10) || ?, -40000) WHERE id=?`
	got := rewriteDeploySQL("mysql", query)
	for _, want := range []string{
		"ended_at=UNIX_TIMESTAMP()",
		"log=RIGHT(CONCAT(COALESCE(log,''),CHAR(10),?),40000)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rewritten query %q does not contain %q", got, want)
		}
	}
}

func TestRewriteResourceGroupAndPreparationMetaQueries(t *testing.T) {
	for _, q := range []string{
		`SELECT value FROM deploy_meta WHERE key LIKE 'resource_group:%' ORDER BY key`,
		`SELECT key,value FROM deploy_meta WHERE key LIKE 'application_resource_group:%'`,
		`UPDATE deploy_meta SET value=? WHERE key=? AND value=?`,
		`DELETE FROM deploy_meta WHERE key=? AND value=?`,
	} {
		got := rewriteDeploySQL("mysql", q)
		if strings.Contains(got, "WHERE key") || strings.Contains(got, "ORDER BY key") || strings.Contains(got, "SELECT key") {
			t.Fatal("reserved meta key remains", got)
		}
	}
	q := rewriteDeploySQL("mysql", `SELECT id FROM pipeline_runs WHERE service=? AND trigger=? ORDER BY id DESC LIMIT 1`)
	if !strings.Contains(q, "AND run_trigger=?") {
		t.Fatal("preparation recovery lookup not translated", q)
	}
}

func TestRewriteDeploySQLConditionalConflictAndJSON(t *testing.T) {
	query := `INSERT INTO builds(service,pipeline_run_id) VALUES(?,?)
		ON CONFLICT(pipeline_run_id) WHERE pipeline_run_id>0 DO UPDATE SET service=excluded.service`
	got := rewriteDeploySQL("mysql", query)
	if !strings.Contains(got, "ON DUPLICATE KEY UPDATE service=VALUES(service)") {
		t.Fatalf("conditional conflict was not translated: %s", got)
	}

	query = `SELECT 1 FROM service_environments
		WHERE target_type=? AND service||char(0)||environment<>?
		AND json_extract(deploy_config,'$.connection')=? AND json_extract(deploy_config,'$.app_id')=?`
	got = rewriteDeploySQL("mysql", query)
	for _, want := range []string{
		"CONCAT(service,CHAR(0),environment)",
		"JSON_UNQUOTE(JSON_EXTRACT(deploy_config,'$.connection'))",
		"JSON_UNQUOTE(JSON_EXTRACT(deploy_config,'$.app_id'))",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rewritten query %q does not contain %q", got, want)
		}
	}
}

func TestRewriteMySQLInstanceMigrationMetadataUnion(t *testing.T) {
	query := `SELECT key,value FROM deploy_meta WHERE key LIKE 'resource_group:%' OR key LIKE 'application_resources_v2:%'`
	want := `SELECT meta_key,value FROM deploy_meta WHERE meta_key LIKE 'resource_group:%' OR meta_key LIKE 'application_resources_v2:%'`
	if got := rewriteDeploySQL("mysql", query); got != want {
		t.Fatalf("migration query invalid for MySQL: %s", got)
	}
	if got := rewriteDeploySQL("sqlite", query); got != query {
		t.Fatalf("SQLite query changed: %s", got)
	}
}
