package deploy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

type WorkCandidate struct {
	ResearchIssueFields
	ReleaseRequired    *bool  `json:"release_required"`
	ID                 int64  `json:"id"`
	Source             string `json:"source"`
	ExternalID         string `json:"external_id"`
	Kind               string `json:"kind"`
	Title              string `json:"title"`
	Status             string `json:"status"`
	ExternalStatus     string `json:"external_status"`
	Owner              string `json:"owner"`
	AssignedTo         string `json:"assigned_to"`
	ResolvedBy         string `json:"resolved_by"`
	UpdatedAt          int64  `json:"updated_at"`
	ChangeCount        int    `json:"change_count"`
	ReadyCount         int    `json:"ready_count"`
	PlannedCount       int    `json:"planned_count"`
	PartialDelivery    bool   `json:"partial_delivery"`
	SuccessfulDelivery bool   `json:"successful_delivery"`
}
type WorkCandidateFilter struct {
	WorkItemFilter
	Source, Environment, Delivery, Planning, Numbers string
	Start, End, VersionID                            int64
	IDs                                              []int64
	ProductionOnly                                   bool
}
type WorkCandidatePage struct {
	Items    []WorkCandidate `json:"items"`
	Total    int             `json:"total"`
	Page     int             `json:"page"`
	PageSize int             `json:"page_size"`
}

func candidateIDs(raw string) ([]int64, error) {
	if raw == "" {
		return []int64{}, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > 500 {
		return nil, fmt.Errorf("最多选择 500 项")
	}
	ids := []int64{}
	seen := map[int64]bool{}
	for _, p := range parts {
		id, e := strconv.ParseInt(p, 10, 64)
		if e != nil || id <= 0 || seen[id] {
			return nil, fmt.Errorf("需求编号无效或重复")
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids, nil
}
func placeholders(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }

// Evidence uses the immutable, explicitly selected work-item snapshot of a
// successful delivery, NOT the current version scope or tracker closed status.
func (s *Store) WorkCandidates(f WorkCandidateFilter) (WorkCandidatePage, error) {
	out := WorkCandidatePage{Items: []WorkCandidate{}}
	if e := f.WorkItemFilter.Validate(); e != nil {
		return out, e
	}
	if len(f.Source) > 191 || f.VersionID < 0 || f.Start < 0 || f.End < 0 || (f.End > 0 && f.Start > f.End) || len(f.IDs) > 500 {
		return out, fmt.Errorf("候选筛选参数无效")
	}
	if f.Delivery != "" && f.Delivery != "recorded" && f.Delivery != "not_recorded" {
		return out, fmt.Errorf("上线记录筛选无效")
	}
	if f.Planning != "" && f.Planning != "planned" && f.Planning != "unplanned" {
		return out, fmt.Errorf("排期筛选无效")
	}
	numbers := strings.FieldsFunc(f.Numbers, func(r rune) bool {
		return r == ',' || r == '，' || r == ';' || r == '；' || r == ' ' || r == '\n' || r == '\r' || r == '\t'
	})
	if len(numbers) > 200 || len(f.Numbers) > 20000 || (len(numbers) > 0 && (f.Source == "" || f.Source == "manual")) {
		return out, fmt.Errorf("批量外部编号最多 200 项，请先选择来源平台")
	}
	for _, number := range numbers {
		if len(number) > 191 {
			return out, fmt.Errorf("外部编号过长")
		}
	}
	if f.Environment != "" && s.GetEnvironment(f.Environment) == nil {
		return out, fmt.Errorf("上线环境不存在")
	}
	if f.Delivery != "" && f.Environment == "" && !f.ProductionOnly {
		return out, fmt.Errorf("请指定上线环境")
	}
	out.Page = f.Page
	out.PageSize = f.PageSize
	if f.IDs != nil && len(f.IDs) == 0 {
		return out, nil
	}
	workJSON := `json_each(COALESCE(json_extract(dr.plan,'$.scope.work_items'),json_extract(dr.plan,'$.version.work_items'),'[]')) wi`
	changesJSON := `json_each(COALESCE(json_extract(dr.plan,'$.scope.changes'),json_extract(dr.plan,'$.version.changes'),'[]')) dc`
	changeID := `json_extract(dc.value,'$.id')`
	workID := `json_extract(wi.value,'$.id')`
	env := `json_extract(dr.plan,'$.environment.name')`
	workbench := `COALESCE(json_extract(dr.plan,'$.template.workbench'),'')`
	scopeJSON := `json_each(vs.document,'$.work_item_ids') ws`
	scopeID := "ws.value"
	if s.Driver() == "mysql" {
		workJSON = `JSON_TABLE(COALESCE(JSON_EXTRACT(dr.plan,'$.scope.work_items'),JSON_EXTRACT(dr.plan,'$.version.work_items'),JSON_ARRAY()),'$[*]' COLUMNS (work_id BIGINT PATH '$.id')) wi`
		changesJSON = `JSON_TABLE(COALESCE(JSON_EXTRACT(dr.plan,'$.scope.changes'),JSON_EXTRACT(dr.plan,'$.version.changes'),JSON_ARRAY()),'$[*]' COLUMNS (change_id BIGINT PATH '$.id')) dc`
		changeID = "dc.change_id"
		workID = "wi.work_id"
		env = `JSON_UNQUOTE(JSON_EXTRACT(dr.plan,'$.environment.name'))`
		workbench = `COALESCE(JSON_UNQUOTE(JSON_EXTRACT(dr.plan,'$.template.workbench')),'')`
		// MySQL can evaluate a nested lateral JSON_TABLE before its correlated
		// delivery row is available (notably the single-item partial-proof path).
		// Guard the input, not just the extracted value: COALESCE outside
		// JSON_EXTRACT is too late to prevent Error 3141. Valid immutable plans
		// retain their exact interpretation; absent inputs cannot prove delivery.
		for _, expression := range []*string{&workJSON, &changesJSON, &env, &workbench} {
			*expression = strings.ReplaceAll(*expression, "dr.plan", mysqlDeliveryEvidenceDocument("dr.plan"))
		}
		scopeJSON = `JSON_TABLE(vs.document,'$.work_item_ids[*]' COLUMNS (work_id BIGINT PATH '$')) ws`
		scopeID = "ws.work_id"
	}
	// Environment is bound even when not filtering; an empty environment never
	// invents a production success signal.
	owned := ` AND wc.change_id IN (` + deliveryOwnedChanges("w.id") + `)`
	coverage := `NOT EXISTS(SELECT 1 FROM work_item_changes wc WHERE wc.work_item_id=w.id` + owned + ` AND NOT EXISTS(SELECT 1 FROM ` + changesJSON + ` WHERE ` + changeID + `=wc.change_id))`
	envClause, envArgs := env+`=?`, []any{f.Environment}
	if f.ProductionOnly {
		names := []any{}
		for _, e := range s.Environments() {
			if e.Purpose == "production" {
				names = append(names, e.Name)
			}
		}
		if len(names) == 0 {
			return out, fmt.Errorf("尚未配置生产用途的环境，请管理员在环境管理中指定用途")
		}
		envClause, envArgs = env+` IN (`+placeholders(len(names))+`)`, names
	}
	baseRecord := `SELECT 1 FROM delivery_runs dr JOIN ` + workJSON + ` ON ` + workID + `=w.id WHERE dr.status='success' AND ` + workbench + `<>'research' AND ` + envClause
	success := `EXISTS(` + baseRecord + ` AND ` + coverage + `)`
	partial := `EXISTS(` + baseRecord + ` AND NOT ` + coverage + ` AND EXISTS(SELECT 1 FROM work_item_changes wc JOIN ` + changesJSON + ` ON ` + changeID + `=wc.change_id WHERE wc.work_item_id=w.id))`
	planned := `(SELECT COUNT(*) FROM release_versions rv JOIN release_version_scope vs ON vs.release_version_id=rv.id JOIN ` + scopeJSON + ` ON ` + scopeID + `=w.id WHERE rv.status IN ('draft','ready') AND rv.id<>?)`
	changeCount := `(SELECT COUNT(*) FROM work_item_changes wc JOIN changes c ON c.id=wc.change_id WHERE wc.work_item_id=w.id` + owned + `)`
	readyCount := `(SELECT COUNT(*) FROM work_item_changes wc JOIN changes c ON c.id=wc.change_id WHERE wc.work_item_id=w.id` + owned + ` AND c.status='ready')`
	where, args := []string{"1=1"}, []any{}
	if f.Relation != "" {
		where = append(where, researchRelationClause("w", f.Relation))
		args = append(args, f.RelationActor)
	}
	fieldExpr := func(field string) string {
		expr := `json_extract(document,'$.` + field + `')`
		if s.Driver() == "mysql" {
			expr = "JSON_UNQUOTE(" + expr + ")"
		}
		return `COALESCE((SELECT ` + expr + ` FROM research_issue_fields WHERE work_item_id=w.id),'')`
	}
	for _, filter := range []struct{ field, value string }{{"severity", f.Severity}, {"priority", f.Priority}, {"resolution", f.Resolution}} {
		if filter.value != "" {
			where = append(where, fieldExpr(filter.field)+"=?")
			args = append(args, filter.value)
		}
	}
	order := "w.updated_at DESC,w.id DESC"
	switch f.Order {
	case "updated_asc":
		order = "w.updated_at ASC,w.id ASC"
	case "severity_asc", "severity_desc", "priority_asc", "priority_desc", "resolution_asc", "resolution_desc":
		parts := strings.Split(f.Order, "_")
		direction := "ASC"
		if parts[1] == "desc" {
			direction = "DESC"
		}
		expr := fieldExpr(parts[0])
		order = "CASE WHEN " + expr + "='' THEN 1 ELSE 0 END," + expr + " " + direction + ",w.id DESC"
	}
	if f.ReleaseRequired != "" {
		where = append(where, s.researchIntentFilter("w", f.ReleaseRequired))
	}
	if f.Status != "" {
		if f.Status == WorkItemActive || f.Status == WorkItemResolved {
			where = append(where, `((w.source='manual' AND w.status=?) OR (w.source<>'manual' AND w.external_status=?))`)
			args = append(args, f.Status, f.Status)
		} else {
			where = append(where, "w.status=?")
			args = append(args, f.Status)
		}
	}
	if f.Kind != "" {
		where = append(where, "w.kind=?")
		args = append(args, f.Kind)
	}
	if f.Source != "" {
		where = append(where, "w.source=?")
		args = append(args, f.Source)
	}
	if len(numbers) > 0 {
		where = append(where, "w.external_id IN ("+placeholders(len(numbers))+")")
		for _, number := range numbers {
			args = append(args, number)
		}
	}
	if f.Query != "" {
		term := "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(f.Query) + "%"
		where = append(where, `(w.title LIKE ? ESCAPE '!' OR w.external_id LIKE ? ESCAPE '!' OR w.id=?)`)
		id, _ := strconv.ParseInt(strings.TrimPrefix(f.Query, "#"), 10, 64)
		args = append(args, term, term, id)
	}
	if len(f.PersonAccounts) > 0 {
		clause, values := workMineClause("w", f.WorkItemFilter)
		where, args = append(where, clause), append(args, values...)
	} else if f.Person != "" {
		switch f.PersonRole {
		case "", "related":
			where = append(where, `(w.assigned_to=? OR w.resolved_by=? OR w.opened_by=? OR w.closed_by=? OR (w.source='manual' AND w.owner=?))`)
			for i := 0; i < 5; i++ {
				args = append(args, f.Person)
			}
		case "owner":
			where = append(where, `w.source='manual' AND w.owner=?`)
			args = append(args, f.Person)
		default:
			where = append(where, "w."+f.PersonRole+"=?")
			args = append(args, f.Person)
		}
	}
	if f.TimeField == "closed" && (f.Start > 0 || f.End > 0) {
		return out, fmt.Errorf("关闭时间请使用 source_start / source_end 日期，不使用平台时间戳")
	}
	if f.TimeField == "closed" && (f.SourceStart != "" || f.SourceEnd != "") {
		expr := fieldExpr("source_closed_at")
		where = append(where, "w.kind='bug'", expr+">='0001-01-01 00:00:00'")
		if f.SourceStart != "" {
			where = append(where, expr+">=?")
			args = append(args, f.SourceStart+" 00:00:00")
		}
		if f.SourceEnd != "" {
			where = append(where, expr+"<=?")
			args = append(args, f.SourceEnd+" 23:59:59")
		}
	}
	if f.Start > 0 {
		where = append(where, "w.updated_at>=?")
		args = append(args, f.Start)
	}
	if f.End > 0 {
		where = append(where, "w.updated_at<=?")
		args = append(args, f.End)
	}
	if f.Delivery != "" {
		clause := success
		if f.Delivery == "not_recorded" {
			clause = "NOT " + success
		}
		where = append(where, clause)
		args = append(args, envArgs...)
	}
	if f.Planning != "" {
		clause := planned + ">0"
		if f.Planning == "unplanned" {
			clause = planned + "=0"
		}
		where = append(where, clause)
		args = append(args, f.VersionID)
	}
	if f.IDs != nil {
		where = append(where, "w.id IN ("+placeholders(len(f.IDs))+")")
		for _, id := range f.IDs {
			if id <= 0 {
				return out, fmt.Errorf("需求编号无效")
			}
			args = append(args, id)
		}
	}
	suffix := " FROM work_items w WHERE " + strings.Join(where, " AND ")
	tx, e := s.db.Begin()
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	if e = tx.QueryRow("SELECT COUNT(*)"+suffix, args...).Scan(&out.Total); e != nil {
		return out, e
	}
	out.Page = min(f.Page, max(1, (out.Total+f.PageSize-1)/f.PageSize))
	limit := f.PageSize
	offset := (out.Page - 1) * f.PageSize
	if f.IDs != nil {
		limit = len(f.IDs)
		offset = 0
		out.Page = 1
		out.PageSize = limit
	}
	fields := `SELECT w.id,w.source,COALESCE(w.external_id,''),w.kind,w.title,w.status,COALESCE(w.external_status,''),COALESCE(w.owner,''),COALESCE(w.assigned_to,''),COALESCE(w.resolved_by,''),w.updated_at,` + changeCount + "," + readyCount + "," + planned + "," + success + "," + partial + "," + s.researchIntentExpr("w")
	fields += `,COALESCE((SELECT document FROM research_issue_fields WHERE work_item_id=w.id),'{}')`
	params := append([]any{f.VersionID}, envArgs...)
	params = append(params, envArgs...)
	params = append(params, args...)
	params = append(params, limit, offset)
	rows, e := tx.Query(fields+suffix+" ORDER BY "+order+" LIMIT ? OFFSET ?", params...)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var v WorkCandidate
		var recorded, partially int
		var sourceFields string
		if e = rows.Scan(&v.ID, &v.Source, &v.ExternalID, &v.Kind, &v.Title, &v.Status, &v.ExternalStatus, &v.Owner, &v.AssignedTo, &v.ResolvedBy, &v.UpdatedAt, &v.ChangeCount, &v.ReadyCount, &v.PlannedCount, &recorded, &partially, &v.ReleaseRequired, &sourceFields); e != nil {
			rows.Close()
			return out, e
		}
		if e = json.Unmarshal([]byte(sourceFields), &v.ResearchIssueFields); e != nil {
			rows.Close()
			return out, e
		}
		v.SuccessfulDelivery = recorded != 0
		v.PartialDelivery = partially != 0 && !v.SuccessfulDelivery
		out.Items = append(out.Items, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}

func mysqlDeliveryEvidenceDocument(column string) string {
	return "(CASE WHEN JSON_VALID(" + column + ") THEN " + column + " ELSE '{}' END)"
}

func registerWorkCandidateRoutes(mux *http.ServeMux, s *Store) {
	registerDeliveryWorkList(mux, s)
	read := func(selected bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			f := WorkCandidateFilter{WorkItemFilter: WorkItemFilter{Query: q.Get("q"), Kind: q.Get("kind"), Status: q.Get("status"), Person: q.Get("person"), PersonRole: q.Get("person_role"), ReleaseRequired: q.Get("release_required"), Severity: q.Get("severity"), Priority: q.Get("priority"), Resolution: q.Get("resolution"), Order: q.Get("order")}, Source: q.Get("source"), Environment: q.Get("environment"), Delivery: q.Get("delivery"), Planning: q.Get("planning"), Numbers: q.Get("numbers")}
			f.TimeField, f.SourceStart, f.SourceEnd = q.Get("time_field"), q.Get("source_start"), q.Get("source_end")
			if q.Get("mine") == "1" {
				if deliveryActor(r) == "local" {
					deliveryWriteError(w, 403, "请先登录后使用本人筛选")
					return
				}
				f.PersonAccounts = s.workActorAccounts(deliveryActor(r))
			}
			for key, dst := range map[string]*int{"page": &f.Page, "page_size": &f.PageSize} {
				if q.Get(key) != "" {
					v, e := strconv.Atoi(q.Get(key))
					if e != nil || v <= 0 {
						deliveryWriteError(w, 400, "分页参数无效")
						return
					}
					*dst = v
				}
			}
			for key, dst := range map[string]*int64{"start": &f.Start, "end": &f.End, "version_id": &f.VersionID} {
				if q.Get(key) != "" {
					v, e := strconv.ParseInt(q.Get(key), 10, 64)
					if e != nil {
						deliveryWriteError(w, 400, "时间或版本参数无效")
						return
					}
					*dst = v
				}
			}
			if selected {
				ids, e := candidateIDs(q.Get("ids"))
				if e != nil {
					deliveryWriteError(w, 400, e)
					return
				}
				f.IDs = ids
			}
			out, e := s.WorkCandidates(f)
			if e != nil {
				deliveryWriteError(w, 400, e)
				return
			}
			deliveryJSON(w, out)
		}
	}
	mux.HandleFunc("GET /api/work-items/candidates", read(false))
	mux.HandleFunc("GET /api/work-items/candidates/selected", read(true))
	mux.HandleFunc("GET /api/work-items/candidate-sources", func(w http.ResponseWriter, r *http.Request) {
		rows, e := s.db.Query("SELECT DISTINCT source FROM work_items ORDER BY source")
		if e != nil {
			deliveryWriteError(w, 500, "来源读取失败")
			return
		}
		defer rows.Close()
		out := []string{}
		for rows.Next() {
			var v string
			if rows.Scan(&v) != nil {
				deliveryWriteError(w, 500, "来源读取失败")
				return
			}
			out = append(out, v)
		}
		deliveryJSON(w, out)
	})
	// A pure metadata preview. Saving the version still derives the authoritative
	// full scope inside its own transaction, so preview is never execution.
	mux.HandleFunc("POST /api/release-versions/scope-preview", func(w http.ResponseWriter, r *http.Request) {
		var scope VersionScope
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&scope) != nil || len(scope.WorkItemIDs) > 500 || len(scope.ManualChangeIDs) > 500 {
			deliveryWriteError(w, 400, "范围参数无效")
			return
		}
		ids, args := []string{}, []any{}
		for _, list := range [][]int64{scope.WorkItemIDs, scope.ManualChangeIDs} {
			seen := map[int64]bool{}
			for _, id := range list {
				if id <= 0 || seen[id] {
					deliveryWriteError(w, 400, "范围编号无效或重复")
					return
				}
				seen[id] = true
			}
		}
		if len(scope.WorkItemIDs) > 0 {
			ids = append(ids, "c.id IN (SELECT wc.change_id FROM work_item_changes wc LEFT JOIN change_delivery_ownership co ON co.change_id=wc.change_id WHERE wc.work_item_id IN ("+placeholders(len(scope.WorkItemIDs))+") AND (co.change_id IS NULL OR (co.mode='draft' AND co.origin_work_item_id=wc.work_item_id) OR co.delivery_work_item_id=wc.work_item_id))")
			for _, id := range scope.WorkItemIDs {
				args = append(args, id)
			}
		}
		if len(scope.ManualChangeIDs) > 0 {
			ids = append(ids, "c.id IN ("+placeholders(len(scope.ManualChangeIDs))+")")
			for _, id := range scope.ManualChangeIDs {
				args = append(args, id)
			}
		}
		out := []map[string]any{}
		if len(ids) == 0 {
			deliveryJSON(w, out)
			return
		}
		rows, e := s.db.Query("SELECT c.id,c.kind,c.title,c.service,c.status FROM changes c WHERE "+strings.Join(ids, " OR ")+" ORDER BY c.id LIMIT 501", args...)
		if e != nil {
			deliveryWriteError(w, 500, "范围预览失败")
			return
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			var kind, title, service, status string
			if rows.Scan(&id, &kind, &title, &service, &status) != nil {
				deliveryWriteError(w, 500, "范围预览失败")
				return
			}
			out = append(out, map[string]any{"id": id, "kind": kind, "title": title, "service": service, "status": status})
		}
		if rows.Err() != nil || len(out) > 500 {
			deliveryWriteError(w, 400, "关联变更超过 500 项或预览失败，请缩小范围")
			return
		}
		deliveryJSON(w, out)
	})
}
