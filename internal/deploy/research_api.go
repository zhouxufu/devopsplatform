package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"logplatform/pkg/personnames"
)

func registerResearchRoutes(mux *http.ServeMux, s *Store, pr *PipelineRunner, manager *ReleaseManager) {
	registerDeliveryMCPRoutes(mux, s, manager, pr)
	registerWorkLoopRoutes(mux, s, pr, manager)
	registerWorkPersonnel(mux, s)
	registerResearchDeliveryPolicy(mux, s)
	registerResearchReleaseMarkRoutes(mux, s)
	registerResearchLiveRoutes(mux, s)
	registerResearchCollaboration(mux, s)
	registerResearchPreparationActions(mux, s)
	registerResearchQueryTasks(mux, s)
	registerResearchFieldRoutes(mux, s)
	registerResearchDictionaryRoutes(mux, s)
	registerResearchImageRoutes(mux, s)
	registerWorkDeliveryRecordRoutes(mux, s)
	mux.HandleFunc("GET /api/research/tasks/{id}/detail", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		visible := false
		for _, view := range []string{"todo", "done"} {
			tasks, err := s.ResearchTasks(deliveryActor(r), view)
			if err != nil {
				deliveryWriteError(w, 500, err)
				return
			}
			for _, t := range tasks {
				visible = visible || t.RunID == id
			}
		}
		if !visible {
			deliveryWriteError(w, 403, "该执行单没有属于本人的任务")
			return
		}
		deliveryJSON(w, s.DeliveryRun(id))
	})
	decode := func(w http.ResponseWriter, r *http.Request, v any) bool {
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(v) != nil {
			deliveryWriteError(w, 400, "请求格式无效")
			return false
		}
		return true
	}
	idOf := func(r *http.Request) int64 { id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64); return id }
	write := func(w http.ResponseWriter, v any, err error) {
		if err != nil {
			var conflict *researchSourceConflict
			if errors.As(err, &conflict) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusConflict)
				json.NewEncoder(w).Encode(map[string]string{"error": conflict.Error(), "code": "research_source_conflict"})
				return
			}
			deliveryWriteError(w, 409, err)
			return
		}
		deliveryJSON(w, v)
	}
	mux.HandleFunc("GET /api/research/overview", func(w http.ResponseWriter, r *http.Request) {
		aliases := []string{deliveryActor(r)}
		connections := []map[string]any{}
		for _, c := range s.Connections() {
			if c.Kind == "zentao" {
				account, status := s.researchAuthStatus(c.Name, deliveryActor(r))
				if account != "" {
					aliases = append(aliases, account)
				}
				connections = append(connections, map[string]any{"name": c.Name, "account": account, "authorized": account != "" && status != "needs_verification", "credential_saved": account != "", "auth_status": status, "public_read_configured": c.SecretConfigured, "field_labels": map[string]any{"severity": sourceLabels(&c, "severity"), "priority": sourceLabels(&c, "priority"), "resolution": sourceLabels(&c, "resolution")}})
			}
		}
		names := personnames.FromRequest(r)
		people := map[string]string{}
		var enabled []string
		_ = json.Unmarshal([]byte(r.Header.Get("X-Reviewer-Directory")), &enabled)
		for _, account := range enabled {
			people[account] = names[account]
			if people[account] == "" {
				people[account] = account
			}
		}
		if _, ok := people[deliveryActor(r)]; !ok {
			people[deliveryActor(r)] = deliveryActor(r)
		}
		deliveryJSON(w, map[string]any{"actor": deliveryActor(r), "aliases": aliases, "connections": connections, "people": names, "assignees": people})
	})
	mux.HandleFunc("GET /api/research/connections/{name}/account-hint", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		account, err := s.researchAccountHint(ctx, r, r.PathValue("name"))
		write(w, map[string]any{"account": account, "linked": account != ""}, err)
	})
	mux.HandleFunc("POST /api/research/connections/{name}/authorize", func(w http.ResponseWriter, r *http.Request) {
		var v ResearchCredential
		if !decode(w, r, &v) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		err := s.AuthorizeResearch(ctx, r.PathValue("name"), deliveryActor(r), v)
		write(w, map[string]bool{"ok": err == nil}, err)
	})
	mux.HandleFunc("DELETE /api/research/connections/{name}/authorize", func(w http.ResponseWriter, r *http.Request) {
		_, err := s.db.Exec(`DELETE FROM research_credentials WHERE actor=? AND connection_name=?`, deliveryActor(r), r.PathValue("name"))
		if err == nil {
			s.forgetZentaoSession(deliveryActor(r), r.PathValue("name"))
		}
		write(w, map[string]bool{"ok": err == nil}, err)
	})
	mux.HandleFunc("GET /api/research/work-items/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := idOf(r)
		item := s.GetWorkItem(id)
		if item == nil {
			deliveryWriteError(w, 404, "工作项不存在")
			return
		}
		activity, err := s.ResearchActivities(id)
		viewItems := []researchLiveItem{{WorkItem: *item}}
		if e := s.decorateResearchRelations(viewItems, deliveryActor(r)); e != nil {
			deliveryWriteError(w, 500, e)
			return
		}
		*item = viewItems[0].WorkItem
		if err != nil {
			write(w, nil, err)
			return
		}
		pending := []ResearchOperation{}
		rows, err := s.db.Query(`SELECT result,state FROM research_operations WHERE work_item_id=? AND actor=? AND state<>'success' ORDER BY created_at DESC LIMIT 20`, id, deliveryActor(r))
		if err != nil {
			write(w, nil, err)
			return
		}
		for rows.Next() {
			var raw, state string
			var operation ResearchOperation
			if err = rows.Scan(&raw, &state); err != nil || json.Unmarshal([]byte(raw), &operation) != nil {
				rows.Close()
				deliveryWriteError(w, 500, "操作记录读取失败")
				return
			}
			operation.State = state
			pending = append(pending, operation)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			write(w, nil, err)
			return
		}
		out := map[string]any{"item": item, "activity": activity, "pending_operations": pending}
		if strings.HasPrefix(item.Source, "zentao:") {
			ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
			defer cancel()
			z, c, identity, err := s.liveReadClient(ctx, strings.TrimPrefix(item.Source, "zentao:"), deliveryActor(r), false)
			if err != nil {
				out["source_error"] = err.Error()
			} else {
				remote, e := z.researchReadItem(ctx, item, "view")
				product := 0
				if remote != nil {
					product, _ = strconv.Atoi(researchValue(remote.Bug, "product"))
				}
				if e == nil && product != 0 && !liveProductAllowed(c, product) {
					e = fmt.Errorf("Bug 已不在连接允许的产品范围")
				}
				if e != nil {
					out["source_error"] = e.Error()
				} else {
					z.enrichResearchResolution(ctx, c, remote)
					out["remote"] = remote
					out["images"] = researchImageManifest(remote, z.root)
					out["source_identity"] = identity
					fresh := *item
					fresh.Title, fresh.Description, fresh.DescriptionFormat = researchValue(remote.Bug, "title"), researchValue(remote.Bug, "steps"), "html"
					if item.Kind == "requirement" {
						fresh.Description = researchValue(remote.Story, "spec")
					}
					fresh.ExternalStatus = researchValue(remote.Bug, "status")
					fresh.Status = WorkItemOpen
					if fresh.ExternalStatus == "closed" {
						fresh.Status = WorkItemClosed
					}
					fresh.AssignedTo, fresh.OpenedBy, fresh.ResolvedBy, fresh.ClosedBy = researchValue(remote.Bug, "assignedTo"), researchValue(remote.Bug, "openedBy"), researchValue(remote.Bug, "resolvedBy"), researchValue(remote.Bug, "closedBy")
					fresh.ResearchIssueFields = issueFields(c, researchValue(remote.Bug, "severity"), researchValue(remote.Bug, "pri"), researchValue(remote.Bug, "resolution"), researchValue(remote.Bug, "closedDate"))
					fresh.PersonLabels = issuePersonLabels(remote.Users, fresh.AssignedTo, fresh.OpenedBy, fresh.ResolvedBy, fresh.ClosedBy)
					fresh.ProjectID = researchValue(remote.Bug, "project")
					fresh.SourceProductID = researchValue(remote.Bug, "product")
					var manual *bool
					if e := s.db.QueryRow(`SELECT release_required FROM research_work_metadata WHERE work_item_id=?`, id).Scan(&manual); e != nil && e != sql.ErrNoRows {
						deliveryWriteError(w, 500, e)
						return
					}
					fresh.ReleaseRequired = manual
					if e := s.decorateResearchPolicy(&fresh); e != nil {
						deliveryWriteError(w, 500, e)
						return
					}
					if label := remote.Resolutions[fresh.Resolution]; label != "" {
						fresh.ResolutionLabel = label
					}
					out["item"] = &fresh // Presentation only; no background snapshot write.
				}
			}
		}
		if out["source_error"] != nil && item.ReleaseOrigin == "rule" {
			item.ReleaseRequired = nil
			item.ReleaseOrigin = "unknown"
			item.ReleaseReason = "来源读取失败，当前上线判定待核对"
		}
		deliveryJSON(w, out)
	})
	mux.HandleFunc("PUT /api/research/work-items/{id}/release-intent", func(w http.ResponseWriter, r *http.Request) {
		var v ResearchMetadata
		if !decode(w, r, &v) {
			return
		}
		id := idOf(r)
		item := s.GetWorkItem(id)
		if item == nil {
			deliveryWriteError(w, 404, "工作项不存在")
			return
		}
		if item.Source == "manual" && !deliveryManage(r, item.Owner) {
			deliveryWriteError(w, 403, "仅创建人或管理员可维护自定义工作项")
			return
		}
		if item.Source != "manual" {
			ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
			defer cancel()
			z, _, err := s.researchClient(ctx, item, deliveryActor(r))
			if err == nil {
				_, err = z.researchReadItem(ctx, item, "edit")
			}
			if err != nil {
				write(w, nil, err)
				return
			}
		}
		err := s.SetResearchMetadata(id, v, deliveryActor(r))
		write(w, s.GetWorkItem(id), err)
	})
	mux.HandleFunc("POST /api/research/work-items/{id}/comment", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Comment     string   `json:"comment"`
			Attachments []string `json:"attachments"`
		}
		if !decode(w, r, &v) {
			return
		}
		item := s.GetWorkItem(idOf(r))
		if item == nil || item.Source != "manual" {
			deliveryWriteError(w, 400, "外部工作项评论须通过来源操作提交")
			return
		}
		err := s.AddResearchComment(item.ID, deliveryActor(r), v.Comment, v.Attachments)
		write(w, map[string]bool{"ok": err == nil}, err)
	})
	mux.HandleFunc("GET /api/research/work-items/{id}/action-options", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		item := s.GetWorkItem(idOf(r))
		z, _, err := s.researchClient(ctx, item, deliveryActor(r))
		if err != nil {
			write(w, nil, err)
			return
		}
		method := r.URL.Query().Get("action")
		if method == "comment" || method == "develop" {
			method = "edit"
		}
		switch method {
		case "resolve", "activate", "close", "assignTo", "edit":
		default:
			deliveryWriteError(w, 400, "操作不支持")
			return
		}
		v, err := z.researchReadItem(ctx, item, method)
		if err == nil && method == "resolve" {
			z.enrichResearchResolution(ctx, s.GetConnection(strings.TrimPrefix(item.Source, "zentao:")), v)
		}
		write(w, v, err)
	})
	mux.HandleFunc("POST /api/research/work-items/{id}/actions", func(w http.ResponseWriter, r *http.Request) {
		var v ResearchAction
		if !decode(w, r, &v) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		op, err := s.ApplyResearchAction(ctx, idOf(r), deliveryActor(r), v)
		write(w, op, err)
	})
	mux.HandleFunc("GET /api/research/operations/{operation}", func(w http.ResponseWriter, r *http.Request) {
		op, err := s.ResearchOperation(r.PathValue("operation"), deliveryActor(r))
		write(w, op, err)
	})
	mux.HandleFunc("POST /api/research/operations/{operation}/reconcile", func(w http.ResponseWriter, r *http.Request) {
		actor := deliveryActor(r)
		op, err := s.ResearchOperation(r.PathValue("operation"), actor)
		if err != nil {
			write(w, nil, err)
			return
		}
		if op.State == "success" {
			deliveryJSON(w, op)
			return
		}
		var id int64
		s.db.QueryRow(`SELECT work_item_id FROM research_operations WHERE operation_id=? AND actor=?`, op.ID, actor).Scan(&id)
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		z, _, err := s.researchClient(ctx, s.GetWorkItem(id), actor)
		if err != nil {
			write(w, nil, err)
			return
		}
		remote, err := z.researchReadItem(ctx, s.GetWorkItem(id), "view")
		if err == nil {
			err = s.completeResearchOperation(id, actor, op, remote)
		}
		write(w, op, err)
	})
	mux.HandleFunc("GET /api/research/tasks", func(w http.ResponseWriter, r *http.Request) {
		items, err := s.ResearchTasks(deliveryActor(r), r.URL.Query().Get("view"))
		if err != nil {
			write(w, nil, err)
			return
		}
		others, err := s.researchOtherTasks(deliveryActor(r), r.URL.Query().Get("view"))
		if err != nil {
			write(w, nil, err)
			return
		}
		items = append(items, others...)
		if r.URL.Query().Get("view") != "done" {
			exceptions, e := s.unresolvedResearchExceptions(deliveryActor(r))
			if e != nil {
				write(w, nil, e)
				return
			}
			// Prefer the specific code-conflict task over a duplicate generic failure.
			for _, t := range exceptions {
				duplicate := false
				for _, old := range items {
					if old.RunID == t.RunID && t.SourceKind == "delivery" && old.Kind == "code-conflict" {
						duplicate = true
					}
				}
				if !duplicate {
					items = append(items, t)
				}
			}
		} else {
			history, e := s.researchExceptionHistory(deliveryActor(r))
			if e != nil {
				write(w, nil, e)
				return
			}
			items = append(items, history...)
		}
		items = s.enrichResearchTasks(items)
		items, err = s.projectWorkLoopTasks(deliveryActor(r), r.URL.Query().Get("view"), items, false)
		if err != nil {
			write(w, nil, err)
			return
		}
		items, err = s.attentionTasks(deliveryActor(r), r.URL.Query().Get("view"), items)
		if err != nil {
			write(w, nil, err)
			return
		}
		sort.SliceStable(items, func(i, j int) bool {
			if (items[i].Kind == "exception") != (items[j].Kind == "exception") {
				return items[i].Kind == "exception"
			}
			return max(items[i].UpdatedAt, items[i].At) > max(items[j].UpdatedAt, items[j].At)
		})
		q, kind := strings.ToLower(r.URL.Query().Get("q")), r.URL.Query().Get("kind")
		filtered := []ResearchTask{}
		for _, v := range items {
			if id := r.URL.Query().Get("id"); id != "" && v.ID != id {
				continue
			}
			if (kind == "" || kind == v.Kind) && (q == "" || strings.Contains(strings.ToLower(v.Title+v.Version+v.Owner+v.Stage+strings.Join(v.Services, " ")), q)) {
				filtered = append(filtered, v)
			}
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
	mux.HandleFunc("POST /api/research/tasks/{id}/{node}", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			WorkItemIDs []int64 `json:"work_item_ids"`
			Digest      string  `json:"digest"`
			Comment     string  `json:"comment"`
			Approve     bool    `json:"approve"`
		}
		if !decode(w, r, &v) {
			return
		}
		id, actor, node := idOf(r), deliveryActor(r), r.PathValue("node")
		run := s.DeliveryRun(id)
		if run == nil {
			deliveryWriteError(w, 404, "执行单不存在")
			return
		}
		var out *DeliveryRun
		var err error
		if run.Plan.Template.Workbench == "research" {
			var enabled []string
			if json.Unmarshal([]byte(r.Header.Get("X-Reviewer-Directory")), &enabled) != nil {
				deliveryWriteError(w, 403, "人员目录不可用")
				return
			}
			out, err = s.ReviewResearchTask(id, node, actor, v.Digest, v.Comment, v.Approve, enabled, v.WorkItemIDs)
		} else if node == "environment" {
			out, err = s.ReviewDeliveryRun(id, actor, v.Digest, v.Approve)
		} else if n := approvalNode(&run.Plan, node); n != nil {
			out, err = s.ReviewDeliveryNode(id, actor, v.Digest, node, v.Comment, v.Approve)
		} else if step, n := researchNextNode(run); n != nil && n.ID == node && step.Kind == "verify" {
			if err = validateDeliveryReviewerDirectory(&run.Plan, r.Header.Get("X-Reviewer-Directory"), run.Owner); err == nil {
				out, err = s.reviewVerification(id, node, v.Digest, actor, v.Comment, v.Approve)
			}
		} else {
			step, n := researchNextNode(run)
			if !deliveryManage(r, run.Owner) || run.Digest != v.Digest || n == nil || n.ID != node || n.Kind != "manual" || step.Status == "success" || !v.Approve {
				deliveryWriteError(w, 403, "仅当前执行负责人可确认人工节点")
				return
			}
			out, err = s.AdvanceDeliveryRun(id, actor, true, manager, v.Comment)
		}
		write(w, out, err)
	})
	mux.HandleFunc("POST /api/delivery/preparations/preview", func(w http.ResponseWriter, r *http.Request) {
		var v DeliveryRequest
		if !decode(w, r, &v) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		accounts, enabled, err := s.researchPeople(ctx, r)
		if err != nil {
			write(w, nil, err)
			return
		}
		plan, err := s.ResearchFlowPlan(v, accounts, enabled, deliveryActor(r))
		if err != nil {
			write(w, nil, err)
			return
		}
		deliveryJSON(w, map[string]any{"plan": plan, "digest": digestJSON(plan)})
	})
	mux.HandleFunc("POST /api/delivery/preparations/runs", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			DeliveryRequest
			ExpectedDigest string `json:"expected_digest"`
		}
		if !decode(w, r, &v) {
			return
		}
		if v.VersionID > 0 {
			version := s.GetReleaseVersion(v.VersionID)
			if version == nil || !deliveryManage(r, version.Owner) {
				deliveryWriteError(w, 403, "仅版本维护人可发起准备流程")
				return
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		accounts, enabled, err := s.researchPeople(ctx, r)
		if err != nil {
			write(w, nil, err)
			return
		}
		plan, err := s.ResearchFlowPlan(v.DeliveryRequest, accounts, enabled, deliveryActor(r))
		if err != nil {
			write(w, nil, err)
			return
		}
		if v.ExpectedDigest == "" || v.ExpectedDigest != digestJSON(plan) {
			deliveryWriteError(w, 409, "准备范围或人员已变化，请重新预览")
			return
		}
		out, err := s.CreateResearchFlow(v.DeliveryRequest, accounts, enabled, deliveryActor(r))
		write(w, out, err)
	})
	mux.HandleFunc("GET /api/delivery/preparations/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		v := s.DeliveryRun(idOf(r))
		if v == nil || v.Plan.PreparationRequest == nil || v.Plan.Template.Workbench != "research" {
			deliveryWriteError(w, 404, "流程不存在")
			return
		}
		deliveryJSON(w, v)
	})
	mux.HandleFunc("POST /api/delivery/preparations/runs/{id}/advance", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Confirm bool   `json:"confirm"`
			Digest  string `json:"digest"`
		}
		if !decode(w, r, &v) {
			return
		}
		run := s.DeliveryRun(idOf(r))
		if run == nil || !deliveryManage(r, run.Owner) || !v.Confirm || v.Digest != run.Digest {
			deliveryWriteError(w, 403, "请由负责人核对并明确确认执行当前代码节点")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		out, err := s.AdvanceResearchFlow(ctx, run.ID, run.Owner, pr)
		write(w, out, err)
	})
}
