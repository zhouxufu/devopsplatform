package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func TestSQLMySQLDDLExtendedRoundTrip(t *testing.T) {
	st, m, db, table := sqlTargetFixture(t)
	script := fmt.Sprintf(`CREATE TABLE %s (
 id BIGINT NOT NULL PRIMARY KEY COMMENT 'identifier', state int DEFAULT 0,
 task_code varchar(32) COLLATE utf8mb4_bin DEFAULT NULL,
 created datetime(3) DEFAULT CURRENT_TIMESTAMP(3),
 pending_id bigint GENERATED ALWAYS AS (CASE WHEN state IN (0,1) THEN id ELSE NULL END) STORED,
 shared_id bigint GENERATED ALWAYS AS (CASE WHEN task_code = 'HOUSE_SHARE' THEN id ELSE NULL END) VIRTUAL COMMENT 'generated',
 UNIQUE KEY uk_pending (pending_id) COMMENT 'pending key'
 ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='messages';
 ALTER TABLE %s ADD COLUMN notice_key VARCHAR(96) COLLATE utf8mb4_bin NULL DEFAULT NULL COMMENT 'notice';
 ALTER TABLE %s ADD UNIQUE KEY uk_notice (notice_key) COMMENT 'notice key'`, table, table, table)
	c := sqlChangeFixture(t, st, script)
	run := sqlRunFixture(t, st, m, c, "test")
	got, err := st.AdvanceDeliveryRun(run.ID, "alice", false, m)
	if err != nil || got.Steps[0].Status != "success" {
		ops, _ := parseChangeSQL(script)
		expected := sqlCatalog{}
		for i := range ops {
			if e := applySQLSchema(expected, &ops[i]); e != nil {
				t.Fatal(e)
			}
		}
		actual, _ := readSQLCatalogVersion(context.Background(), db, "delivery_qa", 2)
		a, _ := json.MarshalIndent(actual[table], "", " ")
		b, _ := json.MarshalIndent(expected[table], "", " ")
		t.Logf("actual %s\nexpected %s", a, b)
		t.Fatalf("extended DDL execution/readback: %v %+v", err, got.Steps)
	}
	if _, err = db.Exec("INSERT INTO `" + table + "` (id,state,task_code,notice_key) VALUES (1,0,'HOUSE_SHARE','A'),(2,2,'house_share','a')"); err != nil {
		t.Fatal(err)
	}
	var pending, shared int
	if err = db.QueryRow("SELECT pending_id,shared_id FROM `"+table+"` WHERE id=1").Scan(&pending, &shared); err != nil || pending != 1 || shared != 1 {
		t.Fatal("generated behavior", pending, shared, err)
	}
	var count int
	if err = db.QueryRow("SELECT COUNT(*) FROM `" + table + "` WHERE id=2 AND pending_id IS NULL AND shared_id IS NULL AND created IS NOT NULL").Scan(&count); err != nil || count != 1 {
		t.Fatal("collation/default behavior", count, err)
	}
	// New identical ALTER is skipped; a conflicting comment cannot be skipped.
	c2 := sqlChangeFixture(t, st, fmt.Sprintf("ALTER TABLE %s ADD UNIQUE KEY uk_notice (notice_key) COMMENT 'notice key'", table))
	p, err := st.prepareSQLNode(context.Background(), "same", []int64{c2.ID}, "test")
	if err != nil || !p.Statements[0].Skip {
		t.Fatal("identical index", err)
	}
	c3 := sqlChangeFixture(t, st, fmt.Sprintf("ALTER TABLE %s ADD UNIQUE KEY uk_notice (notice_key) COMMENT 'different'", table))
	if _, err = st.prepareSQLNode(context.Background(), "different", []int64{c3.ID}, "test"); err == nil {
		t.Fatal("index comment mismatch ignored")
	}
	legacy, err := readSQLCatalogVersion(context.Background(), db, "delivery_qa", 0)
	if err != nil {
		t.Fatal(err)
	}
	original, err := readSQLCatalog(context.Background(), db, "delivery_qa")
	if err != nil || digestJSON(legacy) != digestJSON(original) {
		t.Fatal("legacy snapshot changed", err)
	}
}

func TestSQLMySQLDDLCommentDriftBlocksWrites(t *testing.T) {
	for _, kind := range []string{"table", "index"} {
		t.Run(kind, func(t *testing.T) {
			st, m, db, table := sqlTargetFixture(t)
			if _, err := db.Exec(fmt.Sprintf("CREATE TABLE %s (id bigint PRIMARY KEY, KEY idx(id) COMMENT 'before') COMMENT='before'", table)); err != nil {
				t.Fatal(err)
			}
			c := sqlChangeFixture(t, st, fmt.Sprintf("ALTER TABLE %s ADD COLUMN new_value int DEFAULT 0", table))
			run := sqlRunFixture(t, st, m, c, "test")
			change := fmt.Sprintf("ALTER TABLE %s COMMENT='after'", table)
			if kind == "index" {
				change = fmt.Sprintf("ALTER TABLE %s DROP INDEX idx, ADD KEY idx(id) COMMENT 'after'", table)
			}
			if _, err := db.Exec(change); err != nil {
				t.Fatal(err)
			}
			got, err := st.AdvanceDeliveryRun(run.ID, "alice", false, m)
			if err == nil && got.Steps[0].Status == "success" {
				t.Fatal("drift executed")
			}
			var count int
			if err = db.QueryRow("SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA='delivery_qa' AND TABLE_NAME=? AND COLUMN_NAME='new_value'", table).Scan(&count); err != nil || count != 0 {
				t.Fatal("DDL ran after drift", count, err)
			}
		})
	}
}

// Actual reported definitions, isolated table names and synthetic pre-existing
// base tables. This verifies the engine path, not the business database state.
func TestSQLMySQLDDLReportedCorpus(t *testing.T) {
	rows := reportedSQLCorpus(t)
	st, m, db, prefix := sqlTargetFixture(t)
	names := map[string]string{}
	created := map[string]bool{}
	for _, row := range rows {
		ops, e := parseChangeSQL(row.Content)
		if e != nil {
			t.Fatal(e)
		}
		for _, op := range ops {
			if _, ok := names[op.Table]; !ok {
				names[op.Table] = fmt.Sprintf("%s_%d", prefix, len(names))
			}
			if op.Action == "create-table" {
				created[op.Table] = true
			}
		}
	}
	for original, name := range names {
		name := name
		t.Cleanup(func() { db.Exec("DROP TABLE IF EXISTS `" + name + "`") })
		if !created[original] {
			if _, e := db.Exec("CREATE TABLE `" + name + "` (id bigint NOT NULL PRIMARY KEY) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"); e != nil {
				t.Fatal(e)
			}
		}
	}
	for _, row := range rows {
		t.Run(fmt.Sprint(row.ID), func(t *testing.T) {
			ops, e := parseChangeSQL(row.Content)
			if e != nil {
				t.Fatal(e)
			}
			var scripts []string
			for _, op := range ops {
				// Every supported DDL has a single unqualified target. Match only its
				// first identifier occurrence; never alter column/comment/string content.
				re := regexp.MustCompile("(?i)^(create\\s+table\\s+|alter\\s+table\\s+|create\\s+(?:unique\\s+)?index\\s+[`A-Za-z0-9_]+\\s+on\\s+)`?" + regexp.QuoteMeta(op.Table) + "`?(\\s|\\()")
				rewritten := re.ReplaceAllString(op.SQL, "${1}`"+names[op.Table]+"`${2}")
				if rewritten == op.SQL {
					t.Fatal("target remap failed")
				}
				scripts = append(scripts, rewritten)
			}
			c := sqlChangeFixture(t, st, strings.Join(scripts, ";"))
			run := sqlRunFixture(t, st, m, c, "test")
			got, e := st.AdvanceDeliveryRun(run.ID, "alice", false, m)
			if e != nil || got.Steps[0].Status != "success" {
				t.Fatalf("reported #%d: %v %+v", row.ID, e, got.Steps)
			}
		})
	}
}
