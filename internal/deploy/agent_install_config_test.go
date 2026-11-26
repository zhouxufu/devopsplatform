//go:build linux || darwin

package deploy

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	agentcfg "logplatform/internal/agent"
)

func TestGeneratedCollectionUpdatePreservesDeploymentPolicy(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			dir := t.TempDir()
			cfg, err := runtimeAgentConfig(Host{Name: "node", Environment: "test"}, validAgentDesiredConfig(), "new-ingest-token")
			if err != nil {
				t.Fatal(err)
			}
			proposal, err := yaml.Marshal(cfg) // The same serialization used by install and apply.
			if err != nil {
				t.Fatal(err)
			}
			var next map[string]any
			if err = yaml.Unmarshal(proposal, &next); err != nil {
				t.Fatal(err)
			}
			if _, present := next["deployment"]; present {
				t.Fatal("collection update contains a deployment policy")
			}
			policy := map[string]any{"enabled": enabled, "token": "independent-deploy-token", "url": "http://127.0.0.1:1/api/deployment-agent", "state_dir": filepath.Join(dir, "deployment"), "allowed_roots": []any{"/srv/isolated"}, "worker_mode": "systemd", "max_parallel": 2}
			old := map[string]any{"server_url": "http://127.0.0.1:1", "token": "old-ingest-token", "registry": filepath.Join(dir, "registry.json"), "spool": filepath.Join(dir, "logs.db"), "collect": []any{map[string]any{"service": "old", "paths": []any{"/tmp/old.log"}}}, "deployment": policy}
			oldBytes, err := yaml.Marshal(old)
			if err != nil {
				t.Fatal(err)
			}
			current, proposed := filepath.Join(dir, "current.yaml"), filepath.Join(dir, "next.yaml")
			if err = os.WriteFile(current, oldBytes, 0600); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(proposed, proposal, 0600); err != nil {
				t.Fatal(err)
			}
			u, err := user.Current()
			if err != nil {
				t.Fatal(err)
			}
			for pass := 0; pass < 2; pass++ {
				if err = agentcfg.MergeCollectionConfig(current, proposed, u.Username); err != nil {
					t.Fatal(err)
				}
				merged, err := os.ReadFile(proposed)
				if err != nil {
					t.Fatal(err)
				}
				var result map[string]any
				if err = yaml.Unmarshal(merged, &result); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(result["deployment"], policy) || result["token"] != "new-ingest-token" {
					t.Fatal("deployment policy changed or collection update lost")
				}
				if _, err = agentcfg.LoadConfig(proposed); err != nil {
					t.Fatal(err)
				}
				// A second installation/update must work too, including the old
				// explicit disabled stanza left by previous platform versions.
				if err = os.WriteFile(current, merged, 0600); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(proposed, proposal, 0600); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestAgentInstallValidatesPolicyBeforeReplacingBinary(t *testing.T) {
	cmd := renderAgentInstallCommand("/tmp/bin", "/tmp/config", "/tmp/unit", "logagent", "logagent")
	check := strings.Index(cmd, "$SUDO /usr/local/bin/.logagent.new -config")
	promote := strings.Index(cmd, "$SUDO mv -f /usr/local/bin/.logagent.new")
	restart := strings.Index(cmd, "$SUDO systemctl restart logagent")
	if check < 0 || promote < check || restart < promote {
		t.Fatal("binary or process changed before configuration validation")
	}
}
