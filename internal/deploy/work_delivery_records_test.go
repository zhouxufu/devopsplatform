package deploy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkDeliveryRecordsUseFrozenScopeAndPermissions(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	work := &WorkItem{Title: "Bug", Source: "manual", Kind: "bug", Owner: "alice"}
	s.CreateWorkItem(work)
	other := &WorkItem{Title: "Other", Source: "manual"}
	s.CreateWorkItem(other)
	insert := func(plan DeliveryPlan, owner string, status string) int64 {
		raw, _ := json.Marshal(plan)
		result, err := s.db.Exec(`INSERT INTO delivery_runs(plan,digest,status,execution,env_secrets,owner,approver,created_at,revision) VALUES(?,'digest',?,'[]','{}',?,'',1,1)`, string(raw), status, owner)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := result.LastInsertId()
		return id
	}
	plan := DeliveryPlan{Scope: &DeliveryScope{WorkItems: []WorkItem{*work}, Changes: []Change{{ID: 10, Kind: "mysql", Title: "冻结SQL", SQL: &SQLChange{Content: "UPDATE test SET n=1"}}}}, Environment: Environment{Name: "test"}}
	first := insert(plan, "alice", "failed")
	insert(DeliveryPlan{Scope: &DeliveryScope{WorkItems: []WorkItem{*other}}}, "alice", "success")
	// A newly linked implementation must not retroactively link an old run.
	foreign := insert(plan, "bob", "pending_approval")
	got, more, err := s.WorkDeliveryRecords(work.ID, 0, "alice", false)
	if err != nil || more || len(got) != 1 || got[0].ID != first || got[0].Status != "failed" || got[0].ChangeCount != 1 || got[0].Loaded {
		t.Fatal(got, more, err)
	}
	got, _, err = s.WorkDeliveryRecords(work.ID, 0, "admin", true)
	if err != nil || len(got) != 2 || got[0].ID != foreign {
		t.Fatal(got, err)
	}
	mux := http.NewServeMux()
	registerWorkDeliveryRecordRoutes(mux, s)
	read := func(item, run int64, actor, role string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", fmt.Sprintf("/api/research/work-items/%d/delivery-records/%d", item, run), nil)
		req.Header.Set("X-User", actor)
		req.Header.Set("X-Role", role)
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, req)
		return out
	}
	if out := read(work.ID, first, "alice", ""); out.Code != 200 || !strings.Contains(out.Body.String(), "UPDATE test SET n=1") {
		t.Fatal(out.Code, out.Body.String())
	}
	if out := read(work.ID, foreign, "alice", ""); out.Code != 404 {
		t.Fatal("private run leaked", out.Code)
	}
	if out := read(other.ID, first, "admin", "admin"); out.Code != 404 {
		t.Fatal("unrelated work item matched run", out.Code)
	}
	// Legacy version and change-scoped snapshots are also matched exactly.
	insert(DeliveryPlan{Version: &ReleaseVersion{WorkItems: []WorkItem{*work}}}, "alice", "success")
	insert(DeliveryPlan{Scope: &DeliveryScope{Changes: []Change{{ID: 22, WorkItems: []WorkItem{*work}}}}}, "alice", "approved")
	for i := 0; i < 22; i++ {
		insert(plan, "alice", "success")
	}
	got, more, err = s.WorkDeliveryRecords(work.ID, 0, "alice", false)
	if err != nil || !more || len(got) != 20 {
		t.Fatal(len(got), more, err)
	}
	snapshot := workDeliveryRecord(s.DeliveryRun(first))
	if snapshot.Changes[0].SQL.Content != "UPDATE test SET n=1" || !snapshot.Loaded {
		t.Fatal("snapshot lost", snapshot)
	}
	cursor := got[len(got)-1].ID
	next, more, err := s.WorkDeliveryRecords(work.ID, cursor, "alice", false)
	if err != nil || more || len(next) != 5 {
		t.Fatal(fmt.Sprint(len(next)), more, err)
	}
	for _, r := range next {
		if r.ID >= cursor {
			t.Fatal("pagination duplicated a run")
		}
	}
	empty, more, err := s.WorkDeliveryRecords(other.ID, 0, "bob", false)
	if err != nil || more || len(empty) != 0 {
		t.Fatal(empty, more, err)
	}
}
