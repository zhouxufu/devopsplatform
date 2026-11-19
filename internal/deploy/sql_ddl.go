package deploy

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/xwb1989/sqlparser"
)

// An exhaustive DDL grammar around the existing expression parser. Every token
// must be consumed; unknown options cannot be discarded by a partial AST.
type ddlToken struct {
	raw, value string
	quoted     bool
	literal    bool
}

func ddlLex(source string) ([]ddlToken, error) {
	var out []ddlToken
	for i := 0; i < len(source); {
		if unicode.IsSpace(rune(source[i])) {
			i++
			continue
		}
		start := i
		b := source[i]
		if b == '\'' || b == '`' {
			i++
			var value strings.Builder
			closed := false
			for i < len(source) {
				if source[i] == b {
					if i+1 < len(source) && source[i+1] == b {
						value.WriteByte(b)
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				if source[i] == '\\' {
					return nil, fmt.Errorf("不支持反斜线转义")
				}
				value.WriteByte(source[i])
				i++
			}
			if !closed {
				return nil, fmt.Errorf("SQL 引号未闭合")
			}
			out = append(out, ddlToken{source[start:i], value.String(), true, b == '\''})
			continue
		}
		if b == '"' || b == '\\' {
			return nil, fmt.Errorf("SQL 字符串请使用单引号，标识符使用反引号")
		}
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' {
			i++
			for i < len(source) {
				c := source[i]
				if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
					break
				}
				i++
			}
		} else {
			i++
		}
		out = append(out, ddlToken{raw: source[start:i], value: source[start:i]})
	}
	return out, nil
}
func ddlText(tokens []ddlToken) string {
	parts := make([]string, len(tokens))
	for i, t := range tokens {
		parts[i] = t.raw
	}
	return strings.Join(parts, " ")
}
func ddlWord(t ddlToken, s string) bool { return !t.quoted && strings.EqualFold(t.value, s) }
func ddlIdent(t ddlToken) bool          { return !t.literal && sqlIdentifierRE.MatchString(t.value) }
func ddlString(v string) string         { return "'" + strings.ReplaceAll(v, "'", "''") + "'" }
func ddlGroup(ts []ddlToken, start int) (int, error) {
	if start >= len(ts) || !ddlWord(ts[start], "(") {
		return 0, fmt.Errorf("缺少括号")
	}
	depth := 0
	for i := start; i < len(ts); i++ {
		if ddlWord(ts[i], "(") {
			depth++
		}
		if ddlWord(ts[i], ")") {
			depth--
			if depth == 0 {
				return i, nil
			}
		}
	}
	return 0, fmt.Errorf("SQL 括号未闭合")
}
func ddlParts(ts []ddlToken) ([][]ddlToken, error) {
	var out [][]ddlToken
	start, depth := 0, 0
	for i, t := range ts {
		if ddlWord(t, "(") {
			depth++
		}
		if ddlWord(t, ")") {
			depth--
			if depth < 0 {
				return nil, fmt.Errorf("SQL 括号不匹配")
			}
		}
		if ddlWord(t, ",") && depth == 0 {
			if i == start {
				return nil, fmt.Errorf("空字段定义")
			}
			out = append(out, ts[start:i])
			start = i + 1
		}
	}
	if depth != 0 || start == len(ts) {
		return nil, fmt.Errorf("字段定义不完整")
	}
	return append(out, ts[start:]), nil
}

var ddlNumberRE = regexp.MustCompile(`^[+-]?[0-9]+(?:\.[0-9]+)?$`)

func ddlCollation(value string) bool {
	return strings.HasPrefix(value, "utf8mb4_") && sqlIdentifierRE.MatchString(value)
}

func parseDDLColumn(ts []ddlToken) (string, sqlColumn, bool, error) {
	fail := func(message string) (string, sqlColumn, bool, error) {
		return "", sqlColumn{}, false, fmt.Errorf("字段定义：%s", message)
	}
	if len(ts) < 2 || !ddlIdent(ts[0]) || ts[1].quoted {
		return fail("缺少字段名或类型")
	}
	name := ts[0].value
	i := 2
	if i < len(ts) && ddlWord(ts[i], "(") {
		end, e := ddlGroup(ts, i)
		if e != nil {
			return fail(e.Error())
		}
		i = end + 1
	}
	if i < len(ts) && ddlWord(ts[i], "unsigned") {
		i++
	}
	stmt, e := sqlparser.Parse("create table preview (`" + name + "` " + ddlText(ts[1:i]) + ")")
	if e != nil {
		return fail("类型无法解析")
	}
	ddl, ok := stmt.(*sqlparser.DDL)
	if !ok || ddl.TableSpec == nil || len(ddl.TableSpec.Columns) != 1 || !sameSQLTokens("create table preview ("+ddlText(ts[:i])+")", sqlparser.String(ddl)) {
		return fail("不支持该字段类型")
	}
	col, e := parseColumn(ddl.TableSpec.Columns[0])
	if e != nil {
		return fail(e.Error())
	}
	seen := map[string]bool{}
	primary := false
	for i < len(ts) {
		key := strings.ToLower(ts[i].value)
		if ts[i].quoted {
			return fail("未知选项 " + ts[i].raw)
		}
		i++
		if key == "not" {
			key = "null"
			if i >= len(ts) || !ddlWord(ts[i], "null") {
				return fail("NOT 后须为 NULL")
			}
			col.Nullable = false
			i++
		} else if key == "null" {
			col.Nullable = true
		}
		if seen[key] {
			return fail("重复选项 " + key)
		}
		seen[key] = true
		switch key {
		case "null":
		case "unsigned":
			return fail("UNSIGNED 必须紧随类型")
		case "default":
			if i >= len(ts) {
				return fail("缺少默认值")
			}
			token := ts[i]
			i++
			if ddlWord(token, "null") {
				col.Default = nil
				continue
			}
			value := token.value
			if ddlWord(token, "current_timestamp") {
				typ := strings.Split(col.Type, "(")[0]
				if typ != "timestamp" && typ != "datetime" {
					return fail("CURRENT_TIMESTAMP 仅适用于时间字段")
				}
				precision := 0
				if i < len(ts) && ddlWord(ts[i], "(") {
					end, err := ddlGroup(ts, i)
					if err != nil {
						return fail(err.Error())
					}
					if end != i+1 && end != i+2 {
						return fail("时间精度应为 0–6")
					}
					if end == i+2 {
						precision, err = strconv.Atoi(ts[i+1].value)
						if err != nil || ts[i+1].quoted || precision < 0 || precision > 6 {
							return fail("时间精度应为 0–6")
						}
					}
					i = end + 1
				}
				value = fmt.Sprintf("current_timestamp(%d)", precision)
				col.Extra = "DEFAULT_GENERATED"
			} else if !token.literal {
				if value == "-" || value == "+" {
					if i >= len(ts) {
						return fail("缺少数字默认值")
					}
					value += ts[i].value
					token = ts[i]
					i++
				}
				if token.quoted {
					return fail("默认值须为字面量")
				}
				if i+1 < len(ts) && ddlWord(ts[i], ".") {
					value += "." + ts[i+1].value
					i += 2
				}
				if !ddlNumberRE.MatchString(value) {
					return fail("默认值仅支持字面量或 CURRENT_TIMESTAMP")
				}
			}
			col.Default = &value
		case "auto_increment":
			col.AutoIncrement = true
		case "comment":
			if i >= len(ts) || !ts[i].literal {
				return fail("COMMENT 须为字符串")
			}
			col.Comment = ts[i].value
			i++
		case "collate":
			if i >= len(ts) || ts[i].literal || !ddlCollation(ts[i].value) {
				return fail("COLLATE 仅支持 utf8mb4 排序规则")
			}
			col.Collation = ts[i].value
			i++
		case "character":
			if i+1 >= len(ts) || !ddlWord(ts[i], "set") || !ddlWord(ts[i+1], "utf8mb4") {
				return fail("字符集仅支持 utf8mb4")
			}
			i += 2
		case "primary":
			if i >= len(ts) || !ddlWord(ts[i], "key") {
				return fail("PRIMARY 后须为 KEY")
			}
			primary = true
			i++
		case "generated":
			if i+1 >= len(ts) || !ddlWord(ts[i], "always") || !ddlWord(ts[i+1], "as") {
				return fail("生成列须使用 GENERATED ALWAYS AS")
			}
			i += 2
			end, err := ddlGroup(ts, i)
			if err != nil {
				return fail(err.Error())
			}
			value, err := canonicalSQLGenerationRefs(ddlText(ts[i+1:end]), &col.generationRefs)
			if err != nil {
				return fail(err.Error())
			}
			col.Generation = value
			i = end + 1
			col.Extra = "VIRTUAL GENERATED"
			if i < len(ts) && (ddlWord(ts[i], "stored") || ddlWord(ts[i], "virtual")) {
				col.Extra = strings.ToUpper(ts[i].value) + " GENERATED"
				i++
			}
		default:
			return fail("暂不支持 " + key)
		}
	}
	if col.Generation != "" && (seen["default"] || col.AutoIncrement || primary) {
		return fail("生成列不能组合 DEFAULT、AUTO_INCREMENT 或主键")
	}
	if primary {
		col.Nullable = false
	}
	if (col.Collation != "" || seen["character"]) && !ddlCharacterColumn(col.Type) {
		return fail("非字符字段不能指定 COLLATE")
	}
	return name, col, primary, nil
}
func ddlCharacterColumn(t string) bool {
	return strings.HasPrefix(t, "varchar") || strings.HasPrefix(t, "char") || t == "text" || t == "longtext"
}
func parseDDLIndex(ts []ddlToken) (string, sqlIndex, error) {
	index := sqlIndex{Columns: []string{}}
	i := 0
	name := ""
	bad := func() (string, sqlIndex, error) {
		return "", sqlIndex{}, fmt.Errorf("索引仅支持主键、普通/唯一字段索引和 COMMENT")
	}
	if len(ts) == 0 {
		return bad()
	}
	if ddlWord(ts[i], "primary") {
		name = "PRIMARY"
		index.Unique = true
		i++
		if i >= len(ts) || !ddlWord(ts[i], "key") {
			return bad()
		}
		i++
	} else {
		if ddlWord(ts[i], "unique") {
			index.Unique = true
			i++
		}
		if i >= len(ts) || !(ddlWord(ts[i], "key") || ddlWord(ts[i], "index")) {
			return bad()
		}
		i++
		if i >= len(ts) || !ddlIdent(ts[i]) {
			return bad()
		}
		name = ts[i].value
		i++
	}
	end, e := ddlGroup(ts, i)
	if e != nil {
		return bad()
	}
	parts, e := ddlParts(ts[i+1 : end])
	if e != nil {
		return bad()
	}
	for _, p := range parts {
		if len(p) != 1 || !ddlIdent(p[0]) {
			return bad()
		}
		index.Columns = append(index.Columns, p[0].value)
	}
	i = end + 1
	if i < len(ts) {
		if i+2 != len(ts) || !ddlWord(ts[i], "comment") || !ts[i+1].literal {
			return bad()
		}
		index.Comment = ts[i+1].value
		i += 2
	}
	if i != len(ts) {
		return bad()
	}
	return name, index, nil
}
func parseChangeDDL(piece string) (SQLStatement, bool, error) {
	ts, e := ddlLex(piece)
	if e != nil {
		return SQLStatement{}, false, e
	}
	fail := func(err error) (SQLStatement, bool, error) { return SQLStatement{}, true, err }
	if len(ts) < 2 {
		return SQLStatement{}, false, nil
	}
	create := ddlWord(ts[0], "create") && ddlWord(ts[1], "table")
	alter := ddlWord(ts[0], "alter") && ddlWord(ts[1], "table")
	index := ddlWord(ts[0], "create") && (ddlWord(ts[1], "index") || ddlWord(ts[1], "unique"))
	if !create && !alter && !index {
		return SQLStatement{}, false, nil
	}
	op := SQLStatement{SQL: piece}
	i := 2
	if create {
		if i+2 < len(ts) && ddlWord(ts[i], "if") && ddlWord(ts[i+1], "not") && ddlWord(ts[i+2], "exists") {
			i += 3
		}
		if i >= len(ts) || !ddlIdent(ts[i]) {
			return fail(fmt.Errorf("建表须指定当前库中的单表名称"))
		}
		op.Table = ts[i].value
		tableRaw := ts[i].raw
		i++
		end, err := ddlGroup(ts, i)
		if err != nil {
			return fail(err)
		}
		parts, err := ddlParts(ts[i+1 : end])
		if err != nil {
			return fail(err)
		}
		def := &sqlTable{Exists: true, Engine: "InnoDB", Columns: map[string]sqlColumn{}, Order: []string{}, Indexes: map[string]sqlIndex{}}
		for _, part := range parts {
			if ddlWord(part[0], "primary") || ddlWord(part[0], "key") || ddlWord(part[0], "index") || ddlWord(part[0], "unique") {
				name, value, err := parseDDLIndex(part)
				if err != nil {
					return fail(err)
				}
				if _, ok := def.Indexes[name]; ok {
					return fail(fmt.Errorf("重复索引 %s", name))
				}
				def.Indexes[name] = value
			} else {
				name, value, primary, err := parseDDLColumn(part)
				if err != nil {
					return fail(err)
				}
				if _, ok := def.Columns[name]; ok {
					return fail(fmt.Errorf("重复字段 %s", name))
				}
				def.Columns[name] = value
				def.Order = append(def.Order, name)
				if primary {
					if _, ok := def.Indexes["PRIMARY"]; ok {
						return fail(fmt.Errorf("重复主键"))
					}
					def.Indexes["PRIMARY"] = sqlIndex{Unique: true, Columns: []string{name}}
				}
			}
		}
		if len(def.Columns) == 0 {
			return fail(fmt.Errorf("建表语句缺少字段"))
		}
		for name, column := range def.Columns {
			if err := validateDDLGenerationRefs(name, column, def); err != nil {
				return fail(err)
			}
		}
		for name, idx := range def.Indexes {
			for _, column := range idx.Columns {
				c, ok := def.Columns[column]
				if !ok {
					return fail(fmt.Errorf("索引引用不存在的字段 %s", column))
				}
				if name == "PRIMARY" {
					c.Nullable = false
					def.Columns[column] = c
				}
			}
		}
		seen := map[string]bool{}
		options := ts[end+1:]
		for j := 0; j < len(options); {
			key := strings.ToLower(options[j].value)
			if options[j].quoted {
				return fail(fmt.Errorf("非法建表选项"))
			}
			j++
			if key == "default" {
				if j >= len(options) {
					return fail(fmt.Errorf("不完整 DEFAULT 表选项"))
				}
				key = strings.ToLower(options[j].value)
				if options[j].quoted || (key != "charset" && key != "character" && key != "collate") {
					return fail(fmt.Errorf("DEFAULT 仅适用于字符集或排序规则"))
				}
				j++
			}
			if key == "character" {
				if j >= len(options) || !ddlWord(options[j], "set") {
					return fail(fmt.Errorf("字符集选项不完整"))
				}
				key = "charset"
				j++
			}
			if seen[key] {
				return fail(fmt.Errorf("重复建表选项 %s", key))
			}
			seen[key] = true
			if j < len(options) && ddlWord(options[j], "=") {
				j++
			}
			if j >= len(options) {
				return fail(fmt.Errorf("缺少建表选项值"))
			}
			value := options[j]
			j++
			switch key {
			case "engine":
				if value.quoted || !strings.EqualFold(value.value, "innodb") {
					return fail(fmt.Errorf("仅支持 InnoDB 引擎"))
				}
			case "charset":
				if value.quoted || !strings.EqualFold(value.value, "utf8mb4") {
					return fail(fmt.Errorf("仅支持 utf8mb4 字符集"))
				}
			case "collate":
				if value.literal || !ddlCollation(value.value) {
					return fail(fmt.Errorf("仅支持 utf8mb4 排序规则"))
				}
				def.Collation = value.value
			case "comment":
				if !value.literal {
					return fail(fmt.Errorf("表 COMMENT 须为字符串"))
				}
				def.Comment = value.value
			default:
				return fail(fmt.Errorf("暂不支持建表选项 %s", key))
			}
		}
		for name, c := range def.Columns {
			def.Columns[name] = defaultColumnCollation(c, def.Collation)
		}
		op.Action = "create-table"
		op.definition = def
		op.SQL = "create table " + tableRaw + " " + ddlBodySource(piece) + " ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"
		if def.Collation != "" {
			op.SQL += " COLLATE=" + def.Collation
		}
		if seen["comment"] {
			op.SQL += " COMMENT=" + ddlString(def.Comment)
		}
		return op, true, nil
	}
	if alter {
		if len(ts) < 5 || !ddlIdent(ts[2]) || !ddlWord(ts[3], "add") {
			return fail(fmt.Errorf("ALTER TABLE 仅支持 ADD COLUMN 或 ADD INDEX/UNIQUE KEY"))
		}
		op.Table = ts[2].value
		i = 4
		if ddlWord(ts[i], "column") {
			i++
			if i >= len(ts) {
				return fail(fmt.Errorf("缺少字段定义"))
			}
		} else if ddlWord(ts[i], "key") || ddlWord(ts[i], "index") || ddlWord(ts[i], "unique") {
			op.indexName, op.index, e = parseDDLIndex(ts[i:])
			if e != nil {
				return fail(e)
			}
			op.Action = "create-index"
			return op, true, nil
		}
		var primary bool
		op.columnName, op.column, primary, e = parseDDLColumn(ts[i:])
		if e != nil {
			return fail(e)
		}
		if primary {
			return fail(fmt.Errorf("新增字段不能同时修改主键"))
		}
		op.Action = "add-column"
		return op, true, nil
	}
	// CREATE [UNIQUE] INDEX name ON table (columns) [COMMENT '...'].
	i = 1
	unique := false
	if ddlWord(ts[i], "unique") {
		unique = true
		i++
	}
	if i+3 >= len(ts) || !ddlWord(ts[i], "index") || !ddlIdent(ts[i+1]) || !ddlWord(ts[i+2], "on") || !ddlIdent(ts[i+3]) {
		return fail(fmt.Errorf("CREATE INDEX 格式不支持"))
	}
	op.Table = ts[i+3].value
	parts := []ddlToken{{raw: "key", value: "key"}, ts[i+1]}
	parts = append(parts, ts[i+4:]...)
	op.indexName, op.index, e = parseDDLIndex(parts)
	if e != nil {
		return fail(e)
	}
	op.index.Unique = unique
	op.Action = "create-index"
	return op, true, nil
}

// Keep the original body bytes so old frozen SQL statement/ledger digests remain
// stable. Grammar validation above still consumes every token independently.
func ddlBodySource(source string) string {
	start := strings.IndexByte(source, '(')
	depth := 0
	var quote byte
	for i := start; i < len(source); i++ {
		b := source[i]
		if quote != 0 {
			if b == quote {
				if i+1 < len(source) && source[i+1] == quote {
					i++
				} else {
					quote = 0
				}
			}
			continue
		}
		if b == '\'' || b == '`' {
			quote = b
			continue
		}
		if b == '(' {
			depth++
		}
		if b == ')' {
			depth--
			if depth == 0 {
				return source[start : i+1]
			}
		}
	}
	return ""
}
