package rulesplugin

import (
	"encoding/json"
	p "logplatform/pkg/extensionprotocol"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCompilerPermissionsAndValidation(t *testing.T) {
	h := Handler("fixture-token")
	for _, tc := range []struct{ role, operation, input, code string }{{"member", "compile", `{}`, "permission_denied"}, {"admin", "compile", `{"policy":{"version":99}}`, "invalid_argument"}, {"admin", "schema", `{}`, ""}, {"admin", "compile", `{"policy":{"version":1,"default":"unknown","rules":[]}}`, ""}} {
		raw, _ := json.Marshal(p.Request{Capability: "policy.release.v1", Operation: tc.operation, Actor: p.Actor{Role: tc.role}, Input: json.RawMessage(tc.input)})
		r := httptest.NewRequest("POST", "/v1/invoke", strings.NewReader(string(raw)))
		r.Header.Set("Authorization", "Bearer fixture-token")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var got p.Response
		if json.Unmarshal(w.Body.Bytes(), &got) != nil {
			t.Fatal(w.Body.String())
		}
		if tc.code != "" && (got.Error == nil || got.Error.Code != tc.code) {
			t.Fatal(tc, w.Body.String())
		}
		if tc.code == "" && (got.Error != nil || got.Data == nil) {
			t.Fatal(tc, w.Body.String())
		}
	}
	r := httptest.NewRequest("GET", "/v1/describe", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("missing host token accepted")
	}
}
