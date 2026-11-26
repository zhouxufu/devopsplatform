package deploy

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Only unified deployment templates own a preparation phase. The human/Git
// engine marker is internal; standalone research templates are not adapted.
func preparationTemplate(t *DeliveryTemplate) *DeliveryTemplate {
	if t == nil || t.Workbench != "" || len(t.Preparation) == 0 {
		return nil
	}
	raw, _ := json.Marshal(t)
	var out DeliveryTemplate
	_ = json.Unmarshal(raw, &out)
	if t.Workbench == "" && len(t.Preparation) > 0 {
		out.Workbench = "research"
		out.Nodes = out.Preparation
	}
	return &out
}

func validateWorkflowBinding(req DeliveryRequest, env Environment, policy DeliveryPolicy) error {
	if req.Scene != "" && req.Scene != "normal" && req.Scene != "hotfix" {
		return fmt.Errorf("交付场景仅支持 normal 或 hotfix")
	}
	id := policy.WorkflowTemplateID
	if req.Scene == "hotfix" {
		if env.Purpose != "production" {
			return fmt.Errorf("Hotfix 须选择生产用途环境")
		}
		if policy.HotfixTemplateID > 0 {
			id = policy.HotfixTemplateID
		}
	}
	if id > 0 && (req.StrategyKind != "custom" || req.TemplateID != id) {
		return fmt.Errorf("目标环境已绑定交付流程 #%d，请按绑定流程重新生成交付", id)
	}
	return nil
}

func validPolicyBranch(s string) bool {
	s = strings.ReplaceAll(s, "{version}", "version")
	s = strings.ReplaceAll(s, "{work_item}", "work-item")
	return validPreparationBranch(s)
}

func validateCodePolicy(p CodePolicy) bool {
	return (p.Mode == "integration" || p.Mode == "isolated") && validPolicyBranch(p.TargetBranch) && (p.Mode != "isolated" || validPolicyBranch(p.BaseBranch))
}

func expandCodePolicy(p CodePolicy, scope *DeliveryScope, v *ReleaseVersion) (CodePolicy, error) {
	for _, target := range []*string{&p.BaseBranch, &p.TargetBranch} {
		if strings.Contains(*target, "{version}") {
			if v == nil {
				return p, fmt.Errorf("版本分支占位符要求选择发布版本")
			}
			*target = strings.ReplaceAll(*target, "{version}", v.Version)
		}
		if strings.Contains(*target, "{work_item}") {
			if len(scope.WorkItems) != 1 {
				return p, fmt.Errorf("工作项分支占位符要求本次只选择一个需求 / Bug")
			}
			*target = strings.ReplaceAll(*target, "{work_item}", fmt.Sprint(scope.WorkItems[0].ID))
		}
	}
	if (p.Mode != "integration" && p.Mode != "isolated") || !validPreparationBranch(p.TargetBranch) || p.Mode == "isolated" && !validPreparationBranch(p.BaseBranch) {
		return p, fmt.Errorf("代码策略不完整或占位符展开后的分支无效")
	}
	return p, nil
}

func resolvePreparationPolicies(nodes []DeliveryNode, scope *DeliveryScope, v *ReleaseVersion, defaults DeliveryDefaults, sources map[string]string) error {
	seen := map[string]bool{}
	missing := map[string]bool{}
	for i := range nodes {
		n := &nodes[i]
		if n.Kind != "code-prepare" && n.Kind != "code-verify" {
			continue
		}
		if n.ApplicationScope != "delivery" {
			return fmt.Errorf("代码准备节点必须跟随交付范围")
		}
		{
			overrides := n.Policies
			n.Policies = map[string]CodePolicy{}
			for _, c := range scope.Changes {
				if c.Kind != "code" || len(sources) > 0 && sources[c.Service] != "build" {
					continue
				}
				p, ok := overrides[c.Service]
				if !ok || p == (CodePolicy{}) {
					p = defaults.Applications[c.Service].CodePolicy
				}
				if p == (CodePolicy{}) && n.DefaultCodePolicy != nil {
					p = *n.DefaultCodePolicy
				}
				if !validateCodePolicy(p) {
					missing[c.Service] = true
					continue
				}
				n.Policies[c.Service] = p
			}
		}
		if len(n.Policies) > 20 {
			return fmt.Errorf("单个代码节点最多处理 20 个应用")
		}
		for app, p := range n.Policies {
			if seen[app] {
				return fmt.Errorf("应用 %s 在多个代码节点重复，请每个应用配置一个代码准备节点", app)
			}
			matches := false
			for _, c := range scope.Changes {
				matches = matches || c.Kind == "code" && c.Service == app
			}
			if !matches {
				return fmt.Errorf("代码节点应用 %s 不在本次代码范围", app)
			}
			p, err := expandCodePolicy(p, scope, v)
			if err != nil {
				return fmt.Errorf("应用 %s：%w", app, err)
			}
			n.Policies[app], seen[app] = p, true
		}
	}
	if len(missing) > 0 {
		var blocked, configured []string
		for _, change := range scope.Changes {
			if change.Kind != "code" {
				continue
			}
			label := fmt.Sprintf("%s #%d", change.Service, change.ID)
			if missing[change.Service] {
				blocked = append(blocked, label)
			} else if seen[change.Service] {
				configured = append(configured, label)
			}
		}
		message := "缺少代码分支策略，请到应用 → 环境 → 配置环境 → 代码与制品策略补齐。待配置：" + strings.Join(blocked, "、")
		if len(configured) > 0 {
			message += "。分支策略已配置：" + strings.Join(configured, "、")
		}
		return fmt.Errorf("%s", message)
	}
	return nil
}

// Pure metadata gate. Checking it never merges code or performs a remote read.
func (s *Store) validateWorkflowPreparation(req DeliveryRequest, draft *DeliveryDraftPlan) error {
	scope, t := draft.Scope, &draft.Template
	if len(t.Preparation) == 0 {
		if req.PreparationRunID != 0 {
			return fmt.Errorf("本流程没有准备阶段，请清除不匹配的准备记录")
		}
		return nil
	}
	run := s.DeliveryRun(req.PreparationRunID)
	if scope == nil || run == nil || run.Status != "success" || run.Plan.PreparationRequest == nil || run.Plan.Scope == nil || run.Plan.Scene != req.Scene || digestJSON(run.Plan.Template) != digestJSON(preparationTemplate(t)) || run.Plan.Environment.Name != req.Environment || researchScopeDigest(run.Plan.Scope) != researchScopeDigest(scope) {
		if scope != nil {
			var version *ReleaseVersion
			if req.VersionID != 0 {
				version = s.GetReleaseVersion(req.VersionID)
			}
			nodes := append([]DeliveryNode(nil), t.Preparation...)
			if err := resolvePreparationPolicies(nodes, scope, version, draft.Defaults, req.ArtifactSources); err != nil {
				return fmt.Errorf("环境 %s 的交付准备受阻：%w", req.Environment, err)
			}
		}
		return fmt.Errorf("环境 %s：请先完成本次交付的范围确认与代码准备。进入交付准备，使用原范围完成准备后再预检发布", req.Environment)
	}
	if err := s.checkResearchScope(run); err != nil {
		return err
	}
	for _, step := range run.Steps {
		if step.Kind == "verify" {
			if step.Evidence == nil || step.Evidence.PlanDigest != run.Digest || len(step.Evidence.Artifacts) == 0 {
				return fmt.Errorf("独立验证缺少冻结制品证据")
			}
			for app, digest := range step.Evidence.Artifacts {
				a := s.GetArtifact(req.Artifacts[app])
				if a == nil || a.Service != app || a.Digest != digest {
					return fmt.Errorf("应用 %s 制品与已验证内容不一致，请重新验证", app)
				}
			}
		}
		if step.Kind != "code-prepare" && step.Kind != "code-verify" || step.Detail == "本次无需构建代码，已跳过" {
			continue
		}
		p := s.CodePreparation(step.Detail)
		if p == nil || p.State != "ready" {
			return fmt.Errorf("流程中的代码准备未完成")
		}
		proof := p.Request
		proof.CodePreparationID, proof.Artifacts = p.ID, map[string]int64{}
		for _, b := range p.Builds {
			proof.Artifacts[b.Service] = b.ArtifactID
			if req.Artifacts[b.Service] != b.ArtifactID {
				return fmt.Errorf("应用 %s 的制品与本次流程准备结果不一致", b.Service)
			}
		}
		if err := s.validatePreparedArtifacts(proof, scope); err != nil {
			return err
		}
	}
	return nil
}
