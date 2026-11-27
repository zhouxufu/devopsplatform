package deploy

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type ResourceConnection struct {
	Revision         string            `json:"revision,omitempty"`
	LastCheck        *ResourceCheck    `json:"last_check,omitempty"`
	Name             string            `json:"name"`
	Kind             string            `json:"kind"` // git | maven | docker | aliyun-sae | mysql | zentao
	Endpoint         string            `json:"endpoint"`
	Config           map[string]string `json:"config,omitempty"`
	Username         string            `json:"username,omitempty"`
	Password         string            `json:"password,omitempty"`
	PrivateKey       string            `json:"private_key,omitempty"`
	Passphrase       string            `json:"passphrase,omitempty"`
	SecretConfigured bool              `json:"secret_configured"`
	UpdatedAt        int64             `json:"updated_at"`
}

type HostGroup struct {
	Name        string   `json:"name"`
	Environment string   `json:"environment"`
	Description string   `json:"description"`
	Hosts       []string `json:"hosts"`
	UpdatedAt   int64    `json:"updated_at"`
}

type VariableGroup struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Values      map[string]string `json:"values,omitempty"`
	Keys        []string          `json:"keys"`
	UpdatedAt   int64             `json:"updated_at"`
}

type BuildRunner struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Labels      string `json:"labels"`
	Enabled     bool   `json:"enabled"`
	MaxParallel int    `json:"max_parallel"`
	UpdatedAt   int64  `json:"updated_at"`
}

type ServiceDelivery struct {
	BuildEnvironment      *BuildEnvironment `json:"build_environment,omitempty"`
	Service               string            `json:"service"`
	Runner                string            `json:"runner"`
	GitConnection         string            `json:"git_connection"`
	MavenConnection       string            `json:"maven_connection"`
	RegistryConnection    string            `json:"registry_connection"`
	VariableGroups        []string          `json:"variable_groups"`
	WebhookToken          string            `json:"webhook_token"`
	ScheduleMinutes       int               `json:"schedule_minutes"`
	AutoDeployEnvironment string            `json:"auto_deploy_environment"`
	LastScheduleAt        int64             `json:"last_schedule_at"`
	UpdatedAt             int64             `json:"updated_at"`
}

type AuditEvent struct {
	ID           int64          `json:"id"`
	Actor        string         `json:"actor"`
	Action       string         `json:"action"`
	ResourceType string         `json:"resource_type"`
	ResourceID   string         `json:"resource_id"`
	Detail       map[string]any `json:"detail,omitempty"`
	CreatedAt    int64          `json:"created_at"`
}

func (s *Store) bootstrapDeliveryResources() error {
	now := time.Now().Unix()
	_, err := s.db.Exec(`INSERT OR IGNORE INTO build_runners(name,kind,labels,enabled,max_parallel,created_at,updated_at)
		VALUES('local','local','darwin,local',1,1,?,?)`, now, now)
	return err
}

// migrateResourceEnvironmentScopes 给升级前的主机和主机组补环境归属。
// 只有能从单一应用环境明确推断时才迁移，否则保留在 default 等待人工调整。
func (s *Store) migrateResourceEnvironmentScopes() error {
	for _, q := range []string{
		`UPDATE hosts SET environment=(SELECT MIN(seh.environment) FROM service_environment_hosts seh WHERE seh.host=hosts.name)
		 WHERE COALESCE(environment,'default')='default' AND (SELECT COUNT(DISTINCT seh.environment) FROM service_environment_hosts seh WHERE seh.host=hosts.name)=1`,
		`UPDATE host_groups SET environment=(SELECT MIN(se.environment) FROM service_environments se WHERE se.host_group=host_groups.name)
		 WHERE COALESCE(environment,'default')='default' AND (SELECT COUNT(DISTINCT se.environment) FROM service_environments se WHERE se.host_group=host_groups.name)=1`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

func validResourceName(name string) bool {
	return environmentNameRE.MatchString(strings.TrimSpace(name))
}

var hostNameRE = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N}_.-]{0,63}$`)

// Connection names are user-facing resource names just like host names. Keep
// path separators and whitespace out, but allow operators to use meaningful
// Chinese names instead of forcing an implementation-shaped ASCII id.
func validConnectionName(name string) bool {
	return hostNameRE.MatchString(strings.TrimSpace(name))
}

func normalizeConnectionEndpoint(kind, endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" || strings.Contains(endpoint, "://") || strings.HasPrefix(endpoint, "git@") {
		return endpoint
	}
	// Resource endpoints are HTTP(S) service roots. HTTPS is the safe and most
	// common default; explicitly configured http:// addresses remain unchanged.
	if kind == "git" || kind == "maven" || kind == "docker" || kind == "zentao" || kind == "nacos" || kind == xxlJobKind {
		return "https://" + endpoint
	}
	return endpoint
}

// 主机名称直接展示给运维人员，因此允许中文等 Unicode 字母。空格和路径
// 分隔符仍不允许，因为名称同时会出现在 API 路径和日志 Agent 标识中。
func validHostName(name string) bool {
	return hostNameRE.MatchString(strings.TrimSpace(name))
}

func (s *Store) UpsertConnection(c ResourceConnection) error {
	s.resourceConnectionMu.Lock()
	defer s.resourceConnectionMu.Unlock()
	return s.upsertConnection(c)
}
func (s *Store) upsertConnection(c ResourceConnection) error {
	c.Name, c.Kind = strings.TrimSpace(c.Name), strings.TrimSpace(c.Kind)
	current := s.GetConnection(c.Name)
	if !validConnectionName(c.Name) {
		return fmt.Errorf("连接名称支持中文、字母、数字、点、短横线和下划线，长度 1-64")
	}
	if c.Kind != "git" && c.Kind != "maven" && c.Kind != "docker" && c.Kind != TargetAliyunSAE && c.Kind != "mysql" && c.Kind != "zentao" && c.Kind != "nacos" && c.Kind != "redis" && c.Kind != xxlJobKind {
		return fmt.Errorf("连接类型仅支持 git、maven、docker、aliyun-sae、mysql、zentao、nacos、redis、xxl_job")
	}
	if current != nil && current.Kind != c.Kind {
		return fmt.Errorf("已有连接不能更换类型，请新建连接")
	}
	if c.Config == nil {
		c.Config = map[string]string{}
	}
	if c.Kind == "mysql" {
		if strings.TrimSpace(c.Config["database"]) != "" {
			return fmt.Errorf("MySQL 连接只配置实例和凭据，请在应用或资源组绑定中选择数据库")
		}
		config := make(map[string]string, len(c.Config))
		for key, value := range c.Config {
			if key != "database" {
				config[key] = value
			}
		}
		c.Config = config
	}
	if c.Kind == "git" {
		mode, err := normalizeGitAuthMode(c.Config["auth_mode"])
		if err != nil {
			return err
		}
		c.Config["auth_mode"] = mode
		if mode == gitAuthSSH {
			c.Endpoint, err = normalizeGitSSHEndpoint(c.Endpoint)
			if err != nil {
				return err
			}
			if c.Username == "" {
				c.Username = "git"
			}
		} else {
			c.Endpoint = normalizeConnectionEndpoint(c.Kind, c.Endpoint)
		}
	} else {
		c.Endpoint = normalizeConnectionEndpoint(c.Kind, c.Endpoint)
	}
	if c.Kind != TargetAliyunSAE && c.Endpoint == "" {
		return fmt.Errorf("服务地址不能为空")
	}
	if err := validateIntegrationConnection(&c); err != nil {
		return err
	}
	if c.Kind == xxlJobKind {
		if s.GetEnvironment(c.Config["environment"]) == nil {
			return fmt.Errorf("XXL-JOB 连接的环境不存在")
		}
		if current != nil && current.Config["environment"] != c.Config["environment"] {
			return fmt.Errorf("已有 XXL-JOB 连接的环境不能更换，请新建连接")
		}
	}
	if current != nil && (c.Kind == "zentao" || c.Kind == "mysql") && (current.Endpoint != c.Endpoint || current.Config["database"] != c.Config["database"]) {
		var refs int
		query := `SELECT COUNT(*) FROM external_work_items WHERE connection=?`
		if c.Kind == "mysql" {
			query = `SELECT COUNT(*) FROM sql_execution_ledger WHERE connection=?`
		}
		if err := s.db.QueryRow(query, c.Name).Scan(&refs); err != nil {
			return err
		}
		if refs > 0 {
			return fmt.Errorf("连接已有外部同步或 SQL 执行记录，不能更换目标，请新建连接；账号密码可以轮换")
		}
	}
	if c.Kind == TargetAliyunSAE {
		if c.Config == nil || strings.TrimSpace(c.Config["region"]) == "" {
			return fmt.Errorf("SAE 连接必须配置地域")
		}
		if c.Username == "" && c.Password == "" && (current == nil || !current.SecretConfigured) {
			return fmt.Errorf("SAE 连接必须配置 AccessKey ID 和 AccessKey Secret")
		}
	}
	securityToken := ""
	if c.Config != nil {
		securityToken = c.Config["security_token"]
		delete(c.Config, "security_token")
	}
	currentMode := ""
	if current != nil && current.Kind == "git" {
		currentMode, _ = normalizeGitAuthMode(current.Config["auth_mode"])
	}
	modeUnchanged := c.Kind != "git" || currentMode == c.Config["auth_mode"]
	if current != nil && c.Kind == "redis" && current.Config["redis_auth"] != c.Config["redis_auth"] {
		modeUnchanged = false
		if c.Config["redis_auth"] == "password" && c.Password == "" {
			return fmt.Errorf("更换 Redis 认证方式时必须重填密码")
		}
	}
	if current != nil && c.Kind == "nacos" && current.Config["nacos_auth"] != c.Config["nacos_auth"] {
		modeUnchanged = false
		if c.Config["nacos_auth"] != "none" && c.Password == "" {
			return fmt.Errorf("更换 Nacos 认证方式时必须重新填写密码或 Token，不能复用旧凭据")
		}
	}
	if current != nil && modeUnchanged {
		if c.Username == "" {
			c.Username = current.Username
		}
		if c.Password == "" {
			c.Password = current.Password
		}
		if c.PrivateKey == "" {
			c.PrivateKey = current.PrivateKey
		}
		if c.Passphrase == "" {
			c.Passphrase = current.Passphrase
		}
		if securityToken == "" {
			securityToken = current.Config["security_token"]
		}
	}
	if c.Kind == "git" {
		if c.Config["auth_mode"] == gitAuthSSH {
			c.Password = ""
			if strings.TrimSpace(c.PrivateKey) == "" {
				return fmt.Errorf("SSH 私钥不能为空")
			}
			if strings.TrimSpace(c.Config["known_hosts"]) == "" || strings.TrimSpace(c.Config["ssh_fingerprint"]) == "" {
				return fmt.Errorf("请先获取并确认 Git 服务器 SSH 主机指纹")
			}
			if _, err := parseGitPrivateKey(c.PrivateKey, c.Passphrase); err != nil {
				return err
			}
		} else {
			c.PrivateKey, c.Passphrase = "", ""
		}
	}
	if c.Kind == "nacos" {
		if c.Config["nacos_auth"] == "none" {
			c.Username, c.Password = "", ""
		}
		if c.Config["nacos_auth"] == "password" && (c.Username == "" || c.Password == "") {
			return fmt.Errorf("请填写 Nacos 专用账号和密码")
		}
		if c.Config["nacos_auth"] == "token" && c.Password == "" {
			return fmt.Errorf("请填写 Nacos Token")
		}
	}
	if c.Kind == "redis" {
		if c.Config["redis_auth"] == "none" {
			c.Username, c.Password = "", ""
		}
		if c.Config["redis_auth"] == "password" && c.Password == "" {
			return fmt.Errorf("请填写 Redis 密码")
		}
	}
	if c.Kind == xxlJobKind {
		c.Username, c.PrivateKey, c.Passphrase = "", "", ""
		if len(c.Password) < 32 || strings.ContainsAny(c.Password, " \r\n\t") {
			return fmt.Errorf("请填写至少 32 个字符的 XXL-JOB DevOps 专用接入令牌")
		}
	}
	config, _ := json.Marshal(c.Config)
	secret := ""
	if c.Username != "" || c.Password != "" || c.PrivateKey != "" || c.Passphrase != "" || securityToken != "" {
		plain, _ := json.Marshal(map[string]string{
			"username": c.Username, "password": c.Password, "private_key": c.PrivateKey,
			"passphrase": c.Passphrase, "security_token": securityToken,
		})
		var err error
		secret, err = s.encryptSecret(string(plain))
		if err != nil {
			return err
		}
	}
	now := time.Now().Unix()
	_, err := s.db.Exec(`INSERT INTO resource_connections(name,kind,endpoint,config,secret,secret_configured,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(name) DO UPDATE SET kind=excluded.kind,endpoint=excluded.endpoint,config=excluded.config,
		secret=excluded.secret,secret_configured=excluded.secret_configured,updated_at=excluded.updated_at`,
		c.Name, c.Kind, c.Endpoint, string(config), secret, secret != "", now, now)
	return err
}

func (s *Store) Connections() []ResourceConnection {
	rows, err := s.db.Query(`SELECT name,kind,endpoint,config,secret_configured,updated_at FROM resource_connections ORDER BY kind,name`)
	if err != nil {
		return []ResourceConnection{}
	}
	defer rows.Close()
	out := []ResourceConnection{}
	for rows.Next() {
		var c ResourceConnection
		var config string
		if rows.Scan(&c.Name, &c.Kind, &c.Endpoint, &config, &c.SecretConfigured, &c.UpdatedAt) == nil {
			_ = json.Unmarshal([]byte(config), &c.Config)
			out = append(out, c)
		}
	}
	rows.Close()
	for i := range out {
		private := s.GetConnection(out[i].Name)
		out[i].Revision = s.connectionCheckFingerprint(private)
		out[i].LastCheck = s.readResourceCheck("resource_connection_check:"+out[i].Name, s.connectionCheckFingerprint(private))
	}

	return out
}

func (s *Store) GetConnection(name string) *ResourceConnection {
	var c ResourceConnection
	var config, secret string
	if s.db.QueryRow(`SELECT name,kind,endpoint,config,secret,secret_configured,updated_at FROM resource_connections WHERE name=?`, name).
		Scan(&c.Name, &c.Kind, &c.Endpoint, &config, &secret, &c.SecretConfigured, &c.UpdatedAt) != nil {
		return nil
	}
	_ = json.Unmarshal([]byte(config), &c.Config)
	if plain, err := s.decryptSecret(secret); err == nil && plain != "" {
		var values map[string]string
		_ = json.Unmarshal([]byte(plain), &values)
		c.Username, c.Password = values["username"], values["password"]
		c.PrivateKey, c.Passphrase = values["private_key"], values["passphrase"]
		if values["security_token"] != "" {
			if c.Config == nil {
				c.Config = map[string]string{}
			}
			c.Config["security_token"] = values["security_token"]
		}
	}
	return &c
}

func (s *Store) DeleteConnection(name string) error {
	s.resourceConnectionMu.Lock()
	defer s.resourceConnectionMu.Unlock()
	if s.compositionUses("connection", name) {
		return fmt.Errorf("连接仍被环境 Nacos 或应用自有资源使用")
	}
	if s.groupUses("connection", name) {
		return fmt.Errorf("连接仍被资源组使用，请先调整资源组绑定")
	}
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM service_delivery WHERE git_connection=? OR maven_connection=? OR registry_connection=?`, name, name, name).Scan(&n)
	var envRefs int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM service_environments WHERE json_extract(deploy_config,'$.connection')=?`, name).Scan(&envRefs)
	n += envRefs
	var resourceRefs int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM application_environment_resources WHERE connection=?`, name).Scan(&resourceRefs)
	n += resourceRefs
	var externalRefs, executionRefs int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM external_work_items WHERE connection=?`, name).Scan(&externalRefs); err != nil {
		return err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sql_execution_ledger WHERE connection=?`, name).Scan(&executionRefs); err != nil {
		return err
	}
	if externalRefs+executionRefs > 0 {
		return fmt.Errorf("连接已有外部同步或 SQL 执行记录，不能删除，以保留身份关联和防重台账")
	}
	if n > 0 {
		return fmt.Errorf("连接仍被 %d 个应用使用", n)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM issue_sync_state WHERE connection=?`, name); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM resource_connections WHERE name=?`, name); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM deploy_meta WHERE key=?`, "mysql_direct_query_scope:"+name); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UpsertHostGroup(g HostGroup) error {
	g.Name, g.Environment = strings.TrimSpace(g.Name), strings.TrimSpace(g.Environment)
	if !validResourceName(g.Name) || s.GetEnvironment(g.Environment) == nil {
		return fmt.Errorf("主机组标识格式错误或环境不存在")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockArtifactLifecycle(tx); err != nil {
		return err
	}
	var currentEnvironment string
	err = tx.QueryRow(`SELECT environment FROM host_groups WHERE name=?`, g.Name).Scan(&currentEnvironment)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	var refs int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM service_environments WHERE host_group=?`, g.Name).Scan(&refs); err != nil {
		return err
	}
	if refs > 0 && currentEnvironment != g.Environment {
		return fmt.Errorf("主机组已被应用环境使用，不能直接变更所属环境")
	}
	if refs > 0 && len(g.Hosts) == 0 {
		return fmt.Errorf("主机组已被应用环境使用，不能清空")
	}
	seen := map[string]bool{}
	for _, name := range g.Hosts {
		if seen[name] {
			return fmt.Errorf("主机重复: %s", name)
		}
		seen[name] = true
		var environment string
		if err := tx.QueryRow(`SELECT environment FROM hosts WHERE name=?`, name).Scan(&environment); err != nil {
			return fmt.Errorf("主机不存在: %s", name)
		}
		if environment != g.Environment {
			return fmt.Errorf("主机 %s 属于 %s 环境，不能加入 %s 环境的主机组", name, environment, g.Environment)
		}
	}
	now := time.Now().Unix()
	if _, err = tx.Exec(`INSERT INTO host_groups(name,environment,description,created_at,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(name) DO UPDATE SET environment=excluded.environment,description=excluded.description,updated_at=excluded.updated_at`, g.Name, g.Environment, g.Description, now, now); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM host_group_hosts WHERE group_name=?`, g.Name); err != nil {
		return err
	}
	for i, h := range g.Hosts {
		if _, err = tx.Exec(`INSERT INTO host_group_hosts(group_name,host,sort_order) VALUES(?,?,?)`, g.Name, h, i); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) HostGroups() []HostGroup {
	rows, err := s.db.Query(`SELECT name,COALESCE(environment,'default'),description,updated_at FROM host_groups ORDER BY environment,name`)
	if err != nil {
		return []HostGroup{}
	}
	out := []HostGroup{}
	for rows.Next() {
		var g HostGroup
		if rows.Scan(&g.Name, &g.Environment, &g.Description, &g.UpdatedAt) == nil {
			out = append(out, g)
		}
	}
	rows.Close()
	for i := range out {
		out[i].Hosts = s.HostGroupHosts(out[i].Name)
	}
	return out
}
func (s *Store) GetHostGroup(name string) *HostGroup {
	for _, group := range s.HostGroups() {
		if group.Name == name {
			return &group
		}
	}
	return nil
}
func (s *Store) HostGroupHosts(name string) []string {
	rows, err := s.db.Query(`SELECT host FROM host_group_hosts WHERE group_name=? ORDER BY sort_order,host`, name)
	if err != nil {
		return []string{}
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var h string
		if rows.Scan(&h) == nil {
			out = append(out, h)
		}
	}
	return out
}
func (s *Store) DeleteHostGroup(name string) error {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM service_environments WHERE host_group=?`, name).Scan(&n)
	if n > 0 {
		return fmt.Errorf("主机组仍被 %d 个应用环境使用", n)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`DELETE FROM host_group_hosts WHERE group_name=?`, name)
	if err == nil {
		_, err = tx.Exec(`DELETE FROM host_groups WHERE name=?`, name)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UpsertVariableGroup(g VariableGroup) error {
	g.Name = strings.TrimSpace(g.Name)
	if !validResourceName(g.Name) {
		return fmt.Errorf("变量组标识格式错误")
	}
	if len(g.Values) == 0 {
		var existing string
		if s.db.QueryRow(`SELECT values_encrypted FROM variable_groups WHERE name=?`, g.Name).Scan(&existing) == nil {
			_, err := s.db.Exec(`UPDATE variable_groups SET description=?,updated_at=? WHERE name=?`, g.Description, time.Now().Unix(), g.Name)
			return err
		}
	}
	keys := make([]string, 0, len(g.Values))
	for k := range g.Values {
		if strings.TrimSpace(k) == "" {
			return fmt.Errorf("变量名不能为空")
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	plain, _ := json.Marshal(g.Values)
	enc, err := s.encryptSecret(string(plain))
	if err != nil {
		return err
	}
	keyJSON, _ := json.Marshal(keys)
	now := time.Now().Unix()
	_, err = s.db.Exec(`INSERT INTO variable_groups(name,description,values_encrypted,key_names,created_at,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(name) DO UPDATE SET description=excluded.description,values_encrypted=excluded.values_encrypted,key_names=excluded.key_names,updated_at=excluded.updated_at`, g.Name, g.Description, enc, string(keyJSON), now, now)
	return err
}
func (s *Store) VariableGroups() []VariableGroup {
	rows, err := s.db.Query(`SELECT name,description,key_names,updated_at FROM variable_groups ORDER BY name`)
	if err != nil {
		return []VariableGroup{}
	}
	defer rows.Close()
	out := []VariableGroup{}
	for rows.Next() {
		var g VariableGroup
		var keys string
		if rows.Scan(&g.Name, &g.Description, &keys, &g.UpdatedAt) == nil {
			_ = json.Unmarshal([]byte(keys), &g.Keys)
			out = append(out, g)
		}
	}
	return out
}
func (s *Store) variableGroupValues(name string) map[string]string {
	var enc string
	if s.db.QueryRow(`SELECT values_encrypted FROM variable_groups WHERE name=?`, name).Scan(&enc) != nil {
		return nil
	}
	plain, err := s.decryptSecret(enc)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	_ = json.Unmarshal([]byte(plain), &out)
	return out
}
func (s *Store) DeleteVariableGroup(name string) error {
	for _, d := range s.AllServiceDelivery() {
		for _, g := range d.VariableGroups {
			if g == name {
				return fmt.Errorf("变量组仍被应用 %s 使用", d.Service)
			}
		}
	}
	_, err := s.db.Exec(`DELETE FROM variable_groups WHERE name=?`, name)
	return err
}

func (s *Store) UpsertRunner(v BuildRunner) error {
	v.Name = strings.TrimSpace(v.Name)
	if !validResourceName(v.Name) {
		return fmt.Errorf("Runner 标识格式错误")
	}
	if v.Kind == "" {
		v.Kind = "local"
	}
	if v.Kind != "local" {
		return fmt.Errorf("当前节点只支持 local Runner")
	}
	if v.MaxParallel < 1 {
		v.MaxParallel = 1
	}
	now := time.Now().Unix()
	_, err := s.db.Exec(`INSERT INTO build_runners(name,kind,labels,enabled,max_parallel,created_at,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(name) DO UPDATE SET kind=excluded.kind,labels=excluded.labels,enabled=excluded.enabled,max_parallel=excluded.max_parallel,updated_at=excluded.updated_at`, v.Name, v.Kind, v.Labels, v.Enabled, v.MaxParallel, now, now)
	return err
}
func (s *Store) Runners() []BuildRunner {
	rows, err := s.db.Query(`SELECT name,kind,labels,enabled,max_parallel,updated_at FROM build_runners ORDER BY name`)
	if err != nil {
		return []BuildRunner{}
	}
	defer rows.Close()
	out := []BuildRunner{}
	for rows.Next() {
		var v BuildRunner
		if rows.Scan(&v.Name, &v.Kind, &v.Labels, &v.Enabled, &v.MaxParallel, &v.UpdatedAt) == nil {
			out = append(out, v)
		}
	}
	return out
}
func (s *Store) GetRunner(name string) *BuildRunner {
	var v BuildRunner
	if s.db.QueryRow(`SELECT name,kind,labels,enabled,max_parallel,updated_at FROM build_runners WHERE name=?`, name).Scan(&v.Name, &v.Kind, &v.Labels, &v.Enabled, &v.MaxParallel, &v.UpdatedAt) != nil {
		return nil
	}
	return &v
}

func randomToken() string { b := make([]byte, 16); _, _ = rand.Read(b); return hex.EncodeToString(b) }
func (s *Store) UpsertServiceDelivery(d ServiceDelivery) error {
	if s.GetService(d.Service) == nil {
		return fmt.Errorf("应用不存在")
	}
	if d.BuildEnvironment == nil {
		d.BuildEnvironment = s.GetServiceDelivery(d.Service).BuildEnvironment
	}
	if err := validateBuildEnvironment(d.BuildEnvironment, s.GetService(d.Service)); err != nil {
		return err
	}
	if d.Runner == "" {
		d.Runner = "local"
	}
	r := s.GetRunner(d.Runner)
	if r == nil || !r.Enabled {
		return fmt.Errorf("构建 Runner 不存在或已停用")
	}
	if d.MavenConnection != "" {
		c := s.GetConnection(d.MavenConnection)
		if c == nil || c.Kind != "maven" {
			return fmt.Errorf("Maven 连接无效")
		}
	}
	if d.GitConnection != "" {
		c := s.GetConnection(d.GitConnection)
		if c == nil || c.Kind != "git" {
			return fmt.Errorf("Git 连接无效")
		}
	}
	if d.RegistryConnection != "" {
		c := s.GetConnection(d.RegistryConnection)
		if c == nil || c.Kind != "docker" {
			return fmt.Errorf("镜像仓库连接无效")
		}
	}
	for _, g := range d.VariableGroups {
		if s.variableGroupValues(g) == nil {
			return fmt.Errorf("变量组不存在: %s", g)
		}
	}
	if d.ScheduleMinutes < 0 {
		return fmt.Errorf("定时构建间隔不能小于 0")
	}
	if d.AutoDeployEnvironment != "" && s.GetServiceEnvironment(d.Service, d.AutoDeployEnvironment) == nil {
		return fmt.Errorf("自动发布环境尚未配置")
	}
	if d.WebhookToken == "" {
		if old := s.GetServiceDelivery(d.Service); old != nil && old.WebhookToken != "" {
			d.WebhookToken = old.WebhookToken
		} else {
			d.WebhookToken = randomToken()
		}
	}
	groups, _ := json.Marshal(d.VariableGroups)
	buildEnvironment, _ := json.Marshal(d.BuildEnvironment)
	now := time.Now().Unix()
	_, err := s.db.Exec(`INSERT INTO service_delivery(service,runner,git_connection,maven_connection,registry_connection,variable_groups,webhook_token,schedule_minutes,auto_deploy_environment,last_schedule_at,updated_at,build_environment) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(service) DO UPDATE SET runner=excluded.runner,git_connection=excluded.git_connection,maven_connection=excluded.maven_connection,registry_connection=excluded.registry_connection,variable_groups=excluded.variable_groups,webhook_token=excluded.webhook_token,schedule_minutes=excluded.schedule_minutes,auto_deploy_environment=excluded.auto_deploy_environment,updated_at=excluded.updated_at,build_environment=excluded.build_environment`, d.Service, d.Runner, d.GitConnection, d.MavenConnection, d.RegistryConnection, string(groups), d.WebhookToken, d.ScheduleMinutes, d.AutoDeployEnvironment, d.LastScheduleAt, now, string(buildEnvironment))
	return err
}
func (s *Store) GetServiceDelivery(service string) *ServiceDelivery {
	var d ServiceDelivery
	var groups, buildEnvironment string
	if s.db.QueryRow(`SELECT service,runner,git_connection,maven_connection,registry_connection,variable_groups,webhook_token,schedule_minutes,auto_deploy_environment,last_schedule_at,updated_at,build_environment FROM service_delivery WHERE service=?`, service).Scan(&d.Service, &d.Runner, &d.GitConnection, &d.MavenConnection, &d.RegistryConnection, &groups, &d.WebhookToken, &d.ScheduleMinutes, &d.AutoDeployEnvironment, &d.LastScheduleAt, &d.UpdatedAt, &buildEnvironment) != nil {
		return &ServiceDelivery{Service: service, Runner: "local"}
	}
	_ = json.Unmarshal([]byte(groups), &d.VariableGroups)
	_ = json.Unmarshal([]byte(buildEnvironment), &d.BuildEnvironment)
	return &d
}
func (s *Store) AllServiceDelivery() []ServiceDelivery {
	rows, err := s.db.Query(`SELECT service,runner,git_connection,maven_connection,registry_connection,variable_groups,webhook_token,schedule_minutes,auto_deploy_environment,last_schedule_at,updated_at,build_environment FROM service_delivery ORDER BY service`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []ServiceDelivery{}
	for rows.Next() {
		var d ServiceDelivery
		var groups, buildEnvironment string
		if rows.Scan(&d.Service, &d.Runner, &d.GitConnection, &d.MavenConnection, &d.RegistryConnection, &groups, &d.WebhookToken, &d.ScheduleMinutes, &d.AutoDeployEnvironment, &d.LastScheduleAt, &d.UpdatedAt, &buildEnvironment) == nil {
			_ = json.Unmarshal([]byte(groups), &d.VariableGroups)
			_ = json.Unmarshal([]byte(buildEnvironment), &d.BuildEnvironment)
			out = append(out, d)
		}
	}
	return out
}
func (s *Store) MarkScheduled(service string, at int64) {
	_, _ = s.db.Exec(`UPDATE service_delivery SET last_schedule_at=? WHERE service=?`, at, service)
}

func (s *Store) PrepareServiceResources(base *Service) (*Service, error) {
	return s.prepareServiceResources(base, s.GetServiceDelivery(base.Name))
}
func (s *Store) prepareServiceResources(base *Service, d *ServiceDelivery) (*Service, error) {
	copyService := *base
	copyService.RuntimeEnv = map[string]string{}
	if d == nil {
		return &copyService, nil
	}
	copyService.BuildEnvironment = d.BuildEnvironment
	if err := validateBuildEnvironment(d.BuildEnvironment, base); err != nil {
		return nil, err
	}
	r := s.GetRunner(d.Runner)
	if r == nil || !r.Enabled {
		return nil, fmt.Errorf("Runner %s 不可用", d.Runner)
	}
	for _, g := range d.VariableGroups {
		for k, v := range s.variableGroupValues(g) {
			copyService.RuntimeEnv[k] = v
			if v != "" {
				copyService.RuntimeSecrets = append(copyService.RuntimeSecrets, v)
			}
		}
	}
	if d.GitConnection != "" {
		c := s.GetConnection(d.GitConnection)
		if c == nil {
			return nil, fmt.Errorf("Git 连接不存在")
		}
		if connectionHost(c.Endpoint) != connectionHost(base.GitURL) {
			return nil, fmt.Errorf("Git 连接 %s 与代码仓库主机不匹配", c.Name)
		}
		mode, err := normalizeGitAuthMode(c.Config["auth_mode"])
		if err != nil {
			return nil, err
		}
		sshURL := isGitSSHURL(base.GitURL)
		if mode == gitAuthSSH && !sshURL {
			return nil, fmt.Errorf("Git 连接 %s 使用 SSH 私钥，但代码仓库地址是 HTTPS；请统一认证方式", c.Name)
		}
		if mode == gitAuthHTTPS && sshURL {
			return nil, fmt.Errorf("Git 连接 %s 使用 HTTPS/PAT，但代码仓库地址是 SSH；请统一认证方式", c.Name)
		}
		copyService.RuntimeEnv["GIT_USERNAME"] = c.Username
		if mode == gitAuthSSH {
			copyService.RuntimeEnv["GIT_SSH_PRIVATE_KEY"] = c.PrivateKey
			copyService.RuntimeEnv["GIT_SSH_PASSPHRASE"] = c.Passphrase
			copyService.RuntimeEnv["GIT_SSH_KNOWN_HOSTS"] = c.Config["known_hosts"]
			copyService.RuntimeEnv["GIT_SSH_FINGERPRINT"] = c.Config["ssh_fingerprint"]
			copyService.RuntimeSecrets = append(copyService.RuntimeSecrets, c.PrivateKey)
			if c.Passphrase != "" {
				copyService.RuntimeSecrets = append(copyService.RuntimeSecrets, c.Passphrase)
			}
		} else {
			copyService.RuntimeEnv["GIT_PASSWORD"] = c.Password
			if c.Password != "" {
				copyService.RuntimeSecrets = append(copyService.RuntimeSecrets, c.Password)
			}
		}
	}
	if d.MavenConnection != "" {
		c := s.GetConnection(d.MavenConnection)
		if c == nil {
			return nil, fmt.Errorf("依赖仓库连接不存在")
		}
		if copyService.BuildTool == "maven" && copyService.MavenSettingsPath == "" {
			copyService.MavenSettingsPath = c.Config["settings_path"]
			if copyService.MavenSettingsPath == "" {
				var err error
				copyService.MavenSettingsPath, err = s.writeMavenSettings(c)
				if err != nil {
					return nil, err
				}
			}
		}
		copyService.RuntimeEnv["MAVEN_REPOSITORY_URL"] = c.Endpoint
		copyService.RuntimeEnv["MAVEN_USERNAME"] = c.Username
		copyService.RuntimeEnv["MAVEN_PASSWORD"] = c.Password
		copyService.RuntimeEnv["GRADLE_REPOSITORY_URL"] = c.Endpoint
		copyService.RuntimeEnv["GRADLE_REPOSITORY_USERNAME"] = c.Username
		copyService.RuntimeEnv["GRADLE_REPOSITORY_PASSWORD"] = c.Password
		if c.Password != "" {
			copyService.RuntimeSecrets = append(copyService.RuntimeSecrets, c.Password)
		}
	}
	if d.RegistryConnection != "" {
		c := s.GetConnection(d.RegistryConnection)
		if c == nil {
			return nil, fmt.Errorf("镜像仓库连接不存在")
		}
		copyService.RuntimeEnv["DOCKER_REGISTRY"] = c.Endpoint
		copyService.RuntimeEnv["DOCKER_USERNAME"] = c.Username
		copyService.RuntimeEnv["DOCKER_PASSWORD"] = c.Password
		if c.Password != "" {
			copyService.RuntimeSecrets = append(copyService.RuntimeSecrets, c.Password)
		}
	}
	return &copyService, nil
}

func connectionHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if parsed, err := url.Parse(raw); err == nil && parsed.Hostname() != "" {
		return strings.ToLower(parsed.Hostname())
	}
	if at := strings.LastIndex(raw, "@"); at >= 0 {
		raw = raw[at+1:]
	}
	if i := strings.IndexAny(raw, ":/"); i >= 0 {
		raw = raw[:i]
	}
	return strings.ToLower(raw)
}

func xmlText(v string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(v))
	return b.String()
}

// writeMavenSettings 生成不含明文凭据的 settings.xml。Maven 在构建进程内
// 通过环境变量读取账号，连接配置因此可以开箱即用且不会把密码写入磁盘。
func (s *Store) writeMavenSettings(c *ResourceConnection) (string, error) {
	if strings.TrimSpace(c.Endpoint) == "" {
		return "", fmt.Errorf("Maven 连接 %s 未配置仓库地址", c.Name)
	}
	dir := filepath.Join(s.dataDir, "generated", "maven")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	id := c.Config["repository_id"]
	if id == "" {
		id = c.Name
	}
	mirrorOf := c.Config["mirror_of"]
	if mirrorOf == "" {
		mirrorOf = "*"
	}
	id = xmlText(id)
	endpoint := xmlText(strings.TrimRight(c.Endpoint, "/"))
	mirrorOf = xmlText(mirrorOf)
	content := `<?xml version="1.0" encoding="UTF-8"?>
<settings xmlns="http://maven.apache.org/SETTINGS/1.2.0">
  <servers><server><id>` + id + `</id><username>${env.MAVEN_USERNAME}</username><password>${env.MAVEN_PASSWORD}</password></server></servers>
  <mirrors><mirror><id>` + id + `</id><name>LogPlatform managed repository</name><url>` + endpoint + `</url><mirrorOf>` + mirrorOf + `</mirrorOf></mirror></mirrors>
</settings>
`
	path := filepath.Join(dir, c.Name+".xml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Store) Audit(actor, action, resourceType, resourceID string, detail any) {
	if actor == "" {
		actor = "local"
	}
	raw, _ := json.Marshal(detail)
	_, _ = s.db.Exec(`INSERT INTO audit_events(actor,action,resource_type,resource_id,detail,created_at) VALUES(?,?,?,?,?,?)`, actor, action, resourceType, resourceID, string(raw), time.Now().Unix())
}
func (s *Store) AuditEvents(limit int) []AuditEvent {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id,actor,action,resource_type,resource_id,detail,created_at FROM audit_events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return []AuditEvent{}
	}
	defer rows.Close()
	out := []AuditEvent{}
	for rows.Next() {
		var e AuditEvent
		var detail string
		if rows.Scan(&e.ID, &e.Actor, &e.Action, &e.ResourceType, &e.ResourceID, &detail, &e.CreatedAt) == nil {
			_ = json.Unmarshal([]byte(detail), &e.Detail)
			out = append(out, e)
		}
	}
	return out
}

var _ = sql.ErrNoRows
