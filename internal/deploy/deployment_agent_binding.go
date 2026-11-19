package deploy

import (
	"encoding/json"
	"fmt"
	"strings"
)

func deploymentAgentTarget(c map[string]string) bool {
	return c["deployment_agent_id"] != "" || c["deployment_agent_ids"] != "" || c["deployment_transport"] == "agent"
}
func deploymentAgentID(c map[string]string, host string) (string, error) {
	if raw := c["deployment_agent_ids"]; raw != "" {
		var ids map[string]string
		if len(raw) > 32768 || json.Unmarshal([]byte(raw), &ids) != nil || ids[host] == "" {
			return "", fmt.Errorf("缺少冻结的主机 Agent 绑定：%s", host)
		}
		return ids[host], nil
	}
	if id := strings.TrimSpace(c["deployment_agent_id"]); id != "" {
		return id, nil
	}
	return "", fmt.Errorf("Agent 绑定尚未冻结")
}

// Resolve once during submission; later approvals and dispatches use only the
// frozen host -> installation map, never the newest mutable registration.
func (s *Store) freezeDeploymentAgents(v *ServiceEnvironment) error {
	if !deploymentAgentTarget(v.DeployConfig) {
		return nil
	}
	config := map[string]string{}
	for k, value := range v.DeployConfig {
		config[k] = value
	}
	v.DeployConfig = config
	ids := map[string]string{}
	if raw := config["deployment_agent_ids"]; raw != "" {
		if len(raw) > 32768 || json.Unmarshal([]byte(raw), &ids) != nil {
			return fmt.Errorf("主机 Agent 绑定无效")
		}
	}
	if len(ids) > 0 {
		for host := range ids {
			found := false
			for _, h := range v.Hosts {
				if host == h {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("Agent 绑定包含未选择的主机")
			}
		}
	}
	for _, host := range v.Hosts {
		id := ids[host]
		if id == "" {
			id = config["deployment_agent_id"]
		}
		if id == "" && config["deployment_transport"] == "agent" {
			rows, e := s.db.Query(`SELECT agent_id FROM deployment_agents WHERE host_name=? AND environment=? AND enabled=1`, host, v.Environment)
			if e != nil {
				return e
			}
			candidates := []string{}
			for rows.Next() {
				var id string
				if e = rows.Scan(&id); e != nil {
					rows.Close()
					return e
				}
				candidates = append(candidates, id)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return e
			}
			allowed := []string{}
			for _, candidate := range candidates {
				a := s.deploymentAgent(candidate)
				if a != nil {
					for _, service := range a.Services {
						if service == v.Service {
							allowed = append(allowed, candidate)
							break
						}
					}
				}
			}
			if len(allowed) != 1 {
				return fmt.Errorf("主机 %s 需要唯一、已授权的部署 Agent，当前匹配 %d 个", host, len(allowed))
			}
			id = allowed[0]
		}
		if id == "" {
			return fmt.Errorf("主机 %s 没有 Agent 绑定", host)
		}
		ids[host] = id
	}
	raw, _ := json.Marshal(ids)
	config["deployment_agent_ids"] = string(raw)
	config["deployment_agent_protocol"] = "2"
	for _, host := range v.Hosts {
		h := s.GetHost(host)
		if h == nil {
			return fmt.Errorf("主机不存在")
		}
		if e := s.validateDeploymentAgent(v.Apply(s.GetService(v.Service)), *h); e != nil {
			return e
		}
	}
	return nil
}
