package apitest

import (
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

func validateEventSelector(event, selection string) error {
	if len(event) > 200 || strings.ContainsAny(event, "\r\n\x00") {
		return fmt.Errorf("SSE 事件类型无效")
	}
	if strings.HasPrefix(selection, "index:") {
		n, err := strconv.Atoi(strings.TrimPrefix(selection, "index:"))
		if err == nil && n >= 0 && n < 100 {
			return nil
		}
	}
	if selection != "" && selection != "first" && selection != "last" {
		return fmt.Errorf("请选择第一条或最后一条匹配事件")
	}
	return nil
}
func SelectEvent(events []Event, event, selection string) (Event, bool) {
	if strings.HasPrefix(selection, "index:") {
		n, err := strconv.Atoi(strings.TrimPrefix(selection, "index:"))
		if err != nil || n < 0 {
			return Event{}, false
		}
		count := 0
		for _, v := range events {
			kind := v.Event
			if kind == "" {
				kind = "message"
			}
			if event == "" || kind == event {
				if count == n {
					return v, true
				}
				count++
			}
		}
		return Event{}, false
	}
	for i := 0; i < len(events); i++ {
		index := i
		if selection != "first" {
			index = len(events) - 1 - i
		}
		v := events[index]
		kind := v.Event
		if kind == "" {
			kind = "message"
		}
		if event == "" || kind == event {
			return v, true
		}
	}
	return Event{}, false
}
func compileSchema(raw string) (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	c.LoadURL = func(string) (io.ReadCloser, error) {
		return nil, fmt.Errorf("仅支持内联 Schema 和文档内引用，不能加载外部资源")
	}
	if err := c.AddResource("https://schema.invalid/response.json", strings.NewReader(raw)); err != nil {
		return nil, err
	}
	return c.Compile("https://schema.invalid/response.json")
}
func number(s string) (*big.Rat, bool) { v := new(big.Rat); _, ok := v.SetString(s); return v, ok }
func rangeBounds(s string) (*big.Rat, *big.Rat, bool) {
	var v []json.Number
	if json.Unmarshal([]byte(s), &v) != nil || len(v) != 2 {
		return nil, nil, false
	}
	low, a := number(v[0].String())
	high, b := number(v[1].String())
	return low, high, a && b && low.Cmp(high) <= 0
}
func validateExtendedAssertion(a Assertion, mode string) error {
	if err := validateEventSelector(a.Event, a.Select); err != nil {
		return err
	}
	if a.Type != "sse_json" && a.Type != "sse_contains" && (a.Event != "" || a.Select != "") {
		return fmt.Errorf("事件选择仅用于 SSE 断言")
	}
	dynamic := requestVariable.MatchString(a.Expected)
	switch a.Type {
	case "regex", "json_regex":
		if !dynamic {
			if _, err := regexp.Compile(a.Expected); err != nil {
				return fmt.Errorf("正则表达式无效（使用 RE2 语法）")
			}
		}
	case "json_range":
		if !dynamic {
			if _, _, ok := rangeBounds(a.Expected); !ok {
				return fmt.Errorf("数值范围应为 [最小值,最大值]")
			}
		}
	case "duration":
		if !dynamic {
			n, ok := number(a.Expected)
			if !ok || n.Sign() < 0 || n.Cmp(big.NewRat(120000, 1)) > 0 {
				return fmt.Errorf("响应耗时上限为 0–120000 毫秒")
			}
		}
	case "schema":
		if dynamic {
			return fmt.Errorf("Schema 不支持变量替换，请填写完整 JSON Schema")
		}
		if _, err := compileSchema(a.Expected); err != nil {
			return fmt.Errorf("JSON Schema 无效或引用了外部资源")
		}
	case "sse_json", "sse_contains":
		if mode != "sse" {
			return fmt.Errorf("SSE 断言仅可用于事件流请求")
		}
	}
	if (a.Type == "json_regex" || a.Type == "json_range" || a.Type == "schema" || a.Type == "sse_json") && a.Path != "" && !strings.HasPrefix(a.Path, "/") {
		return fmt.Errorf("字段请使用 JSON Pointer，例如 /data/id")
	}
	return nil
}
func responseValue(body, path string) (any, bool) {
	var value any
	d := json.NewDecoder(strings.NewReader(body))
	d.UseNumber()
	if !json.Valid([]byte(body)) || d.Decode(&value) != nil {
		return nil, false
	}
	return Pointer(value, path)
}
func evaluateExtended(a Assertion, r Result) (bool, string) {
	if a.Type == "duration" {
		n, ok := number(a.Expected)
		return ok && big.NewRat(r.DurationMS, 1).Cmp(n) <= 0, fmt.Sprint(r.DurationMS, " ms")
	}
	if a.Type == "sse_json" || a.Type == "sse_contains" {
		event, ok := SelectEvent(r.Events, a.Event, a.Select)
		if !ok {
			return false, "没有匹配的 SSE 事件"
		}
		if a.Type == "sse_contains" {
			return strings.Contains(event.Data, a.Expected), event.Data
		}
		checks := Evaluate([]Assertion{{Type: "json", Path: a.Path, Expected: a.Expected}}, Result{Body: event.Data})
		return checks[0].Passed, checks[0].Actual
	}
	if r.Truncated {
		return false, "响应已截断，无法完整核对"
	}
	value, ok := responseValue(r.Body, a.Path)
	switch a.Type {
	case "regex":
		re, err := regexp.Compile(a.Expected)
		return err == nil && re.MatchString(r.Body), "响应正文正则匹配"
	case "json_regex":
		if !ok {
			return false, "JSON 字段不存在或响应无效"
		}
		text, isString := value.(string)
		if !isString {
			return false, "JSON 字段不是字符串"
		}
		re, err := regexp.Compile(a.Expected)
		return err == nil && re.MatchString(text), text
	case "json_range":
		if !ok {
			return false, "JSON 字段不存在或响应无效"
		}
		v, isNumber := value.(json.Number)
		if !isNumber {
			return false, "JSON 字段不是数字"
		}
		n, valid := number(v.String())
		low, high, bounds := rangeBounds(a.Expected)
		return valid && bounds && n.Cmp(low) >= 0 && n.Cmp(high) <= 0, v.String()
	case "schema":
		if !ok {
			return false, "JSON 字段不存在或响应无效"
		}
		schema, err := compileSchema(a.Expected)
		if err != nil {
			return false, "JSON Schema 无效"
		}
		if err = schema.Validate(value); err != nil {
			return false, "JSON Schema 校验未通过"
		}
		return true, "JSON Schema 校验通过"
	}
	return false, "断言类型无效"
}
