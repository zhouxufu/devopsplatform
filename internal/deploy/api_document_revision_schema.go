package deploy

import (
	"database/sql"
	"fmt"
	"strings"
)

// Restoring an earlier definition is a new revision, not a build and not a
// mutation/promotion of the old row. Include its frozen parent in uniqueness.
func (s *Store) ensureDocumentRevisionIdentity() error {
	const oldColumns = "application_id,environment,run_id,artifact_id,digest"
	if s.Driver() == "mysql" {
		rows, e := s.db.Query(`SELECT INDEX_NAME,GROUP_CONCAT(COLUMN_NAME ORDER BY SEQ_IN_INDEX SEPARATOR ',') FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='api_document_versions' AND NON_UNIQUE=0 GROUP BY INDEX_NAME`)
		if e != nil {
			return e
		}
		old := ""
		for rows.Next() {
			var name, cols string
			if e = rows.Scan(&name, &cols); e != nil {
				rows.Close()
				return e
			}
			if cols == oldColumns {
				old = name
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if old == "" {
			return nil
		}
		_, e = s.db.Exec("ALTER TABLE api_document_versions DROP INDEX `" + strings.ReplaceAll(old, "`", "``") + "`, ADD UNIQUE KEY document_revision_identity(" + oldColumns + ",baseline_id)")
		return e
	}
	var ddl string
	if e := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='api_document_versions'`).Scan(&ddl); e != nil {
		return e
	}
	compact := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(ddl, " ", ""), "\n", ""))
	if !strings.Contains(compact, "unique("+oldColumns+")") {
		return nil
	}
	var create string
	for _, v := range integrationSchema("INTEGER PRIMARY KEY AUTOINCREMENT", "TEXT") {
		if strings.Contains(v, "CREATE TABLE IF NOT EXISTS api_document_versions (") {
			create = strings.Replace(v, "IF NOT EXISTS api_document_versions", "api_document_versions_revision", 1)
		}
	}
	if create == "" {
		return fmt.Errorf("文档修订表定义不存在")
	}
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec(create); e != nil {
		return e
	}
	var sequence int64
	if e = tx.QueryRow(`SELECT seq FROM sqlite_sequence WHERE name='api_document_versions'`).Scan(&sequence); e != nil && e != sql.ErrNoRows {
		return e
	}
	const columns = "id,application_id,environment,service,run_id,artifact_id,artifact_digest,commit_ref,provider,provider_version,digest,document,created_at,baseline_id,comparison"
	if _, e = tx.Exec(`INSERT INTO api_document_versions_revision(` + columns + `) SELECT ` + columns + ` FROM api_document_versions`); e != nil {
		return e
	}
	if _, e = tx.Exec(`DROP TABLE api_document_versions`); e != nil {
		return e
	}
	if _, e = tx.Exec(`ALTER TABLE api_document_versions_revision RENAME TO api_document_versions`); e != nil {
		return e
	}
	if _, e = tx.Exec(`UPDATE sqlite_sequence SET seq=MAX(seq,?) WHERE name='api_document_versions'`, sequence); e != nil {
		return e
	}
	return tx.Commit()
}
