package deploy

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func (s *Store) applicationResourceRevision(service string) int64 {
	var value string
	_ = s.db.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, "application_resources_revision:"+service).Scan(&value)
	revision, _ := strconv.ParseInt(value, 10, 64)
	return revision
}

// A monotonic revision and the resource document commit in the same transaction.
// Trusted internal migrations may omit expected; all public saves supply it.
func (s *Store) advanceApplicationResourceRevision(tx *deployTx, service string, expected *int64) error {
	key := "application_resources_revision:" + service
	if _, err := tx.Exec(`INSERT OR IGNORE INTO deploy_meta(key,value) VALUES(?,'0')`, key); err != nil {
		return err
	}
	var value string
	if err := tx.QueryRow(s.resourceGroupLockQuery(), key).Scan(&value); err != nil {
		return err
	}
	revision, err := strconv.ParseInt(value, 10, 64)
	if err != nil || revision < 0 {
		return fmt.Errorf("应用资源修订无效，请核对配置")
	}
	if expected != nil && *expected != revision {
		return fmt.Errorf("应用资源已被其他窗口修改，请保留草稿并重新核对差异")
	}
	_, err = tx.Exec(`UPDATE deploy_meta SET value=? WHERE key=?`, strconv.FormatInt(revision+1, 10), key)
	return err
}

// Freeze the effective scope of pre-upgrade catalog references once. Adding a
// future environment must never silently extend an existing application's scope.
func (s *Store) migrateLegacyNacosResourceScopes() error {
	rows, err := s.db.Query(`SELECT key,value FROM deploy_meta WHERE key LIKE 'application_resources_v2:%'`)
	if err != nil {
		return err
	}
	values := map[string]string{}
	for rows.Next() {
		var key, raw string
		if err = rows.Scan(&key, &raw); err != nil {
			rows.Close()
			return err
		}
		values[key] = raw
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for key, raw := range values {
		var value ApplicationResources
		if json.Unmarshal([]byte(raw), &value) != nil || value.NacosScopeVersion >= 1 || len(value.NacosConfigs) == 0 {
			continue
		}
		refs := []ApplicationNacosConfig{}
		for _, ref := range value.NacosConfigs {
			if ref.Environment != "" {
				refs = append(refs, ref)
				continue
			}
			count := 0
			for _, environment := range s.Environments() {
				connection := s.environmentNacos(environment.Name)
				if c := s.GetConnection(connection); c != nil && c.Kind == "nacos" {
					copy := ref
					copy.Environment = environment.Name
					copy.Connection = connection
					refs = append(refs, copy)
					count++
				}
			}
			if count == 0 {
				refs = append(refs, ref)
			}
		}
		value.NacosConfigs = refs
		value.NacosScopeVersion = 1
		encoded, e := json.Marshal(value)
		if e != nil {
			return e
		}
		tx, e := s.db.Begin()
		if e != nil {
			return e
		}
		if e = s.advanceApplicationResourceRevision(tx, strings.TrimPrefix(key, "application_resources_v2:"), nil); e != nil {
			tx.Rollback()
			return e
		}
		var current string
		if e = tx.QueryRow(s.resourceGroupLockQuery(), key).Scan(&current); e != nil {
			tx.Rollback()
			return e
		}
		if current != raw {
			tx.Rollback()
			continue
		}
		if _, e = tx.Exec(`UPDATE deploy_meta SET value=? WHERE key=?`, string(encoded), key); e != nil {
			tx.Rollback()
			return e
		}
		if e = tx.Commit(); e != nil {
			return e
		}
	}
	return nil
}
