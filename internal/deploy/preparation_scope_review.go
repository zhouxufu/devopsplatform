package deploy

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

type PreparationScopeItem struct {
	ID      int64  `json:"id"`
	Kind    string `json:"kind"`
	Service string `json:"service"`
	Title   string `json:"title"`
}
type PreparationScopeReview struct {
	PreparationID       string                 `json:"preparation_id"`
	RunID               int64                  `json:"run_id"`
	RunStatus           string                 `json:"run_status"`
	Changed             bool                   `json:"changed"`
	FrozenCount         int                    `json:"frozen_count"`
	CurrentCount        int                    `json:"current_count"`
	Added               []PreparationScopeItem `json:"added"`
	Removed             []PreparationScopeItem `json:"removed"`
	Modified            []PreparationScopeItem `json:"modified"`
	Message             string                 `json:"message"`
	CanCancel           bool                   `json:"can_cancel"`
	CancelBlockedReason string                 `json:"cancel_blocked_reason,omitempty"`
	NewPreparationHash  string                 `json:"new_preparation_hash"`
}

func scopeChangeIdentity(c Change) string {
	c.CreatedAt = 0
	c.UpdatedAt = 0
	c.Status = ""
	c.WorkItems = nil
	return digestJSON(c)
}
func scopeReviewDiff(before, after *DeliveryScope) PreparationScopeReview {
	out := PreparationScopeReview{Added: []PreparationScopeItem{}, Removed: []PreparationScopeItem{}, Modified: []PreparationScopeItem{}}
	old, current := map[int64]Change{}, map[int64]Change{}
	if before != nil {
		out.FrozenCount = len(before.Changes)
		for _, c := range before.Changes {
			old[c.ID] = c
		}
	}
	if after != nil {
		out.CurrentCount = len(after.Changes)
		for _, c := range after.Changes {
			current[c.ID] = c
		}
	}
	item := func(c Change) PreparationScopeItem { return PreparationScopeItem{c.ID, c.Kind, c.Service, c.Title} }
	for id, c := range current {
		if v, ok := old[id]; !ok {
			out.Added = append(out.Added, item(c))
		} else if scopeChangeIdentity(v) != scopeChangeIdentity(c) {
			out.Modified = append(out.Modified, item(c))
		}
	}
	for id, c := range old {
		if _, ok := current[id]; !ok {
			out.Removed = append(out.Removed, item(c))
		}
	}
	for _, items := range [][]PreparationScopeItem{out.Added, out.Removed, out.Modified} {
		sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	}
	if before == nil {
		before = &DeliveryScope{}
	}
	if after == nil {
		after = &DeliveryScope{}
	}
	out.Changed = researchScopeDigest(before) != researchScopeDigest(after)
	return out
}
func (s *Store) preparationCancelBlocker(p *CodePreparation) string {
	if p.LeaseUntil > time.Now().Unix() || p.State == "merging" || p.State == "building" {
		return "执行器仍在工作或等待核对，不能终止；请先核对原任务"
	}
	if p.State == "uncertain" {
		return "推送结果尚未核对，不能直接释放冻结范围"
	}
	if p.PreparationRunID > 0 {
		run := s.DeliveryRun(p.PreparationRunID)
		if run == nil || run.Owner != p.Owner {
			return "原准备单归属无法核验"
		}
		step, _ := researchNextNode(run)
		if step == nil || step.Detail != p.ID || (step.Kind != "code-prepare" && step.Kind != "code-verify") {
			return "代码任务不是原准备单的当前节点，请在原流程核对"
		}
		if run.Status != "blocked" && run.Status != "running" && run.Status != "approved" {
			return "原准备单已经结束"
		}
		ctrl := s.DeliveryControl(run.ID)
		if ctrl.State == "running" || ctrl.State == "pausing" || ctrl.State == "interrupted" {
			return "请先暂停或核对原协调器，不能直接终止"
		}
	}
	return ""
}
func (s *Store) preparationScopeReview(p *CodePreparation) (*PreparationScopeReview, error) {
	if p.PreparationRunID <= 0 {
		return nil, fmt.Errorf("此代码任务没有关联统一准备单")
	}
	run := s.DeliveryRun(p.PreparationRunID)
	if run == nil || run.Owner != p.Owner || run.Plan.PreparationRequest == nil {
		return nil, fmt.Errorf("原准备单不存在或归属不一致")
	}
	req := *run.Plan.PreparationRequest
	tpl := s.DeliveryTemplate(req.TemplateID)
	if tpl == nil {
		return nil, fmt.Errorf("原流程已删除，请在交付配置中选择有效流程后重新准备")
	}
	current, _, err := s.resolveDeliveryScope(req, tpl)
	if err != nil {
		return nil, fmt.Errorf("当前范围无法读取：%w；请先核对关联工作项", err)
	}
	if current == nil || run.Plan.Scope == nil {
		return nil, fmt.Errorf("原准备单范围无法核验")
	}
	out := scopeReviewDiff(run.Plan.Scope, current)
	out.PreparationID = p.ID
	out.RunID = run.ID
	out.RunStatus = run.Status
	if validationErr := s.checkResearchScope(run); validationErr != nil {
		out.Changed = true
		out.Message = validationErr.Error()
	}
	out.CancelBlockedReason = s.preparationCancelBlocker(p)
	out.CanCancel = out.CancelBlockedReason == "" && p.State != "canceled"
	if out.Changed {
		out.Message = fmt.Sprintf("准备单 #%d 冻结 %d 项，当前 %d 项：新增 %d 项、移出 %d 项、内容修改 %d 项。原范围确认不适用于现在的内容；请先核对差异，终止旧准备后按当前范围重新预览和确认。", run.ID, out.FrozenCount, out.CurrentCount, len(out.Added), len(out.Removed), len(out.Modified))
	} else {
		out.Message = "当前交付内容与原准备单一致"
	}
	if req.VersionID > 0 {
		out.NewPreparationHash = fmt.Sprintf("#/orchestration?start=1&version=%d", req.VersionID)
	} else if len(req.WorkItemIDs) > 0 {
		ids := []string{}
		for _, id := range req.WorkItemIDs {
			ids = append(ids, fmt.Sprint(id))
		}
		out.NewPreparationHash = "#/orchestration?start=1&work_items=" + strings.Join(ids, ",")
	}
	return &out, nil
}
func registerPreparationScopeReview(mux *http.ServeMux, s *Store) {
	mux.HandleFunc("GET /api/delivery/code-preparations/{id}/scope-review", func(w http.ResponseWriter, r *http.Request) {
		p := s.CodePreparation(r.PathValue("id"))
		if p == nil {
			deliveryWriteError(w, 404, "代码准备不存在")
			return
		}
		if !deliveryManage(r, p.Owner) {
			deliveryWriteError(w, 403, "仅发起人或管理员可核对准备范围")
			return
		}
		review, err := s.preparationScopeReview(p)
		if err != nil {
			deliveryWriteError(w, 409, err)
			return
		}
		deliveryJSON(w, review)
	})
}
