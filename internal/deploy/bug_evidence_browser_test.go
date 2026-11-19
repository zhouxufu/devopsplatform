package deploy

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Opt-in real handlers/storage + official UI, synthetic requests only.
func TestBugEvidenceBrowserFixture(t *testing.T) {
	if os.Getenv("BUG_EVIDENCE_BROWSER_FIXTURE") != "1" {
		t.Skip("isolated browser fixture")
	}
	s, h := apiTestingFixture(t)
	c := evidenceCase(t, h)
	run := seedBugEvidenceRun(t, s, "alice", c)
	var business atomic.Int32
	s.apiTests.client.Transport = apiTestRoundTrip(func(r *http.Request) (*http.Response, error) {
		business.Add(1)
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":7,"status":"paid","token":"fixture-secret"}`))}, nil
	})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/me" {
			json.NewEncoder(w).Encode(map[string]any{"username": "alice", "role": "admin", "auth_enabled": false})
			return
		}
		if r.URL.Path == "/api/search" {
			json.NewEncoder(w).Encode(map[string]any{"hits": []any{map[string]any{"service": "orders", "environment": "test", "host": "fixture-test-host", "ts": time.Now().UnixMilli(), "level": "ERROR", "trace_id": "trace-fixture-123", "message": "ERROR order missing id=7\nAuthorization: Bearer fixture-secret\naccess_token=fixture-secret"}}, "has_more": false})
			return
		}
		if r.URL.Path == "/api/extensions" {
			json.NewEncoder(w).Encode([]any{})
			return
		}
		if r.URL.Path == "/fixture/state" {
			json.NewEncoder(w).Encode(map[string]any{"run_id": run.ID, "case_id": c.ID, "business": business.Load()})
			return
		}
		r.Header.Set("X-User", "alice")
		r.Header.Set("X-Role", "admin")
		h.ServeHTTP(w, r)
	})
	listener, e := net.Listen("tcp", "127.0.0.1:19983")
	if e != nil {
		t.Fatal(e)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go server.Serve(listener)
	defer server.Close()
	fmt.Println("Bug evidence isolated backend ready http://127.0.0.1:19983")
	<-time.After(25 * time.Minute)
}
