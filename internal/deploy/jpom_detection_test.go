package deploy

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func detectionRecord(dir, project string, options ...string) string {
	argv := []string{"java", "-Xmx512m", "-DJpom.application=" + project, "-DJpom.basedir=" + dir, "-jar", dir + "/original.jar"}
	argv = append(argv, options...)
	fields := []string{dir, "/jdk8/bin/java", filepath.Dir(dir) + "/console.log", `JAVA_VERSION="1.8.0_402"`, strings.Join(argv, "\x00") + "\x00"}
	for i, v := range fields {
		fields[i] = base64.StdEncoding.EncodeToString([]byte(v))
	}
	return "JPOM1\t" + strings.Join(fields, "\t") + "\n"
}
func TestJpomDetectionParsesExactRuntimeAndRejectsUnsafeOrIncompleteResults(t *testing.T) {
	dir := "/old/app/jar"
	record := detectionRecord(dir, "native-id", "--spring.profiles.active=test")
	items, complete, err := parseJpomDetection(record+"JPOM_DONE:0\n", dir, "")
	if err != nil || !complete || len(items) != 1 {
		t.Fatal(items, complete, err)
	}
	c := items[0].Config
	if c["java_version"] != "8" || c["java_home"] != "/jdk8" || c["jpom_project_id"] != "native-id" || c["java_args"] != "--spring.profiles.active=test" || c["java_jvm_options"] != "-Xmx512m" {
		t.Fatal(c)
	}
	for _, raw := range []string{record, record + "JPOM_DONE:1\n", record + "JPOM_DONE:0\nnoise", strings.Replace(record, "JPOM1", "broken", 1) + "JPOM_DONE:0\n", detectionRecord(dir, "native-id", "--password=never-return-this") + "JPOM_DONE:0\n"} {
		_, ok, e := parseJpomDetection(raw, dir, "")
		if ok && e == nil {
			t.Fatal("incomplete/unsafe result accepted")
		}
	}
	if _, _, e := parseJpomDetection(record+"JPOM_DONE:0\n", dir, "other-id"); e == nil {
		t.Fatal("other project matched")
	}
}
func TestJpomDetectionCommandOnlyReadsMatchingJarAndKeepsLiteralPaths(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "literal $(touch NOPE) library")
	proc := filepath.Join(root, "proc")
	pid := filepath.Join(proc, "123")
	for _, p := range []string{dir, pid + "/fd", root + "/jdk/bin"} {
		if e := os.MkdirAll(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	os.WriteFile(dir+"/original.jar", []byte("unchanged"), 0600)
	os.WriteFile(root+"/jdk/release", []byte("JAVA_VERSION=\"17.0.11\"\n"), 0600)
	argv := []string{"java", "-Xmx512m", "-DJpom.application=native", "-DJpom.basedir=" + dir, "-jar", dir + "/original.jar"}
	os.WriteFile(pid+"/cmdline", []byte(strings.Join(argv, "\x00")+"\x00"), 0600)
	os.Symlink(dir, pid+"/cwd")
	os.Symlink(root+"/jdk/bin/java", pid+"/exe")
	os.Symlink(root+"/console.log", pid+"/fd/1")
	out, e := exec.Command("bash", "-c", jpomDetectionCommand(dir, "", proc)).CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	items, ok, e := parseJpomDetection(string(out), dir, "")
	if e != nil || !ok || len(items) != 1 || items[0].Config["java_version"] != "17" {
		t.Fatal(string(out), items, ok, e)
	}
	b, _ := os.ReadFile(dir + "/original.jar")
	if string(b) != "unchanged" {
		t.Fatal("detection changed jar")
	}
	if _, e := os.Stat(root + "/NOPE"); !os.IsNotExist(e) {
		t.Fatal("path executed as shell")
	}
	out, e = exec.Command("bash", "-c", jpomDetectionCommand(dir, "other", proc)).CombinedOutput()
	if e != nil || string(out) != "JPOM_DONE:0\n" {
		t.Fatal("unrelated process leaked", string(out), e)
	}
}
func detectionStore(t *testing.T) *Store {
	t.Helper()
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	for _, env := range []Environment{{Name: "test", DisplayName: "测试", Purpose: "test"}, {Name: "prod", DisplayName: "生产", Purpose: "production", Protected: true, Approvers: "qa"}} {
		if e := s.UpsertEnvironment(env); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.UpsertService(Service{Name: "api", ArtifactType: "jar", BuildCmd: "true"}); e != nil {
		t.Fatal(e)
	}
	for _, h := range []Host{{Name: "one", Environment: "test", Addr: "192.0.2.1", User: "test"}, {Name: "two", Environment: "test", Addr: "192.0.2.2", User: "test"}, {Name: "prod", Environment: "prod", Addr: "192.0.2.3", User: "test"}} {
		if e := s.UpsertHost(h); e != nil {
			t.Fatal(e)
		}
	}
	return s
}
func TestJpomDetectionHostAgreementScopeAndNoPersistence(t *testing.T) {
	s := detectionStore(t)
	v := ServiceEnvironment{Service: "api", Environment: "test", TargetType: "ssh-jar", RemoteDir: "/original/app/.devopsplatform/api", Hosts: []string{"one", "two"}}
	dir := "/original/app/jar"
	calls := 0
	probe := func(_ context.Context, h Host, cmd string) (string, error) {
		calls++
		if !strings.Contains(cmd, dir) {
			t.Fatal("wrong migration directory")
		}
		return detectionRecord(dir, "native-id") + "JPOM_DONE:0\n", nil
	}
	got, e := s.detectJpomJava(context.Background(), v, probe)
	if e != nil || got.Status != "matched" || calls != 2 || got.Candidate.Config["jpom_project_id"] != "native-id" {
		t.Fatal(got, e, calls)
	}
	if s.GetServiceEnvironment("api", "test") != nil {
		t.Fatal("detection persisted a target")
	}
	cases := []struct {
		status string
		raw    func(Host) string
		fail   bool
	}{
		{"ambiguous", func(h Host) string {
			if h.Name == "two" {
				return detectionRecord(dir, "different")
			}
			return detectionRecord(dir, "native-id")
		}, false},
		{"ambiguous", func(Host) string { return detectionRecord(dir, "native-id") + detectionRecord(dir, "second") }, false},
		{"not_found", func(Host) string { return "" }, false},
		{"incomplete", func(Host) string { return detectionRecord(dir, "native-id", "--token=hidden") }, false},
		{"unavailable", func(Host) string { return "" }, true},
	}
	for _, c := range cases {
		got, e = s.detectJpomJava(context.Background(), v, func(_ context.Context, h Host, _ string) (string, error) {
			if c.fail {
				return "private SSH diagnostic", fmt.Errorf("private failure")
			}
			return c.raw(h) + "JPOM_DONE:0\n", nil
		})
		if e != nil || got.Status != c.status || got.Candidate != nil || strings.Contains(got.Message, "private") {
			t.Fatal(c.status, got, e)
		}
	}
	for _, mutate := range []func(*ServiceEnvironment){func(v *ServiceEnvironment) { v.Hosts = []string{"prod"} }, func(v *ServiceEnvironment) { v.Hosts = []string{"one", "one"} }, func(v *ServiceEnvironment) { v.Environment = "prod" }, func(v *ServiceEnvironment) { v.Protected = true }, func(v *ServiceEnvironment) { v.TargetType = "docker-host" }, func(v *ServiceEnvironment) { v.RemoteDir = "/" }} {
		copy := v
		mutate(&copy)
		if _, e := s.detectJpomJava(context.Background(), copy, func(context.Context, Host, string) (string, error) {
			t.Fatal("invalid scope reached SSH")
			return "", nil
		}); e == nil {
			t.Fatal("invalid scope allowed")
		}
	}
}
func TestJpomDetectionRouteRequiresAdminAndDoesNotLeakRuntimeSecrets(t *testing.T) {
	s := detectionStore(t)
	mux := http.NewServeMux()
	calls := 0
	registerJpomDetectionRoutes(mux, s, func(context.Context, Host, string) (string, error) {
		calls++
		return detectionRecord("/old/jar", "native", "--password=hidden-secret") + "JPOM_DONE:0\n", nil
	})
	for _, role := range []string{"member", "admin"} {
		r := httptest.NewRequest("POST", "/api/services/api/environments/test/java-detection", strings.NewReader(`{"target_type":"ssh-jar","remote_dir":"/old/jar","hosts":["one"]}`))
		r.Header.Set("X-User", "alice")
		r.Header.Set("X-Role", role)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		want := 200
		if role == "member" {
			want = 403
		}
		if w.Code != want || strings.Contains(w.Body.String(), "hidden-secret") {
			t.Fatal(role, w.Code, w.Body.String())
		}
	}
	if calls != 1 {
		t.Fatal("unauthorized probe", calls)
	}
}
