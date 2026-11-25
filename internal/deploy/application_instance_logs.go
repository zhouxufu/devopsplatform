package deploy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

var instanceLogCredential = regexp.MustCompile(`(?i)((?:["']?[a-z0-9_\-]*(?:password|passwd|secret|token|api[_-]?key|authorization)[a-z0-9_\-]*["']?)\s*[:=]\s*["']?)([^\s,"';}]+)`)
var instanceLogBearer = regexp.MustCompile(`(?i)(bearer\s+)[a-z0-9._~+/=-]+`)
var instanceLogANSI = regexp.MustCompile("\x1b\\[[0-?]*[ -/]*[@-~]")

func redactInstanceLog(raw string, values []string) string {
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	for _, v := range values {
		if v != "" {
			raw = strings.ReplaceAll(raw, v, "[REDACTED]")
		}
	}
	raw = instanceLogBearer.ReplaceAllString(raw, "${1}[REDACTED]")
	raw = instanceLogCredential.ReplaceAllString(raw, "${1}[REDACTED]")
	raw = instanceLogANSI.ReplaceAllString(raw, "")
	return strings.Map(func(r rune) rune {
		if r < 32 && r != '\n' && r != '\t' && r != '\r' {
			return -1
		}
		return r
	}, raw)
}

func (m *instanceManager) logs(t *ServiceEnvironment, sv *Service, h Host, fp string) (string, error) {
	r := m.inspect(t, sv, h)
	if !r.Owned || r.Fingerprint == "" || fp != r.Fingerprint {
		return "", fmt.Errorf("实例已变化或归属未核验，请刷新后重新查看日志")
	}
	values := []string{h.Password}
	if env, err := m.st.decryptRuntimeEnvironment(t.runtimeEnvEncrypted); err == nil {
		for _, v := range env {
			values = append(values, v)
		}
	} else {
		return "", fmt.Errorf("日志脱敏配置不可用")
	}
	if jpomJava(sv) {
		command, err := jpomInstanceLogCommand(sv, r, "/proc")
		if err != nil {
			return "", err
		}
		raw, err := m.exec(h, command, 20*time.Second)
		if err != nil {
			return "", fmt.Errorf("日志读取未确认，请检查输出路径和读取权限")
		}
		text, secrets, err := parseJpomInstanceLog(raw)
		if err != nil {
			return "", err
		}
		return redactInstanceLog(text, append(values, secrets...)), nil
	}
	var command string
	if sv.TargetType == "docker-host" {
		// Fetch env only into the redactor, never return/store it or an inspect dump.
		raw, err := m.exec(h, "docker inspect --format '{{json .Config.Env}}' "+shellQuote(r.ContainerID), 10*time.Second)
		var env []string
		if err != nil || len(raw) > 128<<10 || json.Unmarshal([]byte(strings.TrimSpace(raw)), &env) != nil {
			return "", fmt.Errorf("无法读取实例脱敏信息，日志未返回")
		}
		for _, v := range env {
			_, value, ok := strings.Cut(v, "=")
			if ok {
				values = append(values, value)
			}
		}
		command = `tmp=$(mktemp) || exit 2; trap 'rm -f "$tmp"' EXIT; docker logs --tail 200 --timestamps --since 15m ` + shellQuote(r.ContainerID) + ` >"$tmp" 2>&1 || exit 2; tail -c 65536 "$tmp"`
	} else {
		// The same canonical, known release path supplies both logs and redaction.
		if r.Release == "" || r.DeployID <= 0 {
			return "", fmt.Errorf("当前版本归属未确认")
		}
		file := strings.TrimRight(sv.RemoteDir, "/") + "/releases/" + r.Release + "/runtime.env"
		raw, err := m.exec(h, "head -c 131073 -- "+shellQuote(file), 10*time.Second)
		if err != nil || len(raw) > 128<<10 {
			return "", fmt.Errorf("无法读取实例脱敏信息，日志未返回")
		}
		for _, line := range strings.Split(raw, "\n") {
			_, v, ok := strings.Cut(line, "=")
			if ok {
				v = strings.Trim(v, "'")
				v = strings.ReplaceAll(v, `'\''`, "'")
				values = append(values, v)
			}
		}
		base := strings.TrimRight(sv.RemoteDir, "/") + "/logs"
		command = "root=" + shellQuote(base) + `; file=$(readlink -f "$root/application.log") || exit 2; case "$file" in "$root"/*) ;; *) exit 3;; esac; test -f "$file" || exit 2; tail -n 200 "$file" | tail -c 65536`
	}
	out, err := m.exec(h, command, 15*time.Second)
	if err != nil {
		return "", fmt.Errorf("日志读取失败，请检查权限或日志文件")
	}
	if len(out) > 65536 {
		out = out[len(out)-65536:]
	}
	return redactInstanceLog(out, values), nil
}

func registerInstanceLogRoute(mux *http.ServeMux, m *instanceManager) {
	mux.HandleFunc("GET /api/services/{name}/environments/{environment}/instances/{host}/logs", func(w http.ResponseWriter, r *http.Request) {
		t, sv, hosts, err := m.target(r.PathValue("name"), r.PathValue("environment"))
		if err != nil {
			deliveryWriteError(w, 409, err)
			return
		}
		for _, h := range hosts {
			if h.Name == r.PathValue("host") {
				text, err := m.logs(t, sv, h, r.URL.Query().Get("fingerprint"))
				if err != nil {
					deliveryWriteError(w, 409, err)
					return
				}
				w.Header().Set("Cache-Control", "no-store")
				deliveryJSON(w, map[string]any{"text": text, "checked_at": time.Now().Unix(), "limit": 200})
				return
			}
		}
		deliveryWriteError(w, 400, "主机不属于该应用环境")
	})
}
