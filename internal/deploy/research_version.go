package deploy

import (
	"fmt"
)

func (s *Store) bindResearchPreparedArtifacts(v *DeliveryRun) error {
	if v.Status != "success" || v.Plan.Version == nil {
		return nil
	}
	if v.Plan.PreparationRequest != nil {
		// Explicit association for readiness evidence, without repurposing the
		// version creation never owns a separate preparation template.
		if _, err := s.db.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, fmt.Sprintf("version-workflow-preparation:%d", v.Plan.Version.ID), fmt.Sprint(v.ID)); err != nil {
			return err
		}
		if _, err := s.db.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, fmt.Sprintf("version-workflow-preparation:%d:%s", v.Plan.Version.ID, v.Plan.Environment.Name), fmt.Sprint(v.ID)); err != nil {
			return err
		}
	}
	expected := map[string]bool{}
	for _, c := range v.Plan.Scope.Changes {
		if c.Kind == "code" || c.Kind == "artifact" {
			expected[c.Service] = true
		}
	}
	bindings := map[string]VersionArtifactBinding{}
	for _, step := range v.Steps {
		if step.Kind != "code-prepare" && step.Kind != "code-verify" {
			continue
		}
		p := s.CodePreparation(step.Detail)
		if p == nil || p.State != "ready" {
			return fmt.Errorf("代码准备结果未就绪")
		}
		for _, b := range p.Builds {
			if _, exists := bindings[b.Service]; exists {
				return fmt.Errorf("同一应用在多个代码节点重复构建")
			}
			bindings[b.Service] = VersionArtifactBinding{Service: b.Service, ArtifactID: b.ArtifactID, CodeConfirmed: true}
		}
	}
	if len(bindings) == 0 {
		return nil
	} // A manual-only flow leaves artifact selection explicit.
	if len(bindings) != len(expected) {
		return nil // Manual-only applications still require explicit full artifact binding at delivery.
	}
	out := []VersionArtifactBinding{}
	for _, b := range bindings {
		out = append(out, b)
	}
	_, err := s.SaveVersionArtifacts(v.Plan.Version.ID, out, v.Owner, v.Plan.Environment.Name)
	return err
}
