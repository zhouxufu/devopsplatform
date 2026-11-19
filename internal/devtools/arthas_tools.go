package devtools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

func arthasTools() []Tool {
	str := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	scope := map[string]any{"service": str("明确的应用标识"), "environment": str("明确的环境标识")}
	return []Tool{
		{Name: "list_arthas_instances", Title: "查询 Java 诊断实例", Description: "列出指定应用和环境已配置的 Arthas 实例；不连接 JVM、不返回地址或认证凭据。需要本人管理员账号及专用 arthas:read 权限。", Scope: "arthas:read", Annotations: readonly("查询 Java 诊断实例"), InputSchema: objectSchema(scope, "service", "environment")},
		{Name: "diagnose_jvm", Title: "常用 Java 诊断", Description: "对明确实例执行一次有界 JVM/内存/热点线程/阻塞线程诊断。使用服务器已配置认证；无需打开页面或传密码。结果未知时不自动重试。只接受固定诊断，不接受命令或表达式，不提供热更新、堆转储或强制 GC。", Scope: "arthas:read", Annotations: map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": false, "openWorldHint": false}, InputSchema: objectSchema(map[string]any{"service": scope["service"], "environment": scope["environment"], "instance_id": str("list_arthas_instances 返回的实例 ID"), "revision": map[string]any{"type": "integer", "minimum": 1, "description": "实例列表返回的配置修订号"}, "diagnostic": map[string]any{"type": "string", "enum": []string{"overview", "memory", "hot_threads", "blocked_threads"}, "description": "JVM 概览、内存、前五个热点线程或阻塞线程"}}, "service", "environment", "instance_id", "revision", "diagnostic")},
	}
}
func isArthasTool(name string) bool { return name == "list_arthas_instances" || name == "diagnose_jvm" }
func hasArthasScope(t *AccessToken) bool {
	for _, scope := range t.Scopes {
		if scope == "arthas:read" {
			return true
		}
	}
	return false
}

func (s *ToolService) arthasRequest(ctx context.Context, t *AccessToken, method, path string, body any) (any, error) {
	var b io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		b = bytes.NewReader(raw)
	}
	r, err := http.NewRequestWithContext(ctx, method, s.up.Monitor+path, b)
	if err != nil {
		return nil, fmt.Errorf("诊断服务地址无效")
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-User", t.Owner)
	r.Header.Set("X-Role", t.role)
	client := *s.client
	client.Timeout = 30 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Do(r)
	if err != nil {
		return nil, fmt.Errorf("诊断响应未确认，不会重试；请核对目标")
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 {
		return nil, fmt.Errorf("诊断结果读取不完整，不会重试")
	}
	if res.StatusCode != 200 {
		var v struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &v)
		if v.Error == "" {
			v.Error = "诊断服务异常，请在监控中核对连接"
		}
		return nil, fmt.Errorf("诊断服务返回 %d：%s", res.StatusCode, truncate(v.Error, 500))
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil, fmt.Errorf("诊断响应格式无效，不会重试")
	}
	return value, nil
}

func (s *ToolService) callArthas(ctx context.Context, t *AccessToken, name string, args map[string]any) (any, error) {
	if !hasArthasScope(t) {
		return nil, fmt.Errorf("Java 诊断必须单独授权 arthas:read；通配权限不会自动扩权")
	}
	if !t.verified || t.role != "admin" || t.Owner == "" || t.Owner == "local" {
		return nil, fmt.Errorf("Java 诊断需要本人已登录管理员账号创建的专用令牌，并通过平台网关验证身份")
	}
	tool, _ := s.tool(name)
	if err := validateToolArgs(tool.InputSchema, args); err != nil {
		return nil, err
	}
	if name == "diagnose_jvm" {
		switch stringArg(args, "diagnostic") {
		case "overview", "memory", "hot_threads", "blocked_threads":
		default:
			return nil, fmt.Errorf("仅支持 JVM、内存、热点线程或阻塞线程诊断")
		}
	}
	service, environment := stringArg(args, "service"), stringArg(args, "environment")
	if service == "" || environment == "" || service == "*" || environment == "*" {
		return nil, fmt.Errorf("应用和环境必须明确，不能省略")
	}
	if err := t.Allows(service, environment); err != nil {
		return nil, err
	}
	q := url.Values{"service": {service}, "environment": {environment}}
	value, err := s.arthasRequest(ctx, t, "GET", "/api/arthas/targets?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	rows, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("实例列表格式无效")
	}
	instances := []any{}
	var selected map[string]any
	for _, item := range rows {
		row, ok := item.(map[string]any)
		if !ok || row["service"] != service || row["environment"] != environment {
			continue
		}
		meta := map[string]any{"id": row["id"], "name": row["name"], "service": service, "environment": environment, "revision": row["revision"], "has_credential": row["has_credential"]}
		instances = append(instances, meta)
		if row["id"] == stringArg(args, "instance_id") {
			selected = meta
		}
	}
	if name == "list_arthas_instances" {
		return instances, nil
	}
	if selected == nil || anyInt(selected["revision"], 0) != intArg(args, "revision", 0) {
		return nil, fmt.Errorf("实例不属于授权范围或配置已变化，请重新列出实例")
	}
	result, err := s.arthasRequest(ctx, t, "POST", "/api/arthas/diagnose", map[string]any{"target_id": selected["id"], "revision": selected["revision"], "service": service, "environment": environment, "diagnostic": args["diagnostic"]})
	if err != nil {
		return nil, err
	}
	response, ok := result.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("诊断结果格式无效")
	}
	target, ok := response["target"].(map[string]any)
	if !ok || target["id"] != selected["id"] || target["service"] != service || target["environment"] != environment || anyInt(target["revision"], 0) != intArg(args, "revision", 0) {
		return nil, fmt.Errorf("诊断结果目标与请求不符，请核对，不要重试")
	}
	return result, nil
}
