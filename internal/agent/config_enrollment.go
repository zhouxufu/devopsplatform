package agent

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"gopkg.in/yaml.v3"
	"logplatform/internal/agent/deployment"
	"logplatform/pkg/deploytask"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
)

type DeploymentConfigRequest struct {
	Scope             deploytask.EnrollmentScope `json:"scope"`
	Token             string                     `json:"token"`
	URL               string                     `json:"url"`
	ExpectedConfigSHA string                     `json:"expected_config_sha"`
	Output            string                     `json:"output"`
}

// PrepareDeploymentConfig only writes a private candidate and, on a fresh
// installation, its new identity. It never rewrites collection settings or
// starts/restarts an application. Existing queue identity must be readable.
func PrepareDeploymentConfig(current, request string) error {
	b, e := os.ReadFile(request)
	if e != nil {
		return e
	}
	if len(b) > 65536 {
		return fmt.Errorf("oversized enrollment request")
	}
	var req DeploymentConfigRequest
	if json.Unmarshal(b, &req) != nil {
		return fmt.Errorf("invalid enrollment request")
	}
	if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(req.Scope.OperationID) || !validPersistentID(req.Scope.AgentID) || req.Scope.Host == "" || req.Scope.Environment == "" || req.Token == "" || !filepath.IsAbs(req.Output) || filepath.Clean(req.Output) != req.Output || req.Output == current {
		return fmt.Errorf("invalid enrollment identity or output")
	}
	u, err := url.Parse(req.URL)
	if err != nil || u.User != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.RawQuery != "" || u.Fragment != "" || !filepath.IsAbs(req.Scope.StateDir) || filepath.Clean(req.Scope.StateDir) != req.Scope.StateDir || len(req.Scope.Roots) == 0 {
		return fmt.Errorf("invalid enrollment endpoint or policy")
	}
	old, e := os.ReadFile(current)
	exists := e == nil
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	digest := ""
	if exists {
		digest = fmt.Sprintf("%x", sha256.Sum256(old))
	}
	if digest != req.ExpectedConfigSHA {
		return fmt.Errorf("Agent configuration changed after preview")
	}
	var doc yaml.Node
	var cfg *Config
	if exists {
		if yaml.Unmarshal(old, &doc) != nil {
			return fmt.Errorf("invalid existing configuration")
		}
		cfg, e = LoadConfig(current)
		if e != nil {
			return e
		}
		if cfg.Host != req.Scope.Host || cfg.Environment != req.Scope.Environment {
			return fmt.Errorf("existing Agent belongs to another host or environment")
		}
		if cfg.Deployment.Enabled {
			if e = deployment.EnrollmentIdle(cfg.Deployment.StateDir); e != nil {
				return e
			}
		}
	} else {
		cfg = &Config{Host: req.Scope.Host, Environment: req.Scope.Environment, AgentID: req.Scope.AgentID, Registry: filepath.Join(filepath.Dir(req.Scope.StateDir), "registry.json"), Spool: filepath.Join(filepath.Dir(req.Scope.StateDir), "agent-spool.db"), SpoolMaxMB: 1024}
		raw, _ := yaml.Marshal(cfg)
		if yaml.Unmarshal(raw, &doc) != nil {
			return fmt.Errorf("invalid candidate configuration")
		}
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("invalid configuration mapping")
	}
	if !filepath.IsAbs(cfg.Spool) || !filepath.IsAbs(cfg.Registry) {
		return fmt.Errorf("existing queue paths must be absolute before enrollment")
	}
	if req.Token == cfg.Token {
		return fmt.Errorf("deployment credential must differ from ingest credential")
	}
	identity, e := ReadIdentity(cfg.Spool)
	if e != nil {
		if _, err := os.Lstat(cfg.Spool); !os.IsNotExist(err) {
			return fmt.Errorf("existing queue identity cannot be safely adopted")
		}
		queue := cfg.Spool + ".queue"
		if _, err := os.Lstat(queue); !os.IsNotExist(err) {
			return fmt.Errorf("existing queue identity cannot be replaced")
		}
		// Initialize the queue through its public constructor, including format,
		// cursors and lock. An identity-only directory cannot be opened by Agent.
		spool, err := OpenSpool(cfg.Spool, cfg.SpoolMaxMB<<20)
		if err != nil {
			return err
		}
		if e = writeQueueJSON(filepath.Join(queue, "identity.json"), agentIdentity{ID: req.Scope.AgentID}); e != nil {
			spool.Close()
			return e
		}
		if e = spool.Close(); e != nil {
			return e
		}
		identity = req.Scope.AgentID
	}
	if identity != req.Scope.AgentID {
		return fmt.Errorf("persisted Agent identity differs from preview")
	}
	c := deployment.Config{Enabled: true, URL: req.URL, Token: req.Token, StateDir: req.Scope.StateDir, AllowedRoots: req.Scope.Roots, WorkerMode: "systemd", WorkerLauncher: req.Scope.Launcher, EnrollmentID: req.Scope.OperationID, MaxParallel: 1}
	node := &yaml.Node{}
	if e = node.Encode(c); e != nil {
		return e
	}
	mapping := doc.Content[0]
	found := false
	for i := 0; i < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == "deployment" {
			mapping.Content[i+1] = node
			found = true
		}
	}
	if !found {
		mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "deployment"}, node)
	}
	merged, e := yaml.Marshal(&doc)
	if e != nil {
		return e
	}
	if e = os.WriteFile(req.Output, merged, 0600); e != nil {
		return e
	}
	loaded, e := LoadConfig(req.Output)
	if e != nil {
		os.Remove(req.Output)
		return e
	}
	loaded.Deployment.AgentID, loaded.Deployment.Host, loaded.Deployment.Environment = identity, loaded.Host, loaded.Environment
	return deployment.PrepareEnrollmentState(loaded.Deployment)
}

func CheckDeploymentEnrollment(config string) error {
	c, e := LoadConfig(config)
	if e != nil {
		return e
	}
	if !c.Deployment.Enabled {
		return fmt.Errorf("deployment module disabled")
	}
	id, e := ReadIdentity(c.Spool)
	if e != nil {
		return e
	}
	c.Deployment.AgentID, c.Deployment.Host, c.Deployment.Environment = id, c.Host, c.Environment
	return deployment.CheckEnrollmentWorker(c.Deployment)
}
