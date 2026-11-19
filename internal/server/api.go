package server

import (
	"compress/gzip"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"logplatform/internal/appcatalog"
	"logplatform/internal/types"
)

// APIServer HTTP 层：ingest（数据面，token 鉴权）+ 查询 API + tail + 订阅
type APIServer struct {
	st     *Storage
	token  string
	hub    *Hub
	alerts *AlertEngine
	agg    *MetricsAggregator
	apps   *appcatalog.Observer

	agentsMu sync.Mutex
	agents   map[string]*AgentStat
}

// AgentStat agent 心跳状态（由 ingest 更新）
type AgentStat struct {
	ID         string `json:"id"`
	Name       string `json:"name,omitempty"`
	stable     bool
	aliases    []string
	LastSeen   int64 `json:"last_seen"`
	Events     int64 `json:"events"`
	Dropped    int64 `json:"dropped"`
	QueueDepth int   `json:"queue_depth"`
	SpoolBytes int64 `json:"spool_bytes"`
	Online     bool  `json:"online"`
}

func NewAPI(st *Storage, token, busURL string) *APIServer {
	a := &APIServer{st: st, token: token, hub: NewHub(), apps: appcatalog.NewObserver(busURL), agents: map[string]*AgentStat{}}
	a.alerts = NewAlertEngine(st)
	a.agg = NewMetricsAggregator(busURL)
	go a.agg.Run(st.stop)
	return a
}

func (a *APIServer) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /ingest", a.handleIngest)
	mux.HandleFunc("GET /api/search", a.handleSearch)
	mux.HandleFunc("GET /api/count", a.handleCount)
	mux.HandleFunc("GET /api/services", a.handleServices)
	mux.HandleFunc("GET /api/environments", a.handleEnvironments)
	mux.HandleFunc("GET /api/stats", a.handleStats)
	mux.HandleFunc("GET /api/tail", a.handleTail)
	mux.HandleFunc("GET /api/alerts", a.handleAlertsList)
	mux.HandleFunc("POST /api/alerts", a.handleAlertsCreate)
	mux.HandleFunc("PUT /api/alerts/{id}", a.handleAlertsUpdate)
	mux.HandleFunc("DELETE /api/alerts/{id}", a.handleAlertsDelete)
	mux.HandleFunc("POST /api/alerts/{id}/test", a.handleAlertsTest)
	mux.HandleFunc("GET /api/alerts/status", a.handleAlertsStatus)
	mux.HandleFunc("GET /api/alert-notifications", a.handleAlertNotifications)
	a.registerQuarantine(mux)
	a.registerUI(mux)
	return mux
}

// handleIngest 数据面：X-Token 鉴权 + gzip 批量 + 服务端兜底截断
func (a *APIServer) handleIngest(w http.ResponseWriter, r *http.Request) {
	if a.token != "" && r.Header.Get("X-Token") != a.token {
		http.Error(w, `{"error":"unauthorized"}`, 401)
		return
	}
	body := http.MaxBytesReader(w, r.Body, 8<<20)
	if r.Header.Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(body)
		if err != nil {
			http.Error(w, `{"error":"bad gzip"}`, 400)
			return
		}
		defer zr.Close()
		body = zr
	}
	raw, err := io.ReadAll(io.LimitReader(body, (8<<20)+1))
	if err != nil {
		http.Error(w, `{"error":"read body"}`, 400)
		return
	}
	if len(raw) > 8<<20 {
		http.Error(w, `{"error":"decompressed body too large"}`, http.StatusRequestEntityTooLarge)
		return
	}
	var req types.IngestRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		http.Error(w, `{"error":"bad json"}`, 400)
		return
	}
	if len(req.BatchID) > 256 || strings.ContainsAny(req.BatchID, "\r\n\x00") {
		http.Error(w, `{"error":"批次标识无效"}`, 400)
		return
	}
	if len(req.AgentID) > 256 || len(req.AgentName) > 256 || len(req.AgentAliases) > 32 || strings.ContainsAny(req.AgentID+req.AgentName, "\r\n\x00") {
		http.Error(w, `{"error":"invalid agent identity metadata"}`, http.StatusBadRequest)
		return
	}
	for _, alias := range req.AgentAliases {
		if len(alias) > 256 || strings.ContainsAny(alias, "\r\n\x00") {
			http.Error(w, `{"error":"invalid agent alias"}`, http.StatusBadRequest)
			return
		}
	}
	if len(req.Events) > 2000 {
		http.Error(w, `{"error":"too many events"}`, http.StatusRequestEntityTooLarge)
		return
	}
	if len(req.Events) == 0 {
		a.trackAgent(req, 0)
		writeJSON(w, types.IngestResponse{BatchID: req.BatchID, Accepted: 0, Acknowledged: 0})
		return
	}
	if req.AgentID == "" || req.BatchID == "" {
		http.Error(w, `{"error":"非空批次需要 agent_id 和 batch_id"}`, 400)
		return
	}
	// Resolve the source-specific service name to the platform-wide stable ID.
	// Catalog availability must never block ingestion: on a transient failure the
	// event is accepted with its current ID and a later batch retries resolution.
	sourceHash := hashLog(req.Events)
	resolved := map[string]string{}
	for i := range req.Events {
		event := &req.Events[i]
		key := event.Service + "\x00" + event.ApplicationID
		id, ok := resolved[key]
		if !ok {
			id, _ = a.apps.Resolve(event.Service, "logs", event.ApplicationID)
			resolved[key] = id
		}
		if id != "" {
			event.ApplicationID = id
		}
	}
	accepted, quarantined, err := a.st.AcceptBatch(req.AgentID, req.BatchID, sourceHash, req.Events)
	if err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, ErrLogIdentityConflict) {
			status = http.StatusConflict
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	a.trackAgent(req, len(accepted))
	// 仅 fan-out 新写入事件；重试批次不会在实时流和指标中重复出现。
	hits := make([]types.Hit, len(accepted))
	for i := range accepted {
		e := &accepted[i]
		hits[i] = types.Hit{EventID: e.ID, Ts: e.Ts, ApplicationID: e.ApplicationID, Service: e.Service, Environment: e.Environment, Host: e.Host, Path: e.Path, Level: e.Level, TraceID: e.TraceID, Message: e.Message}
	}
	a.hub.Publish(hits)
	a.agg.Add(accepted)
	writeJSON(w, types.IngestResponse{BatchID: req.BatchID, Accepted: len(accepted), Acknowledged: len(req.Events), Quarantined: quarantined})
}

// trackAgent 记录 agent 心跳与累计量
func stableAgentID(id string) bool {
	if !strings.HasPrefix(id, "agent_") || len(id) != 38 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(id, "agent_"))
	return err == nil
}

func (a *APIServer) trackAgent(req types.IngestRequest, events int) {
	if req.AgentID == "" {
		return
	}
	a.agentsMu.Lock()
	defer a.agentsMu.Unlock()
	s := a.agents[req.AgentID]
	if s == nil {
		s = &AgentStat{ID: req.AgentID}
		a.agents[req.AgentID] = s
	}
	s.LastSeen = time.Now().Unix()
	s.Events += int64(events)
	s.Dropped += int64(req.Dropped)
	s.QueueDepth, s.SpoolBytes = req.QueueDepth, req.SpoolBytes
	if req.AgentName != "" && stableAgentID(req.AgentID) {
		s.Name = req.AgentName
		s.stable = true
		s.aliases = append([]string(nil), req.AgentAliases...)
		if len(s.aliases) == 0 {
			s.aliases = []string{s.Name}
		}
	}
	// Only explicit, uniquely claimed aliases of offline legacy senders migrate.
	// Never merge live senders or two stable installations just by display name.
	if !s.stable {
		return
	}
	for _, alias := range s.aliases {
		old := a.agents[alias]
		if old == nil || old.stable || alias == s.ID || s.LastSeen-old.LastSeen < 30 {
			continue
		}
		unique := true
		for id, other := range a.agents {
			if id != s.ID && other.stable {
				for _, name := range other.aliases {
					if name == alias {
						unique = false
					}
				}
			}
		}
		if !unique {
			continue
		}
		s.Events += old.Events
		s.Dropped += old.Dropped
		delete(a.agents, alias)
	}
}

// handleStats agent 在线状态 + 服务计数 + 磁盘
func (a *APIServer) handleStats(w http.ResponseWriter, r *http.Request) {
	now := time.Now().Unix()
	a.agentsMu.Lock()
	agents := make([]AgentStat, 0, len(a.agents))
	for _, s := range a.agents {
		online := now-s.LastSeen < 30
		agents = append(agents, AgentStat{ID: s.ID, Name: s.Name, LastSeen: s.LastSeen, Events: s.Events, Dropped: s.Dropped, QueueDepth: s.QueueDepth, SpoolBytes: s.SpoolBytes})
		agents[len(agents)-1].Online = online
	}
	a.agentsMu.Unlock()
	sort.Slice(agents, func(i, j int) bool {
		left, right := agents[i].Name, agents[j].Name
		if left == "" {
			left = agents[i].ID
		}
		if right == "" {
			right = agents[j].ID
		}
		if left == right {
			return agents[i].ID < agents[j].ID
		}
		return left < right
	})
	services, servicesErr := a.st.ListServices()
	health := a.st.backendHealth()
	storage := a.st.StorageStatus()
	if servicesErr != nil {
		storage.LastError = servicesErr.Error()
	} else if health.Error != "" {
		storage.LastError = health.Error
	}
	writeJSON(w, map[string]any{
		"agents":         agents,
		"services":       services,
		"disk_used":      health.Bytes,
		"storage":        storage,
		"retention_days": a.st.retentionDays,
	})
}

// handleSearch 查询：service/start/end/q/trace_id/cursor/limit
func (a *APIServer) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	end := time.Now().UnixMilli()
	if v := q.Get("end"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			end = n
		}
	}
	start := end - 15*60*1000
	if cursor := q.Get("cursor"); cursor != "" {
		from, to, e := logCursorWindow(cursor)
		if e != nil {
			writeQueryError(w, e)
			return
		}
		if q.Get("start") == "" {
			start = from
		}
		if q.Get("end") == "" {
			end = to
		}
	}
	if v := q.Get("start"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			start = n
		}
	}
	if start > end {
		start, end = end, start
	}
	limit := 100
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}
	resp, err := a.st.Search(q.Get("service"), q.Get("environment"), q.Get("host"), strings.Join(q["level"], ","), start, end, q.Get("q"), q.Get("trace_id"), q.Get("cursor"), limit)
	if err != nil {
		writeQueryError(w, err)
		return
	}
	writeJSON(w, resp)
}

// handleCount 关键字分钟计数（monitor 阈值报警评估用）
func (a *APIServer) handleCount(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	end := time.Now().UnixMilli()
	start := end - 15*60*1000
	if v := q.Get("start"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			start = n
		}
	}
	if v := q.Get("end"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			end = n
		}
	}
	pts, err := a.st.CountSeries(q.Get("service"), q.Get("environment"), strings.Join(q["level"], ","), q.Get("q"), start, end)
	if err != nil {
		writeQueryError(w, err)
		return
	}
	writeJSON(w, map[string]any{"points": pts})
}

func writeQueryError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	var syntaxErr *QuerySyntaxError
	if errors.As(err, &syntaxErr) {
		status = http.StatusBadRequest
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func (a *APIServer) handleServices(w http.ResponseWriter, r *http.Request) {
	m, err := a.st.ListServices()
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
		return
	}
	writeJSON(w, m)
}

func (a *APIServer) handleEnvironments(w http.ResponseWriter, r *http.Request) {
	list, err := a.st.ListEnvironments(r.URL.Query().Get("service"))
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
		return
	}
	writeJSON(w, list)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// ---------- 报警规则 CRUD HTTP ----------

func (a *APIServer) handleAlertsList(w http.ResponseWriter, r *http.Request) {
	environment := r.URL.Query().Get("environment")
	rules := a.alerts.List()
	if environment != "" {
		filtered := rules[:0]
		for _, rule := range rules {
			if rule.Environment == environment || rule.Environment == "*" {
				filtered = append(filtered, rule)
			}
		}
		rules = filtered
	}
	writeJSON(w, rules)
}

func (a *APIServer) handleAlertsCreate(w http.ResponseWriter, r *http.Request) {
	var rule AlertRule
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&rule); err != nil {
		http.Error(w, `{"error":"bad request"}`, 400)
		return
	}
	if err := a.alerts.Create(&rule); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, 400)
		return
	}
	writeJSON(w, rule)
}

func (a *APIServer) handleAlertsUpdate(w http.ResponseWriter, r *http.Request) {
	var rule AlertRule
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&rule); err != nil {
		http.Error(w, `{"error":"bad request"}`, 400)
		return
	}
	var id int64
	fmt.Sscanf(r.PathValue("id"), "%d", &id)
	rule.ID = id
	if err := a.alerts.Update(&rule); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, 400)
		return
	}
	writeJSON(w, rule)
}

func (a *APIServer) handleAlertsDelete(w http.ResponseWriter, r *http.Request) {
	var id int64
	fmt.Sscanf(r.PathValue("id"), "%d", &id)
	if err := a.alerts.Delete(id); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *APIServer) handleAlertsTest(w http.ResponseWriter, r *http.Request) {
	var id int64
	fmt.Sscanf(r.PathValue("id"), "%d", &id)
	if err := a.alerts.Test(id); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *APIServer) handleAlertsStatus(w http.ResponseWriter, r *http.Request) {
	statuses := a.alerts.Statuses()
	environment := r.URL.Query().Get("environment")
	if environment != "" {
		allowed := map[int64]bool{}
		for _, rule := range a.alerts.List() {
			if rule.Environment == environment || rule.Environment == "*" {
				allowed[rule.ID] = true
			}
		}
		for id := range statuses {
			if !allowed[id] {
				delete(statuses, id)
			}
		}
	}
	writeJSON(w, statuses)
}

func (a *APIServer) handleAlertNotifications(w http.ResponseWriter, r *http.Request) {
	var rid int64
	fmt.Sscanf(r.URL.Query().Get("rule_id"), "%d", &rid)
	limit := 50
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 500 {
		limit = n
	}
	writeJSON(w, a.alerts.Notifications(rid, r.URL.Query().Get("environment"), limit))
}
