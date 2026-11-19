package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	policy "logplatform/pkg/releasepolicy"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// These are source-field rules, not Bug copies. Manual overrides live in the
// existing CAS metadata table and always take precedence over a rule read.
type ResearchProjectRule struct {
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	Decision  string `json:"decision"`
}
type ResearchReleaseRules struct {
	Provider        string                `json:"provider,omitempty"`
	ProviderVersion string                `json:"provider_version,omitempty"`
	Policy          *policy.Policy        `json:"policy,omitempty"`
	Enabled         bool                  `json:"enabled"`
	Default         string                `json:"default"`
	Projects        []ResearchProjectRule `json:"projects"`
	Revision        int64                 `json:"revision"`
}

func (s *Store) researchReleaseRules(name string) (ResearchReleaseRules, error) {
	v := ResearchReleaseRules{Default: "unknown", Projects: []ResearchProjectRule{}}
	var raw string
	err := s.db.QueryRow(`SELECT document,revision FROM research_release_rules WHERE connection_name=?`, name).Scan(&raw, &v.Revision)
	if err == sql.ErrNoRows {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	if err = json.Unmarshal([]byte(raw), &v); err != nil {
		return v, fmt.Errorf("上线规则读取失败，未推断为无需上线")
	}
	if v.Policy != nil {
		if err := policy.Validate(*v.Policy); err != nil {
			return v, err
		}
	}
	return v, nil
}
func releaseDecision(value string) *bool {
	var result bool
	switch value {
	case "independent":
		result = true
	case "not_independent":
		result = false
	default:
		return nil
	}
	return &result
}
func applyResearchReleaseRules(w *WorkItem, rules ResearchReleaseRules, project string) {
	if w.ReleaseRequired != nil {
		w.ReleaseOrigin = "manual"
		w.ReleaseReason = "人工调整"
		if w.NoReleaseReason != "" {
			w.ReleaseReason = w.NoReleaseReason
		}
		return
	}
	w.ReleaseOrigin, w.ReleaseReason, w.RuleRevision = "unknown", "尚未配置或命中上线规则", rules.Revision
	if !rules.Enabled {
		return
	}
	if rules.Policy != nil {
		result := policy.Evaluate(*rules.Policy, researchPolicyValues(w))
		w.ReleaseRequired = releaseDecision(result.Decision)
		w.ReleaseOrigin, w.ReleaseReason = "rule", result.Reason
		return
	}
	// A missing source field is not evidence for a negative default.
	if project == "" {
		w.ReleaseReason = "来源未返回所属项目，待判定"
		return
	}
	decision, reason := rules.Default, "未命中项目规则，使用默认结果"
	for _, r := range rules.Projects {
		if r.ProjectID == project {
			decision = r.Decision
			reason = "所属项目 #" + project
			if r.Name != "" {
				reason += " · " + r.Name
			}
			break
		}
	}
	w.ReleaseRequired = releaseDecision(decision)
	w.ReleaseOrigin, w.ReleaseReason = "rule", reason
}
func (s *Store) decorateResearchPolicy(w *WorkItem) error {
	if !strings.HasPrefix(w.Source, "zentao:") {
		if w.ReleaseRequired != nil {
			w.ReleaseOrigin = "manual"
		}
		return nil
	}
	rules, err := s.researchReleaseRules(strings.TrimPrefix(w.Source, "zentao:"))
	if err != nil {
		return err
	}
	applyResearchReleaseRules(w, rules, w.ProjectID)
	return nil
}

func researchRelationClause(table, relation string) string {
	base := `SELECT 1 FROM research_work_relations rr WHERE rr.actor=? AND rr.work_item_id=` + table + `.id`
	switch relation {
	case "participating":
		return `EXISTS(` + base + ` AND rr.kind='participant')`
	case "following":
		return `EXISTS(` + base + ` AND rr.kind='follower')`
	case "related":
		return `EXISTS(SELECT 1 FROM research_work_relations rr WHERE rr.actor=? AND (rr.work_item_id=` + table + `.id OR EXISTS(SELECT 1 FROM change_delivery_ownership co WHERE co.origin_work_item_id=rr.work_item_id AND co.mode='follow' AND co.delivery_work_item_id=` + table + `.id)))`
	}
	return ""
}
func recordResearchRelation(tx *deployTx, id int64, actor, kind string) error {
	if actor == "" || actor == "local" {
		return nil
	}
	_, err := tx.Exec(`INSERT OR IGNORE INTO research_work_relations(work_item_id,actor,kind,created_at) VALUES(?,?,?,?)`, id, actor, kind, time.Now().Unix())
	return err
}
func (s *Store) decorateResearchRelations(items []researchLiveItem, actor string) error {
	ids, args := []string{}, []any{actor}
	for _, item := range items {
		if item.ID > 0 {
			ids = append(ids, "?")
			args = append(args, item.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.db.Query(`SELECT work_item_id,kind FROM research_work_relations WHERE actor=? AND work_item_id IN (`+strings.Join(ids, ",")+`)`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var kind string
		if err = rows.Scan(&id, &kind); err != nil {
			return err
		}
		for i := range items {
			if items[i].ID == id {
				if kind == "follower" {
					items[i].Following = true
				}
				if kind == "participant" {
					items[i].Participating = true
				}
			}
		}
	}
	return rows.Err()
}

type ChangeDeliveryOwnership struct {
	Mode             string `json:"mode"` // self | follow | draft
	OriginWorkItemID int64  `json:"origin_work_item_id"`
	WorkItemID       int64  `json:"work_item_id"`
}

func loadChangeOwnership(q interface{ QueryRow(string, ...any) *sql.Row }, c *Change) error {
	v := &ChangeDeliveryOwnership{}
	err := q.QueryRow(`SELECT mode,origin_work_item_id,delivery_work_item_id FROM change_delivery_ownership WHERE change_id=?`, c.ID).Scan(&v.Mode, &v.OriginWorkItemID, &v.WorkItemID)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	c.DeliveryOwnership = v
	return nil
}

// Query builders share this condition: a source Bug keeps its provenance, but
// selecting it must not pull changes already assigned to another main item.
func deliveryOwnedChanges(table string) string {
	return `SELECT wc.change_id FROM work_item_changes wc LEFT JOIN change_delivery_ownership co ON co.change_id=wc.change_id WHERE wc.work_item_id=` + table + ` AND wc.change_id IN (SELECT id FROM changes WHERE status<>'archived') AND (co.change_id IS NULL OR (co.mode='draft' AND co.origin_work_item_id=wc.work_item_id) OR co.delivery_work_item_id=wc.work_item_id)`
}
func saveChangeOwnership(tx *deployTx, c *Change, origin int64, actor string) error {
	if origin == 0 {
		if c.DeliveryOwnership != nil {
			return fmt.Errorf("独立变更请先关联来源工作项")
		}
		return nil
	}
	v := c.DeliveryOwnership
	if v == nil {
		return nil
	} // Existing records retain their legacy associations.
	var previousOrigin int64
	err := tx.QueryRow(`SELECT origin_work_item_id FROM change_delivery_ownership WHERE change_id=?`, c.ID).Scan(&previousOrigin)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if previousOrigin != 0 && previousOrigin != origin {
		return fmt.Errorf("只能在原始工作项维护交付归属")
	}
	v.OriginWorkItemID = origin
	switch v.Mode {
	case "self":
		v.WorkItemID = origin
	case "draft":
		v.WorkItemID = 0
	case "follow":
		if v.WorkItemID <= 0 || v.WorkItemID == origin {
			return fmt.Errorf("请选择其他主 Bug 或需求")
		}
	default:
		return fmt.Errorf("请选择自行交付、随其他工作项交付或先保存草稿")
	}
	if v.WorkItemID > 0 {
		var id int64
		if err = tx.QueryRow(`SELECT id FROM work_items WHERE id=?`, v.WorkItemID).Scan(&id); err != nil {
			return fmt.Errorf("交付归属工作项不存在")
		}
	}
	var frozen int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM release_version_changes vc JOIN release_versions v ON v.id=vc.release_version_id WHERE vc.change_id=? AND v.status<>'draft'`, c.ID).Scan(&frozen); err != nil {
		return err
	}
	if frozen > 0 {
		return fmt.Errorf("变更已纳入冻结版本，不能修改交付归属；请新增后续变更")
	}
	if previousOrigin == 0 {
		var planned int
		if err = tx.QueryRow(`SELECT COUNT(*) FROM release_version_changes WHERE change_id=?`, c.ID).Scan(&planned); err != nil {
			return err
		}
		if planned > 0 {
			return fmt.Errorf("变更已纳入版本，请先从草稿版本移出再设置交付归属")
		}
	}
	// Do not rewrite a draft version under its owner's feet either. The owner
	// explicitly adjusts/re-saves its range and receives the new aggregation.
	if previousOrigin != 0 {
		var mode string
		var target int64
		if err = tx.QueryRow(`SELECT mode,delivery_work_item_id FROM change_delivery_ownership WHERE change_id=?`, c.ID).Scan(&mode, &target); err != nil {
			return err
		}
		if mode == v.Mode && target == v.WorkItemID {
			return nil // No ownership change; content saves have their own activity.
		}
		if mode != v.Mode || target != v.WorkItemID {
			var planned int
			if err = tx.QueryRow(`SELECT COUNT(*) FROM release_version_changes WHERE change_id=?`, c.ID).Scan(&planned); err != nil {
				return err
			}
			if planned > 0 {
				return fmt.Errorf("变更已纳入版本，请先在草稿版本移出后调整归属")
			}
			if target > 0 && target != origin {
				if _, err = tx.Exec(`DELETE FROM work_item_changes WHERE work_item_id=? AND change_id=?`, target, c.ID); err != nil {
					return err
				}
			}
		}
	}
	_, err = tx.Exec(`INSERT INTO change_delivery_ownership(change_id,origin_work_item_id,delivery_work_item_id,mode,actor,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(change_id) DO UPDATE SET delivery_work_item_id=excluded.delivery_work_item_id,mode=excluded.mode,actor=excluded.actor,updated_at=excluded.updated_at`, c.ID, origin, v.WorkItemID, v.Mode, actor, time.Now().Unix())
	if err != nil {
		return err
	}
	if v.Mode == "follow" {
		_, err = tx.Exec(`INSERT OR IGNORE INTO work_item_changes(work_item_id,change_id) VALUES(?,?)`, v.WorkItemID, c.ID)
	}
	if err == nil && v.WorkItemID > 0 {
		err = recordResearchRelation(tx, v.WorkItemID, actor, "participant")
	}
	if err == nil {
		raw, _ := json.Marshal(map[string]any{"change_id": c.ID, "kind": c.Kind, "title": c.Title, "mode": v.Mode, "work_item_id": v.WorkItemID})
		_, err = tx.Exec(`INSERT INTO research_activity(work_item_id,actor,kind,content,created_at) VALUES(?,?,'delivery-ownership',?,?)`, origin, actor, string(raw), time.Now().Unix())
	}
	return err
}

func (s *Store) backfillResearchParticipants() error {
	for _, idx := range []struct{ name, table, columns string }{
		{"idx_research_relations_actor", "research_work_relations", "actor,kind,work_item_id"},
		{"idx_change_delivery_origin", "change_delivery_ownership", "origin_work_item_id,mode,delivery_work_item_id"},
		{"idx_change_delivery_target", "change_delivery_ownership", "delivery_work_item_id"},
	} {
		statement := "CREATE INDEX IF NOT EXISTS " + idx.name + " ON " + idx.table + "(" + idx.columns + ")"
		if s.Driver() == "mysql" {
			var count int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name=? AND index_name=?`, idx.table, idx.name).Scan(&count); err != nil {
				return err
			}
			if count > 0 {
				continue
			}
			statement = "CREATE INDEX " + idx.name + " ON " + idx.table + "(" + idx.columns + ")"
		}
		if _, err := s.db.Exec(statement); err != nil {
			return err
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var done string
	if err = tx.QueryRow(`SELECT value FROM deploy_meta WHERE key='research_participants_v1'`).Scan(&done); err == nil && done == "1" {
		return nil
	} else if err != nil && err != sql.ErrNoRows {
		return err
	}
	for _, query := range []string{
		`INSERT OR IGNORE INTO research_work_relations(work_item_id,actor,kind,created_at) SELECT wc.work_item_id,c.owner,'participant',c.created_at FROM work_item_changes wc JOIN changes c ON c.id=wc.change_id WHERE c.owner<>'' AND c.owner<>'local'`,
		`INSERT OR IGNORE INTO research_work_relations(work_item_id,actor,kind,created_at) SELECT work_item_id,actor,'participant',created_at FROM research_activity WHERE kind IN ('resolve','edit','comment') AND actor<>'' AND actor<>'local'`,
		`INSERT OR IGNORE INTO research_work_relations(work_item_id,actor,kind,created_at) SELECT id,owner,'participant',created_at FROM work_items WHERE source='manual' AND owner<>'' AND owner<>'local'`,
	} {
		if _, err := tx.Exec(query); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`INSERT INTO deploy_meta(key,value) VALUES('research_participants_v1','1') ON CONFLICT(key) DO UPDATE SET value=excluded.value`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) canReadResearchItem(ctx context.Context, actor string, item *WorkItem) error {
	if item == nil {
		return fmt.Errorf("工作项不存在")
	}
	if !strings.HasPrefix(item.Source, "zentao:") {
		return nil
	}
	z, c, _, err := s.liveReadClient(ctx, strings.TrimPrefix(item.Source, "zentao:"), actor, false)
	if err != nil {
		return err
	}
	remote, err := z.researchReadItem(ctx, item, "view")
	if err != nil {
		return err
	}
	product, _ := strconv.Atoi(researchValue(remote.Bug, "product"))
	if !liveProductAllowed(c, product) {
		return fmt.Errorf("Bug 不在允许的产品范围")
	}
	return nil
}
func registerResearchDeliveryPolicy(mux *http.ServeMux, s *Store) {
	registerResearchRuleEditor(mux, s)
	mux.HandleFunc("GET /api/research/work-items/{id}/tracking", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err := s.canReadResearchItem(ctx, deliveryActor(r), s.GetWorkItem(id)); err != nil {
			deliveryWriteError(w, 403, err)
			return
		}
		out, err := s.researchWorkTracking(id)
		if err != nil {
			deliveryWriteError(w, 500, err)
			return
		}
		deliveryJSON(w, out)
	})
	mux.HandleFunc("GET /api/research/connections/{name}/release-rules", func(w http.ResponseWriter, r *http.Request) {
		if c := s.GetConnection(r.PathValue("name")); c == nil || c.Kind != "zentao" {
			deliveryWriteError(w, 404, "来源连接不存在")
			return
		}
		v, err := s.researchReleaseRules(r.PathValue("name"))
		if err != nil {
			deliveryWriteError(w, 500, err)
			return
		}
		if v.Policy == nil {
			p := portableResearchRules(v)
			v.Policy = &p
			v.Provider = "builtin"
			v.ProviderVersion = "1"
		}
		deliveryJSON(w, v)
	})
	mux.HandleFunc("PUT /api/research/connections/{name}/release-rules", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Role") != "admin" {
			deliveryWriteError(w, 403, "仅管理员可维护上线识别规则")
			return
		}
		name := r.PathValue("name")
		if c := s.GetConnection(name); c == nil || c.Kind != "zentao" {
			deliveryWriteError(w, 404, "来源连接不存在")
			return
		}
		var v ResearchReleaseRules
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&v) != nil || len(v.Projects) > 100 {
			deliveryWriteError(w, 400, "上线规则格式无效")
			return
		}
		if v.Policy != nil {
			if err := policy.Validate(*v.Policy); err != nil {
				deliveryWriteError(w, 400, err)
				return
			}
			v.Default = v.Policy.Default
			v.Projects = []ResearchProjectRule{}
			if v.Provider == "" {
				v.Provider = "builtin"
			}
			if v.Provider == "builtin" {
				v.ProviderVersion = "1"
			}
		}
		valid := func(d string) bool { return d == "unknown" || d == "independent" || d == "not_independent" }
		if !valid(v.Default) {
			deliveryWriteError(w, 400, "请选择未命中规则时的默认结果")
			return
		}
		seen := map[string]bool{}
		for _, p := range v.Projects {
			id, err := strconv.ParseInt(p.ProjectID, 10, 64)
			if err != nil || id < 0 || strconv.FormatInt(id, 10) != p.ProjectID || seen[p.ProjectID] || !valid(p.Decision) || len(p.Name) > 256 {
				deliveryWriteError(w, 400, "项目 ID 无效、重复或规则结果无效")
				return
			}
			seen[p.ProjectID] = true
		}
		tx, err := s.db.Begin()
		if err != nil {
			deliveryWriteError(w, 500, err)
			return
		}
		defer tx.Rollback()
		_, err = tx.Exec(`INSERT OR IGNORE INTO research_release_rules(connection_name,document,revision,actor,updated_at) VALUES(?,'{}',0,'',0)`, name)
		if err == nil {
			v.Revision++
			raw, _ := json.Marshal(v)
			v.Revision--
			var result sql.Result
			result, err = tx.Exec(`UPDATE research_release_rules SET document=?,revision=revision+1,actor=?,updated_at=? WHERE connection_name=? AND revision=?`, string(raw), deliveryActor(r), time.Now().Unix(), name, v.Revision)
			if err == nil {
				n, _ := result.RowsAffected()
				if n != 1 {
					err = fmt.Errorf("规则已被其他人修改，请重新读取")
				}
			}
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			deliveryWriteError(w, 409, err)
			return
		}
		s.Audit(deliveryActor(r), "source-release-rules", "connection", name, map[string]any{"revision": v.Revision + 1})
		v.Revision++
		deliveryJSON(w, v)
	})
	mux.HandleFunc("PUT /api/research/work-items/{id}/follow", func(w http.ResponseWriter, r *http.Request) {
		actor := deliveryActor(r)
		if actor == "" || actor == "local" {
			deliveryWriteError(w, 403, "请登录后关注工作项")
			return
		}
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		item := s.GetWorkItem(id)
		var req struct {
			Following *bool `json:"following"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req) != nil || req.Following == nil {
			deliveryWriteError(w, 400, "请选择关注或取消关注")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err := s.canReadResearchItem(ctx, actor, item); err != nil {
			deliveryWriteError(w, 403, err)
			return
		}
		tx, err := s.db.Begin()
		if err != nil {
			deliveryWriteError(w, 500, err)
			return
		}
		defer tx.Rollback()
		if *req.Following {
			err = recordResearchRelation(tx, id, actor, "follower")
		} else {
			_, err = tx.Exec(`DELETE FROM research_work_relations WHERE work_item_id=? AND actor=? AND kind='follower'`, id, actor)
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			deliveryWriteError(w, 500, err)
			return
		}
		deliveryJSON(w, map[string]bool{"following": *req.Following})
	})
}

// Read only frozen references, never infer historical delivery from today's
// version membership, follower list, or source Bug status.
func (s *Store) researchWorkTracking(id int64) (map[string]any, error) {
	workJSON := `json_each(COALESCE(json_extract(dr.plan,'$.scope.work_items'),json_extract(dr.plan,'$.version.work_items'),'[]')) wi`
	changeJSON := `json_each(COALESCE(json_extract(dr.plan,'$.scope.changes'),json_extract(dr.plan,'$.version.changes'),'[]')) ci, json_each(ci.value,'$.work_items') cw`
	workID, changeWorkID := `json_extract(wi.value,'$.id')`, `json_extract(cw.value,'$.id')`
	if s.Driver() == "mysql" {
		workJSON = `JSON_TABLE(COALESCE(JSON_EXTRACT(dr.plan,'$.scope.work_items'),JSON_EXTRACT(dr.plan,'$.version.work_items'),JSON_ARRAY()),'$[*]' COLUMNS(work_id BIGINT PATH '$.id')) wi`
		changeJSON = `JSON_TABLE(COALESCE(JSON_EXTRACT(dr.plan,'$.scope.changes'),JSON_EXTRACT(dr.plan,'$.version.changes'),JSON_ARRAY()),'$[*].work_items[*]' COLUMNS(work_id BIGINT PATH '$.id')) cw`
		workID, changeWorkID = "wi.work_id", "cw.work_id"
	}
	rows, err := s.db.Query(`SELECT dr.id FROM delivery_runs dr WHERE EXISTS(SELECT 1 FROM `+workJSON+` WHERE `+workID+`=?) OR EXISTS(SELECT 1 FROM `+changeJSON+` WHERE `+changeWorkID+`=?) ORDER BY dr.id DESC LIMIT 101`, id, id)
	if err != nil {
		return nil, err
	}
	ids := []int64{}
	for rows.Next() {
		var run int64
		if err = rows.Scan(&run); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, run)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	more := len(ids) > 100
	if more {
		ids = ids[:100]
	}
	items := []ResearchTask{}
	preparations := map[int64]bool{}
	for _, runID := range ids {
		if run := s.DeliveryRun(runID); run != nil {
			items = append(items, s.researchSummary(run))
			preparations[runID] = run.Plan.PreparationRequest != nil
		}
	}
	// Current draft planning is separate from immutable execution history.
	versions := []map[string]any{}
	rows, err = s.db.Query(`SELECT DISTINCT v.id,v.version,v.status FROM release_versions v JOIN release_version_changes vc ON vc.release_version_id=v.id JOIN work_item_changes wc ON wc.change_id=vc.change_id WHERE wc.work_item_id=? ORDER BY v.id DESC LIMIT 100`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var versionID int64
		var version, state string
		if err = rows.Scan(&versionID, &version, &state); err != nil {
			return nil, err
		}
		versions = append(versions, map[string]any{"id": versionID, "version": version, "status": state})
	}
	return map[string]any{"deliveries": items, "preparations": preparations, "versions": versions, "has_more": more}, rows.Err()
}

func (s *Store) researchNodePeople(n DeliveryNode, w WorkItem, scope *DeliveryScope, accounts map[string]string) ([]string, error) {
	if n.AssigneeSource == "work_item_participants" {
		rows, err := s.db.Query(`SELECT actor FROM research_work_relations WHERE work_item_id=? AND kind='participant' ORDER BY actor`, w.ID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		people := []string{}
		for rows.Next() {
			var actor string
			if err = rows.Scan(&actor); err != nil {
				return nil, err
			}
			people = append(people, actor)
		}
		return people, rows.Err()
	}
	if n.AssigneeSource == "change_submitters" {
		people := []string{}
		seen := map[string]bool{}
		for _, c := range scope.Changes {
			if n.Service != "" && c.Service != n.Service {
				continue
			}
			matches := false
			for _, cw := range c.WorkItems {
				matches = matches || cw.ID == w.ID
			}
			if matches && c.Owner != "" && !seen[c.Owner] {
				seen[c.Owner] = true
				people = append(people, c.Owner)
			}
		}
		return people, nil
	}
	person := w.ResolvedBy
	if n.AssigneeSource == "work_item_assignees" {
		person = w.AssignedTo
	}
	if w.Source == "manual" {
		person = w.Owner
	} else {
		person = accounts[w.Source+"\x00"+person]
	}
	return []string{person}, nil
}
