package deploy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

const (
	gitAuthHTTPS = "https"
	gitAuthSSH   = "ssh"
)

func normalizeGitAuthMode(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", gitAuthHTTPS:
		return gitAuthHTTPS, nil
	case gitAuthSSH:
		return gitAuthSSH, nil
	default:
		return "", fmt.Errorf("Git 认证方式仅支持 HTTPS/PAT 或 SSH 私钥")
	}
}

func isGitSSHURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	return strings.HasPrefix(strings.ToLower(raw), "ssh://") ||
		(strings.Contains(raw, "@") && strings.Contains(raw[strings.LastIndex(raw, "@")+1:], ":"))
}

// normalizeGitSSHEndpoint accepts a service host, host:port or ssh:// URL and
// stores only host:port. A repository path belongs to the application, not the
// reusable credential.
func normalizeGitSSHEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("SSH 服务地址不能为空")
	}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || !strings.EqualFold(u.Scheme, "ssh") || u.Hostname() == "" {
			return "", fmt.Errorf("SSH 服务地址格式错误，请填写 codeup.aliyun.com:22 或 ssh://git@codeup.aliyun.com:22")
		}
		if strings.Trim(u.Path, "/") != "" {
			return "", fmt.Errorf("SSH 服务地址只填写服务器，不要包含仓库路径")
		}
		port := u.Port()
		if port == "" {
			port = "22"
		}
		return normalizeSSHAddress(net.JoinHostPort(u.Hostname(), port))
	}
	if at := strings.LastIndex(raw, "@"); at >= 0 {
		raw = raw[at+1:]
	}
	return normalizeSSHAddress(raw)
}

func parseGitPrivateKey(privateKey, passphrase string) (ssh.Signer, error) {
	privateKey = strings.TrimSpace(privateKey)
	if privateKey == "" {
		return nil, errors.New("SSH 私钥不能为空")
	}
	var (
		signer ssh.Signer
		err    error
	)
	if passphrase != "" {
		signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(privateKey), []byte(passphrase))
	} else {
		signer, err = ssh.ParsePrivateKey([]byte(privateKey))
	}
	if err == nil {
		return signer, nil
	}
	var missing *ssh.PassphraseMissingError
	if errors.As(err, &missing) {
		return nil, errors.New("SSH 私钥已加密，请填写私钥口令")
	}
	if passphrase != "" {
		return nil, fmt.Errorf("SSH 私钥或口令无效: %w", err)
	}
	return nil, fmt.Errorf("SSH 私钥格式无效: %w", err)
}

// testGitSSHConnection verifies the pinned host key and authenticates with the
// stored private key. It does not run a remote command or read a repository.
func testGitSSHConnection(ctx context.Context, c *ResourceConnection) error {
	if c == nil {
		return errors.New("Git SSH 连接不存在")
	}
	hp, err := normalizeGitSSHEndpoint(c.Endpoint)
	if err != nil {
		return err
	}
	signer, err := parseGitPrivateKey(c.PrivateKey, c.Passphrase)
	if err != nil {
		return err
	}
	knownHostsLine := strings.TrimSpace(c.Config["known_hosts"])
	if knownHostsLine == "" {
		return errors.New("未确认 Git SSH 主机指纹")
	}
	dir, err := os.MkdirTemp("", "devopsplatform-git-ssh-test-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	knownHostsFile := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(knownHostsFile, []byte(knownHostsLine+"\n"), 0o600); err != nil {
		return err
	}
	callback, err := knownhosts.New(knownHostsFile)
	if err != nil {
		return fmt.Errorf("Git SSH 主机指纹配置无效: %w", err)
	}
	user := strings.TrimSpace(c.Username)
	if user == "" {
		user = "git"
	}
	config := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: callback,
		Timeout:         10 * time.Second,
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", hp)
	if err != nil {
		return fmt.Errorf("连接 Git SSH 服务 %s: %w", hp, err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return fmt.Errorf("设置 Git SSH 认证超时: %w", err)
	}
	clientConn, channels, requests, err := ssh.NewClientConn(conn, hp, config)
	if err != nil {
		return fmt.Errorf("Git SSH 认证失败: %w", err)
	}
	client := ssh.NewClient(clientConn, channels, requests)
	return client.Close()
}
