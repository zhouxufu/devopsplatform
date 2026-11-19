package deploy

import "fmt"

// Defaults are suggestions, never an exemption from the environment's gates.
// Keep them with the versioned policy so changing them invalidates preflight.
type DeliveryDefaults struct {
	StrategyKind   string                                `json:"strategy_kind,omitempty"`
	TemplateID     int64                                 `json:"template_id,omitempty"`
	ArtifactSource string                                `json:"artifact_source,omitempty"`
	Applications   map[string]ApplicationDeliveryDefault `json:"applications,omitempty"`
}
type ApplicationDeliveryDefault struct {
	BuildVariant   BuildVariant `json:"build_variant,omitempty"`
	ArtifactSource string       `json:"artifact_source,omitempty"`
	CodePolicy     CodePolicy   `json:"code_policy"`
}

func (s *Store) validateDeliveryDefaults(d DeliveryDefaults) error {
	if d.StrategyKind != "" && d.StrategyKind != "standard" && d.StrategyKind != "custom" {
		return fmt.Errorf("默认交付方式无效")
	}
	if d.StrategyKind == "custom" {
		t := s.DeliveryTemplate(d.TemplateID)
		if t == nil || t.Workbench != "" {
			return fmt.Errorf("请选择有效的部署编排模板")
		}
	} else if d.TemplateID != 0 {
		return fmt.Errorf("默认模板只能用于自定义编排")
	}
	validSource := func(v string) bool { return v == "" || v == "build" || v == "existing" }
	if !validSource(d.ArtifactSource) || len(d.Applications) > 200 {
		return fmt.Errorf("默认制品来源或应用数量无效")
	}
	for name, app := range d.Applications {
		if s.GetService(name) == nil || !validSource(app.ArtifactSource) {
			return fmt.Errorf("默认应用或制品来源无效：%s", name)
		}
		if app.BuildVariant != (BuildVariant{}) {
			if _, err := applyBuildVariant(s.GetService(name), app.BuildVariant); err != nil {
				return fmt.Errorf("应用 %s：%w", name, err)
			}
		}
		p := app.CodePolicy
		if p.Mode != "" && p.Mode != "integration" && p.Mode != "isolated" {
			return fmt.Errorf("应用 %s 的分支策略无效", name)
		}
		if p.BaseBranch != "" && !validPolicyBranch(p.BaseBranch) || p.TargetBranch != "" && !validPolicyBranch(p.TargetBranch) {
			return fmt.Errorf("应用 %s 的默认分支无效", name)
		}
	}
	return nil
}

// An absent map keeps old submitted plans compatible. New clients explicitly
// choose independently for every app; unknown apps/values fail closed.
func deliveryArtifactSources(req DeliveryRequest, scope *DeliveryScope, fixed []string) (map[string]string, error) {
	apps := map[string]string{}
	if scope == nil {
		for _, name := range fixed {
			apps[name] = "existing"
		}
	}
	if scope != nil {
		for _, c := range scope.Changes {
			if (c.Kind == "code" || c.Kind == "artifact") && c.Service != "" {
				apps[c.Service] = "existing"
			}
		}
	}
	if len(req.ArtifactSources) == 0 {
		return apps, nil
	}
	if len(req.ArtifactSources) != len(apps) {
		return nil, fmt.Errorf("请为本次每个应用选择制品来源")
	}
	for name, mode := range req.ArtifactSources {
		if _, ok := apps[name]; !ok || (mode != "build" && mode != "existing") {
			return nil, fmt.Errorf("应用 %s 的制品来源无效", name)
		}
		if mode == "build" {
			code := false
			if scope != nil {
				for _, c := range scope.Changes {
					if c.Service == name {
						if c.Kind == "artifact" {
							return nil, fmt.Errorf("应用 %s 包含制品引用，不能同时按代码构建", name)
						}
						code = code || c.Kind == "code"
					}
				}
			}
			if !code {
				return nil, fmt.Errorf("应用 %s 没有关联可构建的代码变更", name)
			}
		}
		apps[name] = mode
	}
	return apps, nil
}

// A range checkbox cannot remove code already baked into a version artifact.
// Excluding an entire app is fine; slicing the same app requires a new scoped
// version/build, not re-labeling the old image as a smaller release.
func validateVersionArtifactSubset(scope *DeliveryScope, version *ReleaseVersion, pinned VersionArtifacts) error {
	if scope == nil || version == nil || len(pinned.Bindings) == 0 {
		return nil
	}
	apps, selected := map[string]bool{}, map[int64]bool{}
	for _, c := range scope.Changes {
		selected[c.ID] = true
		if c.Kind == "code" || c.Kind == "artifact" {
			apps[c.Service] = true
		}
	}
	for _, c := range version.Changes {
		if (c.Kind == "code" || c.Kind == "artifact") && apps[c.Service] && !selected[c.ID] {
			return fmt.Errorf("应用 %s 的版本制品包含本次排除的代码变更 #%d；请按实际范围创建版本并重新构建，不能通过取消勾选裁剪镜像", c.Service, c.ID)
		}
	}
	return nil
}
