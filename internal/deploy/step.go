package deploy

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ---------- 步骤定义（云效 Flow 式：YAML 声明 + 脚本执行体） ----------

// StepParam 步骤参数声明（UI 表单由声明渲染）
type StepParam struct {
	Name    string `yaml:"name" json:"name"`
	Type    string `yaml:"type" json:"type"` // input|shell|select|password
	Label   string `yaml:"label" json:"label"`
	Default string `yaml:"default" json:"default"`
	Options []struct {
		Value string `yaml:"value" json:"value"`
		Label string `yaml:"label" json:"label"`
	} `yaml:"options" json:"options,omitempty"`
}

// StepDef 一个步骤包的声明（steps/{id}/step.yaml）
type StepDef struct {
	APIVersion  string      `yaml:"apiVersion" json:"api_version"`
	ID          string      `yaml:"id" json:"id"`
	Name        string      `yaml:"name" json:"name"`
	Description string      `yaml:"description" json:"description"`
	Category    string      `yaml:"category" json:"category"` // 源码|构建|打包|部署
	Kind        string      `yaml:"kind" json:"kind"`         // source|build|package|deploy
	Driver      string      `yaml:"driver" json:"driver,omitempty"`
	Accepts     []string    `yaml:"accepts" json:"accepts,omitempty"`
	Produces    string      `yaml:"produces" json:"produces,omitempty"`
	Params      []StepParam `yaml:"params" json:"params"`
	Dir         string      `yaml:"-" json:"-"` // 步骤包目录
}

// StepLib 步骤库：扫描内置目录 + 用户目录
type StepLib struct{ dirs []string }

func NewStepLib(dirs ...string) *StepLib { return &StepLib{dirs: dirs} }

// Load 全部可用步骤
func (l *StepLib) Load() []StepDef {
	var out []StepDef
	seen := map[string]bool{}
	for i := len(l.dirs) - 1; i >= 0; i-- { // 后面的目录优先（用户覆盖内置）
		d := l.dirs[i]
		ents, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if !e.IsDir() || seen[e.Name()] {
				continue
			}
			def := loadStepDef(filepath.Join(d, e.Name()))
			if def != nil {
				out = append(out, *def)
				seen[e.Name()] = true
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (l *StepLib) ValidatePipeline(steps []StepInst) error {
	produced := ""
	for index, inst := range steps {
		def := l.Get(inst.Step)
		if def == nil {
			return fmt.Errorf("第 %d 步引用了未知步骤 %s", index+1, inst.Step)
		}
		if def.Produces != "" {
			produced = def.Produces
		}
		if def.Kind == "deploy" {
			if produced == "" {
				return fmt.Errorf("部署步骤 %s 之前没有制品输出", inst.Step)
			}
			if !def.AcceptsArtifact(produced) {
				return fmt.Errorf("部署步骤 %s 不接受 %s 制品", inst.Step, produced)
			}
		}
	}
	return nil
}

// ValidateBuildPipeline 校验构建流水线。部署目标由发布单和环境主机组解析，
// 因此部署步骤不能再混入构建编排，也不能要求操作者临时填写 HOST。
func (l *StepLib) ValidateBuildPipeline(steps []StepInst) error {
	if err := l.ValidatePipeline(steps); err != nil {
		return err
	}
	for index, inst := range steps {
		def := l.Get(inst.Step)
		if def != nil && def.Kind == "deploy" {
			return fmt.Errorf("第 %d 步 %s 是部署步骤；请通过发布单选择环境，主机由环境主机组自动解析", index+1, inst.Step)
		}
		if def != nil && def.ID == "shell-script" {
			script := def.RenderParams(inst)["SCRIPT"]
			if strings.TrimSpace(script) == "" || len(script) > 64<<10 || strings.ContainsRune(script, '\x00') {
				return fmt.Errorf("第 %d 步脚本内容应为非空 Bash 文本，且不超过 64 KiB", index+1)
			}
		}
	}
	return nil
}

func (l *StepLib) Get(id string) *StepDef {
	for _, s := range l.Load() {
		if s.ID == id {
			return &s
		}
	}
	return nil
}

func loadStepDef(dir string) *StepDef {
	raw, err := os.ReadFile(filepath.Join(dir, "step.yaml"))
	if err != nil {
		return nil
	}
	var def StepDef
	if yaml.Unmarshal(raw, &def) != nil {
		return nil
	}
	if def.ID == "" {
		def.ID = filepath.Base(dir)
	}
	if def.APIVersion == "" {
		def.APIVersion = "deploy.logplatform.io/v1"
	}
	if def.Kind == "" {
		def.Kind = map[string]string{"源码": "source", "构建": "build", "打包": "package", "部署": "deploy"}[def.Category]
	}
	if _, err := os.Stat(filepath.Join(dir, "run.sh")); err != nil && def.Kind != "deploy" {
		return nil // shell 步骤必须有执行体；部署类步骤走内置状态机
	}
	if def.Kind == "deploy" && def.Driver == "" {
		def.Driver = strings.TrimSuffix(def.ID, "-deploy")
	}
	def.Dir = dir
	return &def
}

func (d *StepDef) AcceptsArtifact(kind string) bool {
	if len(d.Accepts) == 0 {
		return true
	}
	for _, accepted := range d.Accepts {
		if accepted == kind {
			return true
		}
	}
	return false
}

// ---------- 流水线中的步骤实例 ----------

// StepInst 服务配置中的步骤实例（步骤 + 参数值）
type StepInst struct {
	Step   string            `json:"step"`
	Params map[string]string `json:"params,omitempty"`
}

// RenderParams 合并声明默认值与实例参数
func (d *StepDef) RenderParams(inst StepInst) map[string]string {
	m := map[string]string{}
	for _, p := range d.Params {
		m[p.Name] = p.Default
	}
	for k, v := range inst.Params {
		m[k] = v
	}
	return m
}

// 保留环境变量约定（执行体可用）
const (
	EnvWorkspace = "LP_WORKSPACE" // 当前运行工作区（源码目录）
	EnvService   = "LP_SERVICE"
	EnvVersion   = "LP_VERSION"
	EnvOutputDir = "LP_OUTPUT_DIR"   // 本次运行的产物目录
	EnvOutputsF  = "LP_OUTPUTS_FILE" // 步骤输出 kv 文件（追加）
	EnvHostsDB   = "LP_DATA_DIR"
)

// BuildEnv 构造步骤执行环境
func (d *StepDef) BuildEnv(inst StepInst, workspace, service, version, outputDir string) []string {
	env := append(os.Environ(),
		EnvWorkspace+"="+workspace,
		EnvService+"="+service,
		EnvVersion+"="+version,
		EnvOutputDir+"="+outputDir,
		EnvOutputsF+"="+filepath.Join(outputDir, ".outputs"),
	)
	for k, v := range d.RenderParams(inst) {
		env = append(env, "LP_"+k+"="+v)
	}
	return env
}

var _ = fmt.Sprintf
var _ = strings.TrimSpace
