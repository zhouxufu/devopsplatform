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

func TestResearchReleaseMarkLifecycleDoesNotForgeDelivery(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.UpsertEnvironment(Environment{Name: "prod", DisplayName: "生产", Purpose: "production"}); err != nil {
		t.Fatal(err)
	}
	yes := true
	item := &WorkItem{Title: "已关闭但待上线", Kind: "bug", Owner: "alice", ReleaseRequired: &yes}
	if err = s.CreateWorkItem(item); err != nil {
		t.Fatal(err)
	}
	query := researchLiveFilter{Connection: "manual", Delivery: "pending_release", WorkItemFilter: WorkItemFilter{Relation: "related", RelationActor: "alice"}}
	page, err := s.ResearchLivePage(context.Background(), "alice", query)
	if err != nil || page.Total != 1 {
		t.Fatal(page, err)
	}
	if _, _, err = s.setResearchReleaseMark(item.ID, 0, true, "已随生产版本上线", "eve", false); err != errReleaseMarkForbidden {
		t.Fatal("unrelated user accepted", err)
	}
	mark, changed, err := s.setResearchReleaseMark(item.ID, 0, true, "已随生产版本上线", "alice", false)
	if err != nil || !changed || !mark.Marked || mark.Revision != 1 || mark.Actor != "alice" || mark.At == 0 {
		t.Fatal(mark, changed, err)
	}
	if _, changed, err = s.setResearchReleaseMark(item.ID, 0, true, "已随生产版本上线", "alice", false); err != nil || changed {
		t.Fatal("retry duplicated", changed, err)
	}
	if _, _, err = s.setResearchReleaseMark(item.ID, 0, false, "", "alice", false); err == nil {
		t.Fatal("stale revision accepted")
	}
	page, err = s.ResearchLivePage(context.Background(), "alice", query)
	if err != nil || page.Total != 0 || len(page.Items) != 0 {
		t.Fatal("marked item still pending", page, err)
	}
	query.Delivery = ""
	page, err = s.ResearchLivePage(context.Background(), "alice", query)
	if err != nil || page.Total != 1 || page.Items[0].ReleaseMark == nil || !page.Items[0].ReleaseMark.Marked || page.Items[0].SuccessfulDelivery {
		t.Fatal("manual mark confused with machine success", page, err)
	}
	if current := s.GetWorkItem(item.ID); current.ReleaseRequired == nil || !*current.ReleaseRequired || current.Status != item.Status {
		t.Fatal("mark changed work item state", current)
	}
	candidates, err := s.WorkCandidates(WorkCandidateFilter{IDs: []int64{item.ID}, ProductionOnly: true})
	if err != nil || len(candidates.Items) != 1 || candidates.Items[0].SuccessfulDelivery {
		t.Fatal(candidates, err)
	}
	if s.DeleteWorkItem(item.ID) == nil {
		t.Fatal("deleted release history")
	}
	mark, changed, err = s.setResearchReleaseMark(item.ID, 1, false, "误操作撤销", "admin", true)
	if err != nil || !changed || mark.Marked || mark.Revision != 2 {
		t.Fatal(mark, changed, err)
	}
	query.Delivery = "pending_release"
	page, err = s.ResearchLivePage(context.Background(), "alice", query)
	if err != nil || page.Total != 1 {
		t.Fatal("undo did not restore pending", page, err)
	}
	rows, err := s.ResearchActivities(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, row := range rows {
		if row.Kind == "release-mark" {
			count++
		}
	}
	if count != 2 {
		t.Fatal("expected one mark and one undo activity", count)
	}
	if _, _, err = s.setResearchReleaseMark(item.ID, 2, true, "", "local", true); err == nil {
		t.Fatal("anonymous admin accepted")
	}
}

func TestResearchReleaseMarkLiveSourceBatchAndNative(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(fmt.Sprint(native), func(t *testing.T) {
			s, state := researchLiveFixture(t, 4)
			items := registerPendingFixture(t, s, 4)
			for _, item := range items {
				if _, err := s.db.Exec(`INSERT INTO research_work_relations(work_item_id,actor,kind,created_at) VALUES(?,'alice','participant',1)`, item.ID); err != nil {
					t.Fatal(err)
				}
			}
			state.mu.Lock()
			state.modelDisabled = native
			state.mu.Unlock()
			_, _, err := s.setResearchReleaseMark(items[0].ID, 0, true, "已上线", "alice", false)
			if err != nil {
				t.Fatal(err)
			}
			f := researchLiveFilter{Connection: "issues", Delivery: "pending_release", WorkItemFilter: WorkItemFilter{Relation: "related", RelationActor: "alice"}}
			page, err := s.ResearchLivePage(context.Background(), "alice", f)
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range page.Items {
				if w.ID == items[0].ID {
					t.Fatal("marked source item still pending")
				}
			}
			f.Delivery = ""
			f.Relation = ""
			f.RelationActor = ""
			page, err = s.ResearchLivePage(context.Background(), "alice", f)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, w := range page.Items {
				if w.ID == items[0].ID {
					found = true
					if !manuallyMarkedReleased(w.WorkItem) || w.SuccessfulDelivery {
						t.Fatal(w)
					}
				}
			}
			if !found {
				t.Fatal("source item disappeared from all")
			}
			state.mu.Lock()
			defer state.mu.Unlock()
			if state.businessWrites != 0 {
				t.Fatal("source business mutation", state.businessWrites)
			}
		})
	}
}

func TestResearchReleaseMarkAPIAndPersistentRevision(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	item := &WorkItem{Title: "上线标记 API", Owner: "alice"}
	if err = s.CreateWorkItem(item); err != nil {
		t.Fatal(err)
	}
	h := http.NewServeMux()
	registerResearchReleaseMarkRoutes(h, s)
	send := func(actor, method, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, fmt.Sprintf("/api/research/work-items/%d/release-mark", item.ID), strings.NewReader(body))
		r.Header.Set("X-User", actor)
		r.Header.Set("X-Role", "member")
		out := httptest.NewRecorder()
		h.ServeHTTP(out, r)
		return out
	}
	if r := send("alice", "GET", ""); r.Code != 200 || !strings.Contains(r.Body.String(), `"can_mark":true`) {
		t.Fatal(r.Code, r.Body.String())
	}
	if r := send("eve", "PUT", `{"marked":true,"revision":0}`); r.Code != 403 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r := send("alice", "PUT", `{"marked":true}`); r.Code != 400 {
		t.Fatal(r.Code, r.Body.String())
	}
	r := send("alice", "PUT", `{"marked":true,"revision":0,"comment":"确认上线"}`)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	var mark ResearchReleaseMark
	if err = json.Unmarshal(r.Body.Bytes(), &mark); err != nil || !mark.Marked {
		t.Fatal(mark, err)
	}
	if r := send("alice", "PUT", `{"marked":false,"revision":0}`); r.Code != 409 {
		t.Fatal(r.Code, r.Body.String())
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	saved, err := s.researchReleaseMark(item.ID)
	if err != nil || saved != mark {
		t.Fatal("restart lost mark", saved, err)
	}
}
