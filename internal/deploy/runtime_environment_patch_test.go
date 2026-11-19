package deploy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestRuntimeEnvironmentIncrementalAPI(t *testing.T) {
	s, _, _ := deliveryFixture(t)
	h := Routes(s, NewBuilder(s, t.TempDir(), NewLogHub()), NewLogHub(), "", NewStepLib(t.TempDir()+"/steps", t.TempDir()+"/custom"))
	put := func(edit string, status int) {
		t.Helper()
		body := `{"target_type":"ssh-jar","remote_dir":"/opt/orders","start_cmd":"true","host_group":"test-group",` + edit + `}`
		// Reuse the fixture's existing host group rather than guessing its name.
		var payload map[string]any
		if err := json.Unmarshal([]byte(body), &payload); err != nil {
			t.Fatal(err)
		}
		payload["host_group"] = s.GetServiceEnvironment("orders", "test").HostGroup
		raw, _ := json.Marshal(payload)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/services/orders/environments/test", strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		if rec.Code != status {
			t.Fatalf("status=%d expected=%d: %s", rec.Code, status, rec.Body.String())
		}
		for _, secret := range []string{"private-original", "private-new", "private-updated", "enc:v1:"} {
			if strings.Contains(rec.Body.String(), secret) {
				t.Fatal("response leaked runtime values")
			}
		}
		if status == http.StatusOK && strings.Contains(rec.Body.String(), `"runtime_env_patch"`) {
			t.Fatal("response echoed patch")
		}
	}
	read := func(want map[string]string) {
		t.Helper()
		target := s.GetServiceEnvironment("orders", "test")
		got, err := s.decryptRuntimeEnvironment(target.runtimeEnvEncrypted)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("stored variables mismatch (count=%d), err=%v", len(got), err)
		}
	}
	put(`"runtime_env":{"PROFILE":"test","SECRET":"private-original"}`, 200)
	frozen := *s.GetServiceEnvironment("orders", "test")
	put(`"runtime_env_patch":{"NEW":"private-new"}`, 200)
	read(map[string]string{"PROFILE": "test", "SECRET": "private-original", "NEW": "private-new"})
	put(`"runtime_env_patch":{"PROFILE":"prod","SECRET":"private-updated","EMPTY":""}`, 200)
	read(map[string]string{"PROFILE": "prod", "SECRET": "private-updated", "NEW": "private-new", "EMPTY": ""})
	put(`"runtime_env_patch":{}`, 200)
	put(`"health_url":""`, 200)
	read(map[string]string{"PROFILE": "prod", "SECRET": "private-updated", "NEW": "private-new", "EMPTY": ""})
	for _, edit := range []string{
		`"runtime_env_patch":{"PROFILE":null,"BROKEN":"private-new\ninvalid"}`,
		`"runtime_env_patch":{"BAD KEY":null}`,
		`"runtime_env":{},"runtime_env_patch":{"NEW":"private-new"}`,
	} {
		put(edit, 400)
	}
	read(map[string]string{"PROFILE": "prod", "SECRET": "private-updated", "NEW": "private-new", "EMPTY": ""})
	put(`"runtime_env_patch":{"PROFILE":null,"MISSING":null}`, 200)
	read(map[string]string{"SECRET": "private-updated", "NEW": "private-new", "EMPTY": ""})
	put(`"runtime_env":{}`, 200)
	read(map[string]string{})
	old, err := s.decryptRuntimeEnvironment(frozen.runtimeEnvEncrypted)
	if err != nil || old["SECRET"] != "private-original" {
		t.Fatal("frozen snapshot was changed")
	}
}

func TestRuntimeEnvironmentConcurrentPatches(t *testing.T) {
	s, _, _ := deliveryFixture(t)
	base := *s.GetServiceEnvironment("orders", "test")
	base.RuntimeEnv = map[string]string{"ORIGINAL": "private-original"}
	if err := s.UpsertServiceEnvironment(base); err != nil {
		t.Fatal(err)
	}
	base.RuntimeEnv = nil
	const writers = 12
	errs := make(chan error, writers)
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			next := base
			value := "private-new"
			next.RuntimeEnvPatch = map[string]*string{fmt.Sprintf("KEY_%d", i): &value}
			errs <- s.UpsertServiceEnvironment(next)
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.decryptRuntimeEnvironment(s.GetServiceEnvironment("orders", "test").runtimeEnvEncrypted)
	if err != nil || len(got) != writers+1 || got["ORIGINAL"] != "private-original" {
		t.Fatalf("concurrent additions lost: count=%d err=%v", len(got), err)
	}
}
