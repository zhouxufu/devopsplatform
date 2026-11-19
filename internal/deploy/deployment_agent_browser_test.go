package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"logplatform/pkg/deploytask"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDeploymentAgentBrowserFixture(t *testing.T) {
	port := os.Getenv("DEPLOYMENT_AGENT_BROWSER_PORT")
	if port == "" {
		t.Skip("opt-in isolated Agent receipt browser fixture")
	}
	s, m, _, token, a := deploymentBrokerFixture(t)
	_ = token
	if os.Getenv("DEPLOYMENT_AGENT_V2_BROWSER") == "1" {
		target := s.GetServiceEnvironment("api", "qa")
		target.DeployConfig["health_failure_action"] = "rollback"
		if e := s.UpsertServiceEnvironment(*target); e != nil {
			t.Fatal(e)
		}
		if e := s.UpsertEnvironment(Environment{Name: "plain", DisplayName: "普通 JAR"}); e != nil {
			t.Fatal(e)
		}
		if e := s.UpsertHost(Host{Name: "plainhost", Environment: "plain", Addr: "192.0.2.99:1", User: "never-connect"}); e != nil {
			t.Fatal(e)
		}
		if e := s.UpsertServiceEnvironment(ServiceEnvironment{Service: "api", Environment: "plain", Hosts: []string{"plainhost"}, TargetType: "ssh-jar", RemoteDir: "/isolated/plain", DeployConfig: map[string]string{"java_mode": "managed", "java_version": "8", "java_port": "18889", "health_failure_action": "keep"}}); e != nil {
			t.Fatal(e)
		}
	}
	o, e := m.CreateArtifact("api", a.ID, "qa", "模拟慢启动", "fixture")
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
	m.sweepAgentTasks(time.Now().Unix() + 121)
	hub := NewLogHub()
	routes := Routes(s, NewBuilder(s, t.TempDir(), hub), hub, "", NewStepLib("../../steps"))
	files := http.FileServer(http.Dir("../../web/dist"))
	done := make(chan struct{})
	var once sync.Once
	var blocked atomic.Int32
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/__fixture/stop":
			once.Do(func() { close(done) })
			fmt.Fprint(w, `{}`)
			return
		case "/__fixture/ready", "/__fixture/rollback":
			state, phase := "success", "verified-fixture"
			if r.URL.Path == "/__fixture/rollback" {
				state = "rolled-back"
				phase = "verified-rollback-fixture"
			}
			report := deploytask.Report{TaskID: task.ID, TaskDigest: deploytask.Digest(task), Sequence: 1, State: state, Phase: phase, At: time.Now().Unix()}
			if _, e := s.acceptAgentReport("agent_fixture", report); e != nil {
				http.Error(w, e.Error(), 409)
				return
			}
			m.resumeAgentRelease(task.DeployID)
			fmt.Fprint(w, `{}`)
			return
		case "/__fixture/state":
			json.NewEncoder(w).Encode(map[string]any{"release": s.GetReleaseOrder(o.ID), "deploy": s.GetDeploy(task.DeployID), "blocked": blocked.Load()})
			return
		case "/api/me":
			json.NewEncoder(w).Encode(map[string]any{"username": "fixture", "role": "admin", "auth_enabled": true})
			return
		}
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/api/plugins/deploy")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if r.Method != "GET" {
				blocked.Add(1)
				http.Error(w, "fixture blocks all execution", 403)
				return
			}
			r.Header.Set("X-User", "fixture")
			r.Header.Set("X-Role", "admin")
			routes.ServeHTTP(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})}
	ln, e := net.Listen("tcp", "127.0.0.1:"+port)
	if e != nil {
		t.Fatal(e)
	}
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(ln) }()
	t.Log("isolated Agent browser fixture http://" + ln.Addr().String() + "/deploy.html#/svc/api/release")
	select {
	case <-done:
	case <-time.After(20 * time.Minute):
		t.Error("fixture expired")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	server.Shutdown(ctx)
	<-finished
}
