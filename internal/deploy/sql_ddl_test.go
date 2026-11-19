package deploy

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

type reportedSQL struct {
	ID      int64  `json:"id"`
	Content string `json:"content"`
}

func reportedSQLCorpus(t *testing.T) []reportedSQL {
	t.Helper()
	path := os.Getenv("DEVOPS_SQL_DDL_CORPUS")
	if path == "" {
		t.Skip("opt-in recorded SQL static regression")
	}
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var rows []reportedSQL
	if e = json.Unmarshal(b, &rows); e != nil {
		t.Fatal(e)
	}
	return rows
}
func TestSQLDDLReportedCorpus(t *testing.T) {
	for _, row := range reportedSQLCorpus(t) {
		t.Run(fmt.Sprint(row.ID), func(t *testing.T) {
			ops, e := parseChangeSQL(row.Content)
			if e != nil {
				t.Fatal(e)
			}
			if len(ops) == 0 {
				t.Fatal("no operations")
			}
			for _, op := range ops {
				again, e := parseChangeSQL(op.SQL)
				if e != nil || len(again) != 1 {
					t.Fatal("normalized execution cannot be reparsed", e)
				}
				if digestJSON(op.definition) != digestJSON(again[0].definition) || digestJSON(op.column) != digestJSON(again[0].column) || digestJSON(op.index) != digestJSON(again[0].index) {
					t.Fatal("execution meaning changed")
				}
			}
		})
	}
}
func TestSQLDDLTypedOptionsAndGuards(t *testing.T) {
	script := "CREATE TABLE MixedCase (id bigint NOT NULL PRIMARY KEY COMMENT 'ID', status int DEFAULT 0, request_id varchar(64) COLLATE utf8mb4_bin NULL DEFAULT NULL, created datetime(3) DEFAULT CURRENT_TIMESTAMP(3), active_id bigint GENERATED ALWAYS AS (CASE WHEN status IN (0,1) THEN id ELSE NULL END) STORED, UNIQUE KEY uk_active (active_id) COMMENT 'unique key') COMMENT='table; not SQL' COLLATE=utf8mb4_general_ci ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"
	ops, e := parseChangeSQL(script)
	if e != nil {
		t.Fatal(e)
	}
	d := ops[0].definition
	if ops[0].Table != "MixedCase" || d.Comment != "table; not SQL" || d.Collation != "utf8mb4_general_ci" || d.Columns["request_id"].Collation != "utf8mb4_bin" || d.Columns["id"].Nullable || d.Indexes["uk_active"].Comment != "unique key" || d.Columns["active_id"].Extra != "STORED GENERATED" {
		t.Fatalf("lost DDL semantics: %+v", d)
	}
	for _, bad := range []string{
		"CREATE TABLE t (id int DEFAULT NaN)", "CREATE TABLE t (id int CHARACTER SET utf8mb4)", "CREATE TABLE t (id int) DEFAULT COMMENT='bad'", "CREATE TABLE t (id int, g bigint GENERATED ALWAYS AS (CASE WHEN missing=1 THEN id ELSE NULL END) STORED)", "CREATE TABLE t (id int, g bigint GENERATED ALWAYS AS (CASE WHEN id=1 THEN id ELSE NULL END FROM dual) STORED)",
		"CREATE TABLE t (id int) ENGINE=MyISAM", "CREATE TABLE t (id int) ENGINE=InnoDB ENGINE=InnoDB", "CREATE TABLE t (id int) DATA DIRECTORY='/tmp'", "CREATE TABLE t (id int) AS SELECT 1", "CREATE TABLE t LIKE another", "CREATE TABLE t (id int, CHECK(id>0))", "CREATE TABLE t (id int DEFAULT (sleep(1)))", "CREATE TABLE t (id int, active bigint GENERATED ALWAYS AS (sleep(1)) STORED)", "CREATE TABLE t (id int, active bigint GENERATED ALWAYS AS (CASE WHEN id=1 THEN (SELECT 1) ELSE NULL END) STORED)", "CREATE TABLE t (id int, active bigint GENERATED ALWAYS AS (CASE WHEN id=1 THEN @secret ELSE NULL END) STORED)", "ALTER TABLE t ADD COLUMN id int, DROP COLUMN name", "ALTER TABLE t ADD UNIQUE KEY idx (id) INVISIBLE", "CREATE INDEX idx ON t (id) USING HASH", "ALTER TABLE t ADD id int AFTER name", "CREATE TABLE t (id int); DROP TABLE t", "CREATE TABLE t (id int) /*!50100 PARTITION BY HASH(id) */",
	} {
		if _, e := parseChangeSQL(bad); e == nil {
			t.Fatalf("unsupported SQL accepted: %s", bad)
		}
	}
}
func TestSQLDDLGenerationReadbackEquivalence(t *testing.T) {
	a, e := canonicalSQLGeneration("CASE WHEN task_code = 'HOUSE_SHARE' THEN share_id ELSE NULL END")
	if e != nil {
		t.Fatal(e)
	}
	b, e := canonicalSQLGeneration("(case when (`task_code` = _utf8mb4'HOUSE_SHARE') then `share_id` else NULL end)")
	if e != nil || a != b {
		t.Fatal("server quoting/charset changed meaning", a, b, e)
	}
	c, _ := canonicalSQLGeneration("CASE WHEN task_code = 'house_share' THEN share_id ELSE NULL END")
	if a == c {
		t.Fatal("string literal case lost")
	}
}
func TestSQLDDLExplicitCollationAndCommentsCannotBeSkipped(t *testing.T) {
	ops, e := parseChangeSQL("CREATE TABLE t (id bigint PRIMARY KEY, name varchar(10), KEY idx (name) COMMENT 'index') COLLATE=utf8mb4_general_ci COMMENT='table'")
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(ops[0].definition)
	for _, field := range []string{"table-comment", "index-comment", "collation", "column-collation"} {
		var actual sqlTable
		json.Unmarshal(raw, &actual)
		switch field {
		case "table-comment":
			actual.Comment = "other"
		case "index-comment":
			v := actual.Indexes["idx"]
			v.Comment = "other"
			actual.Indexes["idx"] = v
		case "collation":
			actual.Collation = "utf8mb4_bin"
		case "column-collation":
			v := actual.Columns["name"]
			v.Collation = "utf8mb4_bin"
			actual.Columns["name"] = v
		}
		op := ops[0]
		if e := applySQLSchema(sqlCatalog{"t": &actual}, &op); e == nil {
			t.Fatal("drift ignored", field)
		}
	}
	if !strings.Contains(ops[0].SQL, "COMMENT='table'") {
		t.Fatal("table comment discarded")
	}
}

func TestSQLDDLReportedReadinessPreservesOriginal(t *testing.T) {
	rows := reportedSQLCorpus(t)
	s, _, _ := resourceGroupFixture(t)
	w := &WorkItem{Title: "DDL corpus isolated", Owner: "alice"}
	if err := s.CreateWorkItem(w); err != nil {
		t.Fatal(err)
	}
	base, _ := s.WorkDelivery(w.ID)
	req := WorkDeliverySave{Digest: base.Digest}
	for _, row := range rows {
		req.Changes = append(req.Changes, Change{Kind: "mysql", Title: fmt.Sprint(row.ID), Service: "orders", SQL: &SQLChange{ResourceAlias: "db", Content: row.Content, MaxAffectedRows: 100}})
	}
	if err := s.SaveWorkDelivery(w.ID, &req, "alice", false); err != nil {
		t.Fatal(err)
	}
	base, _ = s.WorkDelivery(w.ID)
	req.Digest = base.Digest
	req.MarkReady = true
	if err := s.SaveWorkDelivery(w.ID, &req, "alice", false); err != nil {
		t.Fatal(err)
	}
	for i, c := range req.Changes {
		got := s.GetChange(c.ID)
		if got.Status != ChangeReady || got.SQL.Content != rows[i].Content {
			t.Fatalf("changed original/state #%d", rows[i].ID)
		}
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sql_execution_ledger`).Scan(&n); err != nil || n != 0 {
		t.Fatal("readiness executed SQL", n, err)
	}
}

func TestSQLDDLLegacyStatementBytes(t *testing.T) {
	for _, script := range []string{"CREATE TABLE IF NOT EXISTS `legacy` (id bigint NOT NULL,\n name varchar(10) DEFAULT '', PRIMARY KEY (id)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4", "CREATE TABLE legacy (id int)"} {
		match := createTableRE.FindStringSubmatch(script)
		ops, err := parseChangeSQL(script)
		if err != nil || len(ops) != 1 {
			t.Fatal(err)
		}
		want := "create table " + match[1] + " " + match[2] + " ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"
		if ops[0].SQL != want {
			t.Fatalf("historical ledger bytes changed: %q != %q", ops[0].SQL, want)
		}
	}
}
