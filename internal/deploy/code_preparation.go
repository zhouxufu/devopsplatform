package deploy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

type CodePolicy struct {
	Mode         string `json:"mode"`
	BaseBranch   string `json:"base_branch"`
	TargetBranch string `json:"target_branch"`
}
type PreparedSource struct {
	Branch  string  `json:"branch"`
	Commit  string  `json:"commit"`
	Changes []int64 `json:"change_ids"`
}
type PreparedRepository struct {
	URL                  string           `json:"url"`
	Service              string           `json:"service"`
	Services             []string         `json:"services"`
	Policy               CodePolicy       `json:"policy"`
	ConnectionDigest     string           `json:"connection_digest"`
	BaseBranch           string           `json:"base_branch"`
	BaseCommit           string           `json:"base_commit"`
	TargetBranch         string           `json:"target_branch"`
	TargetCommit         string           `json:"target_commit"`
	ExpectedResultCommit string           `json:"expected_result_commit,omitempty"`
	ResultCommit         string           `json:"result_commit"`
	Sources              []PreparedSource `json:"sources"`
	Conflicts            []string         `json:"conflicts,omitempty"`
	Status               string           `json:"status"`
}
type PreparedBuild struct {
	Message             string `json:"message,omitempty"`
	RunNumber           int64  `json:"run_number,omitempty"`
	Service             string `json:"service"`
	Commit              string `json:"commit"`
	ConfigurationDigest string `json:"configuration_digest"`
	RunID               int64  `json:"run_id"`
	ArtifactID          int64  `json:"artifact_id"`
	ArtifactDigest      string `json:"artifact_digest"`
	Status              string `json:"status"`
}
type CodePreparation struct {
	BuildError       string               `json:"build_error,omitempty"`
	BuildConcurrency int                  `json:"build_concurrency,omitempty"`
	PreparationRunID int64                `json:"preparation_run_id,omitempty"`
	PolicyRevision   int64                `json:"policy_revision,omitempty"`
	VerifyOnly       bool                 `json:"verify_only,omitempty"`
	ID               string               `json:"id"`
	Owner            string               `json:"owner"`
	Revision         int64                `json:"revision"`
	State            string               `json:"state"`
	Message          string               `json:"message"`
	UpdatedAt        int64                `json:"updated_at"`
	LeaseUntil       int64                `json:"lease_until"`
	Request          DeliveryRequest      `json:"request"`
	ScopeDigest      string               `json:"scope_digest"`
	Protected        bool                 `json:"protected"`
	Repositories     []PreparedRepository `json:"repositories"`
	Builds           []PreparedBuild      `json:"builds"`
	raw              string
}
type CodePreparationRequest struct {
	BuildConcurrency int                   `json:"build_concurrency,omitempty"`
	VerifyOnly       bool                  `json:"verify_only,omitempty"`
	Delivery         DeliveryRequest       `json:"delivery"`
	Policies         map[string]CodePolicy `json:"policies"`
}

func codeScopeDigest(scope *DeliveryScope) string {
	items := []any{}
	if scope != nil {
		for _, c := range scope.Changes {
			if c.Kind == "code" || c.Kind == "artifact" {
				items = append(items, []any{c.ID, c.Kind, c.Service, c.Reference})
			}
		}
	}
	return digestJSON(items)
}
func preparationGitIdentity(url string, c *ResourceConnection) string {
	if c == nil {
		return digestJSON([]any{url})
	}
	return digestJSON([]any{url, c.Name, c.Endpoint, c.Username, c.Config})
}
func preparationConfiguration(sv *Service, d *ServiceDelivery) string {
	if d == nil {
		return digestJSON([]any{sv})
	}
	copy := *d
	copy.WebhookToken, copy.AutoDeployEnvironment = "", ""
	copy.LastScheduleAt, copy.UpdatedAt, copy.ScheduleMinutes = 0, 0, 0
	return digestJSON([]any{sv, copy})
}

// Task IDs are opaque identifiers; unlike environment/resource names, a random
// hexadecimal ID may start with a digit. Keep the resource-name rules unchanged.
var codePreparationIDRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

func (s *Store) CodePreparation(id string) *CodePreparation {
	if !codePreparationIDRE.MatchString(id) {
		return nil
	}
	var raw string
	if s.db.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, "code_preparation:"+id).Scan(&raw) != nil {
		return nil
	}
	var p CodePreparation
	if json.Unmarshal([]byte(raw), &p) != nil {
		return nil
	}
	p.raw = raw
	for i := range p.Builds {
		p.Builds[i].RunNumber = s.PipelineRunNumber(p.Builds[i].RunID, p.Builds[i].Service)
	}
	return &p
}
func (s *Store) saveCodePreparation(p *CodePreparation) error {
	p.Revision++
	p.UpdatedAt = time.Now().Unix()
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ids := []int64{}
	for _, build := range p.Builds {
		ids = append(ids, build.ArtifactID)
	}
	for _, id := range p.Request.Artifacts {
		ids = append(ids, id)
	}
	if err := lockArtifactReferences(tx, ids...); err != nil {
		return err
	}
	if p.raw == "" {
		_, err = tx.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?)`, "code_preparation:"+p.ID, string(raw))
	} else {
		r, e := tx.Exec(`UPDATE deploy_meta SET value=? WHERE key=? AND value=?`, string(raw), "code_preparation:"+p.ID, p.raw)
		err = e
		if err == nil {
			n, _ := r.RowsAffected()
			if n != 1 {
				err = ErrRunConflict
			}
		}
	}
	if err == nil {
		err = tx.Commit()
	}
	if err == nil {
		p.raw = string(raw)
	}
	return err
}
func codeChangeReadinessError(change Change) error {
	if change.Status == ChangeDraft {
		return fmt.Errorf("代码变更 #%d 仍为草稿，尚未标记就绪。请先核对变更内容并标记就绪，再重新检查代码来源。", change.ID)
	}
	return fmt.Errorf("代码变更 #%d 当前状态无法确认为已就绪。请打开变更核对状态后重新检查代码来源。", change.ID)
}

func (s *Store) previewCodePreparation(ctx context.Context, req CodePreparationRequest, actor string) (*CodePreparation, error) {
	if req.BuildConcurrency < 0 || req.BuildConcurrency > 8 {
		return nil, fmt.Errorf("构建并发数须为 1–8")
	}
	if req.BuildConcurrency == 0 {
		req.BuildConcurrency = 2
	}
	tpl, strategyErr := s.deliveryStrategyTemplate(req.Delivery)
	env := s.GetEnvironment(req.Delivery.Environment)
	if strategyErr != nil || tpl == nil || (tpl.Workbench != "" && tpl.Workbench != "research") || env == nil {
		return nil, fmt.Errorf("请选择部署编排和目标环境")
	}
	if req.Delivery.VersionID > 0 && s.VersionArtifacts(req.Delivery.VersionID, req.Delivery.Environment).Locked {
		return nil, fmt.Errorf("版本制品已锁定，请复用现有制品；新增代码需创建新版本")
	}
	scope, version, err := s.resolveDeliveryScope(req.Delivery, tpl)
	if err != nil {
		return nil, err
	}
	if scope == nil {
		return nil, fmt.Errorf("自动代码准备仅支持版本或工作项范围")
	}
	modes, err := deliveryArtifactSources(req.Delivery, scope, nil)
	if err != nil {
		return nil, err
	}
	policySnapshot, err := s.deliveryPolicy(env.Name)
	if err != nil {
		return nil, err
	}
	bytes := make([]byte, 16)
	if _, err = rand.Read(bytes); err != nil {
		return nil, err
	}
	p := &CodePreparation{BuildConcurrency: req.BuildConcurrency, ID: hex.EncodeToString(bytes), Owner: actor, State: "preview", Request: req.Delivery, ScopeDigest: codeScopeDigest(scope), Protected: env.Protected, Repositories: []PreparedRepository{}, Builds: []PreparedBuild{}}
	p.PolicyRevision = policySnapshot.Revision
	p.Protected = env.Protected || env.Purpose == "production"
	p.Request.PreflightToken = ""
	p.VerifyOnly = req.VerifyOnly
	p.Request.Artifacts = nil
	p.Request.CodePreparationID = ""
	group := map[string]int{}
	sessions := map[string]*gitSession{}
	defer func() {
		for _, g := range sessions {
			g.close()
		}
	}()
	codeCount := 0
	for _, c := range scope.Changes {
		if c.Kind == "code" {
			codeCount++
		}
	}
	if codeCount > 100 {
		return nil, fmt.Errorf("单次代码准备最多 100 个代码变更，请缩小范围")
	}
	apps := map[string]bool{}
	for _, change := range scope.Changes {
		if len(req.Delivery.ArtifactSources) > 0 && modes[change.Service] != "build" {
			continue
		}
		if change.Kind != "code" {
			if change.Kind == "artifact" {
				return nil, fmt.Errorf("制品引用请手工选择；不能与自动合并范围混用")
			}
			continue
		}
		if change.Status != ChangeReady {
			return nil, codeChangeReadinessError(change)
		}
		sv := s.GetService(change.Service)
		if sv == nil {
			return nil, fmt.Errorf("代码应用不存在")
		}
		if _, e := s.preparedBuildService(&PreparedBuild{Service: sv.Name, Commit: strings.Repeat("a", 40)}); e != nil {
			return nil, e
		}
		if !apps[sv.Name] && len(apps) >= 20 {
			return nil, fmt.Errorf("代码准备最多 20 个应用")
		}
		if sv.PreCmd != "" {
			return nil, fmt.Errorf("应用 %s 的前置命令可能改变源码，自动准备暂不支持", sv.Name)
		}
		policy := req.Policies[sv.Name]
		if policy == (CodePolicy{}) {
			policy = policySnapshot.Defaults.Applications[sv.Name].CodePolicy
		}
		if policy.Mode != "integration" && policy.Mode != "isolated" {
			return nil, fmt.Errorf("应用 %s 请选择常驻分支或独立发布分支", sv.Name)
		}
		policy, err = expandCodePolicy(policy, scope, version)
		if err != nil {
			return nil, fmt.Errorf("应用 %s：%w", sv.Name, err)
		}
		if policy.Mode == "integration" {
			policy.BaseBranch = policy.TargetBranch
		}
		if !validPreparationBranch(policy.BaseBranch) || !validPreparationBranch(policy.TargetBranch) || !validPreparationBranch(change.Reference) {
			return nil, fmt.Errorf("应用 %s 的基线、目标和关联源分支必须是有效分支名", sv.Name)
		}
		if (env.Protected || env.Purpose == "production") && (policy.Mode != "isolated" || (!strings.HasPrefix(policy.TargetBranch, "release/") && !strings.HasPrefix(policy.TargetBranch, "hotfix/"))) {
			return nil, fmt.Errorf("受保护环境只能从明确生产基线准备新的 release/ 或 hotfix/ 分支，不能使用常驻 dev 分支")
		}
		url, c, e := s.gitDiscoverySource(GitBranchesRequest{Service: sv.Name})
		if e != nil {
			return nil, e
		}
		key := url + "\x00" + policy.TargetBranch
		index, exists := group[key]
		if !exists {
			if len(group) >= 10 {
				return nil, fmt.Errorf("代码准备最多 10 个仓库目标")
			}
			g, e := newGitSession(url, c)
			if e != nil {
				return nil, e
			}
			sessions[key] = g
			e = g.clone(ctx, url)
			if e != nil {
				return nil, e
			}
			base, e := g.branch(ctx, policy.BaseBranch)
			if e != nil {
				return nil, fmt.Errorf("基线分支不存在或无法读取")
			}
			target, _ := g.branch(ctx, policy.TargetBranch)
			if policy.Mode == "isolated" && target != "" && !p.VerifyOnly {
				return nil, fmt.Errorf("独立发布分支已存在，请使用新的分支名称")
			}
			if p.VerifyOnly && target == "" {
				return nil, fmt.Errorf("人工合入核对要求目标分支已经存在")
			}
			if policy.Mode == "integration" && target == "" {
				return nil, fmt.Errorf("常驻目标分支必须已存在")
			}
			index = len(p.Repositories)
			group[key] = index
			p.Repositories = append(p.Repositories, PreparedRepository{URL: url, Service: sv.Name, Policy: policy, BaseBranch: policy.BaseBranch, BaseCommit: base, TargetBranch: policy.TargetBranch, TargetCommit: target, ConnectionDigest: preparationGitIdentity(url, c), Status: "pending", Services: []string{}, Sources: []PreparedSource{}})
		}
		r := &p.Repositories[index]
		if r.Policy != policy || r.ConnectionDigest != preparationGitIdentity(url, c) {
			return nil, fmt.Errorf("同仓库同目标分支的应用必须使用一致基线、策略和 Git 凭据")
		}
		found := false
		for i := range r.Sources {
			if r.Sources[i].Branch == change.Reference {
				r.Sources[i].Changes = append(r.Sources[i].Changes, change.ID)
				found = true
			}
		}
		if !found {
			g := sessions[key]
			commit, e := g.branch(ctx, change.Reference)
			if e != nil {
				return nil, fmt.Errorf("关联源分支不存在或无法读取")
			}
			r.Sources = append(r.Sources, PreparedSource{Branch: change.Reference, Commit: commit, Changes: []int64{change.ID}})
		}
		if !apps[sv.Name] {
			configuration, e := s.preparationEnvironmentConfiguration(sv, req.Delivery.Environment)
			if e != nil {
				return nil, e
			}
			apps[sv.Name] = true
			r.Services = append(r.Services, sv.Name)
			p.Builds = append(p.Builds, PreparedBuild{Service: sv.Name, ConfigurationDigest: configuration, Status: "pending"})
		}
	}
	if len(p.Repositories) == 0 || len(p.Repositories) > 10 || len(p.Builds) > 20 {
		return nil, fmt.Errorf("代码准备支持 1–10 个仓库、最多 20 个应用")
	}
	for i := range p.Repositories {
		sort.Slice(p.Repositories[i].Sources, func(a, b int) bool { return p.Repositories[i].Sources[a].Branch < p.Repositories[i].Sources[b].Branch })
	}
	p.Message = "源提交和目标基线已冻结；请核对代码范围，再明确开始合并。尚未推送或构建。"
	return p, s.saveCodePreparation(p)
}

func (s *Store) preparationSourceValid(p *CodePreparation) error {
	if p.PreparationRunID > 0 {
		run := s.DeliveryRun(p.PreparationRunID)
		if run == nil || run.Plan.PreparationRequest == nil || run.Owner != p.Owner || (run.Status != "running" && run.Status != "approved" && run.Status != "blocked" && !(p.State == "ready" && run.Status == "success")) {
			return fmt.Errorf("关联交付准备任务不可执行，请核对原流程")
		}
		if err := s.checkResearchScope(run); err != nil {
			return err
		}
		linked := false
		for _, step := range run.Steps {
			if step.Detail == p.ID && (step.Kind == "code-prepare" || step.Kind == "code-verify") {
				current, _ := researchNextNode(run)
				linked = (p.State == "ready" && step.Status == "success") || (current != nil && current.NodeID == step.NodeID && (p.State == "ready" || step.Status == "running" || step.Status == "blocked"))
			}
		}
		if !linked {
			return fmt.Errorf("代码任务不属于当前交付节点")
		}
	}
	tpl, strategyErr := s.deliveryStrategyTemplate(p.Request)
	if strategyErr != nil || tpl == nil {
		return fmt.Errorf("原编排已删除或交付方式不正确")
	}
	if p.State != "ready" && p.Request.VersionID > 0 && s.VersionArtifacts(p.Request.VersionID, p.Request.Environment).Locked {
		return fmt.Errorf("版本制品已锁定，不再继续准备；请复用已锁定制品")
	}
	env := s.GetEnvironment(p.Request.Environment)
	if env == nil || (env.Protected || env.Purpose == "production") != p.Protected {
		return fmt.Errorf("环境保护策略变化，请重新检查")
	}
	policy, err := s.deliveryPolicy(env.Name)
	if err != nil {
		return err
	}
	if policy.Revision != p.PolicyRevision {
		return fmt.Errorf("环境默认流程或审核策略变化，请重新检查代码准备")
	}
	scope, _, err := s.resolveDeliveryScope(p.Request, tpl)
	if err != nil {
		return err
	}
	if scope == nil || codeScopeDigest(scope) != p.ScopeDigest {
		return fmt.Errorf("代码范围变化，请重新检查代码准备")
	}
	for _, c := range scope.Changes {
		if c.Kind == "code" && c.Status != ChangeReady {
			return codeChangeReadinessError(c)
		}
	}
	for _, b := range p.Builds {
		sv := s.GetService(b.Service)
		configuration, e := s.preparationEnvironmentConfiguration(sv, p.Request.Environment)
		if sv == nil || e != nil || configuration != b.ConfigurationDigest {
			return fmt.Errorf("应用 %s 的构建配置变化，请重新检查", b.Service)
		}
	}
	return nil
}
func (s *Store) preparationGit(r *PreparedRepository) (*gitSession, error) {
	url, c, err := s.gitDiscoverySource(GitBranchesRequest{Service: r.Service})
	if err != nil {
		return nil, err
	}
	if preparationGitIdentity(url, c) != r.ConnectionDigest {
		return nil, fmt.Errorf("仓库或 Git 连接身份变化，请重新检查")
	}
	return newGitSession(url, c)
}
func (s *Store) acquirePreparationLocks(p *CodePreparation) error {
	for i := range p.Repositories {
		r := &p.Repositories[i]
		key := "code_merge_lock:" + digestJSON([]string{r.URL, r.TargetBranch})
		value := p.ID
		_, err := s.db.Exec(`INSERT OR IGNORE INTO deploy_meta(key,value) VALUES(?,?)`, key, value)
		if err != nil {
			return err
		}
		var owner string
		if err = s.db.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, key).Scan(&owner); err != nil {
			return err
		}
		if owner != p.ID {
			return fmt.Errorf("该仓库目标分支被代码准备 %s 占用，请先核对或取消原任务", owner)
		}
	}
	return nil
}
func (s *Store) releasePreparationLocks(p *CodePreparation) {
	for _, r := range p.Repositories {
		s.db.Exec(`DELETE FROM deploy_meta WHERE key=? AND value=?`, "code_merge_lock:"+digestJSON([]string{r.URL, r.TargetBranch}), p.ID)
	}
}

func (s *Store) performCodePreparation(p *CodePreparation, pr *PipelineRunner, resume bool) {
	defer s.reconcileResearchPreparation(p.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	fail := func(err error) {
		p.State = "failed"
		for _, r := range p.Repositories {
			if r.Status == "conflict" {
				p.State = "conflict"
			}
			if r.Status == "publishing" {
				p.State = "uncertain"
			}
		}
		p.Message = err.Error()
		p.LeaseUntil = 0
		s.saveCodePreparation(p)
	}
	if err := s.preparationSourceValid(p); err != nil {
		fail(err)
		return
	}
	if err := s.acquirePreparationLocks(p); err != nil {
		fail(err)
		return
	}
	for i := range p.Repositories {
		r := &p.Repositories[i]
		if r.Status == "merged" && (!resume || r.ExpectedResultCommit == "") {
			continue
		}
		g, err := s.preparationGit(r)
		if err != nil {
			fail(err)
			return
		}
		err = mergePreparationRepository(ctx, g, r, resume || p.VerifyOnly)
		if err == nil && !resume && !p.VerifyOnly {
			r.Status = "publishing"
			if e := s.saveCodePreparation(p); e != nil {
				g.close()
				return
			}
			_, err = g.repo(ctx, "push", "--porcelain", "origin", r.ResultCommit+":refs/heads/"+r.TargetBranch)
			if err == nil {
				tip, e := g.run(ctx, "ls-remote", "--", r.URL, "refs/heads/"+r.TargetBranch)
				if e != nil || !strings.HasPrefix(tip, r.ResultCommit+"\t") {
					err = fmt.Errorf("推送后目标提交无法确认，请核对恢复，不自动重推")
				}
			}
		}
		g.close()
		if err != nil {
			fail(err)
			return
		}
		r.Status = "merged"
		for j := range p.Builds {
			for _, svc := range r.Services {
				if p.Builds[j].Service == svc {
					p.Builds[j].Commit = r.ResultCommit
				}
			}
		}
		if err = s.saveCodePreparation(p); err != nil {
			return
		}
	}
	s.releasePreparationLocks(p)
	p.State = "building"
	p.LeaseUntil = time.Now().Add(2 * time.Minute).Unix()
	p.Message = "代码合并已确认，按冻结提交逐应用构建；不自动发布。"
	if s.saveCodePreparation(p) != nil {
		return
	}
	s.performPreparedBuilds(p, pr)
}

func (s *Store) performPreparedBuilds(p *CodePreparation, pr *PipelineRunner) {
	deadline := time.Now().Add(40 * time.Minute)
	fail := func(err error) {
		p.State = "failed"
		p.Message = err.Error()
		p.LeaseUntil = 0
		s.saveCodePreparation(p)
	}
	for {
		done, err := s.advancePreparedBuilds(p, func(b *PreparedBuild) (int64, error) {
			if pr == nil {
				return 0, fmt.Errorf("构建 Runner 不可用")
			}
			sv, err := s.preparedBuildService(b)
			if err != nil {
				return 0, err
			}
			return pr.StartWithOptions(sv, false, PipelineRunOptions{Branch: b.Commit, BuildEnvironment: p.Request.Environment, Trigger: "code-preparation:" + p.ID + ":" + b.Service}, p.Owner)
		})
		if err != nil {
			fail(err)
			return
		}
		if done {
			break
		}
		if time.Now().After(deadline) {
			fail(fmt.Errorf("构建等待超时，原构建仍可核对，不自动重跑"))
			return
		}
		time.Sleep(3 * time.Second)
	}
	if err := s.preparationSourceValid(p); err != nil {
		fail(err)
		return
	}
	template := s.DeliveryTemplate(p.Request.TemplateID)
	if p.Request.VersionID > 0 && len(p.Request.ArtifactSources) == 0 && (template == nil || template.Workbench != "research") {
		bindings := []VersionArtifactBinding{}
		for _, b := range p.Builds {
			bindings = append(bindings, VersionArtifactBinding{Service: b.Service, ArtifactID: b.ArtifactID, CodeConfirmed: true})
		}
		if _, err := s.SaveVersionArtifacts(p.Request.VersionID, bindings, p.Owner, p.Request.Environment); err != nil {
			fail(err)
			return
		}
	}
	p.State, p.Message, p.LeaseUntil = "ready", "代码与制品已准备完成，可回到交付预检；尚未部署或执行配置 / SQL。", 0
	s.saveCodePreparation(p)
}
func (s *Store) preparedBuildService(b *PreparedBuild) (*Service, error) {
	sv := s.GetService(b.Service)
	if sv == nil || !fullBuildCommit.MatchString(b.Commit) {
		return nil, fmt.Errorf("应用或冻结提交无效")
	}
	copy := *sv
	copy.Branch = b.Commit
	if len(copy.Steps) > 0 {
		raw, _ := json.Marshal(copy.Steps)
		copy.Steps = nil // Keep frozen source overrides separate from the application configuration.
		json.Unmarshal(raw, &copy.Steps)
		sources := 0
		for i := range copy.Steps {
			if copy.Steps[i].Step == "git-checkout" {
				sources++
				if copy.Steps[i].Params == nil {
					copy.Steps[i].Params = map[string]string{}
				}
				copy.Steps[i].Params["GIT_URL"] = sv.GitURL
				copy.Steps[i].Params["BRANCH"] = b.Commit
			}
		}
		if sources != 1 {
			return nil, fmt.Errorf("自动代码准备要求流水线恰有一个标准 git-checkout 源码步骤")
		}
	}
	return &copy, nil
}
