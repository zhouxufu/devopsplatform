package deploy

import (
	"encoding/json"
	"fmt"
	"logplatform/pkg/deploytask"
	"net/http"
	"os"
	"strings"
)

func registerDeploymentAgentRoutes(mux *http.ServeMux, m *ReleaseManager) {
	s := m.st
	mux.HandleFunc("POST /api/deployment-agents", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Role") != "admin" {
			deliveryWriteError(w, 403, "仅管理员可登记部署 Agent")
			return
		}
		var v deploymentAgentRegistration
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32768)).Decode(&v) != nil {
			deliveryWriteError(w, 400, "登记参数无效")
			return
		}
		token, e := s.registerDeploymentAgent(v)
		if e != nil {
			deliveryWriteError(w, 409, e)
			return
		}
		s.Audit(deliveryActor(r), "register", "deployment-agent", v.AgentID, map[string]any{"host": v.Host, "environment": v.Environment, "services": v.Services})
		w.Header().Set("Cache-Control", "no-store")
		deliveryJSON(w, map[string]any{"agent_id": v.AgentID, "token": token})
	})
	mux.HandleFunc("GET /api/deployment-agents", func(w http.ResponseWriter, r *http.Request) {
		service, env := r.URL.Query().Get("service"), r.URL.Query().Get("environment")
		if service == "" || env == "" {
			deliveryWriteError(w, 400, "请指定应用和环境")
			return
		}
		rows, e := s.db.Query(`SELECT agent_id FROM deployment_agents WHERE environment=? AND enabled=1 ORDER BY host_name,agent_id`, env)
		if e != nil {
			deliveryWriteError(w, 500, "Agent 授权读取失败")
			return
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				deliveryWriteError(w, 500, "Agent 授权读取失败")
				return
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			deliveryWriteError(w, 500, "Agent 授权读取失败")
			return
		}
		out := []deploymentAgentRegistration{}
		for _, id := range ids {
			a := s.deploymentAgent(id)
			if s.deploymentAgentAllowsService(a, service) {
				out = append(out, *a)
			}
		}
		deliveryJSON(w, out)
	})
	mux.HandleFunc("GET /api/deployment-agents/{id}", func(w http.ResponseWriter, r *http.Request) {
		a := s.deploymentAgent(r.PathValue("id"))
		if a == nil {
			deliveryWriteError(w, 404, "Agent 未登记")
			return
		}
		deliveryJSON(w, a)
	})

	mux.HandleFunc("DELETE /api/deployment-agents/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Role") != "admin" {
			deliveryWriteError(w, 403, "仅管理员可禁用部署 Agent")
			return
		}
		id := r.PathValue("id")
		if s.deploymentAgent(id) == nil {
			deliveryWriteError(w, 404, "Agent 未登记")
			return
		}
		if _, e := s.db.Exec(`UPDATE deployment_agents SET enabled=0 WHERE agent_id=?`, id); e != nil {
			deliveryWriteError(w, 500, "禁用失败")
			return
		}
		if a := s.deploymentAgent(id); a != nil {
			if setup := s.enrollment(a.Host); setup != nil && setup.Plan.Scope.AgentID == id {
				_, _ = s.db.Exec(`UPDATE deployment_agent_enrollments SET phase='disabled',credential='',updated_at=strftime('%s','now') WHERE host_name=?`, a.Host)
			}
		}
		s.Audit(deliveryActor(r), "disable", "deployment-agent", id, nil)
		deliveryJSON(w, map[string]any{"agent_id": id, "enabled": false})
	})
	auth := func(w http.ResponseWriter, r *http.Request) string {
		id := r.Header.Get("X-Deployment-Agent-ID")
		header := r.Header.Get("Authorization")
		token := ""
		if strings.HasPrefix(header, "Bearer ") {
			token = strings.TrimPrefix(header, "Bearer ")
		}
		if !s.authorizeDeploymentAgent(id, token) {
			deliveryWriteError(w, 401, "部署 Agent 凭据无效")
			return ""
		}
		w.Header().Set("Cache-Control", "no-store")
		return id
	}
	mux.HandleFunc("GET /api/deployment-agent/tasks", func(w http.ResponseWriter, r *http.Request) {
		id := auth(w, r)
		if id == "" {
			return
		}
		tasks, e := s.pendingAgentTasks(id)
		if e != nil {
			deliveryWriteError(w, 500, "任务读取失败")
			return
		}
		_, _ = s.db.Exec(`UPDATE deployment_agents SET last_seen=strftime('%s','now') WHERE agent_id=?`, id)
		deliveryJSON(w, tasks)
	})
	mux.HandleFunc("GET /api/deployment-agent/tasks/{id}/artifact", func(w http.ResponseWriter, r *http.Request) {
		agent := auth(w, r)
		if agent == "" {
			return
		}
		t, e := s.agentTask(r.PathValue("id"))
		if e != nil || t.AgentID != agent {
			deliveryWriteError(w, 404, "任务不存在")
			return
		}
		d := s.GetDeploy(t.DeployID)
		if d == nil || d.ArtifactDigest != t.ArtifactDigest || d.Service != t.Service || d.Environment != t.Environment {
			deliveryWriteError(w, 409, "冻结制品归属已变化")
			return
		}
		f, e := os.Open(d.ArtifactURI)
		if e != nil {
			deliveryWriteError(w, 404, "制品不可用")
			return
		}
		defer f.Close()
		info, e := f.Stat()
		if e != nil || info.Size() != t.ArtifactSize {
			deliveryWriteError(w, 409, "制品已变化")
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeContent(w, r, "artifact", info.ModTime(), f)
	})
	mux.HandleFunc("POST /api/deployment-agent/reports", func(w http.ResponseWriter, r *http.Request) {
		agent := auth(w, r)
		if agent == "" {
			return
		}
		var v deploytask.Report
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&v) != nil {
			deliveryWriteError(w, 400, "状态回执无效")
			return
		}
		changed, e := s.acceptAgentReport(agent, v)
		if e != nil {
			deliveryWriteError(w, 409, e)
			return
		}
		if changed {
			t, e := s.agentTask(v.TaskID)
			if e == nil {
				message := v.Message
				for _, secret := range t.Secrets {
					if secret != "" {
						message = strings.ReplaceAll(message, secret, "[REDACTED]")
					}
				}
				line := redactDeploymentText(fmt.Sprintf("Agent · %s · %s · %s", v.State, v.Phase, message))
				s.AppendDeployLog(t.DeployID, line)
				m.dp.hub.emit(-t.DeployID, line)
				if deploytask.Terminal(v.State) {
					d := s.GetDeploy(t.DeployID)
					if d != nil {
						m.dp.publishEvent(d.Service, d.Version, d.Host, d.Environment, d.ArtifactDigest, v.State)
					}
					m.dp.hub.Close(-t.DeployID)
				}
				m.resumeAgentRelease(t.DeployID)
			}
		}
		deliveryJSON(w, deploytask.Ack{TaskID: v.TaskID, Sequence: v.Sequence})
	})
}
