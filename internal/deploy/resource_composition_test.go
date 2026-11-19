package deploy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResourceCompositionMultipleGroupsOwnAndConflicts(t *testing.T) {
	s, _, g := resourceGroupFixture(t)
	second := &ResourceGroup{Name: "dictionary", Requirements: []ApplicationResourceRequirement{{Alias: "dictionary", Kind: "mysql"}}, Bindings: []ApplicationEnvironmentResource{{Alias: "dictionary", Environment: "test", Connection: "db-second", Config: map[string]string{"database": "second_db"}}}}
	if err := s.SaveResourceGroup(second); err != nil {
		t.Fatal(err)
	}
	in := ApplicationResources{CompositionVersion: 2, ResourceGroups: []string{g.Name, second.Name}, GroupRevisions: map[string]int64{g.Name: g.Revision, second.Name: second.Revision}, OwnRequirements: []ApplicationResourceRequirement{{Alias: "private", Kind: "mysql"}}, OwnBindings: []ApplicationEnvironmentResource{{Alias: "private", Environment: "test", Connection: "db-dev", Config: map[string]string{"database": "dev_db"}}}}
	if err := s.SaveApplicationResources("orders", in); err != nil {
		t.Fatal(err)
	}
	got := s.ApplicationResources("orders")
	if got.Error != "" || len(got.Requirements) != 4 || len(got.OwnRequirements) != 1 {
		t.Fatalf("bad composition: %+v", got)
	}
	for _, alias := range []string{"db", "dictionary", "private"} {
		if _, err := s.sqlBinding("orders", "test", alias); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteResourceGroup(second.Name); err == nil {
		t.Fatal("deleted referenced group")
	}
	target, err := s.deliveryChangeTarget(&Change{ID: 101, Service: "orders", Kind: "mysql", SQL: &SQLChange{ResourceAlias: "dictionary", Content: "UPDATE orders SET status=1 WHERE id=1", MaxAffectedRows: 1}}, "test")
	if err != nil || target.GroupName != second.Name || target.GroupRevision != second.Revision || target.Connection != "db-second" {
		t.Fatal("lost frozen resource provenance", target, err)
	}
	in.OwnRequirements[0].Alias = "db"
	in.OwnBindings[0].Alias = "db"
	if err := s.SaveApplicationResources("orders", in); err == nil {
		t.Fatal("accepted alias collision")
	}
	if len(s.ApplicationResources("orders").Requirements) != 4 {
		t.Fatal("failed save changed existing bindings")
	}
	in.OwnRequirements[0].Alias = "private"
	in.OwnBindings[0].Alias = "private"
	in.GroupRevisions[g.Name]--
	if err := s.SaveApplicationResources("orders", in); err == nil {
		t.Fatal("stale group accepted")
	}
	// A later group edit that introduces a collision must block ALL target resolution.
	second.Requirements = append(second.Requirements, ApplicationResourceRequirement{Alias: "private", Kind: "mysql"})
	if err := s.SaveResourceGroup(second); err != nil {
		t.Fatal(err)
	}
	if _, err := s.sqlBinding("orders", "test", "db"); err == nil {
		t.Fatal("conflicting composition did not fail closed")
	}
}

func TestEnvironmentNacosCatalogAndRuntimeResolution(t *testing.T) {
	s, _, _ := deliveryFixture(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/cs/configs" || r.URL.Query().Get("search") != "blur" || r.URL.Query().Get("pageSize") != "20" {
			t.Errorf("unexpected request %s", r.URL)
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"totalCount":1,"pageItems":[{"dataId":"bus.yml","group":"DEFAULT_GROUP","content":"SECRET_BODY","encryptedDataKey":"SECRET_KEY"}]}`))
	}))
	defer upstream.Close()
	if err := s.UpsertConnection(ResourceConnection{Name: "nacos-test", Kind: "nacos", Endpoint: upstream.URL, Config: map[string]string{"nacos_auth": "none", "namespace": "testing"}}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	registerEnvironmentNacosRoutes(mux, s)
	call := func(method, path, body, actor, role string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("X-User", actor)
		r.Header.Set("X-Role", role)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	// Use the actual actor header used by deliveryActor (local when absent).
	denied := call("PUT", "/api/environments/test/nacos", `{"connection":"nacos-test"}`, "member", "member")
	if denied.Code != 403 {
		t.Fatal("member changed environment access", denied.Code)
	}
	w := call("PUT", "/api/environments/test/nacos", `{"connection":"nacos-test"}`, "", "admin")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call("PUT", "/api/environments/test/nacos", `{"connection":"nacos-test","previous":"wrong"}`, "", "admin")
	if w.Code != 409 {
		t.Fatal("missing CAS", w.Code)
	}
	w = call("GET", "/api/environments/test/nacos/configs?q=bus", "", "", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "SECRET") {
		t.Fatal(w.Code, w.Body.String())
	}
	var catalog struct {
		Items []ApplicationNacosConfig `json:"items"`
	}
	if json.Unmarshal(w.Body.Bytes(), &catalog) != nil || len(catalog.Items) != 1 {
		t.Fatal(w.Body.String())
	}
	if err := s.SaveApplicationResources("orders", ApplicationResources{CompositionVersion: 2, NacosConfigs: catalog.Items}); err != nil {
		t.Fatal(err)
	}
	c, err := s.nacosBinding("orders", "test", catalog.Items[0].Alias)
	if err != nil || c.Config["data_id"] != "bus.yml" || c.Config["namespace"] != "testing" {
		t.Fatal(c, err)
	}
	if _, err = s.nacosBinding("orders", "prod", catalog.Items[0].Alias); err == nil {
		t.Fatal("unconfigured production fell back to test")
	}
	if err = s.DeleteConnection("nacos-test"); err == nil {
		t.Fatal("environment connection deleted")
	}
}
