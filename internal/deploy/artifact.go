package deploy

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	ArtifactJar           = "jar"
	ArtifactStatic        = "static-archive"
	ArtifactOCI           = "oci-image"
	ArtifactDockerArchive = "docker-image-archive"
)

// Artifact is the immutable hand-off between a build and every deployment.
// URI is a local path for file artifacts and image@sha256:digest for OCI images.
type Artifact struct {
	SourceNumber int64             `json:"source_number,omitempty"`
	ID           int64             `json:"id"`
	Service      string            `json:"service"`
	Version      string            `json:"version"`
	Kind         string            `json:"kind"`
	URI          string            `json:"uri"`
	Digest       string            `json:"digest"`
	Size         int64             `json:"size,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	Source       string            `json:"source,omitempty"`
	SourceID     int64             `json:"source_id,omitempty"`
	CreatedAt    int64             `json:"created_at"`
}

func artifactKindForService(artifactType string) string {
	switch artifactType {
	case ArtifactJavaBundle:
		return ArtifactJavaBundle
	case "static", ArtifactStatic:
		return ArtifactStatic
	case "image", ArtifactOCI:
		return ArtifactOCI
	default:
		return ArtifactJar
	}
}

// Image applications can be delivered through a registry or an offline archive.
func artifactMatchesService(kind, artifactType string) bool {
	return kind == artifactKindForService(artifactType) || kind == ArtifactDockerArchive && artifactKindForService(artifactType) == ArtifactOCI
}

func describeArtifact(service, version, kind, uri, digest string) (Artifact, error) {
	a := Artifact{Service: service, Version: version, Kind: kind, URI: strings.TrimSpace(uri), Digest: strings.TrimSpace(digest), CreatedAt: time.Now().Unix()}
	if a.Kind == "" {
		a.Kind = ArtifactJar
	}
	if a.URI == "" {
		return a, fmt.Errorf("制品 URI 为空")
	}
	switch a.Kind {
	case ArtifactJar, ArtifactStatic, ArtifactJavaBundle, ArtifactDockerArchive:
		info, err := os.Stat(a.URI)
		if err != nil || info.IsDir() {
			return a, fmt.Errorf("制品文件不可用: %s", a.URI)
		}
		actual, size, err := artifactMetadata(a.URI)
		if err != nil {
			return a, err
		}
		if a.Digest != "" && a.Digest != actual {
			return a, fmt.Errorf("制品摘要不匹配")
		}
		a.Digest, a.Size = actual, size
		if a.Kind == ArtifactJavaBundle {
			m, e := inspectJavaBundle(a.URI, a.Service, 0)
			if e != nil {
				return a, e
			}
			a.Metadata = map[string]string{"main_jar": m.MainJar, "bundle_format": m.Format, "bundle_files": fmt.Sprint(len(m.Files))}
		}
		if a.Kind == ArtifactDockerArchive {
			a.Metadata, err = dockerArchiveMetadata(a.URI)
			if err != nil {
				return a, err
			}
		}
	case ArtifactOCI:
		if !strings.HasPrefix(a.Digest, "sha256:") {
			return a, fmt.Errorf("OCI 镜像必须包含 sha256 digest")
		}
		// A pushed image is addressed immutably. Local image IDs may be built for
		// validation, but a deployment driver will reject them.
		if strings.Contains(a.URI, "@") && !strings.HasSuffix(a.URI, a.Digest) {
			return a, fmt.Errorf("镜像 URI 与 digest 不匹配")
		}
	default:
		return a, fmt.Errorf("不支持的制品类型: %s", a.Kind)
	}
	return a, nil
}

func (s *Store) RegisterArtifact(a *Artifact) error {
	if a == nil {
		return fmt.Errorf("制品为空")
	}
	if a.CreatedAt == 0 {
		a.CreatedAt = time.Now().Unix()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockArtifactLifecycle(tx); err != nil {
		return err
	}
	q := `SELECT id FROM artifacts WHERE uri=? ORDER BY id`
	if tx.dialect == "mysql" {
		q += ` FOR UPDATE`
	}
	rows, err := tx.Query(q, a.URI)
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
	retiredRows, err := tx.Query(`SELECT uri FROM artifact_cleanup`)
	if err != nil {
		return err
	}
	retiredPath := false
	for retiredRows.Next() {
		var uri string
		if err = retiredRows.Scan(&uri); err != nil {
			retiredRows.Close()
			return err
		}
		if a.URI == uri || canonicalArtifactPath(a.URI) != "" && canonicalArtifactPath(a.URI) == canonicalArtifactPath(uri) {
			retiredPath = true
		}
	}
	err = retiredRows.Err()
	retiredRows.Close()
	if err != nil {
		return err
	}
	if retiredPath {
		return fmt.Errorf("制品路径已清理，不能重新使用")
	}
	metadata, _ := json.Marshal(a.Metadata)
	if a.Source != "" && a.SourceID > 0 {
		var id int64
		err := tx.QueryRow(`SELECT id FROM artifacts WHERE source=? AND source_id=? AND kind=? AND digest=?`, a.Source, a.SourceID, a.Kind, a.Digest).Scan(&id)
		if err == nil {
			a.ID = id
			if err := lockArtifactReferences(tx, id); err != nil {
				return err
			}
			var service, version, uri string
			if err = tx.QueryRow(`SELECT service,version,uri FROM artifacts WHERE id=?`, id).Scan(&service, &version, &uri); err != nil {
				return err
			}
			if service != a.Service || version != a.Version || uri != a.URI {
				return fmt.Errorf("同一构建制品摘要已登记，不能改写原始身份")
			}
			_, err = tx.Exec(`UPDATE artifacts SET metadata=? WHERE id=?`, string(metadata), a.ID)
			if err != nil {
				return err
			}
			return tx.Commit()
		}
		if err != sql.ErrNoRows {
			return err
		}
	}
	res, err := tx.Exec(`INSERT INTO artifacts(service,version,kind,uri,digest,size,metadata,source,source_id,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		a.Service, a.Version, a.Kind, a.URI, a.Digest, a.Size, string(metadata), a.Source, a.SourceID, a.CreatedAt)
	if err != nil {
		return err
	}
	a.ID, _ = res.LastInsertId()
	return tx.Commit()
}

func (s *Store) GetArtifact(id int64) *Artifact {
	if id <= 0 {
		return nil
	}
	var a Artifact
	var metadata string
	if s.db.QueryRow(`SELECT id,service,version,kind,uri,digest,size,metadata,source,source_id,created_at FROM artifacts WHERE id=? AND `+liveArtifactSQL, id).
		Scan(&a.ID, &a.Service, &a.Version, &a.Kind, &a.URI, &a.Digest, &a.Size, &metadata, &a.Source, &a.SourceID, &a.CreatedAt) != nil {
		return nil
	}
	_ = json.Unmarshal([]byte(metadata), &a.Metadata)
	if a.Source == "pipeline-run" {
		a.SourceNumber = s.PipelineRunNumber(a.SourceID, a.Service)
	}
	return &a
}

func (s *Store) Artifacts(service string, limit int) []Artifact {
	query := `SELECT id,service,version,kind,uri,digest,size,metadata,source,source_id,created_at FROM artifacts`
	args := []any{}
	if service != "" {
		query += ` WHERE service=? AND ` + liveArtifactSQL
		args = append(args, service)
	}
	if service == "" {
		query += ` WHERE ` + liveArtifactSQL
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return []Artifact{}
	}
	defer rows.Close()
	out := []Artifact{}
	for rows.Next() {
		var a Artifact
		var metadata string
		if rows.Scan(&a.ID, &a.Service, &a.Version, &a.Kind, &a.URI, &a.Digest, &a.Size, &metadata, &a.Source, &a.SourceID, &a.CreatedAt) == nil {
			_ = json.Unmarshal([]byte(metadata), &a.Metadata)
			out = append(out, a)
		}
	}
	rows.Close()
	for i := range out {
		if out[i].Source == "pipeline-run" {
			out[i].SourceNumber = s.PipelineRunNumber(out[i].SourceID, out[i].Service)
		}
	}
	return out
}

func (s *Store) ArtifactForBuild(b *Build, sv *Service) *Artifact {
	if b == nil {
		return nil
	}
	if a := s.GetArtifact(b.ArtifactID); a != nil {
		return a
	}
	kind := b.ArtifactKind
	if kind == "" && sv != nil {
		kind = artifactKindForService(sv.ArtifactType)
	}
	a, err := describeArtifact(b.Service, b.Version, kind, b.Artifact, b.Digest)
	if err != nil {
		return nil
	}
	return &a
}

// RecordPipelineArtifact atomically links a pipeline run to an immutable
// artifact and creates the successful build record used by later deployments.
func (s *Store) RecordPipelineArtifact(runID int64, actor string, a Artifact) (*Artifact, error) {
	a.Source, a.SourceID = "pipeline-run", runID
	// Display provenance is derived from the encrypted run snapshot, never from
	// mutable application settings or a user-supplied artifact label.
	var sealed string
	if s.db.QueryRow(`SELECT config_cipher FROM build_run_snapshots WHERE run_id=?`, runID).Scan(&sealed) == nil {
		if plain, err := s.decryptSecret(sealed); err == nil {
			var snapshot buildSnapshot
			if json.Unmarshal([]byte(plain), &snapshot) == nil {
				if a.Metadata == nil {
					a.Metadata = map[string]string{}
				}
				a.Metadata["build_variant"] = snapshot.Service.BuildVariant
				a.Metadata["build_configuration_digest"] = buildVariantDigest(&snapshot.Service, &snapshot.Delivery)
			}
		}
	}
	if err := s.RegisterArtifact(&a); err != nil {
		return nil, err
	}
	if _, err := s.db.Exec(`UPDATE pipeline_runs SET version=?,artifact=?,artifact_id=?,artifact_kind=?,artifact_digest=?,artifact_size=? WHERE id=?`,
		a.Version, a.URI, a.ID, a.Kind, a.Digest, a.Size, runID); err != nil {
		return nil, err
	}
	_, err := s.db.Exec(`INSERT INTO builds(service,version,status,artifact,artifact_id,artifact_kind,artifact_digest,artifact_size,pipeline_run_id,actor,started_at,ended_at)
		SELECT ?,?,'success',?,?,?,?,?,?,?,started_at,strftime('%s','now') FROM pipeline_runs WHERE id=?
		ON CONFLICT(pipeline_run_id) WHERE pipeline_run_id>0 DO UPDATE SET version=excluded.version,artifact=excluded.artifact,
		artifact_id=excluded.artifact_id,artifact_kind=excluded.artifact_kind,artifact_digest=excluded.artifact_digest,artifact_size=excluded.artifact_size`,
		a.Service, a.Version, a.URI, a.ID, a.Kind, a.Digest, a.Size, runID, actor, runID)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *Store) backfillArtifacts() error {
	rows, err := s.db.Query(`SELECT b.id,b.service,b.version,b.artifact,b.artifact_digest,b.artifact_size,s.artifact_type
		FROM builds b LEFT JOIN services s ON s.name=b.service
		WHERE b.status='success' AND COALESCE(b.artifact,'')<>'' AND COALESCE(b.artifact_id,0)=0`)
	if err != nil {
		return err
	}
	type legacy struct {
		id                                          int64
		service, version, uri, digest, artifactType string
		size                                        int64
	}
	var list []legacy
	for rows.Next() {
		var v legacy
		if rows.Scan(&v.id, &v.service, &v.version, &v.uri, &v.digest, &v.size, &v.artifactType) == nil {
			list = append(list, v)
		}
	}
	rows.Close()
	for _, v := range list {
		a := Artifact{Service: v.service, Version: v.version, Kind: artifactKindForService(v.artifactType), URI: v.uri,
			Digest: v.digest, Size: v.size, Source: "legacy-build", SourceID: v.id, CreatedAt: time.Now().Unix()}
		// Old local files may already have been removed. Preserve their records;
		// deployment validation will still fail closed if the content is missing.
		if a.Digest == "" && a.Kind != ArtifactOCI {
			if digest, size, err := artifactMetadata(a.URI); err == nil {
				a.Digest, a.Size = digest, size
			}
		}
		if a.Digest == "" {
			continue
		}
		if err := s.RegisterArtifact(&a); err != nil {
			return err
		}
		if _, err := s.db.Exec(`UPDATE builds SET artifact_id=?,artifact_kind=? WHERE id=?`, a.ID, a.Kind, v.id); err != nil {
			return err
		}
	}
	runRows, err := s.db.Query(`SELECT r.id,r.service,r.version,r.artifact,r.actor,s.artifact_type
		FROM pipeline_runs r LEFT JOIN services s ON s.name=r.service
		WHERE COALESCE(r.artifact,'')<>'' AND COALESCE(r.artifact_id,0)=0`)
	if err != nil {
		return err
	}
	type legacyRun struct {
		id                                         int64
		service, version, uri, actor, artifactType string
	}
	var runs []legacyRun
	for runRows.Next() {
		var run legacyRun
		if runRows.Scan(&run.id, &run.service, &run.version, &run.uri, &run.actor, &run.artifactType) == nil {
			runs = append(runs, run)
		}
	}
	runRows.Close()
	for _, run := range runs {
		a, err := describeArtifact(run.service, run.version, artifactKindForService(run.artifactType), run.uri, "")
		if err != nil {
			continue
		}
		if _, err := s.RecordPipelineArtifact(run.id, run.actor, a); err != nil {
			return err
		}
	}
	return nil
}
