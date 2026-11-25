package deploy

import (
	"encoding/json"
	"fmt"
)

func (s *Store) ownedWorkLoops(actor string) ([]WorkLoop, error) {
	out := []WorkLoop{}
	if actor == "" || actor == "local" {
		return out, nil
	}
	rows, e := s.db.Query(`SELECT value FROM deploy_meta WHERE key LIKE 'work_loop:%'`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var t WorkLoop
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		if e = json.Unmarshal([]byte(raw), &t); e != nil {
			return nil, e
		}
		if t.Owner == actor {
			t.raw = raw
			out = append(out, t)
		}
	}
	return out, rows.Err()
}
func (s *Store) workLoopResearchTask(t *WorkLoop) ResearchTask {
	title := "需求 / Bug 跟进"
	if w := s.GetWorkItem(t.Grant.Spec.WorkItemID); w != nil {
		title = w.Title
	}
	stages := map[string]string{"development": "开发与变更", "preparation": "代码与制品准备", "delivery": "交付与回归", "writeback": "来源回写", "acceptance": "人工验收"}
	stage := stages[t.Stage]
	if stage == "" {
		stage = t.Stage
	}
	task := ResearchTask{ID: "work-loop:" + t.ID, WorkLoopID: t.ID, SourceKind: "work-loop", Kind: "work-loop", WorkItemID: t.Grant.Spec.WorkItemID, Title: title, Owner: t.Owner, Status: t.State, Stage: stage, NextAction: t.Message, Environment: t.Grant.Spec.Environment, Services: t.Grant.Spec.Applications, Revision: t.Revision, UpdatedAt: t.UpdatedAt, At: t.UpdatedAt, ReadOnly: true, Assignees: []string{t.Owner}, Progress: fmt.Sprintf("第 %d 轮", len(t.Iterations))}
	if t.State == "blocked" {
		task.BlockedReason = t.Message
	}
	return task
}
func (s *Store) workLoopAttentionTarget(actor, id string) (attentionTarget, error) {
	t, e := s.workLoop(id)
	if e != nil {
		return attentionTarget{}, e
	}
	if t.Owner != actor || actor == "" || actor == "local" {
		return attentionTarget{}, fmt.Errorf("仅授权人可以维护自己的任务关注")
	}
	task := s.workLoopResearchTask(t)
	task.AttentionKey = task.ID
	// Polling and CAS revisions do not resurrect dismissed events. A new state
	// transition/iteration/authorization does, even if its eventual text is equal.
	task.AttentionEvent = digestJSON([]any{t.ID, t.State, t.Stage, t.Message, len(t.Events), len(t.Iterations), t.Grant.Digest})
	canIgnore := t.State == "blocked" && (s.workLoopMayIterate(t) || t.Stage == "development" && t.current().PreparationID == 0 && t.current().DeliveryID == 0)
	if t.Intent != nil && t.Intent.State == "dispatching" {
		canIgnore = false
	}
	return attentionTarget{Kind: "work-loop", Task: task, SourceDigest: t.raw, CanIgnore: canIgnore}, nil
}
func (s *Store) projectWorkLoopTasks(actor, view string, tasks []ResearchTask, tracking bool) ([]ResearchTask, error) {
	loops, e := s.ownedWorkLoops(actor)
	if e != nil {
		return nil, e
	}
	children := map[int64]bool{}
	for _, t := range loops {
		for _, it := range t.Iterations {
			if it.PreparationID > 0 {
				children[it.PreparationID] = true
			}
			if it.DeliveryID > 0 {
				children[it.DeliveryID] = true
			}
		}
	}
	out := []ResearchTask{}
	for _, task := range tasks {
		// Keep all mandatory reviews, manual checks and other people's tasks.
		child := task.SourceKind == "delivery" || task.SourceKind == ""
		if child && children[task.RunID] && task.Owner == actor && (tracking || task.Kind == "exception" || task.Kind == "code-conflict") {
			continue
		}
		out = append(out, task)
	}
	for i := range loops {
		if loops[i].Grant.Spec.DraftOnly {
			continue
		}
		task := s.workLoopResearchTask(&loops[i])
		terminal := attentionIsTerminal(task)
		if !tracking && (view == "done") != terminal {
			continue
		}
		if !tracking && view != "done" && loops[i].State == "running" {
			continue
		}
		out = append(out, task)
	}
	return out, nil
}
