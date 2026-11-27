package deploy

import "fmt"

type ReleaseChecklistItem struct {
	ID            int64                `json:"id"`
	Title         string               `json:"title"`
	Status        string               `json:"status"`
	Reason        string               `json:"reason,omitempty"`
	PriorRunID    int64                `json:"prior_run_id,omitempty"`
	Prerequisites []ChangePrerequisite `json:"prerequisites,omitempty"`
}

type ReleaseChecklistStep struct {
	NodeID string                 `json:"node_id"`
	Name   string                 `json:"name"`
	Kind   string                 `json:"kind"`
	Items  []ReleaseChecklistItem `json:"items,omitempty"`
}

func releaseChecklist(plan *DeliveryPlan) []ReleaseChecklistStep {
	if plan.Scope == nil {
		return nil
	}
	applied := map[int64]int64{}
	exempt := map[int64]string{}
	for _, p := range plan.SQLPlans {
		for _, a := range p.Applied {
			applied[a.ChangeID] = a.RunID
		}
		for _, s := range p.ScriptSelections {
			if s.Skip {
				exempt[s.ChangeID] = s.Reason
			}
		}
	}
	for _, p := range plan.NacosPlans {
		for _, c := range p.Configs {
			if c.Skip && c.PriorRunID > 0 {
				applied[c.ChangeID] = c.PriorRunID
			}
		}
	}
	steps := make([]ReleaseChecklistStep, 0, len(plan.Nodes))
	for _, node := range plan.Nodes {
		step := ReleaseChecklistStep{NodeID: node.ID, Name: node.Name, Kind: node.Kind}
		for _, c := range plan.Scope.Changes {
			if !changeNodeMatches(node, c) {
				continue
			}
			item := ReleaseChecklistItem{ID: c.ID, Title: c.Title, Status: "planned", Prerequisites: c.Prerequisites}
			if c.Status != ChangeReady {
				item.Status, item.Reason = "blocked", "请先校验并就绪"
			}
			if id := applied[c.ID]; id > 0 {
				item.Status, item.PriorRunID, item.Reason = "applied", id, fmt.Sprintf("目标已核对成功回执 #%d", id)
			} else if reason, ok := exempt[c.ID]; ok {
				item.Status, item.Reason = "not_applicable", reason
			}
			if item.Status == "planned" {
				for _, dep := range c.Prerequisites {
					if _, skip := exempt[dep.ChangeID]; skip && applied[dep.ChangeID] == 0 {
						item.Status = "blocked"
						item.Reason = fmt.Sprintf("前置变更 #%d 已声明本环境无需执行，无法证明此前置已完成：%s", dep.ChangeID, dep.Reason)
						break
					}
				}
			}
			if note, skip := plan.SkippedNodes[node.ID]; skip {
				item.Status, item.Reason = "skipped", note
			}
			step.Items = append(step.Items, item)
		}
		steps = append(steps, step)
	}
	return steps
}
