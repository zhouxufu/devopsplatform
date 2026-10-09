package platform

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkbenchDiscoveryWithoutDeploy(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "manifest.yaml"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("delivery-core", "name: delivery-core\ninternal: true\nbinary: ../../bin/deploy\nadmin_addr: 127.0.0.1:19803\n")
	write("deploy", "name: deploy\nenabled: false\nbackend: delivery-core\nui_path: /ui\n")
	write("dbms", "name: dbms\nbackend: delivery-core\nui_path: /ui/dbms\n")
	ms, err := DiscoverPlugins(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 {
		t.Fatalf("want core and dbms, got %v", ms)
	}
	for _, m := range ms {
		if m.Name == "dbms" && (m.Binary != "" || m.AdminAddr != "127.0.0.1:19803") {
			t.Fatalf("alias must not launch another process: %+v", m)
		}
	}
	write("delivery-core", "name: delivery-core\nenabled: false\ninternal: true\nbinary: x\nadmin_addr: 127.0.0.1:19803\n")
	if _, err = DiscoverPlugins(dir); err == nil {
		t.Fatal("missing required backend accepted")
	}
}

func TestWorkbenchGatewayScopesAndSharedResources(t *testing.T) {
	var path string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { path = r.URL.Path; w.Write([]byte(`[]`)) }))
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	g := NewGateway("")
	g.SetPlugins([]*Manifest{{Name: "delivery-core", Internal: true, AdminAddr: u.Host}, {Name: "dbms", Backend: "delivery-core", AdminAddr: u.Host, UIPath: "/ui/dbms"}, {Name: "redis", Backend: "delivery-core", AdminAddr: u.Host, UIPath: "/ui/redis"}})
	h := testGatewayRoutes(t, g)
	for _, test := range []struct {
		url, want string
		status    int
	}{
		{"/api/plugins/dbms/ui", "/ui/dbms", 200}, {"/api/plugins/redis/ui", "/ui/redis", 200},
		{"/api/plugins/dbms/api/workbench/dbms/changes", "/api/workbench/dbms/changes", 200},
		{"/api/resources/environments", "/api/environments", 200},
		{"/api/plugins/dbms/api/builds", "", 404}, {"/api/plugins/dbms/api/workbench/redis/changes", "", 404}, {"/api/plugins/delivery-core/api/services", "", 404},
	} {
		path = ""
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", test.url, nil))
		if w.Code != test.status || path != test.want {
			t.Fatalf("%s: status %d, path %q", test.url, w.Code, path)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/plugins", nil))
	var visible []Manifest
	if json.Unmarshal(w.Body.Bytes(), &visible) != nil || len(visible) != 2 {
		t.Fatalf("internal service exposed: %s", w.Body)
	}
}
