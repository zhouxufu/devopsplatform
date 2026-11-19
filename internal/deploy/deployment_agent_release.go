package deploy

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (m *ReleaseManager) resumeAgentRelease(deployID int64) {
	var id int64
	if m.st.db.QueryRow(`SELECT release_id FROM release_order_deploys WHERE deploy_id=?`, deployID).Scan(&id) != nil {
		return
	}
	if o := m.st.GetReleaseOrder(id); o != nil && deploymentAgentTarget(o.Target.DeployConfig) {
		m.reconcileAgentOrder(id, o.NextIndex)
	}
}

// Report-driven scheduling; database row locking in enqueueAgentTask also
// enforces max_parallel across multiple control-plane API processes.
func (m *ReleaseManager) reconcileAgentOrder(id int64, end int) {
	m.agentMu.Lock()
	defer m.agentMu.Unlock()
	o := m.st.GetReleaseOrder(id)
	if o == nil || o.NextIndex != end || (o.Status != "running" && o.Status != "needs_review" && o.Status != "rolling_back") {
		return
	}
	ids := m.st.ReleaseDeployIDs(id)
	if len(ids) == 0 {
		return
	}
	waiting := []*Deploy{}
	failedCount := 0
	active := 0
	review, rolling, failed, allRolledBack := false, false, false, true
	for _, deployID := range ids {
		d := m.st.GetDeploy(deployID)
		if d == nil {
			review = true
			continue
		}
		if d.Status == "needs_review" {
			review = true
		}
		if d.Status != "rolled-back" {
			allRolledBack = false
		}
		switch d.Status {
		case "running", "needs_review":
			var state string
			e := m.st.db.QueryRow(`SELECT status FROM agent_deployment_tasks WHERE deploy_id=?`, deployID).Scan(&state)
			if e != nil {
				if e != sql.ErrNoRows {
					review = true
					continue
				}
				if d.Status == "running" {
					waiting = append(waiting, d)
				}
				continue
			}
			active++
			rolling = rolling || state == "rolling_back"
		case "failed", "rollback-failed", "rolled-back", "canceled":
			failed = true
			failedCount++
		case "success":
		default:
			review = true
		}
	}
	if review {
		m.st.compareReleaseStatus(id, o.Status, "needs_review", "", false)
		m.st.RefreshReleaseCounts(id)
		return
	}
	if o.Status == "needs_review" {
		if m.st.compareReleaseStatus(id, "needs_review", "running", "", false) != nil {
			return
		}
		o.Status = "running"
	}
	if rolling {
		m.st.compareReleaseStatus(id, o.Status, "rolling_back", "", false)
		return
	}
	hardFailure := failed && (failedCount > o.FailureThreshold || (canaryTarget(o.Target) && end == policyInt(o.Target.DeployConfig, "canary_count", 1)))
	if hardFailure {
		// Only rows with no task are canceled. Already dispatched tasks keep their
		// reservation and report their own outcome; successful peers stay untouched.
		for _, d := range waiting {
			m.st.FinishDeploy(d.ID, "canceled")
			m.st.AppendDeployLog(d.ID, "前序主机未成功，未下发本机任务；已运行的其他主机不自动回滚")
		}
		if active > 0 {
			return
		}
		m.st.RefreshReleaseCounts(id)
		state := "failed"
		if allRolledBack && len(waiting) == 0 {
			state = "rolled-back"
		}
		m.st.compareReleaseStatus(id, o.Status, state, "", true)
		return
	}
	if len(waiting) > 0 {
		parallel := o.Target.MaxParallel
		if parallel < 1 {
			parallel = 1
		}
		slots := parallel - active
		for _, d := range waiting {
			if slots <= 0 {
				break
			}
			if e := m.dispatchAgentDeploy(o, d); e != nil {
				if errors.Is(e, errAgentCapacity) {
					break
				}
				m.st.FinishDeploy(d.ID, "failed")
				m.st.AppendDeployLog(d.ID, "任务下发前校验失败："+redactDeploymentText(e.Error()))
				go m.reconcileAgentOrder(id, end)
				return
			}
			slots--
			active++
		}
	}
	if active > 0 {
		if rolling {
			m.st.compareReleaseStatus(id, o.Status, "rolling_back", "", false)
		}
		return
	}
	m.st.RefreshReleaseCounts(id)
	if end >= len(o.Hosts) {
		m.st.compareReleaseStatus(id, o.Status, "success", "", true)
		return
	}
	if o.PauseBetweenBatches || (canaryTarget(o.Target) && end == policyInt(o.Target.DeployConfig, "canary_count", 1)) {
		m.st.compareReleaseStatus(id, o.Status, "waiting_next_batch", "", false)
		return
	}
	if m.st.compareReleaseStatus(id, o.Status, "queued", "", false) == nil {
		go m.startNextBatch(id)
	}
}
func (m *ReleaseManager) sweepAgentTasks(now int64) {
	rows, e := m.st.db.Query(`SELECT deploy_id FROM agent_deployment_tasks WHERE status NOT IN ('success','failed','rolled-back','rollback-failed','needs_review') AND updated_at<?`, now-120)
	if e != nil {
		return
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		tx, e := m.st.db.Begin()
		if e != nil {
			continue
		}
		res, e := tx.Exec(`UPDATE agent_deployment_tasks SET status='needs_review' WHERE deploy_id=? AND status NOT IN ('success','failed','rolled-back','rollback-failed','needs_review') AND updated_at<?`, id, now-120)
		if e != nil {
			tx.Rollback()
			continue
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			tx.Rollback()
			continue
		}
		_, e = tx.Exec(`UPDATE deploys SET status='needs_review',ended_at=0 WHERE id=? AND status='running'`, id)
		if e != nil {
			tx.Rollback()
			continue
		}
		if tx.Commit() != nil {
			continue
		}
		m.st.AppendDeployLog(id, "Agent 回执等待较长，进入待核对；保留任务、进程和执行保护，不停止、不重发")
		m.resumeAgentRelease(id)
	}
}
func (m *ReleaseManager) startAgentReconciler() {
	m.st.deploymentAgentOnce.Do(func() {
		m.st.deploymentAgentWG.Add(1)
		go func() {
			defer m.st.deploymentAgentWG.Done()
			timer := time.NewTicker(15 * time.Second)
			defer timer.Stop()
			for {
				select {
				case <-m.st.deploymentAgentStop:
					return
				case now := <-timer.C:
					m.sweepAgentTasks(now.Unix())
				}
			}
		}()
	})
}
func (m *ReleaseManager) recoverAgentOrders() {
	rows, e := m.st.db.Query(`SELECT id FROM release_orders WHERE status IN ('interrupted','running','needs_review','rolling_back')`)
	if e != nil {
		return
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		o := m.st.GetReleaseOrder(id)
		if o == nil || !deploymentAgentTarget(o.Target.DeployConfig) {
			continue
		}
		if o.Status != "running" && o.Status != "needs_review" && o.Status != "rolling_back" && o.Status != "interrupted" {
			continue
		}
		if o.Status != "running" {
			if m.st.compareReleaseStatus(id, o.Status, "running", "", false) != nil {
				continue
			}
		}
		for _, deployID := range m.st.ReleaseDeployIDs(id) {
			var state string
			e := m.st.db.QueryRow(`SELECT status FROM agent_deployment_tasks WHERE deploy_id=?`, deployID).Scan(&state)
			if e == nil && state != "success" && state != "failed" && state != "rolled-back" && state != "rollback-failed" {
				m.st.db.Exec(`UPDATE deploys SET status='needs_review',ended_at=0 WHERE id=? AND status='running'`, deployID)
			}
			// No task row is proof that the atomic batch was not dispatched. This
			// only restores queue scheduling, never replays any Agent side effect.
			if e == sql.ErrNoRows {
				m.st.db.Exec(`UPDATE deploys SET status='running',ended_at=0 WHERE id=? AND status='interrupted'`, deployID)
			}
		}
		if len(m.st.ReleaseDeployIDs(id)) == 0 {
			if o.NextIndex == 0 && o.Target.DeployConfig["deployment_agent_protocol"] == "2" {
				res, e := m.st.db.Exec(`UPDATE release_orders SET status='queued',ended_at=NULL WHERE id=? AND status='running' AND next_index=0 AND NOT EXISTS (SELECT 1 FROM release_order_deploys WHERE release_id=?)`, id, id)
				if e == nil {
					n, _ := res.RowsAffected()
					if n == 1 {
						go m.startNextBatch(id)
					}
				}
			} else {
				m.st.compareReleaseStatus(id, "running", "needs_review", "", false)
			}
			continue
		}
		m.st.AppendDeployLog(firstDeployID(m.st.ReleaseDeployIDs(id)), fmt.Sprintf("发布单 %d 从持久任务和主机回执恢复；不重复执行已派发任务", id))
		m.reconcileAgentOrder(id, o.NextIndex)
	}
}
func firstDeployID(ids []int64) int64 {
	if len(ids) > 0 {
		return ids[0]
	}
	return 0
}
