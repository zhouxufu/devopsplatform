package platform

import (
	"context"
	crand "crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"logplatform/internal/appcatalog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "modernc.org/sqlite"
)

// Registry 服务注册表：平台唯一共享库（服务/主机/标签/version——deploy 插件生产，其余消费）
type Registry struct {
	applicationCreateMu  sync.Mutex
	mu                   sync.Mutex
	db                   *sql.DB
	dialect              string
	deploymentReferences func(context.Context) ([]appcatalog.ApplicationReference, error)
}

// RegistryConfig configures the shared application catalog database. SQLite is
// intentionally kept for local development; production installations can use
// MySQL without changing the catalog API.
type RegistryConfig struct {
	Driver string
	DSN    string
}

type Service struct {
	ApplicationID string   `json:"application_id"`
	Name          string   `json:"name"`
	DisplayName   string   `json:"display_name"`
	Identifier    string   `json:"identifier"`
	Status        string   `json:"status"`
	Sources       []string `json:"sources"`
	Version       string   `json:"version,omitempty"`
	Hosts         []string `json:"hosts,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	Updated       int64    `json:"updated"`
}

type DeploymentState struct {
	Service     string   `json:"service"`
	Environment string   `json:"environment"`
	Version     string   `json:"version"`
	Hosts       []string `json:"hosts"`
	Digest      string   `json:"artifact_digest,omitempty"`
	Updated     int64    `json:"updated"`
}

type ApplicationIdentity struct {
	Source        string `json:"source"`
	Identifier    string `json:"identifier"`
	ApplicationID string `json:"application_id"`
	FirstSeen     int64  `json:"first_seen"`
	LastSeen      int64  `json:"last_seen"`
}

// RegistrySnapshot is the portable catalog representation used by the
// SQLite-to-MySQL migration command. It contains password hashes (never plain
// passwords), so callers must keep it in memory and must not log it. Import is
// idempotent and never deletes records that exist only in the destination.
type RegistrySnapshot struct {
	Services       []Service             `json:"services"`
	Deployments    []DeploymentState     `json:"deployments"`
	Identities     []ApplicationIdentity `json:"identities"`
	Users          []User                `json:"users"`
	UserIdentities []UserIdentity        `json:"user_identities"`
}

func OpenRegistry(path string) (*Registry, error) {
	return OpenRegistryDatabase(RegistryConfig{Driver: "sqlite", DSN: path})
}

// OpenRegistryDatabase opens and migrates the shared application catalog.
// For sqlite, DSN may be either a database file path or a complete file: DSN.
// For mysql, DSN uses the go-sql-driver/mysql format and must include a schema.
func OpenRegistryDatabase(cfg RegistryConfig) (*Registry, error) {
	driver := strings.ToLower(strings.TrimSpace(cfg.Driver))
	if driver == "" {
		driver = "sqlite"
	}
	if driver != "sqlite" && driver != "mysql" {
		return nil, fmt.Errorf("unsupported registry database driver %q (use sqlite or mysql)", driver)
	}
	dsn := strings.TrimSpace(cfg.DSN)
	if dsn == "" {
		return nil, fmt.Errorf("registry database DSN is required")
	}
	if driver == "sqlite" && !strings.HasPrefix(dsn, "file:") {
		if dir := filepath.Dir(dsn); dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("create registry database directory: %w", err)
			}
		}
		dsn = "file:" + dsn + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect registry database: %w", err)
	}
	if driver == "mysql" {
		db.SetMaxOpenConns(20)
		db.SetMaxIdleConns(5)
		db.SetConnMaxLifetime(5 * time.Minute)
	} else {
		db.SetMaxOpenConns(1)
	}
	r := &Registry{db: db, dialect: driver}
	if err := r.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	if err := r.migrateApplicationNames(); err != nil {
		db.Close()
		return nil, err
	}
	if err := r.migrateApplicationLifecycle(); err != nil {
		db.Close()
		return nil, err
	}
	if err := r.backfillApplicationIDs(); err != nil {
		db.Close()
		return nil, err
	}
	if err := r.ensureApplicationIDIndex(); err != nil {
		db.Close()
		return nil, err
	}
	return r, nil
}

func (r *Registry) migrate() error {
	var schema []string
	if r.dialect == "mysql" {
		schema = []string{
			`CREATE TABLE IF NOT EXISTS services (
				name VARCHAR(255) PRIMARY KEY, application_id VARCHAR(64) NOT NULL,
				status VARCHAR(32) NOT NULL DEFAULT 'discovered', sources TEXT,
				version VARCHAR(255), hosts TEXT, tags TEXT, updated BIGINT NOT NULL DEFAULT 0
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
			`CREATE TABLE IF NOT EXISTS deployment_states (
				service VARCHAR(255) NOT NULL, environment VARCHAR(128) NOT NULL,
				version VARCHAR(255), hosts TEXT, artifact_digest VARCHAR(255) NOT NULL DEFAULT '',
				updated BIGINT NOT NULL DEFAULT 0, PRIMARY KEY(service,environment)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
			`CREATE TABLE IF NOT EXISTS application_identities (
				source VARCHAR(32) NOT NULL, identifier VARCHAR(255) NOT NULL,
				application_id VARCHAR(64) NOT NULL, first_seen BIGINT NOT NULL,
				last_seen BIGINT NOT NULL, PRIMARY KEY(source,identifier),
				INDEX idx_identity_application_id(application_id)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
			`CREATE TABLE IF NOT EXISTS platform_users (
				username VARCHAR(64) PRIMARY KEY, display_name VARCHAR(128) NOT NULL DEFAULT '',
				role VARCHAR(16) NOT NULL DEFAULT 'member', password_hash VARCHAR(255) NOT NULL,
				enabled TINYINT NOT NULL DEFAULT 1, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
		}
	} else {
		schema = []string{
			`CREATE TABLE IF NOT EXISTS services (
				name TEXT PRIMARY KEY, application_id TEXT DEFAULT '', status TEXT DEFAULT 'discovered', sources TEXT DEFAULT '[]',
				version TEXT, hosts TEXT, tags TEXT, updated INTEGER)`,
			`CREATE TABLE IF NOT EXISTS deployment_states (
				service TEXT NOT NULL, environment TEXT NOT NULL, version TEXT, hosts TEXT DEFAULT '[]',
				artifact_digest TEXT DEFAULT '', updated INTEGER, PRIMARY KEY(service,environment))`,
			`CREATE TABLE IF NOT EXISTS application_identities (
				source TEXT NOT NULL, identifier TEXT NOT NULL, application_id TEXT NOT NULL,
				first_seen INTEGER NOT NULL, last_seen INTEGER NOT NULL, PRIMARY KEY(source,identifier))`,
			`CREATE TABLE IF NOT EXISTS platform_users (
				username TEXT PRIMARY KEY, display_name TEXT NOT NULL DEFAULT '', role TEXT NOT NULL DEFAULT 'member',
				password_hash TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`,
		}
	}
	userIdentityDDL := `CREATE TABLE IF NOT EXISTS platform_user_identities (
		source VARCHAR(80) NOT NULL, external_id VARCHAR(64) NOT NULL,
		username VARCHAR(64) NOT NULL UNIQUE, PRIMARY KEY(source,external_id))`
	if r.dialect == "mysql" {
		userIdentityDDL += ` ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`
	}
	schema = append(schema, userIdentityDDL)
	dutiesDDL := `CREATE TABLE IF NOT EXISTS platform_user_duties (username VARCHAR(64) PRIMARY KEY, document TEXT NOT NULL)`
	if r.dialect == "mysql" {
		dutiesDDL += ` ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`
	}
	schema = append(schema, dutiesDDL)
	for _, ddl := range schema {
		if _, err := r.db.Exec(ddl); err != nil {
			return fmt.Errorf("migrate registry database: %w", err)
		}
	}
	if r.dialect == "sqlite" {
		for _, ddl := range []string{
			`ALTER TABLE services ADD COLUMN application_id TEXT DEFAULT ''`,
			`ALTER TABLE services ADD COLUMN status TEXT DEFAULT 'discovered'`,
			`ALTER TABLE services ADD COLUMN sources TEXT DEFAULT '[]'`,
		} {
			_, _ = r.db.Exec(ddl)
		}
	}
	return nil
}

func (r *Registry) ensureApplicationIDIndex() error {
	if r.dialect == "mysql" {
		var count int
		if err := r.db.QueryRow(`SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='services' AND index_name='idx_registry_application_id'`).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			_, err := r.db.Exec(`CREATE UNIQUE INDEX idx_registry_application_id ON services(application_id)`)
			return err
		}
		return nil
	}
	_, err := r.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_registry_application_id ON services(application_id) WHERE application_id<>''`)
	return err
}

func (r *Registry) Close() error { return r.db.Close() }

// Driver reports the configured backend without exposing its DSN or secrets.
func (r *Registry) Driver() string { return r.dialect }

func (r *Registry) Snapshot() (RegistrySnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var snapshot RegistrySnapshot
	rows, err := r.db.Query(`SELECT COALESCE(application_id,''),name,COALESCE(status,'discovered'),COALESCE(sources,'[]'),COALESCE(version,''),COALESCE(hosts,'[]'),COALESCE(tags,'[]'),updated,COALESCE(display_name,'') FROM services ORDER BY name`)
	if err != nil {
		return snapshot, err
	}
	for rows.Next() {
		var item Service
		var sources, hosts, tags string
		if err := rows.Scan(&item.ApplicationID, &item.Name, &item.Status, &sources, &item.Version, &hosts, &tags, &item.Updated, &item.DisplayName); err != nil {
			rows.Close()
			return snapshot, err
		}
		_ = json.Unmarshal([]byte(sources), &item.Sources)
		item.Identifier = appcatalog.Identifier(item.Name)
		_ = json.Unmarshal([]byte(hosts), &item.Hosts)
		_ = json.Unmarshal([]byte(tags), &item.Tags)
		snapshot.Services = append(snapshot.Services, item)
	}
	if err := rows.Close(); err != nil {
		return snapshot, err
	}
	rows, err = r.db.Query(`SELECT service,environment,COALESCE(version,''),COALESCE(hosts,'[]'),COALESCE(artifact_digest,''),updated FROM deployment_states ORDER BY service,environment`)
	if err != nil {
		return snapshot, err
	}
	for rows.Next() {
		var item DeploymentState
		var hosts string
		if err := rows.Scan(&item.Service, &item.Environment, &item.Version, &hosts, &item.Digest, &item.Updated); err != nil {
			rows.Close()
			return snapshot, err
		}
		_ = json.Unmarshal([]byte(hosts), &item.Hosts)
		snapshot.Deployments = append(snapshot.Deployments, item)
	}
	if err := rows.Close(); err != nil {
		return snapshot, err
	}
	rows, err = r.db.Query(`SELECT source,identifier,application_id,first_seen,last_seen FROM application_identities ORDER BY source,identifier`)
	if err != nil {
		return snapshot, err
	}
	defer rows.Close()
	for rows.Next() {
		var item ApplicationIdentity
		if err := rows.Scan(&item.Source, &item.Identifier, &item.ApplicationID, &item.FirstSeen, &item.LastSeen); err != nil {
			return snapshot, err
		}
		snapshot.Identities = append(snapshot.Identities, item)
	}
	if err := rows.Close(); err != nil {
		return snapshot, err
	}
	users, err := r.usersLocked()
	if err != nil {
		return snapshot, err
	}
	snapshot.Users = users
	snapshot.UserIdentities, err = r.userIdentitiesLocked()
	if err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

func (r *Registry) Restore(snapshot RegistrySnapshot) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	rollback := func(cause error) error {
		_ = tx.Rollback()
		return cause
	}
	serviceQuery := `INSERT INTO services(name,application_id,status,sources,version,hosts,tags,updated) VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET application_id=excluded.application_id,status=excluded.status,sources=excluded.sources,version=excluded.version,hosts=excluded.hosts,tags=excluded.tags,updated=excluded.updated`
	deploymentQuery := `INSERT INTO deployment_states(service,environment,version,hosts,artifact_digest,updated) VALUES(?,?,?,?,?,?)
		ON CONFLICT(service,environment) DO UPDATE SET version=excluded.version,hosts=excluded.hosts,artifact_digest=excluded.artifact_digest,updated=excluded.updated`
	identityQuery := `INSERT INTO application_identities(source,identifier,application_id,first_seen,last_seen) VALUES(?,?,?,?,?)
		ON CONFLICT(source,identifier) DO UPDATE SET application_id=excluded.application_id,first_seen=excluded.first_seen,last_seen=excluded.last_seen`
	userQuery := `INSERT INTO platform_users(username,display_name,role,password_hash,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(username) DO UPDATE SET display_name=excluded.display_name,role=excluded.role,password_hash=excluded.password_hash,enabled=excluded.enabled,updated_at=excluded.updated_at`
	if r.dialect == "mysql" {
		serviceQuery = `INSERT INTO services(name,application_id,status,sources,version,hosts,tags,updated) VALUES(?,?,?,?,?,?,?,?)
			ON DUPLICATE KEY UPDATE application_id=VALUES(application_id),status=VALUES(status),sources=VALUES(sources),version=VALUES(version),hosts=VALUES(hosts),tags=VALUES(tags),updated=VALUES(updated)`
		deploymentQuery = `INSERT INTO deployment_states(service,environment,version,hosts,artifact_digest,updated) VALUES(?,?,?,?,?,?)
			ON DUPLICATE KEY UPDATE version=VALUES(version),hosts=VALUES(hosts),artifact_digest=VALUES(artifact_digest),updated=VALUES(updated)`
		identityQuery = `INSERT INTO application_identities(source,identifier,application_id,first_seen,last_seen) VALUES(?,?,?,?,?)
			ON DUPLICATE KEY UPDATE application_id=VALUES(application_id),first_seen=VALUES(first_seen),last_seen=VALUES(last_seen)`
		userQuery = `INSERT INTO platform_users(username,display_name,role,password_hash,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?)
			ON DUPLICATE KEY UPDATE display_name=VALUES(display_name),role=VALUES(role),password_hash=VALUES(password_hash),enabled=VALUES(enabled),updated_at=VALUES(updated_at)`
	}
	for _, item := range snapshot.Services {
		if strings.TrimSpace(item.ApplicationID) == "" || strings.TrimSpace(item.Name) == "" {
			return rollback(fmt.Errorf("snapshot contains an application without name or application_id"))
		}
		if _, err := tx.Exec(serviceQuery, item.Name, item.ApplicationID, item.Status, mustJSON(item.Sources), item.Version, mustJSON(item.Hosts), mustJSON(item.Tags), item.Updated); err != nil {
			return rollback(fmt.Errorf("restore application %q: %w", item.Name, err))
		}
		if item.DisplayName != "" {
			if _, err := tx.Exec(`UPDATE services SET display_name=? WHERE application_id=?`, item.DisplayName, item.ApplicationID); err != nil {
				return rollback(err)
			}
		}
	}
	for _, item := range snapshot.Deployments {
		if _, err := tx.Exec(deploymentQuery, item.Service, item.Environment, item.Version, mustJSON(item.Hosts), item.Digest, item.Updated); err != nil {
			return rollback(fmt.Errorf("restore deployment %q/%q: %w", item.Service, item.Environment, err))
		}
	}
	for _, item := range snapshot.Identities {
		if _, err := tx.Exec(identityQuery, item.Source, item.Identifier, item.ApplicationID, item.FirstSeen, item.LastSeen); err != nil {
			return rollback(fmt.Errorf("restore identity %q/%q: %w", item.Source, item.Identifier, err))
		}
	}
	for _, item := range snapshot.Users {
		if _, err := tx.Exec(userQuery, item.Username, item.DisplayName, item.Role, item.PasswordHash, item.Enabled, item.CreatedAt, item.UpdatedAt); err != nil {
			return rollback(fmt.Errorf("restore user %q: %w", item.Username, err))
		}
		if item.Duties != nil {
			if _, err := normalizeDuties(item.Duties); err != nil {
				return rollback(err)
			}
			if _, err := tx.Exec(`DELETE FROM platform_user_duties WHERE username=?`, item.Username); err != nil {
				return rollback(err)
			}
			if _, err := tx.Exec(`INSERT INTO platform_user_duties(username,document) VALUES(?,?)`, item.Username, mustJSON(item.Duties)); err != nil {
				return rollback(err)
			}
		}
	}
	for _, item := range snapshot.UserIdentities {
		rows, err := tx.Query(`SELECT source,external_id,username FROM platform_user_identities WHERE (source=? AND external_id=?) OR username=?`, item.Source, item.ExternalID, item.Username)
		if err != nil {
			return rollback(err)
		}
		exists := false
		for rows.Next() {
			var old UserIdentity
			if err = rows.Scan(&old.Source, &old.ExternalID, &old.Username); err != nil {
				rows.Close()
				return rollback(err)
			}
			if old != item {
				rows.Close()
				return rollback(fmt.Errorf("用户外部身份冲突，不能覆盖已有账号关联"))
			}
			exists = true
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return rollback(err)
		}
		rows.Close()
		if exists {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO platform_user_identities(source,external_id,username) VALUES(?,?,?)`, item.Source, item.ExternalID, item.Username); err != nil {
			return rollback(err)
		}
	}
	return tx.Commit()
}

func (r *Registry) RecordDeployment(d DeploymentState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var oldVersion, hostsJSON string
	var hosts []string
	if r.db.QueryRow(`SELECT version,hosts FROM deployment_states WHERE service=? AND environment=?`, d.Service, d.Environment).Scan(&oldVersion, &hostsJSON) == nil && oldVersion == d.Version {
		json.Unmarshal([]byte(hostsJSON), &hosts)
	}
	seen := map[string]bool{}
	for _, host := range hosts {
		seen[host] = true
	}
	for _, host := range d.Hosts {
		if !seen[host] {
			hosts = append(hosts, host)
			seen[host] = true
		}
	}
	b, _ := json.Marshal(hosts)
	query := `INSERT INTO deployment_states(service,environment,version,hosts,artifact_digest,updated) VALUES(?,?,?,?,?,?)
		ON CONFLICT(service,environment) DO UPDATE SET version=excluded.version,hosts=excluded.hosts,artifact_digest=excluded.artifact_digest,updated=excluded.updated`
	if r.dialect == "mysql" {
		query = `INSERT INTO deployment_states(service,environment,version,hosts,artifact_digest,updated) VALUES(?,?,?,?,?,?)
			ON DUPLICATE KEY UPDATE version=VALUES(version),hosts=VALUES(hosts),artifact_digest=VALUES(artifact_digest),updated=VALUES(updated)`
	}
	_, err := r.db.Exec(query, d.Service, d.Environment, d.Version, string(b), d.Digest, time.Now().Unix())
	return err
}

func (r *Registry) DeploymentStates() []DeploymentState {
	r.mu.Lock()
	defer r.mu.Unlock()
	rows, err := r.db.Query(`SELECT service,environment,COALESCE(version,''),COALESCE(hosts,'[]'),COALESCE(artifact_digest,''),updated FROM deployment_states ORDER BY service,environment`)
	if err != nil {
		return []DeploymentState{}
	}
	defer rows.Close()
	out := []DeploymentState{}
	for rows.Next() {
		var d DeploymentState
		var hosts string
		if rows.Scan(&d.Service, &d.Environment, &d.Version, &hosts, &d.Digest, &d.Updated) == nil {
			json.Unmarshal([]byte(hosts), &d.Hosts)
			out = append(out, d)
		}
	}
	return out
}

// Upsert 注册/更新服务信息
func (r *Registry) Upsert(s Service) error {
	observed, err := r.Observe(s.Name, "manual", s.ApplicationID)
	if err != nil {
		return err
	}
	hosts, _ := json.Marshal(s.Hosts)
	tags, _ := json.Marshal(s.Tags)
	query := `INSERT INTO services(name,application_id,status,sources,version,hosts,tags,updated) VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET version=excluded.version, hosts=excluded.hosts, tags=excluded.tags, updated=excluded.updated`
	if r.dialect == "mysql" {
		query = `INSERT INTO services(name,application_id,status,sources,version,hosts,tags,updated) VALUES(?,?,?,?,?,?,?,?)
			ON DUPLICATE KEY UPDATE version=VALUES(version),hosts=VALUES(hosts),tags=VALUES(tags),updated=VALUES(updated)`
	}
	_, err = r.db.Exec(query, s.Name, observed.ApplicationID, observed.Status, mustJSON(observed.Sources), s.Version, string(hosts), string(tags), time.Now().Unix())
	return err
}

func (r *Registry) List() ([]Service, error) {
	return r.list(true)
}

func (r *Registry) ListActive() ([]Service, error) {
	return r.list(false)
}

func (r *Registry) list(includeArchived bool) ([]Service, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	query := `SELECT COALESCE(application_id,''),name,COALESCE(status,'discovered'),COALESCE(sources,'[]'),COALESCE(version,''),COALESCE(hosts,'[]'),COALESCE(tags,'[]'),updated,COALESCE(display_name,'') FROM services`
	if !includeArchived {
		query += ` WHERE COALESCE(status,'discovered')<>'archived'`
	}
	query += ` ORDER BY name`
	rows, err := r.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Service{}
	for rows.Next() {
		var s Service
		var hosts, tags, sources string
		if rows.Scan(&s.ApplicationID, &s.Name, &s.Status, &sources, &s.Version, &hosts, &tags, &s.Updated, &s.DisplayName) == nil {
			s.DisplayName = appcatalog.DisplayName(s.DisplayName, s.Name)
			s.Identifier = appcatalog.Identifier(s.Name)
			json.Unmarshal([]byte(hosts), &s.Hosts)
			json.Unmarshal([]byte(tags), &s.Tags)
			json.Unmarshal([]byte(sources), &s.Sources)
			out = append(out, s)
		}
	}
	return out, nil
}

func (r *Registry) Observe(name, source, requestedID string) (*Service, error) {
	name, source, requestedID = strings.TrimSpace(name), strings.TrimSpace(source), strings.TrimSpace(requestedID)
	if name == "" || source == "" {
		return nil, fmt.Errorf("name and source are required")
	}
	switch source {
	case "deploy", "logs", "otel", "manual":
	default:
		return nil, fmt.Errorf("unsupported application source %q", source)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().Unix()
	var app Service
	var sourcesJSON, hostsJSON, tagsJSON string
	err := r.db.QueryRow(`SELECT COALESCE(application_id,''),name,COALESCE(status,'discovered'),COALESCE(sources,'[]'),COALESCE(version,''),COALESCE(hosts,'[]'),COALESCE(tags,'[]'),updated,COALESCE(display_name,'')
		FROM services WHERE application_id=(SELECT application_id FROM application_identities WHERE source=? AND identifier=?) OR name=? OR (?<>'' AND application_id=?) LIMIT 1`, source, name, name, requestedID, requestedID).
		Scan(&app.ApplicationID, &app.Name, &app.Status, &sourcesJSON, &app.Version, &hostsJSON, &tagsJSON, &app.Updated, &app.DisplayName)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if err == sql.ErrNoRows {
		id := requestedID
		if id == "" {
			id, err = newRegistryApplicationID()
			if err != nil {
				return nil, err
			}
		}
		app = Service{ApplicationID: id, Name: name, Status: "discovered", Sources: []string{source}, Updated: now}
		if source == "deploy" || source == "manual" {
			app.Status = "managed"
		}
		_, err = r.db.Exec(`INSERT INTO services(name,application_id,status,sources,version,hosts,tags,updated) VALUES(?,?,?,?,?,'[]','[]',?)`, app.Name, app.ApplicationID, app.Status, mustJSON(app.Sources), "", now)
		if err != nil {
			return nil, err
		}
	} else {
		_ = json.Unmarshal([]byte(sourcesJSON), &app.Sources)
		_ = json.Unmarshal([]byte(hostsJSON), &app.Hosts)
		_ = json.Unmarshal([]byte(tagsJSON), &app.Tags)
		// Telemetry retains the identity of archived applications, but cannot
		// undo an explicit directory deletion. Manual/deploy onboarding may
		// deliberately adopt the same stable identity again.
		archivedTelemetry := app.Status == "archived" && (source == "logs" || source == "otel")
		if !archivedTelemetry && !containsString(app.Sources, source) {
			app.Sources = append(app.Sources, source)
		}
		if source == "deploy" || source == "manual" {
			app.Status = "managed"
		}
		if app.Status == "archived" && !archivedTelemetry {
			app.Status = applicationStatus(app.Sources)
		}
		app.Updated = now
		if _, err := r.db.Exec(`UPDATE services SET status=?,sources=?,updated=? WHERE application_id=?`, app.Status, mustJSON(app.Sources), now, app.ApplicationID); err != nil {
			return nil, err
		}
	}
	query := `INSERT INTO application_identities(source,identifier,application_id,first_seen,last_seen) VALUES(?,?,?,?,?)
		ON CONFLICT(source,identifier) DO UPDATE SET application_id=excluded.application_id,last_seen=excluded.last_seen`
	if r.dialect == "mysql" {
		query = `INSERT INTO application_identities(source,identifier,application_id,first_seen,last_seen) VALUES(?,?,?,?,?)
			ON DUPLICATE KEY UPDATE application_id=VALUES(application_id),last_seen=VALUES(last_seen)`
	}
	_, err = r.db.Exec(query, source, name, app.ApplicationID, now, now)
	app.DisplayName = appcatalog.DisplayName(app.DisplayName, app.Name)
	app.Identifier = appcatalog.Identifier(app.Name)
	return &app, err
}

// DetachSource marks one integration as no longer active without deleting the
// application identity or historical telemetry linkage. A later observation
// from the same source adopts the same application_id again.
func (r *Registry) DetachSource(name, source, requestedID string) (*Service, error) {
	name, source, requestedID = strings.TrimSpace(name), strings.TrimSpace(source), strings.TrimSpace(requestedID)
	if name == "" || source == "" {
		return nil, fmt.Errorf("name and source are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var app Service
	var sourcesJSON, hostsJSON, tagsJSON string
	err := r.db.QueryRow(`SELECT COALESCE(application_id,''),name,COALESCE(status,'discovered'),COALESCE(sources,'[]'),COALESCE(version,''),COALESCE(hosts,'[]'),COALESCE(tags,'[]'),updated
		FROM services WHERE application_id=(SELECT application_id FROM application_identities WHERE source=? AND identifier=?) OR (?<>'' AND application_id=?) OR name=? LIMIT 1`, source, name, requestedID, requestedID, name).
		Scan(&app.ApplicationID, &app.Name, &app.Status, &sourcesJSON, &app.Version, &hostsJSON, &tagsJSON, &app.Updated)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(sourcesJSON), &app.Sources)
	_ = json.Unmarshal([]byte(hostsJSON), &app.Hosts)
	_ = json.Unmarshal([]byte(tagsJSON), &app.Tags)
	kept := app.Sources[:0]
	for _, item := range app.Sources {
		if item != source {
			kept = append(kept, item)
		}
	}
	app.Sources = kept
	app.Status = applicationStatus(kept)
	app.Updated = time.Now().Unix()
	if _, err := r.db.Exec(`UPDATE services SET status=?,sources=?,updated=? WHERE application_id=?`, app.Status, mustJSON(app.Sources), app.Updated, app.ApplicationID); err != nil {
		return nil, err
	}
	return &app, nil
}

func applicationStatus(sources []string) string {
	if containsString(sources, "deploy") || containsString(sources, "manual") {
		return "managed"
	}
	if len(sources) == 0 {
		return "archived"
	}
	return "discovered"
}

func (r *Registry) backfillApplicationIDs() error {
	rows, err := r.db.Query(`SELECT name FROM services WHERE COALESCE(application_id,'')=''`)
	if err != nil {
		return err
	}
	var names []string
	for rows.Next() {
		var name string
		if rows.Scan(&name) == nil {
			names = append(names, name)
		}
	}
	rows.Close()
	for _, name := range names {
		id, err := newRegistryApplicationID()
		if err != nil {
			return err
		}
		if _, err := r.db.Exec(`UPDATE services SET application_id=?,status=COALESCE(NULLIF(status,''),'discovered'),sources=COALESCE(NULLIF(sources,''),'[]') WHERE name=?`, id, name); err != nil {
			return err
		}
	}
	return nil
}

func newRegistryApplicationID() (string, error) {
	raw := make([]byte, 16)
	if _, err := crand.Read(raw); err != nil {
		return "", err
	}
	return "app_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
func containsString(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

// HandleRoutes 注册表 HTTP API（经网关供壳与插件消费）
func (r *Registry) HandleRoutes(mux *http.ServeMux) {
	r.applicationNameRoutes(mux)
	r.applicationLifecycleRoutes(mux)
	mux.HandleFunc("GET /api/registry/services", func(w http.ResponseWriter, req *http.Request) {
		list, _ := r.List()
		writeJSON(w, list)
	})
	mux.HandleFunc("GET /api/registry/applications", func(w http.ResponseWriter, req *http.Request) {
		var list []Service
		if req.URL.Query().Get("include_archived") == "true" {
			list, _ = r.List()
		} else {
			list, _ = r.ListActive()
		}
		writeJSON(w, list)
	})
	mux.HandleFunc("POST /api/registry/applications/observe", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Name          string `json:"name"`
			Source        string `json:"source"`
			ApplicationID string `json:"application_id"`
			DisplayName   string `json:"display_name"`
			Identifier    string `json:"identifier"`
			CreateOnly    bool   `json:"create_only"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, req.Body, 16<<10)).Decode(&body) != nil {
			http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
			return
		}
		if body.Identifier != "" && body.Identifier != appcatalog.Identifier(body.Name) {
			writeJSONStatus(w, 400, map[string]string{"error": "应用标识不可修改"})
			return
		}
		if body.CreateOnly {
			r.applicationCreateMu.Lock()
			defer r.applicationCreateMu.Unlock()
			if !appcatalog.ValidIdentifier(body.Name) {
				writeJSONStatus(w, 400, map[string]string{"error": "应用标识以小写英文开头，只能包含小写英文、数字和短横线，最长 63 字符"})
				return
			}
			var n int
			if err := r.db.QueryRow(`SELECT COUNT(*) FROM services WHERE name=?`, body.Name).Scan(&n); err != nil {
				writeJSONStatus(w, 500, map[string]string{"error": "读取应用失败"})
				return
			}
			if n > 0 {
				writeJSONStatus(w, 409, map[string]string{"error": "应用标识已存在"})
				return
			}
		}
		if body.DisplayName != "" {
			if _, err := appcatalog.ValidateDisplayName(body.DisplayName); err != nil {
				writeJSONStatus(w, 400, map[string]string{"error": err.Error()})
				return
			}
		}
		app, err := r.Observe(body.Name, body.Source, body.ApplicationID)
		if err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
			return
		}
		// An onboarding label initializes only an unset label. Telemetry and
		// stale configuration submissions can never undo a deliberate rename.
		if body.DisplayName != "" {
			if _, err := r.db.Exec(`UPDATE services SET display_name=? WHERE application_id=? AND COALESCE(display_name,'')=''`, appcatalog.DisplayName(body.DisplayName, ""), app.ApplicationID); err != nil {
				writeJSONStatus(w, 500, map[string]string{"error": "保存应用名称失败"})
				return
			}
			_ = r.db.QueryRow(`SELECT COALESCE(NULLIF(display_name,''),name) FROM services WHERE application_id=?`, app.ApplicationID).Scan(&app.DisplayName)
		}
		writeJSON(w, app)
	})
	mux.HandleFunc("DELETE /api/registry/applications/source", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Name          string `json:"name"`
			Source        string `json:"source"`
			ApplicationID string `json:"application_id"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, req.Body, 16<<10)).Decode(&body) != nil {
			http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
			return
		}
		app, err := r.DetachSource(body.Name, body.Source, body.ApplicationID)
		if err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
			return
		}
		writeJSON(w, app)
	})
	mux.HandleFunc("POST /api/registry/services", func(w http.ResponseWriter, req *http.Request) {
		var s Service
		if json.NewDecoder(http.MaxBytesReader(w, req.Body, 64<<10)).Decode(&s) != nil || s.Name == "" {
			http.Error(w, `{"error":"bad request"}`, 400)
			return
		}
		if err := r.Upsert(s); err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
			return
		}
		writeJSON(w, s)
	})
	mux.HandleFunc("GET /api/registry/deployments", func(w http.ResponseWriter, req *http.Request) {
		writeJSON(w, r.DeploymentStates())
	})
	mux.HandleFunc("POST /api/registry/deployments", func(w http.ResponseWriter, req *http.Request) {
		var d DeploymentState
		if json.NewDecoder(http.MaxBytesReader(w, req.Body, 16<<10)).Decode(&d) != nil || d.Service == "" || d.Environment == "" || d.Version == "" {
			http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
			return
		}
		if err := r.RecordDeployment(d); err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
			return
		}
		writeJSON(w, d)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// ---------- 事件总线：插件间解耦协作的唯一通道（禁止点对点直连） ----------

// Bus publish/subscribe，HTTP 回调式
type Bus struct {
	mu   sync.RWMutex
	subs []busSub // {topics 前缀, 回调 URL}
}

type busSub struct {
	topics []string
	url    string
}

type BusEvent struct {
	Topic   string `json:"topic"`
	From    string `json:"from"`
	Payload any    `json:"payload,omitempty"`
	At      int64  `json:"at"`
}

func NewBus() *Bus { return &Bus{} }

func (b *Bus) Subscribe(topics []string, url string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	// 同 URL 重复订阅去重（订阅方周期性重订阅是常态，不能重复投递）
	kept := b.subs[:0]
	for _, s := range b.subs {
		if s.url != url {
			kept = append(kept, s)
		}
	}
	b.subs = append(kept, busSub{topics: topics, url: url})
}

// Publish 同步分发给匹配前缀的订阅者（通配 topic.*）；单订阅者失败不影响其他
func (b *Bus) Publish(ev BusEvent) int {
	if ev.At == 0 {
		ev.At = time.Now().Unix()
	}
	b.mu.RLock()
	targets := make([]string, 0, len(b.subs))
	for _, s := range b.subs {
		for _, t := range s.topics {
			if strings.HasPrefix(ev.Topic, strings.TrimSuffix(t, "*")) {
				targets = append(targets, s.url)
				break
			}
		}
	}
	b.mu.RUnlock()
	client := &http.Client{Timeout: 3 * time.Second}
	for _, u := range targets {
		go func(u string) {
			body, _ := json.Marshal(ev)
			for attempt := 0; attempt < 3; attempt++ {
				req, _ := http.NewRequest("POST", u, strings.NewReader(string(body)))
				req.Header.Set("Content-Type", "application/json")
				resp, err := client.Do(req)
				if err == nil {
					resp.Body.Close()
					if resp.StatusCode >= 200 && resp.StatusCode < 300 {
						return
					}
				}
				if attempt < 2 {
					time.Sleep(time.Duration(attempt+1) * 250 * time.Millisecond)
				}
			}
			log.Printf("[bus] delivery failed after retries: topic=%s target=%s", ev.Topic, u)
		}(u)
	}
	return len(targets)
}

func (b *Bus) HandleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/bus/publish", func(w http.ResponseWriter, req *http.Request) {
		var ev BusEvent
		if json.NewDecoder(http.MaxBytesReader(w, req.Body, 1<<20)).Decode(&ev) != nil || ev.Topic == "" {
			http.Error(w, `{"error":"bad request"}`, 400)
			return
		}
		n := b.Publish(ev)
		writeJSON(w, map[string]int{"delivered": n})
	})
	mux.HandleFunc("POST /api/bus/subscribe", func(w http.ResponseWriter, req *http.Request) {
		var sub struct {
			Topics []string `json:"topics"`
			URL    string   `json:"url"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, req.Body, 4096)).Decode(&sub) != nil || sub.URL == "" || len(sub.Topics) == 0 {
			http.Error(w, `{"error":"bad request"}`, 400)
			return
		}
		b.Subscribe(sub.Topics, sub.URL)
		writeJSON(w, map[string]bool{"ok": true})
	})
}
