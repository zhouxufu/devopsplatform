package deploy

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestConfigKeyInspectionNeverReturnsValuesOrOtherKeys(t *testing.T) {
	for _, tc := range []struct{ format, content string }{{"yaml", "feature:\n  enabled: true\npassword: hidden-secret\n"}, {"json", `{"feature":{"enabled":true},"password":"hidden-secret"}`}, {"properties", "feature.enabled=true\npassword=hidden-secret"}} {
		out, err := inspectConfigKeys(tc.content, tc.format, []string{"feature.enabled", "password", "missing"})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(out)
		if strings.Contains(string(raw), "hidden-secret") || !out[0].(map[string]any)["exists"].(bool) || out[2].(map[string]any)["exists"].(bool) {
			t.Fatal(string(raw))
		}
	}
	for _, v := range []string{"a: 1\na: 2", "a: &x {b: true}\nc: *x", "a.b: 1\na: {b: 2}", "a: 1\n---\nb: 2"} {
		if _, err := inspectConfigKeys(v, "yaml", []string{"a.b"}); err == nil {
			t.Fatal("ambiguous config accepted", v)
		}
	}
	for _, key := range []string{"*", "a.*", "a[0]", ""} {
		if validateInspection(resourceInspectionRequest{Format: "yaml", Keys: []string{key}}, "nacos") == nil {
			t.Fatal("key expansion accepted")
		}
	}
}
func TestResourceInspectionUsesBoundNacosAndNoPublish(t *testing.T) {
	s, _, n, _ := nacosFixture(t)
	// Replace legacy unbound Data ID with the exact environment target.
	in := s.ApplicationResources("orders")
	for i := range in.Bindings {
		in.Bindings[i].Config = map[string]string{"data_id": "orders.yaml", "group": "DEFAULT_GROUP"}
	}
	if err := s.SaveApplicationResources("orders", in); err != nil {
		t.Fatal(err)
	}
	req := resourceInspectionRequest{Environment: "test", Alias: "config", Format: "yaml", Keys: []string{"feature.enabled", "absent"}}
	v, err := s.inspectResource(context.Background(), "orders", "nacos", req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(v)
	if strings.Contains(string(raw), "backup-only-secret") || strings.Contains(string(raw), "qa-password") || v["status"] != "differences" {
		t.Fatal(string(raw))
	}
	_, writes := n.snapshot()
	if writes != 0 {
		t.Fatal("published")
	}
	for i := range in.Bindings {
		if in.Bindings[i].Environment == "test" {
			in.Bindings[i].Config = map[string]string{}
		}
	}
	if err = s.SaveApplicationResources("orders", in); err != nil {
		t.Fatal(err)
	}
	if _, err = s.inspectResource(context.Background(), "orders", "nacos", req); err == nil {
		t.Fatal("missing exact config target accepted")
	}
	req.Environment = "missing"
	if _, err = s.inspectResource(context.Background(), "orders", "nacos", req); err == nil {
		t.Fatal("fell back to another environment")
	}
}

// Small driver verifies the exact bounded metadata contract, without a business DB.
type inspectionConnector struct{ t *testing.T }

func (c inspectionConnector) Connect(context.Context) (driver.Conn, error) {
	return inspectionConn{c.t}, nil
}
func (c inspectionConnector) Driver() driver.Driver { return inspectionDriver{} }

type inspectionDriver struct{}

func (inspectionDriver) Open(string) (driver.Conn, error) { return nil, fmt.Errorf("unused") }

type inspectionConn struct{ t *testing.T }

func (c inspectionConn) Prepare(string) (driver.Stmt, error) { return nil, fmt.Errorf("no statements") }
func (c inspectionConn) Close() error                        { return nil }
func (c inspectionConn) Begin() (driver.Tx, error)           { return nil, fmt.Errorf("no writes") }
func (c inspectionConn) QueryContext(_ context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if len(args) != 2 || args[0].Value != "bound_db" || args[1].Value != "orders" {
		c.t.Fatal("scope escaped", q, args)
	}
	switch {
	case strings.Contains(q, "information_schema.TABLES"):
		return &inspectionRows{cols: []string{"ENGINE", "TABLE_COLLATION"}, data: [][]driver.Value{{"InnoDB", "utf8mb4_general_ci"}}}, nil
	case strings.Contains(q, "information_schema.COLUMNS") && strings.Contains(q, "LIMIT 501"):
		return &inspectionRows{cols: []string{"COLUMN_NAME", "COLUMN_TYPE", "IS_NULLABLE", "COLUMN_DEFAULT", "EXTRA", "GENERATION_EXPRESSION"}, data: [][]driver.Value{{"id", "bigint", "NO", nil, "auto_increment", ""}, {"token", "varchar(64)", "YES", "never-return-this-default", "", ""}, {"total", "int", "YES", nil, "VIRTUAL GENERATED", "price * qty"}}}, nil
	case strings.Contains(q, "information_schema.STATISTICS") && strings.Contains(q, "LIMIT 501"):
		return &inspectionRows{cols: []string{"INDEX_NAME", "NON_UNIQUE", "COLUMN_NAME", "SUB_PART", "INDEX_TYPE"}, data: [][]driver.Value{{"PRIMARY", int64(0), "id", nil, "BTREE"}}}, nil
	}
	c.t.Fatal("unexpected query", q)
	return nil, fmt.Errorf("unexpected")
}

type inspectionRows struct {
	cols []string
	data [][]driver.Value
	at   int
}

func (r *inspectionRows) Columns() []string { return r.cols }
func (r *inspectionRows) Close() error      { return nil }
func (r *inspectionRows) Next(dest []driver.Value) error {
	if r.at >= len(r.data) {
		return io.EOF
	}
	copy(dest, r.data[r.at])
	r.at++
	return nil
}
func TestDatabaseInspectionBoundedMetadataAndDifferences(t *testing.T) {
	db := sql.OpenDB(inspectionConnector{t})
	defer db.Close()
	yes, no := true, false
	def := "different"
	result, err := readInspectionTable(context.Background(), db, "bound_db", schemaTableExpectation{Name: "orders", Columns: []schemaColumnExpectation{{Name: "id", Type: "varchar(20)", Nullable: &yes}, {Name: "total", Generated: &yes}, {Name: "token", Default: &def}, {Name: "missing"}}, Indexes: []schemaIndexExpectation{{Name: "PRIMARY", Unique: &no, Columns: []string{"token"}}, {Name: "missing"}}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "never-return-this-default") || strings.Contains(string(raw), "price * qty") || len(result["differences"].([]string)) != 7 {
		t.Fatal(string(raw))
	}
	for _, n := range []string{"other.orders", "orders;DROP TABLE x", "*"} {
		if validateInspection(resourceInspectionRequest{Tables: []schemaTableExpectation{{Name: n}}}, "mysql") == nil {
			t.Fatal("unsafe name")
		}
	}
}
