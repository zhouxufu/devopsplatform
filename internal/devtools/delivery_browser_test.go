package devtools

import (
	"net/http"
	"os"
	"testing"
)

// Disposable UI fixture. It has no business upstream, real credentials or data.
func TestDeliveryTokenBrowserFixture(t *testing.T) {
	if os.Getenv("DELIVERY_TOKEN_BROWSER") != "1" {
		t.Skip("opt-in browser fixture")
	}
	store, err := OpenTokenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := NewToolService(Upstreams{})
	routes := Routes(store, service)
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
		case "/api/resources/environments":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[{"name":"test","display_name":"测试环境"}]`))
			return
		case "/api/registry/services":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[{"name":"fixture-app"}]`))
			return
		}
		r.Header.Set("X-User", "fixture-user")
		r.Header.Set("X-Role", "admin")
		routes.ServeHTTP(w, r)
	})
	server := &http.Server{Addr: "127.0.0.1:19986", Handler: handler}
	defer server.Close()
	go func() {
		if e := server.ListenAndServe(); e != nil && e != http.ErrServerClosed {
			t.Error(e)
			done <- struct{}{}
		}
	}()
	t.Log("fixture http://127.0.0.1:19986/ui")
	<-done
}
