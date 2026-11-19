package smartdocplugin

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"logplatform/pkg/apidoc"
	p "logplatform/pkg/extensionprotocol"
	"logplatform/pkg/workflownode"
	"net/http"
	"regexp"
	"strings"
)

const ID = "smart-doc"
const Version = "1.1.0"

type Settings struct {
	ConfigFile    string `json:"config_file"`
	OutputFile    string `json:"output_file"`
	PluginVersion string `json:"plugin_version"`
	ModulePOM     string `json:"module_pom,omitempty"`
	GradleTask    string `json:"gradle_task,omitempty"`
}

var versionRE = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-.][A-Za-z0-9]+)*$`)
var gradleTaskRE = regexp.MustCompile(`^(?:[A-Za-z0-9_][A-Za-z0-9_.-]*:)*smartDocOpenApi$`)

func Prepare(in apidoc.PlanInput) (*apidoc.Plan, error) {
	if in.Tool != "maven" && in.Tool != "gradle" {
		return nil, &p.Error{Code: "unsupported", Message: "smart-doc 支持 Maven 和 Gradle，请先配置应用构建工具"}
	}
	var s Settings
	decoder := json.NewDecoder(bytes.NewReader(in.Settings))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&s) != nil {
		return nil, &p.Error{Code: "invalid_argument", Message: "smart-doc 配置无效"}
	}
	if s.ConfigFile == "" {
		s.ConfigFile = "src/main/resources/smart-doc.json"
	}
	if s.OutputFile == "" {
		s.OutputFile = "target/api-docs/openapi.json"
		if in.Tool == "gradle" {
			s.OutputFile = "build/api-docs/openapi.json"
		}
	}
	if in.Tool == "maven" && (!versionRE.MatchString(s.PluginVersion) || strings.Contains(strings.ToUpper(s.PluginVersion), "SNAPSHOT")) {
		return nil, &p.Error{Code: "invalid_argument", Message: "请固定实际使用的 smart-doc Maven 插件版本"}
	}
	if !apidoc.RelativeFile(s.ConfigFile) || s.ModulePOM != "" {
		return nil, &p.Error{Code: "invalid_argument", Message: "配置文件须为当前构建目录内相对路径，不支持 module_pom"}
	}
	plan := &apidoc.Plan{Version: 1, Provider: ID, ProviderVersion: Version, Tool: "maven", Tasks: []string{"com.github.shalousun:smart-doc-maven-plugin:" + s.PluginVersion + ":openapi"}, Properties: map[string]string{"file.encoding": "UTF-8", "configFile": "./" + s.ConfigFile}, InputFiles: []string{s.ConfigFile}, OutputFile: s.OutputFile, Format: "openapi-json"}
	if in.Tool == "gradle" {
		if s.GradleTask == "" {
			s.GradleTask = "smartDocOpenApi"
		}
		if !gradleTaskRE.MatchString(s.GradleTask) {
			return nil, &p.Error{Code: "invalid_argument", Message: "Gradle 文档任务须为 smartDocOpenApi 或 子项目:smartDocOpenApi"}
		}
		plan.Tool = "gradle"
		plan.Tasks = []string{s.GradleTask}
		// Gradle maps this system-property namespace to project properties (-P).
		// Keep the existing vendor-neutral plan and the application's own -P values.
		plan.Properties = map[string]string{"file.encoding": "UTF-8", "org.gradle.project.smartdoc.configFile": "./" + s.ConfigFile}
	} else if s.GradleTask != "" {
		return nil, &p.Error{Code: "invalid_argument", Message: "Maven 来源不能配置 Gradle 任务"}
	}
	if err := apidoc.ValidatePlan(*plan); err != nil {
		return nil, &p.Error{Code: "invalid_argument", Message: err.Error()}
	}
	return plan, nil
}
func Handler(token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		write := func(v any) { _ = json.NewEncoder(w).Encode(v) }
		if r.Method == "GET" && r.URL.Path == "/v1/describe" {
			write(p.Description{Protocol: p.Major, ID: ID, Version: Version, Capabilities: []string{apidoc.Capability, workflownode.Capability}})
			return
		}
		if r.Method == "GET" && r.URL.Path == "/v1/health" {
			write(map[string]bool{"ok": true})
			return
		}
		if r.Method == "POST" && r.URL.Path == "/v1/test" {
			write(p.Response{Data: json.RawMessage(`{"ok":true}`)})
			return
		}
		if r.Method != "POST" || r.URL.Path != "/v1/invoke" {
			http.NotFound(w, r)
			return
		}
		fail := func(code, msg string) { write(p.Response{Error: &p.Error{Code: code, Message: msg}}) }
		var req p.Request
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req) != nil {
			fail("invalid_argument", "文档请求格式无效")
			return
		}
		if req.Capability == workflownode.Capability && req.Operation == "describe" {
			raw, _ := json.Marshal(workflownode.Descriptor{Version: 1, Key: "api-documentation", Name: "生成接口文档", Executor: apidoc.Capability, Configuration: "application-binding"})
			write(p.Response{Data: raw})
			return
		}
		if req.Actor.Role != "admin" {
			fail("permission_denied", "文档生成计划仅允许受授权构建与管理员请求")
			return
		}
		if req.Capability != apidoc.Capability {
			fail("unsupported", "不支持此文档操作")
			return
		}
		if req.Operation == "schema" {
			var in apidoc.PlanInput
			if len(req.Input) > 0 && json.Unmarshal(req.Input, &in) != nil {
				fail("invalid_argument", "文档输入无效")
				return
			}
			if in.Tool == "" {
				in.Tool = "maven"
			}
			if in.Tool != "maven" && in.Tool != "gradle" {
				fail("unsupported", "smart-doc 支持 Maven 和 Gradle")
				return
			}
			output := "target/api-docs/openapi.json"
			if in.Tool == "gradle" {
				output = "build/api-docs/openapi.json"
			}
			fields := []map[string]any{
				{"key": "config_file", "label": "仓库内配置文件", "required": true, "default": "src/main/resources/smart-doc.json"},
				{"key": "output_file", "label": "生成的 OpenAPI 文件", "required": true, "default": output, "help": "需与 smart-doc 配置的 outPath 一致；输出须位于当前构建目录的 target/ 或 build/"},
			}
			if in.Tool == "maven" {
				fields = append(fields, map[string]any{"key": "plugin_version", "label": "项目使用的 Maven 插件版本", "required": true, "help": "填写已有项目验证过的固定版本，不自动升级工具链"})
			} else {
				fields = append(fields, map[string]any{"key": "gradle_task", "label": "Gradle 文档任务", "default": "smartDocOpenApi", "help": "仓库须应用 smart-doc Gradle 插件并固定版本，平台复用已有 Gradle/Wrapper 和 JDK。可填写 子项目:smartDocOpenApi；旧版插件须在 build.gradle 中将 smartdoc.configFile 项目属性接入 configFile，路径相对构建工作目录。"})
			}
			raw, _ := json.Marshal(map[string]any{"tools": []string{"maven", "gradle"}, "fields": fields})
			write(p.Response{Data: raw})
			return
		}
		if req.Operation != "plan" {
			fail("unsupported", "不支持此文档操作")
			return
		}
		var in apidoc.PlanInput
		if json.Unmarshal(req.Input, &in) != nil {
			fail("invalid_argument", "文档输入无效")
			return
		}
		plan, err := Prepare(in)
		if err != nil {
			if e, ok := err.(*p.Error); ok {
				write(p.Response{Error: e})
			} else {
				fail("invalid_argument", err.Error())
			}
			return
		}
		raw, _ := json.Marshal(plan)
		write(p.Response{Data: raw})
	})
}
