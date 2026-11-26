package deploy

import (
	"database/sql"
	"fmt"
)

type AppliedSQLChange struct {
	ChangeID          int64  `json:"change_id"`
	RunID             int64  `json:"run_id"`
	NodeID            string `json:"node_id"`
	Service           string `json:"service"`
	Alias             string `json:"alias"`
	ChangeDigest      string `json:"change_digest"`
	DestinationDigest string `json:"destination_digest"`
	LedgerDigest      string `json:"ledger_digest"`
}

// Prove success from both the durable ledger and the original approved plan.
// A success flag alone is insufficient: payload or connection identity may have
// changed. Equivalent connection aliases share the same proven physical target.
func (s *Store) appliedSQLChange(c *Change, destination *ResourceConnection, environment string) (*AppliedSQLChange, error) {
	type entry struct {
		connection, database, status, digest, node string
		run                                        int64
	}
	rows, err := s.db.Query(`SELECT connection,database_name,status,digest,node_id,run_id FROM sql_execution_ledger WHERE change_id=?`, c.ID)
	if err != nil {
		return nil, err
	}
	entries := []entry{}
	for rows.Next() {
		var e entry
		if err = rows.Scan(&e.connection, &e.database, &e.status, &e.digest, &e.node, &e.run); err != nil {
			rows.Close()
			return nil, err
		}
		entries = append(entries, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		prior := s.DeliveryRun(e.run)
		var plan *SQLExecutionPlan
		if prior != nil {
			for _, p := range prior.Plan.SQLPlans {
				if p.NodeID == e.node {
					plan = p
					break
				}
			}
		}
		copy := *destination
		copy.Name = e.connection
		matches := plan != nil && e.database == destination.Config["database"] && plan.DestinationDigest == sqlDestination(&copy)
		if !matches {
			if e.connection == destination.Name && e.database == destination.Config["database"] {
				return nil, fmt.Errorf("变更 #%d 历史执行目标无法验证或连接身份已变化，请核对台账", c.ID)
			}
			continue
		}
		if e.status != "success" {
			return nil, fmt.Errorf("变更 #%d 在目标上存在 %s 执行记录，必须人工核对，不能自动跳过或重试", c.ID, e.status)
		}
		selected, _, err := s.selectEnvironmentSQL(c.SQL, environment)
		if err != nil {
			return nil, err
		}
		previous, _, err := s.selectEnvironmentSQL(c.SQL, prior.Plan.Environment.Name)
		if err != nil || selected.Skip || previous.Skip || !sameSQLTokens(selected.Content, previous.Content) {
			return nil, fmt.Errorf("变更 #%d 在同一数据库上的历史脚本与当前环境不同，请新增后续变更", c.ID)
		}
		proof := digestJSON([]any{c.Service, c.SQL})
		valid := false
		for i, id := range plan.ChangeIDs {
			if id == c.ID && i < len(plan.ChangeDigests) && plan.ChangeDigests[i] == proof && e.digest == sqlChangeDigest(plan, c.ID) {
				valid = true
			}
		}
		if !valid {
			return nil, fmt.Errorf("变更 #%d 内容与历史成功快照不一致，请新增后续变更", c.ID)
		}
		return &AppliedSQLChange{c.ID, e.run, e.node, c.Service, c.SQL.ResourceAlias, proof, sqlDestination(destination), e.digest}, nil
	}
	return nil, nil
}

func (s *Store) verifySQLReuse(p *SQLExecutionPlan, environment string) error {
	for _, skip := range p.Applied {
		c := s.GetChange(skip.ChangeID)
		if c == nil || c.SQL == nil || c.Status != ChangeReady || digestJSON([]any{c.Service, c.SQL}) != skip.ChangeDigest {
			return fmt.Errorf("已执行变更 #%d 的内容或状态变化，请重新预检", skip.ChangeID)
		}
		connection, err := s.sqlBinding(skip.Service, environment, skip.Alias)
		if err != nil || sqlDestination(connection) != skip.DestinationDigest {
			return fmt.Errorf("已执行变更的环境资源绑定变化，请重新预检")
		}
		found, err := s.appliedSQLChange(c, connection, environment)
		if err != nil {
			return err
		}
		if found == nil || found.RunID != skip.RunID || found.LedgerDigest != skip.LedgerDigest {
			return fmt.Errorf("历史成功记录变化，请重新预检")
		}
	}
	return nil
}

func (s *Store) changeExecutionTouched(id int64) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM sql_execution_ledger WHERE change_id=?) + (SELECT COUNT(*) FROM nacos_execution_ledger WHERE change_id=?)`, id, id).Scan(&n)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return n > 0, err
}
