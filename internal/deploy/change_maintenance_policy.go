package deploy

import (
	"encoding/json"
	"fmt"
)

// Only a code record's label and explanation are descriptive. A manual record's
// summary, rollback instructions, references and payloads affect execution.
func maintenanceDescriptionPatch(req changeMaintenanceRequest) bool {
	if req.Action != "edit" || req.Change != nil || len(req.Patch) == 0 || req.WorkItemIDs != nil || req.OriginWorkItemID != 0 {
		return false
	}
	for key := range req.Patch {
		if key != "title" && key != "summary" {
			return false
		}
	}
	return true
}

type changeMaintenanceBlocker struct {
	RunID  int64  `json:"run_id"`
	Status string `json:"status"`
	Path   string `json:"path"`
}

func maintenanceActiveRuns(tx *deployTx, id int64) ([]changeMaintenanceBlocker, error) {
	rows, err := tx.Query(`SELECT id,status,plan FROM delivery_runs WHERE status NOT IN ('success','failed','canceled','rejected') ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	blockers := []changeMaintenanceBlocker{}
	for rows.Next() {
		var runID int64
		var status, raw string
		if err = rows.Scan(&runID, &status, &raw); err != nil {
			return nil, err
		}
		var plan DeliveryPlan
		if json.Unmarshal([]byte(raw), &plan) != nil {
			return nil, fmt.Errorf("执行单 #%d 的冻结范围无法核验", runID)
		}
		found := false
		if plan.Scope != nil {
			for _, c := range plan.Scope.Changes {
				found = found || c.ID == id
			}
		}
		if plan.Version != nil {
			for _, c := range plan.Version.Changes {
				found = found || c.ID == id
			}
		}
		for _, node := range plan.Nodes {
			for _, cid := range node.ChangeIDs {
				found = found || cid == id
			}
		}
		if found {
			blockers = append(blockers, changeMaintenanceBlocker{RunID: runID, Status: status, Path: fmt.Sprintf("#/orchestration?run=%d", runID)})
		}
	}
	return blockers, rows.Err()
}
