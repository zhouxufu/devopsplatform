package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
)

func integrationSchema(identity, doc string) []string {
	return []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS data_sync_tasks (id %s, document %s NOT NULL, owner VARCHAR(191) NOT NULL, revision BIGINT NOT NULL)`, identity, doc),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS release_version_scope (release_version_id BIGINT PRIMARY KEY, document %s NOT NULL)`, doc),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS change_payloads (change_id BIGINT PRIMARY KEY, document %s NOT NULL)`, doc),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS issue_sync_state (connection VARCHAR(191) PRIMARY KEY, last_at BIGINT NOT NULL, status VARCHAR(32) NOT NULL, detail %s NOT NULL)`, doc),
		`CREATE TABLE IF NOT EXISTS external_work_items (connection VARCHAR(191) NOT NULL, kind VARCHAR(32) NOT NULL, external_id VARCHAR(191) NOT NULL, work_item_id BIGINT NOT NULL, PRIMARY KEY(connection,kind,external_id))`,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS sql_execution_ledger (connection VARCHAR(191) NOT NULL, database_name VARCHAR(191) NOT NULL, change_id BIGINT NOT NULL, digest VARCHAR(80) NOT NULL, status VARCHAR(32) NOT NULL, run_id BIGINT NOT NULL, node_id VARCHAR(191) NOT NULL, detail %s NOT NULL, PRIMARY KEY(connection,database_name,change_id))`, doc),
	}
}

func validateIntegrationConnection(c *ResourceConnection) error {
	if c.Kind == "mysql" {
		host, port, err := net.SplitHostPort(c.Endpoint)
		portNumber, portErr := strconv.Atoi(port)
		if err != nil || strings.TrimSpace(host) == "" || strings.ContainsAny(host, " /@?#") || portErr != nil || portNumber < 1 || portNumber > 65535 {
			return fmt.Errorf("MySQL 地址请填写 host:port，例如 192.168.0.32:3306；不要填写带密码的 DSN")
		}
		if !sqlIdentifierRE.MatchString(c.Config["database"]) {
			return fmt.Errorf("MySQL 必须指定数据库，名称仅支持字母、数字、下划线")
		}
		if tls := c.Config["tls"]; tls != "" && tls != "true" && tls != "false" {
			return fmt.Errorf("MySQL TLS 仅支持 true（校验证书）或 false（内网明文）")
		}
	}
	if c.Kind == "zentao" {
		u, err := url.Parse(c.Endpoint)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("禅道地址必须为 HTTP(S) 安装根地址，包含实际子路径，不包含账号、查询参数或 #")
		}
		if c.Config["api_mode"] != "" && c.Config["api_mode"] != "legacy" {
			return fmt.Errorf("本版适配禅道开源 16.0，API 模式为 legacy")
		}
		c.Config["api_mode"] = "legacy"
		if minutes := c.Config["sync_minutes"]; minutes != "" {
			n, err := strconv.Atoi(minutes)
			if err != nil || n < 0 || n > 1440 {
				return fmt.Errorf("同步周期为 0（关闭）或 1–1440 分钟")
			}
		}
		ids, err := zentaoProductIDs(c)
		if strings.TrimSpace(c.Config["product_ids"]) != "" && err != nil {
			return fmt.Errorf("请填写产品 ID，多个用逗号分隔，最多 20 个")
		}
		if n, _ := strconv.Atoi(c.Config["sync_minutes"]); n > 0 && len(ids) == 0 {
			return fmt.Errorf("自动同步 Bug 需要填写产品 ID；仅同步用户时可留空并关闭自动同步")
		}
		if baseline := c.Config["baseline"]; baseline != "" {
			if _, err := time.Parse("2006-01-02", baseline); err != nil {
				return fmt.Errorf("日期基线格式为 YYYY-MM-DD")
			}
		}
	}
	return nil
}

func openChangeMySQL(ctx context.Context, c *ResourceConnection) (*sql.DB, error) {
	if c == nil || c.Kind != "mysql" {
		return nil, fmt.Errorf("MySQL 连接不存在")
	}
	if err := validateIntegrationConnection(c); err != nil {
		return nil, err
	}
	cfg := mysql.NewConfig()
	cfg.User, cfg.Passwd, cfg.Net, cfg.Addr, cfg.DBName = c.Username, c.Password, "tcp", c.Endpoint, c.Config["database"]
	cfg.Timeout, cfg.ReadTimeout, cfg.WriteTimeout = 10*time.Second, 30*time.Second, 30*time.Second
	cfg.MultiStatements, cfg.AllowAllFiles = false, false
	cfg.Params = map[string]string{"charset": "utf8mb4", "sql_mode": "'STRICT_ALL_TABLES'"}
	if c.Config["tls"] == "true" {
		cfg.TLSConfig = "true"
	}
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("MySQL 连接配置无效")
	}
	db.SetMaxOpenConns(1)
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("MySQL 连接或认证失败：%w", err)
	}
	return db, nil
}

func testIntegrationConnection(ctx context.Context, c *ResourceConnection) error {
	if c.Kind == "mysql" {
		db, err := openChangeMySQL(ctx, c)
		if err != nil {
			return err
		}
		defer db.Close()
		_, err = readSQLCatalog(ctx, db, c.Config["database"])
		return err
	}
	client, err := newZentaoLegacy(ctx, c)
	if err != nil {
		return err
	}
	// User-directory-only connections do not need a Bug product scope.
	if strings.TrimSpace(c.Config["product_ids"]) == "" {
		return nil
	}
	ids, err := zentaoProductIDs(c)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = client.bugs(ctx, id, 1); err != nil {
			return err
		}
	}
	return nil
}

// The 16.0 provider uses documented legacy JSON sessions, not REST v1 tokens.
// Cookies and passwords are never included in URLs, logs or the public config.
type zentaoLegacy struct {
	root     string
	client   *http.Client
	pathInfo bool
}

// Only the anonymous, read-only session request may probe another route.
// Never retry login or permission failures with the password on another URL.
type zentaoRouteError struct{ message string }

func (e *zentaoRouteError) Error() string { return e.message }

func newZentaoLegacy(ctx context.Context, c *ResourceConnection) (*zentaoLegacy, error) {
	if err := validateIntegrationConnection(c); err != nil {
		return nil, err
	}
	if c.Username == "" || c.Password == "" {
		return nil, fmt.Errorf("请配置禅道专用同步账号和密码")
	}
	jar, _ := cookiejar.New(nil)
	z := &zentaoLegacy{root: strings.TrimRight(c.Endpoint, "/"), client: &http.Client{Jar: jar, Timeout: 15 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
	var session struct {
		SessionName string `json:"sessionName"`
		SessionID   string `json:"sessionID"`
	}
	err := z.call(ctx, "api", "getSessionID", nil, nil, &session)
	var routeErr *zentaoRouteError
	if errors.As(err, &routeErr) || (err == nil && (session.SessionName == "" || session.SessionID == "")) {
		z.pathInfo = true
		err = z.call(ctx, "api", "getSessionID", nil, nil, &session)
	}
	if err != nil {
		return nil, fmt.Errorf("获取禅道会话失败（已探测兼容路由）：%w", err)
	}
	if session.SessionName == "" || session.SessionID == "" {
		return nil, fmt.Errorf("禅道未返回 Session，请确认安装根地址与 legacy JSON 接口")
	}
	u, _ := url.Parse(z.root + "/index.php")
	jar.SetCookies(u, []*http.Cookie{{Name: session.SessionName, Value: session.SessionID, Path: "/"}})
	var logged struct {
		User struct {
			Account string `json:"account"`
		} `json:"user"`
	}
	if err := z.call(ctx, "user", "login", nil, url.Values{"account": {c.Username}, "password": {c.Password}}, &logged); err != nil {
		return nil, fmt.Errorf("禅道登录失败：%w", err)
	}
	if logged.User.Account != strings.TrimSpace(c.Username) {
		if logged.User.Account == "" {
			return nil, fmt.Errorf("禅道登录未返回认证结果（可能仍是登录表单），请检查代理是否正确转发 POST 与同源 Referer，以及禅道 CSRF 设置")
		}
		return nil, fmt.Errorf("禅道登录未返回匹配的已认证账号，请检查用户名、密码及账号状态")
	}
	return z, nil
}

func (z *zentaoLegacy) requestURL(module, method string, args url.Values) (string, error) {
	if !z.pathInfo {
		q := url.Values{"m": {module}, "f": {method}, "t": {"json"}}
		for k, v := range args {
			q[k] = v
		}
		return z.root + "/index.php?" + q.Encode(), nil
	}
	// PATHINFO parameters are positional, not named query parameters. Defaults
	// and order match the official zentaopms_16.0 controller signatures.
	var keys, defaults []string
	switch module + "/" + method {
	case "api/getSessionID", "user/login":
	case "bug/browse":
		keys = []string{"productID", "branch", "browseType", "param", "orderBy", "recTotal", "recPerPage", "pageID"}
		defaults = []string{"0", "0", "all", "0", "id_asc", "0", "100", "1"}
	case "company/browse":
		keys = []string{"browseType", "param", "type", "orderBy", "recTotal", "recPerPage", "pageID"}
		defaults = []string{"inside", "0", "bydept", "id_asc", "0", "100", "1"}
	default:
		return "", fmt.Errorf("不支持的禅道 PATHINFO 接口")
	}
	allowed := map[string]bool{}
	parts := []string{strings.ToLower(module), strings.ToLower(method)}
	for i, key := range keys {
		allowed[key] = true
		value := defaults[i]
		if args.Has(key) {
			value = args.Get(key)
		}
		if value == "" {
			return "", fmt.Errorf("禅道 PATHINFO 参数不能为空")
		}
		for _, r := range value {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_') {
				return "", fmt.Errorf("禅道 PATHINFO 参数格式无效")
			}
		}
		parts = append(parts, value)
	}
	for key := range args {
		if !allowed[key] {
			return "", fmt.Errorf("不支持的禅道 PATHINFO 参数")
		}
	}
	return z.root + "/" + strings.Join(parts, "-") + ".json", nil
}

func (z *zentaoLegacy) bugURL(id string) string {
	if z.pathInfo {
		return z.root + "/bug-view-" + url.PathEscape(id) + ".html"
	}
	return z.root + "/index.php?m=bug&f=view&bugID=" + url.QueryEscape(id)
}

func (z *zentaoLegacy) call(ctx context.Context, module, method string, args, form url.Values, out any) error {
	address, err := z.requestURL(module, method, args)
	if err != nil {
		return err
	}
	httpMethod := http.MethodGet
	var body io.Reader
	if form != nil {
		httpMethod = http.MethodPost
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, httpMethod, address, body)
	if err != nil {
		return fmt.Errorf("禅道请求地址无效")
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		// 16.0 filterCSRF clears POST without a same-origin Referer. Keep the
		// server protection enabled and never put the session/password in URLs.
		req.Header.Set("Referer", z.root+"/")
	}
	resp, err := z.client.Do(req)
	if err != nil {
		return fmt.Errorf("无法连接禅道服务")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == 404 || resp.StatusCode == 405 || (resp.StatusCode >= 300 && resp.StatusCode < 400) {
			return &zentaoRouteError{fmt.Sprintf("禅道 %s/%s 返回 HTTP %d，请检查安装路径或路由配置", module, method, resp.StatusCode)}
		}
		return fmt.Errorf("禅道接口返回 HTTP %d，请检查地址、权限或代理", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20+1))
	if err != nil || len(raw) > 4<<20 {
		return fmt.Errorf("禅道响应过大或读取失败")
	}
	var envelope struct {
		Status string          `json:"status"`
		Data   json.RawMessage `json:"data"`
		User   json.RawMessage `json:"user"`
	}
	raw = []byte(strings.TrimPrefix(strings.TrimSpace(string(raw)), "\ufeff"))
	if json.Unmarshal(raw, &envelope) != nil {
		return &zentaoRouteError{fmt.Sprintf("禅道 %s/%s 未返回 JSON（可能是登录页、代理拦截页或路由不匹配）", module, method)}
	}
	if envelope.Status != "success" {
		if module == "user" && method == "login" {
			return fmt.Errorf("账号认证未通过，请检查用户名、密码、账号锁定或禁用状态；未自动重试登录")
		}
		return fmt.Errorf("禅道接口拒绝请求，请检查账号权限和会话")
	}
	data := envelope.Data
	// ZenTao 16.0 user/login responds with {status:success,user:{...}},
	// whereas display() endpoints usually put JSON in the data envelope.
	if module == "user" && method == "login" && len(envelope.User) > 0 {
		data = raw
	}
	if len(data) > 0 && data[0] == '"' {
		var nested string
		if json.Unmarshal(data, &nested) != nil {
			return fmt.Errorf("禅道响应解析失败")
		}
		data = []byte(nested)
	}
	if len(data) == 0 || string(data) == "null" {
		data = []byte("{}")
	}
	if err = json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("禅道接口数据格式不兼容，请检查 16.0 JSON 接口")
	}
	return nil
}

type zentaoBug struct {
	ID             json.Number `json:"id"`
	Title          string      `json:"title"`
	Status         string      `json:"status"`
	Steps          string      `json:"steps"`
	LastEditedDate string      `json:"lastEditedDate"`
	ClosedDate     string      `json:"closedDate"`
}
type zentaoBugPage struct {
	Bugs json.RawMessage `json:"bugs"`
}

func (z *zentaoLegacy) bugs(ctx context.Context, product, page int) ([]zentaoBug, error) {
	var v zentaoBugPage
	err := z.call(ctx, "bug", "browse", url.Values{"productID": {strconv.Itoa(product)}, "browseType": {"all"}, "recPerPage": {"100"}, "pageID": {strconv.Itoa(page)}}, nil, &v)
	if err != nil {
		return nil, fmt.Errorf("读取禅道产品 %d 的 Bug 列表失败（请检查产品访问及 Bug 列表权限）：%w", product, err)
	}
	if len(v.Bugs) == 0 || string(v.Bugs) == "null" {
		return nil, fmt.Errorf("禅道 Bug 列表缺少 bugs 字段，请检查权限或接口参数")
	}
	decode := func(raw json.RawMessage, out any) error {
		d := json.NewDecoder(strings.NewReader(string(raw)))
		d.UseNumber()
		return d.Decode(out)
	}
	var rows []zentaoBug
	if decode(v.Bugs, &rows) != nil {
		var indexed map[string]zentaoBug
		if decode(v.Bugs, &indexed) != nil {
			return nil, fmt.Errorf("禅道 Bug 列表格式不兼容")
		}
		for _, bug := range indexed {
			rows = append(rows, bug)
		}
	}
	return rows, nil
}

func zentaoProductIDs(c *ResourceConnection) ([]int, error) {
	ids := []int{}
	seen := map[int]bool{}
	for _, raw := range strings.Split(c.Config["product_ids"], ",") {
		id, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || id < 1 {
			return nil, fmt.Errorf("产品 ID 必须为正整数")
		}
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	if len(ids) > 20 {
		return nil, fmt.Errorf("最多同步 20 个产品")
	}
	return ids, nil
}

type IssueSyncState struct {
	Connection string `json:"connection"`
	LastAt     int64  `json:"last_at"`
	Status     string `json:"status"`
	Detail     string `json:"detail"`
}

var issueSyncLocks sync.Map

func (s *Store) issueSyncStates() []IssueSyncState {
	out := []IssueSyncState{}
	rows, err := s.db.Query(`SELECT connection,last_at,status,detail FROM issue_sync_state ORDER BY connection`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var v IssueSyncState
		if rows.Scan(&v.Connection, &v.LastAt, &v.Status, &v.Detail) == nil {
			out = append(out, v)
		}
	}
	return out
}

func (s *Store) syncZentao(ctx context.Context, name, baseline, actor string) (state IssueSyncState, err error) {
	key := s.dataDir + "\x00" + name
	if _, loaded := issueSyncLocks.LoadOrStore(key, true); loaded {
		return state, fmt.Errorf("该禅道连接正在同步")
	}
	defer issueSyncLocks.Delete(key)
	c := s.GetConnection(name)
	if c == nil || c.Kind != "zentao" {
		return state, fmt.Errorf("禅道连接不存在")
	}
	if baseline == "" {
		baseline = c.Config["baseline"]
	}
	if baseline != "" {
		if _, err = time.Parse("2006-01-02", baseline); err != nil {
			return state, fmt.Errorf("日期基线格式为 YYYY-MM-DD")
		}
	}
	state = IssueSyncState{Connection: name, LastAt: time.Now().Unix(), Status: "failed"}
	defer func() {
		if err != nil {
			state.Detail = err.Error()
		}
		_, saveErr := s.db.Exec(`INSERT INTO issue_sync_state(connection,last_at,status,detail) VALUES(?,?,?,?) ON CONFLICT(connection) DO UPDATE SET last_at=excluded.last_at,status=excluded.status,detail=excluded.detail`, state.Connection, state.LastAt, state.Status, state.Detail)
		if err == nil {
			err = saveErr
		}
		s.Audit(actor, "sync", "zentao", name, map[string]any{"status": state.Status, "detail": state.Detail})
	}()
	z, err := newZentaoLegacy(ctx, c)
	if err != nil {
		return state, err
	}
	ids, err := zentaoProductIDs(c)
	if err != nil {
		return state, fmt.Errorf("同步 Bug 前请在禅道连接中填写有效产品 ID")
	}
	// Fetch first, then persist in one transaction: a failed page cannot advance
	// the successful sync baseline or leave a partly refreshed issue pool.
	bugs := map[string]zentaoBug{}
	for _, product := range ids {
		seen := map[string]bool{}
		for page := 1; page <= 100; page++ {
			rows, e := z.bugs(ctx, product, page)
			if e != nil {
				return state, e
			}
			for _, bug := range rows {
				id := string(bug.ID)
				if _, e := strconv.ParseInt(id, 10, 64); e != nil || id == "" || bug.Title == "" {
					return state, fmt.Errorf("禅道返回无效 Bug ID 或标题")
				}
				if seen[id] {
					return state, fmt.Errorf("禅道分页重复，请检查 pageID 参数；未保存本次结果")
				}
				seen[id] = true
				// Refresh previously imported issues even if their status changes.
				if baseline == "" || bug.LastEditedDate >= baseline || bug.ClosedDate >= baseline {
					bugs[id] = bug
				}
			}
			if len(rows) < 100 {
				break
			}
			if page == 100 {
				return state, fmt.Errorf("同步超过 10000 条/产品，请缩小产品范围")
			}
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return state, err
	}
	defer tx.Rollback()
	for id, bug := range bugs {
		var itemID int64
		e := tx.QueryRow(`SELECT work_item_id FROM external_work_items WHERE connection=? AND kind='bug' AND external_id=?`, name, id).Scan(&itemID)
		if e != nil && e != sql.ErrNoRows {
			return state, e
		}
		status := WorkItemOpen
		if bug.Status == "closed" {
			status = WorkItemClosed
		}
		link := z.bugURL(id)
		if e == sql.ErrNoRows {
			r, e := tx.Exec(`INSERT INTO work_items(source,external_id,kind,title,url,external_status,description,status,owner,created_at,updated_at) VALUES(?,?,'bug',?,?,?,?,?,?,?,?)`, "zentao:"+name, id, bug.Title, link, bug.Status, bug.Steps, status, actor, state.LastAt, state.LastAt)
			if e != nil {
				return state, e
			}
			itemID, _ = r.LastInsertId()
			if _, e = tx.Exec(`INSERT INTO external_work_items(connection,kind,external_id,work_item_id) VALUES(?,'bug',?,?)`, name, id, itemID); e != nil {
				return state, e
			}
		} else {
			if _, e = tx.Exec(`UPDATE work_items SET title=?,url=?,external_status=?,description=?,status=?,updated_at=? WHERE id=?`, bug.Title, link, bug.Status, bug.Steps, status, state.LastAt, itemID); e != nil {
				return state, e
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return state, err
	}
	state.Status, state.Detail = "success", fmt.Sprintf("已更新 %d 个 Bug；关闭状态与上线状态分别管理", len(bugs))
	return state, nil
}

func registerIntegrationRoutes(mux *http.ServeMux, s *Store) {
	registerDataSyncRoutes(mux, s)
	registerIssueUserRoutes(mux, s)
	mux.HandleFunc("GET /api/sql-executions", func(w http.ResponseWriter, r *http.Request) { writeIntegrationJSON(w, s.SQLLedger(), nil) })
	mux.HandleFunc("GET /api/issue-sync", func(w http.ResponseWriter, r *http.Request) { writeIntegrationJSON(w, s.issueSyncStates(), nil) })
	mux.HandleFunc("POST /api/issue-sync/{name}", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Baseline string `json:"baseline"`
		}
		if e := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); e != nil {
			writeIntegrationJSON(w, nil, fmt.Errorf("同步参数无效"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		actor := strings.TrimSpace(r.Header.Get("X-User"))
		if actor == "" {
			actor = "local"
		}
		state, err := s.syncZentao(ctx, r.PathValue("name"), req.Baseline, actor)
		writeIntegrationJSON(w, state, err)
	})
	mux.HandleFunc("POST /api/changes/{id}/sql-preview", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Environment string `json:"environment"`
		}
		if e := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); e != nil {
			writeIntegrationJSON(w, nil, fmt.Errorf("预检参数无效"))
			return
		}
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		plan, err := s.prepareSQLNode(ctx, "preview", []int64{id}, req.Environment)
		writeIntegrationJSON(w, plan, err)
	})
}
func writeIntegrationJSON(w http.ResponseWriter, value any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(value)
}

// Scheduled sync is read-only externally. Failed syncs retain the old issue pool.
func (s *Store) syncDueIssues(now time.Time) {
	last := map[string]int64{}
	for _, v := range s.issueSyncStates() {
		last[v.Connection] = v.LastAt
	}
	for _, public := range s.Connections() {
		minutes, _ := strconv.Atoi(public.Config["sync_minutes"])
		if public.Kind != "zentao" || minutes < 1 || now.Unix()-last[public.Name] < int64(minutes*60) {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		_, _ = s.syncZentao(ctx, public.Name, "", "scheduler")
		cancel()
	}
}
