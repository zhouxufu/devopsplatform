package deploy

import (
	"encoding/json"
	"fmt"
	"logplatform/pkg/deploytask"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func addWildcardService(t *testing.T, s *Store, name, env, host, root string) {
	t.Helper()
	if s.GetHost(host) == nil {
		if err := s.UpsertHost(Host{Name: host, Environment: env, Addr: "192.0.2.2", User: "root", HostKey: "SHA256:isolated"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UpsertService(Service{Name: name, ArtifactType: ArtifactJar, BuildCmd: "true"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertServiceEnvironment(ServiceEnvironment{Service: name, Environment: env, Hosts: []string{host}, TargetType: "ssh-jar", RemoteDir: root, DeployConfig: map[string]string{"java_mode": "managed", "java_version": "8", "java_port": "18888"}}); err != nil {
		t.Fatal(err)
	}
}

func TestEnrollmentWildcardResolvesOnlyBoundManagedTargets(t *testing.T) {
	s, h, _, _, _ := enrollmentFixture(t)
	addWildcardService(t, s, "second", "qa", "node", "/isolated/second")
	addWildcardService(t, s, "shared", "qa", "node", "/isolated/orders")
	addWildcardService(t, s, "unbound", "qa", "elsewhere", "/isolated/unbound")
	if err := s.UpsertEnvironment(Environment{Name: "dev", DisplayName: "开发", Purpose: "development"}); err != nil {
		t.Fatal(err)
	}
	addWildcardService(t, s, "other-env", "dev", "dev-node", "/isolated/dev")
	addWildcardService(t, s, "unmanaged", "qa", "node", "/isolated/unmanaged")
	target := s.GetServiceEnvironment("unmanaged", "qa")
	target.DeployConfig["java_mode"] = "custom"
	if err := s.UpsertServiceEnvironment(*target); err != nil {
		t.Fatal(err)
	}
	input := enrollmentInput{Services: []string{"*"}, URL: "http://127.0.0.1:19803/api/deployment-agent"}
	services, roots, err := s.validateEnrollment(h, input)
	if err != nil || !reflect.DeepEqual(services, []string{"*"}) || !reflect.DeepEqual(roots, []string{"/isolated/orders", "/isolated/second"}) {
		t.Fatal(services, roots, err)
	}
	for _, bad := range [][]string{{"*", "orders"}, {"*", "*"}, {"ord*"}, {"unbound"}, {"other-env"}, {"unmanaged"}, {}} {
		input.Services = bad
		if _, _, err = s.validateEnrollment(h, input); err == nil {
			t.Fatal("invalid scope accepted", bad)
		}
	}
	h.Name = "no-bindings"
	input.Services = []string{"*"}
	if _, _, err = s.validateEnrollment(h, input); err == nil {
		t.Fatal("empty wildcard produced a directory policy")
	}
	h.Name = "node"
	target = s.GetServiceEnvironment("second", "qa")
	target.RemoteDir = "/"
	// Exercise the enrollment boundary independently of environment editor validation.
	if _, err = s.db.Exec(`UPDATE service_environments SET remote_dir='/' WHERE service='second' AND environment='qa'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.validateEnrollment(h, input); err == nil {
		t.Fatal("wildcard allowed filesystem root")
	}
}

func TestEnrollmentWildcardHandlesMoreThanExplicitSelectionLimit(t *testing.T) {
	s, h, _, _, _ := enrollmentFixture(t)
	for i := 0; i < 33; i++ {
		name := fmt.Sprintf("app-%02d", i)
		addWildcardService(t, s, name, "qa", "node", "/isolated/"+name)
	}
	services, roots, err := s.validateEnrollment(h, enrollmentInput{Services: []string{"*"}, URL: "http://127.0.0.1:19803/api/deployment-agent"})
	if err != nil || len(roots) != 34 || !reflect.DeepEqual(services, []string{"*"}) {
		t.Fatal(services, len(roots), err)
	}
}

func TestEnrollmentWildcardReentryReusesSettingsAndRefreshesRoots(t *testing.T) {
	s, h, tr, mutations, token := enrollmentFixture(t)
	input := enrollmentInput{Services: []string{"*"}, URL: "http://127.0.0.1:19803/custom/deployment-agent", RunUser: "deployuser", RunGroup: "deploygroup"}
	v, err := s.prepareEnrollment(h, input, "admin", tr)
	if err != nil {
		t.Fatal(err)
	}
	// A binding changed after preview must not be installed silently.
	addWildcardService(t, s, "later", "qa", "node", "/isolated/later")
	if _, err = s.applyEnrollment(h, v.OperationID, "admin", tr); err == nil || *mutations != 0 {
		t.Fatal("stale wildcard preview installed", err)
	}
	v, err = s.prepareEnrollment(h, enrollmentInput{}, "admin", tr)
	if err != nil || !reflect.DeepEqual(v.Plan.Services, []string{"*"}) || v.Plan.URL != input.URL || v.Plan.RunUser != input.RunUser || v.Plan.RunGroup != input.RunGroup || len(v.Plan.Scope.Roots) != 2 {
		t.Fatal(v, err)
	}
	v, err = s.applyEnrollment(h, v.OperationID, "admin", tr)
	if err != nil {
		t.Fatal(err)
	}
	proof := deploytask.EnrollmentProof{EnrollmentScope: v.Plan.Scope, WorkerVerified: true}
	if err = s.acceptEnrollment(proof, *token); err != nil {
		t.Fatal(err)
	}
	oldToken := *token
	id := proof.AgentID
	original := tr.exec
	tr.exec = func(h Host, cmd string, d time.Duration) (string, error) {
		if cmd == enrollmentInspect {
			return fmt.Sprintf("DEVOPS_ENROLL platform Linux x86_64 systemd\nDEVOPS_ENROLL config abc\nDEVOPS_ENROLL binary def\nDEVOPS_ENROLL unit ghi\nDEVOPS_ENROLL identity %s\nDEVOPS_ENROLL user deployuser\nDEVOPS_ENROLL group deploygroup\n", id), nil
		}
		return original(h, cmd, d)
	}
	addWildcardService(t, s, "new-app", "qa", "node", "/isolated/new-app")
	sv := s.GetServiceEnvironment("new-app", "qa").Apply(s.GetService("new-app"))
	sv.DeployConfig["deployment_agent_id"] = id
	if !s.deploymentAgentAllowsService(s.deploymentAgent(id), "new-app") {
		t.Fatal("wildcard became a frozen application list")
	}
	if err = s.validateDeploymentAgent(sv, h); err == nil || !strings.Contains(err.Error(), "重新接入") {
		t.Fatal("new root did not request policy update", err)
	}
	v, err = s.prepareEnrollment(h, enrollmentInput{}, "admin", tr)
	if err != nil || v.Plan.Scope.AgentID != id || len(v.Plan.Scope.Roots) != 3 || v.Plan.URL != input.URL || v.Plan.RunUser != input.RunUser || v.Plan.RunGroup != input.RunGroup {
		t.Fatal(v, err)
	}
	if !s.authorizeDeploymentAgent(id, oldToken) {
		t.Fatal("reentry preview revoked active key")
	}
	if err = s.validateDeploymentAgent(sv, h); err == nil {
		t.Fatal("unconfirmed preview granted a new directory to the active key")
	}
	existing := s.GetServiceEnvironment("orders", "qa").Apply(s.GetService("orders"))
	existing.DeployConfig["deployment_agent_id"] = id
	if err = s.validateDeploymentAgent(existing, h); err != nil {
		t.Fatal("preview interrupted existing directory authorization", err)
	}
	// Replacing a preview must carry the same active roots, not the prior
	// preview's proposed roots.
	v, err = s.prepareEnrollment(h, enrollmentInput{}, "admin", tr)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.validateDeploymentAgent(sv, h); err == nil {
		t.Fatal("repeated preview promoted unconfirmed roots")
	}
	// Explicitly clearing selection must not reuse or broaden the saved scope.
	if _, err = s.prepareEnrollment(h, enrollmentInput{Services: []string{}}, "admin", tr); err == nil {
		t.Fatal("empty explicit scope reused prior authorization")
	}
	v, err = s.applyEnrollment(h, v.OperationID, "admin", tr)
	if err != nil {
		t.Fatal(err)
	}
	if !s.authorizeDeploymentAgent(id, oldToken) {
		t.Fatal("rotation retired active key before proof")
	}
	if err = s.acceptEnrollment(deploytask.EnrollmentProof{EnrollmentScope: v.Plan.Scope, WorkerVerified: true}, *token); err != nil {
		t.Fatal(err)
	}
	if s.authorizeDeploymentAgent(id, oldToken) || !s.authorizeDeploymentAgent(id, *token) {
		t.Fatal("rotation failed")
	}
	if err = s.validateDeploymentAgent(sv, h); err != nil {
		t.Fatal("reused wildcard setting cannot deploy newly authorized root", err)
	}
	if !reflect.DeepEqual(s.deploymentAgent(id).Services, []string{"*"}) {
		t.Fatal("wildcard expanded in saved registration")
	}
	n := *mutations
	if _, err = s.applyEnrollment(h, v.OperationID, "admin", tr); err != nil || *mutations != n {
		t.Fatal("repeated confirmation replayed installation")
	}
	// Closing and reopening the database preserves the wildcard and reentry settings.
	dir := s.dataDir
	s.Close()
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	saved := reopened.enrollment(h.Name)
	if saved.Plan.URL != input.URL || !reflect.DeepEqual(saved.Plan.Services, []string{"*"}) || saved.Plan.RunUser != input.RunUser {
		t.Fatal("restart lost settings", saved)
	}
}

func TestDeploymentAgentWildcardDiscoveryBindingAndIsolation(t *testing.T) {
	s, h, _, _, _ := enrollmentFixture(t)
	id := "agent_wildcard"
	if _, err := s.registerDeploymentAgent(deploymentAgentRegistration{AgentID: id, Host: h.Name, Environment: h.Environment, Services: []string{"*"}}); err != nil {
		t.Fatal(err)
	}
	addWildcardService(t, s, "later", "qa", "node", "/isolated/later")
	addWildcardService(t, s, "unbound", "qa", "other", "/isolated/unbound")
	a := s.deploymentAgent(id)
	if !s.deploymentAgentAllowsService(a, "orders") || !s.deploymentAgentAllowsService(a, "later") || s.deploymentAgentAllowsService(a, "unbound") || s.deploymentAgentAllowsService(a, "unknown") || s.deploymentAgentAllowsService(a, "*") {
		t.Fatal("wildcard host or application boundary failed")
	}
	disabled := *a
	disabled.Enabled = false
	if s.deploymentAgentAllowsService(&disabled, "orders") {
		t.Fatal("disabled wildcard discovered")
	}
	wrongEnv := *a
	wrongEnv.Environment = "dev"
	if s.deploymentAgentAllowsService(&wrongEnv, "orders") {
		t.Fatal("wildcard crossed environments")
	}
	target := s.GetServiceEnvironment("later", "qa")
	target.DeployConfig["deployment_transport"] = "agent"
	if err := s.freezeDeploymentAgents(target); err != nil {
		t.Fatal("wildcard not resolved for frozen host map", err)
	}
	resolved, err := deploymentAgentID(target.DeployConfig, h.Name)
	if err != nil || resolved != id {
		t.Fatal(resolved, err)
	}
	mux := http.NewServeMux()
	registerDeploymentAgentRoutes(mux, NewReleaseManager(s, nil))
	for service, want := range map[string]int{"orders": 1, "later": 1, "unbound": 0, "unknown": 0} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/deployment-agents?environment=qa&service="+service, nil))
		var rows []deploymentAgentRegistration
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &rows) != nil || len(rows) != want {
			t.Fatal(service, w.Code, w.Body.String())
		}
	}
	if _, err = s.registerDeploymentAgent(deploymentAgentRegistration{AgentID: "agent_invalid", Host: h.Name, Environment: h.Environment, Services: []string{"*", "orders"}}); err == nil {
		t.Fatal("mixed wildcard registration accepted")
	}
}
