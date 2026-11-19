// Package releasepolicy defines a portable, bounded declarative policy.
// Compilers may be installed separately; execution never runs supplied code.
package releasepolicy

import (
	"fmt"
	"strings"
)

const Capability = "policy.release.v1"

type Field struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

var Fields = []Field{{"kind", "工作项类型"}, {"product", "所属产品"}, {"project", "所属项目"}, {"status", "来源状态"}, {"resolution", "修复方案"}, {"severity", "严重程度"}, {"priority", "优先级"}, {"title", "标题"}}

type Condition struct {
	Field    string   `json:"field"`
	Operator string   `json:"operator"`
	Values   []string `json:"values"`
}
type Rule struct {
	Name       string      `json:"name"`
	Match      string      `json:"match"`
	Conditions []Condition `json:"conditions"`
	Decision   string      `json:"decision"`
}
type Policy struct {
	Version        int      `json:"version"`
	Default        string   `json:"default"`
	RequiredFields []string `json:"required_fields,omitempty"`
	Rules          []Rule   `json:"rules"`
}
type Result struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
	Rule     int    `json:"rule"`
}

func ValidDecision(d string) bool {
	return d == "unknown" || d == "independent" || d == "not_independent"
}
func ValidField(f string) bool {
	for _, v := range Fields {
		if v.Key == f {
			return true
		}
	}
	return false
}
func Validate(p Policy) error {
	if p.Version != 1 || !ValidDecision(p.Default) || len(p.Rules) > 100 || len(p.RequiredFields) > len(Fields) {
		return fmt.Errorf("规则版本、默认结果或数量无效（最多100条）")
	}
	for _, f := range p.RequiredFields {
		if !ValidField(f) {
			return fmt.Errorf("规则引用了不支持的字段")
		}
	}
	for i, r := range p.Rules {
		if strings.TrimSpace(r.Name) == "" || len(r.Name) > 160 || !ValidDecision(r.Decision) || (r.Match != "all" && r.Match != "any") || len(r.Conditions) < 1 || len(r.Conditions) > 8 {
			return fmt.Errorf("第%d条规则名称、结果或条件无效", i+1)
		}
		for _, c := range r.Conditions {
			if !ValidField(c.Field) || (c.Operator != "in" && c.Operator != "not_in" && c.Operator != "contains") || len(c.Values) < 1 || len(c.Values) > 50 || (c.Operator == "contains" && (c.Field != "title" || len(c.Values) != 1)) {
				return fmt.Errorf("第%d条规则字段或操作符无效", i+1)
			}
			for _, v := range c.Values {
				if strings.TrimSpace(v) == "" || len(v) > 256 {
					return fmt.Errorf("第%d条规则存在空值或过长条件", i+1)
				}
			}
		}
	}
	return nil
}
func Evaluate(p Policy, values map[string]string) Result {
	unknown := func(reason string) Result { return Result{Decision: "unknown", Reason: reason} }
	if err := Validate(p); err != nil {
		return unknown(err.Error())
	}
	for _, f := range p.RequiredFields {
		if values[f] == "" {
			return unknown("来源缺少字段：" + fieldLabel(f))
		}
	}
	for i, r := range p.Rules {
		yes, no, missing := false, false, false
		for _, c := range r.Conditions {
			v := values[c.Field]
			if v == "" {
				missing = true
				continue
			}
			match := false
			if c.Operator == "contains" {
				match = strings.Contains(v, c.Values[0])
			} else {
				for _, x := range c.Values {
					if v == x {
						match = true
						break
					}
				}
				if c.Operator == "not_in" {
					match = !match
				}
			}
			yes = yes || match
			no = no || !match
		}
		matched := (r.Match == "all" && !no && !missing) || (r.Match == "any" && yes)
		if matched {
			return Result{Decision: r.Decision, Reason: "命中规则：" + r.Name, Rule: i + 1}
		}
		// An earlier uncertain rule must not silently fall through to a negative default.
		if missing && ((r.Match == "all" && !no) || (r.Match == "any" && !yes)) {
			return unknown("规则“" + r.Name + "”所需来源字段不完整")
		}
	}
	return Result{Decision: p.Default, Reason: "未命中规则，使用默认结果"}
}
func fieldLabel(f string) string {
	for _, v := range Fields {
		if v.Key == f {
			return v.Label
		}
	}
	return f
}
