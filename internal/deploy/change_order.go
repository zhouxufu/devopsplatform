package deploy

import (
	"fmt"
	"slices"
	"strings"
)

type ChangePrerequisite struct {
	ChangeID int64  `json:"change_id"`
	Reason   string `json:"reason"`
}

func validateChangePrerequisites(change *Change) error {
	if len(change.Prerequisites) > 32 {
		return fmt.Errorf("单项变更最多关联 32 个前置变更")
	}
	seen := map[int64]bool{}
	for i := range change.Prerequisites {
		dep := &change.Prerequisites[i]
		dep.Reason = strings.TrimSpace(dep.Reason)
		if dep.ChangeID < 1 || dep.ChangeID == change.ID || seen[dep.ChangeID] || dep.Reason == "" || len(dep.Reason) > 1000 {
			return fmt.Errorf("前置变更编号须为其他已保存的变更、不能重复，并需填写 1–1000 字节的原因")
		}
		seen[dep.ChangeID] = true
	}
	return nil
}

func changeNodeMatches(node DeliveryNode, c Change) bool {
	kind := c.Kind
	if kind == "code" || kind == "artifact" {
		kind = "deploy"
	}
	if node.Kind != kind || node.Service != "" && node.Service != c.Service {
		return false
	}
	if node.Kind == "deploy" {
		return true
	}
	if len(node.ChangeIDs) > 0 {
		return slices.Contains(node.ChangeIDs, c.ID)
	}
	return kind == "mysql" || kind == "nacos" || kind == "redis"
}

// Build the order only from explicit change dependencies. A workflow gate is
// a boundary: moving a business operation across it needs a new workflow.
func orderChangeNodes(nodes []DeliveryNode, scope *DeliveryScope, suggest bool) ([]DeliveryNode, error) {
	relevant := map[int64]bool{}
	changes := map[int64]Change{}
	if scope != nil {
		for _, c := range scope.Changes {
			changes[c.ID] = c
			for _, dep := range c.Prerequisites {
				relevant[dep.ChangeID] = true
				relevant[c.ID] = true
			}
		}
	}
	if len(relevant) == 0 {
		return nodes, nil
	}
	if suggest {
		var split []DeliveryNode
		replacements := map[string][]string{}
		for _, node := range nodes {
			var matches []int64
			touched := false
			if node.Kind == "mysql" {
				for _, c := range scope.Changes {
					if changeNodeMatches(node, c) {
						matches = append(matches, c.ID)
						touched = touched || relevant[c.ID]
					}
				}
			}
			if !touched || len(matches) < 2 {
				split = append(split, node)
				continue
			}
			if len(node.ChangeIDs) > 0 {
				matches = node.ChangeIDs
			}
			// Preserve declared SQL order, grouping consecutive unrelated records.
			var groups [][]int64
			for i, id := range matches {
				if i == 0 || relevant[id] || relevant[matches[i-1]] {
					groups = append(groups, []int64{})
				}
				last := len(groups) - 1
				groups[last] = append(groups[last], id)
			}
			for i, ids := range groups {
				part := node
				if i > 0 {
					part.ID = standardNodeID("change-stage", node.ID, fmt.Sprint(ids[0]))
				}
				part.ChangeIDs = ids
				part.Name = fmt.Sprintf("%s · #%d", node.Name, ids[0])
				split = append(split, part)
				replacements[node.ID] = append(replacements[node.ID], part.ID)
			}
		}
		if len(split) > 30 {
			return nil, fmt.Errorf("变更拆分后超过 30 个执行步骤，请缩小发布范围")
		}
		for i, node := range split {
			var deps []string
			for _, id := range node.DependsOn {
				if ids := replacements[id]; len(ids) > 0 {
					deps = append(deps, ids...)
				} else {
					deps = append(deps, id)
				}
			}
			if len(deps) > 0 {
				split[i].DependsOn = deps
			}
		}
		nodes = split
	}
	positions := map[int64]int{}
	for id := range relevant {
		c, ok := changes[id]
		if !ok {
			return nil, fmt.Errorf("前置变更 #%d 未选入本次范围，请核对；不会自动追加", id)
		}
		pos := -1
		for i, node := range nodes {
			if changeNodeMatches(node, c) {
				if pos >= 0 {
					return nil, fmt.Errorf("变更 #%d 匹配多个发布步骤，请明确拆分范围", id)
				}
				pos = i
			}
		}
		if pos < 0 {
			return nil, fmt.Errorf("变更 #%d 没有对应的执行步骤", id)
		}
		positions[id] = pos
	}
	edges := make([]map[int]bool, len(nodes))
	for i := range nodes {
		edges[i] = map[int]bool{}
	}
	for _, c := range scope.Changes {
		for _, dep := range c.Prerequisites {
			from, to := positions[dep.ChangeID], positions[c.ID]
			if from == to || !suggest && from > to {
				return nil, fmt.Errorf("#%d 必须先于 #%d 单独完成：%s", dep.ChangeID, c.ID, dep.Reason)
			}
			edges[to][from] = true
		}
	}
	if !suggest {
		return nodes, nil
	}
	index := map[string]int{}
	for i, node := range nodes {
		if _, ok := index[node.ID]; ok {
			return nil, fmt.Errorf("发布步骤编号重复：%s", node.ID)
		}
		index[node.ID] = i
	}
	for i, node := range nodes {
		for _, id := range node.DependsOn {
			from, ok := index[id]
			if !ok {
				return nil, fmt.Errorf("步骤 %s 缺少前置步骤 %s", node.Name, id)
			}
			edges[i][from] = true
		}
		if !releaseOrderBarrier(node) {
			continue
		}
		for j := 0; j < i; j++ {
			edges[i][j] = true
		}
		for j := i + 1; j < len(nodes); j++ {
			edges[j][i] = true
		}
	}
	result := make([]DeliveryNode, 0, len(nodes))
	for len(result) < len(nodes) {
		next := -1
		for i, deps := range edges {
			if deps != nil && len(deps) == 0 {
				next = i
				break
			}
		}
		if next < 0 {
			var names []string
			for i, deps := range edges {
				if deps != nil {
					names = append(names, nodes[i].Name)
				}
			}
			return nil, fmt.Errorf("发布依赖成环或与流程关卡冲突：%s", strings.Join(names, "、"))
		}
		result = append(result, nodes[next])
		edges[next] = nil
		for _, deps := range edges {
			delete(deps, next)
		}
	}
	return result, nil
}

func releaseOrderBarrier(node DeliveryNode) bool {
	switch node.Kind {
	case "deploy", "mysql", "redis", "nacos":
		return false
	case "manual":
		return len(node.ChangeIDs) == 0 || len(node.Approvers) > 0
	default:
		return true
	}
}

// A user adjustment must preserve each generated step's position relative to
// the workflow gates used to compute the dependency order, including SQL splits.
func validateChangeOrderGates(draft *DeliveryDraftPlan, actual []DeliveryNode) error {
	if draft.Scope == nil || !slices.ContainsFunc(draft.Scope.Changes, func(c Change) bool { return len(c.Prerequisites) > 0 }) {
		return nil
	}
	segments := map[string]int{}
	var gates []DeliveryNode
	for _, node := range draft.Nodes {
		if releaseOrderBarrier(node) {
			gates = append(gates, node)
		} else {
			segments[node.ID] = len(gates)
		}
	}
	if len(gates) == 0 {
		return nil
	}
	segment := 0
	for _, node := range actual {
		if releaseOrderBarrier(node) {
			if segment >= len(gates) || digestJSON(node) != digestJSON(gates[segment]) {
				return fmt.Errorf("发布依赖所用的流程关卡发生变化，请重新预检")
			}
			segment++
			continue
		}
		origin := node.ID
		if node.SQLGroup != "" {
			origin = node.SQLGroup
		}
		if expected, ok := segments[origin]; !ok || expected != segment {
			return fmt.Errorf("步骤 %s 不能跨越原发布流程关卡", node.Name)
		}
	}
	if segment != len(gates) {
		return fmt.Errorf("发布顺序缺少原流程关卡")
	}
	return nil
}
