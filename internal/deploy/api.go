package deploy

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/gorilla/websocket"
	"logplatform/internal/appcatalog"
)

//go:embed assets
var uiFS embed.FS

var upgrader = websocket.Upgrader{CheckOrigin: sameOrigin}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}

// Routes 管理面
func Routes(st *Store, b *Builder, hub *logHub, busURL string, stepLib *StepLib) *http.ServeMux {
	mux := http.NewServeMux()
	registerGitBranchRoutes(mux, st)
	applicationObserver := appcatalog.NewObserver(busURL)
	wj := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		wj(w, map[string]any{"ok": true, "service": "deploy", "metadata_database": st.Driver()})
	})
	writeError := func(w http.ResponseWriter, status int, err any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprint(err)})
	}
	actorOf := func(r *http.Request) string {
		if actor := strings.TrimSpace(r.Header.Get("X-User")); actor != "" {
			return actor
		}
		return "local"
	}
	errorStatus := func(err error, fallback int) int {
		if errors.Is(err, ErrRunConflict) {
			return http.StatusConflict
		}
		return fallback
	}

	syncApplications := func() []Service {
		services := st.Services()
		for i := range services {
			resolved, err := applicationObserver.Resolve(services[i].Name, "deploy", services[i].ApplicationID)
			if err == nil && resolved != "" && resolved != services[i].ApplicationID {
				if st.RebindApplicationID(services[i].Name, resolved) == nil {
					services[i].ApplicationID = resolved
				}
			}
		}
		return services
	}
	mux.HandleFunc("GET /api/services", func(w http.ResponseWriter, r *http.Request) { wj(w, syncApplications()) })
	mux.HandleFunc("POST /api/services", func(w http.ResponseWriter, r *http.Request) {
		var sv Service
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&sv) != nil || sv.Name == "" {
			http.Error(w, `{"error":"bad request"}`, 400)
			return
		}
		if sv.ArtifactType == "" {
			sv.ArtifactType = "jar"
		}
		if sv.ArtifactType != "jar" && sv.ArtifactType != "static" && sv.ArtifactType != "image" {
			writeError(w, http.StatusBadRequest, "artifact_type 支持 jar | static | image")
			return
		}
		if sv.ArtifactType == "image" && strings.TrimSpace(sv.ImageName) == "" {
			writeError(w, http.StatusBadRequest, "Docker 构建必须配置镜像名称（含 registry/repository）")
			return
		}
		if sv.BuildMode == "" {
			sv.BuildMode = "local"
		}
		if err := prepareBuildConfig(&sv); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		pipelineSteps := sv.Steps
		if len(pipelineSteps) == 0 {
			pipelineSteps = ExpandTemplate(sv)
		}
		if err := stepLib.ValidateBuildPipeline(pipelineSteps); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if resolved, err := applicationObserver.Resolve(sv.Name, "deploy", sv.ApplicationID); err == nil && resolved != "" {
			sv.ApplicationID = resolved
		}
		if current := st.GetService(sv.Name); current != nil && sv.ApplicationID != "" && current.ApplicationID != sv.ApplicationID {
			if err := st.RebindApplicationID(sv.Name, sv.ApplicationID); err != nil {
				writeError(w, http.StatusConflict, err)
				return
			}
		}
		if err := st.UpsertService(sv); err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
			return
		}
		st.Audit(actorOf(r), "upsert", "application", sv.Name, map[string]any{"artifact_type": sv.ArtifactType})
		wj(w, st.GetService(sv.Name))
	})
	mux.HandleFunc("DELETE /api/services/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		existing := st.GetService(name)
		if err := st.DeleteService(name); err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
			return
		}
		catalogDetached := true
		if existing != nil {
			catalogDetached = applicationObserver.Detach(existing.Name, "deploy", existing.ApplicationID) == nil
		}
		st.Audit(actorOf(r), "delete", "application", name, map[string]any{"catalog_detached": catalogDetached})
		wj(w, map[string]bool{"ok": true, "catalog_detached": catalogDetached})
	})

	// ---------- 环境与服务部署目标 ----------
	mux.HandleFunc("GET /api/setup", func(w http.ResponseWriter, r *http.Request) { wj(w, st.EnvironmentSetup()) })
	mux.HandleFunc("POST /api/setup/environments", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Environments []Environment `json:"environments"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req) != nil {
			writeError(w, http.StatusBadRequest, "初始化参数格式错误")
			return
		}
		if err := st.InitializeEnvironments(req.Environments); err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		st.Audit(actorOf(r), "initialize", "environments", "first-use", map[string]any{"count": len(req.Environments)})
		wj(w, st.EnvironmentSetup())
	})
	mux.HandleFunc("GET /api/environments", func(w http.ResponseWriter, r *http.Request) { wj(w, st.Environments()) })
	mux.HandleFunc("POST /api/environments", func(w http.ResponseWriter, r *http.Request) {
		if !st.EnvironmentSetup().Completed {
			writeError(w, http.StatusConflict, "请先在控制台完成首次环境初始化")
			return
		}
		var env Environment
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&env) != nil {
			writeError(w, http.StatusBadRequest, "bad request")
			return
		}
		env.Name, env.DisplayName = strings.TrimSpace(env.Name), strings.TrimSpace(env.DisplayName)
		if err := st.UpsertEnvironment(env); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		st.Audit(actorOf(r), "upsert", "environment", env.Name, map[string]any{"protected": env.Protected})
		wj(w, st.GetEnvironment(env.Name))
	})
	mux.HandleFunc("DELETE /api/environments/{name}", func(w http.ResponseWriter, r *http.Request) {
		if err := st.DeleteEnvironment(r.PathValue("name")); err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		st.Audit(actorOf(r), "delete", "environment", r.PathValue("name"), nil)
		wj(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/services/{name}/environments", func(w http.ResponseWriter, r *http.Request) {
		wj(w, st.ServiceEnvironments(r.PathValue("name")))
	})
	mux.HandleFunc("PUT /api/services/{name}/environments/{environment}", func(w http.ResponseWriter, r *http.Request) {
		var target ServiceEnvironment
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&target) != nil {
			writeError(w, http.StatusBadRequest, "bad request")
			return
		}
		target.Service, target.Environment = r.PathValue("name"), r.PathValue("environment")
		if err := st.UpsertServiceEnvironment(target); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		st.Audit(actorOf(r), "configure", "application-environment", target.Service+"/"+target.Environment, map[string]any{"target_type": target.TargetType, "targets": target.ExecutionTargets(), "host_group": target.HostGroup})
		wj(w, st.GetServiceEnvironment(target.Service, target.Environment))
	})
	mux.HandleFunc("DELETE /api/services/{name}/environments/{environment}", func(w http.ResponseWriter, r *http.Request) {
		if err := st.DeleteServiceEnvironment(r.PathValue("name"), r.PathValue("environment")); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		wj(w, map[string]bool{"ok": true})
	})

	mux.HandleFunc("GET /api/builds", func(w http.ResponseWriter, r *http.Request) {
		limit := 50
		if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 500 {
			limit = n
		}
		wj(w, st.Builds(r.URL.Query().Get("service"), limit))
	})
	mux.HandleFunc("GET /api/artifacts", func(w http.ResponseWriter, r *http.Request) {
		limit := 50
		if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 500 {
			limit = n
		}
		wj(w, st.Artifacts(r.URL.Query().Get("service"), limit))
	})

	// 触发构建
	mux.HandleFunc("POST /api/builds", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Service string `json:"service"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil || req.Service == "" {
			http.Error(w, `{"error":"bad request"}`, 400)
			return
		}
		sv := st.GetService(req.Service)
		if sv == nil {
			http.Error(w, `{"error":"service not found"}`, 404)
			return
		}
		id, err := b.Start(sv, actorOf(r))
		if err != nil {
			writeError(w, errorStatus(err, http.StatusInternalServerError), err)
			return
		}
		wj(w, map[string]any{"id": id})
	})

	// 构建日志（历史）
	mux.HandleFunc("GET /api/builds/{id}/log", func(w http.ResponseWriter, r *http.Request) {
		var id int64
		fmt.Sscanf(r.PathValue("id"), "%d", &id)
		wj(w, map[string]string{"log": st.BuildLog(id)})
	})

	// 构建日志实时流（WS）
	mux.HandleFunc("GET /api/builds/{id}/tail", func(w http.ResponseWriter, r *http.Request) {
		var id int64
		fmt.Sscanf(r.PathValue("id"), "%d", &id)
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		ch, unsub := b.Subscribe(id)
		defer unsub()
		// 先补历史尾部再实时
		c.WriteMessage(websocket.TextMessage, []byte(st.BuildLog(id)))
		for {
			select {
			case <-r.Context().Done():
				return
			case line, ok := <-ch:
				if !ok {
					c.WriteControl(websocket.CloseMessage,
						websocket.FormatCloseMessage(1000, "build finished"), time.Now().Add(2*time.Second))
					return
				}
				c.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if c.WriteMessage(websocket.TextMessage, []byte(line)) != nil {
					return
				}
			}
		}
	})

	// ---------- 主机 CRUD ----------
	mux.HandleFunc("GET /api/hosts", func(w http.ResponseWriter, r *http.Request) { wj(w, st.PublicHosts()) })
	mux.HandleFunc("POST /api/hosts/scan-key", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Addr string `json:"addr"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "主机地址格式错误")
			return
		}
		if _, err := normalizeSSHAddress(req.Addr); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		scan, err := ScanSSHHostKey(req.Addr, 10*time.Second)
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		wj(w, scan)
	})
	mux.HandleFunc("POST /api/hosts", func(w http.ResponseWriter, r *http.Request) {
		var h Host
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&h) != nil || h.Name == "" || h.Addr == "" {
			http.Error(w, `{"error":"bad request"}`, 400)
			return
		}
		if h.User == "" {
			h.User = "root"
		}
		if err := st.UpsertHost(h); err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
			return
		}
		st.Audit(actorOf(r), "upsert", "host", h.Name, map[string]any{"addr": h.Addr})
		if saved := st.GetHost(h.Name); saved != nil {
			saved.Password = ""
			wj(w, saved)
			return
		}
		writeError(w, http.StatusInternalServerError, "host save verification failed")
	})
	mux.HandleFunc("DELETE /api/hosts/{name}", func(w http.ResponseWriter, r *http.Request) {
		if err := st.DeleteHost(r.PathValue("name")); err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		wj(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/hosts/{name}/test", func(w http.ResponseWriter, r *http.Request) {
		h := st.GetHost(r.PathValue("name"))
		if h == nil {
			http.Error(w, `{"error":"not found"}`, 404)
			return
		}
		out, err := SSHExec(*h, "echo ok", 10*time.Second)
		if err != nil || strings.TrimSpace(out) != "ok" {
			msg := strings.TrimSpace(out)
			if err != nil {
				msg = strings.TrimSpace(err.Error() + " " + out)
			}
			wj(w, map[string]any{"ok": false, "error": msg})
			return
		}
		wj(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/hosts/{name}/agent/status", func(w http.ResponseWriter, r *http.Request) {
		h := st.GetHost(r.PathValue("name"))
		if h == nil {
			writeError(w, http.StatusNotFound, "主机不存在")
			return
		}
		status, err := AgentStatus(*h)
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		wj(w, status)
	})
	mux.HandleFunc("GET /api/hosts/{name}/agent/config", func(w http.ResponseWriter, r *http.Request) {
		h := st.GetHost(r.PathValue("name"))
		if h == nil {
			writeError(w, http.StatusNotFound, "主机不存在")
			return
		}
		if cfg := st.AgentDesiredConfig(h.Name); cfg != nil {
			wj(w, cfg)
			return
		}
		wj(w, AgentDesiredConfig{HostName: h.Name, RunUser: "logagent", RunGroup: "logagent", SpoolMaxMB: 1024, Collect: []AgentCollectRequest{}, TokenAvailable: agentDataToken() != ""})
	})
	mux.HandleFunc("PUT /api/hosts/{name}/agent/config", func(w http.ResponseWriter, r *http.Request) {
		var cfg AgentDesiredConfig
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&cfg); err != nil {
			writeError(w, http.StatusBadRequest, "采集配置格式错误")
			return
		}
		for i := range cfg.Collect {
			item := &cfg.Collect[i]
			if resolved, err := applicationObserver.Resolve(item.Service, "logs", item.ApplicationID); err == nil && resolved != "" {
				item.ApplicationID = resolved
			}
		}
		saved, err := st.UpsertAgentDesiredConfig(r.PathValue("name"), cfg)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		st.Audit(actorOf(r), "save-config", "log-agent", r.PathValue("name"), map[string]any{"collect_count": len(saved.Collect)})
		wj(w, saved)
	})
	mux.HandleFunc("POST /api/hosts/{name}/agent/config/apply", func(w http.ResponseWriter, r *http.Request) {
		h := st.GetHost(r.PathValue("name"))
		if h == nil {
			writeError(w, http.StatusNotFound, "主机不存在")
			return
		}
		cfg := st.AgentDesiredConfig(h.Name)
		if cfg == nil {
			writeError(w, http.StatusConflict, "请先保存 Agent 采集配置")
			return
		}
		if !cfg.TokenAvailable {
			writeError(w, http.StatusServiceUnavailable, "平台尚未配置 DEVOPSPLATFORM_DATA_TOKEN，无法下发")
			return
		}
		err := ApplyAgentConfig(*h, *cfg, agentDataToken())
		st.MarkAgentConfigApplied(h.Name, err)
		if err != nil {
			st.Audit(actorOf(r), "apply-config-failed", "log-agent", h.Name, map[string]any{"environment": h.Environment, "error": err.Error()})
			writeError(w, http.StatusBadGateway, err)
			return
		}
		st.Audit(actorOf(r), "apply-config", "log-agent", h.Name, map[string]any{"environment": h.Environment, "collect_count": len(cfg.Collect)})
		wj(w, st.AgentDesiredConfig(h.Name))
	})
	mux.HandleFunc("POST /api/hosts/{name}/agent/install", func(w http.ResponseWriter, r *http.Request) {
		h := st.GetHost(r.PathValue("name"))
		if h == nil {
			writeError(w, http.StatusNotFound, "主机不存在")
			return
		}
		cfg := st.AgentDesiredConfig(h.Name)
		if cfg == nil {
			writeError(w, http.StatusConflict, "请先保存 Agent 采集配置")
			return
		}
		if !cfg.TokenAvailable {
			writeError(w, http.StatusServiceUnavailable, "平台尚未配置 DEVOPSPLATFORM_DATA_TOKEN，无法安装")
			return
		}
		result, err := InstallAgent(*h, *cfg, agentDataToken())
		if err != nil {
			st.MarkAgentConfigApplied(h.Name, err)
			st.Audit(actorOf(r), "install-failed", "log-agent", h.Name, map[string]any{"environment": h.Environment, "error": err.Error()})
			writeError(w, http.StatusBadGateway, err)
			return
		}
		st.MarkAgentConfigApplied(h.Name, nil)
		st.Audit(actorOf(r), "install", "log-agent", h.Name, map[string]any{"environment": h.Environment, "agent_id": result.AgentID, "platform": result.Platform, "version": result.Version, "sha256": result.SHA256})
		wj(w, result)
	})

	// ---------- 资源中心：连接 / 主机组 / 变量组 / Runner ----------
	mux.HandleFunc("GET /api/connections", func(w http.ResponseWriter, r *http.Request) { wj(w, st.Connections()) })
	mux.HandleFunc("POST /api/connections/scan-key", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Endpoint string `json:"endpoint"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "SSH 服务地址格式错误")
			return
		}
		hp, err := normalizeGitSSHEndpoint(req.Endpoint)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		scan, err := ScanSSHHostKey(hp, 10*time.Second)
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		wj(w, scan)
	})
	mux.HandleFunc("POST /api/connections", func(w http.ResponseWriter, r *http.Request) {
		var v ResourceConnection
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&v) != nil {
			writeError(w, 400, "bad request")
			return
		}
		if err := st.UpsertConnection(v); err != nil {
			writeError(w, 400, err)
			return
		}
		st.Audit(actorOf(r), "upsert", "connection", v.Name, map[string]any{"kind": v.Kind, "endpoint": v.Endpoint})
		for _, item := range st.Connections() {
			if item.Name == v.Name {
				wj(w, item)
				return
			}
		}
	})
	mux.HandleFunc("DELETE /api/connections/{name}", func(w http.ResponseWriter, r *http.Request) {
		if err := st.DeleteConnection(r.PathValue("name")); err != nil {
			writeError(w, 409, err)
			return
		}
		st.Audit(actorOf(r), "delete", "connection", r.PathValue("name"), nil)
		wj(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/connections/{name}/test", func(w http.ResponseWriter, r *http.Request) {
		c := st.GetConnection(r.PathValue("name"))
		if c == nil {
			writeError(w, 404, "连接不存在")
			return
		}
		if c.Kind == "mysql" || c.Kind == "zentao" || c.Kind == "nacos" || c.Kind == "redis" {
			ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
			defer cancel()
			err := testIntegrationConnection(ctx, c)
			if err != nil {
				wj(w, map[string]any{"ok": false, "error": err.Error()})
				return
			}
			status := "认证及读取权限验证成功"
			if c.Kind == "redis" {
				status = "Redis 认证、DB 选择及 PING 成功；未验证维护命令权限，不执行写操作"
			}
			if c.Kind == "nacos" && c.Config["test_data_id"] == "" {
				status = "Nacos 登录成功；未指定测试 Data ID，尚未验证配置读取和发布权限"
			}
			if c.Kind == "zentao" && strings.TrimSpace(c.Config["product_ids"]) == "" {
				status = "禅道登录认证成功；未配置 Bug 产品 ID，用户列表权限请在成员管理中验证"
			}
			wj(w, map[string]any{"ok": true, "status": status})
			return
		}
		if c.Kind == TargetAliyunSAE {
			provider, err := newAliyunSAEProvider(c)
			if err == nil {
				ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
				defer cancel()
				err = provider.Test(ctx)
			}
			if err != nil {
				wj(w, map[string]any{"ok": false, "error": err.Error()})
				return
			}
			wj(w, map[string]any{"ok": true, "status": "SAE credentials verified"})
			return
		}
		if c.Kind == "git" {
			mode, err := normalizeGitAuthMode(c.Config["auth_mode"])
			if err != nil {
				wj(w, map[string]any{"ok": false, "error": err.Error()})
				return
			}
			if mode == gitAuthSSH {
				ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
				defer cancel()
				if err := testGitSSHConnection(ctx, c); err != nil {
					wj(w, map[string]any{"ok": false, "error": err.Error()})
					return
				}
				wj(w, map[string]any{"ok": true, "status": "Git SSH host key and private key verified"})
				return
			}
		}
		endpoint := strings.TrimSuffix(c.Endpoint, "/")
		if c.Kind == "docker" {
			endpoint += "/v2/"
		}
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, endpoint, nil)
		if err != nil {
			writeError(w, 400, err)
			return
		}
		if c.Username != "" {
			req.SetBasicAuth(c.Username, c.Password)
		}
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			wj(w, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		resp.Body.Close()
		ok := resp.StatusCode < 500
		wj(w, map[string]any{"ok": ok, "status": resp.Status})
	})
	mux.HandleFunc("GET /api/host-groups", func(w http.ResponseWriter, r *http.Request) { wj(w, st.HostGroups()) })
	mux.HandleFunc("POST /api/host-groups", func(w http.ResponseWriter, r *http.Request) {
		var v HostGroup
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&v) != nil {
			writeError(w, 400, "bad request")
			return
		}
		if err := st.UpsertHostGroup(v); err != nil {
			writeError(w, 400, err)
			return
		}
		st.Audit(actorOf(r), "upsert", "host-group", v.Name, map[string]any{"hosts": v.Hosts})
		wj(w, v)
	})
	mux.HandleFunc("DELETE /api/host-groups/{name}", func(w http.ResponseWriter, r *http.Request) {
		if err := st.DeleteHostGroup(r.PathValue("name")); err != nil {
			writeError(w, 409, err)
			return
		}
		st.Audit(actorOf(r), "delete", "host-group", r.PathValue("name"), nil)
		wj(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/variable-groups", func(w http.ResponseWriter, r *http.Request) { wj(w, st.VariableGroups()) })
	mux.HandleFunc("POST /api/variable-groups", func(w http.ResponseWriter, r *http.Request) {
		var v VariableGroup
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&v) != nil {
			writeError(w, 400, "bad request")
			return
		}
		if err := st.UpsertVariableGroup(v); err != nil {
			writeError(w, 400, err)
			return
		}
		st.Audit(actorOf(r), "upsert", "variable-group", v.Name, map[string]any{"keys": v.Keys})
		wj(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("DELETE /api/variable-groups/{name}", func(w http.ResponseWriter, r *http.Request) {
		if err := st.DeleteVariableGroup(r.PathValue("name")); err != nil {
			writeError(w, 409, err)
			return
		}
		st.Audit(actorOf(r), "delete", "variable-group", r.PathValue("name"), nil)
		wj(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/runners", func(w http.ResponseWriter, r *http.Request) { wj(w, st.Runners()) })
	mux.HandleFunc("POST /api/runners", func(w http.ResponseWriter, r *http.Request) {
		var v BuildRunner
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&v) != nil {
			writeError(w, 400, "bad request")
			return
		}
		if err := st.UpsertRunner(v); err != nil {
			writeError(w, 400, err)
			return
		}
		st.Audit(actorOf(r), "upsert", "runner", v.Name, nil)
		wj(w, v)
	})
	mux.HandleFunc("GET /api/services/{name}/delivery", func(w http.ResponseWriter, r *http.Request) { wj(w, st.GetServiceDelivery(r.PathValue("name"))) })
	mux.HandleFunc("PUT /api/services/{name}/delivery", func(w http.ResponseWriter, r *http.Request) {
		var v ServiceDelivery
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&v) != nil {
			writeError(w, 400, "bad request")
			return
		}
		v.Service = r.PathValue("name")
		if err := st.UpsertServiceDelivery(v); err != nil {
			writeError(w, 400, err)
			return
		}
		st.Audit(actorOf(r), "configure", "delivery", v.Service, nil)
		wj(w, st.GetServiceDelivery(v.Service))
	})
	mux.HandleFunc("GET /api/audit", func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		wj(w, st.AuditEvents(limit))
	})

	// ---------- 部署 ----------
	deployer := NewDeployer(st, hub, busURL)
	releaseManager := NewReleaseManager(st, deployer)
	releaseManager.Recover()
	registerDeliveryRoutes(mux, st, releaseManager)
	registerIntegrationRoutes(mux, st)
	managedJavaTarget := func(service, environment string) (*ServiceEnvironment, *Service, []Host, error) {
		target := st.GetServiceEnvironment(service, environment)
		base := st.GetService(service)
		if target == nil || base == nil {
			return nil, nil, nil, fmt.Errorf("应用环境不存在")
		}
		sv := target.Apply(base)
		if !managedJava(sv) {
			return nil, nil, nil, fmt.Errorf("实例启停仅支持标准 Java 部署模式")
		}
		hosts := make([]Host, 0, len(target.Hosts))
		for _, name := range target.Hosts {
			host := st.GetHost(name)
			if host == nil {
				return nil, nil, nil, fmt.Errorf("环境主机不存在: %s", name)
			}
			hosts = append(hosts, *host)
		}
		return target, sv, hosts, nil
	}
	mux.HandleFunc("GET /api/services/{name}/environments/{environment}/instances", func(w http.ResponseWriter, r *http.Request) {
		_, sv, hosts, err := managedJavaTarget(r.PathValue("name"), r.PathValue("environment"))
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		wj(w, deployer.ManagedJavaInstances(sv, hosts))
	})
	mux.HandleFunc("POST /api/services/{name}/environments/{environment}/instances/{host}/{action}", func(w http.ResponseWriter, r *http.Request) {
		service, environment, hostName, action := r.PathValue("name"), r.PathValue("environment"), r.PathValue("host"), r.PathValue("action")
		target, sv, hosts, err := managedJavaTarget(service, environment)
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		if action != "start" && action != "stop" && action != "restart" && action != "ready" {
			writeError(w, http.StatusBadRequest, "实例操作仅支持 start、stop、restart 或 ready")
			return
		}
		var host *Host
		for i := range hosts {
			if hosts[i].Name == hostName {
				host = &hosts[i]
				break
			}
		}
		if host == nil {
			writeError(w, http.StatusBadRequest, "主机不属于该应用环境")
			return
		}
		if active := st.ActiveDeploy(service, hostName); active != nil {
			writeError(w, http.StatusConflict, fmt.Sprintf("该主机正在执行部署 #%d", active.ID))
			return
		}
		if active := st.ActiveReleaseForServiceEnvironment(service, environment); active != nil {
			writeError(w, http.StatusConflict, fmt.Sprintf("该应用环境正在处理发布单 #%d", active.ID))
			return
		}
		var req struct {
			Reason           string `json:"reason"`
			ConfirmProtected bool   `json:"confirm_protected"`
		}
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req)
		req.Reason = strings.TrimSpace(req.Reason)
		if action != "ready" {
			if target.Protected && !req.ConfirmProtected {
				writeError(w, http.StatusPreconditionRequired, "受保护环境实例操作需要二次确认")
				return
			}
			if (target.Protected || target.RequireReason) && req.Reason == "" {
				writeError(w, http.StatusBadRequest, "该环境实例操作必须填写原因")
				return
			}
		}
		result, controlErr := deployer.ControlManagedJavaInstance(sv, *host, action)
		detail := map[string]any{"action": action, "reason": req.Reason, "state": result.State}
		if controlErr != nil {
			detail["error"] = controlErr.Error()
			st.Audit(actorOf(r), "instance-"+action+"-failed", "application-instance", service+"/"+environment+"/"+hostName, detail)
			writeError(w, http.StatusBadGateway, controlErr)
			return
		}
		st.Audit(actorOf(r), "instance-"+action, "application-instance", service+"/"+environment+"/"+hostName, detail)
		wj(w, result)
	})

	// ---------- 需求、变更、版本与环境推进 ----------
	changeID := func(r *http.Request) int64 {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		return id
	}
	canManageOwned := func(r *http.Request, owner string) bool {
		actor := actorOf(r)
		return actor == "local" || actor == owner || r.Header.Get("X-Role") == "admin"
	}
	mux.HandleFunc("GET /api/work-items", func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		wj(w, st.WorkItems(r.URL.Query().Get("status"), r.URL.Query().Get("q"), limit))
	})
	mux.HandleFunc("GET /api/work-items/{id}/delivery-content", func(w http.ResponseWriter, r *http.Request) {
		out, err := st.WorkDelivery(changeID(r))
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		wj(w, out)
	})
	mux.HandleFunc("PUT /api/work-items/{id}/delivery-content", func(w http.ResponseWriter, r *http.Request) {
		var req WorkDeliverySave
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&req) != nil {
			writeError(w, http.StatusBadRequest, "交付内容格式错误")
			return
		}
		admin := canManageOwned(r, "")
		if err := st.SaveWorkDelivery(changeID(r), &req, actorOf(r), admin); err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		st.Audit(actorOf(r), "save", "work-delivery", fmt.Sprint(changeID(r)), map[string]any{"items": len(req.Changes)})
		out, err := st.WorkDelivery(changeID(r))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		wj(w, out)
	})
	mux.HandleFunc("POST /api/work-items", func(w http.ResponseWriter, r *http.Request) {
		var item WorkItem
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&item) != nil {
			writeError(w, http.StatusBadRequest, "需求参数格式错误")
			return
		}
		item.Owner = actorOf(r)
		if err := st.CreateWorkItem(&item); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		st.Audit(actorOf(r), "create", "work-item", fmt.Sprint(item.ID), map[string]any{"source": item.Source, "external_id": item.ExternalID})
		wj(w, item)
	})
	mux.HandleFunc("PUT /api/work-items/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := changeID(r)
		current := st.GetWorkItem(id)
		if current == nil || !canManageOwned(r, current.Owner) {
			writeError(w, http.StatusForbidden, "仅创建人或管理员可编辑该需求")
			return
		}
		var item WorkItem
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&item) != nil {
			writeError(w, http.StatusBadRequest, "需求参数格式错误")
			return
		}
		item.ID, item.Owner = id, current.Owner
		if err := st.UpdateWorkItem(&item); err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		wj(w, st.GetWorkItem(id))
	})
	mux.HandleFunc("DELETE /api/work-items/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := changeID(r)
		current := st.GetWorkItem(id)
		if current == nil || !canManageOwned(r, current.Owner) {
			writeError(w, http.StatusForbidden, "仅创建人或管理员可删除该需求")
			return
		}
		if err := st.DeleteWorkItem(id); err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		wj(w, map[string]bool{"ok": true})
	})

	mux.HandleFunc("GET /api/changes", func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		wj(w, st.Changes(r.URL.Query().Get("status"), r.URL.Query().Get("service"), r.URL.Query().Get("q"), limit))
	})
	mux.HandleFunc("POST /api/changes", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Change
			WorkItemIDs []int64 `json:"work_item_ids"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&req) != nil {
			writeError(w, http.StatusBadRequest, "变更参数格式错误")
			return
		}
		req.Owner = actorOf(r)
		if err := st.CreateChange(&req.Change); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := st.SetChangeWorkItems(req.ID, req.WorkItemIDs); err != nil {
			_ = st.DeleteChange(req.ID)
			writeError(w, http.StatusBadRequest, err)
			return
		}
		st.Audit(actorOf(r), "create", "change", fmt.Sprint(req.ID), map[string]any{"kind": req.Kind, "service": req.Service})
		wj(w, st.GetChange(req.ID))
	})
	mux.HandleFunc("PUT /api/changes/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := changeID(r)
		current := st.GetChange(id)
		if current == nil || !canManageOwned(r, current.Owner) {
			writeError(w, http.StatusForbidden, "仅创建人或管理员可编辑该变更")
			return
		}
		var req struct {
			Change
			WorkItemIDs []int64 `json:"work_item_ids"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&req) != nil {
			writeError(w, http.StatusBadRequest, "变更参数格式错误")
			return
		}
		req.ID, req.Owner = id, current.Owner
		if err := st.UpdateChange(&req.Change); err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		if err := st.SetChangeWorkItems(id, req.WorkItemIDs); err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		wj(w, st.GetChange(id))
	})
	mux.HandleFunc("DELETE /api/changes/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := changeID(r)
		current := st.GetChange(id)
		if current == nil || !canManageOwned(r, current.Owner) {
			writeError(w, http.StatusForbidden, "仅创建人或管理员可删除该变更")
			return
		}
		if err := st.DeleteChange(id); err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		wj(w, map[string]bool{"ok": true})
	})
	for action, ready := range map[string]bool{"ready": true, "reopen": false} {
		action, ready := action, ready
		mux.HandleFunc("POST /api/changes/{id}/"+action, func(w http.ResponseWriter, r *http.Request) {
			id := changeID(r)
			current := st.GetChange(id)
			if current == nil || !canManageOwned(r, current.Owner) {
				writeError(w, http.StatusForbidden, "仅创建人或管理员可更改变更状态")
				return
			}
			v, err := st.SetChangeReady(id, ready)
			if err != nil {
				writeError(w, http.StatusConflict, err)
				return
			}
			wj(w, v)
		})
	}

	mux.HandleFunc("GET /api/release-versions", func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		wj(w, st.ReleaseVersions(r.URL.Query().Get("status"), limit))
	})
	mux.HandleFunc("POST /api/release-versions", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ReleaseVersion
			ChangeIDs   []int64  `json:"change_ids"`
			WorkItemIDs []int64  `json:"work_item_ids"`
			ManualIDs   *[]int64 `json:"manual_change_ids"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&req) != nil {
			writeError(w, http.StatusBadRequest, "发布版本参数格式错误")
			return
		}
		req.ID, req.Owner = 0, actorOf(r)
		manual := req.ChangeIDs
		if req.ManualIDs != nil {
			manual = *req.ManualIDs
		}
		if err := st.SaveReleaseDraft(&req.ReleaseVersion, VersionScope{WorkItemIDs: req.WorkItemIDs, ManualChangeIDs: manual}); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		st.Audit(actorOf(r), "create", "release-version", fmt.Sprint(req.ID), map[string]any{"version": req.Version})
		wj(w, st.GetReleaseVersion(req.ID))
	})
	mux.HandleFunc("PUT /api/release-versions/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := changeID(r)
		current := st.GetReleaseVersion(id)
		if current == nil || !canManageOwned(r, current.Owner) {
			writeError(w, http.StatusForbidden, "仅创建人或管理员可编辑该发布版本")
			return
		}
		var req struct {
			ReleaseVersion
			ChangeIDs   []int64  `json:"change_ids"`
			WorkItemIDs []int64  `json:"work_item_ids"`
			ManualIDs   *[]int64 `json:"manual_change_ids"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&req) != nil {
			writeError(w, http.StatusBadRequest, "发布版本参数格式错误")
			return
		}
		req.ID, req.Owner = id, current.Owner
		manual := req.ChangeIDs
		if req.ManualIDs != nil {
			manual = *req.ManualIDs
		}
		if err := st.SaveReleaseDraft(&req.ReleaseVersion, VersionScope{WorkItemIDs: req.WorkItemIDs, ManualChangeIDs: manual}); err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		wj(w, st.GetReleaseVersion(id))
	})
	mux.HandleFunc("DELETE /api/release-versions/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := changeID(r)
		current := st.GetReleaseVersion(id)
		if current == nil || !canManageOwned(r, current.Owner) {
			writeError(w, http.StatusForbidden, "仅创建人或管理员可删除该发布版本")
			return
		}
		if err := st.DeleteReleaseVersion(id); err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		wj(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/release-versions/{id}/ready", func(w http.ResponseWriter, r *http.Request) {
		id := changeID(r)
		current := st.GetReleaseVersion(id)
		if current == nil || !canManageOwned(r, current.Owner) {
			writeError(w, http.StatusForbidden, "仅创建人或管理员可提交该发布版本")
			return
		}
		v, err := st.SetReleaseVersionReady(id)
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		wj(w, v)
	})
	mux.HandleFunc("POST /api/release-versions/{id}/promotions", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Environment string `json:"environment"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req) != nil {
			writeError(w, http.StatusBadRequest, "目标环境参数格式错误")
			return
		}
		v, err := st.CreateReleasePromotion(changeID(r), req.Environment, actorOf(r))
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		st.Audit(actorOf(r), "promote", "release-version", fmt.Sprint(v.ReleaseVersionID), map[string]any{"environment": v.Environment, "status": v.Status})
		wj(w, v)
	})
	for action, handler := range map[string]func(int64, *http.Request) (*ReleasePromotion, error){
		"approve": func(id int64, r *http.Request) (*ReleasePromotion, error) {
			return st.ReviewReleasePromotion(id, actorOf(r), true)
		},
		"reject": func(id int64, r *http.Request) (*ReleasePromotion, error) {
			return st.ReviewReleasePromotion(id, actorOf(r), false)
		},
		"complete": func(id int64, r *http.Request) (*ReleasePromotion, error) {
			return st.SetReleasePromotionStatus(id, []string{PromotionApproved}, PromotionSuccess)
		},
		"cancel": func(id int64, r *http.Request) (*ReleasePromotion, error) {
			return st.SetReleasePromotionStatus(id, []string{PromotionPendingApproval, PromotionApproved, PromotionRejected}, PromotionCanceled)
		},
	} {
		action, handler := action, handler
		mux.HandleFunc("POST /api/release-promotions/{id}/"+action, func(w http.ResponseWriter, r *http.Request) {
			v, err := handler(changeID(r), r)
			if err != nil {
				writeError(w, http.StatusConflict, err)
				return
			}
			wj(w, v)
		})
	}
	mux.HandleFunc("GET /api/services/{name}/resources", func(w http.ResponseWriter, r *http.Request) {
		if st.GetService(r.PathValue("name")) == nil {
			writeError(w, http.StatusNotFound, "应用不存在")
			return
		}
		wj(w, st.ApplicationResources(r.PathValue("name")))
	})
	mux.HandleFunc("PUT /api/services/{name}/resources", func(w http.ResponseWriter, r *http.Request) {
		var req ApplicationResources
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&req) != nil {
			writeError(w, http.StatusBadRequest, "应用资源参数格式错误")
			return
		}
		if err := st.SaveApplicationResources(r.PathValue("name"), req); err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		st.Audit(actorOf(r), "update", "application-resources", r.PathValue("name"), nil)
		wj(w, st.ApplicationResources(r.PathValue("name")))
	})
	mux.HandleFunc("GET /api/releases", func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		wj(w, st.ReleaseOrders(r.URL.Query().Get("service"), limit))
	})
	mux.HandleFunc("GET /api/releases/{id}", func(w http.ResponseWriter, r *http.Request) {
		var id int64
		fmt.Sscanf(r.PathValue("id"), "%d", &id)
		v := st.GetReleaseOrder(id)
		if v == nil {
			writeError(w, 404, "发布单不存在")
			return
		}
		wj(w, v)
	})
	mux.HandleFunc("POST /api/releases", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Service     string `json:"service"`
			ArtifactID  int64  `json:"artifact_id"`
			Version     string `json:"version"`
			Environment string `json:"environment"`
			Reason      string `json:"reason"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req) != nil {
			writeError(w, 400, "bad request")
			return
		}
		var v *ReleaseOrder
		var err error
		if req.ArtifactID > 0 {
			v, err = releaseManager.CreateArtifact(req.Service, req.ArtifactID, req.Environment, req.Reason, actorOf(r))
		} else {
			// Keep the old version selector for API compatibility. New clients must
			// send artifact_id so repeated version labels cannot be ambiguous.
			v, err = releaseManager.Create(req.Service, req.Version, req.Environment, req.Reason, actorOf(r))
		}
		if err != nil {
			writeError(w, 400, err)
			return
		}
		wj(w, v)
	})
	for action, handler := range map[string]func(int64, string) error{
		"approve": releaseManager.Approve, "reject": releaseManager.Reject, "continue": releaseManager.Continue, "cancel": releaseManager.Cancel,
	} {
		action, handler := action, handler
		mux.HandleFunc("POST /api/releases/{id}/"+action, func(w http.ResponseWriter, r *http.Request) {
			var id int64
			fmt.Sscanf(r.PathValue("id"), "%d", &id)
			order := st.GetReleaseOrder(id)
			if order == nil {
				writeError(w, http.StatusNotFound, "发布单不存在")
				return
			}
			if (action == "continue" || action == "cancel") && !canManageOwned(r, order.Actor) {
				writeError(w, http.StatusForbidden, "仅发起人或管理员可继续或取消发布")
				return
			}
			if err := handler(id, actorOf(r)); err != nil {
				writeError(w, 409, err)
				return
			}
			wj(w, st.GetReleaseOrder(id))
		})
	}
	mux.HandleFunc("POST /api/releases/{id}/rollback", func(w http.ResponseWriter, r *http.Request) {
		var id int64
		fmt.Sscanf(r.PathValue("id"), "%d", &id)
		order := st.GetReleaseOrder(id)
		if order == nil || !canManageOwned(r, order.Actor) {
			writeError(w, http.StatusForbidden, "仅发起人或管理员可回滚发布")
			return
		}
		var req struct {
			Reason string `json:"reason"`
		}
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req)
		if err := releaseManager.Rollback(id, strings.TrimSpace(req.Reason), actorOf(r)); err != nil {
			writeError(w, 409, err)
			return
		}
		wj(w, st.GetReleaseOrder(id))
	})
	mux.HandleFunc("POST /api/deploys", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Service     string `json:"service"`
			ArtifactID  int64  `json:"artifact_id"`
			Version     string `json:"version"`
			Host        string `json:"host"`
			Environment string `json:"environment"`
			Reason      string `json:"reason"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req) != nil || req.Service == "" || (req.ArtifactID <= 0 && req.Version == "") || req.Environment == "" {
			http.Error(w, `{"error":"bad request"}`, 400)
			return
		}
		if strings.TrimSpace(req.Host) != "" {
			writeError(w, http.StatusBadRequest, "发布不能临时指定目标；请选择应用环境，平台会从环境配置生成不可变目标快照")
			return
		}
		var order *ReleaseOrder
		var err error
		if req.ArtifactID > 0 {
			order, err = releaseManager.CreateArtifact(req.Service, req.ArtifactID, req.Environment, strings.TrimSpace(req.Reason), actorOf(r))
		} else {
			order, err = releaseManager.Create(req.Service, req.Version, req.Environment, strings.TrimSpace(req.Reason), actorOf(r))
		}
		if err != nil {
			writeError(w, errorStatus(err, http.StatusBadRequest), err)
			return
		}
		wj(w, map[string]any{"id": order.ID, "release_id": order.ID, "status": order.Status, "environment": order.Environment})
	})
	mux.HandleFunc("GET /api/deploys", func(w http.ResponseWriter, r *http.Request) {
		limit := 50
		if n, _ := strconv.Atoi(r.URL.Query().Get("limit")); n > 0 && n <= 500 {
			limit = n
		}
		list := st.Deploys(r.URL.Query().Get("service"), limit)
		if environment := r.URL.Query().Get("environment"); environment != "" {
			filtered := make([]Deploy, 0, len(list))
			for _, deploy := range list {
				if deploy.Environment == environment {
					filtered = append(filtered, deploy)
				}
			}
			list = filtered
		}
		wj(w, list)
	})
	mux.HandleFunc("GET /api/deploys/{id}/log", func(w http.ResponseWriter, r *http.Request) {
		var id int64
		fmt.Sscanf(r.PathValue("id"), "%d", &id)
		var log string
		st.db.QueryRow(`SELECT log FROM deploys WHERE id=?`, id).Scan(&log)
		wj(w, map[string]string{"log": log})
	})
	mux.HandleFunc("POST /api/deploys/{id}/rollback", func(w http.ResponseWriter, r *http.Request) {
		var id int64
		fmt.Sscanf(r.PathValue("id"), "%d", &id)
		dp := st.GetDeploy(id)
		if dp == nil {
			writeError(w, http.StatusNotFound, "deploy not found")
			return
		}
		var req struct {
			Reason           string `json:"reason"`
			ConfirmProtected bool   `json:"confirm_protected"`
		}
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req)
		if dp.Environment != "" {
			env := st.GetEnvironment(dp.Environment)
			if env == nil || st.GetServiceEnvironment(dp.Service, dp.Environment) == nil {
				writeError(w, http.StatusConflict, "原部署环境配置已不存在，拒绝按当前默认配置回滚")
				return
			}
			if env != nil && env.Protected && !req.ConfirmProtected {
				writeError(w, http.StatusPreconditionRequired, "受保护环境回滚需要二次确认")
				return
			}
			if env != nil && env.RequireReason && strings.TrimSpace(req.Reason) == "" {
				writeError(w, http.StatusBadRequest, "受保护环境回滚必须填写原因")
				return
			}
		} else if env := st.ProtectedEnvironmentForHost(dp.Service, dp.Host); env != nil {
			writeError(w, http.StatusConflict, "该历史直发主机现已属于受保护环境 "+env.DisplayName+"，请在环境发布页选择目标版本重新发布")
			return
		}
		nid, err := deployer.Rollback(id, strings.TrimSpace(req.Reason), actorOf(r))
		if err != nil {
			writeError(w, errorStatus(err, http.StatusBadRequest), err)
			return
		}
		wj(w, map[string]any{"id": nid})
	})
	// 部署日志实时（WS，key 为 -deployID）
	mux.HandleFunc("GET /api/deploys/{id}/tail", func(w http.ResponseWriter, r *http.Request) {
		var id int64
		fmt.Sscanf(r.PathValue("id"), "%d", &id)
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		ch, unsub := hub.Subscribe(-id)
		defer unsub()
		for {
			select {
			case <-r.Context().Done():
				return
			case line, ok := <-ch:
				if !ok {
					c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(1000, "deploy finished"), time.Now().Add(2*time.Second))
					return
				}
				c.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if c.WriteMessage(websocket.TextMessage, []byte(line)) != nil {
					return
				}
			}
		}
	})

	// 配置导出（YAML，可提交 git 做版本化备份）
	mux.HandleFunc("GET /api/services/export", func(w http.ResponseWriter, r *http.Request) {
		type expSvc struct {
			Name                    string     `yaml:"name" json:"name"`
			GitURL                  string     `yaml:"git_url" json:"git_url"`
			Branch                  string     `yaml:"branch" json:"branch"`
			BuildTool               string     `yaml:"build_tool,omitempty" json:"build_tool,omitempty"`
			MavenExecutable         string     `yaml:"maven_executable,omitempty" json:"maven_executable,omitempty"`
			MavenGoals              string     `yaml:"maven_goals,omitempty" json:"maven_goals,omitempty"`
			MavenProfiles           string     `yaml:"maven_profiles,omitempty" json:"maven_profiles,omitempty"`
			MavenProperties         string     `yaml:"maven_properties,omitempty" json:"maven_properties,omitempty"`
			MavenSettingsPath       string     `yaml:"maven_settings_path,omitempty" json:"maven_settings_path,omitempty"`
			GradleExecutable        string     `yaml:"gradle_executable,omitempty" json:"gradle_executable,omitempty"`
			GradleTasks             string     `yaml:"gradle_tasks,omitempty" json:"gradle_tasks,omitempty"`
			GradleExcludedTasks     string     `yaml:"gradle_excluded_tasks,omitempty" json:"gradle_excluded_tasks,omitempty"`
			GradleProjectProperties string     `yaml:"gradle_project_properties,omitempty" json:"gradle_project_properties,omitempty"`
			GradleSystemProperties  string     `yaml:"gradle_system_properties,omitempty" json:"gradle_system_properties,omitempty"`
			Steps                   []StepInst `yaml:"steps" json:"steps"`
		}
		var out []expSvc
		for _, sv := range st.Services() {
			out = append(out, expSvc{Name: sv.Name, GitURL: sv.GitURL, Branch: sv.Branch,
				BuildTool: sv.BuildTool, MavenExecutable: sv.MavenExecutable, MavenGoals: sv.MavenGoals,
				MavenProfiles: sv.MavenProfiles, MavenProperties: sv.MavenProperties, MavenSettingsPath: sv.MavenSettingsPath,
				GradleExecutable: sv.GradleExecutable, GradleTasks: sv.GradleTasks, GradleExcludedTasks: sv.GradleExcludedTasks,
				GradleProjectProperties: sv.GradleProjectProperties, GradleSystemProperties: sv.GradleSystemProperties,
				Steps: sv.Steps})
		}
		b, _ := yaml.Marshal(out)
		w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
		w.Write(b)
	})

	// 步骤库（插件目录）
	mux.HandleFunc("GET /api/steps", func(w http.ResponseWriter, r *http.Request) {
		wj(w, stepLib.Load())
	})
	mux.HandleFunc("GET /api/deploy/drivers", func(w http.ResponseWriter, r *http.Request) {
		wj(w, deployer.DriverManifests())
	})

	// ---------- 流水线 ----------
	pr := NewPipelineRunner(st, b, hub, deployer, stepLib)
	startRun := func(sv *Service, opts PipelineRunOptions, actor string) (int64, error) {
		delivery := st.GetServiceDelivery(sv.Name)
		deployEnvironment := strings.TrimSpace(opts.DeployEnvironment)
		deployReason := strings.TrimSpace(opts.DeployReason)
		if deployEnvironment == "" && delivery != nil && delivery.AutoDeployEnvironment != "" && opts.Trigger != "manual" {
			deployEnvironment = delivery.AutoDeployEnvironment
			deployReason = "流水线自动发布"
		}
		opts.DeployEnvironment = deployEnvironment
		opts.DeployReason = deployReason
		id, err := pr.StartWithOptions(sv, deployEnvironment != "", opts, actor)
		if err != nil {
			return 0, err
		}
		if deployEnvironment != "" {
			go func() {
				deadline := time.Now().Add(30 * time.Minute)
				for time.Now().Before(deadline) {
					run := st.GetRun(id)
					if run == nil || run.Status == "failed" || run.Status == "interrupted" {
						return
					}
					if (run.Status == "success" || run.Status == "waiting_deploy") && run.ArtifactID > 0 {
						order, releaseErr := releaseManager.CreateArtifact(sv.Name, run.ArtifactID, deployEnvironment, deployReason, actor)
						if releaseErr != nil {
							st.AppendRunLog(id, "✗ 构建成功，但自动提交发布失败: "+releaseErr.Error())
							st.Audit(actor, "fail", "pipeline-release", fmt.Sprint(id), map[string]any{"service": sv.Name, "environment": deployEnvironment, "error": releaseErr.Error()})
						} else {
							st.AppendRunLog(id, fmt.Sprintf("✓ 已自动创建发布单 #%d → %s（%s）", order.ID, deployEnvironment, order.Status))
						}
						return
					}
					time.Sleep(time.Second)
				}
				st.AppendRunLog(id, "✗ 构建超过 30 分钟，未自动提交发布")
			}()
		}
		return id, nil
	}
	mux.HandleFunc("POST /api/hooks/{service}/{token}", func(w http.ResponseWriter, r *http.Request) {
		delivery := st.GetServiceDelivery(r.PathValue("service"))
		if delivery == nil || delivery.WebhookToken == "" || delivery.WebhookToken != r.PathValue("token") {
			writeError(w, 404, "hook not found")
			return
		}
		var req struct {
			Branch string `json:"branch"`
			Ref    string `json:"ref"`
		}
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req)
		branch := strings.TrimSpace(req.Branch)
		if branch == "" {
			branch = strings.TrimPrefix(strings.TrimSpace(req.Ref), "refs/heads/")
		}
		sv := st.GetService(r.PathValue("service"))
		id, err := startRun(sv, PipelineRunOptions{Branch: branch, Trigger: "webhook"}, "webhook")
		if err != nil {
			writeError(w, errorStatus(err, 500), err)
			return
		}
		st.Audit("webhook", "trigger", "pipeline", fmt.Sprint(id), map[string]any{"service": sv.Name, "branch": branch})
		wj(w, map[string]any{"id": id})
	})
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for now := range ticker.C {
			st.syncDueIssues(now)
		}
	}()
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for now := range ticker.C {
			for _, delivery := range st.AllServiceDelivery() {
				if delivery.ScheduleMinutes < 1 || now.Unix()-delivery.LastScheduleAt < int64(delivery.ScheduleMinutes*60) {
					continue
				}
				sv := st.GetService(delivery.Service)
				if sv == nil || st.ActiveRun(sv.Name) != nil {
					continue
				}
				if id, err := startRun(sv, PipelineRunOptions{Trigger: "schedule"}, "scheduler"); err == nil {
					st.MarkScheduled(sv.Name, now.Unix())
					st.Audit("scheduler", "trigger", "pipeline", fmt.Sprint(id), map[string]any{"service": sv.Name})
				}
			}
		}
	}()
	mux.HandleFunc("POST /api/pipeline/runs", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Service                 string `json:"service"`
			Branch                  string `json:"branch"` // 可选：本次运行的分支覆盖
			AutoDeploy              bool   `json:"auto_deploy"`
			DeployEnvironment       string `json:"deploy_environment"`
			Reason                  string `json:"reason"`
			MavenProfiles           string `json:"maven_profiles"`
			MavenProperties         string `json:"maven_properties"`
			OverrideMaven           bool   `json:"override_maven"`
			GradleTasks             string `json:"gradle_tasks"`
			GradleExcludedTasks     string `json:"gradle_excluded_tasks"`
			GradleProjectProperties string `json:"gradle_project_properties"`
			GradleSystemProperties  string `json:"gradle_system_properties"`
			OverrideGradle          bool   `json:"override_gradle"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&req) != nil || req.Service == "" {
			http.Error(w, `{"error":"bad request"}`, 400)
			return
		}
		sv := st.GetService(req.Service)
		if sv == nil {
			http.Error(w, `{"error":"service not found"}`, 404)
			return
		}
		if strings.TrimSpace(req.DeployEnvironment) != "" {
			if _, err := releaseManager.ValidateTarget(sv.Name, strings.TrimSpace(req.DeployEnvironment), strings.TrimSpace(req.Reason)); err != nil {
				writeError(w, errorStatus(err, http.StatusBadRequest), err)
				return
			}
		}
		id, err := startRun(sv, PipelineRunOptions{
			Branch: req.Branch, DeployEnvironment: req.DeployEnvironment, DeployReason: req.Reason,
			MavenProfiles: req.MavenProfiles, MavenProperties: req.MavenProperties, OverrideMaven: req.OverrideMaven,
			GradleTasks: req.GradleTasks, GradleExcludedTasks: req.GradleExcludedTasks,
			GradleProjectProperties: req.GradleProjectProperties, GradleSystemProperties: req.GradleSystemProperties,
			OverrideGradle: req.OverrideGradle, Trigger: "manual",
		}, actorOf(r))
		if err != nil {
			writeError(w, errorStatus(err, http.StatusInternalServerError), err)
			return
		}
		st.Audit(actorOf(r), "trigger", "pipeline", fmt.Sprint(id), map[string]any{"service": req.Service, "branch": req.Branch, "deploy_environment": strings.TrimSpace(req.DeployEnvironment)})
		wj(w, map[string]any{"id": id})
	})
	mux.HandleFunc("GET /api/pipeline/runs", func(w http.ResponseWriter, r *http.Request) {
		limit := 30
		if n, _ := strconv.Atoi(r.URL.Query().Get("limit")); n > 0 && n <= 200 {
			limit = n
		}
		wj(w, st.Runs(r.URL.Query().Get("service"), limit))
	})
	mux.HandleFunc("GET /api/pipeline/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		var id int64
		fmt.Sscanf(r.PathValue("id"), "%d", &id)
		run := st.GetRun(id)
		if run == nil {
			http.Error(w, `{"error":"not found"}`, 404)
			return
		}
		wj(w, run)
	})
	mux.HandleFunc("GET /api/pipeline/runs/{id}/log", func(w http.ResponseWriter, r *http.Request) {
		var id int64
		fmt.Sscanf(r.PathValue("id"), "%d", &id)
		wj(w, map[string]string{"log": st.RunLog(id)})
	})
	mux.HandleFunc("POST /api/pipeline/runs/{id}/deploy", func(w http.ResponseWriter, r *http.Request) {
		var id int64
		fmt.Sscanf(r.PathValue("id"), "%d", &id)
		var req struct {
			Host             string `json:"host"`
			Environment      string `json:"environment"`
			Reason           string `json:"reason"`
			ConfirmProtected bool   `json:"confirm_protected"`
		}
		json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req)
		if req.Environment != "" {
			run := st.GetRun(id)
			if run == nil {
				writeError(w, http.StatusNotFound, "run not found")
				return
			}
			target := st.GetServiceEnvironment(run.Service, req.Environment)
			if target == nil {
				writeError(w, http.StatusBadRequest, "服务未配置该环境")
				return
			}
			if target.Protected {
				writeError(w, http.StatusPreconditionRequired, "受保护环境必须通过发布单审批，不能从流水线直接部署")
				return
			}
			if target.RequireReason && strings.TrimSpace(req.Reason) == "" {
				writeError(w, http.StatusBadRequest, "该环境要求填写发布原因")
				return
			}
			if err := pr.ContinueDeployEnvironment(id, target, strings.TrimSpace(req.Reason), actorOf(r)); err != nil {
				writeError(w, errorStatus(err, http.StatusBadRequest), err)
				return
			}
			wj(w, map[string]bool{"ok": true})
			return
		}
		if err := pr.ContinueDeploy(id, req.Host, actorOf(r)); err != nil {
			writeError(w, errorStatus(err, http.StatusBadRequest), err)
			return
		}
		wj(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/pipeline/runs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		var id int64
		fmt.Sscanf(r.PathValue("id"), "%d", &id)
		if !st.CancelWaitingRun(id) {
			writeError(w, http.StatusConflict, "仅等待部署的流水线可以取消")
			return
		}
		wj(w, map[string]bool{"ok": true})
	})
	// 流水线日志实时（WS，hub key=1e6+runID）
	mux.HandleFunc("GET /api/pipeline/runs/{id}/tail", func(w http.ResponseWriter, r *http.Request) {
		var id int64
		fmt.Sscanf(r.PathValue("id"), "%d", &id)
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		// 先补历史
		c.WriteMessage(websocket.TextMessage, []byte(st.RunLog(id)))
		ch, unsub := hub.Subscribe(1000000 + id)
		defer unsub()
		for {
			select {
			case <-r.Context().Done():
				return
			case line, ok := <-ch:
				if !ok {
					c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(1000, "finished"), time.Now().Add(2*time.Second))
					return
				}
				c.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if c.WriteMessage(websocket.TextMessage, []byte(line)) != nil {
					return
				}
			}
		}
	})

	// UI
	sub, _ := fs.Sub(uiFS, "assets/app/assets")
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(sub))))
	// Vue 版（vite 产物）；/ui-legacy 保留旧手写版过渡
	mux.HandleFunc("GET /ui", func(w http.ResponseWriter, r *http.Request) {
		bt, _ := uiFS.ReadFile("assets/app/deploy.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(bt)
	})
	mux.HandleFunc("GET /ui-legacy", func(w http.ResponseWriter, r *http.Request) {
		bt, _ := uiFS.ReadFile("assets/ui.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(bt)
	})
	return mux
}
