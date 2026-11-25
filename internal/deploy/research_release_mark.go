package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// A human report of production completion, kept separate from execution proof.
type ResearchReleaseMark struct {
	Marked   bool   `json:"marked"`
	Revision int64  `json:"revision"`
	Actor    string `json:"actor"`
	At       int64  `json:"at"`
	Comment  string `json:"comment"`
}

var errReleaseMarkForbidden = errors.New("仅相关成员或管理员可维护已上线标记")

func (s *Store) researchReleaseMark(id int64) (ResearchReleaseMark, error) {
	var v ResearchReleaseMark
	err := s.db.QueryRow(`SELECT marked,revision,actor,updated_at,comment FROM research_release_marks WHERE work_item_id=?`, id).Scan(&v.Marked, &v.Revision, &v.Actor, &v.At, &v.Comment)
	if err == sql.ErrNoRows {
		err = nil
	}
	return v, err
}
func (s *Store) setResearchReleaseMark(id, revision int64, marked bool, comment, actor string, admin bool) (ResearchReleaseMark, bool, error) {
	v := ResearchReleaseMark{}
	comment = strings.TrimSpace(comment)
	if actor == "" || actor == "local" {
		return v, false, errReleaseMarkForbidden
	}
	if revision < 0 || len(comment) > 2000 {
		return v, false, fmt.Errorf("上线说明不超过 2000 字节，修订号须有效")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return v, false, err
	}
	defer tx.Rollback()
	query := `SELECT id FROM work_items WHERE id=?`
	if tx.dialect == "mysql" {
		query += " FOR UPDATE"
	}
	var existing int64
	if err = tx.QueryRow(query, id).Scan(&existing); err != nil {
		return v, false, fmt.Errorf("工作项不存在")
	}
	if !admin {
		var n int
		if err = tx.QueryRow(`SELECT COUNT(*) FROM work_items w WHERE w.id=? AND `+researchRelationClause("w", "related"), id, actor).Scan(&n); err != nil {
			return v, false, err
		}
		if n == 0 {
			return v, false, errReleaseMarkForbidden
		}
	}
	if _, err = tx.Exec(`INSERT OR IGNORE INTO research_release_marks(work_item_id,marked,revision,actor,updated_at,comment) VALUES(?,0,0,'',0,'')`, id); err != nil {
		return v, false, err
	}
	if err = tx.QueryRow(`SELECT marked,revision,actor,updated_at,comment FROM research_release_marks WHERE work_item_id=?`, id).Scan(&v.Marked, &v.Revision, &v.Actor, &v.At, &v.Comment); err != nil {
		return v, false, err
	}
	// An exact repeated request acknowledges the original record without another activity.
	if v.Revision == revision+1 && v.Marked == marked && v.Comment == comment && v.Actor == actor {
		return v, false, tx.Commit()
	}
	if v.Revision != revision {
		return v, false, fmt.Errorf("上线标记已变化，请重新打开核对后操作")
	}
	if v.Marked == marked {
		return v, false, tx.Commit()
	}
	v = ResearchReleaseMark{Marked: marked, Revision: revision + 1, Actor: actor, At: time.Now().Unix(), Comment: comment}
	result, err := tx.Exec(`UPDATE research_release_marks SET marked=?,revision=?,actor=?,updated_at=?,comment=? WHERE work_item_id=? AND revision=?`, v.Marked, v.Revision, v.Actor, v.At, v.Comment, id, revision)
	if err != nil {
		return v, false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return v, false, err
	}
	if n != 1 {
		return v, false, fmt.Errorf("上线标记已变化，请重新打开核对后操作")
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return v, false, err
	}
	if _, err = tx.Exec(`INSERT INTO research_activity(work_item_id,actor,kind,content,created_at) VALUES(?,?,'release-mark',?,?)`, id, actor, string(raw), v.At); err != nil {
		return v, false, err
	}
	if err = tx.Commit(); err != nil {
		return v, false, err
	}
	return v, true, nil
}

func (s *Store) decorateResearchReleaseMarks(items []researchLiveItem) error {
	ids := []any{}
	for _, item := range items {
		if item.ID > 0 {
			ids = append(ids, item.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.db.Query(`SELECT work_item_id,marked,revision,actor,updated_at,comment FROM research_release_marks WHERE work_item_id IN (`+placeholders(len(ids))+`)`, ids...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var v ResearchReleaseMark
		if err = rows.Scan(&id, &v.Marked, &v.Revision, &v.Actor, &v.At, &v.Comment); err != nil {
			return err
		}
		for i := range items {
			if items[i].ID == id {
				copy := v
				items[i].ReleaseMark = &copy
			}
		}
	}
	return rows.Err()
}
func manuallyMarkedReleased(w WorkItem) bool { return w.ReleaseMark != nil && w.ReleaseMark.Marked }

func registerResearchReleaseMarkRoutes(mux *http.ServeMux, s *Store) {
	readItem := func(w http.ResponseWriter, r *http.Request) *WorkItem {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			deliveryWriteError(w, 400, "工作项编号无效")
			return nil
		}
		item := s.GetWorkItem(id)
		if item == nil {
			deliveryWriteError(w, 404, "工作项不存在")
			return nil
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err := s.canReadResearchItem(ctx, deliveryActor(r), item); err != nil {
			deliveryWriteError(w, 403, err)
			return nil
		}
		return item
	}
	mux.HandleFunc("GET /api/research/work-items/{id}/release-mark", func(w http.ResponseWriter, r *http.Request) {
		item := readItem(w, r)
		if item == nil {
			return
		}
		v, err := s.researchReleaseMark(item.ID)
		if err != nil {
			deliveryWriteError(w, 500, err)
			return
		}
		actor := deliveryActor(r)
		allowed := r.Header.Get("X-Role") == "admin"
		if !allowed {
			var n int
			if err = s.db.QueryRow(`SELECT COUNT(*) FROM work_items w WHERE w.id=? AND `+researchRelationClause("w", "related"), item.ID, actor).Scan(&n); err != nil {
				deliveryWriteError(w, 500, err)
				return
			}
			allowed = n > 0
		}
		deliveryJSON(w, map[string]any{"mark": v, "can_mark": allowed && actor != "" && actor != "local"})
	})
	mux.HandleFunc("PUT /api/research/work-items/{id}/release-mark", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Marked   *bool  `json:"marked"`
			Revision *int64 `json:"revision"`
			Comment  string `json:"comment"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil || req.Marked == nil || req.Revision == nil {
			deliveryWriteError(w, 400, "请选择上线标记并携带当前修订号")
			return
		}
		item := readItem(w, r)
		if item == nil {
			return
		}
		v, changed, err := s.setResearchReleaseMark(item.ID, *req.Revision, *req.Marked, req.Comment, deliveryActor(r), r.Header.Get("X-Role") == "admin")
		if err != nil {
			code := 409
			if errors.Is(err, errReleaseMarkForbidden) {
				code = 403
			}
			deliveryWriteError(w, code, err)
			return
		}
		if changed {
			s.Audit(deliveryActor(r), "release-mark", "work-item", fmt.Sprint(item.ID), v)
		}
		deliveryJSON(w, v)
	})
}
