package deploy

import (
	"encoding/json"
	"testing"
)

func TestChangeMaintenanceAtomicPreviewCASOwnershipArchive(t *testing.T) {
	s, _, _ := deliveryFixture(t)
	a, b := &WorkItem{Title: "source"}, &WorkItem{Title: "target"}
	for _, w := range []*WorkItem{a, b} {
		if err := s.CreateWorkItem(w); err != nil {
			t.Fatal(err)
		}
	}
	c := &Change{Kind: "code", Title: "old", Service: "orders", Reference: "feature/old", Owner: "alice"}
	if err := s.CreateChange(c); err != nil {
		t.Fatal(err)
	}
	state, err := s.maintenanceState(c.ID, "orders", "alice")
	if err != nil {
		t.Fatal(err)
	}
	next := *state["change"].(*Change)
	next.Title = "corrected"
	next.Reference = "master-house-claim"
	ids := []int64{a.ID}
	req := changeMaintenanceRequest{Service: "orders", Digest: state["digest"].(string), Action: "edit", Reason: "correct original draft", Change: &next, WorkItemIDs: &ids, OriginWorkItemID: a.ID}
	next.DeliveryOwnership = &ChangeDeliveryOwnership{Mode: "follow", WorkItemID: b.ID}
	if _, err = s.maintainChange(c.ID, req, "bob", false); err == nil {
		t.Fatal("foreign draft")
	}
	if _, err = s.maintainChange(c.ID, req, "alice", false); err != nil {
		t.Fatal(err)
	}
	if s.GetChange(c.ID).Title != "old" || len(s.GetChange(c.ID).WorkItems) != 0 {
		t.Fatal("preview persisted")
	}
	if _, err = s.maintainChange(c.ID, req, "alice", true); err != nil {
		t.Fatal(err)
	}
	after := s.GetChange(c.ID)
	if after.Title != "corrected" || after.DeliveryOwnership.Mode != "follow" || len(after.WorkItems) != 2 {
		t.Fatalf("%+v", after)
	}
	if _, err = s.maintainChange(c.ID, req, "alice", true); err == nil {
		t.Fatal("stale digest")
	}
	state, _ = s.maintenanceState(c.ID, "orders", "alice")
	next = *state["change"].(*Change)
	next.Title = "must rollback"
	next.DeliveryOwnership = &ChangeDeliveryOwnership{Mode: "follow", WorkItemID: 9999}
	req = changeMaintenanceRequest{Service: "orders", Digest: state["digest"].(string), Action: "edit", Reason: "bad target", Change: &next}
	if _, err = s.maintainChange(c.ID, req, "alice", true); err == nil {
		t.Fatal("invalid ownership")
	}
	if s.GetChange(c.ID).Title != "corrected" {
		t.Fatal("partial save")
	}
	req = changeMaintenanceRequest{Service: "orders", Digest: state["digest"].(string), Action: "archive", Reason: "canceled by author"}
	if _, err = s.maintainChange(c.ID, req, "alice", true); err != nil {
		t.Fatal(err)
	}
	active, err := s.WorkDelivery(a.ID)
	if err != nil || len(active.Changes) != 0 {
		t.Fatal("archive remained in delivery content", err)
	}
	var selected int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM changes WHERE id IN (`+deliveryOwnedChanges("?")+`)`, b.ID).Scan(&selected); err != nil || selected != 0 {
		t.Fatal("archive remained in version range", err)
	}
	if s.GetChange(c.ID).Status != "archived" {
		t.Fatal("archive lost")
	}
	if _, err = s.SetChangeReady(c.ID, true); err == nil {
		t.Fatal("archived became ready")
	}
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='maintain-change'`).Scan(&count)
	if count != 2 {
		t.Fatal("audit not atomic", count)
	}
}
func TestChangeMaintenanceBlocksFrozenAndExecuted(t *testing.T) {
	for _, kind := range []string{"ready", "version", "run"} {
		t.Run(kind, func(t *testing.T) {
			s, _, _ := deliveryFixture(t)
			c := &Change{Kind: "code", Title: "old", Service: "orders", Reference: "feature/fix", Owner: "alice"}
			if err := s.CreateChange(c); err != nil {
				t.Fatal(err)
			}
			if kind == "ready" {
				s.SetChangeReady(c.ID, true)
			}
			if kind == "version" {
				if _, err := s.db.Exec(`INSERT INTO release_version_changes(release_version_id,change_id) VALUES(99,?)`, c.ID); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "run" {
				raw, _ := json.Marshal(DeliveryPlan{Scope: &DeliveryScope{Changes: []Change{*c}}})
				if _, err := s.db.Exec(`INSERT INTO delivery_runs(plan,digest,status,execution,env_secrets,owner,approver,created_at,revision) VALUES(?,'x','running','[]','{}','alice','',0,1)`, string(raw)); err != nil {
					t.Fatal(err)
				}
			}
			state, _ := s.maintenanceState(c.ID, "orders", "alice")
			_, err := s.maintainChange(c.ID, changeMaintenanceRequest{Service: "orders", Digest: state["digest"].(string), Action: "archive", Reason: "cancel"}, "alice", true)
			if err == nil {
				t.Fatal("frozen draft changed")
			}
		})
	}
}
