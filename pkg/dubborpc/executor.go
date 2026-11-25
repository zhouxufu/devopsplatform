// Package dubborpc isolates the Java SDK from the platform and transports no
// credentials through arguments, logs or persisted reports.
package dubborpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Method struct {
	Interface      string   `json:"interface"`
	Method         string   `json:"method"`
	Group          string   `json:"group,omitempty"`
	Version        string   `json:"version,omitempty"`
	ParameterTypes []string `json:"parameter_types"`
}
type Registry struct {
	Address   string `json:"address"`
	Namespace string `json:"namespace"`
	Group     string `json:"group"`
	Username  string `json:"username"`
	Password  string `json:"password"`
}
type Input struct {
	Action      string            `json:"action"`
	RPC         Method            `json:"rpc"`
	Endpoint    string            `json:"endpoint,omitempty"`
	Registry    *Registry         `json:"registry,omitempty"`
	Arguments   json.RawMessage   `json:"arguments,omitempty"`
	Attachments map[string]string `json:"attachments,omitempty"`
	TimeoutMS   int               `json:"timeout_ms"`
}
type Output struct {
	Providers        []string        `json:"providers"`
	Body             json.RawMessage `json:"body"`
	Success          bool            `json:"success"`
	ExceptionType    string          `json:"exception_type"`
	ExceptionMessage string          `json:"exception_message"`
	Error            string          `json:"error"`
	OutcomeUnknown   bool            `json:"outcome_unknown"`
}

var slots = make(chan struct{}, 4)

func Available() bool {
	p := os.Getenv("DEVOPS_DUBBO_EXECUTOR_JAR")
	s, e := os.Stat(p)
	return filepath.IsAbs(p) && e == nil && s.Mode().IsRegular()
}
func Endpoint(raw string) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Hostname() == "" || (u.Scheme != "dubbo" && u.Scheme != "tri") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("Dubbo 地址须为 dubbo://主机:端口 或 tri://主机:端口，不含参数或凭据")
	}
	port, e := strconv.Atoi(u.Port())
	if e != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("Dubbo 端口无效")
	}
	return u, nil
}

// Resolve pins DNS before handing an endpoint to the SDK. Loopback/metadata
// addresses cannot be introduced through provider metadata or user input.
func Resolve(ctx context.Context, raw string) (string, error) {
	u, e := Endpoint(raw)
	if e != nil {
		return "", e
	}
	ips, e := net.DefaultResolver.LookupIP(ctx, "ip", u.Hostname())
	if e != nil || len(ips) == 0 {
		return "", fmt.Errorf("Dubbo 提供者地址无法解析")
	}
	for _, ip := range ips {
		if !ip.IsGlobalUnicast() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.Equal(net.ParseIP("100.100.100.200")) {
			return "", fmt.Errorf("Dubbo 提供者地址不允许访问")
		}
	}
	return u.Scheme + "://" + net.JoinHostPort(ips[0].String(), u.Port()), nil
}

type boundedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, fmt.Errorf("执行器响应超过限制")
	}
	return b.Buffer.Write(p)
}
func Execute(ctx context.Context, in Input) (Output, error) {
	var out Output
	if !Available() {
		return out, fmt.Errorf("Dubbo 执行器未安装，请配置 DEVOPS_DUBBO_EXECUTOR_JAR")
	}
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	case <-ctx.Done():
		return out, ctx.Err()
	}
	if in.TimeoutMS < 1000 || in.TimeoutMS > 120000 {
		return out, fmt.Errorf("Dubbo 超时无效")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(in.TimeoutMS+5000)*time.Millisecond)
	defer cancel()
	dir, e := os.MkdirTemp("", "devops-dubbo-")
	if e != nil {
		return out, e
	}
	defer os.RemoveAll(dir)
	java := os.Getenv("DEVOPS_DUBBO_JAVA")
	if java == "" {
		java = "java"
	}
	cmd := exec.CommandContext(ctx, java, "-Xms32m", "-Xmx256m", "-Duser.home="+dir, "-Djava.io.tmpdir="+dir, "-Dorg.slf4j.simpleLogger.defaultLogLevel=error", "-jar", os.Getenv("DEVOPS_DUBBO_EXECUTOR_JAR"))
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "LANG=C.UTF-8"}
	raw, e := json.Marshal(in)
	if e != nil {
		return out, e
	}
	cmd.Stdin = bytes.NewReader(raw)
	buf := &boundedBuffer{limit: 2 << 20}
	cmd.Stdout = buf
	cmd.Stderr = io.Discard
	// Once a worker starts invoking, a lost reply is conservatively unknown.
	if e = cmd.Start(); e != nil {
		return out, fmt.Errorf("Dubbo 执行器无法启动，请核对 Java 路径")
	}
	if e = cmd.Wait(); e != nil {
		out.OutcomeUnknown = in.Action == "invoke"
		return out, fmt.Errorf("Dubbo 执行器超时或中断；请核对原请求，未自动重试")
	}
	if e = json.Unmarshal(buf.Bytes(), &out); e != nil {
		out.OutcomeUnknown = in.Action == "invoke"
		return out, fmt.Errorf("Dubbo 执行器响应无效")
	}
	if strings.TrimSpace(out.Error) != "" {
		out.OutcomeUnknown = in.Action == "invoke"
		return out, fmt.Errorf("%s", out.Error)
	}
	return out, nil
}
