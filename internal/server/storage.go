package server

import (
	"context"
	"database/sql"
	"fmt"
	_ "modernc.org/sqlite"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Storage has one searchable log backend: VictoriaLogs. SQLite holds only
// settings, bounded delivery/replay payloads, quarantine and retry receipts.
type Storage struct {
	db            *sql.DB
	dir           string
	retentionDays int
	opts          StorageOptions
	client        *http.Client
	base          *url.URL
	mu            sync.Mutex
	deliveryMu    sync.Mutex
	healthMu      sync.Mutex
	healthAt      time.Time
	health        vlHealth
	stop          chan struct{}
	stopped       chan struct{}
	wake          chan struct{}
	closeOnce     sync.Once
	ctx           context.Context
	cancel        context.CancelFunc
}

type StorageOptions struct {
	URL            string
	Token          string
	AccountID      string
	ProjectID      string
	RetentionDays  int
	QueueMaxBytes  int64
	ReplayWindow   time.Duration
	RetryWindow    time.Duration
	RequestTimeout time.Duration
}

func DefaultStorageOptions() StorageOptions {
	u := os.Getenv("DEVOPSPLATFORM_VICTORIALOGS_URL")
	if u == "" {
		u = "http://127.0.0.1:9428"
	}
	return StorageOptions{URL: u, Token: os.Getenv("DEVOPSPLATFORM_VICTORIALOGS_TOKEN"), AccountID: os.Getenv("DEVOPSPLATFORM_VICTORIALOGS_ACCOUNT_ID"), ProjectID: os.Getenv("DEVOPSPLATFORM_VICTORIALOGS_PROJECT_ID"), RetentionDays: 7, QueueMaxBytes: 2 << 30, ReplayWindow: time.Hour, RetryWindow: 48 * time.Hour, RequestTimeout: 15 * time.Second}
}
func Open(dir string, retentionDays int, maxDiskGB float64) (*Storage, error) {
	o := DefaultStorageOptions()
	o.RetentionDays = retentionDays
	if maxDiskGB > 0 {
		o.QueueMaxBytes = int64(maxDiskGB * 1024 * 1024 * 1024)
	}
	return OpenVictoriaLogs(dir, o)
}
func OpenVictoriaLogs(dir string, o StorageOptions) (*Storage, error) {
	u, err := url.Parse(o.URL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("VictoriaLogs URL 必须为无凭据、查询参数的 HTTP(S) 地址")
	}
	if o.RetentionDays < 1 || o.QueueMaxBytes < 1<<20 || o.ReplayWindow < time.Minute || o.RetryWindow < o.ReplayWindow || o.RetryWindow > time.Duration(o.RetentionDays)*24*time.Hour || o.RequestTimeout <= 0 {
		return nil, fmt.Errorf("日志保留期、队列容量或回放/重试窗口配置无效")
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "state.db")+"?_pragma=synchronous(FULL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	fail := func(e error) (*Storage, error) { db.Close(); return nil, e }
	if _, err = db.Exec(`PRAGMA auto_vacuum=INCREMENTAL`); err != nil {
		return fail(err)
	}
	if _, err = db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		return fail(err)
	}
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS log_batches (
 id TEXT PRIMARY KEY, source_hash TEXT NOT NULL, payload BLOB NOT NULL, created_at INTEGER NOT NULL,
 sent_at INTEGER NOT NULL DEFAULT 0, checked_at INTEGER NOT NULL DEFAULT 0, visible_at INTEGER NOT NULL DEFAULT 0, attempts INTEGER NOT NULL DEFAULT 0, last_error TEXT NOT NULL DEFAULT '');
 CREATE INDEX IF NOT EXISTS log_batches_delivery ON log_batches(sent_at,checked_at);
 CREATE TABLE IF NOT EXISTS log_batch_receipts (id TEXT PRIMARY KEY,source_hash TEXT NOT NULL,created_at INTEGER NOT NULL);
 CREATE INDEX IF NOT EXISTS log_batch_receipts_expiry ON log_batch_receipts(created_at);
 CREATE TABLE IF NOT EXISTS log_receipts (event_key TEXT PRIMARY KEY, payload_hash TEXT NOT NULL, batch_id TEXT NOT NULL, created_at INTEGER NOT NULL);
 CREATE INDEX IF NOT EXISTS log_receipts_expiry ON log_receipts(created_at);
 CREATE TABLE IF NOT EXISTS log_quarantine (id INTEGER PRIMARY KEY AUTOINCREMENT, batch_id TEXT NOT NULL,event_id TEXT NOT NULL,payload BLOB NOT NULL,reason TEXT NOT NULL,created_at INTEGER NOT NULL,resolved_at INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS log_state (key TEXT PRIMARY KEY,value TEXT NOT NULL);`); err != nil {
		return fail(err)
	}
	var pageSize int64
	if err = db.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
		return fail(err)
	}
	pageLimit := (o.QueueMaxBytes - o.QueueMaxBytes/8) / pageSize
	if _, err = db.Exec(fmt.Sprintf("PRAGMA max_page_count=%d", pageLimit)); err != nil {
		return fail(err)
	}
	// Do not silently reopen an obsolete searchable log store or erase its data.
	(&AlertEngine{db: db}).ensureTables()
	if err = migrateLogSettings(db, dir); err != nil {
		return fail(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Storage{db: db, dir: dir, retentionDays: o.RetentionDays, opts: o, base: u, client: &http.Client{Timeout: o.RequestTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, stop: make(chan struct{}), stopped: make(chan struct{}), wake: make(chan struct{}, 1), ctx: ctx, cancel: cancel}
	go s.deliveryLoop()
	return s, nil
}

// Existing alert configuration is retained once; old log tables are never read.
func migrateLogSettings(db *sql.DB, dir string) error {
	var done string
	if db.QueryRow(`SELECT value FROM log_state WHERE key='settings_imported'`).Scan(&done) == nil {
		return nil
	}
	old := filepath.Join(dir, "logs.db")
	if _, err := os.Stat(old); err == nil {
		if _, err = db.Exec(`ATTACH DATABASE ? AS legacy_settings`, "file:"+old+"?mode=ro"); err != nil {
			return fmt.Errorf("读取旧日志配置失败: %w", err)
		}
		defer db.Exec(`DETACH DATABASE legacy_settings`)
		allowed := map[string][]string{
			"alert_rules":         {"id", "name", "service", "environment", "level", "keyword", "threshold", "duration_min", "webhook", "silence_sec", "suppress_by", "silence_until", "enabled"},
			"alert_notifications": {"id", "rule_id", "rule", "environment", "kind", "at", "detail"},
		}
		for _, table := range []string{"alert_rules", "alert_notifications"} {
			var exists, n int
			if err = db.QueryRow(`SELECT COUNT(*) FROM legacy_settings.sqlite_master WHERE type='table' AND name=?`, table).Scan(&exists); err != nil {
				return err
			}
			if exists == 0 {
				continue
			}
			if err = db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				continue
			}
			rows, e := db.Query(`PRAGMA legacy_settings.table_info(` + table + `)`)
			if e != nil {
				return e
			}
			found := map[string]bool{}
			for rows.Next() {
				var id, notnull, pk int
				var name, kind string
				var def any
				if e = rows.Scan(&id, &name, &kind, &notnull, &def, &pk); e != nil {
					rows.Close()
					return e
				}
				found[name] = true
			}
			rows.Close()
			columns := []string{}
			for _, col := range allowed[table] {
				if found[col] {
					columns = append(columns, col)
				}
			}
			names := strings.Join(columns, ",")
			if len(columns) > 0 {
				if _, err = db.Exec(`INSERT INTO ` + table + ` (` + names + `) SELECT ` + names + ` FROM legacy_settings.` + table); err != nil {
					return err
				}
			}
		}
	}
	_, err := db.Exec(`INSERT OR IGNORE INTO log_state(key,value) VALUES('settings_imported','1')`)
	return err
}
func (s *Storage) Close() error {
	s.closeOnce.Do(func() { s.cancel(); close(s.stop) })
	<-s.stopped
	return s.db.Close()
}

// DiskUsage counts only the current queue/settings database and its journals.
// Retained legacy logs and backups are not active delivery-queue occupancy.
func (s *Storage) DiskUsage() int64 {
	var n int64
	for _, name := range []string{"state.db", "state.db-wal", "state.db-shm"} {
		if info, err := os.Stat(filepath.Join(s.dir, name)); err == nil && !info.IsDir() {
			n += info.Size()
		}
	}
	return n
}
