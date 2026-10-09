package deploy

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	WorkItemOpen   = "open"
	WorkItemClosed = "closed"

	ChangeDraft = "draft"
	ChangeReady = "ready"

	ReleaseVersionDraft     = "draft"
	ReleaseVersionReady     = "ready"
	ReleaseVersionCompleted = "completed"
	ReleaseVersionCanceled  = "canceled"

	PromotionPendingApproval = "pending_approval"
	PromotionApproved        = "approved"
	PromotionRejected        = "rejected"
	PromotionSuccess         = "success"
	PromotionFailed          = "failed"
	PromotionCanceled        = "canceled"
)

// WorkItem represents a requirement or bug. It may be synchronized from an
// external system or entered manually. It has no release/environment owner.
type WorkItem struct {
	ID             int64  `json:"id"`
	Source         string `json:"source"`
	ExternalID     string `json:"external_id,omitempty"`
	Kind           string `json:"kind"`
	Title          string `json:"title"`
	URL            string `json:"url,omitempty"`
	ExternalStatus string `json:"external_status,omitempty"`
	Description    string `json:"description,omitempty"`
	Status         string `json:"status"`
	Owner          string `json:"owner,omitempty"`
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
}

// Change is an independent implementation change. The same record is promoted
// through environments and is never copied for dev/test/prod.
type Change struct {
	ID           int64        `json:"id"`
	Kind         string       `json:"kind"`
	Title        string       `json:"title"`
	Service      string       `json:"service,omitempty"`
	Reference    string       `json:"reference,omitempty"`
	Summary      string       `json:"summary,omitempty"`
	RiskLevel    string       `json:"risk_level"`
	RollbackPlan string       `json:"rollback_plan,omitempty"`
	Status       string       `json:"status"`
	Owner        string       `json:"owner,omitempty"`
	CreatedAt    int64        `json:"created_at"`
	UpdatedAt    int64        `json:"updated_at"`
	WorkItems    []WorkItem   `json:"work_items,omitempty"`
	SQL          *SQLChange   `json:"sql,omitempty"`
	Nacos        *NacosChange `json:"nacos,omitempty"`
	Redis        *RedisChange `json:"redis,omitempty"`
}

type ReleaseVersion struct {
	ID              int64              `json:"id"`
	Version         string             `json:"version"`
	Title           string             `json:"title"`
	Description     string             `json:"description,omitempty"`
	Status          string             `json:"status"`
	Owner           string             `json:"owner,omitempty"`
	CreatedAt       int64              `json:"created_at"`
	UpdatedAt       int64              `json:"updated_at"`
	Changes         []Change           `json:"changes,omitempty"`
	Promotions      []ReleasePromotion `json:"promotions,omitempty"`
	WorkItems       []WorkItem         `json:"work_items"`
	ManualChangeIDs []int64            `json:"manual_change_ids"`
}

type ReleasePromotion struct {
	ID               int64  `json:"id"`
	ReleaseVersionID int64  `json:"release_version_id"`
	Environment      string `json:"environment"`
	Status           string `json:"status"`
	Actor            string `json:"actor,omitempty"`
	Approver         string `json:"approver,omitempty"`
	CreatedAt        int64  `json:"created_at"`
	UpdatedAt        int64  `json:"updated_at"`
}

type ApplicationResourceRequirement struct {
	Service     string `json:"service"`
	Alias       string `json:"alias"`
	Kind        string `json:"kind"`
	Required    bool   `json:"required"`
	Description string `json:"description,omitempty"`
}

type ApplicationEnvironmentResource struct {
	Service     string            `json:"service"`
	Environment string            `json:"environment"`
	Alias       string            `json:"alias"`
	Connection  string            `json:"connection"`
	Config      map[string]string `json:"config,omitempty"`
	UpdatedAt   int64             `json:"updated_at"`
}

type ApplicationResources struct {
	Requirements []ApplicationResourceRequirement `json:"requirements"`
	Bindings     []ApplicationEnvironmentResource `json:"bindings"`
}

type rowScanner interface{ Scan(...any) error }

const workItemSelect = `SELECT id,source,COALESCE(external_id,''),kind,title,COALESCE(url,''),COALESCE(external_status,''),COALESCE(description,''),status,COALESCE(owner,''),created_at,updated_at FROM work_items`

func scanWorkItem(row rowScanner) (WorkItem, error) {
	var v WorkItem
	err := row.Scan(&v.ID, &v.Source, &v.ExternalID, &v.Kind, &v.Title, &v.URL, &v.ExternalStatus, &v.Description, &v.Status, &v.Owner, &v.CreatedAt, &v.UpdatedAt)
	return v, err
}

func normalizeWorkItem(v *WorkItem) error {
	v.Source, v.ExternalID = strings.TrimSpace(v.Source), strings.TrimSpace(v.ExternalID)
	v.Kind, v.Title, v.URL = strings.TrimSpace(v.Kind), strings.TrimSpace(v.Title), strings.TrimSpace(v.URL)
	v.ExternalStatus, v.Description = strings.TrimSpace(v.ExternalStatus), strings.TrimSpace(v.Description)
	if v.Source == "" {
		v.Source = "manual"
	}
	if v.Kind == "" {
		v.Kind = "requirement"
	}
	if v.Title == "" {
		return fmt.Errorf("需求或缺陷标题必填")
	}
	if v.Source != "manual" && v.ExternalID == "" {
		return fmt.Errorf("外部需求必须包含外部编号")
	}
	return nil
}

func (s *Store) CreateWorkItem(v *WorkItem) error {
	if err := normalizeWorkItem(v); err != nil {
		return err
	}
	now := time.Now().Unix()
	v.Status, v.CreatedAt, v.UpdatedAt = WorkItemOpen, now, now
	res, err := s.db.Exec(`INSERT INTO work_items(source,external_id,kind,title,url,external_status,description,status,owner,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		v.Source, v.ExternalID, v.Kind, v.Title, v.URL, v.ExternalStatus, v.Description, v.Status, strings.TrimSpace(v.Owner), now, now)
	if err == nil {
		v.ID, _ = res.LastInsertId()
	}
	return err
}

func (s *Store) WorkItems(status, query string, limit int) []WorkItem {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	q, args, where := workItemSelect, []any{}, []string{}
	if status = strings.TrimSpace(status); status != "" {
		where, args = append(where, "status=?"), append(args, status)
	}
	if query = strings.TrimSpace(query); query != "" {
		like := "%" + query + "%"
		where, args = append(where, "(title LIKE ? OR external_id LIKE ? OR description LIKE ?)"), append(args, like, like, like)
	}
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY updated_at DESC,id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return []WorkItem{}
	}
	defer rows.Close()
	out := []WorkItem{}
	for rows.Next() {
		if v, err := scanWorkItem(rows); err == nil {
			out = append(out, v)
		}
	}
	return out
}

func (s *Store) GetWorkItem(id int64) *WorkItem {
	v, err := scanWorkItem(s.db.QueryRow(workItemSelect+` WHERE id=?`, id))
	if err != nil {
		return nil
	}
	return &v
}

func (s *Store) UpdateWorkItem(v *WorkItem) error {
	current := s.GetWorkItem(v.ID)
	if current == nil {
		return fmt.Errorf("需求或缺陷不存在")
	}
	if strings.HasPrefix(current.Source, "zentao:") && (v.Source != current.Source || v.ExternalID != current.ExternalID || v.Kind != current.Kind) {
		return fmt.Errorf("同步的外部记录不能修改来源、编号或类型")
	}
	if err := normalizeWorkItem(v); err != nil {
		return err
	}
	if v.Status != WorkItemOpen && v.Status != WorkItemClosed {
		return fmt.Errorf("需求状态仅支持 open 或 closed")
	}
	_, err := s.db.Exec(`UPDATE work_items SET source=?,external_id=?,kind=?,title=?,url=?,external_status=?,description=?,status=?,owner=?,updated_at=? WHERE id=?`,
		v.Source, v.ExternalID, v.Kind, v.Title, v.URL, v.ExternalStatus, v.Description, v.Status, strings.TrimSpace(v.Owner), time.Now().Unix(), v.ID)
	return err
}

func (s *Store) DeleteWorkItem(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE work_items SET updated_at=updated_at WHERE id=?`, id); err != nil {
		return err
	}
	var source string
	if err = tx.QueryRow(`SELECT source FROM work_items WHERE id=?`, id).Scan(&source); err != nil {
		return fmt.Errorf("需求或缺陷不存在")
	}
	if strings.HasPrefix(source, "zentao:") {
		return fmt.Errorf("同步的外部记录不能直接删除；请调整同步范围")
	}
	rows, err := tx.Query(`SELECT document FROM release_version_scope`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		var scope VersionScope
		if err = json.Unmarshal([]byte(raw), &scope); err != nil {
			rows.Close()
			return err
		}
		for _, wid := range scope.WorkItemIDs {
			if wid == id {
				rows.Close()
				return fmt.Errorf("需求已纳入版本范围，请先从草稿版本移出")
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var refs int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM work_item_changes WHERE work_item_id=?`, id).Scan(&refs); err != nil {
		return err
	}
	if refs > 0 {
		return fmt.Errorf("需求已关联变更，不能删除")
	}
	result, err := tx.Exec(`DELETE FROM work_items WHERE id=?`, id)
	if err == nil {
		if n, _ := result.RowsAffected(); n == 0 {
			return fmt.Errorf("需求或缺陷不存在")
		}
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

const changeSelect = `SELECT id,kind,title,COALESCE(service,''),COALESCE(reference,''),COALESCE(summary,''),risk_level,COALESCE(rollback_plan,''),status,COALESCE(owner,''),created_at,updated_at FROM changes`

func scanChange(row rowScanner) (Change, error) {
	var v Change
	err := row.Scan(&v.ID, &v.Kind, &v.Title, &v.Service, &v.Reference, &v.Summary, &v.RiskLevel, &v.RollbackPlan, &v.Status, &v.Owner, &v.CreatedAt, &v.UpdatedAt)
	return v, err
}

func normalizeChange(v *Change) error {
	v.Kind, v.Title, v.Service = strings.TrimSpace(v.Kind), strings.TrimSpace(v.Title), strings.TrimSpace(v.Service)
	v.Reference, v.Summary = strings.TrimSpace(v.Reference), strings.TrimSpace(v.Summary)
	v.RiskLevel, v.RollbackPlan = strings.TrimSpace(v.RiskLevel), strings.TrimSpace(v.RollbackPlan)
	if v.Kind == "" || v.Title == "" {
		return fmt.Errorf("变更类型和标题必填")
	}
	if v.RiskLevel == "" {
		v.RiskLevel = "medium"
	}
	if v.RiskLevel != "low" && v.RiskLevel != "medium" && v.RiskLevel != "high" {
		return fmt.Errorf("风险等级仅支持 low、medium 或 high")
	}
	if v.SQL != nil {
		if v.Kind != "mysql" || v.Service == "" || !validResourceName(v.SQL.ResourceAlias) {
			return fmt.Errorf("SQL 变更必须关联应用与资源别名")
		}
		if _, err := parseChangeSQL(v.SQL.Content); err != nil {
			return err
		}
		if v.SQL.MaxAffectedRows < 1 || v.SQL.MaxAffectedRows > 10000 {
			return fmt.Errorf("允许影响行数为 1–10000")
		}
		if v.SQL.DataSync != nil {
			if err := validateSyncScript(v); err != nil {
				return err
			}
		}
	}
	if v.Nacos != nil {
		if err := normalizeNacosChange(v); err != nil {
			return err
		}
	}
	if v.Redis != nil {
		if err := normalizeRedisChange(v); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) CreateChange(v *Change) error {
	if err := normalizeChange(v); err != nil {
		return err
	}
	now := time.Now().Unix()
	v.Status, v.CreatedAt, v.UpdatedAt = ChangeDraft, now, now
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO changes(kind,title,service,reference,summary,risk_level,rollback_plan,status,owner,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		v.Kind, v.Title, v.Service, v.Reference, v.Summary, v.RiskLevel, v.RollbackPlan, v.Status, strings.TrimSpace(v.Owner), now, now)
	if err == nil {
		v.ID, _ = res.LastInsertId()
	}
	if err != nil {
		return err
	}
	if err = saveSQLPayload(tx, v); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Changes(status, service, query string, limit int) []Change {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	q, args, where := changeSelect, []any{}, []string{}
	for _, filter := range []struct{ column, value string }{{"status", status}, {"service", service}} {
		if filter.value = strings.TrimSpace(filter.value); filter.value != "" {
			where, args = append(where, filter.column+"=?"), append(args, filter.value)
		}
	}
	if query = strings.TrimSpace(query); query != "" {
		like := "%" + query + "%"
		where, args = append(where, "(title LIKE ? OR summary LIKE ? OR reference LIKE ?)"), append(args, like, like, like)
	}
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY updated_at DESC,id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return []Change{}
	}
	defer rows.Close()
	out := []Change{}
	for rows.Next() {
		if v, err := scanChange(rows); err == nil {
			out = append(out, v)
		}
	}
	rows.Close() // SQLite uses one connection: release it before loading associations.
	for i := range out {
		out[i].WorkItems = s.ChangeWorkItems(out[i].ID)
		s.loadSQLPayload(&out[i])
	}
	return out
}

func (s *Store) GetChange(id int64) *Change {
	v, err := scanChange(s.db.QueryRow(changeSelect+` WHERE id=?`, id))
	if err != nil {
		return nil
	}
	v.WorkItems = s.ChangeWorkItems(id)
	s.loadSQLPayload(&v)
	return &v
}

func (s *Store) ChangeWorkItems(changeID int64) []WorkItem {
	rows, err := s.db.Query(workItemSelect+` WHERE id IN (SELECT work_item_id FROM work_item_changes WHERE change_id=?) ORDER BY updated_at DESC,id DESC`, changeID)
	if err != nil {
		return []WorkItem{}
	}
	defer rows.Close()
	out := []WorkItem{}
	for rows.Next() {
		if v, err := scanWorkItem(rows); err == nil {
			out = append(out, v)
		}
	}
	return out
}

func (s *Store) UpdateChange(v *Change) error {
	current := s.GetChange(v.ID)
	if current == nil {
		return fmt.Errorf("变更不存在")
	}
	if current.Status != ChangeDraft {
		return fmt.Errorf("仅草稿变更可编辑")
	}
	if current.SQL != nil && current.SQL.DataSync != nil && (v.Kind != current.Kind || v.Service != current.Service || digestJSON(v.SQL) != digestJSON(current.SQL)) {
		return fmt.Errorf("数据同步差异已冻结，请重新对比生成新计划，不可直接改写同步 SQL")
	}
	if err := normalizeChange(v); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`UPDATE changes SET kind=?,title=?,service=?,reference=?,summary=?,risk_level=?,rollback_plan=?,updated_at=? WHERE id=? AND status='draft'`,
		v.Kind, v.Title, v.Service, v.Reference, v.Summary, v.RiskLevel, v.RollbackPlan, time.Now().Unix(), v.ID)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		// MySQL reports zero changed rows when only the separately stored SQL
		// payload changes within the same second. UPDATE still holds its row
		// lock until commit; verify the draft before saving that payload.
		var status string
		if err := tx.QueryRow(`SELECT status FROM changes WHERE id=?`, v.ID).Scan(&status); err != nil || status != ChangeDraft {
			return fmt.Errorf("变更状态已变化，请刷新")
		}
	}
	if err = saveSQLPayload(tx, v); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SetChangeWorkItems(changeID int64, ids []int64) error {
	change := s.GetChange(changeID)
	if change == nil {
		return fmt.Errorf("变更不存在")
	}
	if change.Status != ChangeDraft {
		return fmt.Errorf("仅草稿变更可调整关联需求")
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if id < 1 || seen[id] || s.GetWorkItem(id) == nil {
			return fmt.Errorf("关联的需求或缺陷不存在")
		}
		seen[id] = true
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ordered := append([]int64{}, ids...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	for _, wid := range ordered {
		q := `SELECT id FROM work_items WHERE id=?`
		if s.db.dialect == "mysql" {
			q += " FOR UPDATE"
		}
		var exists int64
		if err = tx.QueryRow(q, wid).Scan(&exists); err != nil {
			return fmt.Errorf("关联需求已删除，请刷新")
		}
	}
	if _, err = tx.Exec(`UPDATE changes SET updated_at=updated_at WHERE id=?`, changeID); err != nil {
		return err
	}
	var state string
	if err = tx.QueryRow(`SELECT status FROM changes WHERE id=?`, changeID).Scan(&state); err != nil || state != ChangeDraft {
		return fmt.Errorf("仅草稿变更可调整关联需求，请刷新")
	}
	if _, err := tx.Exec(`DELETE FROM work_item_changes WHERE change_id=?`, changeID); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := tx.Exec(`INSERT INTO work_item_changes(work_item_id,change_id) VALUES(?,?)`, id, changeID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE changes SET updated_at=? WHERE id=?`, time.Now().Unix(), changeID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SetChangeReady(id int64, ready bool) (*Change, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE changes SET updated_at=updated_at WHERE id=?`, id); err != nil {
		return nil, err
	}
	var current string
	if err = tx.QueryRow(`SELECT status FROM changes WHERE id=?`, id).Scan(&current); err != nil {
		return nil, fmt.Errorf("变更不存在")
	}
	status := ChangeDraft
	if ready {
		status = ChangeReady
	} else {
		var refs int
		if err = tx.QueryRow(`SELECT COUNT(*) FROM release_version_changes c JOIN release_versions v ON v.id=c.release_version_id WHERE c.change_id=? AND v.status<>'draft'`, id).Scan(&refs); err != nil {
			return nil, err
		}
		if refs > 0 {
			return nil, fmt.Errorf("变更已进入冻结版本，不能退回草稿")
		}
	}
	if _, err := tx.Exec(`UPDATE changes SET status=?,updated_at=? WHERE id=?`, status, time.Now().Unix(), id); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetChange(id), nil
}

func (s *Store) DeleteChange(id int64) error {
	v := s.GetChange(id)
	if v == nil {
		return fmt.Errorf("变更不存在")
	}
	if v.Status != ChangeDraft {
		return fmt.Errorf("仅草稿变更可删除")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE changes SET updated_at=updated_at WHERE id=?`, id); err != nil {
		return err
	}
	var state string
	var refs int
	if err = tx.QueryRow(`SELECT status FROM changes WHERE id=?`, id).Scan(&state); err != nil || state != ChangeDraft {
		return fmt.Errorf("仅草稿变更可删除，请刷新")
	}
	if err = tx.QueryRow(`SELECT COUNT(*) FROM release_version_changes WHERE change_id=?`, id).Scan(&refs); err != nil {
		return err
	}
	if refs > 0 {
		return fmt.Errorf("变更已进入发布版本，请先从草稿版本移出")
	}
	if _, err := tx.Exec(`DELETE FROM work_item_changes WHERE change_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM changes WHERE id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM change_payloads WHERE change_id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

const releaseVersionSelect = `SELECT id,version,title,COALESCE(description,''),status,COALESCE(owner,''),created_at,updated_at FROM release_versions`

func scanReleaseVersion(row rowScanner) (ReleaseVersion, error) {
	var v ReleaseVersion
	err := row.Scan(&v.ID, &v.Version, &v.Title, &v.Description, &v.Status, &v.Owner, &v.CreatedAt, &v.UpdatedAt)
	return v, err
}

func normalizeReleaseVersion(v *ReleaseVersion) error {
	v.Version, v.Title, v.Description = strings.TrimSpace(v.Version), strings.TrimSpace(v.Title), strings.TrimSpace(v.Description)
	if v.Version == "" || v.Title == "" {
		return fmt.Errorf("版本号和标题必填")
	}
	return nil
}

func (s *Store) CreateReleaseVersion(v *ReleaseVersion) error {
	if err := normalizeReleaseVersion(v); err != nil {
		return err
	}
	now := time.Now().Unix()
	v.Status, v.CreatedAt, v.UpdatedAt = ReleaseVersionDraft, now, now
	res, err := s.db.Exec(`INSERT INTO release_versions(version,title,description,status,owner,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`,
		v.Version, v.Title, v.Description, v.Status, strings.TrimSpace(v.Owner), now, now)
	if err == nil {
		v.ID, _ = res.LastInsertId()
	}
	return err
}

func (s *Store) ReleaseVersions(status string, limit int) []ReleaseVersion {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	q, args := releaseVersionSelect, []any{}
	if status = strings.TrimSpace(status); status != "" {
		q, args = q+` WHERE status=?`, append(args, status)
	}
	q += ` ORDER BY updated_at DESC,id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return []ReleaseVersion{}
	}
	defer rows.Close()
	out := []ReleaseVersion{}
	for rows.Next() {
		if v, err := scanReleaseVersion(rows); err == nil {
			out = append(out, v)
		}
	}
	rows.Close()
	for i := range out {
		out[i].Changes, out[i].Promotions = s.ReleaseVersionChanges(out[i].ID), s.ReleasePromotions(out[i].ID)
		s.loadVersionScope(&out[i])
	}
	return out
}

func (s *Store) GetReleaseVersion(id int64) *ReleaseVersion {
	v, err := scanReleaseVersion(s.db.QueryRow(releaseVersionSelect+` WHERE id=?`, id))
	if err != nil {
		return nil
	}
	v.Changes, v.Promotions = s.ReleaseVersionChanges(id), s.ReleasePromotions(id)
	s.loadVersionScope(&v)
	return &v
}

func (s *Store) UpdateReleaseVersion(v *ReleaseVersion) error {
	current := s.GetReleaseVersion(v.ID)
	if current == nil {
		return fmt.Errorf("发布版本不存在")
	}
	if current.Status != ReleaseVersionDraft {
		return fmt.Errorf("仅草稿版本可编辑")
	}
	if err := normalizeReleaseVersion(v); err != nil {
		return err
	}
	ids := []int64{}
	for _, w := range current.WorkItems {
		ids = append(ids, w.ID)
	}
	return s.SaveReleaseDraft(v, VersionScope{WorkItemIDs: ids, ManualChangeIDs: current.ManualChangeIDs})
}

func (s *Store) ReleaseVersionChanges(id int64) []Change {
	rows, err := s.db.Query(changeSelect+` WHERE id IN (SELECT change_id FROM release_version_changes WHERE release_version_id=?) ORDER BY id`, id)
	if err != nil {
		return []Change{}
	}
	defer rows.Close()
	out := []Change{}
	for rows.Next() {
		if v, err := scanChange(rows); err == nil {
			out = append(out, v)
		}
	}
	rows.Close()
	for i := range out {
		out[i].WorkItems = s.ChangeWorkItems(out[i].ID)
		s.loadSQLPayload(&out[i])
	}
	return out
}

func (s *Store) SetReleaseVersionChanges(id int64, changeIDs []int64) error {
	v := s.GetReleaseVersion(id)
	if v == nil {
		return fmt.Errorf("发布版本不存在")
	}
	if v.Status != ReleaseVersionDraft {
		return fmt.Errorf("仅草稿版本可选择变更")
	}
	if len(changeIDs) == 0 {
		return fmt.Errorf("发布版本至少选择一个变更")
	}
	seen := map[int64]bool{}
	for _, changeID := range changeIDs {
		change := s.GetChange(changeID)
		if change == nil || change.Status != ChangeReady || seen[changeID] {
			return fmt.Errorf("只能选择已就绪且未重复的变更")
		}
		seen[changeID] = true
	}
	return s.SaveReleaseDraft(v, VersionScope{ManualChangeIDs: changeIDs})
}

func (s *Store) SetReleaseVersionReady(id int64) (*ReleaseVersion, error) {
	if err := s.freezeReleaseVersion(id); err != nil {
		return nil, err
	}
	return s.GetReleaseVersion(id), nil
}

func (s *Store) DeleteReleaseVersion(id int64) error {
	v := s.GetReleaseVersion(id)
	if v == nil {
		return fmt.Errorf("发布版本不存在")
	}
	if v.Status != ReleaseVersionDraft || len(v.Promotions) > 0 {
		return fmt.Errorf("仅未推进的草稿版本可删除")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE release_versions SET updated_at=updated_at WHERE id=?`, id); err != nil {
		return err
	}
	var state string
	var refs int
	if err = tx.QueryRow(`SELECT status FROM release_versions WHERE id=?`, id).Scan(&state); err != nil || state != ReleaseVersionDraft {
		return fmt.Errorf("仅草稿版本可删除，请刷新")
	}
	if err = tx.QueryRow(`SELECT COUNT(*) FROM release_promotions WHERE release_version_id=?`, id).Scan(&refs); err != nil {
		return err
	}
	if refs > 0 {
		return fmt.Errorf("版本已有推进记录，不能删除")
	}
	if _, err := tx.Exec(`DELETE FROM release_version_scope WHERE release_version_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM release_version_changes WHERE release_version_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM release_versions WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

const promotionSelect = `SELECT id,release_version_id,environment,status,COALESCE(actor,''),COALESCE(approver,''),created_at,updated_at FROM release_promotions`

func scanReleasePromotion(row rowScanner) (ReleasePromotion, error) {
	var v ReleasePromotion
	err := row.Scan(&v.ID, &v.ReleaseVersionID, &v.Environment, &v.Status, &v.Actor, &v.Approver, &v.CreatedAt, &v.UpdatedAt)
	return v, err
}

func (s *Store) ReleasePromotions(versionID int64) []ReleasePromotion {
	rows, err := s.db.Query(promotionSelect+` WHERE release_version_id=? ORDER BY id`, versionID)
	if err != nil {
		return []ReleasePromotion{}
	}
	defer rows.Close()
	out := []ReleasePromotion{}
	for rows.Next() {
		if v, err := scanReleasePromotion(rows); err == nil {
			out = append(out, v)
		}
	}
	return out
}

func (s *Store) GetReleasePromotion(id int64) *ReleasePromotion {
	v, err := scanReleasePromotion(s.db.QueryRow(promotionSelect+` WHERE id=?`, id))
	if err != nil {
		return nil
	}
	return &v
}

func (s *Store) CreateReleasePromotion(versionID int64, environment, actor string) (*ReleasePromotion, error) {
	v := s.GetReleaseVersion(versionID)
	if v == nil || (v.Status != ReleaseVersionReady && v.Status != ReleaseVersionCompleted) {
		return nil, fmt.Errorf("发布版本尚未就绪")
	}
	environment = strings.TrimSpace(environment)
	env := s.GetEnvironment(environment)
	if env == nil {
		return nil, fmt.Errorf("目标环境不存在")
	}
	status := PromotionApproved
	if env.Protected {
		if strings.TrimSpace(env.Approvers) == "" {
			return nil, fmt.Errorf("受保护环境未配置发布审核人员")
		}
		status = PromotionPendingApproval
	}
	now := time.Now().Unix()
	res, err := s.db.Exec(`INSERT INTO release_promotions(release_version_id,environment,status,actor,approver,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`,
		versionID, environment, status, strings.TrimSpace(actor), "", now, now)
	if err != nil {
		return nil, fmt.Errorf("该版本已创建过当前环境的推进记录")
	}
	id, _ := res.LastInsertId()
	return s.GetReleasePromotion(id), nil
}

func (s *Store) ReviewReleasePromotion(id int64, actor string, approve bool) (*ReleasePromotion, error) {
	v := s.GetReleasePromotion(id)
	if v == nil || v.Status != PromotionPendingApproval {
		return nil, fmt.Errorf("环境推进不在待审核状态")
	}
	env := s.GetEnvironment(v.Environment)
	if strings.TrimSpace(v.Actor) == strings.TrimSpace(actor) {
		return nil, fmt.Errorf("发布发起人不能审核自己的推进申请")
	}
	if env == nil || !allowedApprover(env.Approvers, actor) {
		return nil, fmt.Errorf("当前用户不是该环境的发布审核人员")
	}
	status := PromotionRejected
	if approve {
		status = PromotionApproved
	}
	if _, err := s.db.Exec(`UPDATE release_promotions SET status=?,approver=?,updated_at=? WHERE id=?`, status, strings.TrimSpace(actor), time.Now().Unix(), id); err != nil {
		return nil, err
	}
	return s.GetReleasePromotion(id), nil
}

func (s *Store) SetReleasePromotionStatus(id int64, from []string, status string) (*ReleasePromotion, error) {
	v := s.GetReleasePromotion(id)
	if v == nil {
		return nil, fmt.Errorf("环境推进不存在")
	}
	allowed := false
	for _, candidate := range from {
		allowed = allowed || v.Status == candidate
	}
	if !allowed {
		return nil, fmt.Errorf("当前状态不能执行该操作")
	}
	if _, err := s.db.Exec(`UPDATE release_promotions SET status=?,updated_at=? WHERE id=?`, status, time.Now().Unix(), id); err != nil {
		return nil, err
	}
	if status == PromotionSuccess {
		var remaining int
		_ = s.db.QueryRow(`SELECT COUNT(*) FROM release_promotions WHERE release_version_id=? AND status<>?`, v.ReleaseVersionID, PromotionSuccess).Scan(&remaining)
		if remaining == 0 {
			_, _ = s.db.Exec(`UPDATE release_versions SET status=?,updated_at=? WHERE id=?`, ReleaseVersionCompleted, time.Now().Unix(), v.ReleaseVersionID)
		}
	}
	return s.GetReleasePromotion(id), nil
}

func validApplicationResourceKind(kind string) bool {
	return kind == "mysql" || kind == "nacos" || kind == "redis"
}

func (s *Store) ApplicationResources(service string) ApplicationResources {
	service = strings.TrimSpace(service)
	out := ApplicationResources{Requirements: []ApplicationResourceRequirement{}, Bindings: []ApplicationEnvironmentResource{}}
	rows, err := s.db.Query(`SELECT service,alias,kind,required,COALESCE(description,'') FROM application_resource_requirements WHERE service=? ORDER BY alias`, service)
	if err == nil {
		for rows.Next() {
			var v ApplicationResourceRequirement
			if rows.Scan(&v.Service, &v.Alias, &v.Kind, &v.Required, &v.Description) == nil {
				out.Requirements = append(out.Requirements, v)
			}
		}
		rows.Close()
	}
	rows, err = s.db.Query(`SELECT service,environment,alias,connection,COALESCE(config,'{}'),updated_at FROM application_environment_resources WHERE service=? ORDER BY environment,alias`, service)
	if err == nil {
		for rows.Next() {
			var v ApplicationEnvironmentResource
			var raw string
			if rows.Scan(&v.Service, &v.Environment, &v.Alias, &v.Connection, &raw, &v.UpdatedAt) == nil {
				_ = json.Unmarshal([]byte(raw), &v.Config)
				out.Bindings = append(out.Bindings, v)
			}
		}
		rows.Close()
	}
	return out
}

func (s *Store) SaveApplicationResources(service string, resources ApplicationResources) error {
	service = strings.TrimSpace(service)
	if s.GetService(service) == nil {
		return fmt.Errorf("应用不存在")
	}
	requirements := map[string]ApplicationResourceRequirement{}
	for _, requirement := range resources.Requirements {
		requirement.Service = service
		requirement.Alias, requirement.Kind = strings.TrimSpace(requirement.Alias), strings.TrimSpace(requirement.Kind)
		requirement.Description = strings.TrimSpace(requirement.Description)
		if !validResourceName(requirement.Alias) {
			return fmt.Errorf("资源别名支持字母、数字、点、短横线和下划线")
		}
		if !validApplicationResourceKind(requirement.Kind) {
			return fmt.Errorf("应用资源类型仅支持 mysql、nacos 或 redis")
		}
		if _, exists := requirements[requirement.Alias]; exists {
			return fmt.Errorf("资源别名不能重复")
		}
		requirements[requirement.Alias] = requirement
	}
	seenBindings := map[string]bool{}
	for i := range resources.Bindings {
		binding := &resources.Bindings[i]
		binding.Service, binding.Environment = service, strings.TrimSpace(binding.Environment)
		binding.Alias, binding.Connection = strings.TrimSpace(binding.Alias), strings.TrimSpace(binding.Connection)
		requirement, ok := requirements[binding.Alias]
		if !ok {
			return fmt.Errorf("资源绑定引用了未声明的别名 %s", binding.Alias)
		}
		if s.GetEnvironment(binding.Environment) == nil {
			return fmt.Errorf("资源绑定的环境不存在")
		}
		connection := s.GetConnection(binding.Connection)
		if connection == nil || connection.Kind != requirement.Kind {
			return fmt.Errorf("资源 %s 必须绑定 %s 类型连接", binding.Alias, requirement.Kind)
		}
		key := binding.Environment + "\x00" + binding.Alias
		if seenBindings[key] {
			return fmt.Errorf("同一环境的资源别名不能重复绑定")
		}
		seenBindings[key] = true
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM application_environment_resources WHERE service=?`, service); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM application_resource_requirements WHERE service=?`, service); err != nil {
		return err
	}
	for _, requirement := range requirements {
		if _, err := tx.Exec(`INSERT INTO application_resource_requirements(service,alias,kind,required,description) VALUES(?,?,?,?,?)`,
			service, requirement.Alias, requirement.Kind, requirement.Required, requirement.Description); err != nil {
			return err
		}
	}
	now := time.Now().Unix()
	for _, binding := range resources.Bindings {
		if binding.Config == nil {
			binding.Config = map[string]string{}
		}
		raw, _ := json.Marshal(binding.Config)
		if _, err := tx.Exec(`INSERT INTO application_environment_resources(service,environment,alias,connection,config,updated_at) VALUES(?,?,?,?,?,?)`,
			service, binding.Environment, binding.Alias, binding.Connection, string(raw), now); err != nil {
			return err
		}
	}
	return tx.Commit()
}
