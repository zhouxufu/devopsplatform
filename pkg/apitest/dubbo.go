package apitest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"logplatform/pkg/apidoc"
	"logplatform/pkg/dubborpc"
	"strings"
	"time"
)

func validateDubboRequest(r *Request) error {
	m := r.RPC
	if m == nil || !apidoc.JavaName.MatchString(m.Interface) || !strings.Contains(m.Interface, ".") || len(m.Interface) > 512 || !apidoc.JavaName.MatchString(m.Method) || strings.Contains(m.Method, ".") || len(m.Method) > 128 || len(m.Group) > 191 || len(m.Version) > 191 || strings.ContainsAny(m.Group+m.Version, "\x00\r\n*/?&") || len(m.ParameterTypes) > 32 {
		return fmt.Errorf("请填写有效 Dubbo 接口、方法、参数类型、Group 和 Version")
	}
	for _, t := range m.ParameterTypes {
		if !apidoc.ValidJavaType(t) || t == "void" {
			return fmt.Errorf("Dubbo 参数 Java 类型无效")
		}
	}
	if r.Timeout == 0 {
		r.Timeout = 30
	}
	if r.Timeout < 1 || r.Timeout > 120 || len(r.Body) > 256<<10 || len(r.Headers) > 64 || len(r.Assertions) > 30 {
		return fmt.Errorf("Dubbo 请求大小、超时或断言超限")
	}
	if r.BodyType != "" && r.BodyType != "json" || len(r.Form) > 0 || r.File != nil || r.AuthenticationOnly {
		return fmt.Errorf("Dubbo 仅支持有序 JSON 参数，不支持 HTTP 表单或登录验证")
	}
	var args []json.RawMessage
	if json.Unmarshal([]byte(r.Body), &args) != nil || args == nil || len(args) != len(m.ParameterTypes) {
		return fmt.Errorf("Dubbo 参数须为 JSON 数组，数量与方法签名一致")
	}
	for k, v := range r.Headers {
		if !responseHeaderName.MatchString(k) || len(k) > 100 || len(v) > 8192 || strings.ContainsAny(v, "\r\n\x00") {
			return fmt.Errorf("Dubbo 附加参数无效")
		}
		switch strings.ToLower(k) {
		case "path", "interface", "group", "version", "timeout", "retries", "cluster", "generic", "serialization", "method", "dubbo", "application", "remote.application":
			return fmt.Errorf("附加参数不能覆盖 Dubbo 调用身份或策略：%s", k)
		}
	}
	for _, a := range r.Assertions {
		if len(a.Path) > 512 || len(a.Expected) > 4096 {
			return fmt.Errorf("断言过长")
		}
		if a.Type == "rpc_success" {
			if a.Expected != "true" && a.Expected != "false" {
				return fmt.Errorf("RPC 成功断言使用 true 或 false")
			}
			continue
		}
		if a.Type == "exception" {
			continue
		}
		switch a.Type {
		case "json", "contains", "regex", "json_regex", "json_range", "duration", "schema":
		default:
			return fmt.Errorf("Dubbo 不支持 HTTP 状态码、响应头或 SSE 断言")
		}
		if a.Type == "json" && a.Path != "" && !strings.HasPrefix(a.Path, "/") {
			return fmt.Errorf("JSON 字段请使用 /data/id 形式")
		}
		if e := validateExtendedAssertion(a, "dubbo"); e != nil {
			return e
		}
	}
	r.Method = "DUBBO"
	r.Path = m.Interface + "#" + m.Method + "(" + strings.Join(m.ParameterTypes, ",") + ")"
	return nil
}
func performDubbo(ctx context.Context, base string, r Request, a Auth, vars map[string]string, scope map[string]any, extractions []Extraction, capture func(Result) []string) Result {
	start := time.Now()
	trace := make([]byte, 16)
	_, _ = rand.Read(trace)
	out := Result{Protocol: "dubbo", Phase: "validation", TraceID: hex.EncodeToString(trace), URL: base, Headers: map[string]string{}, Events: []Event{}, Checks: []Check{}}
	secrets := []string{}
	for _, v := range vars {
		if v != "" {
			secrets = append(secrets, v)
		}
	}
	for _, v := range scope {
		if text, ok := ScalarText(v); ok {
			secrets = append(secrets, text)
		}
	}
	finish := func() Result {
		out.DurationMS = time.Since(start).Milliseconds()
		out.Body = Redact(out.Body, secrets)
		out.Error = redactText(out.Error, secrets)
		out.ExceptionMessage = redactText(out.ExceptionMessage, secrets)
		out.ExceptionType = redactText(out.ExceptionType, secrets)
		for i := range out.Checks {
			out.Checks[i].Actual = checkDetail(out.Checks[i].Actual, secrets)
			out.Checks[i].Expected = checkDetail(out.Checks[i].Expected, secrets)
		}
		return out
	}
	bound, e := BindStepRequest(r, vars, scope)
	if e != nil {
		out.Error = e.Error()
		return finish()
	}
	r = bound
	secrets = append(secrets, requestBodySecrets(r)...)
	for _, value := range r.Headers {
		if value != "" {
			secrets = append(secrets, value)
		}
	}
	if e = validateDubboRequest(&r); e != nil {
		out.Error = e.Error()
		return finish()
	}
	if e = ValidateExtractions(extractions, "dubbo"); e != nil {
		out.Error = e.Error()
		return finish()
	}
	if a.Mode != "" && a.Mode != "none" {
		out.Error = "Dubbo 认证通过附加参数引用环境变量，不复用 HTTP 登录认证"
		return finish()
	}
	endpoint, e := dubborpc.Resolve(ctx, base)
	if e != nil {
		out.Error = e.Error()
		return finish()
	}
	out.URL = endpoint
	out.Phase = "request"
	reply, e := dubborpc.Execute(ctx, dubborpc.Input{Action: "invoke", RPC: *r.RPC, Endpoint: endpoint, Arguments: json.RawMessage(r.Body), Attachments: r.Headers, TimeoutMS: r.Timeout * 1000})
	out.OutcomeUnknown = reply.OutcomeUnknown
	if e != nil {
		out.Error = e.Error()
		return finish()
	}
	out.Phase = "complete"
	out.RPCSuccess = reply.Success
	out.ExceptionType = reply.ExceptionType
	out.ExceptionMessage = reply.ExceptionMessage
	out.Body = string(reply.Body)
	if len(out.Body) > MaxBody {
		out.Truncated = true
		out.Body = out.Body[:MaxBody]
	}
	out.DurationMS = time.Since(start).Milliseconds()
	out.Checks = Evaluate(r.Assertions, out)
	if !reply.Success {
		expected := false
		for _, c := range out.Checks {
			if c.Passed && (c.Type == "exception" || c.Type == "rpc_success") {
				expected = true
			}
		}
		if !expected {
			out.Error = "Dubbo 服务返回异常：" + reply.ExceptionType
		}
	}
	secrets = append(secrets, capture(out)...)
	return finish()
}
