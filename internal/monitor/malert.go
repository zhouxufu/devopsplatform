package monitor

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// MetricRule 指标报警规则（监控大盘自己的报警，区别于日志插件的关键字订阅）。
// 三类判定：
//   - error_rate  窗口内错误率 = 失败/(总-忽略) ≥ threshold%（状态码按可用率规则 ok/ignore 判定）
//   - latency     窗口内聚合分位数 ≥ threshold ms（跨状态合并直方图桶）
//   - traffic_up / traffic_down  调用量环比前一个等长窗口变化 ≥ threshold%（基线样本 < min_sample 不判定，防小流量毛刺）
type MetricRule struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	Service      string  `json:"service"`
	Environment  string  `json:"environment"`
	Instance     string  `json:"instance"`
	Metric       string  `json:"metric"` // OTLP 指标名（http.server.duration 等）
	Iface        string  `json:"iface"`  // 空 = 全部接口聚合
	Type         string  `json:"type"`
	Quantile     float64 `json:"quantile"`   // latency 用：0.5/0.95/0.99
	WindowMin    int     `json:"window_min"` // 评估窗口（完整分钟）
	Threshold    float64 `json:"threshold"`
	MinSample    float64 `json:"min_sample"` // 样本/基线下限
	Webhook      string  `json:"webhook"`
	SilenceSec   int     `json:"silence_sec"`
	SuppressBy   string  `json:"suppress_by"`
	SilenceUntil int64   `json:"silence_until"`
	Enabled      bool    `json:"enabled"`
}

type MetricAlertStatus struct {
	ID           int64          `json:"id"`
	Firing       bool           `json:"firing"`
	LastEval     string         `json:"last_eval,omitempty"`
	LastFire     string         `json:"last_fire,omitempty"`
	Sent         int            `json:"sent"`
	Suppressed   bool           `json:"suppressed"`
	SuppressedBy string         `json:"suppressed_by,omitempty"`
	Silenced     bool           `json:"silenced"`
	SilenceUntil string         `json:"silence_until,omitempty"`
	Recent       map[string]any `json:"recent,omitempty"` // 最近一次评估的观测值
}

type malertRT struct {
	firing             bool
	windowStart        time.Time
	lastEval, lastFire time.Time
	sent, sentHour     int
	hourStart          time.Time
	limited            bool
	suppressedBy       string
	recent             map[string]any
}

const malertHourlyLimit = 20

type MetricAlertNotification struct {
	ID          int64  `json:"id"`
	RuleID      int64  `json:"rule_id"`
	Rule        string `json:"rule"`
	Environment string `json:"environment"`
	Kind        string `json:"kind"`
	At          int64  `json:"at"`
	Detail      string `json:"detail"`
}

type mnotifyJob struct {
	rule   *MetricRule
	kind   string
	recent map[string]any
}

// MetricAlertEngine 指标评估 + 收敛 + 通知出口（状态机与日志插件订阅报警同语义）
type MetricAlertEngine struct {
	db    *monitorDB
	http  *http.Client
	mu    sync.Mutex
	rules []*MetricRule
	state map[int64]*malertRT
}

func NewMetricAlertEngine(db *monitorDB) *MetricAlertEngine {
	e := &MetricAlertEngine{db: db, http: &http.Client{Timeout: 8 * time.Second}, state: map[int64]*malertRT{}}
	db.Exec(`CREATE TABLE IF NOT EXISTS metric_alert_rules (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL, service TEXT NOT NULL, environment TEXT NOT NULL DEFAULT '', instance TEXT NOT NULL DEFAULT '', metric TEXT NOT NULL, iface TEXT DEFAULT '',
		type TEXT NOT NULL, quantile REAL DEFAULT 0.99, window_min INTEGER NOT NULL,
		threshold REAL NOT NULL, min_sample REAL DEFAULT 10,
		webhook TEXT NOT NULL, silence_sec INTEGER DEFAULT 300,
		suppress_by TEXT DEFAULT '', silence_until INTEGER DEFAULT 0,
		enabled INTEGER DEFAULT 1)`)
	if cols, err := tableColumns(db, "metric_alert_rules"); err == nil && !cols["environment"] {
		db.Exec(`ALTER TABLE metric_alert_rules ADD COLUMN environment TEXT NOT NULL DEFAULT ''`)
	}
	if cols, err := tableColumns(db, "metric_alert_rules"); err == nil && !cols["instance"] {
		db.Exec(`ALTER TABLE metric_alert_rules ADD COLUMN instance TEXT NOT NULL DEFAULT ''`)
	}
	// 旧版空串表示隐式跨环境；升级后保留语义但改成显式作用域。
	db.Exec(`UPDATE metric_alert_rules SET environment='*' WHERE TRIM(COALESCE(environment,''))=''`)
	db.Exec(`CREATE TABLE IF NOT EXISTS metric_alert_notifications (
		id INTEGER PRIMARY KEY AUTOINCREMENT, rule_id INTEGER, rule TEXT, environment TEXT NOT NULL DEFAULT '*', kind TEXT, at INTEGER, detail TEXT)`)
	db.Exec(`ALTER TABLE metric_alert_notifications ADD COLUMN environment TEXT NOT NULL DEFAULT '*'`)
	db.Exec(`DELETE FROM metric_alert_notifications WHERE at < ?`, time.Now().Add(-7*24*time.Hour).Unix())
	e.reload()
	go e.Run(make(chan struct{}))
	return e
}

func (e *MetricAlertEngine) reload() {
	rows, err := e.db.Query(`SELECT id,name,service,environment,instance,metric,iface,type,quantile,window_min,threshold,min_sample,webhook,silence_sec,suppress_by,silence_until,enabled FROM metric_alert_rules ORDER BY id`)
	if err != nil {
		return
	}
	defer rows.Close()
	var out []*MetricRule
	for rows.Next() {
		var r MetricRule
		var en int
		if rows.Scan(&r.ID, &r.Name, &r.Service, &r.Environment, &r.Instance, &r.Metric, &r.Iface, &r.Type, &r.Quantile, &r.WindowMin,
			&r.Threshold, &r.MinSample, &r.Webhook, &r.SilenceSec, &r.SuppressBy, &r.SilenceUntil, &en) == nil {
			r.Enabled = en == 1
			out = append(out, &r)
		}
	}
	e.rules = out
}

// Run 每 20s 评估全部启用规则
func (e *MetricAlertEngine) Run(stop <-chan struct{}) {
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

func (e *MetricAlertEngine) evaluateAll() {
	type result struct {
		r      *MetricRule
		fired  bool
		recent map[string]any
	}
	var results []result
	for _, r := range e.rules {
		if !r.Enabled {
			continue
		}
		fired, recent := e.evalRule(r)
		results = append(results, result{r, fired, recent})
	}
	firingNames := map[string]bool{}
	for _, x := range results {
		if x.fired {
			firingNames[x.r.Environment+"\x00"+x.r.Instance+"\x00"+x.r.Name] = true
		}
	}
	var jobs []*mnotifyJob
	for _, x := range results {
		supBy := ""
		for _, n := range strings.Split(x.r.SuppressBy, ",") {
			if n = strings.TrimSpace(n); n != "" && firingNames[x.r.Environment+"\x00"+x.r.Instance+"\x00"+n] {
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

// evalRule 窗口取最近 window_min 个完整分钟
func (e *MetricAlertEngine) evalRule(r *MetricRule) (bool, map[string]any) {
	cur := time.Now().UnixMilli() / 60000 * 60000
	start := cur - int64(r.WindowMin)*60000
	end := cur - 1
	switch r.Type {
	case "error_rate":
		return e.evalErrorRate(r, start, end)
	case "latency":
		return e.evalLatency(r, start, end)
	case "traffic_up", "traffic_down":
		return e.evalTraffic(r, start, end)
	}
	return false, nil
}

// evalErrorRate 错误率：hist+counter 按状态聚合，verdict 判定 ok/ignore
func (e *MetricAlertEngine) evalErrorRate(r *MetricRule, start, end int64) (bool, map[string]any) {
	// 注意：单连接池下不能在 rows 未关闭时嵌套查询，规则必须先于主查询加载
	rule := loadAvailRule(e.db, r.Service, r.Environment, r.Metric)
	environment := metricEnvironmentFilter(r.Environment)
	rows, err := e.db.Query(`SELECT status,SUM(c) FROM (
			SELECT status,SUM(cnt) c FROM otlp_hist WHERE service=? AND (?='' OR environment=?) AND (?='' OR instance=?) AND name=? AND iface=? AND minute BETWEEN ? AND ? GROUP BY status
			UNION ALL
			SELECT status,SUM(val) c FROM otlp_counter WHERE service=? AND (?='' OR environment=?) AND (?='' OR instance=?) AND name=? AND iface=? AND minute BETWEEN ? AND ? GROUP BY status)
		GROUP BY status`,
		r.Service, environment, environment, r.Instance, r.Instance, r.Metric, r.Iface, start, end,
		r.Service, environment, environment, r.Instance, r.Instance, r.Metric, r.Iface, start, end)
	if err != nil {
		return false, nil
	}
	defer rows.Close()
	var total, ok float64
	for rows.Next() {
		var status string
		var c float64
		if rows.Scan(&status, &c) != nil {
			continue
		}
		switch verdict(status, rule) {
		case verdictIgnore:
			continue
		case verdictOK:
			total += c
			ok += c
		default:
			total += c
		}
	}
	if total < r.MinSample {
		return false, map[string]any{"total": total, "min_sample": r.MinSample}
	}
	rate := (total - ok) / total * 100
	return rate >= r.Threshold, map[string]any{"error_rate_pct": round2(rate), "total": total}
}

// evalLatency 分位数：跨状态合并窗口内全部直方图桶
func (e *MetricAlertEngine) evalLatency(r *MetricRule, start, end int64) (bool, map[string]any) {
	environment := metricEnvironmentFilter(r.Environment)
	rows, err := e.db.Query(`SELECT cnt,sum,bounds,buckets FROM otlp_hist
		WHERE service=? AND (?='' OR environment=?) AND (?='' OR instance=?) AND name=? AND iface=? AND minute BETWEEN ? AND ?`,
		r.Service, environment, environment, r.Instance, r.Instance, r.Metric, r.Iface, start, end)
	if err != nil {
		return false, nil
	}
	defer rows.Close()
	var cnt, sum float64
	var bounds, buckets []float64
	for rows.Next() {
		var c, s float64
		var b1, b2 string
		var bs, bk []float64
		if rows.Scan(&c, &s, &b1, &b2) != nil {
			continue
		}
		json.Unmarshal([]byte(b1), &bs)
		json.Unmarshal([]byte(b2), &bk)
		cnt += c
		sum += s
		if len(bs) > 0 {
			bounds = bs
		}
		if len(bk) > len(buckets) {
			nb := make([]float64, len(bk))
			copy(nb, buckets)
			buckets = nb
		}
		for i, v := range bk {
			buckets[i] += v
		}
	}
	if cnt < r.MinSample || len(bounds) == 0 {
		return false, map[string]any{"sample": cnt, "min_sample": r.MinSample}
	}
	q := r.Quantile
	if q <= 0 {
		q = 0.99
	}
	v := bucketQuantile(bounds, buckets, q)
	return v >= r.Threshold, map[string]any{"quantile": q, "value_ms": round2(v), "sample": cnt}
}

// evalTraffic 调用量突变：当前窗口 vs 前一个等长窗口
func (e *MetricAlertEngine) evalTraffic(r *MetricRule, start, end int64) (bool, map[string]any) {
	baseEnd := start - 1
	baseStart := start - int64(r.WindowMin)*60000
	cur := e.windowCount(r, start, end)
	base := e.windowCount(r, baseStart, baseEnd)
	if base < r.MinSample {
		return false, map[string]any{"cur": cur, "base": base, "min_sample": r.MinSample}
	}
	change := (cur - base) / base * 100
	if r.Type == "traffic_up" {
		return change >= r.Threshold, map[string]any{"cur": cur, "base": base, "change_pct": round2(change)}
	}
	return change <= -r.Threshold, map[string]any{"cur": cur, "base": base, "change_pct": round2(change)}
}

func (e *MetricAlertEngine) windowCount(r *MetricRule, start, end int64) float64 {
	var cur float64
	environment := metricEnvironmentFilter(r.Environment)
	e.db.QueryRow(`SELECT SUM(c) FROM (
		SELECT SUM(cnt) c FROM otlp_hist WHERE service=? AND (?='' OR environment=?) AND (?='' OR instance=?) AND name=? AND iface=? AND minute BETWEEN ? AND ?
		UNION ALL
		SELECT SUM(val) c FROM otlp_counter WHERE service=? AND (?='' OR environment=?) AND (?='' OR instance=?) AND name=? AND iface=? AND minute BETWEEN ? AND ?)`,
		r.Service, environment, environment, r.Instance, r.Instance, r.Metric, r.Iface, start, end,
		r.Service, environment, environment, r.Instance, r.Instance, r.Metric, r.Iface, start, end).Scan(&cur)
	return cur
}

func metricEnvironmentFilter(environment string) string {
	if environment == "*" {
		return ""
	}
	return environment
}

// loadAvailRule 可用率判定规则：(service,metric) → 服务默认 → nil（内置语义）
func loadAvailRule(db *monitorDB, svc, environment, name string) *AvailRule {
	for _, key := range [][3]string{{svc, environment, name}, {svc, environment, ""}, {svc, "*", name}, {svc, "*", ""}} {
		var okC, igC string
		if err := db.QueryRow(`SELECT ok_codes,ignore_codes FROM avail_rules WHERE service=? AND environment=? AND name=?`, key[0], key[1], key[2]).
			Scan(&okC, &igC); err != nil {
			continue
		}
		var r AvailRule
		json.Unmarshal([]byte(okC), &r.OkCodes)
		json.Unmarshal([]byte(igC), &r.IgnoreCodes)
		r.Service, r.Environment, r.Name = key[0], key[1], key[2]
		return &r
	}
	return nil
}

func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }

// transition 状态机（与日志订阅报警同语义）
func (e *MetricAlertEngine) transition(r *MetricRule, fired bool, recent map[string]any, supBy string) *mnotifyJob {
	e.mu.Lock()
	defer e.mu.Unlock()
	rt := e.state[r.ID]
	if rt == nil {
		rt = &malertRT{hourStart: time.Now()}
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
	if now.UnixMilli() < r.SilenceUntil {
		e.record(r.ID, r.Name, r.Environment, "静默", kind+"（静默期内未通知）")
		return nil
	}
	if supBy != "" {
		e.record(r.ID, r.Name, r.Environment, "被抑制", kind+"（被「"+supBy+"」抑制未通知）")
		return nil
	}
	if rt.sentHour >= malertHourlyLimit {
		rt.limited = true
		e.record(r.ID, r.Name, r.Environment, "限流", kind+"（超小时上限未通知）")
		return nil
	}
	rt.sentHour++
	rt.sent++
	return &mnotifyJob{rule: r, kind: kind, recent: recent}
}

func (e *MetricAlertEngine) flush(jobs []*mnotifyJob) {
	groups := map[string][]*mnotifyJob{}
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
	fmt.Fprintf(&b, "[LogPlatform·监控] 同周期 %d 条报警（已合并）\n", len(jobs))
	rules := make([]string, 0, len(jobs))
	for i, j := range jobs {
		fmt.Fprintf(&b, "%d. 「%s」%s：%s\n", i+1, j.rule.Name, j.kind, conditionText(j.rule))
		rules = append(rules, j.rule.Name)
	}
	if err := mpostWebhook(e.http, jobs[0].rule.Webhook, b.String(), map[string]any{"kind": "汇总", "rules": rules}); err != nil {
		log.Printf("[malerts] digest webhook: %v", err)
	}
	e.record(0, strings.Join(rules, ","), jobs[0].rule.Environment, "汇总", fmt.Sprintf("同周期 %d 条合并", len(jobs)))
}

func (e *MetricAlertEngine) send(j *mnotifyJob) {
	r := j.rule
	text := fmt.Sprintf("[LogPlatform·监控] 规则「%s」%s\n对象: %s[%s/%s] %s%s\n条件: %s\n观测: %s",
		r.Name, j.kind, r.Service, environmentLabel(r.Environment), instanceLabel(r.Instance), r.Metric, ifaceSuffix(r.Iface), conditionText(r), recentText(j.recent))
	detail := fmt.Sprintf("%s[%s/%s] %s%s %s（%s）", r.Service, environmentLabel(r.Environment), instanceLabel(r.Instance), r.Metric, ifaceSuffix(r.Iface), j.kind, recentText(j.recent))
	if err := mpostWebhook(e.http, r.Webhook, text, map[string]any{
		"rule": r.Name, "kind": j.kind, "service": r.Service, "environment": r.Environment, "instance": r.Instance, "metric": r.Metric, "iface": r.Iface,
		"type": r.Type, "threshold": r.Threshold, "window_min": r.WindowMin, "recent": j.recent,
	}); err != nil {
		log.Printf("[malerts] webhook %s: %v", r.Name, err)
		detail += "（发送失败: " + err.Error() + "）"
	}
	e.record(r.ID, r.Name, r.Environment, j.kind, detail)
}

func ifaceSuffix(iface string) string {
	if iface == "" {
		return "（全部接口）"
	}
	return " " + iface
}

func environmentLabel(environment string) string {
	if environment == "" || environment == "*" {
		return "全部环境"
	}
	return environment
}

func instanceLabel(instance string) string {
	if instance == "" {
		return "全部实例"
	}
	return instance
}

func conditionText(r *MetricRule) string {
	switch r.Type {
	case "error_rate":
		return fmt.Sprintf("%d分钟错误率 ≥ %g%%（样本≥%g）", r.WindowMin, r.Threshold, r.MinSample)
	case "latency":
		return fmt.Sprintf("%d分钟 %s ≥ %gms", r.WindowMin, quantileName(r.Quantile), r.Threshold)
	case "traffic_up":
		return fmt.Sprintf("调用量环比前%d分钟激增 ≥ %g%%（基线≥%g）", r.WindowMin, r.Threshold, r.MinSample)
	case "traffic_down":
		return fmt.Sprintf("调用量环比前%d分钟骤降 ≥ %g%%（基线≥%g）", r.WindowMin, r.Threshold, r.MinSample)
	}
	return r.Type
}

func quantileName(q float64) string {
	switch q {
	case 0.5:
		return "TP50"
	case 0.95:
		return "TP95"
	default:
		return "TP99"
	}
}

func recentText(recent map[string]any) string {
	if recent == nil {
		return "无数据"
	}
	var parts []string
	for k, v := range recent {
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	return strings.Join(parts, " ")
}

func mpostWebhook(client *http.Client, webhook, text string, generic map[string]any) error {
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

func (e *MetricAlertEngine) record(ruleID int64, rule, environment, kind, detail string) {
	e.db.Exec(`INSERT INTO metric_alert_notifications(rule_id,rule,environment,kind,at,detail) VALUES(?,?,?,?,?,?)`,
		ruleID, rule, environment, kind, time.Now().Unix(), detail)
}

func (e *MetricAlertEngine) Notifications(ruleID int64, environment string, limit int) []MetricAlertNotification {
	q := `SELECT id,rule_id,rule,environment,kind,at,detail FROM metric_alert_notifications`
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
	out := []MetricAlertNotification{}
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var n MetricAlertNotification
		var d sql.NullString
		if rows.Scan(&n.ID, &n.RuleID, &n.Rule, &n.Environment, &n.Kind, &n.At, &d) == nil {
			n.Detail = d.String
			out = append(out, n)
		}
	}
	return out
}

// ---------- CRUD ----------

func (e *MetricAlertEngine) List() []*MetricRule {
	out := make([]*MetricRule, len(e.rules))
	copy(out, e.rules)
	return out
}

func (e *MetricAlertEngine) Create(r *MetricRule) error {
	if err := validateMetricRule(r); err != nil {
		return err
	}
	if r.WindowMin < 1 {
		r.WindowMin = 5
	}
	if r.SilenceSec <= 0 {
		r.SilenceSec = 300
	}
	if r.MinSample <= 0 {
		r.MinSample = 10
	}
	res, err := e.db.Exec(`INSERT INTO metric_alert_rules(name,service,environment,instance,metric,iface,type,quantile,window_min,threshold,min_sample,webhook,silence_sec,suppress_by,silence_until,enabled)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,1)`,
		r.Name, r.Service, r.Environment, r.Instance, r.Metric, r.Iface, r.Type, r.Quantile, r.WindowMin, r.Threshold, r.MinSample, r.Webhook, r.SilenceSec, r.SuppressBy, r.SilenceUntil)
	if err != nil {
		return err
	}
	r.ID, _ = res.LastInsertId()
	r.Enabled = true
	e.reload()
	return nil
}

func (e *MetricAlertEngine) Update(r *MetricRule) error {
	if err := validateMetricRule(r); err != nil {
		return err
	}
	_, err := e.db.Exec(`UPDATE metric_alert_rules SET name=?,service=?,environment=?,instance=?,metric=?,iface=?,type=?,quantile=?,window_min=?,threshold=?,min_sample=?,webhook=?,silence_sec=?,suppress_by=?,silence_until=?,enabled=? WHERE id=?`,
		r.Name, r.Service, r.Environment, r.Instance, r.Metric, r.Iface, r.Type, r.Quantile, r.WindowMin, r.Threshold, r.MinSample, r.Webhook, r.SilenceSec, r.SuppressBy, r.SilenceUntil, r.Enabled, r.ID)
	e.reload()
	return err
}

func (e *MetricAlertEngine) Delete(id int64) error {
	_, err := e.db.Exec(`DELETE FROM metric_alert_rules WHERE id=?`, id)
	e.mu.Lock()
	delete(e.state, id)
	e.mu.Unlock()
	e.reload()
	return err
}

func (e *MetricAlertEngine) Test(id int64) error {
	var r *MetricRule
	for _, x := range e.rules {
		if x.ID == id {
			r = x
			break
		}
	}
	if r == nil {
		return fmt.Errorf("not found")
	}
	err := mpostWebhook(e.http, r.Webhook,
		fmt.Sprintf("[LogPlatform·监控] 测试通知：规则「%s」（%s[%s/%s] %s%s %s）",
			r.Name, r.Service, environmentLabel(r.Environment), instanceLabel(r.Instance), r.Metric, ifaceSuffix(r.Iface), conditionText(r)),
		map[string]any{"rule": r.Name, "kind": "测试"})
	e.record(r.ID, r.Name, r.Environment, "测试", "手动测试通知")
	return err
}

func validateMetricRule(r *MetricRule) error {
	r.Name = strings.TrimSpace(r.Name)
	r.Service = strings.TrimSpace(r.Service)
	r.Environment = strings.TrimSpace(r.Environment)
	r.Metric = strings.TrimSpace(r.Metric)
	r.Webhook = strings.TrimSpace(r.Webhook)
	if r.Name == "" || r.Service == "" || r.Environment == "" || r.Metric == "" || r.Webhook == "" {
		return fmt.Errorf("名称、服务、环境、指标和 Webhook 必填；跨环境规则请显式选择全部环境")
	}
	return nil
}

func (e *MetricAlertEngine) Statuses() map[int64]MetricAlertStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := map[int64]MetricAlertStatus{}
	for id, rt := range e.state {
		st := MetricAlertStatus{
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
	for _, r := range e.rules {
		if st, ok := out[r.ID]; ok && r.SilenceUntil > time.Now().UnixMilli() {
			st.Silenced = true
			st.SilenceUntil = time.UnixMilli(r.SilenceUntil).Format("15:04")
			out[r.ID] = st
		}
	}
	return out
}
