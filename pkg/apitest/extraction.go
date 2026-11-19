package apitest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Extracted values are private to one execution; explicit suite mappings may pass them to later cases. Only checks belong in reports.
type Extraction struct {
	Event  string `json:"event,omitempty"`
	Select string `json:"select,omitempty"`
	Name   string `json:"name"`
	Source string `json:"source"`
	Path   string `json:"path"`
}
type ExtractionCheck struct {
	Event   string `json:"event,omitempty"`
	Select  string `json:"select,omitempty"`
	Name    string `json:"name"`
	Source  string `json:"source"`
	Path    string `json:"path"`
	Status  string `json:"status"`
	Message string `json:"message"`
}
type ExtractionOutcome struct {
	Values map[string]any    `json:"-"`
	Checks []ExtractionCheck `json:"checks"`
}

var extractionName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
var stepVariable = regexp.MustCompile(`\{\{step\.([A-Za-z_][A-Za-z0-9_]{0,63})\}\}`)
var requestVariable = regexp.MustCompile(`\{\{(var|step|data|case)\.([A-Za-z_][A-Za-z0-9_]*)\}\}`)
var scopedVariable = regexp.MustCompile(`\{\{(step|data|case)\.([A-Za-z_][A-Za-z0-9_]{0,63})\}\}`)

func scopeKey(namespace, name string) string {
	if namespace == "data" || namespace == "case" {
		return namespace + "." + name
	}
	return name
}

var responseHeaderName = regexp.MustCompile("^[A-Za-z0-9!#$%&'*+.^_`|~-]+$")

func ValidateExtractions(extractions []Extraction, mode string) error {
	if len(extractions) > 10 {
		return fmt.Errorf("每个步骤最多提取 10 个变量")
	}
	names := map[string]bool{}
	for _, e := range extractions {
		if !extractionName.MatchString(e.Name) || names[e.Name] {
			return fmt.Errorf("提取变量名须以字母或下划线开头，且不能重复（最多 64 字符）")
		}
		names[e.Name] = true
		if err := validateEventSelector(e.Event, e.Select); err != nil {
			return err
		}
		if e.Source != "sse" && (e.Event != "" || e.Select != "") {
			return fmt.Errorf("事件选择仅用于 SSE 提取")
		}
		if len(e.Path) > 512 {
			return fmt.Errorf("提取路径过长")
		}
		switch e.Source {
		case "json", "sse":
			if e.Source == "sse" && mode != "sse" {
				return fmt.Errorf("SSE 提取仅可用于 SSE 步骤")
			}
			if e.Source == "json" && mode == "sse" {
				return fmt.Errorf("事件流请使用 SSE 最后一条事件提取")
			}
			if e.Path != "" && !strings.HasPrefix(e.Path, "/") {
				return fmt.Errorf("提取字段请使用 JSON Pointer，例如 /data/id")
			}
			for i := 0; i < len(e.Path); i++ {
				if e.Path[i] == '~' && (i+1 >= len(e.Path) || (e.Path[i+1] != '0' && e.Path[i+1] != '1')) {
					return fmt.Errorf("JSON Pointer 转义无效")
				}
				if e.Path[i] == '~' {
					i++
				}
			}
		case "header":
			if e.Path == "" || !responseHeaderName.MatchString(e.Path) {
				return fmt.Errorf("请填写有效响应头名称")
			}
		default:
			return fmt.Errorf("提取来源无效")
		}
	}
	return nil
}
func ValidateStepReferences(r Request, available map[string]bool) error {
	fields := requestFields(r)
	for _, value := range r.Headers {
		fields = append(fields, value)
	}
	for _, a := range r.Assertions {
		fields = append(fields, a.Path, a.Expected, a.Event)
	}
	for _, field := range fields {
		if strings.Contains(stepVariable.ReplaceAllString(field, ""), "{{step.") {
			return fmt.Errorf("步骤变量引用格式无效，请使用 {{step.order_id}}")
		}
		for _, match := range stepVariable.FindAllStringSubmatch(field, -1) {
			if !available[match[1]] {
				return fmt.Errorf("步骤变量 %s 尚未由前面的步骤提取", match[1])
			}
		}
	}
	return nil
}
func ScalarText(v any) (string, bool) {
	switch value := v.(type) {
	case string:
		return value, value != ""
	case json.Number:
		return value.String(), true
	case bool:
		return strconv.FormatBool(value), true
	}
	return "", false
}
func decodeScalar(raw, path string) (any, string) {
	var body any
	d := json.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	if !json.Valid([]byte(raw)) || d.Decode(&body) != nil {
		return nil, "响应不是有效 JSON"
	}
	value, ok := Pointer(body, path)
	if !ok {
		return nil, "字段不存在"
	}
	text, ok := ScalarText(value)
	if !ok {
		return nil, "字段须为非空字符串、数字或布尔值"
	}
	if len(text) > 8192 {
		return nil, "变量超过 8 KiB"
	}
	return value, ""
}
func extractionValue(e Extraction, r Result) (any, string) {
	switch e.Source {
	case "header":
		for k, v := range r.Headers {
			if strings.EqualFold(k, e.Path) {
				if v == "" {
					return nil, "响应头为空"
				}
				if len(v) > 8192 {
					return nil, "变量超过 8 KiB"
				}
				return v, ""
			}
		}
		return nil, "响应头不存在"
	case "json":
		if r.Truncated {
			return nil, "响应已截断，不能提取"
		}
		return decodeScalar(r.Body, e.Path)
	case "sse":
		if len(r.Events) == 0 {
			return nil, "没有收到 SSE 事件"
		}
		event, ok := SelectEvent(r.Events, e.Event, e.Select)
		if !ok {
			return nil, "没有匹配的 SSE 事件"
		}
		return decodeScalar(event.Data, e.Path)
	}
	return nil, "提取来源无效"
}
func skippedExtractions(extractions []Extraction) ExtractionOutcome {
	out := ExtractionOutcome{Checks: []ExtractionCheck{}, Values: map[string]any{}}
	for _, e := range extractions {
		out.Checks = append(out.Checks, ExtractionCheck{Event: e.Event, Select: e.Select, Name: e.Name, Source: e.Source, Path: e.Path, Status: "skipped", Message: "本步骤尚未通过，未提取"})
	}
	return out
}
func responsePassed(r Result) bool {
	if r.Error != "" || r.OutcomeUnknown || r.Status < 200 || r.Status >= 400 {
		return false
	}
	for _, c := range r.Checks {
		if !c.Passed {
			return false
		}
	}
	return true
}
func extractResponse(extractions []Extraction, r Result) (ExtractionOutcome, []string) {
	out := skippedExtractions(extractions)
	secrets := []string{}
	failed := false
	for i, e := range extractions {
		value, message := extractionValue(e, r)
		if message != "" {
			out.Checks[i].Status = "failed"
			out.Checks[i].Message = message
			failed = true
			continue
		}
		out.Values[e.Name] = value
		out.Checks[i].Status = "success"
		out.Checks[i].Message = "提取成功，值已脱敏"
		text, _ := ScalarText(value)
		secrets = append(secrets, text)
	}
	if failed {
		out.Values = map[string]any{}
		for i := range out.Checks {
			if out.Checks[i].Status == "success" {
				out.Checks[i].Message = "已提取，因本步其他提取失败而未传递"
			}
		}
	} // Publish a step's variables atomically.
	return out, secrets
}
func extractionProgressSecrets(extractions []Extraction, r Result) []string {
	secrets := []string{}
	for _, e := range extractions {
		if e.Source == "json" {
			continue
		}
		if value, err := extractionValue(e, r); err == "" {
			text, _ := ScalarText(value)
			secrets = append(secrets, text)
		}
	}
	return secrets
}

// Empty text and null are valid test data (for example validation-error cases).
// Response extraction keeps its stricter non-empty scalar contract.
func scopedText(namespace string, value any) (string, bool) {
	if namespace == "data" {
		if value == nil {
			return "", true
		}
		if text, ok := value.(string); ok {
			return text, true
		}
	}
	return ScalarText(value)
}

func expandRequestText(s string, env map[string]string, scope map[string]any) (string, error) {
	var failure error
	out := requestVariable.ReplaceAllStringFunc(s, func(template string) string {
		match := requestVariable.FindStringSubmatch(template)
		if match[1] == "var" {
			if v, ok := env[match[2]]; ok {
				return v
			}
			failure = fmt.Errorf("未绑定变量 %s", match[2])
			return ""
		}
		v, ok := scope[scopeKey(match[1], match[2])]
		text, scalar := scopedText(match[1], v)
		if !ok || !scalar {
			failure = fmt.Errorf("步骤变量 %s 未提取或不是有效标量", match[2])
			return ""
		}
		return text
	})
	return out, failure
}
func bindRequestBody(body string, env map[string]string, scope map[string]any) (string, error) {
	if !json.Valid([]byte(body)) {
		return expandRequestText(body, env, scope)
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.UseNumber()
	_ = decoder.Decode(&value)
	var failure error
	var walk func(any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case string:
			if match := scopedVariable.FindStringSubmatch(x); len(match) > 0 && match[0] == x {
				v, ok := scope[scopeKey(match[1], match[2])]
				_, scalar := scopedText(match[1], v)
				if !ok || !scalar {
					failure = fmt.Errorf("变量 %s.%s 未提供", match[1], match[2])
					return nil
				}
				return v
			}
			text, e := expandRequestText(x, env, scope)
			if e != nil {
				failure = e
			}
			return text
		case []any:
			for i := range x {
				x[i] = walk(x[i])
			}
		case map[string]any:
			for k, v := range x {
				if strings.Contains(k, "{{step.") || strings.Contains(k, "{{data.") || strings.Contains(k, "{{case.") {
					failure = fmt.Errorf("步骤变量仅支持 JSON 值，不支持 JSON 字段名")
				}
				x[k] = walk(v)
			}
		}
		return v
	}
	raw, _ := json.Marshal(walk(value))
	return string(raw), failure
}
func BindStepRequest(r Request, env map[string]string, scope map[string]any) (Request, error) {
	out := r
	out.Headers = map[string]string{}
	out.Assertions = append([]Assertion(nil), r.Assertions...)
	out.Form = append([]FormField(nil), r.Form...)
	available := map[string]bool{}
	for key := range scope {
		available[key] = true
	}
	if e := ValidateDataReferences(r, scope); e != nil {
		return out, e
	}
	if e := ValidateCaseReferences(r, available, true); e != nil {
		return out, e
	}
	if e := ValidateStepReferences(r, available); e != nil {
		return out, e
	}
	// Encode each substituted URL component; response data cannot introduce a host,
	// extra query parameters, fragments, or another template-expansion pass.
	parts := strings.SplitN(r.Path, "?", 2)
	var failure error
	for i := range parts {
		parts[i] = scopedVariable.ReplaceAllStringFunc(parts[i], func(t string) string {
			match := scopedVariable.FindStringSubmatch(t)
			key := scopeKey(match[1], match[2])
			value, ok := scopedText(match[1], scope[key])
			if !ok {
				failure = fmt.Errorf("步骤变量 %s 无效", key)
				return ""
			}
			if i == 0 {
				return url.PathEscape(value)
			}
			return url.QueryEscape(value)
		})
	}
	out.Path = strings.Join(parts, "?")
	if failure != nil {
		return out, failure
	}
	for key, value := range r.Headers {
		bound, e := expandRequestText(value, env, scope)
		if e != nil {
			return out, e
		}
		out.Headers[http.CanonicalHeaderKey(key)] = bound
	}
	var e error
	for i, field := range out.Form {
		out.Form[i].Value, e = expandRequestText(field.Value, env, scope)
		if e != nil {
			return out, e
		}
	}
	out.Body, e = bindRequestBody(r.Body, env, scope)
	if e != nil {
		return out, e
	}
	for i, a := range out.Assertions {
		out.Assertions[i].Event, e = expandRequestText(a.Event, env, scope)
		if e != nil {
			return out, e
		}
		out.Assertions[i].Path, e = expandRequestText(a.Path, env, scope)
		if e != nil {
			return out, e
		}
		if match := scopedVariable.FindStringSubmatch(a.Expected); (a.Type == "json" || a.Type == "sse_json") && len(match) > 0 && match[0] == a.Expected {
			raw, _ := json.Marshal(scope[scopeKey(match[1], match[2])])
			out.Assertions[i].Expected = string(raw)
			continue
		}
		out.Assertions[i].Expected, e = expandRequestText(a.Expected, env, scope)
		if e != nil {
			return out, e
		}
	}
	return out, nil
}

// Data values occupy a separate namespace; they cannot collide with extracted variables.
func ValidateDataReferences(r Request, scope map[string]any) error {
	fields := requestFields(r)
	for _, v := range r.Headers {
		fields = append(fields, v)
	}
	for _, a := range r.Assertions {
		fields = append(fields, a.Path, a.Expected, a.Event)
	}
	for _, field := range fields {
		if strings.Contains(scopedVariable.ReplaceAllString(field, ""), "{{data.") {
			return fmt.Errorf("数据变量格式无效，请使用 {{data.order_id}}")
		}
		for _, match := range scopedVariable.FindAllStringSubmatch(field, -1) {
			if match[1] == "data" {
				if _, ok := scope["data."+match[2]]; !ok {
					return fmt.Errorf("数据列 %s 未提供", match[2])
				}
			}
		}
	}
	return nil
}
