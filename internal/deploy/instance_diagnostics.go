package deploy

import (
	"errors"
	"net"
	"strings"

	"golang.org/x/crypto/ssh"
)

// Publish only allowlisted explanations, never raw SSH/Docker stderr, which
// can contain paths, registry credentials or environment values.
func classifyInstanceReadError(output string, err error) (state, code, message string) {
	var exit *ssh.ExitError
	if errors.As(err, &exit) {
		text := strings.ToLower(output)
		switch {
		case strings.Contains(text, "template parsing error"), strings.Contains(text, "map has no entry"), strings.Contains(text, "can't evaluate field"):
			return "inspection-failed", "docker-template", "主机已连接，Docker 状态字段解析失败，请更新平台兼容修复"
		case strings.Contains(text, "permission denied"):
			return "inspection-failed", "docker-permission", "主机已连接，但当前账号没有 Docker 访问权限"
		case strings.Contains(text, "cannot connect to the docker daemon"), strings.Contains(text, "is the docker daemon running"), strings.Contains(text, "error during connect"):
			return "inspection-failed", "docker-daemon", "主机已连接，但 Docker 服务暂不可访问"
		case strings.Contains(text, "command not found"), strings.Contains(text, "docker: not found"):
			return "inspection-failed", "docker-missing", "主机已连接，但当前会话找不到 Docker 命令"
		case strings.Contains(text, "unknown flag"), strings.Contains(text, "is not a docker command"):
			return "inspection-failed", "docker-command", "主机已连接，但 Docker 不支持当前查询命令"
		default:
			return "inspection-failed", "docker-query", "主机已连接，但容器查询失败，请检查 Docker 服务与权限"
		}
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "unable to authenticate"), strings.Contains(text, "no supported methods"):
		return "unreachable", "ssh-auth", "SSH 认证失败，请检查资源中心的主机凭据"
	case strings.Contains(text, "指纹"), strings.Contains(text, "known_hosts"), strings.Contains(text, "knownhosts"), strings.Contains(text, "host key"):
		return "unreachable", "ssh-host-key", "SSH 主机身份校验未通过，请核对资源中心的主机指纹"
	}
	var network net.Error
	if errors.As(err, &network) && network.Timeout() {
		return "unreachable", "ssh-timeout", "主机连接或状态查询超时，请检查目标主机网络与负载"
	}
	return "unreachable", "ssh-connection", "无法建立或保持 SSH 会话，请检查主机连接配置"
}
