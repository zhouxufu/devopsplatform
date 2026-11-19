package deploy

import (
	"reflect"
	"strings"
	"testing"
)

func TestWorkbenchCustomSQLReadExpressions(t *testing.T) {
	for _, input := range []string{
		"SELECT id,IFNULL(name,'unknown') AS label FROM menu WHERE name LIKE '%QA%' ORDER BY id DESC LIMIT 50",
		"SELECT kind,COUNT(DISTINCT id) AS total FROM menu WHERE id IN (1,2,3) GROUP BY kind HAVING COUNT(id)>1 ORDER BY total DESC",
		"SELECT CASE WHEN id>1 THEN 'active' ELSE 'other' END AS state,COALESCE(name,'') AS label FROM menu",
		"SELECT CONCAT(UPPER(name),LOWER(kind)),ROUND(ABS(id),2),DATE_FORMAT(created_at,'%Y-%m-%d') FROM menu",
		"SELECT SUBSTR(name,1,2),CAST(id AS CHAR),CONVERT(0x433a5c74656d70 USING utf8mb4) FROM menu",
		"SELECT id FROM menu WHERE created_at >= DATE_SUB(NOW(), INTERVAL 7 DAY)",
		"SELECT id FROM menu WHERE (id>1 AND name!='') OR name IS NULL LIMIT 100 OFFSET 100",
	} {
		if _, err := parseWorkbenchSQL(input); err != nil {
			t.Error(input, err)
		}
	}
	for _, input := range []string{
		"SELECT IFNULL(SLEEP(1),'') FROM menu", "SELECT CONCAT(LOAD_FILE('/etc/passwd'),name) FROM menu",
		"SELECT CASE WHEN id>0 THEN GET_LOCK('x',30) END FROM menu",
		"SELECT (SELECT name FROM secrets) FROM menu", "SELECT otherdb.name FROM menu",
		"SELECT id FROM menu WHERE name=?", "SELECT id FROM menu; DELETE FROM menu",
		"SELECT CONVERT(name USING unknown_charset) FROM menu", "SELECT schema_name.custom_fn(name) FROM menu",
	} {
		if _, err := parseWorkbenchSQL(input); err == nil {
			t.Error("unsafe custom query accepted", input)
		}
	}
}

func TestWorkbenchStringLiteralsBecomeBoundArguments(t *testing.T) {
	p, err := parseWorkbenchSQL("SELECT id FROM menu WHERE name='O''Neil; DELETE FROM menu' AND tag='x'")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Args, []any{"O'Neil; DELETE FROM menu", "x"}) || strings.Count(p.SQL, "?") != 2 || strings.Contains(p.SQL, "Neil") || strings.Contains(p.SQL, "DELETE") {
		t.Fatal("literal leaked into SQL text", p)
	}
}
