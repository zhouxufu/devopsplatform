package deploy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"logplatform/pkg/extensionclient"
	p "logplatform/pkg/extensionprotocol"
	"logplatform/pkg/workflownode"
	"time"
)

func workflowExecutionID(runID int64, nodeID string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("delivery:%d:node:%s", runID, nodeID)))
	return hex.EncodeToString(sum[:])
}
func invokeWorkflowJob(ctx context.Context, b *workflownode.Binding, id, operation string) (*workflownode.JobStatus, error) {
	client, err := extensionclient.FromEnvironment()
	if err != nil {
		return nil, err
	}
	input, _ := json.Marshal(workflownode.JobRequest{ExecutionID: id, NodeKey: b.NodeKey, Environment: b.Environment, Targets: b.Targets, Parameters: b.Parameters})
	out, err := client.Invoke(ctx, b.Provider, p.Request{Capability: workflownode.Capability, Operation: operation, ExpectedVersion: b.ProviderVersion, ExpectedDigest: b.ProviderDigest, Input: input})
	if err != nil {
		return nil, err
	}
	var status workflownode.JobStatus
	if json.Unmarshal(out.Data, &status) != nil || status.ExecutionID != id || len(status.Message) > 4096 {
		return nil, fmt.Errorf("插件任务响应身份或大小无效")
	}
	switch status.State {
	case "queued", "running", "success", "failed", "canceled", "unknown":
	default:
		return nil, fmt.Errorf("插件任务状态无效")
	}
	return &status, nil
}
func runWorkflowJob(ctx context.Context, b *workflownode.Binding, id string) (bool, error) {
	status, err := invokeWorkflowJob(ctx, b, id, "job.start")
	if err != nil {
		return true, fmt.Errorf("插件启动结果待核对，禁止自动重发：%w", err)
	}
	for {
		switch status.State {
		case "success":
			return false, nil
		case "failed", "canceled":
			return false, fmt.Errorf("插件任务%s：%s", status.State, status.Message)
		case "unknown":
			return true, fmt.Errorf("插件任务结果未知，请按执行标识核对")
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return true, fmt.Errorf("插件任务等待超时，结果待核对，不自动重发")
		case <-timer.C:
		}
		status, err = invokeWorkflowJob(ctx, b, id, "job.status")
		if err != nil {
			return true, fmt.Errorf("插件任务状态读取失败，结果待核对：%w", err)
		}
	}
}
func (s *Store) finishWorkflowExtension(runID int64, nodeID string, success, unknown bool, detail string) {
	// Reload for every CAS, preserving concurrent control/approval state.
	if len(detail) > 20000 {
		detail = detail[:20000]
	}
	for attempt := 0; attempt < 4; attempt++ {
		latest := s.DeliveryRun(runID)
		if latest == nil {
			return
		}
		found := false
		for i := range latest.Steps {
			step := &latest.Steps[i]
			if step.NodeID != nodeID || step.Status != "executing" {
				continue
			}
			found = true
			step.Detail = detail
			step.LeaseUntil = 0
			if !unknown {
				step.EndedAt = time.Now().Unix()
				if success {
					step.Status = "success"
				} else {
					step.Status = "failed"
					latest.Status = "failed"
				}
			}
		}
		if !found {
			return
		}
		if success && latest.Status == "running" {
			all := len(latest.Steps) > 0
			for _, step := range latest.Steps {
				all = all && step.Status == "success"
			}
			if all {
				latest.Status = "success"
			}
		}
		if s.saveDeliveryRun(latest) == nil {
			return
		}
	}
}
