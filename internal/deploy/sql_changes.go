package deploy

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/xwb1989/sqlparser"
)

// SQL is deliberately a small, fail-closed dialect, not a remote SQL console.
// Destructive DDL, routines, joins, subqueries and arbitrary functions are not
// accepted. DML and DDL cannot share a transaction/approval node.
type SQLChange struct {
	ResourceAlias   string            `json:"resource_alias"`
	Content         string            `json:"content"`
	MaxAffectedRows int               `json:"max_affected_rows"`
	DataSync        *DataSyncSnapshot `json:"data_sync,omitempty"`
}
type sqlColumn struct {
	Type          string  `json:"type"`
	Nullable      bool    `json:"nullable"`
	Default       *string `json:"default"`
	AutoIncrement bool    `json:"auto_increment"`
	Comment       string  `json:"comment"`
	Collation     string  `json:"collation,omitempty"`
	Generation    string  `json:"generation,omitempty"`
	Extra         string  `json:"extra,omitempty"`
}
type sqlIndex struct {
	Unique  bool     `json:"unique"`
	Columns []string `json:"columns"`
}
type sqlTable struct {
	Exists    bool                 `json:"exists"`
	Columns   map[string]sqlColumn `json:"columns"`
	Order     []string             `json:"order"`
	Indexes   map[string]sqlIndex  `json:"indexes"`
	Engine    string               `json:"engine"`
	Collation string               `json:"collation"`
}
type sqlCatalog map[string]*sqlTable
type SQLStatement struct {
	ChangeID        int64  `json:"change_id"`
	SQL             string `json:"sql"`
	Table           string `json:"table"`
	Action          string `json:"action"`
	Skip            bool   `json:"skip"`
	Reason          string `json:"reason,omitempty"`
	MaxAffectedRows int    `json:"max_affected_rows"`
	ExpectedRows    int    `json:"expected_rows"`
	DataDigest      string `json:"data_digest,omitempty"`
	Scope           string `json:"scope,omitempty"`
	definition      *sqlTable
	columnName      string
	column          sqlColumn
	indexName       string
	index           sqlIndex
	columns         []string
}
type SQLExecutionPlan struct {
	NodeID            string         `json:"node_id"`
	ChangeIDs         []int64        `json:"change_ids"`
	Services          []string       `json:"services"`
	Aliases           []string       `json:"aliases"`
	ChangeDigests     []string       `json:"change_digests"`
	Connection        string         `json:"connection"`
	Database          string         `json:"database"`
	DestinationDigest string         `json:"destination_digest"`
	SchemaDigest      string         `json:"schema_digest"`
	Tables            []string       `json:"tables"`
	Statements        []SQLStatement `json:"statements"`
	Mode              string         `json:"mode"`
	Warnings          []string       `json:"warnings"`
}

var sqlIdentifierRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
var intWidthRE = regexp.MustCompile(`\b(tinyint|smallint|mediumint|int|bigint)\([0-9]+\)`)
var createTableRE = regexp.MustCompile("(?is)^create\\s+table\\s+(?:if\\s+not\\s+exists\\s+)?(`?[A-Za-z_][A-Za-z0-9_]*`?)\\s*(\\(.*\\))\\s*(?:engine\\s*=\\s*InnoDB)?\\s*(?:(?:default\\s+)?charset\\s*=\\s*utf8mb4)?\\s*$")
var addColumnRE = regexp.MustCompile("(?is)^alter\\s+table\\s+(`?[A-Za-z_][A-Za-z0-9_]*`?)\\s+add\\s+(?:column\\s+)?(.+)$")
var createIndexRE = regexp.MustCompile("(?is)^create\\s+(unique\\s+)?index\\s+(`?[A-Za-z_][A-Za-z0-9_]*`?)\\s+on\\s+(`?[A-Za-z_][A-Za-z0-9_]*`?)\\s*\\(([^()]*)\\)\\s*$")

func saveSQLPayload(tx *deployTx, v *Change) error {
	doc, _ := json.Marshal(struct {
		SQL   *SQLChange   `json:"sql,omitempty"`
		Nacos *NacosChange `json:"nacos,omitempty"`
		Redis *RedisChange `json:"redis,omitempty"`
	}{v.SQL, v.Nacos, v.Redis})
	_, err := tx.Exec(`INSERT INTO change_payloads(change_id,document) VALUES(?,?) ON CONFLICT(change_id) DO UPDATE SET document=excluded.document`, v.ID, string(doc))
	return err
}
func (s *Store) loadSQLPayload(v *Change) {
	var doc string
	if s.db.QueryRow(`SELECT document FROM change_payloads WHERE change_id=?`, v.ID).Scan(&doc) == nil {
		var payload struct {
			SQL   *SQLChange   `json:"sql"`
			Nacos *NacosChange `json:"nacos"`
			Redis *RedisChange `json:"redis"`
		}
		if json.Unmarshal([]byte(doc), &payload) == nil {
			v.SQL = payload.SQL
			v.Nacos = payload.Nacos
			v.Redis = payload.Redis
		}
	}
}
func digestJSON(v any) string {
	data, _ := json.Marshal(v)
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func sqlDestination(c *ResourceConnection) string {
	return digestJSON([]any{c.Name, c.Kind, c.Endpoint, c.Config, c.Username})
}

// Token comparison rejects DDL partially accepted by the parser. Quoted string
// contents remain case-sensitive; executable comments are never accepted.
func sqlTokens(input string) ([]string, error) {
	out := []string{}
	for i := 0; i < len(input); {
		b := input[i]
		if unicode.IsSpace(rune(b)) {
			i++
			continue
		}
		if b == '\'' || b == '`' {
			quote := b
			i++
			var value strings.Builder
			closed := false
			for i < len(input) {
				if input[i] == quote {
					if i+1 < len(input) && input[i+1] == quote {
						value.WriteByte(quote)
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				if input[i] == '\\' {
					return nil, fmt.Errorf("不支持反斜线转义，请用两个单引号表示单引号")
				}
				value.WriteByte(input[i])
				i++
			}
			if !closed {
				return nil, fmt.Errorf("SQL 引号未闭合")
			}
			if quote == '`' {
				out = append(out, strings.ToLower(value.String()))
			} else {
				out = append(out, "literal:"+value.String())
			}
			continue
		}
		if b == '"' || b == '\\' {
			return nil, fmt.Errorf("SQL 字符串请使用单引号，标识符使用反引号")
		}
		if i+1 < len(input) && ((b == '/' && input[i+1] == '*') || (b == '-' && input[i+1] == '-')) || b == '#' {
			return nil, fmt.Errorf("执行 SQL 请移除注释（包括版本注释和优化器提示）")
		}
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' {
			start := i
			i++
			for i < len(input) && (input[i] >= 'a' && input[i] <= 'z' || input[i] >= 'A' && input[i] <= 'Z' || input[i] >= '0' && input[i] <= '9' || input[i] == '_') {
				i++
			}
			out = append(out, strings.ToLower(input[start:i]))
			continue
		}
		out = append(out, string(b))
		i++
	}
	return out, nil
}
func sameSQLTokens(a, b string) bool {
	x, e := sqlTokens(a)
	if e != nil {
		return false
	}
	y, e := sqlTokens(b)
	return e == nil && strings.Join(x, "\x00") == strings.Join(y, "\x00")
}

func parseColumn(c *sqlparser.ColumnDefinition) (sqlColumn, error) {
	ct := c.Type
	t := strings.ToLower(ct.Type)
	if !sqlIdentifierRE.MatchString(c.Name.String()) || ct.Charset != "" || ct.Collate != "" || ct.OnUpdate != nil || bool(ct.Zerofill) || len(ct.EnumValues) > 0 || ct.KeyOpt != 0 {
		return sqlColumn{}, fmt.Errorf("字段仅支持基本类型、NOT NULL、DEFAULT、AUTO_INCREMENT、COMMENT；主键/索引请单独声明")
	}
	switch t {
	case "tinyint", "smallint", "mediumint", "int", "bigint", "varchar", "char", "decimal", "date", "datetime", "timestamp", "text", "longtext", "blob", "longblob":
	default:
		return sqlColumn{}, fmt.Errorf("暂不支持字段类型 %s", t)
	}
	if ct.Length != nil {
		if ct.Scale != nil {
			t += "(" + string(ct.Length.Val) + "," + string(ct.Scale.Val) + ")"
		} else {
			t += "(" + string(ct.Length.Val) + ")"
		}
	}
	if ct.Unsigned {
		t += " unsigned"
	}
	v := sqlColumn{Type: intWidthRE.ReplaceAllString(t, "$1"), Nullable: !bool(ct.NotNull), AutoIncrement: bool(ct.Autoincrement)}
	if ct.Default != nil {
		d := string(ct.Default.Val)
		v.Default = &d
	}
	if ct.Comment != nil {
		v.Comment = string(ct.Comment.Val)
	}
	return v, nil
}
func tableFromSpec(spec *sqlparser.TableSpec) (*sqlTable, error) {
	t := &sqlTable{Exists: true, Engine: "InnoDB", Columns: map[string]sqlColumn{}, Order: []string{}, Indexes: map[string]sqlIndex{}}
	if spec == nil || len(spec.Columns) == 0 {
		return nil, fmt.Errorf("建表语句缺少完整字段定义")
	}
	for _, c := range spec.Columns {
		v, e := parseColumn(c)
		if e != nil {
			return nil, e
		}
		name := c.Name.String()
		if _, ok := t.Columns[name]; ok {
			return nil, fmt.Errorf("重复字段 %s", name)
		}
		t.Columns[name] = v
		t.Order = append(t.Order, name)
	}
	for _, idx := range spec.Indexes {
		if idx.Info.Spatial || len(idx.Options) > 0 {
			return nil, fmt.Errorf("仅支持普通、唯一和主键索引")
		}
		name := idx.Info.Name.String()
		if idx.Info.Primary {
			name = "PRIMARY"
		}
		if !sqlIdentifierRE.MatchString(name) {
			return nil, fmt.Errorf("索引名称无效")
		}
		if _, ok := t.Indexes[name]; ok {
			return nil, fmt.Errorf("索引重复")
		}
		v := sqlIndex{Unique: idx.Info.Unique || idx.Info.Primary, Columns: []string{}}
		for _, c := range idx.Columns {
			if c.Length != nil {
				return nil, fmt.Errorf("暂不支持前缀索引")
			}
			n := c.Column.String()
			col, ok := t.Columns[n]
			if !ok {
				return nil, fmt.Errorf("索引引用不存在的字段 %s", n)
			}
			v.Columns = append(v.Columns, n)
			if idx.Info.Primary {
				col.Nullable = false
				t.Columns[n] = col
			}
		}
		t.Indexes[name] = v
	}
	return t, nil
}

func parseChangeSQL(content string) ([]SQLStatement, error) {
	if len(content) > 64<<10 || strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("SQL 必填且不超过 64 KiB")
	}
	if _, err := sqlTokens(content); err != nil {
		return nil, err
	}
	pieces, err := sqlparser.SplitStatementToPieces(content)
	if err != nil {
		return nil, fmt.Errorf("SQL 分句失败：%w", err)
	}
	out := []SQLStatement{}
	mode := ""
	for _, piece := range pieces {
		piece = strings.TrimSpace(piece)
		if piece == "" {
			continue
		}
		op := SQLStatement{SQL: piece}
		if m := createTableRE.FindStringSubmatch(piece); m != nil {
			table := strings.Trim(m[1], "`")
			synthetic := "create table " + m[1] + " " + m[2]
			stmt, e := sqlparser.Parse(synthetic)
			if e != nil {
				return nil, fmt.Errorf("建表语句无法解析：%w", e)
			}
			ddl, ok := stmt.(*sqlparser.DDL)
			if !ok || ddl.TableSpec == nil || !sameSQLTokens(synthetic, sqlparser.String(ddl)) {
				return nil, fmt.Errorf("建表语句含未支持的语法，请使用完整字段定义及独立主键/索引声明")
			}
			op.definition, e = tableFromSpec(ddl.TableSpec)
			if e != nil {
				return nil, e
			}
			op.Action, op.Table = "create-table", table
			// Always pin the engine and charset instead of depending on server defaults.
			op.SQL = synthetic + " ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"
		} else if m := addColumnRE.FindStringSubmatch(piece); m != nil {
			synthetic := "create table preview (" + m[2] + ")"
			stmt, e := sqlparser.Parse(synthetic)
			if e != nil {
				return nil, fmt.Errorf("新增字段定义无法解析")
			}
			ddl, ok := stmt.(*sqlparser.DDL)
			if !ok || ddl.TableSpec == nil || len(ddl.TableSpec.Columns) != 1 || len(ddl.TableSpec.Indexes) > 0 || !sameSQLTokens(synthetic, sqlparser.String(ddl)) {
				return nil, fmt.Errorf("ALTER 仅支持每条新增一个字段，不支持位置、约束或其他修改")
			}
			c := ddl.TableSpec.Columns[0]
			op.column, e = parseColumn(c)
			if e != nil {
				return nil, e
			}
			op.columnName = c.Name.String()
			op.Table = strings.Trim(m[1], "`")
			op.Action = "add-column"
		} else if m := createIndexRE.FindStringSubmatch(piece); m != nil {
			op.Action, op.Table, op.indexName = "create-index", strings.Trim(m[3], "`"), strings.Trim(m[2], "`")
			op.index = sqlIndex{Unique: m[1] != "", Columns: []string{}}
			for _, part := range strings.Split(m[4], ",") {
				n := strings.Trim(strings.TrimSpace(part), "`")
				if !sqlIdentifierRE.MatchString(n) {
					return nil, fmt.Errorf("索引仅支持字段列表")
				}
				op.index.Columns = append(op.index.Columns, n)
			}
		} else {
			stmt, e := sqlparser.Parse(piece)
			if e != nil {
				return nil, fmt.Errorf("SQL 解析失败：%w", e)
			}
			var where *sqlparser.Where
			switch v := stmt.(type) {
			case *sqlparser.Insert:
				values, ok := v.Rows.(sqlparser.Values)
				if v.Action != "insert" || v.Ignore != "" || len(v.OnDup) > 0 || len(v.Partitions) > 0 || !ok || len(v.Columns) == 0 || len(values) > 10000 {
					return nil, fmt.Errorf("INSERT 仅支持指定字段的 VALUES，不支持 IGNORE/REPLACE/SELECT/ON DUPLICATE")
				}
				op.Table = v.Table.Name.String()
				if !v.Table.Qualifier.IsEmpty() {
					return nil, fmt.Errorf("禁止跨库 SQL")
				}
				op.Action = "insert"
				for _, c := range v.Columns {
					op.columns = append(op.columns, c.String())
				}
				for _, row := range values {
					if len(row) != len(v.Columns) {
						return nil, fmt.Errorf("INSERT 字段和值数量不一致")
					}
					for _, expr := range row {
						switch expr.(type) {
						case *sqlparser.SQLVal, *sqlparser.NullVal, sqlparser.BoolVal:
						default:
							return nil, fmt.Errorf("INSERT 值仅支持字面量")
						}
					}
				}
				op.ExpectedRows = len(values)
			case *sqlparser.Update:
				if len(v.OrderBy) > 0 || v.Limit != nil {
					return nil, fmt.Errorf("UPDATE 不支持 ORDER BY/LIMIT")
				}
				op.Table, e = singleSQLTable(v.TableExprs)
				where = v.Where
				op.Action = "update"
				for _, expr := range v.Exprs {
					switch expr.Expr.(type) {
					case *sqlparser.SQLVal, *sqlparser.NullVal, sqlparser.BoolVal:
					default:
						return nil, fmt.Errorf("UPDATE 目标值仅支持字面量")
					}
				}
			case *sqlparser.Delete:
				if len(v.Targets) > 0 || len(v.Partitions) > 0 || len(v.OrderBy) > 0 || v.Limit != nil {
					return nil, fmt.Errorf("DELETE 仅支持单表带 WHERE")
				}
				op.Table, e = singleSQLTable(v.TableExprs)
				where = v.Where
				op.Action = "delete"
			default:
				return nil, fmt.Errorf("仅支持 CREATE TABLE、ALTER TABLE ADD COLUMN、CREATE INDEX，以及单表 INSERT/UPDATE/DELETE")
			}
			if e != nil {
				return nil, e
			}
			if (op.Action == "update" || op.Action == "delete") && where == nil {
				return nil, fmt.Errorf("UPDATE/DELETE 必须包含 WHERE")
			}
			if where != nil {
				if e = validateSQLPredicate(where.Expr); e != nil {
					return nil, e
				}
				op.Scope = sqlparser.String(where.Expr)
			}
			if e = sqlparser.Walk(func(node sqlparser.SQLNode) (bool, error) {
				switch n := node.(type) {
				case *sqlparser.FuncExpr, *sqlparser.Subquery:
					return false, fmt.Errorf("不支持函数或子查询")
				case *sqlparser.ColName:
					if !n.Qualifier.IsEmpty() || !sqlIdentifierRE.MatchString(n.Name.String()) {
						return false, fmt.Errorf("字段不得带库/表限定或会话变量")
					}
					op.columns = append(op.columns, n.Name.String())
				case *sqlparser.SQLVal:
					if n.Type == sqlparser.ValArg {
						return false, fmt.Errorf("SQL 变更不支持未绑定占位符")
					}
				}
				return true, nil
			}, stmt); e != nil {
				return nil, e
			}
		}
		if !sqlIdentifierRE.MatchString(op.Table) {
			return nil, fmt.Errorf("表名称无效")
		}
		current := "ddl"
		if op.Action == "insert" || op.Action == "update" || op.Action == "delete" {
			current = "dml"
		}
		if mode != "" && mode != current {
			return nil, fmt.Errorf("DDL 和 DML 请拆成不同 SQL 节点，不能承诺统一事务")
		}
		mode = current
		out = append(out, op)
	}
	if len(out) == 0 || len(out) > 50 {
		return nil, fmt.Errorf("每个 SQL 变更支持 1–50 条语句")
	}
	return out, nil
}

func singleSQLTable(exprs sqlparser.TableExprs) (string, error) {
	if len(exprs) != 1 {
		return "", fmt.Errorf("仅支持单表 SQL")
	}
	a, ok := exprs[0].(*sqlparser.AliasedTableExpr)
	if !ok || !a.As.IsEmpty() || a.Hints != nil || len(a.Partitions) > 0 {
		return "", fmt.Errorf("不支持 JOIN、别名或提示")
	}
	n, ok := a.Expr.(sqlparser.TableName)
	if !ok || !n.Qualifier.IsEmpty() {
		return "", fmt.Errorf("不支持跨库或子查询")
	}
	return n.Name.String(), nil
}
func validateSQLPredicate(expr sqlparser.Expr) error {
	switch v := expr.(type) {
	case *sqlparser.AndExpr:
		if err := validateSQLPredicate(v.Left); err != nil {
			return err
		}
		return validateSQLPredicate(v.Right)
	case *sqlparser.OrExpr:
		if err := validateSQLPredicate(v.Left); err != nil {
			return err
		}
		return validateSQLPredicate(v.Right)
	case *sqlparser.ParenExpr:
		return validateSQLPredicate(v.Expr)
	case *sqlparser.ComparisonExpr:
		if _, ok := v.Left.(*sqlparser.ColName); !ok {
			return fmt.Errorf("WHERE 必须比较字段与字面量，不能使用恒真条件")
		}
		switch v.Operator {
		case "=", "!=", "<>", ">", "<", ">=", "<=":
		default:
			return fmt.Errorf("WHERE 本版仅支持比较与 AND/OR")
		}
		switch v.Right.(type) {
		case *sqlparser.SQLVal, sqlparser.BoolVal:
			return nil
		}
	}
	return fmt.Errorf("WHERE 本版仅支持字段与字面量比较，以及 AND/OR")
}

// Query only the target schema; do not scan metadata across all databases.
func readSQLCatalog(ctx context.Context, db sqlQueryer, database string) (sqlCatalog, error) {
	catalog := sqlCatalog{}
	rows, err := db.QueryContext(ctx, `SELECT TABLE_NAME,ENGINE,TABLE_COLLATION FROM information_schema.TABLES WHERE TABLE_SCHEMA=? AND TABLE_TYPE='BASE TABLE'`, database)
	if err != nil {
		return nil, fmt.Errorf("读取目标表结构失败：%w", err)
	}
	for rows.Next() {
		var n, engine, collation string
		if err = rows.Scan(&n, &engine, &collation); err != nil {
			rows.Close()
			return nil, err
		}
		catalog[n] = &sqlTable{Exists: true, Engine: engine, Collation: collation, Columns: map[string]sqlColumn{}, Order: []string{}, Indexes: map[string]sqlIndex{}}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = db.QueryContext(ctx, `SELECT TABLE_NAME,COLUMN_NAME,COLUMN_TYPE,IS_NULLABLE,COLUMN_DEFAULT,EXTRA,COLUMN_COMMENT,COALESCE(COLLATION_NAME,''),GENERATION_EXPRESSION FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=? ORDER BY TABLE_NAME,ORDINAL_POSITION`, database)
	if err != nil {
		return nil, fmt.Errorf("读取字段结构失败：%w", err)
	}
	for rows.Next() {
		var table, n, typ, nullable, extra, comment, collation, generation string
		var d sql.NullString
		if err = rows.Scan(&table, &n, &typ, &nullable, &d, &extra, &comment, &collation, &generation); err != nil {
			rows.Close()
			return nil, err
		}
		t := catalog[table]
		if t == nil {
			continue
		}
		v := sqlColumn{Type: intWidthRE.ReplaceAllString(strings.ToLower(typ), "$1"), Nullable: nullable == "YES", AutoIncrement: strings.Contains(extra, "auto_increment"), Comment: comment, Collation: collation, Generation: generation, Extra: strings.TrimSpace(strings.ReplaceAll(extra, "auto_increment", ""))}
		if d.Valid {
			v.Default = &d.String
		}
		t.Columns[n] = v
		t.Order = append(t.Order, n)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = db.QueryContext(ctx, `SELECT TABLE_NAME,INDEX_NAME,NON_UNIQUE,COLUMN_NAME,SUB_PART,INDEX_TYPE FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=? ORDER BY TABLE_NAME,INDEX_NAME,SEQ_IN_INDEX`, database)
	if err != nil {
		return nil, fmt.Errorf("读取索引结构失败：%w", err)
	}
	for rows.Next() {
		var table, n, typ string
		var nonUnique int
		var column sql.NullString
		var prefix sql.NullInt64
		if err = rows.Scan(&table, &n, &nonUnique, &column, &prefix, &typ); err != nil {
			rows.Close()
			return nil, err
		}
		t := catalog[table]
		if t == nil {
			continue
		}
		v := t.Indexes[n]
		v.Unique = nonUnique == 0
		col := column.String
		if prefix.Valid || typ != "BTREE" || !column.Valid {
			col += "[unsupported]"
		}
		v.Columns = append(v.Columns, col)
		t.Indexes[n] = v
	}
	err = rows.Err()
	rows.Close()
	return catalog, err
}

func catalogSnapshot(c sqlCatalog, tables []string) sqlCatalog {
	out := sqlCatalog{}
	for _, name := range tables {
		out[name] = c[name]
	}
	return out
}
func defaultColumnCollation(c sqlColumn, collation string) sqlColumn {
	if strings.HasPrefix(c.Type, "varchar") || strings.HasPrefix(c.Type, "char") || c.Type == "text" || c.Type == "longtext" {
		c.Collation = collation
	}
	return c
}
func applySQLSchema(c sqlCatalog, op *SQLStatement) error {
	t := c[op.Table]
	switch op.Action {
	case "create-table":
		if t != nil {
			raw, _ := json.Marshal(op.definition)
			var expected sqlTable
			_ = json.Unmarshal(raw, &expected)
			expected.Collation = t.Collation
			for name, col := range expected.Columns {
				expected.Columns[name] = defaultColumnCollation(col, t.Collation)
			}
			if !strings.HasPrefix(t.Collation, "utf8mb4_") || digestJSON(t) != digestJSON(&expected) {
				return fmt.Errorf("表 %s 已存在但结构不一致，禁止跳过或覆盖", op.Table)
			}
			op.Skip, op.Reason = true, "表已存在且字段、索引、引擎一致"
			return nil
		}
		clone, _ := json.Marshal(op.definition)
		var next sqlTable
		_ = json.Unmarshal(clone, &next)
		c[op.Table] = &next
	case "add-column":
		if t == nil {
			return fmt.Errorf("表 %s 不存在", op.Table)
		}
		expected := defaultColumnCollation(op.column, t.Collation)
		if col, ok := t.Columns[op.columnName]; ok {
			if digestJSON(col) != digestJSON(expected) {
				return fmt.Errorf("字段 %s.%s 已存在但定义不同", op.Table, op.columnName)
			}
			op.Skip, op.Reason = true, "字段已存在且定义一致"
			return nil
		}
		t.Columns[op.columnName] = expected
		t.Order = append(t.Order, op.columnName)
	case "create-index":
		if t == nil {
			return fmt.Errorf("表 %s 不存在", op.Table)
		}
		for _, name := range op.index.Columns {
			if _, ok := t.Columns[name]; !ok {
				return fmt.Errorf("索引字段 %s 不存在", name)
			}
		}
		if idx, ok := t.Indexes[op.indexName]; ok {
			if digestJSON(idx) != digestJSON(op.index) {
				return fmt.Errorf("索引 %s 已存在但定义不同", op.indexName)
			}
			op.Skip, op.Reason = true, "索引已存在且字段顺序、唯一性一致"
			return nil
		}
		for n, idx := range t.Indexes {
			if digestJSON(idx) == digestJSON(op.index) {
				op.Skip, op.Reason = true, "等效索引已存在："+n
				return nil
			}
		}
		t.Indexes[op.indexName] = op.index
	default:
		if t == nil {
			return fmt.Errorf("表 %s 不存在", op.Table)
		}
		if t.Engine != "InnoDB" {
			return fmt.Errorf("DML 仅支持 InnoDB 事务表")
		}
		for _, name := range op.columns {
			if _, ok := t.Columns[name]; !ok {
				return fmt.Errorf("字段 %s.%s 不存在", op.Table, name)
			}
		}
	}
	return nil
}

func (s *Store) sqlBinding(service, environment, alias string) (*ResourceConnection, error) {
	if !s.hasResourceApplication(service) || s.GetEnvironment(environment) == nil {
		return nil, fmt.Errorf("应用或环境不存在")
	}
	resources := s.ApplicationResources(service)
	isMySQL := false
	for _, r := range resources.Requirements {
		if r.Alias == alias && r.Kind == "mysql" {
			isMySQL = true
		}
	}
	if !isMySQL {
		return nil, fmt.Errorf("应用未声明 MySQL 资源 %s", alias)
	}
	for _, b := range resources.Bindings {
		if b.Environment == environment && b.Alias == alias {
			c := s.GetConnection(b.Connection)
			if c == nil || c.Kind != "mysql" {
				return nil, fmt.Errorf("绑定的 MySQL 连接不存在")
			}
			if len(b.Config) > 0 {
				return nil, fmt.Errorf("MySQL 数据库在连接中指定，本版不支持绑定配置覆盖")
			}
			return c, nil
		}
	}
	return nil, fmt.Errorf("应用 %s 的资源 %s 尚未绑定 %s 环境连接", service, alias, environment)
}

func (s *Store) prepareSQLNode(ctx context.Context, nodeID string, ids []int64, environment string) (*SQLExecutionPlan, error) {
	if len(ids) == 0 || len(ids) > 50 {
		return nil, fmt.Errorf("SQL 节点需选择 1–50 个变更")
	}
	p := &SQLExecutionPlan{NodeID: nodeID, ChangeIDs: ids, Warnings: []string{}, Statements: []SQLStatement{}, Services: []string{}, Aliases: []string{}}
	seen := map[int64]bool{}
	tables := map[string]bool{}
	var connection *ResourceConnection
	syncChanges := map[int64]bool{}
	syncSnapshots := map[int64]*DataSyncSnapshot{}
	for _, id := range ids {
		if seen[id] {
			return nil, fmt.Errorf("SQL 变更不能重复")
		}
		seen[id] = true
		c := s.GetChange(id)
		if c == nil || c.Kind != "mysql" || c.SQL == nil {
			return nil, fmt.Errorf("变更 #%d 缺少结构化 SQL 配置，请编辑后保存", id)
		}
		resource, err := s.sqlBinding(c.Service, environment, c.SQL.ResourceAlias)
		if err != nil {
			return nil, err
		}
		if connection != nil && connection.Name != resource.Name {
			return nil, fmt.Errorf("同一 SQL 节点只能操作一个数据库连接，请分成多个节点")
		}
		connection = resource
		if c.SQL.DataSync != nil {
			if err := validateSyncScript(c); err != nil {
				return nil, err
			}
			syncChanges[id] = true
			syncSnapshots[id] = c.SQL.DataSync
			if err := verifyDataSyncTarget(ctx, resource, environment, c.SQL.DataSync); err != nil {
				return nil, fmt.Errorf("同步任务 #%d：%w", c.SQL.DataSync.TaskID, err)
			}
		}
		ops, err := parseChangeSQL(c.SQL.Content)
		if err != nil {
			return nil, err
		}
		for _, op := range ops {
			op.ChangeID, op.MaxAffectedRows = id, c.SQL.MaxAffectedRows
			p.Statements = append(p.Statements, op)
			tables[op.Table] = true
		}
		p.Services = append(p.Services, c.Service)
		p.Aliases = append(p.Aliases, c.SQL.ResourceAlias)
		p.ChangeDigests = append(p.ChangeDigests, digestJSON([]any{c.Service, c.SQL}))
	}
	if len(p.Statements) > 100 {
		return nil, fmt.Errorf("SQL 节点最多 100 条语句")
	}
	p.Connection, p.Database, p.DestinationDigest = connection.Name, connection.Config["database"], sqlDestination(connection)
	for _, id := range ids {
		var status string
		e := s.db.QueryRow(`SELECT status FROM sql_execution_ledger WHERE connection=? AND database_name=? AND change_id=?`, p.Connection, p.Database, id).Scan(&status)
		if e == nil {
			return nil, fmt.Errorf("变更 #%d 已有执行记录（%s），请核对台账；禁止重复提交", id, status)
		}
		if e != sql.ErrNoRows {
			return nil, e
		}
	}
	for table := range tables {
		p.Tables = append(p.Tables, table)
	}
	sort.Strings(p.Tables)
	db, err := openChangeMySQL(ctx, connection)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	catalog, err := readSQLCatalog(ctx, db, p.Database)
	if err != nil {
		return nil, err
	}
	p.SchemaDigest = digestJSON(catalogSnapshot(catalog, p.Tables))
	for _, snapshot := range syncSnapshots {
		q, _ := parseSyncQuery(snapshot.TargetSQL)
		table := catalog[q.Table]
		unique := false
		if table != nil {
			for _, idx := range table.Indexes {
				if idx.Unique && strings.Join(idx.Columns, "\x00") == strings.Join(snapshot.Keys, "\x00") {
					unique = true
				}
			}
		}
		if !unique {
			return nil, fmt.Errorf("同步匹配键不再对应目标唯一索引，请重新对比")
		}
	}
	dmlTables := map[string]int64{}
	syncScopes := map[string]bool{}
	for i := range p.Statements {
		op := &p.Statements[i]
		mode := "ddl"
		if op.Action == "insert" || op.Action == "update" || op.Action == "delete" {
			mode = "dml"
		}
		if p.Mode != "" && p.Mode != mode {
			return nil, fmt.Errorf("DDL 与 DML 必须分成不同 SQL 节点")
		}
		p.Mode = mode
		if err = applySQLSchema(catalog, op); err != nil {
			return nil, fmt.Errorf("第 %d 条预检失败：%w", i+1, err)
		}
		if mode == "dml" {
			if prior := dmlTables[op.Table]; prior != 0 && (prior != op.ChangeID || !syncChanges[op.ChangeID]) {
				return nil, fmt.Errorf("本版同一 SQL 节点每个表仅支持一条 DML，避免前序语句改变后续审批范围；批量插入请使用多行 VALUES")
			}
			dmlTables[op.Table] = op.ChangeID
			if op.ExpectedRows > op.MaxAffectedRows {
				return nil, fmt.Errorf("INSERT 超过允许影响行数")
			}
			// All DML statements are individually baseline-checked immediately
			// before executing, including when earlier statements changed rows.
			if op.Scope != "" {
				count, hash, e := sqlDataScope(ctx, db, op, false)
				if e != nil {
					return nil, e
				}
				op.ExpectedRows, op.DataDigest = count, hash
				if syncChanges[op.ChangeID] {
					key := op.Table + ":" + hash
					if count != 1 || syncScopes[key] {
						return nil, fmt.Errorf("同步更新必须对应互不重叠的单行唯一键")
					}
					syncScopes[key] = true
				}
			}
		}
	}
	if p.Mode == "ddl" {
		p.Warnings = append(p.Warnings, "DDL 会隐式提交；部分成功后停止，不自动重跑或假装事务回滚。")
	}
	if p.Mode == "dml" {
		p.Warnings = append(p.Warnings, "DML 在单个 InnoDB 事务中执行；目标数据变化或行数超过上限时停止。普通 SQL 每个表限一条 DML；数据同步仅允许同一冻结计划的互不重叠唯一键操作。")
	}
	return p, nil
}

type sqlQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func sqlDataScope(ctx context.Context, q sqlQueryer, op *SQLStatement, lock bool) (int, string, error) {
	query := "SELECT * FROM `" + op.Table + "` WHERE " + op.Scope + " LIMIT " + strconv.Itoa(op.MaxAffectedRows+1)
	if lock {
		query += " FOR UPDATE"
	}
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return 0, "", fmt.Errorf("读取 SQL 影响范围失败：%w", err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return 0, "", err
	}
	hashes := []string{}
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err = rows.Scan(ptrs...); err != nil {
			return 0, "", err
		}
		hashes = append(hashes, digestJSON(values))
		if len(hashes) > op.MaxAffectedRows {
			return 0, "", fmt.Errorf("SQL 影响范围超过 %d 行上限", op.MaxAffectedRows)
		}
	}
	if err = rows.Err(); err != nil {
		return 0, "", err
	}
	sort.Strings(hashes)
	return len(hashes), digestJSON([]any{cols, hashes}), nil
}

// The target advisory lock coordinates distinct platform processes/connections.
// A durable local ledger reserves all changes before any external write. An
// interrupted/uncertain execution is never retried automatically.
func (s *Store) executeSQLNode(ctx context.Context, run *DeliveryRun, p *SQLExecutionPlan) (output string, executionErr error) {
	c := s.GetConnection(p.Connection)
	if c == nil || sqlDestination(c) != p.DestinationDigest {
		return "", fmt.Errorf("已审核数据库连接身份发生变化，请重新预检审批")
	}
	if len(p.ChangeDigests) != len(p.ChangeIDs) || len(p.Services) != len(p.ChangeIDs) || len(p.Aliases) != len(p.ChangeIDs) {
		return "", fmt.Errorf("SQL 快照不完整，请重新生成执行单")
	}
	for i, id := range p.ChangeIDs {
		current := s.GetChange(id)
		if current == nil || current.SQL == nil || current.Status != ChangeReady || digestJSON([]any{current.Service, current.SQL}) != p.ChangeDigests[i] {
			return "", fmt.Errorf("SQL 变更内容或状态已变化，请重新预检审批")
		}
		binding, e := s.sqlBinding(p.Services[i], run.Plan.Environment.Name, p.Aliases[i])
		if e != nil || binding.Name != p.Connection {
			return "", fmt.Errorf("环境资源绑定发生变化，请重新提交")
		}
	}
	db, err := openChangeMySQL(ctx, c)
	if err != nil {
		return "", err
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	var acquired int
	if err = conn.QueryRowContext(ctx, `SELECT GET_LOCK(?,0)`, "devops-sql:"+p.Database).Scan(&acquired); err != nil || acquired != 1 {
		return "", fmt.Errorf("数据库正在执行其他变更，请稍后提交")
	}
	defer func() {
		unlock, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(unlock, `SELECT RELEASE_LOCK(?)`, "devops-sql:"+p.Database)
	}()
	catalog, err := readSQLCatalog(ctx, conn, p.Database)
	if err != nil {
		return "", err
	}
	if digestJSON(catalogSnapshot(catalog, p.Tables)) != p.SchemaDigest {
		return "", fmt.Errorf("审批后目标表结构发生变化，请重新预检审批")
	}
	if p.Mode == "ddl" {
		for _, approved := range p.Statements {
			ops, e := parseChangeSQL(approved.SQL)
			if e != nil {
				return "", e
			}
			if e = applySQLSchema(catalog, &ops[0]); e != nil {
				return "", e
			}
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	for _, id := range p.ChangeIDs {
		digest := sqlChangeDigest(p, id)
		if _, err = tx.Exec(`INSERT INTO sql_execution_ledger(connection,database_name,change_id,digest,status,run_id,node_id,detail) VALUES(?,?,?,?,'running',?,?,'已保留执行权；如中断需人工核对')`, p.Connection, p.Database, id, digest, run.ID, p.NodeID); err != nil {
			return "", fmt.Errorf("变更 #%d 已执行或执行结果待核对；禁止重复执行", id)
		}
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	status, detail := "uncertain", "执行中断或结果未知，禁止自动重试"
	defer func() {
		for _, id := range p.ChangeIDs {
			result, err := s.db.Exec(`UPDATE sql_execution_ledger SET status=?,detail=? WHERE connection=? AND database_name=? AND change_id=? AND run_id=?`, status, detail, p.Connection, p.Database, id, run.ID)
			if err == nil {
				n, _ := result.RowsAffected()
				if n != 1 {
					err = fmt.Errorf("执行台账记录未找到")
				}
			}
			if err != nil {
				output = "目标执行结果需人工核对：执行台账保存失败"
				executionErr = fmt.Errorf("%s", output)
			}
		}
	}()
	if p.Mode == "dml" {
		target, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
		if err != nil {
			status, detail = "failed", err.Error()
			return detail, err
		}
		defer target.Rollback()
		// Lock/check every approved scope BEFORE performing any write.
		for i := range p.Statements {
			op := &p.Statements[i]
			if op.Scope != "" {
				_, hash, e := sqlDataScope(ctx, target, op, true)
				if e != nil || hash != op.DataDigest {
					status, detail = "failed", "审批后目标数据变化或影响范围无法锁定，事务已回滚"
					return detail, fmt.Errorf("%s", detail)
				}
			}
		}
		total := int64(0)
		for _, op := range p.Statements {
			r, e := target.ExecContext(ctx, op.SQL)
			if e != nil {
				status, detail = "failed", "SQL 执行失败，事务回滚："+e.Error()
				return detail, e
			}
			n, e := r.RowsAffected()
			if e != nil || n > int64(op.MaxAffectedRows) {
				status, detail = "failed", "超过允许影响行数，事务回滚"
				return detail, fmt.Errorf("%s", detail)
			}
			total += n
		}
		if err = target.Commit(); err != nil {
			detail = "事务提交返回异常，需人工核对提交结果"
			return detail, fmt.Errorf("%s", detail)
		}
		status, detail = "success", fmt.Sprintf("事务提交成功，影响 %d 行", total)
		return detail, nil
	}
	completed := 0
	for _, op := range p.Statements {
		if op.Skip {
			completed++
			continue
		}
		if _, err = conn.ExecContext(ctx, op.SQL); err != nil {
			detail = fmt.Sprintf("DDL 第 %d 条失败；前 %d 条已完成，需人工核对：%s", completed+1, completed, err)
			return detail, fmt.Errorf("%s", detail)
		}
		completed++
	}
	// Re-read and apply the same declared operations: every operation should
	// now be an identical skip. No success on silently different definitions.
	after, err := readSQLCatalog(ctx, conn, p.Database)
	if err != nil {
		return "结构回读失败，需人工核对", err
	}
	for _, table := range p.Tables {
		expected, actual := catalog[table], after[table]
		if expected != nil && actual != nil && expected.Collation == "" && strings.HasPrefix(actual.Collation, "utf8mb4_") {
			expected.Collation = actual.Collation
			for name, col := range expected.Columns {
				expected.Columns[name] = defaultColumnCollation(col, actual.Collation)
			}
		}
		if digestJSON(expected) != digestJSON(actual) {
			detail = "DDL 回读验证不一致，需人工核对：" + table
			return detail, fmt.Errorf("%s", detail)
		}
	}
	status, detail = "success", fmt.Sprintf("%d 条 DDL 完成（含结构一致跳过），回读验证通过", completed)
	return detail, nil
}
func sqlChangeDigest(p *SQLExecutionPlan, id int64) string {
	ops := []SQLStatement{}
	for _, op := range p.Statements {
		if op.ChangeID == id {
			ops = append(ops, op)
		}
	}
	return digestJSON(ops)
}

func (s *Store) SQLLedger() []map[string]any {
	out := []map[string]any{}
	rows, e := s.db.Query(`SELECT connection,database_name,change_id,status,run_id,node_id,detail FROM sql_execution_ledger ORDER BY run_id DESC LIMIT 200`)
	if e != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var c, d, status, node, detail string
		var change, run int64
		if rows.Scan(&c, &d, &change, &status, &run, &node, &detail) == nil {
			out = append(out, map[string]any{"connection": c, "database": d, "change_id": change, "status": status, "run_id": run, "node_id": node, "detail": detail})
		}
	}
	return out
}

func sqlExecutionContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 2*time.Minute)
}
