package deploy

import (
	"encoding/json"
	"fmt"
	"logplatform/pkg/deploytask"
	"sync"
	"testing"
)

func agentMultiFixture(t *testing.T, parallel, batch int, pause bool) (*Store, *ReleaseManager, *Artifact) {
	t.Helper()
	s, m, _, _, a := deploymentBrokerFixture(t)
	for i := 2; i <= 3; i++ {
		host := fmt.Sprintf("fixture%d", i)
		if e := s.UpsertHost(Host{Name: host, Environment: "qa", Addr: fmt.Sprintf("192.0.2.%d:1", i), User: "never-connect"}); e != nil {
			t.Fatal(e)
		}
		if _, e := s.registerDeploymentAgent(deploymentAgentRegistration{AgentID: "agent_" + host, Host: host, Environment: "qa", Services: []string{"api"}}); e != nil {
			t.Fatal(e)
		}
	}
	target := s.GetServiceEnvironment("api", "qa")
	target.Hosts = []string{"fixture", "fixture2", "fixture3"}
	delete(target.DeployConfig, "deployment_agent_id")
	target.DeployConfig["deployment_transport"] = "agent"
	target.MaxParallel = parallel
	target.BatchSize = batch
	target.PauseBetweenBatches = pause
	if e := s.UpsertServiceEnvironment(*target); e != nil {
		t.Fatal(e)
	}
	return s, m, a
}
func allAgentTasks(t *testing.T, s *Store) []deploytask.Task {
	t.Helper()
	rows, e := s.db.Query(`SELECT task_id FROM agent_deployment_tasks ORDER BY deploy_id`)
	if e != nil {
		t.Fatal(e)
	}
	ids := []string{}
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	out := []deploytask.Task{}
	for _, id := range ids {
		v, e := s.agentTask(id)
		if e != nil {
			t.Fatal(e)
		}
		out = append(out, v)
	}
	return out
}
func commitAgentReport(t *testing.T, s *Store, m *ReleaseManager, task deploytask.Task, seq int64, state, phase string) {
	t.Helper()
	r := deploytask.Report{TaskID: task.ID, TaskDigest: deploytask.Digest(task), Sequence: seq, State: state, Phase: phase, At: 1234}
	if _, e := s.acceptAgentReport(task.AgentID, r); e != nil {
		t.Fatal(e)
	}
	m.resumeAgentRelease(task.DeployID)
}
func TestDeploymentAgentMultiHostWaitsForReceiptsAndPause(t *testing.T) {
	s, m, a := agentMultiFixture(t, 1, 2, true)
	o, e := m.CreateArtifact("api", a.ID, "qa", "isolated multi-host", "tester")
	if e != nil {
		t.Fatal(e)
	}
	awaitBroker(t, func() bool { return len(allAgentTasks(t, s)) == 1 })
	tasks := allAgentTasks(t, s)
	first := tasks[0]
	if len(s.ReleaseDeployIDs(o.ID)) != 2 || first.Host != "fixture" || first.ReleaseID != o.ID {
		t.Fatal("batch was not frozen before dispatch")
	}
	var bindings map[string]string
	if json.Unmarshal([]byte(s.GetReleaseOrder(o.ID).Target.DeployConfig["deployment_agent_ids"]), &bindings) != nil || len(bindings) != 3 {
		t.Fatal("per-host installation bindings missing")
	}
	commitAgentReport(t, s, m, first, 1, "needs_review", "verifying")
	other := NewReleaseManager(s, NewDeployer(s, NewLogHub(), ""))
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); other.reconcileAgentOrder(o.ID, 2) }()
	}
	wg.Wait()
	if len(allAgentTasks(t, s)) != 1 || s.GetReleaseOrder(o.ID).Status != "needs_review" {
		t.Fatal("unknown result advanced another host")
	}
	commitAgentReport(t, s, m, first, 2, "success", "verified-late")
	awaitBroker(t, func() bool { return len(allAgentTasks(t, s)) == 2 })
	second := allAgentTasks(t, s)[1]
	if second.Host != "fixture2" {
		t.Fatal(second.Host)
	}
	commitAgentReport(t, s, m, second, 1, "success", "verified")
	if s.GetReleaseOrder(o.ID).Status != "waiting_next_batch" || len(allAgentTasks(t, s)) != 2 {
		t.Fatal("batch pause bypassed")
	}
	m.reconcileAgentOrder(o.ID, 2)
	if len(allAgentTasks(t, s)) != 2 {
		t.Fatal("duplicate callback continued paused batch")
	}
	if e = m.Continue(o.ID, "tester"); e != nil {
		t.Fatal(e)
	}
	awaitBroker(t, func() bool { return len(allAgentTasks(t, s)) == 3 })
	third := allAgentTasks(t, s)[2]
	if third.Host != "fixture3" {
		t.Fatal(third.Host)
	}
	commitAgentReport(t, s, m, third, 1, "success", "verified")
	if got := s.GetReleaseOrder(o.ID); got.Status != "success" || got.Succeeded != 3 || got.Failed != 0 {
		t.Fatal(got)
	}
}
func TestDeploymentAgentMultiHostFailureDoesNotRollBackPeers(t *testing.T) {
	s, m, a := agentMultiFixture(t, 2, 3, false)
	o, e := m.CreateArtifact("api", a.ID, "qa", "failure stops new dispatch", "tester")
	if e != nil {
		t.Fatal(e)
	}
	awaitBroker(t, func() bool { return len(allAgentTasks(t, s)) == 2 })
	tasks := allAgentTasks(t, s)
	commitAgentReport(t, s, m, tasks[0], 1, "failed", "process-exited")
	if len(allAgentTasks(t, s)) != 2 {
		t.Fatal("failed predecessor dispatched next host")
	}
	commitAgentReport(t, s, m, tasks[1], 1, "success", "verified")
	if got := s.GetReleaseOrder(o.ID); got.Status != "failed" || got.Succeeded != 1 {
		t.Fatal(got)
	}
	if s.GetDeploy(tasks[1].DeployID).Status != "success" {
		t.Fatal("successful peer rolled back without instruction")
	}
	for _, id := range s.ReleaseDeployIDs(o.ID) {
		d := s.GetDeploy(id)
		if d.Host == "fixture3" && d.Status != "canceled" {
			t.Fatal("undispatched host not canceled")
		}
	}
}
func TestDeploymentAgentApprovalAndFrozenBindings(t *testing.T) {
	s, m, _, _, a := deploymentBrokerFixture(t)
	if e := s.UpsertEnvironment(Environment{Name: "qa", DisplayName: "隔离", Protected: true, Approvers: "reviewer", RequireReason: true}); e != nil {
		t.Fatal(e)
	}
	target := s.GetServiceEnvironment("api", "qa")
	target.DeployConfig["health_failure_action"] = "rollback"
	if e := s.UpsertServiceEnvironment(*target); e != nil {
		t.Fatal(e)
	}
	o, e := m.CreateArtifact("api", a.ID, "qa", "approved isolated task", "tester")
	if e != nil {
		t.Fatal(e)
	}
	if o.Status != "pending_approval" || len(allAgentTasks(t, s)) != 0 {
		t.Fatal("protected release executed before approval")
	}
	if m.Approve(o.ID, "tester") == nil || len(allAgentTasks(t, s)) != 0 {
		t.Fatal("author approved own release")
	}
	if e = m.Approve(o.ID, "reviewer"); e != nil {
		t.Fatal(e)
	}
	awaitBroker(t, func() bool { return len(allAgentTasks(t, s)) == 1 })
	task := allAgentTasks(t, s)[0]
	if task.Rollback == nil || task.ReleaseID != o.ID {
		t.Fatal("approval did not freeze rollback and task ownership")
	}
	if _, e = m.dp.StartArtifactRelease(target.Apply(s.GetService("api")), *s.GetHost("fixture"), *a, "qa", "bypass", "tester"); e == nil {
		t.Fatal("bare Agent publication bypassed release gate")
	}
}

func TestDeploymentAgentMultiHostDatabaseReopenDoesNotResend(t *testing.T) {
	s, m, a := agentMultiFixture(t, 1, 3, false)
	o, e := m.CreateArtifact("api", a.ID, "qa", "restart pending batch", "tester")
	if e != nil {
		t.Fatal(e)
	}
	awaitBroker(t, func() bool { return len(allAgentTasks(t, s)) == 1 })
	first := allAgentTasks(t, s)[0]
	commitAgentReport(t, s, m, first, 1, "needs_review", "verifying")
	dir := s.dataDir
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	restored, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	recovered := NewReleaseManager(restored, NewDeployer(restored, NewLogHub(), ""))
	recovered.Recover()
	if len(allAgentTasks(t, restored)) != 1 || allAgentTasks(t, restored)[0].ID != first.ID {
		t.Fatal("restart reassigned or resent pending task")
	}
	for _, id := range restored.ReleaseDeployIDs(o.ID) {
		if d := restored.GetDeploy(id); d.Host != "fixture" && d.Status != "running" {
			t.Fatal("undispatched batch reservation lost")
		}
	}
	commitAgentReport(t, restored, recovered, first, 2, "success", "verified-after-recovery")
	if len(allAgentTasks(t, restored)) != 2 || allAgentTasks(t, restored)[1].Host != "fixture2" {
		t.Fatal("recovery did not resume only next host")
	}
}
func TestDeploymentAgentFailureThresholdRemainsFrozen(t *testing.T) {
	s, m, a := agentMultiFixture(t, 1, 3, false)
	target := s.GetServiceEnvironment("api", "qa")
	target.FailureThreshold = 1
	if e := s.UpsertServiceEnvironment(*target); e != nil {
		t.Fatal(e)
	}
	o, e := m.CreateArtifact("api", a.ID, "qa", "one tolerated known failure", "tester")
	if e != nil {
		t.Fatal(e)
	}
	awaitBroker(t, func() bool { return len(allAgentTasks(t, s)) == 1 })
	first := allAgentTasks(t, s)[0]
	commitAgentReport(t, s, m, first, 1, "failed", "process-exited")
	if len(allAgentTasks(t, s)) != 2 {
		t.Fatal("frozen failure tolerance ignored")
	}
	second := allAgentTasks(t, s)[1]
	commitAgentReport(t, s, m, second, 1, "success", "verified")
	third := allAgentTasks(t, s)[2]
	commitAgentReport(t, s, m, third, 1, "success", "verified")
	if got := s.GetReleaseOrder(o.ID); got.Status != "success" || got.Failed != 1 || got.Succeeded != 2 {
		t.Fatal("tolerated failure hidden or policy changed", got)
	}
}
func TestDeploymentAgentAutoBindingRejectsAmbiguity(t *testing.T) {
	s, m, _ := agentMultiFixture(t, 1, 3, false)
	if _, e := s.registerDeploymentAgent(deploymentAgentRegistration{AgentID: "duplicate_fixture", Host: "fixture", Environment: "qa", Services: []string{"api"}}); e != nil {
		t.Fatal(e)
	}
	if _, e := m.ValidateTarget("api", "qa", "read only preflight"); e == nil {
		t.Fatal("ambiguous host identity silently selected")
	}
	if len(allAgentTasks(t, s)) != 0 {
		t.Fatal("preflight executed deployment")
	}
}

func TestDeploymentAgentRestartPreservesApprovedContinue(t *testing.T) {
	s, m, a := agentMultiFixture(t, 1, 1, true)
	o, e := m.CreateArtifact("api", a.ID, "qa", "continue acknowledgement before restart", "tester")
	if e != nil {
		t.Fatal(e)
	}
	awaitBroker(t, func() bool { return len(allAgentTasks(t, s)) == 1 })
	first := allAgentTasks(t, s)[0]
	commitAgentReport(t, s, m, first, 1, "success", "verified")
	if s.GetReleaseOrder(o.ID).Status != "waiting_next_batch" {
		t.Fatal("first batch did not pause")
	}
	// Model a crash after the user's Continue state was committed and before its
	// scheduling goroutine ran. Recovery must honor the already granted action.
	if e = s.compareReleaseStatus(o.ID, "waiting_next_batch", "queued", "", false); e != nil {
		t.Fatal(e)
	}
	dir := s.dataDir
	s.Close()
	restored, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	recovered := NewReleaseManager(restored, NewDeployer(restored, NewLogHub(), ""))
	recovered.Recover()
	awaitBroker(t, func() bool { return len(allAgentTasks(t, restored)) == 2 })
	if allAgentTasks(t, restored)[0].ID != first.ID || allAgentTasks(t, restored)[1].Host != "fixture2" {
		t.Fatal("Continue was lost or first host replayed")
	}
}
func TestDeploymentAgentRestartBeforeAtomicBatch(t *testing.T) {
	s, m, _, _, a := deploymentBrokerFixture(t)
	target := s.GetServiceEnvironment("api", "qa")
	if e := s.freezeDeploymentAgents(target); e != nil {
		t.Fatal(e)
	}
	o := &ReleaseOrder{Service: "api", ArtifactID: a.ID, Version: a.Version, Environment: "qa", Status: "running", BatchSize: 1, Hosts: []string{"fixture"}, Target: *target, Actor: "tester", runtimeEnvEncrypted: target.runtimeEnvEncrypted}
	if e := s.CreateReleaseOrder(o); e != nil {
		t.Fatal(e)
	}
	m.Recover()
	awaitBroker(t, func() bool { return len(allAgentTasks(t, s)) == 1 })
	if len(s.ReleaseDeployIDs(o.ID)) != 1 {
		t.Fatal("atomic batch recovery duplicated deploy records")
	}
}

func TestDeploymentAgentConcurrentStartupRecoveryKeepsOneBatch(t *testing.T) {
	s, m, _, _, a := deploymentBrokerFixture(t)
	target := s.GetServiceEnvironment("api", "qa")
	if e := s.freezeDeploymentAgents(target); e != nil {
		t.Fatal(e)
	}
	o := &ReleaseOrder{Service: "api", ArtifactID: a.ID, Version: a.Version, Environment: "qa", Status: "queued", BatchSize: 1, Hosts: []string{"fixture"}, Target: *target, Actor: "tester", runtimeEnvEncrypted: target.runtimeEnvEncrypted}
	if e := s.CreateReleaseOrder(o); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			controller := NewReleaseManager(s, NewDeployer(s, NewLogHub(), ""))
			if i%2 == 0 {
				controller.Recover()
			} else {
				controller.startNextBatch(o.ID)
			}
		}(i)
	}
	wg.Wait()
	awaitBroker(t, func() bool { return len(allAgentTasks(t, s)) == 1 })
	if len(s.ReleaseDeployIDs(o.ID)) != 1 {
		t.Fatal("startup recovery duplicated the batch")
	}
	if status := s.GetReleaseOrder(o.ID).Status; status != "running" && status != "needs_review" {
		t.Fatal("stale batch owner closed active release", status)
	}
	m.reconcileAgentOrder(o.ID, 1)
	if len(allAgentTasks(t, s)) != 1 {
		t.Fatal("task was repeated")
	}
}
