package deploy

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// Scoped automation cannot replace another environment or edit inherited groups.
type resourceBindingPatch struct {
	Revision            int64                            `json:"revision"`
	Environment         string                           `json:"environment"`
	Requirements        []ApplicationResourceRequirement `json:"requirements,omitempty"`
	Bindings            []ApplicationEnvironmentResource `json:"bindings"`
	ConnectionRevisions map[string]string                `json:"connection_revisions,omitempty"`
}

func (s *Store) prepareResourceBindingPatch(service string, patch resourceBindingPatch, verifyConnections bool) (ApplicationResources, error) {
	current := s.ApplicationResources(service)
	if current.Error != "" {
		return current, fmt.Errorf("应用资源配置待核对，请先在编辑器修复")
	}
	if current.Revision != patch.Revision {
		return current, fmt.Errorf("应用资源已被其他窗口修改，请重新预览")
	}
	if patch.Environment == "" || s.GetEnvironment(patch.Environment) == nil || len(patch.Bindings)+len(patch.Requirements) == 0 || len(patch.Bindings) > 100 || len(patch.Requirements) > 100 {
		return current, fmt.Errorf("请明确选择环境和 1–100 条绑定")
	}
	result := current
	if current.CompositionVersion != 2 {
		result.CompositionVersion = 2
		if current.ResourceGroup != "" {
			result.ResourceGroups = []string{current.ResourceGroup}
			result.GroupRevisions = map[string]int64{current.ResourceGroup: current.GroupRevision}
			result.OwnRequirements = nil
			result.OwnBindings = nil
		} else {
			result.OwnRequirements = append([]ApplicationResourceRequirement{}, current.Requirements...)
			result.OwnBindings = append([]ApplicationEnvironmentResource{}, current.Bindings...)
		}
	}
	own := map[string]string{}
	all := map[string]string{}
	for _, req := range result.OwnRequirements {
		own[req.Alias] = req.Kind
	}
	for _, req := range current.Requirements {
		all[req.Alias] = req.Kind
	}
	for _, req := range patch.Requirements {
		for env := range req.Sources {
			if env != patch.Environment {
				return result, fmt.Errorf("范围内来源说明只能更新所选环境")
			}
		}
		if !validResourceName(req.Alias) || !validApplicationResourceKind(req.Kind) {
			return result, fmt.Errorf("资源声明无效")
		}
		if kind, exists := all[req.Alias]; exists {
			if own[req.Alias] == "" || kind != req.Kind {
				return result, fmt.Errorf("不能通过范围内绑定修改共享、目录引用或已有资源类型")
			}
			if source, ok := req.Sources[patch.Environment]; ok {
				for i := range result.OwnRequirements {
					if result.OwnRequirements[i].Alias == req.Alias {
						current := result.OwnRequirements[i].Sources
						copy := map[string]ResourceSourceEvidence{}
						for env, v := range current {
							copy[env] = v
						}
						copy[patch.Environment] = source
						result.OwnRequirements[i].Sources = copy
					}
				}
			}
			continue
		}
		req.Service = service
		req.GroupName = ""
		req.GroupRevision = 0
		result.OwnRequirements = append(result.OwnRequirements, req)
		own[req.Alias] = req.Kind
		all[req.Alias] = req.Kind
	}
	seen := map[string]bool{}
	for _, binding := range patch.Bindings {
		if binding.Environment != patch.Environment || seen[binding.Alias] || own[binding.Alias] == "" {
			return result, fmt.Errorf("只允许修改所选环境的自有资源，每个别名仅一条")
		}
		seen[binding.Alias] = true
		c := s.GetConnection(binding.Connection)
		if c == nil || c.Kind != own[binding.Alias] {
			return result, fmt.Errorf("连接不存在或类型不匹配")
		}
		if verifyConnections && patch.ConnectionRevisions[c.Name] != s.connectionCheckFingerprint(c) {
			return result, fmt.Errorf("目标连接已变化或未冻结，请重新预览")
		}
	}
	kept := []ApplicationEnvironmentResource{}
	for _, binding := range result.OwnBindings {
		if binding.Environment != patch.Environment || !seen[binding.Alias] {
			kept = append(kept, binding)
		}
	}
	for _, binding := range patch.Bindings {
		binding.Service = service
		kept = append(kept, binding)
	}
	result.OwnBindings = kept
	result.ExpectedRevision = &patch.Revision
	if _, err := s.composeResources(service, result); err != nil {
		return result, err
	}
	return result, nil
}
func registerResourcePatchRoutes(mux *http.ServeMux, s *Store) {
	for _, preview := range []bool{false, true} {
		preview := preview
		path := "POST /api/services/{name}/resources/bindings"
		if preview {
			path += "/preview"
		}
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if deliveryActor(r) != "local" && r.Header.Get("X-Role") != "admin" {
				deliveryWriteError(w, 403, "资源配置需要管理员权限")
				return
			}
			service := r.PathValue("name")
			if !s.hasResourceApplication(service) {
				deliveryWriteError(w, 404, "应用不存在")
				return
			}
			var patch resourceBindingPatch
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&patch) != nil {
				deliveryWriteError(w, 400, "资源绑定参数无效")
				return
			}
			s.resourceConnectionMu.Lock()
			defer s.resourceConnectionMu.Unlock()
			value, err := s.prepareResourceBindingPatch(service, patch, !preview)
			if err != nil {
				deliveryWriteError(w, 409, err)
				return
			}
			if preview {
				patch.ConnectionRevisions = map[string]string{}
				for _, b := range patch.Bindings {
					patch.ConnectionRevisions[b.Connection] = s.connectionCheckFingerprint(s.GetConnection(b.Connection))
				}
				before := []ApplicationEnvironmentResource{}
				for _, b := range s.ApplicationResources(service).Bindings {
					for _, p := range patch.Bindings {
						if b.Alias == p.Alias && b.Environment == patch.Environment {
							before = append(before, b)
						}
					}
				}
				deliveryJSON(w, map[string]any{"service": service, "environment": patch.Environment, "revision": patch.Revision, "before": before, "after": patch.Bindings, "request": patch, "no_execution": true})
				return
			}
			if err = s.SaveApplicationResources(service, value); err != nil {
				deliveryWriteError(w, 409, err)
				return
			}
			s.Audit(deliveryActor(r), "update", "application-resources", service, map[string]any{"environment": patch.Environment, "bindings": len(patch.Bindings)})
			deliveryJSON(w, map[string]any{"service": service, "environment": patch.Environment, "revision": s.ApplicationResources(service).Revision, "bindings": patch.Bindings, "saved": true, "no_execution": true})
		})
	}
}
