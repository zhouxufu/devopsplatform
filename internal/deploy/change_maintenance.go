package deploy

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type changeMaintenanceRequest struct {
	Service          string                     `json:"service"`
	Digest           string                     `json:"digest"`
	Action           string                     `json:"action"` // edit | archive | ready
	Change           *Change                    `json:"change,omitempty"`
	WorkItemIDs      *[]int64                   `json:"work_item_ids,omitempty"`
	OriginWorkItemID int64                      `json:"origin_work_item_id,omitempty"`
	Reason           string                     `json:"reason"`
	Patch            map[string]json.RawMessage `json:"patch,omitempty"`
}

func loadMaintenanceChange(tx *deployTx, id int64) (*Change, error) {
	c, err := scanChange(tx.QueryRow(changeSelect+` WHERE id=?`, id))
	if err != nil {
		return nil, fmt.Errorf("变更不存在")
	}
	if err = loadChangeOwnership(tx, &c); err != nil {
		return nil, err
	}
	var doc string
	err = tx.QueryRow(`SELECT document FROM change_payloads WHERE change_id=?`, id).Scan(&doc)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if doc != "" {
		var p struct {
			Prerequisites []ChangePrerequisite `json:"prerequisites,omitempty"`
			SQL           *SQLChange           `json:"sql"`
			Nacos         *NacosChange         `json:"nacos"`
			Redis         *RedisChange         `json:"redis"`
		}
		if json.Unmarshal([]byte(doc), &p) != nil {
			return nil, fmt.Errorf("变更载荷损坏")
		}
		c.SQL, c.Nacos, c.Redis = p.SQL, p.Nacos, p.Redis
		c.Prerequisites = p.Prerequisites
	}
	rows, err := tx.Query(workItemSelect+` WHERE id IN (SELECT work_item_id FROM work_item_changes WHERE change_id=?) ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		w, e := scanWorkItem(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		c.WorkItems = append(c.WorkItems, w)
	}
	err = rows.Err()
	rows.Close()
	return &c, err
}
func (s *Store) maintenanceState(id int64, service, actor string) (map[string]any, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	c, err := loadMaintenanceChange(tx, id)
	if err != nil {
		return nil, err
	}
	if c.Service != service || actor == "" || actor == "local" || c.Owner != actor {
		return nil, fmt.Errorf("只能维护本人指定应用的变更")
	}
	blockers, err := maintenanceActiveRuns(tx, id)
	if err != nil {
		return nil, err
	}
	return map[string]any{"change": c, "digest": digestJSON(c), "maintenance": map[string]any{
		"description_fields": []string{"title", "summary"}, "ready_code_description_keeps_ready": c.Kind == "code" && c.Status == ChangeReady,
		"content_edit_returns_draft": c.Status == ChangeReady, "blocking_runs": blockers,
		"guidance": "代码仅修订 title/summary 保留就绪；执行内容修改退回草稿重新校验。活动交付、执行台账和冻结版本仍检查，保留原编号与历史快照。",
	}}, nil
}

// A single transaction locks and rechecks the exact reviewed record, payload,
// ownership and links. Preview performs identical validation then rolls back.
func (s *Store) maintainChange(id int64, req changeMaintenanceRequest, actor string, save bool) (map[string]any, error) {
	if req.Digest == "" || len(req.Reason) < 1 || len(req.Reason) > 2000 || (req.Action != "edit" && req.Action != "archive" && req.Action != "ready") {
		return nil, fmt.Errorf("请提供原摘要、edit/archive/ready 动作和说明")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	out, err := s.maintainChangeTx(tx, id, req, actor, save)
	if err != nil {
		return nil, err
	}
	if save {
		err = tx.Commit()
	}
	return out, err
}

func (s *Store) maintainChangeTx(tx *deployTx, id int64, req changeMaintenanceRequest, actor string, save bool) (map[string]any, error) {
	if _, err := tx.Exec(`UPDATE changes SET updated_at=updated_at WHERE id=?`, id); err != nil {
		return nil, err
	}
	current, err := loadMaintenanceChange(tx, id)
	if err != nil {
		return nil, err
	}
	if current.Owner != actor || actor == "" || actor == "local" || current.Service != req.Service {
		return nil, fmt.Errorf("只能维护本人指定应用的变更")
	}
	if digestJSON(current) != req.Digest {
		return nil, fmt.Errorf("变更内容或状态已变化，请重新读取 get_change_maintenance 并预览")
	}
	if current.Status != ChangeDraft && (current.Status != ChangeReady || req.Action != "edit") {
		return nil, fmt.Errorf("当前状态 %s 不允许此维护操作；已归档记录不能重开，就绪记录只允许预览修订", current.Status)
	}
	descriptionOnly := current.Status == ChangeReady && current.Kind == "code" && maintenanceDescriptionPatch(req)
	if !descriptionOnly && current.SQL != nil && current.SQL.DataSync != nil {
		return nil, fmt.Errorf("数据同步计划已冻结，请重新生成")
	}
	var touched int
	if err = tx.QueryRow(`SELECT (SELECT COUNT(*) FROM sql_execution_ledger WHERE change_id=?) + (SELECT COUNT(*) FROM nacos_execution_ledger WHERE change_id=?) + (SELECT COUNT(*) FROM redis_execution_ledger WHERE change_id=?) + (SELECT COUNT(*) FROM release_version_changes WHERE change_id=?)`, id, id, id, id).Scan(&touched); err != nil {
		return nil, err
	}
	if !descriptionOnly && touched > 0 {
		return nil, fmt.Errorf("变更已有执行记录或已纳入版本，请新增后续变更或先移出草稿版本")
	}
	blockers, err := maintenanceActiveRuns(tx, id)
	if err != nil {
		return nil, err
	}
	if len(blockers) > 0 {
		return nil, fmt.Errorf("变更被活动交付执行单 #%d（%s）冻结；请打开 %s 核对并完成或终止原交付，再重新预览；归档个人关注不会解除冻结", blockers[0].RunID, blockers[0].Status, blockers[0].Path)
	}
	before := *current
	next := *current
	now := time.Now().Unix()
	if req.Action == "ready" {
		if next.Kind != "code" || len(next.WorkItems) != 0 || next.DeliveryOwnership != nil || !validPreparationBranch(next.Reference) || next.SQL != nil || next.Nacos != nil || next.Redis != nil {
			return nil, fmt.Errorf("请选择本人带有有效源分支的独立代码草稿")
		}
		if req.Change != nil || req.Patch != nil || req.WorkItemIDs != nil || req.OriginWorkItemID != 0 {
			return nil, fmt.Errorf("就绪不能同时修改变更内容或关联")
		}
		validated := next
		if err = normalizeChange(&validated); err != nil {
			return nil, err
		}
		next.Status = ChangeReady
	} else if req.Action == "archive" {
		if req.Change != nil || req.Patch != nil || req.WorkItemIDs != nil || req.OriginWorkItemID != 0 {
			return nil, fmt.Errorf("归档不接受内容/关联替换")
		}
		next.Status = "archived"
	} else if descriptionOnly {
		for key, value := range req.Patch {
			target := &next.Title
			if key == "summary" {
				target = &next.Summary
			}
			if string(value) == "null" || json.Unmarshal(value, target) != nil {
				return nil, fmt.Errorf("说明字段须为字符串")
			}
			*target = strings.TrimSpace(*target)
		}
		if next.Title == "" || len(next.Title) > 512 || len(next.Summary) > 65536 {
			return nil, fmt.Errorf("标题必填且最多512字节，说明最多64 KiB")
		}
	} else {
		if req.Change == nil && req.Patch == nil {
			return nil, fmt.Errorf("edit须提供明确修改字段")
		}
		if req.Change != nil && req.Patch != nil {
			return nil, fmt.Errorf("不能同时提交完整内容与字段补丁")
		}
		if req.Change != nil {
			next = *req.Change
		} else {
			raw, _ := json.Marshal(current)
			var merged map[string]json.RawMessage
			json.Unmarshal(raw, &merged)
			allowed := map[string]bool{"title": true, "reference": true, "summary": true, "risk_level": true, "rollback_plan": true, "sql": true, "nacos": true, "redis": true, "delivery_ownership": true}
			allowed["prerequisites"] = true
			if len(req.Patch) == 0 {
				return nil, fmt.Errorf("修改字段不能为空")
			}
			for key, value := range req.Patch {
				if !allowed[key] {
					return nil, fmt.Errorf("不允许修改字段 %s", key)
				}
				if string(value) == "null" {
					return nil, fmt.Errorf("修改字段不可为null")
				}
				merged[key] = value
			}
			raw, _ = json.Marshal(merged)
			if json.Unmarshal(raw, &next) != nil {
				return nil, fmt.Errorf("修改内容格式无效")
			}
		}
		next.ID, next.Owner, next.Status, next.CreatedAt, next.UpdatedAt = id, current.Owner, ChangeDraft, current.CreatedAt, now
		if next.Service != current.Service || next.Kind != current.Kind {
			return nil, fmt.Errorf("维护不允许替换应用或变更类型")
		}
		if next.SQL != nil && next.SQL.DataSync != nil || next.Nacos != nil && next.Nacos.Restore != nil {
			return nil, fmt.Errorf("不能注入冻结计划或恢复身份")
		}
		if err = normalizeChangeForSave(&next, true); err != nil {
			return nil, err
		}
		// Validate declared aliases inside this transaction, including shared sources.
		kinds, e := s.workDeliveryResourceKinds(tx, next.Service)
		if e != nil {
			return nil, e
		}
		alias, kind := "", ""
		if next.SQL != nil {
			alias, kind = next.SQL.ResourceAlias, "mysql"
		}
		if next.Nacos != nil {
			alias, kind = next.Nacos.ResourceAlias, "nacos"
		}
		if next.Redis != nil {
			alias, kind = next.Redis.ResourceAlias, "redis"
		}
		if alias != "" && kinds[alias] != kind {
			return nil, fmt.Errorf("应用未声明对应资源别名")
		}
		if next.DeliveryOwnership == nil {
			next.DeliveryOwnership = current.DeliveryOwnership
		}
		if current.DeliveryOwnership != nil && req.WorkItemIDs != nil {
			return nil, fmt.Errorf("已有来源归属不可覆盖关联；请调整self/follow/draft")
		}
		if req.WorkItemIDs != nil {
			if len(*req.WorkItemIDs) > 100 {
				return nil, fmt.Errorf("关联工作项超过100项")
			}
			if _, err = tx.Exec(`DELETE FROM work_item_changes WHERE change_id=?`, id); err != nil {
				return nil, err
			}
			seen := map[int64]bool{}
			for _, wid := range *req.WorkItemIDs {
				var exists int64
				if wid <= 0 || seen[wid] {
					return nil, fmt.Errorf("关联工作项无效或重复")
				}
				seen[wid] = true
				if err = tx.QueryRow(`SELECT id FROM work_items WHERE id=?`, wid).Scan(&exists); err != nil {
					return nil, fmt.Errorf("关联工作项不存在")
				}
				if _, err = tx.Exec(`INSERT INTO work_item_changes(work_item_id,change_id) VALUES(?,?)`, wid, id); err != nil {
					return nil, err
				}
			}
		}
		if next.DeliveryOwnership != nil {
			origin := req.OriginWorkItemID
			if current.DeliveryOwnership != nil {
				if origin != 0 && origin != current.DeliveryOwnership.OriginWorkItemID {
					return nil, fmt.Errorf("不能改变原始来源工作项")
				}
				origin = current.DeliveryOwnership.OriginWorkItemID
			}
			var linked int
			if err = tx.QueryRow(`SELECT COUNT(*) FROM work_item_changes WHERE change_id=? AND work_item_id=?`, id, origin).Scan(&linked); err != nil {
				return nil, err
			}
			if linked == 0 {
				return nil, fmt.Errorf("归属的原始工作项须已关联")
			}
			if err = saveChangeOwnership(tx, &next, origin, actor); err != nil {
				return nil, err
			}
		}
	}
	next.UpdatedAt = now
	if _, err = tx.Exec(`UPDATE changes SET title=?,reference=?,summary=?,risk_level=?,rollback_plan=?,status=?,updated_at=? WHERE id=?`, next.Title, next.Reference, next.Summary, next.RiskLevel, next.RollbackPlan, next.Status, now, id); err != nil {
		return nil, err
	}
	if req.Action == "edit" && !descriptionOnly {
		if err = saveSQLPayload(tx, &next); err != nil {
			return nil, err
		}
	}
	after, err := loadMaintenanceChange(tx, id)
	if err != nil {
		return nil, err
	}
	// Execution payloads stay out of audit; code-description revisions retain
	// their before/after text together with digests and the author reason.
	effect := "draft_updated"
	if req.Action == "ready" {
		effect = "marked_ready"
	} else if descriptionOnly {
		effect = "ready_description_revised"
	} else if before.Status == ChangeReady {
		effect = "returned_to_draft"
	} else if req.Action == "archive" {
		effect = "archived"
	}
	audit := map[string]any{"before_digest": req.Digest, "after_digest": digestJSON(after), "reason": req.Reason, "action": req.Action, "effect": effect, "before_status": before.Status, "after_status": after.Status}
	if descriptionOnly {
		audit["before_description"] = map[string]string{"title": before.Title, "summary": before.Summary}
		audit["after_description"] = map[string]string{"title": after.Title, "summary": after.Summary}
	}
	detail, _ := json.Marshal(audit)
	if _, err = tx.Exec(`INSERT INTO audit_events(actor,action,resource_type,resource_id,detail,created_at) VALUES(?,?,?,?,?,?)`, actor, "maintain-change", "change", fmt.Sprint(id), string(detail), now); err != nil {
		return nil, err
	}
	for _, w := range after.WorkItems {
		raw, _ := json.Marshal(map[string]any{"change_id": id, "kind": after.Kind, "title": after.Title, "status": after.Status})
		if _, err = tx.Exec(`INSERT INTO research_activity(work_item_id,actor,kind,content,created_at) VALUES(?,?,?,?,?)`, w.ID, actor, "change-"+req.Action, string(raw), now); err != nil {
			return nil, err
		}
	}
	out := map[string]any{"before": before, "after": after, "digest": digestJSON(after), "request": req, "no_execution": true, "saved": save, "effect": effect, "requires_ready_validation": before.Status == ChangeReady && after.Status == ChangeDraft}
	return out, nil
}
func registerChangeMaintenanceRoutes(mux *http.ServeMux, s *Store) {
	mux.HandleFunc("GET /api/changes/{id}/maintenance", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		v, e := s.maintenanceState(id, r.URL.Query().Get("service"), deliveryActor(r))
		if e != nil {
			deliveryWriteError(w, 403, e)
			return
		}
		deliveryJSON(w, v)
	})
	for _, suffix := range []string{"", "/preview"} {
		mux.HandleFunc("POST /api/changes/{id}/maintenance"+suffix, func(w http.ResponseWriter, r *http.Request) {
			id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
			var req changeMaintenanceRequest
			d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10))
			d.DisallowUnknownFields()
			if d.Decode(&req) != nil {
				deliveryWriteError(w, 400, "变更维护参数格式错误")
				return
			}
			v, e := s.maintainChange(id, req, deliveryActor(r), suffix == "")
			if e != nil {
				deliveryWriteError(w, 409, e)
				return
			}
			deliveryJSON(w, v)
		})
	}
}
