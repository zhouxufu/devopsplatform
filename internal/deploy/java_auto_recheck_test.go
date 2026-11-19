package deploy

import (
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func javaAutoFixture(t *testing.T) (*Store, *ReleaseManager, *Deploy, *ReleaseOrder) {
	t.Helper()
	s, m, _, _, a := deploymentBrokerFixture(t)
	target := s.GetServiceEnvironment("api", "qa")
	delete(target.DeployConfig, "deployment_agent_id")
	if e := s.UpsertServiceEnvironment(*target); e != nil {
		t.Fatal(e)
	}
	o := &ReleaseOrder{Service: "api", Environment: "qa", ArtifactID: a.ID, Status: "needs_review", Target: *target, Hosts: []string{"fixture"}, NextIndex: 1}
	if e := s.CreateReleaseOrder(o); e != nil {
		t.Fatal(e)
	}
	d := &Deploy{Service: "api", Environment: "qa", Host: "fixture", ArtifactID: a.ID, ArtifactDigest: a.Digest, Status: "running"}
	if e := s.CreateDeploy(d); e != nil {
		t.Fatal(e)
	}
	if e := s.FinishDeploy(d.ID, "needs_review"); e != nil {
		t.Fatal(e)
	}
	if e := s.AttachReleaseDeploys(o.ID, 1, []int64{d.ID}); e != nil {
		t.Fatal(e)
	}
	return s, m, d, o
}
func TestJavaAutoRecheckDelayedReadyAndPersistentBackoff(t *testing.T) {
	s, m, d, o := javaAutoFixture(t)
	calls := 0
	probe := func(_ Host, cmd string, _ time.Duration) (string, error) {
		calls++
		if strings.Contains(cmd, "agent-probe") && calls == 1 {
			return "starting", javaTestExit(10)
		}
		return "ready", nil
	}
	m.sweepJavaRechecks(1000, probe, 0)
	m.sweepJavaRechecks(1014, probe, 0)
	if calls != 0 {
		t.Fatal("initial delay ignored")
	}
	m.sweepJavaRechecks(1015, probe, 0)
	state, _, e := s.javaAutoState(d.ID)
	if e != nil || state.Attempts != 1 || state.NextAt != 1045 || s.GetDeploy(d.ID).Status != "needs_review" {
		t.Fatalf("state=%+v error=%v", state, e)
	}
	// A new controller observes the same persisted schedule, not a new window.
	m = NewReleaseManager(s, nil)
	m.sweepJavaRechecks(1044, probe, 0)
	if calls != 1 {
		t.Fatal("restart reset schedule")
	}
	m.sweepJavaRechecks(1045, probe, 0)
	if s.GetReleaseOrder(o.ID).Status != "success" || s.GetReleaseOrder(o.ID).Succeeded != 1 {
		t.Fatal("parent not converged")
	}
	if calls != 3 {
		t.Fatalf("expected two probes and one original unlock, got %d", calls)
	}
	m.sweepJavaRechecks(1200, probe, 0)
	if calls != 3 {
		t.Fatal("replayed terminal deployment")
	}
}
func TestJavaAutoRecheckStopsOnConflictAndDeadline(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		manual bool
	}{
		{"identity", javaTestExit(1), true}, {"pid-receipt", javaTestExit(30), true}, {"pid-ambiguous", javaTestExit(31), true}, {"transport", errors.New("offline"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, m, d, _ := javaAutoFixture(t)
			calls := 0
			probe := func(_ Host, _ string, _ time.Duration) (string, error) { calls++; return "", tc.err }
			m.sweepJavaRechecks(1000, probe, 0)
			m.sweepJavaRechecks(1015, probe, 0)
			state, _, _ := s.javaAutoState(d.ID)
			if (state.State == "manual") != tc.manual {
				t.Fatalf("%+v", state)
			}
			m.sweepJavaRechecks(2000, probe, 0)
			state, _, _ = s.javaAutoState(d.ID)
			if calls != 1 || state.State != "manual" || s.GetDeploy(d.ID).Status != "needs_review" {
				t.Fatal("unsafe retry or lost protection")
			}
			deployments := s.Deploys("api", 20)
			s.attachJavaAutoStates(deployments)
			if deployments[0].AutoRecheck == nil || deployments[0].AutoRecheck.Message == "" {
				t.Fatal("missing public reason")
			}
		})
	}
}
func TestJavaAutoRecheckClaimPreventsConcurrentObservers(t *testing.T) {
	s, m, d, _ := javaAutoFixture(t)
	m.autoRecheckJava(d.ID, 1000, nil)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	probe := func(_ Host, cmd string, _ time.Duration) (string, error) {
		calls.Add(1)
		close(entered)
		<-release
		return "starting", javaTestExit(10)
	}
	go func() { defer close(done); m.autoRecheckJava(d.ID, 1015, probe) }()
	<-entered
	other := NewReleaseManager(s, nil)
	if other.autoRecheckJava(d.ID, 1016, probe) {
		t.Fatal("claimed live observation twice")
	}
	close(release)
	<-done
	if calls.Load() != 1 {
		t.Fatal("duplicate observation")
	}
	v, raw, _ := s.javaAutoState(d.ID)
	v.State = "checking"
	v.LeaseUntil = 1200
	v.NextAt = 0
	if _, e := s.saveJavaAutoState(d.ID, v, raw); e != nil {
		t.Fatal(e)
	}
	if other.autoRecheckJava(d.ID, 1199, nil) {
		t.Fatal("live lease ignored")
	}
	if !other.autoRecheckJava(d.ID, 1201, func(_ Host, _ string, _ time.Duration) (string, error) { return "still starting", javaTestExit(10) }) {
		t.Fatal("expired observer not recovered")
	}
}
func TestJavaAutoRecheckNeverDispatchesRemainingBatch(t *testing.T) {
	s, m, d, o := javaAutoFixture(t)
	if _, e := s.db.Exec(`UPDATE release_orders SET hosts='["fixture","later"]',total=2 WHERE id=?`, o.ID); e != nil {
		t.Fatal(e)
	}
	m.sweepJavaRechecks(1000, nil, 0)
	m.sweepJavaRechecks(1015, func(_ Host, _ string, _ time.Duration) (string, error) { return "ready", nil }, 0)
	if s.GetReleaseOrder(o.ID).Status != "waiting_next_batch" || len(s.ReleaseDeployIDs(o.ID)) != 1 {
		t.Fatal("automatic check dispatched another batch")
	}
	if s.GetDeploy(d.ID).Status != "success" {
		t.Fatal("original result not retained")
	}
}
func TestJavaAutoRecheckRecoveryGapAndExcludedTargets(t *testing.T) {
	s, m, d, o := javaAutoFixture(t)
	if _, e := s.db.Exec(`UPDATE release_orders SET status='running' WHERE id=?`, o.ID); e != nil {
		t.Fatal(e)
	}
	m.Recover()
	if s.GetReleaseOrder(o.ID).Status != "needs_review" {
		t.Fatal("restart lost pending original result")
	}
	// Simulate crash between child completion and parent reconciliation.
	if _, e := s.db.Exec(`UPDATE deploys SET status='success' WHERE id=?`, d.ID); e != nil {
		t.Fatal(e)
	}
	m.sweepJavaRechecks(1000, func(_ Host, _ string, _ time.Duration) (string, error) {
		t.Fatal("replayed completed child")
		return "", nil
	}, 0)
	if s.GetReleaseOrder(o.ID).Status != "success" {
		t.Fatal("parent projection remains stuck")
	}
	for _, target := range []ServiceEnvironment{{TargetType: "ssh-static"}, {TargetType: "ssh-jar", DeployConfig: map[string]string{"java_mode": "managed", "deployment_agent_id": "agent"}}, {TargetType: "ssh-java-bundle", DeployConfig: map[string]string{"java_mode": "managed"}}} {
		if javaAutoEligible(&ReleaseOrder{Status: "needs_review", Target: target}) {
			t.Fatal("unsupported target observed")
		}
	}
}

func TestJavaAutoRecheckSurvivesStoreReopen(t *testing.T) {
	s, m, d, _ := javaAutoFixture(t)
	m.autoRecheckJava(d.ID, 1000, nil)
	dir := s.dataDir
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	v, _, err := restored.javaAutoState(d.ID)
	if err != nil || v == nil || v.StartedAt != 1000 || v.Deadline != 1900 || v.NextAt != 1015 {
		t.Fatalf("persisted state=%+v err=%v", v, err)
	}
	manager := NewReleaseManager(restored, nil)
	calls := 0
	manager.sweepJavaRechecks(1015, func(_ Host, _ string, _ time.Duration) (string, error) { calls++; return "ready", nil }, 0)
	if calls != 2 || restored.GetDeploy(d.ID).Status != "success" {
		t.Fatal("reopened observer failed to converge")
	}
}

func TestJavaAutoRecheckCrashAfterChildReceipt(t *testing.T) {
	s, m, d, o := javaAutoFixture(t)
	m.autoRecheckJava(d.ID, 1000, nil)
	if _, e := s.db.Exec(`UPDATE release_orders SET status='running' WHERE id=?`, o.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.Exec(`UPDATE deploys SET status='success' WHERE id=?`, d.ID); e != nil {
		t.Fatal(e)
	}
	m.Recover()
	m.sweepJavaRechecks(1050, func(_ Host, _ string, _ time.Duration) (string, error) {
		t.Fatal("completed probe replayed")
		return "", nil
	}, 0)
	if s.GetReleaseOrder(o.ID).Status != "success" {
		t.Fatal("lost child receipt did not settle parent")
	}
}

func TestJavaAutoRecheckLeavesOrdinaryRollingCoordinatorAlone(t *testing.T) {
	s, m, d, o := javaAutoFixture(t)
	if _, e := s.db.Exec(`UPDATE release_orders SET status='running',hosts='["fixture","later"]',total=2 WHERE id=?`, o.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.Exec(`UPDATE deploys SET status='success' WHERE id=?`, d.ID); e != nil {
		t.Fatal(e)
	}
	m.sweepJavaRechecks(1000, func(_ Host, _ string, _ time.Duration) (string, error) {
		t.Fatal("ordinary release probed")
		return "", nil
	}, 0)
	if s.GetReleaseOrder(o.ID).Status != "running" {
		t.Fatal("observer changed normal rolling schedule")
	}
}
func TestJavaAutoRecheckDeadlineWinsOverFutureRetry(t *testing.T) {
	s, m, d, _ := javaAutoFixture(t)
	m.autoRecheckJava(d.ID, 1000, nil)
	v, raw, _ := s.javaAutoState(d.ID)
	v.NextAt = 1950
	if _, e := s.saveJavaAutoState(d.ID, v, raw); e != nil {
		t.Fatal(e)
	}
	m.autoRecheckJava(d.ID, 1900, func(_ Host, _ string, _ time.Duration) (string, error) {
		t.Fatal("probe past deadline")
		return "", nil
	})
	v, _, _ = s.javaAutoState(d.ID)
	if v.State != "manual" {
		t.Fatal("deadline hidden by retry time")
	}
}

func TestJavaAutoRecheckMySQLCAS(t *testing.T) {
	dsn := os.Getenv("JAVA_AUTO_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("requires disposable MySQL schema")
	}
	s, err := OpenDatabase(t.TempDir(), DatabaseConfig{Driver: "mysql", DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id := int64(901)
	defer s.db.Exec(`DELETE FROM deploy_meta WHERE key=?`, javaAutoKey(id))
	v := &JavaAutoRecheck{State: "scheduled", StartedAt: 1000, Deadline: 1900, NextAt: 1015}
	before, err := s.saveJavaAutoState(id, v, "")
	if err != nil {
		t.Fatal(err)
	}
	v.State = "checking"
	v.Attempts = 1
	v.LeaseUntil = 1135
	if _, err = s.saveJavaAutoState(id, v, before); err != nil {
		t.Fatal(err)
	}
	v.Attempts = 2
	if _, err = s.saveJavaAutoState(id, v, before); err != ErrRunConflict {
		t.Fatalf("stale claim accepted: %v", err)
	}
	list := []Deploy{{ID: id, Status: "needs_review"}, {ID: 902, Status: "success"}}
	s.attachJavaAutoStates(list)
	if list[0].AutoRecheck == nil || list[0].AutoRecheck.Attempts != 1 || list[1].AutoRecheck != nil {
		t.Fatalf("metadata readback=%+v", list)
	}
	got, _, err := s.javaAutoState(id)
	if err != nil || got.LeaseUntil != 1135 {
		t.Fatalf("durable claim=%+v err=%v", got, err)
	}
}
