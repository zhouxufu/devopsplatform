package agent

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"logplatform/pkg/deploytask"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func enrollmentConfigFixture(t *testing.T) (string, string, DeploymentConfigRequest) {
	t.Helper()
	dir, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	cfg := filepath.Join(dir, "agent.yaml")
	request := filepath.Join(dir, "request.json")
	r := DeploymentConfigRequest{Scope: deploytask.EnrollmentScope{OperationID: strings.Repeat("1", 32), AgentID: "agent_" + strings.Repeat("a", 32), Host: "node", Environment: "qa", Roots: []string{filepath.Join(dir, "apps")}, StateDir: filepath.Join(dir, "tasks"), Launcher: "/usr/local/libexec/logagent-deployment-worker"}, Token: "independent-deployment-secret", URL: "http://127.0.0.1:1/api/deployment-agent", Output: filepath.Join(dir, "candidate.yaml")}
	return cfg, request, r
}
func saveEnrollmentRequest(t *testing.T, p string, r DeploymentConfigRequest) {
	t.Helper()
	b, _ := json.Marshal(r)
	if e := os.WriteFile(p, b, 0600); e != nil {
		t.Fatal(e)
	}
}

func TestPrepareDeploymentPreservesCollectionAndIdentity(t *testing.T) {
	cfg, request, r := enrollmentConfigFixture(t)
	dir := filepath.Dir(cfg)
	old := fmt.Sprintf("# preserved operator comment\nserver_url: http://logs:9801\ntoken: ingest-private\nhost: node\nenvironment: qa\nregistry: %s\nspool: %s\nspool_max_mb: 512\ncustom_extension: keep-me\ncollect:\n  - service: orders\n    paths: [/var/log/orders.log]\n    multiline: '^start'\n", filepath.Join(dir, "registry.json"), filepath.Join(dir, "agent-spool.db"))
	os.WriteFile(cfg, []byte(old), 0600)
	r.ExpectedConfigSHA = fmt.Sprintf("%x", sha256.Sum256([]byte(old)))
	os.Mkdir(filepath.Join(dir, "agent-spool.db.queue"), 0700)
	writeQueueJSON(filepath.Join(dir, "agent-spool.db.queue", "identity.json"), agentIdentity{ID: r.Scope.AgentID, Aliases: []string{"legacy-name"}})
	saveEnrollmentRequest(t, request, r)
	if e := PrepareDeploymentConfig(cfg, request); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(cfg)
	if string(b) != old {
		t.Fatal("original collection configuration changed")
	}
	b, _ = os.ReadFile(r.Output)
	if !strings.Contains(string(b), "custom_extension: keep-me") || !strings.Contains(string(b), "operator comment") {
		t.Fatal("unknown configuration or comments lost")
	}
	c, e := LoadConfig(r.Output)
	if e != nil {
		t.Fatal(e)
	}
	if c.Token != "ingest-private" || c.SpoolMaxMB != 512 || len(c.Collect) != 1 || c.Collect[0].Multiline != "^start" || c.Deployment.Token != r.Token {
		t.Fatal("configuration scope changed")
	}
	if info, _ := os.Stat(r.Output); info.Mode().Perm() != 0600 {
		t.Fatal("credential file not private")
	}
	if id, e := ReadIdentity(c.Spool); e != nil || id != r.Scope.AgentID {
		t.Fatal("identity replaced")
	}
	r.ExpectedConfigSHA = "changed"
	saveEnrollmentRequest(t, request, r)
	if PrepareDeploymentConfig(cfg, request) == nil {
		t.Fatal("changed original configuration accepted")
	}
	r.ExpectedConfigSHA = fmt.Sprintf("%x", sha256.Sum256([]byte(old)))
	r.Token = "ingest-private"
	saveEnrollmentRequest(t, request, r)
	if PrepareDeploymentConfig(cfg, request) == nil {
		t.Fatal("ingest credential reused")
	}
}

func TestPrepareDeploymentOnlyBootstrapAndQueueGuard(t *testing.T) {
	cfg, request, r := enrollmentConfigFixture(t)
	saveEnrollmentRequest(t, request, r)
	if e := PrepareDeploymentConfig(cfg, request); e != nil {
		t.Fatal(e)
	}
	c, e := LoadConfig(r.Output)
	if e != nil || len(c.Collect) != 0 || c.ServerURL != "" || !c.Deployment.Enabled {
		t.Fatal(c, e)
	}
	if _, e = os.Stat(cfg); !os.IsNotExist(e) {
		t.Fatal("candidate installed without root confirmation")
	}
	queue, err := OpenSpool(c.Spool, c.SpoolMaxMB<<20)
	if err != nil {
		t.Fatal("prepared queue cannot start", err)
	}
	if err = queue.BindIdentity(c); err != nil {
		t.Fatal(err)
	}
	if c.AgentID != r.Scope.AgentID {
		t.Fatal("first startup replaced enrolled identity")
	}
	queue.Close()
	r.Scope.AgentID = "agent_" + strings.Repeat("b", 32)
	saveEnrollmentRequest(t, request, r)
	if PrepareDeploymentConfig(cfg, request) == nil {
		t.Fatal("existing identity replaced")
	}
}
