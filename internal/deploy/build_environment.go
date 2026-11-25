package deploy

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// BuildEnvironment only changes compilation. Checkout and artifact archiving stay
// on the runner; deployment never inherits the build container or its credentials.
type BuildEnvironment struct {
	Mode        string `json:"mode"` // local (backwards compatible) | container
	Preset      string `json:"preset,omitempty"`
	Image       string `json:"image,omitempty"`        // only used by custom preset
	Network     string `json:"network,omitempty"`      // bridge (default) | host
	ImageSource string `json:"image_source,omitempty"` // registry (default) | dockerfile
	Dockerfile  string `json:"dockerfile,omitempty"`   // repository-relative
	Context     string `json:"context,omitempty"`      // repository-relative; default .
}

var buildImages = map[string]string{
	"maven-jdk8":   "maven:3.9.9-eclipse-temurin-8",
	"maven-jdk17":  "maven:3.9.9-eclipse-temurin-17",
	"maven-jdk21":  "maven:3.9.9-eclipse-temurin-21",
	"gradle-jdk8":  "gradle:8.14.3-jdk8",
	"gradle-jdk17": "gradle:8.14.3-jdk17",
	"gradle-jdk21": "gradle:8.14.3-jdk21",
	"node20":       "node:20-bookworm", "node22": "node:22-bookworm",
}

func (e *BuildEnvironment) isolated() bool { return e != nil && e.Mode == "container" }
func (e *BuildEnvironment) image() string {
	if e.Preset == "custom" {
		return strings.TrimSpace(e.Image)
	}
	return buildImages[e.Preset]
}

var buildImageRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]*$`)

func validateBuildEnvironment(e *BuildEnvironment, sv *Service) error {
	if e == nil || e.Mode == "" || e.Mode == "local" {
		return nil
	}
	if e.Mode != "container" {
		return fmt.Errorf("构建环境仅支持本机或隔离容器")
	}
	if e.Preset != "custom" && buildImages[e.Preset] == "" {
		return fmt.Errorf("请选择有效的构建环境模板")
	}
	image := e.image()
	if len(image) > 512 || !buildImageRE.MatchString(image) {
		return fmt.Errorf("构建镜像地址无效，不可填写 Docker 命令或运行参数")
	}
	if !strings.Contains(filepath.Base(image), ":") {
		return fmt.Errorf("构建镜像必须指定版本标签或 sha256 digest")
	}
	if e.Network != "" && e.Network != "bridge" && e.Network != "host" {
		return fmt.Errorf("构建网络仅支持默认隔离网络或宿主机网络")
	}
	if err := validateBuildImageSource(e); err != nil {
		return err
	}
	if sv != nil && e.Preset != "custom" {
		if sv.BuildTool == "gradle" && !strings.HasPrefix(e.Preset, "gradle-") {
			return fmt.Errorf("Gradle 构建请选择 Gradle 环境或自定义镜像")
		}
		if sv.BuildTool == "maven" && !strings.HasPrefix(e.Preset, "maven-") {
			return fmt.Errorf("Maven 构建请选择 Maven 环境或自定义镜像")
		}
	}
	return nil
}

func checkBuildDocker() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Never disclose Docker daemon configuration / credentials in the error.
	if err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}").Run(); err != nil {
		return fmt.Errorf("隔离构建需要 Runner 上可用的 Docker 引擎：请确认已安装、启动，且平台运行用户有访问权限")
	}
	return nil
}

// Absolute, same-path binds let existing commands and Maven settings paths keep
// working. --mount (not -v) refuses missing paths instead of creating root dirs.
func buildMount(source, target string, readonly bool) (string, error) {
	abs, err := filepath.Abs(source)
	if err != nil {
		return "", err
	}
	if strings.ContainsAny(abs+target, ",\n\r") {
		return "", fmt.Errorf("容器挂载路径不能包含逗号或换行")
	}
	if _, err := os.Stat(abs); err != nil {
		return "", err
	}
	value := "type=bind,source=" + abs + ",target=" + target
	if readonly {
		value += ",readonly"
	}
	return value, nil
}

func containerBuildArgs(sv *Service, def *StepDef, env []string, cache, name, image string, stagedSettings ...string) ([]string, []string, error) {
	values := map[string]string{}
	for _, item := range env {
		if k, v, ok := strings.Cut(item, "="); ok {
			values[k] = v
		}
	}
	args := []string{"run", "--rm", "--name", name, "--init", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), "--entrypoint", "bash"}
	if sv.BuildEnvironment != nil && sv.BuildEnvironment.Network == "host" {
		// Explicit opt-in for trusted, self-hosted runners whose intranet DNS,
		// proxy or artifact repositories are only reachable from the host network.
		args = append(args, "--network", "host")
	}
	for _, pair := range [][3]string{{values[EnvWorkspace], values[EnvWorkspace], ""}, {values[EnvOutputDir], values[EnvOutputDir], ""}, {def.Dir, "/opt/devops-step", "ro"}, {cache, "/lp-home", ""}} {
		mount, err := buildMount(pair[0], pair[1], pair[2] == "ro")
		if err != nil {
			return nil, nil, fmt.Errorf("构建目录不可挂载: %w", err)
		}
		args = append(args, "--mount", mount)
	}
	if sv.MavenSettingsPath != "" {
		if !filepath.IsAbs(sv.MavenSettingsPath) {
			return nil, nil, fmt.Errorf("隔离构建的 Maven settings.xml 必须使用绝对路径")
		}
		info, err := os.Stat(sv.MavenSettingsPath)
		if err != nil || !info.Mode().IsRegular() {
			return nil, nil, fmt.Errorf("Maven settings.xml 不存在或不是普通文件")
		}
		if len(stagedSettings) != 1 || stagedSettings[0] == "" || strings.ContainsAny(stagedSettings[0]+sv.MavenSettingsPath, ",:\r\n") {
			return nil, nil, fmt.Errorf("隔离构建尚未准备可读的 Maven 临时配置")
		}
		staged, err := os.Stat(stagedSettings[0])
		if err != nil || !staged.Mode().IsRegular() {
			return nil, nil, fmt.Errorf("Maven 临时配置不可用")
		}
		// :Z applies only to this per-build private copy, never the original
		// host settings or a shared/system directory. Keep the original
		// container target so frozen commands and old retries still work.
		args = append(args, "--volume", stagedSettings[0]+":"+sv.MavenSettingsPath+":ro,Z")
	}
	// Only forward the step contract and explicitly configured build variables.
	// In particular, do not copy host HOME/PATH, cloud credentials or Git login.
	allowed := map[string]bool{EnvWorkspace: true, EnvService: true, EnvVersion: true, EnvOutputDir: true, EnvOutputsF: true}
	for _, p := range def.Params {
		allowed["LP_"+p.Name] = true
	}
	for k := range sv.RuntimeEnv {
		if strings.HasPrefix(k, "GIT_") || strings.HasPrefix(k, "DOCKER_") {
			continue
		}
		if k == "HOME" || k == "PATH" || k == "JAVA_HOME" || k == "GRADLE_USER_HOME" || strings.HasPrefix(k, "LP_") {
			return nil, nil, fmt.Errorf("隔离构建不能用变量组覆盖 %s，请使用镜像内工具路径", k)
		}
		allowed[k] = true
	}
	keys := make([]string, 0, len(allowed))
	for k := range allowed {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	childEnv := append([]string{}, os.Environ()...)
	for _, k := range keys {
		// Values are inherited by Docker, not exposed as command-line arguments.
		args = append(args, "--env", k)
		childEnv = append(childEnv, k+"="+values[k])
	}
	args = append(args, "--env", "HOME=/lp-home", "--env", "GRADLE_USER_HOME=/lp-home/.gradle", "--workdir", values[EnvWorkspace], image, "-e", "-c", `export JAVA_TOOL_OPTIONS="${JAVA_TOOL_OPTIONS:-} -Duser.home=/lp-home"
for tool in java mvn gradle node npm; do
  if command -v "$tool" >/dev/null 2>&1; then
    case "$tool" in java) java -version;; *) "$tool" --version;; esac
  fi
done
bash /opt/devops-step/run.sh`)
	return args, childEnv, nil
}

func (b *Builder) execBuildStep(sv *Service, def *StepDef, env []string, emit func(string)) int {
	return b.execBuildStepContext(context.Background(), sv, def, env, emit)
}
func (b *Builder) execBuildStepContext(parent context.Context, sv *Service, def *StepDef, env []string, emit func(string)) int {
	if !sv.BuildEnvironment.isolated() || def.Kind != "build" {
		return b.execStepContext(parent, def, env, emit)
	}
	settings, cleanupSettings, err := stageBuildSettings(sv.MavenSettingsPath)
	if err != nil {
		emit("✗ " + err.Error() + "\n")
		return -1
	}
	defer cleanupSettings()
	if err := checkGradleWrapperCompatibility(sv, b.buildDir(sv)); err != nil {
		emit("✗ " + err.Error() + "\n")
		return -1
	}
	ctx, cancel := context.WithTimeout(parent, 40*time.Minute)
	defer cancel()
	image := sv.BuildEnvironment.image()
	imageID, err := prepareBuildImage(ctx, sv.BuildEnvironment, b.buildDir(sv), emit,
		func() (string, error) {
			out, err := exec.CommandContext(ctx, "docker", "image", "inspect", "--format", "{{.Id}}", image).Output()
			return strings.TrimSpace(string(out)), err
		}, func(args []string) error {
			if runBuildCommand(exec.CommandContext(ctx, "docker", args...), emit) != 0 {
				return fmt.Errorf("Docker 命令失败")
			}
			return nil
		})
	if err != nil {
		emit("✗ " + err.Error() + "\n")
		return -1
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(image)))
	cache, _ := filepath.Abs(filepath.Join(b.workspaceDir, ".build-cache", sv.Name, hash[:16]))
	if err := os.MkdirAll(cache, 0700); err != nil {
		emit("✗ 创建构建缓存失败\n")
		return -1
	}
	name := "devops-build-" + randomToken()
	args, childEnv, err := containerBuildArgs(sv, def, env, cache, name, strings.TrimSpace(string(imageID)), settings)
	if err != nil {
		emit("✗ " + err.Error() + "\n")
		return -1
	}
	emit("▶ 隔离构建：" + image + " · " + strings.TrimSpace(string(imageID)) + "\n")
	// Killing the Docker client alone does not guarantee container termination.
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		_ = exec.CommandContext(cleanup, "docker", "rm", "-f", name).Run()
	}()
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = childEnv
	return runBuildCommand(cmd, emit)
}

func checkGradleWrapperCompatibility(sv *Service, workspace string) error {
	if sv.BuildTool != "gradle" || !sv.BuildEnvironment.isolated() || sv.BuildEnvironment.Preset == "custom" {
		return nil
	}
	if sv.GradleExecutable != "" && sv.GradleExecutable != "auto" && sv.GradleExecutable != "./gradlew" {
		return nil
	}
	if _, err := os.Stat(filepath.Join(workspace, "gradlew")); err != nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(workspace, "gradle/wrapper/gradle-wrapper.properties"))
	if err != nil {
		return nil
	}
	match := regexp.MustCompile(`gradle-(\d+)\.(\d+)[^/]*-(?:bin|all)\.zip`).FindStringSubmatch(string(data))
	if len(match) != 3 {
		return nil
	}
	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	preset := sv.BuildEnvironment.Preset
	if (preset == "gradle-jdk21" && (major < 8 || major == 8 && minor < 5)) || (preset == "gradle-jdk17" && (major < 7 || major == 7 && minor < 3)) || (preset == "gradle-jdk8" && major >= 9) {
		return fmt.Errorf("Gradle Wrapper %s.%s 与所选 %s 不兼容：JDK 21 需要 Gradle ≥ 8.5，JDK 17 需要 ≥ 7.3，JDK 8 不支持 Gradle 9；请更换构建环境或升级项目 Wrapper", match[1], match[2], preset)
	}
	return nil
}
