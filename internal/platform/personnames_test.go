package platform

import (
	"encoding/json"
	"logplatform/pkg/personnames"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestGatewaySuppliesNamesWithoutTrustingClientIdentity(t *testing.T) {
	reg, e := OpenRegistry(filepath.Join(t.TempDir(), "registry.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer reg.Close()
	if _, e = reg.SaveUser(User{Username: "alice", DisplayName: "艾丽丝", Role: RoleMember, Enabled: true}, "member-password"); e != nil {
		t.Fatal(e)
	}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		names := personnames.FromRequest(r)
		if names.Name("alice") != "艾丽丝" || names["mallory"] != "" {
			t.Error("spoofed or absent labels", names)
		}
		if r.Header.Get("X-Role") != RoleAdmin || r.Header.Get("X-User") != "local" {
			t.Error("names changed identity")
		}
		if strings.Contains(r.Header.Get(personnames.Header), "password") {
			t.Error("credential leak")
		}
		w.Write([]byte(`[]`))
	}))
	defer up.Close()
	u, _ := url.Parse(up.URL)
	g := NewGateway("")
	g.SetPlugins([]*Manifest{{Name: "devtools", AdminAddr: u.Host}, {Name: "deploy", AdminAddr: u.Host}})
	h := g.Routes(reg, NewBus())
	for _, path := range []string{"/api/plugins/devtools/api/tokens", "/api/plugins/deploy/api/api-documents/search"} {
		r := httptest.NewRequest("GET", path, nil)
		raw, _ := json.Marshal(map[string]string{"alice": "Forged", "mallory": "Mallory"})
		r.Header.Set(personnames.Header, string(raw))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
