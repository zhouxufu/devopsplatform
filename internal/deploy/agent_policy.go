package deploy

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Observations of the same policy used by preparation. Never creates a plan,
// resolves live Git refs, or treats a configured branch as inclusion evidence.
func (s *Store) workflowBindingDiagnostic(environment string) (map[string]any, error) {
	e := s.GetEnvironment(environment)
	if e == nil {
		return nil, fmt.Errorf("环境不存在")
	}
	p, err := s.deliveryPolicy(environment)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"environment": e.Name, "environment_display_name": e.DisplayName, "policy_revision": p.Revision, "workflow_template_id": p.WorkflowTemplateID, "hotfix_template_id": p.HotfixTemplateID, "protected": e.Protected, "purpose": e.Purpose, "checked_at": time.Now().Unix(), "status": "unbound", "missing_phases": []string{"preparation"}}
	if p.WorkflowTemplateID > 0 {
		t := s.DeliveryTemplate(p.WorkflowTemplateID)
		if t == nil {
			out["status"] = "template_missing"
		} else {
			out["template_revision_digest"] = digestJSON(t)
			out["template_mode"] = t.Mode
			out["preparation_node_count"] = len(t.Preparation)
			out["release_node_count"] = len(t.Nodes)
			if t.Workbench != "" || t.Mode != "version" {
				out["status"] = "invalid_template"
			} else if preparationTemplate(t) == nil {
				out["status"] = "preparation_missing"
			} else {
				out["status"] = "bound"
				out["missing_phases"] = []string{}
			}
		}
	}
	return out, nil
}
func propertyNames(raw string) []string {
	out := []string{}
	for _, line := range strings.Split(raw, "\n") {
		k, _, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && k != "" && !strings.HasPrefix(k, "#") {
			out = append(out, k)
		}
	}
	return out
}
func (s *Store) applicationCodePolicy(service, environment string) (map[string]any, error) {
	sv := s.GetService(service)
	if sv == nil {
		return nil, fmt.Errorf("应用不存在")
	}
	state, err := s.applicationDeliveryDefault(service, environment)
	if err != nil {
		return nil, err
	}
	v := ApplicationDeliveryDefault{}
	if state.Value != nil {
		v = *state.Value
	}
	effective, err := applyBuildVariant(sv, v.BuildVariant)
	if err != nil {
		return nil, err
	}
	binding, err := s.workflowBindingDiagnostic(environment)
	if err != nil {
		return nil, err
	}
	nodes := []any{}
	p, err := s.deliveryPolicy(environment)
	if err != nil {
		return nil, err
	}
	if t := s.DeliveryTemplate(p.WorkflowTemplateID); t != nil {
		for _, n := range t.Preparation {
			if n.Kind != "code-prepare" && n.Kind != "code-verify" {
				continue
			}
			policy, ok := n.Policies[service]
			origin := "node_application_override"
			if !ok || policy == (CodePolicy{}) {
				policy = v.CodePolicy
				origin = "application_environment"
			}
			if policy == (CodePolicy{}) && n.DefaultCodePolicy != nil {
				policy = *n.DefaultCodePolicy
				origin = "node_default"
			}
			nodes = append(nodes, map[string]any{"node_id": n.ID, "code_policy": policy, "source": origin})
		}
	}
	effectivePolicy := v.CodePolicy
	if len(nodes) == 1 {
		effectivePolicy = nodes[0].(map[string]any)["code_policy"].(CodePolicy)
	}
	branchType := "unconfigured"
	if effectivePolicy.Mode == "integration" {
		branchType = "fixed"
	}
	if effectivePolicy.Mode == "isolated" {
		branchType = "feature"
	}
	source := v.ArtifactSource
	if source == "" {
		source = state.ArtifactSource
	}
	return map[string]any{"service": service, "environment": environment, "policy_revision": state.Revision, "policy_digest": digestJSON([]any{p, sv, nodes, binding["template_revision_digest"]}), "code_policy": effectivePolicy, "application_code_policy": v.CodePolicy, "code_nodes": nodes, "requires_scope_resolution": len(nodes) > 1, "policy_precedence": "node application override > application environment > node default; placeholders resolved only with explicit scope", "branch_type": branchType, "application_default_branch": sv.Branch, "requirement_source": "change.reference (selected change IDs; never substitutes for target branch)", "merge_method": "git merge --no-ff", "build_variant": map[string]any{"name": v.BuildVariant.Name, "maven_profiles": effective.MavenProfiles, "gradle_tasks": effective.GradleTasks, "maven_property_names": propertyNames(effective.MavenProperties), "gradle_project_property_names": propertyNames(effective.GradleProjectProperties), "gradle_system_property_names": propertyNames(effective.GradleSystemProperties)}, "build_configuration": map[string]any{"tool": sv.BuildTool, "artifact_type": sv.ArtifactType, "maven_goals": sv.MavenGoals, "custom_command_configured": sv.BuildCmd != "", "steps_count": len(sv.Steps)}, "artifact_rules": map[string]any{"source": source, "glob": sv.ArtifactGlob, "requires_environment_provenance": v.BuildVariant.Name != ""}, "workflow_binding": binding, "git_refs_resolved": false, "sha_evidence": "get_code_preparation: repositories[].sources[].commit / target_commit / result_commit; result_commit exists only after merge"}, nil
}
func registerAgentPolicyRoutes(mux *http.ServeMux, s *Store) {
	mux.HandleFunc("GET /api/environments/{name}/workflow-diagnostic", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.workflowBindingDiagnostic(r.PathValue("name"))
		if e != nil {
			deliveryWriteError(w, 400, e)
			return
		}
		deliveryJSON(w, v)
	})
	mux.HandleFunc("GET /api/services/{name}/environments/{environment}/code-policy", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.applicationCodePolicy(r.PathValue("name"), r.PathValue("environment"))
		if e != nil {
			deliveryWriteError(w, 400, e)
			return
		}
		deliveryJSON(w, v)
	})
}
