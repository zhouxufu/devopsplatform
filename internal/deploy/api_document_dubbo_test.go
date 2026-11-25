package deploy

import (
	"encoding/json"
	"fmt"
	"logplatform/pkg/apitest"
	"logplatform/pkg/dubborpc"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const dubboDocumentFixture = `{"dubbo":"1.0","info":{"title":"RPC","version":"1"},"methods":[{"interface":"example.HouseService","method":"get","summary":"查询房源","parameters":[{"name":"id","java_type":"java.lang.Long"}],"returns":{"java_type":"java.lang.String"}}]}`

func TestAPIDocumentDubboProtocolIsolation(t *testing.T) {
	st, h := apiTestingFixture(t)
	sv := st.GetService("orders")
	httpID, e := st.recordAPIDocument(sv, APIDocumentSnapshot{Environment: "test", DefaultMaintainer: "alice"}, []byte(apiDocumentFixture))
	if e != nil {
		t.Fatal(e)
	}
	rpcID, e := st.recordAPIDocument(sv, APIDocumentSnapshot{Environment: "test", DefaultMaintainer: "alice"}, []byte(dubboDocumentFixture), 0)
	if e != nil {
		t.Fatal(e)
	}
	for protocol, id := range map[string]int64{"http": httpID, "dubbo": rpcID} {
		_, base, e := st.documentBaseline(sv, "test", protocol)
		if e != nil || base.ID != id {
			t.Fatal(protocol, base, e)
		}
	}
	call := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("X-User", "alice")
		r.Header.Set("X-Role", "admin")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for suffix, id := range map[string]int64{"": httpID, "&protocol=dubbo": rpcID} {
		w := call("GET", "/api/api-documents?service=orders&environment=test"+suffix, "")
		var list []APIDocumentSnapshot
		if json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list) != 1 || list[0].ID != id {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	after := strings.Replace(dubboDocumentFixture, "查询房源", "查询房源详情", 1)
	w := call("POST", fmt.Sprintf("/api/api-documents/%d/edit-preview?service=orders&environment=test", rpcID), after)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call("POST", fmt.Sprintf("/api/api-documents/%d/edit-preview?service=orders&environment=test", rpcID), apiDocumentFixture)
	if w.Code != 400 {
		t.Fatal("protocol mutation allowed", w.Code)
	}
	w = call("GET", "/api/api-documents/search?service=orders&environment=test&protocol=dubbo", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "查询房源") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestAPIDubboMixedPreflightBeforeHTTP(t *testing.T) {
	st, h := apiTestingFixture(t)
	t.Setenv("DEVOPS_DUBBO_EXECUTOR_JAR", "")
	var calls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, `{"ok":true}`) }))
	defer target.Close()
	st.apiTests.client = apiTestingClient(target)
	c := saveTeamCase(t, h, "alice", "member", apiTestCase{Name: "mixed", Steps: []apiTestStep{{ID: "http", Name: "HTTP", Request: apitest.Request{Path: "/"}}, {ID: "rpc", Kind: "dubbo", Name: "RPC", Request: apitest.Request{Mode: "dubbo", RPC: &dubborpc.Method{Interface: "example.House", Method: "ping", ParameterTypes: []string{}}, Body: `[]`}}}})
	input := apiTestingInput(t, st, "mixed-preflight-no-effects")
	input.CaseID = c.ID
	input.CaseRevision = c.Revision
	input.Request = apitest.Request{}
	if _, e := st.apiTests.start(st.GetService("orders"), "alice", false, input); e == nil {
		t.Fatal("missing Dubbo target accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("HTTP ran before RPC target validation")
	}
}
