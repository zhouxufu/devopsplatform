package deploy

// The inbox is a projection of existing execution ledgers, not another workflow
// engine. Reading or polling it must never reconcile or replay remote writes.
import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func researchApprovers(raw string) []string {
	out := []string{}
	for _, a := range strings.Split(raw, ",") {
		if a = strings.TrimSpace(a); a != "" {
			out = append(out, a)
		}
	}
	return out
}

func researchRunSummary(v *DeliveryRun) ResearchTask {
	t := ResearchTask{ID: fmt.Sprintf("delivery:%d", v.ID), SourceKind: "delivery", RunID: v.ID, Title: v.Plan.Template.Name, Owner: v.Owner, At: v.CreatedAt, UpdatedAt: v.CreatedAt, Digest: v.Digest, Status: v.Status, ReadOnly: true, Environment: v.Plan.Environment.DisplayName}
	t.Revision = v.Revision
	if t.Environment == "" {
		t.Environment = v.Plan.Environment.Name
	}
	if v.Plan.Version != nil {
		t.Version = v.Plan.Version.Version
	}
	apps := map[string]bool{}
	for _, app := range v.Plan.Services {
		apps[app.Service] = true
	}
	if v.Plan.Scope != nil {
		for _, c := range v.Plan.Scope.Changes {
			if c.Service != "" {
				apps[c.Service] = true
			}
		}
	}
	for app := range apps {
		t.Services = append(t.Services, app)
	}
	sort.Strings(t.Services)
	done := 0
	var current *DeliveryStep
	for i := range v.Steps {
		x := &v.Steps[i]
		t.UpdatedAt = max(t.UpdatedAt, x.StartedAt, x.EndedAt)
		for _, r := range x.Reviews {
			t.UpdatedAt = max(t.UpdatedAt, r.At)
		}
		if x.Status == "success" {
			done++
		} else if current == nil {
			current = x
		}
	}
	t.Progress = fmt.Sprintf("%d / %d", done, len(v.Steps))
	if current != nil {
		t.NodeID, t.Stage, t.Kind = current.NodeID, current.Name, current.Kind
		for _, n := range v.Plan.Nodes {
			if n.ID == current.NodeID {
				t.Assignees = append([]string{}, n.Approvers...)
				if n.Kind == "manual" && len(t.Assignees) == 0 {
					t.Assignees = []string{v.Owner}
				}
				// Already completed signers are not shown as the next handler.
				remaining := []string{}
				for _, a := range t.Assignees {
					if n.Kind == "approval" && a == v.Owner {
						continue
					}
					if !researchPersonComplete(current, &n, a) {
						remaining = append(remaining, a)
					}
				}
				t.Assignees = remaining
			}
		}
	}
	switch v.Status {
	case "success":
		if v.Plan.Template.Workbench == "research" {
			t.Stage, t.NextAction = "前置流程完成（尚未发布）", "发起人继续发布交付"
			t.Assignees = []string{v.Owner}
		} else {
			t.Stage, t.NextAction = "交付完成", "查看执行结果；运行状态以实时检测为准"
		}
	case "failed":
		t.Kind, t.NextAction, t.Assignees = "exception", "核对执行台账和日志，再按原交付恢复；不重放成功的写操作", []string{v.Owner}
		if current != nil {
			t.BlockedReason = current.Detail
		}
		if t.BlockedReason == "" {
			t.BlockedReason = "交付失败，需核对实际执行结果"
		}
	case "rejected", "canceled":
		t.NextAction, t.Assignees = "发起人核对原因；修改内容后重新预检和审核", []string{v.Owner}
	case "pending_approval", "waiting_confirmation":
		if len(t.Assignees) == 0 && !hasApprovalNodes(&v.Plan) && v.Status == "pending_approval" {
			t.Stage, t.Kind = "环境发布审批", "approval"
			t.Assignees = researchApprovers(v.Plan.Environment.Approvers)
		}
		t.NextAction = "等待当前节点处理人；通过后进入下一阶段"
	case "approved":
		t.Assignees, t.NextAction = []string{v.Owner}, "发起人执行当前节点（尚未执行远程操作）"
	case "running":
		if t.Kind == "manual" || t.Kind == "verify" && len(t.Assignees) > 0 {
			t.NextAction = "核对当前人工节点后确认继续"
		} else {
			t.Assignees, t.NextAction = []string{v.Owner}, "跟踪当前执行结果；未开始的节点需显式继续"
		}
	default:
		t.Assignees, t.NextAction = []string{v.Owner}, "查看原执行单核对状态"
	}
	return t
}

func researchRunVisible(v *DeliveryRun, actor string) bool {
	if v == nil || actor == "" {
		return false
	}
	if v.Plan.Template.Workbench == "research" && v.Plan.PreparationRequest == nil {
		return false
	}
	if v.Owner == actor {
		return true
	}
	for _, n := range v.Plan.Nodes {
		if namedApprover(&n, actor) {
			return true
		}
	}
	for _, step := range v.Steps {
		for _, r := range step.Reviews {
			if r.Actor == actor {
				return true
			}
		}
	}
	return !hasApprovalNodes(&v.Plan) && allowedApprover(v.Plan.Environment.Approvers, actor) && actor != v.Owner
}

func researchReleaseSummary(v *ReleaseOrder) ResearchTask {
	t := ResearchTask{ID: fmt.Sprintf("release:%d", v.ID), SourceKind: "release", RunID: v.ID, RunNumber: v.Number, Title: v.Service + " · 应用发布", Owner: v.Actor, Service: v.Service, Services: []string{v.Service}, Version: v.Version, Environment: v.Environment, Status: v.Status, At: v.StartedAt, UpdatedAt: max(v.StartedAt, v.EndedAt), ReadOnly: true, Progress: fmt.Sprintf("%d / %d", v.Succeeded, v.Total), Assignees: []string{v.Actor}}
	switch v.Status {
	case "pending_approval":
		t.Stage, t.NextAction = "应用发布审批", "在原发布单核对后审批（通过可能立即启动部署）"
		t.Assignees = researchApprovers(v.Target.Approvers)
	case "waiting_next_batch":
		t.Stage, t.NextAction = "批次观察确认", "核对首批日志和健康状态，再继续下一批"
	case "failed":
		t.Kind, t.Stage, t.BlockedReason, t.NextAction = "exception", "发布异常", fmt.Sprintf("失败 %d 个实例；需核对日志和实际运行状态", v.Failed), "在原发布单核对结果后恢复，不自动重试"
	case "success":
		t.Stage, t.NextAction = "发布完成", "查看发布结果；运行状态以实时检测为准"
		t.Assignees = nil
	case "rolled_back":
		t.Stage, t.NextAction = "已回退", "核对回退结果及运行状态"
		t.Assignees = nil
	default:
		t.Stage, t.NextAction = "应用发布", "查看原发布单跟踪结果"
	}
	return t
}

func (s *Store) researchSummary(v *DeliveryRun) ResearchTask {
	s.projectResearchCodeState(v)
	t := researchRunSummary(v)
	if v.Status != "running" && v.Status != "approved" && v.Status != "blocked" {
		return t
	}
	if v.Plan.Template.Workbench != "research" {
		control := s.DeliveryControl(v.ID)
		if control.State == "interrupted" {
			t.Kind, t.BlockedReason, t.NextAction = "exception", control.Message, "核对当前节点和执行台账后恢复，不自动重放"
			// A second coordinator interruption is a new exception even when no
			// node advanced. Do not let the old acknowledgement hide it.
			t.EventDigest = digestJSON([]any{control.lease, control.LeaseUntil, control.State})
		}
		if control.State == "running" {
			t.NextAction = "协调器自动推进中；人工节点与批次观察将暂停"
		}
	}
	for _, step := range v.Steps {
		if step.Status == "success" {
			continue
		}
		if step.ReleaseID > 0 {
			if child := s.GetReleaseOrder(step.ReleaseID); child != nil {
				if child.Status == "waiting_next_batch" || child.Status == "failed" {
					c := researchReleaseSummary(child)
					t.Stage, t.NextAction, t.BlockedReason, t.Assignees, t.Kind = c.Stage, c.NextAction, c.BlockedReason, []string{v.Owner}, c.Kind
					t.UpdatedAt = max(t.UpdatedAt, c.UpdatedAt)
					t.EventDigest = digestJSON([]any{child.ID, child.EndedAt, child.Failed, child.Succeeded, child.Status})
					if child.Status == "waiting_next_batch" {
						t.Kind = "manual"
					}
				}
			}
		}
		if step.Kind == "code-prepare" || step.Kind == "code-verify" {
			if p := s.CodePreparation(step.Detail); p != nil {
				if p.State == "conflict" || p.State == "failed" || p.State == "uncertain" || ((p.State == "merging" || p.State == "building") && p.LeaseUntil < time.Now().Unix()) {
					t.PreparationID = p.ID
					t.Assignees = []string{v.Owner}
					t.Kind, t.BlockedReason, t.NextAction = "code-conflict", p.Message, "在当前交付的代码节点解决冲突并推送，再点击“验证合并并继续构建”"
				}
			}
		}
		break
	}
	return t
}

func (s *Store) researchOwnedDeliveries(actor string) ([]ResearchTask, error) {
	out := []ResearchTask{}
	rows, err := s.db.Query(deliveryRunSelect+` WHERE owner=? ORDER BY id DESC`, actor)
	if err != nil {
		return nil, err
	}
	child := map[int64]bool{}
	runs := []*DeliveryRun{}
	for rows.Next() {
		v, e := scanDeliveryRun(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		runs = append(runs, v)
		for _, step := range v.Steps {
			if step.ReleaseID > 0 {
				child[step.ReleaseID] = true
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, v := range runs {
		out = append(out, s.researchSummary(v))
	}
	rows, err = s.db.Query(releaseSelect+` WHERE actor=? ORDER BY id DESC`, actor)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		v, e := scanRelease(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		if !child[v.ID] && v.Actor == actor {
			out = append(out, researchReleaseSummary(&v))
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	// MySQL's default collation is case insensitive; ownership is verified again.
	filtered := []ResearchTask{}
	for _, t := range out {
		if t.Owner == actor {
			filtered = append(filtered, t)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].At > filtered[j].At })
	return filtered, nil
}

func (s *Store) researchExceptionTasks(actor string) ([]ResearchTask, error) {
	owned, err := s.researchOwnedDeliveries(actor)
	if err != nil {
		return nil, err
	}
	out := []ResearchTask{}
	for _, t := range owned {
		if t.Status == "failed" || t.Kind == "exception" {
			t.Kind, t.ReadOnly = "exception", true
			t.Title = "处理异常 · " + t.Title
			out = append(out, t)
		}
		if t.SourceKind == "delivery" && t.Kind == "manual" && t.Status == "running" && t.EventDigest != "" {
			t.ReadOnly = true
			t.Title = "批次观察确认 · " + t.Title
			out = append(out, t)
		}
	}
	rows, err := s.db.Query(`SELECT operation_id,work_item_id,actor,state,result,created_at FROM research_operations WHERE actor=? AND state IN ('uncertain','executing','failed') ORDER BY created_at DESC`, actor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var op ResearchOperation
		var id int64
		var owner, state, raw, operation string
		var at int64
		if err = rows.Scan(&operation, &id, &owner, &state, &raw, &at); err != nil {
			return nil, err
		}
		if owner != actor {
			continue
		}
		if err = json.Unmarshal([]byte(raw), &op); err != nil {
			return nil, err
		}
		out = append(out, ResearchTask{ID: "operation:" + operation, SourceKind: "operation", OperationID: operation, WorkItemID: id, Kind: "exception", Title: fmt.Sprintf("来源回写待核对 · 工作项 #%d", id), Owner: actor, Assignees: []string{actor}, Status: state, At: at, UpdatedAt: at, Stage: "来源操作结果核对", BlockedReason: op.Message, NextAction: "返回工作项读取结果；不重复提交写操作", ReadOnly: true})
	}
	return out, rows.Err()
}

func (s *Store) enrichResearchTasks(tasks []ResearchTask) []ResearchTask {
	cache := map[int64]ResearchTask{}
	for i := range tasks {
		t := &tasks[i]
		if t.ResolutionDigest != "" {
			continue
		}
		if t.SourceKind == "release" {
			if v := s.GetReleaseOrder(t.RunID); v != nil {
				summary := researchReleaseSummary(v)
				t.Stage, t.Assignees, t.NextAction, t.UpdatedAt, t.Services, t.Progress = summary.Stage, summary.Assignees, summary.NextAction, summary.UpdatedAt, summary.Services, summary.Progress
			}
			continue
		}
		if t.SourceKind == "query" {
			if q, e := s.queryTicket(t.RunID, t.Workbench); e == nil {
				t.Stage, t.NextAction, t.UpdatedAt = "查询审批", "审核通过后由申请人执行，不自动查询", q.CreatedAt
				if q.Query.Service != "" {
					t.Services = []string{q.Query.Service}
				}
				if q.Status != "pending_approval" {
					t.Stage = "查询审核结果"
					t.Assignees = []string{q.Owner}
				} else {
					candidates := []string{}
					for _, allow := range q.Approvers {
						candidates = researchApprovers(allow)
						break
					}
					for _, actor := range candidates {
						valid := actor != q.Owner
						for env, allow := range q.Approvers {
							current := s.GetEnvironment(env)
							valid = valid && allowedApprover(allow, actor) && current != nil
							if current != nil {
								valid = valid && allowedApprover(current.Approvers, actor)
							}
						}
						if valid {
							t.Assignees = append(t.Assignees, actor)
						}
					}
				}
			}
			continue
		}
		if t.SourceKind != "" && t.SourceKind != "delivery" {
			continue
		}
		summary, ok := cache[t.RunID]
		if !ok {
			if v := s.DeliveryRun(t.RunID); v != nil {
				summary = s.researchSummary(v)
				cache[t.RunID] = summary
			}
		}
		t.Stage, t.Assignees, t.NextAction, t.UpdatedAt, t.Services, t.Progress = summary.Stage, summary.Assignees, summary.NextAction, summary.UpdatedAt, summary.Services, summary.Progress
	}
	return tasks
}

func (s *Store) researchExceptionHistory(actor string) ([]ResearchTask, error) {
	rows, err := s.db.Query(`SELECT actor,document,comment,created_at FROM research_exception_resolutions WHERE actor=? ORDER BY created_at DESC`, actor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ResearchTask{}
	for rows.Next() {
		var owner, raw, comment string
		var at int64
		var t ResearchTask
		if err = rows.Scan(&owner, &raw, &comment, &at); err != nil {
			return nil, err
		}
		if owner != actor {
			continue
		}
		if err = json.Unmarshal([]byte(raw), &t); err != nil {
			return nil, err
		}
		t.ID += "/" + t.ResolutionDigest
		t.Title = "异常核对记录 · " + t.Title
		t.Stage = "异常核对已记录（原状态保留）"
		t.Assignees = nil
		t.ReadOnly = true
		t.NextAction = "已人工核对"
		if t.ResolutionAction == "ignore" {
			t.Title = "异常忽略记录 · " + strings.TrimPrefix(t.Title, "异常核对记录 · ")
			t.Stage = "异常待办已忽略（原状态保留）"
			t.NextAction = "已忽略异常待办"
		}
		if comment != "" {
			t.NextAction += "：" + comment
		}
		t.NextAction += "（原发布状态不变）"
		t.At, t.UpdatedAt = at, at
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) unresolvedResearchExceptions(actor string) ([]ResearchTask, error) {
	items, err := s.researchExceptionTasks(actor)
	if err != nil {
		return nil, err
	}
	out := []ResearchTask{}
	for _, t := range items {
		if t.Kind == "exception" && t.SourceKind != "operation" {
			t.ResolutionDigest = digestJSON(t)
			var owner string
			err = s.db.QueryRow(`SELECT actor FROM research_exception_resolutions WHERE task_id=? AND actor=? AND digest=?`, t.ID, actor, t.ResolutionDigest).Scan(&owner)
			if err == nil && owner == actor {
				target, e := s.attentionTarget(actor, t.ID)
				if e != nil {
					return nil, e
				}
				var state string
				e = s.db.QueryRow(`SELECT state FROM research_attention_states WHERE actor=? AND item_key=? AND event_digest=?`, actor, t.ID, target.Task.AttentionEvent).Scan(&state)
				if e != nil && e != sql.ErrNoRows {
					return nil, e
				}
				if state != "restored" && state != "archived" {
					continue
				}
			}
		}
		out = append(out, t)
	}
	return out, nil
}

// Only explicit frozen preparation references establish a handoff. Overlapping
// Bugs/versions are not sufficient proof that a later release is its successor.
func (s *Store) researchLinkedDeliveries(v *DeliveryRun, actor string) ([]ResearchTask, error) {
	rows, err := s.db.Query(deliveryRunSelect + ` ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	runs := []*DeliveryRun{}
	for rows.Next() {
		other, e := scanDeliveryRun(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		runs = append(runs, other)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	codeIDs := map[string]bool{}
	for _, step := range v.Steps {
		if step.Kind == "code-prepare" || step.Kind == "code-verify" {
			if step.Detail != "" {
				codeIDs[step.Detail] = true
			}
		}
	}
	out := []ResearchTask{}
	for _, other := range runs {
		if other.ID == v.ID || !researchRunVisible(other, actor) {
			continue
		}
		linked := false
		if v.Plan.Template.Workbench == "research" && other.Plan.Template.Workbench != "research" {
			linked = other.Plan.PreparationRunID == v.ID || (other.Plan.CodePreparationID != "" && codeIDs[other.Plan.CodePreparationID])
		}
		if v.Plan.Template.Workbench != "research" && other.Plan.Template.Workbench == "research" {
			linked = v.Plan.PreparationRunID == other.ID
			if v.Plan.CodePreparationID != "" {
				for _, step := range other.Steps {
					if step.Detail == v.Plan.CodePreparationID && (step.Kind == "code-prepare" || step.Kind == "code-verify") {
						linked = true
					}
				}
			}
		}
		if linked {
			out = append(out, s.researchSummary(other))
		}
	}
	return out, nil
}

func registerResearchCollaboration(mux *http.ServeMux, s *Store) {
	registerResearchAttention(mux, s)
	mux.HandleFunc("POST /api/research/exceptions/resolve", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ID      string `json:"id"`
			Digest  string `json:"digest"`
			Comment string `json:"comment"`
			Action  string `json:"action"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16000)).Decode(&body) != nil || len(body.Comment) > 8000 {
			deliveryWriteError(w, 400, "请求格式有误或处理说明超过 8000 字节")
			return
		}
		if body.Action == "" {
			body.Action = "review"
		}
		if body.Action != "review" && body.Action != "ignore" {
			deliveryWriteError(w, 400, "异常处理方式仅支持记录结果或忽略待办")
			return
		}
		actor := deliveryActor(r)
		items, err := s.unresolvedResearchExceptions(actor)
		if err != nil {
			deliveryWriteError(w, 500, err)
			return
		}
		for _, t := range items {
			if t.ID == body.ID && t.Kind == "exception" && t.SourceKind != "operation" && t.ResolutionDigest != "" && t.ResolutionDigest == body.Digest {
				if utf8.RuneCountInString(strings.TrimSpace(body.Comment)) < 6 {
					kind := t.SourceKind
					if kind == "" {
						kind = "delivery"
					}
					target, e := s.attentionTarget(actor, fmt.Sprintf("%s:%d", kind, t.RunID))
					if e != nil || !target.ReasonOptional {
						deliveryWriteError(w, 400, "此记录需要填写至少 6 个字符的处理说明")
						return
					}
				}
				if t.SourceKind == "release" {
					target, e := s.attentionTarget(actor, "release:"+fmt.Sprint(t.RunID))
					if e != nil || !target.CanIgnore {
						deliveryWriteError(w, 409, "原发布仍有运行中或待核对结果，请先在原发布单核对，不能关闭异常待办")
						return
					}
				}
				t.ResolutionAction = body.Action
				raw, e := json.Marshal(t)
				if e != nil {
					deliveryWriteError(w, 500, e)
					return
				}
				_, e = s.db.Exec(`INSERT INTO research_exception_resolutions(task_id,actor,digest,document,comment,created_at) VALUES(?,?,?,?,?,?)`, t.ID, actor, t.ResolutionDigest, string(raw), strings.TrimSpace(body.Comment), time.Now().Unix())
				if e != nil {
					deliveryWriteError(w, 409, e)
					return
				}
				auditAction := "research-exception-reviewed"
				if body.Action == "ignore" {
					auditAction = "research-exception-ignored"
				}
				s.Audit(actor, auditAction, t.SourceKind, fmt.Sprint(t.RunID), map[string]string{"task": t.ID, "comment": strings.TrimSpace(body.Comment)})
				deliveryJSON(w, map[string]bool{"ok": true})
				return
			}
		}
		deliveryWriteError(w, 409, "异常已变化、已处理或不属于本人；请重新核对，不允许隐藏未确认的来源回写")
	})
	mux.HandleFunc("GET /api/research/deliveries", func(w http.ResponseWriter, r *http.Request) {
		items, err := s.attentionDeliveries(deliveryActor(r), r.URL.Query().Get("attention"))
		if err != nil {
			deliveryWriteError(w, 500, err)
			return
		}
		q, status := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q"))), r.URL.Query().Get("status")
		filtered := []ResearchTask{}
		for _, t := range items {
			if id := r.URL.Query().Get("id"); id != "" && t.ID != id {
				continue
			}
			if status != "" && status != t.Status {
				continue
			}
			if q != "" && !strings.Contains(strings.ToLower(t.Title+t.Version+t.Environment+t.Stage+strings.Join(t.Services, " ")), q) {
				continue
			}
			filtered = append(filtered, t)
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		if page > 1000000 {
			deliveryWriteError(w, 400, "页码过大")
			return
		}
		size, err := requestListPageSize(r)
		if err != nil {
			deliveryWriteError(w, 400, err)
			return
		}
		start := min(len(filtered), (page-1)*size)
		end := min(len(filtered), start+size)
		deliveryJSON(w, map[string]any{"items": filtered[start:end], "total": len(filtered), "page": page, "page_size": size})
	})
	mux.HandleFunc("GET /api/research/deliveries/{kind}/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		actor := deliveryActor(r)
		if r.PathValue("kind") == "release" {
			v := s.GetReleaseOrder(id)
			if v == nil {
				deliveryWriteError(w, 404, "发布单不存在")
				return
			}
			if v.Actor != actor && v.Approver != actor && !allowedApprover(v.Target.Approvers, actor) {
				deliveryWriteError(w, 403, "发布单不属于本人协作范围")
				return
			}
			var parent int64
			rows, e := s.db.Query(deliveryRunSelect + ` ORDER BY id DESC`)
			if e != nil {
				deliveryWriteError(w, 500, e)
				return
			}
			for rows.Next() {
				run, e := scanDeliveryRun(rows)
				if e != nil {
					rows.Close()
					deliveryWriteError(w, 500, e)
					return
				}
				if researchRunVisible(run, actor) {
					for _, step := range run.Steps {
						if step.ReleaseID == id {
							parent = run.ID
						}
					}
				}
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				deliveryWriteError(w, 500, e)
				return
			}
			deliveryJSON(w, map[string]any{"release": v, "summary": researchReleaseSummary(v), "parent_run_id": parent})
			return
		}
		if r.PathValue("kind") != "delivery" {
			deliveryWriteError(w, 400, "交付类型无效")
			return
		}
		v := s.DeliveryRun(id)
		if !researchRunVisible(v, actor) {
			deliveryWriteError(w, 403, "交付不属于本人协作范围")
			return
		}
		summary := s.researchSummary(v)
		children := []ReleaseOrder{}
		for _, step := range v.Steps {
			if step.ReleaseID > 0 {
				if child := s.GetReleaseOrder(step.ReleaseID); child != nil {
					children = append(children, *child)
					if child.Status == "waiting_next_batch" || child.Status == "failed" {
						c := researchReleaseSummary(child)
						summary.Stage, summary.NextAction, summary.BlockedReason, summary.Assignees = c.Stage, c.NextAction, c.BlockedReason, []string{v.Owner}
					}
				}
			}
		}
		linked, err := s.researchLinkedDeliveries(v, actor)
		if err != nil {
			deliveryWriteError(w, 500, err)
			return
		}
		deliveryJSON(w, map[string]any{"run": v, "summary": summary, "releases": children, "linked_deliveries": linked})
	})
}
