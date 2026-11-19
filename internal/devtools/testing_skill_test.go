package devtools

import (
	"bytes"
	"encoding/json"
	"logplatform/internal/deploy"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
)

// Exercise the actual examples bundled in the Skill against the real platform
// validators and storage. No execution endpoint or business target is allowed.
func TestTestingSkillExamplesValidatePreviewSaveAndLayout(t *testing.T) {
	raw, err := workflowSkillFS.ReadFile("skills/devops-api-testing/references/test-graphs.md")
	if err != nil {
		t.Fatal(err)
	}
	blocks := regexp.MustCompile("(?s)```json\\s*(.*?)```").FindAllSubmatch(raw, -1)
	if len(blocks) != 2 {
		t.Fatal("expected case and layout examples")
	}
	args := make([]map[string]any, 2)
	for i, b := range blocks {
		d := json.NewDecoder(bytes.NewReader(b[1]))
		d.UseNumber()
		if err = d.Decode(&args[i]); err != nil {
			t.Fatal(err)
		}
	}
	st, err := deploy.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for _, err = range []error{
		st.UpsertService(deploy.Service{Name: "orders", ArtifactType: "jar"}),
		st.UpsertEnvironment(deploy.Environment{Name: "test", DisplayName: "测试"}),
		st.UpsertHost(deploy.Host{Name: "test-host", Environment: "test", Addr: "192.0.2.55:22", User: "fixture"}),
		st.UpsertHostGroup(deploy.HostGroup{Name: "test-group", Environment: "test", Hosts: []string{"test-host"}}),
		st.UpsertServiceEnvironment(deploy.ServiceEnvironment{Service: "orders", Environment: "test", HostGroup: "test-group", TargetType: "ssh-jar", RemoteDir: "/srv/orders", StartCmd: "java -jar app.jar", DeployConfig: map[string]string{"java_port": "8080"}}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	hub := deploy.NewLogHub()
	routes := deploy.Routes(st, deploy.NewBuilder(st, t.TempDir(), hub), hub, "", deploy.NewStepLib(t.TempDir()))
	var caseWrites, layoutWrites, executions atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/runs") && r.Method == "POST" {
			executions.Add(1)
			http.Error(w, "execution forbidden by fixture", 409)
			return
		}
		if r.URL.Path == "/api/api-testing/cases" && r.Method == "POST" {
			caseWrites.Add(1)
		}
		if strings.HasSuffix(r.URL.Path, "/layout") && r.Method == "PUT" {
			layoutWrites.Add(1)
		}
		routes.ServeHTTP(w, r)
	}))
	t.Cleanup(up.Close)
	store, err := OpenTokenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := store.Create(CreateTokenRequest{Owner: "alice", Name: "testing skill", Scopes: []string{"apitests:read", "apitests:write", "apitests:execute"}, Applications: []string{"orders"}, Environments: []string{"test"}})
	if err != nil {
		t.Fatal(err)
	}
	token.verified = true
	token.role = "member"
	svc := NewToolService(Upstreams{Deploy: up.URL})
	svc.actions = store
	checked := mapValue(docCall(t, svc, &token, "validate_api_test_case", args[0]))
	if checked["valid"] != true {
		t.Fatal(checked)
	}
	args[0]["case"] = checked["case"]
	preview := docCall(t, svc, &token, "save_api_test_case", args[0])
	if caseWrites.Load() != 0 || executions.Load() != 0 {
		t.Fatal("example preview performed a write/execution")
	}
	confirmed := docConfirm(t, svc, &token, preview)
	saved := mapValue(confirmed["result"])
	id := intArg(saved, "id", 0)
	if id <= 0 || caseWrites.Load() != 1 || intArg(saved, "definition_version", 0) != 2 {
		t.Fatal(confirmed)
	}
	read := mapValue(docCall(t, svc, &token, "get_api_test_case", map[string]any{"service": "orders", "environment": "test", "case_id": id}))
	if len(mapValue(read["case"])["steps"].([]any)) != 7 {
		t.Fatal("saved graph differs from the example")
	}
	args[1]["case_id"] = id
	preview = docCall(t, svc, &token, "save_api_test_layout", args[1])
	if layoutWrites.Load() != 0 {
		t.Fatal("layout preview wrote")
	}
	docConfirm(t, svc, &token, preview)
	layout := mapValue(docCall(t, svc, &token, "get_api_test_layout", map[string]any{"service": "orders", "environment": "test", "case_id": id, "workspace": "standalone"}))
	if intArg(layout, "revision", 0) != 1 || len(mapValue(layout["positions"])) != 3 || layoutWrites.Load() != 1 || executions.Load() != 0 {
		t.Fatal(layout, executions.Load())
	}
}

func TestTestingSkillCompatibilityAndOptionalPermissions(t *testing.T) {
	svc := NewToolService(Upstreams{})
	tools := []Tool{}
	for _, tool := range svc.Tools() {
		if tool.Name != "save_api_test_layout" {
			tools = append(tools, tool)
		}
	}
	catalog, err := workflowSkillCatalog(tools)
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range catalog {
		if skill.ID == "devops-api-testing" {
			if skill.Compatible || len(skill.MissingTools) != 1 || skill.MissingTools[0] != "save_api_test_layout" {
				t.Fatal(skill)
			}
			for _, scope := range skill.Scopes {
				if strings.HasPrefix(scope, "workitems:") || strings.HasPrefix(scope, "apidocs:") {
					t.Fatal("optional source access became mandatory", skill.Scopes)
				}
			}
		} else if !skill.Compatible {
			t.Fatal("unrelated workflow affected", skill)
		}
	}
}
