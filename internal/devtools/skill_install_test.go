package devtools

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestWorkflowSkillMCPAuthenticatedVersionedDistribution(t *testing.T) {
	store, err := OpenTokenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	raw := createTestToken(t, store) // Existing read-only token needs no new grant.
	svc := NewToolService(Upstreams{})
	h := NewMCPHandler(store, svc)
	before := store.List()[0]
	call := func(name string, args map[string]any, authenticated bool) (map[string]any, int) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
		req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
		if authenticated {
			req.Header.Set("Authorization", "Bearer "+raw)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 200 {
			return nil, w.Code
		}
		if strings.Contains(w.Body.String(), raw) {
			t.Fatal("credential leaked in package")
		}
		var rpc struct {
			Result struct {
				IsError bool `json:"isError"`
				Content []struct{ Text string }
			}
		}
		if json.Unmarshal(w.Body.Bytes(), &rpc) != nil || len(rpc.Result.Content) != 1 {
			t.Fatal(w.Body.String())
		}
		var value any
		if json.Unmarshal([]byte(rpc.Result.Content[0].Text), &value) != nil {
			t.Fatal("invalid tool JSON")
		}
		if array, ok := value.([]any); ok {
			return map[string]any{"catalog": array}, 200
		}
		obj := value.(map[string]any)
		obj["isError"] = rpc.Result.IsError
		return obj, 200
	}
	if _, status := call("list_platform_skills", map[string]any{}, false); status != 401 {
		t.Fatal("anonymous package access", status)
	}
	catalog, status := call("list_platform_skills", map[string]any{}, true)
	if status != 200 || len(catalog["catalog"].([]any)) != 5 {
		t.Fatal(catalog, status)
	}
	visible := svc.VisibleTools(&before)
	seen := map[string]bool{}
	for _, tool := range visible {
		if tool.Name == "list_platform_skills" || tool.Name == "get_platform_skill" {
			if tool.Annotations["readOnlyHint"] != true || tool.Scope != "" {
				t.Fatal("distribution acquired business permissions")
			}
			seen[tool.Name] = true
		}
		if tool.Name == "report_changes" || tool.Name == "confirm_platform_action" {
			t.Fatal("read-only token acquired write tool")
		}
	}
	if len(seen) != 2 {
		t.Fatal("new tools hidden", seen)
	}
	for _, v := range catalog["catalog"].([]any) {
		skill := v.(map[string]any)
		access := skill["access"].(map[string]any)
		if access["ready"] != false || len(access["missing_scopes"].([]any)) == 0 {
			t.Fatal("permission gap not exposed")
		}
		response, _ := call("get_platform_skill", map[string]any{"id": skill["id"], "expected_sha256": skill["sha256"]}, true)
		if response["isError"] != false || response["installation"].(map[string]any)["state"] != "not_checked_on_client" {
			t.Fatal(response)
		}
		installer := response["installer"].(map[string]any)
		if installer["content"] != workflowSkillInstaller || installer["sha256"] != skillSHA([]byte(workflowSkillInstaller)) {
			t.Fatal("installer bytes changed in RPC")
		}
		rawPackage, _ := json.Marshal(response["package"])
		var bundle workflowSkillPackage
		if json.Unmarshal(rawPackage, &bundle) != nil || bundle.SHA256 != skill["sha256"] {
			t.Fatal("manifest mismatch")
		}
		for _, file := range bundle.Files {
			original, _ := workflowSkillFS.ReadFile("skills/" + bundle.ID + "/" + file.Path)
			if !bytes.Equal(original, []byte(file.Content)) || file.SHA256 != skillSHA(original) {
				t.Fatal("redacted or changed bundle file", file.Path)
			}
		}
	}
	for _, args := range []map[string]any{
		{"id": "../skills"}, {"id": "unknown"}, {"id": "devops-api-docs", "expected_sha256": ""},
		{"id": "devops-api-docs", "expected_sha256": strings.Repeat("0", 64)},
		{"id": "devops-api-docs", "client_root": "/tmp"}, {"id": 1},
	} {
		response, _ := call("get_platform_skill", args, true)
		if response["isError"] != true {
			t.Fatal("invalid package request accepted", args)
		}
	}
	after := store.List()[0]
	if !reflect.DeepEqual(before.Scopes, after.Scopes) || !reflect.DeepEqual(before.Applications, after.Applications) || !reflect.DeepEqual(before.Environments, after.Environments) || len(store.List()) != 1 || len(store.data.Actions) != 0 {
		t.Fatal("installation distribution changed permissions or business state")
	}
	if len(store.AuditEvents(100)) < 3 {
		t.Fatal("MCP distribution not audited")
	}
	store.Revoke(before.ID)
	if _, status := call("get_platform_skill", map[string]any{"id": "devops-api-docs"}, true); status != 401 {
		t.Fatal("revoked token still downloads")
	}
}

func TestWorkflowSkillLocalInstaller(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("Python 3 is required to verify the shipped installer:", err)
	}
	dir := t.TempDir()
	installer := filepath.Join(dir, "install_platform_skill.py")
	if err = os.WriteFile(installer, []byte(workflowSkillInstaller), 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := workflowSkillCatalog(NewToolService(Upstreams{}).Tools())
	if err != nil {
		t.Fatal(err)
	}
	var bundles []workflowSkillPackage
	for _, skill := range catalog {
		bundle, e := workflowSkillBundle(skill.ID, skill.Version)
		if e != nil {
			t.Fatal(e)
		}
		bundles = append(bundles, bundle)
	}
	raw, _ := json.Marshal(bundles)
	file := filepath.Join(dir, "bundles.json")
	if err = os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(python, "skill_install_test.py", installer, file)
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("installer filesystem tests failed: %v\n%s", err, output)
	}
	t.Log(string(output))
}
