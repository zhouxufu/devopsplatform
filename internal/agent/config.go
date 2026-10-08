package agent

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config agent 配置：一机一实例，多服务列表式
type Config struct {
	ServerURL   string       `yaml:"server_url"`
	Token       string       `yaml:"token"`
	AgentID     string       `yaml:"agent_id"` // 默认 environment/host，避免跨环境同名主机冲突
	Host        string       `yaml:"host"`
	Environment string       `yaml:"environment"`
	Registry    string       `yaml:"registry"`
	Spool       string       `yaml:"spool"`        // 未 ACK 日志的本地持久队列
	SpoolMaxMB  int64        `yaml:"spool_max_mb"` // 达到上限后背压暂停追读
	MonitorURL  string       `yaml:"monitor_url"`  // 机器指标上报（空 = 不采集）
	Collect     []CollectCfg `yaml:"collect"`
}

// CollectCfg 一个采集项：service 由配置显式声明，不猜路径
type CollectCfg struct {
	ApplicationID string   `yaml:"application_id,omitempty"`
	Service       string   `yaml:"service"`
	Environment   string   `yaml:"environment"`
	Paths         []string `yaml:"paths"`
	Multiline     string   `yaml:"multiline"` // 行首正则，空 = 单行模式
}

func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := &Config{}
	if err := yaml.Unmarshal(b, c); err != nil {
		return nil, err
	}
	if c.ServerURL == "" {
		return nil, fmt.Errorf("server_url is required")
	}
	if len(c.Collect) == 0 {
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
