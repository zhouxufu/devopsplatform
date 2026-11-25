package deploy

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type DeliveryControl struct {
	State      string `json:"state"`
	Actor      string `json:"actor"`
	Message    string `json:"message"`
	LeaseUntil int64  `json:"lease_until"`
	lease      string
}

func (s *Store) DeliveryControl(id int64) DeliveryControl {
	c := DeliveryControl{State: "idle", Message: "尚未启动自动推进"}
	if s.db.QueryRow(`SELECT state,actor,lease,lease_until,message FROM delivery_controls WHERE run_id=?`, id).Scan(&c.State, &c.Actor, &c.lease, &c.LeaseUntil, &c.Message) != nil {
		return DeliveryControl{State: "idle", Message: "尚未启动自动推进"}
	}
	if (c.State == "running" || c.State == "pausing") && c.LeaseUntil < time.Now().Unix() {
		c.State = "interrupted"
		c.Message = "执行协调器已中断，请核对当前节点后手动恢复；不会自动重放写操作"
	}
	return c
}
func (s *Store) claimDeliveryControl(id int64, actor string) (string, error) {
	run := s.deliveryRunLedger(id)
	if run != nil && run.Plan.PreparationRequest != nil {
		s.projectResearchCodeState(run)
	}
	if run == nil || (run.Status != "approved" && run.Status != "running") {
		return "", fmt.Errorf("仅待执行或执行中的交付可启动")
	}
	bytes := make([]byte, 16)
	if _, e := rand.Read(bytes); e != nil {
		return "", e
	}
	lease := hex.EncodeToString(bytes)
	now := time.Now().Unix()
	if _, e := s.db.Exec(`INSERT OR IGNORE INTO delivery_controls(run_id,state,actor,lease,lease_until,message) VALUES(?,'idle','','',0,'')`, id); e != nil {
		return "", e
	}
	r, e := s.db.Exec(`UPDATE delivery_controls SET state='running',actor=?,lease=?,lease_until=?,message='自动推进中；人工与观察节点会暂停' WHERE run_id=? AND (state NOT IN ('running','pausing') OR lease_until<?)`, actor, lease, now+45, id, now)
	if e != nil {
		return "", e
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return "", fmt.Errorf("本次交付已有执行协调器，不能重复启动")
	}
	return lease, nil
}
func (s *Store) finishDeliveryControl(id int64, lease, state, message string) {
	s.db.Exec(`UPDATE delivery_controls SET state=?,message=?,lease_until=0 WHERE run_id=? AND lease=?`, state, message, id, lease)
}
func deliveryControlDecision(s *Store, run *DeliveryRun) (string, string) {
	if run == nil {
		return "finish", "执行单不存在"
	}
	if run.Status == "success" {
		return "complete", "交付已完成"
	}
	if run.Status != "approved" && run.Status != "running" {
		return "finish", "交付已停止：" + run.Status
	}
	for _, step := range run.Steps {
		if step.Status == "success" || step.Status == "skipped" {
			continue
		}
		if step.Status == "executing" && !(step.Kind == "deploy" && step.ReleaseID > 0) {
			if step.Kind == "extension" && step.LeaseUntil > time.Now().Unix() {
				return "wait", "编排插件任务执行中"
			}
			if step.Kind == "script" && step.LeaseUntil > time.Now().Unix() {
				return "wait", "交付脚本执行中"
			}
			return "pause", "节点执行中或上次中断，必须先核对台账，禁止自动重复执行"
		}
		if step.Kind == "deploy" && step.Status == "failed" {
			return "pause", "应用发布失败，等待重试或人工处理；已完成步骤保留"
		}
		if step.Kind == "manual" {
			return "pause", "等待人工确认：" + step.Name
		}
		for _, node := range run.Plan.Nodes {
			if node.ID == step.NodeID && node.Kind == "verify" && node.Verification != nil && node.Verification.Mode == "manual" {
				return "pause", "等待独立验证人员提交结论：" + step.Name
			}
		}
		if step.ReleaseID > 0 {
			child := s.GetReleaseOrder(step.ReleaseID)
			if child == nil {
				return "pause", "子发布单不存在，请核对"
			}
			if child.Status == "waiting_manual" {
				return "pause", "等待负责人下载制品并登记手动发布结果：" + step.Service
			}
			if child.Status == "waiting_next_batch" {
				return "pause", "等待批次观察确认，请核对后继续下一批"
			}
			if child.Status == "needs_review" {
				return "wait", "应用启动较慢或结果待核对；保留进程与原执行身份，不重发"
			}
			if releaseStatusActive(child.Status) {
				return "wait", "应用发布中"
			}
		}
		return "advance", ""
	}
	return "advance", ""
}

// A durable lease prevents competing workers. A restart never replays a write:
// its executing marker remains a hard gate until an operator reconciles it.
func (s *Store) runDeliveryControl(id int64, actor, lease string, m *ReleaseManager) {
	s.runDeliveryControlWithAdvance(id, lease, func() error { _, err := s.AdvanceDeliveryRun(id, actor, false, m); return err }, m)
}

func (s *Store) runDeliveryControlWithAdvance(id int64, lease string, advance func() error, managers ...*ReleaseManager) {
	done := make(chan struct{})
	defer close(done)
	go func() {
		tick := time.NewTicker(10 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				s.db.Exec(`UPDATE delivery_controls SET lease_until=? WHERE run_id=? AND lease=? AND state IN ('running','pausing')`, time.Now().Unix()+45, id, lease)
			}
		}
	}()
	for started := time.Now(); time.Since(started) < 2*time.Hour; {
		control := s.DeliveryControl(id)
		if control.lease != lease || (control.State != "running" && control.State != "pausing") {
			return
		}
		if control.State == "pausing" {
			s.finishDeliveryControl(id, lease, "paused", "已暂停后续步骤；已启动的应用批次仍须在节点中核对")
			return
		}
		run := s.deliveryRunLedger(id)
		if run != nil && run.Plan.PreparationRequest != nil {
			s.projectResearchCodeState(run)
		}
		decision, message := deliveryControlDecision(s, run)
		if decision == "pause" && run != nil && len(managers) > 0 {
			for _, step := range run.Steps {
				if step.Status == "success" || step.Status == "skipped" {
					continue
				}
				if step.ReleaseID > 0 {
					for _, node := range run.Plan.Nodes {
						if node.ID == step.NodeID && node.Monitor != nil {
							if child := s.GetReleaseOrder(step.ReleaseID); child != nil && child.Status == "waiting_next_batch" {
								var err error
								decision, err = s.observeDeliveryBatch(run, step, managers[0])
								if err != nil {
									decision, message = "pause", err.Error()
								}
							}
						}
					}
				}
				break
			}
		}
		switch decision {
		case "pause":
			s.finishDeliveryControl(id, lease, "paused", message)
			return
		case "finish":
			s.finishDeliveryControl(id, lease, "stopped", message)
			return
		case "complete":
			s.finishDeliveryControl(id, lease, "completed", message)
			return
		case "wait":
			time.Sleep(750 * time.Millisecond)
			continue
		}
		if err := advance(); err != nil {
			s.finishDeliveryControl(id, lease, "paused", err.Error())
			return
		}
	}
	s.finishDeliveryControl(id, lease, "paused", "自动推进达到时限，请核对后手动继续")
}
func registerDeliveryControlRoutes(mux *http.ServeMux, s *Store, m *ReleaseManager) {
	mux.HandleFunc("GET /api/delivery/runs/{id}/control", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if s.DeliveryRun(id) == nil {
			deliveryWriteError(w, 404, "执行单不存在")
			return
		}
		deliveryJSON(w, s.DeliveryControl(id))
	})
	for _, action := range []string{"start", "pause", "stop"} {
		action := action
		mux.HandleFunc("POST /api/delivery/runs/{id}/"+action, func(w http.ResponseWriter, r *http.Request) {
			id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
			run := s.DeliveryRun(id)
			if run == nil || !deliveryManage(r, run.Owner) {
				deliveryWriteError(w, 403, "仅发起人或管理员可控制交付")
				return
			}
			if action == "start" {
				if err := validateDeliveryReviewerDirectory(&run.Plan, r.Header.Get("X-Reviewer-Directory"), run.Owner); err != nil {
					deliveryWriteError(w, 409, err)
					return
				}
				if err := s.checkDeliveryPolicy(&run.Plan); err != nil {
					deliveryWriteError(w, 409, err)
					return
				}
			}
			var body struct {
				Digest  string `json:"digest"`
				Confirm bool   `json:"confirm"`
				Reason  string `json:"reason"`
			}
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil || body.Digest != run.Digest || !body.Confirm {
				deliveryWriteError(w, 409, "请核对快照并明确确认此操作")
				return
			}
			if action == "stop" {
				control := s.DeliveryControl(id)
				if (run.Status != "approved" && run.Status != "running") || control.State == "running" || control.State == "pausing" || strings.TrimSpace(body.Reason) == "" || len(body.Reason) > 512 {
					deliveryWriteError(w, 409, "请先暂停协调器、核对当前结果并填写终止原因")
					return
				}
				for i := range run.Steps {
					step := &run.Steps[i]
					if step.Kind == "script" && step.Status == "executing" {
						deliveryWriteError(w, 409, "脚本执行中或中断待核对，请先在节点中记录核对结果")
						return
					}
					if step.ReleaseID <= 0 {
						continue
					}
					child := s.GetReleaseOrder(step.ReleaseID)
					if child == nil || releaseStatusActive(child.Status) {
						deliveryWriteError(w, 409, "子发布仍在运行或等待批次，请先在节点中核对处理")
						return
					}
					if child.Status == "success" {
						step.Status = "success"
					} else {
						step.Status = "failed"
					}
					step.EndedAt = time.Now().Unix()
				}
				run.Status = "failed"
				if err := s.saveDeliveryRun(run); err != nil {
					deliveryWriteError(w, 409, err)
					return
				}
				s.db.Exec("UPDATE delivery_controls SET state='stopped',lease_until=0,message='操作人终止后续编排；不确定的写操作仍须核对' WHERE run_id=?", id)
				s.Audit(deliveryActor(r), "stop-delivery", "delivery-run", fmt.Sprint(id), map[string]any{"reason": body.Reason})
				deliveryJSON(w, run)
				return
			}
			if action == "pause" {
				s.db.Exec(`UPDATE delivery_controls SET state='pausing',message='已请求暂停：当前操作结束后停止后续步骤' WHERE run_id=? AND state='running'`, id)
				s.Audit(deliveryActor(r), "pause-delivery", "delivery-run", fmt.Sprint(id), nil)
				deliveryJSON(w, s.DeliveryControl(id))
				return
			}
			lease, e := s.claimDeliveryControl(id, deliveryActor(r))
			if e != nil {
				deliveryWriteError(w, 409, e)
				return
			}
			s.Audit(deliveryActor(r), "start-delivery", "delivery-run", fmt.Sprint(id), nil)
			go s.runDeliveryControl(id, deliveryActor(r), lease, m)
			deliveryJSON(w, s.DeliveryControl(id))
		})
	}
}
