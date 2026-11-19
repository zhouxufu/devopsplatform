package deploy

import (
	"fmt"
	"strings"
	"time"
)

// Personnel from the issue tracker is independent of Owner, the platform actor
// used for edit authorization. Never treat the synchronizing actor as assignee.
type WorkItemFilter struct {
	PersonAccounts                                           map[string]string
	SourceStart, SourceEnd                                   string
	TimeField                                                string
	Relation, RelationActor                                  string
	Source                                                   string
	Query, Status, Kind, Person, PersonRole, ReleaseRequired string
	Severity, Priority, Resolution, Order                    string
	Page, PageSize                                           int
}

type WorkItemPage struct {
	Items    []WorkItem `json:"items"`
	Total    int        `json:"total"`
	Page     int        `json:"page"`
	PageSize int        `json:"page_size"`
}

func (f *WorkItemFilter) Validate() error {
	if f.TimeField != "" && f.TimeField != "updated" && f.TimeField != "closed" {
		return fmt.Errorf("时间类型不支持")
	}
	for _, value := range []string{f.SourceStart, f.SourceEnd} {
		if value != "" {
			if _, err := time.Parse("2006-01-02", value); err != nil {
				return fmt.Errorf("来源日期格式无效")
			}
		}
	}
	if f.SourceStart != "" && f.SourceEnd != "" && f.SourceStart > f.SourceEnd {
		return fmt.Errorf("开始日期不能晚于结束日期")
	}
	if f.Relation != "" && (researchRelationClause("work_items", f.Relation) == "" || f.RelationActor == "" || f.RelationActor == "local") {
		return fmt.Errorf("请登录后使用参与或关注筛选")
	}
	if f.Source != "" && f.Source != "manual" {
		return fmt.Errorf("本地来源筛选仅支持自定义工作项")
	}
	f.Query, f.Person = strings.TrimSpace(f.Query), strings.TrimSpace(f.Person)
	if len(f.Query) > 512 || len(f.Person) > 191 {
		return fmt.Errorf("搜索条件过长")
	}
	for _, value := range []string{f.Severity, f.Priority, f.Resolution} {
		if len(value) > 128 {
			return fmt.Errorf("来源字段筛选过长")
		}
	}
	switch f.Order {
	case "", "updated_desc", "updated_asc", "severity_asc", "severity_desc", "priority_asc", "priority_desc", "resolution_asc", "resolution_desc":
	default:
		return fmt.Errorf("排序字段不支持")
	}
	if f.Page == 0 {
		f.Page = 1
	}
	if f.PageSize == 0 {
		f.PageSize = 20
	}
	if f.Page < 1 || f.Page > 1000000 || (f.PageSize != 20 && f.PageSize != 50 && f.PageSize != 100) {
		return fmt.Errorf("页码必须为正整数，每页条数仅支持 20、50、100")
	}
	if f.ReleaseRequired != "" && f.ReleaseRequired != "yes" && f.ReleaseRequired != "no" && f.ReleaseRequired != "unknown" && f.ReleaseRequired != "eligible" {
		return fmt.Errorf("上线意图筛选不支持")
	}
	if f.Status != "" && f.Status != "open" && f.Status != "closed" && f.Status != WorkItemActive && f.Status != WorkItemResolved {
		return fmt.Errorf("需求或 Bug 状态不支持")
	}
	if f.Kind != "" && f.Kind != "bug" && f.Kind != "requirement" {
		return fmt.Errorf("需求类型不支持")
	}
	switch f.PersonRole {
	case "", "related", "assigned_to", "opened_by", "resolved_by", "closed_by", "owner":
	default:
		return fmt.Errorf("人员关系不支持")
	}
	return nil
}

func (s *Store) WorkItemsPage(f WorkItemFilter) (WorkItemPage, error) {
	out := WorkItemPage{Items: []WorkItem{}}
	if err := f.Validate(); err != nil {
		return out, err
	}
	where, args := []string{}, []any{}
	if f.Relation != "" {
		where = append(where, researchRelationClause("work_items", f.Relation))
		args = append(args, f.RelationActor)
	}
	if f.Source != "" {
		where, args = append(where, "source=?"), append(args, f.Source)
	}
	fieldExpr := func(field string) string {
		expr := `json_extract(document,'$.` + field + `')`
		if s.db.dialect == "mysql" {
			expr = "JSON_UNQUOTE(" + expr + ")"
		}
		return `COALESCE((SELECT ` + expr + ` FROM research_issue_fields WHERE work_item_id=work_items.id),'')`
	}
	for _, filter := range []struct{ field, value string }{{"severity", f.Severity}, {"priority", f.Priority}, {"resolution", f.Resolution}} {
		if filter.value != "" {
			where = append(where, fieldExpr(filter.field)+"=?")
			args = append(args, filter.value)
		}
	}
	order := "updated_at DESC,id DESC"
	switch f.Order {
	case "updated_asc":
		order = "updated_at ASC,id ASC"
	case "severity_asc", "severity_desc", "priority_asc", "priority_desc", "resolution_asc", "resolution_desc":
		parts := strings.Split(f.Order, "_")
		direction := "ASC"
		if parts[1] == "desc" {
			direction = "DESC"
		}
		expr := fieldExpr(parts[0])
		order = "CASE WHEN " + expr + "='' THEN 1 ELSE 0 END," + expr + " " + direction + ",id DESC"
	}
	if f.ReleaseRequired != "" {
		where = append(where, s.researchIntentFilter("work_items", f.ReleaseRequired))
	}
	if f.Status != "" {
		if f.Status == WorkItemActive || f.Status == WorkItemResolved {
			where = append(where, `((source='manual' AND status=?) OR (source<>'manual' AND external_status=?))`)
			args = append(args, f.Status, f.Status)
		} else {
			where, args = append(where, "status=?"), append(args, f.Status)
		}
	}
	if f.Kind != "" {
		where, args = append(where, "kind=?"), append(args, f.Kind)
	}
	if f.Query != "" {
		// Literal keyword search, not LIKE wildcard or SQL syntax.
		like := "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(f.Query) + "%"
		where = append(where, `(title LIKE ? ESCAPE '!' OR external_id LIKE ? ESCAPE '!' OR description LIKE ? ESCAPE '!')`)
		args = append(args, like, like, like)
	}
	if len(f.PersonAccounts) > 0 {
		clause, values := workMineClause("", f)
		where, args = append(where, clause), append(args, values...)
	} else if f.Person != "" {
		switch f.PersonRole {
		case "", "related":
			where = append(where, `(assigned_to=? OR opened_by=? OR resolved_by=? OR closed_by=? OR (source='manual' AND owner=?))`)
			args = append(args, f.Person, f.Person, f.Person, f.Person, f.Person)
		case "owner":
			where, args = append(where, "(source='manual' AND owner=?)"), append(args, f.Person)
		default:
			// Validate whitelists the column; values always remain parameters.
			where, args = append(where, f.PersonRole+"=?"), append(args, f.Person)
		}
	}
	suffix := ""
	if len(where) > 0 {
		suffix = " WHERE " + strings.Join(where, " AND ")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = tx.QueryRow("SELECT COUNT(*) FROM work_items"+suffix, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	maxPage := max(1, (out.Total+f.PageSize-1)/f.PageSize)
	out.Page, out.PageSize = min(f.Page, maxPage), f.PageSize
	rows, err := tx.Query(workItemSelect+suffix+" ORDER BY "+order+" LIMIT ? OFFSET ?", append(args, f.PageSize, (out.Page-1)*f.PageSize)...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		v, err := scanWorkItem(rows)
		if err != nil {
			rows.Close()
			return out, err
		}
		out.Items = append(out.Items, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func (s *Store) WorkItemPeople() ([]string, error) {
	rows, err := s.db.Query(`SELECT person FROM (
		SELECT assigned_to AS person FROM work_items UNION SELECT opened_by FROM work_items
		UNION SELECT resolved_by FROM work_items UNION SELECT closed_by FROM work_items
		UNION SELECT owner FROM work_items WHERE source='manual'
	) WHERE person IS NOT NULL AND person<>'' AND person<>'closed' ORDER BY person`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	people := []string{}
	for rows.Next() {
		var person string
		if err := rows.Scan(&person); err != nil {
			return nil, err
		}
		people = append(people, person)
	}
	return people, rows.Err()
}
