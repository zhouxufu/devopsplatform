package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func arthasDeploymentFixture(t *testing.T, kind string) (*arthasDeploymentManager, arthasDeployment, *[]string, *map[string][]byte, *string) {
	t.Helper()
	st, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { st.Close() })
	if e = st.InitializeEnvironments([]Environment{{Name: "test", DisplayName: "测试"}}); e != nil {
		t.Fatal(e)
	}
	if e = st.UpsertService(Service{Name: "orders", ArtifactType: "jar"}); e != nil {
		t.Fatal(e)
	}
	if e = st.UpsertHost(Host{Name: "test-node", Addr: "fixture.invalid:22", User: "root", Password: "SSH-SECRET", HostKey: "SHA256:fixture", Environment: "test"}); e != nil {
		t.Fatal(e)
	}
	if e = st.UpsertHostGroup(HostGroup{Name: "orders-test", Environment: "test", Hosts: []string{"test-node"}}); e != nil {
		t.Fatal(e)
	}
	if e = st.UpsertServiceEnvironment(ServiceEnvironment{Service: "orders", Environment: "test", TargetType: kind, RemoteDir: "/opt/orders", StartCmd: "java -jar /opt/orders/app.jar", HostGroup: "orders-test", DeployConfig: map[string]string{"container_name": "orders"}}); e != nil {
		t.Fatal(e)
	}
	if _, e = st.db.Exec(`INSERT INTO deploys(service,environment,host,status) VALUES('orders','test','test-node','success')`); e != nil {
		t.Fatal(e)
	}
	v := st.arthasDeployments("orders", "test")
	if len(v) != 1 {
		t.Fatalf("candidates=%#v", v)
	}
	commands := []string{}
	uploads := map[string][]byte{}
	start := "1234"
	m := &arthasDeploymentManager{st: st, busy: map[string]bool{}}
	m.io = arthasDeploymentIO{
		exec: func(ctx context.Context, h Host, cmd string, timeout time.Duration) (string, error) {
			commands = append(commands, cmd)
			if strings.Contains(cmd, "docker inspect --format") {
				return strings.Repeat("c", 64) + " 900 true 0", nil
			}
			if strings.Contains(cmd, "jdk=no") {
				return "42\t1000\t1000\tapp\t" + start + "\tabcd-1234\t/opt/jdk/bin/java\tyes\n", nil
			}
			return "", nil
		},
		upload: func(ctx context.Context, h Host, content []byte, path string) error {
			uploads[path] = append([]byte{}, content...)
			return nil
		},
		pack: func(context.Context) ([]byte, error) { return []byte("verified-tar-fixture"), nil },
		request: func(ctx context.Context, h Host, cmd string, body []byte) ([]byte, error) {
			commands = append(commands, cmd)
			return []byte(`{"state":"SUCCEEDED","sessionId":"s1","consumerId":"c1"}`), nil
		},
	}
	return m, v[0], &commands, &uploads, &start
}

func TestManagedArthasHostAndDockerOnboarding(t *testing.T) {
	for _, kind := range []string{"ssh-jar", "docker-host"} {
		t.Run(kind, func(t *testing.T) {
			m, v, commands, uploads, start := arthasDeploymentFixture(t, kind)
			h := m.st.GetHost(v.Host)
			ps, e := m.inspect(context.Background(), v, *h)
			if e != nil || len(ps) != 1 {
				t.Fatalf("inspect %v %#v", e, ps)
			}
			rec, e := m.onboard(context.Background(), "admin", v, *h, ps[0])
			if e != nil {
				t.Fatal(e)
			}
			if rec.State != "ready" {
				t.Fatalf("state=%s", rec.State)
			}
			persisted, e := m.load(v.ID)
			if e != nil || persisted.Binding != v.Binding || persisted.ContainerID != ps[0].ContainerID {
				t.Fatalf("persist: %#v %v", persisted, e)
			}
			_, e = m.remoteAPI(context.Background(), persisted, []byte(`{"action":"exec","command":"jvm"}`))
			if e != nil {
				t.Fatal(e)
			}
			joined := strings.Join(*commands, "\n")
			if strings.Contains(joined, "SSH-SECRET") || strings.Contains(joined, "--password") || strings.Contains(joined, "0.0.0.0") || strings.Contains(joined, "docker restart") || strings.Contains(joined, "kill ") {
				t.Fatalf("unsafe commands: %s", joined)
			}
			if !strings.Contains(joined, "-pid 42") || !strings.Contains(joined, "-telnet-port -1") || !strings.Contains(joined, "-target-ip 127.0.0.1") {
				t.Fatal("missing bounded loopback attach")
			}
			if kind == "docker-host" && (!strings.Contains(joined, "docker cp") || !strings.Contains(joined, "docker exec -u 1000:1000") || !strings.Contains(joined, "nsenter -t 900 -n -- curl")) {
				t.Fatal("Docker namespace/identity/user not honored")
			}
			var props string
			for name, body := range *uploads {
				if strings.HasSuffix(name, "arthas.properties") {
					props = string(body)
				}
			}
			if !strings.Contains(props, "localConnectionNonAuth=false") || !strings.Contains(props, "arthas.password=") {
				t.Fatal("missing managed authentication")
			}
			for _, line := range strings.Split(props, "\n") {
				if strings.HasPrefix(line, "arthas.password=") {
					secret := strings.TrimPrefix(line, "arthas.password=")
					if strings.Contains(joined, secret[:32]) {
						t.Fatal("authentication bytes leaked into operational commands")
					}
				}
			}
			before := len(*uploads)
			if _, e = m.onboard(context.Background(), "admin", v, *h, ps[0]); e != nil || len(*uploads) != before {
				t.Fatal("duplicate attach should reuse handshake, not upload/attach")
			}
			*start = "9999"
			before = len(*commands)
			if _, e = m.remoteAPI(context.Background(), persisted, []byte(`{"action":"exec","command":"jvm"}`)); e == nil {
				t.Fatal("stale JVM accepted")
			}
			for _, cmd := range (*commands)[before:] {
				if strings.Contains(cmd, "curl --silent") {
					t.Fatal("command dispatched to replacement JVM")
				}
			}
		})
	}
}
func TestManagedArthasUncertainAttachNeverReplayed(t *testing.T) {
	m, v, commands, _, _ := arthasDeploymentFixture(t, "docker-host")
	h := m.st.GetHost(v.Host)
	ps, _ := m.inspect(context.Background(), v, *h)
	base := m.io.exec
	m.io.exec = func(ctx context.Context, h Host, cmd string, d time.Duration) (string, error) {
		out, e := base(ctx, h, cmd, d)
		if strings.Contains(cmd, " -pid ") {
			return "PRIVATE ROOT CAUSE", errors.New("SSH-SECRET")
		}
		return out, e
	}
	m.io.request = func(context.Context, Host, string, []byte) ([]byte, error) { return nil, errors.New("unreachable") }
	if _, e := m.onboard(context.Background(), "admin", v, *h, ps[0]); e == nil || strings.Contains(e.Error(), "PRIVATE") || strings.Contains(e.Error(), "SSH-SECRET") {
		t.Fatalf("unsafe failure %v", e)
	}
	if _, e := m.onboard(context.Background(), "admin", v, *h, ps[0]); e == nil {
		t.Fatal("uncertain action replayed")
	}
	n := 0
	for _, cmd := range *commands {
		if strings.Contains(cmd, " -pid ") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("attaches=%d", n)
	}
}
func TestManagedArthasRoutesScopeAndPrivilege(t *testing.T) {
	m, v, commands, _, _ := arthasDeploymentFixture(t, "ssh-jar")
	mux := http.NewServeMux()
	registerArthasDeploymentHandler(mux, m)
	request := func(method, path, role, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("X-User", "admin")
		r.Header.Set("X-Role", role)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	if w := request("GET", "/api/arthas-managed/instances", "member", ""); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := request("GET", "/api/arthas-managed/instances?environment=prod", "admin", ""); strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatal(w.Body.String())
	}
	if len(*commands) != 0 {
		t.Fatal("listing unexpectedly contacted SSH")
	}
	w := request("GET", "/api/arthas-managed/instances/"+v.ID, "admin", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "/opt/jdk") || strings.Contains(w.Body.String(), "host_pid") || strings.Contains(w.Body.String(), "SSH-SECRET") {
		t.Fatal(w.Body.String())
	}
	var out struct {
		Processes []arthasProcess `json:"processes"`
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	w = request("POST", "/api/arthas-managed/instances/"+v.ID+"/attach", "admin", `{"fingerprint":"`+strings.Repeat("a", 40)+`"}`)
	if w.Code != 409 {
		t.Fatal(w.Code)
	}
	for _, cmd := range *commands {
		if strings.Contains(cmd, " -pid ") {
			t.Fatal("stale fingerprint attached")
		}
	}
}
func TestManagedArthasRejectsUnsupportedAndMalformedProcessIdentity(t *testing.T) {
	for _, raw := range []string{"", "42\t1000\t1000\tapp\t123\tabcd\t/opt/jre/bin/java\tno", "42\t1000\t1000\tapp;touch x\t123\tabcd\t/opt/jdk/bin/java\tyes"} {
		if _, e := parseArthasProcesses(raw, arthasDeployment{}, "", 0); e == nil {
			t.Fatal("accepted invalid runtime")
		}
	}
}

func TestManagedArthasRejectsContainerChangeDuringInspection(t *testing.T) {
	for _, tc := range []struct {
		name, after string
		readFails   bool
	}{
		{"restart while reading", strings.Repeat("c", 64) + " 901 true 1", true},
		{"restart with successful stale output", strings.Repeat("c", 64) + " 901 true 1", false},
		{"restart with reused host PID", strings.Repeat("c", 64) + " 900 true 1", false},
		{"container exits", strings.Repeat("c", 64) + " 0 false 0", true},
		{"replacement container", strings.Repeat("d", 64) + " 900 true 0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, v, commands, _, _ := arthasDeploymentFixture(t, "docker-host")
			base, snapshots := m.io.exec, 0
			m.io.exec = func(ctx context.Context, h Host, cmd string, timeout time.Duration) (string, error) {
				out, err := base(ctx, h, cmd, timeout)
				if strings.Contains(cmd, "docker inspect --format") {
					snapshots++
					if snapshots == 2 {
						return tc.after, nil
					}
				}
				if strings.Contains(cmd, "jdk=no") && tc.readFails {
					return "PRIVATE-CONTAINER-OUTPUT", errors.New("PRIVATE-SSH-ERROR")
				}
				return out, err
			}
			ps, err := m.inspect(context.Background(), v, *m.st.GetHost(v.Host))
			if err == nil || len(ps) != 0 || !strings.Contains(err.Error(), "重启、退出或被替换") || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatalf("stale container identity accepted or misdiagnosed: %#v %v", ps, err)
			}
			if snapshots != 2 {
				t.Fatalf("unexpected retry: %d snapshots", snapshots)
			}
			for _, cmd := range *commands {
				if strings.Contains(cmd, "load instrument") || strings.Contains(cmd, " -pid ") || strings.Contains(cmd, "docker restart") {
					t.Fatal("inspection executed a business mutation")
				}
			}
		})
	}
}

func TestManagedArthasStoppedContainerAndStableReadFailure(t *testing.T) {
	for _, stopped := range []bool{true, false} {
		m, v, _, _, _ := arthasDeploymentFixture(t, "docker-host")
		base, reads := m.io.exec, 0
		m.io.exec = func(ctx context.Context, h Host, cmd string, timeout time.Duration) (string, error) {
			out, err := base(ctx, h, cmd, timeout)
			if stopped && strings.Contains(cmd, "docker inspect --format") {
				return strings.Repeat("c", 64) + " 0 false 7", nil
			}
			if strings.Contains(cmd, "jdk=no") {
				reads++
				return "PRIVATE", errors.New("SECRET")
			}
			return out, err
		}
		_, err := m.inspect(context.Background(), v, *m.st.GetHost(v.Host))
		if err == nil || strings.Contains(err.Error(), "SECRET") {
			t.Fatalf("unsafe/missing error: %v", err)
		}
		if stopped {
			if reads != 0 || !strings.Contains(err.Error(), "累计重启 7 次") {
				t.Fatalf("stopped container probed: %v", err)
			}
		} else if !strings.Contains(err.Error(), "无法读取 JVM 身份") {
			t.Fatalf("stable read failure falsely reported as restart: %v", err)
		}
	}
}

func TestManagedArthasJREUsesPinnedNativeAttach(t *testing.T) {
	for _, kind := range []string{"ssh-jar", "docker-host"} {
		for _, arch := range []string{"x64", "arm64"} {
			t.Run(kind+"/"+arch, func(t *testing.T) {
				m, v, commands, uploads, _ := arthasDeploymentFixture(t, kind)
				exec := m.io.exec
				m.io.exec = func(ctx context.Context, h Host, cmd string, d time.Duration) (string, error) {
					out, e := exec(ctx, h, cmd, d)
					if strings.Contains(cmd, "jdk=no") {
						out = strings.ReplaceAll(out, "\tyes\n", "\tnative-"+arch+"\n")
					}
					return out, e
				}
				m.io.native = arthasNativeAttachBinary
				h := m.st.GetHost(v.Host)
				ps, e := m.inspect(context.Background(), v, *h)
				if e != nil || len(ps) != 1 {
					t.Fatalf("JRE rejected: %v", e)
				}
				if ps[0].AttachMode != "native-"+arch {
					t.Fatal("missing native mode")
				}
				if _, e = m.onboard(context.Background(), "admin", v, *h, ps[0]); e != nil {
					t.Fatal(e)
				}
				joined := strings.Join(*commands, "\n")
				if !strings.Contains(joined, "load instrument false") || strings.Contains(joined, " -pid 42") || strings.Contains(joined, "apt ") || strings.Contains(joined, "apk ") || strings.Contains(joined, "docker run") {
					t.Fatal("JRE should use direct native attach without JDK installation or helper container")
				}
				found := false
				for path, body := range *uploads {
					if strings.HasSuffix(path, "/jattach") {
						found = len(body) > 0
					}
				}
				if !found {
					t.Fatal("native helper not uploaded")
				}
				if !strings.Contains(joined, "chmod 700") {
					t.Fatal("native executable permission missing")
				}
			})
		}
	}
}
func TestManagedArthasNativeReleasesAreVerifiedStaticELF(t *testing.T) {
	for arch := range arthasNativeReleases {
		if _, e := arthasNativeAttachBinary(context.Background(), arch); e != nil {
			t.Fatalf("%s: %v", arch, e)
		}
		raw, e := uiFS.ReadFile("assets/arthas/jattach-linux-" + arch + ".tgz")
		if e != nil {
			t.Fatal(e)
		}
		raw[len(raw)/2] ^= 1
		if _, e = decodeArthasNativeBinary(raw, arch); e == nil {
			t.Fatal("tampered native binary accepted")
		}
	}
	if _, e := arthasNativeAttachBinary(context.Background(), "unsupported"); e == nil {
		t.Fatal("unsupported arch accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := arthasNativeAttachBinary(ctx, "x64"); e == nil {
		t.Fatal("cancelled install continued")
	}
}
func TestManagedArthasNumericContainerUser(t *testing.T) {
	raw := "42\t1000\t1000\tUNKNOWN\t123\tabcd\t/opt/jre/bin/java\tnative-x64"
	if _, e := parseArthasProcesses(raw, arthasDeployment{}, strings.Repeat("c", 64), 900); e != nil {
		t.Fatal("container numeric user rejected", e)
	}
	if _, e := parseArthasProcesses(raw, arthasDeployment{}, "", 0); e == nil {
		t.Fatal("host user without account accepted")
	}
}
