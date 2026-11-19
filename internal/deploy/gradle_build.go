package deploy

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	gradleExecutableRE = regexp.MustCompile(`^[A-Za-z0-9_./-]+$`)
	gradleTaskRE       = regexp.MustCompile(`^[A-Za-z0-9_.:+-]+$`)
	gradleParamNameRE  = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	gradleEnvRefRE     = regexp.MustCompile(`^\$\{env\.([A-Za-z_][A-Za-z0-9_]*)\}$`)
)

type gradleProperty struct {
	key, value, envName string
}

func parseGradleProperties(raw, label string) ([]gradleProperty, error) {
	var properties []gradleProperty
	for lineNo, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok || key == "" || !gradleParamNameRE.MatchString(key) {
			return nil, fmt.Errorf("Gradle %s第 %d 行应为 key=value", label, lineNo+1)
		}
		envName := ""
		if match := gradleEnvRefRE.FindStringSubmatch(value); len(match) == 2 {
			envName = match[1]
		}
		if sensitiveParamRE.MatchString(key) && envName == "" {
			return nil, fmt.Errorf("Gradle 属性 %s 可能包含敏感信息，请改用 ${env.NAME} 引用变量组", key)
		}
		if strings.ContainsRune(value, '\x00') {
			return nil, fmt.Errorf("Gradle 属性 %s 含非法字符", key)
		}
		properties = append(properties, gradleProperty{key: key, value: value, envName: envName})
	}
	return properties, nil
}

func parseGradleTasks(raw, label string, allowedOptions ...string) ([]string, error) {
	items := strings.Fields(strings.ReplaceAll(raw, ",", " "))
	for _, item := range items {
		// Options must be explicitly allowed by the caller and match exactly.
		// Never allow arbitrary CLI flags (e.g. init scripts or project paths).
		allowed := false
		for _, option := range allowedOptions {
			allowed = allowed || item == option
		}
		if allowed {
			continue
		}
		if strings.HasPrefix(item, "-") || !gradleTaskRE.MatchString(item) {
			return nil, fmt.Errorf("Gradle %s含不支持的字符: %q", label, item)
		}
	}
	return items, nil
}

func appendGradleProperties(args []string, prefix string, properties []gradleProperty) []string {
	for _, property := range properties {
		if property.envName != "" {
			args = append(args, shellQuote(prefix+property.key+"=")+`"${`+property.envName+`}"`)
		} else {
			args = append(args, shellQuote(prefix+property.key+"="+property.value))
		}
	}
	return args
}

func prepareGradleBuildConfig(sv *Service) error {
	if sv.ArtifactType != "jar" && sv.ArtifactType != "image" && sv.ArtifactType != ArtifactJavaBundle {
		return fmt.Errorf("结构化 Gradle 构建仅支持 Java JAR 或 Java 镜像应用")
	}
	sv.GradleExecutable = strings.TrimSpace(sv.GradleExecutable)
	if sv.GradleExecutable == "" {
		sv.GradleExecutable = "auto"
	}
	if !gradleExecutableRE.MatchString(sv.GradleExecutable) {
		return fmt.Errorf("Gradle 可执行文件路径含不支持的字符")
	}
	sv.GradleTasks = strings.TrimSpace(sv.GradleTasks)
	if sv.GradleTasks == "" {
		sv.GradleTasks = "clean build"
	}
	tasks, err := parseGradleTasks(sv.GradleTasks, "任务", "--refresh-dependencies")
	if err != nil {
		return err
	}
	excluded, err := parseGradleTasks(sv.GradleExcludedTasks, "排除任务")
	if err != nil {
		return err
	}
	projectProperties, err := parseGradleProperties(sv.GradleProjectProperties, "-P 项目属性")
	if err != nil {
		return err
	}
	systemProperties, err := parseGradleProperties(sv.GradleSystemProperties, "-D 系统属性")
	if err != nil {
		return err
	}
	args := []string{}
	if sv.GradleExecutable == "auto" {
		args = append(args, autoBuildToolCommand("_lp_gradle", "./gradlew", "gradle", " Gradle"))
	} else {
		args = append(args, shellQuote(sv.GradleExecutable))
	}
	args = append(args, shellQuote("--no-daemon"), shellQuote("--console=plain"))
	args = appendGradleProperties(args, "-P", projectProperties)
	args = appendGradleProperties(args, "-D", systemProperties)
	for _, task := range excluded {
		args = append(args, shellQuote("-x"), shellQuote(task))
	}
	for _, task := range tasks {
		args = append(args, shellQuote(task))
	}
	if strings.TrimSpace(sv.ArtifactGlob) == "" {
		sv.ArtifactGlob = "build/libs/*.jar"
	}
	sv.BuildCmd = strings.Join(args, " ")
	return nil
}
