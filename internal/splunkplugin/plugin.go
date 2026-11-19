// Package splunkplugin implements a vendor adapter in an independent process.
// Neither platform nor the native log service import this package.
package splunkplugin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	p "logplatform/pkg/extensionprotocol"
)

const ID = "splunk-logs"
const Version = "1.0.0"

var fieldRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]{0,127}$`)
var indexRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
var sidRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,200}$`)

type Binding struct {
	Service     string            `json:"service"`
	Environment string            `json:"environment"`
	Index       string            `json:"index"`
	Sourcetype  string            `json:"sourcetype"`
	Filters     map[string]string `json:"filters,omitempty"`
}
type Config struct {
	Endpoint    string            `json:"endpoint"`
	Username    string            `json:"username"`
	Password    string            `json:"password"`
	Users       []string          `json:"users"`
	Bindings    []Binding         `json:"bindings"`
	Fields      map[string]string `json:"fields"`
	TLSInsecure string            `json:"tls-insecure"`
}
type page struct {
	SID, Hash, User string
	Offset          int
	Expires         time.Time
}
type Adapter struct {
	config  Config
	client  *http.Client
	mu      sync.Mutex
	session string
	cursors map[string]page
	root    context.Context
	cancel  context.CancelFunc
}

func fault(code, msg string) *p.Error { return &p.Error{Code: code, Message: msg} }
func New(config Config) (*Adapter, error) {
	u, err := url.Parse(config.Endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
		return nil, fault("invalid_argument", "请填写 Splunk REST API 根地址（通常 HTTPS 8089），不能填写 search 网页入口")
	}
	if config.Username == "" || config.Password == "" || len(config.Users) == 0 || len(config.Bindings) == 0 {
		return nil, fault("invalid_argument", "请配置账号、允许查询的用户和应用环境索引绑定")
	}
	seen := map[string]bool{}
	for _, b := range config.Bindings {
		k := b.Service + "\x00" + b.Environment
		if b.Service == "" || b.Environment == "" || !indexRE.MatchString(b.Index) || b.Sourcetype != "" && !fieldRE.MatchString(b.Sourcetype) || seen[k] {
			return nil, fault("invalid_argument", "应用、环境与索引绑定无效或重复")
		}
		if len(b.Filters) > 20 {
			return nil, fault("invalid_argument", "绑定过滤条件过多")
		}
		for field, value := range b.Filters {
			if !fieldRE.MatchString(field) || len(value) > 512 {
				return nil, fault("invalid_argument", "绑定过滤字段或值无效")
			}
		}
		seen[k] = true
	}
	if config.Fields == nil {
		config.Fields = map[string]string{}
	}
	for _, k := range []string{"host", "path", "message"} {
		if config.Fields[k] == "" {
			config.Fields[k] = map[string]string{"host": "host", "path": "source", "message": "_raw"}[k]
		}
	}
	for k, v := range config.Fields {
		if k != "host" && k != "path" && k != "message" && k != "level" && k != "trace_id" {
			return nil, fault("invalid_argument", "包含未知映射字段")
		}
		if !fieldRE.MatchString(v) {
			return nil, fault("invalid_argument", "映射字段名无效")
		}
	}
	if config.TLSInsecure != "" && config.TLSInsecure != "false" && config.TLSInsecure != "true" {
		return nil, fault("invalid_argument", "TLS 选项必须为 true 或 false")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: config.TLSInsecure == "true"}
	ctx, cancel := context.WithCancel(context.Background())
	a := &Adapter{config: config, client: &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, cursors: map[string]page{}, root: ctx, cancel: cancel}
	go a.reap()
	return a, nil
}
func (a *Adapter) request(ctx context.Context, method, path string, values url.Values, authenticated bool) ([]byte, error) {
	endpoint := strings.TrimRight(a.config.Endpoint, "/") + path
	var body io.Reader
	if method == "GET" {
		if len(values) > 0 {
			endpoint += "?" + values.Encode()
		}
	} else {
		body = strings.NewReader(values.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, fault("invalid_argument", "接口地址无效")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if authenticated {
		a.mu.Lock()
		session := a.session
		a.mu.Unlock()
		req.Header.Set("Authorization", "Splunk "+session)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fault("timeout", "查询超时或已取消")
		}
		return nil, fault("unavailable", "无法连接 Splunk REST API，请检查地址、网络及证书")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20+1))
	if err != nil || len(raw) > 2<<20 {
		return nil, fault("resource_exhausted", "Splunk 返回内容超过 2 MiB 限制，请缩小范围")
	}
	if resp.StatusCode == 401 {
		a.mu.Lock()
		a.session = ""
		a.mu.Unlock()
		return nil, fault("unauthenticated", "Splunk 认证失效，请检查账号配置后重新查询")
	}
	if resp.StatusCode == 403 {
		return nil, fault("permission_denied", "Splunk 账号无权执行查询")
	}
	if resp.StatusCode == 404 {
		return nil, fault("unavailable", "地址未提供所需 REST API，请勿使用 Splunk 搜索网页地址")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fault("unavailable", "Splunk 请求失败，请核对 REST API 与账号权限")
	}
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/html") {
		return nil, fault("invalid_argument", "该地址返回网页，请配置 REST API 地址")
	}
	return raw, nil
}
func (a *Adapter) login(ctx context.Context) error {
	a.mu.Lock()
	exists := a.session != ""
	a.mu.Unlock()
	if exists {
		return nil
	}
	raw, err := a.request(ctx, "POST", "/services/auth/login", url.Values{"username": {a.config.Username}, "password": {a.config.Password}, "output_mode": {"json"}}, false)
	if err != nil {
		return err
	}
	var login struct {
		SessionKey string `json:"sessionKey" xml:"sessionKey"`
	}
	if json.Unmarshal(raw, &login) != nil {
		xml.Unmarshal(raw, &login)
	}
	if login.SessionKey == "" {
		return fault("unauthenticated", "Splunk 未返回登录会话，请检查接口兼容性")
	}
	a.mu.Lock()
	a.session = login.SessionKey
	a.mu.Unlock()
	return nil
}
func (a *Adapter) Test(ctx context.Context) error {
	if err := a.login(ctx); err != nil {
		return err
	}
	raw, err := a.request(ctx, "GET", "/services/server/info", url.Values{"output_mode": {"json"}}, true)
	if err != nil {
		return err
	}
	var data struct {
		Entry []json.RawMessage `json:"entry"`
	}
	if json.Unmarshal(raw, &data) != nil || len(data.Entry) == 0 {
		return fault("unavailable", "REST API 返回格式不兼容")
	}
	return nil
}
func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`).Replace(s) + `"`
}
func (a *Adapter) build(q p.LogSearch, actor p.Actor) (string, error) {
	allowed := false
	for _, u := range a.config.Users {
		if u == actor.User {
			allowed = true
		}
	}
	if !allowed {
		return "", fault("permission_denied", "当前用户未授权使用此数据源")
	}
	var binding *Binding
	for i := range a.config.Bindings {
		b := &a.config.Bindings[i]
		if b.Service == q.Service && b.Environment == q.Environment {
			binding = b
			break
		}
	}
	if binding == nil {
		return "", fault("permission_denied", "此数据源未配置该应用与环境的索引绑定")
	}
	if q.Start <= 0 || q.End < q.Start || q.End-q.Start > 24*60*60*1000 || q.Limit < 1 || q.Limit > 200 || len(q.Text) > 1000 || len(q.Host) > 256 || len(q.Levels) > 6 {
		return "", fault("invalid_argument", "请选择应用、环境及不超过 24 小时的范围；每页 1–200 条")
	}
	query := "search index=" + quote(binding.Index)
	if binding.Sourcetype != "" {
		query += " sourcetype=" + quote(binding.Sourcetype)
	}
	terms := []string{}
	boundFields := []string{}
	for field := range binding.Filters {
		boundFields = append(boundFields, field)
	}
	sort.Strings(boundFields)
	for _, field := range boundFields {
		terms = append(terms, "coalesce('"+field+"',\"\")="+quote(binding.Filters[field]))
	}
	if q.Host != "" {
		terms = append(terms, "lower(coalesce('"+a.config.Fields["host"]+"',\"\"))=lower("+quote(q.Host)+")")
	}
	if len(q.Levels) > 0 {
		field := a.config.Fields["level"]
		if field == "" {
			return "", fault("unsupported", "该数据源未配置日志级别字段")
		}
		levels := []string{}
		for _, level := range q.Levels {
			switch level {
			case "ERROR", "WARN", "INFO", "DEBUG", "TRACE", "FATAL":
			default:
				return "", fault("invalid_argument", "日志级别无效")
			}
			levels = append(levels, "upper('"+field+"')="+quote(level))
		}
		terms = append(terms, "("+strings.Join(levels, " OR ")+")")
	}
	if q.Text != "" {
		terms = append(terms, "match(coalesce('"+a.config.Fields["message"]+"',\"\"),"+quote("(?i)"+regexp.QuoteMeta(q.Text))+")")
	}
	if len(terms) > 0 {
		query += " | where " + strings.Join(terms, " AND ")
	}
	fields := []string{"__lp_ts", "_cd"}
	for _, f := range a.config.Fields {
		fields = append(fields, f)
	}
	sort.Strings(fields)
	query += " | sort 1001 -_time -_cd | eval __lp_ts=round(_time*1000) | fields " + strings.Join(fields, ",")
	return query, nil
}
func digest(q p.LogSearch) string {
	q.Cursor = ""
	raw, _ := json.Marshal(q)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func token() string { var b [24]byte; rand.Read(b[:]); return hex.EncodeToString(b[:]) }
func (a *Adapter) cleanup(sid string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	a.request(ctx, "DELETE", "/services/search/jobs/"+sid, nil, true)
}
func (a *Adapter) Search(ctx context.Context, q p.LogSearch, actor p.Actor) (*p.LogResult, error) {
	query, err := a.build(q, actor)
	if err != nil {
		return nil, err
	}
	if err = a.login(ctx); err != nil {
		return nil, err
	}
	hash := digest(q)
	job := page{Hash: hash, User: actor.User, Expires: time.Now().Add(2 * time.Minute)}
	if q.Cursor != "" {
		a.mu.Lock()
		saved, ok := a.cursors[q.Cursor]
		if ok && saved.Hash == hash && saved.User == actor.User {
			delete(a.cursors, q.Cursor)
		}
		a.mu.Unlock()
		if !ok || saved.Hash != hash || saved.User != actor.User || time.Now().After(saved.Expires) {
			return nil, fault("cursor_expired", "分页已失效或查询条件已变化，请重新查询")
		}
		job = saved
	} else {
		raw, e := a.request(ctx, "POST", "/services/search/jobs", url.Values{"search": {query}, "earliest_time": {strconv.FormatFloat(float64(q.Start)/1000, 'f', 3, 64)}, "latest_time": {strconv.FormatFloat(float64(q.End)/1000, 'f', 3, 64)}, "output_mode": {"json"}, "exec_mode": {"normal"}, "max_time": {"15"}, "auto_cancel": {"60"}, "timeout": {"120"}}, true)
		if e != nil {
			return nil, e
		}
		var created struct {
			SID string `json:"sid" xml:"sid"`
		}
		if json.Unmarshal(raw, &created) != nil {
			xml.Unmarshal(raw, &created)
		}
		if !sidRE.MatchString(created.SID) {
			return nil, fault("internal", "Splunk 搜索任务响应格式异常")
		}
		job.SID = created.SID
	}
	keep := false
	defer func() {
		if !keep {
			a.cleanup(job.SID)
		}
	}()
	for {
		raw, e := a.request(ctx, "GET", "/services/search/jobs/"+job.SID, url.Values{"output_mode": {"json"}}, true)
		if e != nil {
			return nil, e
		}
		var status struct {
			Entry []struct {
				Content struct {
					IsDone        any    `json:"isDone"`
					IsFailed      any    `json:"isFailed"`
					IsFinalized   any    `json:"isFinalized"`
					DispatchState string `json:"dispatchState"`
				} `json:"content"`
			} `json:"entry"`
		}
		if json.Unmarshal(raw, &status) != nil || len(status.Entry) != 1 {
			return nil, fault("internal", "Splunk 任务状态格式异常")
		}
		s := status.Entry[0].Content
		if s.DispatchState == "FAILED" || truth(s.IsFailed) {
			return nil, fault("unavailable", "Splunk 搜索任务失败，请检查字段映射与权限")
		}
		if truth(s.IsFinalized) {
			return nil, fault("timeout", "Splunk 搜索提前结束，结果可能不完整，请缩小范围")
		}
		if truth(s.IsDone) {
			break
		}
		select {
		case <-ctx.Done():
			return nil, fault("timeout", "查询超时或已取消")
		case <-time.After(150 * time.Millisecond):
		}
	}
	raw, e := a.request(ctx, "GET", "/services/search/jobs/"+job.SID+"/results", url.Values{"output_mode": {"json"}, "count": {strconv.Itoa(q.Limit + 1)}, "offset": {strconv.Itoa(job.Offset)}}, true)
	if e != nil {
		return nil, e
	}
	var result struct {
		Results  []map[string]json.RawMessage `json:"results"`
		Messages []struct {
			Type string `json:"type"`
		} `json:"messages"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Results == nil {
		return nil, fault("internal", "Splunk 查询结果格式异常")
	}
	for _, msg := range result.Messages {
		if msg.Type == "ERROR" || msg.Type == "WARN" {
			return nil, fault("unavailable", "Splunk 返回查询警告，不能确认结果完整，请缩小范围或检查权限")
		}
	}
	out := &p.LogResult{Hits: []p.LogHit{}, Complete: true}
	more := len(result.Results) > q.Limit
	rows := result.Results
	if more {
		rows = rows[:q.Limit]
	}
	for _, r := range rows {
		if job.Offset+len(out.Hits) >= 1000 {
			out.Truncated = true
			break
		}
		ts, e := strconv.ParseFloat(value(r["__lp_ts"]), 64)
		if e != nil || ts <= 0 {
			return nil, fault("internal", "日志时间字段无效")
		}
		hit := p.LogHit{Ts: int64(ts), Service: q.Service, Environment: q.Environment, EventID: value(r["_cd"]), Host: value(r[a.config.Fields["host"]]), Path: value(r[a.config.Fields["path"]]), Level: strings.ToUpper(value(r[a.config.Fields["level"]])), TraceID: value(r[a.config.Fields["trace_id"]]), Message: value(r[a.config.Fields["message"]])}
		if len(hit.Message) > 65536 {
			return nil, fault("resource_exhausted", "日志单条超过 64 KiB，请缩小范围")
		}
		out.Hits = append(out.Hits, hit)
	}
	job.Offset += len(out.Hits)
	if job.Offset >= 1000 && more {
		out.Truncated = true
	}
	out.HasMore = more && !out.Truncated
	out.Complete = !out.Truncated
	if out.HasMore {
		a.mu.Lock()
		if len(a.cursors) >= 100 {
			a.mu.Unlock()
			return nil, fault("resource_exhausted", "未完成的分页过多，请稍后重试")
		}
		out.NextCursor = token()
		a.cursors[out.NextCursor] = job
		a.mu.Unlock()
		keep = true
	}
	return out, nil
}
func truth(v any) bool { return v == true || v == float64(1) || v == "1" || v == "true" }
func value(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}
func (a *Adapter) reap() {
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-a.root.Done():
			return
		case now := <-tick.C:
			a.mu.Lock()
			expired := []string{}
			for key, p := range a.cursors {
				if now.After(p.Expires) {
					expired = append(expired, p.SID)
					delete(a.cursors, key)
				}
			}
			a.mu.Unlock()
			for _, sid := range expired {
				a.cleanup(sid)
			}
		}
	}
}
func (a *Adapter) Close() {
	a.cancel()
	a.mu.Lock()
	ids := []string{}
	for _, job := range a.cursors {
		ids = append(ids, job.SID)
	}
	a.cursors = map[string]page{}
	a.mu.Unlock()
	for _, sid := range ids {
		a.cleanup(sid)
	}
	a.client.CloseIdleConnections()
}
func Handler(a *Adapter, token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		write := func(v any) { json.NewEncoder(w).Encode(v) }
		if r.Method == "GET" && r.URL.Path == "/v1/describe" {
			write(p.Description{Protocol: p.Major, ID: ID, Version: Version, Capabilities: []string{p.LogsSearch}})
			return
		}
		if r.Method == "GET" && r.URL.Path == "/v1/health" {
			write(map[string]bool{"ok": true})
			return
		}
		if r.Method != "POST" {
			http.NotFound(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 18*time.Second)
		defer cancel()
		var data any
		var err error
		switch r.URL.Path {
		case "/v1/test":
			err = a.Test(ctx)
			data = map[string]bool{"ok": err == nil}
		case "/v1/invoke":
			var req p.Request
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req) != nil {
				err = fault("invalid_argument", "请求格式无效")
			} else if req.Capability != p.LogsSearch || req.Operation != "search" {
				err = fault("unsupported", "不支持此能力或操作")
			} else {
				var q p.LogSearch
				if json.Unmarshal(req.Input, &q) != nil {
					err = fault("invalid_argument", "查询格式无效")
				} else {
					data, err = a.Search(ctx, q, req.Actor)
				}
			}
		default:
			http.NotFound(w, r)
			return
		}
		if err != nil {
			e, ok := err.(*p.Error)
			if !ok {
				e = fault("internal", "插件内部错误")
			}
			write(p.Response{Error: e})
			return
		}
		raw, _ := json.Marshal(data)
		write(p.Response{Data: raw})
	})
}

// Configuration failures are available through the startup handshake, without
// printing credentials or requiring a working connection to the remote server.
func ConfigurationErrorHandler(token string, initial error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" && r.URL.Path == "/v1/describe" {
			json.NewEncoder(w).Encode(p.Description{Protocol: p.Major, ID: ID, Version: Version, Capabilities: []string{p.LogsSearch}})
			return
		}
		e, ok := initial.(*p.Error)
		if !ok {
			e = fault("invalid_argument", "配置字段类型与插件要求不一致，请核对 JSON 数组和对象")
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": e})
	})
}
