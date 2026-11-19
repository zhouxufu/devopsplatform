package deploy

import (
	"fmt"
	"sort"
)

// A scope is immutable inside a submitted plan. Later additions to a Bug belong
// to its next delivery, not to an already approved execution.
type DeliveryScope struct {
	SourceDigest      string     `json:"source_digest,omitempty"`
	ExcludedChangeIDs []int64    `json:"excluded_change_ids,omitempty"`
	Type              string     `json:"type"`
	WorkItems         []WorkItem `json:"work_items"`
	Changes           []Change   `json:"changes"`
	Digest            string     `json:"digest"`
}

func (s *Store) resolveWorkItemScope(ids []int64) (*DeliveryScope, error) {
	if len(ids) == 0 || len(ids) > 500 {
		return nil, fmt.Errorf("请选择 1–500 项需求或 Bug")
	}
	p := &DeliveryScope{Type: "work_items", WorkItems: []WorkItem{}, Changes: []Change{}}
	seen, changes := map[int64]bool{}, map[int64]Change{}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return nil, fmt.Errorf("工作项编号无效或重复")
		}
		seen[id] = true
		w := s.GetWorkItem(id)
		if w == nil {
			return nil, fmt.Errorf("工作项 #%d 不存在", id)
		}
		d, err := s.WorkDelivery(id)
		if err != nil {
			return nil, err
		}
		if len(d.Changes) == 0 {
			return nil, fmt.Errorf("工作项 #%d 尚无交付内容", id)
		}
		p.WorkItems = append(p.WorkItems, *w)
		for _, c := range d.Changes {
			changes[c.ID] = c
		}
	}
	if len(changes) > 500 {
		return nil, fmt.Errorf("工作项关联变更超过 500 项")
	}
	for _, c := range changes {
		p.Changes = append(p.Changes, c)
	}
	sort.Slice(p.WorkItems, func(i, j int) bool { return p.WorkItems[i].ID < p.WorkItems[j].ID })
	sort.Slice(p.Changes, func(i, j int) bool { return p.Changes[i].ID < p.Changes[j].ID })
	p.Digest = digestJSON(p)
	return p, nil
}

func (s *Store) resolveDeliveryScope(req DeliveryRequest, tpl *DeliveryTemplate) (*DeliveryScope, *ReleaseVersion, error) {
	kind := req.SourceType
	if kind == "" {
		if len(req.WorkItemIDs) > 0 {
			kind = "work_items"
		} else {
			kind = tpl.Mode
		}
	}
	if kind != "changes" && len(req.SourceChangeIDs) > 0 {
		return nil, nil, fmt.Errorf("不能混用独立变更来源和工作项/版本来源")
	}
	switch kind {
	case "fixed":
		if tpl.Mode != "fixed" || req.VersionID != 0 || len(req.WorkItemIDs) > 0 {
			return nil, nil, fmt.Errorf("固定服务范围不允许混入版本或工作项")
		}
		return nil, nil, nil
	case "work_items":
		if (tpl.Workbench != "" && tpl.Workbench != "research") || tpl.Mode != "version" || req.VersionID != 0 {
			return nil, nil, fmt.Errorf("工作项交付请选择动态范围编排，不能混用版本或工作台模板")
		}
		p, err := s.resolveWorkItemScope(req.WorkItemIDs)
		if err != nil {
			return nil, nil, err
		}
		p, err = selectDeliveryChanges(p, req.SelectedChangeIDs)
		return p, nil, err
	case "version":
		if tpl.Mode != "version" || len(req.WorkItemIDs) > 0 {
			return nil, nil, fmt.Errorf("版本交付不能同时指定工作项范围")
		}
		v := s.GetReleaseVersion(req.VersionID)
		if v == nil || (!req.previewDrafts && tpl.Workbench != "research" && v.Status != ReleaseVersionReady && v.Status != ReleaseVersionCompleted) {
			return nil, nil, fmt.Errorf("请选择已就绪的发布版本")
		}
		p := &DeliveryScope{Type: "version", WorkItems: v.WorkItems, Changes: v.Changes}
		p.Digest = digestJSON(p)
		p, err := selectDeliveryChanges(p, req.SelectedChangeIDs)
		return p, v, err
	case "changes":
		sourceIDs := req.SourceChangeIDs
		if sourceIDs == nil {
			sourceIDs = req.SelectedChangeIDs
		}
		if tpl.Workbench != "" || tpl.Mode != "version" || req.VersionID != 0 || len(req.WorkItemIDs) > 0 || len(sourceIDs) == 0 || len(sourceIDs) > 500 {
			return nil, nil, fmt.Errorf("独立变更交付须明确选择 1–500 项，不混入版本或工作项")
		}
		p := &DeliveryScope{Type: "changes", WorkItems: []WorkItem{}, Changes: []Change{}}
		seen := map[int64]bool{}
		for _, id := range sourceIDs {
			c := s.GetChange(id)
			if c == nil || seen[id] {
				return nil, nil, fmt.Errorf("变更 #%d 不存在或重复", id)
			}
			seen[id] = true
			p.Changes = append(p.Changes, *c)
		}
		sort.Slice(p.Changes, func(i, j int) bool { return p.Changes[i].ID < p.Changes[j].ID })
		p.Digest = digestJSON(p)
		p, err := selectDeliveryChanges(p, req.SelectedChangeIDs)
		return p, nil, err
	default:
		return nil, nil, fmt.Errorf("交付来源仅支持 fixed、version、work_items、changes")
	}
}
