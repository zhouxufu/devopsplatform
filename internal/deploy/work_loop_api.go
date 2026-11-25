package deploy

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Internal routing keeps the original preparation, CAS and permission checks.
// Only fixed server-selected paths are accepted; callers cannot supply a path.
type loopResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (r *loopResponse) Header() http.Header { return r.header }
func (r *loopResponse) WriteHeader(n int)   { r.status = n }
func (r *loopResponse) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = 200
	}
	return r.body.Write(b)
}
func loopCall(mux *http.ServeMux, parent *http.Request, path string, body any, out any) error {
	b, e := json.Marshal(body)
	if e != nil {
		return e
	}
	method := "POST"
	if strings.HasSuffix(path, "/delivery-content") {
		method = "PUT"
	}
	r, e := http.NewRequestWithContext(parent.Context(), method, path, bytes.NewReader(b))
	if e != nil {
		return e
	}
	r.Header = parent.Header.Clone()
	r.Header.Set("Content-Type", "application/json")
	w := &loopResponse{header: make(http.Header)}
	mux.ServeHTTP(w, r)
	if w.status >= 300 {
		var v struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(w.body.Bytes(), &v)
		if v.Error == "" {
			v.Error = "服务返回错误"
		}
		return fmt.Errorf("%s", v.Error)
	}
	if out != nil {
		return json.Unmarshal(w.body.Bytes(), out)
	}
	return nil
}
func (s *Store) dispatchWorkLoop(mux *http.ServeMux, r *http.Request, t *WorkLoop, name, path string, body any, out any) error {
	if t.Intent != nil && t.Intent.State == "dispatching" {
		return fmt.Errorf("上次操作结果待核对，不能重复派发")
	}
	b, e := json.Marshal(body)
	if e != nil {
		return e
	}
	t.Intent = &WorkLoopIntent{Name: name, Path: path, Body: b, State: "dispatching"}
	if e = s.saveWorkLoop(t); e != nil {
		return e
	}
	var raw json.RawMessage
	e = loopCall(mux, r, path, body, &raw)
	if e != nil {
		t.setState("blocked", t.Stage, "操作结果需核对原记录："+e.Error())
		_ = s.saveWorkLoop(t)
		return e
	}
	t.Intent.State, t.Intent.Result = "received", raw
	if e = s.saveWorkLoop(t); e != nil {
		return e
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}
func registerWorkLoopRoutes(mux *http.ServeMux, s *Store, pr *PipelineRunner, m *ReleaseManager) {
	registerWorkLoopAuthorizationRoutes(mux, s)
	registerWorkLoopBackgroundStatus(mux, s)
	decode := func(w http.ResponseWriter, r *http.Request, v any) bool {
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
		d.DisallowUnknownFields()
		if d.Decode(v) != nil {
			deliveryWriteError(w, 400, "任务请求格式无效")
			return false
		}
		return true
	}
	owned := func(w http.ResponseWriter, r *http.Request) *WorkLoop {
		t, e := s.workLoop(r.PathValue("id"))
		if e != nil {
			deliveryWriteError(w, 404, e)
			return nil
		}
		if t.Owner != deliveryActor(r) {
			deliveryWriteError(w, 403, "跟进任务仅对授权人可见")
			return nil
		}
		return t
	}
	mux.HandleFunc("GET /api/work-loops", func(w http.ResponseWriter, r *http.Request) {
		id, e := strconv.ParseInt(r.URL.Query().Get("work_item_id"), 10, 64)
		if r.URL.Query().Get("work_item_id") != "" && (e != nil || id <= 0) {
			deliveryWriteError(w, 400, "请选择工作项")
			return
		}
		rows, e := s.db.Query(`SELECT value FROM deploy_meta WHERE key LIKE 'work_loop:%'`)
		if e != nil {
			deliveryWriteError(w, 500, e)
			return
		}
		defer rows.Close()
		out := []WorkLoop{}
		for rows.Next() {
			var raw string
			var t WorkLoop
			if rows.Scan(&raw) == nil && json.Unmarshal([]byte(raw), &t) == nil && t.Owner == deliveryActor(r) && (id == 0 || t.Grant.Spec.WorkItemID == id) {
				out = append(out, t)
			}
		}
		slices.SortFunc(out, func(a, b WorkLoop) int {
			if a.UpdatedAt > b.UpdatedAt {
				return -1
			}
			if a.UpdatedAt < b.UpdatedAt {
				return 1
			}
			return strings.Compare(a.ID, b.ID)
		})
		deliveryJSON(w, out)
	})
	mux.HandleFunc("POST /api/work-loops/preview", func(w http.ResponseWriter, r *http.Request) {
		var spec WorkLoopSpec
		if !decode(w, r, &spec) {
			return
		}
		g, e := s.previewWorkLoop(r.Context(), spec, deliveryActor(r))
		if e != nil {
			deliveryWriteError(w, 409, e)
			return
		}
		deliveryJSON(w, g)
	})
	mux.HandleFunc("POST /api/work-loops", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID             string       `json:"id"`
			Spec           WorkLoopSpec `json:"spec"`
			ExpectedDigest string       `json:"expected_digest"`
		}
		if !decode(w, r, &req) {
			return
		}
		if !attachmentIDRE.MatchString(req.ID) {
			deliveryWriteError(w, 400, "请提供稳定的任务请求编号")
			return
		}
		actor := deliveryActor(r)
		if old, e := s.workLoop(req.ID); e == nil {
			if old.Owner != actor || old.Grant.Digest != req.ExpectedDigest || digestJSON(old.Grant.Spec) != digestJSON(req.Spec) {
				deliveryWriteError(w, 409, "请求编号已用于其他任务")
				return
			}
			deliveryJSON(w, old)
			return
		}
		g, e := s.previewWorkLoop(r.Context(), req.Spec, actor)
		if e != nil {
			deliveryWriteError(w, 409, e)
			return
		}
		if g.Digest != req.ExpectedDigest {
			deliveryWriteError(w, 409, "授权预览已变化，请核对后重新启动")
			return
		}
		t := &WorkLoop{Authority: r.Header.Get("X-Work-Loop-Authority"), AuthorityKeyID: r.Header.Get("X-Work-Loop-Key-ID"), ID: req.ID, Owner: actor, Grant: *g, Authorizations: []WorkLoopGrant{*g}, Iterations: []WorkLoopIteration{{Number: 1, GrantDigest: g.Digest, ChangeIDs: []int64{}, State: "developing"}}, Events: []WorkLoopEvent{}}
		if g.Spec.DraftOnly {
			t.setState("developing", "development", "已授权在开发过程中登记本人变更草稿")
		} else {
			t.setState("developing", "development", "等待人员或 Agent 接手开发；未启动编码进程")
		}
		if e = s.saveWorkLoop(t); e != nil {
			deliveryWriteError(w, 409, e)
			return
		}
		s.Audit(actor, "authorize-work-loop", "work-loop", t.ID, g)
		deliveryJSON(w, t)
	})
	mux.HandleFunc("GET /api/work-loops/{id}", func(w http.ResponseWriter, r *http.Request) {
		if t := owned(w, r); t != nil {
			deliveryJSON(w, t)
		}
	})
	mux.HandleFunc("POST /api/work-loops/{id}/control", func(w http.ResponseWriter, r *http.Request) {
		t := owned(w, r)
		if t == nil {
			return
		}
		var req struct {
			Revision int64  `json:"revision"`
			Action   string `json:"action"`
			Claimant string `json:"claimant"`
		}
		if !decode(w, r, &req) {
			return
		}
		if req.Revision != t.Revision {
			deliveryWriteError(w, 409, "任务已更新，请读取当前状态")
			return
		}
		if t.Grant.Spec.DraftOnly && req.Action != "pause" && req.Action != "revoke" && req.Action != "resume" {
			deliveryWriteError(w, 409, "草稿登记只支持暂停、恢复或撤销")
			return
		}
		switch req.Action {
		case "pause":
			t.Paused = true
			t.setState("paused", t.Stage, "已暂停新步骤派发；在途操作继续按原执行记录核对")
		case "revoke":
			t.Paused = true
			t.Revoked = true
			t.setState("canceled", t.Stage, "已撤销任务授权；不撤销已发生的执行")
		case "resume":
			t.Paused = false
			if e := s.validateWorkLoop(r.Context(), t, t.Owner); e != nil {
				deliveryWriteError(w, 409, e)
				return
			}
			if t.Grant.Spec.DraftOnly {
				t.setState("developing", "development", "已恢复登记本人变更草稿")
			} else {
				t.setState("running", t.Stage, "按原任务核对并继续，未知操作不重放")
			}
		case "claim":
			if t.Stage != "development" || len(req.Claimant) > 100 || strings.TrimSpace(req.Claimant) == "" {
				deliveryWriteError(w, 409, "仅开发阶段可以由本人接手")
				return
			}
			t.Claimant = req.Claimant
		case "next_iteration":
			if !s.workLoopMayIterate(t) || len(t.Iterations) >= t.Grant.Spec.MaxIterations || t.Intent != nil && t.Intent.State == "dispatching" {
				deliveryWriteError(w, 409, "当前轮次尚未收敛或已达到授权轮次预算")
				return
			}
			if e := s.validateWorkLoop(r.Context(), t, t.Owner); e != nil {
				deliveryWriteError(w, 409, e)
				return
			}
			t.current().State = t.State
			t.Iterations = append(t.Iterations, WorkLoopIteration{Number: len(t.Iterations) + 1, GrantDigest: t.Grant.Digest, ChangeIDs: []int64{}, State: "developing"})
			t.Intent = nil
			t.setState("developing", "development", "新轮次开发；原交付及测试快照保留")
		default:
			deliveryWriteError(w, 400, "任务动作无效")
			return
		}
		if e := s.saveWorkLoop(t); e != nil {
			deliveryWriteError(w, 409, e)
			return
		}
		s.Audit(t.Owner, "work-loop-control", "work-loop", t.ID, map[string]any{"action": req.Action, "revision": req.Revision})
		deliveryJSON(w, t)
	})
	mux.HandleFunc("POST /api/work-loops/{id}/changes", func(w http.ResponseWriter, r *http.Request) {
		t := owned(w, r)
		if t == nil {
			return
		}
		var req struct {
			Revision int64            `json:"revision"`
			Content  WorkDeliverySave `json:"content"`
		}
		if !decode(w, r, &req) {
			return
		}
		if t.Grant.Spec.DraftOnly {
			if t.Authority == "mcp" && r.Header.Get("X-Work-Loop-Authority") == "mcp" && t.AuthorityKeyID != r.Header.Get("X-Work-Loop-Key-ID") {
				deliveryWriteError(w, 403, "请使用原授权 Key")
				return
			}
			if err := s.validateWorkLoop(r.Context(), t, t.Owner); err != nil {
				deliveryWriteError(w, 403, err)
				return
			}
			if err := s.saveTrackedDrafts(t, req.Revision, &req.Content); err != nil {
				deliveryWriteError(w, 409, err)
				return
			}
			deliveryJSON(w, t)
			return
		}
		if req.Revision != t.Revision || t.Stage != "development" {
			deliveryWriteError(w, 409, "当前轮次已冻结或修订变化")
			return
		}
		if e := s.validateWorkLoop(r.Context(), t, t.Owner); e != nil {
			deliveryWriteError(w, 403, e)
			return
		}
		if len(req.Content.AlsoWorkItemIDs) > 0 {
			deliveryWriteError(w, 403, "任务不能扩大工作项关联")
			return
		}
		for _, c := range req.Content.Changes {
			if !slices.Contains(t.Grant.Spec.Applications, c.Service) {
				deliveryWriteError(w, 403, "变更应用超出任务授权")
				return
			}
		}
		// The receipt and changes commit in the original transaction.
		if e := s.saveWorkLoopChanges(t, &req.Content); e != nil {
			deliveryWriteError(w, 409, e)
			return
		}
		deliveryJSON(w, t)
	})
	mux.HandleFunc("POST /api/work-loops/{id}/start", func(w http.ResponseWriter, r *http.Request) {
		t := owned(w, r)
		if t == nil {
			return
		}
		if t.Grant.Spec.DraftOnly {
			deliveryWriteError(w, 403, "此授权仅可登记草稿")
			return
		}
		var req struct {
			Revision  int64   `json:"revision"`
			ChangeIDs []int64 `json:"change_ids"`
			Summary   string  `json:"summary"`
		}
		if !decode(w, r, &req) {
			return
		}
		if t.Revision != req.Revision || t.Stage != "development" || strings.TrimSpace(req.Summary) == "" || len(req.Summary) > 4000 {
			deliveryWriteError(w, 409, "请在开发阶段提交当前修订、变更及验证说明")
			return
		}
		if e := s.validateWorkLoop(r.Context(), t, t.Owner); e != nil {
			deliveryWriteError(w, 403, e)
			return
		}
		changes, e := s.workLoopChanges(t, req.ChangeIDs)
		if e != nil {
			deliveryWriteError(w, 409, e)
			return
		}
		if e = s.workLoopCompleteScope(t, req.ChangeIDs); e != nil {
			deliveryWriteError(w, 409, e)
			return
		}
		t.current().ChangeIDs = req.ChangeIDs
		t.current().ChangesDigest = digestJSON(changes)
		t.current().Summary = req.Summary
		t.Intent = nil
		t.setState("running", "preparation", "正在准备本轮冻结范围")
		if e = s.saveWorkLoop(t); e != nil {
			deliveryWriteError(w, 409, e)
			return
		}
		deliveryJSON(w, t)
	})
	mux.HandleFunc("POST /api/work-loops/{id}/advance", func(w http.ResponseWriter, r *http.Request) {
		t := owned(w, r)
		if t == nil {
			return
		}
		if t.Grant.Spec.DraftOnly {
			deliveryWriteError(w, 403, "此授权仅可登记草稿")
			return
		}
		var req struct {
			Revision int64 `json:"revision"`
		}
		if !decode(w, r, &req) {
			return
		}
		if req.Revision != t.Revision {
			deliveryWriteError(w, 409, "任务已更新，请读取当前状态")
			return
		}
		if t.Stage != "acceptance" {
			if e := s.validateWorkLoop(r.Context(), t, t.Owner); e != nil {
				if t.Revoked || t.Paused {
					deliveryWriteError(w, 403, e)
					return
				}
				t.setState("blocked", t.Stage, e.Error())
				if saveErr := s.saveWorkLoop(t); saveErr != nil {
					deliveryWriteError(w, 409, saveErr)
					return
				}
				deliveryWriteError(w, 403, e)
				return
			}
		}
		if e := s.advanceWorkLoop(mux, r, t, pr, m); e != nil {
			t.setState("blocked", t.Stage, e.Error())
			if saveErr := s.saveWorkLoop(t); saveErr != nil {
				deliveryWriteError(w, 409, saveErr)
				return
			}
		}
		deliveryJSON(w, t)
	})
}

// Work-loop source updates are recovered separately; they never return to deploy.
func (s *Store) writeWorkLoopSource(ctx context.Context, t *WorkLoop) error {
	w := s.GetWorkItem(t.Grant.Spec.WorkItemID)
	if w == nil {
		return fmt.Errorf("工作项已不存在")
	}
	it := t.current()
	message := fmt.Sprintf("跟进任务 %s · 第 %d 轮：%s\n环境 %s，交付执行单 #%d，回归报告 %v。", t.ID, it.Number, it.Summary, t.Grant.Spec.Environment, it.DeliveryID, it.Reports)
	if !t.Grant.Spec.CompleteScope {
		message += "本轮为部分范围交付，工作项保持原状态。"
	}
	if w.Source == "manual" {
		// Status, comment and the receipt commit atomically, making restart safe.
		tx, e := s.db.Begin()
		if e != nil {
			return e
		}
		defer tx.Rollback()
		key := fmt.Sprintf("work_loop_receipt:%s:%d", t.ID, it.Number)
		var old string
		if tx.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, key).Scan(&old) == nil {
			return nil
		}
		status := w.Status
		if t.Grant.Spec.CompleteScope {
			status = WorkItemResolved
		}
		if w.Status == WorkItemClosed {
			return fmt.Errorf("工作项已关闭，请核对后再发起新轮次")
		}
		result, e := tx.Exec(`UPDATE work_items SET status=?,updated_at=? WHERE id=? AND status=? AND updated_at=? AND (owner=? OR assigned_to=?)`, status, time.Now().Unix(), w.ID, w.Status, w.UpdatedAt, t.Owner, t.Owner)
		if e != nil {
			return e
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return ErrRunConflict
		}
		if _, e = tx.Exec(`INSERT INTO research_activity(work_item_id,actor,kind,content,created_at) VALUES(?,?,'comment',?,?)`, w.ID, t.Owner, message, time.Now().Unix()); e != nil {
			return e
		}
		if _, e = tx.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?)`, key, digestJSON([]any{it.DeliveryID, it.Reports, status})); e != nil {
			return e
		}
		return tx.Commit()
	}
	if it.SourceOperation != "" {
		op, e := s.ResearchOperation(it.SourceOperation, t.Owner)
		if e != nil {
			return e
		}
		if op.State != "success" {
			return fmt.Errorf("来源操作 %s 为 %s，仅核对原操作，不重新交付", op.ID, op.State)
		}
		return nil
	}
	z, _, e := s.researchClient(ctx, w, t.Owner)
	if e != nil {
		return e
	}
	action := "comment"
	if t.Grant.Spec.CompleteScope {
		action = "resolve"
		if w.Kind == "requirement" {
			action = "develop"
		}
	}
	remote, e := z.researchReadItem(ctx, w, "view")
	if e != nil {
		return e
	}
	if action == "resolve" && researchValue(remote.Bug, "status") == "resolved" {
		proven := false
		for _, previous := range t.Iterations {
			if previous.Number == it.Number || previous.SourceOperation == "" {
				continue
			}
			op, e := s.ResearchOperation(previous.SourceOperation, t.Owner)
			if e == nil && op.State == "success" && researchMarkerFound(remote, op.ID, op.RemoteAccount, op.Request.Action) {
				proven = true
				break
			}
		}
		if !proven {
			return fmt.Errorf("来源 Bug 已解决，但未匹配本任务此前回写记录；请核对来源状态")
		}
		action = "comment" // Later authorized iterations add evidence without resolving twice.
	}
	opID := strings.TrimPrefix(digestJSON([]any{t.ID, it.Number, "source"}), "sha256:")[:32]
	req := ResearchAction{OperationID: opID, Action: action, ExpectedDigest: remote.Digest, Resolution: t.Grant.Spec.Resolution, ResolvedBuild: t.Grant.Spec.ResolvedBuild, AssignedTo: t.Grant.Spec.AssignedTo, Comment: message, Metadata: ResearchMetadata{Revision: w.MetadataRevision}}
	// Persist exact request before remote dispatch; a lost response stays unknown.
	b, _ := json.Marshal(req)
	t.Intent = &WorkLoopIntent{Name: "write_source", State: "dispatching", Body: b}
	it.SourceOperation = opID
	if e = s.saveWorkLoop(t); e != nil {
		return e
	}
	op, e := s.ApplyResearchAction(ctx, w.ID, t.Owner, req)
	if e != nil {
		// The original adapter persists its operation BEFORE any remote write.
		// Only a confirmed absent row is a safe pre-write rejection.
		var found string
		if err := s.db.QueryRow(`SELECT operation_id FROM research_operations WHERE operation_id=?`, opID).Scan(&found); errors.Is(err, sql.ErrNoRows) {
			t.Intent.State = "rejected"
			it.SourceOperation = ""
			if saveErr := s.saveWorkLoop(t); saveErr != nil {
				return saveErr
			}
		}
		return e
	}
	if op.State != "success" {
		return fmt.Errorf("交付及回归已完成，来源回写 %s：%s", op.State, op.Message)
	}
	t.Intent.State = "received"
	return nil
}
