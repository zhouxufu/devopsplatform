package deploy

import (
	"context"
	"errors"
	"fmt"
	"time"
)

func deliveryResourceKind(kind string) bool {
	return kind == "mysql" || kind == "redis" || kind == "nacos" || kind == xxlJobKind
}

type deliveryResourceOperation func(context.Context, *DeliveryRun) (string, error)

func (s *Store) resourceOperation(plan *DeliveryPlan, step *DeliveryStep) (deliveryResourceOperation, error) {
	switch step.Kind {
	case xxlJobKind:
		for _, p := range plan.XXLJobPlans {
			if p.NodeID == step.NodeID {
				return func(ctx context.Context, r *DeliveryRun) (string, error) { return s.executeXXLJob(ctx, r, p) }, nil
			}
		}
	case "mysql":
		for _, p := range plan.SQLPlans {
			if p.NodeID == step.NodeID {
				return func(ctx context.Context, r *DeliveryRun) (string, error) { return s.executeSQLNode(ctx, r, p) }, nil
			}
		}
	case "nacos":
		for _, p := range plan.NacosPlans {
			if p.NodeID == step.NodeID {
				return func(ctx context.Context, r *DeliveryRun) (string, error) { return s.executeNacosNode(ctx, r, p) }, nil
			}
		}
	case "redis":
		for _, p := range plan.RedisPlans {
			if p.NodeID == step.NodeID {
				return func(ctx context.Context, r *DeliveryRun) (string, error) { return s.executeRedisNode(ctx, r, p) }, nil
			}
		}
	}
	return nil, fmt.Errorf("%s 节点缺少已审核执行计划", step.Name)
}

// Claim before dispatch. After restart the executing marker needs reconciliation;
// neither a poll nor lease expiry grants permission to repeat the resource write.
func (s *Store) dispatchResourceStep(run *DeliveryRun, index int, actor string, operation deliveryResourceOperation) error {
	step := &run.Steps[index]
	if step.Status != "pending" {
		return fmt.Errorf("节点已经派发，请核对原执行回执")
	}
	step.Status, run.Status = "executing", "running"
	if run.Plan.ExecutionMode == dependencyExecution {
		step.LeaseUntil = time.Now().Add(3 * time.Minute).Unix()
	}
	if err := s.saveDeliveryRun(run); err != nil {
		return err
	}
	execute := func(snapshot *DeliveryRun) (string, string) {
		ctx, cancel := sqlExecutionContext()
		defer cancel()
		detail, err := operation(ctx, snapshot)
		if err != nil {
			if detail == "" {
				detail = err.Error()
			}
			if errors.Is(err, errXXLJobUncertain) {
				return "uncertain", detail
			}
			return "failed", detail
		}
		return "success", detail
	}
	auditKind := map[string]string{"mysql": "sql", "nacos": "nacos", "redis": "redis", xxlJobKind: "xxl-job"}[step.Kind]
	audit := func(r *DeliveryRun, st DeliveryStep) {
		s.Audit(actor, "execute-"+auditKind, "delivery-run", fmt.Sprint(r.ID), map[string]any{"node": st.NodeID, "status": st.Status, "digest": r.Digest})
	}
	if run.Plan.ExecutionMode != dependencyExecution {
		step.Status, step.Detail = execute(run)
		step.EndedAt = time.Now().Unix()
		if step.Status == "failed" {
			run.Status = "failed"
		}
		if err := s.saveDeliveryRun(run); err != nil {
			return err
		}
		audit(run, *step)
		return nil
	}
	// The worker owns a detached input and merges only its receipt into the newest
	// revision. Retrying this merge never calls the external target a second time.
	snapshot := *run
	snapshot.Steps = append([]DeliveryStep(nil), run.Steps...)
	go func() {
		status, detail := execute(&snapshot)
		for attempt := 0; attempt < 64; attempt++ {
			latest := s.deliveryRunLedger(snapshot.ID)
			if latest == nil || latest.Digest != snapshot.Digest || index >= len(latest.Steps) {
				return
			}
			st := &latest.Steps[index]
			claim := snapshot.Steps[index]
			if st.NodeID != claim.NodeID || st.Status != "executing" || st.StartedAt != claim.StartedAt || st.LeaseUntil != claim.LeaseUntil {
				return
			}
			st.Status, st.Detail, st.EndedAt, st.LeaseUntil = status, detail, time.Now().Unix(), 0
			err := s.saveDeliveryRun(latest)
			if err == nil {
				audit(latest, *st)
				return
			}
			if !errors.Is(err, ErrRunConflict) {
				return
			}
		}
	}()
	return nil
}
