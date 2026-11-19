package deploy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExistingApplicationAndCollectionCatalogUseSession(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpsertEnvironment(Environment{Name: "test", DisplayName: "测试"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertHost(Host{Name: "node", Environment: "test", Addr: "192.0.2.1", User: "root"}); err != nil {
		t.Fatal(err)
	}
	if err = st.UpsertService(Service{Name: "existing", ArtifactType: "image"}); err != nil {
		t.Fatal(err)
	}
	id := st.GetService("existing").ApplicationID
	seen := map[string]bool{}
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "lp_session=fixture" {
			http.Error(w, "unauthorized", 401)
			return
		}
		if r.URL.Path != "/api/registry/applications/observe" {
			if r.URL.Path == "/api/registry/applications" && r.Method == "GET" {
				json.NewEncoder(w).Encode([]any{})
				return
			}
			t.Fatal(r.URL.Path)
		}
		var body struct{ Name, Source, ApplicationID string }
		var raw map[string]string
		json.NewDecoder(r.Body).Decode(&raw)
		body.Name, body.Source, body.ApplicationID = raw["name"], raw["source"], raw["application_id"]
		if body.Name != "existing" || body.ApplicationID != id {
			t.Errorf("identity changed: %+v", body)
		}
		seen[body.Source] = true
		json.NewEncoder(w).Encode(map[string]string{"application_id": id})
	}))
	defer catalog.Close()
	h := Routes(st, NewBuilder(st, dir, NewLogHub()), NewLogHub(), catalog.URL, NewStepLib(dir+"/steps", dir+"/custom"))
	for _, tc := range []struct{ method, path, body string }{{"GET", "/api/services", ""}, {"PUT", "/api/hosts/node/agent/config", `{"server_url":"http://logs:9801","collect":[{"service":"existing","application_id":"` + id + `","paths":["/var/log/existing.log"]}]}`}} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		r.Header.Set("Cookie", "lp_session=fixture")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if !seen["deploy"] || !seen["logs"] {
		t.Fatal("catalog registration lost", seen)
	}
	if st.GetService("existing").ApplicationID != id || st.AgentDesiredConfig("node").Collect[0].ApplicationID != id {
		t.Fatal("stable application identity changed")
	}
}
