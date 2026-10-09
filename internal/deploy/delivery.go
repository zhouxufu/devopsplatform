package deploy

// Delivery plans are immutable approval snapshots. The first executor is
// deliberately stepwise: deploy uses the existing batch release engine and
// manual gates require an explicit confirmation. Unimplemented executors fail
// closed instead of marking configuration/database changes as successful.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type DeliveryNode struct {
	ID           string  `json:"id"`
	Kind         string  `json:"kind"`
	Name         string  `json:"name"`
	Service      string  `json:"service,omitempty"`
	Instructions string  `json:"instructions,omitempty"`
	ChangeIDs    []int64 `json:"change_ids,omitempty"`
}
type DeliveryTemplate struct {
	Workbench string         `json:"workbench,omitempty"`
	ID        int64          `json:"id"`
	Name      string         `json:"name"`
	Mode      string         `json:"mode"`
	Services  []string       `json:"services"`
	Nodes     []DeliveryNode `json:"nodes"`
	Owner     string         `json:"owner"`
}
type DeliveryRequest struct {
	TemplateID  int64            `json:"template_id"`
	VersionID   int64            `json:"version_id"`
	Environment string           `json:"environment"`
	Reason      string           `json:"reason"`
	Artifacts   map[string]int64 `json:"artifacts"`
	// A run may override/add/remove nodes without changing the reusable template.
	Nodes []DeliveryNode `json:"nodes,omitempty"`
}
type DeliveryService struct {
	Service           string             `json:"service"`
	Artifact          Artifact           `json:"artifact"`
	Target            ServiceEnvironment `json:"target"`
	DestinationDigest string             `json:"destination_digest"`
}
type DeliveryPlan struct {
	Template    DeliveryTemplate      `json:"template"`
	Version     *ReleaseVersion       `json:"version,omitempty"`
	Environment Environment           `json:"environment"`
	Reason      string                `json:"reason"`
	Services    []DeliveryService     `json:"services"`
	Nodes       []DeliveryNode        `json:"nodes"`
	Blockers    []string              `json:"blockers"`
	Warnings    []string              `json:"warnings"`
	SQLPlans    []*SQLExecutionPlan   `json:"sql_plans,omitempty"`
	NacosPlans  []*NacosExecutionPlan `json:"nacos_plans,omitempty"`
	RedisPlans  []*RedisExecutionPlan `json:"redis_plans,omitempty"`
}
type DeliveryStep struct {
	NodeID    string `json:"node_id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Service   string `json:"service,omitempty"`
	Status    string `json:"status"`
	ReleaseID int64  `json:"release_id,omitempty"`
	Detail    string `json:"detail,omitempty"`
}
type DeliveryRun struct {
	ID         int64          `json:"id"`
	Plan       DeliveryPlan   `json:"plan"`
	Digest     string         `json:"digest"`
	Status     string         `json:"status"`
	Steps      []DeliveryStep `json:"steps"`
	Owner      string         `json:"owner"`
	Approver   string         `json:"approver,omitempty"`
	CreatedAt  int64          `json:"created_at"`
	Revision   int64          `json:"revision"`
	envSecrets map[string]string
}

func (v DeliveryRun) MarshalJSON() ([]byte, error) {
	type publicRun DeliveryRun
	p := publicRun(v)
	p.Plan.NacosPlans = publicNacosPlans(v.Plan.NacosPlans)
	return json.Marshal(p)
}

func deliverySchema(dialect string) []string {
	identity, doc := "INTEGER PRIMARY KEY AUTOINCREMENT", "TEXT"
	if dialect == "mysql" {
		identity, doc = "BIGINT PRIMARY KEY AUTO_INCREMENT", "LONGTEXT"
	}
	queries := []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS delivery_templates (id %s, document %s NOT NULL, owner VARCHAR(191) NOT NULL)`, identity, doc),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS delivery_runs (id %s, plan %s NOT NULL, digest VARCHAR(80) NOT NULL, status VARCHAR(32) NOT NULL, execution %s NOT NULL, env_secrets %s NOT NULL, owner VARCHAR(191) NOT NULL, approver VARCHAR(191) NOT NULL, created_at BIGINT NOT NULL, revision BIGINT NOT NULL)`, identity, doc, doc, doc),
	}
	queries = append(queries, integrationSchema(identity, doc)...)
	if dialect == "mysql" {
		for i := range queries {
			queries[i] += ` ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`
		}
	}
	return queries
}
func (s *Store) initDeliverySchema() error {
	for _, q := range deliverySchema(s.Driver()) {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}
	_, err := s.db.Exec(`INSERT OR IGNORE INTO resource_applications(name) SELECT DISTINCT service FROM application_resource_requirements`)
	return err
}

func validateDeliveryNodes(nodes []DeliveryNode) error {
	if len(nodes) == 0 || len(nodes) > 30 {
		return fmt.Errorf("编排需要 1–30 个顺序节点")
	}
	seen := map[string]bool{}
	for _, n := range nodes {
		if !environmentNameRE.MatchString(n.ID) || seen[n.ID] || strings.TrimSpace(n.Name) == "" {
			return fmt.Errorf("节点需要不重复的英文标识和名称")
		}
		seen[n.ID] = true
		switch n.Kind {
		case "deploy", "manual", "mysql", "nacos", "data-sync", "redis":
		default:
			return fmt.Errorf("不支持的节点类型：%s", n.Kind)
		}
		if n.Kind == "manual" && strings.TrimSpace(n.Instructions) == "" {
			return fmt.Errorf("人工节点需要填写操作与确认说明")
		}
	}
	return nil
}
func (s *Store) SaveDeliveryTemplate(v *DeliveryTemplate) error {
	v.Name = strings.TrimSpace(v.Name)
	if v.Name == "" || (v.Mode != "fixed" && v.Mode != "version") {
		return fmt.Errorf("模板名称必填；范围支持 fixed 或 version")
	}
	if err := validateDeliveryNodes(v.Nodes); err != nil {
		return err
	}
	if v.Workbench != "" {
		if err := validateWorkbenchTemplate(*v); err != nil {
			return err
		}
	}
	if v.Mode == "fixed" && len(v.Services) == 0 {
		for _, node := range v.Nodes {
			if node.Kind == "deploy" {
				return fmt.Errorf("包含部署节点的固定服务模式至少选择一个应用")
			}
		}
	}
	seen := map[string]bool{}
	for _, name := range v.Services {
		if seen[name] || s.GetService(name) == nil {
			return fmt.Errorf("固定服务不存在或重复：%s", name)
		}
		seen[name] = true
	}
	if v.Mode == "version" {
		v.Services = []string{}
	}
	data, _ := json.Marshal(v)
	if v.ID == 0 {
		r, err := s.db.Exec(`INSERT INTO delivery_templates(document,owner) VALUES(?,?)`, string(data), v.Owner)
		if err != nil {
			return err
		}
		v.ID, _ = r.LastInsertId()
		return nil
	}
	r, err := s.db.Exec(`UPDATE delivery_templates SET document=? WHERE id=? AND owner=?`, string(data), v.ID, v.Owner)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		current := s.DeliveryTemplate(v.ID)
		if current == nil || current.Owner != v.Owner {
			return fmt.Errorf("模板不存在或已改变")
		}
	}
	return nil
}
func (s *Store) DeliveryTemplates() []DeliveryTemplate {
	out := []DeliveryTemplate{}
	rows, err := s.db.Query(`SELECT id,document,owner FROM delivery_templates ORDER BY id DESC`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var doc, owner string
		if rows.Scan(&id, &doc, &owner) == nil {
			var v DeliveryTemplate
			if json.Unmarshal([]byte(doc), &v) == nil {
				v.ID, v.Owner = id, owner
				out = append(out, v)
			}
		}
	}
	return out
}
func (s *Store) DeliveryTemplate(id int64) *DeliveryTemplate {
	for _, v := range s.DeliveryTemplates() {
		if v.ID == id {
			return &v
		}
	}
	return nil
}

// Pin destination identities, not SSH/cloud credential plaintext. Rotating a
// credential does not change the host identity, changing its endpoint does.
func (s *Store) deliveryDestinationDigest(target ServiceEnvironment) (string, error) {
	identities := []any{}
	if target.ManagedTarget() {
		connection := s.GetConnection(target.DeployConfig["connection"])
		if connection == nil {
			return "", fmt.Errorf("云资源连接不存在")
		}
		identities = append(identities, map[string]any{"name": connection.Name, "kind": connection.Kind, "endpoint": connection.Endpoint, "config": connection.Config})
	} else {
		for _, name := range target.Hosts {
			host := s.GetHost(name)
			if host == nil || host.Environment != target.Environment {
				return "", fmt.Errorf("目标主机不存在或环境不一致：%s", name)
			}
			identities = append(identities, map[string]string{"name": host.Name, "environment": host.Environment, "addr": host.Addr, "user": host.User, "key_path": host.KeyPath, "host_key": host.HostKey})
		}
	}
	data, _ := json.Marshal(identities)
	hash := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(hash[:]), nil
}

func (s *Store) ResolveDelivery(req DeliveryRequest, manager *ReleaseManager) (*DeliveryPlan, map[string]string, error) {
	tpl := s.DeliveryTemplate(req.TemplateID)
	env := s.GetEnvironment(req.Environment)
	if tpl == nil || env == nil {
		return nil, nil, fmt.Errorf("请选择有效模板和环境")
	}
	nodes := tpl.Nodes
	if req.Nodes != nil {
		nodes = req.Nodes
	}
	if err := validateDeliveryNodes(nodes); err != nil {
		return nil, nil, err
	}
	p := &DeliveryPlan{Template: *tpl, Environment: *env, Reason: strings.TrimSpace(req.Reason), Nodes: nodes, Services: []DeliveryService{}, Blockers: []string{}, Warnings: []string{}}
	if tpl.Workbench != "" {
		candidate := *tpl
		candidate.Nodes = nodes
		if err := validateWorkbenchTemplate(candidate); err != nil {
			return nil, nil, err
		}
		if strings.TrimSpace(env.Approvers) == "" {
			p.Blockers = append(p.Blockers, "工作台操作必须审核，请先为环境配置审核人")
		}
	}
	secrets := map[string]string{}
	services := map[string]bool{}
	if tpl.Mode == "fixed" {
		for _, name := range tpl.Services {
			services[name] = true
		}
	} else {
		version := s.GetReleaseVersion(req.VersionID)
		if version == nil || (version.Status != ReleaseVersionReady && version.Status != ReleaseVersionCompleted) {
			return nil, nil, fmt.Errorf("请选择已就绪的发布版本")
		}
		p.Version = version
		for _, c := range version.Changes {
			if c.Kind == "code" || c.Kind == "artifact" {
				if c.Service == "" {
					p.Blockers = append(p.Blockers, "代码/制品变更未关联应用："+c.Title)
				} else {
					services[c.Service] = true
				}
			} else if c.Kind != "manual" && c.Kind != "mysql" && c.Kind != "nacos" && c.Kind != "redis" {
				p.Blockers = append(p.Blockers, "变更执行器尚未接通："+c.Title+"（"+c.Kind+"）")
			}
		}
		p.Warnings = append(p.Warnings, "不会自动合并分支或触发构建；请确认每个候选制品包含本版本所选代码，不能用测试分支的全部代码替代。")
		if len(version.Changes) == 0 {
			p.Blockers = append(p.Blockers, "版本未包含变更")
		}
	}
	if env.RequireReason && p.Reason == "" {
		p.Blockers = append(p.Blockers, "该环境必须填写发布原因")
	}
	if env.Protected && strings.TrimSpace(env.Approvers) == "" {
		p.Blockers = append(p.Blockers, "受保护环境尚未配置发布审核人")
	}
	names := []string{}
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		sv, artifact := s.GetService(name), s.GetArtifact(req.Artifacts[name])
		if sv == nil || artifact == nil || artifact.Service != name || artifact.Kind != artifactKindForService(sv.ArtifactType) {
			p.Blockers = append(p.Blockers, "请选择应用的有效制品："+name)
			continue
		}
		target, err := manager.ValidateTarget(name, env.Name, p.Reason)
		if err != nil {
			p.Blockers = append(p.Blockers, name+"："+err.Error())
			continue
		}
		if artifact.Digest == "" {
			p.Blockers = append(p.Blockers, name+"：制品缺少摘要")
		}
		secrets[name] = target.runtimeEnvEncrypted
		target.RuntimeEnv = nil // never persist/reveal plaintext application secrets
		destination, err := s.deliveryDestinationDigest(*target)
		if err != nil {
			p.Blockers = append(p.Blockers, name+"："+err.Error())
			continue
		}
		p.Services = append(p.Services, DeliveryService{Service: name, Artifact: *artifact, Target: *target, DestinationDigest: destination})
	}
	allocated := map[string]int{}
	sqlAllocated := map[int64]int{}
	sqlConnections := map[string]bool{}
	manualCount := 0
	sqlCount := 0
	nacosAllocated := map[int64]int{}
	nacosTargets := map[string]bool{}
	nacosCount := 0
	redisAllocated := map[int64]int{}
	redisCount := 0
	for _, node := range nodes {
		switch node.Kind {
		case "redis":
			if strings.TrimSpace(env.Approvers) == "" {
				p.Blockers = append(p.Blockers, "Redis 操作必须审核，请先为该环境配置审核人（开发/测试也需要）")
			}
			ids := append([]int64{}, node.ChangeIDs...)
			if p.Version != nil {
				allowed := map[int64]bool{}
				for _, c := range p.Version.Changes {
					if c.Kind == "redis" {
						allowed[c.ID] = true
						if len(node.ChangeIDs) == 0 && (node.Service == "" || node.Service == c.Service) {
							ids = append(ids, c.ID)
						}
					}
				}
				for _, id := range ids {
					if !allowed[id] {
						p.Blockers = append(p.Blockers, fmt.Sprintf("Redis 节点引用版本外变更 #%d", id))
					}
				}
			}
			ready := true
			for _, id := range ids {
				redisAllocated[id]++
				c := s.GetChange(id)
				if c == nil || c.Status != ChangeReady {
					p.Blockers = append(p.Blockers, fmt.Sprintf("Redis 变更 #%d 尚未就绪", id))
					ready = false
				}
			}
			if !ready {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			plan, err := s.prepareRedisNode(ctx, node.ID, ids, env.Name)
			cancel()
			if err != nil {
				p.Blockers = append(p.Blockers, node.Name+"："+err.Error())
				continue
			}
			p.RedisPlans = append(p.RedisPlans, plan)
			redisCount++
			p.Warnings = append(p.Warnings, "Redis 命令必须由另一名环境审核人审核；精确 Key 的当前数据不冻结、不校验内容，命令顺序执行、非事务、无自动回滚；超时不得重试。缓存清理无法恢复，请确认业务可重建缓存。")
		case "nacos":
			ids := append([]int64{}, node.ChangeIDs...)
			if p.Version != nil {
				allowed := map[int64]bool{}
				for _, c := range p.Version.Changes {
					if c.Kind == "nacos" {
						allowed[c.ID] = true
						if len(node.ChangeIDs) == 0 && (node.Service == "" || node.Service == c.Service) {
							ids = append(ids, c.ID)
						}
					}
				}
				for _, id := range ids {
					if !allowed[id] {
						p.Blockers = append(p.Blockers, fmt.Sprintf("Nacos 节点引用版本外变更 #%d", id))
					}
				}
			}
			ready := true
			for _, id := range ids {
				nacosAllocated[id]++
				c := s.GetChange(id)
				if c == nil || c.Status != ChangeReady {
					p.Blockers = append(p.Blockers, fmt.Sprintf("Nacos 变更 #%d 尚未就绪", id))
					ready = false
				}
			}
			if !ready {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			plan, err := s.prepareNacosNode(ctx, node.ID, ids, env.Name)
			cancel()
			if err != nil {
				p.Blockers = append(p.Blockers, node.Name+"："+err.Error())
				continue
			}
			for _, item := range plan.Configs {
				key := nacosTargetKey(item)
				if nacosTargets[key] {
					p.Blockers = append(p.Blockers, "同一 Nacos 配置不能在多个节点重复修改")
				}
				nacosTargets[key] = true
			}
			p.NacosPlans = append(p.NacosPlans, plan)
			nacosCount++
			p.Warnings = append(p.Warnings, "Nacos 多文档顺序发布，不保证跨配置或跨服务原子性；失败停止，已成功项不自动恢复。差异值可能包含敏感信息，请限制执行单访问。")
		case "deploy":
			if node.Service != "" && !services[node.Service] {
				p.Blockers = append(p.Blockers, "部署节点应用不在交付范围："+node.Service)
			}
			for _, name := range names {
				if node.Service == "" || node.Service == name {
					allocated[name]++
				}
			}
		case "manual":
			manualCount++
		case "mysql":
			if strings.TrimSpace(env.Approvers) == "" {
				p.Blockers = append(p.Blockers, "SQL 写操作必须审核，请先为该环境配置审核人")
			}
			ids := append([]int64{}, node.ChangeIDs...)
			if p.Version != nil {
				allowed := map[int64]bool{}
				for _, c := range p.Version.Changes {
					if c.Kind == "mysql" {
						allowed[c.ID] = true
						if len(node.ChangeIDs) == 0 && (node.Service == "" || node.Service == c.Service) {
							ids = append(ids, c.ID)
						}
					}
				}
				for _, id := range ids {
					if !allowed[id] {
						p.Blockers = append(p.Blockers, fmt.Sprintf("SQL 节点引用版本外变更 #%d", id))
					}
				}
			}
			ready := true
			for _, id := range ids {
				sqlAllocated[id]++
				c := s.GetChange(id)
				if c == nil || c.Status != ChangeReady {
					p.Blockers = append(p.Blockers, fmt.Sprintf("SQL 变更 #%d 尚未就绪", id))
					ready = false
				}
			}
			if !ready {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			plan, err := s.prepareSQLNode(ctx, node.ID, ids, env.Name)
			cancel()
			if err != nil {
				p.Blockers = append(p.Blockers, node.Name+"："+err.Error())
				continue
			}
			if sqlConnections[plan.Connection] {
				p.Blockers = append(p.Blockers, "同一数据库连接只允许一个 SQL 节点，避免基线被前序节点改变；DDL/DML 请分次交付")
			} else {
				sqlConnections[plan.Connection] = true
			}
			p.SQLPlans = append(p.SQLPlans, plan)
			p.Warnings = append(p.Warnings, plan.Warnings...)
			sqlCount++
		default:
			p.Blockers = append(p.Blockers, "节点仅支持设计与保存，执行器尚未接通："+node.Name)
		}
	}
	for _, name := range names {
		if allocated[name] != 1 {
			p.Blockers = append(p.Blockers, name+" 必须且只能安排在一个部署节点中")
		}
	}
	if p.Version != nil {
		for _, c := range p.Version.Changes {
			if c.Kind == "redis" && redisAllocated[c.ID] != 1 {
				p.Blockers = append(p.Blockers, fmt.Sprintf("Redis 变更 #%d 必须且只能安排到一个 Redis 节点", c.ID))
			}
			if c.Kind == "nacos" && nacosAllocated[c.ID] != 1 {
				p.Blockers = append(p.Blockers, fmt.Sprintf("Nacos 变更 #%d 必须且只能安排到一个 Nacos 节点", c.ID))
			}
			if c.Kind == "mysql" && sqlAllocated[c.ID] != 1 {
				p.Blockers = append(p.Blockers, fmt.Sprintf("SQL 变更 #%d 必须且只能安排到一个 SQL 节点", c.ID))
			}
			if c.Kind == "manual" && manualCount == 0 {
				p.Blockers = append(p.Blockers, "人工变更需要配置人工确认节点："+c.Title)
			}
		}
	}
	for id, count := range sqlAllocated {
		if count != 1 {
			p.Blockers = append(p.Blockers, fmt.Sprintf("SQL 变更 #%d 在多个节点中重复", id))
		}
	}
	for id, count := range nacosAllocated {
		if count != 1 {
			p.Blockers = append(p.Blockers, fmt.Sprintf("Nacos 变更 #%d 在多个节点重复", id))
		}
	}
	for id, count := range redisAllocated {
		if count != 1 {
			p.Blockers = append(p.Blockers, fmt.Sprintf("Redis 变更 #%d 在多个节点重复", id))
		}
	}
	if len(names) == 0 && manualCount == 0 && sqlCount == 0 && nacosCount == 0 && redisCount == 0 {
		p.Blockers = append(p.Blockers, "交付范围没有可执行应用或人工确认节点")
	}
	return p, secrets, nil
}

func (s *Store) CreateDeliveryRun(req DeliveryRequest, actor string, manager *ReleaseManager) (*DeliveryRun, error) {
	p, secrets, err := s.ResolveDelivery(req, manager)
	if err != nil {
		return nil, err
	}
	if len(p.Blockers) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(p.Blockers, "；"))
	}
	planJSON, _ := json.Marshal(p)
	hash := sha256.Sum256(planJSON)
	run := &DeliveryRun{Plan: *p, Digest: "sha256:" + hex.EncodeToString(hash[:]), Status: "approved", Owner: actor, CreatedAt: time.Now().Unix(), Revision: 1, Steps: []DeliveryStep{}, envSecrets: secrets}
	if p.Environment.Protected || len(p.RedisPlans) > 0 || len(p.SQLPlans) > 0 || p.Template.Workbench != "" {
		run.Status = "pending_approval"
	}
	for _, node := range p.Nodes {
		if node.Kind == "deploy" {
			for _, app := range p.Services {
				if node.Service == "" || node.Service == app.Service {
					run.Steps = append(run.Steps, DeliveryStep{NodeID: node.ID, Name: node.Name + " · " + app.Service, Kind: "deploy", Service: app.Service, Status: "pending"})
				}
			}
		} else {
			run.Steps = append(run.Steps, DeliveryStep{NodeID: node.ID, Name: node.Name, Kind: node.Kind, Status: "pending"})
		}
	}
	execution, _ := json.Marshal(run.Steps)
	secretJSON, _ := json.Marshal(secrets)
	result, err := s.db.Exec(`INSERT INTO delivery_runs(plan,digest,status,execution,env_secrets,owner,approver,created_at,revision) VALUES(?,?,?,?,?,?,?, ?,1)`, string(planJSON), run.Digest, run.Status, string(execution), string(secretJSON), actor, "", run.CreatedAt)
	if err != nil {
		return nil, err
	}
	run.ID, _ = result.LastInsertId()
	s.Audit(actor, "create", "delivery-run", fmt.Sprint(run.ID), map[string]any{"digest": run.Digest, "environment": req.Environment})
	return run, nil
}

const deliveryRunSelect = `SELECT id,plan,digest,status,execution,env_secrets,owner,approver,created_at,revision FROM delivery_runs`

func scanDeliveryRun(row rowScanner) (*DeliveryRun, error) {
	v := &DeliveryRun{}
	var plan, steps, secrets string
	if err := row.Scan(&v.ID, &plan, &v.Digest, &v.Status, &steps, &secrets, &v.Owner, &v.Approver, &v.CreatedAt, &v.Revision); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(plan), &v.Plan); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(steps), &v.Steps); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(secrets), &v.envSecrets); err != nil {
		return nil, err
	}
	return v, nil
}
func (s *Store) DeliveryRun(id int64) *DeliveryRun {
	v, err := scanDeliveryRun(s.db.QueryRow(deliveryRunSelect+` WHERE id=?`, id))
	if err != nil {
		return nil
	}
	return v
}
func (s *Store) DeliveryRuns() []DeliveryRun {
	out := []DeliveryRun{}
	rows, err := s.db.Query(deliveryRunSelect + ` ORDER BY id DESC LIMIT 100`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		if v, err := scanDeliveryRun(rows); err == nil {
			out = append(out, *v)
		}
	}
	return out
}
func (s *Store) saveDeliveryRun(v *DeliveryRun) error {
	execution, _ := json.Marshal(v.Steps)
	res, err := s.db.Exec(`UPDATE delivery_runs SET status=?,execution=?,approver=?,revision=revision+1 WHERE id=? AND revision=?`, v.Status, string(execution), v.Approver, v.ID, v.Revision)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrRunConflict
	}
	v.Revision++
	return nil
}
func (s *Store) ReviewDeliveryRun(id int64, actor, digest string, approve bool) (*DeliveryRun, error) {
	v := s.DeliveryRun(id)
	if v == nil || v.Status != "pending_approval" || digest != v.Digest {
		return nil, fmt.Errorf("审核单状态或内容摘要已改变")
	}
	env := s.GetEnvironment(v.Plan.Environment.Name)
	if env == nil || actor == v.Owner || !allowedApprover(v.Plan.Environment.Approvers, actor) || !allowedApprover(env.Approvers, actor) {
		return nil, fmt.Errorf("仅指定发布审核人可审核，且不能审核自己的发布")
	}
	v.Approver, v.Status = actor, "rejected"
	if approve {
		v.Status = "approved"
	}
	if err := s.saveDeliveryRun(v); err != nil {
		return nil, err
	}
	s.Audit(actor, v.Status, "delivery-run", fmt.Sprint(id), map[string]any{"digest": v.Digest})
	return v, nil
}

func (s *Store) AdvanceDeliveryRun(id int64, actor string, confirmManual bool, manager *ReleaseManager) (*DeliveryRun, error) {
	return s.advanceDeliveryRun(id, actor, confirmManual, manager, func(childID int64) { go manager.startNextBatch(childID) })
}

func (s *Store) advanceDeliveryRun(id int64, actor string, confirmManual bool, manager *ReleaseManager, launch func(int64)) (*DeliveryRun, error) {
	v := s.DeliveryRun(id)
	if v == nil || (v.Status != "approved" && v.Status != "running") {
		return nil, fmt.Errorf("仅已审核或执行中的编排可以继续")
	}
	env := s.GetEnvironment(v.Plan.Environment.Name)
	if env == nil || (env.Protected && (v.Approver == "" || !allowedApprover(env.Approvers, v.Approver))) {
		return nil, fmt.Errorf("环境保护策略已变化，请重新提交审核")
	}
	if (len(v.Plan.RedisPlans) > 0 || len(v.Plan.SQLPlans) > 0 || v.Plan.Template.Workbench != "") && (v.Approver == "" || v.Approver == v.Owner || !allowedApprover(env.Approvers, v.Approver) || !allowedApprover(v.Plan.Environment.Approvers, v.Approver)) {
		return nil, fmt.Errorf("SQL/Redis 工作台操作必须经另一名环境审核人审核，且审核人仍须有权限")
	}
	if env.RequireReason && v.Plan.Reason == "" {
		return nil, fmt.Errorf("环境已要求发布原因，请重新提交执行单")
	}
	for i := range v.Steps {
		step := &v.Steps[i]
		if step.Status == "success" {
			continue
		}
		if step.Kind == "redis" {
			if step.Status == "executing" {
				return nil, fmt.Errorf("Redis 节点正在执行或中断待核对，禁止自动重试")
			}
			var plan *RedisExecutionPlan
			for _, candidate := range v.Plan.RedisPlans {
				if candidate.NodeID == step.NodeID {
					plan = candidate
					break
				}
			}
			if plan == nil {
				return nil, fmt.Errorf("Redis 节点缺少已审核快照")
			}
			step.Status, v.Status = "executing", "running"
			if err := s.saveDeliveryRun(v); err != nil {
				return nil, err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			detail, err := s.executeRedisNode(ctx, v, plan)
			cancel()
			step.Detail = detail
			if err != nil {
				step.Status, v.Status = "failed", "failed"
				if detail == "" {
					step.Detail = err.Error()
				}
			} else {
				step.Status = "success"
			}
			if err := s.saveDeliveryRun(v); err != nil {
				return nil, err
			}
			s.Audit(actor, "execute-redis", "delivery-run", fmt.Sprint(id), map[string]any{"node": step.NodeID, "status": step.Status, "digest": v.Digest})
			return v, nil
		}
		if step.Kind == "nacos" {
			if step.Status == "executing" {
				return nil, fmt.Errorf("Nacos 节点正在执行或中断待核对，禁止自动重试")
			}
			var plan *NacosExecutionPlan
			for _, candidate := range v.Plan.NacosPlans {
				if candidate.NodeID == step.NodeID {
					plan = candidate
					break
				}
			}
			if plan == nil {
				return nil, fmt.Errorf("Nacos 节点缺少已审核快照")
			}
			step.Status, v.Status = "executing", "running"
			if err := s.saveDeliveryRun(v); err != nil {
				return nil, err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			detail, err := s.executeNacosNode(ctx, v, plan)
			cancel()
			step.Detail = detail
			if err != nil {
				step.Status, v.Status = "failed", "failed"
				if detail == "" {
					step.Detail = err.Error()
				}
			} else {
				step.Status = "success"
			}
			if err := s.saveDeliveryRun(v); err != nil {
				return nil, err
			}
			s.Audit(actor, "execute-nacos", "delivery-run", fmt.Sprint(id), map[string]any{"node": step.NodeID, "status": step.Status, "digest": v.Digest})
			return v, nil
		}
		if step.Kind == "mysql" {
			if step.Status == "executing" {
				return nil, fmt.Errorf("SQL 节点正在执行或上次执行中断，请检查执行台账；禁止自动重复执行")
			}
			var plan *SQLExecutionPlan
			for _, candidate := range v.Plan.SQLPlans {
				if candidate.NodeID == step.NodeID {
					plan = candidate
					break
				}
			}
			if plan == nil {
				return nil, fmt.Errorf("SQL 节点缺少已审核执行计划")
			}
			step.Status, v.Status = "executing", "running"
			if err := s.saveDeliveryRun(v); err != nil {
				return nil, err
			}
			ctx, cancel := sqlExecutionContext()
			detail, err := s.executeSQLNode(ctx, v, plan)
			cancel()
			step.Detail = detail
			if err != nil {
				step.Status, v.Status = "failed", "failed"
				if step.Detail == "" {
					step.Detail = err.Error()
				}
			} else {
				step.Status = "success"
			}
			if saveErr := s.saveDeliveryRun(v); saveErr != nil {
				return nil, saveErr
			}
			s.Audit(actor, "execute-sql", "delivery-run", fmt.Sprint(id), map[string]any{"node": step.NodeID, "status": step.Status, "digest": v.Digest})
			return v, nil
		}
		if step.ReleaseID > 0 {
			child := s.GetReleaseOrder(step.ReleaseID)
			if child == nil {
				return nil, fmt.Errorf("子发布单不存在")
			}
			if child.Status == "success" {
				step.Status = "success"
			} else if !releaseStatusActive(child.Status) {
				step.Status, v.Status = "failed", "failed"
			} else {
				return nil, fmt.Errorf("当前应用尚未发布完成；分批暂停请在子发布单中继续")
			}
			if err := s.saveDeliveryRun(v); err != nil {
				return nil, err
			}
			return v, nil
		}
		if step.Kind == "manual" {
			if !confirmManual {
				return nil, fmt.Errorf("请完成说明中的操作后显式确认人工节点")
			}
			step.Status, v.Status = "success", "running"
			if err := s.saveDeliveryRun(v); err != nil {
				return nil, err
			}
			s.Audit(actor, "confirm-manual", "delivery-run", fmt.Sprint(id), map[string]any{"node": step.NodeID, "digest": v.Digest})
			return v, nil
		}
		var app *DeliveryService
		for j := range v.Plan.Services {
			if v.Plan.Services[j].Service == step.Service {
				app = &v.Plan.Services[j]
				break
			}
		}
		if step.Kind != "deploy" || app == nil {
			return nil, fmt.Errorf("节点没有可用执行器")
		}
		currentArtifact := s.GetArtifact(app.Artifact.ID)
		currentService := s.GetService(app.Service)
		if currentArtifact == nil || currentService == nil || currentArtifact.Digest != app.Artifact.Digest || currentArtifact.Service != app.Service || currentArtifact.Kind != artifactKindForService(currentService.ArtifactType) {
			return nil, fmt.Errorf("已审核制品不存在或摘要发生变化，请重新提交")
		}
		currentTarget, err := manager.ValidateTarget(app.Service, env.Name, v.Plan.Reason)
		if err != nil {
			return nil, err
		}
		currentJSON, _ := json.Marshal(currentTarget)
		approvedJSON, _ := json.Marshal(app.Target)
		if string(currentJSON) != string(approvedJSON) || currentTarget.runtimeEnvEncrypted != v.envSecrets[app.Service] {
			return nil, fmt.Errorf("应用环境配置或环境变量已变化，请重新生成执行单并审核")
		}
		destination, err := s.deliveryDestinationDigest(*currentTarget)
		if err != nil || destination != app.DestinationDigest {
			return nil, fmt.Errorf("主机或云资源的执行地址已变化，请重新生成执行单并审核")
		}
		// Reserve the child and update the run in ONE transaction. A competing
		// request loses the revision CAS and cannot launch a duplicate release.
		target := app.Target
		batch := target.BatchSize
		if batch < 1 {
			batch = target.MaxParallel
		}
		if batch < 1 {
			batch = 1
		}
		hosts, _ := json.Marshal(target.ExecutionTargets())
		snapshot, _ := json.Marshal(target)
		tx, err := s.db.Begin()
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		if err := lockReleaseTarget(tx, app.Service, env.Name); err != nil {
			return nil, err
		}
		result, err := tx.Exec(`INSERT INTO release_orders(service,artifact_id,version,environment,reason,strategy,status,batch_size,pause_between_batches,failure_threshold,hosts,target_snapshot,runtime_env_encrypted,next_index,total,succeeded,failed,actor,approver,started_at) VALUES(?,?,?,?,?,?,'queued',?,?,?,?,?,?,0,?,0,0,?,?,?)`, app.Service, app.Artifact.ID, app.Artifact.Version, env.Name, v.Plan.Reason, target.Strategy, batch, target.PauseBetweenBatches, target.FailureThreshold, string(hosts), string(snapshot), v.envSecrets[app.Service], len(target.ExecutionTargets()), v.Owner, v.Approver, time.Now().Unix())
		if err != nil {
			return nil, err
		}
		childID, _ := result.LastInsertId()
		step.ReleaseID, step.Status, v.Status = childID, "waiting", "running"
		execution, _ := json.Marshal(v.Steps)
		result, err = tx.Exec(`UPDATE delivery_runs SET status=?,execution=?,revision=revision+1 WHERE id=? AND revision=?`, v.Status, string(execution), id, v.Revision)
		if err != nil {
			return nil, err
		}
		if count, _ := result.RowsAffected(); count != 1 {
			return nil, ErrRunConflict
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		v.Revision++
		s.Audit(actor, "execute", "delivery-run", fmt.Sprint(id), map[string]any{"release_id": childID, "digest": v.Digest})
		launch(childID)
		return v, nil
	}
	v.Status = "success"
	if err := s.saveDeliveryRun(v); err != nil {
		return nil, err
	}
	return v, nil
}

func registerDeliveryRoutes(mux *http.ServeMux, st *Store, manager *ReleaseManager) {
	actor := func(r *http.Request) string {
		if v := strings.TrimSpace(r.Header.Get("X-User")); v != "" {
			return v
		}
		return "local"
	}
	manage := func(r *http.Request, owner string) bool {
		return actor(r) == "local" || actor(r) == owner || r.Header.Get("X-Role") == "admin"
	}
	id := func(r *http.Request) int64 { v, _ := strconv.ParseInt(r.PathValue("id"), 10, 64); return v }
	reply := func(w http.ResponseWriter, v any, err error) {
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(v)
	}
	decode := func(w http.ResponseWriter, r *http.Request, v any) error {
		return json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(v)
	}
	mux.HandleFunc("GET /api/delivery/templates", func(w http.ResponseWriter, r *http.Request) {
		out := []DeliveryTemplate{}
		for _, t := range st.DeliveryTemplates() {
			if t.Workbench == "" {
				out = append(out, t)
			}
		}
		reply(w, out, nil)
	})
	saveTemplate := func(w http.ResponseWriter, r *http.Request) {
		var v DeliveryTemplate
		if err := decode(w, r, &v); err != nil {
			reply(w, nil, err)
			return
		}
		v.ID, v.Owner = id(r), actor(r)
		if v.ID > 0 {
			old := st.DeliveryTemplate(v.ID)
			if old == nil || !manage(r, old.Owner) {
				reply(w, nil, fmt.Errorf("仅创建人或管理员可修改模板"))
				return
			}
			v.Owner = old.Owner
		}
		err := st.SaveDeliveryTemplate(&v)
		reply(w, v, err)
	}
	mux.HandleFunc("POST /api/delivery/templates", saveTemplate)
	mux.HandleFunc("PUT /api/delivery/templates/{id}", saveTemplate)
	mux.HandleFunc("POST /api/delivery/preview", func(w http.ResponseWriter, r *http.Request) {
		var req DeliveryRequest
		if err := decode(w, r, &req); err != nil {
			reply(w, nil, err)
			return
		}
		p, _, err := st.ResolveDelivery(req, manager)
		if p != nil {
			p.NacosPlans = publicNacosPlans(p.NacosPlans)
		}
		reply(w, p, err)
	})
	mux.HandleFunc("GET /api/delivery/runs", func(w http.ResponseWriter, r *http.Request) { reply(w, st.DeliveryRuns(), nil) })
	mux.HandleFunc("POST /api/delivery/runs", func(w http.ResponseWriter, r *http.Request) {
		var req DeliveryRequest
		if err := decode(w, r, &req); err != nil {
			reply(w, nil, err)
			return
		}
		v, err := st.CreateDeliveryRun(req, actor(r), manager)
		reply(w, v, err)
	})
	mux.HandleFunc("GET /api/delivery/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		v := st.DeliveryRun(id(r))
		if v == nil {
			reply(w, nil, fmt.Errorf("编排执行单不存在"))
			return
		}
		reply(w, v, nil)
	})
	for _, action := range []string{"approve", "reject", "advance", "cancel"} {
		action := action
		mux.HandleFunc("POST /api/delivery/runs/{id}/"+action, func(w http.ResponseWriter, r *http.Request) {
			v := st.DeliveryRun(id(r))
			if v == nil {
				reply(w, nil, fmt.Errorf("编排执行单不存在"))
				return
			}
			var req struct {
				Digest        string `json:"digest"`
				ConfirmManual bool   `json:"confirm_manual"`
			}
			if err := decode(w, r, &req); err != nil {
				reply(w, nil, err)
				return
			}
			var err error
			if action == "approve" || action == "reject" {
				v, err = st.ReviewDeliveryRun(v.ID, actor(r), req.Digest, action == "approve")
			} else {
				if !manage(r, v.Owner) {
					reply(w, nil, fmt.Errorf("仅发起人或管理员可执行或取消"))
					return
				}
				if action == "advance" {
					v, err = st.AdvanceDeliveryRun(v.ID, actor(r), req.ConfirmManual, manager)
				} else {
					if v.Status != "pending_approval" && v.Status != "approved" {
						reply(w, nil, fmt.Errorf("仅未执行的编排可取消；运行中的子发布单请单独停止"))
						return
					}
					v.Status = "canceled"
					err = st.saveDeliveryRun(v)
				}
			}
			reply(w, v, err)
		})
	}
}
