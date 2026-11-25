package deploy

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var environmentNameRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
var runtimeEnvironmentKeyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Environment 是跨服务复用的发布环境及其保护策略。
type Environment struct {
	Name          string `json:"name"`
	DisplayName   string `json:"display_name"`
	Purpose       string `json:"purpose"`
	Protected     bool   `json:"protected"`
	RequireReason bool   `json:"require_reason"`
	SortOrder     int    `json:"sort_order"`
	Approvers     string `json:"approvers,omitempty"`
}

// EnvironmentSetup 描述平台是否已完成首次环境初始化。
type EnvironmentSetup struct {
	Completed    bool          `json:"completed"`
	Environments []Environment `json:"environments"`
}

// ServiceEnvironment 保存一个服务在某环境下独立的部署参数，以及直接选择或主机组提供的发布主机。
type ServiceEnvironment struct {
	Service             string            `json:"service"`
	Environment         string            `json:"environment"`
	DisplayName         string            `json:"display_name,omitempty"`
	Protected           bool              `json:"protected"`
	RequireReason       bool              `json:"require_reason"`
	TargetType          string            `json:"target_type"`
	RemoteDir           string            `json:"remote_dir"`
	StartCmd            string            `json:"start_cmd"`
	StopCmd             string            `json:"stop_cmd"`
	HealthURL           string            `json:"health_url"`
	Strategy            string            `json:"strategy"`
	MaxParallel         int               `json:"max_parallel"`
	HostGroup           string            `json:"host_group,omitempty"`
	BatchSize           int               `json:"batch_size"`
	PauseBetweenBatches bool              `json:"pause_between_batches"`
	FailureThreshold    int               `json:"failure_threshold"`
	Hosts               []string          `json:"hosts"`
	DeployConfig        map[string]string `json:"deploy_config,omitempty"`
	// RuntimeEnv 仅接受写入，不从查询接口回传明文；RuntimeEnvKeys 用于展示已保存项。
	RuntimeEnv map[string]string `json:"runtime_env,omitempty"`
	// RuntimeEnvPatch 增量更新；null 明确删除该项，空字符串仍是有效值。
	RuntimeEnvPatch     map[string]*string `json:"runtime_env_patch,omitempty"`
	RuntimeEnvKeys      []string           `json:"runtime_env_keys,omitempty"`
	runtimeEnvEncrypted string
	Approvers           string `json:"approvers,omitempty"`
}

const TargetAliyunSAE = "aliyun-sae"

// ManagedTarget reports whether the deployment destination is a cloud resource
// rather than a list of machines owned by logplatform.
func (v ServiceEnvironment) ManagedTarget() bool { return v.TargetType == TargetAliyunSAE }

// ExecutionTargets returns the immutable target snapshot used by release
// orders. The legacy Hosts JSON field is retained for database compatibility,
// but for managed targets it contains a provider resource reference.
func (v ServiceEnvironment) ExecutionTargets() []string {
	if v.ScriptTarget() {
		return []string{"runner:" + v.DeployConfig["script_runner"]}
	}
	if !v.ManagedTarget() {
		return append([]string{}, v.Hosts...)
	}
	appID := strings.TrimSpace(v.DeployConfig["app_id"])
	if appID == "" {
		return nil
	}
	return []string{"sae:" + appID}
}

func (v ServiceEnvironment) ExecutionHosts() []Host {
	if v.ScriptTarget() {
		return []Host{{Name: v.ExecutionTargets()[0], Addr: "Runner / " + v.DeployConfig["script_runner"], Environment: v.Environment}}
	}
	if !v.ManagedTarget() {
		return nil
	}
	region := strings.TrimSpace(v.DeployConfig["region"])
	if region == "" {
		region = "managed"
	}
	refs := v.ExecutionTargets()
	if len(refs) == 0 {
		return nil
	}
	return []Host{{Name: refs[0], Addr: "SAE / " + region, Environment: v.Environment}}
}

func (s *Store) bootstrapEnvironments() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// v2 起，新安装不再擅自创建 default/dev/test/staging/prod，而由首次使用
	// 向导按团队实际流程创建。已有环境的旧库直接视为已完成，保证平滑升级。
	var setupState string
	setupErr := tx.QueryRow(`SELECT value FROM deploy_meta WHERE key='environment_setup_v2'`).Scan(&setupState)
	if setupErr != nil && setupErr != sql.ErrNoRows {
		return setupErr
	}
	if setupErr == sql.ErrNoRows {
		var envCount, legacyCount int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM environments`).Scan(&envCount); err != nil {
			return err
		}
		if err := tx.QueryRow(`SELECT
			(SELECT COUNT(*) FROM services WHERE COALESCE(remote_dir,'')<>'')+
			(SELECT COUNT(*) FROM service_environments)+
			(SELECT COUNT(*) FROM hosts)+
			(SELECT COUNT(*) FROM host_groups)`).Scan(&legacyCount); err != nil {
			return err
		}
		setupState = "pending"
		if envCount > 0 || legacyCount > 0 {
			setupState = "complete"
		}
		if legacyCount > 0 && envCount == 0 {
			if _, err := tx.Exec(`INSERT INTO environments(name,display_name,protected,require_reason,sort_order,created_at)
				VALUES('default','默认',0,0,0,?)`, time.Now().Unix()); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`INSERT INTO deploy_meta(key,value) VALUES('environment_setup_v2',?)`, setupState); err != nil {
			return err
		}
	}

	// 旧服务部署字段迁入 default，原字段继续保留以兼容旧 API。只有旧库
	// 才会存在 default；新安装完成向导后不会生成这个迁移环境。
	var migrated int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM deploy_meta WHERE key='service_environments_v1'`).Scan(&migrated); err != nil {
		return err
	}
	if migrated == 0 {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO service_environments(service,environment,target_type,remote_dir,start_cmd,stop_cmd,health_url,strategy,max_parallel,updated_at)
			SELECT name,'default',target_type,remote_dir,start_cmd,stop_cmd,health_url,'rolling',1,? FROM services
			WHERE remote_dir<>'' AND EXISTS(SELECT 1 FROM environments WHERE name='default')`, time.Now().Unix()); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO deploy_meta(key,value) VALUES('service_environments_v1','done')`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func normalizeEnvironment(env Environment) (Environment, error) {
	env.Name, env.DisplayName, env.Approvers = strings.TrimSpace(env.Name), strings.TrimSpace(env.DisplayName), strings.TrimSpace(env.Approvers)
	if !environmentNameRE.MatchString(env.Name) || env.DisplayName == "" {
		return env, fmt.Errorf("环境标识需为小写字母开头、最多 32 位，仅含字母数字、下划线或横线，且名称必填")
	}
	env.Purpose = strings.TrimSpace(env.Purpose)
	if env.Purpose == "" {
		env.Purpose = legacyEnvironmentPurpose(env.Name)
	}
	switch env.Purpose {
	case "development", "test", "staging", "production", "other":
	default:
		return env, fmt.Errorf("环境用途仅支持 development、test、staging、production 或 other")
	}
	if env.Protected && env.Approvers == "" {
		return env, fmt.Errorf("受保护环境必须配置至少一名审批人")
	}
	return env, nil
}

// Compatibility inference only for conventional legacy identifiers. Protection
// is NOT proof of production: protected test/UAT must never count as上线.
func legacyEnvironmentPurpose(name string) string {
	switch name {
	case "dev", "development":
		return "development"
	case "test", "testing", "qa", "uat":
		return "test"
	case "stage", "staging", "pre", "preprod":
		return "staging"
	case "prod", "production", "live":
		return "production"
	default:
		return "other"
	}
}

func (s *Store) EnvironmentSetup() EnvironmentSetup {
	var state string
	_ = s.db.QueryRow(`SELECT value FROM deploy_meta WHERE key='environment_setup_v2'`).Scan(&state)
	return EnvironmentSetup{Completed: state == "complete", Environments: s.Environments()}
}

// InitializeEnvironments 原子地完成首次环境初始化，避免向导中途退出留下半套环境。
func (s *Store) InitializeEnvironments(environments []Environment) error {
	if len(environments) == 0 || len(environments) > 12 {
		return fmt.Errorf("请配置 1 到 12 个环境")
	}
	normalized := make([]Environment, 0, len(environments))
	seen := map[string]bool{}
	for i, env := range environments {
		if env.SortOrder == 0 {
			env.SortOrder = (i + 1) * 10
		}
		var err error
		env, err = normalizeEnvironment(env)
		if err != nil {
			return err
		}
		if seen[env.Name] {
			return fmt.Errorf("环境标识重复: %s", env.Name)
		}
		seen[env.Name] = true
		normalized = append(normalized, env)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var state string
	if err := tx.QueryRow(`SELECT value FROM deploy_meta WHERE key='environment_setup_v2'`).Scan(&state); err != nil && err != sql.ErrNoRows {
		return err
	}
	if state == "complete" {
		return fmt.Errorf("环境已经初始化，请在环境管理中调整")
	}
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM environments`).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return fmt.Errorf("检测到已有环境，不能重复执行首次初始化")
	}
	now := time.Now().Unix()
	for _, env := range normalized {
		if _, err := tx.Exec(`INSERT INTO environments(name,display_name,purpose,protected,require_reason,sort_order,approvers,created_at) VALUES(?,?,?,?,?,?,?,?)`,
			env.Name, env.DisplayName, env.Purpose, env.Protected, env.RequireReason, env.SortOrder, env.Approvers, now); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO deploy_meta(key,value) VALUES('environment_setup_v2','complete')
		ON CONFLICT(key) DO UPDATE SET value='complete'`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Environments() []Environment {
	rows, err := s.db.Query(`SELECT name,display_name,COALESCE(purpose,''),protected,require_reason,sort_order,COALESCE(approvers,'') FROM environments ORDER BY sort_order,name`)
	if err != nil {
		return []Environment{}
	}
	defer rows.Close()
	out := []Environment{}
	for rows.Next() {
		var env Environment
		if rows.Scan(&env.Name, &env.DisplayName, &env.Purpose, &env.Protected, &env.RequireReason, &env.SortOrder, &env.Approvers) == nil {
			if env.Purpose == "" {
				env.Purpose = legacyEnvironmentPurpose(env.Name)
			}
			out = append(out, env)
		}
	}
	return out
}

func (s *Store) GetEnvironment(name string) *Environment {
	var env Environment
	if s.db.QueryRow(`SELECT name,display_name,COALESCE(purpose,''),protected,require_reason,sort_order,COALESCE(approvers,'') FROM environments WHERE name=?`, name).
		Scan(&env.Name, &env.DisplayName, &env.Purpose, &env.Protected, &env.RequireReason, &env.SortOrder, &env.Approvers) == nil {
		if env.Purpose == "" {
			env.Purpose = legacyEnvironmentPurpose(env.Name)
		}
		return &env
	}
	return nil
}

func (s *Store) UpsertEnvironment(env Environment) error {
	var err error
	if env.Purpose == "" {
		if current := s.GetEnvironment(env.Name); current != nil {
			env.Purpose = current.Purpose
		}
	}
	if env, err = normalizeEnvironment(env); err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO environments(name,display_name,purpose,protected,require_reason,sort_order,approvers,created_at) VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET display_name=excluded.display_name,purpose=excluded.purpose,protected=excluded.protected,require_reason=excluded.require_reason,sort_order=excluded.sort_order,approvers=excluded.approvers`,
		env.Name, env.DisplayName, env.Purpose, env.Protected, env.RequireReason, env.SortOrder, strings.TrimSpace(env.Approvers), time.Now().Unix())
	return err
}

func (s *Store) DeleteEnvironment(name string) error {
	if s.compositionUses("environment", strings.TrimSpace(name)) {
		return fmt.Errorf("环境仍有 Nacos 接入或应用自有资源绑定，请先解除")
	}
	if s.groupUses("environment", strings.TrimSpace(name)) {
		return fmt.Errorf("环境仍被资源组使用，请先调整资源组绑定")
	}
	name = strings.TrimSpace(name)
	if s.GetEnvironment(name) == nil {
		return fmt.Errorf("环境不存在")
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM environments`).Scan(&total); err != nil {
		return err
	}
	if total <= 1 {
		return fmt.Errorf("至少保留一个环境")
	}
	var n int
	if err := s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM service_environments WHERE environment=?)+(SELECT COUNT(*) FROM hosts WHERE environment=?)+(SELECT COUNT(*) FROM host_groups WHERE environment=?)`, name, name, name).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("环境仍被 %d 个应用配置、主机或主机组使用", n)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM deploy_meta WHERE key=?`, "delivery_policy:"+name); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM environments WHERE name=?`, name); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ServiceEnvironments(service string) []ServiceEnvironment {
	rows, err := s.db.Query(`SELECT se.service,se.environment,e.display_name,e.protected,e.require_reason,se.target_type,se.remote_dir,se.start_cmd,se.stop_cmd,se.health_url,se.strategy,se.max_parallel,COALESCE(se.host_group,''),COALESCE(se.batch_size,1),COALESCE(se.pause_between_batches,0),COALESCE(se.failure_threshold,0),COALESCE(se.deploy_config,'{}'),COALESCE(se.runtime_env_encrypted,''),COALESCE(se.runtime_env_keys,'[]'),COALESCE(e.approvers,'')
		FROM service_environments se JOIN environments e ON e.name=se.environment WHERE se.service=? ORDER BY e.sort_order,e.name`, service)
	if err != nil {
		return []ServiceEnvironment{}
	}
	defer rows.Close()
	out := []ServiceEnvironment{}
	for rows.Next() {
		var v ServiceEnvironment
		var config, keys string
		if rows.Scan(&v.Service, &v.Environment, &v.DisplayName, &v.Protected, &v.RequireReason, &v.TargetType, &v.RemoteDir, &v.StartCmd, &v.StopCmd, &v.HealthURL, &v.Strategy, &v.MaxParallel, &v.HostGroup, &v.BatchSize, &v.PauseBetweenBatches, &v.FailureThreshold, &config, &v.runtimeEnvEncrypted, &keys, &v.Approvers) == nil {
			_ = json.Unmarshal([]byte(config), &v.DeployConfig)
			_ = json.Unmarshal([]byte(keys), &v.RuntimeEnvKeys)
			out = append(out, v)
		}
	}
	rows.Close()
	for i := range out {
		if out[i].HostGroup != "" {
			out[i].Hosts = s.HostGroupHosts(out[i].HostGroup)
		} else {
			out[i].Hosts = s.ServiceEnvironmentHosts(service, out[i].Environment)
		}
	}
	return out
}

func (s *Store) GetServiceEnvironment(service, environment string) *ServiceEnvironment {
	for _, target := range s.ServiceEnvironments(service) {
		if target.Environment == environment {
			return &target
		}
	}
	return nil
}

func (s *Store) ServiceEnvironmentHosts(service, environment string) []string {
	rows, err := s.db.Query(`SELECT host FROM service_environment_hosts WHERE service=? AND environment=? ORDER BY sort_order,host`, service, environment)
	if err != nil {
		return []string{}
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var host string
		if rows.Scan(&host) == nil {
			out = append(out, host)
		}
	}
	return out
}

func (s *Store) UpsertServiceEnvironment(v ServiceEnvironment) error {
	return s.upsertServiceEnvironment(v, nil)
}

func (s *Store) upsertServiceEnvironment(v ServiceEnvironment, defaults *applicationDefaultPatch) error {
	environment := s.GetEnvironment(v.Environment)
	if s.GetService(v.Service) == nil || environment == nil {
		return fmt.Errorf("服务或环境不存在")
	}
	if v.TargetType == "" {
		v.TargetType = "ssh-jar"
	}
	if v.TargetType != "ssh-jar" && v.TargetType != TargetJavaBundle && v.TargetType != "ssh-static" && v.TargetType != "docker-host" && v.TargetType != TargetAliyunSAE && !v.ManualTarget() && !v.ScriptTarget() {
		return fmt.Errorf("部署类型仅支持 ssh-jar、ssh-java-bundle、ssh-static、docker-host、aliyun-sae、script 或 manual")
	}
	v.RemoteDir = strings.TrimSpace(v.RemoteDir)
	if v.TargetType != "docker-host" && !v.ManagedTarget() && !v.ManualTarget() && !v.ScriptTarget() && !path.IsAbs(v.RemoteDir) {
		return fmt.Errorf("远端目录必须是绝对路径")
	}
	if v.TargetType == TargetJavaBundle {
		if s.GetService(v.Service).ArtifactType != ArtifactJavaBundle {
			return fmt.Errorf("Java 整包部署需要 Java 整包制品")
		}
		if v.DeployConfig == nil {
			v.DeployConfig = map[string]string{}
		}
		v.DeployConfig["java_mode"] = "managed"
	}
	if (v.TargetType == "ssh-jar" || v.TargetType == TargetJavaBundle) && v.DeployConfig["java_mode"] == "managed" {
		if strings.TrimSpace(v.DeployConfig["health_failure_action"]) == "" {
			v.DeployConfig["health_failure_action"] = healthFailureRollback
		}
		if environment.Protected && v.DeployConfig["health_failure_action"] != healthFailureRollback {
			return fmt.Errorf("受保护环境必须在健康检查失败时自动恢复旧版本")
		}
		sv := &Service{TargetType: v.TargetType, RemoteDir: v.RemoteDir, DeployConfig: v.DeployConfig, HealthURL: v.HealthURL}
		if err := validateManagedJava(sv); err != nil {
			return err
		}
		v.StartCmd = "bash " + shellQuote(path.Join(v.RemoteDir, "current/service.sh")) + " start"
		v.StopCmd = "bash " + shellQuote(path.Join(v.RemoteDir, "current/service.sh")) + " stop"
	}
	if v.TargetType == "ssh-jar" && v.DeployConfig["java_mode"] == "jpom" {
		if environment.Protected || v.Protected || (environment.Purpose != "development" && environment.Purpose != "test") {
			return fmt.Errorf("Jpom 兼容覆盖发布仅支持非受保护的开发和测试环境")
		}
		sv := &Service{TargetType: v.TargetType, RemoteDir: v.RemoteDir, DeployConfig: v.DeployConfig}
		if err := validateJpomJava(sv); err != nil {
			return err
		}
		v.StartCmd, v.StopCmd = jpomJavaCommands(sv)
	}
	if v.TargetType == "ssh-jar" && strings.TrimSpace(v.StartCmd) == "" {
		return fmt.Errorf("JAR 部署必须配置启动命令")
	}
	if v.TargetType == "docker-host" {
		if err := validateDockerHealthURL(&Service{DeployConfig: v.DeployConfig, HealthURL: v.HealthURL}); err != nil {
			return err
		}
		if _, err := parseDockerRuntime(v.DeployConfig); err != nil {
			return err
		}
		if v.DeployConfig == nil {
			v.DeployConfig = map[string]string{}
		}
		if name := strings.TrimSpace(v.DeployConfig["container_name"]); name != "" && !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`).MatchString(name) {
			return fmt.Errorf("容器名格式不正确")
		}
		if envFile := strings.TrimSpace(v.DeployConfig["env_file"]); envFile != "" && !path.IsAbs(envFile) {
			return fmt.Errorf("Docker env-file 必须是目标主机上的绝对路径")
		}
	}
	if v.ManagedTarget() {
		if v.DeployConfig == nil {
			v.DeployConfig = map[string]string{}
		}
		connection := strings.TrimSpace(v.DeployConfig["connection"])
		appID := strings.TrimSpace(v.DeployConfig["app_id"])
		namespaceID := strings.TrimSpace(v.DeployConfig["namespace_id"])
		if connection == "" || appID == "" || namespaceID == "" {
			return fmt.Errorf("SAE 部署必须配置连接、应用 ID 和命名空间 ID")
		}
		c := s.GetConnection(connection)
		if c == nil || c.Kind != TargetAliyunSAE {
			return fmt.Errorf("SAE 连接不存在或类型不正确: %s", connection)
		}
		connectionRegion := strings.TrimSpace(c.Config["region"])
		region := strings.TrimSpace(v.DeployConfig["region"])
		if region != "" && connectionRegion != "" && region != connectionRegion {
			return fmt.Errorf("SAE 目标地域 %s 与连接地域 %s 不一致", region, connectionRegion)
		}
		if connectionRegion != "" {
			region = connectionRegion
		}
		v.DeployConfig["region"] = region
		if region == "" {
			return fmt.Errorf("SAE 部署必须配置地域")
		}
		sv := s.GetService(v.Service)
		if sv != nil && sv.ArtifactType != "jar" && sv.ArtifactType != "image" {
			return fmt.Errorf("SAE 当前仅支持 JAR 或 Docker 镜像制品")
		}
		if sv != nil && sv.ArtifactType == "jar" && strings.TrimSpace(v.DeployConfig["oss_bucket"]) == "" && strings.TrimSpace(c.Config["oss_bucket"]) == "" {
			return fmt.Errorf("SAE JAR 部署必须配置 OSS Bucket")
		}
		for _, key := range []string{"envs", "command_args", "update_strategy"} {
			if raw := strings.TrimSpace(v.DeployConfig[key]); raw != "" && !json.Valid([]byte(raw)) {
				return fmt.Errorf("SAE 参数 %s 必须是合法 JSON", key)
			}
		}
		if raw := strings.TrimSpace(v.DeployConfig["min_ready_ratio"]); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < -1 || n > 100 {
				return fmt.Errorf("SAE 最小可用实例比例必须是 -1 到 100")
			}
		}
		if raw := strings.TrimSpace(v.DeployConfig["batch_wait_seconds"]); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 0 {
				return fmt.Errorf("SAE 批次间隔必须是非负整数")
			}
		}
		if raw := strings.TrimSpace(v.DeployConfig["timeout_minutes"]); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > 180 {
				return fmt.Errorf("SAE 超时必须是 1 到 180 分钟")
			}
		}
		var boundService, boundEnvironment string
		err := s.db.QueryRow(`SELECT service,environment FROM service_environments
			WHERE target_type=? AND service||char(0)||environment<>? AND json_extract(deploy_config,'$.connection')=? AND json_extract(deploy_config,'$.app_id')=? LIMIT 1`,
			TargetAliyunSAE, v.Service+"\x00"+v.Environment, connection, appID).Scan(&boundService, &boundEnvironment)
		if err == nil {
			return fmt.Errorf("SAE 应用 %s 已绑定 %s/%s；不同环境必须使用不同 SAE 应用", appID, boundService, boundEnvironment)
		}
		v.HostGroup = ""
		v.Hosts = nil
		v.RemoteDir, v.StartCmd, v.StopCmd, v.HealthURL = "", "", "", ""
		v.BatchSize, v.MaxParallel = 1, 1
		v.PauseBetweenBatches, v.FailureThreshold = false, 0
	}
	if v.ScriptTarget() {
		if err := s.validateScriptTarget(&v); err != nil {
			return err
		}
	}
	if v.ManualTarget() {
		if err := validateManualTarget(&v, s.GetService(v.Service)); err != nil {
			return err
		}
	}
	if v.Strategy == "" {
		v.Strategy = "rolling"
	}
	if v.Strategy != "rolling" {
		return fmt.Errorf("当前仅支持 rolling 发布策略")
	}
	if v.MaxParallel < 1 {
		v.MaxParallel = 1
	}
	if v.BatchSize < 1 {
		v.BatchSize = v.MaxParallel
	}
	if v.FailureThreshold < 0 {
		return fmt.Errorf("失败阈值不能小于 0")
	}
	if v.RuntimeEnv != nil && v.RuntimeEnvPatch != nil {
		return fmt.Errorf("环境变量不能同时整体替换和增量更新")
	}
	beforePolicy, afterPolicy, err := s.prepareApplicationDefault(v.Service, v.Environment, defaults)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockArtifactLifecycle(tx); err != nil {
		return err
	}
	if err := instanceOperationBusy(tx, v.Service, v.Environment, ""); err != nil {
		return err
	}
	if err := resolveEnvironmentHosts(tx, &v); err != nil {
		return err
	}
	// Serialize even the first environment insert on MySQL. Reading before the
	// transaction loses other editors' additions, including ordinary config saves.
	if tx.dialect == "mysql" {
		var name string
		if err := tx.QueryRow(`SELECT name FROM services WHERE name=? FOR UPDATE`, v.Service).Scan(&name); err != nil {
			return err
		}
	}
	runtimeEnvEncrypted, runtimeEnvKeys := "", []string{}
	keysJSONText := "[]"
	query := `SELECT COALESCE(runtime_env_encrypted,''),COALESCE(runtime_env_keys,'[]') FROM service_environments WHERE service=? AND environment=?`
	if tx.dialect == "mysql" {
		query += " FOR UPDATE"
	}
	if err := tx.QueryRow(query, v.Service, v.Environment).Scan(&runtimeEnvEncrypted, &keysJSONText); err != nil && err != sql.ErrNoRows {
		return err
	}
	if err := json.Unmarshal([]byte(keysJSONText), &runtimeEnvKeys); err != nil {
		return fmt.Errorf("无法读取已保存的环境变量名")
	}
	values := v.RuntimeEnv
	if v.RuntimeEnvPatch != nil {
		values, err = s.decryptRuntimeEnvironment(runtimeEnvEncrypted)
		if err != nil {
			return fmt.Errorf("无法读取已保存的环境变量，未修改配置")
		}
		for key, value := range v.RuntimeEnvPatch {
			if !runtimeEnvironmentKeyRE.MatchString(key) {
				return fmt.Errorf("环境变量名格式错误")
			}
			if value == nil {
				delete(values, key)
			} else {
				values[key] = *value
			}
		}
	}
	if values != nil {
		runtimeEnvEncrypted, runtimeEnvKeys, err = s.encryptRuntimeEnvironment(values)
		if err != nil {
			return err
		}
	}
	config, _ := json.Marshal(v.DeployConfig)
	keysJSON, _ := json.Marshal(runtimeEnvKeys)
	if _, err := tx.Exec(`INSERT INTO service_environments(service,environment,target_type,remote_dir,start_cmd,stop_cmd,health_url,strategy,max_parallel,host_group,batch_size,pause_between_batches,failure_threshold,deploy_config,runtime_env_encrypted,runtime_env_keys,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(service,environment) DO UPDATE SET target_type=excluded.target_type,remote_dir=excluded.remote_dir,start_cmd=excluded.start_cmd,stop_cmd=excluded.stop_cmd,health_url=excluded.health_url,strategy=excluded.strategy,max_parallel=excluded.max_parallel,host_group=excluded.host_group,batch_size=excluded.batch_size,pause_between_batches=excluded.pause_between_batches,failure_threshold=excluded.failure_threshold,deploy_config=excluded.deploy_config,runtime_env_encrypted=excluded.runtime_env_encrypted,runtime_env_keys=excluded.runtime_env_keys,updated_at=excluded.updated_at`,
		v.Service, v.Environment, v.TargetType, v.RemoteDir, v.StartCmd, v.StopCmd, v.HealthURL, v.Strategy, v.MaxParallel, v.HostGroup, v.BatchSize, v.PauseBetweenBatches, v.FailureThreshold, string(config), runtimeEnvEncrypted, string(keysJSON), time.Now().Unix()); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM service_environment_hosts WHERE service=? AND environment=?`, v.Service, v.Environment); err != nil {
		return err
	}
	if v.HostGroup == "" && !v.ManagedTarget() && !v.ManualTarget() && !v.ScriptTarget() {
		for i, host := range v.Hosts {
			if _, err := tx.Exec(`INSERT INTO service_environment_hosts(service,environment,host,sort_order) VALUES(?,?,?,?)`, v.Service, v.Environment, host, i); err != nil {
				return err
			}
		}
	}
	if err := writeApplicationDefault(tx, v.Environment, beforePolicy, afterPolicy); err != nil {
		return err
	}
	return tx.Commit()
}

// ProtectedEnvironmentForHost 返回主机所属的首个受保护服务环境。
// 用于阻止旧“按主机直发”接口绕过生产环境确认策略。
func (s *Store) ProtectedEnvironmentForHost(service, host string) *Environment {
	var env Environment
	if s.db.QueryRow(`SELECT DISTINCT e.name,e.display_name,e.protected,e.require_reason,e.sort_order
		FROM service_environments se
		JOIN environments e ON e.name=se.environment
		LEFT JOIN host_group_hosts hgh ON hgh.group_name=se.host_group
		LEFT JOIN service_environment_hosts seh ON seh.service=se.service AND seh.environment=se.environment
		WHERE se.service=? AND (hgh.host=? OR seh.host=?) AND e.protected=1
		ORDER BY e.sort_order,e.name LIMIT 1`, service, host, host).
		Scan(&env.Name, &env.DisplayName, &env.Protected, &env.RequireReason, &env.SortOrder) == nil {
		return &env
	}
	return nil
}

func (s *Store) DeleteServiceEnvironment(service, environment string) error {
	if release := s.ActiveReleaseForServiceEnvironment(service, environment); release != nil {
		return fmt.Errorf("该应用环境存在进行中的发布单 %s，不能删除", release.DisplayLabel())
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockArtifactLifecycle(tx); err != nil {
		return err
	}
	if err := instanceOperationBusy(tx, service, environment, ""); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM service_environment_hosts WHERE service=? AND environment=?`, service, environment); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM service_environments WHERE service=? AND environment=?`, service, environment); err != nil && err != sql.ErrNoRows {
		return err
	}
	return tx.Commit()
}

func (v ServiceEnvironment) Apply(sv *Service) *Service {
	copy := *sv
	copy.TargetType, copy.RemoteDir = v.TargetType, v.RemoteDir
	copy.StartCmd, copy.StopCmd, copy.HealthURL = v.StartCmd, v.StopCmd, v.HealthURL
	copy.DeployConfig = v.DeployConfig
	copy.RuntimeEnv = v.RuntimeEnv
	copy.RuntimeEnvironment = v.Environment
	return &copy
}

func (s *Store) encryptRuntimeEnvironment(values map[string]string) (string, []string, error) {
	keys := make([]string, 0, len(values))
	for key, value := range values {
		if !runtimeEnvironmentKeyRE.MatchString(key) {
			return "", nil, fmt.Errorf("环境变量名格式错误: %s", key)
		}
		if strings.ContainsAny(value, "\x00\r\n") {
			return "", nil, fmt.Errorf("环境变量 %s 的值不能包含换行或 NUL", key)
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	plain, err := json.Marshal(values)
	if err != nil {
		return "", nil, err
	}
	encrypted, err := s.encryptSecret(string(plain))
	return encrypted, keys, err
}

func (s *Store) decryptRuntimeEnvironment(encrypted string) (map[string]string, error) {
	if strings.TrimSpace(encrypted) == "" {
		return map[string]string{}, nil
	}
	plain, err := s.decryptSecret(encrypted)
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	if err := json.Unmarshal([]byte(plain), &values); err != nil {
		return nil, err
	}
	return values, nil
}
