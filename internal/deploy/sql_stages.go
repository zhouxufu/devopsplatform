package deploy

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Runtime evidence is separate from the immutable reviewed plan. It contains
// counts and hashes, never row contents, and can only be inserted once.
type SQLStageScope struct {
	Statement int    `json:"statement"`
	Rows      int    `json:"rows"`
	Digest    string `json:"digest"`
}
type SQLStageBaseline struct {
	NodeID       string          `json:"node_id"`
	PlanDigest   string          `json:"plan_digest"`
	SchemaDigest string          `json:"schema_digest"`
	Scopes       []SQLStageScope `json:"scopes"`
	CheckedAt    int64           `json:"checked_at"`
}

func sqlStageBaselineKey(runID int64, node string) string {
	return fmt.Sprintf("sql_stage_baseline:%d:%s", runID, node)
}
func (s *Store) loadSQLStageBaselines(run *DeliveryRun) {
	if run == nil {
		return
	}
	for _, plan := range run.Plan.SQLPlans {
		if !plan.DeferredData {
			continue
		}
		var raw string
		if s.db.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, sqlStageBaselineKey(run.ID, plan.NodeID)).Scan(&raw) != nil {
			continue
		}
		var baseline SQLStageBaseline
		if json.Unmarshal([]byte(raw), &baseline) == nil {
			run.SQLStageBaselines = append(run.SQLStageBaselines, baseline)
		}
	}
}
func sqlStageSchemaMatches(p *SQLExecutionPlan, actual sqlCatalog) bool {
	if len(p.PreviousNodes) == 0 {
		return digestJSON(catalogSnapshot(actual, p.Tables)) == p.SchemaDigest
	}
	if p.ExpectedSchema == nil {
		return false
	}
	raw, _ := json.Marshal(p.ExpectedSchema)
	expected := sqlCatalog{}
	_ = json.Unmarshal(raw, &expected)
	// CREATE TABLE without COLLATE inherits the server's utf8mb4 collation.
	// Normalize only this unspecified value, as the existing DDL readback does.
	for name, table := range expected {
		current := actual[name]
		if table != nil && current != nil && table.Collation == "" && strings.HasPrefix(current.Collation, "utf8mb4_") {
			table.Collation = current.Collation
			for column, value := range table.Columns {
				table.Columns[column] = defaultColumnCollation(value, current.Collation)
			}
		}
	}
	return digestJSON(expected) == digestJSON(catalogSnapshot(actual, p.Tables))
}
func (s *Store) verifySQLStagePredecessors(run *DeliveryRun, p *SQLExecutionPlan) error {
	for _, nodeID := range p.PreviousNodes {
		completed := false
		for _, step := range run.Steps {
			if step.NodeID == nodeID && step.Kind == "mysql" && step.Status == "success" {
				completed = true
			}
		}
		if !completed {
			return fmt.Errorf("前序 SQL 阶段 %s 尚未成功，不能执行当前阶段", nodeID)
		}
		var previous *SQLExecutionPlan
		for _, candidate := range run.Plan.SQLPlans {
			if candidate.NodeID == nodeID {
				previous = candidate
			}
		}
		if previous == nil || s.sqlPhysicalTarget(previous.Connection, previous.Database) != s.sqlPhysicalTarget(p.Connection, p.Database) {
			return fmt.Errorf("前序 SQL 阶段目标不匹配")
		}
		if _, err := s.reviewedSQLConnection(previous, run.Plan.Environment.Name); err != nil {
			return err
		}
		for _, id := range previous.ChangeIDs {
			var status, digest string
			var owner int64
			if err := s.db.QueryRow(`SELECT status,digest,run_id FROM sql_execution_ledger WHERE connection=? AND database_name=? AND change_id=?`, previous.Connection, previous.Database, id).Scan(&status, &digest, &owner); err != nil || status != "success" || owner != run.ID || digest != sqlChangeDigest(previous, id) {
				return fmt.Errorf("前序 SQL 变更 #%d 缺少原执行单的成功回执，请核对台账", id)
			}
		}
	}
	return nil
}
