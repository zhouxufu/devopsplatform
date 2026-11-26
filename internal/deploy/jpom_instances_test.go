//go:build linux || darwin

package deploy

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const jpomTestBoot = "12345678-1234-1234-1234-123456789abc"

func jpomStatusFixture(state string, pid int, ticks, hash string) string {
	return fmt.Sprintf("state=%s\npid=%d\nticks=%s\nboot=%s\njar_hash=%s\ndirectory=/old/jar\njava_version=1.8.0_181\n", state, pid, ticks, jpomTestBoot, hash)
}
func jpomInstanceFixture(t *testing.T) (*Store, *instanceManager, *ServiceEnvironment, *Service, Host, *string) {
	t.Helper()
	st := detectionStore(t)
	config := map[string]string{"java_mode": "jpom", "java_version": "8", "jpom_project_id": "original-project", "jpom_jar_name": "original.jar", "jpom_log_file": "/old/console.log"}
	if e := st.UpsertServiceEnvironment(ServiceEnvironment{Service: "api", Environment: "test", TargetType: "ssh-jar", RemoteDir: "/old/jar", Hosts: []string{"one"}, DeployConfig: config, RuntimeEnv: map[string]string{"JASYPT_ENCRYPTOR_PASSWORD": "fixture-env-secret"}}); e != nil {
		t.Fatal(e)
	}
	state := jpomStatusFixture("running", 42, "100", strings.Repeat("a", 64))
	m := &instanceManager{st: st, exec: func(Host, string, time.Duration) (string, error) { return state, nil }}
	target, sv, hosts, e := m.target("api", "test")
	if e != nil {
		t.Fatal(e)
	}
	return st, m, target, sv, hosts[0], &state
}
func TestJpomInstanceManagementRecognizesOriginalWithoutPlatformReleaseAndRejectsAliases(t *testing.T) {
	st, m, target, sv, h, state := jpomInstanceFixture(t)
	r := m.inspect(target, sv, h)
	if !r.Owned || !r.Controllable || r.Fingerprint == "" || r.Version != "" || r.DeployID != 0 || r.PID != 42 || r.JavaVersion != "1.8.0_181" {
		t.Fatalf("bad shared instance %+v", r)
	}
	*state = jpomStatusFixture("stopped", 0, "0", strings.Repeat("a", 64))
	if !m.inspect(target, sv, h).Controllable {
		t.Fatal("cannot start verified original JAR")
	}
	*state = "state=unknown\n"
	if m.inspect(target, sv, h).Owned {
		t.Fatal("unknown instance granted ownership")
	}
	*state = jpomStatusFixture("running", 42, "100", strings.Repeat("a", 64))
	st.UpsertHost(Host{Name: "alias", Environment: "test", Addr: h.Addr + ":22", User: h.User})
	st.UpsertService(Service{Name: "other", ArtifactType: "jar", BuildCmd: "true"})
	other := *target
	other.Service = "other"
	other.Hosts = []string{"alias"}
	if e := st.UpsertServiceEnvironment(other); e != nil {
		t.Fatal(e)
	}
	if m.inspect(target, sv, h).Owned {
		t.Fatal("duplicate binding allowed control or logs")
	}
	st.db.Exec(`UPDATE environments SET protected=1 WHERE name='test'`)
	if _, _, _, e := m.target("api", "test"); e == nil {
		t.Fatal("Jpom controls allowed in a protected environment")
	}
}
func TestJpomInstanceControlUsesFreshIdentityExactAckAndNeverReplays(t *testing.T) {
	st, m, target, sv, h, state := jpomInstanceFixture(t)
	initial := m.inspect(target, sv, h)
	calls := 0
	fail := false
	m.exec = func(_ Host, cmd string, _ time.Duration) (string, error) {
		if strings.Contains(cmd, "JPOM_INSTANCE_ACK:") {
			calls++
			if strings.Contains(cmd, "flock") || strings.Contains(cmd, "kill -9") || !strings.Contains(cmd, "/usr/bin/env") || !strings.Contains(cmd, "fixture-env-secret") {
				t.Fatal("wrong process/environment/lock contract")
			}
			*state = jpomStatusFixture("running", 43, "200", strings.Repeat("a", 64))
			if fail {
				return "", fmt.Errorf("connection lost")
			}
			id := jpomInstanceIdentity{PID: 43, Ticks: "200", Boot: jpomTestBoot, JarHash: strings.Repeat("a", 64), Directory: "/old/jar", State: "running"}
			return "JPOM_INSTANCE_ACK:" + id.signature() + "\n", nil
		}
		return *state, nil
	}
	req := instanceActionRequest{Fingerprint: initial.Fingerprint, RequestID: "jpom-restart-once-12345"}
	if _, e := m.control("api", "test", h, "restart", "alice", req); e != nil {
		t.Fatal(e)
	}
	if _, e := m.control("api", "test", h, "restart", "alice", req); e == nil || calls != 1 {
		t.Fatal("duplicate was replayed")
	}
	req.RequestID = "jpom-stale-request-12345"
	if _, e := m.control("api", "test", h, "restart", "alice", req); e == nil || calls != 1 {
		t.Fatal("stale process was restarted")
	}
	req.Fingerprint = m.inspect(target, sv, h).Fingerprint
	req.RequestID = "jpom-uncertain-request-12345"
	fail = true
	if _, e := m.control("api", "test", h, "restart", "alice", req); e == nil {
		t.Fatal("lost ACK reported success")
	}
	var recorded string
	st.db.QueryRow(`SELECT state FROM instance_operations WHERE request_id=?`, req.RequestID).Scan(&recorded)
	if recorded != "uncertain" {
		t.Fatal(recorded)
	}
	req.RequestID = "jpom-next-request-123456"
	if _, e := m.control("api", "test", h, "restart", "alice", req); e == nil || calls != 2 {
		t.Fatal("uncertain operation replayed")
	}
}
func TestJpomInstanceRoutesAndLogsKeepSecretsInsideRedactor(t *testing.T) {
	_, m, target, sv, h, state := jpomInstanceFixture(t)
	initial := m.inspect(target, sv, h)
	encoded := func(v string) string { return base64.StdEncoding.EncodeToString([]byte(v)) }
	raw := "JPOM_LOG1\n" + encoded("java\x00--password=runtime-arg-secret\x00") + "\n" + encoded("API_TOKEN=native-env-secret\x00") + "\n" + encoded("fixture-env-secret runtime-arg-secret native-env-secret token=other-secret\n") + "\n"
	m.exec = func(_ Host, cmd string, _ time.Duration) (string, error) {
		if strings.Contains(cmd, "JPOM_LOG1") {
			if !strings.Contains(cmd, "check_original") || !strings.Contains(cmd, "fd/1") {
				t.Fatal("unguarded log read")
			}
			return raw, nil
		}
		return *state, nil
	}
	log, e := m.logs(target, sv, h, initial.Fingerprint)
	if e != nil {
		t.Fatal(e)
	}
	for _, secret := range []string{"fixture-env-secret", "runtime-arg-secret", "native-env-secret", "other-secret"} {
		if strings.Contains(log, secret) {
			t.Fatal("secret returned in logs")
		}
	}
	if _, e := m.logs(target, sv, h, "stale"); e == nil {
		t.Fatal("stale log identity accepted")
	}
	if _, _, e := parseJpomInstanceLog(raw + "extra"); e == nil {
		t.Fatal("malformed secret/log protocol accepted")
	}
	mux := http.NewServeMux()
	registerInstanceRoutes(mux, m.st, m.exec)
	r := httptest.NewRequest("POST", "/api/services/api/environments/test/instances/one/stop", strings.NewReader(`{}`))
	r.Header.Set("X-User", "member")
	r.Header.Set("X-Role", "member")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}
func TestJpomInstanceCommandsGuardProcessGenerationJarAndForeignProcesses(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "library with spaces")
	proc := filepath.Join(root, "proc")
	for _, p := range []string{dir, proc + "/sys/kernel/random", root + "/jdk/bin"} {
		os.MkdirAll(p, 0700)
	}
	os.WriteFile(proc+"/sys/kernel/random/boot_id", []byte(jpomTestBoot), 0600)
	os.WriteFile(dir+"/original-service.jar", []byte("original-jar"), 0600)
	os.WriteFile(root+"/jdk/release", []byte("JAVA_VERSION=\"1.8.0_181\"\n"), 0600)
	sv := jpomFixture(dir)
	sv.DeployConfig["java_home"] = root + "/jdk"
	sv.DeployConfig["jpom_log_file"] = root + "/console.log"
	os.WriteFile(root+"/console.log", []byte("password=fixture\n"), 0600)
	statLine := func(pid int, tick string) string {
		fields := []string{"S"}
		for i := 0; i < 18; i++ {
			fields = append(fields, "0")
		}
		fields = append(fields, tick)
		return fmt.Sprintf("%d (fixture name) %s\n", pid, strings.Join(fields, " "))
	}
	spawn := func(base string) (*exec.Cmd, string) {
		c := exec.Command("sleep", "60")
		if e := c.Start(); e != nil {
			t.Fatal(e)
		}
		done := make(chan struct{})
		go func() { c.Wait(); close(done) }()
		t.Cleanup(func() { c.Process.Kill(); <-done })
		p := filepath.Join(proc, strconv.Itoa(c.Process.Pid))
		os.MkdirAll(p+"/fd", 0700)
		args := []string{"java", "-DJpom.application=old-project-id", "-DJpom.basedir=" + base, "-jar", filepath.Join(base, "original-service.jar")}
		os.WriteFile(p+"/cmdline", []byte(strings.Join(args, "\x00")+"\x00"), 0600)
		os.WriteFile(p+"/stat", []byte(statLine(c.Process.Pid, "123")), 0600)
		os.WriteFile(p+"/environ", []byte("TOKEN=fixture\x00"), 0600)
		os.Symlink(dir, p+"/cwd")
		os.Symlink(root+"/jdk/bin/java", p+"/exe")
		os.Symlink(root+"/console.log", p+"/fd/1")
		return c, p
	}
	original, pidPath := spawn(dir)
	foreign, _ := spawn(root + "/other")
	inspect := func() applicationInstance {
		return inspectJpomInstance(sv, Host{}, func(Host, string, time.Duration) (string, error) {
			out, e := exec.Command("bash", "-c", jpomInstanceStatusCommand(sv, proc)).CombinedOutput()
			return string(out), e
		})
	}
	r := inspect()
	if !r.Owned || r.PID != original.Process.Pid {
		t.Fatalf("native process not recognized: %+v", r)
	}
	originalCmdline, _ := os.ReadFile(pidPath + "/cmdline")
	wrongProject := strings.Replace(string(originalCmdline), "-DJpom.application=old-project-id", "-DJpom.application=different-project", 1)
	os.WriteFile(pidPath+"/cmdline", []byte(wrongProject), 0600)
	if inspect().Owned {
		t.Fatal("same JAR with a foreign project was treated as stopped/controllable")
	}
	duplicate := strings.Replace(string(originalCmdline), "-jar\x00", "-DJpom.application=other\x00-jar\x00", 1)
	os.WriteFile(pidPath+"/cmdline", []byte(duplicate), 0600)
	if inspect().Owned {
		t.Fatal("duplicate JVM ownership properties were accepted")
	}
	os.WriteFile(pidPath+"/cmdline", originalCmdline, 0600)
	logCommand, err := jpomInstanceLogCommand(sv, r, proc)
	if err != nil {
		t.Fatal(err)
	}
	rawLog, err := exec.Command("bash", "-c", logCommand).CombinedOutput()
	if err != nil {
		t.Fatal(err, string(rawLog))
	}
	body, secrets, err := parseJpomInstanceLog(string(rawLog))
	if err != nil || strings.Contains(redactInstanceLog(body, secrets), "fixture") {
		t.Fatal("guarded native log redaction failed", err)
	}
	command, e := jpomInstanceControlCommand(sv, r, "stop", nil, proc)
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(pidPath+"/stat", []byte(statLine(original.Process.Pid, "124")), 0600)
	if out, e := exec.Command("bash", "-c", command).CombinedOutput(); e == nil {
		t.Fatal("PID generation drift allowed", string(out))
	}
	os.WriteFile(pidPath+"/stat", []byte(statLine(original.Process.Pid, "123")), 0600)
	os.WriteFile(dir+"/original-service.jar", []byte("different-jar"), 0600)
	if _, e := exec.Command("bash", "-c", command).CombinedOutput(); e == nil {
		t.Fatal("JAR replacement allowed")
	}
	if syscall.Kill(original.Process.Pid, 0) != nil || syscall.Kill(foreign.Process.Pid, 0) != nil {
		t.Fatal("rejected command signaled a process")
	}
	os.WriteFile(dir+"/original-service.jar", []byte("original-jar"), 0600)
	out, e := exec.Command("bash", "-c", command).CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	next := inspect()
	if next.State != "stopped" || jpomInstanceAck(string(out)) != next.jpom.signature() {
		t.Fatal("stop was not acknowledged")
	}
	if syscall.Kill(foreign.Process.Pid, 0) != nil {
		t.Fatal("foreign process stopped")
	}
	// Isolated fake Java proves launch argv and environment remain literal and
	// application variables cannot rewrite the control script's proc/root variables.
	fake := "#!/bin/bash\np=" + shellQuote(proc) + "/$$\nmkdir -p \"$p/fd\"\nprintf '%s\\0' \"$0\" \"$@\" > \"$p/cmdline\"\nprintf '%s\\0' \"TOKEN=$TOKEN\" > \"$p/environ\"\nprintf '%s\\n' \"$$ (fixture) S " + strings.Repeat("0 ", 18) + "999\" > \"$p/stat\"\nln -s " + shellQuote(dir) + " \"$p/cwd\"\nln -s " + shellQuote(root+"/jdk/bin/java") + " \"$p/exe\"\nln -s " + shellQuote(root+"/console.log") + " \"$p/fd/1\"\nprintf '%s\\n' \"$$\" > " + shellQuote(root+"/launched-pid") + "\nprintf '%s\\n' \"$proc\" \"$TOKEN\" > " + shellQuote(root+"/received-env") + "\nexec sleep 60\n"
	t.Cleanup(func() {
		b, _ := os.ReadFile(root + "/launched-pid")
		pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
		if pid > 0 {
			syscall.Kill(pid, syscall.SIGTERM)
		}
	})
	os.WriteFile(root+"/jdk/bin/java", []byte(fake), 0700)
	command, e = jpomInstanceControlCommand(sv, next, "start", map[string]string{"proc": "application-proc-value", "TOKEN": "$(touch SHOULD_NOT_EXIST)"}, proc)
	if e != nil {
		t.Fatal(e)
	}
	out, e = exec.Command("bash", "-c", command).CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	running := inspect()
	if running.PID == 0 || running.State != "running" || jpomInstanceAck(string(out)) != running.jpom.signature() {
		t.Fatal("start was not verified")
	}
	t.Cleanup(func() { syscall.Kill(running.PID, syscall.SIGTERM) })
	b, _ := os.ReadFile(root + "/received-env")
	if string(b) != "application-proc-value\n$(touch SHOULD_NOT_EXIST)\n" {
		t.Fatalf("runtime variables changed: %s", b)
	}
	if _, e := os.Stat(dir + "/SHOULD_NOT_EXIST"); !os.IsNotExist(e) {
		t.Fatal("runtime variable executed shell expression")
	}
}
