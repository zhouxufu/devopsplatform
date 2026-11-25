package deploy

import (
	"encoding/json"
	"fmt"
	"logplatform/pkg/apidoc"
	"logplatform/pkg/apitest"
	"logplatform/pkg/dubborpc"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAPIDubboBrowserFixture(t *testing.T) {
	endpoint := os.Getenv("API_DUBBO_FIXTURE_ENDPOINT")
	if endpoint == "" {
		t.Skip("opt-in isolated Dubbo provider fixture")
	}
	st, h := apiTestingFixture(t)
	sv := st.GetService("orders")
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"ok":true,"id":7}`) }))
	defer target.Close()
	st.apiTests.client = apiTestingClient(target)
	profile, _ := st.apiTestingProfile(sv.ApplicationID, "test")
	profile.Entries = []apiTestEntry{{Key: "rpc", Label: "Dubbo 隔离服务", Kind: "dubbo", BaseURL: endpoint, Auth: apitest.Auth{Mode: "none"}}}
	if e := st.saveAPITestingProfile(sv.ApplicationID, "test", profile); e != nil {
		t.Fatal(e)
	}
	doc := strings.ReplaceAll(dubboDocumentFixture, "example.HouseService", "platform.fixture.EchoProvider$Echo")
	doc = strings.Replace(doc, `"method":"get"`, `"method":"echo","group":"fixture","version":"3.3.6"`, 1)
	doc = strings.Replace(doc, `"java_type":"java.lang.Long"`, `"java_type":"java.lang.Long","example":9223372036854775806`, 1)
	id, e := st.recordAPIDocument(sv, APIDocumentSnapshot{Environment: "test", DefaultMaintainer: "fixture-admin"}, []byte(doc))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = st.recordAPIDocument(sv, APIDocumentSnapshot{Environment: "test"}, []byte(apiDocumentFixture)); e != nil {
		t.Fatal(e)
	}
	_, ops, _, _ := apidoc.Parse([]byte(doc))
	c := apiTestCase{Name: "HTTP 与 Dubbo 混合回归", Visibility: "team", Steps: []apiTestStep{{ID: "http", Name: "HTTP 查询", Kind: "http", Request: apitest.Request{Mode: "http", Path: "/orders", Assertions: []apitest.Assertion{{Type: "status", Expected: "200"}}}, Extractions: []apitest.Extraction{{Name: "id", Source: "json", Path: "/id"}}}, {ID: "rpc", Name: "Dubbo 校验", Kind: "dubbo", DocumentID: id, OperationID: ops[0].ID, Request: apitest.Request{Mode: "dubbo", RPC: &dubborpc.Method{Interface: "platform.fixture.EchoProvider$Echo", Method: "echo", Group: "fixture", Version: "3.3.6", ParameterTypes: []string{"java.lang.Long"}}, Body: `["{{step.id}}"]`, Assertions: []apitest.Assertion{{Type: "rpc_success", Expected: "true"}, {Type: "json", Path: "", Expected: "7"}}}}}, Graph: &apiTestGraph{Entry: "http", Concurrency: 2, Edges: []apiTestEdge{{From: "http", To: "rpc"}}}}
	saveTeamCase(t, h, "fixture-admin", "admin", c)
	done := make(chan struct{})
	var once sync.Once
	files := http.FileServer(http.Dir("../../web/dist"))
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fixture/quit" {
			once.Do(func() { close(done) })
			return
		}
		if r.URL.Path == "/api/me" {
			json.NewEncoder(w).Encode(map[string]any{"username": "fixture-admin", "role": "admin"})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if r.Method != "GET" && !strings.HasPrefix(r.URL.Path, "/api/api-testing/") && !strings.HasPrefix(r.URL.Path, "/api/api-documents/") {
				http.Error(w, "blocked fixture write", 403)
				return
			}
			r.Header.Set("X-User", "fixture-admin")
			r.Header.Set("X-Role", "admin")
			h.ServeHTTP(w, r)
			return
		}
		files.ServeHTTP(w, r)
	}), ReadHeaderTimeout: 5 * time.Second}
	l, e := net.Listen("tcp", "127.0.0.1:20072")
	if e != nil {
		t.Fatal(e)
	}
	go server.Serve(l)
	defer server.Close()
	fmt.Println("DUBBO_BROWSER_READY http://127.0.0.1:20072/api-center.html#/documents")
	select {
	case <-done:
	case <-time.After(20 * time.Minute):
	}
}
