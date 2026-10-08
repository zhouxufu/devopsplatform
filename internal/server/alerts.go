package server

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// AlertRule 订阅报警规则（由监控插件合并而来）。
// 两种评估路径：keyword 为空 → 按级别分钟计数；非空 → 使用与搜索、Live 相同的日志查询语法计数。
// 收敛三板斧：同周期多规则合并一条通知（digest）、依赖抑制（suppress_by）、手动静默（silence_until）。
type AlertRule struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Service      string `json:"service"`
	Environment  string `json:"environment,omitempty"`
	Level        string `json:"level"`
	Keyword      string `json:"keyword"`
	Threshold    int64  `json:"threshold"`
	DurationMin  int    `json:"duration_min"`
	Webhook      string `json:"webhook"`
	SilenceSec   int    `json:"silence_sec"`
	SuppressBy   string `json:"suppress_by"`   // 依赖抑制：这些规则报警中时本规则不通知（逗号分隔规则名）
	SilenceUntil int64  `json:"silence_until"` // 手动维护静默至（ms），期间不通知
	Enabled      bool   `json:"enabled"`
}

type AlertStatus struct {
	ID           int64          `json:"id"`
	Firing       bool           `json:"firing"`
	LastEval     string         `json:"last_eval,omitempty"`
	LastFire     string         `json:"last_fire,omitempty"`
	Sent         int            `json:"sent"`
	Suppressed   bool           `json:"suppressed"`
	SuppressedBy string         `json:"suppressed_by,omitempty"`
	Silenced     bool           `json:"silenced"`
	SilenceUntil string         `json:"silence_until,omitempty"`
	Recent       map[string]int `json:"recent,omitempty"`
}

type alertRT struct {
	firing             bool
	windowStart        time.Time
	lastEval, lastFire time.Time
	sent, sentHour     int
	hourStart          time.Time
	limited            bool
	suppressedBy       string
	recent             map[string]int
}

const alertHourlyLimit = 20

// AlertNotification 通知历史（含抑制/限流也记录，便于复盘"为什么没收到"）
type AlertNotification struct {
	ID          int64  `json:"id"`
	RuleID      int64  `json:"rule_id"`
	Rule        string `json:"rule"`
	Environment string `json:"environment"`
	Kind        string `json:"kind"` // 触发/持续中/已恢复/汇总/测试/被抑制/限流/静默
	At          int64  `json:"at"`
	Detail      string `json:"detail"`
}

// AlertEngine 阈值评估 + 收敛 + 通知出口。评估直接查本进程 Storage（无 HTTP 自调）。
type AlertEngine struct {
	st   *Storage
	db   *sql.DB
	http *http.Client
	mu   sync.Mutex
	// rules 缓存（评估周期内与 CRUD 共享一份快照）
	rules []*AlertRule
	state map[int64]*alertRT
}

func NewAlertEngine(st *Storage) *AlertEngine {
	e := &AlertEngine{st: st, db: st.db,
		http: &http.Client{Timeout: 8 * time.Second}, state: map[int64]*alertRT{}}
	e.ensureTables()
	e.migrateLegacy()
	e.reload()
	go e.Run(make(chan struct{}))
	return e
}

func (e *AlertEngine) ensureTables() {
	e.db.Exec(`CREATE TABLE IF NOT EXISTS alert_rules (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL, service TEXT NOT NULL, environment TEXT DEFAULT '', level TEXT NOT NULL,
		keyword TEXT DEFAULT '', threshold INTEGER NOT NULL, duration_min INTEGER NOT NULL,
		webhook TEXT NOT NULL, silence_sec INTEGER DEFAULT 300,
		suppress_by TEXT DEFAULT '', silence_until INTEGER DEFAULT 0,
		enabled INTEGER DEFAULT 1)`)
	e.db.Exec(`ALTER TABLE alert_rules ADD COLUMN environment TEXT DEFAULT ''`)
	// 旧版用空串隐式表示全环境；升级后改为可见、可审计的显式通配作用域。
	e.db.Exec(`UPDATE alert_rules SET environment='*' WHERE TRIM(COALESCE(environment,''))=''`)
	e.db.Exec(`CREATE TABLE IF NOT EXISTS alert_notifications (
		id INTEGER PRIMARY KEY AUTOINCREMENT, rule_id INTEGER, rule TEXT, environment TEXT NOT NULL DEFAULT '*', kind TEXT, at INTEGER, detail TEXT)`)
	e.db.Exec(`ALTER TABLE alert_notifications ADD COLUMN environment TEXT NOT NULL DEFAULT '*'`)
	e.db.Exec(`DELETE FROM alert_notifications WHERE at < ?`, time.Now().Add(-7*24*time.Hour).Unix())
}

// migrateLegacy 存量迁移（按规则名去重，幂等）：
// 1) 旧订阅表 subscriptions（即时命中语义 ≈ 连续1分钟≥1条）
// 2) 监控插件的 alert_rules（本功能整体搬迁的来源）
func (e *AlertEngine) migrateLegacy() {
	e.db.Exec(`INSERT INTO alert_rules(name,service,environment,level,keyword,threshold,duration_min,webhook,silence_sec,suppress_by,silence_until,enabled)
		SELECT name,COALESCE(service,''),'*',COALESCE(level,'ERROR'),COALESCE(keyword,''),1,1,webhook,COALESCE(silence_sec,300),'',0,enabled
		FROM subscriptions WHERE name NOT IN (SELECT name FROM alert_rules)`)
	// 监控插件数据目录（与日志数据目录同级）
	monDB := filepath.Join(filepath.Dir(e.st.dir), "monitor-data", "metrics.db")
	if _, err := e.db.Exec(`ATTACH DATABASE 'file:` + monDB + `?mode=ro' AS mon`); err == nil {
		res, err := e.db.Exec(`INSERT INTO alert_rules(name,service,environment,level,keyword,threshold,duration_min,webhook,silence_sec,suppress_by,silence_until,enabled)
			SELECT name,service,'*',level,keyword,threshold,duration_min,webhook,silence_sec,suppress_by,silence_until,enabled
			FROM mon.alert_rules WHERE name NOT IN (SELECT name FROM alert_rules)`)
		if err == nil {
			if n, _ := res.RowsAffected(); n > 0 {
				log.Printf("[alerts] 从监控插件迁移 %d 条报警规则", n)
			}
		}
		e.db.Exec(`DETACH DATABASE mon`)
	}
}

func (e *AlertEngine) reload() {
	rows, err := e.db.Query(`SELECT id,name,service,environment,level,keyword,threshold,duration_min,webhook,silence_sec,suppress_by,silence_until,enabled FROM alert_rules ORDER BY id`)
	if err != nil {
		return
	}
	defer rows.Close()
	var out []*AlertRule
	for rows.Next() {
		var r AlertRule
		var en int
		if rows.Scan(&r.ID, &r.Name, &r.Service, &r.Environment, &r.Level, &r.Keyword, &r.Threshold, &r.DurationMin,
			&r.Webhook, &r.SilenceSec, &r.SuppressBy, &r.SilenceUntil, &en) == nil {
			r.Enabled = en == 1
			out = append(out, &r)
		}
	}
	e.rules = out
}

// Run 每 20s 评估全部启用规则
func (e *AlertEngine) Run(stop <-chan struct{}) {
	tk := time.NewTicker(20 * time.Second)
	defer tk.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tk.C:
			e.evaluateAll()
		}
	}
}

func (e *AlertEngine) evaluateAll() {
	rules := e.list()
	type result struct {
		r      *AlertRule
		fired  bool
		recent map[string]int
	}
	var results []result
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		fired, recent := e.evalRule(r)
		results = append(results, result{r, fired, recent})
	}
	// 依赖抑制只在相同环境作用域内生效，避免 test 同名报警抑制 prod。
	firingNames := map[string]bool{}
	for _, x := range results {
		if x.fired {
			firingNames[x.r.Environment+"\x00"+x.r.Name] = true
		}
	}
	var jobs []*notifyJob
	for _, x := range results {
		supBy := ""
		for _, n := range strings.Split(x.r.SuppressBy, ",") {
			if n = strings.TrimSpace(n); n != "" && firingNames[x.r.Environment+"\x00"+n] {
				supBy = n
				break
			}
		}
		if job := e.transition(x.r, x.fired, x.recent, supBy); job != nil {
			jobs = append(jobs, job)
		}
	}
	e.flush(jobs)
}

// evalRule 判定：连续 duration_min 个完整分钟，每分钟命中数均 ≥ threshold
func (e *AlertEngine) evalRule(r *AlertRule) (bool, map[string]int) {
	cur := time.Now().UnixMilli() / 60000 * 60000
	start := cur - int64(r.DurationMin)*60000
	environment := r.Environment
	if environment == "*" {
		environment = ""
	}
	pts, err := e.st.CountSeries(r.Service, environment, r.Level, r.Keyword, start, cur-1) // 仅完整分钟
	if err != nil || int64(len(pts)) < int64(r.DurationMin) {
		return false, nil // 窗口不满，不判定（防毛刺）
	}
	byMinute := map[int64]int64{}
	var lastCount int64
	for _, p := range pts {
		byMinute[p.Minute] = p.Count
		lastCount = p.Count
	}
	key := r.Level
	if r.Keyword != "" {
		key = "KEYWORD"
	}
	return sustained(byMinute, start, r.DurationMin, r.Threshold), map[string]int{key: int(lastCount)}
}

func sustained(byMinute map[int64]int64, start int64, dur int, threshold int64) bool {
	for i := 0; i < dur; i++ {
		if byMinute[start+int64(i)*60000] < threshold {
			return false
		}
	}
	return true
}

// transition 状态机：返回待发通知（nil = 无需通知）。静默/抑制/限流在此裁决。
func (e *AlertEngine) transition(r *AlertRule, fired bool, recent map[string]int, supBy string) *notifyJob {
	e.mu.Lock()
	defer e.mu.Unlock()
	rt := e.state[r.ID]
	if rt == nil {
		rt = &alertRT{hourStart: time.Now()}
		e.state[r.ID] = rt
	}
	now := time.Now()
	rt.lastEval, rt.recent, rt.suppressedBy = now, recent, supBy
	silence := time.Duration(r.SilenceSec) * time.Second
	if silence <= 0 {
		silence = 5 * time.Minute
	}
	if now.Sub(rt.hourStart) >= time.Hour {
		rt.hourStart, rt.sentHour, rt.limited = now, 0, false
	}

	var kind string
	switch {
	case fired && !rt.firing:
		rt.firing = true
		rt.lastFire, rt.windowStart = now, now
		kind = "触发"
	case fired && rt.firing && now.Sub(rt.windowStart) >= silence:
		rt.windowStart = now
		kind = "持续中"
	case !fired && rt.firing:
		rt.firing = false
		kind = "已恢复"
	}
	if kind == "" {
		return nil
	}
	if now.UnixMilli() < r.SilenceUntil { // 维护静默：状态照转，通知不发
		e.recordNotification(r.ID, r.Name, r.Environment, "静默", kind+"（静默期内未通知）")
		return nil
	}
	if supBy != "" { // 依赖抑制：上游报警中，下游不通知
		e.recordNotification(r.ID, r.Name, r.Environment, "被抑制", kind+"（被「"+supBy+"」抑制未通知）")
		return nil
	}
	if rt.sentHour >= alertHourlyLimit {
		rt.limited = true
		e.recordNotification(r.ID, r.Name, r.Environment, "限流", kind+"（超小时上限未通知）")
		return nil
	}
	rt.sentHour++
	rt.sent++
	return &notifyJob{rule: r, kind: kind, recent: recent, firing: fired, supBy: supBy}
}

type notifyJob struct {
	rule   *AlertRule
	kind   string
	recent map[string]int
	firing bool
	supBy  string
}

// flush 收敛出口：单条正常发；同周期多条合并为一条汇总
func (e *AlertEngine) flush(jobs []*notifyJob) {
	groups := map[string][]*notifyJob{}
	for _, job := range jobs {
		key := job.rule.Environment + "\x00" + job.rule.Webhook
		groups[key] = append(groups[key], job)
	}
	if len(groups) > 1 {
		for _, group := range groups {
			e.flush(group)
		}
		return
	}
	if len(jobs) == 0 {
		return
	}
	if len(jobs) == 1 {
		e.send(jobs[0])
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[LogPlatform·报警] 同周期 %d 条报警（已合并）\n", len(jobs))
	rules := make([]string, 0, len(jobs))
	for i, j := range jobs {
		fmt.Fprintf(&b, "%d. 「%s」%s：%s%s %s 连续%d分钟≥%d/分", i+1,
			j.rule.Name, j.kind, j.rule.Service, environmentSuffix(j.rule.Environment), j.rule.Level, j.rule.DurationMin, j.rule.Threshold)
		if j.rule.Keyword != "" {
			fmt.Fprintf(&b, "（关键字 %s）", j.rule.Keyword)
		}
		b.WriteString("\n")
		rules = append(rules, j.rule.Name)
	}
	if err := postWebhook(e.http, jobs[0].rule.Webhook, b.String(), map[string]any{
		"kind": "汇总", "rules": rules,
	}); err != nil {
		log.Printf("[alerts] digest webhook: %v", err)
	}
	e.recordNotification(0, strings.Join(rules, ","), jobs[0].rule.Environment, "汇总", fmt.Sprintf("同周期 %d 条合并", len(jobs)))
}

func (e *AlertEngine) send(j *notifyJob) {
	r := j.rule
	lvNow := int64(0)
	if j.recent != nil {
		if r.Keyword != "" {
			lvNow = int64(j.recent["KEYWORD"])
		} else {
			lvNow = int64(j.recent[r.Level])
		}
	}
	text := fmt.Sprintf("[LogPlatform·报警] 规则「%s」%s\n服务: %s%s  级别: %s%s\n条件: 连续 %d 分钟每分钟 ≥ %d 条（最近一分钟: %d 条）",
		r.Name, j.kind, r.Service, environmentSuffix(r.Environment), r.Level, keywordSuffix(r.Keyword), r.DurationMin, r.Threshold, lvNow)
	detail := fmt.Sprintf("%s%s %s%s 连续%d分≥%d/分", r.Service, environmentSuffix(r.Environment), r.Level, keywordSuffix(r.Keyword), r.DurationMin, r.Threshold)
	if err := postWebhook(e.http, r.Webhook, text, map[string]any{
		"rule": r.Name, "kind": j.kind, "service": r.Service, "environment": r.Environment, "level": r.Level, "keyword": r.Keyword,
		"threshold": r.Threshold, "duration_min": r.DurationMin, "recent": j.recent,
	}); err != nil {
		log.Printf("[alerts] webhook %s: %v", r.Name, err)
		detail += "（发送失败: " + err.Error() + "）"
	}
	e.recordNotification(r.ID, r.Name, r.Environment, j.kind, detail)
}

func keywordSuffix(kw string) string {
	if kw == "" {
		return ""
	}
	return "  关键字: " + kw
}

func environmentSuffix(environment string) string {
	if environment == "" || environment == "*" {
		if environment == "*" {
			return "[全部环境]"
		}
		return ""
	}
	return "[" + environment + "]"
}

// postWebhook 钉钉/企微/通用 三态适配
func postWebhook(client *http.Client, webhook, text string, generic map[string]any) error {
	var body []byte
	switch {
	case strings.Contains(webhook, "dingtalk"):
		body, _ = json.Marshal(map[string]any{"msgtype": "text", "text": map[string]string{"content": text}})
	case strings.Contains(webhook, "qyapi.weixin"):
		body, _ = json.Marshal(map[string]any{"msgtype": "text", "text": map[string]string{"content": text}})
	default:
		generic["text"] = text
		body, _ = json.Marshal(generic)
	}
	resp, err := client.Post(webhook, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func (e *AlertEngine) recordNotification(ruleID int64, rule, environment, kind, detail string) {
	e.db.Exec(`INSERT INTO alert_notifications(rule_id,rule,environment,kind,at,detail) VALUES(?,?,?,?,?,?)`,
		ruleID, rule, environment, kind, time.Now().Unix(), detail)
}

func (e *AlertEngine) Notifications(ruleID int64, environment string, limit int) []AlertNotification {
	q := `SELECT id,rule_id,rule,environment,kind,at,detail FROM alert_notifications`
	where := []string{}
	args := []any{}
	if ruleID > 0 {
		where = append(where, `rule_id=?`)
		args = append(args, ruleID)
	}
	if environment != "" {
		where = append(where, `(environment=? OR environment='*')`)
		args = append(args, environment)
	}
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, " AND ")
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := e.db.Query(q, args...)
	out := []AlertNotification{}
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var n AlertNotification
		var d sql.NullString
		if rows.Scan(&n.ID, &n.RuleID, &n.Rule, &n.Environment, &n.Kind, &n.At, &d) == nil {
			n.Detail = d.String
			out = append(out, n)
		}
	}
	return out
}

// ---------- CRUD ----------

func (e *AlertEngine) list() []*AlertRule { return e.rules }

// List 返回副本（HTTP 出口）
func (e *AlertEngine) List() []*AlertRule {
	out := make([]*AlertRule, len(e.rules))
	copy(out, e.rules)
	return out
}

func (e *AlertEngine) Create(r *AlertRule) error {
	if err := validateAlertRule(r); err != nil {
		return err
	}
	if r.Level == "" {
		r.Level = "ERROR"
	}
	if r.DurationMin < 1 {
		r.DurationMin = 1
	}
	if r.SilenceSec <= 0 {
		r.SilenceSec = 300
	}
	res, err := e.db.Exec(`INSERT INTO alert_rules(name,service,environment,level,keyword,threshold,duration_min,webhook,silence_sec,suppress_by,silence_until,enabled)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,1)`,
		r.Name, r.Service, r.Environment, r.Level, r.Keyword, r.Threshold, r.DurationMin, r.Webhook, r.SilenceSec, r.SuppressBy, r.SilenceUntil)
	if err != nil {
		return err
	}
	r.ID, _ = res.LastInsertId()
	r.Enabled = true
	e.reload()
	return nil
}

func (e *AlertEngine) Update(r *AlertRule) error {
	if err := validateAlertRule(r); err != nil {
		return err
	}
	_, err := e.db.Exec(`UPDATE alert_rules SET name=?,service=?,environment=?,level=?,keyword=?,threshold=?,duration_min=?,webhook=?,silence_sec=?,suppress_by=?,silence_until=?,enabled=? WHERE id=?`,
		r.Name, r.Service, r.Environment, r.Level, r.Keyword, r.Threshold, r.DurationMin, r.Webhook, r.SilenceSec, r.SuppressBy, r.SilenceUntil, r.Enabled, r.ID)
	e.reload()
	return err
}

func (e *AlertEngine) Delete(id int64) error {
	_, err := e.db.Exec(`DELETE FROM alert_rules WHERE id=?`, id)
	e.mu.Lock()
	delete(e.state, id)
	e.mu.Unlock()
	e.reload()
	return err
}

func (e *AlertEngine) Test(id int64) error {
	var r *AlertRule
	for _, x := range e.list() {
		if x.ID == id {
			r = x
			break
		}
	}
	if r == nil {
		return fmt.Errorf("not found")
	}
	err := postWebhook(e.http, r.Webhook,
		fmt.Sprintf("[LogPlatform·报警] 测试通知：规则「%s」（%s%s %s%s 连续%d分钟≥%d/分钟）",
			r.Name, r.Service, environmentSuffix(r.Environment), r.Level, keywordSuffix(r.Keyword), r.DurationMin, r.Threshold),
		map[string]any{"rule": r.Name, "kind": "测试"})
	e.recordNotification(r.ID, r.Name, r.Environment, "测试", "手动测试通知")
	return err
}

func validateAlertRule(r *AlertRule) error {
	r.Name = strings.TrimSpace(r.Name)
	r.Service = strings.TrimSpace(r.Service)
	r.Environment = strings.TrimSpace(r.Environment)
	r.Webhook = strings.TrimSpace(r.Webhook)
	if r.Name == "" || r.Service == "" || r.Environment == "" || r.Webhook == "" {
		return fmt.Errorf("名称、服务、环境和 Webhook 必填；跨环境规则请显式选择全部环境")
	}
	return nil
}

func (e *AlertEngine) Statuses() map[int64]AlertStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := map[int64]AlertStatus{}
	for id, rt := range e.state {
		st := AlertStatus{
			ID: id, Firing: rt.firing, Sent: rt.sent, Suppressed: rt.limited,
			SuppressedBy: rt.suppressedBy, Recent: rt.recent,
		}
		if !rt.lastEval.IsZero() {
			st.LastEval = rt.lastEval.Format("15:04:05")
		}
		if !rt.lastFire.IsZero() {
			st.LastFire = rt.lastFire.Format("15:04:05")
		}
		out[id] = st
	}
	// 静默窗口来自规则（非运行态）
	for _, r := range e.list() {
		if st, ok := out[r.ID]; ok && r.SilenceUntil > time.Now().UnixMilli() {
			st.Silenced = true
			st.SilenceUntil = time.UnixMilli(r.SilenceUntil).Format("15:04")
			out[r.ID] = st
		}
	}
	return out
}
