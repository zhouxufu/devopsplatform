package deploy

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Mounts belong to an application/environment's frozen destination, not to
// its build configuration. Never create a missing host source implicitly.
type dockerMount struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only"`
}

type dockerRuntimeOptions struct {
	network string
	mounts  []dockerMount
	args    []string
}

var deploymentSecretRE = regexp.MustCompile(`(?i)((?:[a-z0-9_]*(?:password|passwd|secret|token|api[_-]?key)[a-z0-9_]*)\s*[=:]\s*)([^\s,;]+)`)

func redactDeploymentText(text string) string {
	return deploymentSecretRE.ReplaceAllString(text, "$1[REDACTED]")
}

// A small shell-word reader is for legacy run_args ONLY. Parsed words are
// re-quoted before execution; no expansion, command substitution or operators.
func dockerOptionWords(raw string) ([]string, error) {
	words := []string{}
	var b strings.Builder
	quote := rune(0)
	escape := false
	started := false
	for _, r := range raw {
		if strings.ContainsRune("\x00\r\n`$;|&<>", r) {
			return nil, fmt.Errorf("额外 run 参数不能包含 Shell 表达式；变量请放在应用变量中")
		}
		if escape {
			b.WriteRune(r)
			escape = false
			started = true
			continue
		}
		if r == '\\' && quote != '\'' {
			escape = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
			started = true
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			started = true
			continue
		}
		if r == ' ' || r == '\t' {
			if started {
				words = append(words, b.String())
				b.Reset()
				started = false
			}
			continue
		}
		b.WriteRune(r)
		started = true
	}
	if quote != 0 || escape {
		return nil, fmt.Errorf("额外 run 参数引号未闭合")
	}
	if started {
		words = append(words, b.String())
	}
	return words, nil
}

func parseDockerRuntime(cfg map[string]string) (dockerRuntimeOptions, error) {
	o := dockerRuntimeOptions{network: strings.TrimSpace(cfg["network"])}
	if raw := strings.TrimSpace(cfg["mounts"]); raw != "" {
		if err := json.Unmarshal([]byte(raw), &o.mounts); err != nil {
			return o, fmt.Errorf("目录挂载必须是 JSON 数组")
		}
	}
	words, err := dockerOptionWords(cfg["run_args"])
	if err != nil {
		return o, err
	}
	for i := 0; i < len(words); i++ {
		key, value, inline := strings.Cut(words[i], "=")
		switch key {
		case "--init", "--read-only":
			if inline {
				return o, fmt.Errorf("%s 不接受赋值", key)
			}
			o.args = append(o.args, key)
			continue
		case "--network", "--net", "--mount", "-v", "--volume", "--hostname", "--memory", "-m", "--cpus", "--user", "-u":
		default:
			return o, fmt.Errorf("额外 run 参数 %s 不受支持；环境变量（含密钥）、端口、挂载请使用对应配置", key)
		}
		if !inline {
			i++
			if i >= len(words) {
				return o, fmt.Errorf("%s 缺少参数", key)
			}
			value = words[i]
		}
		if value == "" || strings.HasPrefix(value, "-") {
			return o, fmt.Errorf("%s 参数无效", key)
		}
		switch key {
		case "--network", "--net":
			if o.network != "" {
				return o, fmt.Errorf("网络模式配置重复")
			}
			o.network = value
		case "--mount":
			m := dockerMount{}
			kind := ""
			for _, part := range strings.Split(value, ",") {
				k, v, _ := strings.Cut(part, "=")
				switch k {
				case "type":
					kind = v
				case "src", "source":
					m.Source = v
				case "dst", "target", "destination":
					m.Target = v
				case "ro", "readonly":
					if v != "" && v != "true" {
						return o, fmt.Errorf("只读挂载参数无效")
					}
					m.ReadOnly = true
				default:
					return o, fmt.Errorf("挂载选项 %s 不受支持", k)
				}
			}
			if kind != "bind" {
				return o, fmt.Errorf("额外挂载目前仅支持 bind 目录/文件挂载")
			}
			o.mounts = append(o.mounts, m)
		case "-v", "--volume":
			parts := strings.Split(value, ":")
			if len(parts) < 2 || len(parts) > 3 {
				return o, fmt.Errorf("挂载格式应为主机绝对路径:容器绝对路径[:ro|rw]")
			}
			m := dockerMount{Source: parts[0], Target: parts[1]}
			if len(parts) == 3 {
				if parts[2] != "ro" && parts[2] != "rw" {
					return o, fmt.Errorf("挂载模式仅支持 ro/rw")
				}
				m.ReadOnly = parts[2] == "ro"
			}
			o.mounts = append(o.mounts, m)
		default:
			o.args = append(o.args, key, value)
		}
	}
	if o.network != "" && !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`).MatchString(o.network) {
		return o, fmt.Errorf("网络模式仅支持 bridge、host、none 或已有 Docker 网络名")
	}
	if o.network == "host" && strings.TrimSpace(cfg["ports"]) != "" {
		return o, fmt.Errorf("host 网络不能同时设置端口映射；请核对宿主机端口占用")
	}
	if o.network == "host" && strings.TrimSpace(cfg["host_ports"]) == "" {
		return o, fmt.Errorf("host 网络须填写应用监听端口，以便检查与宿主机进程的冲突")
	}
	for _, p := range strings.Split(cfg["host_ports"], ",") {
		if p = strings.TrimSpace(p); p != "" {
			n, e := strconv.Atoi(p)
			if e != nil || n < 1 || n > 65535 {
				return o, fmt.Errorf("host 监听端口应为逗号分隔的 1 到 65535")
			}
		}
	}
	targets := map[string]bool{}
	for _, m := range o.mounts {
		for _, p := range []string{m.Source, m.Target} {
			if !path.IsAbs(p) || path.Clean(p) != p || p == "/" || strings.ContainsAny(p, ",\x00\r\n:") {
				return o, fmt.Errorf("挂载源和目标须为非根目录的规范绝对路径")
			}
		}
		if targets[m.Target] {
			return o, fmt.Errorf("容器挂载目标重复: %s", m.Target)
		}
		// Runtime templates write application logs below /app/logs. Do not
		// silently broaden a saved/reviewed mount's permissions: require an edit.
		if m.ReadOnly && (m.Target == "/app/logs" || strings.HasPrefix(m.Target, "/app/logs/")) {
			return o, fmt.Errorf("应用日志挂载 %s 必须可写，请在环境的 Docker 挂载配置中将 read_only 改为 false（额外参数移除 readonly 或改为 :rw）；模型目录仍可只读", m.Target)
		}
		targets[m.Target] = true
	}
	seen := map[string]bool{}
	for _, port := range strings.Split(cfg["ports"], ",") {
		port = strings.TrimSpace(port)
		if port == "" {
			continue
		}
		parts := strings.Split(strings.TrimSuffix(strings.TrimSuffix(port, "/tcp"), "/udp"), ":")
		if len(parts) != 2 {
			return o, fmt.Errorf("端口映射格式应为 8889:8889[/tcp|/udp]")
		}
		for _, p := range parts {
			n, e := strconv.Atoi(p)
			if e != nil || n < 1 || n > 65535 {
				return o, fmt.Errorf("端口必须为 1 到 65535")
			}
		}
		protocol := "tcp"
		if strings.HasSuffix(port, "/udp") {
			protocol = "udp"
		} else if strings.Contains(port, "/") && !strings.HasSuffix(port, "/tcp") {
			return o, fmt.Errorf("端口协议仅支持 tcp/udp")
		}
		key := parts[0] + "/" + protocol
		if seen[key] {
			return o, fmt.Errorf("宿主机端口映射重复")
		}
		seen[key] = true
	}
	for _, key := range []string{"startup_timeout_seconds", "startup_observe_seconds"} {
		if v := cfg[key]; v != "" {
			n, e := strconv.Atoi(v)
			if e != nil || n < 1 || n > 600 {
				return o, fmt.Errorf("%s 必须为 1 到 600 秒", key)
			}
		}
	}
	if policyInt(cfg, "startup_observe_seconds", 15) > policyInt(cfg, "startup_timeout_seconds", 120) {
		return o, fmt.Errorf("启动稳定观察时长不能超过启动检查超时")
	}
	if strings.TrimSpace(cfg["env_file"]) != "" && (!path.IsAbs(cfg["env_file"]) || strings.ContainsAny(cfg["env_file"], "\x00\r\n")) {
		return o, fmt.Errorf("env-file 必须是目标主机上的绝对路径")
	}
	return o, nil
}

func dockerPreflight(name string, sv *Service, io dockerHostIO) bool {
	o, err := parseDockerRuntime(sv.DeployConfig)
	if err != nil {
		io.emit("✗ " + err.Error() + "\n")
		return false
	}
	for _, m := range o.mounts {
		if _, ok := io.step("检查挂载源存在且可读取", "test -e "+shellQuote(m.Source)+" && test -r "+shellQuote(m.Source), 15*time.Second); !ok {
			io.emit("✗ 挂载源不存在或不可读取：" + m.Source + "；旧容器未停止\n")
			return false
		}
	}
	if f := sv.DeployConfig["env_file"]; f != "" {
		if _, ok := io.step("检查运行变量文件", "test -f "+shellQuote(f)+" && test -r "+shellQuote(f), 15*time.Second); !ok {
			return false
		}
	}
	if o.network != "" && o.network != "host" && o.network != "bridge" && o.network != "none" {
		if _, ok := io.step("检查 Docker 网络", "docker network inspect --format '{{.Name}}' "+shellQuote(o.network), 15*time.Second); !ok {
			return false
		}
	}
	// Check host listeners AND Docker's published ports (userland-proxy may be
	// disabled). Exclude only this application's old container, never other apps.
	ports := sv.DeployConfig["ports"]
	if o.network == "host" {
		parts := []string{}
		for _, p := range strings.Split(sv.DeployConfig["host_ports"], ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				parts = append(parts, p+":"+p)
			}
		}
		ports = strings.Join(parts, ",")
	}
	for _, port := range strings.Split(ports, ",") {
		port = strings.TrimSpace(port)
		if port == "" {
			continue
		}
		hostPort := strings.Split(port, ":")[0]
		proto := "tcp"
		flag := "-ltn"
		if strings.HasSuffix(port, "/udp") {
			proto = "udp"
			flag = "-lun"
		}
		command := `command -v ss >/dev/null || { echo '需要 ss 检查宿主机端口'; exit 1; }; ` +
			`owned=$(docker container ls -a --filter ` + shellQuote("name=^/"+regexp.QuoteMeta(name)+"$") + ` --format '{{.ID}}') || exit 1; ` +
			`oldports=''; if [ -n "$owned" ]; then oldports=$(docker port "$owned" ` + shellQuote(strings.Split(strings.TrimSuffix(strings.TrimSuffix(port, "/tcp"), "/udp"), ":")[1]+"/"+proto) + ` 2>/dev/null || true); fi; ` +
			`listeners=$(ss -H ` + flag + `) || exit 1; ` +
			`if printf '%s\n' "$listeners" | awk '{n=split($4,a,":");print a[n]}' | grep -qx ` + shellQuote(hostPort) + `; then if ! printf '%s\n' "$oldports" | grep -Eq ` + shellQuote(":"+hostPort+"$") + `; then echo '宿主机端口已被其他进程占用'; exit 1; fi; fi; ` +
			`ids=$(docker container ls -q --filter ` + shellQuote("publish="+hostPort+"/"+proto) + `) || exit 1; for cid in $ids; do if [ "$cid" != "$owned" ]; then echo '端口已被其他容器占用'; exit 1; fi; done`
		if o.network == "host" {
			// Host networking has no docker port mapping. Only accept existing
			// listeners whose PIDs can be proved to belong to our old container.
			command = `command -v ss >/dev/null || exit 1; owned=$(docker container ls -a --filter ` + shellQuote("name=^/"+regexp.QuoteMeta(name)+"$") + ` --format '{{.ID}}') || exit 1; ` +
				`lines=$(ss -H ` + flag + `p) || exit 1; matches=$(printf '%s\n' "$lines" | awk -v p=` + shellQuote(hostPort) + ` '{n=split($4,a,":");if(a[n]==p)print}'); ` +
				`if [ -n "$matches" ]; then [ -n "$owned" ] || { echo '宿主机端口已被其他进程占用'; exit 1; }; oldnet=$(docker inspect --format '{{.HostConfig.NetworkMode}}' "$owned") || exit 1; [ "$oldnet" = host ] || exit 1; ` +
				`pids=$(docker top "$owned" -eo pid) || exit 1; printf '%s\n' "$matches" | while IFS= read -r line; do ids=$(printf '%s' "$line" | grep -oE 'pid=[0-9]+' | cut -d= -f2); [ -n "$ids" ] || { echo '无法确认端口进程归属'; exit 1; }; for pid in $ids; do printf '%s\n' "$pids" | awk '{print $1}' | grep -qx "$pid" || { echo '宿主机端口已被其他进程占用'; exit 1; }; done; done || exit 1; fi`
		}
		if _, ok := io.step("检查宿主机端口 "+hostPort+"/"+proto, command, 15*time.Second); !ok {
			io.emit("✗ 端口冲突或无法验证；旧容器及其他进程未停止\n")
			return false
		}
	}
	return true
}

func validateDockerHealthURL(sv *Service) error {
	if sv.HealthURL == "" {
		return nil
	}
	u, err := url.Parse(strings.ReplaceAll(sv.HealthURL, "{host}", "127.0.0.1"))
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("Docker 健康检查须为不含凭据的 HTTP/HTTPS URL")
	}
	p := u.Port()
	if p == "" {
		p = "80"
		if u.Scheme == "https" {
			p = "443"
		}
	}
	o, err := parseDockerRuntime(sv.DeployConfig)
	if err != nil {
		return err
	}
	if o.network == "host" {
		for _, candidate := range strings.Split(sv.DeployConfig["host_ports"], ",") {
			if strings.TrimSpace(candidate) == p {
				return nil
			}
		}
	} else {
		for _, mapping := range strings.Split(sv.DeployConfig["ports"], ",") {
			mapping = strings.TrimSpace(mapping)
			if strings.HasSuffix(mapping, "/udp") {
				continue
			}
			if strings.Split(mapping, ":")[0] == p {
				return nil
			}
		}
	}
	return fmt.Errorf("健康检查端口 %s 不在该容器的发布端口中，拒绝使用可能属于旧进程的检查结果", p)
}

type dockerContainerState struct {
	Status     string `json:"Status"`
	Running    bool   `json:"Running"`
	Restarting bool   `json:"Restarting"`
	ExitCode   int    `json:"ExitCode"`
	OOMKilled  bool   `json:"OOMKilled"`
	Error      string `json:"Error"`
	Health     *struct {
		Status string `json:"Status"`
	} `json:"Health"`
}

func dockerStartupCheck(name string, sv *Service, io dockerHostIO) bool {
	max := time.Duration(policyInt(sv.DeployConfig, "startup_timeout_seconds", 120)) * time.Second
	observe := time.Duration(policyInt(sv.DeployConfig, "startup_observe_seconds", 15)) * time.Second
	now := io.now
	if now == nil {
		now = time.Now
	}
	deadline := now().Add(max)
	stable := now()
	pause := io.pause
	if pause == nil {
		pause = time.Sleep
	}
	diagnose := func(reason string) bool {
		io.emit("✗ " + reason + "；可在本实例发布日志中查看启动错误\n")
		// Read only bounded logs; never inspect Config.Env or print credentials.
		out, _ := io.exec("docker logs --tail 80 "+shellQuote(name)+" 2>&1 | tail -c 16000", 10*time.Second)
		io.emit("\n━━ 容器启动日志（已脱敏）━━\n" + out + "\n")
		return false
	}
	io.emit("⏳ 验证新容器启动状态及重启次数\n")
	for now().Before(deadline) {
		out, err := io.exec("docker inspect --format '{{json .State}}|{{.RestartCount}}' "+shellQuote(name), 10*time.Second)
		if err != nil {
			return diagnose("无法读取新容器状态")
		}
		parts := strings.SplitN(strings.TrimSpace(out), "|", 2)
		var state dockerContainerState
		if len(parts) != 2 || json.Unmarshal([]byte(parts[0]), &state) != nil {
			return diagnose("Docker 状态响应无效")
		}
		restarts, e := strconv.Atoi(parts[1])
		if e != nil {
			return diagnose("Docker 重启次数响应无效")
		}
		if state.Restarting || restarts > 0 || state.Status == "exited" || state.Status == "dead" || state.OOMKilled {
			return diagnose(fmt.Sprintf("容器启动失败：%s，退出码 %d，重启 %d 次，OOM=%t", state.Status, state.ExitCode, restarts, state.OOMKilled))
		}
		if !state.Running {
			stable = now()
		} else {
			if state.Health != nil && state.Health.Status == "unhealthy" {
				return diagnose("容器健康检查失败")
			}
			ready := state.Health == nil || state.Health.Status == "healthy"
			if ready && now().Sub(stable) >= observe {
				if sv.HealthURL == "" {
					io.emit("✓ 容器启动观察通过（未配置 HTTP 检查，不代表业务就绪）\n")
					return true
				}
				if io.health != nil && io.health() {
					// HTTP may be served by an old process: re-check this container.
					verify, e := io.exec("docker inspect --format '{{.State.Running}} {{.RestartCount}}' "+shellQuote(name), 10*time.Second)
					if e == nil && strings.TrimSpace(verify) == "true 0" {
						io.emit("✓ 新容器及健康检查通过\n")
						return true
					}
					return diagnose("健康检查后新容器已退出或重启")
				}
			}
		}
		pause(time.Second)
	}
	return diagnose("容器启动或健康检查超时")
}
