package deploy

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MigrationReport contains the number of source rows copied per table.
// Copying is idempotent: existing rows are updated by their primary/unique key
// and rows that exist only in the target are retained.
type MigrationReport struct {
	Tables map[string]int64 `json:"tables"`
	Total  int64            `json:"total"`
}

type deployMigrationTable struct {
	name    string
	columns []string
	keys    map[string]bool
	rename  map[string]string
}

func migrationTable(name, columns, keys string) deployMigrationTable {
	t := deployMigrationTable{name: name, columns: strings.Split(columns, ","), keys: map[string]bool{}, rename: map[string]string{}}
	for _, key := range strings.Split(keys, ",") {
		t.keys[key] = true
	}
	return t
}

var deployMigrationTables = func() []deployMigrationTable {
	tables := []deployMigrationTable{
		migrationTable("services", "name,application_id,git_url,branch,workdir,artifact_type,tags,build_mode,build_host,build_cmd,build_tool,maven_executable,maven_goals,maven_profiles,maven_properties,maven_settings_path,gradle_executable,gradle_tasks,gradle_excluded_tasks,gradle_project_properties,gradle_system_properties,artifact_glob,image_name,pre_cmd,target_type,remote_dir,steps,start_cmd,stop_cmd,health_url,created_at", "name"),
		migrationTable("builds", "id,service,version,status,artifact,artifact_id,artifact_kind,artifact_digest,artifact_size,pipeline_run_id,log,actor,started_at,ended_at", "id"),
		migrationTable("artifacts", "id,service,version,kind,uri,digest,size,metadata,source,source_id,created_at", "id"),
		migrationTable("hosts", "name,addr,user,key_path,password,host_key,environment", "name"),
		migrationTable("environments", "name,display_name,protected,require_reason,sort_order,approvers,created_at", "name"),
		migrationTable("service_environments", "service,environment,target_type,remote_dir,start_cmd,stop_cmd,health_url,deploy_config,runtime_env_encrypted,runtime_env_keys,strategy,max_parallel,host_group,batch_size,pause_between_batches,failure_threshold,updated_at", "service,environment"),
		migrationTable("service_environment_hosts", "service,environment,host,sort_order", "service,environment,host"),
		migrationTable("deploy_meta", "key,value", "key"),
		migrationTable("pipeline_runs", "id,service,trigger,branch,version,build_command,status,auto_deploy,deploy_host,deploy_environment,artifact,artifact_id,artifact_kind,artifact_digest,artifact_size,stages,log,actor,started_at,ended_at", "id"),
		migrationTable("deploys", "id,service,version,host,environment,artifact_id,artifact_kind,artifact_uri,artifact_digest,external_id,reason,status,log,actor,started_at,ended_at", "id"),
		migrationTable("resource_connections", "name,kind,endpoint,config,secret,secret_configured,created_at,updated_at", "name"),
		migrationTable("host_groups", "name,environment,description,created_at,updated_at", "name"),
		migrationTable("host_group_hosts", "group_name,host,sort_order", "group_name,host"),
		migrationTable("variable_groups", "name,description,values_encrypted,key_names,created_at,updated_at", "name"),
		migrationTable("build_runners", "name,kind,labels,enabled,max_parallel,created_at,updated_at", "name"),
		migrationTable("service_delivery", "service,runner,git_connection,maven_connection,registry_connection,variable_groups,webhook_token,schedule_minutes,auto_deploy_environment,last_schedule_at,updated_at,build_environment", "service"),
		migrationTable("release_orders", "id,service,artifact_id,version,environment,reason,strategy,status,batch_size,pause_between_batches,failure_threshold,hosts,target_snapshot,runtime_env_encrypted,next_index,total,succeeded,failed,actor,approver,started_at,ended_at", "id"),
		migrationTable("release_order_deploys", "release_id,deploy_id,batch_no", "release_id,deploy_id"),
		migrationTable("work_items", "id,source,external_id,kind,title,url,external_status,description,status,owner,created_at,updated_at", "id"),
		migrationTable("changes", "id,kind,title,service,reference,summary,risk_level,rollback_plan,status,owner,created_at,updated_at", "id"),
		migrationTable("work_item_changes", "work_item_id,change_id", "work_item_id,change_id"),
		migrationTable("release_versions", "id,version,title,description,status,owner,created_at,updated_at", "id"),
		migrationTable("release_version_changes", "release_version_id,change_id,sort_order", "release_version_id,change_id"),
		migrationTable("release_version_scope", "release_version_id,document", "release_version_id"),
		migrationTable("release_promotions", "id,release_version_id,environment,status,actor,approver,created_at,updated_at", "id"),
		migrationTable("application_resource_requirements", "service,alias,kind,required,description", "service,alias"),
		migrationTable("application_environment_resources", "service,environment,alias,connection,config,updated_at", "service,environment,alias"),
		migrationTable("change_executions", "id,promotion_id,change_id,environment,connection,status,before_digest,after_digest,log,started_at,ended_at", "id"),
		migrationTable("delivery_templates", "id,document,owner", "id"),
		migrationTable("delivery_runs", "id,plan,digest,status,execution,env_secrets,owner,approver,created_at,revision", "id"),
		migrationTable("change_payloads", "change_id,document", "change_id"),
		migrationTable("data_sync_tasks", "id,document,owner,revision", "id"),
		migrationTable("issue_sync_state", "connection,last_at,status,detail", "connection"),
		migrationTable("external_work_items", "connection,kind,external_id,work_item_id", "connection,kind,external_id"),
		migrationTable("sql_execution_ledger", "connection,database_name,change_id,digest,status,run_id,node_id,detail", "connection,database_name,change_id"),
		migrationTable("audit_events", "id,actor,action,resource_type,resource_id,detail,created_at", "id"),
		migrationTable("agent_configs", "host_name,server_url,monitor_url,run_user,run_group,spool_max_mb,collect,updated_at,applied_at,last_apply_error", "host_name"),
	}
	for i := range tables {
		switch tables[i].name {
		case "deploy_meta":
			tables[i].rename["key"] = "meta_key"
		case "pipeline_runs":
			tables[i].rename["trigger"] = "run_trigger"
		}
	}
	return tables
}()

// MigrateSQLiteToMySQL copies deployment control-plane metadata from an
// existing SQLite database into a MySQL 8 schema. Artifact files, workspaces
// and .credential-key stay in the deployment data directory and are not copied.
func MigrateSQLiteToMySQL(sourcePath, targetDSN string) (MigrationReport, error) {
	report := MigrationReport{Tables: map[string]int64{}}
	absSource, err := filepath.Abs(sourcePath)
	if err != nil {
		return report, err
	}
	if _, err := os.Stat(absSource); err != nil {
		return report, fmt.Errorf("open source deploy database: %w", err)
	}
	sourceURL := (&url.URL{Scheme: "file", Path: absSource, RawQuery: "mode=ro"}).String()
	source, err := sql.Open("sqlite", sourceURL)
	if err != nil {
		return report, err
	}
	defer source.Close()
	if err := source.Ping(); err != nil {
		return report, fmt.Errorf("open source deploy database: %w", err)
	}
	target, err := openDeployDatabase(DatabaseConfig{Driver: "mysql", DSN: targetDSN})
	if err != nil {
		return report, err
	}
	defer target.Close()

	for _, table := range deployMigrationTables {
		count, err := migrateDeployTable(source, target, table)
		if err != nil {
			return report, fmt.Errorf("migrate %s: %w", table.name, err)
		}
		report.Tables[table.name] = count
		report.Total += count
	}
	return report, nil
}

func migrateDeployTable(source *sql.DB, target *deployDB, table deployMigrationTable) (int64, error) {
	available, err := sqliteTableColumns(source, table.name)
	if err != nil {
		return 0, err
	}
	if len(available) == 0 {
		return 0, nil
	}
	columns := make([]string, 0, len(table.columns))
	for _, column := range table.columns {
		if available[column] {
			columns = append(columns, column)
		}
	}
	if len(columns) == 0 {
		return 0, nil
	}
	rows, err := source.Query("SELECT " + quotedColumns(columns, `"`) + " FROM " + quoteIdentifier(table.name, `"`))
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	targetColumns := make([]string, len(columns))
	updates := make([]string, 0, len(columns))
	for i, column := range columns {
		targetColumn := column
		if renamed := table.rename[column]; renamed != "" {
			targetColumn = renamed
		}
		targetColumns[i] = targetColumn
		if !table.keys[column] {
			quoted := quoteIdentifier(targetColumn, "`")
			updates = append(updates, quoted+"=VALUES("+quoted+")")
		}
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(columns)), ",")
	statement := "INSERT INTO " + quoteIdentifier(table.name, "`") + " (" + quotedColumns(targetColumns, "`") + ") VALUES (" + placeholders + ")"
	if len(updates) == 0 {
		statement = "INSERT IGNORE INTO " + quoteIdentifier(table.name, "`") + " (" + quotedColumns(targetColumns, "`") + ") VALUES (" + placeholders + ")"
	} else {
		statement += " ON DUPLICATE KEY UPDATE " + strings.Join(updates, ",")
	}
	tx, err := target.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var copied int64
	for rows.Next() {
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(statement, values...); err != nil {
			return 0, err
		}
		copied++
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return copied, nil
}

func sqliteTableColumns(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query("PRAGMA table_info(" + quoteIdentifier(table, `"`) + ")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, dataType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

func quoteIdentifier(identifier, quote string) string {
	return quote + strings.ReplaceAll(identifier, quote, quote+quote) + quote
}

func quotedColumns(columns []string, quote string) string {
	quoted := make([]string, len(columns))
	for i, column := range columns {
		quoted[i] = quoteIdentifier(column, quote)
	}
	return strings.Join(quoted, ",")
}

// SortedMigrationTables is used by the CLI for deterministic output.
func (r MigrationReport) SortedMigrationTables() []string {
	names := make([]string, 0, len(r.Tables))
	for name := range r.Tables {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
