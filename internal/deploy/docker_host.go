package deploy

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Injectable transport keeps deployment tests away from real target machines.
type dockerHostIO struct {
	exec   func(string, time.Duration) (string, error)
	upload func(string, string) error
	emit   func(string)
	health func() bool
	pause  func(time.Duration)
	now    func() time.Time
}

func (io dockerHostIO) step(label, command string, timeout time.Duration) (string, bool) {
	io.emit("\n▶ " + label + "\n$ " + command + "\n")
	out, err := io.exec(command, timeout)
	if out != "" {
		io.emit(out)
	}
	if err != nil {
		io.emit(fmt.Sprintf("✗ %s 失败: %v\n", label, err))
		return out, false
	}
	return strings.TrimSpace(out), true
}

func prepareDockerArchive(a Artifact, io dockerHostIO) (string, bool) {
	if err := validateDockerArchive(a); err != nil {
		io.emit("✗ " + err.Error() + "\n")
		return "", false
	}
	platform, ok := io.step("检查目标 Docker 平台", "docker version --format '{{.Server.Os}}/{{.Server.Arch}}'", time.Minute)
	if !ok {
		return "", false
	}
	// Variant-specific images need a dedicated target verification, not a guess.
	if platform != a.Metadata["os"]+"/"+a.Metadata["architecture"] || a.Metadata["variant"] != "" {
		io.emit("✗ 镜像架构与目标 Docker 不匹配，或包含尚不支持的 variant；旧容器未停止\n")
		return "", false
	}
	dir, ok := io.step("准备镜像上传目录", "mktemp -d /tmp/devops-image-XXXXXXXXXX", time.Minute)
	if !ok || !regexp.MustCompile(`^/tmp/devops-image-[a-zA-Z0-9]{10}$`).MatchString(dir) {
		return "", false
	}
	remote := dir + "/image.tar"
	defer func() {
		if _, err := io.exec("rm -f -- "+shellQuote(remote)+" && rmdir -- "+shellQuote(dir), time.Minute); err != nil {
			io.emit("⚠ 镜像临时文件清理失败：" + dir + "\n")
		}
	}()
	io.emit("\n▶ 上传镜像文件\n")
	if err := io.upload(a.URI, remote); err != nil {
		io.emit("✗ 上传镜像失败: " + err.Error() + "\n")
		return "", false
	}
	check := "test \"$(sha256sum " + shellQuote(remote) + " | cut -d ' ' -f 1)\" = " + shellQuote(strings.TrimPrefix(a.Digest, "sha256:"))
	if _, ok := io.step("校验镜像文件 SHA-256", check, 15*time.Minute); !ok {
		return "", false
	}
	if _, ok := io.step("导入镜像文件", "docker image load --input "+shellQuote(remote), 15*time.Minute); !ok {
		return "", false
	}
	imageID := a.Metadata["image_id"]
	identity, ok := io.step("核对导入镜像身份和架构", "docker image inspect --format '{{.Id}} {{.Os}}/{{.Architecture}}' "+shellQuote(imageID), time.Minute)
	if !ok || identity != imageID+" "+platform {
		io.emit("✗ 导入镜像身份不匹配；旧容器未停止\n")
		return "", false
	}
	return imageID, true
}

func dockerRunCommand(name, image string, sv *Service) string {
	// Archive images were already loaded and inspected by immutable image ID;
	// registry images were explicitly pulled before replacing the old container.
	// Older Docker clients do not support the run --pull option.
	args := []string{"docker run -d --restart unless-stopped --name", shellQuote(name)}
	args = append(args, "--label", shellQuote("io.devopsplatform.service="+sv.Name), "--label", shellQuote("io.devopsplatform.environment="+sv.RuntimeEnvironment))
	if cfg := sv.DeployConfig; cfg != nil {
		if file := strings.TrimSpace(cfg["env_file"]); file != "" {
			args = append(args, "--env-file", shellQuote(file))
		}
		for _, port := range strings.Split(cfg["ports"], ",") {
			if port = strings.TrimSpace(port); port != "" {
				args = append(args, "-p", shellQuote(port))
			}
		}
		if options, err := parseDockerRuntime(cfg); err == nil {
			if options.network != "" {
				args = append(args, "--network", shellQuote(options.network))
			}
			for _, m := range options.mounts {
				value := "type=bind,src=" + m.Source + ",dst=" + m.Target
				if m.ReadOnly {
					value += ",readonly"
				}
				args = append(args, "--mount", shellQuote(value))
			}
			for _, value := range options.args {
				args = append(args, shellQuote(value))
			}
		}
	}
	if sv.RuntimeEnvFile != "" {
		args = append(args, "--env-file", shellQuote(sv.RuntimeEnvFile))
	}
	return strings.Join(append(args, shellQuote(image)), " ")
}

func deployDockerContainer(id int64, sv *Service, a Artifact, io dockerHostIO) string {
	name := sv.Name
	if configured := strings.TrimSpace(sv.DeployConfig["container_name"]); configured != "" {
		name = configured
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`).MatchString(name) {
		io.emit("✗ 容器名无效\n")
		return "failed"
	}
	if !dockerPreflight(name, sv, io) {
		return "failed"
	}
	if !dockerCapabilityPreflight(sv, a.Kind == ArtifactDockerArchive, io) {
		return "failed"
	}
	image := a.URI
	if a.Kind == ArtifactDockerArchive {
		var ok bool
		image, ok = prepareDockerArchive(a, io)
		if !ok {
			return "failed"
		}
	} else {
		if _, ok := io.step("拉取镜像", "docker pull "+shellQuote(image), 10*time.Minute); !ok {
			return "failed"
		}
	}
	// Keep the actual old container (env, mounts, network, image), not just its tag.
	// Inspect errors must not be confused with 'no previous container'.
	previous, ok := io.step("检查旧容器", "docker container ls -a --filter "+shellQuote("name=^/"+regexp.QuoteMeta(name)+"$")+" --format '{{.ID}}'", time.Minute)
	if !ok {
		return "failed"
	}
	backup := fmt.Sprintf("%s.previous-%d", name, id)
	wasRunning := false
	if previous != "" {
		running, ok := io.step("记录旧容器运行状态", "docker inspect --format '{{.State.Running}}' "+shellQuote(name), time.Minute)
		if !ok || (running != "true" && running != "false") {
			return "failed"
		}
		wasRunning = running == "true"
		if _, ok := io.step("保留旧容器", "docker rename "+shellQuote(name)+" "+shellQuote(backup), time.Minute); !ok {
			return "failed"
		}
		if _, ok := io.step("优雅停止旧容器", "docker stop --time 30 "+shellQuote(backup), 2*time.Minute); !ok {
			_, _ = io.step("恢复旧容器名称", "docker rename "+shellQuote(backup)+" "+shellQuote(name), time.Minute)
			return "failed"
		}
	}
	rollback := func() string {
		io.emit("\n↩ 恢复旧容器及原运行配置\n")
		if _, ok := io.step("移除失败的新容器", "docker rm -f "+shellQuote(name)+" 2>/dev/null || test -z \"$(docker container ls -a --filter "+shellQuote("name=^/"+regexp.QuoteMeta(name)+"$")+" --format '{{.ID}}')\"", time.Minute); !ok {
			return "failed"
		}
		if previous == "" {
			return "failed"
		}
		if _, ok := io.step("恢复旧容器名称", "docker rename "+shellQuote(backup)+" "+shellQuote(name), time.Minute); !ok {
			return "failed"
		}
		if wasRunning {
			if _, ok := io.step("启动旧容器", "docker start "+shellQuote(name), 2*time.Minute); !ok {
				return "failed"
			}
			if _, ok := io.step("验证旧容器", "test \"$(docker inspect --format '{{.State.Running}}' "+shellQuote(name)+")\" = true", time.Minute); !ok {
				return "failed"
			}
		}
		return "rolled-back"
	}
	if _, ok := io.step("启动新容器", dockerRunCommand(name, image, sv), 5*time.Minute); !ok {
		return rollback()
	}
	if !dockerStartupCheck(name, sv, io) {
		return rollback()
	}
	if previous != "" {
		if _, ok := io.step("清理已替换容器", "docker rm "+shellQuote(backup), time.Minute); !ok {
			io.emit("⚠ 新版本正常，旧容器保留待清理\n")
		}
	}
	return "success"
}
