package deploy

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMySQLInstanceBindingsChooseIndependentDatabases(t *testing.T) {
	s, _, _ := deliveryFixture(t)
	connection := ResourceConnection{Name: "shared-instance", Kind: "mysql", Endpoint: "127.0.0.1:1", Username: "reader", Password: "fixture-secret", Config: map[string]string{"tls": "false"}}
	if e := s.UpsertConnection(connection); e != nil {
		t.Fatal("instance without schema rejected", e)
	}
	bindings := func(a, b string) ApplicationResources {
		return ApplicationResources{Requirements: []ApplicationResourceRequirement{{Alias: "db", Kind: "mysql"}}, Bindings: []ApplicationEnvironmentResource{{Alias: "db", Environment: "test", Connection: connection.Name, Config: map[string]string{"database": a}}, {Alias: "db", Environment: "prod", Connection: connection.Name, Config: map[string]string{"database": b}}}}
	}
	if e := s.SaveApplicationResources("orders", bindings("orders_test", "orders_prod")); e != nil {
		t.Fatal(e)
	}
	if e := s.UpsertService(Service{Name: "billing", ArtifactType: "jar"}); e != nil {
		t.Fatal(e)
	}
	if e := s.SaveApplicationResources("billing", bindings("billing_test", "billing_prod")); e != nil {
		t.Fatal(e)
	}
	for _, scope := range []struct{ app, env, db string }{{"orders", "test", "orders_test"}, {"orders", "prod", "orders_prod"}, {"billing", "test", "billing_test"}, {"billing", "prod", "billing_prod"}} {
		c, e := s.sqlBinding(scope.app, scope.env, "db")
		if e != nil || c.Config["database"] != scope.db {
			t.Fatal(scope, c, e)
		}
		if c.Config["tls"] != "false" || c.Password != "fixture-secret" {
			t.Fatal("lost connection configuration")
		}
		c.Config["database"] = "mutated-local-copy"
	}
	if s.GetConnection(connection.Name).Config["database"] != "" {
		t.Fatal("binding polluted shared instance")
	}
	if s.sqlPhysicalTarget(connection.Name, "orders_test") == s.sqlPhysicalTarget(connection.Name, "billing_test") {
		t.Fatal("different databases collapsed to one target")
	}
	var changes []int64
	for _, app := range []string{"orders", "billing"} {
		change := Change{Kind: "mysql", Service: app, Title: "schema target", RiskLevel: "low", SQL: &SQLChange{ResourceAlias: "db", Content: "CREATE TABLE fixture (id int)", MaxAffectedRows: 100}}
		if e := s.CreateChange(&change); e != nil {
			t.Fatal(e)
		}
		changes = append(changes, change.ID)
	}
	nodes, targets, e := s.expandDeliveryResourceNodes([]DeliveryNode{{ID: "sql", Kind: "mysql", ChangeIDs: changes}}, nil, "test")
	if e != nil || len(nodes) != 2 || len(targets) != 2 || targets[0].DestinationDigest == targets[1].DestinationDigest {
		t.Fatalf("different schemas must expand to separate reviewed nodes: %v %v %v", nodes, targets, e)
	}
	for _, bad := range []map[string]string{{}, {"database": "wrong-db"}, {"database": "orders_test", "username": "other"}, {"database": "orders_test", "endpoint": "other:3306"}} {
		in := bindings("orders_test", "orders_prod")
		in.Bindings[0].Config = bad
		if e := s.SaveApplicationResources("orders", in); e == nil {
			t.Fatal("invalid binding accepted", bad)
		}
	}
	if c, e := s.sqlBinding("orders", "test", "db"); e != nil || c.Config["database"] != "orders_test" {
		t.Fatal("failed save changed active binding", e)
	}
	group := ResourceGroup{Name: "shared-databases", Requirements: []ApplicationResourceRequirement{{Alias: "audit", Kind: "mysql"}}, Bindings: []ApplicationEnvironmentResource{{Alias: "audit", Environment: "test", Connection: connection.Name, Config: map[string]string{"database": "audit_test"}}}}
	if e := s.SaveResourceGroup(&group); e != nil {
		t.Fatal(e)
	}
}

func TestMySQLLegacyAndReviewedBindingDrift(t *testing.T) {
	s, _, _ := resourceGroupFixture(t)
	old, e := s.sqlBinding("orders", "test", "db")
	if e != nil || old.Config["database"] != "dev_db" {
		t.Fatal(old, e)
	}
	change := Change{Kind: "mysql", Service: "orders", Title: "schema selection", RiskLevel: "low", SQL: &SQLChange{ResourceAlias: "db", Content: "CREATE TABLE fixture (id int)", MaxAffectedRows: 100}}
	if e = s.CreateChange(&change); e != nil {
		t.Fatal(e)
	}
	current, e := s.SetChangeReady(change.ID, true)
	if e != nil {
		t.Fatal(e)
	}
	plan := &SQLExecutionPlan{Connection: old.Name, Database: "dev_db", DestinationDigest: sqlDestination(old), ChangeIDs: []int64{current.ID}, Services: []string{"orders"}, Aliases: []string{"db"}, ChangeDigests: []string{digestJSON([]any{current.Service, current.SQL})}}
	if _, e = s.reviewedSQLConnection(plan, "test"); e != nil {
		t.Fatal("legacy reviewed plan rejected", e)
	}
	group := s.ResourceGroup("business")
	// Locate the test/db binding instead of relying on storage order.
	for i := range group.Bindings {
		if group.Bindings[i].Environment == "test" && group.Bindings[i].Alias == "db" {
			group.Bindings[i].Config = map[string]string{"database": "different_schema"}
		}
	}
	if e = s.SaveResourceGroup(group); e != nil {
		t.Fatal(e)
	}
	if _, e = s.reviewedSQLConnection(plan, "test"); e == nil {
		t.Fatal("same connection with changed schema bypassed reviewed snapshot")
	}
	if old.Config["database"] != "dev_db" || s.GetConnection(old.Name).Config["database"] != "" {
		t.Fatal("connection instance or effective target changed")
	}
}

func TestMySQLDirectDatabaseApprovalScopeAndDiscoveryAuthorization(t *testing.T) {
	s, _, _ := deliveryFixture(t)
	c := ResourceConnection{Name: "instance", Kind: "mysql", Endpoint: "127.0.0.1:1", Config: map[string]string{}}
	if e := s.UpsertConnection(c); e != nil {
		t.Fatal(e)
	}
	q := WorkbenchQuery{Connection: c.Name, Environment: "test", Database: "alpha", SQL: "SELECT id FROM orders"}
	_, a, e := s.queryTargets("dbms", q)
	if e != nil {
		t.Fatal(e)
	}
	q.Database = "beta"
	_, b, e := s.queryTargets("dbms", q)
	if e != nil || a == b {
		t.Fatal("schema not part of approval identity", e)
	}
	q.Database = ""
	if _, _, e = s.queryTargets("dbms", q); e == nil {
		t.Fatal("instance query without schema accepted")
	}
	q.Service = "orders"
	q.Connection = ""
	q.Alias = "db"
	q.Database = "alpha"
	if _, e = s.workbenchSQLConnection(q); e == nil {
		t.Fatal("application query overrode binding")
	}
	legacy := c
	legacy.Name = "legacy"
	legacy.Config = map[string]string{"database": "old_db"}
	seedLegacyMySQL(t, s, legacy)
	if e = s.migrateMySQLInstanceConnections(); e != nil {
		t.Fatal(e)
	}
	q = WorkbenchQuery{Connection: legacy.Name, Environment: "test", Database: "old_db", SQL: "SELECT id FROM orders"}
	if _, e = s.workbenchSQLConnection(q); e != nil {
		t.Fatal(e)
	}
	q.Database = "other_db"
	if _, e = s.workbenchSQLConnection(q); e == nil {
		t.Fatal("legacy direct-query scope silently expanded")
	}
	h := Routes(s, NewBuilder(s, t.TempDir(), NewLogHub()), NewLogHub(), "", NewStepLib(t.TempDir()))
	request := httptest.NewRequest("GET", "/api/connections/instance/databases", nil)
	request.Header.Set("X-Role", "member")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request)
	if w.Code != 403 || strings.Contains(w.Body.String(), "fixture-secret") {
		t.Fatal(w.Code, w.Body.String())
	}
	request = httptest.NewRequest("GET", "/api/connections/missing/databases", nil)
	request.Header.Set("X-Role", "admin")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, request)
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
}

type schemaCatalogDriver struct{}
type schemaCatalogConn struct{ many bool }
type schemaCatalogRows struct {
	names []string
	i     int
}

func (schemaCatalogDriver) Open(dsn string) (driver.Conn, error) {
	return schemaCatalogConn{many: dsn == "many"}, nil
}
func (schemaCatalogConn) Prepare(string) (driver.Stmt, error) {
	return nil, fmt.Errorf("unexpected prepare")
}
func (schemaCatalogConn) Close() error              { return nil }
func (schemaCatalogConn) Begin() (driver.Tx, error) { return nil, fmt.Errorf("unexpected transaction") }
func (c schemaCatalogConn) QueryContext(ctx context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if q != mysqlDatabaseCatalogSQL {
		return nil, fmt.Errorf("unexpected query")
	}
	names := []string{"information_schema", "mysql", "sys", "performance_schema", "app_test", "app_prod", "unsupported-name"}
	if c.many {
		names = nil
		for i := 0; i < 1001; i++ {
			names = append(names, fmt.Sprintf("db_%04d", i))
		}
	}
	return &schemaCatalogRows{names: names}, nil
}
func (*schemaCatalogRows) Columns() []string { return []string{"SCHEMA_NAME"} }
func (*schemaCatalogRows) Close() error      { return nil }
func (r *schemaCatalogRows) Next(dest []driver.Value) error {
	if r.i >= len(r.names) {
		return io.EOF
	}
	dest[0] = r.names[r.i]
	r.i++
	return nil
}
func init() { sql.Register("schema-catalog-fixture", schemaCatalogDriver{}) }
func TestMySQLDatabaseDiscoveryReadOnlyAndBounded(t *testing.T) {
	for _, many := range []bool{false, true} {
		dsn := ""
		if many {
			dsn = "many"
		}
		db, e := sql.Open("schema-catalog-fixture", dsn)
		if e != nil {
			t.Fatal(e)
		}
		names, truncated, e := readMySQLDatabases(context.Background(), db)
		db.Close()
		if e != nil {
			t.Fatal(e)
		}
		if many {
			if len(names) != 1000 || !truncated {
				t.Fatal(len(names), truncated)
			}
		} else if strings.Join(names, ",") != "app_prod,app_test" || truncated {
			t.Fatal(names, truncated)
		}
	}
}
