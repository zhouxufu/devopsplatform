package deploy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIndependentSQLDraftSharesReadinessContract(t *testing.T) {
	s, _, _ := resourceGroupFixture(t)
	script := "-- 保留原文\nSTART TRANSACTION;\nUPDATE config SET enabled=1 WHERE id=1;\nCOMMIT;\n"
	c := &Change{Kind: "mysql", Title: "独立 SQL", Service: "orders", Owner: "alice", SQL: &SQLChange{ResourceAlias: "db", Content: script, MaxAffectedRows: 100}}
	if err := s.CreateChange(c); err != nil {
		t.Fatal(err)
	}
	c = s.GetChange(c.ID)
	if c.SQL.Content != script {
		t.Fatal("create rewrote SQL")
	}
	c.SQL.Content += "-- 补充\n"
	if err := s.UpdateChange(c); err != nil {
		t.Fatal(err)
	}
	if got := s.GetChange(c.ID); got.SQL.Content != c.SQL.Content {
		t.Fatal("edit rewrote SQL")
	}
	if _, err := s.SetChangeReady(c.ID, true); err == nil {
		t.Fatal("draft bypassed readiness")
	}
	c.SQL.Content = "UPDATE config SET enabled=1 WHERE id=1"
	if err := s.UpdateChange(c); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetChangeReady(c.ID, true); err != nil {
		t.Fatal(err)
	}
}

func TestManualAssigneeAPIKeepsOmittedFieldAndRejectsUnknownAccount(t *testing.T) {
	s, _, _ := resourceGroupFixture(t)
	w := &WorkItem{Title: "原需求", Owner: "alice", AssignedTo: "bob"}
	if err := s.CreateWorkItem(w); err != nil {
		t.Fatal(err)
	}
	hub := NewLogHub()
	routes := Routes(s, NewBuilder(s, s.dataDir, hub), hub, "", NewStepLib("../../steps"))
	for _, tc := range []struct {
		body   string
		status int
		want   string
	}{
		{`{"title":"改标题","source":"manual","kind":"requirement","status":"open"}`, 200, "bob"},
		{`{"title":"改标题","source":"manual","kind":"requirement","status":"open","assigned_to":"unknown"}`, 400, "bob"},
		{`{"title":"改标题","source":"manual","kind":"requirement","status":"open","assigned_to":""}`, 200, ""},
	} {
		r := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/work-items/%d", w.ID), strings.NewReader(tc.body))
		r.Header.Set("X-User", "alice")
		r.Header.Set("X-Role", "member")
		r.Header.Set("X-Reviewer-Directory", `["alice","bob"]`)
		out := httptest.NewRecorder()
		routes.ServeHTTP(out, r)
		if out.Code != tc.status || s.GetWorkItem(w.ID).AssignedTo != tc.want {
			t.Fatal(out.Code, out.Body.String(), s.GetWorkItem(w.ID))
		}
	}
}

func TestManualAssigneeIsDistinctFromCreatorAndSourcePersonnel(t *testing.T) {
	s, _, _ := resourceGroupFixture(t)
	external := &WorkItem{Source: "zentao:source", ExternalID: "11", Title: "外部已关闭记录", AssignedTo: "closed"}
	if err := s.CreateWorkItem(external); err != nil {
		t.Fatal("manual validation affected source personnel", err)
	}
	if external.AssignedTo != "" {
		t.Fatal("unverified external personnel persisted")
	}
	w := &WorkItem{Title: "协作需求", Source: "manual", Kind: "requirement", Owner: "alice", AssignedTo: "bob"}
	if err := s.CreateWorkItem(w); err != nil {
		t.Fatal(err)
	}
	got := s.GetWorkItem(w.ID)
	if got.Owner != "alice" || got.OpenedBy != "alice" || got.AssignedTo != "bob" {
		t.Fatal(got)
	}
	for _, tc := range []struct {
		account, role string
		total         int
	}{{"alice", "assigned_to", 0}, {"bob", "assigned_to", 1}, {"alice", "opened_by", 1}, {"alice", "related", 1}} {
		page, err := s.WorkItemsPage(WorkItemFilter{PersonRole: tc.role, PersonAccounts: map[string]string{"manual": tc.account}})
		if err != nil || page.Total != tc.total {
			t.Fatalf("%+v got %+v %v", tc, page, err)
		}
	}
	for _, tc := range []struct {
		actor string
		total int
	}{{"alice", 0}, {"bob", 1}} {
		page, err := s.ResearchLivePage(context.Background(), tc.actor, researchLiveFilter{Connection: "manual", Mine: true, WorkItemFilter: WorkItemFilter{PersonRole: "assigned_to"}})
		if err != nil || page.Total != tc.total {
			t.Fatal(tc, page, err)
		}
	}
	got.AssignedTo = ""
	if err := s.UpdateWorkItemAs(got, "alice"); err != nil {
		t.Fatal(err)
	}
	if s.GetWorkItem(w.ID).AssignedTo != "" || s.GetWorkItem(w.ID).OpenedBy != "alice" {
		t.Fatal("clear changed creator")
	}
	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Set("X-User", "alice")
	r.Header.Set("X-Reviewer-Directory", `["alice","bob"]`)
	for _, account := range []string{"", "bob", "unknown"} {
		err := validateManualAssignee(r, &WorkItem{AssignedTo: account}, nil)
		if (err == nil) != (account != "unknown") {
			t.Fatal(account, err)
		}
	}
}

func TestDeliveryActivityRecordsContentAndRollsBackWithSave(t *testing.T) {
	s, _, _ := resourceGroupFixture(t)
	w := &WorkItem{Title: "活动验收", Owner: "alice"}
	if err := s.CreateWorkItem(w); err != nil {
		t.Fatal(err)
	}
	base, _ := s.WorkDelivery(w.ID)
	req := WorkDeliverySave{Digest: base.Digest, Changes: []Change{{Kind: "manual", Title: "验收步骤", Reference: "核对", DeliveryOwnership: &ChangeDeliveryOwnership{Mode: "self"}}}}
	if err := s.SaveWorkDelivery(w.ID, &req, "alice", false); err != nil {
		t.Fatal(err)
	}
	base, _ = s.WorkDelivery(w.ID)
	c := base.Changes[0]
	c.Summary = "第一行\n第二行"
	if err := s.SaveWorkDelivery(w.ID, &WorkDeliverySave{Digest: base.Digest, Changes: []Change{c}}, "alice", false); err != nil {
		t.Fatal(err)
	}
	activity, err := s.ResearchActivities(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, a := range activity {
		counts[a.Kind]++
		if strings.HasPrefix(a.Kind, "change-") && !strings.Contains(a.Content, `"change_id":`) {
			t.Fatal(a)
		}
	}
	if counts["change-created"] != 1 || counts["change-updated"] != 1 || counts["delivery-ownership"] != 1 {
		t.Fatal(counts)
	}
	base, _ = s.WorkDelivery(w.ID)
	if err := s.SaveWorkDelivery(w.ID, &WorkDeliverySave{Digest: "stale", Changes: []Change{base.Changes[0]}}, "alice", false); err == nil {
		t.Fatal("stale write succeeded")
	}
	after, _ := s.ResearchActivities(w.ID)
	if len(after) != len(activity) {
		t.Fatal("failed write emitted activity")
	}
}
