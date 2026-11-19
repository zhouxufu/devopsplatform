package deploy

import (
	"context"
	"fmt"
	"time"

	sae "github.com/alibabacloud-go/sae-20190506/v4/client"
)

type saeReleaseControl interface {
	Confirm(context.Context, string) error
	Stop(context.Context, string) error
}

func (p *aliyunSAEProvider) Confirm(ctx context.Context, pipelineID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r, e := p.client.ConfirmPipelineBatch((&sae.ConfirmPipelineBatchRequest{}).SetPipelineId(pipelineID).SetConfirm(true))
	if e != nil {
		return e
	}
	if r == nil || r.Body == nil || r.Body.Success == nil || !*r.Body.Success {
		return fmt.Errorf("SAE 未确认下一批")
	}
	return nil
}
func (p *aliyunSAEProvider) Stop(ctx context.Context, changeID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r, e := p.client.AbortChangeOrder((&sae.AbortChangeOrderRequest{}).SetChangeOrderId(changeID))
	if e != nil {
		return e
	}
	if r == nil || r.Body == nil || r.Body.Success == nil || !*r.Body.Success {
		return fmt.Errorf("SAE 未接受终止变更请求")
	}
	return nil
}
func (m *ReleaseManager) continueSAERelease(o *ReleaseOrder, actor string) error {
	return m.controlSAERelease(o, actor, false)
}
func (m *ReleaseManager) cancelSAERelease(o *ReleaseOrder, actor string) error {
	return m.controlSAERelease(o, actor, true)
}
func (m *ReleaseManager) controlSAERelease(o *ReleaseOrder, actor string, stop bool, expectedPipeline ...string) error {
	ids := m.st.ReleaseDeployIDs(o.ID)
	if len(ids) != 1 {
		return fmt.Errorf("SAE 变更记录异常，禁止继续")
	}
	d := m.st.GetDeploy(ids[0])
	if d == nil || d.Status != "waiting_next_batch" || d.ExternalID == "" {
		return fmt.Errorf("SAE 尚未暂停在可确认批次")
	}
	provider, e := m.dp.saeFactory(m.st.GetConnection(o.Target.DeployConfig["connection"]))
	if e != nil {
		return e
	}
	ctrl, ok := provider.(saeReleaseControl)
	if !ok {
		return fmt.Errorf("当前 SAE 接入不支持批次控制")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	state, e := provider.Change(ctx, d.ExternalID)
	if e != nil {
		return e
	}
	if state.Status != 8 || state.SubStatus != 0 || state.PipelineID == "" {
		return fmt.Errorf("SAE 批次状态已变化，请核对云端变更单，不会重复确认")
	}
	if len(expectedPipeline) > 0 && state.PipelineID != expectedPipeline[0] {
		return fmt.Errorf("SAE 观察批次已变化，不会确认其他批次")
	}
	if e = m.st.compareReleaseStatus(o.ID, "waiting_next_batch", "running", "", false); e != nil {
		return e
	}
	if stop {
		e = ctrl.Stop(ctx, d.ExternalID)
	} else {
		e = ctrl.Confirm(ctx, state.PipelineID)
	}
	// A transport error can occur after the cloud accepts a command. Reconcile
	// by polling the SAME change order; never retry a confirm or redeploy.
	if e == nil && !stop {
		key := fmt.Sprintf("sae-confirmations:%d", d.ID)
		var count string
		m.st.db.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, key).Scan(&count)
		m.st.db.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, fmt.Sprint(policyInt(map[string]string{"n": count}, "n", 0)+1))
	}
	if e != nil {
		m.dp.emit(d.ID, "⚠ 云端控制返回异常，将核对同一变更单状态，不自动重发："+e.Error()+"\n")
	}
	m.st.db.Exec(`UPDATE deploys SET status='running',ended_at=NULL WHERE id=? AND status='waiting_next_batch'`, d.ID)
	m.st.Audit(actor, "sae-batch-control", "release", fmt.Sprint(o.ID), map[string]any{"stop": stop, "pipeline": state.PipelineID, "change_order": d.ExternalID})
	go func() {
		wait, cancel := context.WithTimeout(context.Background(), time.Duration(policyInt(o.Target.DeployConfig, "timeout_minutes", 30))*time.Minute)
		defer cancel()
		status := pollSAEChangeAfter(m.dp, d.ID, provider, wait, d.ExternalID, state.PipelineID)
		if o.Recovery && status == "success" {
			status = "rolled-back"
		}
		m.dp.mu.Lock()
		lt := m.dp.logs[d.ID]
		delete(m.dp.logs, d.ID)
		m.dp.mu.Unlock()
		if lt != nil {
			m.dp.st.AppendDeployLog(d.ID, lt.String())
		}
		m.dp.st.FinishDeploy(d.ID, status)
		m.dp.publishEvent(d.Service, d.Version, d.Host, d.Environment, d.ArtifactDigest, status)
		if o.Recovery && status == "rolled-back" {
			m.st.SetReleaseStatus(o.ID, "rolled-back", "", true)
			return
		}
		if stop && status == "canceled" {
			m.st.SetReleaseStatus(o.ID, "canceled", "", true)
			return
		}
		m.monitorBatch(o.ID, []int64{d.ID}, len(o.Hosts))
	}()
	return nil
}
