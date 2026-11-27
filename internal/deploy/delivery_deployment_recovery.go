package deploy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type deliveryDeploymentRecoveryRequest struct {
	NodeID          string `json:"node_id"`
	Service         string `json:"service"`
	ReleaseID       int64  `json:"release_id"`
	Revision        int64  `json:"revision"`
	Digest          string `json:"digest"`
	Confirm         bool   `json:"confirm"`
	ConfirmArtifact bool   `json:"confirm_artifact"`
	Comment         string `json:"comment"`
}

type deliveryDeploymentRetry struct {
	ReleaseID  int64    `json:"release_id"`
	Revision   int64    `json:"revision"`
	Digest     string   `json:"digest"`
	NodeID     string   `json:"node_id"`
	Service    string   `json:"service"`
	ArtifactID int64    `json:"artifact_id"`
	Version    string   `json:"version"`
	Targets    []string `json:"targets"`
	Skipped    int      `json:"skipped"`
}

func (s *Store) deploymentRecoveryStep(v *DeliveryRun, req deliveryDeploymentRecoveryRequest) (*DeliveryStep, *DeliveryService, *ReleaseOrder, error) {
	if v == nil || (v.Status != "failed" && v.Status != "running") || v.Digest != req.Digest || v.Revision != req.Revision {
		return nil, nil, nil, fmt.Errorf("执行单已变化，请刷新后核对当前失败节点")
	}
	c := s.DeliveryControl(v.ID)
	if c.State == "stopped" && strings.HasPrefix(c.Message, "操作人终止") {
		return nil, nil, nil, fmt.Errorf("本次交付已被明确终止，请创建新的交付草稿")
	}
	if (c.State == "running" || c.State == "pausing") && c.LeaseUntil > time.Now().Unix() {
		return nil, nil, nil, fmt.Errorf("请等待协调器暂停后处理失败节点")
	}
	var step *DeliveryStep
	for i := range v.Steps {
		if v.Plan.ExecutionMode == dependencyExecution {
			st := &v.Steps[i]
			if st.Status == "executing" || st.Status == "uncertain" {
				return nil, nil, nil, fmt.Errorf("请先核对在途执行回执")
			}
			if st.NodeID == req.NodeID && st.Service == req.Service && !scheduleStepDone(*st) {
				n := nodeByID(v.Plan.Nodes, st.NodeID)
				if n != nil && blockedPredecessor(v, n) == "" {
					step = st
				}
			}
			continue
		}
		if v.Steps[i].Status != "success" {
			step = &v.Steps[i]
			break
		}
	}
	if step == nil || step.Kind != "deploy" || step.NodeID != req.NodeID || step.Service != req.Service || step.ReleaseID <= 0 || step.ReleaseID != req.ReleaseID {
		return nil, nil, nil, fmt.Errorf("只能处理当前未完成的应用发布节点，不能跳过前置步骤")
	}
	child := s.GetReleaseOrder(step.ReleaseID)
	if child == nil || child.Recovery || (child.Status != "failed" && child.Status != "interrupted") {
		return nil, nil, nil, fmt.Errorf("仅明确失败且未在回退的子发布单可处理；执行中和结果待核对须先核实")
	}
	var app *DeliveryService
	for i := range v.Plan.Services {
		if v.Plan.Services[i].Service == step.Service {
			app = &v.Plan.Services[i]
			break
		}
	}
	if app == nil || child.Service != app.Service || child.Environment != v.Plan.Environment.Name || child.ArtifactID != app.Artifact.ID {
		return nil, nil, nil, fmt.Errorf("子发布单与冻结的应用制品不一致")
	}
	artifact := s.GetArtifact(app.Artifact.ID)
	if artifact == nil || artifact.Service != app.Service || artifact.Digest != app.Artifact.Digest {
		return nil, nil, nil, fmt.Errorf("本次制品已变化，请重新预检和审核")
	}
	for _, id := range s.ReleaseDeployIDs(child.ID) {
		if d := s.GetDeploy(id); d == nil || d.Status == "running" || d.Status == "waiting_next_batch" {
			return nil, nil, nil, fmt.Errorf("仍有未结束的实例任务，请先核对实际结果")
		}
	}
	if _, err := s.validateDeliveryExecution(v); err != nil {
		return nil, nil, nil, err
	}
	return step, app, child, nil
}

func (s *Store) previewDeploymentRetry(v *DeliveryRun, req deliveryDeploymentRecoveryRequest) (*deliveryDeploymentRetry, error) {
	_, app, child, err := s.deploymentRecoveryStep(v, req)
	if err != nil {
		return nil, err
	}
	if child.Status != "failed" {
		return nil, fmt.Errorf("发布已中断，须先核实实际结果；不能直接重试")
	}
	if app.Target.ManualTarget() {
		return &deliveryDeploymentRetry{ReleaseID: child.ID, Revision: v.Revision, Digest: v.Digest, NodeID: req.NodeID, Service: app.Service, ArtifactID: app.Artifact.ID, Version: app.Artifact.Version, Targets: []string{}}, nil
	}
	candidates := map[string]bool{}
	approved := map[string]bool{}
	for _, host := range app.Target.ExecutionTargets() {
		approved[host] = true
	}
	for _, host := range child.Hosts {
		if !approved[host] || candidates[host] {
			return nil, fmt.Errorf("子发布目标与审核快照不一致")
		}
		candidates[host] = true
	}
	observed := map[string]bool{}
	successful := map[string]bool{}
	ids := s.ReleaseDeployIDs(child.ID)
	if len(ids) == 0 {
		return nil, fmt.Errorf("缺少完整实例执行记录，请先核实实际结果，再使用手动处理入口")
	}
	for _, id := range ids {
		d := s.GetDeploy(id)
		if d == nil || !candidates[d.Host] || observed[d.Host] || d.Service != child.Service || d.Environment != child.Environment || d.ArtifactID != child.ArtifactID || d.ArtifactDigest != app.Artifact.Digest {
			return nil, fmt.Errorf("实例执行记录不完整或不匹配，请先核实")
		}
		if d.Status != "success" && d.Status != "failed" && d.Status != "rolled-back" {
			return nil, fmt.Errorf("存在执行中、中断或超时的实例，不能直接重试，请先核实实际结果")
		}
		observed[d.Host] = true
		successful[d.Host] = d.Status == "success"
	}
	targets := []string{}
	for i, host := range child.Hosts {
		// A dispatched target without a terminal record has an unknown outcome.
		if i < child.NextIndex && !observed[host] {
			return nil, fmt.Errorf("已派发目标缺少执行结果，不能直接重试")
		}
		if !successful[host] {
			targets = append(targets, host)
		}
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("没有失败或尚未开始的目标可重试，请核对后记录处理结果")
	}
	return &deliveryDeploymentRetry{ReleaseID: child.ID, Revision: v.Revision, Digest: v.Digest, NodeID: req.NodeID, Service: app.Service, ArtifactID: app.Artifact.ID, Version: app.Artifact.Version, Targets: targets, Skipped: len(child.Hosts) - len(targets)}, nil
}

func validateRetryReleaseTx(tx *deployTx, id int64, expected string) error {
	var status string
	q := `SELECT status FROM release_orders WHERE id=?`
	if tx.dialect == "mysql" {
		q += " FOR UPDATE"
	}
	if err := tx.QueryRow(q, id).Scan(&status); err != nil {
		return err
	}
	if status != expected || (expected != "failed" && expected != "interrupted") {
		return fmt.Errorf("原发布状态已变化，请刷新后核对")
	}
	return nil
}

func (s *Store) retryDeliveryDeployment(v *DeliveryRun, req deliveryDeploymentRecoveryRequest, actor string, m *ReleaseManager, launch func(int64)) (*DeliveryRun, error) {
	retry, err := s.previewDeploymentRetry(v, req)
	if err != nil {
		return nil, err
	}
	step, _, _, err := s.deploymentRecoveryStep(v, req)
	if err != nil {
		return nil, err
	}
	step.ReleaseID = 0
	step.Status = "pending"
	v.Status = "running"
	next, err := s.advanceDeliveryRunState(v, actor, false, m, launch, retry)
	if err == nil {
		s.Audit(actor, "retry-deployment", "delivery-run", fmt.Sprint(v.ID), map[string]any{"previous_release_id": retry.ReleaseID, "node": req.NodeID, "service": req.Service, "targets": retry.Targets, "digest": req.Digest})
	}
	return next, err
}

func (s *Store) resolveDeliveryDeployment(v *DeliveryRun, req deliveryDeploymentRecoveryRequest, actor string) (*DeliveryRun, error) {
	step, app, child, err := s.deploymentRecoveryStep(v, req)
	if err != nil {
		return nil, err
	}
	if xxlJobReleaseDependency(v.Plan.Nodes, req.NodeID) {
		return nil, fmt.Errorf("定时任务启用依赖本次应用发布成功，请使用原发布的重试入口")
	}
	comment := strings.TrimSpace(req.Comment)
	if !req.ConfirmArtifact {
		return nil, fmt.Errorf("请确认已核对本次审核制品")
	}
	if comment == "" {
		return nil, fmt.Errorf("请填写处理结果与核对依据")
	}
	// Manual attestation is stored separately. The original failed release and all
	// instance outcomes remain untouched; it never becomes machine success proof.
	now := time.Now().Unix()
	step.Status = "success"
	step.EndedAt = now
	step.ManualRecovery = &DeliveryManualRecovery{Actor: actor, Comment: comment, At: now, ArtifactID: app.Artifact.ID, ArtifactDigest: app.Artifact.Digest}
	v.Status = deliveryCompletedStatus(v)
	for _, next := range v.Steps {
		if next.Status != "success" && !(v.Plan.ExecutionMode == dependencyExecution && scheduleStepDone(next)) {
			v.Status = "running"
			break
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = lockReleaseTarget(tx, app.Service, v.Plan.Environment.Name); err != nil {
		return nil, err
	}
	if err = validateRetryReleaseTx(tx, child.ID, child.Status); err != nil {
		return nil, err
	}
	execution, _ := json.Marshal(v.Steps)
	result, err := tx.Exec(`UPDATE delivery_runs SET status=?,execution=?,revision=revision+1 WHERE id=? AND revision=?`, v.Status, string(execution), v.ID, v.Revision)
	if err != nil {
		return nil, err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return nil, ErrRunConflict
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	v.Revision++
	s.Audit(actor, "manual-resolve-deployment", "delivery-run", fmt.Sprint(v.ID), map[string]any{"release_id": child.ID, "node": step.NodeID, "service": step.Service, "artifact_id": app.Artifact.ID, "comment": comment, "digest": v.Digest})
	return v, nil
}

func registerDeliveryDeploymentRecoveryRoutes(mux *http.ServeMux, s *Store, m *ReleaseManager) {
	owned := func(w http.ResponseWriter, r *http.Request) *DeliveryRun {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		v := s.DeliveryRun(id)
		if v == nil {
			deliveryWriteError(w, 404, "执行单不存在")
			return nil
		}
		if !deliveryManage(r, v.Owner) {
			deliveryWriteError(w, 403, "仅发起人或管理员可处理失败应用")
			return nil
		}
		return v
	}
	mux.HandleFunc("GET /api/delivery/runs/{id}/retry-preview", func(w http.ResponseWriter, r *http.Request) {
		v := owned(w, r)
		if v == nil {
			return
		}
		rid, _ := strconv.ParseInt(r.URL.Query().Get("release_id"), 10, 64)
		req := deliveryDeploymentRecoveryRequest{NodeID: r.URL.Query().Get("node_id"), Service: r.URL.Query().Get("service"), ReleaseID: rid, Revision: v.Revision, Digest: v.Digest}
		p, err := s.previewDeploymentRetry(v, req)
		if err != nil {
			deliveryWriteError(w, 409, err)
			return
		}
		deliveryJSON(w, p)
	})
	for _, action := range []string{"retry-deployment", "resolve-deployment"} {
		action := action
		mux.HandleFunc("POST /api/delivery/runs/{id}/"+action, func(w http.ResponseWriter, r *http.Request) {
			v := owned(w, r)
			if v == nil {
				return
			}
			var req deliveryDeploymentRecoveryRequest
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req) != nil || !req.Confirm {
				deliveryWriteError(w, 400, "请明确确认处理失败应用")
				return
			}
			if err := validateDeliveryReviewerDirectory(&v.Plan, r.Header.Get("X-Reviewer-Directory"), v.Owner); err != nil {
				deliveryWriteError(w, 409, err)
				return
			}
			var next *DeliveryRun
			var err error
			if action == "retry-deployment" {
				next, err = s.retryDeliveryDeployment(v, req, deliveryActor(r), m, func(id int64) { go m.startNextBatch(id) })
			} else {
				next, err = s.resolveDeliveryDeployment(v, req, deliveryActor(r))
			}
			if err != nil {
				deliveryWriteError(w, 409, err)
				return
			}
			deliveryJSON(w, next)
		})
	}
}
