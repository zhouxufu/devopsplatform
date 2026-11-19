package deploy

import (
	"os"
	"strings"
	"testing"
)

func TestDeliveryEvidenceDocumentGuardPreservesValidJSON(t *testing.T) {
	s, _, _ := deliveryFixture(t)
	expr := mysqlDeliveryEvidenceDocument("?")
	for _, raw := range []string{"", `{}`, `{"scope":{"work_items":[{"id":7}],"changes":[{"id":4}]}}`, "not-json"} {
		var got string
		if err := s.db.QueryRow("SELECT "+expr, raw, raw).Scan(&got); err != nil {
			t.Fatal(err)
		}
		want := raw
		if raw == "" || raw == "not-json" {
			want = "{}"
		}
		if got != want {
			t.Fatal("changed valid snapshot", got, want)
		}
	}
}

func TestMySQLCandidateSingleItemNestedProof(t *testing.T) {
	dsn := os.Getenv("DEVOPS_TEST_RESEARCH_MYSQL_DSN")
	if dsn == "" {
		t.Skip("requires disposable MySQL schema")
	}
	s, err := OpenDatabase(t.TempDir(), DatabaseConfig{Driver: "mysql", DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w := &WorkItem{Source: "manual", Kind: "bug", Title: "nested proof regression", Owner: "alice"}
	if err = s.CreateWorkItem(w); err != nil {
		t.Fatal(err)
	}
	c := &Change{Kind: "manual", Title: "manual result", Owner: "alice"}
	if err = s.CreateChange(c); err != nil {
		t.Fatal(err)
	}
	if err = s.SetChangeWorkItems(c.ID, []int64{w.ID}); err != nil {
		t.Fatal(err)
	}
	for _, ids := range [][]int64{nil, {w.ID}} {
		out, err := s.WorkCandidates(WorkCandidateFilter{IDs: ids, WorkItemFilter: WorkItemFilter{Query: "nested proof regression", PageSize: 100}})
		if err != nil {
			t.Fatal(err)
		}
		if out.Total != 1 || out.Items[0].SuccessfulDelivery || out.Items[0].PartialDelivery {
			t.Fatal("absent evidence inferred delivery", out)
		}
	}
	if !strings.Contains(mysqlDeliveryEvidenceDocument("dr.plan"), "JSON_VALID(dr.plan)") {
		t.Fatal("guard missing")
	}
}
