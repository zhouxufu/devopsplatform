package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/xwb1989/sqlparser"
)

// A task is reusable query intent. A comparison is never an execution permit.
// Selected differences become immutable, environment-bound SQL changes and use
// the existing approval/snapshot/transaction executor, not a second write path.
type DataSyncTask struct {
	ID            int64    `json:"id"`
	Name          string   `json:"name"`
	Service       string   `json:"service"`
	ResourceAlias string   `json:"resource_alias"`
	SourceSQL     string   `json:"source_sql"`
	TargetSQL     string   `json:"target_sql"`
	Keys          []string `json:"keys"`
	Owner         string   `json:"owner"`
	Revision      int64    `json:"revision"`
}

// Environment choices belong to one comparison, never to the reusable task.
type DataSyncScope struct {
	SourceEnvironment string `json:"source_environment"`
	TargetEnvironment string `json:"target_environment"`
}
type DataSyncSnapshot struct {
	TaskID            int64                `json:"task_id"`
	TaskRevision      int64                `json:"task_revision"`
	SourceEnvironment string               `json:"source_environment"`
	TargetEnvironment string               `json:"target_environment"`
	TargetSQL         string               `json:"target_sql"`
	TargetDigest      string               `json:"target_digest"`
	TargetDestination string               `json:"target_destination"`
	ComparisonDigest  string               `json:"comparison_digest"`
	Keys              []string             `json:"keys"`
	Differences       []DataSyncDifference `json:"differences"`
}
type SyncRow map[string]*string
type DataSyncDifference struct {
	Key    string  `json:"key"`
	Kind   string  `json:"kind"`
	Before SyncRow `json:"before"`
	After  SyncRow `json:"after"`
}
type DataSyncComparison struct {
	Task              DataSyncTask         `json:"task"`
	SourceEnvironment string               `json:"source_environment"`
	TargetEnvironment string               `json:"target_environment"`
	Columns           []string             `json:"columns"`
	Differences       []DataSyncDifference `json:"differences"`
	Unchanged         int                  `json:"unchanged"`
	TargetOnly        int                  `json:"target_only"`
	Digest            string               `json:"digest"`
	TargetDigest      string               `json:"target_digest"`
	SourceDestination string               `json:"source_destination"`
	TargetDestination string               `json:"target_destination"`
}
type syncQuery struct {
	SQL     string
	Table   string
	Columns []string
}

func parseSyncQuery(query string) (*syncQuery, error) {
	if _, err := sqlTokens(query); err != nil {
		return nil, err
	}
	stmt, err := sqlparser.Parse(query)
	if err != nil {
		return nil, fmt.Errorf("查询 SQL 无法解析")
	}
	q, ok := stmt.(*sqlparser.Select)
	if !ok || q.Distinct != "" || len(q.GroupBy) > 0 || q.Having != nil || q.Limit != nil || q.Lock != "" || len(q.OrderBy) > 0 || len(q.Comments) > 0 || len(q.SelectExprs) == 0 || len(q.SelectExprs) > 50 {
		return nil, fmt.Errorf("仅支持显式字段、单表 SELECT 和 WHERE；不支持 *、JOIN、函数、聚合、排序、LIMIT 或锁")
	}
	table, err := singleSQLTable(q.From)
	if err != nil {
		return nil, err
	}
	out := &syncQuery{Table: table, Columns: []string{}}
	seen := map[string]bool{}
	for _, expr := range q.SelectExprs {
		a, ok := expr.(*sqlparser.AliasedExpr)
		if !ok || !a.As.IsEmpty() {
			return nil, fmt.Errorf("请指定原始字段名，不使用 * 或别名")
		}
		c, ok := a.Expr.(*sqlparser.ColName)
		if !ok || !c.Qualifier.IsEmpty() || !sqlIdentifierRE.MatchString(c.Name.String()) || seen[c.Name.String()] {
			return nil, fmt.Errorf("查询字段不合法或重复")
		}
		seen[c.Name.String()] = true
		out.Columns = append(out.Columns, c.Name.String())
	}
	if q.Where != nil {
		if err = validateSQLPredicate(q.Where.Expr); err != nil {
			return nil, err
		}
	}
	if err = sqlparser.Walk(func(node sqlparser.SQLNode) (bool, error) {
		switch n := node.(type) {
		case *sqlparser.ColName:
			if !n.Qualifier.IsEmpty() || !sqlIdentifierRE.MatchString(n.Name.String()) {
				return false, fmt.Errorf("查询字段不能带限定名或变量")
			}
		case *sqlparser.SQLVal:
			if n.Type == sqlparser.ValArg {
				return false, fmt.Errorf("查询不支持未绑定占位符")
			}
		}
		return true, nil
	}, q); err != nil {
		return nil, err
	}
	q.Limit = &sqlparser.Limit{Rowcount: sqlparser.NewIntVal([]byte("1001"))}
	out.SQL = sqlparser.String(q)
	return out, nil
}
func normalizeSyncTask(t *DataSyncTask) error {
	t.Name, t.Service, t.ResourceAlias = strings.TrimSpace(t.Name), strings.TrimSpace(t.Service), strings.TrimSpace(t.ResourceAlias)
	if t.Name == "" || t.Service == "" || !validResourceName(t.ResourceAlias) {
		return fmt.Errorf("任务名称、应用和数据库资源必填")
	}
	source, err := parseSyncQuery(t.SourceSQL)
	if err != nil {
		return err
	}
	target, err := parseSyncQuery(t.TargetSQL)
	if err != nil {
		return err
	}
	if strings.Join(source.Columns, "\x00") != strings.Join(target.Columns, "\x00") {
		return fmt.Errorf("来源与目标查询必须返回相同字段及顺序")
	}
	if len(t.Keys) == 0 {
		return fmt.Errorf("请选择匹配键（目标主键或唯一键）")
	}
	seen := map[string]bool{}
	for _, key := range t.Keys {
		found := false
		for _, col := range target.Columns {
			if col == key {
				found = true
			}
		}
		if !found || seen[key] {
			return fmt.Errorf("匹配键须属于查询字段且不能重复")
		}
		seen[key] = true
	}
	return nil
}
func (s *Store) DataSyncTasks() ([]DataSyncTask, error) {
	out := []DataSyncTask{}
	rows, err := s.db.Query(`SELECT id,document,owner,revision FROM data_sync_tasks ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var t DataSyncTask
		var doc string
		if err = rows.Scan(&t.ID, &doc, &t.Owner, &t.Revision); err != nil {
			return nil, err
		}
		id, owner, rev := t.ID, t.Owner, t.Revision
		if err = json.Unmarshal([]byte(doc), &t); err != nil {
			return nil, err
		}
		t.ID, t.Owner, t.Revision = id, owner, rev
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *Store) DataSyncTask(id int64) (*DataSyncTask, error) {
	var doc, owner string
	var revision int64
	if err := s.db.QueryRow(`SELECT document,owner,revision FROM data_sync_tasks WHERE id=?`, id).Scan(&doc, &owner, &revision); err != nil {
		return nil, fmt.Errorf("数据同步任务不存在")
	}
	var t DataSyncTask
	if err := json.Unmarshal([]byte(doc), &t); err != nil {
		return nil, err
	}
	t.ID, t.Owner, t.Revision = id, owner, revision
	return &t, nil
}
func (s *Store) SaveDataSyncTask(t *DataSyncTask) error {
	if err := normalizeSyncTask(t); err != nil {
		return err
	}
	if !s.hasResourceApplication(t.Service) {
		return fmt.Errorf("应用不存在")
	}
	var kind string
	if err := s.db.QueryRow(`SELECT kind FROM application_resource_requirements WHERE service=? AND alias=?`, t.Service, t.ResourceAlias).Scan(&kind); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("应用未声明 MySQL 资源 %s", t.ResourceAlias)
		}
		return err
	}
	if kind != "mysql" {
		return fmt.Errorf("数据同步任务仅支持 MySQL 资源")
	}
	data, _ := json.Marshal(t)
	if t.ID == 0 {
		res, err := s.db.Exec(`INSERT INTO data_sync_tasks(document,owner,revision) VALUES(?,?,1)`, string(data), t.Owner)
		if err != nil {
			return err
		}
		t.ID, err = res.LastInsertId()
		t.Revision = 1
		return err
	}
	res, err := s.db.Exec(`UPDATE data_sync_tasks SET document=?,revision=revision+1 WHERE id=? AND revision=?`, string(data), t.ID, t.Revision)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return fmt.Errorf("任务已被修改，请重新载入")
	}
	t.Revision++
	return nil
}
func syncKey(row SyncRow, keys []string) (string, error) {
	values := []string{}
	for _, k := range keys {
		v, ok := row[k]
		if !ok || v == nil {
			return "", fmt.Errorf("匹配键不能为空或 NULL")
		}
		values = append(values, *v)
	}
	b, _ := json.Marshal(values)
	return string(b), nil
}
func readSyncRows(ctx context.Context, c *ResourceConnection, q *syncQuery) ([]SyncRow, error) {
	db, err := openChangeMySQL(ctx, c)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, q.SQL)
	if err != nil {
		return nil, fmt.Errorf("查询失败（请核对表结构、字段及权限）：%w", err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	if strings.Join(columns, "\x00") != strings.Join(q.Columns, "\x00") {
		return nil, fmt.Errorf("查询结果字段与任务不一致")
	}
	out := []SyncRow{}
	totalBytes := 0
	for rows.Next() {
		values := make([]sql.RawBytes, len(columns))
		ptr := make([]any, len(columns))
		for i := range ptr {
			ptr[i] = &values[i]
		}
		if err = rows.Scan(ptr...); err != nil {
			return nil, err
		}
		row := SyncRow{}
		for i, col := range columns {
			if values[i] == nil {
				row[col] = nil
				continue
			}
			if !utf8.Valid(values[i]) || len(values[i]) > 65536 {
				return nil, fmt.Errorf("仅支持 UTF-8 文本数据，单元格最多 64 KiB")
			}
			value := string(values[i])
			totalBytes += len(value)
			row[col] = &value
		}
		out = append(out, row)
		if len(out) > 1000 || totalBytes > 2<<20 {
			return nil, fmt.Errorf("查询结果超过 1000 行或 2 MiB，请缩小 WHERE 范围")
		}
	}
	return out, rows.Err()
}
func indexSyncRows(rows []SyncRow, keys []string) (map[string]SyncRow, error) {
	out := map[string]SyncRow{}
	for _, r := range rows {
		k, err := syncKey(r, keys)
		if err != nil {
			return nil, err
		}
		if out[k] != nil {
			return nil, fmt.Errorf("查询结果匹配键重复，无法确定更新目标")
		}
		out[k] = r
	}
	return out, nil
}
func syncRowsDigest(rows []SyncRow) string {
	copyRows := append([]SyncRow{}, rows...)
	sort.Slice(copyRows, func(i, j int) bool { return digestJSON(copyRows[i]) < digestJSON(copyRows[j]) })
	return digestJSON(copyRows)
}
func compareSyncRows(t *DataSyncTask, scope DataSyncScope, source, target []SyncRow) (*DataSyncComparison, error) {
	a, err := indexSyncRows(source, t.Keys)
	if err != nil {
		return nil, err
	}
	b, err := indexSyncRows(target, t.Keys)
	if err != nil {
		return nil, err
	}
	q, _ := parseSyncQuery(t.TargetSQL)
	out := &DataSyncComparison{Task: *t, SourceEnvironment: scope.SourceEnvironment, TargetEnvironment: scope.TargetEnvironment, Columns: q.Columns, Differences: []DataSyncDifference{}, TargetDigest: syncRowsDigest(target)}
	keys := []string{}
	for k := range a {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		after, before := a[k], b[k]
		if digestJSON(after) == digestJSON(before) {
			out.Unchanged++
			continue
		}
		kind := "update"
		if before == nil {
			kind = "insert"
		}
		out.Differences = append(out.Differences, DataSyncDifference{Key: k, Kind: kind, Before: before, After: after})
	}
	for k := range b {
		if a[k] == nil {
			out.TargetOnly++
		}
	}
	out.Digest = digestJSON([]any{t, scope, sourceSortedDigest(source), out.TargetDigest, out.Differences})
	return out, nil
}
func sourceSortedDigest(rows []SyncRow) string { return syncRowsDigest(rows) }
func (s *Store) CompareDataSync(ctx context.Context, t *DataSyncTask, scope DataSyncScope) (*DataSyncComparison, error) {
	if err := normalizeSyncTask(t); err != nil {
		return nil, err
	}
	scope.SourceEnvironment = strings.TrimSpace(scope.SourceEnvironment)
	scope.TargetEnvironment = strings.TrimSpace(scope.TargetEnvironment)
	if scope.SourceEnvironment == "" || scope.TargetEnvironment == "" {
		return nil, fmt.Errorf("请选择本次来源和目标环境")
	}
	if scope.TargetEnvironment == scope.SourceEnvironment {
		return nil, fmt.Errorf("来源和目标环境不能相同")
	}
	source, err := s.sqlBinding(t.Service, scope.SourceEnvironment, t.ResourceAlias)
	if err != nil {
		return nil, err
	}
	target, err := s.sqlBinding(t.Service, scope.TargetEnvironment, t.ResourceAlias)
	if err != nil {
		return nil, err
	}
	if source.Endpoint == target.Endpoint && source.Config["database"] == target.Config["database"] {
		return nil, fmt.Errorf("来源和目标绑定了同一数据库，拒绝同步")
	}
	src, _ := parseSyncQuery(t.SourceSQL)
	dst, _ := parseSyncQuery(t.TargetSQL)
	db, err := openChangeMySQL(ctx, target)
	if err != nil {
		return nil, err
	}
	catalog, err := readSQLCatalog(ctx, db, target.Config["database"])
	db.Close()
	if err != nil {
		return nil, err
	}
	table := catalog[dst.Table]
	if table == nil || !table.Exists || table.Engine != "InnoDB" {
		return nil, fmt.Errorf("目标表必须已存在且为 InnoDB")
	}
	unique := false
	for _, idx := range table.Indexes {
		if idx.Unique && strings.Join(idx.Columns, "\x00") == strings.Join(t.Keys, "\x00") {
			unique = true
		}
	}
	if !unique {
		return nil, fmt.Errorf("匹配键必须完整对应目标表主键或唯一索引，字段顺序也须一致")
	}
	for _, col := range dst.Columns {
		c, ok := table.Columns[col]
		if !ok {
			return nil, fmt.Errorf("目标字段 %s 不存在", col)
		}
		if c.Generation != "" {
			return nil, fmt.Errorf("生成列不能作为同步字段")
		}
	}
	a, err := readSyncRows(ctx, source, src)
	if err != nil {
		return nil, err
	}
	b, err := readSyncRows(ctx, target, dst)
	if err != nil {
		return nil, err
	}
	out, err := compareSyncRows(t, scope, a, b)
	if err != nil {
		return nil, err
	}
	out.SourceDestination, out.TargetDestination = sqlDestination(source), sqlDestination(target)
	out.Digest = digestJSON([]any{out.Digest, out.SourceDestination, out.TargetDestination})
	return out, nil
}
func verifyDataSyncTarget(ctx context.Context, c *ResourceConnection, environment string, snap *DataSyncSnapshot) error {
	if environment != snap.TargetEnvironment {
		return fmt.Errorf("差异只适用于 %s；其他环境请重新对比并生成计划", snap.TargetEnvironment)
	}
	if sqlDestination(c) != snap.TargetDestination {
		return fmt.Errorf("目标资源连接已变化，请重新对比")
	}
	q, err := parseSyncQuery(snap.TargetSQL)
	if err != nil {
		return err
	}
	rows, err := readSyncRows(ctx, c, q)
	if err != nil {
		return err
	}
	if syncRowsDigest(rows) != snap.TargetDigest {
		return fmt.Errorf("目标数据已变化，请重新对比，不能覆盖未经核对的数据")
	}
	return nil
}

type DataSyncSelection struct {
	Key   string  `json:"key"`
	After SyncRow `json:"after"`
}

func syncLiteral(v *string) string {
	if v == nil {
		return "null"
	}
	return "'" + strings.ReplaceAll(*v, "'", "''") + "'"
}
func syncSelectionSQL(c *DataSyncComparison, selected []DataSyncSelection) (string, error) {
	if len(selected) == 0 || len(selected) > 50 {
		return "", fmt.Errorf("请选择 1–50 行差异")
	}
	q, _ := parseSyncQuery(c.Task.TargetSQL)
	known := map[string]DataSyncDifference{}
	for _, d := range c.Differences {
		known[d.Key] = d
	}
	seen := map[string]bool{}
	pieces := []string{}
	for _, pick := range selected {
		diff, ok := known[pick.Key]
		if !ok || seen[pick.Key] {
			return "", fmt.Errorf("选择了不存在或重复的差异")
		}
		seen[pick.Key] = true
		if len(pick.After) != len(c.Columns) {
			return "", fmt.Errorf("结果字段必须与查询字段一致")
		}
		key, err := syncKey(pick.After, c.Task.Keys)
		if err != nil || key != pick.Key {
			return "", fmt.Errorf("不允许修改匹配键")
		}
		for _, col := range c.Columns {
			v, exists := pick.After[col]
			if !exists || (v != nil && (!utf8.ValidString(*v) || len(*v) > 65536)) {
				return "", fmt.Errorf("结果字段缺失或值过大")
			}
		}
		if diff.Kind == "insert" {
			cols, values := []string{}, []string{}
			for _, col := range c.Columns {
				cols = append(cols, "`"+col+"`")
				values = append(values, syncLiteral(pick.After[col]))
			}
			pieces = append(pieces, "INSERT INTO `"+q.Table+"` ("+strings.Join(cols, ",")+") VALUES ("+strings.Join(values, ",")+")")
		} else {
			set := []string{}
			where := []string{}
			for _, col := range c.Columns {
				isKey := false
				for _, key := range c.Task.Keys {
					if key == col {
						isKey = true
					}
				}
				if !isKey {
					set = append(set, "`"+col+"`="+syncLiteral(pick.After[col]))
				}
			}
			if len(set) == 0 {
				return "", fmt.Errorf("无可更新的非键字段")
			}
			for _, key := range c.Task.Keys {
				where = append(where, "`"+key+"`="+syncLiteral(diff.Before[key]))
			}
			pieces = append(pieces, "UPDATE `"+q.Table+"` SET "+strings.Join(set, ",")+" WHERE "+strings.Join(where, " AND "))
		}
	}
	script := strings.Join(pieces, ";\n") + ";"
	if _, err := parseChangeSQL(script); err != nil {
		return "", err
	}
	return script, nil
}

// Only scripts reconstructed from pinned disjoint keys bypass the generic
// one-DML-per-table limit. Arbitrary scripts cannot claim to be data sync.
func validateSyncScript(c *Change) error {
	snap := c.SQL.DataSync
	query, err := parseSyncQuery(snap.TargetSQL)
	if err != nil {
		return err
	}
	if snap.TaskID < 1 || snap.TaskRevision < 1 || len(snap.Keys) == 0 || snap.TargetDestination == "" || snap.TargetDigest == "" || snap.ComparisonDigest == "" {
		return fmt.Errorf("同步差异快照不完整")
	}
	seenKeys := map[string]bool{}
	for _, key := range snap.Keys {
		found := false
		for _, col := range query.Columns {
			if key == col {
				found = true
			}
		}
		if !found || seenKeys[key] {
			return fmt.Errorf("同步匹配键无效")
		}
		seenKeys[key] = true
	}
	comp := &DataSyncComparison{Task: DataSyncTask{TargetSQL: snap.TargetSQL, Keys: snap.Keys}, Columns: query.Columns, Differences: snap.Differences}
	selection := []DataSyncSelection{}
	for _, d := range snap.Differences {
		if d.Kind != "update" && d.Kind != "insert" {
			return fmt.Errorf("同步计划仅允许新增或更新")
		}
		if d.Kind == "update" {
			key, e := syncKey(d.Before, snap.Keys)
			if e != nil || key != d.Key {
				return fmt.Errorf("同步更新匹配键不一致")
			}
		}
		selection = append(selection, DataSyncSelection{Key: d.Key, After: d.After})
	}
	script, err := syncSelectionSQL(comp, selection)
	if err != nil {
		return err
	}
	if script != c.SQL.Content {
		return fmt.Errorf("同步 SQL 与已选择差异不一致，请重新生成")
	}
	return nil
}

func registerDataSyncRoutes(mux *http.ServeMux, s *Store) {
	actorOf := func(r *http.Request) string {
		if user := strings.TrimSpace(r.Header.Get("X-User")); user != "" {
			return user
		}
		return "local"
	}
	wj := func(w http.ResponseWriter, value any) { writeIntegrationJSON(w, value, nil) }
	writeError := func(w http.ResponseWriter, status int, err any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprint(err)})
	}
	managed := func(r *http.Request, owner string) bool {
		return actorOf(r) == "local" || actorOf(r) == owner || r.Header.Get("X-Role") == "admin"
	}
	idOf := func(r *http.Request) int64 { id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64); return id }
	mux.HandleFunc("GET /api/data-sync/tasks", func(w http.ResponseWriter, r *http.Request) {
		tasks, err := s.DataSyncTasks()
		if err != nil {
			writeError(w, 500, err)
			return
		}
		wj(w, tasks)
	})
	save := func(w http.ResponseWriter, r *http.Request) {
		var t DataSyncTask
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&t) != nil {
			writeError(w, 400, "任务格式错误")
			return
		}
		t.ID = idOf(r)
		t.Owner = actorOf(r)
		if t.ID != 0 {
			old, err := s.DataSyncTask(t.ID)
			if err != nil || !managed(r, old.Owner) {
				writeError(w, 403, "仅创建人或管理员可修改任务")
				return
			}
			t.Owner = old.Owner
		}
		if err := s.SaveDataSyncTask(&t); err != nil {
			writeError(w, 409, err)
			return
		}
		s.Audit(actorOf(r), "save", "data-sync-task", fmt.Sprint(t.ID), nil)
		wj(w, t)
	}
	mux.HandleFunc("POST /api/data-sync/tasks", save)
	mux.HandleFunc("PUT /api/data-sync/tasks/{id}", save)
	mux.HandleFunc("POST /api/data-sync/tasks/{id}/compare", func(w http.ResponseWriter, r *http.Request) {
		var req DataSyncScope
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil {
			writeError(w, 400, "请选择本次来源和目标环境")
			return
		}
		t, err := s.DataSyncTask(idOf(r))
		if err != nil {
			writeError(w, 404, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		out, err := s.CompareDataSync(ctx, t, req)
		if err != nil {
			writeError(w, 409, err)
			return
		}
		wj(w, out)
	})
	mux.HandleFunc("POST /api/data-sync/tasks/{id}/freeze", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			DataSyncScope
			Digest     string              `json:"digest"`
			WorkItemID int64               `json:"work_item_id"`
			WorkDigest string              `json:"work_digest"`
			Rows       []DataSyncSelection `json:"rows"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&req) != nil {
			writeError(w, 400, "差异计划格式错误")
			return
		}
		t, err := s.DataSyncTask(idOf(r))
		if err != nil {
			writeError(w, 404, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		comp, err := s.CompareDataSync(ctx, t, req.DataSyncScope)
		if err != nil {
			writeError(w, 409, err)
			return
		}
		if comp.Digest != req.Digest {
			writeError(w, 409, "任务或来源/目标数据已变化，请重新对比；当前修改不会执行")
			return
		}
		script, err := syncSelectionSQL(comp, req.Rows)
		if err != nil {
			writeError(w, 400, err)
			return
		}
		c := Change{Kind: "mysql", Title: t.Name + " · 数据同步 " + comp.SourceEnvironment + " → " + comp.TargetEnvironment, Service: t.Service, RiskLevel: "medium", Owner: actorOf(r), Summary: fmt.Sprintf("数据同步任务 #%d · 修订 %d · %d 行；目标独有数据保留", t.ID, t.Revision, len(req.Rows)), SQL: &SQLChange{ResourceAlias: t.ResourceAlias, Content: script, MaxAffectedRows: len(req.Rows), DataSync: &DataSyncSnapshot{TaskID: t.ID, TaskRevision: t.Revision, SourceEnvironment: comp.SourceEnvironment, TargetEnvironment: comp.TargetEnvironment, TargetSQL: t.TargetSQL, TargetDigest: comp.TargetDigest, TargetDestination: comp.TargetDestination, ComparisonDigest: comp.Digest}}}
		c.SQL.DataSync.Keys = t.Keys
		for _, pick := range req.Rows {
			for _, d := range comp.Differences {
				if d.Key == pick.Key {
					d.After = pick.After
					c.SQL.DataSync.Differences = append(c.SQL.DataSync.Differences, d)
					break
				}
			}
		}
		if req.WorkItemID > 0 {
			batch := WorkDeliverySave{Digest: req.WorkDigest, Changes: []Change{c}}
			err = s.SaveWorkDelivery(req.WorkItemID, &batch, actorOf(r), managed(r, ""))
			if err == nil {
				c = batch.Changes[0]
			}
		} else {
			err = s.CreateChange(&c)
		}
		if err != nil {
			writeError(w, 409, err)
			return
		}
		s.Audit(actorOf(r), "freeze-differences", "data-sync-task", fmt.Sprint(t.ID), map[string]any{"change_id": c.ID, "rows": len(req.Rows), "source_environment": comp.SourceEnvironment, "environment": comp.TargetEnvironment})
		wj(w, s.GetChange(c.ID))
	})
}
