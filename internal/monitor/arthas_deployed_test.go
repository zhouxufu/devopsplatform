package monitor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestManagedArthasRegistrationAndDiagnosticTransport(t *testing.T) {
	f := newArthasFixture(t)
	id := strings.Repeat("a", 40)
	fingerprint := strings.Repeat("b", 40)
	attaches := 0
	calls := []string{}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-User") != "alice" || r.Header.Get("X-Role") != "admin" || r.Header.Get("Cookie") != "" {
			t.Error("untrusted or leaked identity")
		}
		calls = append(calls, r.URL.Path)
		switch {
		case strings.HasSuffix(r.URL.Path, "/attach"):
			attaches++
			writeJSON(w, map[string]any{"id": id, "name": "订单 · 测试容器", "service": "orders", "environment": "test", "fingerprint": fingerprint})
		case strings.HasSuffix(r.URL.Path, "/api"):
			if r.Header.Get("X-Arthas-Fingerprint") != fingerprint {
				t.Error("missing frozen runtime identity")
			}
			var input map[string]any
			json.NewDecoder(r.Body).Decode(&input)
			if input["action"] == "init_session" {
				writeJSON(w, map[string]any{"state": "SUCCEEDED", "sessionId": "managed-session", "consumerId": "consumer"})
				return
			}
			writeJSON(w, map[string]any{"state": "SUCCEEDED", "body": map[string]any{"results": []any{map[string]any{"type": "status", "statusCode": 0}}}})
		default:
			writeJSON(w, []any{map[string]string{"id": id, "service": "orders", "environment": "test"}})
		}
	}))
	defer backend.Close()
	f.st.ConfigureArthasDeploy(backend.URL)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		var input any
		if body != "" {
			input = json.RawMessage(body)
		}
		return f.request(t, method, path, "alice", input)
	}
	w := request("GET", "/api/arthas/deployed?service=orders&environment=test", "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = request("POST", "/api/arthas/deployed/"+id+"/attach", `{"fingerprint":"`+fingerprint+`"}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var target arthasTarget
	json.Unmarshal(w.Body.Bytes(), &target)
	if !target.Managed || target.Endpoint != "deployment://"+id+"/"+fingerprint || target.HasCredential {
		t.Fatalf("target=%#v", target)
	}
	w = request("GET", "/api/arthas/targets", "")
	if !strings.Contains(w.Body.String(), `"managed":true`) {
		t.Fatal(w.Body.String())
	}
	w = request("POST", "/api/arthas/diagnose", `{"target_id":"`+target.ID+`","revision":1,"service":"orders","environment":"test","diagnostic":"overview"}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if attaches != 1 || len(calls) < 5 {
		t.Fatal("registration/diagnostic did not reach managed transport", calls)
	}
	// Manual URL configuration must not be usable to forge a managed target.
	w = request("POST", "/api/arthas/targets", `{"name":"forged","service":"orders","environment":"test","endpoint":"deployment://`+id+`/`+fingerprint+`"}`)
	if w.Code != 400 {
		t.Fatal("managed transport accepted from manual configuration", w.Code)
	}
}

func TestManagedArthasBootstrapHasIndependentHeaderDeadline(t *testing.T) {
	store, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(25 * time.Millisecond); writeJSON(w, []any{}) }))
	defer backend.Close()
	store.ConfigureArthasDeploy(backend.URL)
	client := arthasHTTPClient()
	client.Transport.(*http.Transport).ResponseHeaderTimeout = time.Millisecond
	m := &arthasConsole{st: store, client: client}
	r := httptest.NewRequest("GET", "/api/arthas/deployed", nil)
	r.Header.Set("X-User", "alice")
	w := httptest.NewRecorder()
	m.deployedInstances(w, r)
	if w.Code != 200 {
		t.Fatalf("onboarding inherited diagnostic header timeout: %d %s", w.Code, w.Body.String())
	}
	if client.Transport.(*http.Transport).ResponseHeaderTimeout != time.Millisecond {
		t.Fatal("shared diagnostic timeout mutated")
	}
}
