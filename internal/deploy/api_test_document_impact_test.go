package deploy

import (
	"encoding/json"
	"fmt"
	"logplatform/pkg/apitest"
	"strings"
	"testing"
)

func impactSnapshot(t *testing.T, st *Store, env, version, typ string, removed bool) int64 {
	t.Helper()
	paths := fmt.Sprintf(`{"/orders/{id}":{"parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}],"get":{"responses":{"200":{"description":"OK","content":{"application/json":{"schema":{"$ref":"#/components/schemas/Order"}}}}}}}}`)
	if removed {
		paths = `{}`
	}
	raw := []byte(fmt.Sprintf(`{"openapi":"3.0.3","info":{"title":"订单","version":%q},"paths":%s,"components":{"schemas":{"Order":{"type":"object","properties":{"id":{"type":%q}}}}}}`, version, paths, typ))
	id, e := st.recordAPIDocument(st.GetService("orders"), APIDocumentSnapshot{Environment: env, Provider: "fixture"}, raw)
	if e != nil {
		t.Fatal(e)
	}
	return id
}
func TestAPIDocumentImpactReviewsIsolationAndNewRevision(t *testing.T) {
	st, h := apiTestingFixture(t)
	base := impactSnapshot(t, st, "test", "1", "integer", false)
	c := saveTeamCase(t, h, "alice", "member", apiTestCase{Name: "查询订单", Visibility: "team", DocumentID: base, Steps: []apiTestStep{{Name: "查询", DocumentID: base, OperationID: "get /orders/{id}", Request: apitest.Request{Path: "/orders/7"}}}})
	private := saveTeamCase(t, h, "alice", "member", apiTestCase{Name: "私人", DocumentID: base, Request: apitest.Request{Path: "/orders/7"}})
	next := impactSnapshot(t, st, "test", "2", "string", false)
	_ = impactSnapshot(t, st, "prod", "prod", "boolean", true)
	path := "/api/api-testing/document-impacts?service=orders"
	w := apiTestingRequest(h, "GET", path, "bob", "member", nil)
	var rows []apiTestDocumentImpact
	json.Unmarshal(w.Body.Bytes(), &rows)
	if w.Code != 200 || len(rows) != 1 || rows[0].CaseID == private.ID || rows[0].SnapshotID != next || rows[0].State != "needs_review" || rows[0].CanReview || len(rows[0].Fields) == 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	reviewPath := fmt.Sprintf("/api/api-testing/cases/%d/document-review?service=orders", c.ID)
	input := map[string]any{"revision": c.Revision, "step_index": 0, "snapshot_id": next, "note": "已核对类型，保留用例"}
	if w = apiTestingRequest(h, "POST", reviewPath, "bob", "member", input); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w = apiTestingRequest(h, "POST", reviewPath, "alice", "member", input); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	get := func() apiTestDocumentImpact {
		w := apiTestingRequest(h, "GET", path+fmt.Sprint("&case_id=", c.ID), "alice", "member", nil)
		var rows []apiTestDocumentImpact
		json.Unmarshal(w.Body.Bytes(), &rows)
		if w.Code != 200 || len(rows) != 1 {
			t.Fatal(w.Code, w.Body.String())
		}
		return rows[0]
	}
	if got := get(); got.State != "reviewed" || got.Note == "" {
		t.Fatal(got)
	}
	stored, _ := st.apiTestCase(st.GetService("orders").ApplicationID, "alice", c.ID)
	if stored.Revision != c.Revision || stored.Steps[0].Request.Path != "/orders/7" {
		t.Fatal("review rewrote case")
	}
	removed := impactSnapshot(t, st, "test", "3", "string", true)
	if got := get(); got.State != "needs_review" || got.Kind != "removed" || got.SnapshotID != removed {
		t.Fatal(got)
	}
	if w = apiTestingRequest(h, "POST", reviewPath, "alice", "member", input); w.Code != 409 {
		t.Fatal("stale document acknowledged", w.Code)
	}
	input["snapshot_id"] = removed
	if w = apiTestingRequest(h, "POST", reviewPath, "alice", "member", input); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	c.Name = "改名后的修订"
	c = saveTeamCase(t, h, "alice", "member", c)
	if get().State != "needs_review" {
		t.Fatal("old review silenced new case revision")
	}
}
func TestAPIDocumentLinkValidationAndLegacyInference(t *testing.T) {
	st, h := apiTestingFixture(t)
	id := impactSnapshot(t, st, "test", "1", "integer", false)
	app := st.GetService("orders").ApplicationID
	c := saveTeamCase(t, h, "alice", "member", apiTestCase{Name: "旧用例", DocumentID: id, Request: apitest.Request{Method: "GET", Path: "/orders/7?x=1"}})
	rows, e := st.apiTestDocumentImpacts(app, &c, 0, newAPITestImpactCache())
	if e != nil || len(rows) != 1 || rows[0].OperationID != "get /orders/{id}" || !rows[0].Inferred {
		t.Fatal(rows, e)
	}
	invalid := apiTestCase{Name: "错误关联", Steps: []apiTestStep{{Name: "步骤", DocumentID: id, OperationID: "get /missing", Request: apitest.Request{Path: "/"}}}}
	w := apiTestingRequest(h, "POST", "/api/api-testing/cases?service=orders", "alice", "member", invalid)
	if w.Code == 200 || !strings.Contains(w.Body.String(), "关联接口") {
		t.Fatal(w.Code, w.Body.String())
	}
	invalid.Steps[0].OperationID = "get /orders/{id}"
	w = apiTestingRequest(h, "POST", "/api/api-testing/cases?service=other", "alice", "member", invalid)
	if w.Code == 200 {
		t.Fatal("cross-app document")
	}
}
