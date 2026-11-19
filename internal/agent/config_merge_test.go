package agent

import (
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"testing"
)

func TestDeploymentConfigMergePreservesIndependentPolicy(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "agent.yaml")
	next := filepath.Join(dir, "next.yaml")
	state := filepath.Join(dir, "tasks")
	os.Mkdir(state, 0700)
	body := "server_url: http://127.0.0.1:1\ntoken: ingest-old\nregistry: " + filepath.Join(dir, "registry.json") + "\ncollect:\n  - service: api\n    paths: [/tmp/isolated.log]\n\"deployment\":\n  enabled: true\n  url: http://127.0.0.1:2/api/deployment-agent\n  token: deploy-independent\n  state_dir: " + state + "\n  allowed_roots: [/isolated/apps]\n"
	proposed := "server_url: http://127.0.0.1:3\ntoken: ingest-new\ncollect:\n  - service: api\n    paths: [/tmp/another.log]\n"
	os.WriteFile(old, []byte(body), 0600)
	os.WriteFile(next, []byte(proposed), 0600)
	u, e := user.Current()
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(map[string]int{"uid": os.Geteuid()})
	os.WriteFile(filepath.Join(state, "owner.json"), b, 0600)
	if e = MergeCollectionConfig(old, next, u.Username); e != nil {
		t.Fatal(e)
	}
	c, e := LoadConfig(next)
	if e != nil {
		t.Fatal(e)
	}
	if !c.Deployment.Enabled || c.Deployment.Token != "deploy-independent" || c.Deployment.StateDir != state || c.Token != "ingest-new" || c.Collect[0].Paths[0] != "/tmp/another.log" {
		t.Fatal("independent deployment policy was overwritten")
	}
	info, _ := os.Stat(next)
	if info.Mode().Perm() != 0600 {
		t.Fatal("config is not private")
	}
	os.WriteFile(next, []byte(proposed), 0600)
	b, _ = json.Marshal(map[string]int{"uid": os.Geteuid() + 9999})
	os.WriteFile(filepath.Join(state, "owner.json"), b, 0600)
	if MergeCollectionConfig(old, next, u.Username) == nil {
		t.Fatal("deployment execution user changed")
	}
	b, _ = os.ReadFile(next)
	if string(b) != proposed {
		t.Fatal("rejected update mutated proposed config")
	}
	os.WriteFile(next, []byte(proposed+"deployment:\n  enabled: false\n"), 0600)
	if MergeCollectionConfig(old, next, u.Username) == nil {
		t.Fatal("collection update replaced deployment policy")
	}
}
func TestDeploymentConfigCredentialAndSpoolIsolation(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "agent.yaml")
	base := "token: same\nregistry: " + filepath.Join(dir, "registry.json") + "\nspool: " + filepath.Join(dir, "logs.db") + "\ndeployment:\n  enabled: true\n  token: same\n"
	os.WriteFile(p, []byte(base), 0600)
	if _, e := LoadConfig(p); e == nil {
		t.Fatal("log ingest credential could execute deployment")
	}
	body := "registry: " + filepath.Join(dir, "registry.json") + "\nspool: " + filepath.Join(dir, "logs.db") + "\ndeployment:\n  enabled: true\n  token: independent\n  state_dir: " + filepath.Join(dir, "logs.db.queue", "tasks") + "\n"
	os.WriteFile(p, []byte(body), 0600)
	if _, e := LoadConfig(p); e == nil {
		t.Fatal("deployment journal stored inside log spool")
	}
}
