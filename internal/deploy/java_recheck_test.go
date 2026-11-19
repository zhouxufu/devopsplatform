package deploy

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestJavaRecheckTerminalAndBatchSafety(t *testing.T) {
	for _, tc := range []struct {
		name      string
		probe     error
		unlock    error
		remaining bool
		want      string
	}{
		{name: "ready", want: "success"},
		{name: "exited", probe: javaTestExit(20), want: "failed"},
		{name: "still-starting", probe: javaTestExit(10), want: "needs_review"},
		{name: "identity-mismatch", probe: javaTestExit(1), want: "needs_review"},
		{name: "unlock-uncertain", unlock: javaTestExit(3), want: "needs_review"},
		{name: "remaining-batch-not-dispatched", remaining: true, want: "waiting_next_batch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, m, _, _, a := deploymentBrokerFixture(t)
			target := s.GetServiceEnvironment("api", "qa")
			delete(target.DeployConfig, "deployment_agent_id")
			if err := s.UpsertServiceEnvironment(*target); err != nil {
				t.Fatal(err)
			}
			hosts := []string{"fixture"}
			if tc.remaining {
				hosts = append(hosts, "not-dispatched")
			}
			o := &ReleaseOrder{Service: "api", Environment: "qa", ArtifactID: a.ID, Status: "needs_review", Target: *target, Hosts: hosts, NextIndex: 1}
			if err := s.CreateReleaseOrder(o); err != nil {
				t.Fatal(err)
			}
			d := &Deploy{Service: "api", Environment: "qa", Host: "fixture", ArtifactID: a.ID, ArtifactDigest: a.Digest, Status: "running"}
			if err := s.CreateDeploy(d); err != nil {
				t.Fatal(err)
			}
			s.FinishDeploy(d.ID, "needs_review")
			if err := s.AttachReleaseDeploys(o.ID, 1, []int64{d.ID}); err != nil {
				t.Fatal(err)
			}
			calls := 0
			exec := func(_ Host, cmd string, _ time.Duration) (string, error) {
				calls++
				if strings.Contains(cmd, " start") || strings.Contains(cmd, " stop") {
					t.Fatal("recheck dispatched process control")
				}
				if strings.Contains(cmd, "agent-probe") {
					return "fixture evidence", tc.probe
				}
				if !strings.Contains(cmd, "requested_owner="+shellQuote(fmt.Sprintf("deploy-%d", d.ID))) {
					t.Fatal("lost original lock identity")
				}
				return "", tc.unlock
			}
			err := m.recheckManagedJava(d.ID, exec)
			if (tc.want == "needs_review") != (err != nil) {
				t.Fatalf("error=%v want=%s", err, tc.want)
			}
			got := s.GetReleaseOrder(o.ID)
			if got.Status != tc.want {
				t.Fatalf("order=%+v", got)
			}
			if len(s.ReleaseDeployIDs(o.ID)) != 1 {
				t.Fatal("recheck created another deployment")
			}
			if tc.want != "needs_review" {
				n := calls
				if err := m.recheckManagedJava(d.ID, exec); err == nil || calls != n {
					t.Fatal("terminal deployment re-executed")
				}
				if got.Succeeded+got.Failed != 1 {
					t.Fatal("counts not refreshed")
				}
			} else if s.GetDeploy(d.ID).Status != "needs_review" {
				t.Fatal("uncertain evidence closed original deployment")
			}
		})
	}
}
