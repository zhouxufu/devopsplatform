package platform

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"logplatform/internal/monitor"
)

func TestArthasGatewayUsesTrustedPlatformRole(t *testing.T) {
	store, err := monitor.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	upstream := httptest.NewServer(monitor.Routes(store, nil))
	t.Cleanup(upstream.Close)
	target, _ := url.Parse(upstream.URL)
	g := NewGateway("enabled")
	g.SetPlugins([]*Manifest{{Name: "monitor", AdminAddr: target.Host}})
	h := testGatewayRoutes(t, g)
	admin := sessionToken(User{Username: "arthas-admin", Role: RoleAdmin, Enabled: true})
	member := sessionToken(User{Username: "arthas-member", Role: RoleMember, Enabled: true})
	t.Cleanup(func() { sessionsMu.Lock(); delete(sessions, admin); delete(sessions, member); sessionsMu.Unlock() })
	for _, tc := range []struct {
		token string
		want  int
	}{{"", 401}, {member, 403}, {admin, 200}} {
		r := httptest.NewRequest("GET", "/api/plugins/monitor/api/arthas/targets", nil)
		// Client-supplied identity must not authorize a member or anonymous request.
		r.Header.Set("X-User", "spoofed-admin")
		r.Header.Set("X-Role", "admin")
		if tc.token != "" {
			r.AddCookie(&http.Cookie{Name: "lp_session", Value: tc.token})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("got %d, want %d: %s", w.Code, tc.want, w.Body.String())
		}
	}
}

func TestArthasInternalDeploymentProtocolNotPublic(t *testing.T) {
	calls := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(200) }))
	defer backend.Close()
	u, _ := url.Parse(backend.URL)
	g := NewGateway("")
	g.SetPlugins([]*Manifest{{Name: "deploy", AdminAddr: u.Host}, {Name: "research", AdminAddr: u.Host}})
	h := testGatewayRoutes(t, g)
	for _, plugin := range []string{"deploy", "research"} {
		for _, suffix := range []string{"instances", "instances/a/attach", "instances/a/api"} {
			r := httptest.NewRequest("POST", "/api/plugins/"+plugin+"/api/arthas-managed/"+suffix, nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 404 {
				t.Fatalf("internal route public: %d", w.Code)
			}
		}
	}
	if calls != 0 {
		t.Fatal("internal proxy reached backend")
	}
}
