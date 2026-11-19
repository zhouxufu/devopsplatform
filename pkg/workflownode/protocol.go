// Package workflownode describes an installed provider's orchestration nodes.
// Executors are versioned host capabilities, not vendor names or arbitrary scripts.
package workflownode

import (
	"fmt"
	"logplatform/pkg/apidoc"
	p "logplatform/pkg/extensionprotocol"
	"regexp"
)

const Capability = "workflow.node.v1"

type Descriptor struct {
	BindingPath     string    `json:"binding_path,omitempty"`
	BindingLabel    string    `json:"binding_label,omitempty"`
	Effect          string    `json:"effect,omitempty"`
	Fields          []p.Field `json:"fields,omitempty"`
	ProviderVersion string    `json:"provider_version,omitempty"`
	ProviderDigest  string    `json:"provider_digest,omitempty"`
	Version         int       `json:"version"`
	Key             string    `json:"key"`
	Name            string    `json:"name"`
	Executor        string    `json:"executor"`
	Configuration   string    `json:"configuration"`
}
type DocumentTask struct {
	Environment    string        `json:"environment,omitempty"`
	Service        string        `json:"service"`
	ApplicationID  string        `json:"application_id"`
	ArtifactID     int64         `json:"artifact_id"`
	ArtifactDigest string        `json:"artifact_digest"`
	SourceRunID    int64         `json:"source_run_id"`
	Commit         string        `json:"commit"`
	Source         apidoc.Source `json:"source"`
	Plan           apidoc.Plan   `json:"plan"`
}
type Binding struct {
	Tests           []TestTask        `json:"tests,omitempty"`
	ProviderDigest  string            `json:"provider_digest,omitempty"`
	Descriptor      *Descriptor       `json:"descriptor,omitempty"`
	Environment     string            `json:"environment,omitempty"`
	Parameters      map[string]string `json:"parameters,omitempty"`
	Targets         []Target          `json:"targets,omitempty"`
	Provider        string            `json:"provider"`
	NodeKey         string            `json:"node_key"`
	ProviderVersion string            `json:"provider_version,omitempty"`
	Executor        string            `json:"executor,omitempty"`
	Documents       []DocumentTask    `json:"documents,omitempty"`
}

const RemoteExecutor = "workflow.remote-job.v1"

type Target struct {
	Service        string `json:"service"`
	ApplicationID  string `json:"application_id"`
	ArtifactID     int64  `json:"artifact_id"`
	ArtifactDigest string `json:"artifact_digest"`
}
type JobRequest struct {
	ExecutionID string            `json:"execution_id"`
	NodeKey     string            `json:"node_key"`
	Environment string            `json:"environment"`
	Targets     []Target          `json:"targets"`
	Parameters  map[string]string `json:"parameters"`
}
type JobStatus struct {
	ExecutionID string `json:"execution_id"`
	State       string `json:"state"`
	Message     string `json:"message,omitempty"`
}

var fieldKey = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func ValidateDescriptor(d Descriptor) error {
	if d.Version != 1 || !regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`).MatchString(d.Key) || d.Name == "" || len(d.Name) > 100 {
		return fmt.Errorf("插件节点声明无效")
	}
	if d.BindingPath != "" && (!regexp.MustCompile(`^/[a-z][a-z0-9/-]{0,127}$`).MatchString(d.BindingPath) || d.Configuration != "application-binding" || d.BindingLabel == "" || len(d.BindingLabel) > 100) {
		return fmt.Errorf("应用配置入口声明无效")
	}
	switch d.Executor {
	case APITestExecutor:
		if d.Configuration != "application-binding" || d.Effect != "http-request" {
			return fmt.Errorf("测试节点配置声明无效")
		}
	case apidoc.Capability:
		if d.Configuration != "application-binding" {
			return fmt.Errorf("文档节点应引用应用配置")
		}
	case RemoteExecutor:
		if d.Configuration != "node-fields" || d.Effect != "read-only" || len(d.Fields) > 20 {
			return fmt.Errorf("首版远程节点仅支持声明为只读的有界异步任务")
		}
		seen := map[string]bool{}
		for _, f := range d.Fields {
			if !fieldKey.MatchString(f.Key) || seen[f.Key] || f.Type != "string" || f.Label == "" || len(f.Label) > 200 {
				return fmt.Errorf("节点参数声明无效")
			}
			seen[f.Key] = true
		}
	default:
		return fmt.Errorf("节点执行协议不兼容")
	}
	return nil
}
