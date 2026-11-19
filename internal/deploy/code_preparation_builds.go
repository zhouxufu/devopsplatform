package deploy

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// One coordinator owns the CAS document. Build runners are asynchronous;
// polling all of them here avoids concurrent mutation of the preparation.
func (s *Store) advancePreparedBuilds(p *CodePreparation, start func(*PreparedBuild) (int64, error)) (bool, error) {
	limit := p.BuildConcurrency
	if limit == 0 {
		limit = 1
	} // Historical snapshots keep their original serial contract.
	if limit < 1 || limit > 8 {
		return false, fmt.Errorf("构建并发数须为 1–8")
	}
	failed := p.BuildError
	active, success := 0, 0
	for i := range p.Builds {
		b := &p.Builds[i]
		if b.Status == "success" {
			success++
			continue
		}
		if b.Status == "failed" {
			failed = b.Message
			if failed == "" {
				failed = "原构建失败，请核对构建回执"
			}
			continue
		}
		if b.RunID == 0 {
			var existing int64
			err := s.db.QueryRow(`SELECT id FROM pipeline_runs WHERE service=? AND trigger=? ORDER BY id DESC LIMIT 1`, b.Service, "code-preparation:"+p.ID+":"+b.Service).Scan(&existing)
			if err == nil {
				b.RunID = existing
			} else if err != sql.ErrNoRows {
				return false, err
			}
		}
		if b.RunID == 0 {
			continue
		}
		run := s.GetRun(b.RunID)
		if run == nil || run.Service != b.Service || run.Actor != p.Owner || run.Branch != b.Commit || run.Trigger != "code-preparation:"+p.ID+":"+b.Service {
			b.Status, b.Message = "failed", "构建身份或冻结提交不匹配，请核对原构建"
			failed = b.Message
			continue
		}
		if run.Status == "running" || run.Status == "queued" {
			b.Status, b.Message = run.Status, ""
			active++
			continue
		}
		if run.Status != "success" {
			b.Status, b.Message = "failed", fmt.Sprintf("应用 %s 构建失败或中断，请核对构建 #%d", b.Service, run.ID)
			failed = b.Message
			continue
		}
		var actual string
		_ = s.db.QueryRow(`SELECT commit_ref FROM build_run_snapshots WHERE run_id=?`, run.ID).Scan(&actual)
		artifact := s.artifactForEnvironmentRun(run, p.Request.Environment)
		if actual != b.Commit || artifact == nil || artifact.Service != b.Service || artifact.Digest == "" || artifact.Source != "pipeline-run" || artifact.SourceID != run.ID {
			b.Status, b.Message = "failed", "构建来源或制品摘要无法验证"
			failed = b.Message
			continue
		}
		if err := s.validateArtifactBuildVariant(artifact, p.Request.Environment); err != nil {
			b.Status, b.Message = "failed", err.Error()
			failed = b.Message
			continue
		}
		b.ArtifactID, b.ArtifactDigest, b.Status, b.Message = artifact.ID, artifact.Digest, "success", ""
		success++
	}
	if failed == "" {
		for i := range p.Builds {
			b := &p.Builds[i]
			if b.Status == "success" || b.RunID != 0 {
				continue
			}
			if active >= limit {
				b.Status, b.Message = "queued", "等待本次交付的构建名额"
				continue
			}
			if err := s.preparationSourceValid(p); err != nil {
				failed = err.Error()
				break
			}
			b.Status, b.Message = "starting", ""
			p.LeaseUntil = time.Now().Add(2 * time.Minute).Unix()
			if err := s.saveCodePreparation(p); err != nil {
				return false, err
			}
			id, err := start(b)
			if errors.Is(err, ErrRunConflict) {
				b.Status, b.Message = "queued", "等待 Runner 空闲"
				continue
			}
			if err == nil && id <= 0 {
				err = fmt.Errorf("构建返回身份未知，请核对原操作")
			}
			if err != nil {
				b.Status, b.Message = "failed", err.Error()
				failed = b.Message
				break
			}
			b.RunID, b.Status = id, "running"
			active++
			// Save the returned identity before another application's start.
			if err = s.saveCodePreparation(p); err != nil {
				return false, err
			}
		}
	}
	p.LeaseUntil = time.Now().Add(2 * time.Minute).Unix()
	p.BuildError = failed
	p.Message = fmt.Sprintf("构建完成 %d/%d，进行中 %d，并发上限 %d", success, len(p.Builds), active, limit)
	if failed != "" {
		p.Message = "已有构建失败，停止启动后续应用；等待已启动构建结束：" + failed
	}
	if err := s.saveCodePreparation(p); err != nil {
		return false, err
	}
	if failed != "" && active == 0 {
		return false, fmt.Errorf("%s", failed)
	}
	return success == len(p.Builds), nil
}
