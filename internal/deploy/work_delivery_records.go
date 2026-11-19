package deploy

import (
	"fmt"
	"net/http"
	"strconv"
)

type WorkDeliveryArtifact struct {
	Service string `json:"service"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}
type WorkDeliveryRecord struct {
	Stage         string `json:"stage,omitempty"`
	NextAction    string `json:"next_action,omitempty"`
	BlockedReason string `json:"blocked_reason,omitempty"`
	UpdatedAt     int64  `json:"updated_at"`

	SQLPlans    []*SQLExecutionPlan    `json:"sql_plans,omitempty"`
	ChangeCount int                    `json:"change_count"`
	Loaded      bool                   `json:"loaded"`
	ID          int64                  `json:"id"`
	Status      string                 `json:"status"`
	Environment string                 `json:"environment"`
	Owner       string                 `json:"owner"`
	CreatedAt   int64                  `json:"created_at"`
	Reason      string                 `json:"reason"`
	Changes     []Change               `json:"changes,omitempty"`
	Artifacts   []WorkDeliveryArtifact `json:"artifacts,omitempty"`
	Steps       []DeliveryStep         `json:"steps,omitempty"`
	Preparation bool                   `json:"preparation"`
}

func workDeliveryRecord(run *DeliveryRun) WorkDeliveryRecord {
	r := WorkDeliveryRecord{ID: run.ID, Status: run.Status, Environment: run.Plan.Environment.DisplayName, Owner: run.Owner, CreatedAt: run.CreatedAt, Reason: run.Plan.Reason, Changes: []Change{}, Artifacts: []WorkDeliveryArtifact{}, Steps: run.Steps, SQLPlans: run.Plan.SQLPlans, Preparation: run.Plan.Template.Workbench == "research"}
	if r.Environment == "" {
		r.Environment = run.Plan.Environment.Name
	}
	if run.Plan.Scope != nil {
		r.Changes = append(r.Changes, run.Plan.Scope.Changes...)
	} else if run.Plan.Version != nil {
		r.Changes = append(r.Changes, run.Plan.Version.Changes...)
	}
	for i := range r.Changes {
		r.Changes[i].WorkItems = nil
	}
	for _, v := range run.Plan.Services {
		r.Artifacts = append(r.Artifacts, WorkDeliveryArtifact{Service: v.Service, Version: v.Artifact.Version, Digest: v.Artifact.Digest})
	}
	r.ChangeCount = len(r.Changes)
	r.Loaded = true
	return r
}

func (s *Store) currentWorkDeliveryRecord(run *DeliveryRun) WorkDeliveryRecord {
	summary := s.researchSummary(run)
	record := workDeliveryRecord(run)
	record.Stage, record.NextAction, record.BlockedReason, record.UpdatedAt = summary.Stage, summary.NextAction, summary.BlockedReason, summary.UpdatedAt
	return record
}

// Match frozen work-item associations, never today's editable change links.
func workDeliveryRecordClause(dialect string) string {
	if dialect == "mysql" {
		return `(JSON_CONTAINS(JSON_EXTRACT(plan,'$.scope.work_items'),JSON_OBJECT('id',?)) OR JSON_CONTAINS(JSON_EXTRACT(plan,'$.version.work_items'),JSON_OBJECT('id',?)) OR JSON_CONTAINS(JSON_EXTRACT(plan,'$.scope.changes[*].work_items[*].id'),CAST(? AS JSON)) OR JSON_CONTAINS(JSON_EXTRACT(plan,'$.version.changes[*].work_items[*].id'),CAST(? AS JSON)))`
	}
	return `(EXISTS(SELECT 1 FROM json_each(plan,'$.scope.work_items') WHERE json_extract(value,'$.id')=?) OR EXISTS(SELECT 1 FROM json_each(plan,'$.version.work_items') WHERE json_extract(value,'$.id')=?) OR EXISTS(SELECT 1 FROM json_each(plan,'$.scope.changes') c,json_each(c.value,'$.work_items') w WHERE json_extract(w.value,'$.id')=?) OR EXISTS(SELECT 1 FROM json_each(plan,'$.version.changes') c,json_each(c.value,'$.work_items') w WHERE json_extract(w.value,'$.id')=?))`
}
func (s *Store) WorkDeliveryRecords(id, before int64, actor string, admin bool) ([]WorkDeliveryRecord, bool, error) {
	out := []WorkDeliveryRecord{}
	// Batches bound memory even when the actor cannot see most candidate runs.
	for {
		clause := workDeliveryRecordClause(s.db.dialect)
		args := []any{id, id, id, id}
		if before > 0 {
			clause += " AND id<?"
			args = append(args, before)
		}
		rows, err := s.db.Query(deliveryRunSelect+" WHERE "+clause+" ORDER BY id DESC LIMIT 50", args...)
		if err != nil {
			return nil, false, err
		}
		runs := []*DeliveryRun{}
		for rows.Next() {
			v, e := scanDeliveryRun(rows)
			if e != nil {
				rows.Close()
				return nil, false, e
			}
			runs = append(runs, v)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, false, err
		}
		for _, v := range runs {
			if !admin && !researchRunVisible(v, actor) {
				continue
			}
			if len(out) == 20 {
				return out, true, nil
			}
			record := s.currentWorkDeliveryRecord(v)
			record.Changes = nil
			record.Artifacts = nil
			record.Steps = nil
			record.SQLPlans = nil
			record.Loaded = false
			out = append(out, record)
		}
		if len(runs) < 50 {
			return out, false, nil
		}
		before = runs[len(runs)-1].ID
	}
}
func workDeliveryIncludes(run *DeliveryRun, id int64) bool {
	has := func(items []WorkItem) bool {
		for _, w := range items {
			if w.ID == id {
				return true
			}
		}
		return false
	}
	changes := func(items []Change) bool {
		for _, c := range items {
			if has(c.WorkItems) {
				return true
			}
		}
		return false
	}
	if run.Plan.Scope != nil && (has(run.Plan.Scope.WorkItems) || changes(run.Plan.Scope.Changes)) {
		return true
	}
	return run.Plan.Version != nil && (has(run.Plan.Version.WorkItems) || changes(run.Plan.Version.Changes))
}
func registerWorkDeliveryRecordRoutes(mux *http.ServeMux, s *Store) {
	mux.HandleFunc("GET /api/research/work-items/{id}/delivery-records/{run}", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		runID, _ := strconv.ParseInt(r.PathValue("run"), 10, 64)
		run := s.DeliveryRun(runID)
		if id <= 0 || run == nil || !workDeliveryIncludes(run, id) || (r.Header.Get("X-Role") != "admin" && !researchRunVisible(run, deliveryActor(r))) {
			deliveryWriteError(w, 404, "交付记录不存在或不可访问")
			return
		}
		deliveryJSON(w, s.currentWorkDeliveryRecord(run))
	})
	mux.HandleFunc("GET /api/research/work-items/{id}/delivery-records", func(w http.ResponseWriter, r *http.Request) {
		id, e := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if e != nil || id <= 0 || s.GetWorkItem(id) == nil {
			deliveryWriteError(w, 404, "工作项不存在")
			return
		}
		before := int64(0)
		if raw := r.URL.Query().Get("before"); raw != "" {
			before, e = strconv.ParseInt(raw, 10, 64)
			if e != nil || before <= 0 {
				deliveryWriteError(w, 400, "记录游标无效")
				return
			}
		}
		items, more, e := s.WorkDeliveryRecords(id, before, deliveryActor(r), r.Header.Get("X-Role") == "admin")
		if e != nil {
			deliveryWriteError(w, 500, fmt.Errorf("读取交付记录失败：%w", e))
			return
		}
		next := int64(0)
		if len(items) > 0 {
			next = items[len(items)-1].ID
		}
		deliveryJSON(w, map[string]any{"items": items, "has_more": more, "next_before": next})
	})
}
