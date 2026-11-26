package deploy

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	WorkItemOpen     = "open"
	WorkItemActive   = "active"
	WorkItemResolved = "resolved"
	WorkItemClosed   = "closed"

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
	ReleaseMark *ResearchReleaseMark `json:"release_mark,omitempty"`
	ResearchIssueFields
	ReleaseOrigin     string `json:"release_origin,omitempty"`
	ReleaseReason     string `json:"release_reason,omitempty"`
	RuleRevision      int64  `json:"rule_revision,omitempty"`
	Following         bool   `json:"following"`
	Participating     bool   `json:"participating"`
	ReleaseRequired   *bool  `json:"release_required"`
	NoReleaseReason   string `json:"no_release_reason,omitempty"`
	MetadataRevision  int64  `json:"metadata_revision"`
	ID                int64  `json:"id"`
	Source            string `json:"source"`
	ExternalID        string `json:"external_id,omitempty"`
	Kind              string `json:"kind"`
	Title             string `json:"title"`
	URL               string `json:"url,omitempty"`
	ExternalStatus    string `json:"external_status,omitempty"`
	Description       string `json:"description,omitempty"`
	DescriptionFormat string `json:"description_format,omitempty"`
	Status            string `json:"status"`
	Owner             string `json:"owner,omitempty"`
	AssignedTo        string `json:"assigned_to,omitempty"`
	OpenedBy          string `json:"opened_by,omitempty"`
	ResolvedBy        string `json:"resolved_by,omitempty"`
	ClosedBy          string `json:"closed_by,omitempty"`
	CreatedAt         int64  `json:"created_at"`
	UpdatedAt         int64  `json:"updated_at"`
}

// Change is an independent implementation change. The same record is promoted
// through environments and is never copied for dev/test/prod.
type Change struct {
	Prerequisites     []ChangePrerequisite     `json:"prerequisites,omitempty"`
	DeliveryOwnership *ChangeDeliveryOwnership `json:"delivery_ownership,omitempty"`
	ID                int64                    `json:"id"`
	Kind              string                   `json:"kind"`
	Title             string                   `json:"title"`
	Service           string                   `json:"service,omitempty"`
	Reference         string                   `json:"reference,omitempty"`
	Summary           string                   `json:"summary,omitempty"`
	RiskLevel         string                   `json:"risk_level"`
	RollbackPlan      string                   `json:"rollback_plan,omitempty"`
	Status            string                   `json:"status"`
	Owner             string                   `json:"owner,omitempty"`
	CreatedAt         int64                    `json:"created_at"`
	UpdatedAt         int64                    `json:"updated_at"`
	WorkItems         []WorkItem               `json:"work_items,omitempty"`
	SQL               *SQLChange               `json:"sql,omitempty"`
	Nacos             *NacosChange             `json:"nacos,omitempty"`
	Redis             *RedisChange             `json:"redis,omitempty"`
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
	Sources       map[string]ResourceSourceEvidence `json:"sources,omitempty"`
	GroupName     string                            `json:"group_name,omitempty"`
	GroupRevision int64                             `json:"group_revision,omitempty"`
	Service       string                            `json:"service"`
	Alias         string                            `json:"alias"`
	Kind          string                            `json:"kind"`
	Required      bool                              `json:"required"`
	Description   string                            `json:"description,omitempty"`
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
	NacosScopeVersion  int                              `json:"nacos_scope_version,omitempty"`
	Revision           int64                            `json:"revision"`
	Checks             []ResourceCheck                  `json:"checks,omitempty"`
	ExpectedRevision   *int64                           `json:"-"`
	CompositionVersion int                              `json:"composition_version,omitempty"`
	ResourceGroups     []string                         `json:"resource_groups,omitempty"`
	GroupRevisions     map[string]int64                 `json:"group_revisions,omitempty"`
	OwnRequirements    []ApplicationResourceRequirement `json:"own_requirements,omitempty"`
	OwnBindings        []ApplicationEnvironmentResource `json:"own_bindings,omitempty"`
	NacosConfigs       []ApplicationNacosConfig         `json:"nacos_configs,omitempty"`
	Error              string                           `json:"error,omitempty"`
	ResourceGroup      string                           `json:"resource_group,omitempty"`
	GroupRevision      int64                            `json:"group_revision,omitempty"`
	Requirements       []ApplicationResourceRequirement `json:"requirements"`
	Bindings           []ApplicationEnvironmentResource `json:"bindings"`
}

type rowScanner interface{ Scan(...any) error }

const workItemSelect = `SELECT id,source,COALESCE(external_id,''),kind,title,COALESCE(url,''),COALESCE(external_status,''),COALESCE(description,''),status,COALESCE(owner,''),created_at,updated_at,COALESCE(assigned_to,''),COALESCE(opened_by,''),COALESCE(resolved_by,''),COALESCE(closed_by,''),COALESCE(description_format,''),(SELECT release_required FROM research_work_metadata WHERE work_item_id=work_items.id),COALESCE((SELECT no_release_reason FROM research_work_metadata WHERE work_item_id=work_items.id),''),COALESCE((SELECT revision FROM research_work_metadata WHERE work_item_id=work_items.id),0),COALESCE((SELECT document FROM research_issue_fields WHERE work_item_id=work_items.id),'{}') FROM work_items`

func scanWorkItem(row rowScanner) (WorkItem, error) {
	var v WorkItem
	var fields string
	err := row.Scan(&v.ID, &v.Source, &v.ExternalID, &v.Kind, &v.Title, &v.URL, &v.ExternalStatus, &v.Description, &v.Status, &v.Owner, &v.CreatedAt, &v.UpdatedAt, &v.AssignedTo, &v.OpenedBy, &v.ResolvedBy, &v.ClosedBy, &v.DescriptionFormat, &v.ReleaseRequired, &v.NoReleaseReason, &v.MetadataRevision, &fields)
	if err == nil {
		err = json.Unmarshal([]byte(fields), &v.ResearchIssueFields)
	}
	return v, err
}

func normalizeWorkItem(v *WorkItem) error {
	if v.DescriptionFormat != "" && v.DescriptionFormat != "markdown" && v.DescriptionFormat != "html" && v.DescriptionFormat != "text" {
		return fmt.Errorf("说明格式仅支持 markdown、html 或 text")
	}
	v.Source, v.ExternalID = strings.TrimSpace(v.Source), strings.TrimSpace(v.ExternalID)
	v.Kind, v.Title, v.URL = strings.TrimSpace(v.Kind), strings.TrimSpace(v.Title), strings.TrimSpace(v.URL)
	v.ExternalStatus, v.Description = strings.TrimSpace(v.ExternalStatus), strings.TrimSpace(v.Description)
	v.AssignedTo = strings.TrimSpace(v.AssignedTo)
	if (v.Source == "" || v.Source == "manual") && (len(v.AssignedTo) > 100 || strings.ContainsAny(v.AssignedTo, "\r\n\x00") || strings.EqualFold(v.AssignedTo, "closed")) {
		return fmt.Errorf("请选择有效的负责人")
	}
	if v.Source == "" {
		v.Source = "manual"
	}
	if v.Kind == "" {
		v.Kind = "requirement"
	}
	if v.Kind != "requirement" && v.Kind != "bug" {
		return fmt.Errorf("工作项类型仅支持需求或 Bug")
	}
	if v.Source == "manual" {
		for name, value := range map[string]string{"严重程度": v.Severity, "优先级": v.Priority} {
			if value != "" && value != "1" && value != "2" && value != "3" && value != "4" {
				return fmt.Errorf("%s仅支持 1 至 4", name)
			}
		}
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
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = s.createWorkItemTx(tx, v); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) createWorkItemTx(tx *deployTx, v *WorkItem) error {
	if err := normalizeWorkItem(v); err != nil {
		return err
	}
	now := time.Now().Unix()
	// External personnel is authoritative only when imported by its connector.
	if v.Source != "manual" {
		v.AssignedTo = ""
	}
	v.OpenedBy, v.ResolvedBy, v.ClosedBy = "", "", ""
	if v.Source == "manual" {
		v.OpenedBy = strings.TrimSpace(v.Owner)
	}
	v.Status, v.CreatedAt, v.UpdatedAt = WorkItemOpen, now, now
	res, err := tx.Exec(`INSERT INTO work_items(source,external_id,kind,title,url,external_status,description,status,owner,created_at,updated_at,description_format) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		v.Source, v.ExternalID, v.Kind, v.Title, v.URL, v.ExternalStatus, v.Description, v.Status, strings.TrimSpace(v.Owner), now, now, v.DescriptionFormat)
	if err != nil {
		return err
	}
	v.ID, err = res.LastInsertId()
	if err != nil {
		return err
	}
	if v.Source == "manual" {
		if _, err = tx.Exec(`UPDATE work_items SET assigned_to=?,opened_by=? WHERE id=?`, v.AssignedTo, v.OpenedBy, v.ID); err != nil {
			return err
		}
	}
	if err = bindWorkAttachments(tx, v, v.Owner); err != nil {
		return err
	}
	if v.ReleaseRequired != nil {
		if err = saveResearchMetadata(tx, v.ID, ResearchMetadata{ReleaseRequired: v.ReleaseRequired, NoReleaseReason: v.NoReleaseReason}, v.Owner); err != nil {
			return err
		}
		v.MetadataRevision = 1
	}
	if v.Source == "manual" {
		if err = saveIssueFields(tx, v.ID, v.ResearchIssueFields); err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO research_activity(work_item_id,actor,kind,content,created_at) VALUES(?,?,'created','',?)`, v.ID, v.Owner, now); err != nil {
			return err
		}
		if err = recordResearchRelation(tx, v.ID, v.Owner, "participant"); err != nil {
			return err
		}
	}
	return nil
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
	if err = s.decorateResearchPolicy(&v); err != nil {
		v.ReleaseRequired = nil
		v.ReleaseOrigin = "unknown"
		v.ReleaseReason = err.Error()
	}
	if mark, err := s.researchReleaseMark(id); err == nil && mark.Revision > 0 {
		v.ReleaseMark = &mark
	}
	return &v
}

func (s *Store) UpdateWorkItem(v *WorkItem) error {
	return s.UpdateWorkItemAs(v, v.Owner)
}
func (s *Store) UpdateWorkItemAs(v *WorkItem, actor string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	query := workItemSelect + ` WHERE id=?`
	if tx.dialect == "mysql" {
		query += " FOR UPDATE"
	}
	current, err := scanWorkItem(tx.QueryRow(query, v.ID))
	if err != nil {
		return fmt.Errorf("需求或缺陷不存在")
	}
	if strings.HasPrefix(current.Source, "zentao:") && (v.Source != current.Source || v.ExternalID != current.ExternalID || v.Kind != current.Kind) {
		return fmt.Errorf("同步的外部记录不能修改来源、编号或类型")
	}
	// General edits never own status. Keep the value read by the store so a
	// concurrent dedicated status transition cannot be overwritten.
	v.Status = current.Status
	if v.DescriptionFormat == "" {
		v.DescriptionFormat = current.DescriptionFormat
	}
	if err := normalizeWorkItem(v); err != nil {
		return err
	}
	if v.Status != WorkItemOpen && v.Status != WorkItemActive && v.Status != WorkItemResolved && v.Status != WorkItemClosed {
		return fmt.Errorf("状态仅支持 open、active、resolved 或 closed")
	}
	_, err = tx.Exec(`UPDATE work_items SET source=?,external_id=?,kind=?,title=?,url=?,external_status=?,description=?,owner=?,updated_at=?,description_format=? WHERE id=?`,
		v.Source, v.ExternalID, v.Kind, v.Title, v.URL, v.ExternalStatus, v.Description, strings.TrimSpace(v.Owner), time.Now().Unix(), v.DescriptionFormat, v.ID)
	if err != nil {
		return err
	}
	if err = bindWorkAttachments(tx, v, actor); err != nil {
		return err
	}
	if current.Source == "manual" && v.Source == "manual" {
		if _, err = tx.Exec(`UPDATE work_items SET assigned_to=? WHERE id=?`, v.AssignedTo, v.ID); err != nil {
			return err
		}
		if err = saveIssueFields(tx, v.ID, v.ResearchIssueFields); err != nil {
			return err
		}
		changes := map[string][2]string{}
		if current.Status != v.Status {
			changes["status"] = [2]string{current.Status, v.Status}
		}
		if current.AssignedTo != v.AssignedTo {
			changes["assigned_to"] = [2]string{current.AssignedTo, v.AssignedTo}
		}
		if current.Title != v.Title {
			changes["title"] = [2]string{current.Title, v.Title}
		}
		if current.Description != v.Description {
			changes["description"] = [2]string{"已填写", "已更新"}
		}
		if current.Priority != v.Priority {
			changes["priority"] = [2]string{current.Priority, v.Priority}
		}
		if current.Severity != v.Severity {
			changes["severity"] = [2]string{current.Severity, v.Severity}
		}
		if current.Resolution != v.Resolution {
			changes["resolution"] = [2]string{current.Resolution, v.Resolution}
		}
		if len(changes) > 0 {
			kind := "work-item-updated"
			if len(changes) == 1 {
				if _, ok := changes["status"]; ok {
					kind = "work-item-status"
				} else if _, ok := changes["assigned_to"]; ok {
					kind = "work-item-assigned"
				}
			}
			raw, marshalErr := json.Marshal(changes)
			if marshalErr != nil {
				return marshalErr
			}
			if _, err = tx.Exec(`INSERT INTO research_activity(work_item_id,actor,kind,content,created_at) VALUES(?,?,?,?,?)`, v.ID, actor, kind, string(raw), time.Now().Unix()); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) SetManualWorkItemStatus(id, expectedUpdatedAt int64, expectedStatus, status, actor string, admin bool) error {
	if status != WorkItemOpen && status != WorkItemActive && status != WorkItemResolved && status != WorkItemClosed {
		return fmt.Errorf("状态仅支持 open、active、resolved 或 closed")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	query := `SELECT source,status,owner,assigned_to,updated_at FROM work_items WHERE id=?`
	if tx.dialect == "mysql" {
		query += " FOR UPDATE"
	}
	var source, current, owner, assignee string
	var updatedAt int64
	if err = tx.QueryRow(query, id).Scan(&source, &current, &owner, &assignee, &updatedAt); err != nil {
		return fmt.Errorf("需求或缺陷不存在")
	}
	if source != "manual" {
		return fmt.Errorf("来源工作项状态须在来源平台维护")
	}
	if actor == "" || (!admin && actor != owner && actor != assignee) {
		return fmt.Errorf("仅创建人、负责人或管理员可变更状态")
	}
	if expectedUpdatedAt <= 0 || expectedUpdatedAt != updatedAt || expectedStatus == "" || expectedStatus != current {
		return fmt.Errorf("工作项已被其他人更新，请重新读取")
	}
	if status == current {
		return tx.Commit()
	}
	now := time.Now().Unix()
	result, err := tx.Exec(`UPDATE work_items SET status=?,updated_at=? WHERE id=? AND updated_at=? AND status=?`, status, now, id, expectedUpdatedAt, expectedStatus)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return fmt.Errorf("工作项已被其他人更新，请重新读取")
	}
	content, _ := json.Marshal(map[string][2]string{"status": {current, status}})
	if _, err = tx.Exec(`INSERT INTO research_activity(work_item_id,actor,kind,content,created_at) VALUES(?,?,'work-item-status',?,?)`, id, actor, string(content), now); err != nil {
		return err
	}
	return tx.Commit()
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
	if err = tx.QueryRow(`SELECT COUNT(*) FROM bug_evidence WHERE work_item_id=?`, id).Scan(&refs); err != nil {
		return err
	}
	if refs > 0 {
		return fmt.Errorf("Bug 已有关联证据，请关闭保留记录")
	}
	if err = tx.QueryRow(`SELECT COUNT(*) FROM research_release_marks WHERE work_item_id=? AND revision>0`, id).Scan(&refs); err != nil {
		return err
	}
	if refs > 0 {
		return fmt.Errorf("已有人工上线记录，请关闭保留历史")
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
	return normalizeChangeForSave(v, false)
}

// Delivery drafts retain the author's SQL verbatim. Only metadata is saved;
// readiness and execution still require the strict supported SQL dialect.
func normalizeChangeForSave(v *Change, sqlDraft bool) error {
	if err := validateChangePrerequisites(v); err != nil {
		return err
	}
	v.Kind, v.Title, v.Service = strings.TrimSpace(v.Kind), strings.TrimSpace(v.Title), strings.TrimSpace(v.Service)
	v.Reference, v.Summary = strings.TrimSpace(v.Reference), strings.TrimSpace(v.Summary)
	v.RiskLevel, v.RollbackPlan = strings.TrimSpace(v.RiskLevel), strings.TrimSpace(v.RollbackPlan)
	if v.Kind == "" || v.Title == "" {
		return fmt.Errorf("变更类型和标题必填")
	}
	if v.Kind == "manual" && (len(v.Reference) > 65536 || len(v.Summary) > 65536) {
		return fmt.Errorf("自定义处理记录不能超过 64 KiB")
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
		strictSQL := !sqlDraft || v.SQL.DataSync != nil
		if err := normalizeSQLEnvironmentOverridesForSave(v.SQL, strictSQL); err != nil {
			return err
		}
		if err := validateSQLDraftContent(v.SQL.Content); err != nil {
			return err
		}
		if strictSQL {
			if _, err := parseChangeSQL(v.SQL.Content); err != nil {
				return err
			}
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
	if err := normalizeChangeForSave(v, true); err != nil {
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
		loadChangePayload(s.db, &out[i])
	}
	return out
}

func (s *Store) GetChange(id int64) *Change {
	v, err := scanChange(s.db.QueryRow(changeSelect+` WHERE id=?`, id))
	if err != nil {
		return nil
	}
	v.WorkItems = s.ChangeWorkItems(id)
	if loadChangePayload(s.db, &v) != nil {
		return nil
	}
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
	if touched, err := s.changeExecutionTouched(v.ID); err != nil {
		return err
	} else if touched {
		return fmt.Errorf("变更已有执行记录，不能覆盖修改；请新增后续变更并关联原需求 / Bug")
	}
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
	if err := normalizeChangeForSave(v, true); err != nil {
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
	if change.DeliveryOwnership != nil {
		return fmt.Errorf("此变更已区分来源和交付归属，请在原始工作项维护，不能覆盖关联关系")
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
		if err = recordResearchRelation(tx, id, change.Owner, "participant"); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE changes SET updated_at=? WHERE id=?`, time.Now().Unix(), changeID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SetChangeReady(id int64, ready bool) (*Change, error) {
	if !ready {
		if touched, err := s.changeExecutionTouched(id); err != nil {
			return nil, err
		} else if touched {
			return nil, fmt.Errorf("变更已有执行记录，不能退回修改；请新增后续变更")
		}
	}
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
	if current != ChangeDraft && current != ChangeReady {
		return nil, fmt.Errorf("归档变更不可就绪或重新打开")
	}
	status := ChangeDraft
	if ready {
		// The locked payload is authoritative: draft saving may retain SQL that
		// is not executable. Direct ready endpoints must not bypass validation.
		change, e := scanChange(tx.QueryRow(changeSelect+` WHERE id=?`, id))
		if e != nil {
			return nil, e
		}
		if current == ChangeDraft && change.Kind == "code" && strings.TrimSpace(change.Reference) == "" {
			return nil, fmt.Errorf("请补齐关联分支后再就绪")
		}
		if change.Kind == "mysql" {
			var doc string
			var payload struct {
				SQL *SQLChange `json:"sql"`
			}
			if e = tx.QueryRow(`SELECT document FROM change_payloads WHERE change_id=?`, id).Scan(&doc); e != nil {
				return nil, fmt.Errorf("请补齐 SQL 变更内容")
			}
			if e = json.Unmarshal([]byte(doc), &payload); e != nil || payload.SQL == nil {
				return nil, fmt.Errorf("SQL 变更内容无效，请重新核对草稿")
			}
			change.SQL = payload.SQL
			if e = normalizeChange(&change); e != nil {
				return nil, fmt.Errorf("SQL 尚未通过就绪校验：%w", e)
			}
		}
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
	if _, err := tx.Exec(`DELETE FROM change_delivery_ownership WHERE change_id=?`, id); err != nil {
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
	v.Changes, err = s.readVersionChanges(id)
	if err != nil {
		return nil
	}
	v.Promotions = s.ReleasePromotions(id)
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
	out, _ := s.readVersionChanges(id)
	return out
}
func (s *Store) readVersionChanges(id int64) ([]Change, error) {
	rows, err := s.db.Query(changeSelect+` WHERE id IN (SELECT change_id FROM release_version_changes WHERE release_version_id=?) ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Change{}
	for rows.Next() {
		v, err := scanChange(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for i := range out {
		out[i].WorkItems = s.ChangeWorkItems(out[i].ID)
		if err = loadChangePayload(s.db, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
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
	for attempt := 0; attempt < 3; attempt++ {
		revision := s.applicationResourceRevision(service)
		value := s.readApplicationResources(service)
		if s.applicationResourceRevision(service) == revision {
			value.Revision = revision
			return value
		}
	}
	return ApplicationResources{Revision: s.applicationResourceRevision(service), Error: "应用资源正在变化，请重新读取", Requirements: []ApplicationResourceRequirement{}, Bindings: []ApplicationEnvironmentResource{}}
}

func (s *Store) readApplicationResources(service string) ApplicationResources {
	if out, ok := s.composedApplicationResources(service); ok {
		return out
	}
	service = strings.TrimSpace(service)
	if name := s.applicationResourceGroup(service); name != "" {
		out := ApplicationResources{ResourceGroup: name, Requirements: []ApplicationResourceRequirement{}, Bindings: []ApplicationEnvironmentResource{}}
		if g := s.ResourceGroup(name); g != nil {
			out.GroupRevision = g.Revision
			out.Requirements = g.Requirements
			out.Bindings = g.Bindings
			for i := range out.Requirements {
				out.Requirements[i].Service = service
			}
			for i := range out.Bindings {
				out.Bindings[i].Service = service
			}
		}
		return out
	}
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
	if resources.CompositionVersion != 2 && resources.ResourceGroup == "" {
		for _, r := range resources.Requirements {
			if len(r.Sources) > 0 {
				resources.CompositionVersion = 2
				resources.OwnRequirements, resources.OwnBindings = resources.Requirements, resources.Bindings
				break
			}
		}
	}
	if resources.CompositionVersion == 2 {
		return s.saveComposedResources(service, resources)
	}
	var composed int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM deploy_meta WHERE key=?`, "application_resources_v2:"+service).Scan(&composed)
	if composed > 0 {
		return fmt.Errorf("应用已使用多资源组配置，请刷新页面后保存")
	}
	service = strings.TrimSpace(service)
	if !s.hasResourceApplication(service) {
		return fmt.Errorf("应用不存在")
	}
	resources.ResourceGroup = strings.TrimSpace(resources.ResourceGroup)
	if resources.ResourceGroup != "" {
		g := s.ResourceGroup(resources.ResourceGroup)
		if g == nil {
			return fmt.Errorf("资源组不存在")
		}
		if resources.GroupRevision != 0 && resources.GroupRevision != g.Revision {
			return fmt.Errorf("资源组已变化，请重新读取后确认")
		}
		// Group members have one authority, never accept hidden app overrides.
		resources.Requirements, resources.Bindings = g.Requirements, g.Bindings
		resources.GroupRevision = g.Revision
	}
	requirements, err := s.normalizeApplicationResources(service, &resources)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = s.advanceApplicationResourceRevision(tx, service, resources.ExpectedRevision); err != nil {
		return err
	}
	if resources.ResourceGroup != "" {
		var raw string
		var current ResourceGroup
		if err = tx.QueryRow(s.resourceGroupLockQuery(), "resource_group:"+resources.ResourceGroup).Scan(&raw); err != nil {
			return fmt.Errorf("资源组已删除，请重新读取")
		}
		if json.Unmarshal([]byte(raw), &current) != nil || current.Revision != resources.GroupRevision {
			return fmt.Errorf("资源组已变化，请重新确认")
		}
		if _, err = tx.Exec(`INSERT OR IGNORE INTO resource_applications(name) VALUES(?)`, service); err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, "application_resource_group:"+service, resources.ResourceGroup); err != nil {
			return err
		}
		return tx.Commit()
	}
	if _, err = tx.Exec(`DELETE FROM deploy_meta WHERE key=?`, "application_resource_group:"+service); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM application_environment_resources WHERE service=?`, service); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO resource_applications(name) VALUES(?)`, service); err != nil {
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
