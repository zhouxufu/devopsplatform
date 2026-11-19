package deploy

import (
	"context"
	"fmt"
	"strings"

	"github.com/xwb1989/sqlparser"
)

// Store a structural signature, not a lossy string comparison. MySQL adds
// identifier quoting, charset introducers and redundant parentheses on readback.
func canonicalSQLGeneration(source string) (string, error) {
	return canonicalSQLGenerationRefs(source, nil)
}
func canonicalSQLGenerationRefs(source string, refs *[]string) (string, error) {
	ts, err := ddlLex(source)
	if err != nil {
		return "", err
	}
	cleaned := []ddlToken{}
	for i := 0; i < len(ts); i++ {
		if ddlWord(ts[i], "_utf8mb4") && i+1 < len(ts) && ts[i+1].literal {
			continue
		}
		if !ts[i].quoted && (ddlWord(ts[i], "select") || ddlWord(ts[i], "from")) {
			return "", fmt.Errorf("生成列不能包含查询")
		}
		cleaned = append(cleaned, ts[i])
	}
	stmt, err := sqlparser.Parse("select " + ddlText(cleaned))
	if err != nil {
		return "", fmt.Errorf("生成列表达式无法解析")
	}
	selectSQL, ok := stmt.(*sqlparser.Select)
	if !ok || len(selectSQL.SelectExprs) != 1 || selectSQL.Where != nil || len(selectSQL.GroupBy) > 0 || selectSQL.Having != nil || len(selectSQL.OrderBy) > 0 || selectSQL.Limit != nil || selectSQL.Lock != "" {
		return "", fmt.Errorf("生成列仅支持单行 CASE 表达式")
	}
	if len(selectSQL.From) != 1 || sqlparser.String(selectSQL.From) != "dual" {
		return "", fmt.Errorf("生成列不能引用其他表")
	}
	alias, ok := selectSQL.SelectExprs[0].(*sqlparser.AliasedExpr)
	if !ok || !alias.As.IsEmpty() {
		return "", fmt.Errorf("生成列表达式不能包含别名")
	}
	var signature func(sqlparser.Expr) (any, error)
	signature = func(expr sqlparser.Expr) (any, error) {
		switch x := expr.(type) {
		case *sqlparser.ParenExpr:
			return signature(x.Expr)
		case *sqlparser.ColName:
			if !x.Qualifier.IsEmpty() || !sqlIdentifierRE.MatchString(x.Name.String()) {
				return nil, fmt.Errorf("生成列只能引用本表字段")
			}
			if refs != nil {
				*refs = append(*refs, x.Name.String())
			}
			return []any{"column", strings.ToLower(x.Name.String())}, nil
		case *sqlparser.NullVal:
			return []any{"null"}, nil
		case *sqlparser.SQLVal:
			if x.Type != sqlparser.StrVal && x.Type != sqlparser.IntVal {
				return nil, fmt.Errorf("生成列仅支持整数或字符串字面量")
			}
			return []any{"literal", x.Type, string(x.Val)}, nil
		case sqlparser.ValTuple:
			values := []any{"tuple"}
			if len(x) == 0 || len(x) > 100 {
				return nil, fmt.Errorf("生成列 IN 列表超限")
			}
			for _, e := range x {
				switch e.(type) {
				case *sqlparser.SQLVal:
				default:
					return nil, fmt.Errorf("IN 仅支持字面量")
				}
				v, err := signature(e)
				if err != nil {
					return nil, err
				}
				values = append(values, v)
			}
			return values, nil
		case *sqlparser.ComparisonExpr:
			switch x.Operator {
			case "=", "!=", "<>", "<", ">", "<=", ">=", "in":
			default:
				return nil, fmt.Errorf("生成列不支持比较 %s", x.Operator)
			}
			a, e := signature(x.Left)
			if e != nil {
				return nil, e
			}
			b, e := signature(x.Right)
			if e != nil {
				return nil, e
			}
			return []any{"compare", x.Operator, a, b}, nil
		case *sqlparser.AndExpr:
			a, e := signature(x.Left)
			if e != nil {
				return nil, e
			}
			b, e := signature(x.Right)
			if e != nil {
				return nil, e
			}
			return []any{"and", a, b}, nil
		case *sqlparser.OrExpr:
			a, e := signature(x.Left)
			if e != nil {
				return nil, e
			}
			b, e := signature(x.Right)
			if e != nil {
				return nil, e
			}
			return []any{"or", a, b}, nil
		case *sqlparser.CaseExpr:
			if x.Expr != nil || len(x.Whens) == 0 || len(x.Whens) > 20 || x.Else == nil {
				return nil, fmt.Errorf("生成列须使用 CASE WHEN … THEN … ELSE … END")
			}
			values := []any{"case"}
			for _, when := range x.Whens {
				a, e := signature(when.Cond)
				if e != nil {
					return nil, e
				}
				b, e := signature(when.Val)
				if e != nil {
					return nil, e
				}
				values = append(values, []any{a, b})
			}
			v, e := signature(x.Else)
			if e != nil {
				return nil, e
			}
			return append(values, v), nil
		default:
			return nil, fmt.Errorf("生成列不支持函数、子查询、变量或其他表达式")
		}
	}
	// This bounded extension intentionally covers deterministic CASE-based keys.
	root := alias.Expr
	for {
		p, ok := root.(*sqlparser.ParenExpr)
		if !ok {
			break
		}
		root = p.Expr
	}
	if _, ok := root.(*sqlparser.CaseExpr); !ok {
		return "", fmt.Errorf("生成列目前仅支持确定性的 CASE WHEN 表达式")
	}
	value, err := signature(root)
	if err != nil {
		return "", err
	}
	return digestJSON(value), nil
}

// V1 remains byte-for-byte compatible with historical reviewed digests and
// data-sync snapshots. Only newly prepared SQL plans opt into enriched schema.
func readSQLCatalogVersion(ctx context.Context, db sqlQueryer, database string, version int) (sqlCatalog, error) {
	if version != 0 && version != 2 {
		return nil, fmt.Errorf("不支持 SQL 结构快照版本 %d", version)
	}
	catalog, err := readSQLCatalog(ctx, db, database)
	if err != nil || version == 0 {
		return catalog, err
	}
	rows, err := db.QueryContext(ctx, `SELECT TABLE_NAME,TABLE_COMMENT FROM information_schema.TABLES WHERE TABLE_SCHEMA=? AND TABLE_TYPE='BASE TABLE'`, database)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var name, comment string
		if err = rows.Scan(&name, &comment); err != nil {
			rows.Close()
			return nil, err
		}
		if t := catalog[name]; t != nil {
			t.Comment = comment
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = db.QueryContext(ctx, `SELECT TABLE_NAME,INDEX_NAME,INDEX_COMMENT FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=? AND SEQ_IN_INDEX=1`, database)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var name, index, comment string
		if err = rows.Scan(&name, &index, &comment); err != nil {
			rows.Close()
			return nil, err
		}
		if t := catalog[name]; t != nil {
			v := t.Indexes[index]
			v.Comment = comment
			t.Indexes[index] = v
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, table := range catalog {
		for name, column := range table.Columns {
			if column.Default != nil && strings.HasPrefix(strings.ToLower(*column.Default), "current_timestamp") && (strings.HasPrefix(column.Type, "datetime") || strings.HasPrefix(column.Type, "timestamp")) {
				value := strings.ToLower(*column.Default)
				if value == "current_timestamp" || value == "current_timestamp()" {
					value = "current_timestamp(0)"
				}
				column.Default = &value
			}
			if column.Generation != "" {
				canonical, e := canonicalSQLGeneration(sqlCatalogGenerationSource(column.Generation))
				if e == nil {
					column.Generation = canonical
				} else {
					column.Generation = "unsupported:" + column.Generation
				}
			}
			table.Columns[name] = column
		}
	}
	return catalog, nil
}
func validateSQLCollations(ctx context.Context, db sqlQueryer, ops []SQLStatement) error {
	names := map[string]bool{}
	for _, op := range ops {
		if op.definition != nil {
			if op.definition.Collation != "" {
				names[op.definition.Collation] = true
			}
			for _, c := range op.definition.Columns {
				if c.Collation != "" {
					names[c.Collation] = true
				}
			}
		}
		if op.column.Collation != "" {
			names[op.column.Collation] = true
		}
	}
	for name := range names {
		rows, err := db.QueryContext(ctx, `SELECT COLLATION_NAME FROM information_schema.COLLATIONS WHERE COLLATION_NAME=? AND CHARACTER_SET_NAME='utf8mb4'`, name)
		if err != nil {
			return err
		}
		found := rows.Next()
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("目标数据库不支持排序规则 %s", name)
		}
	}
	return nil
}

func validateDDLGenerationRefs(name string, column sqlColumn, table *sqlTable) error {
	for _, ref := range column.generationRefs {
		found := false
		for existing, target := range table.Columns {
			if strings.EqualFold(ref, existing) && !strings.EqualFold(ref, name) && target.Generation == "" && !target.AutoIncrement {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("生成列 %s 引用的 %s 必须为本表已声明的普通字段（非自增）", name, ref)
		}
	}
	return nil
}

// MySQL 8.4 information_schema escapes quotes around string literals one extra
// layer (SHOW CREATE does not). Decode only this metadata representation; the
// submitted SQL grammar continues rejecting backslash-dependent literals.
func sqlCatalogGenerationSource(source string) string {
	if !strings.Contains(source, `\'`) {
		return source
	}
	var out strings.Builder
	for i := 0; i < len(source); i++ {
		if source[i] == '\\' && i+1 < len(source) && (source[i+1] == '\'' || source[i+1] == '\\') {
			i++
			out.WriteByte(source[i])
			continue
		}
		out.WriteByte(source[i])
	}
	// A quoted apostrophe inside a literal is escaped by MySQL; normalize it
	// to the doubled quote form accepted by the submitted expression grammar.
	decoded := out.String()
	out.Reset()
	inString := false
	for i := 0; i < len(decoded); i++ {
		b := decoded[i]
		if inString && b == '\\' && i+1 < len(decoded) && decoded[i+1] == '\'' {
			out.WriteString("''")
			i++
			continue
		}
		if b == '\'' {
			inString = !inString
		}
		out.WriteByte(b)
	}
	return out.String()
}
