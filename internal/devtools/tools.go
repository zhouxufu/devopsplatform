package devtools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Tool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations,omitempty"`
	Scope       string         `json:"-"`
}

type Upstreams struct {
	Logs    string
	Monitor string
	Deploy  string
}

type ToolService struct {
	actions *TokenStore
	up      Upstreams
	client  *http.Client
}

func NewToolService(up Upstreams) *ToolService {
	up.Logs = strings.TrimRight(up.Logs, "/")
	up.Monitor = strings.TrimRight(up.Monitor, "/")
	up.Deploy = strings.TrimRight(up.Deploy, "/")
	return &ToolService{up: up, client: &http.Client{Timeout: 15 * time.Second}}
}

func readonly(title string) map[string]any {
	return map[string]any{"title": title, "readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false}
}

func objectSchema(properties map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func (s *ToolService) Tools() []Tool {
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	integer := func(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }
	serviceEnv := map[string]any{"service": str("应用标识"), "environment": str("环境标识，如 dev、test、staging、prod")}
	return append([]Tool{
		{Name: "list_applications", Title: "列出应用", Description: "列出当前令牌有权访问的应用及构建类型。", InputSchema: objectSchema(map[string]any{}), Annotations: readonly("列出应用")},
		{Name: "list_environments", Title: "列出环境", Description: "列出当前令牌有权访问的部署环境。", InputSchema: objectSchema(map[string]any{}), Annotations: readonly("列出环境")},
		{Name: "search_logs", Title: "查询日志", Description: "按应用和环境查询日志。必须明确应用与环境；默认最近 15 分钟，最长 24 小时，最多 200 条。", InputSchema: objectSchema(map[string]any{
			"service": serviceEnv["service"], "environment": serviceEnv["environment"], "query": str("日志表达式：空格/AND、OR、NOT/-、双引号短语、括号、后缀 *；字段支持 service/app、env/environment、host、level、path、trace/trace_id、message。例如：(timeout OR refused) level:(ERROR OR WARN) -heartbeat"), "level": str("可选日志级别"), "host": str("可选主机名"), "trace_id": str("可选 Trace ID"), "start_ms": integer("开始时间 Unix 毫秒"), "end_ms": integer("结束时间 Unix 毫秒"), "limit": integer("返回条数，1 到 200"),
		}, "service", "environment"), Annotations: readonly("查询日志"), Scope: "logs:read"},
		{Name: "get_log_context", Title: "查看日志上下文", Description: "查看指定时间点前后的同应用同环境日志，用于还原异常现场。", InputSchema: objectSchema(map[string]any{
			"service": serviceEnv["service"], "environment": serviceEnv["environment"], "timestamp_ms": integer("中心时间 Unix 毫秒"), "before_ms": integer("向前窗口，默认 30000，最大 300000"), "after_ms": integer("向后窗口，默认 30000，最大 300000"), "host": str("可选主机名"), "limit": integer("返回条数，1 到 200"),
		}, "service", "environment", "timestamp_ms"), Annotations: readonly("查看日志上下文"), Scope: "logs:read"},
		{Name: "get_service_metrics", Title: "查询服务指标", Description: "查询日志级别分钟计数；提供 metric_name 时同时查询对应 OTLP 指标序列。", InputSchema: objectSchema(map[string]any{
			"service": serviceEnv["service"], "environment": serviceEnv["environment"], "instance": str("可选服务实例 ID"), "metric_name": str("可选 OTLP 指标名"), "start_ms": integer("开始时间 Unix 毫秒"), "end_ms": integer("结束时间 Unix 毫秒"),
		}, "service", "environment"), Annotations: readonly("查询服务指标"), Scope: "metrics:read"},
		{Name: "get_recent_traces", Title: "查询最近 Trace", Description: "查询应用环境最近两小时的 Trace 摘要，异常 Trace 优先。", InputSchema: objectSchema(map[string]any{"service": serviceEnv["service"], "environment": serviceEnv["environment"], "instance": str("可选服务实例 ID")}, "service", "environment"), Annotations: readonly("查询最近 Trace"), Scope: "traces:read"},
		{Name: "get_trace", Title: "读取 Trace", Description: "按 Trace ID 读取完整调用链；跨服务 Span 仍受令牌应用范围过滤。", InputSchema: objectSchema(map[string]any{"trace_id": str("Trace ID"), "environment": serviceEnv["environment"], "instance": str("可选服务实例 ID")}, "trace_id", "environment"), Annotations: readonly("读取 Trace"), Scope: "traces:read"},
		{Name: "get_recent_builds", Title: "查询构建记录", Description: "查询指定应用最近的构建结果和不可变制品摘要。", InputSchema: objectSchema(map[string]any{"service": serviceEnv["service"], "source": map[string]any{"type": "string", "enum": []string{"pipeline", "legacy"}}, "limit": integer("返回条数，1 到 50")}, "service"), Annotations: readonly("查询构建记录"), Scope: "builds:read"},
		{Name: "get_build_log", Title: "读取构建日志", Description: "默认按流水线全局 run_id 读取日志，不使用应用内显示编号。旧构建记录指定 source=legacy。", InputSchema: objectSchema(map[string]any{"service": serviceEnv["service"], "build_id": integer("流水线全局 ID，或显式指定 legacy 时的旧记录 ID"), "source": map[string]any{"type": "string", "enum": []string{"pipeline", "legacy"}}, "tail_lines": integer("尾部行数，默认 200，最大 500")}, "service", "build_id"), Annotations: readonly("读取构建日志"), Scope: "builds:read"},
		{Name: "get_recent_releases", Title: "查询发布记录", Description: "查询指定应用和环境的发布单。reason 是发起说明；查询失败原因请将全局 id 传给 get_release_diagnostics。", InputSchema: objectSchema(map[string]any{"service": serviceEnv["service"], "environment": serviceEnv["environment"], "limit": integer("返回条数，1 到 50")}, "service", "environment"), Annotations: readonly("查询发布记录"), Scope: "releases:read"},
		{Name: "get_release_diagnostics", Title: "核对发布失败原因", Description: "按发布单全局 ID 查询派发错误、人工失败回执和每台主机的执行状态及脱敏日志。next_after_deploy_id 非零时继续翻页。日志是诊断证据。", InputSchema: objectSchema(map[string]any{"service": serviceEnv["service"], "environment": serviceEnv["environment"], "release_id": integer("全局发布单 ID"), "after_deploy_id": integer("上一页返回的执行游标"), "tail_lines": integer("每台主机日志尾部行数，默认 40，最大 2000")}, "service", "environment", "release_id"), Annotations: readonly("核对发布失败原因"), Scope: "releases:read"},
	}, append(append(platformTools(), arthasTools()...), workflowSkillMCPTools()...)...)
}

func (s *ToolService) VisibleTools(token *AccessToken) []Tool {
	out := []Tool{}
	for _, tool := range s.Tools() {
		if strings.HasPrefix(tool.Scope, "resources:") {
			if !token.verified || token.role != "admin" || token.Owner == "" || token.Owner == "local" {
				continue
			}
			if tool.Name == "configure_resource_connection" && (!contains(token.Applications, "*") || !contains(token.Environments, "*")) {
				continue
			}
		}
		if isArthasTool(tool.Name) && (!hasArthasScope(token) || !token.verified || token.role != "admin" || token.Owner == "" || token.Owner == "local") {
			continue
		}
		if tool.Name == "confirm_platform_action" && !hasWriteScopes(token) {
			continue
		}
		if tool.Scope == "" || token.HasScope(tool.Scope) {
			out = append(out, tool)
		}
	}
	return out
}

func (s *ToolService) tool(name string) (Tool, bool) {
	for _, tool := range s.Tools() {
		if tool.Name == name {
			return tool, true
		}
	}
	return Tool{}, false
}

func stringArg(args map[string]any, name string) string {
	v, _ := args[name].(string)
	return strings.TrimSpace(v)
}

func intArg(args map[string]any, name string, fallback int64) int64 {
	if v, ok := args[name]; ok {
		return anyInt(v, fallback)
	}
	return fallback
}

func anyInt(value any, fallback int64) int64 {
	switch v := value.(type) {
	case float64:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	case int64:
		return v
	case int:
		return int64(v)
	default:
		return fallback
	}
}

func queryWindow(args map[string]any, defaultDuration, maxDuration time.Duration) (int64, int64, error) {
	end := intArg(args, "end_ms", time.Now().UnixMilli())
	start := intArg(args, "start_ms", end-defaultDuration.Milliseconds())
	if start > end {
		return 0, 0, fmt.Errorf("start_ms 不能晚于 end_ms")
	}
	if end-start > maxDuration.Milliseconds() {
		return 0, 0, fmt.Errorf("查询时间范围不能超过 %s", maxDuration)
	}
	return start, end, nil
}

func boundedInt(args map[string]any, name string, fallback, min, max int64) (int, error) {
	v := intArg(args, name, fallback)
	if v < min || v > max {
		return 0, fmt.Errorf("%s 必须在 %d 到 %d 之间", name, min, max)
	}
	return int(v), nil
}

func (s *ToolService) get(ctx context.Context, base, path string, q url.Values, target any) error {
	u := base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("上游服务不可用: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("上游返回 %d: %s", resp.StatusCode, truncate(string(raw), 500))
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("解析上游响应: %w", err)
	}
	return nil
}

func (s *ToolService) Call(ctx context.Context, token *AccessToken, name string, args map[string]any) (any, string, string, error) {
	tool, ok := s.tool(name)
	if !ok {
		return nil, "", "", fmt.Errorf("未知工具 %s", name)
	}
	if tool.Scope != "" && !token.HasScope(tool.Scope) {
		return nil, "", "", fmt.Errorf("令牌缺少权限 %s", tool.Scope)
	}
	service, environment := stringArg(args, "service"), stringArg(args, "environment")
	if err := token.Allows(service, environment); err != nil {
		return nil, service, environment, err
	}
	var result any
	var err error
	if name == "list_platform_skills" || name == "get_platform_skill" {
		// Only immutable, allowlisted embedded text plus a permission summary.
		// Do not run business-data redaction over file bytes used for integrity checks.
		value, e := s.callWorkflowSkill(token, name, args)
		return value, "", "", e
	}
	if isArthasTool(name) {
		value, e := s.callArthas(ctx, token, name, args)
		return scrubAny(value), service, environment, e
	}
	if isPlatformTool(name) {
		value, e := s.callPlatform(ctx, token, name, args)
		if name == "get_work_item_image" {
			return value, service, environment, e
		}
		if strings.HasPrefix(tool.Scope, "apitests:") {
			return testingOutput(name, cloneJSON(value)), service, environment, e
		}
		return scrubAny(cloneJSON(value)), service, environment, e
	}
	switch name {
	case "list_applications":
		result, err = s.listApplications(ctx, token)
	case "list_environments":
		result, err = s.listEnvironments(ctx, token)
	case "search_logs":
		result, err = s.searchLogs(ctx, args)
	case "get_log_context":
		result, err = s.logContext(ctx, args)
	case "get_service_metrics":
		result, err = s.serviceMetrics(ctx, args)
	case "get_recent_traces":
		result, err = s.recentTraces(ctx, args)
	case "get_trace":
		result, err = s.trace(ctx, token, args)
	case "get_recent_builds":
		result, err = s.recentBuilds(ctx, args)
	case "get_build_log":
		result, err = s.buildLog(ctx, args)
	case "get_recent_releases":
		result, err = s.recentReleases(ctx, args)
	case "get_release_diagnostics":
		result, err = s.releaseDiagnostics(ctx, args)
	}
	if err != nil {
		return nil, service, environment, err
	}
	return scrubAny(result), service, environment, nil
}

func (s *ToolService) listApplications(ctx context.Context, token *AccessToken) (any, error) {
	var rows []map[string]any
	if err := s.get(ctx, s.up.Deploy, "/api/services", nil, &rows); err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, row := range rows {
		name, _ := row["name"].(string)
		if token.Allows(name, "") != nil {
			continue
		}
		out = append(out, map[string]any{"name": name, "tags": row["tags"], "artifact_type": row["artifact_type"], "build_tool": row["build_tool"], "branch": row["branch"]})
	}
	return map[string]any{"applications": out}, nil
}

func (s *ToolService) listEnvironments(ctx context.Context, token *AccessToken) (any, error) {
	var rows []map[string]any
	if err := s.get(ctx, s.up.Deploy, "/api/environments", nil, &rows); err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, row := range rows {
		name, _ := row["name"].(string)
		if token.Allows("", name) == nil {
			out = append(out, map[string]any{"name": name, "display_name": row["display_name"], "protected": row["protected"]})
		}
	}
	return map[string]any{"environments": out}, nil
}

func (s *ToolService) searchLogs(ctx context.Context, args map[string]any) (any, error) {
	service, environment := stringArg(args, "service"), stringArg(args, "environment")
	if service == "" || environment == "" {
		return nil, fmt.Errorf("service 和 environment 必填")
	}
	start, end, err := queryWindow(args, 15*time.Minute, 24*time.Hour)
	if err != nil {
		return nil, err
	}
	limit, err := boundedInt(args, "limit", 100, 1, 200)
	if err != nil {
		return nil, err
	}
	q := url.Values{"service": {service}, "environment": {environment}, "start": {strconv.FormatInt(start, 10)}, "end": {strconv.FormatInt(end, 10)}, "limit": {strconv.Itoa(limit)}}
	for arg, param := range map[string]string{"query": "q", "host": "host", "level": "level", "trace_id": "trace_id"} {
		if v := stringArg(args, arg); v != "" {
			q.Set(param, v)
		}
	}
	var result map[string]any
	if err := s.get(ctx, s.up.Logs, "/api/search", q, &result); err != nil {
		return nil, err
	}
	truncateLogHits(result)
	result["query_window"] = map[string]any{"start_ms": start, "end_ms": end}
	return result, nil
}

func (s *ToolService) logContext(ctx context.Context, args map[string]any) (any, error) {
	ts := intArg(args, "timestamp_ms", 0)
	if ts <= 0 {
		return nil, fmt.Errorf("timestamp_ms 必填")
	}
	before := intArg(args, "before_ms", 30000)
	after := intArg(args, "after_ms", 30000)
	if before < 0 || after < 0 || before > 300000 || after > 300000 {
		return nil, fmt.Errorf("上下文窗口不能超过前后各 300000ms")
	}
	copyArgs := make(map[string]any, len(args)+2)
	for k, v := range args {
		copyArgs[k] = v
	}
	copyArgs["start_ms"], copyArgs["end_ms"] = ts-before, ts+after
	return s.searchLogs(ctx, copyArgs)
}

func (s *ToolService) serviceMetrics(ctx context.Context, args map[string]any) (any, error) {
	service, environment := stringArg(args, "service"), stringArg(args, "environment")
	if service == "" || environment == "" {
		return nil, fmt.Errorf("service 和 environment 必填")
	}
	start, end, err := queryWindow(args, time.Hour, 24*time.Hour)
	if err != nil {
		return nil, err
	}
	q := url.Values{"service": {service}, "environment": {environment}, "start": {strconv.FormatInt(start, 10)}, "end": {strconv.FormatInt(end, 10)}}
	var logMetrics map[string]any
	if err := s.get(ctx, s.up.Monitor, "/api/metrics", q, &logMetrics); err != nil {
		return nil, err
	}
	result := map[string]any{"log_metrics": logMetrics, "query_window": map[string]any{"start_ms": start, "end_ms": end}}
	if metric := stringArg(args, "metric_name"); metric != "" {
		q.Set("name", metric)
		if instance := stringArg(args, "instance"); instance != "" {
			q.Set("instance", instance)
		}
		var series any
		if err := s.get(ctx, s.up.Monitor, "/api/otlp/series", q, &series); err != nil {
			return nil, err
		}
		result["otlp_metric"] = map[string]any{"name": metric, "series": series}
	} else {
		q := url.Values{"service": {service}, "environment": {environment}}
		if instance := stringArg(args, "instance"); instance != "" {
			q.Set("instance", instance)
		}
		var names any
		if err := s.get(ctx, s.up.Monitor, "/api/otlp/names", q, &names); err == nil {
			result["available_otlp_metrics"] = names
		}
	}
	return result, nil
}

func (s *ToolService) recentTraces(ctx context.Context, args map[string]any) (any, error) {
	service, environment := stringArg(args, "service"), stringArg(args, "environment")
	if service == "" || environment == "" {
		return nil, fmt.Errorf("service 和 environment 必填")
	}
	q := url.Values{"service": {service}, "environment": {environment}}
	if instance := stringArg(args, "instance"); instance != "" {
		q.Set("instance", instance)
	}
	var result any
	if err := s.get(ctx, s.up.Monitor, "/api/otlp/traces/recent", q, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *ToolService) trace(ctx context.Context, token *AccessToken, args map[string]any) (any, error) {
	id, environment := stringArg(args, "trace_id"), stringArg(args, "environment")
	if id == "" || environment == "" {
		return nil, fmt.Errorf("trace_id 和 environment 必填")
	}
	q := url.Values{"id": {id}, "environment": {environment}}
	if instance := stringArg(args, "instance"); instance != "" {
		q.Set("instance", instance)
	}
	var rows []map[string]any
	if err := s.get(ctx, s.up.Monitor, "/api/otlp/trace", q, &rows); err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		service, _ := row["service"].(string)
		if token.Allows(service, environment) == nil {
			out = append(out, row)
		}
	}
	return map[string]any{"trace_id": id, "environment": environment, "spans": out, "filtered_spans": len(rows) - len(out)}, nil
}

func (s *ToolService) recentBuilds(ctx context.Context, args map[string]any) (any, error) {
	service := stringArg(args, "service")
	if service == "" {
		return nil, fmt.Errorf("service 必填")
	}
	limit, err := boundedInt(args, "limit", 20, 1, 50)
	if err != nil {
		return nil, err
	}
	source, path := stringArg(args, "source"), "/api/pipeline/runs"
	if source == "" {
		source = "pipeline"
	}
	switch source {
	case "pipeline":
	case "legacy":
		path = "/api/builds"
	default:
		return nil, fmt.Errorf("构建来源无效")
	}
	var rows []map[string]any
	if err := s.get(ctx, s.up.Deploy, path, url.Values{"service": {service}, "limit": {strconv.Itoa(limit)}}, &rows); err != nil {
		return nil, err
	}
	for i, row := range rows {
		if row["service"] != service {
			return nil, fmt.Errorf("构建不属于授权应用")
		}
		safe := map[string]any{"source": source}
		if source == "pipeline" {
			safe["run_id"] = row["id"]
		}
		for _, field := range []string{"id", "number", "service", "branch", "version", "status", "artifact_id", "artifact_digest", "started_at", "ended_at"} {
			safe[field] = row[field]
		}
		rows[i] = safe
	}
	return map[string]any{"builds": rows}, nil
}

func (s *ToolService) buildLog(ctx context.Context, args map[string]any) (any, error) {
	service := stringArg(args, "service")
	id := intArg(args, "build_id", 0)
	if service == "" || id <= 0 {
		return nil, fmt.Errorf("service 和 build_id 必填")
	}
	source := stringArg(args, "source")
	if source == "" {
		source = "pipeline"
	}
	path := fmt.Sprintf("/api/pipeline/runs/%d", id)
	if source == "pipeline" {
		var run map[string]any
		if err := s.get(ctx, s.up.Deploy, path, nil, &run); err != nil {
			return nil, err
		}
		if run["service"] != service {
			return nil, fmt.Errorf("构建不属于授权应用")
		}
	} else if source == "legacy" {
		var builds []map[string]any
		if err := s.get(ctx, s.up.Deploy, "/api/builds", url.Values{"service": {service}, "limit": {"500"}}, &builds); err != nil {
			return nil, err
		}
		found := false
		for _, build := range builds {
			if anyInt(build["id"], 0) == id {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("应用 %s 中不存在构建 #%d，或已超出可查询历史", service, id)
		}
		path = fmt.Sprintf("/api/builds/%d", id)
	} else {
		return nil, fmt.Errorf("构建来源无效")
	}
	var result map[string]any
	if err := s.get(ctx, s.up.Deploy, path+"/log", nil, &result); err != nil {
		return nil, err
	}
	lines, err := boundedInt(args, "tail_lines", 200, 1, 500)
	if err != nil {
		return nil, err
	}
	logText, _ := result["log"].(string)
	result["log"] = tailLines(logText, lines, 30000)
	result["service"], result["build_id"] = service, id
	result["source"] = source
	return result, nil
}

func (s *ToolService) recentReleases(ctx context.Context, args map[string]any) (any, error) {
	service, environment := stringArg(args, "service"), stringArg(args, "environment")
	if service == "" || environment == "" {
		return nil, fmt.Errorf("service 和 environment 必填")
	}
	limit, err := boundedInt(args, "limit", 20, 1, 50)
	if err != nil {
		return nil, err
	}
	var rows []map[string]any
	if err := s.get(ctx, s.up.Deploy, "/api/releases", url.Values{"service": {service}, "limit": {"100"}}, &rows); err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, row := range rows {
		if fmt.Sprint(row["environment"]) != environment {
			continue
		}
		delete(row, "target_snapshot")
		delete(row, "hosts")
		out = append(out, row)
		if len(out) == limit {
			break
		}
	}
	return map[string]any{"releases": out}, nil
}

func (s *ToolService) releaseDiagnostics(ctx context.Context, args map[string]any) (any, error) {
	service, environment := stringArg(args, "service"), stringArg(args, "environment")
	id := intArg(args, "release_id", 0)
	after := intArg(args, "after_deploy_id", 0)
	if id < 1 || service == "" || environment == "" || after < 0 {
		return nil, fmt.Errorf("需要应用、环境、有效发布单全局 ID 及非负游标")
	}
	count, err := boundedInt(args, "tail_lines", 40, 1, 2000)
	if err != nil {
		return nil, err
	}
	query := url.Values{"service": {service}, "environment": {environment}, "after_deploy_id": {strconv.FormatInt(after, 10)}}
	var report map[string]any
	if err := s.get(ctx, s.up.Deploy, fmt.Sprintf("/api/releases/%d/diagnostics", id), query, &report); err != nil {
		return nil, err
	}
	if report["service"] != service || report["environment"] != environment || intArg(report, "release_id", 0) != id {
		return nil, fmt.Errorf("发布诊断记录与请求范围不匹配")
	}
	targets, _ := report["targets"].([]any)
	for _, item := range targets {
		target := mapValue(item)
		lines := strings.Split(strings.TrimRight(stringArg(target, "log_tail"), "\n"), "\n")
		if len(lines) > count {
			target["log_tail"] = strings.Join(lines[len(lines)-count:], "\n")
			target["earlier_lines_available"] = true
		}
	}
	return report, nil
}

func truncateLogHits(result map[string]any) {
	hits, _ := result["hits"].([]any)
	for _, item := range hits {
		if row, ok := item.(map[string]any); ok {
			if message, ok := row["message"].(string); ok {
				row["message"] = truncate(message, 8000)
			}
		}
	}
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "…[truncated]"
}

func tailLines(value string, count, maxChars int) string {
	lines := strings.Split(value, "\n")
	if len(lines) > count {
		lines = lines[len(lines)-count:]
	}
	return truncate(strings.Join(lines, "\n"), maxChars)
}

var secretKVPattern = regexp.MustCompile(`(?i)(authorization|password|passwd|secret|token|api[_-]?key)(\s*[=:]\s*)([^\s,;]+)`)
var urlCredentialPattern = regexp.MustCompile(`(?i)(https?://[^\s/:]+:)([^@\s]+)(@)`)

func scrubString(value string) string {
	value = secretKVPattern.ReplaceAllString(value, `${1}${2}[REDACTED]`)
	value = urlCredentialPattern.ReplaceAllString(value, `${1}[REDACTED]${3}`)
	return value
}

func scrubAny(v any) any {
	switch x := v.(type) {
	case string:
		return scrubString(x)
	case []any:
		for i := range x {
			x[i] = scrubAny(x[i])
		}
	case []map[string]any:
		for i := range x {
			x[i] = scrubAny(x[i]).(map[string]any)
		}
	case map[string]any:
		if key, ok := x["key"].(string); ok && sensitiveOutputKey(key) {
			if _, ok := x["value"]; ok {
				x["value"] = "[REDACTED]"
			}
		}
		for k, value := range x {
			if sensitiveOutputKey(k) {
				x[k] = "[REDACTED]"
			} else {
				x[k] = scrubAny(value)
			}
		}
	}
	return v
}

func sensitiveOutputKey(key string) bool {
	lower := strings.ToLower(key)
	for _, part := range []string{"password", "secret", "token", "authorization", "credential", "access_key", "apikey", "api_key"} {
		if strings.Contains(lower, part) {
			return true
		}
	}
	return false
}

func toolNames(tools []Tool) []string {
	names := make([]string, len(tools))
	for i := range tools {
		names[i] = tools[i].Name
	}
	sort.Strings(names)
	return names
}
