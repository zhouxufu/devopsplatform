package deploy

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"logplatform/pkg/deploytask"
	"path"
	"sort"
	"strings"
	"time"
)

func deploymentAgentSchema(doc string) []string {
	return []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS deployment_agent_enrollments (host_name VARCHAR(191) PRIMARY KEY,operation_id VARCHAR(96) NOT NULL UNIQUE,phase VARCHAR(32) NOT NULL,plan %s NOT NULL,credential %s NOT NULL,created_at BIGINT NOT NULL,updated_at BIGINT NOT NULL)`, doc, doc),
		`CREATE TABLE IF NOT EXISTS deployment_agents (agent_id VARCHAR(96) PRIMARY KEY,host_name VARCHAR(191) NOT NULL,environment VARCHAR(191) NOT NULL,services TEXT NOT NULL,token_hash VARCHAR(64) NOT NULL,enabled BIGINT NOT NULL,last_seen BIGINT NOT NULL)`,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS agent_deployment_tasks (deploy_id BIGINT PRIMARY KEY,task_id VARCHAR(96) NOT NULL UNIQUE,agent_id VARCHAR(96) NOT NULL,service VARCHAR(191) NOT NULL,environment VARCHAR(191) NOT NULL,host_name VARCHAR(191) NOT NULL,root_path TEXT NOT NULL,plan_digest VARCHAR(80) NOT NULL,plan_cipher %s NOT NULL,status VARCHAR(32) NOT NULL,last_sequence BIGINT NOT NULL,updated_at BIGINT NOT NULL)`, doc),
		`CREATE TABLE IF NOT EXISTS agent_deployment_reports (task_id VARCHAR(96) NOT NULL,seq_no BIGINT NOT NULL,digest VARCHAR(80) NOT NULL,PRIMARY KEY(task_id,seq_no))`,
	}
}

type deploymentAgentRegistration struct {
	AgentID     string   `json:"agent_id"`
	Host        string   `json:"host"`
	Environment string   `json:"environment"`
	Services    []string `json:"services"`
	Enabled     bool     `json:"enabled"`
	LastSeen    int64    `json:"last_seen"`
}

func (s *Store) registerDeploymentAgent(v deploymentAgentRegistration) (string, error) {
	if !deploytask.IDPattern.MatchString(v.AgentID) || len(v.Services) == 0 || len(v.Services) > 100 {
		return "", fmt.Errorf("请指定 Agent 安装身份与允许发布的应用")
	}
	if setup := s.enrollment(v.Host); setup != nil && setup.Phase != "disabled" {
		return "", fmt.Errorf("Agent 接入尚未核对，不能手工替换授权")
	}
	h := s.GetHost(v.Host)
	if h == nil || h.Environment != v.Environment {
		return "", fmt.Errorf("主机与环境不匹配")
	}
	for _, name := range v.Services {
		if s.GetService(name) == nil {
			return "", fmt.Errorf("应用不存在")
		}
	}
	sort.Strings(v.Services)
	var oldHost, oldEnv string
	e := s.db.QueryRow(`SELECT host_name,environment FROM deployment_agents WHERE agent_id=?`, v.AgentID).Scan(&oldHost, &oldEnv)
	if e != nil && e != sql.ErrNoRows {
		return "", e
	}
	if e == nil && (oldHost != v.Host || oldEnv != v.Environment) {
		return "", fmt.Errorf("Agent 身份不能转移到其他主机或环境")
	}
	if e == nil {
		old := s.deploymentAgent(v.AgentID)
		if old == nil {
			return "", fmt.Errorf("原 Agent 授权数据不可核验")
		}
		var active int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_deployment_tasks WHERE agent_id=? AND status NOT IN ('success','failed','rolled-back','rollback-failed')`, v.AgentID).Scan(&active); err != nil {
			return "", err
		}
		a, _ := json.Marshal(v.Services)
		b, _ := json.Marshal(old.Services)
		if active > 0 && string(a) != string(b) {
			return "", fmt.Errorf("存在执行中/待核对任务，不能改变 Agent 的应用授权")
		}
	}
	var key [32]byte
	if _, e = rand.Read(key[:]); e != nil {
		return "", e
	}
	token := "dpa_" + hex.EncodeToString(key[:])
	hash := sha256.Sum256([]byte(token))
	scopes, _ := json.Marshal(v.Services)
	_, e = s.db.Exec(`INSERT INTO deployment_agents(agent_id,host_name,environment,services,token_hash,enabled,last_seen) VALUES(?,?,?,?,?,1,0) ON CONFLICT(agent_id) DO UPDATE SET services=excluded.services,token_hash=excluded.token_hash,enabled=1`, v.AgentID, v.Host, v.Environment, string(scopes), hex.EncodeToString(hash[:]))
	return token, e
}

func (s *Store) deploymentAgent(id string) *deploymentAgentRegistration {
	var v deploymentAgentRegistration
	var services string
	var enabled int
	if s.db.QueryRow(`SELECT agent_id,host_name,environment,services,enabled,last_seen FROM deployment_agents WHERE agent_id=?`, id).Scan(&v.AgentID, &v.Host, &v.Environment, &services, &enabled, &v.LastSeen) != nil {
		return nil
	}
	if json.Unmarshal([]byte(services), &v.Services) != nil {
		return nil
	}
	v.Enabled = enabled == 1
	return &v
}
func (s *Store) authorizeDeploymentAgent(id, token string) bool {
	var want string
	var enabled int
	if token == "" || id == "" || s.db.QueryRow(`SELECT token_hash,enabled FROM deployment_agents WHERE agent_id=?`, id).Scan(&want, &enabled) != nil || enabled != 1 {
		return false
	}
	h := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare([]byte(want), []byte(hex.EncodeToString(h[:]))) == 1
}
func (s *Store) validateDeploymentAgent(sv *Service, h Host) error {
	if !managedJava(sv) {
		return fmt.Errorf("部署 Agent 仅支持托管 Java JAR 或 Java 整包")
	}
	action := healthFailureAction(sv)
	if action != healthFailureKeep && action != healthFailureRollback && action != healthFailureStop {
		return fmt.Errorf("不支持的发布失败策略")
	}
	env := s.GetEnvironment(sv.RuntimeEnvironment)
	if env == nil {
		return fmt.Errorf("部署环境不存在")
	}
	if env.Protected && action != healthFailureRollback {
		return fmt.Errorf("受保护环境必须保留自动回滚策略")
	}
	if setup := s.enrollment(h.Name); setup != nil && enrollmentBusy(setup.Phase) {
		return fmt.Errorf("此主机的部署接入或令牌轮换尚未核对")
	}
	id, e := deploymentAgentID(sv.DeployConfig, h.Name)
	if e != nil {
		return e
	}
	a := s.deploymentAgent(id)
	if a == nil || !a.Enabled || a.Host != h.Name || a.Environment != sv.RuntimeEnvironment {
		return fmt.Errorf("部署 Agent 未登记、已禁用或主机/环境不匹配")
	}
	for _, name := range a.Services {
		if name == sv.Name {
			return nil
		}
	}
	return fmt.Errorf("部署 Agent 未授权此应用")
}

func (s *Store) enqueueAgentTask(t deploytask.Task) error {
	if e := t.Validate(); e != nil {
		return e
	}
	if t.Version < 2 || t.ReleaseID <= 0 {
		return fmt.Errorf("部署任务必须绑定冻结发布单")
	}
	environment := s.GetEnvironment(t.Environment)
	if environment == nil {
		return fmt.Errorf("部署环境不存在")
	}
	b, _ := json.Marshal(t)
	cipher, e := s.encryptSecret(string(b))
	if e != nil {
		return e
	}
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	q := `SELECT service,environment,artifact_id,status,target_snapshot,approver,actor FROM release_orders WHERE id=?`
	if tx.dialect == "mysql" {
		q += " FOR UPDATE"
	}
	var service, env, status, snapshot, approver, actor string
	var artifactID int64
	if e = tx.QueryRow(q, t.ReleaseID).Scan(&service, &env, &artifactID, &status, &snapshot, &approver, &actor); e != nil {
		return e
	}
	var target ServiceEnvironment
	if json.Unmarshal([]byte(snapshot), &target) != nil {
		return fmt.Errorf("发布目标快照不可核验")
	}
	var parent, deployArtifact int64
	var host, digest, deployStatus string
	if e = tx.QueryRow(`SELECT rd.release_id,d.artifact_id,d.host,d.artifact_digest,d.status FROM release_order_deploys rd JOIN deploys d ON d.id=rd.deploy_id WHERE d.id=?`, t.DeployID).Scan(&parent, &deployArtifact, &host, &digest, &deployStatus); e != nil {
		return e
	}
	assigned, e := deploymentAgentID(target.DeployConfig, host)
	if e != nil {
		return e
	}
	if parent != t.ReleaseID || deployArtifact != artifactID || digest != t.ArtifactDigest || service != t.Service || env != t.Environment || host != t.Host || assigned != t.AgentID || path.Clean(target.RemoteDir) != t.Root {
		return fmt.Errorf("任务与冻结发布目标不匹配")
	}
	if (environment.Protected || target.Protected) && (!target.Protected || !allowedApprover(target.Approvers, approver) || approver == actor || t.Rollback == nil) {
		return fmt.Errorf("受保护环境缺少有效审批或冻结回滚策略")
	}
	if (healthFailureAction(&Service{DeployConfig: target.DeployConfig}) == healthFailureRollback) != (t.Rollback != nil) {
		return fmt.Errorf("任务不能覆盖已审定的失败策略")
	}
	var enrollmentPhase string
	e = tx.QueryRow(enrollmentLockQuery(tx), t.Host).Scan(&enrollmentPhase)
	if e != nil && e != sql.ErrNoRows {
		return e
	}
	if e == nil && (enrollmentBusy(enrollmentPhase) || enrollmentPhase == "disabled") {
		return fmt.Errorf("部署 Agent 接入或令牌轮换尚未核对")
	}
	var oldDigest string
	e = tx.QueryRow(`SELECT plan_digest FROM agent_deployment_tasks WHERE deploy_id=?`, t.DeployID).Scan(&oldDigest)
	if e == nil {
		if oldDigest != deploytask.Digest(t) {
			return fmt.Errorf("同一部署不能替换任务内容")
		}
		return tx.Commit()
	}
	if e != sql.ErrNoRows {
		return e
	}
	if status != "running" || deployStatus != "running" {
		return fmt.Errorf("发布不处于可派发状态")
	}
	var active int
	if e = tx.QueryRow(`SELECT COUNT(*) FROM agent_deployment_tasks t JOIN release_order_deploys rd ON rd.deploy_id=t.deploy_id WHERE rd.release_id=? AND t.status NOT IN ('success','failed','rolled-back','rollback-failed')`, t.ReleaseID).Scan(&active); e != nil {
		return e
	}
	parallel := target.MaxParallel
	if parallel < 1 {
		parallel = 1
	}
	if active >= parallel {
		return errAgentCapacity
	}
	if _, e = tx.Exec(`INSERT INTO agent_deployment_tasks(deploy_id,task_id,agent_id,service,environment,host_name,root_path,plan_digest,plan_cipher,status,last_sequence,updated_at) VALUES(?,?,?,?,?,?,?,?,?,'queued',0,?)`, t.DeployID, t.ID, t.AgentID, t.Service, t.Environment, t.Host, t.Root, deploytask.Digest(t), cipher, time.Now().Unix()); e != nil {
		return e
	}
	return tx.Commit()
}

func (s *Store) agentTask(id string) (deploytask.Task, error) {
	var cipher string
	if e := s.db.QueryRow(`SELECT plan_cipher FROM agent_deployment_tasks WHERE task_id=?`, id).Scan(&cipher); e != nil {
		return deploytask.Task{}, e
	}
	raw, e := s.decryptSecret(cipher)
	if e != nil {
		return deploytask.Task{}, e
	}
	var t deploytask.Task
	e = json.Unmarshal([]byte(raw), &t)
	return t, e
}
func (s *Store) pendingAgentTasks(id string) ([]deploytask.Task, error) {
	rows, e := s.db.Query(`SELECT task_id FROM agent_deployment_tasks WHERE agent_id=? AND status NOT IN ('success','failed','rolled-back','rollback-failed') ORDER BY deploy_id LIMIT 16`, id)
	if e != nil {
		return nil, e
	}
	ids := []string{}
	for rows.Next() {
		var v string
		if e = rows.Scan(&v); e != nil {
			rows.Close()
			return nil, e
		}
		ids = append(ids, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	out := []deploytask.Task{}
	for _, v := range ids {
		t, e := s.agentTask(v)
		if e != nil {
			return nil, e
		}
		out = append(out, t)
	}
	return out, nil
}

// Reports and their exact ACK are committed in the same transaction. Unknown
// outcomes retain the deployment reservation; only machine facts can finish it.
func (s *Store) acceptAgentReport(agent string, r deploytask.Report) (bool, error) {
	if r.Sequence < 1 || len(r.Message) > 4096 || len(r.Phase) > 128 || r.TaskID == "" {
		return false, fmt.Errorf("invalid deployment report")
	}
	switch r.State {
	case "accepted", "executing", "starting", "needs_review", "success", "failed", "rolling_back", "rolled-back", "rollback-failed":
	default:
		return false, fmt.Errorf("invalid deployment state")
	}
	if r.State == "rolling_back" || r.State == "rolled-back" || r.State == "rollback-failed" {
		task, e := s.agentTask(r.TaskID)
		if e != nil || task.Rollback == nil {
			return false, fmt.Errorf("任务没有审定的自动回滚策略")
		}
	}
	tx, e := s.db.Begin()
	if e != nil {
		return false, e
	}
	defer tx.Rollback()
	var assigned, digest, state string
	var sequence, deployID int64
	q := `SELECT agent_id,plan_digest,status,last_sequence,deploy_id FROM agent_deployment_tasks WHERE task_id=?`
	if tx.dialect == "mysql" {
		q += " FOR UPDATE"
	}
	if e = tx.QueryRow(q, r.TaskID).Scan(&assigned, &digest, &state, &sequence, &deployID); e != nil {
		return false, e
	}
	if assigned != agent || digest != r.TaskDigest {
		return false, fmt.Errorf("deployment report scope changed")
	}
	hash := deploytask.Digest(r)
	if r.Sequence <= sequence {
		var old string
		if e = tx.QueryRow(`SELECT digest FROM agent_deployment_reports WHERE task_id=? AND seq_no=?`, r.TaskID, r.Sequence).Scan(&old); e != nil || old != hash {
			return false, fmt.Errorf("conflicting deployment report")
		}
		return false, tx.Commit()
	}
	if r.Sequence != sequence+1 || deploytask.Terminal(state) {
		return false, fmt.Errorf("deployment report out of order or already terminal")
	}
	if (r.State == "success" || r.State == "rolled-back") && !strings.HasPrefix(r.Phase, "verified") {
		return false, fmt.Errorf("readiness evidence is missing")
	}
	if _, e = tx.Exec(`INSERT INTO agent_deployment_reports(task_id,seq_no,digest) VALUES(?,?,?)`, r.TaskID, r.Sequence, hash); e != nil {
		return false, e
	}
	if _, e = tx.Exec(`UPDATE agent_deployment_tasks SET status=?,last_sequence=?,updated_at=? WHERE task_id=? AND last_sequence=?`, r.State, r.Sequence, time.Now().Unix(), r.TaskID, sequence); e != nil {
		return false, e
	}
	status := "running"
	if r.State == "needs_review" {
		status = "needs_review"
	}
	ended := int64(0)
	if deploytask.Terminal(r.State) {
		status = r.State
		ended = time.Now().Unix()
	}
	var current string
	if e = tx.QueryRow(`SELECT status FROM deploys WHERE id=?`, deployID).Scan(&current); e != nil {
		return false, e
	}
	if current == "needs_review" && !deploytask.Terminal(r.State) {
		status = "needs_review"
	}
	// Sequence is also persisted in the log so MySQL's changed-row semantics
	// cannot turn an otherwise valid running -> running report into a conflict.
	line := redactDeploymentText(fmt.Sprintf("Agent #%d · %s · %s", r.Sequence, r.State, r.Phase))
	res, e := tx.Exec(`UPDATE deploys SET status=?,ended_at=?,log=substr(COALESCE(log,'') || char(10) || ?, -20000) WHERE id=? AND status IN ('running','needs_review')`, status, ended, line, deployID)
	if e != nil {
		return false, e
	}
	n, e := res.RowsAffected()
	if e != nil || n != 1 {
		return false, fmt.Errorf("deployment execution no longer owns this result")
	}
	return true, tx.Commit()
}

func deploymentAgentBusy(tx *deployTx, service, env, host string) error {
	var n int
	e := tx.QueryRow(`SELECT COUNT(*) FROM agent_deployment_tasks WHERE service=? AND (environment=? OR host_name=?) AND status NOT IN ('success','failed','rolled-back','rollback-failed')`, service, env, host).Scan(&n)
	if e != nil {
		return e
	}
	if n > 0 {
		return fmt.Errorf("%w: Agent 部署仍在执行或待核对，不会重复发布", ErrRunConflict)
	}
	return nil
}
