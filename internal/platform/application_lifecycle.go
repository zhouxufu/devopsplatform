package platform

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"logplatform/internal/appcatalog"
	"net/http"
	"strings"
	"time"
)

var errApplicationHasDeployment = errors.New("应用仍关联部署配置，请先到应用与构建删除部署配置，再删除目录应用")
var errDeploymentCheckUnavailable = errors.New("无法核实部署关联，未删除应用，请确认部署服务正常后重试")

// Use the configured backend, not user-supplied addresses. Never use the
// ordinary service listing here: that route observes the catalog recursively.
func (g *Gateway) applicationDeploymentReferences(ctx context.Context) ([]appcatalog.ApplicationReference, error) {
	g.mu.RLock()
	addr := ""
	for _, name := range []string{"delivery-core", "deploy"} {
		for _, m := range g.manifests {
			if m.Name == name && m.AdminAddr != "" {
				addr = m.AdminAddr
				break
			}
		}
		if addr != "" {
			break
		}
	}
	g.mu.RUnlock()
	if addr == "" {
		return nil, errDeploymentCheckUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/api/application-references", nil)
	if err != nil {
		return nil, errDeploymentCheckUnavailable
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errDeploymentCheckUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errDeploymentCheckUnavailable
	}
	var body struct {
		Complete     bool                              `json:"complete"`
		Applications []appcatalog.ApplicationReference `json:"applications"`
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 2<<20))
	if decoder.Decode(&body) != nil || !body.Complete || body.Applications == nil {
		return nil, errDeploymentCheckUnavailable
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, errDeploymentCheckUnavailable
	}
	for _, ref := range body.Applications {
		if strings.TrimSpace(ref.Name) == "" {
			return nil, errDeploymentCheckUnavailable
		}
	}
	return body.Applications, nil
}

func (r *Registry) migrateApplicationLifecycle() error {
	ddl := `CREATE TABLE IF NOT EXISTS application_lifecycle_events (application_id VARCHAR(64) NOT NULL, actor VARCHAR(191) NOT NULL, action VARCHAR(32) NOT NULL, previous_status VARCHAR(32) NOT NULL, sources TEXT NOT NULL, created_at BIGINT NOT NULL)`
	if r.dialect == "mysql" {
		ddl += " ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin"
	}
	_, err := r.db.Exec(ddl)
	return err
}

// Archive only the directory presentation. Preserve identities, deployment
// history and source information. Telemetry reuses the ID without restoring
// visibility; deliberate manual/deploy onboarding can restore the application.
func (r *Registry) archiveApplication(id, actor string) error {
	return r.archiveApplicationContext(context.Background(), id, actor)
}

func (r *Registry) archiveApplicationContext(ctx context.Context, id, actor string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	query := `SELECT name,COALESCE(status,'discovered'),COALESCE(sources,'[]') FROM services WHERE application_id=?`
	if r.dialect == "mysql" {
		query += " FOR UPDATE"
	}
	var name, status, raw string
	if err := tx.QueryRow(query, id).Scan(&name, &status, &raw); err != nil {
		return err
	}
	var sources []string
	if err := json.Unmarshal([]byte(raw), &sources); err != nil {
		return err
	}
	if status == "archived" {
		return nil
	}
	if r.deploymentReferences != nil {
		names := []string{name}
		rows, err := tx.Query(`SELECT identifier FROM application_identities WHERE application_id=? AND source='deploy'`, id)
		if err != nil {
			return err
		}
		for rows.Next() {
			var alias string
			if err := rows.Scan(&alias); err != nil {
				rows.Close()
				return err
			}
			names = append(names, alias)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		refs, err := r.deploymentReferences(ctx)
		if err != nil {
			return fmt.Errorf("%w", errDeploymentCheckUnavailable)
		}
		if appcatalog.HasApplicationReference(refs, id, names) {
			return errApplicationHasDeployment
		}
	} else if containsString(sources, "deploy") {
		return errApplicationHasDeployment
	}
	if actor == "" {
		actor = "local"
	}
	kept := []string{}
	for _, source := range sources {
		if source != "deploy" {
			kept = append(kept, source)
		}
	}
	if _, err := tx.Exec(`UPDATE services SET status='archived',sources=?,updated=? WHERE application_id=?`, mustJSON(kept), time.Now().Unix(), id); err != nil {
		return err
	}
	if containsString(sources, "deploy") {
		if _, err := tx.Exec(`INSERT INTO application_lifecycle_events(application_id,actor,action,previous_status,sources,created_at) VALUES(?,?,?,?,?,?)`, id, actor, "detach_stale_deploy", status, raw, time.Now().Unix()); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO application_lifecycle_events(application_id,actor,action,previous_status,sources,created_at) VALUES(?,?,?,?,?,?)`, id, actor, "archive", status, raw, time.Now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Registry) applicationLifecycleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("DELETE /api/registry/applications/{id}", func(w http.ResponseWriter, req *http.Request) {
		// Platform authMiddleware applies the existing administrator boundary.
		if err := r.archiveApplicationContext(req.Context(), req.PathValue("id"), currentUser(req)); err != nil {
			status, message := http.StatusInternalServerError, "删除目录应用失败，请刷新核对后重试"
			switch {
			case errors.Is(err, sql.ErrNoRows):
				status, message = http.StatusNotFound, "应用已不存在，请刷新列表"
			case errors.Is(err, errApplicationHasDeployment):
				status, message = http.StatusConflict, err.Error()
			case errors.Is(err, errDeploymentCheckUnavailable):
				status, message = http.StatusServiceUnavailable, err.Error()
			}
			writeJSONStatus(w, status, map[string]string{"error": message})
			return
		}
		writeJSON(w, map[string]bool{"ok": true, "archived": true})
	})
}
