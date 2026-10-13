package deploy

import (
	"context"
	"strings"
	"testing"
)

func TestSQLChangeAliasNormalization(t *testing.T) {
	for _, input := range []string{
		"UPDATE agent_scope_state t SET t.item_index=1 WHERE t.session_id='session-1'",
		"UPDATE agent_scope_state AS t SET item_index=1 WHERE t.session_id='session-1'",
		"UPDATE estate_ai_seek.agent_scope_state t SET t.item_index=1 WHERE t.session_id='session-1'",
	} {
		ops, err := parseChangeSQL(input)
		if err != nil || len(ops) != 1 {
			t.Fatalf("%s: %v", input, err)
		}
		op := ops[0]
		if op.Table != "agent_scope_state" || op.Scope != "session_id = 'session-1'" || strings.Contains(op.SQL, "t.") || strings.Contains(op.SQL, "estate_ai_seek.") {
			t.Fatalf("alias leaked to scope/execution: %+v", op)
		}
		if err := validateSQLDatabase(op, "estate_ai_seek"); err != nil {
			t.Fatal(err)
		}
		if op.Database != "" && validateSQLDatabase(op, "another_db") == nil {
			t.Fatal("cross database accepted")
		}
	}
	for _, unsafe := range []string{
		"UPDATE a t JOIN b x ON t.id=x.id SET t.name='x' WHERE t.id=1",
		"UPDATE a t,b x SET t.name='x' WHERE t.id=1",
		"UPDATE a t SET x.name='x' WHERE t.id=1",
		"UPDATE a t SET t.name='x' WHERE x.id=1",
		"UPDATE a t SET t.name='x' WHERE t.id IN (SELECT id FROM b)",
		"UPDATE a t SET t.name='x'",
		"UPDATE a t SET t.name='x' WHERE 1=1",
		"UPDATE a t SET t.name=sleep(1) WHERE t.id=1",
		"DELETE t FROM a t WHERE t.id=1",
		"UPDATE a t USE INDEX (id) SET t.name='x' WHERE t.id=1",
	} {
		if _, err := parseChangeSQL(unsafe); err == nil {
			t.Errorf("unsafe SQL accepted: %s", unsafe)
		}
	}
	// Read and data-sync grammars are not widened by the change-only helper.
	if _, err := parseSyncQuery("SELECT t.id FROM a t WHERE t.id=1"); err == nil {
		t.Fatal("sync alias restriction removed")
	}
}

func TestSQLChangeQualifiedDatabaseRejectedBeforeConnecting(t *testing.T) {
	st, _, _ := deliveryFixture(t)
	c := ResourceConnection{Name: "offline", Kind: "mysql", Endpoint: "127.0.0.1:1", Username: "qa", Config: map[string]string{"database": "target_db", "tls": "false"}}
	if err := st.UpsertConnection(c); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveApplicationResources("orders", ApplicationResources{Requirements: []ApplicationResourceRequirement{{Alias: "db", Kind: "mysql"}}, Bindings: []ApplicationEnvironmentResource{{Alias: "db", Environment: "test", Connection: c.Name}}}); err != nil {
		t.Fatal(err)
	}
	change := sqlChangeFixture(t, st, "UPDATE other_db.menus t SET t.name='new' WHERE t.id=1")
	_, err := st.prepareSQLNode(context.Background(), "sql", []int64{change.ID}, "test")
	if err == nil || !strings.Contains(err.Error(), "禁止跨库") {
		t.Fatalf("expected rejection before target connection: %v", err)
	}
}
