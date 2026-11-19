package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// A trusted metrics adapter must scope observations to the actual revision and
// batch. Whole-application averages cannot authorize a canary continuation.
type BatchMetric struct {
	Service        string   `json:"service"`
	Environment    string   `json:"environment"`
	ArtifactDigest string   `json:"artifact_digest"`
	ReleaseID      int64    `json:"release_id"`
	Batch          string   `json:"batch"`
	WindowStart    int64    `json:"window_start"`
	WindowEnd      int64    `json:"window_end"`
	Requests       int64    `json:"requests"`
	ErrorRate      *float64 `json:"error_rate"`
	P95Millis      *float64 `json:"p95_ms"`
}
type BatchObservation struct {
	ReleaseID     int64        `json:"release_id"`
	Batch         string       `json:"batch"`
	StartedAt     int64        `json:"started_at"`
	LastSampleAt  int64        `json:"last_sample_at"`
	LastWindowEnd int64        `json:"last_window_end"`
	Consecutive   int          `json:"consecutive"`
	State         string       `json:"state"`
	Message       string       `json:"message"`
	Metric        *BatchMetric `json:"metric,omitempty"`
	Revision      int64        `json:"revision"`
}

func evaluateBatchMetric(m BatchMetric, p MonitorGate, o *ReleaseOrder, batch string, start, now int64) error {
	if m.Service != o.Service || m.Environment != o.Environment || m.ArtifactDigest == "" || m.ArtifactDigest != o.TargetArtifactDigest() || m.ReleaseID != o.ID || m.Batch != batch {
		return fmt.Errorf("监控未能识别本次制品和批次")
	}
	if m.WindowStart < start+int64(p.WarmupSeconds) || m.WindowEnd > now+2 || now-m.WindowEnd > 15 || m.WindowEnd-m.WindowStart < int64(p.WindowSeconds) || m.Requests < p.MinRequests {
		return fmt.Errorf("监控窗口未完整、已过期或流量不足")
	}
	if m.ErrorRate == nil || m.P95Millis == nil || *m.ErrorRate < 0 || *m.ErrorRate > 1 || *m.P95Millis < 0 {
		return fmt.Errorf("监控缺少有效错误率或延迟")
	}
	if *m.ErrorRate > p.MaxErrorRate || *m.P95Millis > p.MaxP95Millis {
		return fmt.Errorf("监控阈值未达标，停止自动放行")
	}
	return nil
}

// Artifact identity is obtained from the frozen deploy record by the caller;
// this transient value is never serialized as a replacement artifact snapshot.
func (o *ReleaseOrder) TargetArtifactDigest() string { return o.observationArtifactDigest }

func (s *Store) saveBatchObservation(key string, o *BatchObservation) error {
	raw, _ := json.Marshal(o)
	r, err := s.db.Exec(`UPDATE delivery_observations SET document=?,revision=revision+1 WHERE observation_key=? AND revision=?`, string(raw), key, o.Revision)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return ErrRunConflict
	}
	o.Revision++
	return nil
}
func (s *Store) observeDeliveryBatch(run *DeliveryRun, step DeliveryStep, m *ReleaseManager) (string, error) {
	return s.observeDeliveryBatchWithClient(run, step, m, operationHTTPClient(10*time.Second), nil)
}
func (s *Store) observeDeliveryBatchWithClient(run *DeliveryRun, step DeliveryStep, m *ReleaseManager, client *http.Client, continueBatch func(*ReleaseOrder, string) error) (string, error) {
	var node *DeliveryNode
	for i := range run.Plan.Nodes {
		if run.Plan.Nodes[i].ID == step.NodeID {
			node = &run.Plan.Nodes[i]
		}
	}
	if node == nil || node.Monitor == nil {
		return "", fmt.Errorf("未配置自动观察")
	}
	if err := s.checkDeliveryPolicy(&run.Plan); err != nil {
		return "", err
	}
	o := s.GetReleaseOrder(step.ReleaseID)
	if o == nil || o.Status != "waiting_next_batch" || o.Recovery {
		return "", fmt.Errorf("不是可自动推进的发布批次")
	}
	a := s.GetArtifact(o.ArtifactID)
	if a == nil {
		return "", fmt.Errorf("制品不存在")
	}
	o.observationArtifactDigest = a.Digest
	matched := false
	for _, app := range run.Plan.Services {
		if app.Service == o.Service && app.Artifact.ID == a.ID && app.Artifact.Digest == a.Digest {
			matched = true
		}
	}
	if !matched {
		return "", fmt.Errorf("当前批次与审核制品不一致")
	}
	batch := fmt.Sprintf("hosts-%d", o.NextIndex)
	if o.Target.ManagedTarget() {
		ids := s.ReleaseDeployIDs(o.ID)
		if len(ids) != 1 {
			return "", fmt.Errorf("SAE 变更记录异常")
		}
		d := s.GetDeploy(ids[0])
		if d == nil || d.ExternalID == "" {
			return "", fmt.Errorf("SAE 变更单未就绪")
		}
		provider, err := m.dp.saeFactory(s.GetConnection(o.Target.DeployConfig["connection"]))
		if err != nil {
			return "", fmt.Errorf("SAE 状态查询不可用")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		state, err := provider.Change(ctx, d.ExternalID)
		cancel()
		if err != nil || state.Status != 8 || state.SubStatus != 0 || state.PipelineID == "" {
			return "", fmt.Errorf("SAE 未暂停在有效批次，不能自动放行")
		}
		batch = state.PipelineID
	}
	key := digestJSON([]any{run.ID, step.NodeID, o.ID, batch})
	var raw string
	var rev int64
	observation := BatchObservation{ReleaseID: o.ID, Batch: batch, StartedAt: time.Now().Unix(), State: "observing", Revision: 1}
	if err := s.db.QueryRow(`SELECT document,revision FROM delivery_observations WHERE observation_key=?`, key).Scan(&raw, &rev); err == nil {
		if json.Unmarshal([]byte(raw), &observation) != nil {
			return "", fmt.Errorf("观察记录损坏")
		}
		observation.Revision = rev
	} else {
		rawBytes, _ := json.Marshal(observation)
		if _, err = s.db.Exec(`INSERT INTO delivery_observations(observation_key,run_id,document,revision) VALUES(?,?,?,1)`, key, run.ID, string(rawBytes)); err != nil {
			return "", err
		}
	}
	if observation.State != "observing" {
		return "", fmt.Errorf("%s；请核对观察记录，不能自动重复放行", observation.Message)
	}
	p := *node.Monitor
	now := time.Now().Unix()
	if now-observation.StartedAt > int64(p.TimeoutSeconds) {
		observation.State = "inconclusive"
		observation.Message = "观察超时或样本不足，转人工核对"
		_ = s.saveBatchObservation(key, &observation)
		return "", fmt.Errorf("%s", observation.Message)
	}
	if now < observation.StartedAt+int64(p.WarmupSeconds+p.WindowSeconds) || now-observation.LastSampleAt < 10 {
		return "wait", nil
	}
	observation.LastSampleAt = now
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	code, body, err := readOperationHTTP(ctx, client, p.URL, url.Values{"service": {o.Service}, "environment": {o.Environment}, "artifact_digest": {a.Digest}, "release_id": {strconv.FormatInt(o.ID, 10)}, "batch": {batch}, "window_start": {strconv.FormatInt(observation.StartedAt+int64(p.WarmupSeconds), 10)}, "window_seconds": {strconv.Itoa(p.WindowSeconds)}})
	cancel()
	var metric BatchMetric
	if err == nil && (code != 200 || json.Unmarshal(body, &metric) != nil) {
		err = fmt.Errorf("监控响应无效")
	}
	if err == nil {
		err = evaluateBatchMetric(metric, p, o, batch, observation.StartedAt, now)
	}
	if err != nil {
		observation.State = "inconclusive"
		observation.Message = err.Error() + "；保持暂停，不自动回滚"
		observation.Metric = &metric
		_ = s.saveBatchObservation(key, &observation)
		s.Audit(run.Owner, "observe-batch-blocked", "release", fmt.Sprint(o.ID), map[string]any{"batch": batch, "message": observation.Message})
		return "", fmt.Errorf("%s", observation.Message)
	}
	if metric.WindowEnd <= observation.LastWindowEnd {
		_ = s.saveBatchObservation(key, &observation)
		return "wait", nil
	}
	observation.LastWindowEnd = metric.WindowEnd
	observation.Consecutive++
	observation.Metric = &metric
	observation.Message = "本次样本达标"
	if observation.Consecutive < p.Consecutive || o.ContinueAfter > now {
		if err := s.saveBatchObservation(key, &observation); err != nil {
			return "", err
		}
		return "wait", nil
	}
	// Durable claim precedes the external continue. A timeout/restart never
	// reissues the same cloud confirmation or starts a second host batch.
	observation.State = "continuing"
	observation.Message = "观察已达标，正在确认同一批次；中断后须核对"
	if err := s.saveBatchObservation(key, &observation); err != nil {
		return "", err
	}
	if continueBatch != nil {
		err = continueBatch(o, batch)
	} else if o.Target.ManagedTarget() {
		err = m.controlSAERelease(o, run.Owner, false, batch)
	} else {
		err = m.Continue(o.ID, run.Owner)
	}
	if err != nil {
		observation.State = "uncertain"
		observation.Message = "批次继续结果待核对，禁止自动重发"
		_ = s.saveBatchObservation(key, &observation)
		return "", fmt.Errorf("%s", observation.Message)
	}
	observation.State = "continued"
	observation.Message = "指标达标，已请求继续下一批"
	if err = s.saveBatchObservation(key, &observation); err != nil {
		return "", err
	}
	s.Audit(run.Owner, "auto-continue-batch", "release", fmt.Sprint(o.ID), map[string]any{"batch": batch, "plan_digest": run.Digest, "metric": metric})
	return "wait", nil
}
