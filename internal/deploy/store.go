// Package deploy 提供步骤化构建、不可变制品、环境发布与回滚。
// 与其他插件同契约：manifest 接入、双监听、独立 deploy.db。
package deploy

import (
	"crypto/aes"
	"crypto/cipher"
	crand "crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// Store deploy 数据（服务配置/构建历史/构建日志/制品）
type Store struct {
	db        *deployDB
	secretKey []byte
	dataDir   string
}

var ErrRunConflict = errors.New("operation already running")

func Open(dir string) (*Store, error) {
	return OpenDatabase(dir, DatabaseConfig{Driver: "sqlite"})
}

// OpenDatabase opens the deployment control-plane store. The data directory
// continues to hold workspaces, artifacts and the credential encryption key;
// relational metadata can live in SQLite (development) or MySQL 8 (production).
func OpenDatabase(dir string, cfg DatabaseConfig) (*Store, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	dir = absDir
	os.MkdirAll(dir, 0o755)
	cfg.Driver = strings.ToLower(strings.TrimSpace(cfg.Driver))
	if cfg.Driver == "" {
		cfg.Driver = "sqlite"
	}
	if cfg.Driver == "sqlite" && strings.TrimSpace(cfg.DSN) == "" {
		cfg.DSN = "file:" + filepath.Join(dir, "deploy.db") + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	}
	db, err := openDeployDatabase(cfg)
	if err != nil {
		return nil, err
	}
	if db.dialect == "sqlite" {
		for _, q := range []string{
			`CREATE TABLE IF NOT EXISTS services (
			name TEXT PRIMARY KEY, application_id TEXT DEFAULT '', git_url TEXT DEFAULT '', branch TEXT DEFAULT '', workdir TEXT DEFAULT '',
			artifact_type TEXT DEFAULT 'jar', tags TEXT DEFAULT '[]', build_mode TEXT DEFAULT 'local',
			build_host TEXT DEFAULT '', build_cmd TEXT DEFAULT '',
			build_tool TEXT DEFAULT 'command', maven_executable TEXT DEFAULT 'auto',
			maven_goals TEXT DEFAULT 'clean package', maven_profiles TEXT DEFAULT '',
			maven_properties TEXT DEFAULT '', maven_settings_path TEXT DEFAULT '',
			gradle_executable TEXT DEFAULT 'auto', gradle_tasks TEXT DEFAULT 'clean build',
			gradle_excluded_tasks TEXT DEFAULT '', gradle_project_properties TEXT DEFAULT '',
			gradle_system_properties TEXT DEFAULT '',
			artifact_glob TEXT DEFAULT 'build/libs/*.jar',
			image_name TEXT DEFAULT '', pre_cmd TEXT DEFAULT '',
			target_type TEXT DEFAULT 'ssh-jar', remote_dir TEXT DEFAULT '', steps TEXT DEFAULT '[]',
			start_cmd TEXT DEFAULT '', stop_cmd TEXT DEFAULT '', health_url TEXT DEFAULT '',
			created_at INTEGER)`,
			`CREATE TABLE IF NOT EXISTS builds (
			id INTEGER PRIMARY KEY AUTOINCREMENT, service TEXT, version TEXT,
			status TEXT, artifact TEXT, artifact_id INTEGER DEFAULT 0, artifact_kind TEXT DEFAULT '',
			artifact_digest TEXT DEFAULT '', artifact_size INTEGER DEFAULT 0, pipeline_run_id INTEGER DEFAULT 0,
			log TEXT DEFAULT '', actor TEXT DEFAULT '',
			started_at INTEGER, ended_at INTEGER)`,
			`CREATE TABLE IF NOT EXISTS artifacts (
			id INTEGER PRIMARY KEY AUTOINCREMENT, service TEXT NOT NULL, version TEXT NOT NULL,
			kind TEXT NOT NULL, uri TEXT NOT NULL, digest TEXT NOT NULL, size INTEGER DEFAULT 0,
			metadata TEXT DEFAULT '{}', source TEXT DEFAULT '', source_id INTEGER DEFAULT 0, created_at INTEGER NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS hosts (
			name TEXT PRIMARY KEY, addr TEXT, user TEXT DEFAULT 'root',
			key_path TEXT DEFAULT '', password TEXT DEFAULT '', host_key TEXT DEFAULT '', environment TEXT DEFAULT '')`,
			`CREATE TABLE IF NOT EXISTS environments (
			name TEXT PRIMARY KEY, display_name TEXT NOT NULL, protected INTEGER DEFAULT 0,
			require_reason INTEGER DEFAULT 0, sort_order INTEGER DEFAULT 0, created_at INTEGER)`,
			`CREATE TABLE IF NOT EXISTS service_environments (
			service TEXT NOT NULL, environment TEXT NOT NULL, target_type TEXT DEFAULT 'ssh-jar',
			remote_dir TEXT DEFAULT '', start_cmd TEXT DEFAULT '', stop_cmd TEXT DEFAULT '', health_url TEXT DEFAULT '', deploy_config TEXT DEFAULT '{}',
			runtime_env_encrypted TEXT DEFAULT '', runtime_env_keys TEXT DEFAULT '[]',
			strategy TEXT DEFAULT 'rolling', max_parallel INTEGER DEFAULT 1, updated_at INTEGER,
			PRIMARY KEY(service,environment))`,
			`CREATE TABLE IF NOT EXISTS service_environment_hosts (
			service TEXT NOT NULL, environment TEXT NOT NULL, host TEXT NOT NULL, sort_order INTEGER DEFAULT 0,
			PRIMARY KEY(service,environment,host))`,
			`CREATE TABLE IF NOT EXISTS deploy_meta (key TEXT PRIMARY KEY, value TEXT DEFAULT '')`,
			`CREATE TABLE IF NOT EXISTS pipeline_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT, service TEXT, trigger TEXT DEFAULT 'manual',
			branch TEXT DEFAULT '', version TEXT DEFAULT '', build_command TEXT DEFAULT '', status TEXT,
			auto_deploy INTEGER DEFAULT 0, deploy_host TEXT DEFAULT '', deploy_environment TEXT DEFAULT '', artifact TEXT DEFAULT '',
			artifact_id INTEGER DEFAULT 0, artifact_kind TEXT DEFAULT '', artifact_digest TEXT DEFAULT '', artifact_size INTEGER DEFAULT 0,
			stages TEXT DEFAULT '[]', log TEXT DEFAULT '', actor TEXT DEFAULT '', started_at INTEGER, ended_at INTEGER)`,
			`CREATE TABLE IF NOT EXISTS deploys (
			id INTEGER PRIMARY KEY AUTOINCREMENT, service TEXT, version TEXT, host TEXT,
			environment TEXT DEFAULT '', artifact_id INTEGER DEFAULT 0, artifact_kind TEXT DEFAULT '', artifact_uri TEXT DEFAULT '', artifact_digest TEXT DEFAULT '', external_id TEXT DEFAULT '', reason TEXT DEFAULT '',
			status TEXT, log TEXT DEFAULT '', actor TEXT DEFAULT '', started_at INTEGER, ended_at INTEGER)`,
			`CREATE TABLE IF NOT EXISTS resource_connections (
			name TEXT PRIMARY KEY, kind TEXT NOT NULL, endpoint TEXT DEFAULT '', config TEXT DEFAULT '{}',
			secret TEXT DEFAULT '', secret_configured INTEGER DEFAULT 0, created_at INTEGER, updated_at INTEGER)`,
			`CREATE TABLE IF NOT EXISTS host_groups (
			name TEXT PRIMARY KEY, environment TEXT DEFAULT '', description TEXT DEFAULT '', created_at INTEGER, updated_at INTEGER)`,
			`CREATE TABLE IF NOT EXISTS host_group_hosts (
			group_name TEXT NOT NULL, host TEXT NOT NULL, sort_order INTEGER DEFAULT 0,
			PRIMARY KEY(group_name,host))`,
			`CREATE TABLE IF NOT EXISTS variable_groups (
			name TEXT PRIMARY KEY, description TEXT DEFAULT '', values_encrypted TEXT DEFAULT '', key_names TEXT DEFAULT '[]', created_at INTEGER, updated_at INTEGER)`,
			`CREATE TABLE IF NOT EXISTS build_runners (
			name TEXT PRIMARY KEY, kind TEXT DEFAULT 'local', labels TEXT DEFAULT '', enabled INTEGER DEFAULT 1,
			max_parallel INTEGER DEFAULT 1, created_at INTEGER, updated_at INTEGER)`,
			`CREATE TABLE IF NOT EXISTS service_delivery (
			service TEXT PRIMARY KEY, runner TEXT DEFAULT 'local', git_connection TEXT DEFAULT '', maven_connection TEXT DEFAULT '', registry_connection TEXT DEFAULT '',
			variable_groups TEXT DEFAULT '[]', webhook_token TEXT DEFAULT '', schedule_minutes INTEGER DEFAULT 0,
			auto_deploy_environment TEXT DEFAULT '', last_schedule_at INTEGER DEFAULT 0, updated_at INTEGER)`,
			`CREATE TABLE IF NOT EXISTS release_orders (
			id INTEGER PRIMARY KEY AUTOINCREMENT, service TEXT NOT NULL, artifact_id INTEGER NOT NULL, version TEXT NOT NULL,
			environment TEXT NOT NULL, reason TEXT DEFAULT '', strategy TEXT DEFAULT 'rolling', status TEXT NOT NULL,
			batch_size INTEGER DEFAULT 1, pause_between_batches INTEGER DEFAULT 0, failure_threshold INTEGER DEFAULT 0,
			hosts TEXT DEFAULT '[]', target_snapshot TEXT DEFAULT '{}', runtime_env_encrypted TEXT DEFAULT '', next_index INTEGER DEFAULT 0, total INTEGER DEFAULT 0, succeeded INTEGER DEFAULT 0, failed INTEGER DEFAULT 0,
			actor TEXT DEFAULT '', approver TEXT DEFAULT '', started_at INTEGER, ended_at INTEGER)`,
			`CREATE TABLE IF NOT EXISTS release_order_deploys (
				release_id INTEGER NOT NULL, deploy_id INTEGER NOT NULL, batch_no INTEGER DEFAULT 1,
				PRIMARY KEY(release_id,deploy_id))`,
			`CREATE TABLE IF NOT EXISTS work_items (
				id INTEGER PRIMARY KEY AUTOINCREMENT, source TEXT NOT NULL DEFAULT 'manual', external_id TEXT DEFAULT '',
				kind TEXT NOT NULL DEFAULT 'requirement', title TEXT NOT NULL, url TEXT DEFAULT '', external_status TEXT DEFAULT '',
				description TEXT DEFAULT '', status TEXT NOT NULL DEFAULT 'open', owner TEXT DEFAULT '', created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS changes (
				id INTEGER PRIMARY KEY AUTOINCREMENT, kind TEXT NOT NULL, title TEXT NOT NULL, service TEXT DEFAULT '', reference TEXT DEFAULT '',
				summary TEXT DEFAULT '', risk_level TEXT NOT NULL DEFAULT 'medium', rollback_plan TEXT DEFAULT '', status TEXT NOT NULL DEFAULT 'draft',
				owner TEXT DEFAULT '', created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS work_item_changes (
				work_item_id INTEGER NOT NULL, change_id INTEGER NOT NULL, PRIMARY KEY(work_item_id,change_id))`,
			`CREATE TABLE IF NOT EXISTS release_versions (
				id INTEGER PRIMARY KEY AUTOINCREMENT, version TEXT NOT NULL, title TEXT NOT NULL, description TEXT DEFAULT '',
				status TEXT NOT NULL DEFAULT 'draft', owner TEXT DEFAULT '', created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS release_version_changes (
				release_version_id INTEGER NOT NULL, change_id INTEGER NOT NULL, sort_order INTEGER NOT NULL DEFAULT 0,
				PRIMARY KEY(release_version_id,change_id))`,
			`CREATE TABLE IF NOT EXISTS release_promotions (
				id INTEGER PRIMARY KEY AUTOINCREMENT, release_version_id INTEGER NOT NULL, environment TEXT NOT NULL,
				status TEXT NOT NULL DEFAULT 'draft', actor TEXT DEFAULT '', approver TEXT DEFAULT '', created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
				UNIQUE(release_version_id,environment))`,
			`CREATE TABLE IF NOT EXISTS application_resource_requirements (
				service TEXT NOT NULL, alias TEXT NOT NULL, kind TEXT NOT NULL, required INTEGER NOT NULL DEFAULT 1,
				description TEXT DEFAULT '', PRIMARY KEY(service,alias))`,
			`CREATE TABLE IF NOT EXISTS application_environment_resources (
				service TEXT NOT NULL, environment TEXT NOT NULL, alias TEXT NOT NULL, connection TEXT NOT NULL,
				config TEXT DEFAULT '{}', updated_at INTEGER NOT NULL, PRIMARY KEY(service,environment,alias))`,
			`CREATE TABLE IF NOT EXISTS change_executions (
				id INTEGER PRIMARY KEY AUTOINCREMENT, promotion_id INTEGER NOT NULL, change_id INTEGER NOT NULL, environment TEXT NOT NULL,
				connection TEXT DEFAULT '', status TEXT NOT NULL DEFAULT 'pending', before_digest TEXT DEFAULT '', after_digest TEXT DEFAULT '',
				log TEXT DEFAULT '', started_at INTEGER, ended_at INTEGER)`,
			`CREATE TABLE IF NOT EXISTS audit_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT, actor TEXT DEFAULT '', action TEXT NOT NULL, resource_type TEXT NOT NULL,
			resource_id TEXT DEFAULT '', detail TEXT DEFAULT '{}', created_at INTEGER NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS agent_configs (
			host_name TEXT PRIMARY KEY, server_url TEXT NOT NULL, monitor_url TEXT DEFAULT '',
			run_user TEXT DEFAULT 'logagent', run_group TEXT DEFAULT 'logagent', spool_max_mb INTEGER DEFAULT 1024,
			collect TEXT NOT NULL DEFAULT '[]', updated_at INTEGER NOT NULL, applied_at INTEGER DEFAULT 0,
			last_apply_error TEXT DEFAULT '')`,
		} {
			if _, err := db.Exec(q); err != nil {
				db.Close()
				return nil, err
			}
		}
		// 幂等列迁移（旧库升级）
		for col, ddl := range map[string]string{
			"branch":                    `ALTER TABLE services ADD COLUMN branch TEXT DEFAULT ''`,
			"steps":                     `ALTER TABLE services ADD COLUMN steps TEXT DEFAULT '[]'`,
			"target_type":               `ALTER TABLE services ADD COLUMN target_type TEXT DEFAULT 'ssh-jar'`,
			"remote_dir":                `ALTER TABLE services ADD COLUMN remote_dir TEXT DEFAULT ''`,
			"start_cmd":                 `ALTER TABLE services ADD COLUMN start_cmd TEXT DEFAULT ''`,
			"stop_cmd":                  `ALTER TABLE services ADD COLUMN stop_cmd TEXT DEFAULT ''`,
			"health_url":                `ALTER TABLE services ADD COLUMN health_url TEXT DEFAULT ''`,
			"build_tool":                `ALTER TABLE services ADD COLUMN build_tool TEXT DEFAULT 'command'`,
			"maven_executable":          `ALTER TABLE services ADD COLUMN maven_executable TEXT DEFAULT 'auto'`,
			"maven_goals":               `ALTER TABLE services ADD COLUMN maven_goals TEXT DEFAULT 'clean package'`,
			"maven_profiles":            `ALTER TABLE services ADD COLUMN maven_profiles TEXT DEFAULT ''`,
			"maven_properties":          `ALTER TABLE services ADD COLUMN maven_properties TEXT DEFAULT ''`,
			"maven_settings_path":       `ALTER TABLE services ADD COLUMN maven_settings_path TEXT DEFAULT ''`,
			"gradle_executable":         `ALTER TABLE services ADD COLUMN gradle_executable TEXT DEFAULT 'auto'`,
			"gradle_tasks":              `ALTER TABLE services ADD COLUMN gradle_tasks TEXT DEFAULT 'clean build'`,
			"gradle_excluded_tasks":     `ALTER TABLE services ADD COLUMN gradle_excluded_tasks TEXT DEFAULT ''`,
			"gradle_project_properties": `ALTER TABLE services ADD COLUMN gradle_project_properties TEXT DEFAULT ''`,
			"gradle_system_properties":  `ALTER TABLE services ADD COLUMN gradle_system_properties TEXT DEFAULT ''`,
			"tags":                      `ALTER TABLE services ADD COLUMN tags TEXT DEFAULT '[]'`,
		} {
			if _, err := db.Exec(`SELECT ` + col + ` FROM services LIMIT 1`); err != nil {
				db.Exec(ddl)
			}
		}
		for _, m := range []struct{ table, col, ddl string }{
			{"services", "application_id", `ALTER TABLE services ADD COLUMN application_id TEXT DEFAULT ''`},
			{"hosts", "host_key", `ALTER TABLE hosts ADD COLUMN host_key TEXT DEFAULT ''`},
			{"hosts", "environment", `ALTER TABLE hosts ADD COLUMN environment TEXT DEFAULT ''`},
			{"host_groups", "environment", `ALTER TABLE host_groups ADD COLUMN environment TEXT DEFAULT ''`},
			{"builds", "actor", `ALTER TABLE builds ADD COLUMN actor TEXT DEFAULT ''`},
			{"builds", "artifact_digest", `ALTER TABLE builds ADD COLUMN artifact_digest TEXT DEFAULT ''`},
			{"builds", "artifact_size", `ALTER TABLE builds ADD COLUMN artifact_size INTEGER DEFAULT 0`},
			{"builds", "artifact_id", `ALTER TABLE builds ADD COLUMN artifact_id INTEGER DEFAULT 0`},
			{"builds", "artifact_kind", `ALTER TABLE builds ADD COLUMN artifact_kind TEXT DEFAULT ''`},
			{"builds", "pipeline_run_id", `ALTER TABLE builds ADD COLUMN pipeline_run_id INTEGER DEFAULT 0`},
			{"service_environments", "deploy_config", `ALTER TABLE service_environments ADD COLUMN deploy_config TEXT DEFAULT '{}'`},
			{"service_environments", "runtime_env_encrypted", `ALTER TABLE service_environments ADD COLUMN runtime_env_encrypted TEXT DEFAULT ''`},
			{"service_environments", "runtime_env_keys", `ALTER TABLE service_environments ADD COLUMN runtime_env_keys TEXT DEFAULT '[]'`},
			{"pipeline_runs", "actor", `ALTER TABLE pipeline_runs ADD COLUMN actor TEXT DEFAULT ''`},
			{"pipeline_runs", "deploy_environment", `ALTER TABLE pipeline_runs ADD COLUMN deploy_environment TEXT DEFAULT ''`},
			{"pipeline_runs", "build_command", `ALTER TABLE pipeline_runs ADD COLUMN build_command TEXT DEFAULT ''`},
			{"pipeline_runs", "artifact_id", `ALTER TABLE pipeline_runs ADD COLUMN artifact_id INTEGER DEFAULT 0`},
			{"pipeline_runs", "artifact_kind", `ALTER TABLE pipeline_runs ADD COLUMN artifact_kind TEXT DEFAULT ''`},
			{"pipeline_runs", "artifact_digest", `ALTER TABLE pipeline_runs ADD COLUMN artifact_digest TEXT DEFAULT ''`},
			{"pipeline_runs", "artifact_size", `ALTER TABLE pipeline_runs ADD COLUMN artifact_size INTEGER DEFAULT 0`},
			{"deploys", "actor", `ALTER TABLE deploys ADD COLUMN actor TEXT DEFAULT ''`},
			{"deploys", "environment", `ALTER TABLE deploys ADD COLUMN environment TEXT DEFAULT ''`},
			{"deploys", "artifact_digest", `ALTER TABLE deploys ADD COLUMN artifact_digest TEXT DEFAULT ''`},
			{"deploys", "reason", `ALTER TABLE deploys ADD COLUMN reason TEXT DEFAULT ''`},
			{"deploys", "artifact_id", `ALTER TABLE deploys ADD COLUMN artifact_id INTEGER DEFAULT 0`},
			{"deploys", "artifact_kind", `ALTER TABLE deploys ADD COLUMN artifact_kind TEXT DEFAULT ''`},
			{"deploys", "artifact_uri", `ALTER TABLE deploys ADD COLUMN artifact_uri TEXT DEFAULT ''`},
			{"deploys", "external_id", `ALTER TABLE deploys ADD COLUMN external_id TEXT DEFAULT ''`},
			{"environments", "approvers", `ALTER TABLE environments ADD COLUMN approvers TEXT DEFAULT ''`},
			{"service_environments", "host_group", `ALTER TABLE service_environments ADD COLUMN host_group TEXT DEFAULT ''`},
			{"service_environments", "batch_size", `ALTER TABLE service_environments ADD COLUMN batch_size INTEGER DEFAULT 1`},
			{"service_environments", "pause_between_batches", `ALTER TABLE service_environments ADD COLUMN pause_between_batches INTEGER DEFAULT 0`},
			{"service_environments", "failure_threshold", `ALTER TABLE service_environments ADD COLUMN failure_threshold INTEGER DEFAULT 0`},
			{"service_delivery", "git_connection", `ALTER TABLE service_delivery ADD COLUMN git_connection TEXT DEFAULT ''`},
			{"service_delivery", "build_environment", `ALTER TABLE service_delivery ADD COLUMN build_environment TEXT DEFAULT '{}'`},
			{"release_orders", "target_snapshot", `ALTER TABLE release_orders ADD COLUMN target_snapshot TEXT DEFAULT '{}'`},
			{"release_orders", "runtime_env_encrypted", `ALTER TABLE release_orders ADD COLUMN runtime_env_encrypted TEXT DEFAULT ''`},
		} {
			if _, err := db.Exec(`SELECT ` + m.col + ` FROM ` + m.table + ` LIMIT 1`); err != nil {
				if _, err := db.Exec(m.ddl); err != nil {
					return nil, err
				}
			}
		}
	}
	secretKey, err := loadCredentialKey(dir)
	if err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db, secretKey: secretKey, dataDir: dir}
	if db.dialect == "sqlite" {
		for _, q := range []string{
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_services_application_id ON services(application_id) WHERE application_id<>''`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_artifacts_source ON artifacts(source,source_id) WHERE source<>'' AND source_id>0`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_builds_pipeline_run ON builds(pipeline_run_id) WHERE pipeline_run_id>0`,
			`CREATE INDEX IF NOT EXISTS idx_work_items_status ON work_items(status,updated_at)`,
			`CREATE INDEX IF NOT EXISTS idx_changes_status ON changes(status,updated_at)`,
			`CREATE INDEX IF NOT EXISTS idx_release_versions_status ON release_versions(status,updated_at)`,
			`CREATE INDEX IF NOT EXISTS idx_release_promotions_version ON release_promotions(release_version_id,id)`,
			`CREATE INDEX IF NOT EXISTS idx_change_executions_promotion ON change_executions(promotion_id,id)`,
		} {
			if _, err := db.Exec(q); err != nil {
				db.Close()
				return nil, err
			}
		}
	}
	if err := s.backfillArtifacts(); err != nil {
		return nil, err
	}
	if err := s.backfillApplicationIDs(); err != nil {
		return nil, err
	}
	if err := s.migrateHostPasswords(); err != nil {
		return nil, err
	}
	if err := s.bootstrapEnvironments(); err != nil {
		return nil, err
	}
	if err := s.migrateResourceEnvironmentScopes(); err != nil {
		return nil, err
	}
	if err := s.bootstrapDeliveryResources(); err != nil {
		return nil, err
	}
	if err := s.migrateBuildOnlyPipelines(); err != nil {
		return nil, err
	}
	if err := s.recoverInterrupted(); err != nil {
		return nil, err
	}
	if err := s.backfillReleaseOrders(); err != nil {
		return nil, err
	}
	if db.dialect == "sqlite" {
		for _, q := range []string{
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_builds_one_running ON builds(service) WHERE status='running'`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_pipeline_one_active ON pipeline_runs(service) WHERE status IN ('running','waiting_deploy')`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_deploy_one_running ON deploys(service,host) WHERE status='running'`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_artifacts_source ON artifacts(source,source_id) WHERE source<>'' AND source_id>0`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_builds_pipeline_run ON builds(pipeline_run_id) WHERE pipeline_run_id>0`,
		} {
			if _, err := db.Exec(q); err != nil {
				db.Close()
				return nil, err
			}
		}
	}
	if err := s.initDeliverySchema(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// migrateBuildOnlyPipelines 将旧模板中的单主机部署步骤移出构建流水线。
// 只执行一次且只识别旧内置步骤；用户自定义步骤不会被修改。
func (s *Store) migrateBuildOnlyPipelines() error {
	var done string
	if s.db.QueryRow(`SELECT value FROM deploy_meta WHERE key='build_only_pipeline_v1'`).Scan(&done) == nil && done == "1" {
		return nil
	}
	rows, err := s.db.Query(`SELECT name,COALESCE(steps,'[]') FROM services`)
	if err != nil {
		return err
	}
	type pipeline struct{ name, raw string }
	items := []pipeline{}
	for rows.Next() {
		var item pipeline
		if err := rows.Scan(&item.name, &item.raw); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	legacyDeploySteps := map[string]bool{"ssh-jar-deploy": true, "ssh-static-deploy": true, "docker-host-deploy": true}
	for _, item := range items {
		steps := []StepInst{}
		if json.Unmarshal([]byte(item.raw), &steps) != nil {
			continue
		}
		filtered := make([]StepInst, 0, len(steps))
		for _, step := range steps {
			if !legacyDeploySteps[step.Step] {
				filtered = append(filtered, step)
			}
		}
		if len(filtered) == len(steps) {
			continue
		}
		raw, _ := json.Marshal(filtered)
		if _, err := tx.Exec(`UPDATE services SET steps=? WHERE name=?`, string(raw), item.name); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO deploy_meta(key,value) VALUES('build_only_pipeline_v1','1') ON CONFLICT(key) DO UPDATE SET value='1'`); err != nil {
		return err
	}
	return tx.Commit()
}

// recoverInterrupted 将上次进程退出时无法继续执行的任务收口，避免永久显示“运行中”。
// waiting_deploy 已有不可变制品，可以在重启后继续，因此保留。
func (s *Store) recoverInterrupted() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`UPDATE builds SET status='interrupted', ended_at=strftime('%s','now'), log=substr(COALESCE(log,'') || char(10) || '进程重启，构建已中断', -20000) WHERE status='running'`,
		`UPDATE pipeline_runs SET status='interrupted', ended_at=strftime('%s','now'), log=substr(COALESCE(log,'') || char(10) || '进程重启，流水线已中断', -40000) WHERE status='running'`,
		`UPDATE deploys SET status='interrupted', ended_at=strftime('%s','now'), log=substr(COALESCE(log,'') || char(10) || '进程重启，部署结果未知，请人工确认', -20000) WHERE status='running'`,
	} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Service 一个可构建/部署的服务配置
type Service struct {
	Name                    string   `json:"name"`
	ApplicationID           string   `json:"application_id"`
	Tags                    []string `json:"tags,omitempty"`
	GitURL                  string   `json:"git_url"`       // 配置后插件自管源码（clone/pull），与 workdir 二选一
	Branch                  string   `json:"branch"`        // 分支（默认远程 HEAD）
	Workdir                 string   `json:"workdir"`       // 本地模式：已有代码目录
	ArtifactType            string   `json:"artifact_type"` // jar | static | image（image 当前仅构建）
	BuildMode               string   `json:"build_mode"`    // local；ssh 为保留配置
	BuildHost               string   `json:"build_host"`    // 保留字段
	BuildCmd                string   `json:"build_cmd"`     // 如 ./gradlew build -x test 或 docker build
	BuildTool               string   `json:"build_tool"`    // command | maven | gradle
	MavenExecutable         string   `json:"maven_executable,omitempty"`
	MavenGoals              string   `json:"maven_goals,omitempty"`
	MavenProfiles           string   `json:"maven_profiles,omitempty"`
	MavenProperties         string   `json:"maven_properties,omitempty"`
	MavenSettingsPath       string   `json:"maven_settings_path,omitempty"`
	GradleExecutable        string   `json:"gradle_executable,omitempty"`
	GradleTasks             string   `json:"gradle_tasks,omitempty"`
	GradleExcludedTasks     string   `json:"gradle_excluded_tasks,omitempty"`
	GradleProjectProperties string   `json:"gradle_project_properties,omitempty"`
	GradleSystemProperties  string   `json:"gradle_system_properties,omitempty"`
	ArtifactGlob            string   `json:"artifact_glob"` // jar 产物匹配
	ImageName               string   `json:"image_name"`    // image 产物名（registry/repo）
	PreCmd                  string   `json:"pre_cmd"`       // 构建前置（如 git pull）
	// 部署
	TargetType   string            `json:"target_type"`             // ssh-jar | ssh-static
	RemoteDir    string            `json:"remote_dir"`              // 远端目录（jar 上传目标）
	StartCmd     string            `json:"start_cmd"`               // 启动命令（nohup java -jar …）
	StopCmd      string            `json:"stop_cmd"`                // 停止命令（空=pkill 兜底）
	HealthURL    string            `json:"health_url"`              // 健康检查（轮询至 200）
	DeployConfig map[string]string `json:"deploy_config,omitempty"` // 由部署 Driver 解释的扩展配置

	// 步骤序列（插件化）：配置后忽略上面的散字段；未配置时由旧字段按模板展开
	Steps []StepInst `json:"steps,omitempty"`
	// RuntimeEnv 来自变量组和资源连接，仅存在于单次构建内存中，不经 API/数据库返回。
	RuntimeEnv         map[string]string `json:"-"`
	BuildEnvironment   *BuildEnvironment `json:"-"`
	RuntimeSecrets     []string          `json:"-"`
	RuntimeEnvironment string            `json:"-"`
	RuntimeEnvFile     string            `json:"-"`
}

func (s *Store) UpsertService(sv Service) error {
	if strings.TrimSpace(sv.ApplicationID) == "" {
		_ = s.db.QueryRow(`SELECT application_id FROM services WHERE name=?`, sv.Name).Scan(&sv.ApplicationID)
	}
	if strings.TrimSpace(sv.ApplicationID) == "" {
		var err error
		sv.ApplicationID, err = newApplicationID()
		if err != nil {
			return err
		}
	}
	sv.Tags = normalizeServiceTags(sv.Tags)
	tagsJSON, _ := json.Marshal(sv.Tags)
	_, err := s.db.Exec(`INSERT INTO services(name,application_id,git_url,branch,workdir,artifact_type,tags,build_mode,build_host,build_cmd,build_tool,maven_executable,maven_goals,maven_profiles,maven_properties,maven_settings_path,gradle_executable,gradle_tasks,gradle_excluded_tasks,gradle_project_properties,gradle_system_properties,artifact_glob,image_name,pre_cmd,target_type,remote_dir,start_cmd,stop_cmd,health_url,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,strftime('%s','now'))
		ON CONFLICT(name) DO UPDATE SET application_id=excluded.application_id,git_url=excluded.git_url,branch=excluded.branch,workdir=excluded.workdir,
			artifact_type=excluded.artifact_type,tags=excluded.tags,build_mode=excluded.build_mode,build_host=excluded.build_host,
			build_cmd=excluded.build_cmd,build_tool=excluded.build_tool,maven_executable=excluded.maven_executable,maven_goals=excluded.maven_goals,
			maven_profiles=excluded.maven_profiles,maven_properties=excluded.maven_properties,maven_settings_path=excluded.maven_settings_path,
			gradle_executable=excluded.gradle_executable,gradle_tasks=excluded.gradle_tasks,gradle_excluded_tasks=excluded.gradle_excluded_tasks,
			gradle_project_properties=excluded.gradle_project_properties,gradle_system_properties=excluded.gradle_system_properties,
			artifact_glob=excluded.artifact_glob,image_name=excluded.image_name,pre_cmd=excluded.pre_cmd,
			target_type=excluded.target_type,remote_dir=excluded.remote_dir,start_cmd=excluded.start_cmd,
			stop_cmd=excluded.stop_cmd,health_url=excluded.health_url`,
		sv.Name, sv.ApplicationID, sv.GitURL, sv.Branch, sv.Workdir, sv.ArtifactType, string(tagsJSON), sv.BuildMode, sv.BuildHost, sv.BuildCmd,
		sv.BuildTool, sv.MavenExecutable, sv.MavenGoals, sv.MavenProfiles, sv.MavenProperties, sv.MavenSettingsPath,
		sv.GradleExecutable, sv.GradleTasks, sv.GradleExcludedTasks, sv.GradleProjectProperties, sv.GradleSystemProperties,
		sv.ArtifactGlob, sv.ImageName, sv.PreCmd,
		sv.TargetType, sv.RemoteDir, sv.StartCmd, sv.StopCmd, sv.HealthURL)
	if sv.Steps == nil || len(sv.Steps) == 0 {
		sv.Steps = ExpandTemplate(sv)
	}
	stepsJ, _ := json.Marshal(sv.Steps)
	s.db.Exec(`UPDATE services SET steps=? WHERE name=?`, string(stepsJ), sv.Name)
	return err
}

func normalizeServiceTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	seen := map[string]bool{}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || seen[tag] {
			continue
		}
		if len([]rune(tag)) > 24 {
			tag = string([]rune(tag)[:24])
		}
		seen[tag] = true
		out = append(out, tag)
		if len(out) == 12 {
			break
		}
	}
	return out
}

func (s *Store) DeleteService(name string) error {
	if release := s.ActiveReleaseForServiceEnvironment(name, ""); release != nil {
		return fmt.Errorf("应用存在进行中的发布单 #%d，不能删除", release.ID)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM service_environment_hosts WHERE service=?`, name); err == nil {
		_, err = tx.Exec(`DELETE FROM service_environments WHERE service=?`, name)
	}
	if err == nil {
		_, err = tx.Exec(`DELETE FROM service_delivery WHERE service=?`, name)
	}
	if err == nil {
		_, err = tx.Exec(`DELETE FROM services WHERE name=?`, name)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Services() []Service {
	rows, err := s.db.Query(`SELECT name,COALESCE(application_id,''),git_url,branch,workdir,artifact_type,COALESCE(tags,'[]'),build_mode,build_host,build_cmd,build_tool,maven_executable,maven_goals,maven_profiles,maven_properties,maven_settings_path,gradle_executable,gradle_tasks,gradle_excluded_tasks,gradle_project_properties,gradle_system_properties,artifact_glob,image_name,pre_cmd,target_type,remote_dir,start_cmd,stop_cmd,health_url,COALESCE(steps,'[]') FROM services ORDER BY name`)
	out := []Service{}
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var sv Service
		var gitURL, br, wd, at, bm, bh, bc, bt, me, mg, mp, mprop, msp, ge, gt, get, gpp, gsp, ag, in, pc, tt, rd, sc, stc, hu sql.NullString
		var stepsJ, tagsJ string
		if rows.Scan(&sv.Name, &sv.ApplicationID, &gitURL, &br, &wd, &at, &tagsJ, &bm, &bh, &bc, &bt, &me, &mg, &mp, &mprop, &msp, &ge, &gt, &get, &gpp, &gsp, &ag, &in, &pc, &tt, &rd, &sc, &stc, &hu, &stepsJ) == nil {
			json.Unmarshal([]byte(stepsJ), &sv.Steps)
			json.Unmarshal([]byte(tagsJ), &sv.Tags)
			sv.GitURL, sv.Branch, sv.Workdir, sv.ArtifactType, sv.BuildMode = gitURL.String, br.String, wd.String, at.String, bm.String
			sv.BuildHost, sv.BuildCmd, sv.ArtifactGlob, sv.ImageName, sv.PreCmd = bh.String, bc.String, ag.String, in.String, pc.String
			sv.BuildTool, sv.MavenExecutable, sv.MavenGoals = bt.String, me.String, mg.String
			sv.MavenProfiles, sv.MavenProperties, sv.MavenSettingsPath = mp.String, mprop.String, msp.String
			sv.GradleExecutable, sv.GradleTasks, sv.GradleExcludedTasks = ge.String, gt.String, get.String
			sv.GradleProjectProperties, sv.GradleSystemProperties = gpp.String, gsp.String
			sv.TargetType, sv.RemoteDir, sv.StartCmd, sv.StopCmd, sv.HealthURL = tt.String, rd.String, sc.String, stc.String, hu.String
			out = append(out, sv)
		}
	}
	return out
}

func (s *Store) GetService(name string) *Service {
	for _, x := range s.Services() {
		if x.Name == name {
			return &x
		}
	}
	return nil
}

func (s *Store) GetServiceByApplicationID(applicationID string) *Service {
	for _, x := range s.Services() {
		if x.ApplicationID == applicationID {
			return &x
		}
	}
	return nil
}

func newApplicationID() (string, error) {
	raw := make([]byte, 16)
	if _, err := crand.Read(raw); err != nil {
		return "", fmt.Errorf("生成应用 ID: %w", err)
	}
	return "app_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

func (s *Store) backfillApplicationIDs() error {
	rows, err := s.db.Query(`SELECT name FROM services WHERE COALESCE(application_id,'')=''`)
	if err != nil {
		return err
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		names = append(names, name)
	}
	rows.Close()
	for _, name := range names {
		id, err := newApplicationID()
		if err != nil {
			return err
		}
		if _, err := s.db.Exec(`UPDATE services SET application_id=? WHERE name=? AND COALESCE(application_id,'')=''`, id, name); err != nil {
			return err
		}
	}
	return nil
}

// Build 构建记录
type Build struct {
	ID            int64  `json:"id"`
	Service       string `json:"service"`
	Version       string `json:"version"`
	Status        string `json:"status"`   // running|success|failed
	Artifact      string `json:"artifact"` // 兼容字段；新代码使用 ArtifactID/URI
	ArtifactID    int64  `json:"artifact_id,omitempty"`
	ArtifactKind  string `json:"artifact_kind,omitempty"`
	PipelineRunID int64  `json:"pipeline_run_id,omitempty"`
	Digest        string `json:"artifact_digest,omitempty"`
	Size          int64  `json:"artifact_size,omitempty"`
	Actor         string `json:"actor,omitempty"`
	Log           string `json:"log,omitempty"`
	StartedAt     int64  `json:"started_at"`
	EndedAt       int64  `json:"ended_at"`
}

func (s *Store) CreateBuild(b *Build) error {
	if active := s.ActiveBuild(b.Service); active != nil {
		return fmt.Errorf("%w: 服务 %s 已有构建 #%d", ErrRunConflict, b.Service, active.ID)
	}
	res, err := s.db.Exec(`INSERT INTO builds(service,version,status,actor,started_at) VALUES(?,?,?,? ,?)`,
		b.Service, b.Version, "running", b.Actor, b.StartedAt)
	if err != nil {
		if active := s.ActiveBuild(b.Service); active != nil {
			return fmt.Errorf("%w: 服务 %s 已有构建 #%d", ErrRunConflict, b.Service, active.ID)
		}
		return err
	}
	b.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) FinishBuild(id int64, status, artifact, digest string, size int64, logTail string) {
	artifactID, kind := int64(0), ""
	if status == "success" && artifact != "" {
		var service, version, artifactType string
		if s.db.QueryRow(`SELECT b.service,b.version,COALESCE(v.artifact_type,'jar') FROM builds b LEFT JOIN services v ON v.name=b.service WHERE b.id=?`, id).
			Scan(&service, &version, &artifactType) == nil {
			kind = artifactKindForService(artifactType)
			if a, err := describeArtifact(service, version, kind, artifact, digest); err == nil {
				a.Source, a.SourceID = "build", id
				if s.RegisterArtifact(&a) == nil {
					artifactID, digest, size = a.ID, a.Digest, a.Size
				}
			}
		}
	}
	s.db.Exec(`UPDATE builds SET status=?,artifact=?,artifact_id=?,artifact_kind=?,artifact_digest=?,artifact_size=?,ended_at=strftime('%s','now'),log=? WHERE id=?`,
		status, artifact, artifactID, kind, digest, size, logTail, id)
}

func (s *Store) Builds(service string, limit int) []Build {
	q := `SELECT id,service,version,status,artifact,artifact_id,artifact_kind,pipeline_run_id,artifact_digest,artifact_size,actor,started_at,ended_at FROM builds`
	args := []any{}
	if service != "" {
		q += ` WHERE service=?`
		args = append(args, service)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	out := []Build{}
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var b Build
		var art sql.NullString
		var ended sql.NullInt64
		if rows.Scan(&b.ID, &b.Service, &b.Version, &b.Status, &art, &b.ArtifactID, &b.ArtifactKind, &b.PipelineRunID, &b.Digest, &b.Size, &b.Actor, &b.StartedAt, &ended) == nil {
			b.Artifact = art.String
			b.EndedAt = ended.Int64
			out = append(out, b)
		}
	}
	return out
}

func (s *Store) ActiveBuild(service string) *Build {
	var b Build
	if s.db.QueryRow(`SELECT id,service,version,status,actor,started_at FROM builds WHERE service=? AND status='running' ORDER BY id DESC LIMIT 1`, service).
		Scan(&b.ID, &b.Service, &b.Version, &b.Status, &b.Actor, &b.StartedAt) == nil {
		return &b
	}
	return nil
}

func (s *Store) BuildLog(id int64) string {
	var log string
	s.db.QueryRow(`SELECT log FROM builds WHERE id=?`, id).Scan(&log)
	return log
}

func (s *Store) Close() error { return s.db.Close() }

// Driver reports the relational metadata backend used by the deployment
// plugin. Workspaces, artifacts and encryption material remain in dataDir.
func (s *Store) Driver() string { return s.db.dialect }

var _ = json.Marshal

// ---------- 主机 ----------

func (s *Store) UpsertHost(h Host) error {
	h.Name, h.Environment = strings.TrimSpace(h.Name), strings.TrimSpace(h.Environment)
	if h.Environment == "" {
		return fmt.Errorf("主机所属环境不能为空")
	}
	if !validHostName(h.Name) {
		return fmt.Errorf("主机名称格式错误：支持中文、字母、数字、点、短横线和下划线，长度 1-64")
	}
	if s.GetEnvironment(h.Environment) == nil {
		return fmt.Errorf("所属环境 %q 不存在，请刷新后重新选择", h.Environment)
	}
	if current := s.GetHost(h.Name); current != nil {
		if release := s.ActiveReleaseForHost(h.Name); release != nil {
			return fmt.Errorf("主机存在进行中的发布单 #%d，暂不能修改", release.ID)
		}
	}
	if current := s.GetHost(h.Name); current != nil && current.Environment != h.Environment {
		var refs int
		_ = s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM host_group_hosts WHERE host=?)+(SELECT COUNT(*) FROM service_environment_hosts WHERE host=?)`, h.Name, h.Name).Scan(&refs)
		if refs > 0 {
			return fmt.Errorf("主机已被主机组或应用环境使用，不能直接变更环境")
		}
	}
	password, err := s.encryptSecret(h.Password)
	if err != nil {
		return err
	}
	// 编辑时空密码代表保留已有凭据，防止表单脱敏后意外清空。
	_, err = s.db.Exec(`INSERT INTO hosts(name,addr,user,key_path,password,host_key,environment) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET addr=excluded.addr,user=excluded.user,key_path=excluded.key_path,
		password=CASE WHEN excluded.password='' THEN hosts.password ELSE excluded.password END,host_key=excluded.host_key,environment=excluded.environment`,
		h.Name, h.Addr, h.User, h.KeyPath, password, h.HostKey, h.Environment)
	return err
}

func (s *Store) DeleteHost(name string) error {
	if release := s.ActiveReleaseForHost(name); release != nil {
		return fmt.Errorf("主机存在进行中的发布单 #%d，不能删除", release.ID)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM service_environment_hosts WHERE host=?`, name).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("主机仍被 %d 个服务环境使用，请先解除关联", n)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM host_group_hosts WHERE host=?`, name).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("主机仍被 %d 个主机组使用，请先解除关联", n)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM agent_configs WHERE host_name=?`, name); err == nil {
		_, err = tx.Exec(`DELETE FROM hosts WHERE name=?`, name)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Hosts() []Host {
	rows, err := s.db.Query(`SELECT name,addr,user,key_path,password,host_key,COALESCE(environment,'default') FROM hosts ORDER BY environment,name`)
	out := []Host{}
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var h Host
		var u, k, p sql.NullString
		var hk sql.NullString
		if rows.Scan(&h.Name, &h.Addr, &u, &k, &p, &hk, &h.Environment) == nil {
			h.User, h.KeyPath, h.HostKey = u.String, k.String, hk.String
			h.Password, _ = s.decryptSecret(p.String)
			h.CredentialConfigured = h.Password != "" || h.KeyPath != ""
			out = append(out, h)
		}
	}
	return out
}

func loadCredentialKey(dir string) ([]byte, error) {
	if encoded := os.Getenv("LOGPLATFORM_CREDENTIAL_KEY"); encoded != "" {
		key, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("LOGPLATFORM_CREDENTIAL_KEY 必须是 base64 编码的 32 字节密钥")
		}
		return key, nil
	}
	path := filepath.Join(dir, ".credential-key")
	if key, err := os.ReadFile(path); err == nil {
		if len(key) != 32 {
			return nil, fmt.Errorf("凭据密钥长度无效: %s", path)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return nil, fmt.Errorf("收紧凭据密钥权限: %w", err)
		}
		return key, nil
	}
	key := make([]byte, 32)
	if _, err := crand.Read(key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

func (s *Store) encryptSecret(value string) (string, error) {
	if value == "" {
		return value, nil
	}
	block, err := aes.NewCipher(s.secretKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := crand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(value), nil)
	return "enc:v1:" + base64.StdEncoding.EncodeToString(sealed), nil
}

func (s *Store) decryptSecret(value string) (string, error) {
	if value == "" || !strings.HasPrefix(value, "enc:v1:") {
		return value, nil
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, "enc:v1:"))
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(s.secretKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("加密凭据损坏")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	return string(plain), err
}

func (s *Store) migrateHostPasswords() error {
	rows, err := s.db.Query(`SELECT name,password FROM hosts WHERE password<>'' AND password NOT LIKE 'enc:v1:%'`)
	if err != nil {
		return err
	}
	type item struct{ name, password string }
	var items []item
	for rows.Next() {
		var v item
		if rows.Scan(&v.name, &v.password) == nil {
			items = append(items, v)
		}
	}
	rows.Close()
	for _, item := range items {
		encrypted, err := s.encryptSecret(item.password)
		if err != nil {
			return err
		}
		if _, err := s.db.Exec(`UPDATE hosts SET password=? WHERE name=?`, encrypted, item.name); err != nil {
			return err
		}
	}
	// 启动时验证所有密文，密钥丢失或配置错误必须显式失败，不能静默变成空密码。
	rows, err = s.db.Query(`SELECT name,password FROM hosts WHERE password LIKE 'enc:v1:%'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var name, encrypted string
		if err := rows.Scan(&name, &encrypted); err != nil {
			return err
		}
		if _, err := s.decryptSecret(encrypted); err != nil {
			return fmt.Errorf("主机 %s 的凭据无法解密，请检查 LOGPLATFORM_CREDENTIAL_KEY 或 .credential-key: %w", name, err)
		}
	}
	return rows.Err()
}

func (s *Store) PublicHosts() []Host {
	hosts := s.Hosts()
	for i := range hosts {
		hosts[i].Password = ""
	}
	return hosts
}

func (s *Store) GetHost(name string) *Host {
	for _, h := range s.Hosts() {
		if h.Name == name {
			return &h
		}
	}
	return nil
}

// ---------- 部署 ----------

func (s *Store) CreateDeploy(d *Deploy) error {
	if active := s.ActiveDeploy(d.Service, d.Host); active != nil {
		return fmt.Errorf("%w: %s 在 %s 已有部署 #%d", ErrRunConflict, d.Service, d.Host, active.ID)
	}
	res, err := s.db.Exec(`INSERT INTO deploys(service,version,host,environment,artifact_id,artifact_kind,artifact_uri,artifact_digest,reason,status,actor,started_at) VALUES(?,?,?,?,?,?,?,?,?,'running',?,?)`,
		d.Service, d.Version, d.Host, d.Environment, d.ArtifactID, d.ArtifactKind, d.ArtifactURI, d.ArtifactDigest, d.Reason, d.Actor, d.StartedAt)
	if err != nil {
		if active := s.ActiveDeploy(d.Service, d.Host); active != nil {
			return fmt.Errorf("%w: %s 在 %s 已有部署 #%d", ErrRunConflict, d.Service, d.Host, active.ID)
		}
		return err
	}
	d.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) FinishDeploy(id int64, status string) {
	s.db.Exec(`UPDATE deploys SET status=?,ended_at=strftime('%s','now'),log=(SELECT COALESCE(log,'') FROM deploys WHERE id=?) WHERE id=?`, status, id, id)
}

func (s *Store) AppendDeployLog(id int64, tail string) {
	s.db.Exec(`UPDATE deploys SET log=substr(COALESCE(log,'') || char(10) || ?, -20000) WHERE id=?`, tail, id)
}

func (s *Store) SetDeployExternalID(id int64, externalID string) {
	s.db.Exec(`UPDATE deploys SET external_id=? WHERE id=?`, strings.TrimSpace(externalID), id)
}

func (s *Store) Deploys(service string, limit int) []Deploy {
	q := `SELECT id,service,version,host,environment,artifact_id,artifact_kind,artifact_uri,artifact_digest,COALESCE(external_id,''),reason,status,actor,started_at,ended_at FROM deploys`
	args := []any{}
	if service != "" {
		q += ` WHERE service=?`
		args = append(args, service)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	out := []Deploy{}
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var d Deploy
		var ended sql.NullInt64
		if rows.Scan(&d.ID, &d.Service, &d.Version, &d.Host, &d.Environment, &d.ArtifactID, &d.ArtifactKind, &d.ArtifactURI, &d.ArtifactDigest, &d.ExternalID, &d.Reason, &d.Status, &d.Actor, &d.StartedAt, &ended) == nil {
			d.EndedAt = ended.Int64
			out = append(out, d)
		}
	}
	return out
}

func (s *Store) GetDeploy(id int64) *Deploy {
	var d Deploy
	var ended sql.NullInt64
	if s.db.QueryRow(`SELECT id,service,version,host,environment,artifact_id,artifact_kind,artifact_uri,artifact_digest,COALESCE(external_id,''),reason,status,actor,started_at,ended_at FROM deploys WHERE id=?`, id).
		Scan(&d.ID, &d.Service, &d.Version, &d.Host, &d.Environment, &d.ArtifactID, &d.ArtifactKind, &d.ArtifactURI, &d.ArtifactDigest, &d.ExternalID, &d.Reason, &d.Status, &d.Actor, &d.StartedAt, &ended) == nil {
		d.EndedAt = ended.Int64
		return &d
	}
	return nil
}

func (s *Store) ActiveDeploy(service, host string) *Deploy {
	var d Deploy
	if s.db.QueryRow(`SELECT id,service,version,host,status,actor,started_at FROM deploys WHERE service=? AND host=? AND status='running' ORDER BY id DESC LIMIT 1`, service, host).
		Scan(&d.ID, &d.Service, &d.Version, &d.Host, &d.Status, &d.Actor, &d.StartedAt) == nil {
		return &d
	}
	return nil
}

// PrevSuccess 同服务同主机更早的成功部署（回滚目标）
func (s *Store) PrevSuccess(service, host string, beforeID int64) *Deploy {
	var d Deploy
	if s.db.QueryRow(`SELECT id,service,version,host,environment,artifact_id,artifact_kind,artifact_uri,artifact_digest,COALESCE(external_id,''),reason,status,actor,started_at,ended_at FROM deploys
		WHERE service=? AND host=? AND status='success' AND id<? ORDER BY id DESC LIMIT 1`,
		service, host, beforeID).
		Scan(&d.ID, &d.Service, &d.Version, &d.Host, &d.Environment, &d.ArtifactID, &d.ArtifactKind, &d.ArtifactURI, &d.ArtifactDigest, &d.ExternalID, &d.Reason, &d.Status, &d.Actor, &d.StartedAt, &d.EndedAt) == nil {
		return &d
	}
	return nil
}

func (s *Store) GetBuildByVersion(service, version string) *Build {
	var b Build
	if s.db.QueryRow(`SELECT id,service,version,status,artifact,artifact_id,artifact_kind,pipeline_run_id,artifact_digest,artifact_size,actor,started_at,ended_at FROM builds
		WHERE service=? AND version=? AND status='success' ORDER BY id DESC LIMIT 1`, service, version).
		Scan(&b.ID, &b.Service, &b.Version, &b.Status, &b.Artifact, &b.ArtifactID, &b.ArtifactKind, &b.PipelineRunID, &b.Digest, &b.Size, &b.Actor, &b.StartedAt, &b.EndedAt) == nil {
		return &b
	}
	return nil
}

func (s *Store) SetBuildVersion(id int64, version string) {
	s.db.Exec(`UPDATE builds SET version=? WHERE id=?`, version, id)
}

// ---------- 流水线 ----------

// Stage 阶段固定四段：sync → build → artifact → deploy（deploy 可选）
type Stage struct {
	Name      string `json:"name"`
	Status    string `json:"status"` // pending|running|waiting|success|failed|skipped
	StartedAt int64  `json:"started_at,omitempty"`
	DurMs     int64  `json:"dur_ms,omitempty"`
}

// PipelineRun 一次流水线运行（commit→产物→部署 全关联）
type PipelineRun struct {
	ID                int64   `json:"id"`
	Service           string  `json:"service"`
	Trigger           string  `json:"trigger"` // manual|api
	Branch            string  `json:"branch,omitempty"`
	Version           string  `json:"version,omitempty"`
	BuildCommand      string  `json:"build_command,omitempty"`
	Status            string  `json:"status"`
	AutoDeploy        bool    `json:"auto_deploy"`
	DeployHost        string  `json:"deploy_host,omitempty"` // deploy 阶段目标
	DeployEnvironment string  `json:"deploy_environment,omitempty"`
	Artifact          string  `json:"artifact,omitempty"`
	ArtifactID        int64   `json:"artifact_id,omitempty"`
	ArtifactKind      string  `json:"artifact_kind,omitempty"`
	ArtifactDigest    string  `json:"artifact_digest,omitempty"`
	ArtifactSize      int64   `json:"artifact_size,omitempty"`
	Actor             string  `json:"actor,omitempty"`
	Stages            []Stage `json:"stages"`
	StartedAt         int64   `json:"started_at"`
	EndedAt           int64   `json:"ended_at,omitempty"`
}

func defaultStages() []Stage {
	return []Stage{{Name: "sync"}, {Name: "build"}, {Name: "artifact"}, {Name: "deploy"}}
}

func (s *Store) CreateRun(r *PipelineRun) error {
	if active := s.ActiveRun(r.Service); active != nil {
		return fmt.Errorf("%w: 服务 %s 已有流水线 #%d", ErrRunConflict, r.Service, active.ID)
	}
	if len(r.Stages) == 0 {
		r.Stages = defaultStages()
	}
	stJ, _ := json.Marshal(r.Stages)
	res, err := s.db.Exec(`INSERT INTO pipeline_runs(service,trigger,branch,build_command,auto_deploy,deploy_host,deploy_environment,status,stages,actor,started_at)
		VALUES(?,?,?,?,?,?,?,'running',?,?,?)`,
		r.Service, r.Trigger, r.Branch, r.BuildCommand, r.AutoDeploy, r.DeployHost, r.DeployEnvironment, string(stJ), r.Actor, r.StartedAt)
	if err != nil {
		if active := s.ActiveRun(r.Service); active != nil {
			return fmt.Errorf("%w: 服务 %s 已有流水线 #%d", ErrRunConflict, r.Service, active.ID)
		}
		return err
	}
	r.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) UpdateStage(runID int64, idx int, st Stage) {
	var stJ string
	s.db.QueryRow(`SELECT stages FROM pipeline_runs WHERE id=?`, runID).Scan(&stJ)
	var stages []Stage
	json.Unmarshal([]byte(stJ), &stages)
	if idx < 0 || idx >= len(stages) {
		return
	}
	stages[idx] = st
	b, _ := json.Marshal(stages)
	s.db.Exec(`UPDATE pipeline_runs SET stages=? WHERE id=?`, string(b), runID)
}

func (s *Store) SetRunVersion(runID int64, version, artifact string) {
	s.db.Exec(`UPDATE pipeline_runs SET version=?,artifact=? WHERE id=?`, version, artifact, runID)
}

func (s *Store) FinishRun(runID int64, status string) {
	s.db.Exec(`UPDATE pipeline_runs SET status=?,ended_at=strftime('%s','now') WHERE id=?`, status, runID)
}

func (s *Store) SetRunStatus(runID int64, status string) {
	s.db.Exec(`UPDATE pipeline_runs SET status=?,ended_at=NULL WHERE id=?`, status, runID)
}

func (s *Store) SetRunDeployHost(runID int64, host string) {
	s.db.Exec(`UPDATE pipeline_runs SET deploy_host=? WHERE id=?`, host, runID)
}

// ClaimRunDeploy 原子地把等待确认的流水线切到部署中，避免重复点击启动两次部署。
func (s *Store) ClaimRunDeploy(runID int64, host string) bool {
	res, err := s.db.Exec(`UPDATE pipeline_runs SET status='running',deploy_host=?,ended_at=NULL WHERE id=? AND status='waiting_deploy'`, host, runID)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

func (s *Store) ClaimRunEnvironment(runID int64, environment string) bool {
	res, err := s.db.Exec(`UPDATE pipeline_runs SET status='running',deploy_environment=?,ended_at=NULL WHERE id=? AND status='waiting_deploy'`, environment, runID)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

// CancelWaitingRun 放弃待确认部署并释放服务锁。
func (s *Store) CancelWaitingRun(runID int64) bool {
	tx, err := s.db.Begin()
	if err != nil {
		return false
	}
	defer tx.Rollback()
	var stagesJSON string
	if err := tx.QueryRow(`SELECT stages FROM pipeline_runs WHERE id=? AND status='waiting_deploy'`, runID).Scan(&stagesJSON); err != nil {
		return false
	}
	var stages []Stage
	json.Unmarshal([]byte(stagesJSON), &stages)
	for i := range stages {
		if stages[i].Status == "waiting" {
			stages[i].Status = "skipped"
		}
	}
	b, _ := json.Marshal(stages)
	res, err := tx.Exec(`UPDATE pipeline_runs SET status='canceled',stages=?,ended_at=strftime('%s','now'),log=substr(COALESCE(log,'') || char(10) || '已放弃部署', -40000) WHERE id=? AND status='waiting_deploy'`, string(b), runID)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return false
	}
	return tx.Commit() == nil
}

func (s *Store) AppendRunLog(runID int64, tail string) {
	s.db.Exec(`UPDATE pipeline_runs SET log=substr(COALESCE(log,'') || char(10) || ?, -40000) WHERE id=?`, tail, runID)
}

func (s *Store) RunLog(runID int64) string {
	var log string
	s.db.QueryRow(`SELECT log FROM pipeline_runs WHERE id=?`, runID).Scan(&log)
	return log
}

func (s *Store) Runs(service string, limit int) []PipelineRun {
	q := `SELECT id,service,trigger,branch,version,build_command,status,auto_deploy,deploy_host,deploy_environment,artifact,artifact_id,artifact_kind,artifact_digest,artifact_size,actor,stages,started_at,ended_at FROM pipeline_runs`
	args := []any{}
	if service != "" {
		q += ` WHERE service=?`
		args = append(args, service)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	out := []PipelineRun{}
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var r PipelineRun
		var br, ver, dh, de, art sql.NullString
		var ended sql.NullInt64
		var stages string
		if rows.Scan(&r.ID, &r.Service, &r.Trigger, &br, &ver, &r.BuildCommand, &r.Status, &r.AutoDeploy, &dh, &de, &art, &r.ArtifactID, &r.ArtifactKind, &r.ArtifactDigest, &r.ArtifactSize, &r.Actor, &stages, &r.StartedAt, &ended) == nil {
			r.Branch, r.Version, r.DeployHost, r.DeployEnvironment, r.Artifact = br.String, ver.String, dh.String, de.String, art.String
			r.EndedAt = ended.Int64
			json.Unmarshal([]byte(stages), &r.Stages)
			out = append(out, r)
		}
	}
	return out
}

func (s *Store) ActiveRun(service string) *PipelineRun {
	var r PipelineRun
	if s.db.QueryRow(`SELECT id,service,status,actor,started_at FROM pipeline_runs WHERE service=? AND status IN ('running','waiting_deploy') ORDER BY id DESC LIMIT 1`, service).
		Scan(&r.ID, &r.Service, &r.Status, &r.Actor, &r.StartedAt) == nil {
		return &r
	}
	return nil
}

func (s *Store) GetRun(id int64) *PipelineRun {
	for _, r := range s.Runs("", 1000) {
		if r.ID == id {
			return &r
		}
	}
	return nil
}

// ExpandTemplate 旧散字段 → 步骤序列（jar / static / image 三模板）
func ExpandTemplate(sv Service) []StepInst {
	P := func(step string, kv ...string) StepInst {
		m := map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return StepInst{Step: step, Params: m}
	}
	base := []StepInst{P("git-checkout", "GIT_URL", sv.GitURL, "BRANCH", sv.Branch)}
	switch sv.ArtifactType {
	case "static":
		base = append(base,
			P("npm-build", "COMMAND", sv.BuildCmd),
			P("package-static", "GLOB", sv.ArtifactGlob))
	case "image":
		base = append(base,
			P("docker-build", "IMAGE_NAME", sv.ImageName, "CONTEXT", ".", "DOCKERFILE", "Dockerfile", "PUSH", "true"))
	default:
		base = append(base,
			P("java-build", "COMMAND", sv.BuildCmd, "PRE", sv.PreCmd),
			P("package-jar", "GLOB", sv.ArtifactGlob))
	}
	return base
}

// SetStages 按步骤序列初始化阶段视图
func (s *Store) SetStages(runID int64, n int) {
	st := make([]Stage, n)
	for i := range st {
		st[i] = Stage{Name: "pending", Status: "pending"}
	}
	b, _ := json.Marshal(st)
	s.db.Exec(`UPDATE pipeline_runs SET stages=? WHERE id=?`, string(b), runID)
}

// BackfillSteps 老服务（steps 为空）按模板展开回填；返回回填数
func (s *Store) BackfillSteps() int {
	n := 0
	for _, sv := range s.Services() {
		if len(sv.Steps) > 0 {
			continue
		}
		steps := ExpandTemplate(sv)
		b, _ := json.Marshal(steps)
		if _, err := s.db.Exec(`UPDATE services SET steps=? WHERE name=?`, string(b), sv.Name); err == nil {
			n++
		}
	}
	return n
}
