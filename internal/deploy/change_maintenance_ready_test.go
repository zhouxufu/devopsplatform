package deploy

import (
	"encoding/json"
	"strings"
	"testing"
)

func readyMaintenanceFixture(t *testing.T) (*Store, *Change) {
	t.Helper()
	s, _, _ := deliveryFixture(t)
	c := &Change{Kind: "code", Title: "old title", Summary: "old summary", Service: "orders", Reference: "feature/fix", Owner: "alice"}
	if e := s.CreateChange(c); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SetChangeReady(c.ID, true); e != nil {
		t.Fatal(e)
	}
	return s, s.GetChange(c.ID)
}
func maintenancePatch(t *testing.T, s *Store, c *Change, fields map[string]string) changeMaintenanceRequest {
	t.Helper()
	state, e := s.maintenanceState(c.ID, c.Service, c.Owner)
	if e != nil {
		t.Fatal(e)
	}
	patch := map[string]json.RawMessage{}
	for k, v := range fields {
		patch[k], _ = json.Marshal(v)
	}
	return changeMaintenanceRequest{Service: c.Service, Digest: state["digest"].(string), Action: "edit", Reason: "correct verified source explanation", Patch: patch}
}
func maintenanceRun(t *testing.T, s *Store, c Change, status string) (int64, string) {
	t.Helper()
	raw, _ := json.Marshal(DeliveryPlan{Scope: &DeliveryScope{Changes: []Change{c}}})
	r, e := s.db.Exec(`INSERT INTO delivery_runs(plan,digest,status,execution,env_secrets,owner,approver,created_at,revision) VALUES(?,'frozen',?,'[]','{}','alice','',0,1)`, string(raw), status)
	if e != nil {
		t.Fatal(e)
	}
	id, _ := r.LastInsertId()
	return id, string(raw)
}
func TestReadyCodeDescriptionRevisionPreservesSnapshotsAndAudit(t *testing.T) {
	s, c := readyMaintenanceFixture(t)
	runID, frozen := maintenanceRun(t, s, *c, "success")
	if _, e := s.db.Exec(`INSERT INTO release_version_changes(release_version_id,change_id) VALUES(99,?)`, c.ID); e != nil {
		t.Fatal(e)
	}
	req := maintenancePatch(t, s, c, map[string]string{"title": "corrected title", "summary": "verified explanation"})
	preview, e := s.maintainChange(c.ID, req, "alice", false)
	if e != nil || preview["effect"] != "ready_description_revised" {
		t.Fatal(preview, e)
	}
	if s.GetChange(c.ID).Summary != c.Summary {
		t.Fatal("preview persisted")
	}
	if _, e = s.maintainChange(c.ID, req, "bob", true); e == nil {
		t.Fatal("other owner edited")
	}
	result, e := s.maintainChange(c.ID, req, "alice", true)
	if e != nil {
		t.Fatal(e)
	}
	got := s.GetChange(c.ID)
	if got.ID != c.ID || got.Status != ChangeReady || got.Reference != c.Reference || got.Summary != "verified explanation" || result["requires_ready_validation"] != false {
		t.Fatal(got, result)
	}
	var raw, detail string
	if e = s.db.QueryRow(`SELECT plan FROM delivery_runs WHERE id=?`, runID).Scan(&raw); e != nil || raw != frozen {
		t.Fatal("historical snapshot rewritten", e)
	}
	if e = s.db.QueryRow(`SELECT detail FROM audit_events WHERE action='maintain-change'`).Scan(&detail); e != nil {
		t.Fatal(e)
	}
	var audit map[string]any
	json.Unmarshal([]byte(detail), &audit)
	if audit["effect"] != "ready_description_revised" || audit["before_description"] == nil || audit["after_description"] == nil {
		t.Fatal(audit)
	}
	if _, e = s.maintainChange(c.ID, req, "alice", true); e == nil {
		t.Fatal("stale revision accepted")
	}
}
func TestReadyContentRevisionReturnsDraftAndRespectsFrozenScope(t *testing.T) {
	for _, scenario := range []string{"free", "active", "version", "ledger", "archived", "frozen-after-preview"} {
		t.Run(scenario, func(t *testing.T) {
			s, c := readyMaintenanceFixture(t)
			req := maintenancePatch(t, s, c, map[string]string{"reference": "feature/correct"})
			if scenario == "frozen-after-preview" {
				if _, e := s.maintainChange(c.ID, req, "alice", false); e != nil {
					t.Fatal(e)
				}
			}
			switch scenario {
			case "active", "frozen-after-preview":
				maintenanceRun(t, s, *c, "running")
			case "version":
				s.db.Exec(`INSERT INTO release_version_changes(release_version_id,change_id) VALUES(99,?)`, c.ID)
			case "ledger":
				s.db.Exec(`INSERT INTO sql_execution_ledger(connection,database_name,change_id,digest,status,run_id,node_id,detail) VALUES('db','fixture',?,'x','uncertain',1,'n','check')`, c.ID)
			case "archived":
				s.db.Exec(`UPDATE changes SET status='archived' WHERE id=?`, c.ID)
				req = maintenancePatch(t, s, s.GetChange(c.ID), map[string]string{"reference": "feature/correct"})
			}
			result, e := s.maintainChange(c.ID, req, "alice", true)
			if scenario != "free" {
				if e == nil {
					t.Fatal("protection bypassed", scenario)
				}
				if s.GetChange(c.ID).Reference != c.Reference {
					t.Fatal("partial save")
				}
				return
			}
			got := s.GetChange(c.ID)
			if e != nil || got.Status != ChangeDraft || got.Reference != "feature/correct" || result["effect"] != "returned_to_draft" || result["requires_ready_validation"] != true {
				t.Fatal(got, result, e)
			}
		})
	}
}
func TestReadyDescriptionActiveRunHasActionableConflict(t *testing.T) {
	s, c := readyMaintenanceFixture(t)
	req := maintenancePatch(t, s, c, map[string]string{"summary": "revised"})
	id, _ := maintenanceRun(t, s, *c, "running")
	state, e := s.maintenanceState(c.ID, c.Service, "alice")
	if e != nil {
		t.Fatal(e)
	}
	if len(state["maintenance"].(map[string]any)["blocking_runs"].([]changeMaintenanceBlocker)) != 1 {
		t.Fatal(state)
	}
	_, e = s.maintainChange(c.ID, req, "alice", true)
	if e == nil || !strings.Contains(e.Error(), "#/orchestration?run=") {
		t.Fatal(e)
	}
	s.db.Exec(`UPDATE delivery_runs SET status='canceled' WHERE id=?`, id)
	if _, e = s.maintainChange(c.ID, req, "alice", true); e != nil {
		t.Fatal("canceled parent still blocks", e)
	}
}
func TestMaintenanceDescriptionPatchCannotCarryExecutionChanges(t *testing.T) {
	s, c := readyMaintenanceFixture(t)
	for _, field := range []string{"reference", "risk_level", "rollback_plan", "sql", "delivery_ownership"} {
		req := maintenancePatch(t, s, c, map[string]string{"summary": "revised", field: "value"})
		if maintenanceDescriptionPatch(req) {
			t.Fatal("classified execution field as description", field)
		}
	}
}
