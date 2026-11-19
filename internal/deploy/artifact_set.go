package deploy

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The existing primary artifact remains the build/deploy default. All outputs
// share a run identity; intermediate outputs never become a second deployment.
func (s *Store) ArtifactsForRun(id int64) []Artifact {
	rows, err := s.db.Query(`SELECT id FROM artifacts WHERE source='pipeline-run' AND source_id=? AND `+liveArtifactSQL+` ORDER BY id`, id)
	if err != nil {
		return []Artifact{}
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	out := []Artifact{}
	for _, id := range ids {
		if a := s.GetArtifact(id); a != nil {
			if a.Metadata != nil {
				delete(a.Metadata, "build_command")
			}
			out = append(out, *a)
		}
	}
	return out
}

func (s *Store) artifactMatchesEnvironment(a *Artifact, sv *Service, environment string) bool {
	if a == nil || sv == nil || a.Service != sv.Name {
		return false
	}
	target := s.GetServiceEnvironment(sv.Name, environment)
	if target == nil || !target.ManualTarget() {
		return artifactMatchesService(a.Kind, sv.ArtifactType)
	}
	expected := target.DeployConfig["manual_artifact_kind"]
	if expected == "" {
		expected = artifactKindForService(sv.ArtifactType)
	}
	if a.Kind != expected {
		return false
	}
	if artifactMatchesService(a.Kind, sv.ArtifactType) {
		return true
	}
	// A Java image exposes only a server-verified output of this successful
	// build. Metadata describes whether it is the image input or a separate
	// exported output; that display text never authorizes an arbitrary upload.
	if sv.ArtifactType != "image" || a.Kind != ArtifactJar || a.Source != "pipeline-run" {
		return false
	}
	run := s.GetRun(a.SourceID)
	if run == nil || (run.Status != "success" && run.Status != "waiting_deploy") {
		return false
	}
	image := s.GetArtifact(run.ArtifactID)
	return image != nil && (image.Kind == ArtifactOCI || image.Kind == ArtifactDockerArchive) && image.Service == a.Service && image.SourceID == a.SourceID && s.pairedCompilation(a, run.ArtifactID)
}

func (s *Store) pairedCompilation(a *Artifact, parent int64) bool {
	var count int
	if a == nil {
		return false
	}
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM build_artifact_outputs WHERE run_id=? AND artifact_id=? AND role IN ('compilation','build-output') AND digest=? AND parent_artifact_id=?`, a.SourceID, a.ID, a.Digest, parent).Scan(&count)
	return count == 1
}

func (s *Store) artifactBuildComplete(a *Artifact) error {
	if a == nil {
		return fmt.Errorf("制品不存在或已清理")
	}
	if a.Source != "pipeline-run" {
		return nil
	}
	run := s.GetRun(a.SourceID)
	if run == nil || run.Service != a.Service || !(run.ArtifactID == a.ID && run.ArtifactDigest == a.Digest || s.pairedCompilation(a, run.ArtifactID)) {
		return fmt.Errorf("制品与原始构建关联不一致")
	}
	if run.Status != "success" && run.Status != "waiting_deploy" {
		var ready string
		if run.Status != "running" || s.db.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, fmt.Sprintf("build-ready:%d", run.ID)).Scan(&ready) != nil || ready != fmt.Sprintf("%d:%s", run.ArtifactID, run.ArtifactDigest) {
			return fmt.Errorf("制品所属构建尚未成功完成，不能发布或下载")
		}
	}
	return nil
}

// Keep exactly the bytes from the private Docker context already validated as
// this run's fresh Java compilation, never rescan an arbitrary newest JAR.
func retainDockerJar(params, metadata map[string]string, output string) (string, error) {
	if metadata["compiled_input_step"] != "java-build" {
		return "", nil
	}
	source := filepath.Join(params["CONTEXT"], "app.jar")
	dst := filepath.Join(output, "compiled-jar", "application.jar")
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return "", err
	}
	in, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(dst), "compiled-*.jar")
	if err != nil {
		return "", err
	}
	name := out.Name()
	_, err = io.Copy(out, in)
	closeErr := out.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(name)
		return "", err
	}
	digest, _, err := artifactMetadata(name)
	if err != nil || digest != metadata["compiled_input_digest"] {
		os.Remove(name)
		return "", fmt.Errorf("保留 JAR 与镜像编译输入摘要不一致")
	}
	if err = os.Chmod(name, 0444); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

func registerArtifactSetRoutes(mux *http.ServeMux, s *Store, root string) {
	mux.HandleFunc("GET /api/pipeline/runs/{id}/artifacts", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if s.GetRun(id) == nil {
			deliveryWriteError(w, 404, "构建不存在")
			return
		}
		deliveryJSON(w, s.ArtifactsForRun(id))
	})
	mux.HandleFunc("GET /api/artifacts/{id}/download", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		a := s.GetArtifact(id)
		if a == nil {
			deliveryWriteError(w, 404, "制品不存在或已清理")
			return
		}
		if err := s.artifactBuildComplete(a); err != nil {
			deliveryWriteError(w, 409, err)
			return
		}
		if a.Kind != ArtifactJar && a.Kind != ArtifactStatic && a.Kind != ArtifactJavaBundle && a.Kind != ArtifactDockerArchive {
			deliveryWriteError(w, 409, "镜像请使用固定摘要的仓库地址拉取；文件下载仅适用于 JAR、Java 整包、静态包或已生成的镜像离线包")
			return
		}
		artifactPath, _ := filepath.Abs(a.URI)
		storageRoot, _ := filepath.Abs(root)
		resolved, err := filepath.EvalSymlinks(artifactPath)
		allowed, rootErr := filepath.EvalSymlinks(storageRoot)
		rel, relErr := filepath.Rel(allowed, resolved)
		if err != nil || rootErr != nil || relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			deliveryWriteError(w, 409, "制品不在托管存储中，无法下载")
			return
		}
		f, err := os.Open(resolved)
		if err != nil {
			deliveryWriteError(w, 410, "制品文件不可用，历史记录保留")
			return
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			deliveryWriteError(w, 409, "制品文件无效")
			return
		}
		// Verify the same open file handle which will be streamed, including ranges.
		digest, size, err := artifactReaderMetadata(f)
		if err != nil || digest != a.Digest || size != a.Size {
			deliveryWriteError(w, 409, "制品内容与登记摘要不一致，拒绝下载")
			return
		}
		if _, err = f.Seek(0, io.SeekStart); err != nil {
			deliveryWriteError(w, 500, "制品读取失败")
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		extension := filepath.Ext(a.URI)
		if a.Kind == ArtifactJavaBundle {
			extension = ".tar.gz"
		}
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": a.Service + "-" + strconv.FormatInt(a.ID, 10) + extension}))
		w.Header().Set("X-Artifact-Digest", a.Digest)
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method == http.MethodGet {
			s.Audit(deliveryActor(r), "download", "artifact", strconv.FormatInt(id, 10), map[string]any{"digest": a.Digest})
		}
		http.ServeContent(w, r, filepath.Base(a.URI), info.ModTime(), f)
	})
}

// Used by downloaded-file verification without reopening a mutable path.
func artifactReaderMetadata(r io.Reader) (string, int64, error) {
	h := sha256.New()
	n, err := io.Copy(h, r)
	if err != nil {
		return "", 0, err
	}
	return fmt.Sprintf("sha256:%x", h.Sum(nil)), n, nil
}

// Marshal helpers keep run output metadata compatible with existing primary
// artifact APIs and with old records that have no secondary output.
func (s *Store) loadManualPublication(id int64) *ManualPublication {
	var raw string
	if s.db.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, fmt.Sprintf("manual-publication:%d", id)).Scan(&raw) != nil {
		return nil
	}
	var p ManualPublication
	if json.Unmarshal([]byte(raw), &p) != nil {
		return nil
	}
	return &p
}

func (s *Store) linkCompilationArtifact(runID int64, jar, image *Artifact) error {
	if jar == nil || image == nil || jar.Kind != ArtifactJar || (image.Kind != ArtifactOCI && image.Kind != ArtifactDockerArchive) || jar.SourceID != runID || image.SourceID != runID || jar.Service != image.Service || (jar.Digest != image.Metadata["compiled_input_digest"] && jar.Digest != image.Metadata["related_output_digest"]) {
		return fmt.Errorf("镜像与编译 JAR 无法核对")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockArtifactReferences(tx, jar.ID, image.ID); err != nil {
		return err
	}
	role := "compilation"
	if image.Metadata["related_output_digest"] != "" {
		role = "build-output"
	}
	if _, err = tx.Exec(`INSERT INTO build_artifact_outputs(run_id,artifact_id,role,digest,parent_artifact_id) VALUES(?,?,?,?,?)`, runID, jar.ID, role, jar.Digest, image.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) artifactForEnvironmentRun(run *PipelineRun, environment string) *Artifact {
	if run == nil {
		return nil
	}
	sv := s.GetService(run.Service)
	primary := s.GetArtifact(run.ArtifactID)
	if s.artifactMatchesEnvironment(primary, sv, environment) {
		return primary
	}
	var chosen *Artifact
	for _, a := range s.ArtifactsForRun(run.ID) {
		copy := a
		if s.artifactMatchesEnvironment(&copy, sv, environment) {
			if chosen != nil {
				return nil
			}
			chosen = &copy
		}
	}
	return chosen
}

// Old installations enforced one output per run. Preserve rows while replacing
// that identity with one immutable output per kind/content digest.
func (s *Store) ensureArtifactOutputIdentity() error {
	if s.Driver() == "mysql" {
		var count int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='artifacts' AND INDEX_NAME='idx_artifacts_source'`).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			if _, err := s.db.Exec(`ALTER TABLE artifacts DROP INDEX idx_artifacts_source`); err != nil {
				return err
			}
		}
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='artifacts' AND INDEX_NAME='idx_artifacts_outputs'`).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			_, err := s.db.Exec(`ALTER TABLE artifacts ADD UNIQUE KEY idx_artifacts_outputs(source,source_id_unique,kind,digest)`)
			return err
		}
		return nil
	}
	if _, err := s.db.Exec(`DROP INDEX IF EXISTS idx_artifacts_source`); err != nil {
		return err
	}
	_, err := s.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_artifacts_outputs ON artifacts(source,source_id,kind,digest) WHERE source<>'' AND source_id>0`)
	return err
}
