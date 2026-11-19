package deploy

import "strings"

// SQL and live evaluation use the same three-valued, ordered policy semantics.
// All identifiers/paths are fixed here; rule values remain JSON data, never SQL.
func (s *Store) portablePolicySQL(table string) string {
	mysql := s.Driver() == "mysql"
	extract := func(doc, path string) string {
		v := "json_extract(" + doc + ",'" + path + "')"
		if mysql {
			return "JSON_UNQUOTE(" + v + ")"
		}
		return v
	}
	values := map[string]string{"kind": table + ".kind", "status": table + ".external_status", "title": table + ".title"}
	for key, path := range map[string]string{"product": "source_product_id", "project": "project_id", "resolution": "resolution", "severity": "severity", "priority": "priority"} {
		values[key] = "(SELECT " + extract("document", "$."+path) + " FROM research_issue_fields WHERE work_item_id=" + table + ".id)"
	}
	field := func(expr string) string {
		parts := []string{"CASE " + expr}
		for _, key := range []string{"kind", "status", "title", "product", "project", "resolution", "severity", "priority"} {
			parts = append(parts, "WHEN '"+key+"' THEN COALESCE("+values[key]+",'')")
		}
		return strings.Join(append(parts, "ELSE '' END"), " ")
	}
	rulesFrom := "json_each(rr.document,'$.policy.rules') pr"
	ruleDoc := "pr.value"
	order := "CAST(pr.key AS INTEGER)"
	if mysql {
		rulesFrom = "JSON_TABLE(rr.document,'$.policy.rules[*]' COLUMNS(pos FOR ORDINALITY,doc JSON PATH '$')) pr"
		ruleDoc = "pr.doc"
		order = "pr.pos"
	}
	conditionsFrom := "json_each(" + ruleDoc + ",'$.conditions') pc"
	condDoc := "pc.value"
	if mysql {
		conditionsFrom = "JSON_TABLE(" + ruleDoc + ",'$.conditions[*]' COLUMNS(doc JSON PATH '$')) pc"
		condDoc = "pc.doc"
	}
	v := field(extract(condDoc, "$.field"))
	candidate := extract(condDoc, "$.values[0]")
	list := "SELECT pv.value FROM json_each(" + condDoc + ",'$.values') pv"
	compare := v
	if mysql {
		list = "SELECT BINARY pv.v FROM JSON_TABLE(" + condDoc + ",'$.values[*]' COLUMNS(v VARCHAR(256) PATH '$')) pv"
		compare = "BINARY (" + v + ")"
	}
	cond := "CASE WHEN (" + v + ")='' THEN NULL WHEN " + extract(condDoc, "$.operator") + "='in' THEN (" + compare + ") IN (" + list + ") WHEN " + extract(condDoc, "$.operator") + "='not_in' THEN (" + compare + ") NOT IN (" + list + ") WHEN " + extract(condDoc, "$.operator") + "='contains' THEN INSTR(" + compare + "," + candidate + ")>0 ELSE NULL END"
	exists := func(test string) string {
		return "EXISTS(SELECT 1 FROM " + conditionsFrom + " WHERE (" + cond + ") " + test + ")"
	}
	state := "CASE WHEN " + extract(ruleDoc, "$.match") + "='all' THEN CASE WHEN " + exists("=0") + " THEN 0 WHEN " + exists("IS NULL") + " THEN NULL ELSE 1 END ELSE CASE WHEN " + exists("=1") + " THEN 1 WHEN " + exists("IS NULL") + " THEN NULL ELSE 0 END END"
	selected := "(SELECT CASE WHEN (" + state + ")=1 THEN " + extract(ruleDoc, "$.decision") + " ELSE 'unknown' END FROM " + rulesFrom + " WHERE (" + state + ")=1 OR (" + state + ") IS NULL ORDER BY " + order + " LIMIT 1)"
	requiredFrom := "json_each(rr.document,'$.policy.required_fields') rf"
	requiredField := "rf.value"
	if mysql {
		requiredFrom = "JSON_TABLE(rr.document,'$.policy.required_fields[*]' COLUMNS(v VARCHAR(32) PATH '$')) rf"
		requiredField = "rf.v"
	}
	missing := "EXISTS(SELECT 1 FROM " + requiredFrom + " WHERE (" + field(requiredField) + ")='')"
	return "CASE WHEN " + missing + " THEN NULL ELSE CASE COALESCE(" + selected + "," + extract("rr.document", "$.policy.default") + ") WHEN 'independent' THEN 1 WHEN 'not_independent' THEN 0 ELSE NULL END END"
}
