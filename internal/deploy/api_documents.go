package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"logplatform/pkg/apidoc"
	"logplatform/pkg/extensionclient"
	p "logplatform/pkg/extensionprotocol"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type APIDocumentSnapshot struct {
	Protocol                 string                 `json:"protocol"`
	ExpectedMaintainerDigest string                 `json:"-"`
	ChangeContext            *DocumentChangeContext `json:"-"`
	DefaultMaintainer        string                 `json:"default_maintainer,omitempty"`
	Compared                 bool                   `json:"compared"`
	Environment              string                 `json:"environment"`
	BaselineID               int64                  `json:"baseline_id"`
	Changes                  apidoc.ChangeSummary   `json:"changes"`
	ID                       int64                  `json:"id"`
	ApplicationID            string                 `json:"application_id"`
	Service                  string                 `json:"service"`
	RunID                    int64                  `json:"run_id"`
	ArtifactID               int64                  `json:"artifact_id"`
	ArtifactDigest           string                 `json:"artifact_digest"`
	Commit                   string                 `json:"commit"`
	Provider                 string                 `json:"provider"`
	ProviderVersion          string                 `json:"provider_version"`
	Digest                   string                 `json:"digest"`
	CreatedAt                int64                  `json:"created_at"`
	Document                 json.RawMessage        `json:"document,omitempty"`
}

func (s *Store) apiDocumentSource(sv *Service) (apidoc.Source, error) {
	var raw string
	err := s.db.QueryRow(`SELECT document FROM api_document_sources WHERE application_id=?`, sv.ApplicationID).Scan(&raw)
	if err == sql.ErrNoRows {
		return apidoc.Source{Settings: json.RawMessage(`{}`)}, nil
	}
	if err != nil {
		return apidoc.Source{}, err
	}
	var v apidoc.Source
	if json.Unmarshal([]byte(raw), &v) != nil {
		return v, fmt.Errorf("文档来源配置无效")
	}
	return v, nil
}
func (s *Store) saveAPIDocumentSource(sv *Service, v apidoc.Source, actor string) error {
	old, err := s.apiDocumentSource(sv)
	if err != nil {
		return err
	}
	if v.Revision != old.Revision {
		return fmt.Errorf("文档配置已被修改，请重新载入")
	}
	v.Revision++
	raw, _ := json.Marshal(v)
	if old.Revision == 0 {
		_, err = s.db.Exec(`INSERT INTO api_document_sources(application_id,document,actor,updated_at) VALUES(?,?,?,?)`, sv.ApplicationID, string(raw), actor, time.Now().Unix())
		return err
	}
	// The document includes its revision; a CAS protects concurrent saves on both databases.
	previous, _ := json.Marshal(old)
	res, err := s.db.Exec(`UPDATE api_document_sources SET document=?,actor=?,updated_at=? WHERE application_id=? AND document=?`, string(raw), actor, time.Now().Unix(), sv.ApplicationID, string(previous))
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return fmt.Errorf("文档配置已被修改，请重新载入")
	}
	return nil
}
func prepareDocumentPlan(ctx context.Context, sv *Service, source apidoc.Source) (*apidoc.Plan, error) {
	client, err := extensionclient.FromEnvironment()
	if err != nil {
		return nil, err
	}
	in, _ := json.Marshal(apidoc.PlanInput{Tool: sv.BuildTool, Settings: source.Settings})
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := client.Invoke(ctx, source.Provider, p.Request{Capability: apidoc.Capability, Operation: "plan", Input: in})
	if err != nil {
		return nil, err
	}
	if out.Provider == nil {
		return nil, fmt.Errorf("平台未返回插件安装身份，请更新插件宿主")
	}
	var plan apidoc.Plan
	if json.Unmarshal(out.Data, &plan) != nil || plan.Provider != source.Provider || plan.Tool != sv.BuildTool {
		return nil, fmt.Errorf("文档插件计划身份无效")
	}
	if out.Provider != nil {
		if plan.ProviderVersion != out.Provider.Version || plan.Provider != out.Provider.ID {
			return nil, fmt.Errorf("文档插件身份与安装清单不一致")
		}
		plan.ProviderDigest = out.Provider.Digest
	}
	if err := apidoc.ValidatePlan(plan); err != nil {
		return nil, err
	}
	return &plan, nil
}
func (s *Store) freezeAPIDocumentation(sv *Service, replay bool, environments ...string) error {
	environment := sv.DocumentEnvironment
	if !replay && len(environments) > 0 {
		environment = environments[0]
	}
	sv.DocumentEnvironment = environment
	var source apidoc.Source
	if replay {
		if sv.APIDocumentation == nil {
			return nil
		}
		source = sv.APIDocumentation.Source
	} else {
		v, err := s.resolveDocumentSource(sv, environment)
		if err != nil {
			return err
		}
		source = v
	}
	if source.Provider == "" || !source.OnBuild {
		sv.APIDocumentation = nil
		return nil
	}
	plan, err := prepareDocumentPlan(context.Background(), sv, source)
	if err != nil && source.Required {
		return fmt.Errorf("接口文档插件不可用，未启动构建：%w", err)
	}
	if replay && sv.APIDocumentation.Plan != nil && plan != nil && (sv.APIDocumentation.Plan.ProviderVersion != plan.ProviderVersion || sv.APIDocumentation.Plan.ProviderDigest != plan.ProviderDigest) {
		return fmt.Errorf("原文档插件版本已变化，请使用当前配置重新构建")
	}
	frozen := &apidoc.Frozen{Environment: environment, Source: source, Plan: plan}
	if err != nil {
		frozen.Unavailable = err.Error()
	}
	sv.APIDocumentation = frozen
	return nil
}

// safeDocumentPath rejects symlinks at every existing component, including outputs.
func safeDocumentPath(root, relative string) (string, error) {
	if !apidoc.RelativeFile(relative) {
		return "", fmt.Errorf("文档路径必须位于当前源码目录内")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	current := root
	for _, part := range strings.Split(relative, "/") {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("文档输入输出不能经过符号链接")
		}
	}
	return current, nil
}
func (pr *PipelineRunner) generateAPIDocument(ctx context.Context, runID int64, sv *Service, artifact *Artifact, workspace, outputDir string) error {
	return pr.generateAPIDocumentWithEmit(ctx, runID, sv, artifact, workspace, outputDir, func(line string) { pr.emit(runID, line) })
}
func (pr *PipelineRunner) generateAPIDocumentWithEmit(ctx context.Context, runID int64, sv *Service, artifact *Artifact, workspace, outputDir string, emit func(string)) error {
	frozen := sv.APIDocumentation
	if frozen == nil {
		return nil
	}
	failure := func(err error) error {
		emit("⚠ 接口文档未生成：" + err.Error() + "；不会沿用旧文档冒充本次结果\n")
		return err
	}
	if frozen.Plan == nil {
		return failure(fmt.Errorf("%s", frozen.Unavailable))
	}
	plan := frozen.Plan
	if err := apidoc.ValidatePlan(*plan); err != nil {
		return failure(err)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	for _, p := range plan.InputFiles {
		file, err := safeDocumentPath(workspace, p)
		if err != nil {
			return failure(err)
		}
		info, err := os.Stat(file)
		if err != nil || !info.Mode().IsRegular() {
			return failure(fmt.Errorf("文档输入文件不存在：%s", p))
		}
	}
	destination, err := safeDocumentPath(workspace, plan.OutputFile)
	if err != nil {
		return failure(err)
	}
	if err = os.Remove(destination); err != nil && !os.IsNotExist(err) {
		return failure(fmt.Errorf("无法清理旧文档产物"))
	}
	// Reuse the application's selected executable, profiles, resource bindings and container.
	docService := *sv
	docService.BuildTool = plan.Tool
	keys := make([]string, 0, len(plan.Properties))
	for k := range plan.Properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if plan.Tool == "maven" {
		docService.MavenGoals = strings.Join(plan.Tasks, " ")
		for _, k := range keys {
			docService.MavenProperties += "\n" + k + "=" + plan.Properties[k]
		}
	} else {
		docService.GradleTasks = strings.Join(plan.Tasks, " ")
		for _, k := range keys {
			docService.GradleSystemProperties += "\n" + k + "=" + plan.Properties[k]
		}
		// An inherited -P wins over org.gradle.project.* -D values. Replace only
		// provider-owned project keys, preserving the rest of the environment.
		properties, err := parseGradleProperties(docService.GradleProjectProperties, "-P 项目属性")
		if err != nil {
			return failure(err)
		}
		docService.GradleProjectProperties = ""
		for _, property := range properties {
			if _, replaced := plan.Properties["org.gradle.project."+property.key]; !replaced {
				docService.GradleProjectProperties += "\n" + property.key + "=" + property.value
			}
		}
	}
	if err := prepareBuildConfig(&docService); err != nil {
		return failure(err)
	}
	dir, err := os.MkdirTemp(outputDir, "api-document-")
	if err != nil {
		return failure(err)
	}
	defer os.RemoveAll(dir)
	script := "set -eu\ncd \"$LP_WORKSPACE\"\n" + docService.BuildCmd + "\n"
	if err = os.WriteFile(filepath.Join(dir, "run.sh"), []byte(script), 0600); err != nil {
		return failure(err)
	}
	def := &StepDef{ID: "api-document-generation", Name: "生成接口文档", Kind: "build", Dir: dir}
	env := def.BuildEnv(StepInst{}, workspace, sv.Name, "", outputDir)
	for k, v := range sv.RuntimeEnv {
		env = append(env, k+"="+v)
	}
	emit("▶ 生成接口文档 · " + plan.Provider + " " + plan.ProviderVersion + "\n")
	if code := pr.b.execBuildStepContext(ctx, &docService, def, env, emit); code != 0 {
		return failure(fmt.Errorf("文档生成命令失败（退出码 %d）", code))
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	destination, err = safeDocumentPath(workspace, plan.OutputFile)
	if err != nil {
		return failure(err)
	}
	f, err := os.Open(destination)
	if err != nil {
		return failure(fmt.Errorf("未找到本次新生成的文档，请核对 smart-doc outPath 与输出文件路径"))
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return failure(fmt.Errorf("文档输出不是普通文件"))
	}
	raw, err := io.ReadAll(io.LimitReader(f, apidoc.MaxDocument+1))
	if err != nil {
		return failure(err)
	}
	commit := pr.st.buildCommit(runID)
	if commit != "" {
		cmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
		cmd.Dir = workspace
		head, e := cmd.Output()
		if e != nil || strings.TrimSpace(string(head)) != commit {
			return failure(fmt.Errorf("文档生成前后源码身份不一致"))
		}
		cmd = exec.CommandContext(ctx, "git", "diff", "--quiet", "HEAD", "--")
		cmd.Dir = workspace
		if cmd.Run() != nil {
			return failure(fmt.Errorf("源码含未提交变更，不能关联到本次 Commit"))
		}
	}
	id, err := pr.st.recordAPIDocument(sv, APIDocumentSnapshot{Environment: frozen.Environment, RunID: runID, ArtifactID: artifact.ID, ArtifactDigest: artifact.Digest, Commit: commit, Provider: plan.Provider, ProviderVersion: plan.ProviderVersion}, raw)
	if err != nil {
		return failure(err)
	}
	emit(fmt.Sprintf("✓ 文档快照 #%d 已关联制品 #%d · 环境 %s\n", id, artifact.ID, frozen.Environment))
	if snap, e := pr.st.apiDocumentSnapshot(sv.ApplicationID, id); e == nil {
		emit(fmt.Sprintf("✓ 文档对比：新增 %d、删除 %d、修改 %d、未变 %d（基线 #%d）\n", snap.Changes.Added, snap.Changes.Removed, snap.Changes.Modified, snap.Changes.Unchanged, snap.BaselineID))
	}
	return nil
}
func registerAPIDocumentRoutes(mux *http.ServeMux, st *Store, wj func(http.ResponseWriter, any), fail func(http.ResponseWriter, int, any), actor func(*http.Request) string) {
	service := func(w http.ResponseWriter, r *http.Request) *Service {
		sv := st.GetService(r.URL.Query().Get("service"))
		if sv == nil {
			fail(w, 400, "请选择已有应用")
		}
		return sv
	}
	admin := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("X-User") != "" && r.Header.Get("X-Role") != "admin" {
			fail(w, 403, "仅管理员可维护文档来源和导入文档")
			return false
		}
		return true
	}
	registerEnvironmentDocumentRoutes(mux, st, wj, fail, actor, service, admin)
}
