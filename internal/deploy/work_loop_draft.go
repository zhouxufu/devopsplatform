package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

type DraftTrackingItem struct {
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	Kind    string `json:"kind"`
	Created bool   `json:"created"`
}

type DraftTrackingReceipt struct {
	SavedAt   int64               `json:"saved_at,omitempty"`
	Items     []DraftTrackingItem `json:"items,omitempty"`
	Revision  int64               `json:"request_revision"`
	Digest    string              `json:"request_digest"`
	ChangeIDs []int64             `json:"change_ids"`
}

func (s *Store) previewDraftTracking(ctx context.Context, spec WorkLoopSpec, actor string) (*WorkLoopGrant, error) {
	now := time.Now()
	if spec.ExpiresAt <= now.Unix() || spec.ExpiresAt > now.Add(7*24*time.Hour).Unix() {
		return nil, fmt.Errorf("草稿登记有效期为未来 7 天以内")
	}
	if spec.Environment != "" || spec.CompleteScope || spec.Acceptance != "" || spec.Resolution != "" || spec.AssignedTo != "" || spec.ResolvedBuild != "" || spec.MaxIterations > 1 || spec.MaxIterations < 0 {
		return nil, fmt.Errorf("草稿授权仅指定工作项、应用与期限")
	}
	spec.MaxIterations = 1
	w := s.GetWorkItem(spec.WorkItemID)
	if w == nil || (w.Kind != "bug" && w.Kind != "requirement") {
		return nil, fmt.Errorf("请选择需求或 Bug")
	}
	if err := s.canReadResearchItem(ctx, actor, w); err != nil {
		return nil, err
	}
	if len(spec.Applications) == 0 || len(spec.Applications) > 10 {
		return nil, fmt.Errorf("请选择 1–10 个应用")
	}
	slices.Sort(spec.Applications)
	spec.Applications = slices.Compact(spec.Applications)
	for _, app := range spec.Applications {
		if s.GetService(app) == nil {
			return nil, fmt.Errorf("应用 %s 不存在", app)
		}
	}
	grant := &WorkLoopGrant{Spec: spec, Source: map[string]any{"kind": w.Kind, "action": "draft_only", "label": "仅登记本人草稿"}}
	grant.Digest = digestJSON(grant)
	return grant, nil
}

// Empty lists clear these fields, while omitted lists preserve the existing draft.
// Change's public JSON uses omitempty, so retain the distinction in the replay key.
func draftSaveDigest(content *WorkDeliverySave) string {
	var cleared []string
	for i, c := range content.Changes {
		if c.Prerequisites != nil && len(c.Prerequisites) == 0 {
			cleared = append(cleared, fmt.Sprintf("changes[%d].prerequisites", i))
		}
		if c.SQL != nil && c.SQL.EnvironmentOverrides != nil && len(c.SQL.EnvironmentOverrides) == 0 {
			cleared = append(cleared, fmt.Sprintf("changes[%d].sql.environment_overrides", i))
		}
	}
	if len(cleared) == 0 {
		return digestJSON(content)
	}
	return digestJSON(struct {
		Content *WorkDeliverySave
		Cleared []string
	}{content, cleared})
}

func (s *Store) saveTrackedDrafts(t *WorkLoop, revision int64, content *WorkDeliverySave) error {
	digest := draftSaveDigest(content)
	if receipt := t.DraftReceipt; receipt != nil && receipt.Revision == revision && receipt.Digest == digest {
		return nil
	}
	if t.Revision != revision || t.Stage != "development" {
		return ErrRunConflict
	}
	if content.MarkReady || len(content.AlsoWorkItemIDs) > 0 {
		return fmt.Errorf("草稿授权只允许维护当前工作项内的草稿")
	}
	raw, err := json.Marshal(content.Changes)
	if err != nil {
		return err
	}
	if bytes.Contains(raw, []byte("[REDACTED]")) {
		return fmt.Errorf("提交内容含脱敏占位，请核对实际改动后再保存")
	}
	for _, c := range content.Changes {
		if !slices.Contains(t.Grant.Spec.Applications, c.Service) || c.DeliveryOwnership != nil || (c.Nacos != nil && c.Nacos.Restore != nil) || c.Kind == "data-sync" || (c.SQL != nil && c.SQL.DataSync != nil) {
			return fmt.Errorf("变更应用、归属或同步计划超出草稿授权范围")
		}
	}
	next := *t
	next.DraftReceipt = &DraftTrackingReceipt{Revision: revision, Digest: digest}
	content.tracking = &next
	if err := s.SaveWorkDelivery(t.Grant.Spec.WorkItemID, content, t.Owner, false); err != nil {
		return err
	}
	*t = next
	return nil
}

func checkTrackedDraftEdit(tx *deployTx, old Change, next *Change, workItem int64) error {
	if old.Service != next.Service || old.Kind != next.Kind {
		return fmt.Errorf("草稿修订不能更换应用或类型")
	}
	if old.Nacos != nil && old.Nacos.Restore != nil {
		return fmt.Errorf("配置恢复记录需单独核对")
	}
	if old.SQL != nil && next.SQL != nil && next.SQL.EnvironmentOverrides == nil {
		next.SQL.EnvironmentOverrides = old.SQL.EnvironmentOverrides
	}
	if next.Prerequisites == nil {
		next.Prerequisites = old.Prerequisites
	}
	var linked int
	if err := tx.QueryRow(`SELECT (SELECT COUNT(*) FROM work_item_changes WHERE change_id=? AND work_item_id<>?) + (SELECT COUNT(*) FROM redis_execution_ledger WHERE change_id=?) + (SELECT COUNT(*) FROM release_version_changes WHERE change_id=?)`, old.ID, workItem, old.ID, old.ID).Scan(&linked); err != nil {
		return err
	}
	if linked != 0 {
		return fmt.Errorf("草稿已共享、执行或纳入版本，请单独核对")
	}
	blocks, err := maintenanceActiveRuns(tx, old.ID)
	if err != nil {
		return err
	}
	if len(blocks) > 0 {
		return fmt.Errorf("变更已冻结在执行单 #%d", blocks[0].RunID)
	}
	return nil
}

// Store generated IDs and advance the grant in the same transaction as its changes.
func commitDraftTracking(tx *deployTx, t *WorkLoop, changes []Change) error {
	if t.Grant.Spec.ExpiresAt <= time.Now().Unix() {
		return fmt.Errorf("草稿授权已到期")
	}
	for _, c := range changes {
		t.DraftReceipt.ChangeIDs = append(t.DraftReceipt.ChangeIDs, c.ID)
	}
	t.DraftReceipt.SavedAt = time.Now().Unix()
	t.Revision++
	t.UpdatedAt = time.Now().Unix()
	raw, err := json.Marshal(t)
	if err != nil {
		return err
	}
	r, err := tx.Exec(`UPDATE deploy_meta SET value=? WHERE key=? AND value=?`, string(raw), "work_loop:"+t.ID, t.raw)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrRunConflict
	}
	t.raw = string(raw)
	return nil
}
