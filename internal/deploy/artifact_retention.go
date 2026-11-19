package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// A tombstone retires selection before deleting bytes. History and immutable
// identities remain intact; failed file removal is retried, never republished.
const liveArtifactSQL = `NOT EXISTS (SELECT 1 FROM artifact_cleanup c WHERE c.artifact_id=artifacts.id)`

type ArtifactRetention struct {
	KeepCount  int `json:"keep_count"`
	Total      int `json:"total"`
	Protected  int `json:"protected"`
	Candidates int `json:"candidates"`
	Unmanaged  int `json:"unmanaged"`
	Pending    int `json:"pending"`
	Cleaned    int `json:"cleaned"`
}

func artifactRetentionSchema() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS build_artifact_outputs (run_id BIGINT NOT NULL,artifact_id BIGINT NOT NULL,role VARCHAR(32) NOT NULL,digest VARCHAR(80) NOT NULL,parent_artifact_id BIGINT NOT NULL,PRIMARY KEY(run_id,artifact_id))`,
		`CREATE TABLE IF NOT EXISTS artifact_retention_gate (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE IF NOT EXISTS artifact_retention_policies (service VARCHAR(191) PRIMARY KEY, keep_count INTEGER NOT NULL, updated_at BIGINT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS artifact_cleanup (artifact_id BIGINT PRIMARY KEY, service VARCHAR(191) NOT NULL, uri TEXT NOT NULL, retired_at BIGINT NOT NULL, cleaned_at BIGINT NOT NULL DEFAULT 0)`,
	}
}
func (s *Store) initArtifactRetention() error {
	if err := s.ensureArtifactOutputIdentity(); err != nil {
		return err
	}
	for _, q := range artifactRetentionSchema() {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}
	_, err := s.db.Exec(`INSERT OR IGNORE INTO artifact_retention_gate(id) VALUES(1)`)
	return err
}

func lockArtifactLifecycle(tx *deployTx) error {
	if _, err := tx.Exec(`UPDATE artifact_retention_gate SET id=id WHERE id=1`); err != nil {
		return err
	}
	var id int
	return tx.QueryRow(`SELECT id FROM artifact_retention_gate WHERE id=1`).Scan(&id)
}

// Reference creation and cleanup acquire the same artifact row lock. This is
// effective across MySQL processes, and SQLite serializes write transactions.
func lockArtifactReferences(tx *deployTx, ids ...int64) error {
	if err := lockArtifactLifecycle(tx); err != nil {
		return err
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	previous := int64(0)
	for _, id := range ids {
		if id <= 0 || id == previous {
			continue
		}
		previous = id
		query := `SELECT id FROM artifacts WHERE id=?`
		if tx.dialect == "mysql" {
			query += ` FOR UPDATE`
		}
		var found int64
		if err := tx.QueryRow(query, id).Scan(&found); err != nil {
			return fmt.Errorf("制品 #%d 不存在", id)
		}
		retiredQuery := `SELECT artifact_id FROM artifact_cleanup WHERE artifact_id=?`
		if tx.dialect == "mysql" {
			retiredQuery += ` FOR UPDATE`
		}
		var retired int64
		err := tx.QueryRow(retiredQuery, id).Scan(&retired)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == nil {
			return fmt.Errorf("制品 #%d 已按保留策略清理，请选择其他制品或重新构建", id)
		}
	}
	return nil
}
func planArtifactIDs(p *DeliveryPlan) []int64 {
	ids := []int64{}
	for _, app := range p.Services {
		ids = append(ids, app.Artifact.ID)
	}
	return ids
}

// Keep all historical release references: RollbackWithTarget can resolve the
// predecessor of ANY historical deployment, not only the latest two versions.
// Do not weaken that contract to meet a numerical storage limit.
func protectedRetentionArtifacts(tx *deployTx, service string) (map[int64]bool, error) {
	keep := map[int64]bool{}
	referencedRuns := map[int64]bool{}
	for _, q := range []string{
		`SELECT artifact_id FROM deploys WHERE service=?`,
		`SELECT artifact_id FROM release_orders WHERE service=?`,
		`SELECT artifact_id FROM pipeline_runs WHERE service=? AND status NOT IN ('success','failed','canceled')`,
		`SELECT artifact_id FROM builds WHERE service=? AND status NOT IN ('success','failed','canceled')`,
		`SELECT a.id FROM artifacts a JOIN pipeline_runs r ON a.source='pipeline-run' AND a.source_id=r.id WHERE a.service=? AND r.status NOT IN ('success','failed','canceled')`,
		`SELECT a.id FROM artifacts a JOIN builds b ON a.source='build' AND a.source_id=b.id WHERE a.service=? AND b.status NOT IN ('success','failed','canceled')`,
	} {
		rows, err := tx.Query(q, service)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			keep[id] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	// Unsuccessful/legacy builds may have no artifact ID. Resolve references by
	// URI or version as well so migration gaps cannot defeat rollback protection.
	rows, err := tx.Query(`SELECT a.id FROM artifacts a WHERE a.service=? AND (EXISTS (SELECT 1 FROM deploys d WHERE d.service=a.service AND (d.artifact_uri=a.uri OR (d.artifact_id=0 AND d.version=a.version))) OR EXISTS (SELECT 1 FROM changes ch WHERE ch.service=a.service AND ch.kind='artifact' AND ch.reference=a.version))`, service)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		keep[id] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	// Read and parse frozen documents rather than searching raw JSON for IDs.
	for _, q := range []string{`SELECT plan FROM delivery_runs`, `SELECT document FROM delivery_version_artifacts`, `SELECT document FROM delivery_environment_artifacts`, `SELECT value FROM deploy_meta WHERE key LIKE 'code_preparation:%'`} {
		rows, err := tx.Query(q)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var raw string
			if err = rows.Scan(&raw); err != nil {
				rows.Close()
				return nil, err
			}
			var doc any
			if json.Unmarshal([]byte(raw), &doc) != nil {
				rows.Close()
				return nil, fmt.Errorf("制品引用快照不可解析，暂停清理")
			}
			collectArtifactReferences(doc, keep)
			collectBuildRunReferences(doc, referencedRuns)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	rows, err = tx.Query(`SELECT id,artifact_id FROM pipeline_runs WHERE service=?`, service)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var runID, artifactID int64
		if err = rows.Scan(&runID, &artifactID); err != nil {
			rows.Close()
			return nil, err
		}
		if referencedRuns[runID] {
			keep[artifactID] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = tx.Query(`SELECT artifact_id,parent_artifact_id FROM build_artifact_outputs WHERE run_id IN (SELECT id FROM pipeline_runs WHERE service=?)`, service)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var child, parent int64
		if err = rows.Scan(&child, &parent); err != nil {
			rows.Close()
			return nil, err
		}
		if keep[child] || keep[parent] {
			keep[child] = true
			keep[parent] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return keep, nil
}
func collectBuildRunReferences(value any, keep map[int64]bool) {
	switch v := value.(type) {
	case map[string]any:
		for _, key := range []string{"run_id", "build_id"} {
			if id, ok := v[key].(float64); ok {
				keep[int64(id)] = true
			}
		}
		for _, child := range v {
			collectBuildRunReferences(child, keep)
		}
	case []any:
		for _, child := range v {
			collectBuildRunReferences(child, keep)
		}
	}
}
func collectArtifactReferences(value any, keep map[int64]bool) {
	switch v := value.(type) {
	case map[string]any:
		if id, ok := v["artifact_id"].(float64); ok {
			keep[int64(id)] = true
		}
		if artifact, ok := v["artifact"].(map[string]any); ok {
			if id, ok := artifact["id"].(float64); ok {
				keep[int64(id)] = true
			}
		}
		// DeliveryRequest maps service -> artifact ID.
		if artifacts, ok := v["artifacts"].(map[string]any); ok {
			for _, id := range artifacts {
				if number, ok := id.(float64); ok {
					keep[int64(number)] = true
				}
			}
		}
		for _, child := range v {
			collectArtifactReferences(child, keep)
		}
	case []any:
		for _, child := range v {
			collectArtifactReferences(child, keep)
		}
	}
}

func (s *Store) managedArtifactPath(uri string) (string, error) {
	root := filepath.Join(s.dataDir, "artifacts")
	if !filepath.IsAbs(uri) {
		return "", fmt.Errorf("非平台管理的制品路径")
	}
	rel, err := filepath.Rel(root, filepath.Clean(uri))
	if err != nil || rel == "." || rel == ".." || len(rel) > 3 && rel[:3] == ".."+string(filepath.Separator) {
		return "", fmt.Errorf("非平台管理的制品路径")
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	expected := filepath.Join(realRoot, rel)
	// Reject symlink files and directories; missing files can finish a previously
	// committed cleanup after a crash, but cannot redirect deletion elsewhere.
	parent, err := filepath.EvalSymlinks(filepath.Dir(uri))
	if err != nil {
		return "", err
	}
	if parent != filepath.Dir(expected) {
		return "", fmt.Errorf("制品路径包含符号链接")
	}
	info, err := os.Lstat(uri)
	if os.IsNotExist(err) {
		return expected, nil
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("制品不是普通文件")
	}
	return expected, nil
}

func canonicalArtifactPath(uri string) string {
	if !filepath.IsAbs(uri) {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(uri); err == nil {
		return resolved
	}
	return filepath.Clean(uri)
}
func artifactPathCounts(tx *deployTx, liveOnly bool) (map[string]int, error) {
	q := `SELECT uri FROM artifacts`
	if liveOnly {
		q += ` WHERE ` + liveArtifactSQL
	}
	rows, err := tx.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var uri string
		if err := rows.Scan(&uri); err != nil {
			return nil, err
		}
		if path := canonicalArtifactPath(uri); path != "" {
			counts[path]++
		}
	}
	return counts, rows.Err()
}

type retentionCandidate struct {
	ID        int64
	URI, Kind string
}

func (s *Store) artifactRetentionPlan(tx *deployTx, service string, keepCount int) (ArtifactRetention, []retentionCandidate, error) {
	result := ArtifactRetention{KeepCount: keepCount}
	candidates := []retentionCandidate{}
	protected, err := protectedRetentionArtifacts(tx, service)
	if err != nil {
		return result, nil, err
	}
	rows, err := tx.Query(`SELECT id,uri,kind FROM artifacts WHERE service=? AND `+liveArtifactSQL+` ORDER BY id DESC`, service)
	if err != nil {
		return result, nil, err
	}
	all := []retentionCandidate{}
	for rows.Next() {
		var a retentionCandidate
		if err = rows.Scan(&a.ID, &a.URI, &a.Kind); err != nil {
			rows.Close()
			return result, nil, err
		}
		all = append(all, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, nil, err
	}
	result.Total = len(all)
	pathCounts, err := artifactPathCounts(tx, false)
	if err != nil {
		return result, nil, err
	}
	for index, a := range all {
		if protected[a.ID] {
			result.Protected++
			continue
		}
		if keepCount == 0 || index < keepCount {
			continue
		}
		if a.Kind != ArtifactJar && a.Kind != ArtifactStatic && a.Kind != ArtifactJavaBundle && a.Kind != ArtifactDockerArchive {
			result.Unmanaged++
			continue
		}
		if _, err := s.managedArtifactPath(a.URI); err != nil {
			result.Unmanaged++
			continue
		}
		if pathCounts[canonicalArtifactPath(a.URI)] > 1 {
			result.Unmanaged++
			continue
		}
		candidates = append(candidates, a)
	}
	result.Candidates = len(candidates)
	err = tx.QueryRow(`SELECT COUNT(*) FROM artifact_cleanup WHERE service=? AND cleaned_at=0`, service).Scan(&result.Pending)
	if err == nil {
		err = tx.QueryRow(`SELECT COUNT(*) FROM artifact_cleanup WHERE service=? AND cleaned_at>0`, service).Scan(&result.Cleaned)
	}
	return result, candidates, err
}
func (s *Store) ArtifactRetention(service string) (ArtifactRetention, error) {
	if s.GetService(service) == nil {
		return ArtifactRetention{}, fmt.Errorf("应用不存在")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return ArtifactRetention{}, err
	}
	defer tx.Rollback()
	count := 0
	err = tx.QueryRow(`SELECT keep_count FROM artifact_retention_policies WHERE service=?`, service).Scan(&count)
	if err != nil && err != sql.ErrNoRows {
		return ArtifactRetention{}, err
	}
	result, _, err := s.artifactRetentionPlan(tx, service, count)
	return result, err
}
func (s *Store) SaveArtifactRetention(service string, count int) error {
	if count < 0 || count > 10000 {
		return fmt.Errorf("保留份数须为 0–10000 的整数，0 表示全部保留")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockArtifactLifecycle(tx); err != nil {
		return err
	}
	var name string
	if err = tx.QueryRow(`SELECT name FROM services WHERE name=?`, service).Scan(&name); err != nil {
		return fmt.Errorf("应用不存在")
	}
	if _, err = tx.Exec(`INSERT INTO artifact_retention_policies(service,keep_count,updated_at) VALUES(?,?,?) ON CONFLICT(service) DO UPDATE SET keep_count=excluded.keep_count,updated_at=excluded.updated_at`, service, count, time.Now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) cleanRetiredArtifacts(service string) error {
	rows, err := s.db.Query(`SELECT artifact_id,uri FROM artifact_cleanup WHERE service=? AND cleaned_at=0`, service)
	if err != nil {
		return err
	}
	pending := []retentionCandidate{}
	for rows.Next() {
		var a retentionCandidate
		if err = rows.Scan(&a.ID, &a.URI); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, a := range pending {
		path, err := s.managedArtifactPath(a.URI)
		if err != nil {
			return err
		}
		// A second record can intentionally share the same file. Such aliases are
		// not retired in the first place; recheck before deleting any bytes.
		aliasTx, err := s.db.Begin()
		if err != nil {
			return err
		}
		paths, err := artifactPathCounts(aliasTx, true)
		aliasTx.Rollback()
		if err != nil {
			return err
		}
		if paths[canonicalArtifactPath(a.URI)] != 0 {
			return fmt.Errorf("制品文件仍被其他记录引用，暂停删除")
		}
		if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		if _, err = s.db.Exec(`UPDATE artifact_cleanup SET cleaned_at=? WHERE artifact_id=? AND cleaned_at=0`, time.Now().Unix(), a.ID); err != nil {
			return err
		}
		s.Audit("retention", "cleanup", "artifact", fmt.Sprint(a.ID), map[string]any{"service": service})
	}
	return nil
}
func (s *Store) ApplyArtifactRetention(service string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockArtifactLifecycle(tx); err != nil {
		return err
	}
	var count int
	q := `SELECT keep_count FROM artifact_retention_policies WHERE service=?`
	if tx.dialect == "mysql" {
		q += ` FOR UPDATE`
	}
	err = tx.QueryRow(q, service).Scan(&count)
	if err == sql.ErrNoRows {
		tx.Rollback()
		return s.cleanRetiredArtifacts(service)
	}
	if err != nil {
		return err
	}
	if count == 0 {
		if err := tx.Commit(); err != nil {
			return err
		}
		return s.cleanRetiredArtifacts(service)
	}
	// Lock all this application's artifacts in stable order before taking the
	// reference snapshot; writers must acquire the same locks before referencing.
	q = `SELECT id FROM artifacts WHERE service=? ORDER BY id`
	if tx.dialect == "mysql" {
		q += ` FOR UPDATE`
	}
	rows, err := tx.Query(q, service)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	_, candidates, err := s.artifactRetentionPlan(tx, service, count)
	if err != nil {
		return err
	}
	for _, a := range candidates {
		var aliases int
		if err = tx.QueryRow(`SELECT COUNT(*) FROM artifacts WHERE uri=? AND id<>?`, a.URI, a.ID).Scan(&aliases); err != nil {
			return err
		}
		if aliases > 0 {
			continue
		} // A shared path cannot be reclaimed independently.
		if _, err = tx.Exec(`INSERT INTO artifact_cleanup(artifact_id,service,uri,retired_at,cleaned_at) VALUES(?,?,?,?,0)`, a.ID, service, a.URI, time.Now().Unix()); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return s.cleanRetiredArtifacts(service)
}
func (s *Store) RunArtifactRetention(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rows, err := s.db.Query(`SELECT service FROM artifact_retention_policies WHERE keep_count>0 UNION SELECT service FROM artifact_cleanup WHERE cleaned_at=0`)
			if err != nil {
				log.Printf("artifact retention: %v", err)
				continue
			}
			names := []string{}
			for rows.Next() {
				var name string
				if err = rows.Scan(&name); err != nil {
					break
				}
				names = append(names, name)
			}
			if err == nil {
				err = rows.Err()
			}
			rows.Close()
			if err != nil {
				log.Printf("artifact retention: %v", err)
				continue
			}
			for _, name := range names {
				if ctx.Err() != nil {
					return
				}
				if err = s.ApplyArtifactRetention(name); err != nil {
					log.Printf("artifact retention %s: %v", name, err)
				}
			}
		}
	}
}
func registerArtifactRetentionRoutes(mux *http.ServeMux, s *Store) {
	path := "/api/services/{name}/artifact-retention"
	mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
		v, err := s.ArtifactRetention(r.PathValue("name"))
		if err != nil {
			deliveryWriteError(w, 400, err)
			return
		}
		deliveryJSON(w, v)
	})
	mux.HandleFunc("PUT "+path, func(w http.ResponseWriter, r *http.Request) {
		if deliveryActor(r) != "local" && r.Header.Get("X-Role") != "admin" {
			deliveryWriteError(w, 403, "仅管理员可配置制品保留策略")
			return
		}
		var body struct {
			KeepCount *int `json:"keep_count"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || body.KeepCount == nil {
			deliveryWriteError(w, 400, "请填写保留份数（整数）")
			return
		}
		name := r.PathValue("name")
		if err := s.SaveArtifactRetention(name, *body.KeepCount); err != nil {
			deliveryWriteError(w, 400, err)
			return
		}
		s.Audit(deliveryActor(r), "configure", "artifact-retention", name, map[string]any{"keep_count": *body.KeepCount})
		deliveryJSON(w, map[string]any{"keep_count": *body.KeepCount, "ok": true})
	})
}
