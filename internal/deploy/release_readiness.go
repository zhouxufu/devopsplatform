package deploy

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"
)

// Readiness is an advisory metadata observation, never a preflight/approval token.
// Deliberately do not reuse ResolveDelivery: it can perform remote preflight reads.
type ReadinessCheck struct {
	Key      string `json:"key"`
	Status   string `json:"status"`
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	Action   string `json:"action,omitempty"`
	Service  string `json:"service,omitempty"`
	ChangeID int64  `json:"change_id,omitempty"`
}
type ReleaseCodeEvidence struct {
	Service        string           `json:"service"`
	ChangeIDs      []int64          `json:"change_ids"`
	Status         string           `json:"status"`
	ArtifactID     int64            `json:"artifact_id"`
	ArtifactDigest string           `json:"artifact_digest,omitempty"`
	BuildID        int64            `json:"build_id,omitempty"`
	Commit         string           `json:"commit,omitempty"`
	Branch         string           `json:"branch,omitempty"`
	PreparationID  string           `json:"preparation_id,omitempty"`
	Sources        []PreparedSource `json:"sources"`
	Detail         string           `json:"detail"`
}
type ReleaseReadiness struct {
	VersionID   int64                 `json:"version_id"`
	Environment string                `json:"environment"`
	CheckedAt   int64                 `json:"checked_at"`
	ScopeDigest string                `json:"scope_digest"`
	Advisory    bool                  `json:"advisory"`
	Checks      []ReadinessCheck      `json:"checks"`
	Code        []ReleaseCodeEvidence `json:"code"`
	Counts      map[string]int        `json:"counts"`
}

// Only preparations explicitly associated with this version may supply evidence.
// A successful arbitrary build or a matching branch name is not coverage proof.
func (s *Store) readinessPreparationRuns(v *ReleaseVersion, environments ...string) []*DeliveryRun {
	out := []*DeliveryRun{}
	var id int64
	key := fmt.Sprintf("version-workflow-preparation:%d", v.ID)
	if len(environments) > 0 && environments[0] != "" {
		s.db.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, key+":"+environments[0]).Scan(&id)
	}
	if id > 0 || s.db.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, key).Scan(&id) == nil {
		r := s.DeliveryRun(id)
		if r != nil && r.Status == "success" && r.Plan.PreparationRequest != nil && r.Plan.Version != nil && r.Plan.Version.ID == v.ID && s.checkResearchScope(r) == nil {
			out = append(out, r)
		}
	}
	return out
}

func (s *Store) readinessPreparations(v *ReleaseVersion, environments ...string) []*CodePreparation {
	result := []*CodePreparation{}
	scope := &DeliveryScope{Type: "version", WorkItems: v.WorkItems, Changes: v.Changes}
	seen := map[string]bool{}
	for _, r := range s.readinessPreparationRuns(v, environments...) {
		for _, step := range r.Steps {
			if (step.Kind != "code-prepare" && step.Kind != "code-verify") || step.Status != "success" || seen[step.Detail] {
				continue
			}
			seen[step.Detail] = true
			p := s.CodePreparation(step.Detail)
			if p == nil || p.Request.VersionID != v.ID {
				continue
			}
			identitiesValid := true
			for _, repo := range p.Repositories {
				url, connection, err := s.gitDiscoverySource(GitBranchesRequest{Service: repo.Service})
				if err != nil || preparationGitIdentity(url, connection) != repo.ConnectionDigest {
					identitiesValid = false
				}
			}
			if !identitiesValid {
				continue
			}
			req := p.Request
			req.CodePreparationID = p.ID
			req.Artifacts = map[string]int64{}
			for _, b := range p.Builds {
				req.Artifacts[b.Service] = b.ArtifactID
			}
			if s.validatePreparedArtifacts(req, scope) == nil {
				result = append(result, p)
			}
		}
	}
	return result
}

func (s *Store) releaseCodeEvidence(v *ReleaseVersion, bindings VersionArtifacts) []ReleaseCodeEvidence {
	apps := map[string][]Change{}
	for _, c := range v.Changes {
		if c.Kind == "code" || c.Kind == "artifact" {
			apps[c.Service] = append(apps[c.Service], c)
		}
	}
	names := []string{}
	for name := range apps {
		names = append(names, name)
	}
	sort.Strings(names)
	preparations := s.readinessPreparations(v, bindings.Environment)
	out := []ReleaseCodeEvidence{}
	for _, service := range names {
		e := ReleaseCodeEvidence{Service: service, Status: "blocked", ChangeIDs: []int64{}, Sources: []PreparedSource{}, Detail: "尚未固定此应用的版本制品"}
		for _, c := range apps[service] {
			e.ChangeIDs = append(e.ChangeIDs, c.ID)
		}
		var binding *VersionArtifactBinding
		count := 0
		for i := range bindings.Bindings {
			if bindings.Bindings[i].Service == service {
				binding = &bindings.Bindings[i]
				count++
			}
		}
		if count == 1 {
			e.ArtifactID, e.ArtifactDigest, e.BuildID, e.Branch = binding.ArtifactID, binding.Digest, binding.BuildID, binding.Branch
			a, sv := s.GetArtifact(binding.ArtifactID), s.GetService(service)
			if bindings.ScopeDigest != versionCodeScope(v) {
				e.Detail = "版本代码范围已变化，请重新固定制品"
			} else if a == nil || sv == nil || a.Service != service || a.Digest == "" || a.Digest != binding.Digest || !s.artifactMatchesEnvironment(a, sv, bindings.Environment) {
				e.Detail = "制品、摘要或应用类型已变化，请重新核对"
			} else {
				e.Status, e.Detail = "pending", "制品摘要已固定，缺少完整构建 Commit 或代码范围证明"
				if binding.CodeConfirmed {
					e.Status, e.Detail = "manual", "仅有人工代码包含确认，尚无可核对的自动代码范围证明"
				}
				validBuild := false
				if a.Source == "pipeline-run" {
					r := s.GetRun(a.SourceID)
					if r == nil || r.Status != "success" || r.Service != service || (r.ArtifactID != a.ID && !s.pairedCompilation(a, r.ArtifactID)) || binding.BuildID != r.ID {
						e.Status, e.Detail = "blocked", "来源构建未成功或制品关联已变化"
					} else {
						e.Commit = s.buildCommit(r.ID)
						validBuild = fullBuildCommit.MatchString(e.Commit)
						if !validBuild {
							e.Commit = ""
							e.Status, e.Detail = "pending", "历史构建缺少完整 Commit；不能用短版本号或分支名证明代码覆盖"
						}
					}
				}
				if validBuild && e.Status != "blocked" {
					for _, p := range preparations {
						for _, b := range p.Builds {
							if b.Service != service || b.ArtifactID != a.ID || b.ArtifactDigest != a.Digest || b.RunID != e.BuildID || b.Commit != e.Commit {
								continue
							}
							covered := map[int64]bool{}
							sources := []PreparedSource{}
							for _, repo := range p.Repositories {
								belongs := repo.Service == service
								for _, name := range repo.Services {
									belongs = belongs || name == service
								}
								if !belongs || repo.ResultCommit != e.Commit || !fullBuildCommit.MatchString(repo.ResultCommit) {
									continue
								}
								for _, src := range repo.Sources {
									if !fullBuildCommit.MatchString(src.Commit) {
										continue
									}
									ids := []int64{}
									for _, c := range apps[service] {
										for _, id := range src.Changes {
											if id == c.ID && c.Kind == "code" && c.Reference == src.Branch {
												covered[id] = true
												ids = append(ids, id)
											}
										}
									}
									if len(ids) > 0 {
										sources = append(sources, PreparedSource{Branch: src.Branch, Commit: src.Commit, Changes: ids})
									}
								}
							}
							if len(covered) == len(apps[service]) {
								e.Status, e.Detail = "pass", "已核对本版本变更 → 准备分支 Commit → 成功构建 → 固定制品摘要"
								e.PreparationID = p.ID
								e.Sources = sources
							}
						}
					}
				}
			}
		} else if count > 1 {
			e.Detail = "同一应用存在重复制品映射，不能确定执行制品"
		}
		out = append(out, e)
	}
	return out
}

func (s *Store) ReleaseReadiness(id int64, environment string) (*ReleaseReadiness, error) {
	v := s.GetReleaseVersion(id)
	if v == nil {
		return nil, fmt.Errorf("发布版本不存在")
	}
	var env *Environment
	if environment != "" {
		env = s.GetEnvironment(environment)
		if env == nil {
			return nil, fmt.Errorf("目标环境不存在")
		}
	}
	out := &ReleaseReadiness{VersionID: id, Environment: environment, CheckedAt: time.Now().Unix(), ScopeDigest: digestJSON([]any{v.WorkItems, v.Changes}), Advisory: true, Checks: []ReadinessCheck{}, Counts: map[string]int{"pass": 0, "blocked": 0, "pending": 0, "manual": 0}}
	add := func(key, status, title, detail, action, service string, changeID int64) {
		out.Checks = append(out.Checks, ReadinessCheck{key, status, title, detail, action, service, changeID})
		out.Counts[status]++
	}
	if len(v.Changes) == 0 {
		add("scope", "blocked", "交付范围", "尚无实施变更；可关联工作项或独立变更", "scope", "", 0)
	} else {
		add("scope", "pass", "交付范围", fmt.Sprintf("%d 个工作项 · %d 项变更", len(v.WorkItems), len(v.Changes)), "", "", 0)
	}
	for _, w := range v.WorkItems {
		if v.Status == ReleaseVersionDraft {
			var added int
			err := s.db.QueryRow(`SELECT COUNT(*) FROM changes WHERE id IN (`+deliveryOwnedChanges("?")+`) AND id NOT IN (SELECT change_id FROM release_version_changes WHERE release_version_id=?)`, w.ID, id).Scan(&added)
			if err != nil {
				add(fmt.Sprintf("new-changes-%d", w.ID), "pending", "关联范围暂未核对", fmt.Sprintf("工作项 #%d 的最新变更读取失败，请重试", w.ID), "scope", "", 0)
			} else if added > 0 {
				add(fmt.Sprintf("new-changes-%d", w.ID), "blocked", "关联范围有新增内容", fmt.Sprintf("工作项 #%d 新增 %d 项变更；请刷新关联范围并重新核对", w.ID, added), "scope", "", 0)
			}
		}
		covered := false
		for _, c := range v.Changes {
			for _, linked := range c.WorkItems {
				covered = covered || linked.ID == w.ID
			}
		}
		if !covered {
			add(fmt.Sprintf("work-%d", w.ID), "blocked", "工作项尚无交付内容", fmt.Sprintf("#%d · %s", w.ID, w.Title), "scope", "", 0)
		}
	}
	state, detail := "pass", "版本范围已冻结"
	if v.Status == ReleaseVersionDraft {
		state, detail = "blocked", "仍为草稿，需完成范围核对并冻结"
	} else if v.Status == ReleaseVersionCanceled {
		state, detail = "blocked", "版本已取消"
	}
	add("freeze", state, "版本范围", detail, "scope", "", 0)
	for _, c := range v.Changes {
		if c.Status != ChangeReady {
			add(fmt.Sprintf("change-%d", c.ID), "blocked", "变更未就绪", fmt.Sprintf("#%d · %s", c.ID, c.Title), "change", c.Service, c.ID)
		}
	}
	out.Code = s.releaseCodeEvidence(v, s.VersionArtifacts(id, environment))
	for _, e := range out.Code {
		add("code-"+e.Service, e.Status, e.Service+" · 代码与制品", e.Detail, "artifact", e.Service, 0)
		if env != nil {
			for _, b := range s.VersionArtifacts(id, environment).Bindings {
				if b.Service == e.Service {
					if err := s.validateArtifactBuildVariant(s.GetArtifact(b.ArtifactID), environment); err != nil {
						add("build-variant-"+e.Service, "blocked", e.Service+" · 环境构建方案", err.Error(), "artifact", e.Service, 0)
					}
				}
			}
			target := s.GetServiceEnvironment(e.Service, environment)
			if target == nil || (!target.ManualTarget() && len(target.ExecutionTargets()) == 0) {
				add("target-"+e.Service, "blocked", e.Service+" · 部署目标", "当前环境未配置有效部署目标；配置存在也不代表可达", "environment", e.Service, 0)
			} else if target.ManualTarget() {
				add("target-"+e.Service, "pass", e.Service+" · 手动发布配置", "已配置人工发布方式；实际发布与验证结果仍待登记", "environment", e.Service, 0)
			} else {
				add("target-"+e.Service, "pass", e.Service+" · 部署目标", "已配置目标；连接、权限与运行状态仍需正式预检", "environment", e.Service, 0)
			}
		}
	}
	if len(out.Code) > 0 {
		action := "preflight"
		add("exclusive-scope", "manual", "代码范围复核", "分支或合并关系不能证明没有夹带其他需求；请复核实际差异与测试结果", action, "", 0)
	}
	if env == nil {
		add("environment", "pending", "目标环境", "选择环境以核对部署目标和逻辑资源；不会将版本绑定到环境", "", "", 0)
	} else {
		for i := range v.Changes {
			c := &v.Changes[i]
			if c.Kind != "mysql" && c.Kind != "nacos" && c.Kind != "redis" {
				continue
			}
			t, err := s.deliveryChangeTarget(c, environment)
			if err != nil {
				add(fmt.Sprintf("resource-%d", c.ID), "blocked", "环境资源未就绪", fmt.Sprintf("变更 #%d · %s：请核对逻辑资源、连接及环境参数", c.ID, c.Title), "change", c.Service, c.ID)
			} else if t.Skip {
				add(fmt.Sprintf("resource-%d", c.ID), "pass", "SQL 环境例外", fmt.Sprintf("变更 #%d 在此环境明确跳过；执行前仍会核对冻结规则", c.ID), "change", c.Service, c.ID)
			} else {
				add(fmt.Sprintf("resource-%d", c.ID), "pass", "环境资源已解析", fmt.Sprintf("变更 #%d · %s；尚未连接远端预检", c.ID, c.Kind), "change", c.Service, c.ID)
			}
		}
	}
	add("preflight", "pending", "正式交付预检与审批", "此处只读核对平台元数据，不验证远端表结构、配置冲突或命令，也不替代正式执行计划和审批", "preflight", "", 0)
	add("tests", "pending", "测试与运行证据", "尚未接入自动测试/运行健康证明；不能由 Bug 状态、构建成功或无监控数据推断通过", "", "", 0)
	return out, nil
}

func registerReleaseReadinessRoutes(mux *http.ServeMux, s *Store) {
	mux.HandleFunc("GET /api/release-versions/{id}/readiness", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			deliveryWriteError(w, 400, "版本编号无效")
			return
		}
		if s.GetReleaseVersion(id) == nil {
			deliveryWriteError(w, 404, "发布版本不存在")
			return
		}
		out, err := s.ReleaseReadiness(id, r.URL.Query().Get("environment"))
		if err != nil {
			deliveryWriteError(w, 400, err)
			return
		}
		deliveryJSON(w, out)
	})
}
