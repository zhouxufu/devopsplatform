package deploy

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func researchSchema(identity, doc string) []string {
	queries := []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS research_attention_states (actor VARCHAR(191) NOT NULL, item_key VARCHAR(191) NOT NULL, event_digest VARCHAR(80) NOT NULL, state VARCHAR(32) NOT NULL, revision BIGINT NOT NULL, document %s NOT NULL, reason TEXT NOT NULL, request_id VARCHAR(80) NOT NULL, updated_at BIGINT NOT NULL, PRIMARY KEY(actor,item_key,event_digest))`, doc),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS research_release_rules (connection_name VARCHAR(191) PRIMARY KEY, document %s NOT NULL, revision BIGINT NOT NULL, actor VARCHAR(191) NOT NULL, updated_at BIGINT NOT NULL)`, doc),
		`CREATE TABLE IF NOT EXISTS research_work_relations (work_item_id BIGINT NOT NULL, actor VARCHAR(191) NOT NULL, kind VARCHAR(32) NOT NULL, created_at BIGINT NOT NULL, PRIMARY KEY(work_item_id,actor,kind))`,
		`CREATE TABLE IF NOT EXISTS change_delivery_ownership (change_id BIGINT PRIMARY KEY, origin_work_item_id BIGINT NOT NULL, delivery_work_item_id BIGINT NOT NULL, mode VARCHAR(32) NOT NULL, actor VARCHAR(191) NOT NULL, updated_at BIGINT NOT NULL)`,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS research_exception_resolutions (task_id VARCHAR(191) NOT NULL, actor VARCHAR(191) NOT NULL, digest VARCHAR(80) NOT NULL, document %s NOT NULL, comment TEXT NOT NULL, created_at BIGINT NOT NULL, PRIMARY KEY(task_id,actor,digest))`, doc),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS research_issue_fields (work_item_id BIGINT PRIMARY KEY, document %s NOT NULL)`, doc),
		`CREATE TABLE IF NOT EXISTS research_flow_uses (owner VARCHAR(191) NOT NULL, request_id VARCHAR(64) NOT NULL, run_id BIGINT NOT NULL, request_digest VARCHAR(80) NOT NULL, PRIMARY KEY(owner,request_id))`,
		`CREATE TABLE IF NOT EXISTS research_work_metadata (work_item_id BIGINT PRIMARY KEY, release_required INTEGER, no_release_reason TEXT NOT NULL, revision BIGINT NOT NULL, actor VARCHAR(191) NOT NULL, updated_at BIGINT NOT NULL)`,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS research_activity (id %s, work_item_id BIGINT NOT NULL, actor VARCHAR(191) NOT NULL, kind VARCHAR(32) NOT NULL, content %s NOT NULL, created_at BIGINT NOT NULL)`, identity, doc),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS research_credentials (actor VARCHAR(191) NOT NULL, connection_name VARCHAR(191) NOT NULL, endpoint_digest VARCHAR(80) NOT NULL, account VARCHAR(191) NOT NULL, secret %s NOT NULL, PRIMARY KEY(actor,connection_name))`, doc),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS research_operations (operation_id VARCHAR(64) PRIMARY KEY, work_item_id BIGINT NOT NULL, actor VARCHAR(191) NOT NULL, request_digest VARCHAR(80) NOT NULL, state VARCHAR(32) NOT NULL, result %s NOT NULL, created_at BIGINT NOT NULL)`, doc),
	}
	if doc == "LONGTEXT" {
		for i := range queries {
			queries[i] = strings.ReplaceAll(queries[i], "actor VARCHAR(191)", "actor VARCHAR(191) COLLATE utf8mb4_bin")
			queries[i] = strings.ReplaceAll(queries[i], "owner VARCHAR(191)", "owner VARCHAR(191) COLLATE utf8mb4_bin")
		}
	}
	return queries
}

func (s *Store) researchIntentExpr(table string) string {
	manual := `(SELECT release_required FROM research_work_metadata WHERE work_item_id=` + table + `.id)`
	project := `COALESCE((SELECT json_extract(document,'$.project_id') FROM research_issue_fields WHERE work_item_id=` + table + `.id),'')`
	decision := `(SELECT json_extract(pr.value,'$.decision') FROM json_each(rr.document,'$.projects') pr WHERE json_extract(pr.value,'$.project_id')=` + project + ` LIMIT 1)`
	defaultDecision := `json_extract(rr.document,'$.default')`
	if s.Driver() == "mysql" {
		project = `COALESCE((SELECT JSON_UNQUOTE(JSON_EXTRACT(document,'$.project_id')) FROM research_issue_fields WHERE work_item_id=` + table + `.id),'')`
		decision = `(SELECT pr.decision FROM JSON_TABLE(rr.document,'$.projects[*]' COLUMNS(project_id VARCHAR(64) PATH '$.project_id',decision VARCHAR(32) PATH '$.decision')) pr WHERE pr.project_id=` + project + ` LIMIT 1)`
		defaultDecision = `JSON_UNQUOTE(JSON_EXTRACT(rr.document,'$.default'))`
	}
	rule := `(SELECT CASE WHEN json_extract(rr.document,'$.enabled')=true THEN CASE WHEN json_extract(rr.document,'$.policy') IS NOT NULL THEN CASE WHEN json_extract(rr.document,'$.policy.version')=1 THEN ` + s.portablePolicySQL(table) + ` ELSE NULL END WHEN ` + project + `<>'' THEN CASE COALESCE(` + decision + `,` + defaultDecision + `) WHEN 'independent' THEN 1 WHEN 'not_independent' THEN 0 ELSE NULL END ELSE NULL END ELSE NULL END FROM research_release_rules rr WHERE rr.connection_name=SUBSTR(` + table + `.source,8) AND ` + table + `.source LIKE 'zentao:%')`
	return `COALESCE(` + manual + `,` + rule + `)`
}
func (s *Store) researchIntentFilter(table, value string) string {
	q := s.researchIntentExpr(table)
	switch value {
	case "eligible":
		return `COALESCE(` + q + `,1)=1`
	case "yes":
		return q + `=1`
	case "no":
		return q + `=0`
	default:
		return q + ` IS NULL`
	}
}

type ResearchMetadata struct {
	ReleaseRequired *bool  `json:"release_required"`
	NoReleaseReason string `json:"no_release_reason"`
	Revision        int64  `json:"revision"`
	UseRule         bool   `json:"use_rule,omitempty"`
}

type researchQuerier interface {
	Query(string, ...any) (*sql.Rows, error)
}

func checkResearchPlanned(q researchQuerier, id int64) error {
	rows, err := q.Query(`SELECT rv.version,vs.document FROM release_versions rv JOIN release_version_scope vs ON vs.release_version_id=rv.id WHERE rv.status IN ('draft','ready')`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var version, raw string
		if err = rows.Scan(&version, &raw); err != nil {
			return err
		}
		var scope VersionScope
		if json.Unmarshal([]byte(raw), &scope) != nil {
			return fmt.Errorf("版本范围无法读取，禁止修改上线意图")
		}
		for _, wid := range scope.WorkItemIDs {
			if wid == id {
				return fmt.Errorf("已纳入版本 %s；请先由版本维护人移出，不能静默取消上线", version)
			}
		}
	}
	return rows.Err()
}

func validateResearchMetadata(v ResearchMetadata) error {
	if v.UseRule {
		return nil
	}
	if v.ReleaseRequired == nil {
		return fmt.Errorf("请选择是否需要上线")
	}
	if !*v.ReleaseRequired && strings.TrimSpace(v.NoReleaseReason) == "" {
		return fmt.Errorf("不需要上线时须填写原因")
	}
	if len(v.NoReleaseReason) > 4000 {
		return fmt.Errorf("原因过长")
	}
	return nil
}

func saveResearchMetadata(tx *deployTx, id int64, v ResearchMetadata, actor string) error {
	if err := validateResearchMetadata(v); err != nil {
		return err
	}
	if v.UseRule {
		v.NoReleaseReason = ""
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO research_work_metadata(work_item_id,release_required,no_release_reason,revision,actor,updated_at) VALUES(?,NULL,'',0,'',0)`, id); err != nil {
		return err
	}
	var required any
	if !v.UseRule {
		required = *v.ReleaseRequired
	}
	r, err := tx.Exec(`UPDATE research_work_metadata SET release_required=?,no_release_reason=?,revision=revision+1,actor=?,updated_at=? WHERE work_item_id=? AND revision=?`, required, strings.TrimSpace(v.NoReleaseReason), actor, time.Now().Unix(), id, v.Revision)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return fmt.Errorf("上线意图已变化，请重新读取")
	}
	return nil
}

func (s *Store) SetResearchMetadata(id int64, v ResearchMetadata, actor string) error {
	w := s.GetWorkItem(id)
	if w == nil {
		return fmt.Errorf("工作项不存在")
	}
	if v.UseRule || v.ReleaseRequired != nil && !*v.ReleaseRequired {
		if err := checkResearchPlanned(s.db, id); err != nil {
			return err
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE work_items SET updated_at=updated_at WHERE id=?`, id); err != nil {
		return err
	}
	if v.UseRule || v.ReleaseRequired != nil && !*v.ReleaseRequired {
		if err = checkResearchPlanned(tx, id); err != nil {
			return err
		}
	}
	if err = saveResearchMetadata(tx, id, v, actor); err != nil {
		return err
	}
	raw, _ := json.Marshal(v)
	if _, err = tx.Exec(`INSERT INTO research_activity(work_item_id,actor,kind,content,created_at) VALUES(?,?,'release-intent',?,?)`, id, actor, string(raw), time.Now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

type ResearchActivity struct {
	OperationID string `json:"operation_id,omitempty"`
	SourceActor string `json:"source_actor,omitempty"`
	ID          int64  `json:"id"`
	Actor       string `json:"actor"`
	Kind        string `json:"kind"`
	Content     string `json:"content"`
	At          int64  `json:"at"`
}

func (s *Store) ResearchActivities(id int64) ([]ResearchActivity, error) {
	rows, err := s.db.Query(`SELECT id,actor,kind,content,created_at FROM research_activity WHERE work_item_id=? ORDER BY id DESC LIMIT 200`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ResearchActivity{}
	for rows.Next() {
		var v ResearchActivity
		if err = rows.Scan(&v.ID, &v.Actor, &v.Kind, &v.Content, &v.At); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if err = s.linkResearchActivities(id, out); err != nil {
		return nil, err
	}
	return out, nil
}

func researchOperationActivityContent(request ResearchAction) string {
	content := request.Comment
	if request.Action == "comment" {
		for _, file := range request.Attachments {
			content += "\n\n![评论图片](attachment:" + file + ")"
		}
	}
	return content
}

// Preserve local audit rows. The presentation layer can reconcile their source
// counterpart by operation marker, without comparing text or wall-clock time.
func (s *Store) linkResearchActivities(id int64, activities []ResearchActivity) error {
	if len(activities) == 0 {
		return nil
	}
	rows, err := s.db.Query(`SELECT operation_id,actor,result,created_at FROM research_operations WHERE work_item_id=? AND state='success' ORDER BY created_at DESC,operation_id DESC LIMIT 400`, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	type completed struct {
		op    ResearchOperation
		actor string
		at    int64
	}
	operations := []completed{}
	for rows.Next() {
		var key, actor, raw string
		var at int64
		var op ResearchOperation
		if err = rows.Scan(&key, &actor, &raw, &at); err != nil {
			return err
		}
		if json.Unmarshal([]byte(raw), &op) != nil || op.ID != key || op.State != "success" || op.Request.OperationID != key || op.RemoteAccount == "" {
			continue
		}
		operations = append(operations, completed{op, actor, at})
	}
	if err = rows.Err(); err != nil {
		return err
	}
	used := map[int64]bool{}
	match := func(a ResearchActivity, o completed) bool {
		return a.Actor == o.actor && a.Kind == o.op.Request.Action && a.Content == researchOperationActivityContent(o.op.Request) && a.At >= o.at
	}
	bind := func(i int, o completed) {
		activities[i].OperationID = o.op.ID
		activities[i].SourceActor = o.op.RemoteAccount
		used[activities[i].ID] = true
	}
	for _, o := range operations {
		if o.op.ActivityID > 0 {
			for i, a := range activities {
				if a.ID == o.op.ActivityID && match(a, o) {
					bind(i, o)
					break
				}
			}
		}
	}
	// Old completed operations predate activity_id. Only associate an unambiguous
	// one-to-one audit/result pair; repeated identical actions remain visible.
	for _, o := range operations {
		if o.op.ActivityID != 0 {
			continue
		}
		candidate := -1
		count := 0
		for i, a := range activities {
			if !used[a.ID] && match(a, o) {
				candidate = i
				count++
			}
		}
		if count != 1 {
			continue
		}
		opCount := 0
		for _, other := range operations {
			if other.op.ActivityID == 0 && match(activities[candidate], other) {
				opCount++
			}
		}
		if opCount == 1 {
			bind(candidate, o)
		}
	}
	return nil
}

// AddResearchComment binds images and records the comment in one transaction.
// A failed ownership/size check must leave both the comment and bindings untouched.
func (s *Store) AddResearchComment(id int64, actor, comment string, attachments []string) error {
	if (strings.TrimSpace(comment) == "" && len(attachments) == 0) || len(comment) > 32000 || len(attachments) > 20 {
		return fmt.Errorf("评论不能为空；最多 32000 字节和 20 张图片")
	}
	item := s.GetWorkItem(id)
	if item == nil || item.Source != "manual" {
		return fmt.Errorf("仅自定义工作项可添加平台评论")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	seen := map[string]bool{}
	total := 0
	for _, file := range attachments {
		if !attachmentIDRE.MatchString(file) || seen[file] {
			return fmt.Errorf("附件编号无效或重复")
		}
		seen[file] = true
		var owner string
		var bound int64
		var size int
		q := `SELECT owner,work_item_id,size FROM work_item_attachments WHERE id=?`
		if tx.dialect == "mysql" {
			q += " FOR UPDATE"
		}
		if tx.QueryRow(q, file).Scan(&owner, &bound, &size) != nil || owner != actor || (bound != 0 && bound != id) {
			return fmt.Errorf("附件不属于当前用户或工作项")
		}
		total += size
		if total > 16<<20 {
			return fmt.Errorf("图片总大小超过 16 MiB")
		}
		comment += "\n\n![评论图片](attachment:" + file + ")"
	}
	if err = bindWorkAttachments(tx, &WorkItem{ID: id, Description: comment}, actor); err != nil {
		return err
	}
	if err = recordResearchRelation(tx, id, actor, "participant"); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO research_activity(work_item_id,actor,kind,content,created_at) VALUES(?,?,'comment',?,?)`, id, actor, comment, time.Now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}
