package monitor

// Native console for Arthas HTTP API and deployment-managed SSH channels.
import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const arthasTargetDDL = `CREATE TABLE IF NOT EXISTS arthas_targets (
 id VARCHAR(64) PRIMARY KEY, name VARCHAR(128) NOT NULL, service VARCHAR(128) NOT NULL,
 environment VARCHAR(64) NOT NULL, endpoint TEXT NOT NULL, revision BIGINT NOT NULL,
 updated_at BIGINT NOT NULL, deleted INTEGER NOT NULL DEFAULT 0)`
const arthasAuditDDL = `CREATE TABLE IF NOT EXISTS arthas_audit (
 id VARCHAR(64) PRIMARY KEY, actor VARCHAR(191) NOT NULL, target_id VARCHAR(64) NOT NULL,
 action VARCHAR(32) NOT NULL, command_name VARCHAR(32) NOT NULL, command_hash VARCHAR(64) NOT NULL,
 state VARCHAR(32) NOT NULL, at BIGINT NOT NULL)`

type arthasTarget struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Service       string `json:"service"`
	Environment   string `json:"environment"`
	Endpoint      string `json:"endpoint"`
	Revision      int64  `json:"revision"`
	UpdatedAt     int64  `json:"updated_at"`
	HasCredential bool   `json:"has_credential"`
	Managed       bool   `json:"managed"`
}
type arthasSession struct {
	mu                                              sync.Mutex
	id, owner, remote, consumer, username, password string
	target                                          arthasTarget
	created, touched                                time.Time
	busy, uncertain, closed, interrupted            bool
}
type arthasConsole struct {
	st            *Store
	client        *http.Client
	mu            sync.Mutex
	sessions      map[string]*arthasSession
	pending       map[string]int
	targetPending map[string]int
	stopping      bool
	stop          chan struct{}
	done          chan struct{}
}

var arthasCommandName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

func arthasID() string {
	b := make([]byte, 24)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}

func validateArthasTarget(t *arthasTarget) error {
	t.Name = strings.TrimSpace(t.Name)
	t.Service = strings.TrimSpace(t.Service)
	t.Environment = strings.TrimSpace(t.Environment)
	if t.Name == "" || len(t.Name) > 128 || t.Service == "" || len(t.Service) > 128 || t.Environment == "" || len(t.Environment) > 64 {
		return errors.New("请填写连接名称、应用标识和环境标识")
	}
	for _, v := range []string{t.Name, t.Service, t.Environment} {
		if strings.ContainsAny(v, "\r\n\x00") {
			return errors.New("连接信息不能包含控制字符")
		}
	}
	u, e := url.Parse(strings.TrimSpace(t.Endpoint))
	if e != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || (u.Path != "" && u.Path != "/") || u.RawPath != "" {
		return errors.New("请填写 Arthas HTTP 根地址，例如 http://主机:8563，不含账号、密码、查询参数或子路径")
	}
	if len(t.Endpoint) > 2048 || strings.ContainsAny(u.Host, "\\\r\n\x00") || strings.HasSuffix(u.Host, ":") {
		return errors.New("Arthas 地址无效")
	}
	if p := u.Port(); p != "" {
		n, e := strconv.Atoi(p)
		if e != nil || n < 1 || n > 65535 {
			return errors.New("Arthas 端口无效")
		}
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && !arthasIPAllowed(ip) {
		return errors.New("不能使用未指定、多播或链路本地地址")
	}
	u.Path = ""
	t.Endpoint = u.String()
	return nil
}
func arthasIPAllowed(ip net.IP) bool {
	return !ip.IsUnspecified() && !ip.IsMulticast() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast()
}
func arthasHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	return &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect not permitted") }, Transport: &http.Transport{Proxy: nil, ResponseHeaderTimeout: 13 * time.Second, TLSHandshakeTimeout: 3 * time.Second, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, e
		}
		ips, e := net.DefaultResolver.LookupIPAddr(ctx, host)
		if e != nil {
			return nil, e
		}
		for _, ip := range ips {
			if !arthasIPAllowed(ip.IP) {
				return nil, errors.New("address not allowed")
			}
		}
		var last error
		for _, ip := range ips {
			c, e := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if e == nil {
				return c, nil
			}
			last = e
		}
		if last == nil {
			last = errors.New("host unavailable")
		}
		return nil, last
	}}}
}
func newArthasConsole(st *Store) *arthasConsole {
	m := &arthasConsole{st: st, client: arthasHTTPClient(), sessions: map[string]*arthasSession{}, pending: map[string]int{}, targetPending: map[string]int{}, stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(m.done)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-m.stop:
				return
			case now := <-ticker.C:
				m.expire(now)
			}
		}
	}()
	return m
}
func (m *arthasConsole) close() {
	m.mu.Lock()
	m.stopping = true
	m.mu.Unlock()
	close(m.stop)
	<-m.done
	m.mu.Lock()
	all := m.sessions
	m.sessions = map[string]*arthasSession{}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, s := range all {
		wg.Add(1)
		go func(s *arthasSession) { defer wg.Done(); m.closeRemote(s) }(s)
	}
	wg.Wait()
}
func (m *arthasConsole) expire(now time.Time) {
	m.mu.Lock()
	expired := []*arthasSession{}
	for id, s := range m.sessions {
		s.mu.Lock()
		stale := now.Sub(s.created) > 15*time.Minute || !s.busy && now.Sub(s.touched) > 5*time.Minute
		s.mu.Unlock()
		if stale {
			delete(m.sessions, id)
			expired = append(expired, s)
		}
	}
	m.mu.Unlock()
	for _, s := range expired {
		_ = m.audit(s.owner, s.target.ID, "expire", "", "closed")
		m.closeRemote(s)
	}
}
func (m *arthasConsole) closeRemote(s *arthasSession) bool {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return false
	}
	s.closed = true
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := m.call(ctx, s, map[string]any{"action": "close_session"})
	return err == nil && out.State == "SUCCEEDED"
}

type arthasReply struct {
	State      string `json:"state"`
	SessionID  string `json:"sessionId"`
	ConsumerID string `json:"consumerId"`
	Message    string `json:"message"`
	Body       struct {
		Results     []json.RawMessage `json:"results"`
		JobStatus   string            `json:"jobStatus"`
		TimeExpired bool              `json:"timeExpired"`
	} `json:"body"`
}

func (m *arthasConsole) call(ctx context.Context, s *arthasSession, input map[string]any) (*arthasReply, error) {
	if s.remote != "" {
		input["sessionId"] = s.remote
	}
	if s.consumer != "" {
		input["consumerId"] = s.consumer
	}
	body, e := json.Marshal(input)
	if e != nil {
		return nil, errors.New("请求参数无效")
	}
	endpoint := s.target.Endpoint + "/api"
	managedID, fingerprint, managed := managedArthasEndpoint(s.target.Endpoint)
	if managed {
		if m.st.arthasDeployURL == "" {
			return nil, errors.New("内部部署通道未配置")
		}
		endpoint = m.st.arthasDeployURL + "/api/arthas-managed/instances/" + managedID + "/api"
	}
	req, e := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if e != nil {
		return nil, errors.New("Arthas 地址无效")
	}
	req.Header.Set("Content-Type", "application/json")
	if managed {
		req.Header.Set("X-User", s.owner)
		req.Header.Set("X-Role", "admin")
		req.Header.Set("X-Arthas-Fingerprint", fingerprint)
	}
	if s.password != "" {
		req.SetBasicAuth(s.username, s.password)
	}
	res, e := m.client.Do(req)
	if e != nil {
		return nil, errors.New("Arthas 未响应或连接中断，结果未确认；不会自动重试")
	}
	defer res.Body.Close()
	if res.StatusCode == 401 || res.StatusCode == 403 {
		return nil, errors.New("Arthas 认证失败，请核对当前会话的用户名和密码")
	}
	if res.StatusCode != 200 {
		if managed {
			var detail struct {
				Error string `json:"error"`
			}
			if json.NewDecoder(io.LimitReader(res.Body, 8192)).Decode(&detail) == nil && detail.Error != "" {
				return nil, errors.New(detail.Error)
			}
		}
		return nil, errors.New("Arthas HTTP 服务返回异常状态，请核对地址与版本")
	}
	raw, e := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if e != nil || len(raw) > 1<<20 {
		return nil, errors.New("Arthas 输出读取失败或超过 1 MiB，结果未确认；请缩小诊断范围")
	}
	var out arthasReply
	if json.Unmarshal(raw, &out) != nil {
		return nil, errors.New("Arthas 响应不是兼容的 HTTP API JSON")
	}
	if out.State != "SUCCEEDED" && out.State != "FAILED" && out.State != "REFUSED" {
		return nil, errors.New("Arthas 返回未知执行状态，请人工核对，不要重复提交")
	}
	return &out, nil
}
func (m *arthasConsole) audit(actor, id, action, command, state string) error {
	name, hash := "", ""
	if command != "" {
		sum := sha256.Sum256([]byte(command))
		hash = hex.EncodeToString(sum[:])
		name = strings.Fields(command)[0]
		if !arthasCommandName.MatchString(name) {
			name = "custom"
		}
	}
	_, e := m.st.db.Exec(`INSERT INTO arthas_audit(id,actor,target_id,action,command_name,command_hash,state,at) VALUES(?,?,?,?,?,?,?,?)`, arthasID(), actor, id, action, name, hash, state, time.Now().Unix())
	return e
}
func (s *Store) arthasTarget(id string) (*arthasTarget, error) {
	var t arthasTarget
	e := s.db.QueryRow(`SELECT id,name,service,environment,endpoint,revision,updated_at,EXISTS(SELECT 1 FROM arthas_credentials c WHERE c.target_id=arthas_targets.id) FROM arthas_targets WHERE id=? AND deleted=0`, id).Scan(&t.ID, &t.Name, &t.Service, &t.Environment, &t.Endpoint, &t.Revision, &t.UpdatedAt, &t.HasCredential)
	_, _, t.Managed = managedArthasEndpoint(t.Endpoint)
	return &t, e
}
func arthasError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
func arthasDecode(w http.ResponseWriter, r *http.Request, v any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(v) != nil {
		arthasError(w, 400, "请求参数无效或过大")
		return false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		arthasError(w, 400, "请求只能包含一个 JSON 对象")
		return false
	}
	return true
}

func (m *arthasConsole) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/arthas/deployed", m.deployedInstances)
	mux.HandleFunc("GET /api/arthas/deployed/{id}", m.deployedInstances)
	mux.HandleFunc("POST /api/arthas/deployed/{id}/attach", m.attachDeployed)
	mux.HandleFunc("GET /api/arthas/targets", m.listTargets)
	mux.HandleFunc("POST /api/arthas/targets", m.saveTarget)
	mux.HandleFunc("PUT /api/arthas/targets/{id}", m.saveTarget)
	mux.HandleFunc("DELETE /api/arthas/targets/{id}", m.deleteTarget)
	mux.HandleFunc("GET /api/arthas/audit", m.listAudit)
	mux.HandleFunc("POST /api/arthas/diagnose", m.diagnose)
	mux.HandleFunc("POST /api/arthas/sessions", m.connect)
	mux.HandleFunc("POST /api/arthas/sessions/{id}/execute", m.execute)
	mux.HandleFunc("POST /api/arthas/sessions/{id}/interrupt", m.interrupt)
	mux.HandleFunc("DELETE /api/arthas/sessions/{id}", m.disconnect)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Header.Get("X-User") == "" || r.Header.Get("X-Role") != "admin" {
			arthasError(w, 403, "仅平台管理员可配置和使用 Arthas 诊断控制台")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, e := url.Parse(origin)
			if e != nil || u.Host != r.Host {
				arthasError(w, 403, "拒绝跨来源诊断请求")
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}
func (m *arthasConsole) listTargets(w http.ResponseWriter, r *http.Request) {
	rows, e := m.st.db.Query(`SELECT id,name,service,environment,endpoint,revision,updated_at,EXISTS(SELECT 1 FROM arthas_credentials c WHERE c.target_id=arthas_targets.id) FROM arthas_targets WHERE deleted=0 AND (?='' OR service=?) AND (?='' OR environment=?) ORDER BY name,id`, r.URL.Query().Get("service"), r.URL.Query().Get("service"), r.URL.Query().Get("environment"), r.URL.Query().Get("environment"))
	if e != nil {
		arthasError(w, 500, "读取 Arthas 连接失败")
		return
	}
	defer rows.Close()
	out := []arthasTarget{}
	for rows.Next() {
		var t arthasTarget
		if rows.Scan(&t.ID, &t.Name, &t.Service, &t.Environment, &t.Endpoint, &t.Revision, &t.UpdatedAt, &t.HasCredential) != nil {
			arthasError(w, 500, "读取 Arthas 连接失败")
			return
		}
		_, _, t.Managed = managedArthasEndpoint(t.Endpoint)
		out = append(out, t)
	}
	if rows.Err() != nil {
		arthasError(w, 500, "读取 Arthas 连接失败")
		return
	}
	writeJSON(w, out)
}
func (m *arthasConsole) saveTarget(w http.ResponseWriter, r *http.Request) {
	var in struct {
		arthasTarget
		Credential      *arthasCredential `json:"credential"`
		ClearCredential bool              `json:"clear_credential"`
	}
	if !arthasDecode(w, r, &in) {
		return
	}
	t := in.arthasTarget
	t.Managed = false
	if in.Credential != nil && (in.Credential.Password == "" || len(in.Credential.Password) > 4096 || len(in.Credential.Username) > 128 || strings.ContainsAny(in.Credential.Username, ":\r\n\x00") || in.ClearCredential) {
		arthasError(w, 400, "认证配置无效：填写新密码或明确清除，不可同时操作")
		return
	}
	if in.Credential != nil && in.Credential.Username == "" {
		in.Credential.Username = "arthas"
	}
	if e := validateArthasTarget(&t); e != nil {
		arthasError(w, 400, e.Error())
		return
	}
	id := r.PathValue("id")
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.targetPending[id] > 0 {
		arthasError(w, 409, "此实例正在诊断，请结束后再修改配置")
		return
	}
	for _, s := range m.sessions {
		if s.target.ID == id {
			arthasError(w, 409, "此连接仍有活动会话，请先断开后再修改配置")
			return
		}
	}
	creating := r.Method == "POST"
	var previous *arthasTarget
	if !creating {
		var err error
		previous, err = m.st.arthasTarget(id)
		if err != nil || previous.Revision != t.Revision {
			arthasError(w, 409, "连接已变更，请刷新后重新编辑")
			return
		}
		if previous.Managed {
			arthasError(w, 409, "部署实例的连接由平台管理，请通过部署实例重新检测接入")
			return
		}
	}
	if creating && t.ID != "" || !creating && t.ID != "" && t.ID != id {
		arthasError(w, 400, "连接标识不能修改")
		return
	}
	t.UpdatedAt = time.Now().Unix()
	t.ID = id
	if creating {
		t.ID = arthasID()
	}
	sealed := ""
	if in.Credential != nil {
		var err error
		sealed, err = m.st.sealArthasCredential(&t, in.Credential)
		if err != nil {
			arthasError(w, 500, "认证加密失败，请检查本机凭据密钥；未保存配置")
			return
		}
	}
	tx, e := m.st.db.Begin()
	if e != nil {
		arthasError(w, 500, "保存连接失败")
		return
	}
	defer tx.Rollback()
	action := "update-target"
	if creating {
		t.Revision = 1
		action = "create-target"
		_, e = tx.Exec(`INSERT INTO arthas_targets(id,name,service,environment,endpoint,revision,updated_at,deleted) VALUES(?,?,?,?,?,?,?,0)`, t.ID, t.Name, t.Service, t.Environment, t.Endpoint, t.Revision, t.UpdatedAt)
	} else {
		t.ID = id
		var result sql.Result
		result, e = tx.Exec(`UPDATE arthas_targets SET name=?,service=?,environment=?,endpoint=?,revision=revision+1,updated_at=? WHERE id=? AND revision=? AND deleted=0`, t.Name, t.Service, t.Environment, t.Endpoint, t.UpdatedAt, id, t.Revision)
		if e == nil {
			n, _ := result.RowsAffected()
			if n != 1 {
				arthasError(w, 409, "连接已变更或删除，请刷新后重新编辑")
				return
			}
			t.Revision++
		}
	}
	if e == nil {
		// Changing the destination must never forward a previous host's secret.
		if in.ClearCredential || previous != nil && (previous.Endpoint != t.Endpoint || previous.Service != t.Service || previous.Environment != t.Environment) || in.Credential != nil {
			_, e = tx.Exec(`DELETE FROM arthas_credentials WHERE target_id=?`, t.ID)
		}
		if e == nil && in.Credential != nil {
			_, e = tx.Exec(`INSERT INTO arthas_credentials(target_id,encrypted) VALUES(?,?)`, t.ID, sealed)
		}
	}
	if e == nil {
		_, e = tx.Exec(`INSERT INTO arthas_audit(id,actor,target_id,action,command_name,command_hash,state,at) VALUES(?,?,?,?,?,?,?,?)`, arthasID(), r.Header.Get("X-User"), t.ID, action, "", "", "success", t.UpdatedAt)
	}
	if e != nil || tx.Commit() != nil {
		arthasError(w, 500, "保存连接及审计失败")
		return
	}
	saved, err := m.st.arthasTarget(t.ID)
	if err != nil {
		arthasError(w, 500, "连接已保存，读取结果失败；请刷新核对")
		return
	}
	writeJSON(w, saved)
}
func (m *arthasConsole) deleteTarget(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.targetPending[r.PathValue("id")] > 0 {
		arthasError(w, 409, "此实例正在诊断，请结束后再移除")
		return
	}
	for _, s := range m.sessions {
		if s.target.ID == r.PathValue("id") {
			arthasError(w, 409, "此连接仍有活动会话，请先断开后再移除")
			return
		}
	}
	var body struct {
		Revision int64 `json:"revision"`
	}
	if !arthasDecode(w, r, &body) {
		return
	}
	tx, e := m.st.db.Begin()
	if e != nil {
		arthasError(w, 500, "移除连接失败")
		return
	}
	defer tx.Rollback()
	result, e := tx.Exec(`UPDATE arthas_targets SET deleted=1,revision=revision+1,updated_at=? WHERE id=? AND revision=? AND deleted=0`, time.Now().Unix(), r.PathValue("id"), body.Revision)
	if e != nil {
		arthasError(w, 500, "移除连接失败")
		return
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		arthasError(w, 409, "连接已变更，请刷新后重试")
		return
	}
	_, e = tx.Exec(`INSERT INTO arthas_audit(id,actor,target_id,action,command_name,command_hash,state,at) VALUES(?,?,?,?,?,?,?,?)`, arthasID(), r.Header.Get("X-User"), r.PathValue("id"), "remove-target", "", "", "success", time.Now().Unix())
	if e == nil {
		_, e = tx.Exec(`DELETE FROM arthas_credentials WHERE target_id=?`, r.PathValue("id"))
	}
	if e != nil || tx.Commit() != nil {
		arthasError(w, 500, "移除连接及审计失败")
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}
func (m *arthasConsole) connect(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TargetID string `json:"target_id"`
		Revision int64  `json:"revision"`
		Username string `json:"username"`
		Password string `json:"password"`
		UseSaved bool   `json:"use_saved_credential"`
	}
	if !arthasDecode(w, r, &in) {
		return
	}
	if len(in.Username) > 128 || len(in.Password) > 4096 || strings.ContainsAny(in.Username, ":\r\n\x00") {
		arthasError(w, 400, "认证参数无效")
		return
	}
	t, e := m.st.arthasTarget(in.TargetID)
	if e != nil || t.Revision != in.Revision {
		arthasError(w, 409, "连接已变更，请刷新后重新连接")
		return
	}
	if in.UseSaved {
		if in.Password != "" {
			arthasError(w, 400, "请选择保存认证或临时认证")
			return
		}
		credential, err := m.st.savedArthasCredential(t)
		if err != nil || credential == nil {
			arthasError(w, 409, "保存的认证不可用，请在配置连接中核对；不会尝试其他密码")
			return
		}
		in.Username, in.Password = credential.Username, credential.Password
	}
	actor := r.Header.Get("X-User")
	m.mu.Lock()
	count := m.pending[actor]
	pending := 0
	for _, n := range m.pending {
		pending += n
	}
	for _, s := range m.sessions {
		if s.owner == actor {
			count++
		}
	}
	if m.stopping || count >= 4 || len(m.sessions)+pending >= 20 {
		m.mu.Unlock()
		arthasError(w, 429, "当前诊断会话过多，请先断开不再使用的会话")
		return
	}
	m.pending[actor]++
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.pending[actor]--; m.mu.Unlock() }()
	if m.audit(actor, t.ID, "connect", "", "connecting") != nil {
		arthasError(w, 500, "无法记录诊断审计，未连接目标")
		return
	}
	if in.Username == "" {
		in.Username = "arthas"
	}
	s := &arthasSession{id: arthasID(), owner: actor, target: *t, username: in.Username, password: in.Password, created: time.Now(), touched: time.Now()}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	out, e := m.call(ctx, s, map[string]any{"action": "init_session"})
	if e != nil || out.State != "SUCCEEDED" || out.SessionID == "" || len(out.SessionID) > 512 || len(out.ConsumerID) > 512 {
		_ = m.audit(actor, t.ID, "connect", "", "failed")
		if e != nil {
			arthasError(w, 502, e.Error())
		} else {
			arthasError(w, 502, "Arthas 未建立会话，请核对 HTTP API 与认证")
		}
		return
	}
	s.remote = out.SessionID
	s.consumer = out.ConsumerID
	if r.Context().Err() != nil || m.audit(actor, t.ID, "connect", "", "connected") != nil {
		m.closeRemote(s)
		arthasError(w, 500, "连接结果未确认，已尝试关闭会话")
		return
	}
	m.mu.Lock()
	latest, targetErr := m.st.arthasTarget(t.ID)
	if m.stopping || targetErr != nil || latest.Revision != t.Revision {
		m.mu.Unlock()
		m.closeRemote(s)
		arthasError(w, 409, "连接期间配置已变化，已关闭会话，请重新核对")
		return
	}
	m.sessions[s.id] = s
	m.mu.Unlock()
	writeJSON(w, map[string]any{"id": s.id, "target": s.target, "expires_at": s.created.Add(15 * time.Minute).Unix()})
}
func (m *arthasConsole) session(w http.ResponseWriter, r *http.Request, checkRevision bool) *arthasSession {
	m.mu.Lock()
	s := m.sessions[r.PathValue("id")]
	m.mu.Unlock()
	if s == nil || s.owner != r.Header.Get("X-User") {
		arthasError(w, 404, "当前窗口的诊断会话不存在或已过期，请重新连接")
		return nil
	}
	s.mu.Lock()
	expired := s.closed || time.Since(s.created) > 15*time.Minute || !s.busy && time.Since(s.touched) > 5*time.Minute
	if !expired {
		s.touched = time.Now()
	}
	s.mu.Unlock()
	if expired {
		arthasError(w, 410, "诊断会话已过期，请重新连接")
		return nil
	}
	if checkRevision {
		t, e := m.st.arthasTarget(s.target.ID)
		if e != nil || t.Revision != s.target.Revision {
			arthasError(w, 409, "诊断连接配置已变化，请断开并重新核对目标")
			return nil
		}
	}
	return s
}
func (m *arthasConsole) execute(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Command string `json:"command"`
		Confirm bool   `json:"confirm"`
	}
	if !arthasDecode(w, r, &in) {
		return
	}
	in.Command = strings.TrimSpace(in.Command)
	if in.Command == "" || len(in.Command) > 4096 || strings.ContainsAny(in.Command, "\r\n\x00") {
		arthasError(w, 400, "请输入一条不超过 4096 字节的 Arthas 命令，不支持多行批处理")
		return
	}
	safe := map[string]bool{"version": true, "jvm": true, "thread -n 5": true, "dashboard -n 1": true, "help": true}
	if !safe[in.Command] && !in.Confirm {
		arthasError(w, 400, "此命令可能读取敏感数据或修改 JVM，请明确确认目标后提交")
		return
	}
	s := m.session(w, r, true)
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.busy || s.uncertain {
		s.mu.Unlock()
		arthasError(w, 409, "上条命令正在执行或结果未确认，请先中断或断开后核对")
		return
	}
	s.busy = true
	s.interrupted = false
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.busy = false; s.touched = time.Now(); s.mu.Unlock() }()
	if m.audit(s.owner, s.target.ID, "execute", in.Command, "executing") != nil {
		arthasError(w, 500, "无法记录命令审计，未执行")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 13*time.Second)
	defer cancel()
	out, e := m.call(ctx, s, map[string]any{"action": "exec", "command": in.Command, "execTimeout": 10000, "requestId": arthasID()})
	if e != nil {
		s.mu.Lock()
		s.uncertain = true
		s.mu.Unlock()
		_ = m.audit(s.owner, s.target.ID, "execute", in.Command, "unknown")
		arthasError(w, 502, e.Error())
		return
	}
	state := "success"
	if out.State != "SUCCEEDED" {
		state = "failed"
	}
	if out.Body.TimeExpired {
		state = "timeout"
	}
	if out.State == "SUCCEEDED" && out.Body.JobStatus != "TERMINATED" {
		state = "unknown"
		s.mu.Lock()
		s.uncertain = true
		s.mu.Unlock()
	}
	for _, raw := range out.Body.Results {
		var status struct {
			Type string `json:"type"`
			Code int    `json:"statusCode"`
		}
		if state == "success" && json.Unmarshal(raw, &status) == nil && status.Type == "status" && status.Code != 0 {
			state = "failed"
		}
	}
	s.mu.Lock()
	if s.interrupted && state == "success" {
		state = "interrupted"
	}
	s.mu.Unlock()
	var results any
	rawResults, _ := json.Marshal(out.Body.Results)
	_ = json.Unmarshal(rawResults, &results)
	rendered, _ := json.MarshalIndent(redactArthasResult(results, s.password), "", "  ")
	output := string(rendered)
	if output == "null" {
		output = ""
	}
	if m.audit(s.owner, s.target.ID, "execute", in.Command, state) != nil {
		s.mu.Lock()
		s.uncertain = true
		s.mu.Unlock()
		arthasError(w, 500, "命令已发送但结果审计未保存，不要重复执行，请人工核对")
		return
	}
	writeJSON(w, map[string]any{"state": state, "output": output, "time_expired": out.Body.TimeExpired, "message": map[string]string{"success": "执行完成；此结果不代表应用持续健康", "failed": "Arthas 命令未成功，请查看输出", "timeout": "命令已到 10 秒执行上限并由 Arthas 中断，请缩小诊断范围", "unknown": "目标未确认命令终态，请先中断或断开后核对，不要重复提交", "interrupted": "命令已请求中断，请核对已返回输出；不回滚已发生的修改"}[state]})
}
func (m *arthasConsole) interrupt(w http.ResponseWriter, r *http.Request) {
	s := m.session(w, r, false)
	if s == nil {
		return
	}
	if m.audit(s.owner, s.target.ID, "interrupt", "", "requested") != nil {
		arthasError(w, 500, "无法记录中断审计，未发送")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	out, e := m.call(ctx, s, map[string]any{"action": "interrupt_job"})
	if e != nil || out.State != "SUCCEEDED" {
		arthasError(w, 502, "中断结果未确认，请核对目标；不会重试")
		return
	}
	s.mu.Lock()
	s.uncertain = false
	s.interrupted = true
	s.mu.Unlock()
	_ = m.audit(s.owner, s.target.ID, "interrupt", "", "interrupted")
	writeJSON(w, map[string]bool{"ok": true})
}
func (m *arthasConsole) disconnect(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	s := m.sessions[r.PathValue("id")]
	m.mu.Unlock()
	if s == nil {
		// Expired sessions have already been removed by the janitor. Let the
		// window discard its stale handle without claiming the JVM is stopped.
		writeJSON(w, map[string]bool{"ok": true, "remote_closed": false})
		return
	}
	if s.owner != r.Header.Get("X-User") {
		arthasError(w, 404, "当前窗口的诊断会话不存在或已过期")
		return
	}
	if m.audit(s.owner, s.target.ID, "disconnect", "", "closed") != nil {
		arthasError(w, 500, "无法记录断开审计，请重试")
		return
	}
	m.mu.Lock()
	delete(m.sessions, s.id)
	m.mu.Unlock()
	closed := m.closeRemote(s)
	writeJSON(w, map[string]bool{"ok": true, "remote_closed": closed})
}
func (m *arthasConsole) listAudit(w http.ResponseWriter, r *http.Request) {
	rows, e := m.st.db.Query(`SELECT id,actor,target_id,action,command_name,command_hash,state,at FROM arthas_audit ORDER BY at DESC,id DESC LIMIT 100`)
	if e != nil {
		arthasError(w, 500, "读取诊断审计失败")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var auditID, actor, id, action, name, hash, state string
		var at int64
		if rows.Scan(&auditID, &actor, &id, &action, &name, &hash, &state, &at) != nil {
			arthasError(w, 500, "读取诊断审计失败")
			return
		}
		out = append(out, map[string]any{"id": auditID, "actor": actor, "target_id": id, "action": action, "command_name": name, "command_hash": hash, "state": state, "at": at})
	}
	if rows.Err() != nil {
		arthasError(w, 500, "读取诊断审计失败")
		return
	}
	writeJSON(w, out)
}

// Store owns the lifecycle so tests and service shutdown do not leak janitors.
func (s *Store) arthasRoutes() http.Handler {
	s.arthasMu.Lock()
	defer s.arthasMu.Unlock()
	if s.arthas == nil {
		s.arthas = newArthasConsole(s)
	}
	return s.arthas.routes()
}

func redactArthasResult(value any, secret string) any {
	switch v := value.(type) {
	case string:
		if secret != "" {
			return strings.ReplaceAll(v, secret, "[REDACTED]")
		}
		return v
	case []any:
		for i := range v {
			v[i] = redactArthasResult(v[i], secret)
		}
		return v
	case map[string]any:
		out := map[string]any{}
		for key, item := range v {
			k := key
			if secret != "" {
				k = strings.ReplaceAll(k, secret, "[REDACTED]")
			}
			out[k] = redactArthasResult(item, secret)
		}
		return out
	}
	return value
}
