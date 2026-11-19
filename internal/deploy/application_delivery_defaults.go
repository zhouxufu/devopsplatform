package deploy

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// Application defaults remain in the versioned policy so existing readers and
// frozen delivery snapshots retain their meaning. Only this application's entry
// is edited, in the same transaction as its deployment destination.
type applicationDefaultPatch struct {
	Revision int64                       `json:"revision"`
	Value    *ApplicationDeliveryDefault `json:"value"`
}

type applicationDefaultState struct {
	BuildTool      string                      `json:"build_tool"`
	Revision       int64                       `json:"revision"`
	Value          *ApplicationDeliveryDefault `json:"value"`
	ArtifactSource string                      `json:"artifact_source"`
}

func (s *Store) applicationDeliveryDefault(service, environment string) (applicationDefaultState, error) {
	if s.GetService(service) == nil || s.GetEnvironment(environment) == nil {
		return applicationDefaultState{}, fmt.Errorf("应用或环境不存在")
	}
	p, err := s.deliveryPolicy(environment)
	state := applicationDefaultState{Revision: p.Revision, ArtifactSource: p.Defaults.ArtifactSource, BuildTool: s.GetService(service).BuildTool}
	if value, ok := p.Defaults.Applications[service]; ok {
		state.Value = &value
	}
	return state, err
}

func (s *Store) prepareApplicationDefault(service, environment string, patch *applicationDefaultPatch) (string, string, error) {
	if patch == nil {
		return "", "", nil
	}
	var before string
	err := s.db.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, "delivery_policy:"+environment).Scan(&before)
	if err != nil && err != sql.ErrNoRows {
		return "", "", err
	}
	p := DeliveryPolicy{Stages: []DeliveryNode{}}
	if before != "" && json.Unmarshal([]byte(before), &p) != nil {
		return "", "", fmt.Errorf("环境交付策略损坏，未保存应用配置")
	}
	if patch.Revision != p.Revision {
		return "", "", fmt.Errorf("环境策略已改变，请重新载入代码与制品策略后重试；当前配置未保存")
	}
	if patch.Value == nil {
		if _, ok := p.Defaults.Applications[service]; !ok {
			return "", "", nil
		}
		delete(p.Defaults.Applications, service)
	} else {
		value := *patch.Value
		if value.CodePolicy.Mode == "integration" {
			value.CodePolicy.BaseBranch = ""
		}
		if err := s.validateDeliveryDefaults(DeliveryDefaults{Applications: map[string]ApplicationDeliveryDefault{service: value}}); err != nil {
			return "", "", err
		}
		if value.CodePolicy != (CodePolicy{}) && !validateCodePolicy(value.CodePolicy) {
			return "", "", fmt.Errorf("应用分支策略不完整，请填写目标分支；独立发布分支还需要基线分支")
		}
		if old, ok := p.Defaults.Applications[service]; ok && old == value {
			return "", "", nil
		}
		if p.Defaults.Applications == nil {
			p.Defaults.Applications = map[string]ApplicationDeliveryDefault{}
		}
		p.Defaults.Applications[service] = value
	}
	if len(p.Defaults.Applications) > 200 {
		return "", "", fmt.Errorf("环境应用默认值最多配置 200 个应用")
	}
	p.Revision++
	after, err := json.Marshal(p)
	return before, string(after), err
}

func writeApplicationDefault(tx *deployTx, environment, before, after string) error {
	if after == "" {
		return nil
	}
	conflict := fmt.Errorf("环境策略已改变，请重新载入代码与制品策略；部署配置和策略均未保存")
	if before == "" {
		if _, err := tx.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?)`, "delivery_policy:"+environment, after); err != nil {
			return conflict
		}
		return nil
	}
	result, err := tx.Exec(`UPDATE deploy_meta SET value=? WHERE key=? AND value=?`, after, "delivery_policy:"+environment, before)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return conflict
	}
	return nil
}
