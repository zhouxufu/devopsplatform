package deploy

// Feature branches are created at the start of development, never as a side
// effect of synchronizing a Bug status. Preview freezes every repository tip.
import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"
)

type FeatureBranchRequest struct {
	WorkItemID int64    `json:"work_item_id"`
	Services   []string `json:"services"`
	BaseBranch string   `json:"base_branch"`
	BaseCommit string   `json:"base_commit"`
	Branch     string   `json:"branch"`
}
type FeatureRepository struct {
	URL              string   `json:"url"`
	Services         []string `json:"services"`
	Connection       string   `json:"connection,omitempty"`
	CredentialDigest string   `json:"credential_digest"`
	BaseBranch       string   `json:"base_branch"`
	BaseCommit       string   `json:"base_commit"`
	Status           string   `json:"status"`
}
type FeatureBranchPlan struct {
	ID           string              `json:"id"`
	Owner        string              `json:"owner"`
	WorkItemID   int64               `json:"work_item_id"`
	Branch       string              `json:"branch"`
	Repositories []FeatureRepository `json:"repositories"`
	State        string              `json:"state"`
	CreatedAt    int64               `json:"created_at"`
	Message      string              `json:"message,omitempty"`
	Revision     int64               `json:"revision"`
}

func (s *Store) featureBranchPlan(id string) *FeatureBranchPlan {
	var raw string
	var revision int64
	if s.db.QueryRow(`SELECT document,revision FROM feature_branch_plans WHERE id=?`, id).Scan(&raw, &revision) != nil {
		return nil
	}
	var p FeatureBranchPlan
	if json.Unmarshal([]byte(raw), &p) != nil {
		return nil
	}
	p.Revision = revision
	return &p
}
func (s *Store) saveFeatureBranchPlan(p *FeatureBranchPlan) error {
	raw, _ := json.Marshal(p)
	r, err := s.db.Exec(`UPDATE feature_branch_plans SET document=?,revision=revision+1 WHERE id=? AND revision=?`, string(raw), p.ID, p.Revision)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return ErrRunConflict
	}
	p.Revision++
	return nil
}
func (s *Store) previewFeatureBranch(ctx context.Context, req FeatureBranchRequest, actor string) (*FeatureBranchPlan, error) {
	return s.previewFeatureBranchWithDiscovery(ctx, req, actor, discoverGitBranches)
}
func (s *Store) previewFeatureBranchWithDiscovery(ctx context.Context, req FeatureBranchRequest, actor string, discover func(context.Context, string, *ResourceConnection) (*GitBranches, error)) (*FeatureBranchPlan, error) {
	if s.GetWorkItem(req.WorkItemID) == nil || len(req.Services) == 0 || len(req.Services) > 20 {
		return nil, fmt.Errorf("请选择工作项及 1–20 个应用")
	}
	if req.BaseBranch != "" && !validPreparationBranch(req.BaseBranch) {
		return nil, fmt.Errorf("基线分支无效")
	}
	if req.BaseCommit != "" && !gitObjectID.MatchString(req.BaseCommit) {
		return nil, fmt.Errorf("指定提交须为完整 Git SHA")
	}
	p := &FeatureBranchPlan{ID: randomToken(), Owner: actor, WorkItemID: req.WorkItemID, Branch: req.Branch, State: "preview", CreatedAt: time.Now().Unix(), Revision: 1}
	if p.Branch == "" {
		p.Branch = fmt.Sprintf("feature/work-%d-%s", req.WorkItemID, p.ID[:8])
	}
	if !validPreparationBranch(p.Branch) {
		return nil, fmt.Errorf("新分支名称无效")
	}
	groups := map[string]*FeatureRepository{}
	for _, name := range req.Services {
		repo, c, err := s.gitDiscoverySource(GitBranchesRequest{Service: name})
		if err != nil {
			return nil, err
		}
		connection := ""
		if c != nil {
			connection = c.Name
		}
		if existing := groups[repo]; existing != nil {
			if existing.Connection != connection {
				return nil, fmt.Errorf("同一仓库的应用须使用同一 Git 凭据")
			}
			if !containsString(existing.Services, name) {
				existing.Services = append(existing.Services, name)
			}
			continue
		}
		groups[repo] = &FeatureRepository{URL: repo, Services: []string{name}, Connection: connection, CredentialDigest: s.privateOperationDigest(c), BaseBranch: req.BaseBranch, BaseCommit: req.BaseCommit, Status: "pending"}
	}
	keys := []string{}
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		r := groups[key]
		var c *ResourceConnection
		if r.Connection != "" {
			c = s.GetConnection(r.Connection)
		}
		branches, err := discover(ctx, r.URL, c)
		if err != nil {
			return nil, err
		}
		if r.BaseBranch == "" && r.BaseCommit == "" {
			r.BaseBranch = branches.DefaultBranch
		}
		foundBase := r.BaseBranch == ""
		for _, b := range branches.Branches {
			if b.Name == p.Branch {
				return nil, fmt.Errorf("分支 %s 已存在，请直接关联已有分支或更换名称", p.Branch)
			}
			if b.Name == r.BaseBranch {
				foundBase = true
				if r.BaseCommit != "" && r.BaseCommit != b.Commit {
					return nil, fmt.Errorf("基线分支与指定提交不一致")
				}
				r.BaseCommit = b.Commit
			}
		}
		if !foundBase || !gitObjectID.MatchString(r.BaseCommit) {
			return nil, fmt.Errorf("未找到基线，请指定干净的基线分支或完整提交")
		}
		sort.Strings(r.Services)
		p.Repositories = append(p.Repositories, *r)
	}
	raw, _ := json.Marshal(p)
	_, err := s.db.Exec(`INSERT INTO feature_branch_plans(id,document,revision) VALUES(?,?,1)`, p.ID, string(raw))
	return p, err
}
func containsString(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

// Empty expected ref means create-only CAS. Never force an existing branch.
func createFeatureRef(ctx context.Context, g *gitSession, r FeatureRepository, branch string) error {
	if err := g.clone(ctx, r.URL); err != nil {
		return err
	}
	if r.BaseBranch != "" {
		tip, err := g.branch(ctx, r.BaseBranch)
		if err != nil || tip != r.BaseCommit {
			return fmt.Errorf("基线已变化，请重新预览")
		}
	}
	if _, err := g.repo(ctx, "cat-file", "-e", r.BaseCommit+"^{commit}"); err != nil {
		return fmt.Errorf("冻结提交在仓库中不可用")
	}
	ref := "refs/heads/" + branch
	_, err := g.repo(ctx, "push", "--force-with-lease="+ref+":", "origin", r.BaseCommit+":"+ref)
	return err
}
func (s *Store) executeFeatureBranch(ctx context.Context, p *FeatureBranchPlan, reconcile bool) (*FeatureBranchPlan, error) {
	if p.State == "success" {
		return p, nil
	}
	if s.GetWorkItem(p.WorkItemID) == nil {
		return nil, fmt.Errorf("工作项已不存在，禁止创建分支")
	}
	if reconcile {
		if p.State != "running" && p.State != "uncertain" {
			return nil, fmt.Errorf("此计划不需要核对")
		}
	} else {
		if p.State != "preview" || time.Now().Unix()-p.CreatedAt > 600 {
			return nil, fmt.Errorf("预览已过期或已执行；请核对原结果，不能重复创建")
		}
		p.State = "running"
		if err := s.saveFeatureBranchPlan(p); err != nil {
			return nil, err
		}
	}
	for i := range p.Repositories {
		r := &p.Repositories[i]
		var c *ResourceConnection
		if r.Connection != "" {
			c = s.GetConnection(r.Connection)
		}
		for _, app := range r.Services {
			repo, current, err := s.gitDiscoverySource(GitBranchesRequest{Service: app})
			if err != nil || repo != r.URL || s.privateOperationDigest(current) != r.CredentialDigest {
				p.State = "uncertain"
				p.Message = "应用仓库或凭据已变化；核对已创建分支后重新发起"
				_ = s.saveFeatureBranchPlan(p)
				return p, nil
			}
		}
		if reconcile {
			branches, err := discoverGitBranches(ctx, r.URL, c)
			if err != nil {
				return nil, err
			}
			r.Status = "unconfirmed"
			for _, b := range branches.Branches {
				if b.Name == p.Branch && b.Commit == r.BaseCommit {
					r.Status = "created"
				}
			}
		} else {
			g, err := newGitSession(r.URL, c)
			if err == nil {
				err = createFeatureRef(ctx, g, *r, p.Branch)
				g.close()
			}
			if err != nil {
				r.Status = "uncertain"
				p.State = "uncertain"
				p.Message = "创建结果待核实；可能部分仓库已成功。只能读取核对，不会重复推送"
				_ = s.saveFeatureBranchPlan(p)
				return p, nil
			}
			r.Status = "created"
		}
		if err := s.saveFeatureBranchPlan(p); err != nil {
			return nil, err
		}
	}
	p.State = "success"
	p.Message = "分支已创建；请保存交付草稿完成应用关联"
	for _, r := range p.Repositories {
		if r.Status != "created" {
			p.State = "uncertain"
			p.Message = "未能确认全部分支；禁止重放，请人工核对远程仓库"
		}
	}
	return p, s.saveFeatureBranchPlan(p)
}
func registerFeatureBranchRoutes(mux *http.ServeMux, s *Store) {
	mux.HandleFunc("POST /api/research/development/preview", func(w http.ResponseWriter, r *http.Request) {
		var req FeatureBranchRequest
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&req) != nil {
			deliveryWriteError(w, 400, "请求无效")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		p, err := s.previewFeatureBranch(ctx, req, deliveryActor(r))
		if err != nil {
			deliveryWriteError(w, 400, err)
			return
		}
		deliveryJSON(w, p)
	})
	for _, action := range []string{"create", "reconcile"} {
		action := action
		mux.HandleFunc("POST /api/research/development/{id}/"+action, func(w http.ResponseWriter, r *http.Request) {
			p := s.featureBranchPlan(r.PathValue("id"))
			if p == nil || p.Owner != deliveryActor(r) {
				deliveryWriteError(w, 403, "仅发起人可操作本次分支计划")
				return
			}
			var req struct {
				Confirm  bool  `json:"confirm"`
				Revision int64 `json:"revision"`
			}
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil || !req.Confirm || req.Revision != p.Revision {
				deliveryWriteError(w, 409, "请核对冻结基线并确认最新计划")
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			out, err := s.executeFeatureBranch(ctx, p, action == "reconcile")
			if err != nil {
				deliveryWriteError(w, 409, err)
				return
			}
			s.Audit(deliveryActor(r), "feature-branch-"+action, "work-item", fmt.Sprint(p.WorkItemID), map[string]any{"plan": p.ID, "branch": p.Branch, "state": out.State})
			deliveryJSON(w, out)
		})
	}
	mux.HandleFunc("GET /api/research/development/{id}", func(w http.ResponseWriter, r *http.Request) {
		p := s.featureBranchPlan(r.PathValue("id"))
		if p == nil || p.Owner != deliveryActor(r) {
			deliveryWriteError(w, 404, "分支计划不存在")
			return
		}
		deliveryJSON(w, p)
	})
}
