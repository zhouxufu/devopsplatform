package server

import (
	"encoding/json"
	"log"
	"logplatform/internal/logtext"
	"logplatform/internal/types"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ---------- 事件富化：level / traceId 提取（server 侧，规则集中可改） ----------

var levelRe = regexp.MustCompile(`\b(ERROR|FATAL|WARN|WARNING|INFO|DEBUG|TRACE)\b`)

func normLevel(l string) string {
	switch strings.ToUpper(l) {
	case "WARNING":
		return "WARN"
	case "":
		return ""
	default:
		return strings.ToUpper(l)
	}
}

// ---------- traceId 提取规则：文件化 + 按 environment/service 可配 + 热加载 ----------
// rules.json 格式: [{"environment":"prod","service":"order-api","regex":"traceId=([0-9A-Za-z-]+)"}]
// environment/service 都支持显式 "*" 通配；旧规则未填 environment 时迁移为 "*"。

type traceRules struct {
	mu      sync.RWMutex
	path    string
	mtime   int64
	byScope map[string][]*regexp.Regexp
}

var theTraceRules = &traceRules{byScope: map[string][]*regexp.Regexp{}}

var defaultTraceRe = regexp.MustCompile(`(?i)trace[_-]?id["'=:\s]+([0-9a-zA-Z\-]{8,64})`)

// LoadTraceRules 加载规则文件（不存在则仅用内置规则），并每 30s 热加载
func LoadTraceRules(path string) {
	if path == "" {
		return
	}
	theTraceRules.path = path
	loadTraceRulesOnce()
	go func() {
		for range time.Tick(30 * time.Second) {
			loadTraceRulesOnce()
		}
	}()
}

func loadTraceRulesOnce() {
	fi, err := os.Stat(theTraceRules.path)
	if err != nil || fi.ModTime().Unix() == theTraceRules.mtime {
		return
	}
	raw, err := os.ReadFile(theTraceRules.path)
	if err != nil {
		return
	}
	var defs []struct {
		Environment string `json:"environment"`
		Service     string `json:"service"`
		Regex       string `json:"regex"`
	}
	if json.Unmarshal(raw, &defs) != nil {
		log.Printf("[rules] parse %s failed, keep old rules", theTraceRules.path)
		return
	}
	m := map[string][]*regexp.Regexp{}
	for _, d := range defs {
		d.Environment = strings.TrimSpace(d.Environment)
		if d.Environment == "" {
			d.Environment = "*"
		}
		d.Service = strings.TrimSpace(d.Service)
		if d.Service == "" {
			d.Service = "*"
		}
		if re, err := regexp.Compile(d.Regex); err == nil {
			key := d.Environment + "\x00" + d.Service
			m[key] = append(m[key], re)
		}
	}
	theTraceRules.mu.Lock()
	theTraceRules.byScope = m
	theTraceRules.mtime = fi.ModTime().Unix()
	theTraceRules.mu.Unlock()
	log.Printf("[rules] loaded %d trace rules from %s", len(defs), theTraceRules.path)
}

func matchTraceID(environment, service, msg string) string {
	if len(msg) > 2048 {
		msg = msg[:2048]
	}
	theTraceRules.mu.RLock()
	defer theTraceRules.mu.RUnlock()
	for _, scope := range [][2]string{{environment, service}, {environment, "*"}, {"*", service}, {"*", "*"}} {
		for _, re := range theTraceRules.byScope[scope[0]+"\x00"+scope[1]] {
			if m := re.FindStringSubmatch(msg); m != nil && len(m) > 1 {
				return m[1]
			}
		}
	}
	if m := defaultTraceRe.FindStringSubmatch(msg); m != nil {
		return m[1]
	}
	return ""
}

// Enrich 提取 level 与 traceId（JSON 字段优先，其次规则正则；单条超长截断）
func Enrich(e *types.Event, maxLen int) {
	e.Message = logtext.StripANSIColors(e.Message)
	if len(e.Message) > maxLen {
		e.Message = e.Message[:maxLen] + "... [truncated]"
	}
	if e.Level == "" {
		head := e.Message
		if len(head) > 256 {
			head = head[:256]
		}
		if m := levelRe.FindStringSubmatch(head); m != nil {
			e.Level = m[1]
		}
	}
	e.Level = normLevel(e.Level)
	if e.TraceID == "" {
		msg := e.Message
		if len(msg) > 2048 {
			msg = msg[:2048]
		}
		trimmed := strings.TrimSpace(msg)
		if strings.HasPrefix(trimmed, "{") {
			var m map[string]any
			if json.Unmarshal([]byte(trimmed), &m) == nil {
				for _, k := range []string{"trace_id", "traceId", "traceID", "trace-id"} {
					if v, ok := m[k].(string); ok && v != "" {
						e.TraceID = v
						return
					}
				}
			}
		}
		e.TraceID = matchTraceID(e.Environment, e.Service, msg)
	}
}
