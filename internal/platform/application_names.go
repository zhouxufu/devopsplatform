package platform

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"logplatform/internal/appcatalog"
	"net/http"
	"time"
)

func (r *Registry) migrateApplicationNames() error {
	if r.dialect == "mysql" {
		var n int
		if err := r.db.QueryRow(`SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='services' AND COLUMN_NAME='display_name'`).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := r.db.Exec(`ALTER TABLE services ADD COLUMN display_name VARCHAR(400) NOT NULL DEFAULT ''`); err != nil {
				return err
			}
		}
	} else {
		rows, err := r.db.Query(`SELECT display_name FROM services LIMIT 0`)
		if err == nil {
			rows.Close()
		} else if _, err := r.db.Exec(`ALTER TABLE services ADD COLUMN display_name TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	ddl := `CREATE TABLE IF NOT EXISTS application_name_events (application_id VARCHAR(64) NOT NULL, actor VARCHAR(191) NOT NULL, previous_name TEXT NOT NULL, display_name TEXT NOT NULL, created_at BIGINT NOT NULL)`
	if r.dialect == "mysql" {
		ddl += " ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin"
	}
	_, err := r.db.Exec(ddl)
	return err
}

func (r *Registry) setApplicationDisplayName(id, label, expected, actor string) error {
	if actor == "" {
		actor = "local"
	}
	label, err := appcatalog.ValidateDisplayName(label)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current string
	query := `SELECT COALESCE(NULLIF(display_name,''),name) FROM services WHERE application_id=?`
	if r.dialect == "mysql" {
		query += " FOR UPDATE"
	}
	if err := tx.QueryRow(query, id).Scan(&current); err != nil {
		return err
	}
	if current != expected {
		return fmt.Errorf("应用名称已被修改，请刷新后重试")
	}
	if current == label {
		return nil
	}
	result, err := tx.Exec(`UPDATE services SET display_name=? WHERE application_id=? AND COALESCE(NULLIF(display_name,''),name)=?`, label, id, current)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return fmt.Errorf("应用名称已被修改，请刷新后重试")
	}
	if _, err := tx.Exec(`INSERT INTO application_name_events(application_id,actor,previous_name,display_name,created_at) VALUES(?,?,?,?,?)`, id, actor, current, label, time.Now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Registry) applicationNameRoutes(mux *http.ServeMux) {
	mux.HandleFunc("PUT /api/registry/applications/{id}/display-name", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			DisplayName string `json:"display_name"`
			Expected    string `json:"expected_display_name"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, req.Body, 4096))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			http.Error(w, `{"error":"bad request"}`, 400)
			return
		}
		if _, err := appcatalog.ValidateDisplayName(body.DisplayName); err != nil {
			writeJSONStatus(w, 400, map[string]string{"error": err.Error()})
			return
		}
		if err := r.setApplicationDisplayName(req.PathValue("id"), body.DisplayName, body.Expected, currentUser(req)); err != nil {
			status := 409
			if err == sql.ErrNoRows {
				status = 404
			}
			writeJSONStatus(w, status, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, map[string]string{"display_name": appcatalog.DisplayName(body.DisplayName, ""), "application_id": req.PathValue("id")})
	})
}
