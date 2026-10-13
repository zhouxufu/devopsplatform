package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func zentaoFixture(t *testing.T, bugPage func(*http.Request) (any, string)) (*Store, string) {
	t.Helper()
	st, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { st.Close() })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/zentao/index.php" {
			t.Errorf("wrong installation path: %s", r.URL.Path)
		}
		var data any
		switch r.URL.Query().Get("f") {
		case "getSessionID":
			data = map[string]string{"sessionName": "zentaosid", "sessionID": "qa-session"}
		case "login":
			cookie, _ := r.Cookie("zentaosid")
			if cookie == nil || cookie.Value != "qa-session" {
				t.Error("missing session cookie")
			}
			_ = r.ParseForm()
			if r.Method != "POST" || r.PostForm.Get("account") != "sync" || r.PostForm.Get("password") != "qa-secret" || strings.Contains(r.URL.RawQuery, "qa-secret") {
				t.Error("unsafe/missing credentials")
			}
			data = map[string]any{"user": map[string]string{"account": "sync"}}
		case "browse":
			cookie, _ := r.Cookie("zentaosid")
			if cookie == nil {
				t.Error("missing authenticated session")
			}
			var status string
			data, status = bugPage(r)
			if status != "" {
				_ = json.NewEncoder(w).Encode(map[string]any{"status": status})
				return
			}
		default:
			t.Errorf("unexpected request: %s", r.URL)
		}
		// Real legacy JSON envelopes can contain JSON-encoded strings.
		raw, _ := json.Marshal(data)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": string(raw)})
	}))
	t.Cleanup(server.Close)
	if e = st.UpsertConnection(ResourceConnection{Name: "禅道测试", Kind: "zentao", Endpoint: server.URL + "/zentao", Username: "sync", Password: "qa-secret", Config: map[string]string{"product_ids": "1", "api_mode": "legacy"}}); e != nil {
		t.Fatal(e)
	}
	return st, "禅道测试"
}
func TestZentao16SyncDedupPreservesAssociations(t *testing.T) {
	title := "Bug title"
	st, name := zentaoFixture(t, func(r *http.Request) (any, string) {
		return map[string]any{"bugs": map[string]any{"7": map[string]any{"id": "7", "title": title, "status": "closed", "closedDate": "2026-10-01 12:00:00", "steps": "details"}}}, ""
	})
	for i := 0; i < 2; i++ {
		if _, e := st.syncZentao(context.Background(), name, "2026-09-30", "alice"); e != nil {
			t.Fatal(e)
		}
	}
	items := st.WorkItems("", "", 100)
	if len(items) != 1 || items[0].Status != WorkItemClosed || items[0].Source != "zentao:"+name {
		t.Fatalf("items: %+v", items)
	}
	change := &Change{Kind: "manual", Title: "Fix", Owner: "alice"}
	if e := st.CreateChange(change); e != nil {
		t.Fatal(e)
	}
	if e := st.SetChangeWorkItems(change.ID, []int64{items[0].ID}); e != nil {
		t.Fatal(e)
	}
	title = "updated externally"
	if _, e := st.syncZentao(context.Background(), name, "", "scheduler"); e != nil {
		t.Fatal(e)
	}
	got := st.GetChange(change.ID)
	if len(got.WorkItems) != 1 || got.WorkItems[0].Title != title {
		t.Fatalf("association lost: %+v", got)
	}
	data, _ := json.Marshal(st.Connections())
	if strings.Contains(string(data), "qa-secret") {
		t.Fatal("public connection leaked secret")
	}
	connection := st.GetConnection(name)
	connection.Endpoint += "/other-installation"
	if e := st.UpsertConnection(*connection); e == nil {
		t.Fatal("changed external identity under the same synchronized connection")
	}
	if e := st.DeleteConnection(name); e == nil {
		t.Fatal("deleted synchronized connection and orphaned issue identity")
	}
}
func TestZentaoFailedPageIsAtomic(t *testing.T) {
	st, name := zentaoFixture(t, func(r *http.Request) (any, string) {
		if r.URL.Query().Get("pageID") == "2" {
			return nil, "fail"
		}
		bugs := []map[string]any{}
		for i := 1; i <= 100; i++ {
			bugs = append(bugs, map[string]any{"id": i, "title": fmt.Sprint(i), "status": "closed"})
		}
		return map[string]any{"bugs": bugs}, ""
	})
	if _, e := st.syncZentao(context.Background(), name, "", "alice"); e == nil {
		t.Fatal("accepted failed second page")
	}
	if len(st.WorkItems("", "", 500)) != 0 {
		t.Fatal("partly imported a failed sync")
	}
	states := st.issueSyncStates()
	if len(states) != 1 || states[0].Status != "failed" {
		t.Fatalf("states: %+v", states)
	}
}
func TestZentaoBaselineAndRepeatedPagination(t *testing.T) {
	st, name := zentaoFixture(t, func(r *http.Request) (any, string) {
		return map[string]any{"bugs": []map[string]any{{"id": 1, "title": "old", "status": "closed", "closedDate": "2025-01-01"}, {"id": 2, "title": "new", "status": "closed", "closedDate": "2026-10-01"}}}, ""
	})
	if _, e := st.syncZentao(context.Background(), name, "2026-01-01", "alice"); e != nil {
		t.Fatal(e)
	}
	if items := st.WorkItems("", "", 100); len(items) != 1 || items[0].ExternalID != "2" {
		t.Fatalf("baseline not applied: %+v", items)
	}
}
func TestSQLPayloadPersistsAndRejectsUnsafe(t *testing.T) {
	st, _, _ := deliveryFixture(t)
	c := &Change{Kind: "mysql", Title: "add menu", Service: "orders", SQL: &SQLChange{ResourceAlias: "db", Content: "ALTER TABLE menus ADD COLUMN label varchar(64) NOT NULL DEFAULT '';", MaxAffectedRows: 100}}
	if e := st.CreateChange(c); e != nil {
		t.Fatal(e)
	}
	if v := st.GetChange(c.ID); v.SQL == nil || v.SQL.ResourceAlias != "db" {
		t.Fatalf("payload missing %+v", v)
	}
	if v := st.Changes("", "", "", 100); len(v) != 1 || v[0].SQL == nil {
		t.Fatal("list missing payload")
	}
	c.SQL.Content = "DROP TABLE menus"
	if e := st.UpdateChange(c); e == nil {
		t.Fatal("accepted destructive DDL")
	}
	if st.GetChange(c.ID).SQL.Content == c.SQL.Content {
		t.Fatal("failed update modified payload")
	}
}
func TestSQLParserAndSchemaSimulation(t *testing.T) {
	script := "CREATE TABLE menus (id bigint NOT NULL, name varchar(64) NOT NULL DEFAULT '', PRIMARY KEY (id)); ALTER TABLE menus ADD COLUMN enabled int NOT NULL DEFAULT 1; CREATE UNIQUE INDEX idx_name ON menus (name)"
	ops, e := parseChangeSQL(script)
	if e != nil {
		t.Fatal(e)
	}
	catalog := sqlCatalog{}
	for i := range ops {
		if e = applySQLSchema(catalog, &ops[i]); e != nil {
			t.Fatal(e)
		}
	}
	if len(catalog["menus"].Columns) != 3 || !catalog["menus"].Indexes["idx_name"].Unique {
		t.Fatalf("invalid virtual schema: %+v", catalog)
	}
	// Compare CREATE with the original definition, not a table altered later.
	initial := sqlCatalog{}
	first := ops[0]
	if e = applySQLSchema(initial, &first); e != nil {
		t.Fatal(e)
	}
	first = ops[0]
	initial["menus"].Collation = "utf8mb4_0900_ai_ci"
	for name, col := range initial["menus"].Columns {
		initial["menus"].Columns[name] = defaultColumnCollation(col, initial["menus"].Collation)
	}
	if e = applySQLSchema(initial, &first); e != nil || !first.Skip {
		t.Fatalf("identical create not skipped: %v", e)
	}
	col := initial["menus"].Columns["name"]
	col.Type = "varchar(32)"
	initial["menus"].Columns["name"] = col
	first = ops[0]
	if e = applySQLSchema(initial, &first); e == nil {
		t.Fatal("skipped conflicting structure")
	}
	for _, unsafe := range []string{
		"DROP TABLE menus", "ALTER TABLE menus DROP COLUMN name", "CREATE TABLE menus (id int) AS SELECT 1", "CREATE TABLE menus (id int) garbage", "ALTER TABLE menus ADD COLUMN id int, DROP COLUMN name", "CREATE INDEX idx ON menus (name(3))", "UPDATE menus SET name='x'", "DELETE FROM menus WHERE 1=1", "UPDATE menus SET name=sleep(1) WHERE id=1", "INSERT INTO menus (id) SELECT id FROM other", "INSERT INTO menus (id) VALUES (@@version)", "CREATE TABLE menus (id int); INSERT INTO menus (id) VALUES (1)", "/*!50000 DROP TABLE menus */", "SELECT 1", "UPDATE menus SET name='x' WHERE id IN (SELECT id FROM other)",
	} {
		if _, e := parseChangeSQL(unsafe); e == nil {
			t.Errorf("accepted unsafe SQL: %s", unsafe)
		}
	}
	for _, safe := range []string{"INSERT INTO menus (id,name) VALUES (1,'a;b'),(2,'test')", "UPDATE menus SET name='x' WHERE id=1 AND name='old'", "DELETE FROM menus WHERE id=1 OR id=2"} {
		if _, e := parseChangeSQL(safe); e != nil {
			t.Errorf("rejected %s: %s", safe, e)
		}
	}
}
