package deploy

import (
	"fmt"
	"strings"
)

// Personnel from the issue tracker is independent of Owner, the platform actor
// used for edit authorization. Never treat the synchronizing actor as assignee.
type WorkItemFilter struct {
	Query, Status, Kind, Person, PersonRole string
	Page, PageSize                          int
}

type WorkItemPage struct {
	Items    []WorkItem `json:"items"`
	Total    int        `json:"total"`
	Page     int        `json:"page"`
	PageSize int        `json:"page_size"`
}

func (f *WorkItemFilter) Validate() error {
	f.Query, f.Person = strings.TrimSpace(f.Query), strings.TrimSpace(f.Person)
	if len(f.Query) > 512 || len(f.Person) > 191 {
		return fmt.Errorf("搜索条件过长")
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
	if f.Status != "" && f.Status != "open" && f.Status != "closed" && f.Status != "active" && f.Status != "resolved" {
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
	if f.Status != "" {
		column := "status"
		if f.Status == "active" || f.Status == "resolved" {
			column = "external_status"
		}
		where, args = append(where, column+"=?"), append(args, f.Status)
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
	if f.Person != "" {
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
	rows, err := tx.Query(workItemSelect+suffix+" ORDER BY updated_at DESC,id DESC LIMIT ? OFFSET ?", append(args, f.PageSize, (out.Page-1)*f.PageSize)...)
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
