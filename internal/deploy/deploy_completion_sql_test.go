package deploy

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strings"
	"testing"
)

// Mimics the MySQL 1093 restriction to keep the regression executable without
// a database service. The opt-in MySQL smoke test also executes FinishDeploy.
type completionSQLDriver struct{}
type completionSQLConn struct{}

func (completionSQLDriver) Open(string) (driver.Conn, error) { return completionSQLConn{}, nil }
func (completionSQLConn) Prepare(string) (driver.Stmt, error) {
	return nil, fmt.Errorf("unexpected prepare")
}
func (completionSQLConn) Close() error              { return nil }
func (completionSQLConn) Begin() (driver.Tx, error) { return nil, fmt.Errorf("unexpected begin") }
func (completionSQLConn) ExecContext(_ context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	if strings.Contains(q, "SELECT") || strings.Contains(q, "strftime") {
		return nil, fmt.Errorf("MySQL 1093 or unconverted SQLite syntax")
	}
	if !strings.Contains(q, "ended_at=?") || !strings.Contains(q, "status IN") {
		return nil, fmt.Errorf("missing terminal guard/time")
	}
	if len(args) != 3 {
		return nil, fmt.Errorf("missing result bindings")
	}
	ended, ok := args[1].Value.(int64)
	if !ok {
		return nil, fmt.Errorf("invalid completion time")
	}
	if (args[0].Value == "needs_review" && ended != 0) || (args[0].Value != "needs_review" && ended <= 0) {
		return nil, fmt.Errorf("incorrect completion time for result")
	}
	return driver.RowsAffected(1), nil
}
func init() { sql.Register("deploy-completion-mysql-fixture", completionSQLDriver{}) }
func TestFinishDeployUsesPortableMySQLUpdate(t *testing.T) {
	db, err := sql.Open("deploy-completion-mysql-fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &Store{db: &deployDB{DB: db, dialect: "mysql"}}
	if err := s.FinishDeploy(1, "failed"); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishDeploy(2, "needs_review"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err := s.FinishDeploy(1, "success"); err == nil {
		t.Fatal("database update error ignored")
	}
}
