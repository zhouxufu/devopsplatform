package devtools

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Loopback-only fixture with disposable token metadata and no business upstream.
func TestTokenMaintenanceBrowserFixture(t *testing.T) {
	if os.Getenv("TOKEN_MAINTENANCE_BROWSER") != "1" {
		t.Skip("opt-in browser fixture")
	}
	store, err := OpenTokenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"研发助手", "待删除的测试 Key"} {
		if _, _, err = store.Create(CreateTokenRequest{Owner: "fixture-user", Name: name, Scopes: []string{"changes:read"}, Applications: []string{"orders"}, Environments: []string{"test"}}); err != nil {
			t.Fatal(err)
		}
	}
	routes := Routes(store, NewToolService(Upstreams{}))
	root, err := filepath.Abs("../../web/dist")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{}, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/stop":
			select {
			case done <- struct{}{}:
			default:
			}
			w.Write([]byte("stopped"))
			return
		case "/ui":
			http.ServeFile(w, r, filepath.Join(root, "devtools.html"))
			return
		case "/api/resources/environments":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[{"name":"test","display_name":"测试环境"},{"name":"prod","display_name":"生产环境","protected":true}]`))
			return
		case "/api/registry/services":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[{"name":"orders"},{"name":"other"}]`))
			return
		}
		if len(r.URL.Path) > 8 && r.URL.Path[:8] == "/assets/" {
			http.FileServer(http.Dir(root)).ServeHTTP(w, r)
			return
		}
		r.Header.Set("X-User", "fixture-user")
		r.Header.Set("X-Role", "admin")
		routes.ServeHTTP(w, r)
	})
	server := &http.Server{Addr: "127.0.0.1:19987", Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Error(err)
			done <- struct{}{}
		}
	}()
	t.Log("fixture http://127.0.0.1:19987/ui")
	select {
	case <-done:
	case <-time.After(20 * time.Minute):
		t.Error("fixture expired")
	}
}
