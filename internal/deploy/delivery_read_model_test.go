package deploy

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDeliveryReadModelsAgreeWithoutWriting(t *testing.T) {
	s, run, p := recoveryFlowFixture(t)
	for _, state := range []string{"conflict", "merging", "building", "uncertain", "failed"} {
		p.State = state
		p.LeaseUntil = 0
		expected := "blocked"
		if state == "merging" || state == "building" {
			p.LeaseUntil = time.Now().Add(time.Minute).Unix()
			expected = "running"
		}
		if err := s.saveCodePreparation(p); err != nil {
			t.Fatal(err)
		}
		detail := s.DeliveryRun(run.ID)
		if detail.Status != expected {
			t.Fatalf("%s detail: %s", state, detail.Status)
		}
		all := s.DeliveryRuns()
		if len(all) != 1 || all[0].Status != expected {
			t.Fatalf("list differs: %+v", all)
		}
		page, err := s.DeliveryRunPage("", expected, "dev", "", "alice", 0, 0, 1, 20)
		if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].Status != expected || page.Items[0].Applications != 1 {
			t.Fatalf("page differs: %+v %v", page, err)
		}
		other := "running"
		if expected == other {
			other = "blocked"
		}
		filtered, err := s.DeliveryRunPage("", other, "dev", "", "alice", 0, 0, 1, 20)
		if err != nil || filtered.Total != 0 {
			t.Fatalf("status filter raw instead of projected: %+v %v", filtered, err)
		}
		workID := run.Plan.Scope.WorkItems[0].ID
		tracking, err := s.researchWorkTracking(workID)
		if err != nil {
			t.Fatal(err)
		}
		tracked := tracking["deliveries"].([]ResearchTask)
		if len(tracked) != 1 || tracked[0].Status != expected || !tracking["preparations"].(map[int64]bool)[run.ID] {
			t.Fatalf("tracking differs: %+v", tracking)
		}
		records, _, err := s.WorkDeliveryRecords(workID, 0, "alice", false)
		if err != nil || len(records) != 1 || records[0].Status != expected {
			t.Fatalf("records differ: %+v %v", records, err)
		}
		raw, err := scanDeliveryRun(s.db.QueryRow(deliveryRunSelect+` WHERE id=?`, run.ID))
		if err != nil || raw.Status != "running" || raw.Revision != run.Revision || raw.Digest != run.Digest {
			t.Fatal("read changed frozen ledger", raw, err)
		}
	}
}

func TestDeliveryReadCompletedAndCanceledPreserveLedger(t *testing.T) {
	s, run, p := recoveryFlowFixture(t)
	for i := range run.Steps {
		run.Steps[i].Status = "success"
	}
	run.Steps[1].Status = "skipped"
	if err := s.saveDeliveryRun(run); err != nil {
		t.Fatal(err)
	}
	if got := s.DeliveryRun(run.ID); got.Status != "success" {
		t.Fatal("completed legacy ledger remains running", got)
	}
	page, err := s.DeliveryRunPage("", "success", "dev", "", "alice", 0, 0, 1, 20)
	if err != nil || page.Total != 1 || page.Items[0].Completed != 2 {
		t.Fatal(page, err)
	}
	raw, _ := scanDeliveryRun(s.db.QueryRow(deliveryRunSelect+` WHERE id=?`, run.ID))
	if raw.Status != "running" || raw.Revision != run.Revision {
		t.Fatal("query wrote completion", raw)
	}
	p.State = "canceled"
	if err = s.saveCodePreparation(p); err != nil {
		t.Fatal(err)
	}
	run.Status = "failed"
	run.Steps[0].Status = "blocked"
	if err = s.saveDeliveryRun(run); err != nil {
		t.Fatal(err)
	}
	got := s.DeliveryRun(run.ID)
	if got.Status != "failed" || got.Steps[0].Status != "canceled" {
		t.Fatal("terminal stop lost", got)
	}
	raw, _ = scanDeliveryRun(s.db.QueryRow(deliveryRunSelect+` WHERE id=?`, run.ID))
	if raw.Status != "failed" || raw.Steps[0].Status != "blocked" || raw.Revision != run.Revision {
		t.Fatal("terminal projection wrote ledger", raw)
	}
}

func TestDeliveryProjectedStatusPaginationAcrossBatches(t *testing.T) {
	s, run, _ := recoveryFlowFixture(t)
	for i := range run.Steps {
		run.Steps[i].Status = "success"
	}
	plan, _ := json.Marshal(run.Plan)
	execution, _ := json.Marshal(run.Steps)
	for i := 0; i < 105; i++ {
		if _, err := s.db.Exec(`INSERT INTO delivery_runs(plan,digest,status,execution,env_secrets,owner,approver,created_at,revision) VALUES(?,'fixture','running',?,'{}','alice','',1,1)`, string(plan), string(execution)); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.DeliveryRunPage("", "success", "dev", "", "alice", 0, 0, 2, 20)
	if err != nil || page.Total != 105 || len(page.Items) != 20 {
		t.Fatal("projected pagination truncated", page, err)
	}
	for _, r := range page.Items {
		if r.Status != "success" {
			t.Fatal("raw state leaked", r)
		}
	}
}

func TestBlockedPreparationCannotClaimDeploymentCoordinator(t *testing.T) {
	s, run, _ := recoveryFlowFixture(t)
	if _, err := s.claimDeliveryControl(run.ID, "alice"); err == nil {
		t.Fatal("blocked preparation bypassed coordinator status guard")
	}
}
