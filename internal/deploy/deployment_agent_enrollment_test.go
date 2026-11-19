package deploy

import (
	"encoding/json"
	"fmt"
	"logplatform/pkg/deploytask"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func enrollmentFixture(t *testing.T) (*Store, Host, enrollmentTransport, *int, *string) {
	t.Helper()
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	s.UpsertEnvironment(Environment{Name: "qa", DisplayName: "隔离"})
	s.UpsertService(Service{Name: "orders", ArtifactType: ArtifactJar, BuildCmd: "true"})
	h := Host{Name: "node", Environment: "qa", Addr: "192.0.2.1", User: "root", HostKey: "SHA256:isolated"}
	if e = s.UpsertHost(h); e != nil {
		t.Fatal(e)
	}
	if e = s.UpsertServiceEnvironment(ServiceEnvironment{Service: "orders", Environment: "qa", Hosts: []string{"node"}, TargetType: "ssh-jar", RemoteDir: "/isolated/orders", DeployConfig: map[string]string{"java_mode": "managed", "java_version": "8", "java_port": "18888"}}); e != nil {
		t.Fatal(e)
	}
	mutations := 0
	token := ""
	transport := enrollmentTransport{exec: func(_ Host, cmd string, _ time.Duration) (string, error) {
		if cmd == enrollmentInspect {
			return "locale noise\nDEVOPS_ENROLL platform Linux x86_64 systemd\nDEVOPS_ENROLL config -\nDEVOPS_ENROLL binary -\nDEVOPS_ENROLL unit -\n", nil
		}
		mutations++
		return "", nil
	}, upload: func(_ Host, raw []byte, file string) error {
		if strings.HasSuffix(file, "/request.json") {
			var r struct {
				Token string `json:"token"`
			}
			json.Unmarshal(raw, &r)
			token = r.Token
		}
		return nil
	}, binary: func(string) ([]byte, error) { return []byte("synthetic binary"), nil }}
	return s, h, transport, &mutations, &token
}
func TestEnrollmentPreviewConfirmCallbackAndRevoke(t *testing.T) {
	s, h, tr, mutations, token := enrollmentFixture(t)
	input := enrollmentInput{Services: []string{"orders"}, URL: "http://127.0.0.1:19803/api/deployment-agent"}
	v, e := s.prepareEnrollment(h, input, "admin", tr)
	if e != nil {
		t.Fatal(e)
	}
	if *mutations != 0 {
		t.Fatal("preview mutated remote")
	}
	if _, e = s.applyEnrollment(h, v.OperationID, "other", tr); e == nil {
		t.Fatal("another actor used preview")
	}
	v, e = s.applyEnrollment(h, v.OperationID, "admin", tr)
	if e != nil {
		t.Fatal(e)
	}
	if v.Phase != "awaiting_connection" || *mutations != 2 || *token == "" {
		t.Fatal(v, *mutations)
	}
	if _, e = s.applyEnrollment(h, v.OperationID, "admin", tr); e != nil || *mutations != 2 {
		t.Fatal("confirmation replayed installation")
	}
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), *token) || strings.Contains(string(b), "credential") {
		t.Fatal("credential returned to browser")
	}
	if s.authorizeDeploymentAgent(v.Plan.Scope.AgentID, *token) {
		t.Fatal("unproven enrollment can execute tasks")
	}
	proof := deploytask.EnrollmentProof{EnrollmentScope: v.Plan.Scope, WorkerVerified: true}
	wrong := proof
	wrong.Roots = []string{"/"}
	if s.acceptEnrollment(wrong, *token) == nil {
		t.Fatal("widened roots accepted")
	}
	if s.acceptEnrollment(proof, "ingest-token") == nil {
		t.Fatal("wrong credential accepted")
	}
	if e = s.acceptEnrollment(proof, *token); e != nil {
		t.Fatal(e)
	}
	if !s.authorizeDeploymentAgent(proof.AgentID, *token) || s.enrollment(h.Name).Phase != "ready" || s.enrollment(h.Name).credential != "" {
		t.Fatal("verified token not activated or pending secret retained")
	}
	if e = s.acceptEnrollment(proof, *token); e != nil {
		t.Fatal("callback retry failed", e)
	}
	mux := http.NewServeMux()
	registerEnrollmentRoutes(mux, s, tr)
	r := httptest.NewRequest("POST", "/api/hosts/node/deployment/disable", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("non-admin can revoke")
	}
	r = httptest.NewRequest("POST", "/api/hosts/node/deployment/disable", strings.NewReader(`{}`))
	r.Header.Set("X-Role", "admin")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 || s.authorizeDeploymentAgent(proof.AgentID, *token) || s.acceptEnrollment(proof, *token) == nil {
		t.Fatal("disabled enrollment resurrected", w.Body.String())
	}
}

func TestEnrollmentUnknownOutcomeNeverReapplies(t *testing.T) {
	s, h, tr, mutations, token := enrollmentFixture(t)
	original := tr.exec
	tr.exec = func(h Host, cmd string, d time.Duration) (string, error) {
		out, e := original(h, cmd, d)
		if strings.Contains(cmd, "systemctl restart logagent") {
			return "secret must not escape", fmt.Errorf("lost SSH response")
		}
		return out, e
	}
	v, e := s.prepareEnrollment(h, enrollmentInput{Services: []string{"orders"}, URL: "http://127.0.0.1:1/api/deployment-agent"}, "admin", tr)
	if e != nil {
		t.Fatal(e)
	}
	v, e = s.applyEnrollment(h, v.OperationID, "admin", tr)
	if e != nil || v.Phase != "needs_review" {
		t.Fatal(v, e)
	}
	dir := s.dataDir
	s.Close()
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s = reopened
	t.Cleanup(func() { s.Close() })
	if s.enrollment(h.Name).Phase != "needs_review" {
		t.Fatal("restart lost uncertain enrollment")
	}
	n := *mutations
	if _, e = s.applyEnrollment(h, v.OperationID, "admin", tr); e != nil || *mutations != n {
		t.Fatal("unknown side effects repeated")
	}
	if _, e = s.prepareEnrollment(h, enrollmentInput{Services: []string{"orders"}, URL: "http://127.0.0.1:1/api/deployment-agent"}, "admin", tr); e == nil {
		t.Fatal("new preview replaced uncertain operation")
	}
	if e = s.acceptEnrollment(deploytask.EnrollmentProof{EnrollmentScope: v.Plan.Scope, WorkerVerified: true}, *token); e != nil || s.enrollment(h.Name).Phase != "ready" {
		t.Fatal("late callback could not recover original operation", e)
	}
}

func TestEnrollmentRotationKeepsOldCredentialUntilVerified(t *testing.T) {
	s, h, tr, _, token := enrollmentFixture(t)
	input := enrollmentInput{Services: []string{"orders"}, URL: "http://127.0.0.1:1/api/deployment-agent"}
	v, e := s.prepareEnrollment(h, input, "admin", tr)
	if e != nil {
		t.Fatal(e)
	}
	v, e = s.applyEnrollment(h, v.OperationID, "admin", tr)
	if e != nil {
		t.Fatal(e)
	}
	proof := deploytask.EnrollmentProof{EnrollmentScope: v.Plan.Scope, WorkerVerified: true}
	old := *token
	if e = s.acceptEnrollment(proof, old); e != nil {
		t.Fatal(e)
	}
	inspect := tr.exec
	tr.exec = func(h Host, cmd string, d time.Duration) (string, error) {
		if cmd == enrollmentInspect {
			return fmt.Sprintf("DEVOPS_ENROLL platform Linux x86_64 systemd\nDEVOPS_ENROLL config abc\nDEVOPS_ENROLL binary def\nDEVOPS_ENROLL unit ghi\nDEVOPS_ENROLL identity %s\nDEVOPS_ENROLL user logagent\nDEVOPS_ENROLL group logagent\n", proof.AgentID), nil
		}
		return inspect(h, cmd, d)
	}
	v, e = s.prepareEnrollment(h, input, "admin", tr)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.acceptEnrollment(proof, old); e != nil {
		t.Fatal("preview interrupted active Agent", e)
	}
	s.db.Exec(`UPDATE deployment_agents SET last_seen=last_seen+1 WHERE agent_id=?`, proof.AgentID)
	v, e = s.applyEnrollment(h, v.OperationID, "admin", tr)
	if e != nil {
		t.Fatal(e)
	}
	if !s.authorizeDeploymentAgent(proof.AgentID, old) || s.authorizeDeploymentAgent(proof.AgentID, *token) {
		t.Fatal("rotation cut old credential before proof")
	}
	if e = s.acceptEnrollment(deploytask.EnrollmentProof{EnrollmentScope: v.Plan.Scope, WorkerVerified: true}, *token); e != nil {
		t.Fatal(e)
	}
	if s.authorizeDeploymentAgent(proof.AgentID, old) || !s.authorizeDeploymentAgent(proof.AgentID, *token) {
		t.Fatal("rotation did not retire old key")
	}
}

func TestEnrollmentExpiredOrChangedTargetHasNoEffects(t *testing.T) {
	s, h, tr, n, _ := enrollmentFixture(t)
	input := enrollmentInput{Services: []string{"orders"}, URL: "http://127.0.0.1:1/api/deployment-agent"}
	v, e := s.prepareEnrollment(h, input, "admin", tr)
	if e != nil {
		t.Fatal(e)
	}
	v.Plan.Expires = 1
	raw, _ := json.Marshal(v.Plan)
	s.db.Exec(`UPDATE deployment_agent_enrollments SET plan=? WHERE host_name=?`, string(raw), h.Name)
	if _, e = s.applyEnrollment(h, v.OperationID, "admin", tr); e == nil || *n != 0 {
		t.Fatal("expired preview executed")
	}
	v, e = s.prepareEnrollment(h, input, "admin", tr)
	if e != nil {
		t.Fatal(e)
	}
	h.Addr = "192.0.2.2"
	if _, e = s.applyEnrollment(h, v.OperationID, "admin", tr); e == nil || *n != 0 {
		t.Fatal("changed host executed")
	}
}

func TestEnrollmentPrivilegedLauncherRejectsFreeCommands(t *testing.T) {
	p := enrollmentPlan{Scope: deploytask.EnrollmentScope{StateDir: filepath.Join(t.TempDir(), "state")}, RunUser: "logagent", RunGroup: "logagent"}
	for name, body := range map[string]string{"worker": renderEnrollmentLauncher(p), "apply": renderEnrollmentApply(p, "/tmp/isolated-enrollment")} {
		file := filepath.Join(t.TempDir(), name)
		os.WriteFile(file, []byte(body), 0700)
		if b, e := exec.Command("bash", "-n", file).CombinedOutput(); e != nil {
			t.Fatalf("invalid install script %s: %s", name, b)
		}
		if name == "worker" {
			for _, arg := range []string{"../deploy-1", "deploy-1;id", "check-../../root", "--uid=0", "deploy-1 extra"} {
				if exec.Command("bash", file, arg).Run() == nil {
					t.Fatal("free command accepted", arg)
				}
			}
		}
	}
}
