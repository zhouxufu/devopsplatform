package deploy

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type scriptPublicationState struct {
	ArtifactDigest      string                   `json:"artifact_digest"`
	DeployID            int64                    `json:"deploy_id"`
	ConfigDigest        string                   `json:"config_digest"`
	NeedsReconciliation bool                     `json:"needs_reconciliation"`
	Result              *ScriptPublicationResult `json:"result,omitempty"`
}

func (s *Store) scriptScopeBusy(service, environment string) error {
	var value string
	err := s.db.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, scriptScopeKey(service, environment)).Scan(&value)
	if err == nil {
		return fmt.Errorf("%w: 上次脚本发布结果尚未核对，不能开始新的发布", ErrRunConflict)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return nil
}

func (s *Store) scriptPublicationState(o *ReleaseOrder) *scriptPublicationState {
	if !o.Target.ScriptTarget() || len(o.DeployIDs) != 1 {
		return nil
	}
	id := o.DeployIDs[0]
	d := s.GetDeploy(id)
	if d == nil {
		return nil
	}
	var raw string
	locked := s.db.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, scriptScopeKey(o.Service, o.Environment)).Scan(&raw) == nil
	var claim scriptPublicationClaim
	json.Unmarshal([]byte(raw), &claim)
	result := s.scriptResult(id)
	if result == nil && locked && claim.DeployID == id {
		if progress, err := readScriptResult(filepath.Join(s.scriptWorkspace(id), "result.json")); err == nil {
			if secrets, err := s.decryptRuntimeEnvironment(o.runtimeEnvEncrypted); err == nil {
				result = sanitizeScriptResult(progress, secrets)
			}
		}
	}
	return &scriptPublicationState{ArtifactDigest: d.ArtifactDigest, DeployID: id, ConfigDigest: digestJSON(o.Target.DeployConfig), NeedsReconciliation: locked && claim.DeployID == id && (d.Status == "interrupted" || d.Status == "timed_out"), Result: result}
}

type scriptReconciliation struct {
	DeployID       int64  `json:"deploy_id"`
	ArtifactID     int64  `json:"artifact_id"`
	Digest         string `json:"digest"`
	ConfigDigest   string `json:"config_digest"`
	Result         string `json:"result"`
	Comment        string `json:"comment"`
	ConfirmStopped bool   `json:"confirm_stopped"`
}

func (m *ReleaseManager) reconcileScript(id int64, req scriptReconciliation, actor string, admin bool) error {
	o := m.st.GetReleaseOrder(id)
	if o == nil || !o.Target.ScriptTarget() || (o.Status != "failed" && o.Status != "interrupted") || len(o.DeployIDs) != 1 || req.DeployID != o.DeployIDs[0] {
		return fmt.Errorf("发布单不在脚本结果待核对状态")
	}
	if actor == "" || (!admin && actor != o.Actor) {
		return fmt.Errorf("仅发布发起人或管理员可核对结果")
	}
	if req.ArtifactID != o.ArtifactID || req.Digest == "" || req.ConfigDigest != digestJSON(o.Target.DeployConfig) {
		return fmt.Errorf("制品或脚本快照与待核对发布不一致")
	}
	d := m.st.GetDeploy(req.DeployID)
	if d == nil || d.ArtifactID != req.ArtifactID || d.ArtifactDigest != req.Digest || d.Environment != o.Environment || d.Service != o.Service || (d.Status != "interrupted" && d.Status != "timed_out") {
		return fmt.Errorf("执行记录已变化")
	}
	m.dp.mu.Lock()
	running := m.dp.contexts[d.ID] != nil
	m.dp.mu.Unlock()
	if running {
		return fmt.Errorf("脚本执行器尚未结束，请稍后核对")
	}
	if pidRaw, err := os.ReadFile(filepath.Join(m.st.scriptWorkspace(d.ID), "process.pid")); err == nil {
		pid, _ := strconv.Atoi(strings.TrimSpace(string(pidRaw)))
		if pid > 0 && scriptProcessGroupAlive(pid) {
			return fmt.Errorf("原脚本进程组仍存在，不能解除执行保护")
		}
	}
	comment := strings.TrimSpace(req.Comment)
	if !req.ConfirmStopped || comment == "" || len(comment) > 8000 || (req.Result != "success" && req.Result != "failed") {
		return fmt.Errorf("请确认原脚本已停止，并填写外部状态核对依据与结果")
	}
	var raw string
	scope := scriptScopeKey(o.Service, o.Environment)
	if m.st.db.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, scope).Scan(&raw) != nil {
		return ErrRunConflict
	}
	var claim scriptPublicationClaim
	if json.Unmarshal([]byte(raw), &claim) != nil || claim.DeployID != d.ID || claim.ArtifactID != req.ArtifactID || claim.Digest != req.Digest || claim.ConfigDigest != req.ConfigDigest {
		return fmt.Errorf("执行保护与审核快照不一致")
	}
	result := m.st.scriptResult(d.ID)
	if result == nil && o.Script != nil {
		result = o.Script.Result // Keep progress recovered from disk after a process crash.
	}
	if result == nil {
		result = &ScriptPublicationResult{}
	}
	result.Status, result.Comment, result.ReconciledBy = req.Result, comment, actor
	secrets, err := m.st.decryptRuntimeEnvironment(o.runtimeEnvEncrypted)
	if err != nil {
		return fmt.Errorf("环境变量无法读取，未保存核对结果")
	}
	result = sanitizeScriptResult(result, secrets)
	encoded, _ := json.Marshal(result)
	tx, err := m.st.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, key := range []string{scope, scriptPublicationLock(o.Service, o.Environment, o.Target.DeployConfig)} {
		r, e := tx.Exec(`DELETE FROM deploy_meta WHERE key=? AND value=?`, key, raw)
		if e != nil {
			return e
		}
		n, e := r.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return ErrRunConflict
		}
	}
	r, err := tx.Exec(`UPDATE deploys SET status=? WHERE id=? AND status=?`, req.Result, d.ID, d.Status)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrRunConflict
	}
	r, err = tx.Exec(`UPDATE release_orders SET status=?,succeeded=?,failed=?,ended_at=? WHERE id=? AND status=?`, req.Result, boolCount(req.Result == "success"), boolCount(req.Result == "failed"), time.Now().Unix(), id, o.Status)
	if err != nil {
		return err
	}
	n, err = r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrRunConflict
	}
	if _, err = tx.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, scriptResultKey(d.ID), string(encoded)); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	m.st.Audit(actor, "reconcile-script-publication", "release", strconv.FormatInt(id, 10), map[string]any{"deploy_id": d.ID, "result": req.Result, "evidence_kind": "manual-reconciliation"})
	_ = os.RemoveAll(m.st.scriptWorkspace(d.ID))
	return nil
}

func registerScriptPublicationRoutes(mux *http.ServeMux, m *ReleaseManager) {
	mux.HandleFunc("POST /api/releases/{id}/script-result", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		var req scriptReconciliation
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req) != nil {
			deliveryWriteError(w, 400, "脚本核对结果格式无效")
			return
		}
		if err := m.reconcileScript(id, req, deliveryActor(r), r.Header.Get("X-Role") == "admin"); err != nil {
			deliveryWriteError(w, 409, err)
			return
		}
		deliveryJSON(w, m.st.GetReleaseOrder(id))
	})
}
