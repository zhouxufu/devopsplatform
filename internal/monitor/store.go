// Package monitor 监控插件：日志衍生指标（首期）→ 机器指标/OTLP（后续）。
// 数据流：logs 插件分钟聚合 → 平台事件总线 → 本插件 /api/events 落库。
package monitor

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

// Store 指标存储（metrics.db，按分钟粒度）
type Store struct {
	db              *monitorDB
	arthasMu        sync.Mutex
	arthas          *arthasConsole
	arthasKeyMu     sync.Mutex
	arthasSecretDir string
	arthasDeployURL string
}

// ConfigureArthasDeploy sets the trusted internal deployment backend, never a browser URL.
func (s *Store) ConfigureArthasDeploy(base string) { s.arthasDeployURL = strings.TrimRight(base, "/") }

func Open(dir string) (*Store, error) {
	return OpenDatabase(dir, DatabaseConfig{Driver: "sqlite"})
}

func OpenDatabase(dir string, cfg DatabaseConfig) (*Store, error) {
	os.MkdirAll(dir, 0o755)
	cfg.Driver = strings.ToLower(strings.TrimSpace(cfg.Driver))
	if cfg.Driver == "" {
		cfg.Driver = "sqlite"
	}
	if cfg.Driver == "sqlite" && strings.TrimSpace(cfg.DSN) == "" {
		cfg.DSN = "file:" + filepath.Join(dir, "metrics.db") + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	}
	db, err := openMonitorDatabase(cfg)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS metrics_minute (
		minute INTEGER NOT NULL, service TEXT NOT NULL, environment TEXT DEFAULT '', level TEXT NOT NULL,
		count INTEGER NOT NULL, PRIMARY KEY(minute, service, environment, level))`); err != nil {
		return nil, err
	}
	columns, err := tableColumns(db, "metrics_minute")
	if err != nil {
		db.Close()
		return nil, err
	}
	if !columns["environment"] {
		tx, err := db.Begin()
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec(`ALTER TABLE metrics_minute RENAME TO metrics_minute_legacy`); err == nil {
			_, err = tx.Exec(`CREATE TABLE metrics_minute (minute INTEGER NOT NULL,service TEXT NOT NULL,environment TEXT DEFAULT '',level TEXT NOT NULL,count INTEGER NOT NULL,PRIMARY KEY(minute,service,environment,level))`)
		}
		if err == nil {
			_, err = tx.Exec(`INSERT INTO metrics_minute(minute,service,environment,level,count) SELECT minute,service,'',level,count FROM metrics_minute_legacy`)
		}
		if err == nil {
			_, err = tx.Exec(`DROP TABLE metrics_minute_legacy`)
		}
		if err != nil {
			tx.Rollback()
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
	}
	for _, ddl := range []string{arthasTargetDDL, arthasAuditDDL, arthasCredentialDDL} {
		if _, err := db.Exec(ddl); err != nil {
			db.Close()
			return nil, err
		}
	}
	return &Store{db: db, arthasSecretDir: filepath.Join(dir, "arthas-secrets")}, nil
}

// Point 一个分钟点的各级别计数（counts 为最终值，重复投递幂等覆盖）
type Point struct {
	Minute      int64            `json:"minute"`
	Service     string           `json:"service"`
	Environment string           `json:"environment,omitempty"`
	Counts      map[string]int64 `json:"counts"`
}

// UpsertPoints 幂等写入（分钟已关闭，count 为终值）
func (s *Store) UpsertPoints(pts []Point) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO metrics_minute(minute,service,environment,level,count) VALUES(?,?,?,?,?)
		ON CONFLICT(minute,service,environment,level) DO UPDATE SET count=excluded.count`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, p := range pts {
		for lv, n := range p.Counts {
			if _, err := stmt.Exec(p.Minute, p.Service, p.Environment, lv, n); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// SeriesPoint 查询结果点
type SeriesPoint struct {
	Minute int64          `json:"minute"`
	Counts map[string]int `json:"counts"`
}

// Query 按服务查区间分钟序列（level 为空 = 全部级别汇总进 counts）
func (s *Store) Query(service, environment string, start, end int64) ([]SeriesPoint, error) {
	rows, err := s.db.Query(`SELECT minute, level, count FROM metrics_minute
		WHERE service = ? AND (?='' OR environment=?) AND minute BETWEEN ? AND ? ORDER BY minute`, service, environment, environment, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	idx := map[int64]*SeriesPoint{}
	var out []SeriesPoint
	for rows.Next() {
		var m, n int64
		var lv string
		if rows.Scan(&m, &lv, &n) != nil {
			continue
		}
		p := idx[m]
		if p == nil {
			p = &SeriesPoint{Minute: m, Counts: map[string]int{}}
			idx[m] = p
			out = append(out, *p)
		}
		p.Counts[lv] = int(n)
	}
	// out 里存的是早期拷贝，重建为最终值
	final := make([]SeriesPoint, 0, len(out))
	for _, m := range sortedKeys(idx) {
		final = append(final, *idx[m])
	}
	return final, nil
}

func sortedKeys(m map[int64]*SeriesPoint) []int64 {
	keys := make([]int64, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// Services 已有指标的服务列表
func (s *Store) Services() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT service FROM metrics_minute ORDER BY service`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if rows.Scan(&s) == nil {
			out = append(out, s)
		}
	}
	return out, nil
}

func (s *Store) Close() error {
	s.arthasMu.Lock()
	if s.arthas != nil {
		s.arthas.close()
		s.arthas = nil
	}
	s.arthasMu.Unlock()
	return s.db.Close()
}

// DB 暴露底层连接（machine/alerts 等模块共用同库）
func (s *Store) DB() *monitorDB { return s.db }

func (s *Store) Driver() string { return s.db.dialect }
