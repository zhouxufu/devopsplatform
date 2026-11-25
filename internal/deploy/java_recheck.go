package deploy

import (
	"errors"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
)

type javaRecheckEvidenceError struct {
	Message string
	Manual  bool
}

func (e *javaRecheckEvidenceError) Error() string { return e.Message }
func javaProbeError(err error) error {
	var status interface{ ExitStatus() int }
	if errors.As(err, &status) {
		switch status.ExitStatus() {
		case 10, 75:
			return &javaRecheckEvidenceError{Message: "原进程仍未通过就绪检查，保留待核对"}
		case 1, 2, 3, 30, 31:
			return &javaRecheckEvidenceError{Message: "原目录、制品、执行锁或进程回执无法匹配，需要人工核对原发布", Manual: true}
		}
	}
	return &javaRecheckEvidenceError{Message: "连接中断或检查超时，尚未获得明确证据，保留待核对"}
}

// Rechecking never runs start/stop/rollback. It verifies the original release
// identity before allowing a known process/readiness result to close a warning.
func (m *ReleaseManager) recheckManagedJava(id int64, exec javaExec) error {
	m.javaRecheckMu.Lock()
	defer m.javaRecheckMu.Unlock()
	d := m.st.GetDeploy(id)
	if d == nil || d.Status != "needs_review" {
		return fmt.Errorf("仅待核对的托管 Java 部署可以重新核对")
	}
	var agentTask int
	if e := m.st.db.QueryRow(`SELECT COUNT(*) FROM agent_deployment_tasks WHERE deploy_id=?`, id).Scan(&agentTask); e != nil {
		return e
	}
	if agentTask > 0 {
		return fmt.Errorf("Agent 部署由持久任务回执核对，不能用 SSH 覆盖结果")
	}
	var releaseID int64
	if e := m.st.db.QueryRow(`SELECT release_id FROM release_order_deploys WHERE deploy_id=?`, id).Scan(&releaseID); e != nil {
		return fmt.Errorf("缺少原始发布快照，不能猜测目标")
	}
	o := m.st.GetReleaseOrder(releaseID)
	if o == nil || (o.Status != "needs_review" && o.Status != "running") || o.Service != d.Service || o.Environment != d.Environment || o.ArtifactID != d.ArtifactID {
		return fmt.Errorf("原始发布归属不匹配")
	}
	h := m.st.GetHost(d.Host)
	if h == nil || h.Environment != d.Environment {
		return fmt.Errorf("原主机已不可核验")
	}
	source := m.st.GetService(d.Service)
	if source == nil {
		return fmt.Errorf("原应用已不可核验")
	}
	sv := o.Target.Apply(source)
	if !managedJava(sv) || sv.TargetType != "ssh-jar" {
		return fmt.Errorf("仅支持有冻结目标的托管 JAR")
	}
	root := path.Clean(sv.RemoteDir)
	release := fmt.Sprintf("%s/releases/deploy-%d", root, id)
	cmd := "base=" + shellQuote(root) + "; release=" + shellQuote(release) + `; { [ ! -e "$base/.deploy-lock" ] || test "$(cat "$base/.deploy-lock/owner")" = ` + shellQuote(fmt.Sprintf("deploy-%d", id)) + `; } && test "$(cat "$base/.devopsplatform-owner")" = ` + shellQuote(d.Service+"/"+d.Environment) + ` && test "$(readlink "$base/current")" = "$release" && test "$(sha256sum "$release/app.jar" | cut -d' ' -f1)" = ` + shellQuote(strings.TrimPrefix(d.ArtifactDigest, "sha256:")) + ` && { ` + managedJavaAbsentProcessProbe(root, release, "/proc") + ` bash "$release/service.sh" agent-probe; result=$?; if [ "$result" = 20 ]; then pid=$(cat "$base/.devopsplatform.pid" 2>/dev/null); case "$pid" in ""|*[!0-9]*) exit 31;; esac; if [ -d "/proc/$pid" ] && [ "$(awk '{print $3}' "/proc/$pid/stat" 2>/dev/null)" != Z ]; then exit 31; fi; fi; exit "$result"; }`
	out, e := exec(*h, cmd, 15*time.Second)
	status := "success"
	if e != nil {
		if !knownJavaExit(e) && !javaStartRefused(e) {
			detail := redactDeploymentText(strings.TrimSpace(out))
			if len(detail) > 1000 {
				detail = detail[:1000]
			}
			m.st.AppendDeployLog(id, "核对原发布未完成，保留待核对："+detail+"\n")
			return javaProbeError(e)
		}
		status = "failed"
	}
	// Keep the original reservation until its own remote lock is released.
	// An absent lock is safe to re-observe after a lost response; another owner is not.
	if _, e = exec(*h, managedJavaLockReleaseCommand(root, fmt.Sprintf("deploy-%d", id)), 15*time.Second); e != nil {
		return fmt.Errorf("原发布进程已核对，但执行保护释放未确认；保留待核对，请重新核对原发布")
	}
	res, e := m.st.db.Exec(`UPDATE deploys SET status=?,ended_at=? WHERE id=? AND status='needs_review'`, status, time.Now().Unix(), id)
	if e != nil {
		return e
	}
	n, e := res.RowsAffected()
	if e != nil || n != 1 {
		return ErrRunConflict
	}
	m.st.AppendDeployLog(id, redactDeploymentText("只读核对："+status+" · "+out))
	m.reconcileJavaRecheck(releaseID)
	return nil
}

// A recheck observes already dispatched work only. Remaining batches require
// the existing explicit Continue action, even when automatic batching was chosen.
func (m *ReleaseManager) reconcileJavaRecheck(id int64) {
	m.agentMu.Lock()
	defer m.agentMu.Unlock()
	o := m.st.GetReleaseOrder(id)
	if o == nil || (o.Status != "needs_review" && o.Status != "running") {
		return
	}
	m.st.RefreshReleaseCounts(id)
	ids := m.st.ReleaseDeployIDs(id)
	if len(ids) == 0 {
		return
	}
	failed := 0
	for _, id := range ids {
		d := m.st.GetDeploy(id)
		if d == nil {
			return
		}
		switch d.Status {
		case "success":
		case "failed", "rollback-failed", "rolled-back", "canceled":
			failed++
		default:
			return
		}
	}
	state := "waiting_next_batch"
	terminal := false
	if failed > o.FailureThreshold {
		state = "failed"
		terminal = true
	} else if o.NextIndex >= len(o.Hosts) {
		state = "success"
		terminal = true
	}
	m.st.compareReleaseStatus(id, o.Status, state, "", terminal)
}
func registerJavaRecheckRoute(mux *http.ServeMux, m *ReleaseManager) {
	mux.HandleFunc("POST /api/deploys/{id}/recheck", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Role") != "admin" {
			deliveryWriteError(w, 403, "仅管理员可核对部署结果")
			return
		}
		id, e := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if e != nil {
			deliveryWriteError(w, 400, "部署编号无效")
			return
		}
		if e = m.recheckManagedJava(id, SSHExec); e != nil {
			deliveryWriteError(w, 409, e)
			return
		}
		deliveryJSON(w, m.st.GetDeploy(id))
	})
}
