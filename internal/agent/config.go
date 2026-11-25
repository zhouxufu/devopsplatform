package agent

import (
	"fmt"
	"logplatform/internal/agentconfig"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Shared configuration types are independent of the log collector runtime.
type Config = agentconfig.Config
type CollectCfg = agentconfig.CollectCfg

func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := &Config{}
	if err := yaml.Unmarshal(b, c); err != nil {
		return nil, err
	}
	if c.ServerURL == "" && (!c.Deployment.Enabled || len(c.Collect) > 0) {
		return nil, fmt.Errorf("server_url is required")
	}
	if len(c.Collect) == 0 && !c.Deployment.Enabled {
		return nil, fmt.Errorf("collect is required")
	}
	if c.Host == "" {
		c.Host, _ = os.Hostname()
	}
	if c.AgentID == "" {
		c.AgentID = c.Host
		if c.Environment != "" {
			c.AgentID = c.Environment + "/" + c.Host
		}
	}
	if c.MonitorURL != "" && c.Environment == "" {
		return nil, fmt.Errorf("environment is required when monitor_url is configured")
	}
	if c.Registry == "" {
		c.Registry = "registry.json"
	}
	if c.Spool == "" {
		c.Spool = filepath.Join(filepath.Dir(c.Registry), "agent-spool.db")
	}
	if c.SpoolMaxMB <= 0 {
		c.SpoolMaxMB = 1024
	}
	if c.Deployment.Enabled {
		if c.Deployment.Token == "" || c.Deployment.Token == c.Token {
			return nil, fmt.Errorf("deployment requires its own credential; ingest token cannot execute tasks")
		}
		if c.Deployment.StateDir == "" {
			c.Deployment.StateDir = filepath.Join(filepath.Dir(c.Registry), "deployment-tasks")
		}
		var e error
		c.Deployment.StateDir, e = filepath.Abs(c.Deployment.StateDir)
		if e != nil {
			return nil, e
		}
		queue, e := filepath.Abs(c.Spool + ".queue")
		if e != nil {
			return nil, e
		}
		if c.Deployment.StateDir == queue || strings.HasPrefix(c.Deployment.StateDir, queue+string(filepath.Separator)) {
			return nil, fmt.Errorf("deployment state must be separate from the log queue")
		}
	}
	for i := range c.Collect {
		if c.Collect[i].Service == "" || len(c.Collect[i].Paths) == 0 {
			return nil, fmt.Errorf("collect[%d]: service and paths are required", i)
		}
		if c.Collect[i].Environment == "" {
			c.Collect[i].Environment = c.Environment
		}
	}
	return c, nil
}
