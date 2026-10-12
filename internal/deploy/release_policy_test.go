package deploy

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"
)

func TestCanaryBatchBoundsAndSAEPolicy(t *testing.T) {
	target := ServiceEnvironment{DeployConfig: map[string]string{"release_mode": "canary", "canary_count": "1", "observe_seconds": "0", "batch_count": "2"}}
	for _, count := range []int{0, 2, 3, 10} {
		if e := validateReleasePolicy(target, count); e != nil {
			t.Fatal(e)
		}
	}
	if e := validateReleasePolicy(target, 1); e == nil {
		t.Fatal("single instance canary accepted")
	}
	target.DeployConfig["canary_count"] = "2"
	if e := validateReleasePolicy(target, 3); e == nil {
		t.Fatal("over half accepted")
	}
	target.DeployConfig["canary_count"] = "1"
	o := &ReleaseOrder{Target: target, BatchSize: 3, Hosts: []string{"a", "b", "c", "d", "e"}}
	if releaseBatchEnd(o) != 1 {
		t.Fatal("first batch ignored canary count")
	}
	o.NextIndex = 1
	if releaseBatchEnd(o) != 4 {
		t.Fatal("remaining batch size ignored")
	}
	raw, e := saeUpdatePolicy(target)
	if e != nil {
		t.Fatal(e)
	}
	var v map[string]any
	if e = json.Unmarshal([]byte(raw), &v); e != nil {
		t.Fatal(e)
	}
	if v["type"] != "GrayBatchUpdate" || v["batchUpdate"].(map[string]any)["releaseType"] != "manual" {
		t.Fatal(raw)
	}
	target.DeployConfig["release_mode"] = "batch"
	target.DeployConfig["batch_mode"] = "auto"
	raw, _ = saeUpdatePolicy(target)
	if e = json.Unmarshal([]byte(raw), &v); e != nil {
		t.Fatal(e)
	}
	if v["type"] != "BatchUpdate" || v["batchUpdate"].(map[string]any)["releaseType"] != "auto" {
		t.Fatal(raw)
	}
}

type controlledSAEFixture struct {
	mu                                  sync.Mutex
	deploys, confirms, stops, rollbacks int
	state                               saeChangeState
}

func (f *controlledSAEFixture) Test(context.Context) error { return nil }
func (f *controlledSAEFixture) Deploy(context.Context, saeDeployInput) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deploys++
	return "fixture-change", nil
}
func (f *controlledSAEFixture) Change(context.Context, string) (saeChangeState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state, nil
}
func (f *controlledSAEFixture) Confirm(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.confirms++
	f.state = saeChangeState{Status: 2}
	return nil
}
func (f *controlledSAEFixture) Stop(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops++
	f.state = saeChangeState{Status: 6}
	return nil
}
func (f *controlledSAEFixture) AbortRollback(context.Context, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rollbacks++
	f.state = saeChangeState{Status: 8, PipelineID: "rollback-batch"}
	return "fixture-rollback", nil
}
func TestSAECanaryPauseContinueUsesSameChange(t *testing.T) {
	for _, action := range []string{"continue", "cancel", "rollback"} {
		t.Run(action, func(t *testing.T) {
			s, e := Open(t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			must := func(e error) {
				t.Helper()
				if e != nil {
					t.Fatal(e)
				}
			}
			must(s.UpsertEnvironment(Environment{Name: "test", DisplayName: "测试"}))
			must(s.UpsertService(Service{Name: "app", ArtifactType: "jar", BuildTool: "command", BuildCmd: "true"}))
			must(s.UpsertConnection(ResourceConnection{Name: "sae", Kind: TargetAliyunSAE, Username: "fixture-ak", Password: "fixture-secret", Config: map[string]string{"region": "cn-hangzhou", "oss_bucket": "fixture"}}))
			must(s.UpsertServiceEnvironment(ServiceEnvironment{Service: "app", Environment: "test", TargetType: TargetAliyunSAE, DeployConfig: map[string]string{"connection": "sae", "region": "cn-hangzhou", "app_id": "app-test", "namespace_id": "test", "release_mode": "canary", "canary_count": "1", "observe_seconds": "0"}}))
			file := t.TempDir() + "/app.jar"
			must(os.WriteFile(file, []byte("fixture"), 0600))
			a, e := describeArtifact("app", "v1", ArtifactJar, file, "")
			must(e)
			must(s.RegisterArtifact(&a))
			f := &controlledSAEFixture{state: saeChangeState{Status: 8, PipelineID: "first-batch"}}
			d := NewDeployer(s, NewLogHub(), "")
			d.saeFactory = func(*ResourceConnection) (saeProvider, error) { return f, nil }
			d.saePollInterval = time.Millisecond
			m := NewReleaseManager(s, d)
			o, e := m.CreateArtifact("app", a.ID, "test", "fixture", "author")
			must(e)
			wait := func(status string) {
				t.Helper()
				for i := 0; i < 500; i++ {
					o = s.GetReleaseOrder(o.ID)
					if o.Status == status {
						return
					}
					time.Sleep(10 * time.Millisecond)
				}
				t.Fatalf("expected %s got %s", status, o.Status)
			}
			wait("waiting_next_batch")
			if len(o.DeployIDs) != 1 || s.GetDeploy(o.DeployIDs[0]).Status != "waiting_next_batch" {
				t.Fatal("cloud pause not visible")
			}
			if action == "rollback" {
				must(m.Rollback(o.ID, "fixture rollback", "author"))
				wait("waiting_next_batch")
				if !o.Recovery {
					t.Fatal("rollback pause lost its purpose")
				}
				must(m.Continue(o.ID, "author"))
				wait("rolled-back")
			} else if action == "cancel" {
				must(m.Cancel(o.ID, "author"))
				wait("canceled")
			} else {
				must(m.Continue(o.ID, "author"))
				wait("success")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.deploys != 1 || (action == "continue" && f.confirms != 1) || (action == "cancel" && f.stops != 1) || (action == "rollback" && f.rollbacks != 1) {
				t.Fatalf("cloud redeployed or repeated control: %+v", f)
			}
		})
	}
}
