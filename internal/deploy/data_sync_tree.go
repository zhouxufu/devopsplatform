package deploy

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// IDs describe physical relationships; Keys describe the same business menu in
// different databases. Never use a source parent ID directly in the target.
type DataSyncTreeConfig struct {
	IDField     string `json:"id_field"`
	ParentField string `json:"parent_field"`
	LabelField  string `json:"label_field"`
	RootValue   string `json:"root_value"`
}
type DataSyncTreeNode struct {
	Key       string `json:"key"`
	ParentKey string `json:"parent_key,omitempty"`
	Label     string `json:"label"`
	Depth     int    `json:"depth"`
	Kind      string `json:"kind"`
	SourceID  string `json:"source_id"`
	TargetID  string `json:"target_id"`
}

func normalizeSyncTree(t *DataSyncTask, columns []string) error {
	if t.Mode == "" || t.Mode == "table" {
		if t.Tree != nil {
			return fmt.Errorf("树形字段只适用于树状层级模式")
		}
		return nil
	}
	if t.Mode != "tree" || t.Tree == nil {
		return fmt.Errorf("请选择表格或树状层级，并配置层级字段")
	}
	cfg := t.Tree
	cfg.IDField, cfg.ParentField, cfg.LabelField = strings.TrimSpace(cfg.IDField), strings.TrimSpace(cfg.ParentField), strings.TrimSpace(cfg.LabelField)
	seen := map[string]bool{}
	for _, field := range []string{cfg.IDField, cfg.ParentField, cfg.LabelField} {
		found := false
		for _, col := range columns {
			if col == field {
				found = true
			}
		}
		if !found || seen[field] {
			return fmt.Errorf("ID、父 ID、名称字段须为三个不同的查询字段")
		}
		seen[field] = true
	}
	for _, key := range t.Keys {
		if key == cfg.IDField || key == cfg.ParentField {
			return fmt.Errorf("树形匹配键请使用菜单编码、权限标识等稳定字段，不使用 ID 或父 ID")
		}
	}
	cfg.RootValue = strings.TrimSpace(cfg.RootValue)
	if cfg.RootValue == "" {
		cfg.RootValue = "0"
	}
	if _, err := syncTreeNumber(cfg.RootValue); err != nil {
		return fmt.Errorf("根节点父 ID 需为整数；NULL 父 ID 同样视为根节点")
	}
	return nil
}
func syncExecutionKeys(keys []string, tree *DataSyncTreeConfig) []string {
	if tree != nil {
		return []string{tree.IDField}
	}
	return keys
}
func syncTreeNumber(value string) (*big.Int, error) {
	n, ok := new(big.Int).SetString(value, 10)
	if !ok || n.String() != value {
		return nil, fmt.Errorf("树形 ID 必须为规范整数：%s", value)
	}
	return n, nil
}
func treeRoot(cfg *DataSyncTreeConfig, value *string) bool {
	return value == nil || *value == cfg.RootValue
}
func treeID(row SyncRow, cfg *DataSyncTreeConfig) (string, error) {
	id := row[cfg.IDField]
	if id == nil || *id == cfg.RootValue {
		return "", fmt.Errorf("节点 ID 不能为空或等于根节点父 ID")
	}
	if _, err := syncTreeNumber(*id); err != nil {
		return "", err
	}
	return *id, nil
}

// Require complete ancestor chains in each query scope. Guessing a missing
// ancestor by name or treating it as a root would silently corrupt the menu.
func syncTreeGraph(rows []SyncRow, cfg *DataSyncTreeConfig) (map[string]SyncRow, map[string]int, error) {
	nodes := map[string]SyncRow{}
	for _, row := range rows {
		id, err := treeID(row, cfg)
		if err != nil {
			return nil, nil, err
		}
		if _, ok := row[cfg.ParentField]; !ok {
			return nil, nil, fmt.Errorf("缺少父 ID 字段")
		}
		if nodes[id] != nil {
			return nil, nil, fmt.Errorf("节点 ID %s 重复", id)
		}
		nodes[id] = row
	}
	depths, visiting := map[string]int{}, map[string]bool{}
	var visit func(string) (int, error)
	visit = func(id string) (int, error) {
		if depth, ok := depths[id]; ok {
			return depth, nil
		}
		if visiting[id] {
			return 0, fmt.Errorf("菜单存在循环父子关系，涉及 ID %s", id)
		}
		visiting[id] = true
		depth := 0
		parent := nodes[id][cfg.ParentField]
		if !treeRoot(cfg, parent) {
			if _, err := syncTreeNumber(*parent); err != nil {
				return 0, err
			}
			if nodes[*parent] == nil {
				return 0, fmt.Errorf("节点 %s 的父节点 %s 不在查询范围，请将祖先节点纳入查询或核对根节点父 ID", id, *parent)
			}
			d, err := visit(*parent)
			if err != nil {
				return 0, err
			}
			depth = d + 1
		}
		visiting[id] = false
		depths[id] = depth
		return depth, nil
	}
	for id := range nodes {
		if _, err := visit(id); err != nil {
			return nil, nil, err
		}
	}
	return nodes, depths, nil
}
func treeIntegerMax(col sqlColumn) (*big.Int, error) {
	typ := strings.ToLower(col.Type)
	bits := uint(0)
	parts := strings.Fields(typ)
	if len(parts) == 0 {
		return nil, fmt.Errorf("缺少树形 ID 字段类型")
	}
	switch parts[0] {
	case "tinyint":
		bits = 8
	case "smallint":
		bits = 16
	case "mediumint":
		bits = 24
	case "int", "integer":
		bits = 32
	case "bigint":
		bits = 64
	default:
		return nil, fmt.Errorf("树形迁移目前要求 ID 和父 ID 为整数类型")
	}
	if !strings.Contains(typ, "unsigned") {
		bits--
	}
	return new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), bits), big.NewInt(1)), nil
}
func treeIDBase(ctx context.Context, db *sql.DB, table string, cfg *DataSyncTreeConfig, schema *sqlTable) (*big.Int, error) {
	for _, name := range []string{cfg.IDField, cfg.ParentField} {
		if _, err := treeIntegerMax(schema.Columns[name]); err != nil {
			return nil, err
		}
	}
	var max sql.NullString
	// A scalar indexed MAX covers IDs outside the selected WHERE range too.
	if err := db.QueryRowContext(ctx, "SELECT MAX(`"+cfg.IDField+"`) FROM `"+table+"`").Scan(&max); err != nil {
		return nil, fmt.Errorf("读取目标最大 ID 失败：%w", err)
	}
	base := big.NewInt(0)
	if max.Valid {
		n, err := syncTreeNumber(max.String)
		if err != nil {
			return nil, err
		}
		if n.Sign() > 0 {
			base = n
		}
	}
	root, _ := syncTreeNumber(cfg.RootValue)
	if root.Cmp(base) > 0 {
		base = new(big.Int).Set(root)
	}
	return base, nil
}
func copySyncRow(row SyncRow) SyncRow {
	out := SyncRow{}
	for k, v := range row {
		if v != nil {
			value := *v
			out[k] = &value
		} else {
			out[k] = nil
		}
	}
	return out
}
func stringSyncValue(s string) *string { return &s }
func compareSyncTreeRows(t *DataSyncTask, scope DataSyncScope, source, target []SyncRow, base *big.Int, schema *sqlTable) (*DataSyncComparison, error) {
	cfg := t.Tree
	src, depths, err := syncTreeGraph(source, cfg)
	if err != nil {
		return nil, fmt.Errorf("来源：%w", err)
	}
	if _, _, err = syncTreeGraph(target, cfg); err != nil {
		return nil, fmt.Errorf("目标：%w", err)
	}
	for _, rows := range [][]SyncRow{source, target} {
		for _, row := range rows {
			for _, key := range t.Keys {
				if row[key] == nil || strings.TrimSpace(*row[key]) == "" {
					return nil, fmt.Errorf("树形匹配键 %s 不能为空；目录节点也需稳定编码，请选择其他字段或组合键", key)
				}
			}
		}
	}
	a, err := indexSyncRows(source, t.Keys)
	if err != nil {
		return nil, fmt.Errorf("来源：%w", err)
	}
	b, err := indexSyncRows(target, t.Keys)
	if err != nil {
		return nil, fmt.Errorf("目标：%w", err)
	}
	ids := map[string]string{}
	keys := []string{}
	for key := range a {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	next := new(big.Int).Set(base)
	maxID, err := treeIntegerMax(schema.Columns[cfg.IDField])
	if err != nil {
		return nil, err
	}
	maxParent, err := treeIntegerMax(schema.Columns[cfg.ParentField])
	if err != nil {
		return nil, err
	}
	if maxParent.Cmp(maxID) < 0 {
		maxID = maxParent
	}
	for _, key := range keys {
		id, _ := treeID(a[key], cfg)
		if before := b[key]; before != nil {
			ids[id] = *before[cfg.IDField]
		} else {
			next.Add(next, big.NewInt(1))
			if next.Cmp(maxID) > 0 {
				return nil, fmt.Errorf("目标 ID 或父 ID 字段已无可用整数空间")
			}
			ids[id] = next.String()
		}
	}
	mapped := []SyncRow{}
	for _, key := range keys {
		row := copySyncRow(a[key])
		id, _ := treeID(row, cfg)
		row[cfg.IDField] = stringSyncValue(ids[id])
		if !treeRoot(cfg, row[cfg.ParentField]) {
			row[cfg.ParentField] = stringSyncValue(ids[*row[cfg.ParentField]])
		} else if before := b[key]; before != nil && treeRoot(cfg, before[cfg.ParentField]) {
			row[cfg.ParentField] = before[cfg.ParentField]
		} else {
			row[cfg.ParentField] = stringSyncValue(cfg.RootValue)
		}
		mapped = append(mapped, row)
	}
	out, err := compareSyncRows(t, scope, mapped, target)
	if err != nil {
		return nil, err
	}
	kinds := map[string]string{}
	for _, d := range out.Differences {
		kinds[d.Key] = d.Kind
	}
	// Carry unchanged ancestors for the tree view and target-only nodes explicitly.
	for _, key := range keys {
		row := a[key]
		id, _ := treeID(row, cfg)
		label := key
		if row[cfg.LabelField] != nil {
			label = *row[cfg.LabelField]
		}
		parentKey := ""
		if !treeRoot(cfg, row[cfg.ParentField]) {
			parentKey, _ = syncKey(src[*row[cfg.ParentField]], t.Keys)
		}
		kind := kinds[key]
		if kind == "" {
			kind = "unchanged"
		}
		out.TreeNodes = append(out.TreeNodes, DataSyncTreeNode{Key: key, ParentKey: parentKey, Label: label, Depth: depths[id], Kind: kind, SourceID: id, TargetID: ids[id]})
	}
	targetIDs, targetDepths, _ := syncTreeGraph(target, cfg)
	for key, row := range b {
		if a[key] == nil {
			id, _ := treeID(row, cfg)
			label := key
			if row[cfg.LabelField] != nil {
				label = *row[cfg.LabelField]
			}
			parentKey := ""
			if !treeRoot(cfg, row[cfg.ParentField]) {
				parentKey, _ = syncKey(targetIDs[*row[cfg.ParentField]], t.Keys)
			}
			out.TreeNodes = append(out.TreeNodes, DataSyncTreeNode{Key: key, ParentKey: parentKey, Label: label, Kind: "target_only", Depth: targetDepths[id], TargetID: id})
		}
	}
	sort.Slice(out.TreeNodes, func(i, j int) bool { return out.TreeNodes[i].Key < out.TreeNodes[j].Key })
	for _, row := range target {
		out.TreeBaseline = append(out.TreeBaseline, SyncRow{cfg.IDField: row[cfg.IDField], cfg.ParentField: row[cfg.ParentField]})
	}
	// Bind original source IDs as well as the mapped result, independent of query order.
	out.Digest = digestJSON([]any{out.Digest, syncRowsDigest(source), out.TreeNodes})
	return out, nil
}

// Validate the final combined target tree, including rows the user did not
// select. This catches missing new parents and cycles introduced by partial moves.
func orderSyncTreeSelection(c *DataSyncComparison, selected []DataSyncSelection) ([]DataSyncSelection, error) {
	cfg := c.Task.Tree
	baseline, _, err := syncTreeGraph(c.TreeBaseline, cfg)
	if err != nil {
		return nil, err
	}
	known := map[string]DataSyncDifference{}
	for _, d := range c.Differences {
		known[d.Key] = d
	}
	seen, ids := map[string]bool{}, map[string]bool{}
	for _, pick := range selected {
		d, ok := known[pick.Key]
		if !ok || seen[pick.Key] {
			return nil, fmt.Errorf("选择了不存在或重复的差异")
		}
		seen[pick.Key] = true
		id, e := treeID(pick.After, cfg)
		if e != nil {
			return nil, e
		}
		if ids[id] {
			return nil, fmt.Errorf("迁移后的节点 ID 重复")
		}
		ids[id] = true
		if d.Kind == "insert" {
			if baseline[id] != nil {
				return nil, fmt.Errorf("新增节点 ID 与目标已有节点冲突")
			}
		} else {
			prior := baseline[id]
			if prior == nil || digestJSON(prior[cfg.ParentField]) != digestJSON(d.Before[cfg.ParentField]) || digestJSON(d.Before[cfg.IDField]) != digestJSON(pick.After[cfg.IDField]) {
				return nil, fmt.Errorf("目标节点映射与原层级不一致")
			}
		}
	}
	final := map[string]SyncRow{}
	for id, row := range baseline {
		final[id] = row
	}
	for _, pick := range selected {
		id, _ := treeID(pick.After, cfg)
		final[id] = pick.After
	}
	rows := []SyncRow{}
	for _, row := range final {
		rows = append(rows, row)
	}
	_, depths, err := syncTreeGraph(rows, cfg)
	if err != nil {
		return nil, fmt.Errorf("所选差异不能形成完整层级；请一并选择所需父节点或层级调整：%w", err)
	}
	ordered := append([]DataSyncSelection{}, selected...)
	sort.Slice(ordered, func(i, j int) bool {
		a, _ := treeID(ordered[i].After, cfg)
		b, _ := treeID(ordered[j].After, cfg)
		if depths[a] == depths[b] {
			return ordered[i].Key < ordered[j].Key
		}
		return depths[a] < depths[b]
	})
	return ordered, nil
}

// Re-read on the same transaction before committing, using actual MySQL WHERE
// and type semantics rather than approximating them in Go or JavaScript.
func verifySyncTreeResult(rows []SyncRow, snap *DataSyncSnapshot) error {
	cfg := snap.Tree
	ids, _, err := syncTreeGraph(rows, cfg)
	if err != nil {
		return err
	}
	if _, err := indexSyncRows(rows, snap.Keys); err != nil {
		return err
	}
	for _, diff := range snap.Differences {
		id, err := treeID(diff.After, cfg)
		if err != nil {
			return err
		}
		actual := ids[id]
		if actual == nil {
			return fmt.Errorf("节点 %s 未出现在目标查询内；请使用稳定业务字段筛选，不按来源 ID 范围筛选", id)
		}
		key, err := syncKey(actual, snap.Keys)
		if err != nil || key != diff.Key || digestJSON(actual[cfg.ParentField]) != digestJSON(diff.After[cfg.ParentField]) {
			return fmt.Errorf("节点 %s 的匹配键或父 ID 与冻结计划不一致", id)
		}
	}
	return nil
}
