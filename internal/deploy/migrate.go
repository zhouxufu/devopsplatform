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
		migrationTable("research_exception_resolutions", "task_id,actor,digest,document,comment,created_at", "task_id,actor,digest"),
		migrationTable("research_attention_states", "actor,item_key,event_digest,state,revision,document,reason,request_id,updated_at", "actor,item_key,event_digest"),
		migrationTable("api_test_layouts", "application_id,case_id,owner,workspace,document,revision", "application_id,case_id,owner,workspace"),
		migrationTable("deployment_agents", "agent_id,host_name,environment,services,token_hash,enabled,last_seen", "agent_id"),
		migrationTable("agent_deployment_tasks", "deploy_id,task_id,agent_id,service,environment,host_name,root_path,plan_digest,plan_cipher,status,last_sequence,updated_at", "deploy_id"),
		migrationTable("agent_deployment_reports", "task_id,seq_no,digest", "task_id,seq_no"),
		migrationTable("bug_evidence", "id,work_item_id,owner,request_key,request_digest,reference_key,kind,document,created_at", "id"),
		migrationTable("api_test_document_reviews", "application_id,case_id,case_revision,step_index,snapshot_id,actor,reviewed_at,note", "application_id,case_id,case_revision,step_index,snapshot_id"),
		migrationTable("api_test_run_cases", "run_id,case_id", "run_id,case_id"),
		migrationTable("api_test_case_access", "case_id,visibility", "case_id"),
		migrationTable("api_test_suites", "id,application_id,owner,visibility,document,revision,deleted,updated_at", "id"),
		migrationTable("api_test_run_index", "run_id,application_id,owner,source,environment,case_id,suite_id,name", "run_id"),
		migrationTable("api_test_profiles", "application_id,environment,document,revision", "application_id,environment"),
		migrationTable("api_test_cases", "id,application_id,owner,document,revision,deleted,updated_at", "id"),
		migrationTable("api_test_runs", "id,application_id,service,owner,request_key,request_digest,document,status,created_at,updated_at", "id"),
		migrationTable("api_document_environment_sources", "application_id,environment,document,actor,updated_at", "application_id,environment"),
		migrationTable("api_document_protocols", "snapshot_id,protocol", "snapshot_id"),
		migrationTable("api_document_maintainers", "snapshot_id,maintainer", "snapshot_id"),
		migrationTable("api_document_maintainer_maps", "snapshot_id,document", "snapshot_id"),
		migrationTable("api_document_change_contexts", "snapshot_id,document", "snapshot_id"),
		migrationTable("api_document_lineage", "snapshot_id,document", "snapshot_id"),
		migrationTable("api_document_work_item_events", "work_item_id,snapshot_id,document", "work_item_id,snapshot_id"),
		migrationTable("api_document_versions", "id,application_id,environment,service,run_id,artifact_id,artifact_digest,commit_ref,provider,provider_version,digest,document,created_at,baseline_id,comparison", "id"),
		migrationTable("api_document_sources", "application_id,document,actor,updated_at", "application_id"),
		migrationTable("api_document_snapshots", "id,application_id,service,run_id,artifact_id,artifact_digest,commit_ref,provider,provider_version,digest,document,created_at", "id"),
		migrationTable("services", "name,application_id,display_name,git_url,branch,workdir,artifact_type,tags,build_mode,build_host,build_cmd,build_tool,maven_executable,maven_goals,maven_profiles,maven_properties,maven_arguments,maven_threads,maven_update_snapshots,maven_settings_path,gradle_executable,gradle_tasks,gradle_excluded_tasks,gradle_project_properties,gradle_system_properties,artifact_glob,image_name,pre_cmd,target_type,remote_dir,steps,start_cmd,stop_cmd,health_url,created_at", "name"),
		migrationTable("builds", "id,service,version,status,artifact,artifact_id,artifact_kind,artifact_digest,artifact_size,pipeline_run_id,log,actor,started_at,ended_at", "id"),
		migrationTable("artifact_retention_policies", "service,keep_count,updated_at", "service"),
		migrationTable("artifact_cleanup", "artifact_id,service,uri,retired_at,cleaned_at", "artifact_id"),
		migrationTable("build_artifact_outputs", "run_id,artifact_id,role,digest,parent_artifact_id", "run_id,artifact_id"),
		migrationTable("instance_operations", "request_id,service,environment,host,action,actor,fingerprint,state,started_at,ended_at", "request_id"),
		migrationTable("artifacts", "id,service,version,kind,uri,digest,size,metadata,source,source_id,created_at", "id"),
		migrationTable("hosts", "name,addr,user,key_path,password,host_key,environment", "name"),
		migrationTable("environments", "name,display_name,purpose,protected,require_reason,sort_order,approvers,created_at", "name"),
		migrationTable("service_environments", "service,environment,target_type,remote_dir,start_cmd,stop_cmd,health_url,deploy_config,runtime_env_encrypted,runtime_env_keys,strategy,max_parallel,host_group,batch_size,pause_between_batches,failure_threshold,updated_at", "service,environment"),
		migrationTable("service_environment_hosts", "service,environment,host,sort_order", "service,environment,host"),
		migrationTable("deploy_meta", "key,value", "key"),
		migrationTable("pipeline_runs", "id,service,trigger,branch,version,build_command,status,auto_deploy,deploy_host,deploy_environment,artifact,artifact_id,artifact_kind,artifact_digest,artifact_size,stages,log,actor,started_at,ended_at", "id"),
		migrationTable("pipeline_run_counters", "service,last_number", "service"),
		migrationTable("pipeline_run_numbers", "run_id,service,number", "run_id"),
		migrationTable("release_order_counters", "service,last_number", "service"),
		migrationTable("release_order_numbers", "release_id,service,number", "release_id"),
		migrationTable("build_run_snapshots", "run_id,config_cipher,commit_ref,created_at", "run_id"),
		migrationTable("deploys", "id,service,version,host,environment,artifact_id,artifact_kind,artifact_uri,artifact_digest,external_id,reason,status,log,actor,started_at,ended_at", "id"),
		migrationTable("resource_connections", "name,kind,endpoint,config,secret,secret_configured,created_at,updated_at", "name"),
		migrationTable("host_groups", "name,environment,description,created_at,updated_at", "name"),
		migrationTable("host_group_hosts", "group_name,host,sort_order", "group_name,host"),
		migrationTable("variable_groups", "name,description,values_encrypted,key_names,created_at,updated_at", "name"),
		migrationTable("build_runners", "name,kind,labels,enabled,max_parallel,created_at,updated_at", "name"),
		migrationTable("service_delivery", "service,runner,git_connection,maven_connection,registry_connection,variable_groups,webhook_token,schedule_minutes,auto_deploy_environment,last_schedule_at,updated_at,build_environment", "service"),
		migrationTable("release_orders", "id,service,artifact_id,version,environment,reason,strategy,status,batch_size,pause_between_batches,failure_threshold,hosts,target_snapshot,runtime_env_encrypted,next_index,total,succeeded,failed,actor,approver,started_at,ended_at", "id"),
		migrationTable("release_order_deploys", "release_id,deploy_id,batch_no", "release_id,deploy_id"),
		migrationTable("work_items", "id,source,external_id,kind,title,url,external_status,description,description_format,status,owner,created_at,updated_at,assigned_to,opened_by,resolved_by,closed_by", "id"),
		migrationTable("research_work_metadata", "work_item_id,release_required,no_release_reason,revision,actor,updated_at", "work_item_id"),
		migrationTable("research_release_rules", "connection_name,document,revision,actor,updated_at", "connection_name"),
		migrationTable("research_work_relations", "work_item_id,actor,kind,created_at", "work_item_id,actor,kind"),
		migrationTable("change_delivery_ownership", "change_id,origin_work_item_id,delivery_work_item_id,mode,actor,updated_at", "change_id"),
		migrationTable("research_issue_fields", "work_item_id,document", "work_item_id"),
		migrationTable("release_notices", "record_kind,record_id,document,revision,actor,updated_at", "record_kind,record_id"),
		migrationTable("research_flow_uses", "owner,request_id,run_id,request_digest", "owner,request_id"),
		migrationTable("research_release_marks", "work_item_id,marked,revision,actor,updated_at,comment", "work_item_id"),
		migrationTable("research_activity", "id,work_item_id,actor,kind,content,created_at", "id"),
		migrationTable("research_credentials", "actor,connection_name,endpoint_digest,account,secret", "actor,connection_name"),
		migrationTable("research_operations", "operation_id,work_item_id,actor,request_digest,state,result,created_at", "operation_id"),
		migrationTable("work_item_attachments", "id,owner,name,mime,size,work_item_id,created_at", "id"),
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
		migrationTable("delivery_preflight_uses", "nonce,run_id", "nonce"),
		migrationTable("delivery_version_artifacts", "version_id,document,used", "version_id"),
		migrationTable("delivery_controls", "run_id,state,actor,lease,lease_until,message", "run_id"),
		migrationTable("change_payloads", "change_id,document", "change_id"),
		migrationTable("data_sync_tasks", "id,document,owner,revision", "id"),
		migrationTable("workbench_queries", "id,kind,document,status,owner,revision", "id"),
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
