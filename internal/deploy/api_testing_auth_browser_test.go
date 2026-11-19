package deploy

import (
	"encoding/json"
	"fmt"
	"logplatform/pkg/apitest"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Explicitly opt in; all storage, users and credentials are temporary fixtures.
func TestAPITestAuthenticationBrowserFixture(t *testing.T) {
	port := os.Getenv("API_AUTH_FIXTURE_PORT")
	if port == "" {
		t.Skip("opt-in isolated browser fixture")
	}
	st, h := apiTestingFixture(t)
	sv := st.GetService("orders")
	p, _ := st.apiTestingProfile(sv.ApplicationID, "test")
	p.Entries = []apiTestEntry{{Key: "direct", Label: "直连服务", Kind: "direct", BaseURL: "http://192.0.2.60:8080", Auth: apitest.Auth{Mode: "none"}}, {Key: "gateway", Label: "测试网关", Kind: "gateway", BaseURL: "https://gateway.example.com", PathPrefix: "/orders", Auth: apitest.Auth{Mode: "login", BaseURL: "https://identity.example.com", Path: "/login", BodyType: "form", Body: "password={{var.API_PASSWORD}}", TokenPointer: "/data/token", Header: "Authorization", Prefix: "Bearer ", IdentityPointer: "/data/account", ExpiresIn: 1800, Refresh: "per_request"}}}
	if err := st.saveAPITestingProfile(sv.ApplicationID, "test", p); err != nil {
		t.Fatal(err)
	}
	files := http.FileServer(http.Dir("../../web/dist"))
	done := make(chan struct{})
	var once sync.Once
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__fixture/stop" {
			once.Do(func() { close(done) })
			fmt.Fprint(w, `{}`)
			return
		}
		if r.URL.Path == "/api/me" {
			json.NewEncoder(w).Encode(map[string]any{"username": "fixture-admin", "role": "admin", "auth_enabled": false})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			r.Header.Set("X-User", "fixture-admin")
			r.Header.Set("X-Role", "admin")
			if r.Method != "GET" && !(r.Method == "PUT" && r.URL.Path == "/api/api-testing/profile") {
				http.Error(w, "fixture blocks execution", 403)
				return
			}
			h.ServeHTTP(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})}
	listener, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve(listener)
	defer server.Close()
	t.Log("ready: http://127.0.0.1:" + port + "/api-center.html#/test-execution?service=orders&environment=test")
	select {
	case <-done:
	case <-time.After(25 * time.Minute):
	}
}
