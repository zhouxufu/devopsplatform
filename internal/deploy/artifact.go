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
	ArtifactJar    = "jar"
	ArtifactStatic = "static-archive"
	ArtifactOCI    = "oci-image"
)

// Artifact is the immutable hand-off between a build and every deployment.
// URI is a local path for file artifacts and image@sha256:digest for OCI images.
type Artifact struct {
	ID        int64             `json:"id"`
	Service   string            `json:"service"`
	Version   string            `json:"version"`
	Kind      string            `json:"kind"`
	URI       string            `json:"uri"`
	Digest    string            `json:"digest"`
	Size      int64             `json:"size,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	Source    string            `json:"source,omitempty"`
	SourceID  int64             `json:"source_id,omitempty"`
	CreatedAt int64             `json:"created_at"`
}

func artifactKindForService(artifactType string) string {
	switch artifactType {
	case "static", ArtifactStatic:
		return ArtifactStatic
	case "image", ArtifactOCI:
		return ArtifactOCI
	default:
		return ArtifactJar
	}
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
	case ArtifactJar, ArtifactStatic:
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
	metadata, _ := json.Marshal(a.Metadata)
	if a.Source != "" && a.SourceID > 0 {
		var id int64
		err := s.db.QueryRow(`SELECT id FROM artifacts WHERE source=? AND source_id=?`, a.Source, a.SourceID).Scan(&id)
		if err == nil {
			a.ID = id
			_, err = s.db.Exec(`UPDATE artifacts SET service=?,version=?,kind=?,uri=?,digest=?,size=?,metadata=? WHERE id=?`,
				a.Service, a.Version, a.Kind, a.URI, a.Digest, a.Size, string(metadata), a.ID)
			return err
		}
		if err != sql.ErrNoRows {
			return err
		}
	}
	res, err := s.db.Exec(`INSERT INTO artifacts(service,version,kind,uri,digest,size,metadata,source,source_id,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		a.Service, a.Version, a.Kind, a.URI, a.Digest, a.Size, string(metadata), a.Source, a.SourceID, a.CreatedAt)
	if err != nil {
		return err
	}
	a.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) GetArtifact(id int64) *Artifact {
	if id <= 0 {
		return nil
	}
	var a Artifact
	var metadata string
	if s.db.QueryRow(`SELECT id,service,version,kind,uri,digest,size,metadata,source,source_id,created_at FROM artifacts WHERE id=?`, id).
		Scan(&a.ID, &a.Service, &a.Version, &a.Kind, &a.URI, &a.Digest, &a.Size, &metadata, &a.Source, &a.SourceID, &a.CreatedAt) != nil {
		return nil
	}
	_ = json.Unmarshal([]byte(metadata), &a.Metadata)
	return &a
}

func (s *Store) Artifacts(service string, limit int) []Artifact {
	query := `SELECT id,service,version,kind,uri,digest,size,metadata,source,source_id,created_at FROM artifacts`
	args := []any{}
	if service != "" {
		query += ` WHERE service=?`
		args = append(args, service)
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
