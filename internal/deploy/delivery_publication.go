package deploy

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// The signed preflight nonce identifies one immutable release. The grant and
// its first coordinator lease are committed with the run, including on MySQL.
type deliveryPublication struct {
	ID          string `json:"id"`
	RunID       int64  `json:"run_id"`
	Owner       string `json:"owner"`
	Authority   string `json:"authority"`
	KeyID       string `json:"authority_key_id,omitempty"`
	Environment string `json:"environment"`
	Digest      string `json:"digest"`
	Expires     int64  `json:"expires_at"`
	Revision    int64  `json:"revision"`
	State       string `json:"state"`
}

func (p *deliveryPublication) insert(tx *deployTx, run *DeliveryRun, nonce string) error {
	if nonce == "" || run.Plan.Template.Workbench != "" {
		return fmt.Errorf("请预检正式发布范围后再提交执行")
	}
	env := run.Plan.Environment
	if env.Protected || (env.Purpose != "development" && env.Purpose != "test") {
		return fmt.Errorf("一次提交发布仅支持非受保护的开发和测试环境")
	}

	p.ID, p.RunID, p.Owner, p.Environment, p.Digest = nonce, run.ID, run.Owner, env.Name, run.Digest
	p.Expires = time.Now().Add(time.Hour).Unix()
	_, err := tx.Exec(`INSERT INTO delivery_publications(id,run_id,owner,authority,key_id,environment,digest,expires_at) VALUES(?,?,?,?,?,?,?,?)`, p.ID, p.RunID, p.Owner, p.Authority, p.KeyID, p.Environment, p.Digest, p.Expires)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO delivery_controls(run_id,state,actor,lease,lease_until,message) VALUES(?,'running',?,?,?,'已授权发布，等待推进')`, run.ID, p.Owner, p.ID, p.Expires)
	return err
}

func (s *Store) publication(id, owner string) (deliveryPublication, error) {
	var p deliveryPublication
	err := s.db.QueryRow(`SELECT p.id,p.run_id,p.owner,p.authority,p.key_id,p.environment,p.digest,p.expires_at,r.revision,c.state FROM delivery_publications p JOIN delivery_runs r ON r.id=p.run_id JOIN delivery_controls c ON c.run_id=p.run_id WHERE p.id=? AND p.owner=?`, id, owner).Scan(&p.ID, &p.RunID, &p.Owner, &p.Authority, &p.KeyID, &p.Environment, &p.Digest, &p.Expires, &p.Revision, &p.State)
	return p, err
}

func registerPublicationRoutes(mux *http.ServeMux, s *Store, m *ReleaseManager) {
	const base = "/api/delivery/publications"
	mux.HandleFunc("POST "+base, func(w http.ResponseWriter, r *http.Request) {
		var req DeliveryRequest
		actor := deliveryActor(r)
		authority, key := r.Header.Get("X-Work-Loop-Authority"), r.Header.Get("X-Work-Loop-Key-ID")
		if actor == "" || actor == "local" || (authority != "page" && authority != "mcp") || (authority == "mcp") != (key != "") {
			deliveryWriteError(w, 403, "缺少可核对的发布授权身份")
			return
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&req) != nil || req.PreflightToken == "" {
			deliveryWriteError(w, 400, "请先重新预检")
			return
		}
		req.publication = &deliveryPublication{Authority: authority, KeyID: key}
		req.reviewerDirectory = r.Header.Get("X-Reviewer-Directory")
		run, e := s.CreateDeliveryRun(req, actor, m)
		if e == nil {
			var grantID string
			e = s.db.QueryRow(`SELECT id FROM delivery_publications WHERE run_id=? AND owner=? AND authority=? AND key_id=?`, run.ID, actor, authority, key).Scan(&grantID)
			if e == sql.ErrNoRows {
				e = fmt.Errorf("该预检已通过其他入口提交，请打开原执行单核对")
			}
		}
		if e != nil {
			deliveryWriteError(w, 409, e)
			return
		}
		deliveryJSON(w, run)
	})
	// The platform dispatcher lists small identities only; it rechecks the
	// account and original MCP key before dispatching each subsequent step.
	mux.HandleFunc("GET "+base, func(w http.ResponseWriter, r *http.Request) {
		rows, e := s.db.Query(`SELECT p.id FROM delivery_publications p JOIN delivery_controls c ON c.run_id=p.run_id WHERE p.owner=? AND c.state IN ('running','pausing') AND c.lease=p.id`, deliveryActor(r))
		if e != nil {
			deliveryWriteError(w, 500, e)
			return
		}
		defer rows.Close()
		tasks := []map[string]string{}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				break
			}
			tasks = append(tasks, map[string]string{"id": id, "state": "running"})
		}
		if e == nil {
			e = rows.Err()
		}
		if e != nil {
			deliveryWriteError(w, 500, e)
			return
		}
		deliveryJSON(w, tasks)
	})
	owned := func(w http.ResponseWriter, r *http.Request) *deliveryPublication {
		p, e := s.publication(r.PathValue("id"), deliveryActor(r))
		if e != nil {
			deliveryWriteError(w, 404, "发布授权不存在")
			return nil
		}
		return &p
	}
	mux.HandleFunc("GET "+base+"/{id}", func(w http.ResponseWriter, r *http.Request) {
		p := owned(w, r)
		if p != nil {
			deliveryJSON(w, p)
		}
	})
	mux.HandleFunc("POST /internal/delivery/publications/{id}/blocked", func(w http.ResponseWriter, r *http.Request) {
		p := owned(w, r)
		if p == nil {
			return
		}
		var body struct {
			Reason string `json:"reason"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil {
			return
		}
		s.finishDeliveryControl(p.RunID, p.ID, "paused", body.Reason)
		deliveryJSON(w, s.DeliveryControl(p.RunID))
	})
	mux.HandleFunc("POST "+base+"/{id}/advance", func(w http.ResponseWriter, r *http.Request) {
		p := owned(w, r)
		if p == nil {
			return
		}
		if p.Authority != r.Header.Get("X-Work-Loop-Authority") || p.KeyID != r.Header.Get("X-Work-Loop-Key-ID") {
			deliveryWriteError(w, 403, "请使用原发布授权")
			return
		}
		var body struct {
			Revision int64 `json:"revision"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil || body.Revision != p.Revision {
			deliveryWriteError(w, 409, ErrRunConflict)
			return
		}
		run := s.deliveryRunLedger(p.RunID)
		if run == nil || run.Revision != p.Revision || run.Digest != p.Digest {
			deliveryWriteError(w, 409, ErrRunConflict)
			return
		}
		c := s.DeliveryControl(p.RunID)
		if c.lease != p.ID {
			deliveryWriteError(w, 409, "协调器已变更")
			return
		}
		decision, message := deliveryControlDecision(s, run)
		if run.Status == "pending_approval" {
			decision, message = "wait", "等待指定人员审批"
		}
		if p.State == "pausing" {
			decision, message = "pause", "已暂停后续步骤"
		} else if p.State != "running" {
			deliveryWriteError(w, 409, "自动发布已停止")
			return
		}
		env := s.GetEnvironment(p.Environment)
		if time.Now().Unix() >= p.Expires || env == nil || env.Protected || (env.Purpose != "test" && env.Purpose != "development") {
			decision, message = "pause", "环境策略或发布授权已变化，请重新核对"
		}
		if e := validateDeliveryReviewerDirectory(&run.Plan, r.Header.Get("X-Reviewer-Directory"), p.Owner); e != nil {
			decision, message = "pause", e.Error()
		}
		if e := s.checkDeliveryPolicy(&run.Plan); e != nil {
			decision, message = "pause", e.Error()
		}
		switch decision {
		case "wait":
			_, _ = s.db.Exec(`UPDATE delivery_controls SET message=? WHERE run_id=? AND lease=? AND state='running' AND message<>?`, message, p.RunID, p.ID, message)
		case "pause":
			s.finishDeliveryControl(p.RunID, p.ID, "paused", message)
		case "complete":
			s.finishDeliveryControl(p.RunID, p.ID, "completed", message)
		case "finish":
			s.finishDeliveryControl(p.RunID, p.ID, "stopped", message)
		default:
			// Only one caller owns this dispatch. A process crash leaves an expiring
			// lease for manual reconciliation, never a second automatic write.
			lease := "dispatch-" + p.ID
			res, e := s.db.Exec(`UPDATE delivery_controls SET lease=?,lease_until=? WHERE run_id=? AND state='running' AND lease=?`, lease, time.Now().Add(5*time.Minute).Unix(), p.RunID, p.ID)
			if e != nil {
				deliveryWriteError(w, 500, e)
				return
			}
			n, e := res.RowsAffected()
			if e != nil || n != 1 {
				deliveryWriteError(w, 409, ErrRunConflict)
				return
			}
			_, e = s.advanceDeliveryRunState(run, p.Owner, false, m, func(id int64) { go m.startNextBatch(id) }, nil)
			if e != nil {
				s.finishDeliveryControl(p.RunID, lease, "paused", e.Error())
			} else {
				_, e = s.db.Exec(`UPDATE delivery_controls SET lease=?,lease_until=? WHERE run_id=? AND lease=?`, p.ID, p.Expires, p.RunID, lease)
				if e != nil {
					deliveryWriteError(w, 500, e)
					return
				}
			}
		}
		deliveryJSON(w, s.DeliveryControl(p.RunID))
	})
}
