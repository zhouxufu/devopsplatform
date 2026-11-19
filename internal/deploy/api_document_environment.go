package deploy

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"logplatform/pkg/apidoc"
	"time"
)

func (s *Store) documentEnvironmentDefault(environment string) bool {
	env := s.GetEnvironment(environment)
	return env != nil && (env.Purpose == "development" || env.Purpose == "test")
}
func (s *Store) environmentDocumentSource(sv *Service, environment string) (apidoc.Source, bool, error) {
	if s.GetEnvironment(environment) == nil {
		return apidoc.Source{}, false, fmt.Errorf("文档环境不存在")
	}
	var raw string
	e := s.db.QueryRow(`SELECT document FROM api_document_environment_sources WHERE application_id=? AND environment=?`, sv.ApplicationID, environment).Scan(&raw)
	if e == sql.ErrNoRows {
		source, e := s.apiDocumentSource(sv)
		source.Revision = 0
		source.OnBuild = source.Provider != "" && s.documentEnvironmentDefault(environment)
		return source, true, e
	}
	if e != nil {
		return apidoc.Source{}, false, e
	}
	var source apidoc.Source
	if json.Unmarshal([]byte(raw), &source) != nil {
		return source, false, fmt.Errorf("环境文档配置无效")
	}
	return source, false, nil
}
func (s *Store) saveEnvironmentDocumentSource(sv *Service, environment string, v apidoc.Source, actor string) error {
	old, inherited, e := s.environmentDocumentSource(sv, environment)
	if e != nil {
		return e
	}
	if v.Revision != old.Revision {
		return fmt.Errorf("文档配置已被修改，请重新载入")
	}
	v.Revision++
	raw, _ := json.Marshal(v)
	if inherited {
		_, e = s.db.Exec(`INSERT INTO api_document_environment_sources(application_id,environment,document,actor,updated_at) VALUES(?,?,?,?,?)`, sv.ApplicationID, environment, string(raw), actor, time.Now().Unix())
		return e
	}
	previous, _ := json.Marshal(old)
	res, e := s.db.Exec(`UPDATE api_document_environment_sources SET document=?,actor=?,updated_at=? WHERE application_id=? AND environment=? AND document=?`, string(raw), actor, time.Now().Unix(), sv.ApplicationID, environment, string(previous))
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return fmt.Errorf("文档配置已被修改，请重新载入")
	}
	return nil
}
func (s *Store) automaticDocumentEnvironments(sv *Service) ([]string, error) {
	out := []string{}
	for _, env := range s.Environments() {
		source, _, e := s.environmentDocumentSource(sv, env.Name)
		if e != nil {
			return nil, e
		}
		if source.Provider != "" && source.OnBuild {
			out = append(out, env.Name)
		}
	}
	return out, nil
}
func (s *Store) resolveDocumentSource(sv *Service, environment string) (apidoc.Source, error) {
	if environment == "" {
		return s.apiDocumentSource(sv)
	}
	v, _, e := s.environmentDocumentSource(sv, environment)
	return v, e
}
func (s *Store) buildDocumentContext(runID int64) (string, *apidoc.Frozen, error) {
	var sealed string
	if e := s.db.QueryRow(`SELECT config_cipher FROM build_run_snapshots WHERE run_id=?`, runID).Scan(&sealed); e != nil {
		return "", nil, e
	}
	raw, e := s.decryptSecret(sealed)
	if e != nil {
		return "", nil, e
	}
	var v buildSnapshot
	if json.Unmarshal([]byte(raw), &v) != nil {
		return "", nil, fmt.Errorf("构建快照无效")
	}
	return v.Service.DocumentEnvironment, v.Service.APIDocumentation, nil
}
func documentStageName(sv *Service) string {
	if sv.APIDocumentation == nil {
		return "生成接口文档"
	}
	name := "生成接口文档 · " + sv.APIDocumentation.Source.Provider
	if sv.APIDocumentation.Environment != "" {
		name += " · " + sv.APIDocumentation.Environment
	}
	return name
}

const apiDocumentStage = "api-document-generation"

func (pr *PipelineRunner) pipelineSteps(sv *Service) []StepInst {
	steps := append([]StepInst(nil), sv.Steps...)
	if len(steps) == 0 {
		steps = ExpandTemplate(*sv)
	}
	if sv.APIDocumentation == nil {
		return steps
	}
	at := len(steps)
	for i, step := range steps {
		if d := pr.lib.Get(step.Step); d != nil && d.Kind == "deploy" {
			at = i
			break
		}
	}
	steps = append(steps, StepInst{})
	copy(steps[at+1:], steps[at:])
	steps[at] = StepInst{Step: apiDocumentStage}
	return steps
}
func (pr *PipelineRunner) plannedStages(sv *Service) []Stage {
	out := []Stage{}
	for _, step := range pr.pipelineSteps(sv) {
		name := step.Step
		if step.Step == apiDocumentStage {
			name = documentStageName(sv)
		} else if def := pr.lib.Get(step.Step); def != nil {
			name = def.Name
		}
		out = append(out, Stage{Name: name, Status: "pending"})
	}
	return out
}
