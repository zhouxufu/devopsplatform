package deploy

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const javaAutoWindow = int64(15 * 60)
const javaAutoLease = int64(120)

type JavaAutoRecheck struct {
	State      string `json:"state"`
	Attempts   int    `json:"attempts"`
	StartedAt  int64  `json:"started_at"`
	Deadline   int64  `json:"deadline"`
	NextAt     int64  `json:"next_at,omitempty"`
	LastAt     int64  `json:"last_at,omitempty"`
	LeaseUntil int64  `json:"lease_until,omitempty"`
	Message    string `json:"message,omitempty"`
}

func javaAutoKey(id int64) string { return fmt.Sprintf("java_auto_recheck:%d", id) }
func (s *Store) javaAutoState(id int64) (*JavaAutoRecheck, string, error) {
	var raw string
	err := s.db.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, javaAutoKey(id)).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	var v JavaAutoRecheck
	if err = json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, raw, err
	}
	return &v, raw, nil
}
func (s *Store) saveJavaAutoState(id int64, v *JavaAutoRecheck, before string) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	after := string(data)
	if before == "" {
		_, err = s.db.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?)`, javaAutoKey(id), after)
	} else {
		var result sql.Result
		result, err = s.db.Exec(`UPDATE deploy_meta SET value=? WHERE key=? AND value=?`, after, javaAutoKey(id), before)
		if err == nil {
			n, e := result.RowsAffected()
			if e != nil {
				err = e
			} else if n != 1 {
				err = ErrRunConflict
			}
		}
	}
	return after, err
}

func javaAutoEligible(o *ReleaseOrder) bool {
	return o != nil && (o.Status == "needs_review" || o.Status == "running") && o.Target.TargetType == "ssh-jar" && o.Target.DeployConfig["java_mode"] == "managed" && !deploymentAgentTarget(o.Target.DeployConfig)
}
func javaAutoDelay(attempt int) int64 {
	if attempt == 1 {
		return 30
	}
	return 60
}

// The claim is durable before any observation. A crashed observer may repeat
// the read-only probe after its lease expires, never the original deployment.
func (m *ReleaseManager) autoRecheckJava(id, now int64, exec javaExec) bool {
	v, before, err := m.st.javaAutoState(id)
	if err != nil {
		return false
	}
	if v == nil {
		v = &JavaAutoRecheck{State: "scheduled", StartedAt: now, Deadline: now + javaAutoWindow, NextAt: now + 15, Message: "等待自动核对原发布结果"}
		if _, err = m.st.saveJavaAutoState(id, v, ""); err == nil {
			m.st.AppendDeployLog(id, "已进入自动核对，最多观察 15 分钟；不重启、不重新发布")
		}
		return false
	}
	if v.State == "manual" || v.State == "completed" || v.LeaseUntil > now {
		return false
	}
	if now >= v.Deadline {
		v.State = "manual"
		v.NextAt = 0
		v.LeaseUntil = 0
		v.Message = "自动核对已超过 15 分钟，仍无法确认原发布结果。" + v.Message
		if _, err = m.st.saveJavaAutoState(id, v, before); err == nil {
			m.st.AppendDeployLog(id, v.Message+" 请通过原发布核对入口处理。")
		}
		return false
	}
	if v.NextAt > now {
		return false
	}
	v.State = "checking"
	v.Attempts++
	v.LastAt = now
	v.LeaseUntil = now + javaAutoLease
	v.NextAt = 0
	claimed, err := m.st.saveJavaAutoState(id, v, before)
	if err != nil {
		return false
	}
	err = m.recheckManagedJava(id, exec)
	v.LeaseUntil = 0
	d := m.st.GetDeploy(id)
	if d != nil && d.Status != "needs_review" {
		v.State = "completed"
		v.Message = "原发布结果已确认：" + d.Status
	} else if err != nil {
		v.Message = err.Error()
		var stop *javaRecheckEvidenceError
		if errors.As(err, &stop) && stop.Manual {
			v.State = "manual"
		} else {
			v.State = "scheduled"
			v.NextAt = now + javaAutoDelay(v.Attempts)
		}
	} else {
		v.State = "scheduled"
		v.NextAt = now + 60
		v.Message = "原发布结果待重新读取"
	}
	if _, e := m.st.saveJavaAutoState(id, v, claimed); e == nil && v.State == "manual" {
		m.st.AppendDeployLog(id, "自动核对已暂停："+v.Message)
	}
	return true
}

// Bounded, rotating scan: historical/manual records cannot starve newer ones.
func (m *ReleaseManager) sweepJavaRechecks(now int64, exec javaExec, after int64) int64 {
	rows, err := m.st.db.Query(`SELECT d.id,rd.release_id FROM deploys d JOIN release_order_deploys rd ON rd.deploy_id=d.id JOIN release_orders o ON o.id=rd.release_id WHERE o.status IN ('needs_review','running') AND d.id>? AND NOT EXISTS (SELECT 1 FROM agent_deployment_tasks a WHERE a.deploy_id=d.id) ORDER BY d.id LIMIT 16`, after)
	if err != nil {
		return after
	}
	type item struct{ id, release int64 }
	items := []item{}
	for rows.Next() {
		var v item
		if rows.Scan(&v.id, &v.release) == nil {
			items = append(items, v)
		}
	}
	rows.Close()
	attempts := 0
	last := after
	for _, v := range items {
		select {
		case <-m.st.deploymentAgentStop:
			return last
		default:
		}
		last = v.id
		o := m.st.GetReleaseOrder(v.release)
		if !javaAutoEligible(o) {
			continue
		}
		d := m.st.GetDeploy(v.id)
		if d == nil {
			continue
		}
		if o.Status == "running" && d.Status != "needs_review" {
			observed, _, err := m.st.javaAutoState(d.ID)
			if err != nil || observed == nil {
				continue
			}
		}
		// Only reconcile observed warning paths; ordinary rolling releases
		// remain owned by their normal batch coordinator.
		m.reconcileJavaRecheck(o.ID)
		if d.Status != "needs_review" {
			continue
		}
		if attempts >= 4 {
			return v.id - 1
		}
		if m.autoRecheckJava(v.id, now, exec) {
			attempts++
		}
	}
	if len(items) < 16 {
		return 0
	}
	return last
}
func (m *ReleaseManager) startJavaReconciler() {
	m.st.javaRecheckOnce.Do(func() {
		m.st.deploymentAgentWG.Add(1)
		go func() {
			defer m.st.deploymentAgentWG.Done()
			timer := time.NewTicker(15 * time.Second)
			defer timer.Stop()
			after := int64(0)
			for {
				select {
				case <-m.st.deploymentAgentStop:
					return
				default:
				}
				after = m.sweepJavaRechecks(time.Now().Unix(), SSHExec, after)
				select {
				case <-m.st.deploymentAgentStop:
					return
				case <-timer.C:
				}
			}
		}()
	})
}

// Only expose bounded operational metadata, never remote probe output or secrets.
func (s *Store) attachJavaAutoStates(deploys []Deploy) {
	keys := []string{}
	args := []any{}
	indexes := map[string]int{}
	for i, d := range deploys {
		if d.Status == "needs_review" {
			key := javaAutoKey(d.ID)
			keys = append(keys, "?")
			args = append(args, key)
			indexes[key] = i
		}
	}
	if len(keys) == 0 {
		return
	}
	rows, err := s.db.Query(`SELECT key,value FROM deploy_meta WHERE key IN (`+strings.Join(keys, ",")+`)`, args...)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var key, raw string
		if rows.Scan(&key, &raw) != nil {
			continue
		}
		i, ok := indexes[key]
		if !ok {
			continue
		}
		var v JavaAutoRecheck
		if json.Unmarshal([]byte(raw), &v) == nil {
			deploys[i].AutoRecheck = &v
		}
	}
}
