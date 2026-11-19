package deploy

import (
	"encoding/json"
	"math/big"
	"strings"
	"testing"
)

func treeTestTask() *DataSyncTask {
	return &DataSyncTask{ID: 1, Revision: 1, Name: "菜单", Service: "api", ResourceAlias: "db", Mode: "tree", SourceSQL: "SELECT id,parent_id,code,name FROM menu", TargetSQL: "SELECT id,parent_id,code,name FROM menu", Keys: []string{"code"}, Tree: &DataSyncTreeConfig{IDField: "id", ParentField: "parent_id", LabelField: "name", RootValue: "0"}}
}
func treeTestRow(id, parent, code, name string) SyncRow {
	return SyncRow{"id": stringSyncValue(id), "parent_id": stringSyncValue(parent), "code": stringSyncValue(code), "name": stringSyncValue(name)}
}
func treeTestSchema(typ string) *sqlTable {
	return &sqlTable{Columns: map[string]sqlColumn{"id": {Type: typ}, "parent_id": {Type: typ}}}
}
func treeTestCompare(t *testing.T, source, target []SyncRow) *DataSyncComparison {
	t.Helper()
	c, err := compareSyncTreeRows(treeTestTask(), DataSyncScope{SourceEnvironment: "test", TargetEnvironment: "prod"}, source, target, big.NewInt(900), treeTestSchema("bigint"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestDataSyncTreeMappingAndFreeze(t *testing.T) {
	source := []SyncRow{treeTestRow("1", "0", "root", "系统"), treeTestRow("2", "1", "users", "用户"), treeTestRow("3", "2", "button", "新增"), treeTestRow("4", "0", "newroot", "新目录")}
	target := []SyncRow{treeTestRow("100", "0", "root", "系统"), treeTestRow("200", "100", "users", "旧用户"), treeTestRow("800", "100", "target-only", "保留")}
	c := treeTestCompare(t, source, target)
	if c.Unchanged != 1 || c.TargetOnly != 1 || len(c.Differences) != 3 {
		t.Fatalf("incorrect comparison: %+v", c)
	}
	byKey := map[string]DataSyncDifference{}
	for _, d := range c.Differences {
		byKey[d.Key] = d
	}
	users := byKey[`["users"]`]
	button := byKey[`["button"]`]
	root := byKey[`["newroot"]`]
	if *users.After["id"] != "200" || *users.After["parent_id"] != "100" || *button.After["parent_id"] != "200" || *button.After["id"] != "901" || *root.After["id"] != "902" {
		t.Fatalf("wrong ID mapping: %+v", byKey)
	}
	if *source[0]["id"] != "1" || *source[1]["parent_id"] != "1" {
		t.Fatal("source mutated")
	}
	reordered := treeTestCompare(t, []SyncRow{source[3], source[2], source[1], source[0]}, []SyncRow{target[2], target[1], target[0]})
	if reordered.Digest != c.Digest {
		t.Fatal("tree digest depends on query order")
	}
	selection := []DataSyncSelection{{Key: button.Key, After: button.After}, {Key: users.Key, After: users.After}, {Key: root.Key, After: root.After}}
	script, err := syncSelectionSQL(c, selection)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(script, "DELETE") || strings.Contains(script, "WHERE `code`") || !strings.Contains(script, "WHERE `id`='200'") || strings.Contains(script, "SET `id`") {
		t.Fatal(script)
	}
	if strings.Index(script, "UPDATE") > strings.LastIndex(script, "INSERT") {
		t.Fatal("parent/child not topologically ordered")
	}
	snap := &DataSyncSnapshot{TaskID: 1, TaskRevision: 1, TargetSQL: c.Task.TargetSQL, Keys: c.Task.Keys, Tree: c.Task.Tree, TreeBaseline: c.TreeBaseline, TargetDestination: "db", TargetDigest: c.TargetDigest, ComparisonDigest: c.Digest}
	for _, p := range selection {
		d := byKey[p.Key]
		d.After = p.After
		snap.Differences = append(snap.Differences, d)
	}
	change := &Change{SQL: &SQLChange{Content: script, DataSync: snap}}
	if err := validateSyncScript(change); err != nil {
		t.Fatal(err)
	}
	change.SQL.Content += " DELETE FROM menu WHERE id=800;"
	if err := validateSyncScript(change); err == nil {
		t.Fatal("tampered script accepted")
	}
	edited := copySyncRow(button.After)
	edited["parent_id"] = stringSyncValue("100")
	if _, err := syncSelectionSQL(c, []DataSyncSelection{{Key: button.Key, After: edited}}); err == nil {
		t.Fatal("edited mapped parent allowed")
	}
	edited = copySyncRow(button.After)
	edited["id"] = stringSyncValue("999")
	if _, err := syncSelectionSQL(c, []DataSyncSelection{{Key: button.Key, After: edited}}); err == nil {
		t.Fatal("edited mapped ID allowed")
	}
	edited = copySyncRow(button.After)
	edited["name"] = stringSyncValue("可编辑名称")
	if _, err := syncSelectionSQL(c, []DataSyncSelection{{Key: button.Key, After: edited}}); err != nil {
		t.Fatal(err)
	}
}
func TestDataSyncTreeParentDependenciesAndPartialMoveCycle(t *testing.T) {
	c := treeTestCompare(t, []SyncRow{treeTestRow("1", "0", "z-parent", "父"), treeTestRow("2", "1", "a-child", "子")}, nil)
	parent, child := c.Differences[1], c.Differences[0]
	if _, err := syncSelectionSQL(c, []DataSyncSelection{{Key: child.Key, After: child.After}}); err == nil || !strings.Contains(err.Error(), "父节点") {
		t.Fatalf("missing ancestor allowed: %v", err)
	}
	script, err := syncSelectionSQL(c, []DataSyncSelection{{Key: child.Key, After: child.After}, {Key: parent.Key, After: parent.After}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Index(script, "z-parent") > strings.Index(script, "a-child") {
		t.Fatal(script)
	}
	c = treeTestCompare(t, []SyncRow{treeTestRow("1", "0", "a", "甲"), treeTestRow("2", "1", "b", "乙")}, []SyncRow{treeTestRow("100", "200", "a", "甲"), treeTestRow("200", "0", "b", "乙")})
	b := c.Differences[1]
	if _, err := syncSelectionSQL(c, []DataSyncSelection{{Key: b.Key, After: b.After}}); err == nil || !strings.Contains(err.Error(), "循环") {
		t.Fatalf("partial move cycle allowed: %v", err)
	}
	if _, err := syncSelectionSQL(c, []DataSyncSelection{{Key: b.Key, After: b.After}, {Key: c.Differences[0].Key, After: c.Differences[0].After}}); err != nil {
		t.Fatal(err)
	}
}
func TestDataSyncTreeRejectsAmbiguity(t *testing.T) {
	cases := map[string][]SyncRow{
		"cycle":          {treeTestRow("1", "2", "a", "甲"), treeTestRow("2", "1", "b", "乙")},
		"self":           {treeTestRow("1", "1", "a", "甲")},
		"orphan":         {treeTestRow("1", "9", "a", "甲")},
		"duplicate-id":   {treeTestRow("1", "0", "a", "甲"), treeTestRow("1", "0", "b", "乙")},
		"duplicate-code": {treeTestRow("1", "0", "a", "甲"), treeTestRow("2", "0", "a", "乙")},
		"empty-code":     {treeTestRow("1", "0", "", "甲")},
		"invalid-id":     {treeTestRow("01", "0", "a", "甲")},
		"root-id":        {treeTestRow("0", "0", "a", "甲")},
	}
	for name, rows := range cases {
		t.Run(name, func(t *testing.T) {
			for _, sides := range [][2][]SyncRow{{rows, nil}, {nil, rows}} {
				if _, err := compareSyncTreeRows(treeTestTask(), DataSyncScope{}, sides[0], sides[1], big.NewInt(10), treeTestSchema("int")); err == nil {
					t.Fatal("invalid graph accepted")
				}
			}
		})
	}
	task := treeTestTask()
	cols := []string{"id", "parent_id", "code", "name"}
	if err := normalizeSyncTask(task); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*DataSyncTask){func(t *DataSyncTask) { t.Keys = []string{"id"} }, func(t *DataSyncTask) { t.Tree.ParentField = "id" }, func(t *DataSyncTask) { t.Tree.LabelField = "missing" }, func(t *DataSyncTask) { t.Tree.RootValue = "abc" }, func(t *DataSyncTask) { t.Mode = "other" }, func(t *DataSyncTask) { t.Mode = "table" }} {
		value := treeTestTask()
		mutate(value)
		if err := normalizeSyncTask(value); err == nil {
			t.Fatal("invalid config accepted")
		}
	}
	task.Tree.RootValue = ""
	if err := normalizeSyncTree(task, cols); err != nil || task.Tree.RootValue != "0" {
		t.Fatal("root default failed")
	}
	if _, err := compareSyncTreeRows(treeTestTask(), DataSyncScope{}, []SyncRow{treeTestRow("1", "0", "a", "甲")}, nil, big.NewInt(127), treeTestSchema("tinyint")); err == nil {
		t.Fatal("ID overflow accepted")
	}
	if _, err := treeIntegerMax(sqlColumn{Type: "varchar(64)"}); err == nil {
		t.Fatal("noninteger ID accepted")
	}
	if _, err := treeIntegerMax(sqlColumn{}); err == nil {
		t.Fatal("missing schema accepted")
	}
	// SQL BIGINT UNSIGNED IDs must stay exact, beyond JavaScript's number range.
	base, _ := new(big.Int).SetString("18446744073709551000", 10)
	c, err := compareSyncTreeRows(treeTestTask(), DataSyncScope{}, []SyncRow{treeTestRow("1", "0", "a", "甲")}, nil, base, treeTestSchema("bigint unsigned"))
	if err != nil || *c.Differences[0].After["id"] != "18446744073709551001" {
		t.Fatal("lost integer precision", err)
	}
	raw, _ := json.Marshal(c)
	if !strings.Contains(string(raw), `"18446744073709551001"`) {
		t.Fatal("ID serialized as number")
	}
}
func TestDataSyncTreeRootAndIDOnlyDifferences(t *testing.T) {
	a := treeTestRow("1", "0", "a", "甲")
	a["parent_id"] = nil
	b := treeTestRow("20", "0", "a", "甲")
	c := treeTestCompare(t, []SyncRow{a}, []SyncRow{b})
	if c.Unchanged != 1 || len(c.Differences) != 0 {
		t.Fatal("physical ID/root representation created false difference")
	}
}

func TestDataSyncTreeResultScopeAndRelationships(t *testing.T) {
	cfg := treeTestTask().Tree
	root := treeTestRow("100", "0", "root", "目录")
	child := treeTestRow("901", "100", "child", "子菜单")
	snap := &DataSyncSnapshot{Tree: cfg, Keys: []string{"code"}, Differences: []DataSyncDifference{{Key: `["child"]`, Kind: "insert", After: child}}}
	if err := verifySyncTreeResult([]SyncRow{root, child}, snap); err != nil {
		t.Fatal(err)
	}
	if err := verifySyncTreeResult([]SyncRow{root}, snap); err == nil {
		t.Fatal("out-of-scope insert accepted")
	}
	wrong := copySyncRow(child)
	wrong["parent_id"] = stringSyncValue("0")
	if err := verifySyncTreeResult([]SyncRow{root, wrong}, snap); err == nil {
		t.Fatal("wrong parent accepted")
	}
	wrong = copySyncRow(child)
	wrong["code"] = stringSyncValue("other")
	if err := verifySyncTreeResult([]SyncRow{root, wrong}, snap); err == nil {
		t.Fatal("wrong business key accepted")
	}
}
