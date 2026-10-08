package deploy

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

type VersionScope struct {
	WorkItemIDs     []int64 `json:"work_item_ids"`
	ManualChangeIDs []int64 `json:"manual_change_ids"`
}

func (s *Store) loadVersionScope(v *ReleaseVersion) {
	v.WorkItems, v.ManualChangeIDs = []WorkItem{}, []int64{}
	var raw string
	err := s.db.QueryRow(`SELECT document FROM release_version_scope WHERE release_version_id=?`, v.ID).Scan(&raw)
	if err == sql.ErrNoRows {
		for _, c := range v.Changes {
			v.ManualChangeIDs = append(v.ManualChangeIDs, c.ID)
		}
		return
	}
	var scope VersionScope
	if err != nil || json.Unmarshal([]byte(raw), &scope) != nil {
		return
	}
	v.ManualChangeIDs = scope.ManualChangeIDs
	for _, id := range scope.WorkItemIDs {
		if w := s.GetWorkItem(id); w != nil {
			v.WorkItems = append(v.WorkItems, *w)
		}
	}
}

// A draft is planning data. It may be empty and include draft changes. Saving
// metadata, explicit demands, manual selections and derived changes is atomic.
func (s *Store) SaveReleaseDraft(v *ReleaseVersion, scope VersionScope) error {
	if err := normalizeReleaseVersion(v); err != nil {
		return err
	}
	if len(scope.WorkItemIDs) > 500 || len(scope.ManualChangeIDs) > 500 {
		return fmt.Errorf("单个版本最多选择 500 项需求或变更")
	}
	scope.WorkItemIDs = append([]int64{}, scope.WorkItemIDs...)
	scope.ManualChangeIDs = append([]int64{}, scope.ManualChangeIDs...)
	sort.Slice(scope.WorkItemIDs, func(i, j int) bool { return scope.WorkItemIDs[i] < scope.WorkItemIDs[j] })
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	if v.ID == 0 {
		res, e := tx.Exec(`INSERT INTO release_versions(version,title,description,status,owner,created_at,updated_at) VALUES(?,?,?,'draft',?,?,?)`, v.Version, v.Title, v.Description, v.Owner, now, now)
		if e != nil {
			return e
		}
		v.ID, _ = res.LastInsertId()
	} else {
		if _, err = tx.Exec(`UPDATE release_versions SET updated_at=updated_at WHERE id=?`, v.ID); err != nil {
			return err
		}
		var state string
		if err = tx.QueryRow(`SELECT status FROM release_versions WHERE id=?`, v.ID).Scan(&state); err != nil || state != ReleaseVersionDraft {
			return fmt.Errorf("仅草稿版本可调整范围，请刷新")
		}
		if _, err = tx.Exec(`UPDATE release_versions SET version=?,title=?,description=?,updated_at=? WHERE id=?`, v.Version, v.Title, v.Description, now, v.ID); err != nil {
			return err
		}
	}
	changes := map[int64]bool{}
	seen := map[int64]bool{}
	for _, id := range scope.WorkItemIDs {
		if id < 1 || seen[id] {
			return fmt.Errorf("需求编号无效或重复")
		}
		seen[id] = true
		var exists int
		q := `SELECT id FROM work_items WHERE id=?`
		if s.db.dialect == "mysql" {
			q += " FOR UPDATE"
		}
		if err = tx.QueryRow(q, id).Scan(&exists); err != nil {
			return fmt.Errorf("所选需求或 Bug 不存在，请刷新")
		}
		rows, e := tx.Query(`SELECT change_id FROM work_item_changes WHERE work_item_id=?`, id)
		if e != nil {
			return e
		}
		for rows.Next() {
			var cid int64
			if e = rows.Scan(&cid); e != nil {
				rows.Close()
				return e
			}
			changes[cid] = true
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
	}
	seen = map[int64]bool{}
	for _, id := range scope.ManualChangeIDs {
		if id < 1 || seen[id] {
			return fmt.Errorf("变更编号无效或重复")
		}
		seen[id] = true
		changes[id] = true
	}
	ids := []int64{}
	for id := range changes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if len(ids) > 500 {
		return fmt.Errorf("版本关联变更超过 500 项，请缩小范围")
	}
	for _, id := range ids {
		query := `SELECT status FROM changes WHERE id=?`
		if s.db.dialect == "mysql" {
			query += " FOR UPDATE"
		}
		var state string
		if err = tx.QueryRow(query, id).Scan(&state); err != nil || (state != ChangeDraft && state != ChangeReady) {
			return fmt.Errorf("所选变更不存在或状态无效，请刷新")
		}
	}
	if _, err = tx.Exec(`DELETE FROM release_version_changes WHERE release_version_id=?`, v.ID); err != nil {
		return err
	}
	for index, id := range ids {
		if _, err = tx.Exec(`INSERT INTO release_version_changes(release_version_id,change_id,sort_order) VALUES(?,?,?)`, v.ID, id, index); err != nil {
			return err
		}
	}
	doc, _ := json.Marshal(scope)
	if _, err = tx.Exec(`INSERT INTO release_version_scope(release_version_id,document) VALUES(?,?) ON CONFLICT(release_version_id) DO UPDATE SET document=excluded.document`, v.ID, string(doc)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) freezeReleaseVersion(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE release_versions SET updated_at=updated_at WHERE id=?`, id); err != nil {
		return err
	}
	var state string
	if err = tx.QueryRow(`SELECT status FROM release_versions WHERE id=?`, id).Scan(&state); err != nil || state != ReleaseVersionDraft {
		return fmt.Errorf("仅草稿版本可提交")
	}
	rows, err := tx.Query(`SELECT change_id FROM release_version_changes WHERE release_version_id=? ORDER BY change_id`, id)
	if err != nil {
		return err
	}
	ids := []int64{}
	for rows.Next() {
		var cid int64
		if err = rows.Scan(&cid); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, cid)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return fmt.Errorf("发布版本至少选择一个变更")
	}
	for _, cid := range ids {
		query := `SELECT status FROM changes WHERE id=?`
		if s.db.dialect == "mysql" {
			query += " FOR UPDATE"
		}
		if err = tx.QueryRow(query, cid).Scan(&state); err != nil || state != ChangeReady {
			return fmt.Errorf("变更 #%d 尚未就绪，请在版本范围中处理", cid)
		}
	}
	var doc string
	err = tx.QueryRow(`SELECT document FROM release_version_scope WHERE release_version_id=?`, id).Scan(&doc)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if doc != "" {
		var scope VersionScope
		if json.Unmarshal([]byte(doc), &scope) != nil {
			return fmt.Errorf("版本范围数据无效")
		}
		for _, wid := range scope.WorkItemIDs {
			var count int
			if err = tx.QueryRow(`SELECT COUNT(*) FROM work_item_changes WHERE work_item_id=? AND change_id IN (SELECT change_id FROM release_version_changes WHERE release_version_id=?)`, wid, id).Scan(&count); err != nil {
				return err
			}
			if count == 0 {
				return fmt.Errorf("需求 / Bug #%d 尚无纳入版本的变更，请补充或移出范围", wid)
			}
			if err = tx.QueryRow(`SELECT COUNT(*) FROM work_item_changes WHERE work_item_id=? AND change_id NOT IN (SELECT change_id FROM release_version_changes WHERE release_version_id=?)`, wid, id).Scan(&count); err != nil {
				return err
			}
			if count > 0 {
				return fmt.Errorf("需求 / Bug #%d 有新增关联变更，请先刷新版本范围再冻结", wid)
			}
		}
	}
	if _, err = tx.Exec(`UPDATE release_versions SET status='ready',updated_at=? WHERE id=?`, time.Now().Unix(), id); err != nil {
		return err
	}
	return tx.Commit()
}
