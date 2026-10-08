package monitor

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type MigrationReport struct {
	Tables map[string]int64 `json:"tables"`
	Total  int64            `json:"total"`
}

type monitorMigrationTable struct {
	name    string
	columns []string
	keys    map[string]bool
}

func monitorMigrationSpec(name, columns, keys string) monitorMigrationTable {
	t := monitorMigrationTable{name: name, columns: strings.Split(columns, ","), keys: map[string]bool{}}
	for _, key := range strings.Split(keys, ",") {
		t.keys[key] = true
	}
	return t
}

var monitorMigrationTables = []monitorMigrationTable{
	monitorMigrationSpec("metrics_minute", "minute,service,environment,level,count", "minute,service,environment,level"),
	monitorMigrationSpec("machine", "ts,agent,environment,cpu,mem_used,mem_total,disk_used,disk_total,net_rx,net_tx", "ts,agent,environment"),
	monitorMigrationSpec("spans", "trace_id,span_id,parent_id,service,environment,instance,name,start_ms,dur_ms,status,attrs,err", "environment,trace_id,span_id"),
	monitorMigrationSpec("otlp_minute", "minute,service,environment,instance,name,val,cnt", "minute,service,environment,instance,name"),
	monitorMigrationSpec("otlp_hist", "minute,service,environment,instance,name,iface,meth,status,cnt,sum,bounds,buckets", "minute,service,environment,instance,name,iface,meth,status"),
	monitorMigrationSpec("otlp_counter", "minute,service,environment,instance,name,iface,meth,status,val,cnt", "minute,service,environment,instance,name,iface,meth,status"),
	monitorMigrationSpec("avail_rules", "service,environment,name,ok_codes,ignore_codes", "service,environment,name"),
	monitorMigrationSpec("metric_alert_rules", "id,name,service,environment,instance,metric,iface,type,quantile,window_min,threshold,min_sample,webhook,silence_sec,suppress_by,silence_until,enabled", "id"),
	monitorMigrationSpec("metric_alert_notifications", "id,rule_id,rule,environment,kind,at,detail", "id"),
}

// MigrateSQLiteToMySQL copies the existing metrics.db into a MySQL 8 schema.
// It is read-only on the source and idempotent on the target.
func MigrateSQLiteToMySQL(sourcePath, targetDSN string) (MigrationReport, error) {
	report := MigrationReport{Tables: map[string]int64{}}
	absSource, err := filepath.Abs(sourcePath)
	if err != nil {
		return report, err
	}
	if _, err := os.Stat(absSource); err != nil {
		return report, fmt.Errorf("open source monitor database: %w", err)
	}
	sourceURL := (&url.URL{Scheme: "file", Path: absSource, RawQuery: "mode=ro"}).String()
	source, err := sql.Open("sqlite", sourceURL)
	if err != nil {
		return report, err
	}
	defer source.Close()
	if err := source.Ping(); err != nil {
		return report, fmt.Errorf("open source monitor database: %w", err)
	}
	target, err := openMonitorDatabase(DatabaseConfig{Driver: "mysql", DSN: targetDSN})
	if err != nil {
		return report, err
	}
	defer target.Close()
	for _, table := range monitorMigrationTables {
		count, err := migrateMonitorTable(source, target, table)
		if err != nil {
			return report, fmt.Errorf("migrate %s: %w", table.name, err)
		}
		report.Tables[table.name] = count
		report.Total += count
	}
	return report, nil
}

func migrateMonitorTable(source *sql.DB, target *monitorDB, table monitorMigrationTable) (int64, error) {
	available, err := sqliteMonitorTableColumns(source, table.name)
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
	rows, err := source.Query("SELECT " + monitorQuotedColumns(columns, `"`) + " FROM " + monitorQuoteIdentifier(table.name, `"`))
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	updates := make([]string, 0, len(columns))
	for _, column := range columns {
		if !table.keys[column] {
			quoted := monitorQuoteIdentifier(column, "`")
			updates = append(updates, quoted+"=VALUES("+quoted+")")
		}
	}
	if len(updates) == 0 {
		updates = append(updates, monitorQuoteIdentifier(columns[0], "`")+"=VALUES("+monitorQuoteIdentifier(columns[0], "`")+")")
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(columns)), ",")
	statement := "INSERT INTO " + monitorQuoteIdentifier(table.name, "`") + " (" + monitorQuotedColumns(columns, "`") + ") VALUES (" + placeholders + ") ON DUPLICATE KEY UPDATE " + strings.Join(updates, ",")
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

func sqliteMonitorTableColumns(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query("PRAGMA table_info(" + monitorQuoteIdentifier(table, `"`) + ")")
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

func monitorQuoteIdentifier(identifier, quote string) string {
	return quote + strings.ReplaceAll(identifier, quote, quote+quote) + quote
}

func monitorQuotedColumns(columns []string, quote string) string {
	quoted := make([]string, len(columns))
	for i, column := range columns {
		quoted[i] = monitorQuoteIdentifier(column, quote)
	}
	return strings.Join(quoted, ",")
}

func (r MigrationReport) SortedMigrationTables() []string {
	names := make([]string, 0, len(r.Tables))
	for name := range r.Tables {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
