package deploy

// A single-service grant coordinates existing build/release engines. It never
// grants arbitrary writes, approves on somebody's behalf, or replays a dispatch.
import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type ServiceDeploymentSpec struct {
	Service     string `json:"service"`
	Environment string `json:"environment"`
	Mode        string `json:"mode"`
	Branch      string `json:"branch,omitempty"`
	ArtifactID  int64  `json:"artifact_id,omitempty"`
	Reason      string `json:"reason"`
}
type ServiceDeploymentGrant struct {
	Spec                ServiceDeploymentSpec `json:"spec"`
	Commit              string                `json:"commit,omitempty"`
	ArtifactDigest      string                `json:"artifact_digest,omitempty"`
	ConfigurationDigest string                `json:"configuration_digest"`
	TargetDigest        string                `json:"target_digest"`
	Policy              map[string]any        `json:"policy"`
	Targets             []string              `json:"targets"`
	TargetType          string                `json:"target_type"`
	Strategy            string                `json:"strategy"`
	PauseBetweenBatches bool                  `json:"pause_between_batches"`
	Digest              string                `json:"digest"`
}
type ServiceDeployment struct {
	ID             string                 `json:"id"`
	Owner          string                 `json:"owner"`
	Authority      string                 `json:"authority"`
	AuthorityKeyID string                 `json:"authority_key_id,omitempty"`
	Revision       int64                  `json:"revision"`
	Grant          ServiceDeploymentGrant `json:"grant"`
	State          string                 `json:"state"`
	Stage          string                 `json:"stage"`
	Message        string                 `json:"message"`
	LeaseUntil     int64                  `json:"lease_until,omitempty"`
	ExpiresAt      int64                  `json:"expires_at"`
	RunID          int64                  `json:"run_id,omitempty"`
	ReleaseID      int64                  `json:"release_id,omitempty"`
	ArtifactID     int64                  `json:"artifact_id,omitempty"`
	UpdatedAt      int64                  `json:"updated_at"`
	raw            string
}

func (s *Store) serviceDeployment(id string) (*ServiceDeployment, error) {
	if !attachmentIDRE.MatchString(id) {
		return nil, fmt.Errorf("部署任务编号无效")
	}
	var raw string
	if e := s.db.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, "service_deployment:"+id).Scan(&raw); e != nil {
		return nil, e
	}
	var t ServiceDeployment
	if e := json.Unmarshal([]byte(raw), &t); e != nil {
		return nil, e
	}
	t.raw = raw
	return &t, nil
}
func (s *Store) saveServiceDeployment(t *ServiceDeployment) error {
	t.Revision++
	t.UpdatedAt = time.Now().Unix()
	b, e := json.Marshal(t)
	if e != nil {
		return e
	}
	if t.raw == "" {
		_, e = s.db.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?)`, "service_deployment:"+t.ID, string(b))
	} else {
		r, err := s.db.Exec(`UPDATE deploy_meta SET value=? WHERE key=? AND value=?`, string(b), "service_deployment:"+t.ID, t.raw)
		e = err
		if e == nil {
			n, _ := r.RowsAffected()
			if n != 1 {
				e = ErrRunConflict
			}
		}
	}
	if e == nil {
		t.raw = string(b)
	}
	return e
}
func (s *Store) serviceDeploymentTerms(spec ServiceDeploymentSpec) (string, string, map[string]any, error) {
	env := s.GetEnvironment(spec.Environment)
	sv := s.GetService(spec.Service)
	target := s.GetServiceEnvironment(spec.Service, spec.Environment)
	if env == nil || sv == nil || target == nil {
		return "", "", nil, fmt.Errorf("应用或目标环境不存在")
	}
	if env.Protected || env.Purpose == "production" || target.Protected {
		return "", "", nil, fmt.Errorf("一次授权部署仅支持非受保护开发/测试环境；受保护环境请走原审批流程")
	}
	effective, e := s.environmentBuildService(sv, spec.Environment)
	if e != nil {
		return "", "", nil, e
	}
	policy, e := s.applicationCodePolicy(spec.Service, spec.Environment)
	if e != nil {
		return "", "", nil, e
	}
	delete(policy["workflow_binding"].(map[string]any), "checked_at")
	if e := s.freezeDeploymentAgents(target); e != nil {
		return "", "", nil, e
	}
	dest, e := s.deliveryDestinationDigest(*target)
	if e != nil {
		return "", "", nil, e
	}
	// Include encrypted variable material without ever returning it to clients.
	td := digestJSON([]any{target, target.runtimeEnvEncrypted, dest, env})
	config := digestJSON([]any{preparationConfiguration(effective, s.GetServiceDelivery(spec.Service)), policy})
	return config, td, policy, nil
}
func (s *Store) previewServiceDeployment(ctx context.Context, m *ReleaseManager, spec ServiceDeploymentSpec) (*ServiceDeploymentGrant, error) {
	if strings.TrimSpace(spec.Reason) == "" || len(spec.Reason) > 2000 {
		return nil, fmt.Errorf("请填写部署原因（最多2000字符）")
	}
	if spec.Mode != "build" && spec.Mode != "artifact" {
		return nil, fmt.Errorf("请选择 build 或 artifact")
	}
	config, td, policy, e := s.serviceDeploymentTerms(spec)
	if e != nil {
		return nil, e
	}
	target, e := m.ValidateTarget(spec.Service, spec.Environment, spec.Reason)
	if e != nil {
		return nil, e
	}
	g := &ServiceDeploymentGrant{Spec: spec, ConfigurationDigest: config, TargetDigest: td, Policy: policy, Targets: target.ExecutionTargets(), TargetType: target.TargetType, Strategy: target.Strategy, PauseBetweenBatches: target.PauseBetweenBatches}
	if spec.Mode == "artifact" {
		if spec.ArtifactID <= 0 || spec.Branch != "" {
			return nil, fmt.Errorf("已有制品部署只接受明确制品ID，不接受分支")
		}
		a := s.GetArtifact(spec.ArtifactID)
		if a == nil || a.Service != spec.Service || !s.artifactMatchesEnvironment(a, s.GetService(spec.Service), spec.Environment) {
			return nil, fmt.Errorf("制品不属于本次应用或类型不兼容")
		}
		if e = s.artifactBuildComplete(a); e != nil {
			return nil, e
		}
		if e = s.validateArtifactBuildVariant(a, spec.Environment); e != nil {
			return nil, e
		}
		g.ArtifactDigest = a.Digest
	} else {
		if spec.ArtifactID != 0 {
			return nil, fmt.Errorf("构建并发布不能混入已有制品")
		}
		cp := policy["code_policy"].(CodePolicy)
		if policy["requires_scope_resolution"] == true || strings.Contains(cp.TargetBranch, "{") {
			return nil, fmt.Errorf("目标分支依赖需求范围，请使用一次授权的需求/Bug跟进任务")
		}
		branch := cp.TargetBranch
		if branch == "" {
			branch = s.GetService(spec.Service).Branch
		}
		if spec.Branch != "" && cp.TargetBranch != "" && spec.Branch != branch {
			return nil, fmt.Errorf("该环境使用公共目标分支 %s；需求源请经跟进任务合并，不可替代目标", branch)
		}
		if spec.Branch != "" {
			branch = spec.Branch
		}
		if branch == "" {
			return nil, fmt.Errorf("请指定构建分支")
		}
		repository, c, e := s.gitDiscoverySource(GitBranchesRequest{Service: spec.Service})
		if e != nil {
			return nil, e
		}
		refs, e := discoverGitBranches(ctx, repository, c)
		if e != nil {
			return nil, e
		}
		for _, ref := range refs.Branches {
			if ref.Name == branch {
				g.Commit = ref.Commit
				break
			}
		}
		if !fullBuildCommit.MatchString(g.Commit) {
			return nil, fmt.Errorf("未能冻结远端分支 %s 的完整提交", branch)
		}
		g.Spec.Branch = branch
	}
	g.Digest = digestJSON(g)
	return g, nil
}
func (s *Store) advanceServiceDeployment(t *ServiceDeployment, pr *PipelineRunner, m *ReleaseManager) error {
	if t.State != "running" {
		return nil
	}
	stop := func(state, msg string) error { t.State = state; t.Message = msg; return s.saveServiceDeployment(t) }
	spec := t.Grant.Spec
	if (t.Stage == "build_dispatching" || t.Stage == "release_dispatching") && t.LeaseUntil > time.Now().Unix() {
		return nil
	}
	// Observe already-created releases even after grant expiry; no new side effects.
	if t.ReleaseID > 0 {
		o := s.GetReleaseOrder(t.ReleaseID)
		if o == nil || o.Service != spec.Service || o.Environment != spec.Environment || o.Actor != t.Owner || o.ArtifactID != t.ArtifactID {
			return stop("blocked", "原发布身份无法核对；不会重新发布")
		}
		t.Stage = "release"
		t.Message = "原发布单 " + o.DisplayLabel() + "：" + o.Status
		if !releaseStatusActive(o.Status) {
			t.State = o.Status
		}
		return s.saveServiceDeployment(t)
	}
	if t.ExpiresAt <= time.Now().Unix() {
		return stop("blocked", "部署授权已过期；保留原构建结果，请重新核对范围")
	}
	config, td, _, e := s.serviceDeploymentTerms(spec)
	if e != nil {
		return stop("blocked", e.Error())
	}
	if config != t.Grant.ConfigurationDigest || td != t.Grant.TargetDigest {
		return stop("blocked", "构建配置、环境策略或部署目标已变化；未继续发布，请重新预览差异")
	}
	if spec.Mode == "build" {
		if t.Stage == "build_dispatching" && t.RunID == 0 {
			var id int64
			if s.db.QueryRow(`SELECT id FROM pipeline_runs WHERE service=? AND trigger=? AND actor=? ORDER BY id DESC LIMIT 1`, spec.Service, "service-deployment:"+t.ID, t.Owner).Scan(&id) != nil {
				return stop("blocked", "构建派发结果未知；请核对原操作，不会重发")
			}
			t.RunID = id
			t.Stage = "building"
			t.LeaseUntil = 0
			if e = s.saveServiceDeployment(t); e != nil {
				return e
			}
		}
		if t.RunID == 0 {
			t.Stage = "build_dispatching"
			t.LeaseUntil = time.Now().Add(2 * time.Minute).Unix()
			t.Message = "正在启动冻结提交的构建"
			if e = s.saveServiceDeployment(t); e != nil {
				return e
			}
			id, err := pr.StartWithOptions(s.GetService(spec.Service), false, PipelineRunOptions{BuildEnvironment: spec.Environment, Branch: t.Grant.Commit, Trigger: "service-deployment:" + t.ID}, t.Owner)
			if err != nil {
				return stop("blocked", "构建未完成派发核对："+err.Error())
			}
			t.RunID = id
			t.Stage = "building"
			t.LeaseUntil = 0
			return s.saveServiceDeployment(t)
		}
		run := s.GetRun(t.RunID)
		if run == nil || run.Service != spec.Service || run.Actor != t.Owner || run.Trigger != "service-deployment:"+t.ID || run.Branch != t.Grant.Commit {
			return stop("blocked", "原构建身份不匹配")
		}
		if run.Status == "running" {
			return nil
		}
		if run.Status != "success" && run.Status != "waiting_deploy" {
			return stop("failed", "构建未成功，未发布："+run.Status)
		}
		a := s.artifactForEnvironmentRun(run, spec.Environment)
		if a == nil || a.Source != "pipeline-run" || a.SourceID != run.ID {
			return stop("blocked", "未找到本次构建的环境匹配制品")
		}
		var commit string
		if s.db.QueryRow(`SELECT commit_ref FROM build_run_snapshots WHERE run_id=?`, run.ID).Scan(&commit) != nil || commit != t.Grant.Commit {
			return stop("blocked", "实际构建提交与授权冻结提交不同")
		}
		if _, e := s.artifactBuildSnapshot(a); e != nil {
			return stop("blocked", e.Error())
		}
		t.ArtifactID = a.ID
	} else {
		t.ArtifactID = spec.ArtifactID
	}
	a := s.GetArtifact(t.ArtifactID)
	if a == nil || a.Service != spec.Service || (spec.Mode == "artifact" && a.Digest != t.Grant.ArtifactDigest) {
		return stop("blocked", "制品身份或摘要已变化")
	}
	// Serialize competing coordinators before dispatch. Unknown responses retain
	// this state and are never retried; release creation atomically links the task.
	if t.Stage == "release_dispatching" {
		return stop("blocked", "原发布派发结果待核对，不会自动重新发布")
	}
	t.Stage = "release_dispatching"
	t.LeaseUntil = time.Now().Add(2 * time.Minute).Unix()
	t.Message = "已授权，正在创建原制品发布单"
	if e = s.saveServiceDeployment(t); e != nil {
		return e
	}
	before := *t
	_, e = m.createArtifactForTask(spec.Service, a, spec.Environment, spec.Reason, t.Owner, t)
	if e != nil {
		*t = before
		return stop("blocked", e.Error())
	}
	return nil
}
func registerServiceDeploymentRoutes(mux *http.ServeMux, s *Store, pr *PipelineRunner, m *ReleaseManager) {
	decode := func(w http.ResponseWriter, r *http.Request, v any) bool {
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
		d.DisallowUnknownFields()
		if d.Decode(v) != nil {
			deliveryWriteError(w, 400, "部署请求无效")
			return false
		}
		return true
	}
	owned := func(w http.ResponseWriter, r *http.Request) *ServiceDeployment {
		t, e := s.serviceDeployment(r.PathValue("id"))
		if e != nil {
			deliveryWriteError(w, 404, "部署任务不存在")
			return nil
		}
		if t.Owner != deliveryActor(r) {
			deliveryWriteError(w, 403, "仅授权人可查看或推进")
			return nil
		}
		return t
	}
	mux.HandleFunc("POST /api/service-deployments/preview", func(w http.ResponseWriter, r *http.Request) {
		var spec ServiceDeploymentSpec
		if !decode(w, r, &spec) {
			return
		}
		g, e := s.previewServiceDeployment(r.Context(), m, spec)
		if e != nil {
			deliveryWriteError(w, 409, e)
			return
		}
		deliveryJSON(w, g)
	})
	mux.HandleFunc("POST /api/service-deployments", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID             string                `json:"id"`
			Spec           ServiceDeploymentSpec `json:"spec"`
			ExpectedDigest string                `json:"expected_digest"`
		}
		if !decode(w, r, &req) {
			return
		}
		if !attachmentIDRE.MatchString(req.ID) || deliveryActor(r) == "" || deliveryActor(r) == "local" {
			deliveryWriteError(w, 400, "需要本人身份和稳定任务编号")
			return
		}
		if old, e := s.serviceDeployment(req.ID); e == nil {
			if old.Owner != deliveryActor(r) || old.Grant.Digest != req.ExpectedDigest || digestJSON(old.Grant.Spec) != digestJSON(req.Spec) {
				deliveryWriteError(w, 409, "请求编号已用于其他授权")
				return
			}
			deliveryJSON(w, old)
			return
		}
		g, e := s.previewServiceDeployment(r.Context(), m, req.Spec)
		if e != nil {
			deliveryWriteError(w, 409, e)
			return
		}
		if req.ExpectedDigest == "" || g.Digest != req.ExpectedDigest {
			deliveryWriteError(w, 409, "部署范围或分支提交已变化，请重新预览")
			return
		}
		t := &ServiceDeployment{ID: req.ID, Owner: deliveryActor(r), Authority: r.Header.Get("X-Work-Loop-Authority"), AuthorityKeyID: r.Header.Get("X-Work-Loop-Key-ID"), Grant: *g, State: "running", Stage: "authorized", Message: "本次部署已授权，后台自动推进", ExpiresAt: time.Now().Add(time.Hour).Unix()}
		if (t.Authority != "page" && t.Authority != "mcp") || (t.Authority == "mcp" && t.AuthorityKeyID == "") {
			deliveryWriteError(w, 403, "缺少可信授权来源")
			return
		}
		if e = s.saveServiceDeployment(t); e != nil {
			deliveryWriteError(w, 409, e)
			return
		}
		s.Audit(t.Owner, "authorize", "service-deployment", t.ID, map[string]any{"digest": g.Digest})
		deliveryJSON(w, t)
	})
	mux.HandleFunc("GET /api/service-deployments", func(w http.ResponseWriter, r *http.Request) {
		rows, e := s.db.Query(`SELECT value FROM deploy_meta WHERE key LIKE 'service_deployment:%'`)
		if e != nil {
			deliveryWriteError(w, 500, e)
			return
		}
		defer rows.Close()
		out := []ServiceDeployment{}
		for rows.Next() {
			var raw string
			var t ServiceDeployment
			if rows.Scan(&raw) == nil && json.Unmarshal([]byte(raw), &t) == nil && t.Owner == deliveryActor(r) && t.State == "running" {
				out = append(out, t)
			}
		}
		deliveryJSON(w, out)
	})
	mux.HandleFunc("GET /api/service-deployments/{id}", func(w http.ResponseWriter, r *http.Request) {
		if t := owned(w, r); t != nil {
			deliveryJSON(w, t)
		}
	})
	mux.HandleFunc("POST /api/service-deployments/{id}/advance", func(w http.ResponseWriter, r *http.Request) {
		t := owned(w, r)
		if t == nil {
			return
		}
		var req struct {
			Revision int64 `json:"revision"`
		}
		if !decode(w, r, &req) {
			return
		}
		if req.Revision != t.Revision {
			deliveryWriteError(w, 409, ErrRunConflict)
			return
		}
		if t.Authority != r.Header.Get("X-Work-Loop-Authority") || t.AuthorityKeyID != r.Header.Get("X-Work-Loop-Key-ID") {
			deliveryWriteError(w, 403, "请使用原授权身份推进")
			return
		}
		if e := s.advanceServiceDeployment(t, pr, m); e != nil {
			deliveryWriteError(w, 409, e)
			return
		}
		deliveryJSON(w, t)
	})
	mux.HandleFunc("POST /internal/service-deployments/{id}/blocked", func(w http.ResponseWriter, r *http.Request) {
		t := owned(w, r)
		if t == nil {
			return
		}
		var req struct {
			Revision int64  `json:"revision"`
			Reason   string `json:"reason"`
		}
		if !decode(w, r, &req) {
			return
		}
		if req.Revision != t.Revision {
			deliveryWriteError(w, 409, ErrRunConflict)
			return
		}
		t.State = "blocked"
		t.Message = req.Reason
		if e := s.saveServiceDeployment(t); e != nil {
			deliveryWriteError(w, 409, e)
			return
		}
		deliveryJSON(w, t)
	})
}
