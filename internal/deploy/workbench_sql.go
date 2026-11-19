package deploy

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/xwb1989/sqlparser"
)

// Interactive reads deliberately do not share the stricter data-sync grammar.
// Local tables and joins, bounded results, a read-only transaction and a small
// pure function allowlist are accepted. No arbitrary functions or subqueries.
type workbenchSQL struct {
	SQL, Table, Metadata string
	Limit                int
	Args                 []any
}

var workbenchReadFunctions = map[string][2]int{
	"count": {1, 1}, "min": {1, 1}, "max": {1, 1}, "sum": {1, 1}, "avg": {1, 1},
	"coalesce": {1, 8}, "ifnull": {2, 2}, "nullif": {2, 2}, "concat": {1, 8},
	"lower": {1, 1}, "upper": {1, 1}, "trim": {1, 1}, "length": {1, 1}, "char_length": {1, 1},
	"substr": {2, 3}, "substring": {2, 3}, "abs": {1, 1}, "round": {1, 2},
	"date": {1, 1}, "date_format": {2, 2}, "now": {0, 0}, "curdate": {0, 0}, "date_add": {2, 2}, "date_sub": {2, 2},
}

func parseWorkbenchSQL(input string) (*workbenchSQL, error) {
	tokens, err := sqlTokens(input)
	if err != nil {
		return nil, err
	}
	if len(tokens) > 0 && tokens[len(tokens)-1] == ";" {
		tokens = tokens[:len(tokens)-1]
	}
	for _, token := range tokens {
		if token == ";" || token == "@" || token == "into" || token == "outfile" || token == "dumpfile" || token == "procedure" {
			return nil, fmt.Errorf("不支持多语句、变量或导出子句")
		}
	}
	if len(tokens) == 2 && tokens[0] == "show" && tokens[1] == "tables" {
		return &workbenchSQL{Metadata: "tables", Limit: 1000}, nil
	}
	// Token validation above rejects comments and extra statements. Preserve the
	// identifier's case: Linux MySQL table names may be case-sensitive.
	if match := regexp.MustCompile("(?i)^\\s*(?:describe|desc)\\s+`?([A-Za-z_][A-Za-z0-9_]{0,63})`?\\s*;?\\s*$").FindStringSubmatch(input); match != nil {
		return &workbenchSQL{Metadata: "schema", Table: match[1], Limit: 1000}, nil
	}
	if match := regexp.MustCompile("(?i)^\\s*show\\s+(?:index|indexes|keys)\\s+from\\s+`?([A-Za-z_][A-Za-z0-9_]{0,63})`?\\s*;?\\s*$").FindStringSubmatch(input); match != nil {
		return &workbenchSQL{Metadata: "indexes", Table: match[1], Limit: 1000}, nil
	}
	stmt, err := sqlparser.Parse(input)
	if err != nil {
		return nil, fmt.Errorf("查询 SQL 无法解析，请检查语法；一次仅执行一条语句")
	}
	q, ok := stmt.(*sqlparser.Select)
	if !ok || q.Lock != "" || len(q.Comments) > 0 || len(q.SelectExprs) > 50 {
		return nil, fmt.Errorf("查询仅支持 SELECT、SHOW TABLES、DESCRIBE 表名、SHOW INDEX FROM 表名；写操作请提交变更")
	}
	table, qualifiers, err := workbenchSQLTables(q.From)
	if err != nil {
		return nil, err
	}
	validQualifier := func(n sqlparser.TableName) bool {
		return n.Qualifier.IsEmpty() && (n.Name.IsEmpty() || qualifiers[strings.ToLower(n.Name.String())])
	}
	expressionCount := 0
	err = sqlparser.Walk(func(n sqlparser.SQLNode) (bool, error) {
		if _, ok := n.(sqlparser.Expr); ok {
			expressionCount++
			if expressionCount > 512 {
				return false, fmt.Errorf("查询表达式过多，请拆分查询")
			}
			switch n.(type) {
			case *sqlparser.AndExpr, *sqlparser.OrExpr, *sqlparser.NotExpr, *sqlparser.ParenExpr,
				*sqlparser.ComparisonExpr, *sqlparser.RangeCond, *sqlparser.IsExpr,
				*sqlparser.SQLVal, *sqlparser.NullVal, sqlparser.BoolVal, *sqlparser.ColName,
				sqlparser.ValTuple, *sqlparser.BinaryExpr, *sqlparser.UnaryExpr, *sqlparser.FuncExpr, *sqlparser.CaseExpr, *sqlparser.ConvertExpr, *sqlparser.ConvertUsingExpr, *sqlparser.SubstrExpr, *sqlparser.IntervalExpr:
			default:
				return false, fmt.Errorf("不支持此查询表达式，请使用字段、常量、条件或支持的只读函数")
			}
		}
		switch v := n.(type) {
		case sqlparser.Nextval, *sqlparser.Nextval:
			return false, fmt.Errorf("查询不能生成或修改序列")
		case *sqlparser.Subquery:
			return false, fmt.Errorf("暂不支持子查询")
		case *sqlparser.ColName:
			if !validQualifier(v.Qualifier) || !sqlIdentifierRE.MatchString(v.Name.String()) {
				return false, fmt.Errorf("字段限定必须是当前查询的表名或别名，不能跨库")
			}
		case *sqlparser.IntervalExpr:
			switch strings.ToLower(v.Unit) {
			case "year", "quarter", "month", "week", "day", "hour", "minute", "second", "microsecond":
			default:
				return false, fmt.Errorf("不支持此时间单位")
			}
		case *sqlparser.ConvertUsingExpr:
			if !strings.EqualFold(v.Type, "utf8mb4") && !strings.EqualFold(v.Type, "utf8") {
				return false, fmt.Errorf("字符集转换仅支持 UTF-8")
			}
		case *sqlparser.ConvertExpr:
			switch strings.ToLower(v.Type.Type) {
			case "char", "date", "datetime", "decimal", "signed", "unsigned":
			default:
				return false, fmt.Errorf("不支持此转换类型")
			}
			if v.Type.Charset != "" && !strings.EqualFold(v.Type.Charset, "utf8mb4") && !strings.EqualFold(v.Type.Charset, "utf8") {
				return false, fmt.Errorf("字符集转换仅支持 UTF-8")
			}
			for _, size := range []*sqlparser.SQLVal{v.Type.Length, v.Type.Scale} {
				if size != nil {
					n, e := strconv.Atoi(string(size.Val))
					if size.Type != sqlparser.IntVal || e != nil || n < 0 || n > 65536 {
						return false, fmt.Errorf("转换长度超出范围")
					}
				}
			}
		case *sqlparser.FuncExpr:
			name := strings.ToLower(v.Name.String())
			bounds, supported := workbenchReadFunctions[name]
			if !v.Qualifier.IsEmpty() || !supported || len(v.Exprs) < bounds[0] || len(v.Exprs) > bounds[1] {
				return false, fmt.Errorf("函数或参数不支持；可使用聚合、空值处理、常用字符串 / 数值 / 日期函数")
			}
			for _, arg := range v.Exprs {
				switch arg.(type) {
				case *sqlparser.StarExpr:
					if name != "count" || len(v.Exprs) != 1 {
						return false, fmt.Errorf("只有 COUNT 支持 *")
					}
				case *sqlparser.AliasedExpr: // Each expression is checked recursively by Walk.
				default:
					return false, fmt.Errorf("函数参数无效")
				}
			}
		case *sqlparser.SQLVal:
			if v.Type == sqlparser.ValArg {
				return false, fmt.Errorf("不支持未绑定参数")
			}
		case *sqlparser.StarExpr:
			if !validQualifier(v.TableName) {
				return false, fmt.Errorf("* 的限定必须是当前查询的表名或别名")
			}
		}
		return true, nil
	}, q)
	if err != nil {
		return nil, err
	}
	for _, order := range q.OrderBy {
		if _, ok := order.Expr.(*sqlparser.ColName); !ok {
			return nil, fmt.Errorf("排序请使用字段名或结果别名")
		}
	}
	limit := 200
	if q.Limit != nil {
		integer := func(expr sqlparser.Expr) (int, error) {
			v, ok := expr.(*sqlparser.SQLVal)
			if !ok || v.Type != sqlparser.IntVal {
				return 0, fmt.Errorf("LIMIT 必须是整数")
			}
			return strconv.Atoi(string(v.Val))
		}
		limit, err = integer(q.Limit.Rowcount)
		if err != nil || limit < 1 || limit > 1000 {
			return nil, fmt.Errorf("LIMIT 为 1–1000 行")
		}
		if q.Limit.Offset != nil {
			offset, e := integer(q.Limit.Offset)
			if e != nil || offset < 0 || offset > 100000 {
				return nil, fmt.Errorf("偏移量为 0–100000，深分页请使用主键范围")
			}
		}
	} else {
		q.Limit = &sqlparser.Limit{Rowcount: sqlparser.NewIntVal([]byte("200"))}
	}
	// Bind string literals instead of re-escaping them into SQL. This preserves
	// quotes under either MySQL backslash mode, and keeps values out of SQL text.
	args := []any{}
	_ = sqlparser.Walk(func(n sqlparser.SQLNode) (bool, error) {
		if value, ok := n.(*sqlparser.SQLVal); ok && value.Type == sqlparser.StrVal {
			args = append(args, string(value.Val))
			value.Type = sqlparser.ValArg
			value.Val = []byte("?")
		}
		return true, nil
	}, q)
	return &workbenchSQL{SQL: sqlparser.String(q), Table: table, Limit: limit, Args: args}, nil
}

func workbenchSQLTables(exprs sqlparser.TableExprs) (string, map[string]bool, error) {
	qualifiers := map[string]bool{}
	tables := []string{}
	var visit func(sqlparser.TableExpr) error
	visit = func(expr sqlparser.TableExpr) error {
		switch v := expr.(type) {
		case *sqlparser.AliasedTableExpr:
			n, ok := v.Expr.(sqlparser.TableName)
			if !ok || !n.Qualifier.IsEmpty() || v.Hints != nil || len(v.Partitions) > 0 {
				return fmt.Errorf("仅支持同库表查询，不支持跨库、子查询、索引提示或分区限定")
			}
			name, alias := n.Name.String(), n.Name.String()
			if !v.As.IsEmpty() {
				alias = v.As.String()
			}
			if !sqlIdentifierRE.MatchString(name) || !sqlIdentifierRE.MatchString(alias) || qualifiers[strings.ToLower(alias)] {
				return fmt.Errorf("表名或别名无效，联表时请使用不同别名")
			}
			qualifiers[strings.ToLower(alias)] = true
			tables = append(tables, name)
		case *sqlparser.JoinTableExpr:
			if err := visit(v.LeftExpr); err != nil {
				return err
			}
			return visit(v.RightExpr)
		case *sqlparser.ParenTableExpr:
			for _, child := range v.Exprs {
				if err := visit(child); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("不支持此表查询表达式")
		}
		return nil
	}
	for _, expr := range exprs {
		if err := visit(expr); err != nil {
			return "", nil, err
		}
	}
	if len(tables) == 0 {
		return "", nil, fmt.Errorf("请选择查询表")
	}
	return strings.Join(tables, ","), qualifiers, nil
}

func readWorkbenchSQL(ctx context.Context, c *ResourceConnection, p *workbenchSQL) (map[string]any, error) {
	started := time.Now()
	db, err := openChangeMySQL(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("数据库连接失败，请检查地址、网络、认证与权限")
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("无法建立只读事务")
	}
	defer tx.Rollback()
	query, args := p.SQL, p.Args
	switch p.Metadata {
	case "tables":
		query = "SELECT TABLE_NAME AS table_name FROM information_schema.TABLES WHERE TABLE_SCHEMA=? AND TABLE_TYPE='BASE TABLE' ORDER BY TABLE_NAME LIMIT 1000"
		args = []any{c.Config["database"]}
	case "indexes":
		query = "SELECT INDEX_NAME AS index_name,NON_UNIQUE AS non_unique,SEQ_IN_INDEX AS position,COLUMN_NAME AS field,INDEX_TYPE AS index_type FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=? AND TABLE_NAME=? ORDER BY INDEX_NAME,SEQ_IN_INDEX LIMIT 1000"
		args = []any{c.Config["database"], p.Table}
	case "schema":
		query = "SELECT COLUMN_NAME AS field,COLUMN_TYPE AS type,IS_NULLABLE AS nullable,COLUMN_KEY AS key_type,COLUMN_DEFAULT AS default_value FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=? AND TABLE_NAME=? ORDER BY ORDINAL_POSITION LIMIT 1000"
		args = []any{c.Config["database"], p.Table}
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("查询超时，请缩小范围或检查索引")
		}
		return nil, fmt.Errorf("查询失败，请核对表名、字段、数据库账号权限或语法")
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	if len(columns) > 100 {
		return nil, fmt.Errorf("结果最多 100 列，请指定字段")
	}
	seen := map[string]bool{}
	for _, c := range columns {
		if seen[c] {
			return nil, fmt.Errorf("结果字段重名，请使用不同别名")
		}
		seen[c] = true
	}
	out := []map[string]any{}
	total := 0
	truncated := false
	for rows.Next() {
		values := make([]sql.RawBytes, len(columns))
		ptr := make([]any, len(columns))
		for i := range values {
			ptr[i] = &values[i]
		}
		if err = rows.Scan(ptr...); err != nil {
			return nil, fmt.Errorf("读取查询结果失败")
		}
		row := map[string]any{}
		for i, col := range columns {
			if values[i] == nil {
				row[col] = nil
				continue
			}
			if !utf8.Valid(values[i]) {
				row[col] = "[二进制内容，未展示]"
				continue
			}
			v := values[i]
			if len(v) > 65536 {
				v = v[:65536]
				for !utf8.Valid(v) {
					v = v[:len(v)-1]
				}
				truncated = true
			}
			total += len(v)
			row[col] = string(v)
		}
		if total > 2<<20 {
			truncated = true
			break
		}
		out = append(out, row)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("读取结果中断或超时，请缩小查询范围")
	}
	return map[string]any{"columns": columns, "rows": out, "elapsed_ms": time.Since(started).Milliseconds(), "truncated": truncated, "row_limit": p.Limit, "limit_reached": len(out) >= p.Limit, "target": map[string]any{"connection": c.Name, "database": c.Config["database"]}, "metadata": p.Metadata, "table": p.Table}, nil
}
