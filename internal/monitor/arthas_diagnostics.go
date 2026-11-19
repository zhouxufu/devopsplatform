package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

var arthasDiagnostics = map[string]string{"overview": "jvm", "memory": "memory", "hot_threads": "thread -n 5", "blocked_threads": "thread -b"}

// Fixed presets only. Neither the browser nor MCP can supply a command,
// expression, destination URL, credentials or HTTP headers to this endpoint.
func (m *arthasConsole) diagnose(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TargetID    string `json:"target_id"`
		Revision    int64  `json:"revision"`
		Service     string `json:"service"`
		Environment string `json:"environment"`
		Diagnostic  string `json:"diagnostic"`
	}
	if !arthasDecode(w, r, &in) {
		return
	}
	command, ok := arthasDiagnostics[in.Diagnostic]
	if !ok || in.Service == "" || in.Environment == "" {
		arthasError(w, 400, "请选择应用、环境、实例及常用诊断项目")
		return
	}
	t, err := m.st.arthasTarget(in.TargetID)
	if err != nil || t.Service != in.Service || t.Environment != in.Environment || t.Revision != in.Revision {
		arthasError(w, 409, "实例不属于所选应用/环境或配置已变化，请重新选择")
		return
	}
	actor := r.Header.Get("X-User")
	m.mu.Lock()
	count, total := m.pending[actor], len(m.sessions)
	for _, n := range m.pending {
		total += n
	}
	for _, s := range m.sessions {
		if s.owner == actor {
			count++
		}
	}
	if m.stopping || count >= 4 || total >= 20 {
		m.mu.Unlock()
		arthasError(w, 429, "诊断任务过多，请等待当前任务结束")
		return
	}
	m.pending[actor]++
	m.targetPending[t.ID]++
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.pending[actor]--; m.targetPending[t.ID]--; m.mu.Unlock() }()
	credential, err := m.st.savedArthasCredential(t)
	if err != nil {
		arthasError(w, 409, err.Error())
		return
	}
	s := &arthasSession{owner: actor, target: *t, username: "arthas"}
	if credential != nil {
		s.username, s.password = credential.Username, credential.Password
	}
	if err = m.audit(actor, t.ID, "diagnose", command, "connecting"); err != nil {
		arthasError(w, 500, "无法记录诊断审计，未连接目标")
		return
	}
	initCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	out, err := m.call(initCtx, s, map[string]any{"action": "init_session"})
	cancel()
	if err != nil || out.State != "SUCCEEDED" || out.SessionID == "" || len(out.SessionID) > 512 || len(out.ConsumerID) > 512 {
		_ = m.audit(actor, t.ID, "diagnose", command, "failed")
		message := "Arthas 未建立会话，请在配置连接中检查认证和地址"
		if err != nil {
			message = err.Error()
		}
		arthasError(w, 502, message)
		return
	}
	s.remote, s.consumer = out.SessionID, out.ConsumerID
	defer m.closeRemote(s)
	// Configuration is frozen before any JVM command, including concurrent edits.
	latest, e := m.st.arthasTarget(t.ID)
	if e != nil || latest.Revision != t.Revision {
		arthasError(w, 409, "连接期间配置已变化，未执行诊断")
		return
	}
	if r.Context().Err() != nil {
		return
	}
	if m.audit(actor, t.ID, "diagnose", command, "executing") != nil {
		arthasError(w, 500, "无法记录诊断审计，未执行命令")
		return
	}
	execCtx, cancel := context.WithTimeout(r.Context(), 13*time.Second)
	out, err = m.call(execCtx, s, map[string]any{"action": "exec", "command": command, "execTimeout": 10000, "requestId": arthasID()})
	cancel()
	if err != nil {
		_ = m.audit(actor, t.ID, "diagnose", command, "unknown")
		arthasError(w, 502, err.Error()+"；本次独立会话将关闭，请核对后再发起新诊断")
		return
	}
	state := arthasResultState(out)
	if m.audit(actor, t.ID, "diagnose", command, state) != nil {
		arthasError(w, 500, "诊断已发送但结果审计未保存，请核对，不要重复提交")
		return
	}
	var result any
	b, _ := json.Marshal(out.Body.Results)
	_ = json.Unmarshal(b, &result)
	result = sanitizeArthasDiagnostic(redactArthasResult(result, s.password))
	rows := arthasDiagnosticRows(result)
	summary := "诊断已完成；单次采样不能证明应用持续健康"
	if len(rows) == 0 {
		summary = "命令已完成，未返回可解析指标；请展开原始输出核对"
	}
	if state != "success" {
		summary = map[string]string{"failed": "诊断未成功，请查看原始输出", "timeout": "达到 10 秒采样上限，请核对已返回的部分结果", "unknown": "目标未确认终态，不会自动重试"}[state]
	}
	writeJSON(w, map[string]any{"state": state, "diagnostic": in.Diagnostic, "target": map[string]any{"id": t.ID, "name": t.Name, "service": t.Service, "environment": t.Environment, "revision": t.Revision}, "sampled_at": time.Now().UnixMilli(), "summary": summary, "rows": rows, "output": result, "transport": "http-api"})
}

func arthasResultState(out *arthasReply) string {
	if out.Body.TimeExpired {
		return "timeout"
	}
	if out.State != "SUCCEEDED" {
		return "failed"
	}
	if out.Body.JobStatus != "TERMINATED" {
		return "unknown"
	}
	for _, raw := range out.Body.Results {
		var v struct {
			Type string `json:"type"`
			Code int    `json:"statusCode"`
		}
		if json.Unmarshal(raw, &v) == nil && v.Type == "status" && v.Code != 0 {
			return "failed"
		}
	}
	return "success"
}

type arthasMetricRow struct {
	Section string `json:"section"`
	Label   string `json:"label"`
	Value   string `json:"value"`
}

var diagnosticSecret = regexp.MustCompile(`(?i)((?:password|passwd|secret|token|api[_-]?key|authorization|cookie)\s*[:=]\s*)[^\r\n,;]+`)

func arthasSensitiveField(key string) bool {
	k := strings.ToLower(strings.NewReplacer("-", "", "_", "", " ", "").Replace(key))
	for _, s := range []string{"password", "passwd", "secret", "token", "apikey", "authorization", "cookie", "inputargument", "systempropert", "classpath", "environment"} {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}
func sanitizeArthasDiagnostic(v any) any {
	switch x := v.(type) {
	case string:
		return diagnosticSecret.ReplaceAllString(x, "${1}[REDACTED]")
	case []any:
		for i := range x {
			x[i] = sanitizeArthasDiagnostic(x[i])
		}
		return x
	case map[string]any:
		out := map[string]any{}
		name, _ := x["name"].(string)
		for k, value := range x {
			if arthasSensitiveField(k) || k == "value" && arthasSensitiveField(name) {
				out[k] = "[REDACTED]"
			} else {
				out[k] = sanitizeArthasDiagnostic(value)
			}
		}
		return out
	}
	return v
}

// Flatten only diagnostic models, with a strict row/value bound. No status or
// missing model is fabricated into a healthy JVM or a zero-blocking-thread claim.
func arthasDiagnosticRows(v any) []arthasMetricRow {
	rows := []arthasMetricRow{}
	var walk func(any, string, int)
	walk = func(value any, path string, depth int) {
		if len(rows) >= 100 || depth > 6 {
			return
		}
		switch x := value.(type) {
		case map[string]any:
			if name, _ := x["name"].(string); arthasSensitiveField(name) {
				return
			}
			if path == "jvmInfo" || path == "memoryInfo" {
				keys := make([]string, 0, len(x))
				for key := range x {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				for _, key := range keys {
					if !arthasSensitiveField(key) {
						section := key
						if path == "memoryInfo" {
							section = "内存 / " + key
						}
						walk(x[key], section, depth+1)
					}
				}
				return
			}
			if name, ok := x["name"].(string); ok && x["value"] != nil && !arthasSensitiveField(name) {
				b, _ := json.Marshal(x["value"])
				text := string(b)
				if s, ok := x["value"].(string); ok {
					text = s
				}
				if len(text) > 600 {
					text = text[:600] + "…"
				}
				rows = append(rows, arthasMetricRow{path, name, text})
				return
			}
			for _, key := range []string{"jvmInfo", "heapMemory", "nonHeapMemory", "heap", "nonHeap", "bufferPoolMemory", "memoryInfo", "busyThreads", "threadStats", "blockingLockInfo", "threadInfo", "blockingThreadInfo", "RUNTIME", "CLASS-LOADING", "COMPILATION", "GARBAGE-COLLECTORS", "MEMORY-MANAGERS", "MEMORY", "OPERATING-SYSTEM", "THREAD"} {
				if child, ok := x[key]; ok && child != nil {
					walk(child, key, depth+1)
				}
			}
			if depth > 0 {
				for _, key := range []string{"id", "name", "state", "threadId", "threadName", "threadState", "cpu", "cpuUsage", "cpuTime", "deltaTime", "blockedCount", "blockedTime", "blockingThreadCount", "waitedCount", "lockName", "lockOwnerName", "used", "total", "max", "committed", "init", "usage"} {
					if child, ok := x[key]; ok && child != nil {
						text := fmt.Sprint(child)
						if n, ok := child.(float64); ok && strings.HasPrefix(path, "内存 / ") && (key == "used" || key == "total" || key == "max" || key == "committed" || key == "init") {
							text = "未设置"
							if n >= 0 {
								unit := 0
								units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
								for n >= 1024 && unit < len(units)-1 {
									n /= 1024
									unit++
								}
								text = fmt.Sprintf("%.2f %s", n, units[unit])
							}
						}
						if len([]rune(text)) > 600 {
							text = string([]rune(text)[:600]) + "…"
						}
						rows = append(rows, arthasMetricRow{path, key, text})
						if len(rows) >= 100 {
							return
						}
					}
				}
			}
		case []any:
			for i, child := range x {
				walk(child, fmt.Sprintf("%s %d", path, i+1), depth+1)
			}
		}
	}
	walk(v, "", 0)
	return rows
}
