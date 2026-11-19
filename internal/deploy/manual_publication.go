package deploy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const TargetManual = "manual"

func (v ServiceEnvironment) ManualTarget() bool { return v.TargetType == TargetManual }

type ManualPublication struct {
	ArtifactID   int64  `json:"artifact_id"`
	Digest       string `json:"digest"`
	Assignee     string `json:"assignee"`
	Instructions string `json:"instructions"`
	TargetDigest string `json:"target_digest"`
	Result       string `json:"result,omitempty"`
	Actor        string `json:"actor,omitempty"`
	PerformedAt  int64  `json:"performed_at,omitempty"`
	RecordedAt   int64  `json:"recorded_at,omitempty"`
	Comment      string `json:"comment,omitempty"`
}
type ManualPublicationResult struct {
	ArtifactID  int64  `json:"artifact_id"`
	Digest      string `json:"digest"`
	Result      string `json:"result"`
	PerformedAt int64  `json:"performed_at"`
	Comment     string `json:"comment"`
}

func manualPublicationKey(id int64) string { return fmt.Sprintf("manual-publication:%d", id) }
func manualTargetDigest(t ServiceEnvironment) string {
	return digestJSON([]any{t, t.runtimeEnvEncrypted})
}
func initManualPublication(tx *deployTx, o *ReleaseOrder, a *Artifact) error {
	if !o.Target.ManualTarget() {
		return nil
	}
	if a == nil || a.Digest == "" {
		return fmt.Errorf("手动发布缺少固定制品摘要")
	}
	assignee := o.Target.DeployConfig["manual_assignee"]
	if assignee == "" {
		assignee = o.Actor
	}
	o.Manual = &ManualPublication{ArtifactID: a.ID, Digest: a.Digest, Assignee: assignee, Instructions: o.Target.DeployConfig["manual_instructions"], TargetDigest: manualTargetDigest(o.Target)}
	raw, _ := json.Marshal(o.Manual)
	_, err := tx.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?)`, manualPublicationKey(o.ID), string(raw))
	return err
}

func validateManualTarget(v *ServiceEnvironment, sv *Service) error {
	if v.DeployConfig == nil {
		v.DeployConfig = map[string]string{}
	}
	if strings.TrimSpace(v.DeployConfig["manual_instructions"]) == "" || len(v.DeployConfig["manual_instructions"]) > 8000 {
		return fmt.Errorf("请填写手动发布操作说明（最多 8000 字节）")
	}
	if len(v.DeployConfig["manual_assignee"]) > 191 || strings.ContainsAny(v.DeployConfig["manual_assignee"], "\r\n\t ") {
		return fmt.Errorf("负责人账号格式无效")
	}
	kind := v.DeployConfig["manual_artifact_kind"]
	if kind == "" {
		kind = artifactKindForService(sv.ArtifactType)
		v.DeployConfig["manual_artifact_kind"] = kind
	}
	if kind != ArtifactJar && kind != ArtifactStatic && kind != ArtifactJavaBundle && kind != ArtifactOCI && kind != ArtifactDockerArchive {
		return fmt.Errorf("请选择手动发布使用的制品类型")
	}
	if !artifactMatchesService(kind, sv.ArtifactType) && !(sv.ArtifactType == "image" && kind == ArtifactJar) {
		return fmt.Errorf("手动发布制品类型与应用构建类型不匹配")
	}
	// This target has no automatic side effects and never needs invented hosts,
	// SSH commands, runtime secrets or cloud credentials to pass validation.
	v.HostGroup = ""
	v.Hosts = nil
	v.RemoteDir = ""
	v.StartCmd = ""
	v.StopCmd = ""
	v.Strategy = "rolling"
	v.MaxParallel = 1
	v.BatchSize = 1
	v.PauseBetweenBatches = false
	v.FailureThreshold = 0
	v.DeployConfig = map[string]string{"manual_artifact_kind": kind, "manual_assignee": v.DeployConfig["manual_assignee"], "manual_instructions": strings.TrimSpace(v.DeployConfig["manual_instructions"])}
	return nil
}

func (m *ReleaseManager) CompleteManual(id int64, req ManualPublicationResult, actor string, admin bool) error {
	o := m.st.GetReleaseOrder(id)
	if o == nil || !o.Target.ManualTarget() || o.Status != "waiting_manual" || o.Manual == nil {
		return fmt.Errorf("发布单不在待手动发布状态")
	}
	if actor == "" || (!admin && actor != o.Manual.Assignee) {
		return fmt.Errorf("仅指定负责人或管理员可登记手动发布结果")
	}
	if req.ArtifactID != o.ArtifactID || req.ArtifactID != o.Manual.ArtifactID || req.Digest != o.Manual.Digest {
		return fmt.Errorf("制品或摘要与审核快照不一致")
	}
	if req.Result != "success" && req.Result != "failed" {
		return fmt.Errorf("请选择发布成功或发布失败")
	}
	req.Comment = strings.TrimSpace(req.Comment)
	now := time.Now().Unix()
	if req.Comment == "" || len(req.Comment) > 8000 || req.PerformedAt < o.StartedAt || req.PerformedAt > now+60 {
		return fmt.Errorf("请填写发布说明及本次发布单创建后、不晚于当前时间的实际发布时间")
	}
	a := m.st.GetArtifact(o.ArtifactID)
	target := m.st.GetServiceEnvironment(o.Service, o.Environment)
	if a == nil || a.Digest != o.Manual.Digest || target == nil || manualTargetDigest(*target) != o.Manual.TargetDigest {
		return fmt.Errorf("制品或应用环境配置已变化，请重新生成发布单审核")
	}
	if !m.st.artifactMatchesEnvironment(a, m.st.GetService(o.Service), o.Environment) {
		return fmt.Errorf("制品类型或构建关联已变化")
	}
	if err := m.st.validateArtifactBuildVariant(a, o.Environment); err != nil {
		return err
	}
	if err := m.st.artifactBuildComplete(a); err != nil {
		return err
	}
	before, _ := json.Marshal(o.Manual)
	o.Manual.Result = req.Result
	o.Manual.Actor = actor
	o.Manual.PerformedAt = req.PerformedAt
	o.Manual.RecordedAt = now
	o.Manual.Comment = req.Comment
	after, _ := json.Marshal(o.Manual)
	tx, err := m.st.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`UPDATE release_orders SET status=?,succeeded=?,failed=?,ended_at=? WHERE id=? AND status='waiting_manual'`, req.Result, boolCount(req.Result == "success"), boolCount(req.Result == "failed"), now, id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return ErrRunConflict
	}
	result, err = tx.Exec(`UPDATE deploy_meta SET value=? WHERE key=? AND value=?`, string(after), manualPublicationKey(id), string(before))
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return ErrRunConflict
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	m.st.Audit(actor, "record-manual-publication", "release", strconv.FormatInt(id, 10), map[string]any{"artifact_id": req.ArtifactID, "digest": req.Digest, "result": req.Result, "performed_at": req.PerformedAt, "proof": "human-confirmation"})
	return nil
}
func boolCount(v bool) int {
	if v {
		return 1
	}
	return 0
}

func registerManualPublicationRoutes(mux *http.ServeMux, m *ReleaseManager) {
	mux.HandleFunc("POST /api/releases/{id}/manual-result", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		var req ManualPublicationResult
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req) != nil {
			deliveryWriteError(w, 400, "发布结果格式无效")
			return
		}
		if err := m.CompleteManual(id, req, deliveryActor(r), r.Header.Get("X-Role") == "admin"); err != nil {
			deliveryWriteError(w, 409, err)
			return
		}
		deliveryJSON(w, m.st.GetReleaseOrder(id))
	})
}
