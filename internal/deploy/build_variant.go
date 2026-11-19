package deploy

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// BuildVariant is an application/environment contract, not a runtime profile.
// Empty means unconfigured. A shared name permits reuse only with identical
// build configuration. Strings keep the versioned application default comparable.
type BuildVariant struct {
	Name                    string `json:"name,omitempty"`
	MavenProfiles           string `json:"maven_profiles,omitempty"`
	MavenProperties         string `json:"maven_properties,omitempty"`
	GradleTasks             string `json:"gradle_tasks,omitempty"`
	GradleProjectProperties string `json:"gradle_project_properties,omitempty"`
	GradleSystemProperties  string `json:"gradle_system_properties,omitempty"`
}

// Expose only contract names here; build properties may contain private values.
type buildEnvironmentOption struct {
	Environment  string `json:"environment"`
	DisplayName  string `json:"display_name"`
	BuildVariant string `json:"build_variant"`
}

func (s *Store) buildEnvironmentOptions(service string) ([]buildEnvironmentOption, bool, error) {
	if s.GetService(service) == nil {
		return nil, false, fmt.Errorf("应用不存在")
	}
	options := []buildEnvironmentOption{}
	required := false
	for _, env := range s.Environments() {
		p, err := s.deliveryPolicy(env.Name)
		if err != nil {
			return nil, false, err
		}
		variant := p.Defaults.Applications[service].BuildVariant.Name
		required = required || variant != ""
		options = append(options, buildEnvironmentOption{env.Name, env.DisplayName, variant})
	}
	return options, required, nil
}

func mergeBuildProperties(base, override string) string {
	values := map[string]string{}
	for _, raw := range []string{base, override} {
		for _, line := range strings.Split(raw, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			key, value, _ := strings.Cut(line, "=")
			key = strings.TrimSpace(key)
			values[key] = key + "=" + strings.TrimSpace(value)
		}
	}
	keys := []string{}
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := []string{}
	for _, k := range keys {
		lines = append(lines, values[k])
	}
	return strings.Join(lines, "\n")
}

func applyBuildVariant(sv *Service, v BuildVariant) (*Service, error) {
	if v.Name == "" {
		if v != (BuildVariant{}) {
			return nil, fmt.Errorf("请填写构建方案标识")
		}
		copy := *sv
		return &copy, nil
	}
	if !environmentNameRE.MatchString(v.Name) {
		return nil, fmt.Errorf("构建方案标识须为小写字母开头的 1–32 位字母、数字、下划线或短横线")
	}
	copy := *sv
	if sv.BuildTool != "maven" && sv.BuildTool != "gradle" {
		return nil, fmt.Errorf("环境构建方案要求使用结构化 Maven 或 Gradle 构建，不能从自定义脚本推断环境")
	}
	if sv.BuildTool == "maven" {
		if v.GradleTasks != "" || v.GradleProjectProperties != "" || v.GradleSystemProperties != "" {
			return nil, fmt.Errorf("Maven 应用不能配置 Gradle 参数")
		}
		if _, err := parseMavenProperties(v.MavenProperties); err != nil {
			return nil, err
		}
		copy.MavenProfiles = v.MavenProfiles
		copy.MavenProperties = mergeBuildProperties(sv.MavenProperties, v.MavenProperties)
	} else {
		if v.MavenProfiles != "" || v.MavenProperties != "" {
			return nil, fmt.Errorf("Gradle 应用不能配置 Maven 参数")
		}
		if _, err := parseGradleProperties(v.GradleProjectProperties, "-P 项目属性"); err != nil {
			return nil, err
		}
		if _, err := parseGradleProperties(v.GradleSystemProperties, "-D 系统属性"); err != nil {
			return nil, err
		}
		if v.GradleTasks != "" {
			copy.GradleTasks = v.GradleTasks
		}
		copy.GradleProjectProperties = mergeBuildProperties(sv.GradleProjectProperties, v.GradleProjectProperties)
		copy.GradleSystemProperties = mergeBuildProperties(sv.GradleSystemProperties, v.GradleSystemProperties)
	}
	copy.BuildVariant = v.Name
	return serviceForPipelineRun(&copy, PipelineRunOptions{})
}

func (s *Store) environmentBuildService(sv *Service, environment string) (*Service, error) {
	if sv == nil {
		return nil, fmt.Errorf("应用不存在")
	}
	if s.GetEnvironment(environment) == nil {
		return nil, fmt.Errorf("构建目标环境不存在")
	}
	p, err := s.deliveryPolicy(environment)
	if err != nil {
		return nil, err
	}
	if p.Defaults.Applications[sv.Name].BuildVariant.Name == "" {
		for _, env := range s.Environments() {
			other, err := s.deliveryPolicy(env.Name)
			if err != nil {
				return nil, err
			}
			if other.Defaults.Applications[sv.Name].BuildVariant.Name != "" {
				return nil, fmt.Errorf("应用 %s 的环境 %s 尚未绑定构建方案，请先配置后再构建或发布", sv.Name, environment)
			}
		}
	}
	return applyBuildVariant(sv, p.Defaults.Applications[sv.Name].BuildVariant)
}

func (s *Store) preparationEnvironmentConfiguration(sv *Service, environment string) (string, error) {
	if sv == nil {
		return "", fmt.Errorf("应用不存在")
	}
	resolved, err := s.environmentBuildService(sv, environment)
	if err != nil {
		return "", err
	}
	if resolved.BuildVariant == "" {
		return preparationConfiguration(sv, s.GetServiceDelivery(sv.Name)), nil
	}
	return preparationConfiguration(resolved, s.GetServiceDelivery(sv.Name)), nil
}

// Exclude source revisions and deployment settings; include commands, input
// paths, Dockerfile content and toolchain configuration. Never expose this body.
func buildVariantDigest(sv *Service, d *ServiceDelivery) string {
	copy := *sv
	copy.Branch, copy.BuildVariant = "", ""
	copy.APIDocumentation = nil
	copy.DocumentEnvironment = ""
	copy.Tags = nil // Business labels do not change compilation or package inputs.
	copy.RemoteDir, copy.StartCmd, copy.StopCmd, copy.HealthURL, copy.TargetType = "", "", "", "", ""
	copy.DeployConfig = nil
	if len(copy.Steps) == 0 {
		copy.Steps = nil
	}
	raw, _ := json.Marshal(copy.Steps)
	copy.Steps = nil // Decode into fresh slice/maps; never reuse the caller's backing array.
	json.Unmarshal(raw, &copy.Steps)
	for i := range copy.Steps {
		if copy.Steps[i].Step == "git-checkout" {
			delete(copy.Steps[i].Params, "BRANCH")
		}
		if copy.Steps[i].Step == "docker-build" {
			copy.Steps[i].Params = resolvedDockerfileParams(copy.Steps[i].Params)
		}
	}
	return preparationConfiguration(&copy, d)
}

func (s *Store) artifactBuildSnapshot(a *Artifact) (*buildSnapshot, error) {
	if a == nil || a.Source != "pipeline-run" || a.SourceID <= 0 {
		return nil, fmt.Errorf("制品缺少可验证的原始构建快照，请按目标环境重新构建")
	}
	var sealed string
	if err := s.db.QueryRow(`SELECT config_cipher FROM build_run_snapshots WHERE run_id=?`, a.SourceID).Scan(&sealed); err != nil {
		return nil, fmt.Errorf("制品缺少原始构建快照，请重新构建")
	}
	plain, err := s.decryptSecret(sealed)
	if err != nil {
		return nil, fmt.Errorf("制品构建快照不可验证")
	}
	var snapshot buildSnapshot
	if json.Unmarshal([]byte(plain), &snapshot) != nil || snapshot.Service.Name != a.Service {
		return nil, fmt.Errorf("制品构建来源不匹配")
	}
	run := s.GetRun(a.SourceID)
	if run == nil || run.Service != a.Service || !(run.ArtifactID == a.ID && run.ArtifactDigest == a.Digest || s.pairedCompilation(a, run.ArtifactID)) {
		return nil, fmt.Errorf("制品与原始构建身份或摘要不匹配")
	}
	return &snapshot, nil
}

func hasUnscopedGradleBuildProperties(snapshot *buildSnapshot) bool {
	for _, raw := range []string{snapshot.Service.GradleProjectProperties, snapshot.Service.GradleSystemProperties} {
		properties, err := parseGradleProperties(raw, "构建属性")
		if err != nil {
			return true
		}
		for _, property := range properties {
			// These exact references are supplied by the application-wide build
			// repository binding, independently of the deployment environment.
			// Do not exempt literals, arbitrary variables or repository URLs.
			if snapshot.Delivery.MavenConnection != "" && (property.envName == "GRADLE_REPOSITORY_USERNAME" || property.envName == "GRADLE_REPOSITORY_PASSWORD") {
				continue
			}
			return true
		}
	}
	return false
}

// Shared by ordinary release, work-item/version/Hotfix preflight and execution.
// Public metadata is display-only; it can never authorize a deployment.
func (s *Store) validateArtifactBuildVariant(a *Artifact, environment string) error {
	if a == nil {
		return fmt.Errorf("制品不存在")
	}
	if environment == "" {
		for _, env := range s.Environments() {
			p, err := s.deliveryPolicy(env.Name)
			if err != nil {
				return err
			}
			if p.Defaults.Applications[a.Service].BuildVariant.Name != "" {
				return fmt.Errorf("应用配置了环境构建方案，必须选择明确目标环境，不能按裸主机绕过校验")
			}
		}
		if snapshot, err := s.artifactBuildSnapshot(a); err == nil && snapshot.Service.BuildVariant != "" {
			return fmt.Errorf("环境专用制品必须选择明确目标环境")
		}
		return nil
	}
	sv := s.GetService(a.Service)
	if sv == nil {
		return fmt.Errorf("应用不存在")
	}
	expected, err := s.environmentBuildService(sv, environment)
	if err != nil {
		return err
	}
	snapshot, proofErr := s.artifactBuildSnapshot(a)
	if expected.BuildVariant == "" {
		// Once this app declares environment-dependent builds, every destination
		// must explicitly declare its contract (including a shared/common variant).
		for _, env := range s.Environments() {
			p, e := s.deliveryPolicy(env.Name)
			if e != nil {
				return e
			}
			if p.Defaults.Applications[a.Service].BuildVariant.Name != "" {
				return fmt.Errorf("应用 %s 的目标环境 %s 未配置构建方案，禁止复用环境专用制品", a.Service, environment)
			}
		}
		if proofErr == nil && (snapshot.Service.BuildVariant != "" || snapshot.Service.MavenProfiles != "" || hasUnscopedGradleBuildProperties(snapshot)) {
			return fmt.Errorf("制品含构建 Profile／属性，请先为应用目标环境配置构建方案")
		}
		return nil // Existing environment-independent applications remain usable.
	}
	if proofErr != nil {
		return proofErr
	}
	if snapshot.Service.BuildVariant != expected.BuildVariant || buildVariantDigest(&snapshot.Service, &snapshot.Delivery) != buildVariantDigest(expected, s.GetServiceDelivery(a.Service)) {
		return fmt.Errorf("制品构建方案或参数与目标环境 %s 不兼容（要求 %s，制品 %s）；请按目标环境重新构建", environment, expected.BuildVariant, snapshot.Service.BuildVariant)
	}
	run := s.GetRun(a.SourceID)
	if run.Status != "success" && run.Status != "waiting_deploy" {
		var ready string
		if run.Status != "running" || s.db.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, fmt.Sprintf("build-ready:%d", run.ID)).Scan(&ready) != nil || ready != fmt.Sprintf("%d:%s", a.ID, a.Digest) {
			return fmt.Errorf("制品来源构建尚未成功完成，不能发布")
		}
	}
	return nil
}
