package deploy

import (
	"encoding/json"
	"fmt"
	"logplatform/pkg/apitest"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPITestCanvasIDsPreviewAndFrozenRun(t *testing.T) {
	st, h := apiTestingFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"ok":true}`) }))
	defer server.Close()
	st.apiTests.client = apiTestingClient(server)
	c := saveTeamCase(t, h, "alice", "member", apiTestCase{Name: "节点回归", Steps: []apiTestStep{{ID: "create", Name: "创建", Request: apitest.Request{Path: "/create"}}, {ID: "verify", Name: "校验", Request: apitest.Request{Path: "/verify"}}}})
	path := fmt.Sprintf("/api/api-testing/cases/%d?service=orders", c.ID)
	if w := apiTestingRequest(h, "GET", path, "bob", "member", nil); w.Code != 404 {
		t.Fatal("private case leak", w.Code)
	}
	preview := apiTestingRequest(h, "POST", "/api/api-testing/mcp/case-preview?service=orders&environment=test", "alice", "member", c)
	if preview.Code != 200 {
		t.Fatal(preview.Body.String())
	}
	current, _ := st.apiTestCase(st.GetService("orders").ApplicationID, "alice", c.ID)
	if current.Revision != 1 {
		t.Fatal("preview saved a revision")
	}
	in := apiTestingInput(t, st, "canvas-node-ids-run-1")
	in.Request = apitest.Request{}
	in.CaseID = c.ID
	in.CaseRevision = c.Revision
	run, err := st.apiTests.start(st.GetService("orders"), "alice", false, in)
	if err != nil {
		t.Fatal(err)
	}
	result := waitAPITest(t, st, "alice", run.ID)
	if len(result.Steps) != 2 || result.Steps[0].ID != "create" || result.Steps[1].ID != "verify" {
		t.Fatal(result.Steps)
	}
	c.Name = "修订"
	c = saveTeamCase(t, h, "alice", "member", c)
	if c.Steps[0].ID != "create" || result.CaseSnapshot.Revision != 1 {
		t.Fatal("identity/snapshot changed")
	}
	legacy := c
	legacy.DefinitionVersion = 0
	legacy.Steps = append([]apiTestStep(nil), c.Steps...)
	legacy.Steps[0].ID = ""
	if w := apiTestingRequest(h, "PUT", path, "alice", "member", legacy); w.Code != 409 {
		t.Fatal("legacy client overwrote node identity", w.Code)
	}
	c.Steps[1].ID = c.Steps[0].ID
	if w := apiTestingRequest(h, "PUT", path, "alice", "member", c); w.Code != 400 {
		t.Fatal("duplicate node IDs accepted")
	}
}

func TestAPITestAssetPreviewScopesReferencesAndRevision(t *testing.T) {
	_, h := apiTestingFixture(t)
	c := saveTeamCase(t, h, "alice", "member", apiTestCase{Name: "私人用例", Request: apitest.Request{Path: "/"}})
	base := "/api/api-testing/mcp/cases?service=orders&environment=test"
	w := apiTestingRequest(h, "GET", base, "bob", "member", nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "私人用例") {
		t.Fatal(w.Body.String())
	}
	w = apiTestingRequest(h, "GET", base+fmt.Sprint("&case_id=", c.ID), "bob", "member", nil)
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
	w = apiTestingRequest(h, "GET", "/api/api-testing/mcp/cases?service=other&environment=test", "alice", "member", nil)
	if w.Code != 400 {
		t.Fatal("unbound environment", w.Code)
	}
	c.Revision--
	w = apiTestingRequest(h, "POST", "/api/api-testing/mcp/case-preview?service=orders&environment=test", "alice", "member", c)
	if w.Code != 409 {
		t.Fatal("stale preview accepted")
	}
	c.Revision++
	c.Steps[0].Request.Headers = map[string]string{"Authorization": "Bearer inline-secret"}
	w = apiTestingRequest(h, "POST", "/api/api-testing/mcp/case-preview?service=orders&environment=test", "alice", "member", c)
	if w.Code == 200 {
		t.Fatal("inline credential accepted")
	}
	var payload map[string]any
	json.Unmarshal(w.Body.Bytes(), &payload)
}
