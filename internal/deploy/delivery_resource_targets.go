package deploy

import (
	"fmt"
	"strings"
)

type DeliveryResourceTarget struct {
	Skip              bool   `json:"skip,omitempty"`
	Reason            string `json:"reason,omitempty"`
	NodeID            string `json:"node_id"`
	OriginNodeID      string `json:"origin_node_id"`
	ChangeID          int64  `json:"change_id"`
	Service           string `json:"service"`
	Alias             string `json:"alias"`
	GroupName         string `json:"group_name,omitempty"`
	GroupRevision     int64  `json:"group_revision,omitempty"`
	Environment       string `json:"environment"`
	Connection        string `json:"connection"`
	Label             string `json:"label"`
	DestinationDigest string `json:"destination_digest"`
}

func (s *Store) deliveryChangeTarget(c *Change, environment string) (DeliveryResourceTarget, error) {
	t := DeliveryResourceTarget{ChangeID: c.ID, Service: c.Service, Environment: environment}
	resources := s.ApplicationResources(c.Service)
	if resources.Error != "" {
		return t, fmt.Errorf("%s", resources.Error)
	}
	t.GroupName, t.GroupRevision = resources.ResourceGroup, resources.GroupRevision
	var connection *ResourceConnection
	var err error
	switch c.Kind {
	case "mysql":
		if c.SQL == nil {
			return t, fmt.Errorf("变更 #%d 缺少结构化 SQL", c.ID)
		}
		t.Alias = c.SQL.ResourceAlias
		selected, _, selectionErr := s.selectEnvironmentSQL(c.SQL, environment)
		if selectionErr != nil {
			return t, selectionErr
		}
		if selected.Skip {
			t.Skip = true
			t.Reason = selected.Reason
			t.Label = "该环境无需执行 SQL"
			t.DestinationDigest = digestJSON([]any{"sql-environment-skip", c.ID, environment, c.SQL})
			return t, nil
		}

		connection, err = s.sqlBinding(c.Service, environment, t.Alias)
		if err == nil {
			t.Label = connection.Name + " / " + connection.Config["database"]
		}
	case "nacos":
		if c.Nacos == nil {
			return t, fmt.Errorf("变更 #%d 缺少结构化 Nacos 配置", c.ID)
		}
		t.Alias = c.Nacos.ResourceAlias
		connection, err = s.nacosBinding(c.Service, environment, t.Alias)
		if err == nil {
			var dataID, group string
			dataID, group, err = nacosResolvedTarget(connection, c.Nacos)
			t.Label = connection.Name + " / " + group + " / " + dataID
		}
	case "redis":
		if c.Redis == nil {
			return t, fmt.Errorf("变更 #%d 缺少 Redis 命令", c.ID)
		}
		t.Alias = c.Redis.ResourceAlias
		connection, err = s.redisBinding(c.Service, environment, t.Alias)
		if err == nil {
			t.Label = connection.Name + " / DB " + connection.Config["db"]
		}
	default:
		return t, fmt.Errorf("资源目标仅支持 SQL、Nacos、Redis")
	}
	if err != nil {
		return t, fmt.Errorf("变更 #%d：%w", c.ID, err)
	}
	for _, req := range resources.Requirements {
		if req.Alias == t.Alias && req.GroupName != "" {
			t.GroupName, t.GroupRevision = req.GroupName, req.GroupRevision
		}
	}
	t.Connection, t.DestinationDigest = connection.Name, sqlDestination(connection)
	return t, nil
}

// Only scope-driven resource nodes can be inapplicable. Explicit references and
// unscoped/fixed nodes must still undergo normal validation, even when invalid.
// Keep the template immutable and retain dependencies through skipped nodes.
func omitEmptyDeliveryResourceNodes(nodes []DeliveryNode, scope *DeliveryScope) ([]DeliveryNode, []string) {
	if scope == nil {
		return nodes, nil
	}
	skipped := map[string]DeliveryNode{}
	warnings := []string{}
	for _, node := range nodes {
		if (node.Kind != "mysql" && node.Kind != "nacos" && node.Kind != "redis") || len(node.ChangeIDs) > 0 {
			continue
		}
		matched := false
		for _, change := range scope.Changes {
			if change.Kind == node.Kind && (node.Service == "" || node.Service == change.Service) {
				matched = true
				break
			}
		}
		if !matched {
			skipped[node.ID] = node
			warnings = append(warnings, node.Name+"：本次交付范围无匹配变更，已跳过；流程模板保持不变")
		}
	}
	return withoutInapplicableNodes(nodes, skipped), warnings
}

func withoutInapplicableNodes(nodes []DeliveryNode, skipped map[string]DeliveryNode) []DeliveryNode {
	if len(skipped) == 0 {
		return nodes
	}
	out := []DeliveryNode{}
	for _, node := range nodes {
		if _, skip := skipped[node.ID]; skip {
			continue
		}
		if len(node.DependsOn) > 0 {
			node.DependsOn = append([]string(nil), node.DependsOn...)
			deps := []string{}
			seen := map[string]bool{}
			var retain func(string)
			retain = func(id string) {
				if seen[id] {
					return
				}
				seen[id] = true
				if omitted, ok := skipped[id]; ok {
					for _, dep := range omitted.DependsOn {
						retain(dep)
					}
					return
				}
				deps = append(deps, id)
			}
			for _, dep := range node.DependsOn {
				retain(dep)
			}
			node.DependsOn = deps
		}
		out = append(out, node)
	}
	return out
}

// Expand the logical node in-place, preserving approval/manual ordering and
// selection order. This is target resolution only: no network writes or reads.
func (s *Store) expandDeliveryResourceNodes(nodes []DeliveryNode, scope *DeliveryScope, environment string, skips ...map[string]string) ([]DeliveryNode, []DeliveryResourceTarget, error) {
	out := []DeliveryNode{}
	targets := []DeliveryResourceTarget{}
	for _, node := range nodes {
		if node.Kind != "mysql" && node.Kind != "nacos" && node.Kind != "redis" {
			out = append(out, node)
			continue
		}
		ids := append([]int64{}, node.ChangeIDs...)
		allowed := map[int64]bool{}
		if scope != nil {
			for _, c := range scope.Changes {
				if c.Kind == node.Kind {
					allowed[c.ID] = true
					if len(node.ChangeIDs) == 0 && (node.Service == "" || node.Service == c.Service) {
						ids = append(ids, c.ID)
					}
				}
			}
		}
		if len(ids) == 0 {
			out = append(out, node)
			continue
		}
		if len(ids) > 500 {
			return nil, nil, fmt.Errorf("单个逻辑资源节点最多关联 500 个变更")
		}
		seen := map[int64]bool{}
		buckets := map[string][]int64{}
		order := []string{}
		resolved := []DeliveryResourceTarget{}
		for _, id := range ids {
			if seen[id] {
				return nil, nil, fmt.Errorf("变更 #%d 在节点中重复", id)
			}
			seen[id] = true
			if scope != nil && !allowed[id] {
				return nil, nil, fmt.Errorf("节点引用版本外或工作项范围外变更 #%d", id)
			}
			c := s.GetChange(id)
			if c == nil || c.Kind != node.Kind || (node.Service != "" && c.Service != node.Service) {
				return nil, nil, fmt.Errorf("节点变更类型或应用不匹配 #%d", id)
			}
			if len(skips) > 0 {
				if _, ok := skips[0][node.ID]; ok {
					continue
				}
			}
			t, err := s.deliveryChangeTarget(c, environment)
			if err != nil {
				return nil, nil, fmt.Errorf("变更 #%d · %s：%w", id, c.Title, err)
			}
			key := t.Label // Connection is explicit; equivalent aliases still get safety checks below.
			if _, ok := buckets[key]; !ok {
				order = append(order, key)
			}
			buckets[key] = append(buckets[key], id)
			resolved = append(resolved, t)
		}
		if len(order) == 0 {
			node.ChangeIDs = ids
			out = append(out, node)
			continue
		}
		for _, key := range order {
			n := node
			n.ChangeIDs = append([]int64{}, buckets[key]...)
			if len(order) > 1 {
				hash := strings.TrimPrefix(digestJSON([]string{node.ID, node.Kind, key}), "sha256:")
				n.ID = "resource-" + hash[:32]
				n.Name = node.Name + " · " + key
			}
			out = append(out, n)
			for _, t := range resolved {
				if t.Label == key {
					t.NodeID, t.OriginNodeID = n.ID, node.ID
					targets = append(targets, t)
				}
			}
		}
	}
	if len(out) > 200 {
		return nil, nil, fmt.Errorf("目标拆分后最多 200 个顺序节点，请缩小本次交付范围")
	}
	seen := map[string]bool{}
	for _, n := range out {
		if seen[n.ID] {
			return nil, nil, fmt.Errorf("目标拆分后的节点标识重复，请重新配置")
		}
		seen[n.ID] = true
	}
	origins := map[string][]string{}
	for _, t := range targets {
		seen := false
		for _, id := range origins[t.OriginNodeID] {
			if id == t.NodeID {
				seen = true
			}
		}
		if !seen {
			origins[t.OriginNodeID] = append(origins[t.OriginNodeID], t.NodeID)
		}
	}
	for i, n := range out {
		if len(n.DependsOn) == 0 {
			continue
		}
		out[i].DependsOn = []string{}
		for _, dep := range n.DependsOn {
			ids := origins[dep]
			if len(ids) == 0 {
				ids = []string{dep}
			}
			out[i].DependsOn = append(out[i].DependsOn, ids...)
		}
	}
	if err := validateNodeDependencies(out); err != nil {
		return nil, nil, err
	}
	return out, targets, nil
}

// Used by SQL plans to detect aliases which actually point at the same database.
func (s *Store) sqlPhysicalTarget(connectionName string, database ...string) string {
	c := s.GetConnection(connectionName)
	if c == nil {
		return connectionName
	}
	name := c.Config["database"]
	if len(database) > 0 {
		name = database[0]
	}
	return digestJSON([]string{strings.ToLower(strings.TrimSpace(c.Endpoint)), name})
}
