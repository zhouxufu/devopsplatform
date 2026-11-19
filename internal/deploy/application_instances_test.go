package deploy

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func applicationInstanceFixture(t *testing.T) (*Store, *instanceManager, *ServiceEnvironment, *Service, Host, *dockerInstanceInfo) {
	t.Helper()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err = st.InitializeEnvironments([]Environment{{Name: "qa", DisplayName: "测试"}}); err != nil {
		t.Fatal(err)
	}
	if err = st.UpsertService(Service{Name: "api", ArtifactType: "image", BuildCmd: "true"}); err != nil {
		t.Fatal(err)
	}
	h := Host{Name: "qa-a", Environment: "qa", Addr: "127.0.0.1:1", User: "fixture"}
	if err = st.UpsertHost(h); err != nil {
		t.Fatal(err)
	}
	if err = st.UpsertHostGroup(HostGroup{Name: "qa-group", Environment: "qa", Hosts: []string{h.Name}}); err != nil {
		t.Fatal(err)
	}
	target := ServiceEnvironment{Service: "api", Environment: "qa", TargetType: "docker-host", RemoteDir: "/app", HostGroup: "qa-group", DeployConfig: map[string]string{"container_name": "api-qa"}}
	if err = st.UpsertServiceEnvironment(target); err != nil {
		t.Fatal(err)
	}
	a := &Artifact{Service: "api", Version: "abc123", Kind: ArtifactOCI, URI: "registry.invalid/api@sha256:" + strings.Repeat("a", 64), Digest: "sha256:" + strings.Repeat("a", 64)}
	if err = st.RegisterArtifact(a); err != nil {
		t.Fatal(err)
	}
	dp := &Deploy{Service: "api", Host: h.Name, Environment: "qa", ArtifactID: a.ID, ArtifactURI: a.URI, ArtifactKind: a.Kind, ArtifactDigest: a.Digest, Version: a.Version, StartedAt: time.Now().Unix()}
	if err = st.CreateDeploy(dp); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishDeploy(dp.ID, "success"); err != nil {
		t.Fatal(err)
	}
	v := &dockerInstanceInfo{ID: strings.Repeat("c", 64), Name: "/api-qa", ImageID: "sha256:" + strings.Repeat("d", 64), ImageRef: a.URI, State: "running", PID: 42, StartedAt: "2026-10-28T01:00:00Z", Health: "healthy"}
	m := &instanceManager{st: st, exec: func(_ Host, command string, _ time.Duration) (string, error) {
		if strings.Contains(command, "--no-trunc") {
			b, _ := json.Marshal(v)
			return string(b), nil
		}
		return "", nil
	}}
	tg, sv, _, err := m.target("api", "qa")
	if err != nil {
		t.Fatal(err)
	}
	return st, m, tg, sv, h, v
}

func TestApplicationDockerInstanceIdentityAndUnsupportedTargets(t *testing.T) {
	st, m, tg, sv, h, v := applicationInstanceFixture(t)
	r := m.inspect(tg, sv, h)
	if !r.Controllable || r.Version != "abc123" || r.ContainerID != v.ID || r.Fingerprint == "" {
		t.Fatalf("bad instance %+v", r)
	}
	v.LabelService = "someone-else"
	if m.inspect(tg, sv, h).Controllable {
		t.Fatal("foreign label authorized")
	}
	v.LabelService = ""
	v.ImageRef = "registry.invalid/other@sha256:" + strings.Repeat("a", 64)
	if m.inspect(tg, sv, h).Controllable {
		t.Fatal("unrelated image authorized")
	}
	v.ImageRef = r.Image
	v.State = "restarting"
	if !m.inspect(tg, sv, h).Owned {
		t.Fatal("cannot view logs or stop owned restart loop")
	}
	v.State = "running"
	if err := st.UpsertEnvironment(Environment{Name: "other", DisplayName: "另一环境"}); err != nil {
		t.Fatal(err)
	}
	st.UpsertHost(Host{Name: "alias", Environment: "other", Addr: h.Addr, User: h.User})
	st.UpsertHostGroup(HostGroup{Name: "alias-group", Environment: "other", Hosts: []string{"alias"}})
	if err := st.UpsertServiceEnvironment(ServiceEnvironment{Service: "api", Environment: "other", TargetType: "docker-host", RemoteDir: "/app", HostGroup: "alias-group", DeployConfig: map[string]string{"container_name": "api-qa"}}); err != nil {
		t.Fatal(err)
	}
	if m.inspect(tg, sv, h).Controllable {
		t.Fatal("overlapping environment binding authorized")
	}
	st.db.Exec(`UPDATE service_environments SET target_type='manual' WHERE service='api' AND environment='qa'`)
	if _, _, _, err := m.target("api", "qa"); err == nil {
		t.Fatal("manual publication offered remote control")
	}
}

func TestApplicationDockerRestartBoundOnceAndUncertainNotReplayed(t *testing.T) {
	st, m, tg, sv, h, v := applicationInstanceFixture(t)
	initial := m.inspect(tg, sv, h)
	base := m.exec
	count := 0
	fail := false
	m.exec = func(h Host, c string, d time.Duration) (string, error) {
		if strings.Contains(c, "docker restart --time 30") {
			count++
			if !strings.Contains(c, "test \"$(docker inspect") || !strings.Contains(c, v.ID) {
				t.Fatal("missing ID guard")
			}
			if strings.Contains(c, "docker run") || strings.Contains(c, "docker pull") {
				t.Fatal("restart rebuilt/replaced container")
			}
			if fail {
				return "", errors.New("timeout")
			}
			v.StartedAt = "2026-10-28T02:00:00Z"
			return "", nil
		}
		return base(h, c, d)
	}
	req := instanceActionRequest{Fingerprint: initial.Fingerprint, RequestID: "restart-request-123456"}
	if _, err := m.control("api", "qa", h, "restart", "local", req); err != nil {
		t.Fatal(err)
	}
	if _, err := m.control("api", "qa", h, "restart", "local", req); err == nil || count != 1 {
		t.Fatal("duplicate restarted twice")
	}
	req.RequestID = "stale-request-123456"
	if _, err := m.control("api", "qa", h, "restart", "local", req); err == nil || count != 1 {
		t.Fatal("stale snapshot executed")
	}
	req.Fingerprint = m.inspect(tg, sv, h).Fingerprint
	req.RequestID = "uncertain-request-123456"
	fail = true
	if _, err := m.control("api", "qa", h, "restart", "local", req); err == nil {
		t.Fatal("timeout reported success")
	}
	var state string
	st.db.QueryRow(`SELECT state FROM instance_operations WHERE request_id=?`, req.RequestID).Scan(&state)
	if state != "uncertain" {
		t.Fatal(state)
	}
	m.control("api", "qa", h, "restart", "local", req)
	if count != 2 {
		t.Fatal("uncertain write replayed")
	}
	command, err := dockerInstanceControlCommand(initial, "restart")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-n")
	cmd.Stdin = strings.NewReader(command)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("invalid shell %v %s", err, out)
	}
}

func TestApplicationInstanceLeaseBlocksDuplicateAndRelease(t *testing.T) {
	st, m, tg, sv, h, _ := applicationInstanceFixture(t)
	r := m.inspect(tg, sv, h)
	base := m.exec
	started, finish := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	m.exec = func(h Host, c string, d time.Duration) (string, error) {
		if strings.Contains(c, "docker restart --time 30") {
			close(started)
			<-finish
			return "", nil
		}
		return base(h, c, d)
	}
	go func() {
		_, err := m.control("api", "qa", h, "restart", "local", instanceActionRequest{Fingerprint: r.Fingerprint, RequestID: "first-lease-request-12345"})
		done <- err
	}()
	<-started
	if _, err := m.control("api", "qa", h, "restart", "local", instanceActionRequest{Fingerprint: r.Fingerprint, RequestID: "second-lease-request-12345"}); err == nil {
		t.Error("parallel instance operation allowed")
	}
	tx, err := st.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = lockReleaseTarget(tx, "api", "qa"); err == nil {
		t.Error("release allowed during operation")
	}
	tx.Rollback()
	if err = st.CreateDeploy(&Deploy{Service: "api", Host: h.Name, Environment: "qa", StartedAt: time.Now().Unix()}); err == nil {
		t.Error("bare deployment allowed during operation")
	}
	if err := st.UpsertServiceEnvironment(*tg); err == nil {
		t.Error("environment edited during operation")
	}
	if err := st.DeleteServiceEnvironment("api", "qa"); err == nil {
		t.Error("environment removed during operation")
	}
	if err := st.DeleteService("api"); err == nil {
		t.Error("application removed during operation")
	}
	close(finish)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestApplicationInstanceRoutesPermissionsAndProtectedConfirmation(t *testing.T) {
	st, m, _, _, _, _ := applicationInstanceFixture(t)
	var writes atomic.Int32
	base := m.exec
	m.exec = func(h Host, c string, d time.Duration) (string, error) {
		if strings.Contains(c, "docker restart --time 30") {
			writes.Add(1)
		}
		return base(h, c, d)
	}
	mux := http.NewServeMux()
	registerInstanceRoutes(mux, st, m.exec)
	call := func(method, url, body, actor, role string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, url, strings.NewReader(body))
		r.Header.Set("X-User", actor)
		r.Header.Set("X-Role", role)
		mux.ServeHTTP(w, r)
		return w
	}
	if w := call("GET", "/api/services/api/environments/qa/instances", "", "member", "member"); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := call("POST", "/api/services/api/environments/qa/instances/qa-a/restart", `{}`, "member", "member"); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("POST", "/api/services/api/environments/qa/instances/outsider/restart", `{}`, "admin", "admin"); w.Code != 400 {
		t.Fatal(w.Code)
	}
	st.db.Exec(`UPDATE environments SET protected=1 WHERE name='qa'`)
	if w := call("POST", "/api/services/api/environments/qa/instances/qa-a/restart", `{}`, "admin", "admin"); w.Code != http.StatusPreconditionRequired {
		t.Fatal(w.Code)
	}
	if writes.Load() != 0 {
		t.Fatal("guard sent remote write")
	}
}

func TestApplicationInstanceLogsBoundedAndRedacted(t *testing.T) {
	_, m, tg, sv, h, _ := applicationInstanceFixture(t)
	r := m.inspect(tg, sv, h)
	base := m.exec
	m.exec = func(h Host, c string, d time.Duration) (string, error) {
		switch {
		case strings.Contains(c, "json .Config.Env"):
			return `["API_KEY=actual-private-key","NORMAL=value"]`, nil
		case strings.Contains(c, "docker logs --tail 200"):
			if !strings.Contains(c, "--since 15m") || !strings.Contains(c, "tail -c 65536") || !strings.Contains(c, r.ContainerID) {
				t.Fatal("unbounded or unbound log")
			}
			return "API_KEY=actual-private-key\nAuthorization: Bearer arbitrary-bearer\n{\"password\":\"json-private\"}\n\x1b[31mnormal output", nil
		}
		return base(h, c, d)
	}
	text, err := m.logs(tg, sv, h, r.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"actual-private-key", "arbitrary-bearer", "json-private", "\x1b"} {
		if strings.Contains(text, secret) {
			t.Fatalf("leaked %q in %s", secret, text)
		}
	}
	if !strings.Contains(text, "normal output") {
		t.Fatal(text)
	}
	if _, err = m.logs(tg, sv, h, "stale"); err == nil {
		t.Fatal("stale instance log allowed")
	}
}

func TestApplicationInstanceStopStartAndReplacement(t *testing.T) {
	_, m, tg, sv, h, v := applicationInstanceFixture(t)
	base := m.exec
	writes := 0
	replace := false
	m.exec = func(h Host, c string, d time.Duration) (string, error) {
		if strings.Contains(c, "docker stop --time 30") {
			writes++
			v.State = "exited"
			v.PID = 0
			return "", nil
		}
		if strings.Contains(c, "docker start ") {
			writes++
			v.State = "running"
			v.PID = 43
			return "", nil
		}
		if strings.Contains(c, "docker restart --time 30") && replace {
			writes++
			v.ID = strings.Repeat("e", 64)
			return "", nil
		}
		return base(h, c, d)
	}
	for _, action := range []string{"stop", "start"} {
		r := m.inspect(tg, sv, h)
		result, err := m.control("api", "qa", h, action, "local", instanceActionRequest{Fingerprint: r.Fingerprint, RequestID: action + "-lifecycle-request-123456"})
		if err != nil {
			t.Fatal(action, err)
		}
		if action == "stop" && result.State != "stopped" || action == "start" && result.State != "running" {
			t.Fatal(result.State)
		}
	}
	if writes != 2 {
		t.Fatal(writes)
	}
	replace = true
	r := m.inspect(tg, sv, h)
	if _, err := m.control("api", "qa", h, "restart", "local", instanceActionRequest{Fingerprint: r.Fingerprint, RequestID: "replace-instance-request-123456"}); err == nil {
		t.Fatal("replacement during operation reported success")
	}
	if _, err := m.control("api", "qa", h, "restart", "local", instanceActionRequest{Fingerprint: m.inspect(tg, sv, h).Fingerprint, RequestID: "unknown-followup-request-123456"}); err == nil || writes != 3 {
		t.Fatal("uncertain operation permitted a concurrent follow-up")
	}
}
