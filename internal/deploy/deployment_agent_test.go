package deploy

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"logplatform/pkg/deploytask"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func deploymentBrokerFixture(t *testing.T) (*Store, *ReleaseManager, *http.ServeMux, string, *Artifact) {
	t.Helper()
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	if e = s.UpsertEnvironment(Environment{Name: "qa", DisplayName: "隔离"}); e != nil {
		t.Fatal(e)
	}
	if e = s.UpsertService(Service{Name: "api", ArtifactType: ArtifactJar, BuildCmd: "true"}); e != nil {
		t.Fatal(e)
	}
	if e = s.UpsertHost(Host{Name: "fixture", Environment: "qa", Addr: "192.0.2.1:1", User: "never-connect"}); e != nil {
		t.Fatal(e)
	}
	token, e := s.registerDeploymentAgent(deploymentAgentRegistration{AgentID: "agent_fixture", Host: "fixture", Environment: "qa", Services: []string{"api"}})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.UpsertServiceEnvironment(ServiceEnvironment{Service: "api", Environment: "qa", Hosts: []string{"fixture"}, TargetType: "ssh-jar", RemoteDir: "/isolated/api", DeployConfig: map[string]string{"java_mode": "managed", "java_version": "8", "java_port": "18888", "deployment_agent_id": "agent_fixture", "health_failure_action": "keep"}, RuntimeEnv: map[string]string{"APP_SECRET": "frozen-private-value"}}); e != nil {
		t.Fatal(e)
	}
	var jar bytes.Buffer
	z := zip.NewWriter(&jar)
	f, e := z.Create("Example.class")
	if e != nil {
		t.Fatal(e)
	}
	f.Write([]byte{0xca, 0xfe, 0xba, 0xbe, 0, 0, 0, 52})
	z.Close()
	file := filepath.Join(t.TempDir(), "app.jar")
	if e = os.WriteFile(file, jar.Bytes(), 0600); e != nil {
		t.Fatal(e)
	}
	a, e := describeArtifact("api", "fixture-v1", ArtifactJar, file, "")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.RegisterArtifact(&a); e != nil {
		t.Fatal(e)
	}
	d := NewDeployer(s, NewLogHub(), "")
	m := NewReleaseManager(s, d)
	mux := http.NewServeMux()
	registerDeploymentAgentRoutes(mux, m)
	return s, m, mux, token, &a
}
func brokerCall(h http.Handler, method, path, agent, token string, input any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(input)
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.Header.Set("X-Deployment-Agent-ID", agent)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func awaitBroker(t *testing.T, f func() bool) {
	t.Helper()
	end := time.Now().Add(5 * time.Second)
	for time.Now().Before(end) {
		if f() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("isolated broker condition not reached")
}

func TestDeploymentAgentReleaseReportsDeduplicateAndRecover(t *testing.T) {
	s, m, h, token, a := deploymentBrokerFixture(t)
	o, e := m.CreateArtifact("api", a.ID, "qa", "isolated task", "tester")
	if e != nil {
		t.Fatal(e)
	}
	var tasks []deploytask.Task
	awaitBroker(t, func() bool {
		w := brokerCall(h, "GET", "/api/deployment-agent/tasks", "agent_fixture", token, nil)
		return w.Code == 200 && json.Unmarshal(w.Body.Bytes(), &tasks) == nil && len(tasks) == 1
	})
	task := tasks[0]
	if task.ArtifactDigest != a.Digest || task.Host != "fixture" || task.Environment != "qa" || task.Files["runtime.env"] != "APP_SECRET='frozen-private-value'\n" {
		t.Fatal(task)
	}
	if _, e = m.CreateArtifact("api", a.ID, "qa", "duplicate must be rejected", "tester"); e == nil {
		t.Fatal("parallel release bypassed reservation")
	}
	if w := brokerCall(h, "GET", "/api/deployment-agent/tasks", "agent_fixture", "log-ingest-key", nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := brokerCall(h, "GET", "/api/deployment-agent/tasks/"+task.ID+"/artifact", "another-agent", token, nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := brokerCall(h, "GET", "/api/deployment-agent/tasks/"+task.ID+"/artifact", "agent_fixture", token, nil); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), mustRead(t, a.URI)) {
		t.Fatal(w.Code)
	}
	report := func(seq int64, state, phase string) *httptest.ResponseRecorder {
		r := deploytask.Report{TaskID: task.ID, TaskDigest: deploytask.Digest(task), Sequence: seq, State: state, Phase: phase, At: 1234}
		return brokerCall(h, "POST", "/api/deployment-agent/reports", "agent_fixture", token, r)
	}
	if w := report(1, "accepted", "accepted"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := report(1, "accepted", "accepted"); w.Code != 200 {
		t.Fatal("duplicate ACK lost", w.Body.String())
	}
	if w := report(1, "failed", "changed"); w.Code != 409 {
		t.Fatal("conflicting report accepted")
	}
	if w := report(3, "success", "verified"); w.Code != 409 {
		t.Fatal("sequence gap accepted")
	}
	m.sweepAgentTasks(time.Now().Unix() + 121)
	if got := s.GetDeploy(task.DeployID); got.Status != "needs_review" || got.EndedAt != 0 {
		t.Fatal(got)
	}
	if got := s.GetReleaseOrder(o.ID); got.Status != "needs_review" || got.Failed != 0 || got.EndedAt != 0 {
		t.Fatal(got)
	}
	if w := report(2, "starting", "verifying"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if s.GetDeploy(task.DeployID).Status != "needs_review" {
		t.Fatal("warning cleared without positive readiness evidence")
	}
	m.Recover()
	if s.GetDeploy(task.DeployID).Status != "needs_review" || s.GetReleaseOrder(o.ID).Status != "needs_review" {
		t.Fatal("controller restart lost pending task")
	}
	if w := report(3, "success", "verified-after-recovery"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	awaitBroker(t, func() bool { return s.GetReleaseOrder(o.ID).Status == "success" })
	if w := report(3, "success", "verified-after-recovery"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := report(4, "failed", "late"); w.Code != 409 {
		t.Fatal("terminal result overwritten")
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM agent_deployment_reports WHERE task_id=?`, task.ID).Scan(&n)
	if n != 3 {
		t.Fatal("duplicate persisted report", n)
	}
	var raw string
	s.db.QueryRow(`SELECT plan_cipher FROM agent_deployment_tasks WHERE task_id=?`, task.ID).Scan(&raw)
	if strings.Contains(raw, "frozen-private-value") {
		t.Fatal("task secrets persisted in plaintext")
	}
}
func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	return b
}

func TestDeploymentAgentScopeRotationAndRestartPersistence(t *testing.T) {
	s, m, h, token, a := deploymentBrokerFixture(t)
	o, e := m.CreateArtifact("api", a.ID, "qa", "fixture", "tester")
	if e != nil {
		t.Fatal(e)
	}
	var tasks []deploytask.Task
	awaitBroker(t, func() bool { tasks, _ = s.pendingAgentTasks("agent_fixture"); return len(tasks) == 1 })
	if _, e = s.registerDeploymentAgent(deploymentAgentRegistration{AgentID: "agent_fixture", Host: "different", Environment: "qa", Services: []string{"api"}}); e == nil {
		t.Fatal("Agent identity moved")
	}
	newToken, e := s.registerDeploymentAgent(deploymentAgentRegistration{AgentID: "agent_fixture", Host: "fixture", Environment: "qa", Services: []string{"api"}})
	if e != nil {
		t.Fatal(e)
	}
	if w := brokerCall(h, "GET", "/api/deployment-agent/tasks", "agent_fixture", token, nil); w.Code != 401 {
		t.Fatal("rotated key still accepted")
	}
	if w := brokerCall(h, "GET", "/api/deployment-agent/tasks", "agent_fixture", newToken, nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if e = s.recoverInterrupted(); e != nil {
		t.Fatal(e)
	}
	m.Recover()
	if s.GetReleaseOrder(o.ID).Status != "needs_review" {
		t.Fatal(s.GetReleaseOrder(o.ID))
	}
	if pending, e := s.pendingAgentTasks("agent_fixture"); e != nil || len(pending) != 1 || pending[0].ID != tasks[0].ID {
		t.Fatal("durable task identity lost", e)
	}
	if got := s.ActiveDeploy("api", "fixture"); got == nil {
		t.Fatal("restart released task reservation")
	}
	// Registration cannot be performed using only the Agent credential.
	if w := brokerCall(h, "POST", "/api/deployment-agents", "agent_fixture", newToken, map[string]any{}); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if fmt.Sprint(o.ArtifactID) != fmt.Sprint(a.ID) {
		t.Fatal(o)
	}
}

func TestManagedJavaTimeoutIsReviewWithoutStopOrRollback(t *testing.T) {
	for _, failure := range []error{javaTestExit(75), fmt.Errorf("SSH connection lost")} {
		sv := managedJavaFixture()
		d := NewDeployer(nil, NewLogHub(), "")
		commands := []string{}
		r := javaRemote{exec: func(_ Host, cmd string, _ time.Duration) (string, error) {
			commands = append(commands, cmd)
			if strings.Contains(cmd, "then readlink") {
				return "", nil
			}
			if strings.HasSuffix(cmd, "/deploy-7/service.sh' ready") {
				return "still starting", failure
			}
			return "", nil
		}, upload: func(Host, string, string) error { return nil }, write: func(Host, []byte, string) error { return nil }}
		if status := d.deployManagedJavaRemote(7, sv, Host{}, "fixture", r); status != "needs_review" {
			t.Fatal(status)
		}
		for _, cmd := range commands {
			if strings.Contains(cmd, "service.sh' stop") || strings.Contains(cmd, "refusing to remove lock owned") {
				t.Fatal("timeout stopped a process or removed reservation", cmd)
			}
		}
	}
}

func TestDeploymentAgentDatabaseReopenAndRevocation(t *testing.T) {
	s, m, h, token, a := deploymentBrokerFixture(t)
	o, e := m.CreateArtifact("api", a.ID, "qa", "isolated reopen", "tester")
	if e != nil {
		t.Fatal(e)
	}
	var task deploytask.Task
	awaitBroker(t, func() bool {
		tasks, e := s.pendingAgentTasks("agent_fixture")
		if e == nil && len(tasks) == 1 {
			task = tasks[0]
			return true
		}
		return false
	})
	r := deploytask.Report{TaskID: task.ID, TaskDigest: deploytask.Digest(task), Sequence: 1, State: "accepted", Phase: "accepted", At: 123}
	if w := brokerCall(h, "POST", "/api/deployment-agent/reports", "agent_fixture", token, r); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	dataDir := s.dataDir
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := Open(dataDir)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	recovered := NewReleaseManager(reopened, NewDeployer(reopened, NewLogHub(), ""))
	recovered.Recover()
	mux := http.NewServeMux()
	registerDeploymentAgentRoutes(mux, recovered)
	tasks, e := reopened.pendingAgentTasks("agent_fixture")
	if e != nil || len(tasks) != 1 || deploytask.Digest(tasks[0]) != deploytask.Digest(task) {
		t.Fatal("frozen task lost on database reopen", e)
	}
	if w := brokerCall(mux, "POST", "/api/deployment-agent/reports", "agent_fixture", token, r); w.Code != 200 {
		t.Fatal("durable duplicate ACK lost", w.Body.String())
	}
	if _, e = recovered.CreateArtifact("api", a.ID, "qa", "duplicate", "tester"); e == nil {
		t.Fatal("restart released uncertain task reservation")
	}
	wrong := r
	wrong.Sequence = 2
	wrong.TaskDigest = "changed"
	if w := brokerCall(mux, "POST", "/api/deployment-agent/reports", "agent_fixture", token, wrong); w.Code != 409 {
		t.Fatal("changed task digest accepted")
	}
	req := httptest.NewRequest("DELETE", "/api/deployment-agents/agent_fixture", nil)
	req.Header.Set("X-Role", "admin")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w = brokerCall(mux, "GET", "/api/deployment-agent/tasks", "agent_fixture", token, nil); w.Code != 401 {
		t.Fatal("disabled credential retained access")
	}
	if reopened.GetReleaseOrder(o.ID).Status != "needs_review" {
		t.Fatal("disabling Agent falsely finished release")
	}
}

func TestDeploymentAgentPolicyScopePreflight(t *testing.T) {
	s, m, _, _, _ := deploymentBrokerFixture(t)
	target := s.GetServiceEnvironment("api", "qa")
	target.DeployConfig["health_failure_action"] = "rollback"
	if e := s.UpsertServiceEnvironment(*target); e != nil {
		t.Fatal(e)
	}
	if _, e := m.ValidateTarget("api", "qa", "preserve rollback"); e != nil {
		t.Fatal("rollback policy was not accepted", e)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM agent_deployment_tasks`).Scan(&n)
	if n != 0 {
		t.Fatal("invalid policy enqueued execution")
	}
	target.DeployConfig["health_failure_action"] = "keep"
	target.DeployConfig["deployment_agent_id"] = "unregistered"
	if e := s.UpsertServiceEnvironment(*target); e != nil {
		t.Fatal(e)
	}
	if _, e := m.ValidateTarget("api", "qa", "isolated"); e == nil {
		t.Fatal("invalid Agent accepted before build")
	}
}

func TestDeploymentJavaRecheckRequiresFrozenOriginalIdentity(t *testing.T) {
	s, m, _, _, a := deploymentBrokerFixture(t)
	target := s.GetServiceEnvironment("api", "qa")
	delete(target.DeployConfig, "deployment_agent_id")
	if e := s.UpsertServiceEnvironment(*target); e != nil {
		t.Fatal(e)
	}
	// Create only database records: no SSH driver is started.
	o := &ReleaseOrder{Service: "api", Environment: "qa", ArtifactID: a.ID, Status: "needs_review", Target: *target, Hosts: []string{"fixture"}, NextIndex: 1}
	e := s.CreateReleaseOrder(o)
	if e != nil {
		t.Fatal(e)
	}
	d := &Deploy{Service: "api", Environment: "qa", Host: "fixture", ArtifactID: a.ID, ArtifactDigest: a.Digest, ArtifactURI: a.URI, Status: "running"}
	if e = s.CreateDeploy(d); e != nil {
		t.Fatal(e)
	}
	s.FinishDeploy(d.ID, "needs_review")
	if e = s.AttachReleaseDeploys(o.ID, 1, []int64{d.ID}); e != nil {
		t.Fatal(e)
	}
	calls := 0
	exec := func(h Host, cmd string, _ time.Duration) (string, error) {
		calls++
		if strings.Contains(cmd, "agent-probe") {
			for _, needed := range []string{".deploy-lock/owner", ".devopsplatform-owner", "readlink", "sha256sum", fmt.Sprintf("deploy-%d", d.ID)} {
				if !strings.Contains(cmd, needed) {
					t.Fatalf("missing identity guard: %s", needed)
				}
			}
			if strings.Contains(cmd, " start") || strings.Contains(cmd, " stop") {
				t.Fatal("recheck changed application")
			}
			return "not yet ready", javaTestExit(75)
		}
		t.Fatal("unknown outcome released lease")
		return "", nil
	}
	if m.recheckManagedJava(d.ID, exec) == nil || calls != 1 || s.GetDeploy(d.ID).Status != "needs_review" {
		t.Fatal("unknown probe failed or stopped application")
	}
}
