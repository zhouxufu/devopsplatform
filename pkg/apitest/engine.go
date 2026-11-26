// Package apitest executes bounded HTTP/SSE requests without scripts, retries or redirects.
package apitest

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"logplatform/pkg/dubborpc"
	"mime"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const MaxBody = 1 << 20

type Assertion struct {
	Type     string `json:"type"`
	Event    string `json:"event,omitempty"`
	Select   string `json:"select,omitempty"`
	Path     string `json:"path,omitempty"`
	Expected string `json:"expected"`
}
type Check struct {
	Event           string `json:"event,omitempty"`
	Select          string `json:"select,omitempty"`
	Type            string `json:"type"`
	Path            string `json:"path,omitempty"`
	Passed          bool   `json:"passed"`
	Message         string `json:"message"`
	Expected        string `json:"expected,omitempty"`
	Actual          string `json:"actual,omitempty"`
	DetailAvailable bool   `json:"detail_available,omitempty"`
}
type Request struct {
	RPC                *dubborpc.Method  `json:"rpc,omitempty"`
	AuthenticationOnly bool              `json:"-"`
	BodyType           string            `json:"body_type,omitempty"`
	Form               []FormField       `json:"form,omitempty"`
	File               *FileContent      `json:"file,omitempty"`
	Method             string            `json:"method"`
	Path               string            `json:"path"`
	Headers            map[string]string `json:"headers"`
	Body               string            `json:"body"`
	Mode               string            `json:"mode"`
	Timeout            int               `json:"timeout_seconds"`
	MaxEvents          int               `json:"max_events"`
	Assertions         []Assertion       `json:"assertions"`
}
type Auth struct {
	BodyType        string            `json:"body_type,omitempty"`
	Injection       string            `json:"injection,omitempty"`
	Name            string            `json:"name,omitempty"`
	ExpiresIn       int               `json:"expires_in_seconds,omitempty"`
	Refresh         string            `json:"refresh_mode,omitempty"`
	IdentityPointer string            `json:"identity_pointer,omitempty"`
	Mode            string            `json:"mode"`
	BaseURL         string            `json:"base_url,omitempty"`
	Path            string            `json:"path,omitempty"`
	Body            string            `json:"body,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
	TokenPointer    string            `json:"token_pointer,omitempty"`
	TokenVariable   string            `json:"token_variable,omitempty"`
	Header          string            `json:"header,omitempty"`
	Prefix          string            `json:"prefix,omitempty"`
}
type Event struct {
	ID    string `json:"id,omitempty"`
	Event string `json:"event,omitempty"`
	Data  string `json:"data"`
}
type AuthenticationResult struct {
	Mode             string `json:"mode"`
	LoginURL         string `json:"login_url,omitempty"`
	Injection        string `json:"injection"`
	Name             string `json:"name"`
	Identity         string `json:"identity,omitempty"`
	IdentityVerified bool   `json:"identity_verified"`
	ExpiresIn        int    `json:"expires_in_seconds,omitempty"`
	Refresh          string `json:"refresh_mode"`
}
type Result struct {
	Protocol         string                `json:"protocol,omitempty"`
	RPCSuccess       bool                  `json:"rpc_success,omitempty"`
	ExceptionType    string                `json:"exception_type,omitempty"`
	ExceptionMessage string                `json:"exception_message,omitempty"`
	Authentication   *AuthenticationResult `json:"authentication,omitempty"`
	Phase            string                `json:"phase"`
	Status           int                   `json:"status"`
	Headers          map[string]string     `json:"headers"`
	Body             string                `json:"body,omitempty"`
	Events           []Event               `json:"events"`
	Checks           []Check               `json:"checks"`
	DurationMS       int64                 `json:"duration_ms"`
	TraceID          string                `json:"trace_id"`
	URL              string                `json:"url"`
	StopReason       string                `json:"stop_reason,omitempty"`
	Truncated        bool                  `json:"truncated"`
	Error            string                `json:"error,omitempty"`
	OutcomeUnknown   bool                  `json:"outcome_unknown"`
}

var variable = regexp.MustCompile(`\{\{var\.([A-Za-z_][A-Za-z0-9_]*)\}\}`)
var sensitive = regexp.MustCompile(`(?i)(authorization|cookie|password|passwd|secret|token|api[-_]?key)`)

const MaxCaseSteps = 20

func SecretKey(k string) bool { return sensitive.MatchString(k) }
func Validate(r *Request) error {
	if r.Mode == "dubbo" {
		return validateDubboRequest(r)
	}
	if r.RPC != nil {
		return fmt.Errorf("RPC 参数只能用于 Dubbo 请求")
	}
	r.Method = strings.ToUpper(strings.TrimSpace(r.Method))
	if r.Method == "" {
		r.Method = "GET"
	}
	if !strings.Contains(" GET POST PUT PATCH DELETE HEAD OPTIONS ", " "+r.Method+" ") {
		return fmt.Errorf("不支持的 HTTP 方法")
	}
	if r.Mode == "" {
		r.Mode = "http"
	}
	if r.Mode != "http" && r.Mode != "sse" {
		return fmt.Errorf("测试类型无效")
	}
	if r.Timeout == 0 {
		r.Timeout = 30
	}
	if r.Timeout < 1 || r.Timeout > 120 {
		return fmt.Errorf("超时范围为 1–120 秒")
	}
	if r.MaxEvents == 0 {
		r.MaxEvents = 20
	}
	if r.MaxEvents < 1 || r.MaxEvents > 100 {
		return fmt.Errorf("SSE 最多接收 1–100 条事件")
	}
	if len(r.Body) > 256<<10 || len(r.Headers) > 64 || len(r.Assertions) > 30 {
		return fmt.Errorf("请求正文、请求头或断言过多")
	}
	if err := validateBody(*r); err != nil {
		return err
	}
	if _, err := Join("https://target.invalid", r.Path); err != nil {
		return err
	}
	for k, v := range r.Headers {
		if err := validHeader(k, v); err != nil {
			return err
		}
	}
	for _, a := range r.Assertions {
		if len(a.Path) > 512 || len(a.Expected) > 4096 {
			return fmt.Errorf("断言过长")
		}
		if err := validateExtendedAssertion(a, r.Mode); err != nil {
			return err
		}
		switch a.Type {
		case "status":
			n, e := strconv.Atoi(a.Expected)
			if scopedVariable.FindString(a.Expected) == a.Expected && a.Expected != "" {
				break
			}
			if e != nil || n < 100 || n > 599 {
				return fmt.Errorf("状态码断言无效")
			}
		case "json":
			if a.Path != "" && !strings.HasPrefix(a.Path, "/") {
				return fmt.Errorf("JSON 字段请使用 /data/id 形式")
			}
		case "header", "contains", "regex", "json_regex", "json_range", "duration", "schema", "sse_json", "sse_contains":
		case "sse_count":
			n, e := strconv.Atoi(a.Expected)
			if scopedVariable.FindString(a.Expected) == a.Expected && a.Expected != "" {
				break
			}
			if e != nil || n < 0 || n > 100 {
				return fmt.Errorf("事件数量断言无效")
			}
		default:
			return fmt.Errorf("断言类型无效")
		}
	}
	return nil
}
func validHeader(k, v string) error {
	if len(k) > 100 || len(v) > 8192 || strings.ContainsAny(v, "\r\n\x00") || !regexp.MustCompile(`^[A-Za-z0-9!#$%&'*+.^_`+"`"+`|~-]+$`).MatchString(k) {
		return fmt.Errorf("请求头格式无效")
	}
	switch strings.ToLower(k) {
	case "host", "connection", "content-length", "transfer-encoding", "proxy-authorization", "proxy-connection", "upgrade", "trailer", "te":
		return fmt.Errorf("不允许设置传输控制请求头 %s", k)
	}
	return nil
}
func Base(raw string) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(raw, "\r\n\x00") {
		return nil, fmt.Errorf("请配置不含凭据、查询参数的 HTTP(S) 基础地址")
	}
	return u, nil
}
func Join(base, path string) (string, error) {
	u, e := Base(base)
	if e != nil {
		return "", e
	}
	p, e := url.Parse(path)
	if e != nil || len(path) > 8192 || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || p.IsAbs() || p.Host != "" || p.Fragment != "" || strings.ContainsAny(path, "\\\r\n\x00") {
		return "", fmt.Errorf("接口路径须以 / 开头，不可填写另一台主机的 URL")
	}
	for _, segment := range strings.Split(p.Path, "/") {
		if segment == ".." || segment == "." {
			return "", fmt.Errorf("接口路径不可包含相对目录")
		}
	}
	escapedBase := strings.TrimSuffix(u.EscapedPath(), "/")
	u.Path = strings.TrimSuffix(u.Path, "/") + p.Path
	u.RawPath = escapedBase + p.EscapedPath()
	u.RawQuery = p.RawQuery
	return u.String(), nil
}
func AllowedIP(ip net.IP) bool {
	return ip != nil && ip.IsGlobalUnicast() && !ip.Equal(net.ParseIP("100.100.100.200")) && !ip.IsLoopback() && !ip.IsUnspecified() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsMulticast()
}
func Client() *http.Client {
	tr := &http.Transport{Proxy: nil, DisableKeepAlives: true, MaxResponseHeaderBytes: 64 << 10, TLSHandshakeTimeout: 8 * time.Second}
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, e
		}
		ips, e := net.DefaultResolver.LookupIP(ctx, "ip", host)
		if e != nil || len(ips) == 0 {
			return nil, fmt.Errorf("目标域名解析失败")
		}
		for _, ip := range ips {
			if !AllowedIP(ip) {
				return nil, fmt.Errorf("目标解析到回环、链路本地或保留地址，已拒绝")
			}
		}
		return (&net.Dialer{Timeout: 8 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}
	return &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func Pointer(v any, path string) (any, bool) {
	if path == "" {
		return v, true
	}
	if !strings.HasPrefix(path, "/") || len(path) > 512 {
		return nil, false
	}
	for _, key := range strings.Split(path[1:], "/") {
		key = strings.ReplaceAll(strings.ReplaceAll(key, "~1", "/"), "~0", "~")
		switch x := v.(type) {
		case map[string]any:
			var ok bool
			v, ok = x[key]
			if !ok {
				return nil, false
			}
		case []any:
			i, e := strconv.Atoi(key)
			if e != nil || i < 0 || i >= len(x) {
				return nil, false
			}
			v = x[i]
		default:
			return nil, false
		}
	}
	return v, true
}
func expand(s string, vars map[string]string) (string, error) {
	var failure error
	out := variable.ReplaceAllStringFunc(s, func(s string) string {
		k := variable.FindStringSubmatch(s)[1]
		v, ok := vars[k]
		if !ok {
			failure = fmt.Errorf("未绑定变量 %s", k)
		}
		return v
	})
	return out, failure
}
func expandBody(s string, vars map[string]string) (string, error) {
	if !json.Valid([]byte(s)) {
		return expand(s, vars)
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(s))
	decoder.UseNumber()
	_ = decoder.Decode(&value)
	var failure error
	var walk func(any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case string:
			y, e := expand(x, vars)
			if e != nil {
				failure = e
			}
			return y
		case []any:
			for i := range x {
				x[i] = walk(x[i])
			}
		case map[string]any:
			for k, v := range x {
				x[k] = walk(v)
			}
		}
		return v
	}
	raw, _ := json.Marshal(walk(value))
	return string(raw), failure
}
func ValidateAuth(a Auth) error {
	if a.ExpiresIn < 0 || a.ExpiresIn > 31536000 || (a.Refresh != "" && a.Refresh != "per_request") {
		return fmt.Errorf("有效期无效；当前刷新方式为每次执行重新登录，不缓存或重放请求")
	}
	if a.Injection != "" && a.Injection != "header" && a.Injection != "query" && a.Injection != "cookie" {
		return fmt.Errorf("Token 注入位置无效")
	}
	if a.Injection == "cookie" || a.Injection == "query" {
		if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,99}$`).MatchString(a.Name) {
			return fmt.Errorf("请填写有效的 Token 参数名")
		}
	}
	if a.Mode == "login" && a.IdentityPointer != "" && a.IdentityPointer == a.TokenPointer {
		return fmt.Errorf("身份字段路径不能与 Token 提取路径相同")
	}
	if a.IdentityPointer != "" && (!strings.HasPrefix(a.IdentityPointer, "/") || len(a.IdentityPointer) > 512) {
		return fmt.Errorf("身份路径无效")
	}
	switch a.Mode {
	case "", "none":
		return nil
	case "bearer":
		if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(a.TokenVariable) {
			return fmt.Errorf("Bearer Token 请选择已绑定的变量")
		}
	case "login":
		if _, e := Join("https://target.invalid", a.Path); e != nil {
			return e
		}
		if (a.BodyType != "" && a.BodyType != "json" && a.BodyType != "form") || (a.BodyType != "form" && !json.Valid([]byte(a.Body))) || len(a.Body) > 64<<10 || !strings.HasPrefix(a.TokenPointer, "/") || len(a.TokenPointer) > 512 {
			return fmt.Errorf("登录正文应为 JSON 或表单，Token 路径如 /data/token")
		}
		if a.BaseURL != "" {
			if _, e := Base(a.BaseURL); e != nil {
				return e
			}
		}
	default:
		return fmt.Errorf("认证类型无效")
	}
	if a.Header != "" {
		if e := validHeader(a.Header, "x"); e != nil {
			return e
		}
	}
	if strings.ContainsAny(a.Prefix, "\r\n") || len(a.Prefix) > 100 {
		return fmt.Errorf("Token 前缀无效")
	}
	for k, v := range a.Headers {
		if e := validHeader(k, v); e != nil {
			return e
		}
	}
	if len(a.Headers) > 32 {
		return fmt.Errorf("登录请求头过多")
	}
	return nil
}
func Perform(ctx context.Context, client *http.Client, base string, r Request, a Auth, vars map[string]string, emit func(Event), observe ...func(Result)) Result {
	result, _ := PerformScoped(ctx, client, base, r, a, vars, nil, nil, emit, observe...)
	return result
}

// PerformScoped captures raw scalars before redaction, but returns their checks
// separately from private run-local values. No scripts or replacement re-scans.
func PerformScoped(ctx context.Context, client *http.Client, base string, r Request, a Auth, vars map[string]string, scope map[string]any, extractions []Extraction, emit func(Event), observe ...func(Result)) (Result, ExtractionOutcome) {
	outcome := skippedExtractions(extractions)
	capture := func(raw Result) []string {
		if !responsePassed(raw) {
			return nil
		}
		var secrets []string
		outcome, secrets = extractResponse(extractions, raw)
		return secrets
	}
	var result Result
	if r.Mode == "dubbo" {
		result = performDubbo(ctx, base, r, a, vars, scope, extractions, capture)
	} else {
		result = perform(ctx, client, base, r, a, vars, scope, extractions, capture, emit, observe...)
	}
	for _, check := range outcome.Checks {
		if check.Status == "failed" {
			result.Error = "响应变量提取失败：" + check.Name
			break
		}
	}
	return result, outcome
}
func perform(ctx context.Context, client *http.Client, base string, r Request, a Auth, vars map[string]string, scope map[string]any, extractions []Extraction, capture func(Result) []string, emit func(Event), observe ...func(Result)) Result {
	start := time.Now()
	rawTrace := make([]byte, 16)
	_, _ = rand.Read(rawTrace)
	out := Result{Phase: "validation", TraceID: hex.EncodeToString(rawTrace), Events: []Event{}, Checks: []Check{}, Headers: map[string]string{}}
	secrets := []string{}
	for _, v := range vars {
		if v != "" {
			secrets = append(secrets, v)
		}
	}
	for _, value := range scope {
		if text, ok := ScalarText(value); ok {
			secrets = append(secrets, text)
		}
	}
	finish := func() Result {
		out.DurationMS = time.Since(start).Milliseconds()
		out.Body = Redact(out.Body, secrets)
		out.URL = redactURL(out.URL, secrets)
		out.Error = redactText(out.Error, secrets)
		for k, v := range out.Headers {
			if SecretKey(k) {
				out.Headers[k] = "[REDACTED]"
			} else {
				out.Headers[k] = redactText(v, secrets)
			}
		}
		for i := range out.Events {
			out.Events[i].Data = Redact(out.Events[i].Data, secrets)
			out.Events[i].ID = redactText(out.Events[i].ID, secrets)
			out.Events[i].Event = redactText(out.Events[i].Event, secrets)
		}
		for i := range out.Checks {
			out.Checks[i].Event = redactText(out.Checks[i].Event, secrets)
			out.Checks[i].Path = redactText(out.Checks[i].Path, secrets)
			out.Checks[i].Expected = checkDetail(out.Checks[i].Expected, secrets)
			out.Checks[i].Actual = checkDetail(out.Checks[i].Actual, secrets)
		}
		return out
	}
	bound, e := BindStepRequest(r, vars, scope)
	if e != nil {
		out.Error = e.Error()
		return finish()
	}
	r = bound
	if e := ValidateExtractions(extractions, r.Mode); e != nil {
		out.Error = e.Error()
		return finish()
	}
	if e := Validate(&r); e != nil {
		out.Error = e.Error()
		return finish()
	}
	if e := ValidateAuth(a); e != nil {
		out.Error = e.Error()
		return finish()
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(r.Timeout)*time.Second)
	defer cancel()
	target, e := Join(base, r.Path)
	if e != nil {
		out.Error = e.Error()
		return finish()
	}
	out.URL = target
	headers := map[string]string{}
	for k, v := range r.Headers {
		headers[k] = v
		if SecretKey(k) && headers[k] != "" {
			secrets = append(secrets, headers[k])
		}
	}
	token := ""
	if a.Mode == "bearer" {
		token = vars[a.TokenVariable]
		if token == "" {
			out.Error = "Token 变量未绑定或为空"
			return finish()
		}
	}
	if a.Mode == "login" {
		out.Phase = "authentication"
		authBase := base
		if a.BaseURL != "" {
			authBase = a.BaseURL
		}
		authURL, e := Join(authBase, a.Path)
		if e != nil {
			out.Error = e.Error()
			return finish()
		}
		body, e := loginBody(a, vars)
		if e != nil {
			out.Error = e.Error()
			return finish()
		}
		secrets = append(secrets, body)
		secrets = append(secrets, bodySecrets(body)...)
		q, _ := http.NewRequestWithContext(ctx, "POST", authURL, strings.NewReader(body))
		contentType := "application/json"
		if a.BodyType == "form" {
			contentType = "application/x-www-form-urlencoded"
		}
		q.Header.Set("Content-Type", contentType)
		q.Header.Set("X-Request-ID", out.TraceID)
		q.Header.Set("traceparent", "00-"+out.TraceID+"-"+out.TraceID[16:]+"-01")
		for k, v := range a.Headers {
			v, e = expand(v, vars)
			if e != nil {
				out.Error = e.Error()
				return finish()
			}
			q.Header.Set(k, v)
			if SecretKey(k) {
				secrets = append(secrets, v)
			}
		}
		res, e := client.Do(q)
		if e != nil {
			out.Error = "登录请求未完成，不会重试"
			out.OutcomeUnknown = true
			return finish()
		}
		raw, e := io.ReadAll(io.LimitReader(res.Body, MaxBody+1))
		res.Body.Close()
		if e != nil || len(raw) > MaxBody || res.StatusCode < 200 || res.StatusCode >= 300 {
			out.OutcomeUnknown = e != nil
			out.Error = "登录未成功或响应超限，未发送业务请求"
			return finish()
		}
		var payload any
		if json.Unmarshal(raw, &payload) != nil {
			out.Error = "登录响应不是 JSON，未发送业务请求"
			return finish()
		}
		v, ok := Pointer(payload, a.TokenPointer)
		loginToken, okString := v.(string)
		token = loginToken
		if !ok || !okString || token == "" || len(token) > 8192 || strings.ContainsAny(token, "\r\n\x00") {
			out.Error = "Token 提取失败，未发送业务请求"
			return finish()
		}
		secrets = append(secrets, token)
		identity := ""
		if value, found := Pointer(payload, a.IdentityPointer); a.IdentityPointer != "" && found {
			identity, _ = ScalarText(value)
		}
		out.Authentication = &AuthenticationResult{Identity: Redact(identity, secrets), IdentityVerified: identity != "", LoginURL: redactURL(authURL, secrets)}
	}
	if a.Mode == "bearer" || a.Mode == "login" {
		if len(token) > 8192 || strings.ContainsAny(token, "\r\n\x00") {
			out.Error = "Token 格式无效"
			return finish()
		}
		secrets = append(secrets, token)
		injection := a.Injection
		if injection == "" {
			injection = "header"
		}
		name := a.Name
		if injection == "header" {
			name = a.Header
			if name == "" {
				name = "Authorization"
			}
			for k := range headers {
				if strings.EqualFold(k, name) {
					delete(headers, k)
				}
			}
			headers[name] = a.Prefix + token
		}
		if injection == "cookie" {
			for k := range headers {
				if strings.EqualFold(k, "Cookie") {
					delete(headers, k)
				}
			}
			cookie := &http.Cookie{Name: name, Value: token}
			if cookie.Valid() != nil {
				out.Error = "Token 不能作为 Cookie"
				return finish()
			}
			headers["Cookie"] = cookie.String()
		}
		if injection == "query" {
			u, _ := url.Parse(target)
			values := u.Query()
			values.Set(name, a.Prefix+token)
			u.RawQuery = values.Encode()
			target = u.String()
			out.URL = redactURL(target, secrets)
		}
		if out.Authentication == nil {
			out.Authentication = &AuthenticationResult{}
		}
		out.Authentication.Mode = a.Mode
		out.Authentication.Injection = injection
		out.Authentication.Name = name
		out.Authentication.ExpiresIn = a.ExpiresIn
		out.Authentication.Refresh = "per_request"
		if a.Mode == "bearer" {
			out.Authentication.Refresh = "bound_variable"
		}
	}
	if r.AuthenticationOnly {
		if a.Mode != "login" && a.Mode != "bearer" {
			out.Error = "此入口未配置认证方案"
			return finish()
		}
		out.URL = ""
		if out.Authentication != nil {
			out.URL = out.Authentication.LoginURL
		}
		out.Phase = "authentication"
		out.Status = 200
		return finish()
	}

	body, contentType, e := requestBody(r)
	if e != nil {
		out.Error = e.Error()
		return finish()
	}
	secrets = append(secrets, requestBodySecrets(r)...)
	q, e := http.NewRequestWithContext(ctx, r.Method, target, strings.NewReader(body))
	if e != nil {
		out.Error = "请求格式无效"
		return finish()
	}
	for k, v := range headers {
		if e = validHeader(k, v); e != nil {
			out.Error = e.Error()
			return finish()
		}
		q.Header.Set(k, v)
	}
	if contentType != "" && (r.BodyType == "multipart" || q.Header.Get("Content-Type") == "") {
		q.Header.Set("Content-Type", contentType)
	}
	if body != "" && json.Valid([]byte(body)) && q.Header.Get("Content-Type") == "" {
		q.Header.Set("Content-Type", "application/json")
	}
	q.Header.Set("X-Request-ID", out.TraceID)
	q.Header.Set("traceparent", "00-"+out.TraceID+"-"+out.TraceID[:16]+"-01")
	if r.Mode == "sse" {
		q.Header.Set("Accept", "text/event-stream")
	}
	out.Phase = "request"
	res, e := client.Do(q)
	if e != nil {
		out.Error = "请求中断、超时或连接失败；未自动重试"
		out.OutcomeUnknown = true
		return finish()
	}
	defer res.Body.Close()
	out.Phase = "response"
	out.Status = res.StatusCode
	for k, v := range res.Header {
		out.Headers[k] = strings.Join(v, ", ")
	}
	secrets = append(secrets, extractionProgressSecrets(extractions, out)...)
	metadata := Result{Phase: "response", Status: out.Status, TraceID: out.TraceID, URL: redactURL(out.URL, secrets), Headers: map[string]string{}, Events: []Event{}, Checks: []Check{}, DurationMS: time.Since(start).Milliseconds()}
	for k, v := range out.Headers {
		if SecretKey(k) {
			metadata.Headers[k] = "[REDACTED]"
		} else {
			metadata.Headers[k] = redactText(v, secrets)
		}
	}
	for _, observer := range observe {
		if observer != nil {
			observer(metadata)
		}
	}
	if r.Mode == "sse" && res.StatusCode >= 200 && res.StatusCode < 300 {
		mediaType, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
		if mediaType != "text/event-stream" {
			out.Error = "响应类型不是 text/event-stream"
			return finish()
		}
		out.StopReason, e = ReadSSE(res.Body, r.MaxEvents, func(event Event) {
			out.Events = append(out.Events, event)
			secrets = append(secrets, extractionProgressSecrets(extractions, out)...)
			if emit != nil {
				event.Data = Redact(event.Data, secrets)
				event.ID = redactText(event.ID, secrets)
				event.Event = redactText(event.Event, secrets)
				emit(event)
			}
		})
		if e != nil {
			if ctx.Err() == context.DeadlineExceeded {
				out.StopReason = "time_limit"
			} else {
				out.Error = "事件流中断或超过大小限制"
				out.OutcomeUnknown = true
			}
		}
	} else {
		raw, e := io.ReadAll(io.LimitReader(res.Body, MaxBody+1))
		if e != nil {
			out.Error = "响应读取中断"
			out.OutcomeUnknown = true
		}
		if len(raw) > MaxBody {
			raw = raw[:MaxBody]
			out.Truncated = true
		}
		out.Body = string(raw)
	}
	out.DurationMS = time.Since(start).Milliseconds()
	out.Checks = Evaluate(r.Assertions, out)
	if capture != nil {
		secrets = append(secrets, capture(out)...)
	}
	return finish()
}
func ReadSSE(reader io.Reader, max int, emit func(Event)) (string, error) {
	limited := &io.LimitedReader{R: reader, N: MaxBody + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), 64<<10)
	event := Event{}
	data := []string{}
	count := 0
	dataBytes := 0
	first := true
	for scanner.Scan() {
		line := scanner.Text()
		if first {
			line = strings.TrimPrefix(line, "\ufeff")
			first = false
		}
		if line == "" {
			if len(data) > 0 {
				event.Data = strings.Join(data, "\n")
				emit(event)
				count++
				if count >= max {
					return "event_limit", nil
				}
			}
			event = Event{ID: event.ID}
			data = nil
			dataBytes = 0
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		value := ""
		if len(parts) == 2 {
			value = strings.TrimPrefix(parts[1], " ")
		}
		switch parts[0] {
		case "data":
			data = append(data, value)
			dataBytes += len(value) + 1
		case "event":
			event.Event = value
		case "id":
			if !strings.ContainsRune(value, 0) {
				event.ID = value
			}
		}
		if dataBytes > 64<<10 {
			return "size_limit", fmt.Errorf("event too large")
		}
	}
	if scanner.Err() != nil {
		return "interrupted", scanner.Err()
	}
	if limited.N <= 0 {
		return "size_limit", fmt.Errorf("stream too large")
	}
	return "eof", nil
}
func Evaluate(assertions []Assertion, r Result) []Check {
	out := []Check{}
	for _, a := range assertions {
		pass := false
		actual := "未取得值"
		switch a.Type {
		case "regex", "json_regex", "json_range", "duration", "schema", "sse_json", "sse_contains":
			pass, actual = evaluateExtended(a, r)
		case "rpc_success":
			actual = strconv.FormatBool(r.RPCSuccess)
			pass = r.Protocol == "dubbo" && actual == a.Expected
		case "exception":
			actual = r.ExceptionType
			pass = r.Protocol == "dubbo" && actual == a.Expected
		case "status":
			actual = strconv.Itoa(r.Status)
			pass = strconv.Itoa(r.Status) == a.Expected
		case "contains":
			pass = strings.Contains(r.Body, a.Expected)
			actual = "正文不包含期望片段"
			if pass {
				actual = "正文包含期望片段"
			}
		case "header":
			actual = "响应头不存在"
			for k, v := range r.Headers {
				if strings.EqualFold(k, a.Path) {
					actual = v
					pass = v == a.Expected
				}
			}
		case "json":
			actual = "响应不是有效 JSON"
			var value, expected any
			d := json.NewDecoder(strings.NewReader(r.Body))
			d.UseNumber()
			if d.Decode(&value) == nil {
				if json.Unmarshal([]byte(a.Expected), &expected) != nil {
					expected = a.Expected
				} else {
					e := json.NewDecoder(strings.NewReader(a.Expected))
					e.UseNumber()
					_ = e.Decode(&expected)
				}
				v, ok := Pointer(value, a.Path)
				pass = ok && reflect.DeepEqual(v, expected) && !r.Truncated
				actual = "字段不存在"
				if ok {
					raw, _ := json.Marshal(v)
					actual = string(raw)
				}
			}
			if r.Truncated {
				actual = "响应已截断，无法完整核对"
			}
		case "sse_count":
			actual = strconv.Itoa(len(r.Events))
			n, _ := strconv.Atoi(a.Expected)
			pass = len(r.Events) >= n
		}
		message := "未通过"
		if pass {
			message = "通过"
		}
		expected := a.Expected
		if SecretKey(a.Path) {
			actual = "[REDACTED]"
			expected = "[REDACTED]"
		}
		out = append(out, Check{Event: a.Event, Select: a.Select, Type: a.Type, Path: a.Path, Passed: pass, Message: message, Expected: expected, Actual: actual, DetailAvailable: true})
	}
	return out
}

// Redact before bounding, so truncated secrets cannot leak into persisted reports.
func checkDetail(value string, secrets []string) string {
	value = Redact(value, secrets)
	runes := []rune(value)
	if len(runes) > 4096 {
		return string(runes[:4096]) + "…（展示已截断）"
	}
	return value
}
func redactText(s string, secrets []string) string {
	ordered := append([]string{}, secrets...)
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	for _, v := range ordered {
		if v != "" {
			s = strings.ReplaceAll(s, v, "[REDACTED]")
		}
	}
	return s
}
func Redact(s string, secrets []string) string {
	var value any
	decoder := json.NewDecoder(strings.NewReader(s))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return redactText(s, secrets)
	}
	var walk func(any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			for k, v := range x {
				if SecretKey(k) {
					x[k] = "[REDACTED]"
				} else {
					x[k] = walk(v)
				}
			}
		case []any:
			for i := range x {
				x[i] = walk(x[i])
			}
		case string:
			return redactText(x, secrets)
		case json.Number, bool:
			text, _ := ScalarText(x)
			for _, secret := range secrets {
				if secret != "" && text == secret {
					return "[REDACTED]"
				}
			}
		}
		return v
	}
	b, _ := json.MarshalIndent(walk(value), "", "  ")
	return string(b)
}
func redactURL(s string, secrets []string) string {
	u, e := url.Parse(s)
	if e != nil {
		return ""
	}
	q := u.Query()
	for k, v := range q {
		if SecretKey(k) {
			q.Set(k, "[REDACTED]")
		} else {
			for i := range v {
				v[i] = redactText(v[i], secrets)
			}
			q[k] = v
		}
	}
	u.RawQuery = q.Encode()
	maskedPath := redactText(u.Path, secrets)
	if maskedPath != u.Path {
		u.Path = maskedPath
		u.RawPath = ""
	}
	return redactText(u.String(), secrets)
}

func SafeURL(s string, values map[string]string) string {
	secrets := []string{}
	for _, v := range values {
		if v != "" {
			secrets = append(secrets, v)
		}
	}
	return redactURL(s, secrets)
}
func bodySecrets(body string) []string {
	var v any
	if json.Unmarshal([]byte(body), &v) != nil {
		return nil
	}
	out := []string{}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, v := range x {
				if SecretKey(k) {
					if s, ok := v.(string); ok && s != "" {
						out = append(out, s)
					}
				}
				walk(v)
			}
		case []any:
			for _, v := range x {
				walk(v)
			}
		}
	}
	walk(v)
	return out
}

func SanitizeRequest(r Request, variables map[string]string) Request {
	secrets := []string{}
	for _, v := range variables {
		if v != "" {
			secrets = append(secrets, v)
		}
	}
	out := r
	out.Assertions = append([]Assertion{}, r.Assertions...)
	out.Headers = map[string]string{}
	for k, v := range r.Headers {
		if SecretKey(k) {
			out.Headers[k] = "[REDACTED]"
		} else {
			out.Headers[k] = redactText(v, secrets)
		}
	}
	out.Body = Redact(r.Body, append(secrets, requestBodySecrets(r)...))
	out.File = sanitizedFile(r.File)
	out.Form = append([]FormField(nil), r.Form...)
	for i, field := range out.Form {
		out.Form[i].File = sanitizedFile(field.File)
		if SecretKey(field.Name) {
			out.Form[i].Value = "[REDACTED]"
		} else {
			out.Form[i].Value = redactText(field.Value, secrets)
		}
	}
	out.Path = redactURL(r.Path, secrets)
	if r.Mode == "dubbo" && r.RPC != nil {
		out.Path = r.RPC.Interface + "#" + r.RPC.Method + "(" + strings.Join(r.RPC.ParameterTypes, ",") + ")"
	}
	for _, template := range requestVariable.FindAllString(r.Path, -1) {
		out.Path = strings.ReplaceAll(out.Path, url.PathEscape(template), template)
	}
	for i, a := range out.Assertions {
		if SecretKey(a.Path) {
			a.Expected = "[REDACTED]"
		} else {
			a.Expected = redactText(a.Expected, secrets)
		}
		out.Assertions[i] = a
	}
	return out
}
