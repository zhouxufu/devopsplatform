package deploy

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type auditQuery struct {
	Query, ResourceType, ResourceID string
	Start, End                      int64
	Page, PageSize                  int
}

type auditPage struct {
	Items         []AuditEvent `json:"items"`
	Total         int          `json:"total"`
	Page          int          `json:"page"`
	PageSize      int          `json:"page_size"`
	ResourceTypes []string     `json:"resource_types"`
}

func parseAuditQuery(r *http.Request) (auditQuery, error) {
	v := r.URL.Query()
	q := auditQuery{Query: strings.TrimSpace(v.Get("q")), ResourceType: v.Get("resource_type"), ResourceID: v.Get("resource_id"), Page: 1, PageSize: 20}
	for key, target := range map[string]*int{"page": &q.Page, "page_size": &q.PageSize} {
		if raw := v.Get(key); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > 1000000 {
				return q, fmt.Errorf("分页参数无效")
			}
			*target = n
		}
	}
	if q.PageSize > 100 {
		return q, fmt.Errorf("每页最多100条")
	}
	for key, target := range map[string]*int64{"start": &q.Start, "end": &q.End} {
		if raw := v.Get(key); raw != "" {
			t, err := time.Parse(time.RFC3339, raw)
			if err != nil || t.Unix() < 1 {
				return q, fmt.Errorf("时间格式无效")
			}
			*target = t.Unix()
		}
	}
	if q.Start > 0 && q.End > 0 && q.Start > q.End {
		return q, fmt.Errorf("开始时间不能晚于结束时间")
	}
	if len(q.Query) > 300 || len(q.ResourceType) > 100 || len(q.ResourceID) > 300 {
		return q, fmt.Errorf("筛选条件过长")
	}
	return q, nil
}

// Search public fields only. Details may contain sensitive values and are never
// included in list responses or searched. Keep the legacy array API unchanged.
func (s *Store) queryAudit(ctx context.Context, q auditQuery) (auditPage, error) {
	out := auditPage{Items: []AuditEvent{}, ResourceTypes: []string{}, Page: q.Page, PageSize: q.PageSize}
	where, args := []string{"1=1"}, []any{}
	if q.ResourceType != "" {
		where = append(where, "resource_type=?")
		args = append(args, q.ResourceType)
	}
	if q.ResourceID != "" {
		where = append(where, "resource_id=?")
		args = append(args, q.ResourceID)
	}
	if q.Start > 0 {
		where = append(where, "created_at>=?")
		args = append(args, q.Start)
	}
	if q.End > 0 {
		where = append(where, "created_at<=?")
		args = append(args, q.End)
	}
	if q.Query != "" {
		// ! is a portable explicit escape character for SQLite and MySQL.
		term := "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(strings.ToLower(q.Query)) + "%"
		where = append(where, "(LOWER(actor) LIKE ? ESCAPE '!' OR LOWER(action) LIKE ? ESCAPE '!' OR LOWER(resource_type) LIKE ? ESCAPE '!' OR LOWER(resource_id) LIKE ? ESCAPE '!')")
		args = append(args, term, term, term, term)
	}
	clause := strings.Join(where, " AND ")
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_events WHERE "+clause, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	pageArgs := append(append([]any{}, args...), q.PageSize, (q.Page-1)*q.PageSize)
	rows, err := s.db.QueryContext(ctx, "SELECT id,actor,action,resource_type,resource_id,created_at FROM audit_events WHERE "+clause+" ORDER BY id DESC LIMIT ? OFFSET ?", pageArgs...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var e AuditEvent
		if err = rows.Scan(&e.ID, &e.Actor, &e.Action, &e.ResourceType, &e.ResourceID, &e.CreatedAt); err != nil {
			rows.Close()
			return out, err
		}
		out.Items = append(out.Items, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	types, err := s.db.QueryContext(ctx, "SELECT DISTINCT resource_type FROM audit_events ORDER BY resource_type")
	if err != nil {
		return out, err
	}
	defer types.Close()
	for types.Next() {
		var kind string
		if err = types.Scan(&kind); err != nil {
			return out, err
		}
		out.ResourceTypes = append(out.ResourceTypes, kind)
	}
	return out, types.Err()
}

func registerAuditSearch(mux *http.ServeMux, st *Store, wj func(http.ResponseWriter, any), writeError func(http.ResponseWriter, int, any)) {
	mux.HandleFunc("GET /api/audit/page", func(w http.ResponseWriter, r *http.Request) {
		q, err := parseAuditQuery(r)
		if err != nil {
			writeError(w, 400, err)
			return
		}
		result, err := st.queryAudit(r.Context(), q)
		if err != nil {
			writeError(w, 500, "操作记录读取失败")
			return
		}
		wj(w, result)
	})
}
