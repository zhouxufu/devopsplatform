package platform

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPolicyCompilerBoundaryUnavailableAndBuiltin(t *testing.T) {
	g := NewGateway("")
	for _, tc := range []struct {
		role, body string
		ok         bool
		status     int
	}{{"member", `{"provider":"builtin"}`, false, 403}, {"admin", `{"provider":"missing","policy":{}}`, false, 503}, {"admin", `{"provider":"builtin","policy":{"version":1}}`, true, 200}} {
		r := httptest.NewRequest("PUT", "/api/research/connections/issues/release-rules", strings.NewReader(tc.body))
		w := httptest.NewRecorder()
		if got := g.compileReleasePolicy(w, r, "alice", tc.role); got != tc.ok {
			t.Fatal(tc, got)
		}
		if !tc.ok && w.Code != tc.status {
			t.Fatal(w.Code, w.Body.String())
		}
		if tc.ok {
			raw, _ := io.ReadAll(r.Body)
			if string(raw) != tc.body {
				t.Fatal("builtin body lost")
			}
		}
	}
}
