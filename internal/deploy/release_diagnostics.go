package deploy

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

type diagnosticTarget struct {
	DeployID      int64  `json:"deploy_id"`
	Host          string `json:"host"`
	Status        string `json:"status"`
	Log           string `json:"log_tail"`
	ReportedError string `json:"reported_error,omitempty"`
	RollbackError string `json:"rollback_error,omitempty"`
	FailureCause  string `json:"failure_cause,omitempty"`
	RollbackCause string `json:"rollback_cause,omitempty"`
	ManualComment string `json:"manual_failure_comment,omitempty"`
}

func (s *Store) releaseDiagnostics(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	service, environment := query.Get("service"), query.Get("environment")
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 || service == "" || environment == "" {
		deliveryWriteError(w, 400, "需要应用、环境和发布单全局 ID")
		return
	}
	after := int64(0)
	if query.Has("after_deploy_id") {
		after, err = strconv.ParseInt(query.Get("after_deploy_id"), 10, 64)
		if err != nil || after < 0 {
			deliveryWriteError(w, 400, "执行记录游标无效")
			return
		}
	}
	order, err := scanRelease(s.db.QueryRow(releaseSelect+" WHERE id=? AND service=? AND environment=?", id, service, environment))
	if errors.Is(err, sql.ErrNoRows) {
		deliveryWriteError(w, 404, "该应用环境中没有此发布单")
		return
	}
	if err != nil {
		deliveryWriteError(w, 500, "发布单暂时无法读取")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT d.id,d.host,d.status,COALESCE(d.log,'') FROM release_order_deploys rd JOIN deploys d ON d.id=rd.deploy_id
		WHERE rd.release_id=? AND d.service=? AND (d.environment=? OR d.environment='') AND d.id>? ORDER BY d.id LIMIT 6`, id, service, environment, after)
	if err != nil {
		deliveryWriteError(w, 500, "执行记录暂时无法读取")
		return
	}
	defer rows.Close()
	targets := []diagnosticTarget{}
	var next int64
	for rows.Next() {
		if len(targets) == 5 {
			next = targets[len(targets)-1].DeployID
			break
		}
		var target diagnosticTarget
		if err := rows.Scan(&target.DeployID, &target.Host, &target.Status, &target.Log); err != nil {
			deliveryWriteError(w, 500, "执行记录暂时无法读取")
			return
		}
		targets = append(targets, target)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		deliveryWriteError(w, 500, "执行记录读取不完整")
		return
	}
	for i := range targets {
		target := &targets[i]
		target.Log = s.redactReleaseText(order.runtimeEnvEncrypted, target.Log)
		target.collectFailureDetails()
		if order.Target.ScriptTarget() {
			if receipt := s.scriptResult(target.DeployID); receipt != nil && receipt.Status == "failed" && receipt.ReconciledBy != "" {
				target.ManualComment = s.redactReleaseText(order.runtimeEnvEncrypted, receipt.Comment)
			}
		}
	}
	result := map[string]any{"release_id": id, "service": service, "environment": environment, "status": order.Status, "artifact_id": order.ArtifactID, "targets": targets, "next_after_deploy_id": next}
	var detail string
	err = s.db.QueryRowContext(r.Context(), `SELECT detail FROM audit_events WHERE resource_type='release' AND resource_id=? AND action='fail' ORDER BY id DESC LIMIT 1`, strconv.FormatInt(id, 10)).Scan(&detail)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		deliveryWriteError(w, 500, "发布失败记录暂时无法读取")
		return
	}
	var failure struct{ Error string }
	if json.Unmarshal([]byte(detail), &failure) == nil && failure.Error != "" {
		result["release_error"] = s.redactReleaseText(order.runtimeEnvEncrypted, failure.Error)
	}
	if manual := s.loadManualPublication(id); manual != nil && manual.Result == "failed" {
		result["manual_failure_comment"] = s.redactReleaseText(order.runtimeEnvEncrypted, manual.Comment)
	}
	if next == 0 && after == 0 && order.Status == "failed" && failure.Error == "" && result["manual_failure_comment"] == nil {
		haveLog := false
		for _, target := range targets {
			haveLog = haveLog || strings.TrimSpace(target.Log) != "" || target.ManualComment != ""
		}
		if !haveLog {
			result["evidence_note"] = "已确认发布失败，现存记录中没有具体错误日志"
		}
	}
	deliveryJSON(w, result)
}

// Attribute JVM causes to the failed step that emitted them. A later rollback
// can emit its own exception chain and must not replace the original failure.
func (t *diagnosticTarget) collectFailureDetails() {
	var cause string
	for _, raw := range strings.Split(t.Log, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "▶ ") {
			cause = ""
			continue
		}
		if c, ok := strings.CutPrefix(line, "Caused by: "); ok {
			cause = c
			continue
		}
		message, marked := strings.CutPrefix(line, "✗ ")
		if !marked || strings.HasPrefix(message, "部署结束") {
			continue
		}
		if t.Status == "rollback-failed" && (strings.HasPrefix(message, "恢复旧制品和旧环境变量失败") || strings.HasPrefix(message, "停止失败版本失败")) {
			t.RollbackError, t.RollbackCause = message, cause
		} else if t.ReportedError == "" {
			t.ReportedError, t.FailureCause = message, cause
		}
		cause = ""
	}
}
