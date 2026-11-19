package smartdocplugin

import (
	"encoding/json"
	"logplatform/pkg/apidoc"
	p "logplatform/pkg/extensionprotocol"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSmartDocProviderPlans(t *testing.T) {
	settings := json.RawMessage(`{"plugin_version":"2.7.3","config_file":"src/main/resources/smart-doc.json","output_file":"target/docs/openapi.json"}`)
	v, err := Prepare(apidoc.PlanInput{Tool: "maven", Settings: settings})
	if err != nil || v.Provider != ID || len(v.Tasks) != 1 || v.Tasks[0] != "com.github.shalousun:smart-doc-maven-plugin:2.7.3:openapi" || v.OutputFile != "target/docs/openapi.json" {
		t.Fatalf("%+v %v", v, err)
	}
	for _, in := range []apidoc.PlanInput{{Tool: "unsupported", Settings: settings}, {Tool: "maven", Settings: json.RawMessage(`{"plugin_version":"LATEST"}`)}, {Tool: "maven", Settings: json.RawMessage(`{"plugin_version":"2.7.3-SNAPSHOT"}`)}, {Tool: "maven", Settings: json.RawMessage(`{"plugin_version":"2.7.3","config_file":"../secret"}`)}, {Tool: "maven", Settings: json.RawMessage(`{"plugin_version":"2.7.3","output_file":"src/doc.json"}`)}} {
		if _, e := Prepare(in); e == nil {
			t.Fatal(in)
		}
	}
}
func TestProviderAuthenticationAndPermission(t *testing.T) {
	h := Handler("secret")
	for _, token := range []string{"", "Bearer wrong"} {
		r := httptest.NewRequest("GET", "/v1/describe", nil)
		r.Header.Set("Authorization", token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal(w.Code)
		}
	}
	for _, test := range []struct{ role, cap, op, code string }{{"member", apidoc.Capability, "plan", "permission_denied"}, {"admin", "logs.search.v1", "plan", "unsupported"}, {"admin", apidoc.Capability, "execute", "unsupported"}, {"admin", apidoc.Capability, "plan", ""}} {
		in, _ := json.Marshal(apidoc.PlanInput{Tool: "maven", Settings: json.RawMessage(`{"plugin_version":"2.7.3"}`)})
		req, _ := json.Marshal(p.Request{Capability: test.cap, Operation: test.op, Actor: p.Actor{User: "fixture", Role: test.role}, Input: in})
		r := httptest.NewRequest("POST", "/v1/invoke", strings.NewReader(string(req)))
		r.Header.Set("Authorization", "Bearer secret")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var out p.Response
		if json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatal(w.Body.String())
		}
		if test.code == "" {
			if out.Error != nil || len(out.Data) == 0 {
				t.Fatal(w.Body.String())
			}
		} else if out.Error == nil || out.Error.Code != test.code {
			t.Fatal(w.Body.String())
		}
	}
}

func TestGradlePlanUsesProjectBuildAndEnvironmentConfig(t *testing.T) {
	plan, err := Prepare(apidoc.PlanInput{Tool: "gradle", Settings: json.RawMessage(`{"config_file":"src/main/resources/smart-doc-test.json"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Tool != "gradle" || plan.Tasks[0] != "smartDocOpenApi" || plan.OutputFile != "build/api-docs/openapi.json" || plan.Properties["org.gradle.project.smartdoc.configFile"] != "./src/main/resources/smart-doc-test.json" || plan.ProviderVersion != Version {
		t.Fatalf("%+v", plan)
	}
	plan, err = Prepare(apidoc.PlanInput{Tool: "gradle", Settings: json.RawMessage(`{"gradle_task":"orders:smartDocOpenApi","output_file":"build/docs/openapi.json"}`)})
	if err != nil || plan.Tasks[0] != "orders:smartDocOpenApi" {
		t.Fatal(plan, err)
	}
	for _, settings := range []string{`{"gradle_task":"build"}`, `{"gradle_task":"smartDocOpenApi;touch x"}`, `{"gradle_task":"--init-script"}`, `{"config_file":"../config.json"}`, `{"module_pom":"other/pom.xml"}`, `{"output_file":"src/openapi.json"}`} {
		if _, err = Prepare(apidoc.PlanInput{Tool: "gradle", Settings: json.RawMessage(settings)}); err == nil {
			t.Fatal(settings)
		}
	}
}

func TestSchemaUsesApplicationBuildTool(t *testing.T) {
	for _, tool := range []string{"maven", "gradle"} {
		input, _ := json.Marshal(apidoc.PlanInput{Tool: tool})
		body, _ := json.Marshal(p.Request{Capability: apidoc.Capability, Operation: "schema", Actor: p.Actor{Role: "admin"}, Input: input})
		r := httptest.NewRequest("POST", "/v1/invoke", strings.NewReader(string(body)))
		r.Header.Set("Authorization", "Bearer fixture")
		w := httptest.NewRecorder()
		Handler("fixture").ServeHTTP(w, r)
		var response p.Response
		json.Unmarshal(w.Body.Bytes(), &response)
		if response.Error != nil || strings.Contains(string(response.Data), `"plugin_version"`) != (tool == "maven") || strings.Contains(string(response.Data), `"gradle_task"`) != (tool == "gradle") {
			t.Fatal(w.Body.String())
		}
	}
}
