package deploy

import (
	"fmt"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type DeployDriverManifest struct {
	APIVersion   string      `json:"api_version"`
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	Accepts      []string    `json:"accepts"`
	Capabilities []string    `json:"capabilities"`
	Params       []StepParam `json:"params,omitempty"`
}

// DeployDriver is the target-specific boundary. Environment protection,
// concurrency, audit records and release state remain owned by Deployer.
type DeployDriver interface {
	Manifest() DeployDriverManifest
	Validate(service *Service, host Host, artifact Artifact) error
	Deploy(runtime *Deployer, deployID int64, service *Service, host Host, artifact Artifact, version string) string
}

func builtinDeployDrivers() []DeployDriver {
	return []DeployDriver{sshJarDriver{}, sshStaticDriver{}, dockerHostDriver{}, aliyunSAEDriver{}}
}

func (d *Deployer) DriverManifests() []DeployDriverManifest {
	out := make([]DeployDriverManifest, 0, len(d.drivers))
	for _, driver := range d.drivers {
		out = append(out, driver.Manifest())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func requireLocalArtifact(artifact Artifact, kind string) error {
	if artifact.Kind != kind {
		return fmt.Errorf("部署 Driver 需要 %s 制品，实际为 %s", kind, artifact.Kind)
	}
	info, err := os.Stat(artifact.URI)
	if err != nil || info.IsDir() {
		return fmt.Errorf("制品文件不可用: %s", artifact.URI)
	}
	digest, _, err := artifactMetadata(artifact.URI)
	if err != nil {
		return err
	}
	if artifact.Digest != "" && digest != artifact.Digest {
		return fmt.Errorf("制品摘要不匹配，拒绝部署")
	}
	return nil
}

type sshJarDriver struct{}

func (sshJarDriver) Manifest() DeployDriverManifest {
	return DeployDriverManifest{
		APIVersion: "deploy.logplatform.io/v1", ID: "ssh-jar", Name: "SSH JAR",
		Accepts: []string{ArtifactJar}, Capabilities: []string{"deploy", "verify", "rollback"},
	}
}
func (sshJarDriver) Validate(sv *Service, _ Host, a Artifact) error {
	if err := requireLocalArtifact(a, ArtifactJar); err != nil {
		return err
	}
	if !path.IsAbs(strings.TrimSpace(sv.RemoteDir)) {
		return fmt.Errorf("远程目录必须是绝对路径")
	}
	if managedJava(sv) {
		if err := validateManagedJava(sv); err != nil {
			return err
		}
		version, _ := strconv.Atoi(sv.DeployConfig["java_version"])
		return validateJavaBytecode(a.URI, version)
	}
	if strings.TrimSpace(sv.StartCmd) == "" {
		return fmt.Errorf("JAR 部署必须配置启动命令")
	}
	return nil
}
func (sshJarDriver) Deploy(d *Deployer, id int64, sv *Service, h Host, a Artifact, version string) string {
	if managedJava(sv) {
		return d.deployManagedJava(id, sv, h, a.URI)
	}
	return d.deployJar(id, sv, h, a.URI, version)
}

type sshStaticDriver struct{}

func (sshStaticDriver) Manifest() DeployDriverManifest {
	return DeployDriverManifest{
		APIVersion: "deploy.logplatform.io/v1", ID: "ssh-static", Name: "SSH 静态站点",
		Accepts: []string{ArtifactStatic}, Capabilities: []string{"deploy", "verify", "rollback", "atomic-switch"},
	}
}
func (sshStaticDriver) Validate(sv *Service, _ Host, a Artifact) error {
	if err := requireLocalArtifact(a, ArtifactStatic); err != nil {
		return err
	}
	if !path.IsAbs(strings.TrimSpace(sv.RemoteDir)) {
		return fmt.Errorf("远程目录必须是绝对路径")
	}
	return nil
}
func (sshStaticDriver) Deploy(d *Deployer, id int64, sv *Service, h Host, a Artifact, version string) string {
	return d.deployStaticAtomic(id, sv, h, a.URI, version)
}

type dockerHostDriver struct{}

func (dockerHostDriver) Manifest() DeployDriverManifest {
	return DeployDriverManifest{
		APIVersion: "deploy.logplatform.io/v1", ID: "docker-host", Name: "Docker Host（SSH）",
		Accepts: []string{ArtifactOCI}, Capabilities: []string{"deploy", "verify", "rollback", "immutable-digest"},
		Params: []StepParam{
			{Name: "container_name", Type: "input", Label: "容器名"},
			{Name: "ports", Type: "input", Label: "端口映射（逗号分隔）"},
			{Name: "env_file", Type: "input", Label: "远程 env-file"},
			{Name: "run_args", Type: "shell", Label: "额外 docker run 参数"},
		},
	}
}
func (dockerHostDriver) Validate(sv *Service, _ Host, a Artifact) error {
	if a.Kind != ArtifactOCI {
		return fmt.Errorf("docker-host 需要 oci-image 制品")
	}
	if !strings.Contains(a.URI, "@sha256:") || !strings.HasSuffix(a.URI, a.Digest) {
		return fmt.Errorf("Docker 部署必须使用已推送的 image@sha256:digest，不允许可变 tag")
	}
	if cfg := sv.DeployConfig; cfg != nil {
		for _, port := range strings.Split(cfg["ports"], ",") {
			port = strings.TrimSpace(port)
			if port != "" && !regexp.MustCompile(`^[0-9]{1,5}:[0-9]{1,5}(/(tcp|udp))?$`).MatchString(port) {
				return fmt.Errorf("端口映射格式不正确: %s", port)
			}
		}
	}
	return nil
}
func (dockerHostDriver) Deploy(d *Deployer, id int64, sv *Service, h Host, a Artifact, _ string) string {
	return d.deployDockerHost(id, sv, h, a)
}

var safeReleaseRE = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)

func (d *Deployer) deployStaticAtomic(id int64, sv *Service, host Host, artifact, version string) string {
	remote := strings.TrimSuffix(sv.RemoteDir, "/")
	releaseName := strings.Trim(safeReleaseRE.ReplaceAllString(version, "-"), "-")
	if releaseName == "" {
		releaseName = fmt.Sprint(time.Now().Unix())
	}
	releaseDir := remote + "/releases/" + releaseName
	archive := remote + "/.incoming-" + releaseName + ".tar.gz"
	step := func(name, cmd string) (string, bool) {
		d.emit(id, "\n$ "+cmd+"\n")
		out, err := SSHExec(host, cmd, 5*time.Minute)
		if out != "" {
			d.emit(id, out)
		}
		if err != nil {
			d.emit(id, fmt.Sprintf("✗ %s 失败: %v\n", name, err))
			return out, false
		}
		return strings.TrimSpace(out), true
	}
	if _, ok := step("准备版本目录", "mkdir -p "+shellQuote(remote+"/releases")); !ok {
		return "failed"
	}
	previous, _ := SSHExec(host, "readlink "+shellQuote(remote+"/current")+" 2>/dev/null || true", time.Minute)
	d.emit(id, "⇪ 上传 "+artifact+"\n")
	if err := SSHUpload(host, artifact, archive); err != nil {
		d.emit(id, "✗ 上传失败: "+err.Error()+"\n")
		return "failed"
	}
	publish := "rm -rf " + shellQuote(releaseDir) + " && mkdir -p " + shellQuote(releaseDir) + " && tar -xzf " + shellQuote(archive) + " -C " + shellQuote(releaseDir) + " --strip-components=1 && ln -sfn " + shellQuote(releaseDir) + " " + shellQuote(remote+"/current.new") + " && mv -Tf " + shellQuote(remote+"/current.new") + " " + shellQuote(remote+"/current") + " && rm -f " + shellQuote(archive)
	if _, ok := step("原子切换新版本", publish); !ok {
		if prev := strings.TrimSpace(previous); prev != "" {
			_, _ = SSHExec(host, "ln -sfn "+shellQuote(prev)+" "+shellQuote(remote+"/current.new")+" && mv -Tf "+shellQuote(remote+"/current.new")+" "+shellQuote(remote+"/current"), time.Minute)
		}
		return "rolled-back"
	}
	if sv.HealthURL != "" && !d.healthCheckRemote(id, host, sv.HealthURL, 120*time.Second) {
		if prev := strings.TrimSpace(previous); prev != "" {
			_, _ = SSHExec(host, "ln -sfn "+shellQuote(prev)+" "+shellQuote(remote+"/current.new")+" && mv -Tf "+shellQuote(remote+"/current.new")+" "+shellQuote(remote+"/current"), time.Minute)
		}
		return "rolled-back"
	}
	d.emit(id, "\n✓ 静态资源已原子切换 → "+releaseDir+"\n")
	return "success"
}

func (d *Deployer) deployDockerHost(id int64, sv *Service, host Host, artifact Artifact) string {
	cfg := sv.DeployConfig
	name := sv.Name
	if cfg != nil && strings.TrimSpace(cfg["container_name"]) != "" {
		name = strings.TrimSpace(cfg["container_name"])
	}
	step := func(label, command string, timeout time.Duration) (string, bool) {
		d.emit(id, "\n$ "+command+"\n")
		out, err := SSHExec(host, command, timeout)
		if out != "" {
			d.emit(id, out)
		}
		if err != nil {
			d.emit(id, fmt.Sprintf("✗ %s 失败: %v\n", label, err))
			return out, false
		}
		return strings.TrimSpace(out), true
	}
	oldImage, _ := SSHExec(host, "docker inspect --format '{{.Config.Image}}' "+shellQuote(name)+" 2>/dev/null || true", time.Minute)
	if _, ok := step("拉取镜像", "docker pull "+shellQuote(artifact.URI), 10*time.Minute); !ok {
		return "failed"
	}
	_, _ = step("停止旧容器", "docker rm -f "+shellQuote(name)+" 2>/dev/null || true", 2*time.Minute)
	runArgs := []string{"docker run -d --restart unless-stopped --name", shellQuote(name)}
	if cfg != nil {
		if envFile := strings.TrimSpace(cfg["env_file"]); envFile != "" {
			runArgs = append(runArgs, "--env-file", shellQuote(envFile))
		}
		for _, port := range strings.Split(cfg["ports"], ",") {
			port = strings.TrimSpace(port)
			if port != "" {
				runArgs = append(runArgs, "-p", shellQuote(port))
			}
		}
		if extra := strings.TrimSpace(cfg["run_args"]); extra != "" {
			runArgs = append(runArgs, extra)
		}
	}
	if sv.RuntimeEnvFile != "" {
		runArgs = append(runArgs, "--env-file", shellQuote(sv.RuntimeEnvFile))
	}
	runArgs = append(runArgs, shellQuote(artifact.URI))
	if _, ok := step("启动新容器", strings.Join(runArgs, " "), 5*time.Minute); !ok {
		return d.rollbackDocker(id, host, name, strings.TrimSpace(oldImage), cfg, sv.RuntimeEnvFile)
	}
	if sv.HealthURL != "" {
		if !d.healthCheckRemote(id, host, sv.HealthURL, 120*time.Second) {
			return d.rollbackDocker(id, host, name, strings.TrimSpace(oldImage), cfg, sv.RuntimeEnvFile)
		}
	} else if _, ok := step("容器状态检查", "test \"$(docker inspect --format '{{.State.Running}}' "+shellQuote(name)+")\" = true", time.Minute); !ok {
		return d.rollbackDocker(id, host, name, strings.TrimSpace(oldImage), cfg, sv.RuntimeEnvFile)
	}
	return "success"
}

func (d *Deployer) rollbackDocker(id int64, host Host, name, oldImage string, cfg map[string]string, runtimeEnvFile string) string {
	d.emit(id, "\n↩ 恢复上一个容器镜像\n")
	_, _ = SSHExec(host, "docker rm -f "+shellQuote(name)+" 2>/dev/null || true", 2*time.Minute)
	if oldImage == "" {
		return "failed"
	}
	args := []string{"docker run -d --restart unless-stopped --name", shellQuote(name)}
	if cfg != nil {
		if envFile := strings.TrimSpace(cfg["env_file"]); envFile != "" {
			args = append(args, "--env-file", shellQuote(envFile))
		}
		for _, port := range strings.Split(cfg["ports"], ",") {
			if port = strings.TrimSpace(port); port != "" {
				args = append(args, "-p", shellQuote(port))
			}
		}
		if extra := strings.TrimSpace(cfg["run_args"]); extra != "" {
			args = append(args, extra)
		}
	}
	if runtimeEnvFile != "" {
		args = append(args, "--env-file", shellQuote(runtimeEnvFile))
	}
	args = append(args, shellQuote(oldImage))
	if _, err := SSHExec(host, strings.Join(args, " "), 5*time.Minute); err != nil {
		d.emit(id, "✗ 恢复旧容器失败: "+err.Error()+"\n")
		return "failed"
	}
	return "rolled-back"
}

func (d *Deployer) healthCheckRemote(id int64, host Host, url string, max time.Duration) bool {
	url = strings.ReplaceAll(url, "{host}", strings.Split(host.Addr, ":")[0])
	seconds := int(max.Seconds())
	command := fmt.Sprintf("i=0; while [ $i -lt %d ]; do curl -fsS --max-time 5 %s >/dev/null && exit 0; i=$((i+3)); sleep 3; done; exit 1", seconds, shellQuote(url))
	d.emit(id, "\n⏳ 目标主机健康检查 "+url+"\n")
	if out, err := SSHExec(host, command, max+10*time.Second); err != nil {
		if out != "" {
			d.emit(id, out)
		}
		d.emit(id, "✗ 健康检查超时\n")
		return false
	}
	d.emit(id, "✓ 健康检查通过\n")
	return true
}
