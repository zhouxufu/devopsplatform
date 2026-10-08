package deploy

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// AgentDesiredConfig is the platform-owned desired state for one host agent.
// The shared ingest token is deliberately not persisted here; it is injected
// from the platform process environment only while applying the configuration.
type AgentDesiredConfig struct {
	HostName       string                `json:"host_name"`
	ServerURL      string                `json:"server_url"`
	MonitorURL     string                `json:"monitor_url"`
	RunUser        string                `json:"run_user"`
	RunGroup       string                `json:"run_group"`
	SpoolMaxMB     int64                 `json:"spool_max_mb"`
	Collect        []AgentCollectRequest `json:"collect"`
	Configured     bool                  `json:"configured"`
	Synchronized   bool                  `json:"synchronized"`
	TokenAvailable bool                  `json:"token_available"`
	UpdatedAt      int64                 `json:"updated_at,omitempty"`
	AppliedAt      int64                 `json:"applied_at,omitempty"`
	LastApplyError string                `json:"last_apply_error,omitempty"`
}

func (s *Store) AgentDesiredConfig(hostName string) *AgentDesiredConfig {
	var cfg AgentDesiredConfig
	var collect string
	var appliedAt sql.NullInt64
	err := s.db.QueryRow(`SELECT host_name,server_url,monitor_url,run_user,run_group,spool_max_mb,collect,updated_at,applied_at,last_apply_error
		FROM agent_configs WHERE host_name=?`, hostName).Scan(
		&cfg.HostName, &cfg.ServerURL, &cfg.MonitorURL, &cfg.RunUser, &cfg.RunGroup,
		&cfg.SpoolMaxMB, &collect, &cfg.UpdatedAt, &appliedAt, &cfg.LastApplyError)
	if err != nil {
		return nil
	}
	_ = json.Unmarshal([]byte(collect), &cfg.Collect)
	if cfg.Collect == nil {
		cfg.Collect = []AgentCollectRequest{}
	}
	cfg.AppliedAt = appliedAt.Int64
	cfg.Configured = true
	cfg.Synchronized = cfg.AppliedAt > 0 && cfg.AppliedAt >= cfg.UpdatedAt && cfg.LastApplyError == ""
	cfg.TokenAvailable = agentDataToken() != ""
	return &cfg
}

func (s *Store) UpsertAgentDesiredConfig(hostName string, cfg AgentDesiredConfig) (*AgentDesiredConfig, error) {
	host := s.GetHost(hostName)
	if host == nil {
		return nil, fmt.Errorf("主机不存在")
	}
	cfg.HostName = hostName
	if err := normalizeAgentDesiredConfig(&cfg); err != nil {
		return nil, err
	}
	for i := range cfg.Collect {
		item := &cfg.Collect[i]
		var app *Service
		if item.ApplicationID != "" {
			app = s.GetServiceByApplicationID(item.ApplicationID)
		} else if item.Service != "" { // 兼容旧请求和旧配置
			app = s.GetService(item.Service)
		}
		if app != nil {
			item.ApplicationID, item.Service = app.ApplicationID, app.Name
			continue
		}
		// 日志、OTel 和部署共享平台应用目录。日志采集允许引用没有接入
		// 部署插件的应用；API 层已经向应用目录解析并确认稳定 ID。
		if strings.TrimSpace(item.ApplicationID) == "" || strings.TrimSpace(item.Service) == "" {
			return nil, fmt.Errorf("采集项 %d 未选择有效的平台应用", i+1)
		}
	}
	collect, err := json.Marshal(cfg.Collect)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	_, err = s.db.Exec(`INSERT INTO agent_configs(host_name,server_url,monitor_url,run_user,run_group,spool_max_mb,collect,updated_at,applied_at,last_apply_error)
		VALUES(?,?,?,?,?,?,?,?,0,'') ON CONFLICT(host_name) DO UPDATE SET
		server_url=excluded.server_url,monitor_url=excluded.monitor_url,run_user=excluded.run_user,run_group=excluded.run_group,
		spool_max_mb=excluded.spool_max_mb,collect=excluded.collect,updated_at=excluded.updated_at,applied_at=0,last_apply_error=''`,
		hostName, cfg.ServerURL, cfg.MonitorURL, cfg.RunUser, cfg.RunGroup, cfg.SpoolMaxMB, string(collect), now)
	if err != nil {
		return nil, err
	}
	return s.AgentDesiredConfig(hostName), nil
}

func (s *Store) MarkAgentConfigApplied(hostName string, applyErr error) {
	if applyErr != nil {
		_, _ = s.db.Exec(`UPDATE agent_configs SET last_apply_error=? WHERE host_name=?`, applyErr.Error(), hostName)
		return
	}
	_, _ = s.db.Exec(`UPDATE agent_configs SET applied_at=strftime('%s','now'),last_apply_error='' WHERE host_name=?`, hostName)
}

// RebindApplicationID adopts the platform catalog ID and keeps saved agent
// collection rules pointing at the same logical deployment application.
func (s *Store) RebindApplicationID(service, applicationID string) error {
	service, applicationID = strings.TrimSpace(service), strings.TrimSpace(applicationID)
	if service == "" || applicationID == "" {
		return fmt.Errorf("应用和 application_id 不能为空")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE services SET application_id=? WHERE name=?`, applicationID, service); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT host_name,collect FROM agent_configs`)
	if err != nil {
		return err
	}
	type update struct{ host, collect string }
	var updates []update
	for rows.Next() {
		var host, raw string
		if rows.Scan(&host, &raw) != nil {
			continue
		}
		var collect []AgentCollectRequest
		if json.Unmarshal([]byte(raw), &collect) != nil {
			continue
		}
		changed := false
		for i := range collect {
			if strings.EqualFold(collect[i].Service, service) && collect[i].ApplicationID != applicationID {
				collect[i].ApplicationID = applicationID
				changed = true
			}
		}
		if changed {
			encoded, _ := json.Marshal(collect)
			updates = append(updates, update{host: host, collect: string(encoded)})
		}
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, item := range updates {
		if _, err = tx.Exec(`UPDATE agent_configs SET collect=?,updated_at=?,applied_at=0 WHERE host_name=?`, item.collect, time.Now().Unix(), item.host); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ServiceUsedByAgentConfig(service string) (string, bool) {
	rows, err := s.db.Query(`SELECT host_name,collect FROM agent_configs`)
	if err != nil {
		return "", false
	}
	defer rows.Close()
	for rows.Next() {
		var hostName, raw string
		if rows.Scan(&hostName, &raw) != nil {
			continue
		}
		var collect []AgentCollectRequest
		_ = json.Unmarshal([]byte(raw), &collect)
		for _, item := range collect {
			if strings.EqualFold(item.Service, service) {
				return hostName, true
			}
		}
	}
	return "", false
}
