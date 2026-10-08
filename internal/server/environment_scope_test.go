package server

import (
	"regexp"
	"testing"
)

func TestAlertRulesRequireExplicitEnvironment(t *testing.T) {
	rule := &AlertRule{Name: "errors", Service: "orders", Webhook: "http://example.test"}
	if err := validateAlertRule(rule); err == nil {
		t.Fatal("empty environment must not be accepted")
	}
	rule.Environment = "*"
	if err := validateAlertRule(rule); err != nil {
		t.Fatalf("explicit all-environment scope rejected: %v", err)
	}
}

func TestTraceRulesPreferEnvironmentScope(t *testing.T) {
	theTraceRules.mu.Lock()
	old := theTraceRules.byScope
	theTraceRules.byScope = map[string][]*regexp.Regexp{
		"prod\x00orders": {regexp.MustCompile(`prodTrace=([a-z0-9]+)`)},
		"*\x00orders":    {regexp.MustCompile(`globalTrace=([a-z0-9]+)`)},
	}
	theTraceRules.mu.Unlock()
	t.Cleanup(func() {
		theTraceRules.mu.Lock()
		theTraceRules.byScope = old
		theTraceRules.mu.Unlock()
	})

	message := "prodTrace=prod123 globalTrace=global123"
	if got := matchTraceID("prod", "orders", message); got != "prod123" {
		t.Fatalf("prod scoped rule not preferred: %q", got)
	}
	if got := matchTraceID("test", "orders", message); got != "global123" {
		t.Fatalf("global fallback not used for test: %q", got)
	}
}
