package monitor

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

type DatabaseConfig struct {
	Driver string
	DSN    string
}

type monitorDB struct {
	*sql.DB
	dialect string
}

type monitorTx struct {
	*sql.Tx
	dialect string
}

func (d *monitorDB) Exec(query string, args ...any) (sql.Result, error) {
	return d.DB.Exec(rewriteMonitorSQL(d.dialect, query), args...)
}

func (d *monitorDB) Query(query string, args ...any) (*sql.Rows, error) {
	return d.DB.Query(rewriteMonitorSQL(d.dialect, query), args...)
}

func (d *monitorDB) QueryRow(query string, args ...any) *sql.Row {
	return d.DB.QueryRow(rewriteMonitorSQL(d.dialect, query), args...)
}

func (d *monitorDB) Prepare(query string) (*sql.Stmt, error) {
	return d.DB.Prepare(rewriteMonitorSQL(d.dialect, query))
}

func (d *monitorDB) Begin() (*monitorTx, error) {
	tx, err := d.DB.Begin()
	if err != nil {
		return nil, err
	}
	return &monitorTx{Tx: tx, dialect: d.dialect}, nil
}

func (tx *monitorTx) Exec(query string, args ...any) (sql.Result, error) {
	return tx.Tx.Exec(rewriteMonitorSQL(tx.dialect, query), args...)
}

func (tx *monitorTx) Query(query string, args ...any) (*sql.Rows, error) {
	return tx.Tx.Query(rewriteMonitorSQL(tx.dialect, query), args...)
}

func (tx *monitorTx) QueryRow(query string, args ...any) *sql.Row {
	return tx.Tx.QueryRow(rewriteMonitorSQL(tx.dialect, query), args...)
}

func (tx *monitorTx) Prepare(query string) (*sql.Stmt, error) {
	return tx.Tx.Prepare(rewriteMonitorSQL(tx.dialect, query))
}

var (
	monitorConflictUpdateRE = regexp.MustCompile(`(?is)ON\s+CONFLICT\s*\([^)]*\)\s*(?:WHERE\s+.*?\s+)?DO\s+UPDATE\s+SET`)
	monitorExcludedRE       = regexp.MustCompile(`(?i)excluded\.([a-z_][a-z0-9_]*)`)
)

func rewriteMonitorSQL(dialect, query string) string {
	if dialect != "mysql" {
		return query
	}
	trimmed := strings.ToUpper(strings.TrimSpace(query))
	// MySQL tables and indexes are created by the explicit schema below. The
	// remaining CREATE/ALTER statements are SQLite upgrade paths and therefore
	// must not be parsed by MySQL.
	if strings.HasPrefix(trimmed, "CREATE TABLE") || strings.HasPrefix(trimmed, "CREATE INDEX") || strings.HasPrefix(trimmed, "ALTER TABLE") {
		return "DO 1"
	}
	if strings.HasPrefix(trimmed, "INSERT INTO MACHINE(") && !strings.Contains(trimmed, "ON DUPLICATE KEY") {
		query += ` ON DUPLICATE KEY UPDATE cpu=VALUES(cpu),mem_used=VALUES(mem_used),mem_total=VALUES(mem_total),disk_used=VALUES(disk_used),disk_total=VALUES(disk_total),net_rx=VALUES(net_rx),net_tx=VALUES(net_tx)`
	}
	query = monitorConflictUpdateRE.ReplaceAllString(query, "ON DUPLICATE KEY UPDATE")
	query = monitorExcludedRE.ReplaceAllString(query, "VALUES($1)")
	return query
}

func openMonitorDatabase(cfg DatabaseConfig) (*monitorDB, error) {
	driver := strings.ToLower(strings.TrimSpace(cfg.Driver))
	if driver == "" {
		driver = "sqlite"
	}
	if driver != "sqlite" && driver != "mysql" {
		return nil, fmt.Errorf("unsupported monitor database driver %q (use sqlite or mysql)", driver)
	}
	if strings.TrimSpace(cfg.DSN) == "" {
		return nil, fmt.Errorf("monitor database DSN is required")
	}
	raw, err := sql.Open(driver, cfg.DSN)
	if err != nil {
		return nil, err
	}
	if err := raw.Ping(); err != nil {
		raw.Close()
		return nil, fmt.Errorf("connect monitor database: %w", err)
	}
	if driver == "mysql" {
		raw.SetMaxOpenConns(40)
		raw.SetMaxIdleConns(10)
		raw.SetConnMaxLifetime(5 * time.Minute)
		for _, ddl := range mysqlMonitorSchema {
			if _, err := raw.Exec(ddl); err != nil {
				raw.Close()
				return nil, fmt.Errorf("migrate monitor mysql schema: %w", err)
			}
		}
	} else {
		raw.SetMaxOpenConns(1)
	}
	return &monitorDB{DB: raw, dialect: driver}, nil
}

var mysqlMonitorSchema = []string{
	arthasCredentialDDL + ` ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
	arthasTargetDDL + ` ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
	arthasAuditDDL + ` ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
	`CREATE TABLE IF NOT EXISTS metrics_minute (
		minute BIGINT NOT NULL, service VARCHAR(128) NOT NULL, environment VARCHAR(64) NOT NULL DEFAULT '', level VARCHAR(16) NOT NULL, count BIGINT NOT NULL,
		PRIMARY KEY(minute,service,environment,level), KEY idx_metrics_service(service,environment,minute)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
	`CREATE TABLE IF NOT EXISTS machine (
		ts BIGINT NOT NULL, agent VARCHAR(191) NOT NULL, environment VARCHAR(64) NOT NULL DEFAULT '',
		cpu DOUBLE, mem_used DOUBLE, mem_total DOUBLE, disk_used DOUBLE, disk_total DOUBLE, net_rx DOUBLE, net_tx DOUBLE,
		UNIQUE KEY idx_machine_sample(agent,environment,ts), KEY idx_machine_environment(environment,agent,ts), KEY idx_machine(agent,ts)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
	`CREATE TABLE IF NOT EXISTS spans (
		trace_id CHAR(32) NOT NULL, span_id CHAR(16) NOT NULL, parent_id CHAR(16) NOT NULL DEFAULT '',
		service VARCHAR(128), environment VARCHAR(64) NOT NULL DEFAULT '', instance VARCHAR(128) NOT NULL DEFAULT '', name TEXT,
		start_ms BIGINT, dur_ms BIGINT, status INT NOT NULL DEFAULT 0, attrs LONGTEXT NOT NULL DEFAULT (''), err TEXT NOT NULL DEFAULT (''),
		PRIMARY KEY(environment,trace_id,span_id), KEY idx_spans_trace(trace_id), KEY idx_spans_start(start_ms),
		KEY idx_spans_environment_start(environment,start_ms), KEY idx_spans_instance_start(environment,instance,start_ms)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
	`CREATE TABLE IF NOT EXISTS otlp_minute (
		minute BIGINT NOT NULL, service VARCHAR(128) NOT NULL, environment VARCHAR(64) NOT NULL DEFAULT '', instance VARCHAR(128) NOT NULL DEFAULT '', name VARCHAR(128) NOT NULL,
		val DOUBLE NOT NULL DEFAULT 0, cnt BIGINT NOT NULL DEFAULT 0,
		PRIMARY KEY(minute,service,environment,instance,name), KEY idx_otlp_minute_lookup(service,environment,instance,name,minute)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
	`CREATE TABLE IF NOT EXISTS otlp_hist (
		minute BIGINT NOT NULL, service VARCHAR(128) NOT NULL, environment VARCHAR(64) NOT NULL DEFAULT '', instance VARCHAR(128) NOT NULL DEFAULT '', name VARCHAR(128) NOT NULL,
		iface VARCHAR(128) NOT NULL DEFAULT '', meth VARCHAR(32) NOT NULL DEFAULT '', status VARCHAR(64) NOT NULL DEFAULT '',
		cnt DOUBLE NOT NULL DEFAULT 0, sum DOUBLE NOT NULL DEFAULT 0, bounds LONGTEXT NOT NULL DEFAULT ('[]'), buckets LONGTEXT NOT NULL DEFAULT ('[]'),
		PRIMARY KEY(minute,service,environment,instance,name,iface,meth,status), KEY idx_otlp_hist_lookup(service,environment,instance,name,iface,minute)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
	`CREATE TABLE IF NOT EXISTS otlp_counter (
		minute BIGINT NOT NULL, service VARCHAR(128) NOT NULL, environment VARCHAR(64) NOT NULL DEFAULT '', instance VARCHAR(128) NOT NULL DEFAULT '', name VARCHAR(128) NOT NULL,
		iface VARCHAR(128) NOT NULL DEFAULT '', meth VARCHAR(32) NOT NULL DEFAULT '', status VARCHAR(64) NOT NULL DEFAULT '', val DOUBLE NOT NULL DEFAULT 0, cnt BIGINT NOT NULL DEFAULT 0,
		PRIMARY KEY(minute,service,environment,instance,name,iface,meth,status), KEY idx_otlp_counter_lookup(service,environment,instance,name,iface,minute)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
	`CREATE TABLE IF NOT EXISTS avail_rules (
		service VARCHAR(128) NOT NULL, environment VARCHAR(64) NOT NULL DEFAULT '', name VARCHAR(128) NOT NULL DEFAULT '', ok_codes LONGTEXT NOT NULL DEFAULT ('[]'), ignore_codes LONGTEXT NOT NULL DEFAULT ('[]'),
		PRIMARY KEY(service,environment,name)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
	`CREATE TABLE IF NOT EXISTS metric_alert_rules (
		id BIGINT PRIMARY KEY AUTO_INCREMENT, name VARCHAR(255) NOT NULL, service VARCHAR(128) NOT NULL,
		environment VARCHAR(64) NOT NULL DEFAULT '', instance VARCHAR(128) NOT NULL DEFAULT '', metric VARCHAR(128) NOT NULL, iface VARCHAR(128) NOT NULL DEFAULT '',
		type VARCHAR(32) NOT NULL, quantile DOUBLE NOT NULL DEFAULT 0.99, window_min INT NOT NULL, threshold DOUBLE NOT NULL, min_sample DOUBLE NOT NULL DEFAULT 10,
		webhook TEXT NOT NULL, silence_sec INT NOT NULL DEFAULT 300, suppress_by TEXT NOT NULL DEFAULT (''), silence_until BIGINT NOT NULL DEFAULT 0, enabled TINYINT NOT NULL DEFAULT 1
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
	`CREATE TABLE IF NOT EXISTS metric_alert_notifications (
		id BIGINT PRIMARY KEY AUTO_INCREMENT, rule_id BIGINT, rule VARCHAR(255), environment VARCHAR(64) NOT NULL DEFAULT '*', kind VARCHAR(64), at BIGINT, detail LONGTEXT,
		KEY idx_metric_alert_notifications_at(at), KEY idx_metric_alert_notifications_rule(rule_id,at)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
}
