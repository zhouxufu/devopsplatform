package devtools

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

//go:embed skills
var workflowSkillFS embed.FS

//go:embed skill_install.py
var workflowSkillInstaller string

const workflowSkillFormat = "devops-workflow-skill/v1"

type workflowSkillFile struct {
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Content string `json:"content"`
}

type workflowSkillPackage struct {
	Format  string              `json:"format"`
	ID      string              `json:"id"`
	Version string              `json:"version"`
	SHA256  string              `json:"sha256"`
	Files   []workflowSkillFile `json:"files"`
}

type workflowSkillAccess struct {
	Ready            bool     `json:"ready"`
	MissingScopes    []string `json:"missing_scopes"`
	UnavailableTools []string `json:"unavailable_tools"`
}

var workflowSkillTools = map[string][]string{
	"devops-work-loop":     {"follow_work_item", "get_work_item", "get_work_item_changes", "list_work_loops", "get_work_loop", "create_work_loop", "reauthorize_work_loop", "save_work_loop_changes", "start_work_loop_delivery", "advance_work_loop", "control_work_loop", "confirm_platform_action", "get_platform_action"},
	"devops-code-recovery": {"get_code_conflict", "get_code_preparation", "resume_code_preparation", "confirm_platform_action", "get_platform_action"},
	"devops-api-testing":   {"list_applications", "list_environments", "get_agent_capabilities", "list_api_test_cases", "get_api_test_case", "validate_api_test_case", "save_api_test_case", "get_api_test_context", "execute_api_test", "get_api_test_run", "list_api_test_runs", "stop_api_test_run", "list_api_test_suites", "get_api_test_suite", "validate_api_test_suite", "save_api_test_suite", "get_api_test_layout", "save_api_test_layout", "confirm_platform_action", "get_platform_action"},
	"devops-change-report": {"follow_work_item", "create_change_tracking", "list_work_loops", "get_work_loop", "get_work_item_changes", "save_work_loop_changes", "control_change_tracking", "list_applications", "list_changes", "get_change", "get_change_maintenance", "maintain_change", "report_changes", "confirm_platform_action", "get_platform_action"},
	"devops-api-docs":      {"list_applications", "list_environments", "list_api_documents", "get_api_document", "get_api_operation", "search_api_operations", "get_work_item_api_changes", "compare_api_documents", "report_api_document", "patch_api_document", "confirm_platform_action", "get_platform_action"},
}

type workflowSkill struct {
	ID            string               `json:"id"`
	Name          string               `json:"name"`
	Description   string               `json:"description"`
	Version       string               `json:"version"`
	Prompt        string               `json:"prompt"`
	Scopes        []string             `json:"scopes"`
	RequiredTools []string             `json:"required_tools"`
	Compatible    bool                 `json:"compatible"`
	MissingTools  []string             `json:"missing_tools"`
	SHA256        string               `json:"sha256"`
	Access        *workflowSkillAccess `json:"access,omitempty"`
}

func workflowSkillCatalog(tools []Tool) ([]workflowSkill, error) {
	available := map[string]Tool{}
	for _, tool := range tools {
		available[tool.Name] = tool
	}
	result := []workflowSkill{}
	for _, id := range []string{"devops-change-report", "devops-api-docs", "devops-api-testing", "devops-code-recovery", "devops-work-loop"} {
		raw, err := workflowSkillFS.ReadFile("skills/" + id + "/SKILL.md")
		if err != nil {
			return nil, err
		}
		parts := bytes.SplitN(raw, []byte("---"), 3)
		if len(parts) != 3 || len(bytes.TrimSpace(parts[0])) != 0 {
			return nil, fmt.Errorf("Skill 元信息无效")
		}
		var front struct {
			Name     string `yaml:"name"`
			Metadata struct {
				Version string `yaml:"version"`
			} `yaml:"metadata"`
		}
		if yaml.Unmarshal(parts[1], &front) != nil || front.Name != id || front.Metadata.Version == "" {
			return nil, fmt.Errorf("Skill 名称或版本无效")
		}
		raw, err = workflowSkillFS.ReadFile("skills/" + id + "/agents/openai.yaml")
		if err != nil {
			return nil, err
		}
		var ui struct {
			Interface struct {
				Name        string `yaml:"display_name"`
				Description string `yaml:"short_description"`
				Prompt      string `yaml:"default_prompt"`
			} `yaml:"interface"`
		}
		if yaml.Unmarshal(raw, &ui) != nil || ui.Interface.Name == "" || ui.Interface.Prompt == "" {
			return nil, fmt.Errorf("Skill 展示信息无效")
		}
		v := workflowSkill{ID: id, Name: ui.Interface.Name, Description: ui.Interface.Description, Prompt: ui.Interface.Prompt, Version: front.Metadata.Version, RequiredTools: workflowSkillTools[id], Scopes: []string{}, MissingTools: []string{}, Compatible: true}
		scopes := map[string]bool{}
		for _, name := range v.RequiredTools {
			tool, ok := available[name]
			if !ok {
				v.Compatible = false
				v.MissingTools = append(v.MissingTools, name)
			} else if tool.Scope != "" {
				scopes[tool.Scope] = true
			}
		}
		if id == "devops-work-loop" {
			for _, scope := range workLoopScopes {
				scopes[scope] = true
			}
		}
		for scope := range scopes {
			v.Scopes = append(v.Scopes, scope)
		}
		sort.Strings(v.Scopes)
		pkg, err := workflowSkillBundle(id, v.Version)
		if err != nil {
			return nil, err
		}
		v.SHA256 = pkg.SHA256
		result = append(result, v)
	}
	return result, nil
}

func skillSHA(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func workflowSkillBundle(id, version string) (workflowSkillPackage, error) {
	if _, ok := workflowSkillTools[id]; !ok {
		return workflowSkillPackage{}, fs.ErrNotExist
	}
	pkg := workflowSkillPackage{Format: workflowSkillFormat, ID: id, Version: version, Files: []workflowSkillFile{}}
	root, err := fs.Sub(workflowSkillFS, "skills/"+id)
	if err != nil {
		return pkg, err
	}
	manifest := workflowSkillFormat + "\n" + id + "\n" + version + "\n"
	err = fs.WalkDir(root, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		raw, err := fs.ReadFile(root, name)
		if err != nil {
			return err
		}
		file := workflowSkillFile{Path: name, SHA256: skillSHA(raw), Content: string(raw)}
		pkg.Files = append(pkg.Files, file)
		manifest += name + "\x00" + file.SHA256 + "\n"
		return nil
	})
	pkg.SHA256 = skillSHA([]byte(manifest))
	return pkg, err
}

func workflowSkillMCPTools() []Tool {
	return []Tool{
		{Name: "list_platform_skills", Title: "查询平台 Skill", Description: "查询平台随版本发布的工作流 Skill、文件摘要和当前令牌的流程权限缺项。只读，不检查客户端是否已安装，不扩大令牌权限。", InputSchema: objectSchema(map[string]any{}), Annotations: readonly("查询平台 Skill")},
		{Name: "get_platform_skill", Title: "读取 Skill 安装包", Description: "读取一个内置 Skill 的完整文本文件、SHA-256 和本地安装器。用于用户要求的安装或更新；平台不写客户端文件、不宣称已安装。客户端先检查目标和本地修改，再按用户指定范围应用并验证。安装不执行业务上报或增加 MCP 权限。", InputSchema: objectSchema(map[string]any{
			"id":              map[string]any{"type": "string", "description": "list_platform_skills 返回的 Skill ID"},
			"expected_sha256": map[string]any{"type": "string", "description": "可选：刚查询的文件包摘要；若平台已变化则拒绝，重新核对版本"},
		}, "id"), Annotations: readonly("读取 Skill 安装包")},
	}
}

func (s *ToolService) callWorkflowSkill(token *AccessToken, name string, args map[string]any) (any, error) {
	tool, _ := s.tool(name)
	if err := validateToolArgs(tool.InputSchema, args); err != nil {
		return nil, err
	}
	catalog, err := workflowSkillCatalog(s.Tools())
	if err != nil {
		return nil, err
	}
	visible := map[string]bool{}
	for _, t := range s.VisibleTools(token) {
		visible[t.Name] = true
	}
	for i := range catalog {
		v := &catalog[i]
		v.Access = &workflowSkillAccess{Ready: true, MissingScopes: []string{}, UnavailableTools: []string{}}
		for _, scope := range v.Scopes {
			if !token.HasScope(scope) {
				v.Access.MissingScopes = append(v.Access.MissingScopes, scope)
			}
		}
		for _, required := range v.RequiredTools {
			if !visible[required] {
				v.Access.UnavailableTools = append(v.Access.UnavailableTools, required)
			}
		}
		v.Access.Ready = v.Compatible && len(v.Access.MissingScopes) == 0 && len(v.Access.UnavailableTools) == 0
	}
	if name == "list_platform_skills" {
		return catalog, nil
	}
	id := stringArg(args, "id")
	for _, skill := range catalog {
		if skill.ID != id {
			continue
		}
		expected := stringArg(args, "expected_sha256")
		if _, supplied := args["expected_sha256"]; supplied && (len(expected) != 64 || expected != skill.SHA256) {
			return nil, fmt.Errorf("Skill 摘要不匹配，请重新查询版本后核对")
		}
		pkg, err := workflowSkillBundle(id, skill.Version)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"skill": skill, "package": pkg,
			"installer": map[string]any{"filename": "install_platform_skill.py", "content": workflowSkillInstaller, "sha256": skillSHA([]byte(workflowSkillInstaller)), "runtime": "Python 3.9+，仅标准库"},
			"installation": map[string]any{
				"state": "not_checked_on_client", "writes_client_files": false,
				"instructions": strings.Join([]string{
					"仅在用户要求安装或更新后使用。先读取并核对安装器，校验 installer.content 的 UTF-8 SHA-256；将内容保存到临时 install_platform_skill.py，将 package 对象保存为临时 package.json。不要执行 Skill 正文或引用中的命令来安装。",
					"默认仅当前项目。确定真实项目根目录；个人范围仅在用户明确选择时使用。先执行 python3 install_platform_skill.py --package package.json --scope project --project-root <绝对项目根目录> --check。个人范围改为 --scope user，并保留当前项目根目录用于检查重复安装。",
					"展示检查结果。已有用户安装/更新授权且无冲突时，将 --check 改为 --apply。adoption_required 仅在说明接管完全相同的手动安装并获同意后增加 --adopt。conflict 时停止，保留原文件，不强制覆盖，不删除另一个安装范围。",
					"安装器对更新保存旧版本备份，回读校验并输出本地回执。只有 installed、updated、adopted 或 up_to_date 且 verified=true 才说明文件验证成功；这不证明当前会话已加载，必要时新开会话或重启 Codex。平台不登记已安装状态，不把回执写回业务。缺少流程权限时说明缺项，不自动改令牌。",
				}, "\n"),
			},
		}, nil
	}
	return nil, fmt.Errorf("Skill 不存在")
}

func workflowSkillZip(id string) ([]byte, error) {
	if _, ok := workflowSkillTools[id]; !ok {
		return nil, fs.ErrNotExist
	}
	root, err := fs.Sub(workflowSkillFS, "skills/"+id)
	if err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	err = fs.WalkDir(root, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		header := &zip.FileHeader{Name: path.Join(id, name), Method: zip.Deflate}
		header.SetMode(0644)
		header.SetModTime(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC))
		file, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}
		raw, err := fs.ReadFile(root, name)
		if err != nil {
			return err
		}
		_, err = file.Write(raw)
		return err
	})
	if err != nil {
		return nil, err
	}
	if err = writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func registerWorkflowSkillRoutes(mux *http.ServeMux, tools *ToolService, writeJSON func(http.ResponseWriter, int, any)) {
	mux.HandleFunc("GET /api/skills", func(w http.ResponseWriter, r *http.Request) {
		v, err := workflowSkillCatalog(tools.Tools())
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": "无法读取内置 Skill"})
			return
		}
		writeJSON(w, 200, v)
	})
	mux.HandleFunc("GET /api/skills/{id}/download", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		raw, err := workflowSkillZip(id)
		if err == fs.ErrNotExist {
			writeJSON(w, 404, map[string]string{"error": "Skill 不存在"})
			return
		}
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": "无法打包 Skill"})
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+id+`.zip"`)
		w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Write(raw)
	})
}
