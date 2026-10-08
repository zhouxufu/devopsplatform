package deploy

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	mavenExecutableRE = regexp.MustCompile(`^[A-Za-z0-9_./-]+$`)
	mavenGoalRE       = regexp.MustCompile(`^[A-Za-z0-9_.:-]+$`)
	mavenParamNameRE  = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	sensitiveParamRE  = regexp.MustCompile(`(?i)(password|passwd|secret|token|credential)`)
	mavenEnvRefRE     = regexp.MustCompile(`^\$\{env\.([A-Za-z_][A-Za-z0-9_]*)\}$`)
)

type PipelineRunOptions struct {
	Branch                  string
	DeployEnvironment       string
	DeployReason            string
	MavenProfiles           string
	MavenProperties         string
	OverrideMaven           bool
	GradleTasks             string
	GradleExcludedTasks     string
	GradleProjectProperties string
	GradleSystemProperties  string
	OverrideGradle          bool
	Trigger                 string
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

// autoBuildToolCommand 在源码检出完成后解析构建工具：优先 Wrapper，其次 Runner PATH。
// Wrapper 即使没有可执行位也可由 sh 启动，兼容 ZIP 上传或权限丢失的代码目录。
func autoBuildToolCommand(functionName, wrapper, systemCommand, label string) string {
	return functionName + `() { if [ -x ` + shellQuote(wrapper) + ` ]; then ` + shellQuote(wrapper) + ` "$@"; ` +
		`elif [ -f ` + shellQuote(wrapper) + ` ]; then sh ` + shellQuote(wrapper) + ` "$@"; ` +
		`elif command -v ` + shellQuote(systemCommand) + ` >/dev/null 2>&1; then ` + shellQuote(systemCommand) + ` "$@"; ` +
		`else echo ` + shellQuote("未找到"+label+" Wrapper，Runner PATH 中也没有 "+systemCommand) + ` >&2; return 127; fi; }; ` + functionName
}

func parseMavenProfiles(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var profiles []string
	for _, item := range strings.Split(raw, ",") {
		profile := strings.TrimSpace(item)
		if profile == "" || !mavenParamNameRE.MatchString(profile) {
			return nil, fmt.Errorf("Maven profile 含不支持的字符: %q", profile)
		}
		profiles = append(profiles, profile)
	}
	return profiles, nil
}

type mavenProperty struct {
	key, value, envName string
}

func parseMavenProperties(raw string) ([]mavenProperty, error) {
	var properties []mavenProperty
	for lineNo, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok || key == "" || !mavenParamNameRE.MatchString(key) {
			return nil, fmt.Errorf("Maven 属性第 %d 行应为 key=value", lineNo+1)
		}
		envName := ""
		if match := mavenEnvRefRE.FindStringSubmatch(value); len(match) == 2 {
			envName = match[1]
		}
		if sensitiveParamRE.MatchString(key) && envName == "" {
			return nil, fmt.Errorf("Maven 属性 %s 可能包含敏感信息，请改用 settings.xml 或运行节点环境变量", key)
		}
		if strings.ContainsRune(value, '\x00') {
			return nil, fmt.Errorf("Maven 属性 %s 含非法字符", key)
		}
		properties = append(properties, mavenProperty{key: key, value: value, envName: envName})
	}
	return properties, nil
}

// prepareBuildConfig 校验结构化构建参数并生成实际执行命令。命令中的每个参数都
// 单独 shell quote，避免配置值改变命令结构。
func prepareBuildConfig(sv *Service) error {
	sv.BuildTool = strings.TrimSpace(sv.BuildTool)
	if sv.BuildTool == "" {
		sv.BuildTool = "command"
	}
	if sv.BuildTool == "command" {
		if strings.TrimSpace(sv.BuildCmd) == "" && sv.ArtifactType != "image" {
			return fmt.Errorf("构建命令不能为空")
		}
		return nil
	}
	if sv.BuildTool == "gradle" {
		return prepareGradleBuildConfig(sv)
	}
	if sv.BuildTool != "maven" {
		return fmt.Errorf("build_tool 仅支持 command、maven 或 gradle")
	}
	if sv.ArtifactType != "jar" {
		return fmt.Errorf("结构化 Maven 构建目前只支持 jar 服务")
	}
	sv.MavenExecutable = strings.TrimSpace(sv.MavenExecutable)
	if sv.MavenExecutable == "" {
		sv.MavenExecutable = "auto"
	}
	if !mavenExecutableRE.MatchString(sv.MavenExecutable) {
		return fmt.Errorf("Maven 可执行文件路径含不支持的字符")
	}
	sv.MavenGoals = strings.TrimSpace(sv.MavenGoals)
	if sv.MavenGoals == "" {
		sv.MavenGoals = "clean package"
	}
	goals := strings.Fields(sv.MavenGoals)
	for _, goal := range goals {
		if !mavenGoalRE.MatchString(goal) || strings.HasPrefix(goal, "-") {
			return fmt.Errorf("Maven goal 含不支持的字符: %q", goal)
		}
	}
	profiles, err := parseMavenProfiles(sv.MavenProfiles)
	if err != nil {
		return err
	}
	properties, err := parseMavenProperties(sv.MavenProperties)
	if err != nil {
		return err
	}
	sv.MavenSettingsPath = strings.TrimSpace(sv.MavenSettingsPath)
	if sv.MavenSettingsPath != "" && !filepath.IsAbs(sv.MavenSettingsPath) {
		return fmt.Errorf("Maven settings.xml 路径必须是绝对路径")
	}
	args := []string{"--batch-mode", "--no-transfer-progress"}
	if sv.MavenSettingsPath != "" {
		args = append(args, "--settings", sv.MavenSettingsPath)
	}
	if len(profiles) > 0 {
		args = append(args, "-P"+strings.Join(profiles, ","))
	}
	quoted := make([]string, 0, len(args)+len(properties)+len(goals)+1)
	if sv.MavenExecutable == "auto" {
		quoted = append(quoted, autoBuildToolCommand("_lp_maven", "./mvnw", "mvn", " Maven"))
	} else {
		quoted = append(quoted, shellQuote(sv.MavenExecutable))
	}
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	for _, property := range properties {
		if property.envName != "" {
			quoted = append(quoted, shellQuote("-D"+property.key+"=")+`"${`+property.envName+`}"`)
		} else {
			quoted = append(quoted, shellQuote("-D"+property.key+"="+property.value))
		}
	}
	for _, goal := range goals {
		quoted = append(quoted, shellQuote(goal))
	}
	sv.BuildCmd = strings.Join(quoted, " ")
	return nil
}

func serviceForPipelineRun(sv *Service, opts PipelineRunOptions) (*Service, error) {
	copyService := *sv
	copyService.Steps = make([]StepInst, len(sv.Steps))
	for i, step := range sv.Steps {
		copyService.Steps[i] = StepInst{Step: step.Step, Params: map[string]string{}}
		for key, value := range step.Params {
			copyService.Steps[i].Params[key] = value
		}
	}
	if strings.TrimSpace(opts.Branch) != "" {
		copyService.Branch = strings.TrimSpace(opts.Branch)
	}
	if copyService.BuildTool == "maven" && opts.OverrideMaven {
		copyService.MavenProfiles = opts.MavenProfiles
		copyService.MavenProperties = opts.MavenProperties
	}
	if copyService.BuildTool == "gradle" && opts.OverrideGradle {
		copyService.GradleTasks = opts.GradleTasks
		copyService.GradleExcludedTasks = opts.GradleExcludedTasks
		copyService.GradleProjectProperties = opts.GradleProjectProperties
		copyService.GradleSystemProperties = opts.GradleSystemProperties
	}
	if err := prepareBuildConfig(&copyService); err != nil {
		return nil, err
	}
	for i := range copyService.Steps {
		if copyService.Steps[i].Params == nil {
			copyService.Steps[i].Params = map[string]string{}
		}
		switch copyService.Steps[i].Step {
		case "git-checkout":
			copyService.Steps[i].Params["GIT_URL"] = copyService.GitURL
			copyService.Steps[i].Params["BRANCH"] = copyService.Branch
		case "java-build":
			copyService.Steps[i].Params["COMMAND"] = copyService.BuildCmd
			copyService.Steps[i].Params["PRE"] = copyService.PreCmd
		case "npm-build":
			copyService.Steps[i].Params["COMMAND"] = copyService.BuildCmd
		case "package-jar", "package-static":
			copyService.Steps[i].Params["GLOB"] = copyService.ArtifactGlob
		case "docker-build":
			copyService.Steps[i].Params["IMAGE_NAME"] = copyService.ImageName
		}
	}
	return &copyService, nil
}
