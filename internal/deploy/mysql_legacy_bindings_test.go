package deploy

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Seed the on-disk pre-upgrade shape, bypassing today's instance-only writer.
func seedLegacyMySQL(t *testing.T, s *Store, c ResourceConnection) {
	t.Helper()
	config := c.Config
	c.Config = map[string]string{"tls": "false"}
	if err := s.UpsertConnection(c); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(config)
	if _, err := s.db.Exec(`UPDATE resource_connections SET config=? WHERE name=?`, string(raw), c.Name); err != nil {
		t.Fatal(err)
	}
}

func TestMySQLInstanceMigrationMovesAllEditableBindingsAtomically(t *testing.T) {
	s, _, _ := resourceGroupFixture(t)
	legacy := ResourceConnection{Name: "db-dev", Kind: "mysql", Endpoint: "127.0.0.1:1", Username: "qa", Password: "fixture-secret", Config: map[string]string{"database": "dev_db", "tls": "false"}}
	seedLegacyMySQL(t, s, legacy)
	seedLegacyMySQL(t, s, ResourceConnection{Name: "unused", Kind: "mysql", Endpoint: "127.0.0.1:2", Config: map[string]string{"database": "unused_db", "tls": "false"}})
	group := s.ResourceGroup("business")
	for i := range group.Bindings {
		if group.Bindings[i].Connection == legacy.Name {
			group.Bindings[i].Config = nil
		}
	}
	groupRaw, _ := json.Marshal(group)
	var doc map[string]json.RawMessage
	json.Unmarshal(groupRaw, &doc)
	doc["future_field"] = json.RawMessage(`{"keep":true}`)
	groupRaw, _ = json.Marshal(doc)
	s.db.Exec(`UPDATE deploy_meta SET value=? WHERE key='resource_group:business'`, string(groupRaw))
	own := ApplicationResources{CompositionVersion: 2, OwnRequirements: []ApplicationResourceRequirement{{Alias: "private", Kind: "mysql"}}, OwnBindings: []ApplicationEnvironmentResource{{Alias: "private", Environment: "test", Connection: legacy.Name}, {Alias: "private", Environment: "prod", Connection: legacy.Name, Config: map[string]string{"database": "explicit_prod"}}}}
	raw, _ := json.Marshal(own)
	if _, err := s.db.Exec(`INSERT INTO deploy_meta(key,value) VALUES('application_resources_v2:orders',?)`, string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO application_environment_resources(service,environment,alias,connection,config,updated_at) VALUES('flat','test','db',?,'{}',123)`, legacy.Name); err != nil {
		t.Fatal(err)
	}
	before := s.GetConnection(legacy.Name)
	var secret string
	s.db.QueryRow(`SELECT secret FROM resource_connections WHERE name=?`, legacy.Name).Scan(&secret)
	// Use real immutable plan/ledger storage and compare the effective target.
	target, err := mysqlDatabaseTarget(before, map[string]string{"database": "dev_db"})
	if err != nil {
		t.Fatal(err)
	}
	evidence := `{"environment":{"name":"test"},"sql_plans":[{"connection":"db-dev","database":"dev_db","destination_digest":"` + sqlDestination(target) + `"}]}`
	runResult, err := s.db.Exec(`INSERT INTO delivery_runs(plan,digest,status,execution,env_secrets,owner,approver,created_at,revision) VALUES(?,'proof','success','[]','{}','alice','reviewer',1,1)`, evidence)
	if err != nil {
		t.Fatal(err)
	}
	runID, _ := runResult.LastInsertId()
	if _, err = s.db.Exec(`INSERT INTO sql_execution_ledger(connection,database_name,change_id,digest,status,run_id,node_id,detail) VALUES('db-dev','dev_db',1,'proof','success',?,'sql','unchanged')`, runID); err != nil {
		t.Fatal(err)
	}
	if err := s.migrateMySQLInstanceConnections(); err != nil {
		t.Fatal(err)
	}
	for _, c := range s.Connections() {
		if c.Kind == "mysql" {
			if _, ok := c.Config["database"]; ok {
				t.Fatal("default survived in list", c)
			}
		}
	}
	after := s.GetConnection(legacy.Name)
	if _, ok := after.Config["database"]; ok {
		t.Fatal("default survived in connection")
	}
	if after.Endpoint != before.Endpoint || after.Username != before.Username || after.Password != before.Password || after.UpdatedAt != before.UpdatedAt || after.Config["tls"] != "false" {
		t.Fatal("instance identity or credentials changed")
	}
	var afterSecret string
	s.db.QueryRow(`SELECT secret FROM resource_connections WHERE name=?`, legacy.Name).Scan(&afterSecret)
	if secret != afterSecret {
		t.Fatal("encrypted credential rewritten")
	}
	var flat string
	var updated int64
	s.db.QueryRow(`SELECT config,updated_at FROM application_environment_resources WHERE service='flat'`).Scan(&flat, &updated)
	if flat != `{"database":"dev_db"}` || updated != 123 {
		t.Fatal(flat, updated)
	}
	got := s.ResourceGroup("business")
	if got.Revision != group.Revision || got.UpdatedAt != group.UpdatedAt {
		t.Fatal("migration changed semantic group revision")
	}
	for _, b := range got.Bindings {
		if b.Connection == legacy.Name && b.Config["database"] != "dev_db" {
			t.Fatal("group selection lost")
		}
	}
	s.db.QueryRow(`SELECT value FROM deploy_meta WHERE key='resource_group:business'`).Scan(&flat)
	if !strings.Contains(flat, `"future_field":{"keep":true}`) {
		t.Fatal("unknown group data lost", flat)
	}
	composed := s.ApplicationResources("orders")
	if composed.Error != "" || len(composed.OwnBindings) != 2 || composed.OwnBindings[0].Config["database"] != "dev_db" || composed.OwnBindings[1].Config["database"] != "explicit_prod" {
		t.Fatal("v2 selection lost", composed)
	}
	if err := s.SaveApplicationResources("orders", composed); err != nil {
		t.Fatal(err)
	}
	if _, err := s.workbenchSQLConnection(WorkbenchQuery{Connection: legacy.Name, Environment: "test", Database: "dev_db"}); err != nil {
		t.Fatal(err)
	}
	for _, database := range []string{"", "other_db"} {
		if _, err := s.workbenchSQLConnection(WorkbenchQuery{Connection: legacy.Name, Environment: "test", Database: database}); err == nil {
			t.Fatal("legacy scope expanded or default inferred")
		}
	}
	if err := s.db.QueryRow(`SELECT plan FROM delivery_runs WHERE id=?`, runID).Scan(&flat); err != nil || flat != evidence {
		t.Fatal("historical plan rewritten", err)
	}
	var ledgerDB, ledgerDigest string
	if err := s.db.QueryRow(`SELECT database_name,digest FROM sql_execution_ledger WHERE run_id=?`, runID).Scan(&ledgerDB, &ledgerDigest); err != nil || ledgerDB != "dev_db" || ledgerDigest != "proof" {
		t.Fatal("historical ledger rewritten", err)
	}
	migratedTarget, err := s.sqlBinding("orders", "test", "private")
	if err != nil || sqlDestination(migratedTarget) != sqlDestination(target) {
		t.Fatal("approved target identity drifted", err)
	}
	snapshot := s.ApplicationResources("orders")
	if err := s.migrateMySQLInstanceConnections(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot, s.ApplicationResources("orders")) {
		t.Fatal("migration is not idempotent")
	}
	// Upgrades execute the same migration on open; reopen the real SQLite file.
	dir := s.dataDir
	s.Close()
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, ok := reopened.GetConnection(legacy.Name).Config["database"]; ok {
		t.Fatal("restart restored default")
	}
	if reopened.ApplicationResources("orders").OwnBindings[0].Config["database"] != "dev_db" {
		t.Fatal("restart lost migrated binding")
	}
}

func TestMySQLInstanceMigrationRollbackAndStartup(t *testing.T) {
	s, _, _ := deliveryFixture(t)
	seedLegacyMySQL(t, s, ResourceConnection{Name: "legacy", Kind: "mysql", Endpoint: "127.0.0.1:1", Config: map[string]string{"database": "original_db"}})
	s.db.Exec(`INSERT INTO application_environment_resources(service,environment,alias,connection,config,updated_at) VALUES('orders','test','db','legacy','{}',123)`)
	s.db.Exec(`INSERT INTO deploy_meta(key,value) VALUES('resource_group:broken','{broken')`)
	if err := s.migrateMySQLInstanceConnections(); err == nil {
		t.Fatal("corrupt document accepted")
	}
	var config string
	s.db.QueryRow(`SELECT config FROM application_environment_resources WHERE service='orders'`).Scan(&config)
	if config != "{}" || s.GetConnection("legacy").Config["database"] != "original_db" {
		t.Fatal("failed migration committed partially", config, s.GetConnection("legacy").Config)
	}
	s.db.Exec(`DELETE FROM deploy_meta WHERE key='resource_group:broken'`)
	dir := s.dataDir
	s.Close()
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, exists := reopened.GetConnection("legacy").Config["database"]; exists {
		t.Fatal("open did not migrate")
	}
	reopened.db.QueryRow(`SELECT config FROM application_environment_resources WHERE service='orders'`).Scan(&config)
	if config != `{"database":"original_db"}` {
		t.Fatal("open lost database selection", config)
	}
}

func TestMySQLConnectionsCannotReintroduceDatabaseDefaults(t *testing.T) {
	s, _, _ := deliveryFixture(t)
	c := ResourceConnection{Name: "instance", Kind: "mysql", Endpoint: "127.0.0.1:1", Config: map[string]string{"database": "", "tls": "false"}}
	if err := s.UpsertConnection(c); err != nil {
		t.Fatal(err)
	}
	if _, exists := s.GetConnection(c.Name).Config["database"]; exists {
		t.Fatal("empty obsolete field retained")
	}
	c.Config["database"] = "forbidden"
	if err := s.UpsertConnection(c); err == nil {
		t.Fatal("connection default accepted")
	}
	if c.Config["database"] != "forbidden" {
		t.Fatal("caller mutated")
	}
	if _, err := mysqlDatabaseTarget(s.GetConnection(c.Name), nil); err == nil {
		t.Fatal("implicit schema accepted")
	}
	s.db.Exec(`INSERT INTO deploy_meta(key,value) VALUES('mysql_direct_query_scope:instance','prior_db')`)
	if err := s.DeleteConnection(c.Name); err != nil {
		t.Fatal(err)
	}
	c.Config = map[string]string{}
	if err := s.UpsertConnection(c); err != nil {
		t.Fatal(err)
	}
	if _, err := s.workbenchSQLConnection(WorkbenchQuery{Connection: c.Name, Environment: "test", Database: "new_db"}); err != nil {
		t.Fatal("deleted connection policy leaked to new instance", err)
	}
}

func TestMySQLInstanceMigrationWriteFailureRollsBack(t *testing.T) {
	s, _, _ := deliveryFixture(t)
	seedLegacyMySQL(t, s, ResourceConnection{Name: "legacy", Kind: "mysql", Endpoint: "127.0.0.1:1", Config: map[string]string{"database": "old_db"}})
	if _, err := s.db.Exec(`INSERT INTO application_environment_resources(service,environment,alias,connection,config,updated_at) VALUES('orders','test','db','legacy','{}',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_instance_migration BEFORE UPDATE ON resource_connections BEGIN SELECT RAISE(ABORT,'simulated disk write failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.migrateMySQLInstanceConnections(); err == nil {
		t.Fatal("simulated failure ignored")
	}
	var raw string
	if err := s.db.QueryRow(`SELECT config FROM application_environment_resources WHERE service='orders'`).Scan(&raw); err != nil || raw != "{}" {
		t.Fatal("partial binding write committed", raw, err)
	}
	if s.GetConnection("legacy").Config["database"] != "old_db" {
		t.Fatal("legacy default lost")
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM deploy_meta WHERE key='mysql_direct_query_scope:legacy'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial scope policy committed", err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_instance_migration`); err != nil {
		t.Fatal(err)
	}
	if err := s.migrateMySQLInstanceConnections(); err != nil {
		t.Fatal("migration cannot retry after rollback", err)
	}
}
