package deploy

import (
	"context"
	"fmt"
	sae "github.com/alibabacloud-go/sae-20190506/v4/client"
	"time"
)

type saeCanaryRollback interface {
	AbortRollback(context.Context, string) (string, error)
}

func (p *aliyunSAEProvider) AbortRollback(ctx context.Context, id string) (string, error) {
	if e := ctx.Err(); e != nil {
		return "", e
	}
	r, e := p.client.AbortAndRollbackChangeOrder((&sae.AbortAndRollbackChangeOrderRequest{}).SetChangeOrderId(id))
	if e != nil {
		return "", e
	}
	if r == nil || r.Body == nil || r.Body.Success == nil || !*r.Body.Success || r.Body.Data == nil || r.Body.Data.ChangeOrderId == nil {
		return "", fmt.Errorf("SAE 未返回回退变更单，必须人工核对")
	}
	return *r.Body.Data.ChangeOrderId, nil
}
func (m *ReleaseManager) rollbackSAERelease(o *ReleaseOrder, reason, actor string) error {
	ids := m.st.ReleaseDeployIDs(o.ID)
	if len(ids) != 1 {
		return fmt.Errorf("SAE 发布记录异常")
	}
	d := m.st.GetDeploy(ids[0])
	if d == nil || d.Status != "waiting_next_batch" || d.ExternalID == "" {
		return fmt.Errorf("仅云端批次暂停时可终止并回退")
	}
	p, e := m.dp.saeFactory(m.st.GetConnection(o.Target.DeployConfig["connection"]))
	if e != nil {
		return e
	}
	ctrl, ok := p.(saeCanaryRollback)
	if !ok {
		return fmt.Errorf("当前 SAE 接入不支持灰度回退")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	state, e := p.Change(ctx, d.ExternalID)
	if e != nil {
		return e
	}
	if state.Status != 8 {
		return fmt.Errorf("云端状态已变化，请刷新核对")
	}
	if e = m.st.compareReleaseStatus(o.ID, "waiting_next_batch", "rolling_back", "", false); e != nil {
		return e
	}
	if _, e = m.st.db.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, fmt.Sprintf("sae-rollback:%d", d.ID), "true"); e != nil {
		m.st.compareReleaseStatus(o.ID, "rolling_back", "waiting_next_batch", "", false)
		return e
	}
	rollbackID, e := ctrl.AbortRollback(ctx, d.ExternalID)
	if e != nil {
		// Do not unlock/retry: cloud may have accepted a rollback despite a lost
		// response. Keep the reservation until an operator reconciles the cloud.
		m.dp.emit(d.ID, "⚠ SAE 回退结果未知；请在云端核对，平台不会重复提交："+e.Error()+"\n")
		m.dp.flushSAELogs(d.ID)
		m.st.Audit(actor, "sae-rollback-unknown", "release", fmt.Sprint(o.ID), map[string]any{"change_order": d.ExternalID, "reason": reason})
		return fmt.Errorf("回退响应异常，已保留发布锁；请核对 SAE 云端变更单")
	}
	m.st.SetDeployExternalID(d.ID, rollbackID)
	m.st.Audit(actor, "sae-rollback", "release", fmt.Sprint(o.ID), map[string]any{"change_order": d.ExternalID, "rollback_change_order": rollbackID, "reason": reason})
	go func() {
		defer m.dp.flushSAELogs(d.ID)
		wait, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		status := pollSAEChange(m.dp, d.ID, p, wait, rollbackID)
		if status == "success" {
			status = "rolled-back"
		}
		if status == "waiting_next_batch" {
			m.dp.emit(d.ID, "⏸ 回退流程暂停，请核对后在平台继续回退批次\n")
			m.st.FinishDeploy(d.ID, "waiting_next_batch")
			m.st.SetReleaseStatus(o.ID, "waiting_next_batch", "", false)
			return
		}
		m.st.FinishDeploy(d.ID, status)
		if status == "rolled-back" {
			m.st.SetReleaseStatus(o.ID, "rolled-back", "", true)
		} else {
			m.st.SetReleaseStatus(o.ID, "rollback-failed", "", true)
		}
	}()
	return nil
}

func (d *Deployer) flushSAELogs(id int64) {
	d.mu.Lock()
	log := d.logs[id]
	delete(d.logs, id)
	d.mu.Unlock()
	if log != nil {
		d.st.AppendDeployLog(id, log.String())
	}
}
