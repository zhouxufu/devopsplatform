package deploy

import (
	"context"
	"strings"
	"testing"
)

func TestDeliverySQLDraftPreservesOriginalAndCannotBecomeReady(t *testing.T) {
	s, _, _ := resourceGroupFixture(t)
	w := &WorkItem{Title: "SQL 原文录入", Owner: "alice"}
	if err := s.CreateWorkItem(w); err != nil {
		t.Fatal(err)
	}
	scripts := []string{
		"-- 上线说明\nSTART TRANSACTION;\nUPDATE config SET enabled=1 WHERE id=1;\nCOMMIT;\n",
		"CREATE TABLE message (id bigint NOT NULL, PRIMARY KEY (id)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='消息记录' ROW_FORMAT=COMPRESSED;",
		"DROP TABLE message",
	}
	for _, script := range scripts {
		base, _ := s.WorkDelivery(w.ID)
		req := WorkDeliverySave{Digest: base.Digest, Changes: []Change{{Kind: "mysql", Title: "待核对", Service: "orders", SQL: &SQLChange{ResourceAlias: "db", Content: script, MaxAffectedRows: 100}}}}
		if err := s.SaveWorkDelivery(w.ID, &req, "alice", false); err != nil {
			t.Fatal(err)
		}
		id := req.Changes[0].ID
		got := s.GetChange(id)
		if got.Kind != "mysql" || got.Status != ChangeDraft || got.SQL.Content != script {
			t.Fatal("SQL draft lost its type or original", got)
		}
		if _, err := s.SetChangeReady(id, true); err == nil {
			t.Fatal("direct ready bypassed SQL validation")
		}
		if s.GetChange(id).Status != ChangeDraft {
			t.Fatal("failed readiness changed state")
		}
		base, _ = s.WorkDelivery(w.ID)
		if err := s.SaveWorkDelivery(w.ID, &WorkDeliverySave{Digest: base.Digest, MarkReady: true, Changes: []Change{*got, {Kind: "manual", Title: "不得部分就绪", Reference: "核对"}}}, "alice", false); err == nil {
			t.Fatal("editor readiness accepted invalid SQL")
		}
		after, _ := s.WorkDelivery(w.ID)
		if after.Digest != base.Digest {
			t.Fatal("readiness partially saved")
		}
		if _, err := s.prepareSQLNode(context.Background(), "sql", []int64{id}, "test"); err == nil {
			t.Fatal("draft entered execution preparation")
		}
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sql_execution_ledger`).Scan(&n); err != nil || n != 0 {
		t.Fatal("draft executed SQL", n, err)
	}
}

func TestDeliverySQLDraftCanBeCorrectedWithoutRecreatingChange(t *testing.T) {
	s, _, _ := resourceGroupFixture(t)
	w := &WorkItem{Title: "补齐 SQL"}
	s.CreateWorkItem(w)
	base, _ := s.WorkDelivery(w.ID)
	req := WorkDeliverySave{Digest: base.Digest, Changes: []Change{{Kind: "mysql", Title: "修改", Service: "orders", SQL: &SQLChange{ResourceAlias: "db", Content: "-- 待补充\nUPDATE menu SET enabled=1", MaxAffectedRows: 1}}}}
	if err := s.SaveWorkDelivery(w.ID, &req, "alice", false); err != nil {
		t.Fatal(err)
	}
	id := req.Changes[0].ID
	base, _ = s.WorkDelivery(w.ID)
	c := base.Changes[0]
	c.SQL.Content = "UPDATE menu SET enabled=1 WHERE id=1"
	if err := s.SaveWorkDelivery(w.ID, &WorkDeliverySave{Digest: base.Digest, MarkReady: true, Changes: []Change{c}}, "alice", false); err != nil {
		t.Fatal(err)
	}
	if got := s.GetChange(id); got.Status != ChangeReady || got.SQL.Content != c.SQL.Content {
		t.Fatal(got)
	}
}

func TestDeliverySQLDraftBoundsAndEnvironmentExceptions(t *testing.T) {
	s, _, _ := resourceGroupFixture(t)
	w := &WorkItem{Title: "环境 SQL"}
	s.CreateWorkItem(w)
	for _, body := range []string{"", "   ", strings.Repeat("x", 65537), "SELECT\x001"} {
		base, _ := s.WorkDelivery(w.ID)
		r := WorkDeliverySave{Digest: base.Digest, Changes: []Change{{Kind: "mysql", Title: "边界", Service: "orders", SQL: &SQLChange{ResourceAlias: "db", Content: body, MaxAffectedRows: 1}}}}
		if err := s.SaveWorkDelivery(w.ID, &r, "alice", false); err == nil {
			t.Fatal("invalid draft payload accepted")
		}
	}
	base, _ := s.WorkDelivery(w.ID)
	r := WorkDeliverySave{Digest: base.Digest, Changes: []Change{{Kind: "mysql", Title: "环境专用", Service: "orders", SQL: &SQLChange{ResourceAlias: "db", Content: "UPDATE menu SET enabled=1 WHERE id=1", MaxAffectedRows: 1, EnvironmentOverrides: []SQLEnvironmentOverride{{Environment: "test", Reason: "待核对原文", Content: "-- 测试说明\nDROP TABLE menu"}}}}}}
	if err := s.SaveWorkDelivery(w.ID, &r, "alice", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetChangeReady(r.Changes[0].ID, true); err == nil {
		t.Fatal("environment exception bypassed readiness")
	}
	base, _ = s.WorkDelivery(w.ID)
	c := base.Changes[0]
	c.SQL.ResourceAlias = "unknown"
	if err := s.SaveWorkDelivery(w.ID, &WorkDeliverySave{Digest: base.Digest, Changes: []Change{c}}, "alice", false); err == nil {
		t.Fatal("draft widened resource authorization")
	}
}
