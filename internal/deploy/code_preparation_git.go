package deploy

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

func verifyPreparedWorkspace(ctx context.Context, workspace, expected string) error {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	cmd.Dir = workspace
	actual, err := cmd.Output()
	if err != nil || !fullBuildCommit.MatchString(expected) || strings.TrimSpace(string(actual)) != expected {
		return fmt.Errorf("构建工作区提交与代码准备不一致，已停止，不关联制品")
	}
	return nil
}

var preparationBranchRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_./-]{0,190}$`)

func validPreparationBranch(branch string) bool {
	return preparationBranchRE.MatchString(branch) && !strings.Contains(branch, "..") && !strings.Contains(branch, "//") && !strings.Contains(branch, "@{") && !strings.HasSuffix(branch, "/") && !strings.HasSuffix(branch, ".lock") && !strings.Contains(branch, "/.")
}
func (g *gitSession) repo(ctx context.Context, args ...string) (string, error) {
	return g.run(ctx, append([]string{"-C", filepath.Join(g.dir, "repo")}, args...)...)
}
func (g *gitSession) clone(ctx context.Context, url string) error {
	_, err := g.run(ctx, "clone", "--no-checkout", "--no-tags", "--", url, filepath.Join(g.dir, "repo"))
	return err
}
func (g *gitSession) branch(ctx context.Context, name string) (string, error) {
	if !validPreparationBranch(name) {
		return "", fmt.Errorf("分支名称无效")
	}
	return g.repo(ctx, "rev-parse", "--verify", "refs/remotes/origin/"+name+"^{commit}")
}
func (g *gitSession) ancestor(ctx context.Context, base, head string) bool {
	_, err := g.repo(ctx, "merge-base", "--is-ancestor", base, head)
	return err == nil
}

// Preparation uses fresh disposable clones: never modify the user's checkout,
// inherit repository hooks, or return Git stderr (which may contain credentials).
func mergePreparationRepository(ctx context.Context, g *gitSession, r *PreparedRepository, resume bool) error {
	if err := g.clone(ctx, r.URL); err != nil {
		return err
	}
	head, err := g.branch(ctx, r.TargetBranch)
	if resume {
		if r.ExpectedResultCommit != "" && (err != nil || head != r.ExpectedResultCommit) {
			return fmt.Errorf("目标分支不再是本次确认的解决提交，请重新核对")
		}
		if err != nil || !g.ancestor(ctx, r.BaseCommit, head) {
			return fmt.Errorf("目标分支不存在或不再包含冻结基线")
		}
		for _, src := range r.Sources {
			if !g.ancestor(ctx, src.Commit, head) {
				return fmt.Errorf("目标分支尚未包含源提交 %s，请完成真实合并再继续", src.Commit)
			}
		}
		if r.Policy.Mode == "isolated" {
			allowed := map[string]bool{}
			for _, src := range r.Sources {
				commits, e := g.repo(ctx, "rev-list", "--no-merges", r.BaseCommit+".."+src.Commit)
				if e != nil {
					return e
				}
				for _, sha := range strings.Fields(commits) {
					allowed[sha] = true
				}
			}
			commits, e := g.repo(ctx, "rev-list", "--no-merges", r.BaseCommit+".."+head)
			if e != nil {
				return e
			}
			for _, sha := range strings.Fields(commits) {
				if !allowed[sha] {
					return fmt.Errorf("独立发布分支包含未选中的额外提交，请重新整理代码范围")
				}
			}
		}
		r.ResultCommit, r.Status, r.Conflicts = head, "merged", nil
		return nil
	}
	if r.TargetCommit != "" && (err != nil || head != r.TargetCommit) {
		return fmt.Errorf("目标分支已变化，请重新检查代码准备")
	}
	if r.TargetCommit == "" && err == nil {
		return fmt.Errorf("目标分支已被创建，请重新检查，禁止覆盖")
	}
	base, err := g.branch(ctx, r.BaseBranch)
	if err != nil || base != r.BaseCommit {
		return fmt.Errorf("基线分支已变化，请重新检查")
	}
	for _, src := range r.Sources {
		tip, e := g.branch(ctx, src.Branch)
		if e != nil || tip != src.Commit {
			return fmt.Errorf("源分支已变化，请重新检查")
		}
	}
	if _, err = g.repo(ctx, "checkout", "--detach", r.BaseCommit); err != nil {
		return err
	}
	for _, src := range r.Sources {
		if g.ancestor(ctx, src.Commit, r.BaseCommit) {
			continue
		}
		_, err = g.repo(ctx, "-c", "user.name=DevOps Platform", "-c", "user.email=devops-platform@localhost", "-c", "commit.gpgSign=false", "merge", "--no-ff", "--no-edit", src.Commit)
		if err != nil {
			files, _ := g.repo(ctx, "diff", "--name-only", "--diff-filter=U")
			if files != "" {
				r.Status = "conflict"
				r.Conflicts = strings.Split(files, "\n")
				return fmt.Errorf("合并冲突：请在本地将冻结源提交合入目标分支并推送，再核对恢复")
			}
			return err
		}
	}
	r.ResultCommit, err = g.repo(ctx, "rev-parse", "HEAD")
	return err
}
