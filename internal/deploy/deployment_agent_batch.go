package deploy

import (
	"errors"
	"fmt"
	"time"
)

var errAgentBatchChanged = errors.New("Agent batch claim changed")

// Persist the whole batch and its release linkage before any task is visible
// to an Agent. A restart can distinguish undispatched rows from real tasks.
func (m *ReleaseManager) startAgentBatch(o *ReleaseOrder, sv *Service, hosts []Host, a *Artifact, end, batch int) error {
	for _, h := range hosts {
		if e := m.dp.validateRelease(sv, h, *a); e != nil {
			return e
		}
	}
	tx, e := m.st.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = lockArtifactReferences(tx, a.ID); e != nil {
		return e
	}
	q := `SELECT status,next_index FROM release_orders WHERE id=?`
	if tx.dialect == "mysql" {
		q += " FOR UPDATE"
	}
	var status string
	var next int
	if e = tx.QueryRow(q, o.ID).Scan(&status, &next); e != nil {
		return e
	}
	if status != "running" || next != o.NextIndex {
		return errAgentBatchChanged
	}
	for _, h := range hosts {
		var n int
		if e = tx.QueryRow(`SELECT COUNT(*) FROM deploys WHERE service=? AND host=? AND status IN ('running','waiting_next_batch','needs_review')`, sv.Name, h.Name).Scan(&n); e != nil {
			return e
		}
		if n > 0 {
			return ErrRunConflict
		}
		if e = instanceOperationBusy(tx, sv.Name, o.Environment, h.Name); e != nil {
			return e
		}
		res, e := tx.Exec(`INSERT INTO deploys(service,version,host,environment,artifact_id,artifact_kind,artifact_uri,artifact_digest,reason,status,actor,started_at) VALUES(?,?,?,?,?,?,?,?,?,'running',?,?)`, sv.Name, a.Version, h.Name, o.Environment, a.ID, a.Kind, a.URI, a.Digest, o.Reason, o.Actor, time.Now().Unix())
		if e != nil {
			return e
		}
		id, e := res.LastInsertId()
		if e != nil {
			return e
		}
		if _, e = tx.Exec(`INSERT INTO release_order_deploys(release_id,batch_no,deploy_id) VALUES(?,?,?)`, o.ID, batch, id); e != nil {
			return e
		}
	}
	res, e := tx.Exec(`UPDATE release_orders SET next_index=? WHERE id=? AND status='running' AND next_index=?`, end, o.ID, o.NextIndex)
	if e != nil {
		return e
	}
	n, e := res.RowsAffected()
	if e != nil || n != 1 {
		return errAgentBatchChanged
	}
	return tx.Commit()
}
func (m *ReleaseManager) dispatchAgentDeploy(o *ReleaseOrder, d *Deploy) error {
	source := m.st.GetService(o.Service)
	if source == nil {
		return fmt.Errorf("原应用不存在")
	}
	h := m.st.GetHost(d.Host)
	if h == nil || h.Environment != o.Environment {
		return fmt.Errorf("冻结主机已不可用")
	}
	a := m.st.GetArtifact(o.ArtifactID)
	if a == nil || a.Digest != d.ArtifactDigest {
		return fmt.Errorf("冻结制品不可用")
	}
	if e := m.st.validateArtifactBuildVariant(a, o.Environment); e != nil {
		return e
	}
	target := o.Target
	runtime, e := m.st.decryptRuntimeEnvironment(o.runtimeEnvEncrypted)
	if e != nil {
		return e
	}
	target.RuntimeEnv = runtime
	task, e := m.dp.managedJavaAgentTask(d.ID, target.Apply(source), *h, *a)
	if e != nil {
		return e
	}
	task.ReleaseID = o.ID
	if e = m.st.enqueueAgentTask(task); e != nil {
		return e
	}
	m.st.AppendDeployLog(d.ID, "冻结 Agent 任务已入队，等待实际就绪回执；下发不等于发布成功")
	return nil
}
