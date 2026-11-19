package deploy

import (
	"fmt"
	"testing"
)

func TestAttentionOnProjectedBlockedPreparationKeepsLedger(t *testing.T) {
	s, run, p := recoveryFlowFixture(t)
	p.State = "conflict"
	p.LeaseUntil = 0
	if err := s.saveCodePreparation(p); err != nil {
		t.Fatal(err)
	}
	raw := digestJSON(s.deliveryRunLedger(run.ID))
	item := attentionTestItem(t, s, run.Owner, fmt.Sprintf("delivery:%d", run.ID))
	state, err := s.setResearchAttention(run.Owner, item, "ignore", "暂不处理此合并冲突", "ignore-projected-conflict")
	if err != nil {
		t.Fatal(err)
	}
	if state.State != "ignored" || state.Document.Status != "blocked" {
		t.Fatalf("unexpected state: %+v", state)
	}
	if got := digestJSON(s.deliveryRunLedger(run.ID)); got != raw {
		t.Fatal("personal ignore changed original execution")
	}
	item.Revision = state.Revision
	if _, err = s.setResearchAttention(run.Owner, item, "restore", "继续跟进此合并冲突", "restore-projected-conflict"); err != nil {
		t.Fatal(err)
	}
	// New source activity must still invalidate the old observed event.
	p.State = "uncertain"
	if err = s.saveCodePreparation(p); err != nil {
		t.Fatal(err)
	}
	item.Revision++
	if _, err = s.setResearchAttention(run.Owner, item, "ignore", "不能忽略结果未知任务", "ignore-new-event"); err == nil {
		t.Fatal("stale event accepted")
	}
}
