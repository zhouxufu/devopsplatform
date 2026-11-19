package releasepolicy

import "testing"

func TestOrderedThreeValuedRules(t *testing.T) {
	p := Policy{Version: 1, Default: "not_independent", Rules: []Rule{{Name: "生产且代码修复", Match: "all", Decision: "independent", Conditions: []Condition{{Field: "project", Operator: "in", Values: []string{"75"}}, {Field: "resolution", Operator: "in", Values: []string{"fixed2"}}}}}}
	for _, tc := range []struct {
		v    map[string]string
		want string
	}{{map[string]string{"project": "75", "resolution": "fixed2"}, "independent"}, {map[string]string{"project": "165"}, "not_independent"}, {map[string]string{"project": "75"}, "unknown"}, {map[string]string{}, "unknown"}} {
		if got := Evaluate(p, tc.v); got.Decision != tc.want {
			t.Fatal(tc, got)
		}
	}
	p.Rules[0].Match = "any"
	if got := Evaluate(p, map[string]string{"project": "75"}); got.Decision != "independent" {
		t.Fatal(got)
	}
	p.Rules[0].Conditions[0].Operator = "not_in"
	if got := Evaluate(p, map[string]string{}); got.Decision != "unknown" {
		t.Fatal("missing negation became true", got)
	}
	p.Rules = append(p.Rules, Rule{Name: "较低优先级", Match: "all", Decision: "independent", Conditions: []Condition{{Field: "kind", Operator: "in", Values: []string{"bug"}}}})
	if got := Evaluate(p, map[string]string{"kind": "bug"}); got.Decision != "unknown" {
		t.Fatal("uncertain earlier rule fell through", got)
	}
	p.Rules[0].Conditions[0].Field = "__sql__"
	if Validate(p) == nil {
		t.Fatal("unknown field accepted")
	}
}
