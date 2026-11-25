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
	EnvironmentOverrides []SQLEnvironmentOverride `json:"environment_overrides,omitempty"`
	ResourceAlias        string                   `json:"resource_alias"`
	Content              string                   `json:"content"`
	MaxAffectedRows      int                      `json:"max_affected_rows"`
	DataSync             *DataSyncSnapshot        `json:"data_sync,omitempty"`
}
type sqlColumn struct {
	generationRefs []string
	Type           string  `json:"type"`
	Nullable       bool    `json:"nullable"`
	Default        *string `json:"default"`
	AutoIncrement  bool    `json:"auto_increment"`
	Comment        string  `json:"comment"`
	Collation      string  `json:"collation,omitempty"`
	Generation     string  `json:"generation,omitempty"`
	Extra          string  `json:"extra,omitempty"`
}
type sqlIndex struct {
	Comment string   `json:"comment,omitempty"`
	Unique  bool     `json:"unique"`
	Columns []string `json:"columns"`
}
type sqlTable struct {
	Comment   string               `json:"comment,omitempty"`
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
	Database        string `json:"database,omitempty"`
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
	SchemaVersion     int                  `json:"schema_version,omitempty"`
	PreviousNodes     []string             `json:"previous_nodes,omitempty"`
	ExpectedSchema    sqlCatalog           `json:"expected_schema,omitempty"`
	DeferredData      bool                 `json:"deferred_data,omitempty"`
	ScriptSelections  []SQLScriptSelection `json:"script_selections,omitempty"`
	Applied           []AppliedSQLChange   `json:"applied,omitempty"`
	NodeID            string               `json:"node_id"`
	ChangeIDs         []int64              `json:"change_ids"`
	Services          []string             `json:"services"`
	Aliases           []string             `json:"aliases"`
	ChangeDigests     []string             `json:"change_digests"`
	Connection        string               `json:"connection"`
	Database          string               `json:"database"`
	DestinationDigest string               `json:"destination_digest"`
	SchemaDigest      string               `json:"schema_digest"`
	Tables            []string             `json:"tables"`
	Statements        []SQLStatement       `json:"statements"`
	Mode              string               `json:"mode"`
	Warnings          []string             `json:"warnings"`
}

var sqlIdentifierRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
var intWidthRE = regexp.MustCompile(`\b(tinyint|smallint|mediumint|int|bigint)\([0-9]+\)`)
var createTableRE = regexp.MustCompile("(?is)^create\\s+table\\s+(?:if\\s+not\\s+exists\\s+)?(`?[A-Za-z_][A-Za-z0-9_]*`?)\\s*(\\(.*\\))\\s*(?:engine\\s*=\\s*InnoDB)?\\s*(?:(?:default\\s+)?charset\\s*=\\s*utf8mb4)?\\s*$")
var addColumnRE = regexp.MustCompile("(?is)^alter\\s+table\\s+(`?[A-Za-z_][A-Za-z0-9_]*`?)\\s+add\\s+(?:column\\s+)?(.+)$")
var createIndexRE = regexp.MustCompile("(?is)^create\\s+(unique\\s+)?index\\s+(`?[A-Za-z_][A-Za-z0-9_]*`?)\\s+on\\s+(`?[A-Za-z_][A-Za-z0-9_]*`?)\\s*\\(([^()]*)\\)\\s*$")

func saveSQLPayload(tx *deployTx, v *Change) error {
	for _, dep := range v.Prerequisites {
		var exists int
		if dep.ChangeID == v.ID {
			return fmt.Errorf("变更不能依赖自身")
		}
		if err := tx.QueryRow(`SELECT COUNT(*) FROM changes WHERE id=? AND status<>'archived'`, dep.ChangeID).Scan(&exists); err != nil {
			return err
		}
		if exists != 1 {
			return fmt.Errorf("前置变更 #%d 不存在或已归档", dep.ChangeID)
		}
	}
	if err := validateSQLOverrideEnvironments(tx, v.SQL); err != nil {
		return err
	}
	doc, _ := json.Marshal(struct {
		Prerequisites []ChangePrerequisite `json:"prerequisites,omitempty"`
		SQL           *SQLChange           `json:"sql,omitempty"`
		Nacos         *NacosChange         `json:"nacos,omitempty"`
		Redis         *RedisChange         `json:"redis,omitempty"`
	}{v.Prerequisites, v.SQL, v.Nacos, v.Redis})
	_, err := tx.Exec(`INSERT INTO change_payloads(change_id,document) VALUES(?,?) ON CONFLICT(change_id) DO UPDATE SET document=excluded.document`, v.ID, string(doc))
	return err
}
func loadChangePayload(q interface{ QueryRow(string, ...any) *sql.Row }, v *Change) error {
	if err := loadChangeOwnership(q, v); err != nil {
		return err
	}
	var doc string
	err := q.QueryRow(`SELECT document FROM change_payloads WHERE change_id=?`, v.ID).Scan(&doc)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	var payload struct {
		Prerequisites []ChangePrerequisite `json:"prerequisites,omitempty"`
		SQL           *SQLChange           `json:"sql"`
		Nacos         *NacosChange         `json:"nacos"`
		Redis         *RedisChange         `json:"redis"`
	}
	if err := json.Unmarshal([]byte(doc), &payload); err != nil {
		return err
	}
	v.Prerequisites, v.SQL, v.Nacos, v.Redis = payload.Prerequisites, payload.SQL, payload.Nacos, payload.Redis
	return validateChangePrerequisites(v)
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
			return nil, fmt.Errorf("含注释的 SQL 暂不支持就绪或执行（包括版本注释和优化器提示）；可保留原文保存草稿")
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
	if err := validateSQLDraftContent(content); err != nil {
		return nil, err
	}
	// Only change scripts accept ordinary comments. Keep the frozen source in
	// SQLChange, but validate and execute the same comment-free statement text.
	// The narrower read-query/data-sync tokenizer remains unchanged.
	content, err := sqlChangeWithoutComments(content)
	if err != nil {
		return nil, err
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
		parsedDDL, isDDL, ddlError := parseChangeDDL(piece)
		if ddlError != nil {
			return nil, ddlError
		}
		if isDDL {
			op = parsedDDL
		} else {
			stmt, e := sqlparser.Parse(piece)
			if e != nil {
				return nil, fmt.Errorf("SQL 解析失败：%w", e)
			}
			var where *sqlparser.Where
			qualifier := ""
			switch v := stmt.(type) {
			case *sqlparser.Insert:
				values, ok := v.Rows.(sqlparser.Values)
				if v.Action != "insert" || v.Ignore != "" || len(v.OnDup) > 0 || len(v.Partitions) > 0 || !ok || len(v.Columns) == 0 || len(values) > 10000 {
					return nil, fmt.Errorf("INSERT 仅支持指定字段的 VALUES，不支持 IGNORE/REPLACE/SELECT/ON DUPLICATE")
				}
				op.Table = v.Table.Name.String()
				op.Database = v.Table.Qualifier.String()
				v.Table.Qualifier = sqlparser.TableIdent{}
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
				op.Table, op.Database, qualifier, e = singleSQLChangeTable(v.TableExprs)
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
				op.Table, op.Database, qualifier, e = singleSQLChangeTable(v.TableExprs)
				where = v.Where
				op.Action = "delete"
			default:
				if _, ok := stmt.(*sqlparser.DDL); ok {
					return nil, fmt.Errorf("该 DDL 操作尚不支持；可使用受限 CREATE TABLE、ADD COLUMN 或 ADD INDEX。原文可保存为草稿，不能就绪或执行")
				}
				if strings.HasPrefix(strings.ToLower(piece), "start transaction") || strings.EqualFold(piece, "begin") || strings.EqualFold(piece, "commit") || strings.EqualFold(piece, "rollback") {
					return nil, fmt.Errorf("平台统一管理事务，暂不支持脚本内的事务控制语句；原文可保存为草稿，不能就绪或执行")
				}
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
			}
			if e = sqlparser.Walk(func(node sqlparser.SQLNode) (bool, error) {
				switch n := node.(type) {
				case *sqlparser.FuncExpr, *sqlparser.Subquery:
					return false, fmt.Errorf("不支持函数或子查询")
				case *sqlparser.ColName:
					if !n.Qualifier.Qualifier.IsEmpty() || (!n.Qualifier.IsEmpty() && n.Qualifier.Name.String() != qualifier) || !sqlIdentifierRE.MatchString(n.Name.String()) {
						return false, fmt.Errorf("字段限定必须是当前单表名称或声明的别名，不支持跨表字段或会话变量")
					}
					n.Qualifier = sqlparser.TableName{}
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
			if op.Database != "" && !sqlIdentifierRE.MatchString(op.Database) {
				return nil, fmt.Errorf("数据库名称无效")
			}
			// Normalize aliases for both scope reads and execution. Never run the
			// original qualified SQL against a different environment database.
			op.SQL = sqlparser.String(stmt)
			if where != nil {
				op.Scope = sqlparser.String(where.Expr)
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

// Change-only normalization; read queries and data synchronization retain their
// existing narrower dialect. Database qualification is checked at preflight.
func singleSQLChangeTable(exprs sqlparser.TableExprs) (table, database, qualifier string, err error) {
	if len(exprs) != 1 {
		return "", "", "", fmt.Errorf("仅支持单表 SQL")
	}
	a, ok := exprs[0].(*sqlparser.AliasedTableExpr)
	if !ok || a.Hints != nil || len(a.Partitions) > 0 {
		return "", "", "", fmt.Errorf("不支持 JOIN、索引提示或分区限定；支持单表别名")
	}
	n, ok := a.Expr.(sqlparser.TableName)
	if !ok {
		return "", "", "", fmt.Errorf("不支持子查询")
	}
	table, database, qualifier = n.Name.String(), n.Qualifier.String(), n.Name.String()
	if !a.As.IsEmpty() {
		qualifier = a.As.String()
		if !sqlIdentifierRE.MatchString(qualifier) {
			return "", "", "", fmt.Errorf("表别名无效")
		}
	}
	n.Qualifier = sqlparser.TableIdent{}
	a.Expr, a.As = n, sqlparser.TableIdent{}
	return table, database, qualifier, nil
}

func validateSQLDatabase(op SQLStatement, database string) error {
	if op.Database != "" && op.Database != database {
		return fmt.Errorf("SQL 指定数据库 %s 与环境绑定数据库 %s 不一致，禁止跨库；请使用不带库名的表名以复用到多个环境", op.Database, database)
	}
	return nil
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
	if c.Collation == "" && ddlCharacterColumn(c.Type) {
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
			if expected.Collation == "" {
				expected.Collation = t.Collation
			}
			for name, col := range expected.Columns {
				expected.Columns[name] = defaultColumnCollation(col, expected.Collation)
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
		if err := validateDDLGenerationRefs(op.columnName, op.column, t); err != nil {
			return err
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
			return mysqlDatabaseTarget(c, b.Config)
		}
	}
	return nil, fmt.Errorf("应用 %s 的资源 %s 尚未绑定 %s 环境连接", service, alias, environment)
}

func (s *Store) prepareSQLNode(ctx context.Context, nodeID string, ids []int64, environment string) (*SQLExecutionPlan, error) {
	return s.prepareSQLStage(ctx, nodeID, ids, environment, nil)
}

func (s *Store) prepareSQLStage(ctx context.Context, nodeID string, ids []int64, environment string, prior []*SQLExecutionPlan) (*SQLExecutionPlan, error) {
	if len(ids) == 0 || len(ids) > 50 {
		return nil, fmt.Errorf("SQL 节点需选择 1–50 个变更")
	}
	p := &SQLExecutionPlan{SchemaVersion: 2, NodeID: nodeID, ChangeIDs: []int64{}, Warnings: []string{}, Statements: []SQLStatement{}, Services: []string{}, Aliases: []string{}}
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
		selected, explicit, err := selectEnvironmentSQL(c.SQL, environment)
		if err != nil {
			return nil, fmt.Errorf("SQL 变更 #%d：%w", id, err)
		}
		if explicit {
			p.ScriptSelections = append(p.ScriptSelections, SQLScriptSelection{ChangeID: id, Environment: environment, Content: selected.Content, DefaultContent: c.SQL.Content, Skip: selected.Skip, Reason: selected.Reason, ChangeDigest: digestJSON([]any{c.Service, c.SQL})})
			if selected.Skip {
				p.Warnings = append(p.Warnings, fmt.Sprintf("变更 #%d 在 %s 无需执行：%s", id, environment, selected.Reason))
				continue
			}
			p.Warnings = append(p.Warnings, fmt.Sprintf("变更 #%d 使用 %s 环境专用 SQL，请核对与默认脚本的差异；其他环境的验证结果不代表本脚本已验证。原因：%s", id, environment, selected.Reason))
		}
		resource, err := s.sqlBinding(c.Service, environment, c.SQL.ResourceAlias)
		if err != nil {
			return nil, err
		}
		if connection != nil && (connection.Name != resource.Name || connection.Config["database"] != resource.Config["database"]) {
			return nil, fmt.Errorf("同一 SQL 节点只能操作一个目标数据库，请分成多个节点")
		}
		connection = resource
		applied, err := s.appliedSQLChange(c, resource, environment)
		if err != nil {
			return nil, err
		}
		if applied != nil {
			p.Applied = append(p.Applied, *applied)
			continue
		}
		p.ChangeIDs = append(p.ChangeIDs, id)
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
		ops, err := parseChangeSQL(selected.Content)
		if err != nil {
			return nil, err
		}
		for _, op := range ops {
			if err := validateSQLDatabase(op, resource.Config["database"]); err != nil {
				return nil, err
			}
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
	if connection == nil {
		p.Mode = "skipped"
		return p, nil
	}
	p.Connection, p.Database, p.DestinationDigest = connection.Name, connection.Config["database"], sqlDestination(connection)
	if len(p.ChangeIDs) == 0 {
		p.Mode = "applied"
		p.Warnings = append(p.Warnings, "全部 SQL 变更已有可验证的成功记录，本节点不再执行 SQL。")
		return p, nil
	}
	for _, id := range p.ChangeIDs {
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
	catalog, err := readSQLCatalogVersion(ctx, db, p.Database, p.SchemaVersion)
	if err != nil {
		return nil, err
	}
	if err = validateSQLCollations(ctx, db, p.Statements); err != nil {
		return nil, err
	}
	for _, previous := range prior {
		if previous.Mode == "applied" || previous.Mode == "skipped" || s.sqlPhysicalTarget(previous.Connection, previous.Database) != s.sqlPhysicalTarget(p.Connection, p.Database) {
			continue
		}
		p.PreviousNodes = append(p.PreviousNodes, previous.NodeID)
		if previous.Mode == "ddl" {
			for _, frozen := range previous.Statements {
				ops, e := parseChangeSQL(frozen.SQL)
				if e != nil || len(ops) != 1 {
					return nil, fmt.Errorf("前序 SQL 阶段解析失败")
				}
				if e = applySQLSchema(catalog, &ops[0]); e != nil {
					return nil, fmt.Errorf("前序 SQL 阶段 %s：%w", previous.NodeID, e)
				}
			}
		}
	}
	if len(p.PreviousNodes) > 0 {
		if len(syncSnapshots) > 0 {
			return nil, fmt.Errorf("数据同步使用冻结数据快照，请在前序阶段完成后单独对比并交付")
		}
		raw, _ := json.Marshal(catalogSnapshot(catalog, p.Tables))
		_ = json.Unmarshal(raw, &p.ExpectedSchema)
		p.DeferredData = true
		p.Warnings = append(p.Warnings, "分阶段 SQL：前序同库节点必须成功；本阶段按冻结脚本和行数上限核验当时数据，保存独立核验回执后执行。")
	}
	p.SchemaDigest = digestJSON(catalogSnapshot(catalog, p.Tables))
	for _, snapshot := range syncSnapshots {
		q, _ := parseSyncQuery(snapshot.TargetSQL)
		table := catalog[q.Table]
		unique := false
		if table != nil {
			for _, idx := range table.Indexes {
				if idx.Unique && strings.Join(idx.Columns, "\x00") == strings.Join(syncExecutionKeys(snapshot.Keys, snapshot.Tree), "\x00") {
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
			if op.Scope != "" && p.DeferredData {
				op.ExpectedRows = -1
				continue
			}
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
	if err := s.verifySQLScriptSelections(p, run.Plan.Environment.Name); err != nil {
		return "", err
	}
	if err := s.verifySQLReuse(p, run.Plan.Environment.Name); err != nil {
		return "", err
	}
	skipped := 0
	for _, selection := range p.ScriptSelections {
		if selection.Skip {
			skipped++
		}
	}
	if len(p.ChangeIDs) == 0 && skipped > 0 {
		return fmt.Sprintf("按环境设置跳过 %d 项 SQL，复用 %d 项历史成功记录；未执行 SQL", skipped, len(p.Applied)), nil
	}
	if len(p.ChangeIDs) == 0 && len(p.Applied) > 0 {
		return fmt.Sprintf("已跳过 %d 项历史成功 SQL 变更，未重复执行", len(p.Applied)), nil
	}
	if err := s.verifySQLStagePredecessors(run, p); err != nil {
		return "", err
	}
	c, err := s.reviewedSQLConnection(p, run.Plan.Environment.Name)
	if err != nil {
		return "", err
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
	catalog, err := readSQLCatalogVersion(ctx, conn, p.Database, p.SchemaVersion)
	if err != nil {
		return "", err
	}
	if !sqlStageSchemaMatches(p, catalog) {
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
		// Tree parents and unselected nodes are also part of the approved topology.
		for _, id := range p.ChangeIDs {
			change := s.GetChange(id)
			if change.SQL.DataSync != nil && change.SQL.DataSync.Tree != nil {
				snapshot := change.SQL.DataSync
				q, e := parseSyncQuery(snapshot.TargetSQL)
				if e != nil {
					return "", e
				}
				rows, e := querySyncRows(ctx, target, q, true)
				if e != nil || syncRowsDigest(rows) != snapshot.TargetDigest {
					status, detail = "failed", "审批后菜单层级或数据已变化，事务已回滚，请重新对比"
					return detail, fmt.Errorf("%s", detail)
				}
			}
		}
		stageScopes := []SQLStageScope{}
		// Lock/check every approved scope BEFORE performing any write.
		for i := range p.Statements {
			op := &p.Statements[i]
			if op.Scope != "" {
				count, hash, e := sqlDataScope(ctx, target, op, true)
				if p.DeferredData && e == nil {
					stageScopes = append(stageScopes, SQLStageScope{Statement: i, Rows: count, Digest: hash})
				}
				if e != nil || (!p.DeferredData && hash != op.DataDigest) {
					status, detail = "failed", "审批后目标数据变化或影响范围无法锁定，事务已回滚"
					return detail, fmt.Errorf("%s", detail)
				}
			}
		}
		if p.DeferredData {
			baseline := SQLStageBaseline{NodeID: p.NodeID, PlanDigest: digestJSON(p), SchemaDigest: digestJSON(catalogSnapshot(catalog, p.Tables)), Scopes: stageScopes, CheckedAt: time.Now().Unix()}
			raw, _ := json.Marshal(baseline)
			if _, e := s.db.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?)`, sqlStageBaselineKey(run.ID, p.NodeID), string(raw)); e != nil {
				status, detail = "failed", "阶段核验回执已存在或无法保存，禁止重复执行"
				return detail, fmt.Errorf("%s", detail)
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
		// Confirm that inserts/moves remain visible in the task's target scope.
		// A WHERE filter on physical IDs must never make a successful migration
		// disappear from the next comparison and create duplicate logical menus.
		for _, id := range p.ChangeIDs {
			change := s.GetChange(id)
			if change.SQL.DataSync != nil && change.SQL.DataSync.Tree != nil {
				snapshot := change.SQL.DataSync
				q, e := parseSyncQuery(snapshot.TargetSQL)
				if e != nil {
					return "", e
				}
				rows, e := querySyncRows(ctx, target, q, true)
				if e == nil {
					e = verifySyncTreeResult(rows, snapshot)
				}
				if e != nil {
					status, detail = "failed", "迁移后菜单超出目标查询范围或层级不一致，事务已回滚："+e.Error()
					return detail, fmt.Errorf("%s", detail)
				}
			}
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
	after, err := readSQLCatalogVersion(ctx, conn, p.Database, p.SchemaVersion)
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

// Resolve and verify every reviewed alias before opening any external connection.
func (s *Store) reviewedSQLConnection(p *SQLExecutionPlan, environment string) (*ResourceConnection, error) {
	c, targetErr := mysqlDatabaseTarget(s.GetConnection(p.Connection), map[string]string{"database": p.Database})
	if targetErr != nil || sqlDestination(c) != p.DestinationDigest {
		return nil, fmt.Errorf("已审核数据库连接身份发生变化，请重新预检审批")
	}
	if len(p.ChangeDigests) != len(p.ChangeIDs) || len(p.Services) != len(p.ChangeIDs) || len(p.Aliases) != len(p.ChangeIDs) {
		return nil, fmt.Errorf("SQL 快照不完整，请重新生成执行单")
	}
	for i, id := range p.ChangeIDs {
		current := s.GetChange(id)
		if current == nil || current.SQL == nil || current.Status != ChangeReady || digestJSON([]any{current.Service, current.SQL}) != p.ChangeDigests[i] {
			return nil, fmt.Errorf("SQL 变更内容或状态已变化，请重新预检审批")
		}
		binding, e := s.sqlBinding(p.Services[i], environment, p.Aliases[i])
		if e != nil || binding.Name != p.Connection || sqlDestination(binding) != p.DestinationDigest || binding.Config["database"] != p.Database {
			return nil, fmt.Errorf("环境资源绑定发生变化，请重新提交")
		}
	}
	return c, nil
}
