package deploy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var fullBuildCommit = regexp.MustCompile(`^(?:[a-f0-9]{40}|[a-f0-9]{64})$`)

type buildSnapshot struct {
	Service  Service         `json:"service"`
	Delivery ServiceDelivery `json:"delivery"`
}

func (s *Store) saveBuildSnapshot(id int64, sv *Service, d *ServiceDelivery) error {
	v := buildSnapshot{Service: *sv}
	if d != nil {
		v.Delivery = *d
	}
	// Credentials and runtime values are deliberately re-authorized on retry,
	// not captured here. User-entered configuration is encrypted at rest.
	v.Delivery.WebhookToken = ""
	v.Delivery.AutoDeployEnvironment = ""
	v.Delivery.ScheduleMinutes = 0
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	sealed, e := s.encryptSecret(string(b))
	if e != nil {
		return e
	}
	_, e = s.db.Exec(`INSERT INTO build_run_snapshots(run_id,config_cipher,commit_ref,created_at) VALUES(?,?,?,?)`, id, sealed, "", time.Now().Unix())
	return e
}
func (s *Store) buildReplay(id int64) (*Service, *ServiceDelivery, string, error) {
	var sealed, commit string
	if e := s.db.QueryRow(`SELECT config_cipher,commit_ref FROM build_run_snapshots WHERE run_id=?`, id).Scan(&sealed, &commit); e != nil {
		return nil, nil, "", fmt.Errorf("该历史构建没有完整源码与配置快照，请使用当前配置重新构建")
	}
	if !fullBuildCommit.MatchString(commit) {
		return nil, nil, "", fmt.Errorf("尚未解析完整 Commit，不能保证按原源码重试")
	}
	plain, e := s.decryptSecret(sealed)
	if e != nil {
		return nil, nil, "", fmt.Errorf("原构建快照无法解密")
	}
	var v buildSnapshot
	if json.Unmarshal([]byte(plain), &v) != nil {
		return nil, nil, "", fmt.Errorf("原构建快照无效")
	}
	current := s.GetService(v.Service.Name)
	if current == nil || current.ApplicationID != v.Service.ApplicationID {
		return nil, nil, "", fmt.Errorf("原应用已删除或身份变化，不能重试")
	}
	if v.Service.GitURL == "" {
		return nil, nil, "", fmt.Errorf("本地工作目录没有不可变源码快照")
	}
	v.Service.Branch = commit
	if v.Service.PreCmd != "" {
		return nil, nil, "", fmt.Errorf("前置命令可能改变源码，请确认当前配置后构建")
	}
	return &v.Service, &v.Delivery, commit, nil
}
func registerBuildControlRoutes(mux *http.ServeMux, st *Store, pr *PipelineRunner, wj func(http.ResponseWriter, any), failure func(http.ResponseWriter, int, any), actor func(*http.Request) string) {
	find := func(w http.ResponseWriter, r *http.Request) *PipelineRun {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		v := st.GetRun(id)
		if v == nil {
			failure(w, 404, "构建不存在")
		}
		return v
	}
	allowed := func(r *http.Request, v *PipelineRun) bool {
		return actor(r) == v.Actor || r.Header.Get("X-Role") == "admin"
	}
	mux.HandleFunc("GET /api/pipeline/runs/{id}/capabilities", func(w http.ResponseWriter, r *http.Request) {
		run := find(w, r)
		if run == nil {
			return
		}
		_, d, commit, replayError := st.buildReplay(run.ID)
		pr.mu.Lock()
		cancel := pr.cancellations[run.ID] != nil
		pr.mu.Unlock()
		detail := ""
		if replayError != nil {
			detail = replayError.Error()
		}
		result := map[string]any{"can_stop": allowed(r, run) && cancel && run.Status == "running", "can_retry": allowed(r, run) && replayError == nil && run.Status != "running" && run.Status != "waiting_deploy", "commit": commit, "retry_unavailable": detail}
		if d != nil {
			result["runner"] = d.Runner
			result["build_environment"] = d.BuildEnvironment
		}
		wj(w, result)
	})
	mux.HandleFunc("POST /api/pipeline/runs/{id}/retry", func(w http.ResponseWriter, r *http.Request) {
		run := find(w, r)
		if run == nil {
			return
		}
		if !allowed(r, run) {
			failure(w, 403, "仅发起人或管理员可重试")
			return
		}
		if run.Status == "running" || run.Status == "waiting_deploy" {
			failure(w, 409, "请先处理当前运行")
			return
		}
		var body struct {
			Confirm bool   `json:"confirm"`
			Reason  string `json:"reason"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&body) != nil || !body.Confirm || strings.TrimSpace(body.Reason) == "" {
			failure(w, 400, "确认并填写重试原因")
			return
		}
		sv, d, commit, e := st.buildReplay(run.ID)
		if e != nil {
			failure(w, 409, e)
			return
		}
		next, e := pr.StartWithOptions(sv, false, PipelineRunOptions{Branch: commit, ReplayDelivery: d, Trigger: "retry"}, actor(r))
		if e != nil {
			failure(w, 409, e)
			return
		}
		st.Audit(actor(r), "retry", "pipeline", fmt.Sprint(next), map[string]any{"source_run": run.ID, "commit": commit, "reason": body.Reason, "auto_deploy": false})
		wj(w, map[string]any{"id": next})
	})
	mux.HandleFunc("POST /api/pipeline/runs/{id}/stop", func(w http.ResponseWriter, r *http.Request) {
		run := find(w, r)
		if run == nil {
			return
		}
		if !allowed(r, run) {
			failure(w, 403, "仅发起人或管理员可取消")
			return
		}
		var body struct {
			Reason string `json:"reason"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&body) != nil || strings.TrimSpace(body.Reason) == "" {
			failure(w, 400, "请填写取消原因")
			return
		}
		if e := pr.cancelBuild(run.ID); e != nil {
			failure(w, 409, e)
			return
		}
		st.Audit(actor(r), "stop", "pipeline", fmt.Sprint(run.ID), map[string]any{"reason": body.Reason})
		w.WriteHeader(http.StatusAccepted)
		wj(w, map[string]any{"requested": true})
	})
}
func (s *Store) buildCommit(id int64) string {
	var v string
	s.db.QueryRow(`SELECT commit_ref FROM build_run_snapshots WHERE run_id=?`, id).Scan(&v)
	return v
}
