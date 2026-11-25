package agentconfig

import (
	"logplatform/internal/agent/deployment"
	"logplatform/pkg/deploytask"
)

// Config agent 配置：一机一实例，多服务列表式
type Config struct {
	Deployment   deployment.Config `yaml:"deployment"`
	ServerURL    string            `yaml:"server_url"`
	Token        string            `yaml:"token"`
	AgentID      string            `yaml:"agent_id"` // 兼容旧配置中的名称；启动后绑定本地持久身份
	AgentName    string            `yaml:"agent_name,omitempty"`
	AgentAliases []string          `yaml:"-"` // 旧名称仅用于状态迁移，不是身份
	Host         string            `yaml:"host"`
	Environment  string            `yaml:"environment"`
	Registry     string            `yaml:"registry"`
	Spool        string            `yaml:"spool"`        // 未 ACK 日志的本地持久队列
	SpoolMaxMB   int64             `yaml:"spool_max_mb"` // 达到上限后背压暂停追读
	MonitorURL   string            `yaml:"monitor_url"`  // 机器指标上报（空 = 不采集）
	Collect      []CollectCfg      `yaml:"collect"`
}

// CollectCfg 一个采集项：service 由配置显式声明，不猜路径
type CollectCfg struct {
	ApplicationID string   `yaml:"application_id,omitempty"`
	Service       string   `yaml:"service"`
	Environment   string   `yaml:"environment"`
	Paths         []string `yaml:"paths"`
	Multiline     string   `yaml:"multiline"` // 行首正则，空 = 单行模式
}

type DeploymentConfigRequest struct {
	Scope             deploytask.EnrollmentScope `json:"scope"`
	Token             string                     `json:"token"`
	URL               string                     `json:"url"`
	ExpectedConfigSHA string                     `json:"expected_config_sha"`
	Output            string                     `json:"output"`
}
