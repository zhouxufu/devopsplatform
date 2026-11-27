package deploy

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// Nacos edits describe intent; environment credentials are resolved at preview.
type NacosChange struct {
	ResourceAlias string        `json:"resource_alias"`
	DataID        string        `json:"data_id"`
	Group         string        `json:"group"`
	Format        string        `json:"format"`
	Entries       []NacosEntry  `json:"entries"`
	Restore       *NacosRestore `json:"restore,omitempty"`
}
type NacosRestore struct {
	RunID    int64 `json:"run_id"`
	ChangeID int64 `json:"change_id"`
}
type NacosEntry struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Action string `json:"action"`
}

func normalizeNacosChange(c *Change) error {
	n := c.Nacos
	n.DataID, n.Group = strings.TrimSpace(n.DataID), strings.TrimSpace(n.Group)
	if c.Kind != "nacos" || c.Service == "" || !validResourceName(n.ResourceAlias) || n.Group == "" {
		return fmt.Errorf("Nacos 配置需要应用、资源别名和默认 Group；Data ID 可在环境资源绑定中指定")
	}
	if n.Format != "yaml" && n.Format != "properties" && n.Format != "json" {
		return fmt.Errorf("配置格式仅支持 yaml、properties 或 json")
	}
	if n.Restore != nil && (n.Restore.RunID <= 0 || n.Restore.ChangeID <= 0) {
		return fmt.Errorf("恢复草稿必须引用有效的原执行单与配置变更")
	}
	if len(n.DataID) > 255 || len(n.Group) > 255 || strings.ContainsAny(n.DataID+n.Group, "\r\n\x00") {
		return fmt.Errorf("Nacos 配置标识无效")
	}
	if len(n.Entries) == 0 || len(n.Entries) > 200 {
		return fmt.Errorf("每个配置需包含 1–200 个配置项")
	}
	seen := map[string]bool{}
	for i := range n.Entries {
		e := &n.Entries[i]
		e.Key = strings.TrimSpace(e.Key)
		if e.Key == "" {
			return fmt.Errorf("第 %d 个配置项：请填写配置项名称", i+1)
		}
		if !nacosKeyRE.MatchString(e.Key) {
			return fmt.Errorf("第 %d 个配置项：名称使用字母、数字、短横线、下划线和点", i+1)
		}
		if seen[e.Key] {
			return fmt.Errorf("第 %d 个配置项：名称重复，请修改或删除重复项", i+1)
		}
		if len(e.Value) > 65536 {
			return fmt.Errorf("第 %d 个配置项：期望值不能超过 64 KiB", i+1)
		}
		if e.Action != "set" && e.Action != "delete" {
			return fmt.Errorf("第 %d 个配置项：请选择设置值或删除项", i+1)
		}
		for key := range seen {
			if strings.HasPrefix(key, e.Key+".") || strings.HasPrefix(e.Key, key+".") {
				return fmt.Errorf("第 %d 个配置项：不能同时修改父路径和子路径", i+1)
			}
		}
		seen[e.Key] = true
		if e.Action == "delete" {
			e.Value = ""
		}
	}
	return nil
}

type WorkDelivery struct {
	ChangeDigests map[int64]string `json:"change_digests,omitempty"`
	Digest        string           `json:"digest"`
	Changes       []Change         `json:"changes"`
}
type WorkDeliverySave struct {
	tracking            *WorkLoop
	loopReceiptKey      string // server-only; committed with the same transaction
	loopCommitAttempted bool   // distinguish validation rollback from uncertain commit
	loopReceiptValue    string

	Digest  string   `json:"digest"`
	Changes []Change `json:"changes"`
	// Metadata readiness only; never performs environment preflight or execution.
	MarkReady bool `json:"mark_ready,omitempty"`
	// Extra links only apply to newly created changes. Existing associations are
	// preserved, so editing a shared implementation cannot silently unlink bugs.
	AlsoWorkItemIDs []int64 `json:"also_work_item_ids"`
}

func loadWorkDelivery(tx *deployTx, id int64) (*WorkDelivery, error) {
	rows, err := tx.Query(changeSelect+` WHERE status<>'archived' AND id IN (SELECT change_id FROM work_item_changes WHERE work_item_id=?) ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	out := &WorkDelivery{Changes: []Change{}}
	for rows.Next() {
		c, e := scanChange(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		out.Changes = append(out.Changes, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range out.Changes {
		c := &out.Changes[i]
		if err = loadChangePayload(tx, c); err != nil {
			return nil, fmt.Errorf("读取变更 #%d 的交付内容：%w", c.ID, err)
		}
		rs, e := tx.Query(workItemSelect+` WHERE id IN (SELECT work_item_id FROM work_item_changes WHERE change_id=?) ORDER BY id`, c.ID)
		if e != nil {
			return nil, e
		}
		c.WorkItems = []WorkItem{}
		for rs.Next() {
			w, e := scanWorkItem(rs)
			if e != nil {
				rs.Close()
				return nil, e
			}
			c.WorkItems = append(c.WorkItems, w)
		}
		e = rs.Err()
		rs.Close()
		if e != nil {
			return nil, e
		}
	}
	out.Digest = digestJSON(out.Changes)
	return out, nil
}
func (s *Store) WorkDelivery(id int64) (*WorkDelivery, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var exists int64
	if err = tx.QueryRow(`SELECT id FROM work_items WHERE id=?`, id).Scan(&exists); err != nil {
		return nil, fmt.Errorf("需求或 BUG 不存在")
	}
	out, err := loadWorkDelivery(tx, id)
	if err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

// SaveWorkDelivery updates only supplied items. Omission never deletes an
// implementation or a frozen version reference. All validation and writes are
// one metadata transaction; no external build/configuration/SQL is executed.
func (s *Store) SaveWorkDelivery(id int64, req *WorkDeliverySave, actor string, admin bool) error {
	if len(req.Changes) == 0 || len(req.Changes) > 100 {
		return fmt.Errorf("一次保存 1–100 项交付内容")
	}
	if len(req.AlsoWorkItemIDs) > 100 {
		return fmt.Errorf("关联需求过多")
	}
	for i := range req.Changes {
		c := &req.Changes[i]
		switch c.Kind {
		case "code", "artifact", "mysql", "nacos", "redis", "manual", "data-sync", xxlJobKind:
		default:
			return fmt.Errorf("第 %d 项：不支持的变更类型", i+1)
		}
		if err := normalizeChangeForSave(c, !req.MarkReady); err != nil {
			return fmt.Errorf("第 %d 项：%w", i+1, err)
		}
		if c.Kind == "code" && c.Service == "" {
			return fmt.Errorf("第 %d 项：请选择代码变更的应用", i+1)
		}
		if c.Kind == "code" && req.MarkReady && c.Reference == "" {
			return fmt.Errorf("第 %d 项：请补齐关联分支后再就绪", i+1)
		}
		if c.Kind == "mysql" && c.SQL == nil {
			return fmt.Errorf("第 %d 项：请填写 SQL", i+1)
		}
		if c.Kind == "redis" && c.Redis == nil {
			return fmt.Errorf("第 %d 项：请填写结构化 Redis 命令", i+1)
		}
		if c.Kind == "nacos" && c.Nacos == nil {
			return fmt.Errorf("第 %d 项：请填写 Nacos 配置项", i+1)
		}
		if req.MarkReady && c.Kind == "manual" && c.Reference == "" && c.Summary == "" {
			return fmt.Errorf("第 %d 项：请填写自定义处理记录", i+1)
		}
		if req.MarkReady && c.Kind == "artifact" && (c.Service == "" || c.Reference == "") {
			return fmt.Errorf("第 %d 项：制品引用需要应用和引用信息", i+1)
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ids := []int64{}
	seen := map[int64]bool{}
	if id > 0 {
		ids, seen[id] = append(ids, id), true
	}
	for _, wid := range req.AlsoWorkItemIDs {
		if wid < 1 {
			return fmt.Errorf("关联需求不存在")
		}
		if !seen[wid] {
			ids = append(ids, wid)
			seen[wid] = true
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, wid := range ids {
		q := `SELECT id FROM work_items WHERE id=?`
		if s.db.dialect == "mysql" {
			q += " FOR UPDATE"
		}
		var exists int64
		if err = tx.QueryRow(q, wid).Scan(&exists); err != nil {
			return fmt.Errorf("需求或 BUG 已删除，请刷新")
		}
	}
	// Lock changes before taking the optimistic snapshot, including status and
	// SQL payloads. Ready/freeze and edits cannot race this transaction.
	idQuery := `SELECT change_id FROM work_item_changes WHERE work_item_id=? ORDER BY change_id`
	if s.db.dialect == "mysql" {
		idQuery += " FOR UPDATE"
	}
	rows, err := tx.Query(idQuery, id)
	if err != nil {
		return err
	}
	cids := []int64{}
	for rows.Next() {
		var cid int64
		if err = rows.Scan(&cid); err != nil {
			rows.Close()
			return err
		}
		cids = append(cids, cid)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, cid := range cids {
		if _, err = tx.Exec(`UPDATE changes SET updated_at=updated_at WHERE id=?`, cid); err != nil {
			return err
		}
	}
	current, err := loadWorkDelivery(tx, id)
	if err != nil {
		return err
	}
	if id > 0 && (req.Digest == "" || req.Digest != current.Digest) {
		return fmt.Errorf("交付内容已被其他操作修改；草稿已保留，请重新载入后核对")
	}
	existing := map[int64]Change{}
	for _, c := range current.Changes {
		existing[c.ID] = c
	}
	resourceKinds := map[string]map[string]string{}
	services := []string{}
	for _, c := range req.Changes {
		if c.Service != "" && (c.SQL != nil || c.Nacos != nil || c.Redis != nil || c.XXLJob != nil) {
			if _, found := resourceKinds[c.Service]; !found {
				services = append(services, c.Service)
				resourceKinds[c.Service] = nil
			}
		}
	}
	sort.Strings(services)
	for _, service := range services {
		kinds, e := s.workDeliveryResourceKinds(tx, service)
		if e != nil {
			return e
		}
		resourceKinds[service] = kinds
	}
	submitted := map[int64]bool{}
	now := time.Now().Unix()
	for i := range req.Changes {
		c := &req.Changes[i]
		created := c.ID == 0
		if c.ID != 0 {
			old, ok := existing[c.ID]
			if !ok || submitted[c.ID] {
				return fmt.Errorf("第 %d 项：变更不属于当前需求或重复提交", i+1)
			}
			submitted[c.ID] = true
			if req.tracking != nil {
				if err := checkTrackedDraftEdit(tx, old, c, id); err != nil {
					return err
				}
			}
			if old.Status != ChangeDraft {
				return fmt.Errorf("第 %d 项：已就绪内容不可编辑，请新增补充内容", i+1)
			}
			if old.SQL != nil && old.SQL.DataSync != nil {
				return fmt.Errorf("第 %d 项：同步差异已冻结，请重新对比生成新计划", i+1)
			}
			if !admin && old.Owner != actor {
				return fmt.Errorf("第 %d 项：仅创建人或管理员可编辑", i+1)
			}
			if old.DeliveryOwnership != nil && old.DeliveryOwnership.OriginWorkItemID != id {
				return fmt.Errorf("第 %d 项：请在原始工作项维护此变更", i+1)
			}
			var touched int
			if err = tx.QueryRow(`SELECT (SELECT COUNT(*) FROM sql_execution_ledger WHERE change_id=?) + (SELECT COUNT(*) FROM nacos_execution_ledger WHERE change_id=?) + (SELECT COUNT(*) FROM deploy_meta WHERE key=?)`, c.ID, c.ID, xxlChangeTouchKey(c.ID)).Scan(&touched); err != nil {
				return err
			}
			if touched > 0 {
				return fmt.Errorf("第 %d 项：变更已有执行记录，请新增后续变更", i+1)
			}
			c.Owner, c.Status, c.CreatedAt = old.Owner, old.Status, old.CreatedAt
			if c.DeliveryOwnership == nil {
				c.DeliveryOwnership = old.DeliveryOwnership
			}
		} else {
			c.Owner, c.Status, c.CreatedAt = actor, ChangeDraft, now
		}
		if req.MarkReady {
			c.Status = ChangeReady
		}
		if c.Service != "" {
			var service string
			if err = tx.QueryRow(`SELECT name FROM services WHERE name=?`, c.Service).Scan(&service); err != nil {
				return fmt.Errorf("第 %d 项：应用不存在", i+1)
			}
		}
		alias, kind := "", ""
		if c.SQL != nil {
			alias, kind = c.SQL.ResourceAlias, "mysql"
		}
		if c.Nacos != nil {
			alias, kind = c.Nacos.ResourceAlias, "nacos"
		}
		if c.Redis != nil {
			alias, kind = c.Redis.ResourceAlias, "redis"
		}
		if c.XXLJob != nil {
			alias, kind = c.XXLJob.ResourceAlias, xxlJobKind
		}
		if alias != "" {
			if resourceKinds[c.Service][alias] != kind {
				return fmt.Errorf("第 %d 项：应用未声明该资源，请先配置资源", i+1)
			}
		}
		c.UpdatedAt = now
		if c.ID == 0 {
			res, e := tx.Exec(`INSERT INTO changes(kind,title,service,reference,summary,risk_level,rollback_plan,status,owner,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, c.Kind, c.Title, c.Service, c.Reference, c.Summary, c.RiskLevel, c.RollbackPlan, c.Status, c.Owner, now, now)
			if e != nil {
				return e
			}
			c.ID, e = res.LastInsertId()
			if e != nil {
				return e
			}
			for _, wid := range ids {
				if _, err = tx.Exec(`INSERT INTO work_item_changes(work_item_id,change_id) VALUES(?,?)`, wid, c.ID); err != nil {
					return err
				}
			}
		} else {
			_, err = tx.Exec(`UPDATE changes SET kind=?,title=?,service=?,reference=?,summary=?,risk_level=?,rollback_plan=?,status=?,updated_at=? WHERE id=?`, c.Kind, c.Title, c.Service, c.Reference, c.Summary, c.RiskLevel, c.RollbackPlan, c.Status, now, c.ID)
			if err != nil {
				return err
			}
		}
		if req.tracking != nil {
			req.tracking.DraftReceipt.Items = append(req.tracking.DraftReceipt.Items, DraftTrackingItem{ID: c.ID, Title: c.Title, Kind: c.Kind, Created: created})
			for _, dep := range c.Prerequisites {
				parent, ok := existing[dep.ChangeID]
				if !ok || !slices.Contains(req.tracking.Grant.Spec.Applications, parent.Service) {
					return fmt.Errorf("前置变更 #%d 超出当前工作项或授权应用", dep.ChangeID)
				}
			}
		}
		if err = saveSQLPayload(tx, c); err != nil {
			return err
		}
		if err = saveChangeOwnership(tx, c, id, actor); err != nil {
			return fmt.Errorf("第 %d 项：%w", i+1, err)
		}
		for _, wid := range ids {
			if err = recordResearchRelation(tx, wid, actor, "participant"); err != nil {
				return err
			}
			kind := "change-updated"
			if created {
				kind = "change-created"
			} else if req.MarkReady {
				kind = "change-ready"
			}
			raw, _ := json.Marshal(map[string]any{"change_id": c.ID, "kind": c.Kind, "title": c.Title, "status": c.Status})
			if _, err = tx.Exec(`INSERT INTO research_activity(work_item_id,actor,kind,content,created_at) VALUES(?,?,?,?,?)`, wid, actor, kind, string(raw), now); err != nil {
				return err
			}
		}
	}
	if req.tracking != nil {
		if err := commitDraftTracking(tx, req.tracking, req.Changes); err != nil {
			return err
		}
	}
	if req.loopReceiptKey != "" {
		if _, err = tx.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?)`, req.loopReceiptKey, req.loopReceiptValue); err != nil {
			return err
		}
	}
	req.loopCommitAttempted = true
	return tx.Commit()
}

// CreateChangeBatch is the same atomic draft transaction without a parent
// work item. Frozen data-sync plans must come from their comparison endpoint.
func (s *Store) CreateChangeBatch(req *WorkDeliverySave, actor string) error {
	for _, c := range req.Changes {
		if c.ID != 0 || c.Kind == "data-sync" || (c.SQL != nil && c.SQL.DataSync != nil) {
			return fmt.Errorf("批量新建仅接受新草稿；数据同步请从对比任务生成")
		}
	}
	return s.SaveWorkDelivery(0, req, actor, false)
}
