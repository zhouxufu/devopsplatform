package deploy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEnvironmentBuildRequiresSelectionBeforeAnySideEffect(t *testing.T) {
	for _, tool := range []string{"maven", "gradle"} {
		t.Run(tool, func(t *testing.T) {
			s, _, _ := variantFixture(t, tool, "jar")
			// A runner without execution dependencies proves rejection happens
			// before resource preparation, run creation or any remote work.
			runner := &PipelineRunner{st: s}
			for _, opts := range []PipelineRunOptions{{}, {BuildEnvironment: "  "}, {Trigger: "schedule"}, {Trigger: "webhook"}} {
				id, err := runner.StartWithOptions(s.GetService("orders"), false, opts, "alice")
				if id != 0 || err == nil || !strings.Contains(err.Error(), "请选择") && !strings.Contains(err.Error(), "请先选择") {
					t.Fatalf("missing selection accepted: id=%d err=%v", id, err)
				}
			}
			if len(s.Runs("orders", 100)) != 0 || len(s.ReleaseOrders("orders", 100)) != 0 {
				t.Fatal("invalid environment selection created execution records")
			}
		})
	}
}

func TestBuildEnvironmentOptionsAreScopedAndReadOnly(t *testing.T) {
	s, _, _ := variantFixture(t, "gradle", "jar")
	h := Routes(s, NewBuilder(s, t.TempDir(), NewLogHub()), NewLogHub(), "", NewStepLib(t.TempDir()))
	read := func() ([]buildEnvironmentOption, bool) {
		t.Helper()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/services/orders/build-environments", nil))
		var result struct {
			Environments []buildEnvironmentOption `json:"environments"`
			Required     bool                     `json:"environment_required"`
		}
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &result) != nil {
			t.Fatal(rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "app.stage") || strings.Contains(rec.Body.String(), "gradle_project_properties") {
			t.Fatal("selector leaked private build parameters")
		}
		return result.Environments, result.Required
	}
	options, required := read()
	if !required || len(options) != len(s.Environments()) {
		t.Fatal("configured application not marked environment dependent", options, required)
	}
	for _, option := range options {
		if (option.Environment == "test" || option.Environment == "prod") && option.BuildVariant != option.Environment {
			t.Fatal("contract mapped to wrong environment", option)
		}
	}
	setVariant(t, s, "prod", BuildVariant{})
	_, required = read()
	if !required {
		t.Fatal("one unconfigured destination disabled selection requirement")
	}
	setVariant(t, s, "test", BuildVariant{})
	_, required = read()
	if required {
		t.Fatal("shared artifact application unexpectedly requires environment")
	}
	if len(s.Runs("orders", 100)) != 0 || len(s.ReleaseOrders("orders", 100)) != 0 {
		t.Fatal("reading build environments created execution records")
	}
}

func TestBuildEnvironmentSelectionDoesNotReadOrChangeRuntimeVariables(t *testing.T) {
	s, _, _ := variantFixture(t, "gradle", "jar")
	target := *s.GetServiceEnvironment("orders", "test")
	target.RuntimeEnv = map[string]string{"SPRING_PROFILES_ACTIVE": "runtime-test", "RUNTIME_SECRET": "private-runtime-value"}
	if err := s.UpsertServiceEnvironment(target); err != nil {
		t.Fatal(err)
	}
	before := s.GetServiceEnvironment("orders", "test").runtimeEnvEncrypted
	for _, env := range []string{"test", "prod"} {
		resolved, err := s.environmentBuildService(s.GetService("orders"), env)
		if err != nil {
			t.Fatal(err)
		}
		if resolved.BuildVariant != env || !strings.Contains(resolved.GradleProjectProperties, "env="+env) {
			t.Fatal("build parameters did not follow selected environment")
		}
		if resolved.RuntimeEnv["SPRING_PROFILES_ACTIVE"] != "" || resolved.RuntimeEnv["RUNTIME_SECRET"] != "" {
			t.Fatal("deployment runtime variables leaked into compilation")
		}
	}
	after := s.GetServiceEnvironment("orders", "test").runtimeEnvEncrypted
	values, err := s.decryptRuntimeEnvironment(after)
	if before != after || err != nil || values["SPRING_PROFILES_ACTIVE"] != "runtime-test" || values["RUNTIME_SECRET"] != "private-runtime-value" {
		t.Fatal("selecting build environment altered runtime configuration")
	}
}
