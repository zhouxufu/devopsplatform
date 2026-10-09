package deploy

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

// deployDB keeps the existing store code database/sql-shaped while translating
// the small SQLite-specific part of the query language for MySQL 8.
type deployDB struct {
	*sql.DB
	dialect string
}

type deployTx struct {
	*sql.Tx
	dialect string
}

func (d *deployDB) Exec(query string, args ...any) (sql.Result, error) {
	return d.DB.Exec(rewriteDeploySQL(d.dialect, query), args...)
}

func (d *deployDB) Query(query string, args ...any) (*sql.Rows, error) {
	return d.DB.Query(rewriteDeploySQL(d.dialect, query), args...)
}

func (d *deployDB) QueryRow(query string, args ...any) *sql.Row {
	return d.DB.QueryRow(rewriteDeploySQL(d.dialect, query), args...)
}

func (d *deployDB) Begin() (*deployTx, error) {
	tx, err := d.DB.Begin()
	if err != nil {
		return nil, err
	}
	return &deployTx{Tx: tx, dialect: d.dialect}, nil
}

func (tx *deployTx) Exec(query string, args ...any) (sql.Result, error) {
	return tx.Tx.Exec(rewriteDeploySQL(tx.dialect, query), args...)
}

func (tx *deployTx) Query(query string, args ...any) (*sql.Rows, error) {
	return tx.Tx.Query(rewriteDeploySQL(tx.dialect, query), args...)
}

func (tx *deployTx) QueryRow(query string, args ...any) *sql.Row {
	return tx.Tx.QueryRow(rewriteDeploySQL(tx.dialect, query), args...)
}

var (
	conflictUpdateRE = regexp.MustCompile(`(?is)ON\s+CONFLICT\s*\([^)]*\)\s*(?:WHERE\s+.*?\s+)?DO\s+UPDATE\s+SET`)
	excludedColumnRE = regexp.MustCompile(`(?i)excluded\.([a-z_][a-z0-9_]*)`)
	appendLogRE      = regexp.MustCompile(`(?is)substr\(COALESCE\(log,''\)\s*\|\|\s*char\(10\)\s*\|\|\s*(.*?),\s*-(20000|40000)\)`)
)

func rewriteDeploySQL(dialect, query string) string {
	if dialect != "mysql" {
		return query
	}
	// key and trigger are accepted by SQLite but reserved by MySQL. Keep the
	// public/store model unchanged and map only the two owning tables here.
	query = strings.ReplaceAll(query, "deploy_meta(key,value)", "deploy_meta(meta_key,value)")
	query = strings.ReplaceAll(query, "FROM deploy_meta WHERE key=", "FROM deploy_meta WHERE meta_key=")
	query = strings.ReplaceAll(query, "pipeline_runs(service,trigger,", "pipeline_runs(service,run_trigger,")
	query = strings.ReplaceAll(query, "id,service,trigger,branch", "id,service,run_trigger,branch")
	query = strings.ReplaceAll(query, "strftime('%s','now')", "UNIX_TIMESTAMP()")
	query = strings.ReplaceAll(query, "INSERT OR IGNORE", "INSERT IGNORE")
	query = strings.ReplaceAll(query, "service||char(0)||environment", "CONCAT(service,CHAR(0),environment)")
	query = strings.ReplaceAll(query, "json_extract(deploy_config,'$.connection')", "JSON_UNQUOTE(JSON_EXTRACT(deploy_config,'$.connection'))")
	query = strings.ReplaceAll(query, "json_extract(deploy_config,'$.app_id')", "JSON_UNQUOTE(JSON_EXTRACT(deploy_config,'$.app_id'))")
	query = appendLogRE.ReplaceAllString(query, "RIGHT(CONCAT(COALESCE(log,''),CHAR(10),$1),$2)")
	query = conflictUpdateRE.ReplaceAllString(query, "ON DUPLICATE KEY UPDATE")
	query = excludedColumnRE.ReplaceAllString(query, "VALUES($1)")
	return query
}

func openDeployDatabase(cfg DatabaseConfig) (*deployDB, error) {
	driver := strings.ToLower(strings.TrimSpace(cfg.Driver))
	if driver == "" {
		driver = "sqlite"
	}
	if driver != "sqlite" && driver != "mysql" {
		return nil, fmt.Errorf("unsupported deploy database driver %q (use sqlite or mysql)", driver)
	}
	if strings.TrimSpace(cfg.DSN) == "" {
		return nil, fmt.Errorf("deploy database DSN is required")
	}
	raw, err := sql.Open(driver, cfg.DSN)
	if err != nil {
		return nil, err
	}
	if err := raw.Ping(); err != nil {
		raw.Close()
		return nil, fmt.Errorf("connect deploy database: %w", err)
	}
	db := &deployDB{DB: raw, dialect: driver}
	if driver == "mysql" {
		raw.SetMaxOpenConns(30)
		raw.SetMaxIdleConns(8)
		raw.SetConnMaxLifetime(5 * time.Minute)
		if err := migrateMySQLDeploySchema(db); err != nil {
			raw.Close()
			return nil, err
		}
	} else {
		raw.SetMaxOpenConns(1)
	}
	return db, nil
}

func migrateMySQLDeploySchema(db *deployDB) error {
	for _, ddl := range append(append([]string{}, mysqlDeploySchema...), deliverySchema("mysql")...) {
		if _, err := db.Exec(ddl); err != nil {
			return fmt.Errorf("migrate deploy mysql schema: %w", err)
		}
	}
	for _, column := range []struct{ name, index string }{{"assigned_to", "idx_work_items_assigned"}, {"opened_by", "idx_work_items_opened"}, {"resolved_by", "idx_work_items_resolved"}, {"closed_by", "idx_work_items_closed"}} {
		var exists int
		if err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='work_items' AND COLUMN_NAME=?`, column.name).Scan(&exists); err != nil {
			return fmt.Errorf("inspect work item personnel schema: %w", err)
		}
		if exists == 0 {
			if _, err := db.Exec(`ALTER TABLE work_items ADD COLUMN ` + column.name + ` VARCHAR(191) NOT NULL DEFAULT '', ADD KEY ` + column.index + ` (` + column.name + `,updated_at)`); err != nil {
				return fmt.Errorf("migrate work item personnel: %w", err)
			}
		}
	}
	return (&Store{db: db}).migrateWorkContent()
}

// mysqlDeploySchema is intentionally explicit. It prevents SQLite affinities,
// partial indexes and implicit defaults from leaking into the production
// schema, and provides generated nullable keys for conditional uniqueness.
var mysqlDeploySchema = []string{
	`CREATE TABLE IF NOT EXISTS services (
		name VARCHAR(255) PRIMARY KEY, application_id VARCHAR(64) NOT NULL DEFAULT '',
		git_url TEXT, branch VARCHAR(255) NOT NULL DEFAULT '', workdir TEXT,
		artifact_type VARCHAR(32) NOT NULL DEFAULT 'jar', tags TEXT,
		build_mode VARCHAR(32) NOT NULL DEFAULT 'local', build_host VARCHAR(255) NOT NULL DEFAULT '', build_cmd TEXT,
		build_tool VARCHAR(32) NOT NULL DEFAULT 'command', maven_executable VARCHAR(255) NOT NULL DEFAULT 'auto',
		maven_goals TEXT, maven_profiles TEXT, maven_properties TEXT, maven_settings_path TEXT,
		gradle_executable VARCHAR(255) NOT NULL DEFAULT 'auto', gradle_tasks TEXT, gradle_excluded_tasks TEXT,
		gradle_project_properties TEXT, gradle_system_properties TEXT, artifact_glob TEXT,
		image_name TEXT, pre_cmd TEXT, target_type VARCHAR(32) NOT NULL DEFAULT 'ssh-jar', remote_dir TEXT,
		steps LONGTEXT, start_cmd TEXT, stop_cmd TEXT, health_url TEXT, created_at BIGINT,
		application_id_unique VARCHAR(64) GENERATED ALWAYS AS (NULLIF(application_id,'')) STORED,
		UNIQUE KEY idx_services_application_id(application_id_unique)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS builds (
		id BIGINT PRIMARY KEY AUTO_INCREMENT, service VARCHAR(255), version VARCHAR(255), status VARCHAR(32), artifact TEXT,
		artifact_id BIGINT NOT NULL DEFAULT 0, artifact_kind VARCHAR(64) NOT NULL DEFAULT '', artifact_digest VARCHAR(255) NOT NULL DEFAULT '',
		artifact_size BIGINT NOT NULL DEFAULT 0, pipeline_run_id BIGINT NOT NULL DEFAULT 0, log LONGTEXT, actor VARCHAR(255) NOT NULL DEFAULT '',
		started_at BIGINT, ended_at BIGINT,
		active_service VARCHAR(255) GENERATED ALWAYS AS (IF(status='running',service,NULL)) STORED,
		pipeline_run_unique BIGINT GENERATED ALWAYS AS (IF(pipeline_run_id>0,pipeline_run_id,NULL)) STORED,
		UNIQUE KEY idx_builds_one_running(active_service), UNIQUE KEY idx_builds_pipeline_run(pipeline_run_unique)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS artifacts (
		id BIGINT PRIMARY KEY AUTO_INCREMENT, service VARCHAR(255) NOT NULL, version VARCHAR(255) NOT NULL,
		kind VARCHAR(64) NOT NULL, uri TEXT NOT NULL, digest VARCHAR(255) NOT NULL, size BIGINT NOT NULL DEFAULT 0,
		metadata LONGTEXT, source VARCHAR(64) NOT NULL DEFAULT '', source_id BIGINT NOT NULL DEFAULT 0, created_at BIGINT NOT NULL,
		source_id_unique BIGINT GENERATED ALWAYS AS (IF(source<>'' AND source_id>0,source_id,NULL)) STORED,
		UNIQUE KEY idx_artifacts_source(source,source_id_unique)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS hosts (
		name VARCHAR(255) PRIMARY KEY, addr VARCHAR(512), user VARCHAR(255) NOT NULL DEFAULT 'root', key_path TEXT,
		password LONGTEXT, host_key TEXT, environment VARCHAR(128) NOT NULL DEFAULT ''
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS environments (
		name VARCHAR(128) PRIMARY KEY, display_name VARCHAR(255) NOT NULL, protected TINYINT NOT NULL DEFAULT 0,
		require_reason TINYINT NOT NULL DEFAULT 0, sort_order INT NOT NULL DEFAULT 0, approvers TEXT, created_at BIGINT
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS service_environments (
		service VARCHAR(255) NOT NULL, environment VARCHAR(128) NOT NULL, target_type VARCHAR(32) NOT NULL DEFAULT 'ssh-jar',
		remote_dir TEXT, start_cmd TEXT, stop_cmd TEXT, health_url TEXT, deploy_config LONGTEXT,
		runtime_env_encrypted LONGTEXT, runtime_env_keys TEXT, strategy VARCHAR(32) NOT NULL DEFAULT 'rolling',
		max_parallel INT NOT NULL DEFAULT 1, host_group VARCHAR(255) NOT NULL DEFAULT '', batch_size INT NOT NULL DEFAULT 1,
		pause_between_batches TINYINT NOT NULL DEFAULT 0, failure_threshold INT NOT NULL DEFAULT 0, updated_at BIGINT,
		PRIMARY KEY(service,environment)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS service_environment_hosts (
		service VARCHAR(255) NOT NULL, environment VARCHAR(128) NOT NULL, host VARCHAR(255) NOT NULL,
		sort_order INT NOT NULL DEFAULT 0, PRIMARY KEY(service,environment,host)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS deploy_meta (
		meta_key VARCHAR(255) PRIMARY KEY, value TEXT
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS pipeline_runs (
		id BIGINT PRIMARY KEY AUTO_INCREMENT, service VARCHAR(255), run_trigger VARCHAR(64) NOT NULL DEFAULT 'manual',
		branch VARCHAR(255) NOT NULL DEFAULT '', version VARCHAR(255) NOT NULL DEFAULT '', build_command TEXT, status VARCHAR(32),
		auto_deploy TINYINT NOT NULL DEFAULT 0, deploy_host VARCHAR(255) NOT NULL DEFAULT '', deploy_environment VARCHAR(128) NOT NULL DEFAULT '',
		artifact TEXT, artifact_id BIGINT NOT NULL DEFAULT 0, artifact_kind VARCHAR(64) NOT NULL DEFAULT '', artifact_digest VARCHAR(255) NOT NULL DEFAULT '',
		artifact_size BIGINT NOT NULL DEFAULT 0, stages LONGTEXT, log LONGTEXT, actor VARCHAR(255) NOT NULL DEFAULT '', started_at BIGINT, ended_at BIGINT,
		active_service VARCHAR(255) GENERATED ALWAYS AS (IF(status IN ('running','waiting_deploy'),service,NULL)) STORED,
		UNIQUE KEY idx_pipeline_one_active(active_service)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS deploys (
		id BIGINT PRIMARY KEY AUTO_INCREMENT, service VARCHAR(255), version VARCHAR(255), host VARCHAR(255), environment VARCHAR(128) NOT NULL DEFAULT '',
		artifact_id BIGINT NOT NULL DEFAULT 0, artifact_kind VARCHAR(64) NOT NULL DEFAULT '', artifact_uri TEXT, artifact_digest VARCHAR(255) NOT NULL DEFAULT '',
		external_id VARCHAR(255) NOT NULL DEFAULT '', reason TEXT, status VARCHAR(32), log LONGTEXT, actor VARCHAR(255) NOT NULL DEFAULT '', started_at BIGINT, ended_at BIGINT,
		active_deploy VARCHAR(512) GENERATED ALWAYS AS (IF(status='running',CONCAT(service,CHAR(0),host),NULL)) STORED,
		UNIQUE KEY idx_deploy_one_running(active_deploy)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS resource_connections (
		name VARCHAR(255) PRIMARY KEY, kind VARCHAR(64) NOT NULL, endpoint TEXT, config LONGTEXT, secret LONGTEXT,
		secret_configured TINYINT NOT NULL DEFAULT 0, created_at BIGINT, updated_at BIGINT
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS host_groups (
		name VARCHAR(255) PRIMARY KEY, environment VARCHAR(128) NOT NULL DEFAULT '', description TEXT, created_at BIGINT, updated_at BIGINT
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS host_group_hosts (
		group_name VARCHAR(255) NOT NULL, host VARCHAR(255) NOT NULL, sort_order INT NOT NULL DEFAULT 0, PRIMARY KEY(group_name,host)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS variable_groups (
		name VARCHAR(255) PRIMARY KEY, description TEXT, values_encrypted LONGTEXT, key_names TEXT, created_at BIGINT, updated_at BIGINT
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS build_runners (
		name VARCHAR(255) PRIMARY KEY, kind VARCHAR(64) NOT NULL DEFAULT 'local', labels TEXT, enabled TINYINT NOT NULL DEFAULT 1,
		max_parallel INT NOT NULL DEFAULT 1, created_at BIGINT, updated_at BIGINT
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS service_delivery (
		service VARCHAR(255) PRIMARY KEY, runner VARCHAR(255) NOT NULL DEFAULT 'local', git_connection VARCHAR(255) NOT NULL DEFAULT '',
		maven_connection VARCHAR(255) NOT NULL DEFAULT '', registry_connection VARCHAR(255) NOT NULL DEFAULT '', variable_groups TEXT,
		webhook_token VARCHAR(255) NOT NULL DEFAULT '', schedule_minutes INT NOT NULL DEFAULT 0, auto_deploy_environment VARCHAR(128) NOT NULL DEFAULT '',
		last_schedule_at BIGINT NOT NULL DEFAULT 0, updated_at BIGINT, build_environment LONGTEXT
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS release_orders (
		id BIGINT PRIMARY KEY AUTO_INCREMENT, service VARCHAR(255) NOT NULL, artifact_id BIGINT NOT NULL, version VARCHAR(255) NOT NULL,
		environment VARCHAR(128) NOT NULL, reason TEXT, strategy VARCHAR(32) NOT NULL DEFAULT 'rolling', status VARCHAR(32) NOT NULL,
		batch_size INT NOT NULL DEFAULT 1, pause_between_batches TINYINT NOT NULL DEFAULT 0, failure_threshold INT NOT NULL DEFAULT 0,
		hosts LONGTEXT, target_snapshot LONGTEXT, runtime_env_encrypted LONGTEXT, next_index INT NOT NULL DEFAULT 0,
		total INT NOT NULL DEFAULT 0, succeeded INT NOT NULL DEFAULT 0, failed INT NOT NULL DEFAULT 0,
		actor VARCHAR(255) NOT NULL DEFAULT '', approver VARCHAR(255) NOT NULL DEFAULT '', started_at BIGINT, ended_at BIGINT
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS release_order_deploys (
		release_id BIGINT NOT NULL, deploy_id BIGINT NOT NULL, batch_no INT NOT NULL DEFAULT 1, PRIMARY KEY(release_id,deploy_id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS work_items (
		id BIGINT PRIMARY KEY AUTO_INCREMENT, source VARCHAR(64) NOT NULL DEFAULT 'manual', external_id VARCHAR(255) NOT NULL DEFAULT '',
		kind VARCHAR(64) NOT NULL DEFAULT 'requirement', title VARCHAR(255) NOT NULL, url TEXT, external_status VARCHAR(64) NOT NULL DEFAULT '',
		description TEXT, status VARCHAR(32) NOT NULL DEFAULT 'open', owner VARCHAR(255) NOT NULL DEFAULT '', created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
		assigned_to VARCHAR(191) NOT NULL DEFAULT '', opened_by VARCHAR(191) NOT NULL DEFAULT '', resolved_by VARCHAR(191) NOT NULL DEFAULT '', closed_by VARCHAR(191) NOT NULL DEFAULT '',
		KEY idx_work_items_status(status,updated_at), KEY idx_work_items_assigned(assigned_to,updated_at),
		KEY idx_work_items_opened(opened_by,updated_at), KEY idx_work_items_resolved(resolved_by,updated_at), KEY idx_work_items_closed(closed_by,updated_at)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS changes (
		id BIGINT PRIMARY KEY AUTO_INCREMENT, kind VARCHAR(64) NOT NULL, title VARCHAR(255) NOT NULL, service VARCHAR(255) NOT NULL DEFAULT '',
		reference TEXT, summary TEXT, risk_level VARCHAR(32) NOT NULL DEFAULT 'medium', rollback_plan TEXT, status VARCHAR(32) NOT NULL DEFAULT 'draft',
		owner VARCHAR(255) NOT NULL DEFAULT '', created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
		KEY idx_changes_status(status,updated_at)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS work_item_changes (
		work_item_id BIGINT NOT NULL, change_id BIGINT NOT NULL, PRIMARY KEY(work_item_id,change_id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS release_versions (
		id BIGINT PRIMARY KEY AUTO_INCREMENT, version VARCHAR(255) NOT NULL, title VARCHAR(255) NOT NULL, description TEXT,
		status VARCHAR(32) NOT NULL DEFAULT 'draft', owner VARCHAR(255) NOT NULL DEFAULT '', created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
		KEY idx_release_versions_status(status,updated_at)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS release_version_changes (
		release_version_id BIGINT NOT NULL, change_id BIGINT NOT NULL, sort_order INT NOT NULL DEFAULT 0, PRIMARY KEY(release_version_id,change_id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS release_promotions (
		id BIGINT PRIMARY KEY AUTO_INCREMENT, release_version_id BIGINT NOT NULL, environment VARCHAR(128) NOT NULL,
		status VARCHAR(32) NOT NULL DEFAULT 'draft', actor VARCHAR(255) NOT NULL DEFAULT '', approver VARCHAR(255) NOT NULL DEFAULT '',
		created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL, UNIQUE KEY idx_release_promotion_environment(release_version_id,environment),
		KEY idx_release_promotions_version(release_version_id,id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS application_resource_requirements (
		service VARCHAR(255) NOT NULL, alias VARCHAR(128) NOT NULL, kind VARCHAR(64) NOT NULL, required TINYINT NOT NULL DEFAULT 1,
		description TEXT, PRIMARY KEY(service,alias)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS application_environment_resources (
		service VARCHAR(255) NOT NULL, environment VARCHAR(128) NOT NULL, alias VARCHAR(128) NOT NULL, connection VARCHAR(255) NOT NULL,
		config LONGTEXT, updated_at BIGINT NOT NULL, PRIMARY KEY(service,environment,alias)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS change_executions (
		id BIGINT PRIMARY KEY AUTO_INCREMENT, promotion_id BIGINT NOT NULL, change_id BIGINT NOT NULL, environment VARCHAR(128) NOT NULL,
		connection VARCHAR(255) NOT NULL DEFAULT '', status VARCHAR(32) NOT NULL DEFAULT 'pending', before_digest VARCHAR(255) NOT NULL DEFAULT '',
		after_digest VARCHAR(255) NOT NULL DEFAULT '', log LONGTEXT, started_at BIGINT, ended_at BIGINT,
		KEY idx_change_executions_promotion(promotion_id,id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS audit_events (
		id BIGINT PRIMARY KEY AUTO_INCREMENT, actor VARCHAR(255) NOT NULL DEFAULT '', action VARCHAR(128) NOT NULL,
		resource_type VARCHAR(128) NOT NULL, resource_id VARCHAR(255) NOT NULL DEFAULT '', detail LONGTEXT, created_at BIGINT NOT NULL
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS agent_configs (
		host_name VARCHAR(255) PRIMARY KEY, server_url TEXT NOT NULL, monitor_url TEXT, run_user VARCHAR(255) NOT NULL DEFAULT 'logagent',
		run_group VARCHAR(255) NOT NULL DEFAULT 'logagent', spool_max_mb INT NOT NULL DEFAULT 1024, collect LONGTEXT NOT NULL,
		updated_at BIGINT NOT NULL, applied_at BIGINT NOT NULL DEFAULT 0, last_apply_error TEXT
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
}
