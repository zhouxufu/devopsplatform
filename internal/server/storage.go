package server

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"logplatform/internal/types"

	_ "modernc.org/sqlite"
)

// Storage SQLite 存储：按天分表（logs_YYYYMMDD + FTS5 虚拟表 + 触发器）。
// 过期整表 DROP，空间瞬时回收；写入永远落在"今天"这张小表上。
type Storage struct {
	db            *sql.DB
	dir           string
	retentionDays int
	maxDiskBytes  int64
	mu            sync.Mutex
}

func dayStr(t time.Time) string { return t.Format("20060102") }

// Open 打开/初始化存储，并启动维护任务（提前建表、保留清理、磁盘水位）
func Open(dir string, retentionDays int, maxDiskGB float64) (*Storage, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	dsn := "file:" + filepath.Join(dir, "logs.db") +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite 单写者，串行化写入避免 SQLITE_BUSY
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS ingest_dedupe (
		event_id TEXT PRIMARY KEY,
		received_at INTEGER NOT NULL
	); CREATE INDEX IF NOT EXISTS ingest_dedupe_received_idx ON ingest_dedupe(received_at)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("create ingest dedupe: %w", err)
	}
	s := &Storage{
		db:            db,
		dir:           dir,
		retentionDays: retentionDays,
		maxDiskBytes:  int64(maxDiskGB * 1024 * 1024 * 1024),
	}
	// 启动即建今天 + 明天的表
	if err := s.ensureDay(time.Now()); err != nil {
		return nil, err
	}
	if err := s.ensureDay(time.Now().Add(26 * time.Hour)); err != nil {
		return nil, err
	}
	if tables, err := s.logTables(); err == nil {
		for _, name := range tables {
			if err := s.ensureLogColumns(name); err != nil {
				return nil, err
			}
		}
	}
	go s.maintenance()
	return s, nil
}

// ensureDay 原子建某天的表 + 索引 + FTS 虚拟表 + 触发器（幂等）
func (s *Storage) ensureDay(t time.Time) error {
	d := dayStr(t)
	name := "logs_" + d
	s.mu.Lock()
	defer s.mu.Unlock()
	var cnt int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&cnt); err != nil {
		return err
	}
	if cnt > 0 {
		return s.ensureLogColumns(name)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmts := []string{
		fmt.Sprintf(`CREATE TABLE %s (
			event_id TEXT, ts INTEGER NOT NULL, application_id TEXT DEFAULT '', service TEXT NOT NULL, environment TEXT DEFAULT '', host TEXT,
			path TEXT, level TEXT, trace_id TEXT, message TEXT)`, name),
		fmt.Sprintf(`CREATE INDEX %s_idx ON %s(service, ts)`, name, name),
		fmt.Sprintf(`CREATE INDEX %s_env_idx ON %s(environment,service,ts)`, name, name),
		fmt.Sprintf(`CREATE VIRTUAL TABLE %s_fts USING fts5(message, content='%s', content_rowid='rowid')`, name, name),
		fmt.Sprintf(`CREATE TRIGGER %s_ai AFTER INSERT ON %s BEGIN
			INSERT INTO %s_fts(rowid, message) VALUES (new.rowid, new.message); END`, name, name, name),
		fmt.Sprintf(`CREATE TRIGGER %s_ad AFTER DELETE ON %s BEGIN
			INSERT INTO %s_fts(%s_fts, rowid, message) VALUES ('delete', old.rowid, old.message); END`, name, name, name, name),
	}
	for _, q := range stmts {
		if _, err := tx.Exec(q); err != nil {
			return fmt.Errorf("ensureDay %s: %w", d, err)
		}
	}
	return tx.Commit()
}

func (s *Storage) ensureLogColumns(name string) error {
	// 旧按日表在线补新增维度；重复列错误可忽略。
	s.db.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN environment TEXT DEFAULT ''`, name))
	s.db.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN event_id TEXT`, name))
	s.db.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN path TEXT`, name))
	s.db.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN application_id TEXT DEFAULT ''`, name))
	s.db.Exec(fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s_app_idx ON %s(application_id,environment,ts)`, name, name))
	_, err := s.db.Exec(fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s_env_idx ON %s(environment,service,ts)`, name, name))
	return err
}

// ---------- 事件富化：level / traceId 提取（server 侧，规则集中可改） ----------

var levelRe = regexp.MustCompile(`\b(ERROR|FATAL|WARN|WARNING|INFO|DEBUG|TRACE)\b`)

func normLevel(l string) string {
	switch strings.ToUpper(l) {
	case "WARNING":
		return "WARN"
	case "":
		return ""
	default:
		return strings.ToUpper(l)
	}
}

// ---------- traceId 提取规则：文件化 + 按 environment/service 可配 + 热加载 ----------
// rules.json 格式: [{"environment":"prod","service":"order-api","regex":"traceId=([0-9A-Za-z-]+)"}]
// environment/service 都支持显式 "*" 通配；旧规则未填 environment 时迁移为 "*"。

type traceRules struct {
	mu      sync.RWMutex
	path    string
	mtime   int64
	byScope map[string][]*regexp.Regexp
}

var theTraceRules = &traceRules{byScope: map[string][]*regexp.Regexp{}}

var defaultTraceRe = regexp.MustCompile(`(?i)trace[_-]?id["'=:\s]+([0-9a-zA-Z\-]{8,64})`)

// LoadTraceRules 加载规则文件（不存在则仅用内置规则），并每 30s 热加载
func LoadTraceRules(path string) {
	if path == "" {
		return
	}
	theTraceRules.path = path
	loadTraceRulesOnce()
	go func() {
		for range time.Tick(30 * time.Second) {
			loadTraceRulesOnce()
		}
	}()
}

func loadTraceRulesOnce() {
	fi, err := os.Stat(theTraceRules.path)
	if err != nil || fi.ModTime().Unix() == theTraceRules.mtime {
		return
	}
	raw, err := os.ReadFile(theTraceRules.path)
	if err != nil {
		return
	}
	var defs []struct {
		Environment string `json:"environment"`
		Service     string `json:"service"`
		Regex       string `json:"regex"`
	}
	if json.Unmarshal(raw, &defs) != nil {
		log.Printf("[rules] parse %s failed, keep old rules", theTraceRules.path)
		return
	}
	m := map[string][]*regexp.Regexp{}
	for _, d := range defs {
		d.Environment = strings.TrimSpace(d.Environment)
		if d.Environment == "" {
			d.Environment = "*"
		}
		d.Service = strings.TrimSpace(d.Service)
		if d.Service == "" {
			d.Service = "*"
		}
		if re, err := regexp.Compile(d.Regex); err == nil {
			key := d.Environment + "\x00" + d.Service
			m[key] = append(m[key], re)
		}
	}
	theTraceRules.mu.Lock()
	theTraceRules.byScope = m
	theTraceRules.mtime = fi.ModTime().Unix()
	theTraceRules.mu.Unlock()
	log.Printf("[rules] loaded %d trace rules from %s", len(defs), theTraceRules.path)
}

func matchTraceID(environment, service, msg string) string {
	if len(msg) > 2048 {
		msg = msg[:2048]
	}
	theTraceRules.mu.RLock()
	defer theTraceRules.mu.RUnlock()
	for _, scope := range [][2]string{{environment, service}, {environment, "*"}, {"*", service}, {"*", "*"}} {
		for _, re := range theTraceRules.byScope[scope[0]+"\x00"+scope[1]] {
			if m := re.FindStringSubmatch(msg); m != nil && len(m) > 1 {
				return m[1]
			}
		}
	}
	if m := defaultTraceRe.FindStringSubmatch(msg); m != nil {
		return m[1]
	}
	return ""
}

// Enrich 提取 level 与 traceId（JSON 字段优先，其次规则正则；单条超长截断）
func Enrich(e *types.Event, maxLen int) {
	if len(e.Message) > maxLen {
		e.Message = e.Message[:maxLen] + "... [truncated]"
	}
	if e.Level == "" {
		head := e.Message
		if len(head) > 256 {
			head = head[:256]
		}
		if m := levelRe.FindStringSubmatch(head); m != nil {
			e.Level = m[1]
		}
	}
	e.Level = normLevel(e.Level)
	if e.TraceID == "" {
		msg := e.Message
		if len(msg) > 2048 {
			msg = msg[:2048]
		}
		trimmed := strings.TrimSpace(msg)
		if strings.HasPrefix(trimmed, "{") {
			var m map[string]any
			if json.Unmarshal([]byte(trimmed), &m) == nil {
				for _, k := range []string{"trace_id", "traceId", "traceID", "trace-id"} {
					if v, ok := m[k].(string); ok && v != "" {
						e.TraceID = v
						return
					}
				}
			}
		}
		e.TraceID = matchTraceID(e.Environment, e.Service, msg)
	}
}

// InsertUnique 按天分组批量事务写入，并通过全局 event_id 表实现跨日期幂等。
// 返回真正新写入的事件；重复请求同样会被完整 ACK，但不会重复入库或推送实时流。
func (s *Storage) InsertUnique(evs []types.Event) ([]types.Event, error) {
	if len(evs) == 0 {
		return []types.Event{}, nil
	}
	byDay := map[string][]types.Event{}
	for i := range evs {
		Enrich(&evs[i], 256<<10)
		t := time.UnixMilli(evs[i].Ts)
		byDay[dayStr(t)] = append(byDay[dayStr(t)], evs[i])
	}
	accepted := make([]types.Event, 0, len(evs))
	for d, batch := range byDay {
		t, err := time.ParseInLocation("20060102", d, time.Local)
		if err != nil {
			continue
		}
		if err := s.ensureDay(t); err != nil {
			return accepted, err
		}
		name := "logs_" + d
		tx, err := s.db.Begin()
		if err != nil {
			return accepted, err
		}
		stmt, err := tx.Prepare(fmt.Sprintf(
			`INSERT INTO %s (event_id,ts,application_id,service,environment,host,path,level,trace_id,message) VALUES (?,?,?,?,?,?,?,?,?,?)`, name))
		if err != nil {
			tx.Rollback()
			return accepted, err
		}
		dedupe, err := tx.Prepare(`INSERT OR IGNORE INTO ingest_dedupe(event_id,received_at) VALUES (?,?)`)
		if err != nil {
			stmt.Close()
			tx.Rollback()
			return accepted, err
		}
		dayAccepted := make([]types.Event, 0, len(batch))
		for _, e := range batch {
			if e.ID != "" {
				res, err := dedupe.Exec(e.ID, time.Now().UnixMilli())
				if err != nil {
					dedupe.Close()
					stmt.Close()
					tx.Rollback()
					return accepted, err
				}
				changed, _ := res.RowsAffected()
				if changed == 0 {
					continue
				}
			}
			if _, err := stmt.Exec(e.ID, e.Ts, e.ApplicationID, e.Service, e.Environment, e.Host, e.Path, e.Level, e.TraceID, e.Message); err != nil {
				dedupe.Close()
				stmt.Close()
				tx.Rollback()
				return accepted, err
			}
			dayAccepted = append(dayAccepted, e)
		}
		dedupe.Close()
		stmt.Close()
		if err := tx.Commit(); err != nil {
			return accepted, err
		}
		accepted = append(accepted, dayAccepted...)
	}
	return accepted, nil
}

// Insert 保留原有计数接口，供内部调用和兼容测试使用。
func (s *Storage) Insert(evs []types.Event) (int, error) {
	accepted, err := s.InsertUnique(evs)
	return len(accepted), err
}

// ---------- 搜索 ----------

// Search 游标分页搜索：cursor 格式 "ts_rowid"，双键排序保证稳定翻页。
// 从 end 所在天倒序遍历到 start，攒够 limit 即停。host 非空时按机器过滤。
func (s *Storage) Search(service, environment, host, level string, start, end int64, q, traceID, cursor string, limit int) (*types.SearchResponse, error) {
	levelClause, levelArgs := parseLevelFilter(level).SQL()
	parsedQuery, err := ParseLogQuery(q)
	if err != nil {
		return nil, err
	}
	var curTs int64 = -1
	var curRow int64
	if cursor != "" {
		parts := strings.SplitN(cursor, "_", 2)
		if len(parts) == 2 {
			curTs, _ = strconv.ParseInt(parts[0], 10, 64)
			curRow, _ = strconv.ParseInt(parts[1], 10, 64)
		}
	}
	hits := make([]types.Hit, 0, limit)
	need := limit + 1 // 多取一条判断 has_more

	dayEnd := time.UnixMilli(end)
	for d := dayEnd; ; d = d.Add(-24 * time.Hour) {
		lo := start
		dayLo := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, d.Location()).UnixMilli()
		if dayLo > lo {
			lo = dayLo
		}
		hi := end
		dayHi := dayLo + 24*3600*1000 - 1
		if dayHi < hi {
			hi = dayHi
		}
		if lo > hi {
			break // 超出起始天
		}
		name := "logs_" + dayStr(d)
		var exists int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&exists); err != nil || exists == 0 {
			if d.Add(-24*time.Hour).UnixMilli() < start {
				break
			}
			continue
		}
		where := []string{"ts >= ?", "ts <= ?"}
		args := []any{lo, hi}
		if service != "" {
			where = append(where, "service = ?")
			args = append(args, service)
		}
		if environment != "" {
			where = append(where, "environment = ?")
			args = append(args, environment)
		}
		if host != "" {
			where = append(where, "host = ?")
			args = append(args, host)
		}
		if levelClause != "" {
			where = append(where, levelClause)
			args = append(args, levelArgs...)
		}
		if traceID != "" {
			where = append(where, "rowid IN (SELECT rowid FROM "+name+"_fts WHERE "+name+"_fts MATCH ?)")
			args = append(args, ftsQuote(traceID))
			where = append(where, "trace_id = ?")
			args = append(args, traceID)
		}
		if clause, queryArgs := parsedQuery.SQL(name); clause != "" {
			where = append(where, clause)
			args = append(args, queryArgs...)
		}
		if curTs >= 0 {
			where = append(where, "(ts < ? OR (ts = ? AND rowid < ?))")
			args = append(args, curTs, curTs, curRow)
		}
		query := fmt.Sprintf(
			`SELECT event_id,ts,application_id,service,environment,host,path,level,trace_id,message,rowid FROM %s
			 WHERE %s ORDER BY ts DESC, rowid DESC LIMIT %d`,
			name, strings.Join(where, " AND "), need-len(hits))
		rows, err := s.db.Query(query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var h types.Hit
			var rowid int64
			var environment, host, level, trace sql.NullString
			var eventID, path sql.NullString
			if err := rows.Scan(&eventID, &h.Ts, &h.ApplicationID, &h.Service, &environment, &host, &path, &level, &trace, &h.Message, &rowid); err != nil {
				rows.Close()
				return nil, err
			}
			h.EventID, h.Environment, h.Host, h.Path, h.Level, h.TraceID = eventID.String, environment.String, host.String, path.String, level.String, trace.String
			h.RowID = rowid
			hits = append(hits, h)
			if len(hits) >= need {
				break
			}
		}
		rows.Close()
		if len(hits) >= need {
			break
		}
		if d.Add(-24*time.Hour).UnixMilli() < start {
			break
		}
	}
	resp := &types.SearchResponse{Hits: []types.Hit{}}
	if len(hits) > limit {
		resp.Hits = hits[:limit]
		last := resp.Hits[len(resp.Hits)-1]
		resp.NextCursor = fmt.Sprintf("%d_%d", last.Ts, last.RowID)
		resp.HasMore = true
	} else {
		resp.Hits = hits
	}
	return resp, nil
}

// MinuteCount 关键字命中的分钟计数（供 monitor 阈值报警跨插件查询）
type MinuteCount struct {
	Minute int64 `json:"minute"`
	Count  int64 `json:"count"`
}

// CountSeries 按 service+level+关键字（搜索语法）统计分钟序列
func (s *Storage) CountSeries(service, environment, level, q string, start, end int64) ([]MinuteCount, error) {
	levelClause, levelArgs := parseLevelFilter(level).SQL()
	parsedQuery, err := ParseLogQuery(q)
	if err != nil {
		return nil, err
	}
	byMinute := map[int64]int64{}
	for d := time.UnixMilli(end); ; d = d.Add(-24 * time.Hour) {
		dayLo := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, d.Location()).UnixMilli()
		lo, hi := start, end
		if dayLo > lo {
			lo = dayLo
		}
		if dayHi := dayLo + 24*3600*1000 - 1; dayHi < hi {
			hi = dayHi
		}
		if lo > hi {
			break
		}
		name := "logs_" + dayStr(d)
		var exists int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&exists); err != nil || exists == 0 {
			if d.Add(-24*time.Hour).UnixMilli() < start {
				break
			}
			continue
		}
		where := []string{"ts >= ?", "ts <= ?"}
		args := []any{lo, hi}
		if service != "" {
			where = append(where, "service = ?")
			args = append(args, service)
		}
		if environment != "" {
			where = append(where, "environment = ?")
			args = append(args, environment)
		}
		if levelClause != "" {
			where = append(where, levelClause)
			args = append(args, levelArgs...)
		}
		if clause, queryArgs := parsedQuery.SQL(name); clause != "" {
			where = append(where, clause)
			args = append(args, queryArgs...)
		}
		rows, err := s.db.Query(fmt.Sprintf(`SELECT (ts/60000)*60000, COUNT(*) FROM %s WHERE %s GROUP BY 1`,
			name, strings.Join(where, " AND ")), args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var m, n int64
			if rows.Scan(&m, &n) == nil {
				byMinute[m] += n
			}
		}
		rows.Close()
		if d.Add(-24*time.Hour).UnixMilli() < start {
			break
		}
	}
	var out []MinuteCount
	for m, n := range byMinute {
		out = append(out, MinuteCount{m, n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Minute < out[j].Minute })
	return out, nil
}

// ListServices 今日各服务条数
func (s *Storage) ListServices() (map[string]int64, error) {
	name := "logs_" + dayStr(time.Now())
	rows, err := s.db.Query(`SELECT service, COUNT(*) FROM ` + name + ` GROUP BY service ORDER BY 2 DESC`)
	if err != nil {
		return map[string]int64{}, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var svc string
		var n int64
		if rows.Scan(&svc, &n) == nil {
			out[svc] = n
		}
	}
	return out, nil
}

// ListEnvironments 返回保留期内出现过的环境，供规则编辑器使用真实作用域而非自由输入。
func (s *Storage) ListEnvironments(service string) ([]string, error) {
	tables, err := s.logTables()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, name := range tables {
		q := `SELECT DISTINCT environment FROM ` + name + ` WHERE environment <> ''`
		args := []any{}
		if service != "" {
			q += ` AND service=?`
			args = append(args, service)
		}
		rows, err := s.db.Query(q, args...)
		if err != nil {
			continue
		}
		for rows.Next() {
			var environment string
			if rows.Scan(&environment) == nil {
				seen[environment] = true
			}
		}
		rows.Close()
	}
	out := make([]string, 0, len(seen))
	for environment := range seen {
		out = append(out, environment)
	}
	sort.Strings(out)
	return out, nil
}

// DiskUsage 数据目录字节数
func (s *Storage) DiskUsage() int64 { return s.dirSize() }

// Close 关闭数据库
func (s *Storage) Close() error { return s.db.Close() }

// ---------- 维护任务：保留清理 + 磁盘水位 ----------

func (s *Storage) logTables() ([]string, error) {
	rows, err := s.db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'logs_%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil && len(n) == len("logs_20060102") {
			if _, err := time.Parse("20060102", n[5:]); err == nil {
				out = append(out, n)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

func (s *Storage) dropDay(table string) error {
	d := table[5:]
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DROP TABLE IF EXISTS ` + table + `_fts`,
		`DROP TABLE IF EXISTS ` + table,
	} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	_ = d
	log.Printf("[storage] dropped %s (retention/watermark)", table)
	return tx.Commit()
}

func (s *Storage) dirSize() int64 {
	var total int64
	filepath.Walk(s.dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// archiveDay 按天归档：把某天的表导出为独立 db 文件（归档即备份）
func (s *Storage) archiveDay(table string) error {
	d := table[5:]
	archDir := filepath.Join(s.dir, "archive")
	os.MkdirAll(archDir, 0o755)
	archPath := filepath.Join(archDir, d+".db")
	if _, err := os.Stat(archPath); err == nil {
		return nil // 已归档
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.Exec(`ATTACH DATABASE ? AS arch`, archPath); err != nil {
		return err
	}
	defer s.db.Exec(`DETACH DATABASE arch`)
	_, err := s.db.Exec(fmt.Sprintf(`CREATE TABLE arch.%s AS SELECT * FROM main.%s`, table, table))
	if err != nil {
		os.Remove(archPath)
		return err
	}
	log.Printf("[storage] archived %s -> %s", table, archPath)
	return nil
}

// maintenance 每 30 分钟：归档昨天的表；DROP 超期表；磁盘超水位时从最老表开始继续删
func (s *Storage) maintenance() {
	s.archiveDay("logs_" + dayStr(time.Now().Add(-24*time.Hour))) // 启动即补归档
	for {
		_ = s.archiveDay("logs_" + dayStr(time.Now().Add(-24*time.Hour)))
		tables, err := s.logTables()
		if err != nil {
			log.Printf("[storage] list tables: %v", err)
			time.Sleep(30 * time.Minute)
			continue
		}
		cutoff := time.Now().AddDate(0, 0, -s.retentionDays).Format("20060102")
		for _, t := range tables {
			if t[5:] < cutoff {
				_ = s.dropDay(t)
			}
		}
		// 去重键只需覆盖日志保留窗口，避免元数据无限增长。
		_, _ = s.db.Exec(`DELETE FROM ingest_dedupe WHERE received_at < ?`, time.Now().AddDate(0, 0, -s.retentionDays-1).UnixMilli())
		if s.maxDiskBytes > 0 {
			for s.dirSize() > s.maxDiskBytes {
				tables, err = s.logTables()
				if err != nil || len(tables) <= 1 { // 至少保留今天
					break
				}
				if err := s.dropDay(tables[0]); err != nil {
					break
				}
			}
		}
		time.Sleep(30 * time.Minute)
	}
}
