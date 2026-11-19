package deploy

import (
	"fmt"
	"strings"
)

// Source observations are configuration metadata, not proof of write authority,
// health or execution. Unknown historical environments are never backfilled.
type ResourceSourceEvidence struct {
	Repository  string `json:"repository,omitempty"`
	Artifact    string `json:"artifact,omitempty"`
	State       string `json:"state"`
	File        string `json:"file,omitempty"`
	Digest      string `json:"digest,omitempty"`
	RuntimeName string `json:"runtime_name,omitempty"`
	Profile     string `json:"profile,omitempty"`
}

func (s *Store) validateResourceSources(sources map[string]ResourceSourceEvidence) error {
	if len(sources) > 10 {
		return fmt.Errorf("资源来源最多包含10个明确环境")
	}
	for env, source := range sources {
		if s.GetEnvironment(env) == nil {
			return fmt.Errorf("资源来源的环境不存在")
		}
		switch source.State {
		case "declared", "not_applicable", "pending_source", "missing_config":
		default:
			return fmt.Errorf("资源来源状态无效")
		}
		for _, value := range []string{source.File, source.Digest, source.RuntimeName, source.Profile, source.Repository, source.Artifact} {
			if len(value) > 512 || strings.ContainsAny(value, "\r\n\x00") {
				return fmt.Errorf("资源来源信息过长或含无效字符")
			}
		}
	}
	return nil
}

func unboundResourceCheck(r ApplicationResourceRequirement, environment string) ResourceCheck {
	check := ResourceCheck{Status: "unbound", Code: "not_configured", Message: "此环境尚未绑定", Suggestion: "选择该环境的连接和目标"}
	switch r.Sources[environment].State {
	case "not_applicable":
		check.Status, check.Code, check.Message, check.Suggestion = "not_applicable", "source_not_used", "本次明确识别的环境未使用此资源", "不需要复制其他环境的绑定；源配置变化后重新识别"
	case "pending_source":
		check.Status, check.Code, check.Message, check.Suggestion = "pending_source", "source_incomplete", "源配置不完整，尚不能确定此环境的依赖", "先补全或恢复源配置，再重新识别；不要猜填其他环境目标"
	case "missing_config":
		check.Status, check.Code, check.Message, check.Suggestion = "missing_config", "source_config_missing", "源码引用的Nacos配置缺失或不可读取", "核对对应环境、命名空间、Group和Data ID"
	case "declared":
		check.Code, check.Message = "missing_binding", "源码已声明此资源，当前环境尚未绑定"
	}
	return check
}
