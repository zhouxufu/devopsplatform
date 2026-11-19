package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

func (s *Store) validatePreparedArtifacts(req DeliveryRequest, scope *DeliveryScope) error {
	p := s.CodePreparation(req.CodePreparationID)
	if p == nil || p.State != "ready" || p.Request.StrategyKind != req.StrategyKind || p.Request.TemplateID != req.TemplateID || p.Request.Environment != req.Environment || p.Request.VersionID != req.VersionID || digestJSON(p.Request.WorkItemIDs) != digestJSON(req.WorkItemIDs) || p.ScopeDigest != codeScopeDigest(scope) {
		return fmt.Errorf("代码准备未完成或交付来源变化，请重新准备")
	}
	if err := s.preparationSourceValid(p); err != nil {
		return err
	}
	if digestJSON(req.ArtifactSources) != digestJSON(p.Request.ArtifactSources) {
		return fmt.Errorf("制品来源方式已变化，请重新准备")
	}
	modes, err := deliveryArtifactSources(req, scope, nil)
	if err != nil {
		return err
	}
	count := 0
	for _, mode := range modes {
		if mode == "build" {
			count++
		}
	}
	if len(req.ArtifactSources) == 0 {
		count = len(req.Artifacts)
	}
	if count != len(p.Builds) {
		return fmt.Errorf("构建应用数量与代码准备不一致")
	}
	for _, b := range p.Builds {
		if len(req.ArtifactSources) > 0 && modes[b.Service] != "build" {
			return fmt.Errorf("制品来源与代码准备不一致")
		}
		a := s.GetArtifact(b.ArtifactID)
		if err := s.validateArtifactBuildVariant(a, req.Environment); err != nil {
			return err
		}
		if req.Artifacts[b.Service] != b.ArtifactID || a == nil || a.SourceID != b.RunID || a.Source != "pipeline-run" || a.Digest == "" || a.Digest != b.ArtifactDigest || a.Service != b.Service {
			return fmt.Errorf("制品与代码准备不一致，请使用准备任务生成的制品")
		}
		run := s.GetRun(b.RunID)
		var actual string
		s.db.QueryRow(`SELECT commit_ref FROM build_run_snapshots WHERE run_id=?`, b.RunID).Scan(&actual)
		if run == nil || run.Status != "success" || run.Actor != p.Owner || run.Service != b.Service || run.Branch != b.Commit || (run.ArtifactID != b.ArtifactID && !s.pairedCompilation(a, run.ArtifactID)) || actual != b.Commit {
			return fmt.Errorf("制品与代码准备不一致，请使用准备任务生成的制品")
		}
	}
	return nil
}

func registerCodePreparationRoutes(mux *http.ServeMux, s *Store, pr *PipelineRunner) {
	registerPreparationScopeReview(mux, s)
	decode := func(w http.ResponseWriter, r *http.Request, v any) bool {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(v); err != nil {
			deliveryWriteError(w, 400, "代码准备请求无效")
			return false
		}
		return true
	}
	owned := func(w http.ResponseWriter, r *http.Request) *CodePreparation {
		p := s.CodePreparation(r.PathValue("id"))
		if p == nil {
			deliveryWriteError(w, 404, "代码准备不存在")
			return nil
		}
		if !deliveryManage(r, p.Owner) {
			deliveryWriteError(w, 403, "仅发起人或管理员可操作代码准备")
			return nil
		}
		if p.Request.VersionID > 0 {
			if v := s.GetReleaseVersion(p.Request.VersionID); v != nil && !deliveryManage(r, v.Owner) {
				deliveryWriteError(w, 403, "仅版本维护人或管理员可准备版本代码")
				return nil
			}
		}
		return p
	}
	mux.HandleFunc("POST /api/delivery/code-preparations/preview", func(w http.ResponseWriter, r *http.Request) {
		var req CodePreparationRequest
		if !decode(w, r, &req) {
			return
		}
		if req.Delivery.VersionID > 0 {
			if v := s.GetReleaseVersion(req.Delivery.VersionID); v != nil && !deliveryManage(r, v.Owner) {
				deliveryWriteError(w, 403, "仅版本维护人或管理员可准备版本代码")
				return
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		p, err := s.previewCodePreparation(ctx, req, deliveryActor(r))
		if err != nil {
			deliveryWriteError(w, 400, err)
			return
		}
		s.Audit(deliveryActor(r), "preview-code", "code-preparation", p.ID, map[string]any{"scope": p.ScopeDigest})
		deliveryJSON(w, p)
	})
	mux.HandleFunc("GET /api/delivery/code-preparations/{id}", func(w http.ResponseWriter, r *http.Request) {
		if p := owned(w, r); p != nil {
			deliveryJSON(w, p)
		}
	})
	for _, action := range []string{"start", "resume", "cancel"} {
		action := action
		mux.HandleFunc("POST /api/delivery/code-preparations/{id}/"+action, func(w http.ResponseWriter, r *http.Request) {
			p := owned(w, r)
			if p == nil {
				return
			}
			var req struct {
				TargetCommits    []string `json:"target_commits,omitempty"`
				Confirm          bool     `json:"confirm"`
				ConfirmScope     bool     `json:"confirm_scope"`
				ExpectedRevision int64    `json:"expected_revision"`
			}
			if !decode(w, r, &req) {
				return
			}
			if !req.Confirm {
				deliveryWriteError(w, 400, "请明确确认代码操作")
				return
			}
			if req.ExpectedRevision != 0 && req.ExpectedRevision != p.Revision {
				deliveryWriteError(w, 409, "代码任务修订已变化，请重新预览")
				return
			}
			if action == "cancel" {
				if reason := s.preparationCancelBlocker(p); reason != "" {
					deliveryWriteError(w, 409, reason)
					return
				}
				p.State, p.Message = "canceled", "准备任务已取消；已经推送的代码不会自动撤回。"
				if err := s.saveCodePreparation(p); err != nil {
					deliveryWriteError(w, 409, err)
					return
				}
				s.releasePreparationLocks(p)
				s.reconcileResearchPreparation(p.ID)
				s.Audit(deliveryActor(r), "prepare-code-cancel", "code-preparation", p.ID, map[string]any{"preparation_run_id": p.PreparationRunID, "reason": "用户明确终止旧准备，保留冻结记录及既有推送结果"})
				deliveryJSON(w, p)
				return
			}
			if p.Protected && !req.ConfirmScope {
				deliveryWriteError(w, 400, "生产代码准备需要确认源分支仅包含本次选中需求，不能夹带 dev 上其他未上线代码")
				return
			}
			if action == "start" && p.State != "preview" {
				deliveryWriteError(w, 409, "仅未开始的检查任务可开始；不能重复推送")
				return
			}
			if action == "resume" {
				if p.State != "conflict" && p.State != "uncertain" && p.State != "failed" && p.State != "merging" && p.State != "building" {
					deliveryWriteError(w, 409, "当前任务不需要核对恢复")
					return
				}
				if p.LeaseUntil > time.Now().Unix() {
					deliveryWriteError(w, 409, "执行器仍在工作或安全等待期内，不能重复恢复")
					return
				}
			}
			if action == "resume" {
				for i := range p.Repositories {
					p.Repositories[i].ExpectedResultCommit = ""
				}
			}
			if action == "resume" && len(req.TargetCommits) > 0 {
				if len(req.TargetCommits) != len(p.Repositories) {
					deliveryWriteError(w, 400, "必须逐仓库指定解决后的目标提交")
					return
				}
				for i, sha := range req.TargetCommits {
					if !fullBuildCommit.MatchString(sha) {
						deliveryWriteError(w, 400, "目标提交必须是完整SHA")
						return
					}
					if (p.State == "building" || p.Repositories[i].Status == "merged") && sha != p.Repositories[i].ResultCommit {
						deliveryWriteError(w, 409, "已有构建必须继续使用原冻结结果提交")
						return
					}
					p.Repositories[i].ExpectedResultCommit = sha
				}
			}
			if err := s.preparationSourceValid(p); err != nil {
				deliveryWriteError(w, 409, err)
				return
			}
			building := p.State == "building"
			p.State = "merging"
			if building {
				p.State = "building"
			}
			p.LeaseUntil = time.Now().Add(7 * time.Minute).Unix()
			p.Message = "正在核对合并并准备制品；不执行部署 / SQL / Nacos。"
			if err := s.saveCodePreparation(p); err != nil {
				deliveryWriteError(w, 409, err)
				return
			}
			s.reconcileResearchPreparation(p.ID)
			s.Audit(deliveryActor(r), "prepare-code-"+action, "code-preparation", p.ID, map[string]any{"scope": p.ScopeDigest})
			worker := s.CodePreparation(p.ID)
			go func() {
				if building {
					s.performPreparedBuilds(worker, pr)
				} else {
					s.performCodePreparation(worker, pr, action == "resume")
				}
			}()
			deliveryJSON(w, p)
		})
	}
}
