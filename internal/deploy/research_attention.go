package deploy

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Personal disposition is independent of execution state. Rows persist per
// actor and meaningful source event; a restore never advances an executor.
type researchAttentionState struct {
	Actor     string       `json:"-"`
	ItemKey   string       `json:"id"`
	Event     string       `json:"event_digest"`
	State     string       `json:"state"`
	Revision  int64        `json:"revision"`
	Document  ResearchTask `json:"document"`
	Reason    string       `json:"reason"`
	RequestID string       `json:"-"`
	UpdatedAt int64        `json:"updated_at"`
}
type attentionTarget struct {
	Task           ResearchTask
	Kind           string
	ID             int64
	SourceRevision int64
	SourceStatus   string
	SourceDigest   string
	CodeEvents     []any
	CanIgnore      bool
	LegacyDigest   string
	LegacyClosed   bool
	LegacyReason   string
}

func releaseAttentionBase(v *ReleaseOrder) string {
	return digestJSON([]any{v.ID, v.Status, v.StartedAt, v.EndedAt, v.Succeeded, v.Failed})
}

// A rollback failure is a known terminal failure, not an unknown execution.
// Recording a personal disposition never changes this result or retries it.
func researchDeploySettled(d *Deploy) bool {
	if d == nil {
		return false
	}
	if d.AutoRecheck != nil && (d.AutoRecheck.State == "scheduled" || d.AutoRecheck.State == "checking" || d.AutoRecheck.LeaseUntil > time.Now().Unix()) {
		return false
	}
	switch d.Status {
	case "success", "failed", "canceled", "rolled_back", "rolled-back", "rollback-failed":
		return true
	}
	return false
}
func (s *Store) releaseAttentionEvent(v *ReleaseOrder) (string, bool) {
	events := []any{releaseAttentionBase(v)}
	settled := true
	for _, id := range v.DeployIDs {
		d := s.GetDeploy(id)
		if d == nil {
			settled = false
			events = append(events, []any{id, "missing"})
			continue
		}
		events = append(events, []any{id, d.Status, d.StartedAt, d.EndedAt})
		if !researchDeploySettled(d) {
			settled = false
		}
	}
	if v.Script != nil && v.Script.NeedsReconciliation {
		settled = false
	}
	return digestJSON(events), settled
}

func (s *Store) attentionTarget(actor, key string) (attentionTarget, error) {
	var out attentionTarget
	parts := strings.Split(key, ":")
	if len(parts) == 2 && parts[0] == "work-loop" {
		return s.workLoopAttentionTarget(actor, parts[1])
	}
	if len(parts) != 2 || (parts[0] != "delivery" && parts[0] != "release") {
		return out, fmt.Errorf("此任务不支持个人归档或忽略")
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || id <= 0 {
		return out, fmt.Errorf("任务编号无效")
	}
	out.Kind, out.ID = parts[0], id
	if out.Kind == "delivery" {
		v := s.deliveryRunLedger(id)
		if v == nil || v.Owner != actor || actor == "" || actor == "local" {
			return out, fmt.Errorf("仅发起人可以维护自己的关注记录")
		}
		out.SourceRevision, out.SourceStatus, out.SourceDigest = v.Revision, v.Status, v.Digest
		// CAS compares persisted state; display projections never rewrite the ledger.
		s.projectResearchCodeState(v)
		events := []any{}
		blockedUnknown := false
		for _, step := range v.Steps {
			events = append(events, []any{step.NodeID, step.Status, step.Detail, step.ReleaseID})
			if step.Status == "uncertain" || step.Status == "executing" || step.Status == "unknown" {
				blockedUnknown = true
			}
			if step.Kind == "code-prepare" || step.Kind == "code-verify" {
				if p := s.CodePreparation(step.Detail); p != nil {
					out.CodeEvents = append(out.CodeEvents, []any{p.ID, p.State, p.LeaseUntil > time.Now().Unix()})
					if p.State == "uncertain" || p.State == "merging" || p.State == "building" || p.LeaseUntil > time.Now().Unix() {
						blockedUnknown = true
					}
				}
			}
			if step.ReleaseID > 0 {
				if child := s.GetReleaseOrder(step.ReleaseID); child != nil {
					event, settled := s.releaseAttentionEvent(child)
					events = append(events, event)
					if !settled {
						blockedUnknown = true
					}
					if child.Status != "success" && child.Status != "failed" && child.Status != "canceled" && child.Status != "rejected" && child.Status != "rolled_back" {
						blockedUnknown = true
					}
				}
			}
		}
		out.Task = s.researchSummary(v)
		out.Task.AttentionEvent = digestJSON([]any{out.Kind, id, out.SourceRevision, v.Status, events, out.CodeEvents, out.Task.EventDigest})
		out.CanIgnore = (out.Task.Status == "failed" || out.Task.Status == "blocked") && !blockedUnknown
		if out.CanIgnore {
			var active int
			err = s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM sql_execution_ledger WHERE run_id=? AND status NOT IN ('success','failed')) + (SELECT COUNT(*) FROM nacos_execution_ledger WHERE run_id=? AND status NOT IN ('success','failed')) + (SELECT COUNT(*) FROM redis_execution_ledger WHERE run_id=? AND status NOT IN ('success','failed'))`, id, id, id).Scan(&active)
			if err != nil {
				return out, err
			}
			control := s.DeliveryControl(id)
			out.CanIgnore = active == 0 && control.State != "running" && control.State != "pausing" && control.State != "interrupted"
		}
	} else {
		v := s.GetReleaseOrder(id)
		if v == nil || v.Actor != actor || actor == "" || actor == "local" {
			return out, fmt.Errorf("仅发起人可以维护自己的关注记录")
		}
		out.SourceStatus = v.Status
		out.SourceDigest = releaseAttentionBase(v)
		out.Task = researchReleaseSummary(v)
		event, settled := s.releaseAttentionEvent(v)
		out.Task.AttentionEvent = event
		out.CanIgnore = v.Status == "failed" && settled
	}
	legacy := out.Task
	legacy.AttentionEvent = ""
	legacy.Kind = "exception"
	legacy.ReadOnly = true
	legacy.Title = "处理异常 · " + legacy.Title
	out.LegacyDigest = digestJSON(legacy)
	if legacy.Status == "failed" || out.Task.Kind == "exception" {
		var comment string
		err = s.db.QueryRow(`SELECT comment FROM research_exception_resolutions WHERE task_id=? AND actor=? AND digest=?`, key, actor, out.LegacyDigest).Scan(&comment)
		if err != nil && err != sql.ErrNoRows {
			return out, err
		}
		out.LegacyClosed = err == nil
		out.LegacyReason = comment
	}
	out.Task.AttentionKey = key
	return out, nil
}
func (s *Store) attentionStates(actor string) (map[string]researchAttentionState, error) {
	rows, err := s.db.Query(`SELECT actor,item_key,event_digest,state,revision,document,reason,request_id,updated_at FROM research_attention_states WHERE actor=?`, actor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]researchAttentionState{}
	for rows.Next() {
		var v researchAttentionState
		var raw string
		if err = rows.Scan(&v.Actor, &v.ItemKey, &v.Event, &v.State, &v.Revision, &raw, &v.Reason, &v.RequestID, &v.UpdatedAt); err != nil {
			return nil, err
		}
		if v.Actor != actor {
			continue
		}
		if json.Unmarshal([]byte(raw), &v.Document) != nil {
			return nil, fmt.Errorf("个人关注记录损坏")
		}
		out[v.ItemKey+"\x00"+v.Event] = v
	}
	return out, rows.Err()
}
func attentionIsTerminal(t ResearchTask) bool {
	return t.Status == "completed" || t.Status == "success" || t.Status == "canceled" || t.Status == "rejected" || t.Status == "rolled_back"
}
func applyAttention(t *ResearchTask, target attentionTarget, states map[string]researchAttentionState) {
	t.AttentionKey = target.Task.AttentionKey
	t.AttentionEvent = target.Task.AttentionEvent
	t.CanIgnore = target.CanIgnore
	t.CanArchive = true
	current, exists := states[t.AttentionKey+"\x00"+t.AttentionEvent]
	t.AttentionState = "active"
	if attentionIsTerminal(target.Task) {
		t.AttentionState = "archived"
		t.CanRestore = true
	}
	if target.LegacyClosed {
		t.AttentionState = "ignored"
		t.AttentionReason = target.LegacyReason
		t.CanRestore = true
	}
	if exists {
		t.AttentionRevision = current.Revision
		t.AttentionState = current.State
		t.AttentionReason = current.Reason
		t.AttentionAt = current.UpdatedAt
		t.CanRestore = current.State == "ignored" || current.State == "archived"
		if current.State == "restored" {
			t.AttentionState = "active"
		}
	}
	if t.AttentionState != "active" {
		t.CanArchive = false
		if t.AttentionState == "ignored" {
			t.CanIgnore = false
		}
	}
}
func (s *Store) attentionDeliveries(actor, view string) ([]ResearchTask, error) {
	if view == "" {
		view = "active"
	}
	if view != "active" && view != "archived" && view != "all" {
		return nil, fmt.Errorf("关注范围仅支持active/archived/all")
	}
	states, err := s.attentionStates(actor)
	if err != nil {
		return nil, err
	}
	owned, err := s.researchOwnedDeliveries(actor)
	if err != nil {
		return nil, err
	}
	owned, err = s.projectWorkLoopTasks(actor, view, owned, true)
	if err != nil {
		return nil, err
	}
	out := []ResearchTask{}
	for _, t := range owned {
		target, e := s.attentionTarget(actor, t.ID)
		if e != nil {
			return nil, e
		}
		applyAttention(&t, target, states)
		if view == "active" && t.AttentionState != "active" || view == "archived" && t.AttentionState == "active" {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

// Actionable tasks retain their source identity, including mandatory approvals.
// Only explicitly ignored current exception/code events are removed.
func (s *Store) attentionTasks(actor, view string, tasks []ResearchTask) ([]ResearchTask, error) {
	states, err := s.attentionStates(actor)
	if err != nil {
		return nil, err
	}
	out := []ResearchTask{}
	targets := map[string]attentionTarget{}
	for _, t := range tasks {
		key := t.SourceKind + ":" + fmt.Sprint(t.RunID)
		if t.WorkLoopID != "" {
			key = "work-loop:" + t.WorkLoopID
		}
		if t.SourceKind == "" {
			key = "delivery:" + fmt.Sprint(t.RunID)
		}
		if t.Owner == actor && (strings.HasPrefix(key, "delivery:") || strings.HasPrefix(key, "release:") || t.WorkLoopID != "") {
			target, ok := targets[key]
			if !ok {
				target, err = s.attentionTarget(actor, key)
				if err != nil {
					continue
				}
				targets[key] = target
			}
			applyAttention(&t, target, states)
			if view != "done" && (t.Kind == "exception" || t.Kind == "code-conflict" || t.Kind == "work-loop") && (t.AttentionState == "ignored" || t.Kind == "work-loop" && t.AttentionState == "archived") {
				continue
			}
			if t.Kind != "exception" && t.Kind != "code-conflict" && t.Kind != "work-loop" {
				t.CanIgnore = false
			}
			if t.ResolutionAction != "" {
				t.CanIgnore = false
				t.CanRestore = target.LegacyClosed && t.ResolutionDigest == target.LegacyDigest && (t.AttentionRevision == 0 || t.AttentionState == "ignored")
			}
		}
		out = append(out, t)
	}
	if view == "done" {
		for _, state := range states {
			if state.State != "ignored" {
				continue
			}
			t := state.Document
			t.ID = "attention:" + state.ItemKey + ":" + state.Event
			t.ResolutionAction = "ignore"
			t.ReadOnly = true
			t.AttentionState = "ignored"
			t.AttentionKey = state.ItemKey
			t.AttentionEvent = state.Event
			t.AttentionRevision = state.Revision
			t.AttentionReason = state.Reason
			t.AttentionAt = state.UpdatedAt
			t.CanArchive = false
			t.CanIgnore = false
			t.CanRestore = false
			if target, e := s.attentionTarget(actor, state.ItemKey); e == nil && target.Task.AttentionEvent == state.Event {
				t.CanRestore = true
			}
			t.At = state.UpdatedAt
			t.UpdatedAt = state.UpdatedAt
			t.NextAction = "忽略原因：" + state.Reason + "；原交付状态保留"
			out = append(out, t)
		}
	}
	return out, nil
}

type researchAttentionItem struct {
	ID       string `json:"id"`
	Event    string `json:"event_digest"`
	Revision int64  `json:"revision"`
}
type researchAttentionRequest struct {
	Action    string                  `json:"action"`
	Reason    string                  `json:"reason"`
	RequestID string                  `json:"request_id"`
	Items     []researchAttentionItem `json:"items"`
}

func (s *Store) setResearchAttention(actor string, item researchAttentionItem, action, reason, requestID string) (researchAttentionState, error) {
	var empty researchAttentionState
	if action != "ignore" && action != "archive" && action != "restore" {
		return empty, fmt.Errorf("不支持的个人处理操作")
	}
	target, err := s.attentionTarget(actor, item.ID)
	if err != nil {
		return empty, err
	}
	if target.Task.AttentionEvent != item.Event {
		return empty, fmt.Errorf("任务已产生新事件，请刷新后重新选择")
	}
	if action == "ignore" && !target.CanIgnore {
		return empty, fmt.Errorf("正在执行、结果未知或必须审核/确认的任务不可忽略")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	if target.Kind == "work-loop" {
		q := `SELECT value FROM deploy_meta WHERE key=?`
		if s.Driver() == "mysql" {
			q += " FOR UPDATE"
		}
		var current string
		if err = tx.QueryRow(q, "work_loop:"+target.Task.WorkLoopID).Scan(&current); err != nil || current != target.SourceDigest {
			return empty, fmt.Errorf("跟进任务已变化，请刷新")
		}
	}
	if target.Kind == "delivery" {
		var revision int64
		var status, digest string
		q := `SELECT revision,status,digest FROM delivery_runs WHERE id=?`
		if s.Driver() == "mysql" {
			q += " FOR UPDATE"
		}
		if err = tx.QueryRow(q, target.ID).Scan(&revision, &status, &digest); err != nil || revision != target.SourceRevision || status != target.SourceStatus || digest != target.SourceDigest {
			return empty, fmt.Errorf("原执行单已变化，请刷新")
		}
	}
	if target.Kind == "release" {
		q := releaseSelect + ` WHERE id=?`
		if s.Driver() == "mysql" {
			q += " FOR UPDATE"
		}
		current, e := scanRelease(tx.QueryRow(q, target.ID))
		if e != nil || releaseAttentionBase(&current) != target.SourceDigest {
			return empty, fmt.Errorf("原发布单已变化，请刷新")
		}
	}
	var old researchAttentionState
	var raw string
	err = tx.QueryRow(`SELECT state,revision,document,reason,request_id,updated_at FROM research_attention_states WHERE actor=? AND item_key=? AND event_digest=?`, actor, item.ID, item.Event).Scan(&old.State, &old.Revision, &raw, &old.Reason, &old.RequestID, &old.UpdatedAt)
	if err != nil && err != sql.ErrNoRows {
		return empty, err
	}
	if err == nil && old.RequestID == requestID && old.State == map[string]string{"ignore": "ignored", "archive": "archived", "restore": "restored"}[action] {
		json.Unmarshal([]byte(raw), &old.Document)
		old.Actor, old.ItemKey, old.Event = actor, item.ID, item.Event
		return old, nil
	}
	if action == "archive" && (old.State == "ignored" || target.LegacyClosed && old.State == "") {
		return empty, fmt.Errorf("此事件已经忽略并归档，请使用恢复入口")
	}
	if old.Revision != item.Revision {
		return empty, fmt.Errorf("个人处理记录已变化，请刷新")
	}
	if action == "restore" && (old.State != "ignored" && old.State != "archived") && !target.LegacyClosed && !attentionIsTerminal(target.Task) {
		return empty, fmt.Errorf("此事件没有可恢复的个人记录")
	}
	next := researchAttentionState{Actor: actor, ItemKey: item.ID, Event: item.Event, State: map[string]string{"ignore": "ignored", "archive": "archived", "restore": "restored"}[action], Revision: old.Revision + 1, Document: target.Task, Reason: reason, RequestID: requestID, UpdatedAt: time.Now().Unix()}
	doc, _ := json.Marshal(next.Document)
	if old.Revision == 0 {
		_, err = tx.Exec(`INSERT INTO research_attention_states(actor,item_key,event_digest,state,revision,document,reason,request_id,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, actor, item.ID, item.Event, next.State, next.Revision, string(doc), reason, requestID, next.UpdatedAt)
	} else {
		var result sql.Result
		result, err = tx.Exec(`UPDATE research_attention_states SET state=?,revision=?,document=?,reason=?,request_id=?,updated_at=? WHERE actor=? AND item_key=? AND event_digest=? AND revision=?`, next.State, next.Revision, string(doc), reason, requestID, next.UpdatedAt, actor, item.ID, item.Event, item.Revision)
		if err == nil {
			if n, _ := result.RowsAffected(); n != 1 {
				err = fmt.Errorf("个人处理记录已变化")
			}
		}
	}
	if err != nil {
		return empty, err
	}
	detail, _ := json.Marshal(map[string]any{"item": item.ID, "event": item.Event, "revision": next.Revision, "reason": reason, "execution_state_unchanged": true})
	if _, err = tx.Exec(`INSERT INTO audit_events(actor,action,resource_type,resource_id,detail,created_at) VALUES(?,?,?,?,?,?)`, actor, "attention-"+action, "research-attention", item.ID, string(detail), next.UpdatedAt); err != nil {
		return empty, err
	}
	if err = tx.Commit(); err != nil {
		return empty, err
	}
	return next, nil
}
func registerResearchAttention(mux *http.ServeMux, s *Store) {
	mux.HandleFunc("POST /api/research/attention", func(w http.ResponseWriter, r *http.Request) {
		var req researchAttentionRequest
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		d.DisallowUnknownFields()
		if d.Decode(&req) != nil || len(req.Items) < 1 || len(req.Items) > 100 || utf8.RuneCountInString(strings.TrimSpace(req.Reason)) < 6 || len(req.Reason) > 2000 || len(req.RequestID) < 8 || len(req.RequestID) > 80 || (req.Action != "ignore" && req.Action != "archive" && req.Action != "restore") {
			deliveryWriteError(w, 400, "请选择1–100条记录、有效操作，填写至少6字的原因和请求编号")
			return
		}
		actor := deliveryActor(r)
		if actor == "local" || actor == "" {
			deliveryWriteError(w, 403, "请登录本人账号")
			return
		}
		results := []any{}
		seen := map[string]bool{}
		for _, item := range req.Items {
			key := item.ID + "\x00" + item.Event
			if seen[key] {
				continue
			}
			seen[key] = true
			state, err := s.setResearchAttention(actor, item, req.Action, strings.TrimSpace(req.Reason), req.RequestID)
			row := map[string]any{"id": item.ID, "event_digest": item.Event, "success": err == nil}
			if err != nil {
				row["error"] = err.Error()
			} else {
				row["revision"] = state.Revision
				row["state"] = state.State
			}
			results = append(results, row)
		}
		deliveryJSON(w, map[string]any{"results": results, "execution_state_unchanged": true})
	})
}
