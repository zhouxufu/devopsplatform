// Package extensionclient is the private control-plane client, never a browser API.
package extensionclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	p "logplatform/pkg/extensionprotocol"
	"net"
	"net/http"
	"net/url"
	"os"
)

const ConfigEnv = "DEVOPS_EXTENSION_BROKER_FILE"

type Config struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}
type Client struct{ config Config }

func FromEnvironment() (*Client, error) {
	raw, err := os.ReadFile(os.Getenv(ConfigEnv))
	if err != nil {
		return nil, fmt.Errorf("平台插件调用通道尚未配置，请从平台启动交付服务")
	}
	var c Config
	if json.Unmarshal(raw, &c) != nil {
		return nil, fmt.Errorf("插件调用配置无效")
	}
	u, err := url.Parse(c.URL)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() || len(c.Token) < 32 {
		return nil, fmt.Errorf("插件调用通道必须是已认证的回环地址")
	}
	return &Client{config: c}, nil
}
func (c *Client) Invoke(ctx context.Context, id string, req p.Request) (*p.Response, error) {
	raw, _ := json.Marshal(struct {
		ID      string    `json:"id"`
		Request p.Request `json:"request"`
	}{id, req})
	r, err := http.NewRequestWithContext(ctx, "POST", c.config.URL+"/invoke", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Authorization", "Bearer "+c.config.Token)
	r.Header.Set("Content-Type", "application/json")
	hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := hc.Do(r)
	if err != nil {
		return nil, fmt.Errorf("文档插件调用通道不可用")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("文档插件调用被拒绝")
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil || len(b) > 2<<20 {
		return nil, fmt.Errorf("文档插件响应超限")
	}
	var out p.Response
	if json.Unmarshal(b, &out) != nil {
		return nil, fmt.Errorf("文档插件响应无效")
	}
	if out.Error != nil {
		return nil, out.Error
	}
	return &out, nil
}
