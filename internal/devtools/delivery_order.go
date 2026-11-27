package devtools

import "fmt"

// Copy executable definitions only from the server draft. MCP supplies order
// and SQL partitions, never new scripts, targets, or approval definitions.
func deliveryOrderedNodes(generated, requested []any) ([]any, error) {
	byID := map[string]map[string]any{}
	for _, raw := range generated {
		n := mapValue(raw)
		byID[stringArg(n, "id")] = n
	}
	out := []any{}
	for _, raw := range requested {
		selection := mapValue(raw)
		for key := range selection {
			if key != "id" && key != "sql_group" && key != "change_ids" && key != "depends_on" {
				return nil, fmt.Errorf("执行顺序不能改写节点字段 %s", key)
			}
		}
		id, group := stringArg(selection, "id"), stringArg(selection, "sql_group")
		if !safeDeliveryID(id) {
			return nil, fmt.Errorf("节点 ID 无效")
		}
		original := id
		if group != "" {
			original = group
		}
		base := byID[original]
		if base == nil {
			return nil, fmt.Errorf("节点 %s 不属于原交付计划", original)
		}
		node := mapValue(cloneJSON(base))
		ids, partition := selection["change_ids"]
		if group != "" || partition {
			if stringArg(base, "kind") != "mysql" {
				return nil, fmt.Errorf("只能拆分原 SQL 节点")
			}
			if !partition {
				return nil, fmt.Errorf("SQL 拆分需明确变更编号")
			}
			node["change_ids"] = cloneJSON(ids)
			if group != "" {
				node["sql_group"] = group
				node["name"] = stringArg(base, "name") + " · 分阶段"
			}
		}
		if raw, ok := selection["depends_on"]; ok {
			deps, ok := raw.([]any)
			if !ok || len(deps) > 30 {
				return nil, fmt.Errorf("前置依赖列表无效")
			}
			for _, dep := range deps {
				id, ok := dep.(string)
				if !ok || !safeDeliveryID(id) {
					return nil, fmt.Errorf("前置依赖标识无效")
				}
			}
			node["depends_on"] = cloneJSON(deps)
		}
		node["id"] = id
		out = append(out, node)
	}
	return out, nil // Backend validates exact coverage, dependency order and gates.
}
