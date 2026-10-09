package deploy

import (
	"encoding/json"
	"fmt"
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
	if c.Kind != "nacos" || c.Service == "" || !validResourceName(n.ResourceAlias) || n.DataID == "" || n.Group == "" {
		return fmt.Errorf("Nacos 配置需要应用、资源别名、Data ID 和 Group")
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
		if !nacosKeyRE.MatchString(e.Key) || seen[e.Key] || len(e.Value) > 65536 || (e.Action != "set" && e.Action != "delete") {
			return fmt.Errorf("配置项不能为空、不能重复；操作仅支持 set 或 delete")
		}
		for key := range seen {
			if strings.HasPrefix(key, e.Key+".") || strings.HasPrefix(e.Key, key+".") {
				return fmt.Errorf("同一变更不能同时修改父路径和子路径")
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
	Digest  string   `json:"digest"`
	Changes []Change `json:"changes"`
}
type WorkDeliverySave struct {
	Digest  string   `json:"digest"`
	Changes []Change `json:"changes"`
	// Extra links only apply to newly created changes. Existing associations are
	// preserved, so editing a shared implementation cannot silently unlink bugs.
	AlsoWorkItemIDs []int64 `json:"also_work_item_ids"`
}

func loadWorkDelivery(tx *deployTx, id int64) (*WorkDelivery, error) {
	rows, err := tx.Query(changeSelect+` WHERE id IN (SELECT change_id FROM work_item_changes WHERE work_item_id=?) ORDER BY id`, id)
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
		var doc string
		if e := tx.QueryRow(`SELECT document FROM change_payloads WHERE change_id=?`, c.ID).Scan(&doc); e == nil {
			var p struct {
				SQL   *SQLChange   `json:"sql"`
				Nacos *NacosChange `json:"nacos"`
				Redis *RedisChange `json:"redis"`
			}
			if e = json.Unmarshal([]byte(doc), &p); e != nil {
				return nil, e
			}
			c.SQL, c.Nacos = p.SQL, p.Nacos
			c.Redis = p.Redis
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
		case "code", "artifact", "mysql", "nacos", "redis", "manual", "data-sync":
		default:
			return fmt.Errorf("第 %d 项：不支持的变更类型", i+1)
		}
		if err := normalizeChange(c); err != nil {
			return fmt.Errorf("第 %d 项：%w", i+1, err)
		}
		if c.Kind == "code" && (c.Service == "" || c.Reference == "") {
			return fmt.Errorf("第 %d 项：代码需要应用和分支", i+1)
		}
		if c.Kind == "mysql" && c.SQL == nil {
			return fmt.Errorf("第 %d 项：请填写 SQL", i+1)
		}
		if c.Kind == "redis" && c.Redis == nil {
			return fmt.Errorf("第 %d 项：请填写结构化 Redis 命令", i+1)
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ids := []int64{id}
	seen := map[int64]bool{id: true}
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
	if req.Digest == "" || req.Digest != current.Digest {
		return fmt.Errorf("交付内容已被其他操作修改；草稿已保留，请重新载入后核对")
	}
	existing := map[int64]Change{}
	for _, c := range current.Changes {
		existing[c.ID] = c
	}
	submitted := map[int64]bool{}
	now := time.Now().Unix()
	for i := range req.Changes {
		c := &req.Changes[i]
		if c.ID != 0 {
			old, ok := existing[c.ID]
			if !ok || submitted[c.ID] {
				return fmt.Errorf("第 %d 项：变更不属于当前需求或重复提交", i+1)
			}
			submitted[c.ID] = true
			if old.Status != ChangeDraft {
				return fmt.Errorf("第 %d 项：已就绪内容不可编辑，请新增补充内容", i+1)
			}
			if old.SQL != nil && old.SQL.DataSync != nil {
				return fmt.Errorf("第 %d 项：同步差异已冻结，请重新对比生成新计划", i+1)
			}
			if !admin && old.Owner != actor {
				return fmt.Errorf("第 %d 项：仅创建人或管理员可编辑", i+1)
			}
			c.Owner, c.Status, c.CreatedAt = old.Owner, old.Status, old.CreatedAt
		} else {
			c.Owner, c.Status, c.CreatedAt = actor, ChangeDraft, now
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
		if alias != "" {
			var actual string
			if err = tx.QueryRow(`SELECT kind FROM application_resource_requirements WHERE service=? AND alias=?`, c.Service, alias).Scan(&actual); err != nil || actual != kind {
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
			_, err = tx.Exec(`UPDATE changes SET kind=?,title=?,service=?,reference=?,summary=?,risk_level=?,rollback_plan=?,updated_at=? WHERE id=?`, c.Kind, c.Title, c.Service, c.Reference, c.Summary, c.RiskLevel, c.RollbackPlan, now, c.ID)
			if err != nil {
				return err
			}
		}
		if err = saveSQLPayload(tx, c); err != nil {
			return err
		}
	}
	return tx.Commit()
}
