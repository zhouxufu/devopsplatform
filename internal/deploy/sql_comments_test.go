package deploy

import (
	"reflect"
	"strings"
	"testing"
)

func TestSQLChangeOrdinaryCommentsKeepValidatedMeaning(t *testing.T) {
	for _, tc := range []struct{ plain, commented string }{
		{"CREATE TABLE message (id bigint NOT NULL COMMENT '消息ID', PRIMARY KEY (id))", "-- 在目标库执行\r\n# ; DROP TABLE ignored\n/* 首次建表；注释中的 ' 和 ; 不分句 */\nCREATE /* 表 */ TABLE message (id bigint NOT NULL COMMENT '消息ID', PRIMARY KEY (id)); -- 完成"},
		{"UPDATE menu SET title='-- # /* not a comment */ /*! literal */' WHERE id=1", "/* 发布说明 */ UPDATE menu SET title='-- # /* not a comment */ /*! literal */' /* 字段说明 */ WHERE id=1; # 尾注释"},
		{"INSERT INTO menu (id,title) VALUES (1,'it''s # ok'); DELETE FROM menu WHERE id=2", "-- 两条数据变更\nINSERT INTO menu (id,title) VALUES (1,'it''s # ok');\n/* ; */ DELETE FROM menu WHERE id=2; /* trailing */"},
		{"ALTER TABLE message ADD COLUMN title varchar(32) COMMENT '标题'", "ALTER /*说明*/ TABLE message ADD COLUMN title varchar(32) COMMENT '标题'"},
		{"CREATE INDEX idx_id ON message (id)", "# index\nCREATE INDEX idx_id ON message /*说明*/ (id)"},
	} {
		expected, err := parseChangeSQL(tc.plain)
		if err != nil {
			t.Fatal("invalid test baseline", err)
		}
		actual, err := parseChangeSQL(tc.commented)
		if err != nil {
			t.Fatal("ordinary comment rejected", err)
		}
		if len(actual) != len(expected) {
			t.Fatal("statement count changed")
		}
		for i := range actual {
			if actual[i].SQL != expected[i].SQL && !sameSQLTokens(actual[i].SQL, expected[i].SQL) {
				t.Fatal("execution tokens changed", actual[i].SQL, expected[i].SQL)
			}
			actual[i].SQL = expected[i].SQL
		}
		if !reflect.DeepEqual(actual, expected) {
			t.Fatalf("comment changed execution plan\n%#v\n%#v", actual, expected)
		}
	}
}

func TestSQLChangeCommentsCannotHideUnsupportedExecution(t *testing.T) {
	for _, script := range []string{
		"/*!50700 DROP TABLE menu */ UPDATE menu SET enabled=1 WHERE id=1",
		"/*M!100100 DROP TABLE menu */ UPDATE menu SET enabled=1 WHERE id=1",
		"UPDATE /*+ SET_VAR(foreign_key_checks=OFF) */ menu SET enabled=1 WHERE id=1",
		"/* outer /* nested */ UPDATE menu SET enabled=1 WHERE id=1",
		"UPDATE menu SET enabled=1 WHERE id=1; /* unterminated",
		"-- only comments\n# no statements\n/* empty */",
		"-- safe looking\nDROP TABLE menu",
		"/* hidden null\x00 */ UPDATE menu SET enabled=1 WHERE id=1",
		"UPDATE menu SET enabled=1 /* no WHERE */",
		"UP/**/DATE menu SET enabled=1 WHERE id=1",
		"--not-a-mysql-comment\nUPDATE menu SET enabled=1 WHERE id=1",
		"-- header\nCREATE TABLE message (id int); DELETE FROM menu WHERE id=1",
		"-- header\nSTART TRANSACTION; UPDATE menu SET enabled=1 WHERE id=1; COMMIT",
	} {
		if _, err := parseChangeSQL(script); err == nil {
			t.Fatalf("unsupported script accepted: %s", script)
		}
	}
	// The shared read-query/data-sync grammar must not silently become broader.
	if _, err := sqlTokens("SELECT id FROM menu /* comment */"); err == nil {
		t.Fatal("read tokenizer comment policy changed")
	}
}

func TestSQLCommentMaskPreservesLineAndTokenBoundaries(t *testing.T) {
	source := "UPDATE/* 说明\r\n; */menu SET title='a''#/*b*/' WHERE id=1 -- end\r\n"
	got, err := sqlChangeWithoutComments(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(source) || strings.Count(got, "\n") != strings.Count(source, "\n") || strings.Count(got, "\r") != strings.Count(source, "\r") {
		t.Fatal("source locations changed")
	}
	if !strings.Contains(got, "'a''#/*b*/'") || strings.Contains(got, "UPDATEmenu") {
		t.Fatal("quoted value or token boundary changed", got)
	}
}

func TestDeliverySQLCommentsReadyPreservesFrozenSource(t *testing.T) {
	s, _, _ := resourceGroupFixture(t)
	w := &WorkItem{Title: "普通注释 SQL"}
	if err := s.CreateWorkItem(w); err != nil {
		t.Fatal(err)
	}
	script := "-- 发布说明\n/* 允许普通注释 */ UPDATE menu SET enabled=1 WHERE id=1; # 保留原文"
	base, _ := s.WorkDelivery(w.ID)
	req := WorkDeliverySave{Digest: base.Digest, MarkReady: true, Changes: []Change{{Kind: "mysql", Title: "说明注释", Service: "orders", SQL: &SQLChange{ResourceAlias: "db", Content: script, MaxAffectedRows: 1, EnvironmentOverrides: []SQLEnvironmentOverride{{Environment: "test", Reason: "测试核对", Content: script}}}}}}
	if err := s.SaveWorkDelivery(w.ID, &req, "alice", false); err != nil {
		t.Fatal(err)
	}
	got := s.GetChange(req.Changes[0].ID)
	if got.Status != ChangeReady || got.SQL.Content != script || got.SQL.EnvironmentOverrides[0].Content != script {
		t.Fatal("ready changed raw source", got)
	}
	if _, err := s.SetChangeReady(got.ID, true); err != nil {
		t.Fatal("direct readiness rejected ordinary comments", err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sql_execution_ledger`).Scan(&n); err != nil || n != 0 {
		t.Fatal("readiness executed SQL", n, err)
	}
}
