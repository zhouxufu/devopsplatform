package deploy

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	mavenExecutableRE = regexp.MustCompile(`^[A-Za-z0-9_./-]+$`)
	mavenGoalRE       = regexp.MustCompile(`^[A-Za-z0-9_.:-]+$`)
	mavenParamNameRE  = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	sensitiveParamRE  = regexp.MustCompile(`(?i)(password|passwd|secret|token|credential)`)
	mavenEnvRefRE     = regexp.MustCompile(`^\$\{env\.([A-Za-z_][A-Za-z0-9_]*)\}$`)
	mavenThreadsRE    = regexp.MustCompile(`^(?:[1-9][0-9]*|[0-9]+(?:\.[0-9]+)?C)$`)
)

type MavenExecutionOptions struct {
	Threads         string `json:"threads,omitempty"`
	UpdateSnapshots bool   `json:"update_snapshots,omitempty"`
}

// Additional arguments are CLI options, not shell or an alternate source of
// profiles/properties/settings. Keep build provenance in the structured fields.
func parseMavenArguments(raw string) ([]string, error) {
	if len(raw) > 4096 {
		return nil, fmt.Errorf("Maven 附加参数不能超过 4096 字节")
	}
	args := strings.Fields(raw)
	if len(args) > 64 {
		return nil, fmt.Errorf("Maven 附加参数过多")
	}
	flags := map[string]bool{
		"-e": true, "--errors": true, "-X": true, "--debug": true,
		"-q": true, "--quiet": true, "-o": true, "--offline": true,
		"-fae": true, "--fail-at-end": true, "-ff": true, "--fail-fast": true,
		"-B": true, "--batch-mode": true, "-ntp": true, "--no-transfer-progress": true,
		"-V": true, "--show-version": true, "-C": true, "--strict-checksums": true,
		"-c": true, "--lax-checksums": true, "-nsu": true, "--no-snapshot-updates": true,
		"-am": true, "--also-make": true, "-amd": true, "--also-make-dependents": true,
	}
	selectors := map[string]bool{"-pl": true, "--projects": true, "-rf": true, "--resume-from": true}
	for i := 0; i < len(args); i++ {
		if flags[args[i]] {
			continue
		}
		option, value, inline := strings.Cut(args[i], "=")
		if !selectors[option] {
			return nil, fmt.Errorf("Maven 附加参数不支持该选项；Profiles、-D、-T、-U 和 settings 请使用对应字段，仅填写执行选项（如 -e -X -o -fae、-pl 模块 -am）")
		}
		if !inline {
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("Maven 模块选择参数缺少模块")
			}
			value = args[i]
		}
		if value == "" || strings.HasPrefix(value, "-") || !regexp.MustCompile(`^[A-Za-z0-9_./:!,?*\-]+$`).MatchString(value) {
			return nil, fmt.Errorf("Maven 模块选择参数无效，请使用模块路径或 :artifactId，多个模块以逗号分隔")
		}
	}
	return args, nil
}

func (o *MavenExecutionOptions) arguments() ([]string, error) {
	args := []string{}
	if o == nil {
		return args, nil
	}
	threads := strings.TrimSpace(o.Threads)
	if threads != "" {
		number, err := strconv.ParseFloat(strings.TrimSuffix(threads, "C"), 64)
		if len(threads) > 32 || !mavenThreadsRE.MatchString(threads) || err != nil || number <= 0 {
			return nil, fmt.Errorf("Maven 并行线程须为正整数或 CPU 倍数，如 4、1C、2.5C")
		}
		args = append(args, "-T", threads)
	}
	if o.UpdateSnapshots {
		args = append(args, "-U")
	}
	return args, nil
}

type PipelineRunOptions struct {
	replayReasonScope       *buildReasonScope
	BuildEnvironment        string
	ReplayDelivery          *ServiceDelivery
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
	if sv.ArtifactType == ArtifactJavaBundle {
		if err := validateBundlePackageParams(bundlePackageParams(sv)); err != nil {
			return err
		}
		if len(sv.Steps) > 0 {
			count := 0
			for i := range sv.Steps {
				if sv.Steps[i].Step == javaBundleStep {
					count++
					if sv.Steps[i].Params == nil {
						sv.Steps[i].Params = map[string]string{}
					}
					sv.Steps[i].Params["ROOT"] = sv.ArtifactGlob
				}
			}
			if count != 1 {
				return fmt.Errorf("Java 整包流水线必须包含一个完整归档步骤")
			}
		}
	}
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
	if sv.ArtifactType != "jar" && sv.ArtifactType != "image" && sv.ArtifactType != ArtifactJavaBundle {
		return fmt.Errorf("结构化 Maven 构建仅支持 Java JAR 或 Java 镜像应用")
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
	sv.MavenThreads = strings.TrimSpace(sv.MavenThreads)
	execution := &MavenExecutionOptions{Threads: sv.MavenThreads, UpdateSnapshots: sv.MavenUpdateSnapshots}
	executionArgs, err := execution.arguments()
	if err != nil {
		return err
	}
	args = append(args, executionArgs...)
	sv.MavenArguments = strings.TrimSpace(sv.MavenArguments)
	additional, err := parseMavenArguments(sv.MavenArguments)
	if err != nil {
		return err
	}
	args = append(args, additional...)
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
			if copyService.ArtifactType != "image" || copyService.BuildCmd != "" {
				copyService.Steps[i].Params["COMMAND"] = copyService.BuildCmd
			}
			if copyService.ArtifactType != "image" || copyService.PreCmd != "" {
				copyService.Steps[i].Params["PRE"] = copyService.PreCmd
			}
		case "npm-build":
			if copyService.ArtifactType != "image" || copyService.BuildCmd != "" {
				copyService.Steps[i].Params["COMMAND"] = copyService.BuildCmd
			}
		case javaBundleStep:
			copyService.Steps[i].Params["ROOT"] = copyService.ArtifactGlob
		case "package-jar", "package-static":
			copyService.Steps[i].Params["GLOB"] = copyService.ArtifactGlob
		case "docker-build":
			copyService.Steps[i].Params["IMAGE_NAME"] = copyService.ImageName
		}
	}
	return &copyService, nil
}
