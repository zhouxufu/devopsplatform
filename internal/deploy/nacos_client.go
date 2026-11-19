package deploy

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const nacosMaxContent = 1 << 20

func validateNacosConnection(c *ResourceConnection) error {
	if c.Config == nil {
		c.Config = map[string]string{}
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("Nacos 地址填写 HTTP(S) 服务根路径，例如 http://host:8848/nacos；不可包含凭据或查询参数")
	}
	if v := c.Config["nacos_version"]; v != "" && v != "2.4" && v != "2.5" {
		return fmt.Errorf("当前 Nacos 执行适配 2.4/2.5 的兼容 v1 配置 API；不自动降级其他版本")
	}
	if c.Config["nacos_version"] == "" {
		c.Config["nacos_version"] = "2.4"
	}
	if c.Config["nacos_auth"] == "" {
		c.Config["nacos_auth"] = "password"
	}
	if c.Config["namespace"] == "public" {
		c.Config["namespace"] = ""
	}
	c.Endpoint = strings.TrimRight(c.Endpoint, "/")
	switch c.Config["nacos_auth"] {
	case "password", "token", "none":
	default:
		return fmt.Errorf("Nacos 认证支持 password、token 或 none")
	}
	for _, key := range []string{"namespace", "test_data_id", "test_group"} {
		if strings.ContainsAny(c.Config[key], "\r\n\x00") || len(c.Config[key]) > 255 {
			return fmt.Errorf("Nacos 命名空间或测试配置标识无效")
		}
	}
	return nil
}

type nacosClient struct {
	connection *ResourceConnection
	http       *http.Client
	token      string
}

func newNacosClient(ctx context.Context, c *ResourceConnection) (*nacosClient, error) {
	if c == nil || c.Kind != "nacos" {
		return nil, fmt.Errorf("Nacos 连接不存在")
	}
	if err := validateNacosConnection(c); err != nil {
		return nil, err
	}
	n := &nacosClient{connection: c, http: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	switch c.Config["nacos_auth"] {
	case "password":
		if c.Username == "" || c.Password == "" {
			return nil, fmt.Errorf("请配置 Nacos 专用账号和密码")
		}
		b, _, err := n.request(ctx, http.MethodPost, "/v1/auth/login", url.Values{"username": {c.Username}, "password": {c.Password}}, "")
		if err != nil {
			return nil, err
		}
		var reply struct {
			AccessToken string `json:"accessToken"`
		}
		if json.Unmarshal(b, &reply) != nil || reply.AccessToken == "" {
			return nil, fmt.Errorf("Nacos 登录未返回有效 Token，请核对版本和服务根路径")
		}
		n.token = reply.AccessToken
	case "token":
		n.token = c.Password
		if n.token == "" {
			return nil, fmt.Errorf("请在密码/Token 字段配置 Nacos Token")
		}
	}
	return n, nil
}

// All errors are classified without exposing URL, upstream body or credentials.
// Tokens use Authorization headers (Nacos 2.4/2.5), never query strings.
func (n *nacosClient) request(ctx context.Context, method, path string, args url.Values, cas string) ([]byte, int, error) {
	target := strings.TrimRight(n.connection.Endpoint, "/") + path
	var body io.Reader
	if method == http.MethodGet {
		target += "?" + args.Encode()
	} else {
		body = strings.NewReader(args.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, 0, fmt.Errorf("Nacos 请求配置无效")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if n.token != "" {
		req.Header.Set("Authorization", "Bearer "+n.token)
	}
	if cas != "" {
		req.Header.Set("casMd5", cas)
	}
	resp, err := n.http.Do(req)
	if err != nil {
		return nil, 0, &resourceTransportError{cause: err}
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, nacosMaxContent+1))
	if err != nil || len(b) > nacosMaxContent || !utf8.Valid(b) {
		return nil, resp.StatusCode, fmt.Errorf("Nacos 响应无效或超过 1 MiB")
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, resp.StatusCode, &resourceRemoteError{Code: "authentication_failed", Message: "Nacos 认证或配置读写权限不足", Suggestion: "核对连接认证及该命名空间的读取授权"}
	}
	if resp.StatusCode == 409 {
		return nil, resp.StatusCode, fmt.Errorf("Nacos 配置基线冲突，禁止覆盖；请重新预览审核")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.StatusCode, fmt.Errorf("Nacos 接口返回 HTTP %d，请核对服务根路径和兼容版本", resp.StatusCode)
	}
	return b, resp.StatusCode, nil
}
func (n *nacosClient) args(dataID, group string) url.Values {
	return url.Values{"dataId": {dataID}, "group": {group}, "tenant": {n.connection.Config["namespace"]}}
}
func (n *nacosClient) read(ctx context.Context, dataID, group string) (string, error) {
	b, status, err := n.request(ctx, http.MethodGet, "/v1/cs/configs", n.args(dataID, group), "")
	if status == 404 {
		return "", &resourceRemoteError{Code: "configuration_missing", Message: "目标 Nacos 配置不存在；本阶段仅修改已有配置，请先在来源平台创建", Suggestion: "核对所选环境、命名空间、Group 和 Data ID"}
	}
	if err == nil && strings.HasPrefix(strings.ToLower(strings.TrimSpace(string(b))), "<!doctype html") {
		return "", fmt.Errorf("Nacos 返回 HTML 页面，请核对服务根路径和认证")
	}
	return string(b), err
}
func nacosMD5(content string) string { h := md5.Sum([]byte(content)); return hex.EncodeToString(h[:]) }
func (n *nacosClient) publish(ctx context.Context, dataID, group, format, content, baseline string) error {
	args := n.args(dataID, group)
	args.Set("content", content)
	args.Set("type", format)
	b, _, err := n.request(ctx, http.MethodPost, "/v1/cs/configs", args, baseline)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(b)) != "true" {
		return fmt.Errorf("Nacos 未确认发布成功（可能基线冲突）；请核对远端结果，不自动重试")
	}
	return nil
}
