package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// The MCP execution surface deliberately supports development/test only. It
// reuses frozen plans and the ordinary executor; it cannot approve a release.
func mcpDeliveryEnvironment(s *Store, name string) error {
	env := s.GetEnvironment(name)
	if env == nil {
		return fmt.Errorf("环境不存在")
	}
	if env.Protected || env.Purpose == "production" {
		return fmt.Errorf("MCP 本轮仅开放非受保护的开发/测试环境；生产与受保护环境沿用页面审批执行")
	}
	return nil
}

func registerDeliveryMCPRoutes(mux *http.ServeMux, s *Store, m *ReleaseManager, pr *PipelineRunner) {
	mux.HandleFunc("POST /api/delivery/mcp/runs/{id}/confirm-scope", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		run := s.DeliveryRun(id)
		var req struct {
			Revision       int64   `json:"revision"`
			Digest         string  `json:"digest"`
			NodeID         string  `json:"node_id"`
			Comment        string  `json:"comment"`
			WorkItemIDs    []int64 `json:"work_item_ids"`
			ConfirmationID string  `json:"confirmation_id"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&req) != nil || run == nil || req.Revision != run.Revision || req.Digest != run.Digest {
			deliveryWriteError(w, 409, "范围修订已变化，请重新预览后确认")
			return
		}
		if err := mcpDeliveryEnvironment(s, run.Plan.Environment.Name); err != nil {
			deliveryWriteError(w, 403, err)
			return
		}
		step, node := researchNextNode(run)
		actor := deliveryActor(r)
		if run.Plan.PreparationRequest == nil || run.Plan.Template.Workbench != "research" || run.Status != "waiting_confirmation" || step == nil || node == nil || step.Kind != "manual" || node.Kind != "manual" || node.ID != req.NodeID || !namedApprover(node, actor) {
			deliveryWriteError(w, 403, "仅当前范围节点指定的本人可以确认；审批与人工验证仍在页面处理")
			return
		}
		var enabled []string
		if json.Unmarshal([]byte(r.Header.Get("X-Reviewer-Directory")), &enabled) != nil || len(req.ConfirmationID) != 32 || len(req.WorkItemIDs) > 100 || strings.TrimSpace(req.Comment) == "" || (len(node.AssignedWorkItems[actor]) > 0 && len(req.WorkItemIDs) == 0) || (len(node.AssignedWorkItems[actor]) == 0 && len(req.WorkItemIDs) > 0) {
			deliveryWriteError(w, 400, "缺少有效人员目录、冻结确认记录、确认意见或本人工作项范围")
			return
		}
		result, err := s.reviewResearchTaskState(run, node.ID, actor, req.Digest, req.Comment, true, enabled, req.WorkItemIDs)
		if err != nil {
			deliveryWriteError(w, 409, err)
			return
		}
		s.Audit(actor, "mcp-self-scope-confirmation", "delivery-run", fmt.Sprint(id), map[string]any{"node": node.ID, "revision": req.Revision, "digest": req.Digest, "confirmation_id": req.ConfirmationID, "work_item_ids": req.WorkItemIDs})
		deliveryJSON(w, result)
	})
	mux.HandleFunc("POST /api/delivery/mcp/preparations/runs", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			DeliveryRequest
			ExpectedDigest string `json:"expected_digest"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&req) != nil {
			deliveryWriteError(w, 400, "请求格式无效")
			return
		}
		if e := mcpDeliveryEnvironment(s, req.Environment); e != nil {
			deliveryWriteError(w, 403, e)
			return
		}
		r.URL.Path = "/api/delivery/preparations/runs"
		body, _ := json.Marshal(req)
		r.Body = io.NopCloser(bytes.NewReader(body))
		mux.ServeHTTP(w, r)
	})
	mux.HandleFunc("POST /api/delivery/mcp/runs/{id}/advance", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		run := s.DeliveryRun(id)
		if run == nil || !deliveryManage(r, run.Owner) {
			deliveryWriteError(w, 403, "仅发起人或管理员可推进交付")
			return
		}
		var req struct {
			Revision int64  `json:"revision"`
			Digest   string `json:"digest"`
			NodeID   string `json:"node_id"`
			Service  string `json:"service"`
			Manual   bool   `json:"manual"`
			Comment  string `json:"comment"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&req) != nil || req.Revision != run.Revision || req.Digest != run.Digest {
			deliveryWriteError(w, 409, "执行单修订已变化，请重新预览")
			return
		}
		if e := mcpDeliveryEnvironment(s, run.Plan.Environment.Name); e != nil {
			deliveryWriteError(w, 403, e)
			return
		}
		control := s.DeliveryControl(id)
		if control.State == "running" || control.State == "pausing" {
			deliveryWriteError(w, 409, "自动协调器运行中，不能同时手动推进")
			return
		}
		var step *DeliveryStep
		for i := range run.Steps {
			if run.Plan.ExecutionMode == dependencyExecution {
				if run.NextIndex != nil && *run.NextIndex == i {
					step = &run.Steps[i]
					break
				}
				continue
			}
			if run.Steps[i].Status != "success" {
				step = &run.Steps[i]
				break
			}
		}
		if step == nil || step.NodeID != req.NodeID || step.Service != req.Service || step.Status == "executing" {
			deliveryWriteError(w, 409, "当前节点已变化或结果待核对，不会推进下一项")
			return
		}
		if req.Manual != (step.Kind == "manual") {
			deliveryWriteError(w, 400, "人工节点必须独立确认，不能当作自动执行")
			return
		}
		if req.Manual && strings.TrimSpace(req.Comment) == "" {
			deliveryWriteError(w, 400, "人工回执必须填写实际处理说明")
			return
		}
		if e := validateDeliveryReviewerDirectory(&run.Plan, r.Header.Get("X-Reviewer-Directory"), run.Owner); e != nil {
			deliveryWriteError(w, 409, e)
			return
		}
		if run.Plan.PreparationRequest != nil {
			if req.Manual || step.Kind == "manual" || step.Kind == "approval" {
				deliveryWriteError(w, 403, "准备范围确认与审批必须由指定人员处理")
				return
			}
			result, e := s.advanceResearchFlowState(r.Context(), run, deliveryActor(r), pr)
			if e != nil {
				deliveryWriteError(w, 409, e)
				return
			}
			deliveryJSON(w, result)
			return
		}
		// Pass THIS revision to the CAS executor. Reloading by ID here could advance
		// a different node between the outer check and execution.
		result, e := s.advanceSelectedDeliveryStep(run, deliveryActor(r), req.Manual, m, func(child int64) { go m.startNextBatch(child) }, nil, step, req.Comment)
		if e != nil {
			deliveryWriteError(w, 409, e)
			return
		}
		deliveryJSON(w, result)
	})
	// The ordinary code executor already freezes sources and claims its revision
	// before any merge/build. Keep the additional MCP environment boundary here.
	for _, action := range []string{"start", "resume", "retry-builds"} {
		action := action
		mux.HandleFunc("POST /api/delivery/mcp/code-preparations/{id}/"+action, func(w http.ResponseWriter, r *http.Request) {
			p := s.CodePreparation(r.PathValue("id"))
			if p == nil || !deliveryManage(r, p.Owner) {
				deliveryWriteError(w, 403, "代码准备不存在或不属于本人")
				return
			}
			if e := mcpDeliveryEnvironment(s, p.Request.Environment); e != nil {
				deliveryWriteError(w, 403, e)
				return
			}
			if p.PreparationRunID <= 0 || p.Protected {
				deliveryWriteError(w, 409, "仅支持统一流程生成的代码任务")
				return
			}
			var req struct {
				Confirm       bool     `json:"confirm"`
				Revision      int64    `json:"expected_revision"`
				TargetCommits []string `json:"target_commits"`
			}
			raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 128<<10))
			if err != nil || json.Unmarshal(raw, &req) != nil || !req.Confirm || req.Revision <= 0 || req.Revision != p.Revision {
				deliveryWriteError(w, 409, "缺少明确确认或代码任务修订已变化")
				return
			}
			if env := r.URL.Query().Get("environment"); env != "" && env != p.Request.Environment {
				deliveryWriteError(w, 409, "代码任务环境不匹配")
				return
			}
			if action == "resume" {
				if len(req.TargetCommits) == 0 || len(req.TargetCommits) != len(p.Repositories) {
					deliveryWriteError(w, 400, "必须逐仓库冻结解决后的目标提交")
					return
				}
				for _, sha := range req.TargetCommits {
					if !fullBuildCommit.MatchString(sha) {
						deliveryWriteError(w, 400, "目标提交必须是完整SHA")
						return
					}
				}
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
			r.URL.Path = "/api/delivery/code-preparations/" + p.ID + "/" + action
			mux.ServeHTTP(w, r)
		})

	}
	mux.HandleFunc("POST /api/delivery/mcp/runs", func(w http.ResponseWriter, r *http.Request) {
		var req DeliveryRequest
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&req) != nil {
			deliveryWriteError(w, 400, "交付请求无效")
			return
		}
		if e := mcpDeliveryEnvironment(s, req.Environment); e != nil {
			deliveryWriteError(w, 403, e)
			return
		}
		if req.PreflightToken == "" {
			deliveryWriteError(w, 400, "必须使用冻结预检凭证")
			return
		}
		req.reviewerDirectory = r.Header.Get("X-Reviewer-Directory")
		run, e := s.CreateDeliveryRun(req, deliveryActor(r), m)
		if e != nil {
			deliveryWriteError(w, 409, e)
			return
		}
		deliveryJSON(w, run)
	})
}
