package deploy

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"logplatform/internal/appcatalog"
	"net/http"
)

func (s *Store) migrateApplicationNames() error {
	if s.db.dialect == "mysql" {
		var count int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='services' AND COLUMN_NAME='display_name'`).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			_, err := s.db.Exec(`ALTER TABLE services ADD COLUMN display_name VARCHAR(400) NOT NULL DEFAULT ''`)
			return err
		}
		return nil
	}
	if rows, err := s.db.Query(`SELECT display_name FROM services LIMIT 0`); err == nil {
		rows.Close()
		return nil
	}
	_, err := s.db.Exec(`ALTER TABLE services ADD COLUMN display_name TEXT NOT NULL DEFAULT ''`)
	return err
}

func (s *Store) applicationNameRoutes(mux *http.ServeMux, catalog *appcatalog.Observer, connected bool, wj func(http.ResponseWriter, any), writeError func(http.ResponseWriter, int, any), actorOf func(*http.Request) string) {
	mux.HandleFunc("PUT /api/services/{name}/display-name", func(w http.ResponseWriter, r *http.Request) {
		// Same administrator boundary as application configuration. Standalone
		// local mode follows the existing local actor contract.
		if actorOf(r) != "local" && r.Header.Get("X-Role") != "admin" {
			writeError(w, 403, "仅管理员可修改应用名称")
			return
		}
		var body struct {
			DisplayName string `json:"display_name"`
			Expected    string `json:"expected_display_name"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil {
			writeError(w, 400, "bad request")
			return
		}
		label, err := appcatalog.ValidateDisplayName(body.DisplayName)
		if err != nil {
			writeError(w, 400, err)
			return
		}
		current := s.GetService(r.PathValue("name"))
		if current == nil {
			writeError(w, 404, "应用不存在")
			return
		}
		if connected {
			if err := catalog.RenamePresentation(current.ApplicationID, label, body.Expected, r.Header.Get("Cookie")); err != nil {
				writeError(w, 409, err)
				return
			}
		} else if current.DisplayName != body.Expected {
			writeError(w, 409, "应用名称已被修改，请刷新后重试")
			return
		}
		if err := s.SetApplicationDisplayName(current.Name, label, current.DisplayName); err != nil {
			writeError(w, 409, err)
			return
		}
		s.Audit(actorOf(r), "rename_display_name", "application", current.Name, map[string]any{"previous_name": body.Expected, "display_name": label, "identifier": current.Identifier, "application_id": current.ApplicationID})
		wj(w, s.GetService(current.Name))
	})
}

// SetApplicationDisplayName only touches presentation metadata. Never update
// services.name/application_id or any immutable execution/snapshot record.
func (s *Store) SetApplicationDisplayName(name, label, expected string) error {
	label, err := appcatalog.ValidateDisplayName(label)
	if err != nil {
		return err
	}
	query := `UPDATE services SET display_name=? WHERE name=? AND COALESCE(NULLIF(display_name,''),name)=?`
	if s.db.dialect == "mysql" {
		query = `UPDATE services SET display_name=? WHERE name=? AND BINARY COALESCE(NULLIF(display_name,''),name)=BINARY ?`
	}
	result, err := s.db.Exec(query, label, name, expected)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		current := s.GetService(name)
		if current == nil {
			return sql.ErrNoRows
		}
		if current.DisplayName == label && expected == label {
			return nil
		}
		return fmt.Errorf("应用名称已被修改，请刷新后重试")
	}
	return nil
}
