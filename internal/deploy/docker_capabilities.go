package deploy

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const dockerVersionProbe = "docker version --format '{{json .}}'"

type dockerVersionSide struct {
	Version       string
	APIVersion    string
	MinAPIVersion string
	OS            string
}
type dockerVersionReport struct {
	Client dockerVersionSide
	Server dockerVersionSide
}

func dockerAPI(v string) (int, error) {
	parts := strings.Split(v, ".")
	if len(parts) != 2 || parts[0] != "1" {
		return 0, fmt.Errorf("无法识别 Docker API 版本")
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil || n < 0 || n > 10000 {
		return 0, fmt.Errorf("无法识别 Docker API 版本")
	}
	return n, nil
}

func validateDockerVersion(raw string) (dockerVersionReport, error) {
	var v dockerVersionReport
	if len(raw) > 64<<10 || json.Unmarshal([]byte(raw), &v) != nil || v.Client.Version == "" || v.Server.Version == "" {
		return v, fmt.Errorf("无法读取 Docker Client/Server 版本；请确认安装、权限及守护进程连接")
	}
	client, err := dockerAPI(v.Client.APIVersion)
	if err != nil {
		return v, err
	}
	server, err := dockerAPI(v.Server.APIVersion)
	if err != nil {
		return v, err
	}
	if v.Server.OS != "linux" {
		return v, fmt.Errorf("Docker 主机发布仅支持 Linux 容器服务端")
	}
	// API 1.25 introduced Mounts in container creation. CLI flags are checked
	// separately: a new daemon does not make an old client understand --mount.
	if client < 25 || server < 25 {
		return v, fmt.Errorf("Docker API 低于平台所需 1.25，请升级客户端及服务端")
	}
	if v.Server.MinAPIVersion != "" {
		min, e := dockerAPI(v.Server.MinAPIVersion)
		if e != nil {
			return v, e
		}
		if client < min {
			return v, fmt.Errorf("Docker 客户端 API 低于服务端最低 API，请升级客户端")
		}
	}
	return v, nil
}

func dockerHasFlag(help, flag string) bool {
	return regexp.MustCompile(`(^|[\s,])` + regexp.QuoteMeta(flag) + `([\s=,]|$)`).MatchString(help)
}

// All probes are read-only and bounded. No run/create/pull/stop is attempted.
func dockerCapabilityPreflight(sv *Service, archive bool, io dockerHostIO) bool {
	fail := func(reason string) bool {
		io.emit("✗ Docker 发布能力检查失败：" + reason + "；旧容器未停止，请检查 Client/Server 或升级 Docker\n")
		return false
	}
	raw, err := io.exec(dockerVersionProbe, 15*time.Second)
	if err != nil {
		return fail("无法连接 Docker 守护进程或无执行权限")
	}
	v, err := validateDockerVersion(raw)
	if err != nil {
		return fail(err.Error())
	}
	// Only print bounded version tokens, never the full version response/context.
	versionToken := regexp.MustCompile(`^[a-zA-Z0-9_.+~-]{1,80}$`)
	if !versionToken.MatchString(v.Client.Version) || !versionToken.MatchString(v.Server.Version) {
		return fail("版本响应无效")
	}
	io.emit(fmt.Sprintf("✓ Docker Client %s / Server %s / API %s（能力检查，不代表该版本已全量认证）\n", v.Client.Version, v.Server.Version, v.Server.APIVersion))
	options, err := parseDockerRuntime(sv.DeployConfig)
	if err != nil {
		return fail(err.Error())
	}
	flags := []string{"--detach", "--restart", "--name", "--env-file"}
	if len(options.mounts) > 0 {
		flags = append(flags, "--mount")
	}
	if options.network != "" {
		flags = append(flags, "--network")
	}
	if strings.TrimSpace(sv.DeployConfig["ports"]) != "" {
		flags = append(flags, "--publish")
	}
	for _, arg := range options.args {
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
		}
	}
	probes := []struct {
		command string
		flags   []string
	}{{"docker run --help", flags}, {"docker stop --help", []string{"--time"}}, {"docker image inspect --help", []string{"--format"}}}
	if archive {
		probes = append(probes, struct {
			command string
			flags   []string
		}{"docker image load --help", []string{"--input"}})
	}
	for _, probe := range probes {
		help, e := io.exec(probe.command, 15*time.Second)
		if e != nil {
			return fail(probe.command + " 不可用")
		}
		if len(help) > 128<<10 {
			return fail("命令帮助响应过大")
		}
		for _, flag := range probe.flags {
			if !dockerHasFlag(help, flag) {
				return fail(probe.command + " 缺少 " + flag)
			}
		}
	}
	io.emit("✓ Docker 发布所需命令参数检查通过\n")
	return true
}
