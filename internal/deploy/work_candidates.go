package deploy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

type WorkCandidate struct {
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
	SuccessfulDelivery bool   `json:"successful_delivery"`
}
type WorkCandidateFilter struct {
	WorkItemFilter
	Source, Environment, Delivery, Planning, Numbers string
	Start, End, VersionID                            int64
	IDs                                              []int64
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
	if f.Delivery != "" && f.Environment == "" {
		return out, fmt.Errorf("请指定上线环境")
	}
	out.Page = f.Page
	out.PageSize = f.PageSize
	if f.IDs != nil && len(f.IDs) == 0 {
		return out, nil
	}
	workJSON := `json_each(dr.plan,'$.version.work_items') wi`
	workID := `json_extract(wi.value,'$.id')`
	env := `json_extract(dr.plan,'$.environment.name')`
	scopeJSON := `json_each(vs.document,'$.work_item_ids') ws`
	scopeID := "ws.value"
	if s.Driver() == "mysql" {
		workJSON = `JSON_TABLE(dr.plan,'$.version.work_items[*]' COLUMNS (work_id BIGINT PATH '$.id')) wi`
		workID = "wi.work_id"
		env = `JSON_UNQUOTE(JSON_EXTRACT(dr.plan,'$.environment.name'))`
		scopeJSON = `JSON_TABLE(vs.document,'$.work_item_ids[*]' COLUMNS (work_id BIGINT PATH '$')) ws`
		scopeID = "ws.work_id"
	}
	// Environment is bound even when not filtering; an empty environment never
	// invents a production success signal.
	success := `EXISTS(SELECT 1 FROM delivery_runs dr JOIN ` + workJSON + ` ON ` + workID + `=w.id WHERE dr.status='success' AND ` + env + `=?)`
	planned := `(SELECT COUNT(*) FROM release_versions rv JOIN release_version_scope vs ON vs.release_version_id=rv.id JOIN ` + scopeJSON + ` ON ` + scopeID + `=w.id WHERE rv.status IN ('draft','ready') AND rv.id<>?)`
	changeCount := `(SELECT COUNT(*) FROM work_item_changes wc JOIN changes c ON c.id=wc.change_id WHERE wc.work_item_id=w.id)`
	readyCount := `(SELECT COUNT(*) FROM work_item_changes wc JOIN changes c ON c.id=wc.change_id WHERE wc.work_item_id=w.id AND c.status='ready')`
	where, args := []string{"1=1"}, []any{}
	if f.Status != "" {
		column := "w.status"
		if f.Status == "active" || f.Status == "resolved" {
			column = "w.external_status"
		}
		where = append(where, column+"=?")
		args = append(args, f.Status)
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
	if f.Person != "" {
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
		args = append(args, f.Environment)
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
	fields := `SELECT w.id,w.source,COALESCE(w.external_id,''),w.kind,w.title,w.status,COALESCE(w.external_status,''),COALESCE(w.owner,''),COALESCE(w.assigned_to,''),COALESCE(w.resolved_by,''),w.updated_at,` + changeCount + "," + readyCount + "," + planned + "," + success
	params := append([]any{f.VersionID, f.Environment}, args...)
	params = append(params, limit, offset)
	rows, e := tx.Query(fields+suffix+" ORDER BY w.updated_at DESC,w.id DESC LIMIT ? OFFSET ?", params...)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var v WorkCandidate
		var recorded int
		if e = rows.Scan(&v.ID, &v.Source, &v.ExternalID, &v.Kind, &v.Title, &v.Status, &v.ExternalStatus, &v.Owner, &v.AssignedTo, &v.ResolvedBy, &v.UpdatedAt, &v.ChangeCount, &v.ReadyCount, &v.PlannedCount, &recorded); e != nil {
			rows.Close()
			return out, e
		}
		v.SuccessfulDelivery = recorded != 0
		out.Items = append(out.Items, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}

func registerWorkCandidateRoutes(mux *http.ServeMux, s *Store) {
	read := func(selected bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			f := WorkCandidateFilter{WorkItemFilter: WorkItemFilter{Query: q.Get("q"), Kind: q.Get("kind"), Status: q.Get("status"), Person: q.Get("person"), PersonRole: q.Get("person_role")}, Source: q.Get("source"), Environment: q.Get("environment"), Delivery: q.Get("delivery"), Planning: q.Get("planning"), Numbers: q.Get("numbers")}
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
			ids = append(ids, "c.id IN (SELECT change_id FROM work_item_changes WHERE work_item_id IN ("+placeholders(len(scope.WorkItemIDs))+"))")
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
