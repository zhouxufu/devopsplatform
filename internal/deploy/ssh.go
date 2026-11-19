package deploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Host 一台部署目标主机
type Host struct {
	Name                 string `json:"name"`
	Environment          string `json:"environment"`
	Addr                 string `json:"addr"` // host:port
	User                 string `json:"user"`
	KeyPath              string `json:"key_path"` // 私钥路径（默认 ~/.ssh/id_rsa）
	Password             string `json:"password,omitempty"`
	HostKey              string `json:"host_key,omitempty"` // SHA256:... 指纹；空时使用 ~/.ssh/known_hosts
	CredentialConfigured bool   `json:"credential_configured,omitempty"`
}

// SSHHostKeyScan is the identity presented by an SSH server during key
// exchange. Scanning does not authenticate to the server and does not trust or
// persist the key; callers must explicitly confirm the fingerprint first.
type SSHHostKeyScan struct {
	Addr        string `json:"addr"`
	Algorithm   string `json:"algorithm"`
	Fingerprint string `json:"fingerprint"`
	KnownHosts  string `json:"known_hosts,omitempty"`
}

var errSSHHostKeyCaptured = errors.New("ssh host key captured")

// ScanSSHHostKey reads the host key selected during SSH key exchange. It stops
// the handshake before authentication so no username, password or private key
// is sent to the target.
func ScanSSHHostKey(addr string, timeout time.Duration) (*SSHHostKeyScan, error) {
	hp, err := normalizeSSHAddress(addr)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	conn, err := net.DialTimeout("tcp", hp, timeout)
	if err != nil {
		return nil, fmt.Errorf("连接 SSH 服务 %s: %w", hp, err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return nil, fmt.Errorf("设置 SSH 扫描超时: %w", err)
	}

	var key ssh.PublicKey
	cfg := &ssh.ClientConfig{
		User:    "devopsplatform-host-key-scan",
		Timeout: timeout,
		HostKeyCallback: func(_ string, _ net.Addr, presented ssh.PublicKey) error {
			key = presented
			return errSSHHostKeyCaptured
		},
	}
	_, _, _, handshakeErr := ssh.NewClientConn(conn, hp, cfg)
	if key == nil {
		if handshakeErr == nil {
			handshakeErr = errors.New("SSH 服务未返回主机公钥")
		}
		return nil, fmt.Errorf("读取 SSH 主机指纹 %s: %w", hp, handshakeErr)
	}
	return &SSHHostKeyScan{
		Addr:        hp,
		Algorithm:   key.Type(),
		Fingerprint: ssh.FingerprintSHA256(key),
		KnownHosts:  knownhosts.Line([]string{knownhosts.Normalize(hp)}, key),
	}, nil
}

func normalizeSSHAddress(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", errors.New("SSH 地址不能为空")
	}
	if strings.ContainsAny(addr, " \t\r\n/\\") {
		return "", fmt.Errorf("SSH 地址格式错误: %q", addr)
	}
	if host, port, err := net.SplitHostPort(addr); err == nil {
		if host == "" {
			return "", errors.New("SSH 主机不能为空")
		}
		if err := validateSSHPort(port); err != nil {
			return "", err
		}
		return net.JoinHostPort(host, port), nil
	}
	if strings.HasPrefix(addr, "[") && strings.HasSuffix(addr, "]") {
		addr = strings.TrimSuffix(strings.TrimPrefix(addr, "["), "]")
	}
	if strings.Count(addr, ":") == 1 {
		return "", fmt.Errorf("SSH 地址端口格式错误: %q", addr)
	}
	return net.JoinHostPort(addr, "22"), nil
}

func validateSSHPort(port string) error {
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("SSH 端口无效: %q", port)
	}
	return nil
}

// SSHExec 建立会话并执行命令，返回输出（合并 stdout/stderr）
func SSHExec(h Host, cmd string, timeout time.Duration) (string, error) {
	return SSHExecContext(context.Background(), h, cmd, timeout)
}

func SSHExecContext(ctx context.Context, h Host, cmd string, timeout time.Duration) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	cli, err := sshClientContext(ctx, h, timeout)
	if err != nil {
		return "", err
	}
	defer cli.Close()
	stop := context.AfterFunc(ctx, func() { cli.Close() })
	defer stop()
	sess, err := cli.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	out, err := sess.CombinedOutput(cmd)
	return string(out), err
}

// SSHUpload 经 sftp 不可用时用 cat 兜底：通过 stdin 流写入远端文件（jar 上传足够）
func SSHUpload(h Host, localPath, remotePath string) error {
	return SSHUploadContext(context.Background(), h, localPath, remotePath)
}

func SSHUploadContext(ctx context.Context, h Host, localPath, remotePath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()
	cli, err := sshClientContext(ctx, h, 10*time.Minute)
	if err != nil {
		return err
	}
	defer cli.Close()
	stop := context.AfterFunc(ctx, func() { cli.Close() })
	defer stop()
	sess, err := cli.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	w, err := sess.StdinPipe()
	if err != nil {
		return err
	}
	if err := sess.Start("umask 077; cat > " + shellQuote(remotePath)); err != nil {
		return err
	}
	if _, err := io.Copy(w, f); err != nil {
		return err
	}
	w.Close()
	return sess.Wait()
}

// SSHUploadContent writes generated deployment data without first persisting
// secrets to a local temporary file.
func SSHUploadContent(h Host, content []byte, remotePath string) error {
	return SSHUploadContentContext(context.Background(), h, content, remotePath)
}

func SSHUploadContentContext(ctx context.Context, h Host, content []byte, remotePath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cli, err := sshClientContext(ctx, h, 10*time.Minute)
	if err != nil {
		return err
	}
	defer cli.Close()
	stop := context.AfterFunc(ctx, func() { cli.Close() })
	defer stop()
	sess, err := cli.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	w, err := sess.StdinPipe()
	if err != nil {
		return err
	}
	if err := sess.Start("umask 077; cat > " + shellQuote(remotePath)); err != nil {
		return err
	}
	if _, err := io.Copy(w, bytes.NewReader(content)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return sess.Wait()
}

func sshClient(h Host, timeout time.Duration) (*ssh.Client, error) {
	return sshClientContext(context.Background(), h, timeout)
}

func sshClientContext(ctx context.Context, h Host, timeout time.Duration) (*ssh.Client, error) {
	hp, err := normalizeSSHAddress(h.Addr)
	if err != nil {
		return nil, err
	}
	callback, err := secureHostKeyCallback(h)
	if err != nil {
		return nil, err
	}
	cfg := &ssh.ClientConfig{User: h.User, Timeout: 10 * time.Second, HostKeyCallback: callback}
	if h.KeyPath != "" {
		key, err := os.ReadFile(h.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("读私钥 %s: %w", h.KeyPath, err)
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, err
		}
		cfg.Auth = []ssh.AuthMethod{ssh.PublicKeys(signer)}
	} else if h.Password != "" {
		cfg.Auth = []ssh.AuthMethod{ssh.Password(h.Password)}
	} else { // 默认私钥
		home, _ := os.UserHomeDir()
		key, err := os.ReadFile(home + "/.ssh/id_rsa")
		if err != nil {
			return nil, fmt.Errorf("未配置凭据且默认私钥不可用: %w", err)
		}
		if signer, err := ssh.ParsePrivateKey(key); err == nil {
			cfg.Auth = []ssh.AuthMethod{ssh.PublicKeys(signer)}
		}
	}
	// ClientConfig.Timeout only bounds TCP dialing, not session commands or
	// even a stalled SSH handshake. Bound the underlying connection as well.
	if timeout <= 0 {
		timeout = time.Minute
	}
	conn, err := net.DialTimeout("tcp", hp, cfg.Timeout)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if err = conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		conn.Close()
		return nil, err
	}
	cc, chans, reqs, err := ssh.NewClientConn(conn, hp, cfg)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return ssh.NewClient(cc, chans, reqs), nil
}

func secureHostKeyCallback(h Host) (ssh.HostKeyCallback, error) {
	if fingerprint := strings.TrimSpace(h.HostKey); fingerprint != "" {
		return func(_ string, _ net.Addr, key ssh.PublicKey) error {
			got := ssh.FingerprintSHA256(key)
			if got != fingerprint {
				return fmt.Errorf("SSH 主机指纹不匹配：期望 %s，实际 %s", fingerprint, got)
			}
			return nil
		}, nil
	}
	home, _ := os.UserHomeDir()
	knownHosts := filepath.Join(home, ".ssh", "known_hosts")
	if _, err := os.Stat(knownHosts); err != nil {
		return nil, fmt.Errorf("未配置 SSH 主机指纹，且 %s 不可用", knownHosts)
	}
	callback, err := knownhosts.New(knownHosts)
	if err != nil {
		return nil, fmt.Errorf("读取 known_hosts: %w", err)
	}
	return callback, nil
}
