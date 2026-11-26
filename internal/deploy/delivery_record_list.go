package deploy

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

type DeliveryLinkedWork struct {
	ID         int64  `json:"id"`
	Title      string `json:"title"`
	Kind       string `json:"kind"`
	ExternalID string `json:"external_id,omitempty"`
}

// List rows contain metadata only. SQL/configuration bodies belong to detail.
type DeliveryListItem struct {
	ID               int64                `json:"id"`
	Title            string               `json:"title"`
	Kind             string               `json:"kind,omitempty"`
	Service          string               `json:"service,omitempty"`
	Version          string               `json:"version,omitempty"`
	Status           string               `json:"status"`
	Owner            string               `json:"owner"`
	UpdatedAt        int64                `json:"updated_at"`
	CreatedAt        int64                `json:"created_at"`
	Frozen           bool                 `json:"frozen,omitempty"`
	MissingBranch    bool                 `json:"missing_branch,omitempty"`
	DataSync         bool                 `json:"data_sync,omitempty"`
	WorkItems        []DeliveryLinkedWork `json:"work_items"`
	ApplicationCount int                  `json:"application_count"`
	ChangeCount      int                  `json:"change_count"`
	WorkItemCount    int                  `json:"work_item_count"`
}
type DeliveryRecordFilter struct {
	Type, Query, Status, Kind, Service, Owner, Association string
	Page, PageSize                                         int
}
type DeliveryRecordPage struct {
	Items    []DeliveryListItem `json:"items"`
	Total    int                `json:"total"`
	Page     int                `json:"page"`
	PageSize int                `json:"page_size"`
}

func (f *DeliveryRecordFilter) Validate() error {
	if f.Type != "changes" && f.Type != "versions" {
		return fmt.Errorf("列表类型不支持")
	}
	f.Query, f.Owner, f.Service = strings.TrimSpace(f.Query), strings.TrimSpace(f.Owner), strings.TrimSpace(f.Service)
	if len(f.Query) > 512 || len(f.Owner) > 255 || len(f.Service) > 255 {
		return fmt.Errorf("筛选条件过长")
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
	if f.Status != "" && f.Status != "draft" && f.Status != "ready" && !(f.Type == "changes" && f.Status == "archived") && !(f.Type == "versions" && (f.Status == "completed" || f.Status == "canceled")) {
		return fmt.Errorf("状态不支持")
	}
	if f.Kind != "" && f.Kind != "code" && f.Kind != "artifact" && f.Kind != "mysql" && f.Kind != "nacos" && f.Kind != "redis" && f.Kind != "manual" {
		return fmt.Errorf("变更类型不支持")
	}
	if f.Association != "" && f.Association != "linked" && f.Association != "independent" {
		return fmt.Errorf("关联条件不支持")
	}
	return nil
}
func (s *Store) DeliveryRecordsPage(f DeliveryRecordFilter) (DeliveryRecordPage, error) {
	out := DeliveryRecordPage{Items: []DeliveryListItem{}}
	if err := f.Validate(); err != nil {
		return out, err
	}
	table := "changes"
	if f.Type == "versions" {
		table = "release_versions"
	}
	where, args := []string{}, []any{}
	for _, pair := range [][2]string{{"status", f.Status}, {"owner", f.Owner}} {
		if pair[1] != "" {
			where = append(where, "r."+pair[0]+"=?")
			args = append(args, pair[1])
		}
	}
	if f.Type == "changes" && f.Kind != "" {
		where = append(where, "r.kind=?")
		args = append(args, f.Kind)
	}
	if f.Service != "" {
		if f.Type == "changes" {
			where = append(where, "r.service=?")
		} else {
			where = append(where, "EXISTS (SELECT 1 FROM release_version_changes vc JOIN changes c ON c.id=vc.change_id WHERE vc.release_version_id=r.id AND c.service=?)")
		}
		args = append(args, f.Service)
	}
	if f.Type == "changes" && f.Association != "" {
		clause := "EXISTS (SELECT 1 FROM work_item_changes wc WHERE wc.change_id=r.id)"
		if f.Association == "independent" {
			clause = "NOT " + clause
		}
		where = append(where, clause)
	}
	if f.Query != "" {
		like := "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(f.Query) + "%"
		if f.Type == "changes" {
			where = append(where, "(r.title LIKE ? ESCAPE '!' OR r.service LIKE ? ESCAPE '!' OR r.reference LIKE ? ESCAPE '!')")
		} else {
			where = append(where, "(r.title LIKE ? ESCAPE '!' OR r.version LIKE ? ESCAPE '!' OR r.description LIKE ? ESCAPE '!')")
		}
		args = append(args, like, like, like)
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
	if err = tx.QueryRow("SELECT COUNT(*) FROM "+table+" r"+suffix, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	out.PageSize = f.PageSize
	out.Page = min(f.Page, max(1, (out.Total+f.PageSize-1)/f.PageSize))
	fields := "r.id,r.title,r.status,COALESCE(r.owner,''),r.created_at,r.updated_at,COALESCE(r.kind,''),COALESCE(r.service,''),'',r.kind='code' AND r.status='draft' AND TRIM(COALESCE(r.reference,''))=''"
	if f.Type == "versions" {
		fields = "r.id,r.title,r.status,COALESCE(r.owner,''),r.created_at,r.updated_at,'','',r.version,0"
	}
	rows, err := tx.Query("SELECT "+fields+" FROM "+table+" r"+suffix+" ORDER BY r.updated_at DESC,r.id DESC LIMIT ? OFFSET ?", append(args, f.PageSize, (out.Page-1)*f.PageSize)...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		v := DeliveryListItem{WorkItems: []DeliveryLinkedWork{}}
		if err = rows.Scan(&v.ID, &v.Title, &v.Status, &v.Owner, &v.CreatedAt, &v.UpdatedAt, &v.Kind, &v.Service, &v.Version, &v.MissingBranch); err != nil {
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
	for i := range out.Items {
		v := &out.Items[i]
		if f.Type == "changes" {
			if err = tx.QueryRow("SELECT EXISTS (SELECT 1 FROM release_version_changes vc JOIN release_versions rv ON rv.id=vc.release_version_id WHERE vc.change_id=? AND rv.status<>'draft')", v.ID).Scan(&v.Frozen); err != nil {
				return out, err
			}
			linked, e := tx.Query("SELECT w.id,w.title,w.kind,COALESCE(w.external_id,'') FROM work_items w JOIN work_item_changes wc ON wc.work_item_id=w.id WHERE wc.change_id=? ORDER BY w.id", v.ID)
			if e != nil {
				return out, e
			}
			for linked.Next() {
				w := DeliveryLinkedWork{}
				if e = linked.Scan(&w.ID, &w.Title, &w.Kind, &w.ExternalID); e != nil {
					linked.Close()
					return out, e
				}
				v.WorkItems = append(v.WorkItems, w)
			}
			e = linked.Err()
			linked.Close()
			if e != nil {
				return out, e
			}
			var raw string
			e = tx.QueryRow("SELECT document FROM change_payloads WHERE change_id=?", v.ID).Scan(&raw)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return out, e
			}
			if e == nil {
				var p struct {
					SQL *SQLChange `json:"sql"`
				}
				if e = json.Unmarshal([]byte(raw), &p); e != nil {
					return out, e
				}
				v.DataSync = p.SQL != nil && p.SQL.DataSync != nil
			}
		} else {
			if err = tx.QueryRow("SELECT COUNT(*),COUNT(DISTINCT NULLIF(c.service,'')) FROM release_version_changes vc JOIN changes c ON c.id=vc.change_id WHERE vc.release_version_id=?", v.ID).Scan(&v.ChangeCount, &v.ApplicationCount); err != nil {
				return out, err
			}
			var raw string
			e := tx.QueryRow("SELECT document FROM release_version_scope WHERE release_version_id=?", v.ID).Scan(&raw)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return out, e
			}
			if e == nil {
				var scope VersionScope
				if e = json.Unmarshal([]byte(raw), &scope); e != nil {
					return out, e
				}
				v.WorkItemCount = len(scope.WorkItemIDs)
			}
		}
	}
	return out, tx.Commit()
}
func registerDeliveryRecordListRoutes(mux *http.ServeMux, st *Store, wj func(http.ResponseWriter, any), writeError func(http.ResponseWriter, int, any)) {
	for _, kind := range []string{"changes", "versions"} {
		kind := kind
		endpoint := kind
		if kind == "versions" {
			endpoint = "release-versions"
		}
		mux.HandleFunc("GET /api/"+endpoint+"/page", func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			f := DeliveryRecordFilter{Type: kind, Query: q.Get("q"), Status: q.Get("status"), Kind: q.Get("kind"), Service: q.Get("service"), Owner: q.Get("owner"), Association: q.Get("association")}
			for key, dest := range map[string]*int{"page": &f.Page, "page_size": &f.PageSize} {
				if q.Get(key) != "" {
					v, e := strconv.Atoi(q.Get(key))
					if e != nil || v <= 0 {
						writeError(w, 400, "分页参数必须为正整数")
						return
					}
					*dest = v
				}
			}
			if e := f.Validate(); e != nil {
				writeError(w, 400, e)
				return
			}
			out, e := st.DeliveryRecordsPage(f)
			if e != nil {
				writeError(w, 500, "读取交付列表失败")
				return
			}
			wj(w, out)
		})
		mux.HandleFunc("GET /api/"+endpoint+"/owners", func(w http.ResponseWriter, r *http.Request) {
			table := "changes"
			if kind == "versions" {
				table = "release_versions"
			}
			rows, e := st.db.Query("SELECT DISTINCT owner FROM " + table + " WHERE owner IS NOT NULL AND owner<>'' ORDER BY owner")
			if e != nil {
				writeError(w, 500, "读取创建人失败")
				return
			}
			defer rows.Close()
			out := []string{}
			for rows.Next() {
				var owner string
				if e = rows.Scan(&owner); e != nil {
					writeError(w, 500, "读取创建人失败")
					return
				}
				out = append(out, owner)
			}
			if rows.Err() != nil {
				writeError(w, 500, "读取创建人失败")
				return
			}
			wj(w, out)
		})
		mux.HandleFunc("GET /api/"+endpoint+"/{id}", func(w http.ResponseWriter, r *http.Request) {
			id, e := strconv.ParseInt(r.PathValue("id"), 10, 64)
			if e != nil || id <= 0 {
				writeError(w, 400, "记录编号错误")
				return
			}
			if kind == "changes" {
				v := st.GetChange(id)
				if v == nil {
					writeError(w, 404, "变更不存在")
					return
				}
				var frozen bool
				if e = st.db.QueryRow("SELECT EXISTS (SELECT 1 FROM release_version_changes vc JOIN release_versions rv ON rv.id=vc.release_version_id WHERE vc.change_id=? AND rv.status<>'draft')", id).Scan(&frozen); e != nil {
					writeError(w, 500, "读取冻结状态失败")
					return
				}
				wj(w, struct {
					*Change
					Frozen bool `json:"frozen"`
				}{v, frozen})
			} else {
				v := st.GetReleaseVersion(id)
				if v == nil {
					writeError(w, 404, "版本不存在")
					return
				}
				wj(w, v)
			}
		})
	}
}
