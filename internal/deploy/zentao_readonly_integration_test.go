package deploy

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Explicit opt-in smoke test: decrypt credentials in memory from a read-only
// SQLite store and query ZenTao only. Never imports Bugs/users or logs records.
func TestZentaoReadOnlyDateScope(t *testing.T) {
	dir, name := os.Getenv("DEVOPS_ZENTAO_TEST_DATA"), os.Getenv("DEVOPS_ZENTAO_TEST_CONNECTION")
	if dir == "" || name == "" {
		t.Skip("requires explicit real-service opt in")
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "deploy.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	key, err := os.ReadFile(filepath.Join(dir, ".credential-key"))
	if err != nil {
		t.Fatal("cannot read credential key")
	}
	st := &Store{db: &deployDB{DB: db, dialect: "sqlite"}, secretKey: key}
	c := st.GetConnection(name)
	if c == nil {
		t.Fatal("connection not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	z, err := newZentaoLegacy(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := zentaoProductIDs(c)
	if err != nil {
		t.Fatal(err)
	}
	baseline := os.Getenv("DEVOPS_ZENTAO_TEST_BASELINE")
	if baseline == "" {
		t.Fatal("explicit baseline is required")
	}
	if _, err = time.Parse("2006-01-02", baseline); err != nil {
		t.Fatal("invalid baseline")
	}
	rows, err := z.filteredBugs(ctx, ids, baseline, "closed")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("read-only scope: products=%d closed_matching=%d baseline=%s; imported=0", len(ids), len(rows), baseline)
}
