package monitor

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func diagnosticInput(t arthasTarget, kind string) map[string]any {
	return map[string]any{"target_id": t.ID, "revision": t.Revision, "service": t.Service, "environment": t.Environment, "diagnostic": kind}
}
func saveFixtureCredential(t *testing.T, f *arthasFixture, target arthasTarget, secret string) arthasTarget {
	t.Helper()
	body := map[string]any{"id": target.ID, "name": target.Name, "service": target.Service, "environment": target.Environment, "endpoint": target.Endpoint, "revision": target.Revision, "credential": map[string]any{"username": "arthas", "password": secret}}
	r := f.request(t, "PUT", "/api/arthas/targets/"+target.ID, "alice", body)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	var out arthasTarget
	_ = json.Unmarshal(r.Body.Bytes(), &out)
	if !out.HasCredential || strings.Contains(r.Body.String(), secret) {
		t.Fatal("credential not saved or leaked", r.Body.String())
	}
	return out
}
func TestArthasSavedCredentialAndDiagnostics(t *testing.T) {
	f := newArthasFixture(t)
	target := f.target(t)
	secret := `fake-"credential-<test>`
	target = saveFixtureCredential(t, f, target, secret)
	f.mu.Lock()
	f.secret = secret
	f.mode = "structured"
	f.mu.Unlock()
	var encrypted string
	if err := f.st.db.QueryRow(`SELECT encrypted FROM arthas_credentials WHERE target_id=?`, target.ID).Scan(&encrypted); err != nil || strings.Contains(encrypted, secret) {
		t.Fatal("plain credential persisted", err)
	}
	key := filepath.Join(f.st.arthasSecretDir, "credentials.key")
	info, err := os.Stat(key)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("key permissions", err)
	}
	for kind, command := range arthasDiagnostics {
		before := f.callCount()
		r := f.request(t, "POST", "/api/arthas/diagnose", "alice", diagnosticInput(target, kind))
		if r.Code != 200 {
			t.Fatal(kind, r.Code, r.Body.String())
		}
		var result struct {
			State string
			Rows  []arthasMetricRow
		}
		_ = json.Unmarshal(r.Body.Bytes(), &result)
		if result.State != "success" || len(result.Rows) < 5 || strings.Contains(r.Body.String(), secret) || strings.Contains(r.Body.String(), "hidden-value") {
			t.Fatal("invalid normalized output", r.Body.String())
		}
		f.mu.Lock()
		calls := append([]map[string]any{}, f.calls[before:]...)
		f.mu.Unlock()
		if len(calls) != 3 || calls[0]["action"] != "init_session" || calls[1]["command"] != command || calls[2]["action"] != "close_session" {
			t.Fatal("not a single isolated sample", calls)
		}
	}
	list := f.request(t, "GET", "/api/arthas/targets", "alice", nil)
	if strings.Contains(list.Body.String(), secret) || strings.Contains(list.Body.String(), encrypted) {
		t.Fatal("credential in target list")
	}
	r := f.request(t, "POST", "/api/arthas/sessions", "alice", map[string]any{"target_id": target.ID, "revision": target.Revision, "use_saved_credential": true})
	if r.Code != 200 {
		t.Fatal("saved credential cannot connect", r.Code, r.Body.String())
	}
}
func TestArthasCredentialBindingClearAndMissingKey(t *testing.T) {
	f := newArthasFixture(t)
	target := saveFixtureCredential(t, f, f.target(t), "test-only-secret")
	target.Name = "renamed"
	r := f.request(t, "PUT", "/api/arthas/targets/"+target.ID, "alice", target)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	_ = json.Unmarshal(r.Body.Bytes(), &target)
	if !target.HasCredential {
		t.Fatal("rename discarded saved credential")
	}
	if err := os.Remove(filepath.Join(f.st.arthasSecretDir, "credentials.key")); err != nil {
		t.Fatal(err)
	}
	before := f.callCount()
	r = f.request(t, "POST", "/api/arthas/diagnose", "alice", diagnosticInput(target, "overview"))
	if r.Code != 409 || f.callCount() != before {
		t.Fatal("missing key contacted JVM", r.Code)
	}
	r = f.request(t, "PUT", "/api/arthas/targets/"+target.ID, "alice", map[string]any{"name": target.Name, "service": target.Service, "environment": target.Environment, "endpoint": target.Endpoint, "revision": target.Revision, "credential": map[string]any{"password": "replacement"}})
	if r.Code != 500 {
		t.Fatal("missing key silently replaced", r.Code)
	}
	target.Environment = "prod"
	r = f.request(t, "PUT", "/api/arthas/targets/"+target.ID, "alice", target)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	_ = json.Unmarshal(r.Body.Bytes(), &target)
	if target.HasCredential {
		t.Fatal("retargeted secret was preserved")
	}
	var n int
	f.st.db.QueryRow(`SELECT COUNT(*) FROM arthas_credentials`).Scan(&n)
	if n != 0 {
		t.Fatal("clear did not remove reference")
	}
}
func TestArthasPresetValidationFailureAndNoReplay(t *testing.T) {
	f := newArthasFixture(t)
	target := f.target(t)
	for _, change := range []map[string]any{{"command": "ognl dangerous"}, {"diagnostic": "ognl"}, {"service": "another-app"}, {"environment": "prod"}, {"revision": 0}, {"endpoint": "http://other"}} {
		body := diagnosticInput(target, "overview")
		for k, v := range change {
			body[k] = v
		}
		before := f.callCount()
		r := f.request(t, "POST", "/api/arthas/diagnose", "alice", body)
		if r.Code < 400 || f.callCount() != before {
			t.Fatal("invalid preset sent", change, r.Code)
		}
	}
	for mode, want := range map[string]string{"failed": "failed", "nonzero": "failed", "timeout": "timeout", "running": "unknown", "disconnect": "", "oversize": ""} {
		f.setMode(mode)
		before := f.callCount()
		r := f.request(t, "POST", "/api/arthas/diagnose", "alice", diagnosticInput(target, "hot_threads"))
		if want == "" {
			if r.Code != 502 {
				t.Fatal(mode, r.Code)
			}
		} else {
			var result map[string]any
			_ = json.Unmarshal(r.Body.Bytes(), &result)
			if r.Code != 200 || result["state"] != want {
				t.Fatal(mode, r.Code, r.Body.String())
			}
		}
		f.mu.Lock()
		execs := 0
		for _, call := range f.calls[before:] {
			if call["action"] == "exec" {
				execs++
			}
		}
		f.mu.Unlock()
		if execs != 1 {
			t.Fatal("command replayed", mode, execs)
		}
	}
}
func TestArthasDiagnosticLocksConfigurationAndRequiresAudit(t *testing.T) {
	f := newArthasFixture(t)
	target := f.target(t)
	f.mu.Lock()
	f.execStarted = make(chan struct{})
	f.execRelease = make(chan struct{})
	f.mode = "blocked"
	f.mu.Unlock()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- f.request(t, "POST", "/api/arthas/diagnose", "alice", diagnosticInput(target, "overview"))
	}()
	select {
	case <-f.execStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("diagnosis not started")
	}
	for _, method := range []string{"PUT", "DELETE"} {
		r := f.request(t, method, "/api/arthas/targets/"+target.ID, "alice", target)
		if r.Code != 409 {
			t.Error("active target was mutable", method, r.Code)
		}
	}
	close(f.execRelease)
	if r := <-done; r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	if _, err := f.st.db.Exec(`DROP TABLE arthas_audit`); err != nil {
		t.Fatal(err)
	}
	before := f.callCount()
	r := f.request(t, "POST", "/api/arthas/diagnose", "alice", diagnosticInput(target, "memory"))
	if r.Code != 500 || f.callCount() != before {
		t.Fatal("diagnosis without durable audit", r.Code)
	}
}
