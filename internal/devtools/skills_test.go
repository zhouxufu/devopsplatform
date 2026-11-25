package devtools

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestWorkflowSkillCatalogCompatibilityAndArchive(t *testing.T) {
	store, err := OpenTokenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tools := NewToolService(Upstreams{})
	routes := Routes(store, tools)
	w := httptest.NewRecorder()
	routes.ServeHTTP(w, httptest.NewRequest("GET", "/api/skills", nil))
	var catalog []workflowSkill
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &catalog) != nil || len(catalog) != 5 {
		t.Fatal(w.Code, w.Body.String())
	}
	wantVersions := map[string]string{"devops-work-loop": "1.0.0", "devops-code-recovery": "1.0.0", "devops-change-report": "1.4.0", "devops-api-docs": "1.2.0", "devops-api-testing": "1.3.0"}
	for _, skill := range catalog {
		if !skill.Compatible || len(skill.MissingTools) > 0 || skill.Version != wantVersions[skill.ID] || !strings.Contains(skill.Prompt, "$"+skill.ID) {
			t.Fatal(skill)
		}
		one := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/skills/"+skill.ID+"/download", nil)
		req.Header.Set("Authorization", "Bearer FIXTURE_NEVER_EMBED")
		req.Header.Set("X-User", "fixture-user")
		routes.ServeHTTP(one, req)
		two := httptest.NewRecorder()
		routes.ServeHTTP(two, httptest.NewRequest("GET", "/api/skills/"+skill.ID+"/download", nil))
		if one.Code != 200 || one.Header().Get("Content-Type") != "application/zip" || !strings.Contains(one.Header().Get("Content-Disposition"), skill.ID+".zip") || !bytes.Equal(one.Body.Bytes(), two.Body.Bytes()) {
			t.Fatal("unstable/invalid download")
		}
		archive, err := zip.NewReader(bytes.NewReader(one.Body.Bytes()), int64(one.Body.Len()))
		if err != nil {
			t.Fatal(err)
		}
		files := map[string][]byte{}
		for _, file := range archive.File {
			if path.Clean(file.Name) != file.Name || !strings.HasPrefix(file.Name, skill.ID+"/") || file.Mode().Perm()&0111 != 0 || file.UncompressedSize64 > 64<<10 {
				t.Fatal("unsafe archive path/mode/size", file.Name)
			}
			reader, err := file.Open()
			if err != nil {
				t.Fatal(err)
			}
			raw, err := io.ReadAll(reader)
			reader.Close()
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(raw, []byte("FIXTURE_NEVER_EMBED")) || bytes.Contains(raw, []byte("fixture-user")) {
				t.Fatal("request data leaked into skill")
			}
			files[strings.TrimPrefix(file.Name, skill.ID+"/")] = raw
		}
		entry := files["SKILL.md"]
		parts := bytes.SplitN(entry, []byte("---"), 3)
		if len(parts) != 3 {
			t.Fatal("missing frontmatter")
		}
		var front struct {
			Name, Description string
			Metadata          map[string]string
		}
		if yaml.Unmarshal(parts[1], &front) != nil || front.Name != skill.ID || front.Description == "" || front.Metadata["version"] != skill.Version {
			t.Fatal("catalog/package mismatch")
		}
		for _, match := range regexp.MustCompile(`\]\((references/[^)]+)\)`).FindAllSubmatch(entry, -1) {
			if files[string(match[1])] == nil {
				t.Fatal("broken skill reference", string(match[1]))
			}
		}
		var metadata struct {
			Dependencies struct {
				Tools []struct{ Type, Value, URL string }
			}
			Policy struct {
				AllowImplicitInvocation *bool `yaml:"allow_implicit_invocation"`
			}
		}
		if yaml.Unmarshal(files["agents/openai.yaml"], &metadata) != nil || len(metadata.Dependencies.Tools) != 1 || metadata.Dependencies.Tools[0].Value != "devOps" || metadata.Dependencies.Tools[0].URL != "" {
			t.Fatal("missing dependency or hardcoded server URL")
		}
		if metadata.Policy.AllowImplicitInvocation != nil && !*metadata.Policy.AllowImplicitInvocation {
			t.Fatal("ordinary discovery should remain enabled")
		}
	}
	missing := []Tool{}
	for _, tool := range tools.Tools() {
		if tool.Name != "report_changes" {
			missing = append(missing, tool)
		}
	}
	compat, err := workflowSkillCatalog(missing)
	if err != nil || compat[0].Compatible || len(compat[0].MissingTools) != 1 || !compat[1].Compatible {
		t.Fatal("tool compatibility drift undetected", compat, err)
	}
	for _, id := range []string{"unknown", "..", "devops-change-report%2F.."} {
		w = httptest.NewRecorder()
		routes.ServeHTTP(w, httptest.NewRequest("GET", "/api/skills/"+id+"/download", nil))
		if w.Code == 200 {
			t.Fatal("unknown skill exposed", id)
		}
	}
}

func TestWorkflowSkillExamplesPreviewAgainstRealMCPRoutes(t *testing.T) {
	// Parse the first payload example shipped to users and exercise actual MCP
	// preview + actual deployment routes, not just a copy of an example fixture.
	firstJSON := func(file string) map[string]any {
		t.Helper()
		raw, err := workflowSkillFS.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		match := regexp.MustCompile("(?s)```json\\s*(.*?)```").FindSubmatch(raw)
		if len(match) != 2 {
			t.Fatal("missing request example")
		}
		var args map[string]any
		d := json.NewDecoder(bytes.NewReader(match[1]))
		d.UseNumber()
		if d.Decode(&args) != nil {
			t.Fatal("invalid request example")
		}
		return args
	}
	svc, tok, st, _, writes := changesFixture(t)
	preview := mapValue(docCall(t, svc, tok, "report_changes", firstJSON("skills/devops-change-report/references/change-payloads.md")))
	if preview["state"] != "preview" || len(st.Changes("", "orders", "", 100)) != 0 || writes.Load() != 0 {
		t.Fatal("example preview wrote changes")
	}
	docs, admin, _, docWrites := documentMCPFixture(t)
	preview = mapValue(docCall(t, docs, admin, "report_api_document", firstJSON("skills/devops-api-docs/references/document-reporting.md")))
	if preview["state"] != "preview" || docWrites.Load() != 0 {
		t.Fatal("document example wrote without confirmation")
	}
}

func TestWorkflowSkillDownloadsDoNotCreateTokensOrActions(t *testing.T) {
	store, err := OpenTokenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := Routes(store, NewToolService(Upstreams{}))
	for _, url := range []string{"/api/skills", "/api/skills/devops-change-report/download", "/api/skills/devops-api-docs/download", "/api/skills/devops-api-testing/download"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	if len(store.List()) != 0 || len(store.data.Actions) != 0 || len(store.AuditEvents(10)) != 0 {
		t.Fatal("read-only distribution mutated access or business state")
	}
}
