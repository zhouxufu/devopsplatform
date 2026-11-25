package deploy

import (
	"crypto/sha256"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	agentcfg "logplatform/internal/agentconfig"
)

const agentInstallPath = "/usr/local/bin/logagent"

type AgentCollectRequest struct {
	ApplicationID string   `json:"application_id"`
	Service       string   `json:"service"`
	Paths         []string `json:"paths"`
	Multiline     string   `json:"multiline"`
}

type AgentInstallResult struct {
	OK       bool   `json:"ok"`
	AgentID  string `json:"agent_id"`
	Platform string `json:"platform"`
	Version  string `json:"version"`
	SHA256   string `json:"sha256"`
	Status   string `json:"status"`
}

type AgentRemoteStatus struct {
	AgentID   string `json:"agent_id,omitempty"`
	Installed bool   `json:"installed"`
	Active    bool   `json:"active"`
	Status    string `json:"status"`
	Version   string `json:"version,omitempty"`
}

var accountPattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,30}\$?$`)

func normalizeAgentDesiredConfig(req *AgentDesiredConfig) error {
	req.ServerURL = strings.TrimRight(strings.TrimSpace(req.ServerURL), "/")
	req.MonitorURL = strings.TrimRight(strings.TrimSpace(req.MonitorURL), "/")
	if err := validateHTTPBaseURL("日志接收地址", req.ServerURL, true); err != nil {
		return err
	}
	if err := validateHTTPBaseURL("监控接收地址", req.MonitorURL, false); err != nil {
		return err
	}
	if req.RunUser == "" {
		req.RunUser = "logagent"
	}
	if req.RunGroup == "" {
		req.RunGroup = req.RunUser
	}
	if !accountPattern.MatchString(req.RunUser) || !accountPattern.MatchString(req.RunGroup) {
		return fmt.Errorf("运行用户和用户组格式无效")
	}
	if req.SpoolMaxMB <= 0 {
		req.SpoolMaxMB = 1024
	}
	if req.SpoolMaxMB < 64 || req.SpoolMaxMB > 10240 {
		return fmt.Errorf("本地缓冲须在 64MB 到 10240MB 之间")
	}
	if len(req.Collect) == 0 || len(req.Collect) > 64 {
		return fmt.Errorf("至少配置一个、最多 64 个日志采集项")
	}
	for i := range req.Collect {
		c := &req.Collect[i]
		c.Service = strings.TrimSpace(c.Service)
		c.Multiline = strings.TrimSpace(c.Multiline)
		if (c.ApplicationID == "" && c.Service == "") || strings.ContainsAny(c.Service+c.ApplicationID, "\r\n\x00") {
			return fmt.Errorf("采集项 %d 的应用标识无效", i+1)
		}
		if len(c.Paths) == 0 || len(c.Paths) > 32 {
			return fmt.Errorf("采集项 %d 至少配置一个、最多 32 个日志路径", i+1)
		}
		for j := range c.Paths {
			c.Paths[j] = strings.TrimSpace(c.Paths[j])
			if !strings.HasPrefix(c.Paths[j], "/") || strings.ContainsAny(c.Paths[j], "\r\n\x00") {
				return fmt.Errorf("采集项 %d 的路径必须是 Linux 绝对路径", i+1)
			}
		}
		if c.Multiline != "" {
			if _, err := regexp.Compile(c.Multiline); err != nil {
				return fmt.Errorf("采集项 %d 的多行首行正则无效: %w", i+1, err)
			}
		}
	}
	return nil
}

func validateHTTPBaseURL(label, raw string, required bool) error {
	if raw == "" && !required {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%s必须是 http(s)://host[:port]", label)
	}
	return nil
}

func parseAgentPlatform(out string) (string, error) {
	parts := strings.Fields(strings.TrimSpace(out))
	for len(parts) >= 4 && parts[0] != "LOGPLATFORM_PLATFORM" {
		parts = parts[1:]
	}
	if len(parts) >= 4 && parts[0] == "LOGPLATFORM_PLATFORM" {
		parts = parts[1:]
	}
	if len(parts) < 3 || !strings.EqualFold(parts[0], "linux") {
		return "", fmt.Errorf("仅支持使用 systemd 的 Linux 主机")
	}
	if parts[2] != "systemd" {
		return "", fmt.Errorf("目标主机未安装 systemd")
	}
	switch strings.ToLower(parts[1]) {
	case "x86_64", "amd64":
		return "linux-amd64", nil
	case "aarch64", "arm64":
		return "linux-arm64", nil
	default:
		return "", fmt.Errorf("暂不支持目标架构 %s", parts[1])
	}
}

func findAgentBinary(platform string) (string, error) {
	name := "logagent-" + platform
	roots := []string{}
	if explicit := strings.TrimSpace(os.Getenv("LOGPLATFORM_AGENT_DIST")); explicit != "" {
		roots = append(roots, explicit)
	}
	if executable, err := os.Executable(); err == nil {
		root := filepath.Dir(filepath.Dir(executable))
		roots = append(roots, filepath.Join(root, "dist", "agents"))
	}
	if cwd, err := os.Getwd(); err == nil {
		roots = append(roots, filepath.Join(cwd, "dist", "agents"))
	}
	for _, root := range roots {
		path := filepath.Join(root, name)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path, nil
		}
	}
	if runtime.GOOS == "linux" && platform == "linux-"+runtime.GOARCH {
		for _, root := range roots {
			path := filepath.Join(filepath.Dir(filepath.Dir(root)), "bin", "agent")
			if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
				return path, nil
			}
		}
	}
	return "", fmt.Errorf("缺少 %s 安装包，请先执行 scripts/build-agents.sh", platform)
}

func agentDataToken() string {
	return os.Getenv("DEVOPSPLATFORM_DATA_TOKEN")
}

func runtimeAgentConfig(h Host, desired AgentDesiredConfig, token string) (agentcfg.Config, error) {
	if strings.TrimSpace(token) == "" {
		return agentcfg.Config{}, fmt.Errorf("平台尚未配置 DEVOPSPLATFORM_DATA_TOKEN")
	}
	if strings.ContainsAny(token, "\r\n\x00") || len(token) > 4096 {
		return agentcfg.Config{}, fmt.Errorf("平台上报 Token 格式无效")
	}
	if err := normalizeAgentDesiredConfig(&desired); err != nil {
		return agentcfg.Config{}, err
	}
	collect := make([]agentcfg.CollectCfg, 0, len(desired.Collect))
	for _, c := range desired.Collect {
		collect = append(collect, agentcfg.CollectCfg{ApplicationID: c.ApplicationID, Service: c.Service, Environment: h.Environment, Paths: c.Paths, Multiline: c.Multiline})
	}
	return agentcfg.Config{
		ServerURL: desired.ServerURL, Token: token, AgentID: h.Environment + "/" + h.Name,
		Host: h.Name, Environment: h.Environment, Registry: "/var/lib/logagent/registry.json",
		Spool: "/var/lib/logagent/agent-spool.db", SpoolMaxMB: desired.SpoolMaxMB,
		MonitorURL: desired.MonitorURL, Collect: collect,
	}, nil
}

func InstallAgent(h Host, desired AgentDesiredConfig, token string) (*AgentInstallResult, error) {
	cfg, err := runtimeAgentConfig(h, desired, token)
	if err != nil {
		return nil, err
	}
	detected, err := SSHExec(h, `set -e; printf 'LOGPLATFORM_PLATFORM %s %s ' "$(uname -s)" "$(uname -m)"; if command -v systemctl >/dev/null 2>&1; then echo systemd; else echo no-systemd; fi`, 15*time.Second)
	if err != nil {
		return nil, fmt.Errorf("检测目标主机失败: %w: %s", err, strings.TrimSpace(detected))
	}
	platform, err := parseAgentPlatform(detected)
	if err != nil {
		return nil, err
	}
	binary, err := findAgentBinary(platform)
	if err != nil {
		return nil, err
	}
	rawBinary, err := os.ReadFile(binary)
	if err != nil {
		return nil, err
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(rawBinary))

	configBytes, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	unit := renderAgentUnit(desired.RunUser, desired.RunGroup)
	tmpDir, err := os.MkdirTemp("", "logplatform-agent-install-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)
	configFile, unitFile := filepath.Join(tmpDir, "agent.yaml"), filepath.Join(tmpDir, "logagent.service")
	if err := os.WriteFile(configFile, configBytes, 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(unitFile, []byte(unit), 0o600); err != nil {
		return nil, err
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	remoteBinary := "/tmp/logagent-" + suffix
	remoteConfig := "/tmp/logagent-config-" + suffix
	remoteUnit := "/tmp/logagent-unit-" + suffix
	for _, upload := range [][2]string{{binary, remoteBinary}, {configFile, remoteConfig}, {unitFile, remoteUnit}} {
		if err := SSHUpload(h, upload[0], upload[1]); err != nil {
			return nil, fmt.Errorf("上传 Agent 文件失败: %w", err)
		}
	}
	cmd := renderAgentInstallCommand(remoteBinary, remoteConfig, remoteUnit, desired.RunUser, desired.RunGroup)
	out, err := SSHExec(h, cmd, 2*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("安装或启动 Agent 失败: %w: %s", err, strings.TrimSpace(out))
	}
	version := strings.TrimSpace(out)
	if lines := strings.Split(version, "\n"); len(lines) > 0 {
		version = strings.TrimSpace(lines[len(lines)-1])
	}
	agentID := ""
	if status, err := AgentStatus(h); err == nil {
		agentID = status.AgentID
	}
	return &AgentInstallResult{OK: true, AgentID: agentID, Platform: platform, Version: version, SHA256: digest, Status: "active"}, nil
}

// ApplyAgentConfig updates desired runtime configuration without replacing the
// agent binary. A service restart is intentional so the new file set takes
// effect atomically; the on-disk spool and file registry are preserved.
func ApplyAgentConfig(h Host, desired AgentDesiredConfig, token string) error {
	cfg, err := runtimeAgentConfig(h, desired, token)
	if err != nil {
		return err
	}
	configBytes, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	unitBytes := []byte(renderAgentUnit(desired.RunUser, desired.RunGroup))
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	remoteConfig := "/tmp/logagent-config-" + suffix
	remoteUnit := "/tmp/logagent-unit-" + suffix
	if err := SSHUploadContent(h, configBytes, remoteConfig); err != nil {
		return fmt.Errorf("上传 Agent 配置失败: %w", err)
	}
	if err := SSHUploadContent(h, unitBytes, remoteUnit); err != nil {
		return fmt.Errorf("上传 Agent 服务配置失败: %w", err)
	}
	out, err := SSHExec(h, renderAgentApplyCommand(remoteConfig, remoteUnit, desired.RunUser, desired.RunGroup), time.Minute)
	if err != nil {
		return fmt.Errorf("下发 Agent 配置失败: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

func AgentStatus(h Host) (*AgentRemoteStatus, error) {
	out, err := SSHExec(h, `if [ ! -x /usr/local/bin/logagent ]; then echo not-installed; exit 0; fi; if command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet logagent; then echo active; else echo inactive; fi; /usr/local/bin/logagent -version 2>/dev/null || true; /usr/local/bin/logagent -config /etc/logagent/agent.yaml -identity 2>/dev/null || true`, 15*time.Second)
	if err != nil {
		return nil, fmt.Errorf("查询 Agent 状态失败: %w: %s", err, strings.TrimSpace(out))
	}
	lines := strings.Fields(strings.TrimSpace(out))
	if len(lines) == 0 || lines[0] == "not-installed" {
		return &AgentRemoteStatus{Status: "not-installed"}, nil
	}
	status := &AgentRemoteStatus{Installed: true, Active: lines[0] == "active", Status: lines[0]}
	if len(lines) > 1 {
		status.Version = lines[1]
	}
	if len(lines) > 2 && strings.HasPrefix(lines[2], "agent_") {
		status.AgentID = lines[2]
	}
	return status, nil
}

func renderAgentUnit(user, group string) string {
	return fmt.Sprintf(`[Unit]
Description=LogPlatform Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/logagent -config /etc/logagent/agent.yaml
Restart=always
RestartSec=5
Nice=10
CPUWeight=20
IOSchedulingClass=idle
MemoryMax=128M
LimitNOFILE=1024
StateDirectory=logagent
UMask=0077
User=%s
Group=%s

[Install]
WantedBy=multi-user.target
`, user, group)
}

func renderAgentInstallCommand(remoteBinary, remoteConfig, remoteUnit, user, group string) string {
	q := shellQuote
	createAccount := ""
	if user == "logagent" {
		createAccount = fmt.Sprintf(`if ! getent group %s >/dev/null 2>&1; then $SUDO groupadd --system %s; fi
if ! id %s >/dev/null 2>&1; then $SUDO useradd --system --gid %s --home-dir /var/lib/logagent --shell /usr/sbin/nologin %s; fi`, q(group), q(group), q(user), q(group), q(user))
	} else {
		createAccount = fmt.Sprintf(`id %s >/dev/null 2>&1 || { echo "运行用户不存在" >&2; exit 1; }
getent group %s >/dev/null 2>&1 || { echo "运行用户组不存在" >&2; exit 1; }`, q(user), q(group))
	}
	return fmt.Sprintf(`set -eu
SUDO=""
if [ "$(id -u)" -ne 0 ]; then command -v sudo >/dev/null 2>&1 || { echo "需要 root 或免密 sudo" >&2; exit 1; }; SUDO="sudo -n"; fi
cleanup() { rm -f %s %s %s; }
trap cleanup EXIT
%s
$SUDO install -d -m 0755 /etc/logagent /var/lib/logagent
if [ -f /usr/local/bin/logagent ]; then
 backup_dir=/var/lib/logagent/backups/install-$(date +%%Y%%m%%d%%H%%M%%S)-$$
 $SUDO install -d -m 0700 "$backup_dir"
 $SUDO cp -p /usr/local/bin/logagent "$backup_dir/logagent"
 if [ -f /etc/logagent/agent.yaml ]; then $SUDO cp -p /etc/logagent/agent.yaml "$backup_dir/agent.yaml"; fi
 if [ -f /etc/systemd/system/logagent.service ]; then $SUDO cp -p /etc/systemd/system/logagent.service "$backup_dir/logagent.service"; fi
fi
$SUDO install -m 0755 %s /usr/local/bin/.logagent.new
$SUDO install -m 0600 -o %s -g %s %s /etc/logagent/.agent.yaml.new
if [ -f /etc/logagent/agent.yaml ]; then
 /usr/local/bin/.logagent.new -h 2>&1 | grep -q -- '-merge-collect-config' || { echo "Agent 安装包不支持保留已有部署策略，请更新平台 Agent 安装包" >&2; exit 1; }
 $SUDO /usr/local/bin/.logagent.new -config /etc/logagent/agent.yaml -merge-collect-config /etc/logagent/.agent.yaml.new -deployment-run-user "$(stat -c %%U /etc/logagent/.agent.yaml.new)"
fi
$SUDO mv -f /usr/local/bin/.logagent.new /usr/local/bin/logagent
$SUDO mv -f /etc/logagent/.agent.yaml.new /etc/logagent/agent.yaml
$SUDO install -m 0644 %s /etc/systemd/system/logagent.service
$SUDO chown -R %s:%s /var/lib/logagent
$SUDO systemctl daemon-reload
$SUDO systemctl enable logagent
$SUDO systemctl restart logagent
sleep 1
if ! $SUDO systemctl is-active --quiet logagent; then
  $SUDO systemctl status logagent --no-pager || true
  $SUDO journalctl -u logagent -n 20 --no-pager || true
  exit 1
fi
/usr/local/bin/logagent -version
`, q(remoteBinary), q(remoteConfig), q(remoteUnit), createAccount, q(remoteBinary), q(user), q(group), q(remoteConfig), q(remoteUnit), q(user), q(group))
}

func renderAgentApplyCommand(remoteConfig, remoteUnit, user, group string) string {
	q := shellQuote
	return fmt.Sprintf(`set -eu
SUDO=""
if [ "$(id -u)" -ne 0 ]; then command -v sudo >/dev/null 2>&1 || { echo "需要 root 或免密 sudo" >&2; exit 1; }; SUDO="sudo -n"; fi
test -x /usr/local/bin/logagent || { echo "Agent 尚未安装" >&2; exit 1; }
id %s >/dev/null 2>&1 || { echo "运行用户不存在" >&2; exit 1; }
getent group %s >/dev/null 2>&1 || { echo "运行用户组不存在" >&2; exit 1; }
cleanup() { rm -f %s %s; }
trap cleanup EXIT
$SUDO install -m 0600 -o %s -g %s %s /etc/logagent/.agent.yaml.new
if [ -f /etc/logagent/agent.yaml ] && /usr/local/bin/logagent -h 2>&1 | grep -q -- '-merge-collect-config'; then
 $SUDO /usr/local/bin/logagent -config /etc/logagent/agent.yaml -merge-collect-config /etc/logagent/.agent.yaml.new -deployment-run-user "$(stat -c %%U /etc/logagent/.agent.yaml.new)"
fi
$SUDO mv -f /etc/logagent/.agent.yaml.new /etc/logagent/agent.yaml
$SUDO install -m 0644 %s /etc/systemd/system/logagent.service
$SUDO systemctl daemon-reload
$SUDO systemctl restart logagent
sleep 1
$SUDO systemctl is-active --quiet logagent || { $SUDO systemctl status logagent --no-pager; exit 1; }
`, q(user), q(group), q(remoteConfig), q(remoteUnit), q(user), q(group), q(remoteConfig), q(remoteUnit))
}
