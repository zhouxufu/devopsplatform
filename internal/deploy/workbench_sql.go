package deploy

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/xwb1989/sqlparser"
)

// Interactive reads deliberately do not share the stricter data-sync grammar.
// Only one local table, bounded results, a read-only transaction and a small
// pure aggregate allowlist are accepted. No arbitrary functions or subqueries.
type workbenchSQL struct {
	SQL, Table, Metadata string
	Limit                int
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
	if len(tokens) == 2 && (tokens[0] == "describe" || tokens[0] == "desc") && sqlIdentifierRE.MatchString(tokens[1]) {
		return &workbenchSQL{Metadata: "schema", Table: tokens[1], Limit: 1000}, nil
	}
	stmt, err := sqlparser.Parse(input)
	if err != nil {
		return nil, fmt.Errorf("查询 SQL 无法解析，请检查语法；一次仅执行一条语句")
	}
	q, ok := stmt.(*sqlparser.Select)
	if !ok || q.Lock != "" || len(q.Comments) > 0 || len(q.SelectExprs) > 50 {
		return nil, fmt.Errorf("查询仅支持 SELECT、SHOW TABLES、DESCRIBE 表名；写操作请提交变更")
	}
	table, err := singleSQLTable(q.From)
	if err != nil {
		return nil, err
	}
	if !sqlIdentifierRE.MatchString(table) {
		return nil, fmt.Errorf("表名无效")
	}
	err = sqlparser.Walk(func(n sqlparser.SQLNode) (bool, error) {
		if _, ok := n.(sqlparser.Expr); ok {
			switch n.(type) {
			case *sqlparser.AndExpr, *sqlparser.OrExpr, *sqlparser.NotExpr, *sqlparser.ParenExpr,
				*sqlparser.ComparisonExpr, *sqlparser.RangeCond, *sqlparser.IsExpr,
				*sqlparser.SQLVal, *sqlparser.NullVal, sqlparser.BoolVal, *sqlparser.ColName,
				sqlparser.ValTuple, *sqlparser.BinaryExpr, *sqlparser.UnaryExpr, *sqlparser.FuncExpr:
			default:
				return false, fmt.Errorf("不支持此查询表达式，请使用字段、常量、条件或支持的聚合函数")
			}
		}
		switch v := n.(type) {
		case sqlparser.Nextval, *sqlparser.Nextval:
			return false, fmt.Errorf("查询不能生成或修改序列")
		case *sqlparser.Subquery:
			return false, fmt.Errorf("暂不支持子查询")
		case *sqlparser.ColName:
			if !v.Qualifier.IsEmpty() || !sqlIdentifierRE.MatchString(v.Name.String()) {
				return false, fmt.Errorf("字段不能带跨库限定或变量")
			}
		case *sqlparser.FuncExpr:
			name := strings.ToLower(v.Name.String())
			if !v.Qualifier.IsEmpty() || (name != "count" && name != "min" && name != "max" && name != "sum" && name != "avg") || len(v.Exprs) != 1 {
				return false, fmt.Errorf("函数仅支持 COUNT、MIN、MAX、SUM、AVG")
			}
			switch a := v.Exprs[0].(type) {
			case *sqlparser.StarExpr:
				if name != "count" {
					return false, fmt.Errorf("只有 COUNT 支持 *")
				}
			case *sqlparser.AliasedExpr:
				if _, ok := a.Expr.(*sqlparser.ColName); !ok {
					return false, fmt.Errorf("聚合参数必须是字段")
				}
			default:
				return false, fmt.Errorf("聚合参数无效")
			}
		case *sqlparser.SQLVal:
			if v.Type == sqlparser.ValArg {
				return false, fmt.Errorf("不支持未绑定参数")
			}
		case *sqlparser.StarExpr:
			if !v.TableName.IsEmpty() {
				return false, fmt.Errorf("不支持限定 *")
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
	return &workbenchSQL{SQL: sqlparser.String(q), Table: table, Limit: limit}, nil
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
	query, args := p.SQL, []any{}
	switch p.Metadata {
	case "tables":
		query = "SELECT TABLE_NAME AS table_name FROM information_schema.TABLES WHERE TABLE_SCHEMA=? AND TABLE_TYPE='BASE TABLE' ORDER BY TABLE_NAME LIMIT 1000"
		args = []any{c.Config["database"]}
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
	return map[string]any{"columns": columns, "rows": out, "elapsed_ms": time.Since(started).Milliseconds(), "truncated": truncated, "row_limit": p.Limit, "limit_reached": len(out) >= p.Limit, "target": map[string]any{"connection": c.Name, "database": c.Config["database"]}, "metadata": p.Metadata}, nil
}
