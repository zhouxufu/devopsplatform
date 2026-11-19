package deploy

import "time"

// Project linked code state without executing or writing anything. This also
// makes old running ledgers readable after a worker stopped with a conflict.
func (s *Store) projectResearchCodeState(v *DeliveryRun) bool {
	if v == nil {
		return false
	}
	// Old ledgers may have completed every node before the coordinator saved its aggregate.
	if v.Status == "running" && len(v.Steps) > 0 {
		complete := true
		for _, step := range v.Steps {
			if step.Status != "success" && step.Status != "skipped" {
				complete = false
				break
			}
		}
		for _, node := range v.Plan.Nodes {
			found := false
			for _, step := range v.Steps {
				if step.NodeID == node.ID {
					found = true
					break
				}
			}
			if !found {
				complete = false
				break
			}
		}
		if complete {
			v.Status = "success"
			return true
		}
	}
	if (v.Status == "failed" || v.Status == "canceled") && v.Plan.PreparationRequest != nil {
		changed := false
		for i := range v.Steps {
			step := &v.Steps[i]
			if (step.Kind == "code-prepare" || step.Kind == "code-verify") && step.Detail != "" && step.Status != "success" {
				if p := s.CodePreparation(step.Detail); p != nil && p.Owner == v.Owner && p.PreparationRunID == v.ID && p.State == "canceled" && step.Status != "canceled" {
					step.Status = "canceled"
					changed = true
				}
			}
		}
		return changed
	}
	if v.Plan.PreparationRequest == nil || v.Plan.Template.Workbench != "research" || (v.Status != "running" && v.Status != "blocked") {
		return false
	}
	step, node := researchNextNode(v)
	if step == nil || node == nil || (node.Kind != "code-prepare" && node.Kind != "code-verify") || step.Detail == "" || (step.Status != "running" && step.Status != "blocked") {
		return false
	}
	p := s.CodePreparation(step.Detail)
	if p == nil || p.Owner != v.Owner || p.PreparationRunID != v.ID {
		return false
	}
	status := "running"
	switch p.State {
	case "conflict", "uncertain", "failed":
		status = "blocked"
	case "merging", "building":
		if p.LeaseUntil <= time.Now().Unix() {
			status = "blocked"
		}
	case "preview", "ready", "canceled":
	default:
		return false
	}
	changed := v.Status != status || step.Status != status
	v.Status, step.Status = status, status
	return changed
}
