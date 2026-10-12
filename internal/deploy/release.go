package deploy

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type ReleaseOrder struct {
	ID                  int64              `json:"id"`
	Service             string             `json:"service"`
	ArtifactID          int64              `json:"artifact_id"`
	Version             string             `json:"version"`
	Environment         string             `json:"environment"`
	Reason              string             `json:"reason,omitempty"`
	Strategy            string             `json:"strategy"`
	Status              string             `json:"status"`
	BatchSize           int                `json:"batch_size"`
	PauseBetweenBatches bool               `json:"pause_between_batches"`
	FailureThreshold    int                `json:"failure_threshold"`
	Hosts               []string           `json:"hosts"`
	Target              ServiceEnvironment `json:"target_snapshot"`
	runtimeEnvEncrypted string
	NextIndex           int     `json:"next_index"`
	Total               int     `json:"total"`
	Succeeded           int     `json:"succeeded"`
	Failed              int     `json:"failed"`
	Actor               string  `json:"actor"`
	Approver            string  `json:"approver,omitempty"`
	DeployIDs           []int64 `json:"deploy_ids,omitempty"`
	StartedAt           int64   `json:"started_at"`
	EndedAt             int64   `json:"ended_at,omitempty"`
	ContinueAfter       int64   `json:"continue_after,omitempty"`
	Recovery            bool    `json:"recovery,omitempty"`
}

type ReleaseManager struct {
	st *Store
	dp *Deployer
}

func NewReleaseManager(st *Store, dp *Deployer) *ReleaseManager {
	return &ReleaseManager{st: st, dp: dp}
}

func (s *Store) CreateReleaseOrder(o *ReleaseOrder) error {
	hosts, _ := json.Marshal(o.Hosts)
	target, _ := json.Marshal(o.Target)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if releaseStatusActive(o.Status) {
		if err := lockReleaseTarget(tx, o.Service, o.Environment); err != nil {
			return err
		}
	}
	res, err := tx.Exec(`INSERT INTO release_orders(service,artifact_id,version,environment,reason,strategy,status,batch_size,pause_between_batches,failure_threshold,hosts,target_snapshot,runtime_env_encrypted,next_index,total,succeeded,failed,actor,started_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0,0,?,?)`, o.Service, o.ArtifactID, o.Version, o.Environment, o.Reason, o.Strategy, o.Status, o.BatchSize, o.PauseBetweenBatches, o.FailureThreshold, string(hosts), string(target), o.runtimeEnvEncrypted, o.NextIndex, len(o.Hosts), o.Actor, o.StartedAt)
	if err != nil {
		return err
	}
	o.ID, _ = res.LastInsertId()
	o.Total = len(o.Hosts)
	return tx.Commit()
}

// All release creation paths share this reservation, including orchestration.
// MySQL row locking serializes competing API instances; SQLite's store owns a
// single connection and serializes these transactions locally.
func lockReleaseTarget(tx *deployTx, service, environment string) error {
	query := `SELECT service FROM service_environments WHERE service=? AND environment=?`
	if tx.dialect == "mysql" {
		query += ` FOR UPDATE`
	}
	var name string
	if err := tx.QueryRow(query, service, environment).Scan(&name); err != nil {
		return fmt.Errorf("应用环境不存在或已被移除")
	}
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM release_orders WHERE service=? AND environment=? AND status IN ('pending_approval','queued','running','waiting_next_batch','rolling_back')`, service, environment).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("%w: 该应用环境已有进行中的发布单", ErrRunConflict)
	}
	return nil
}
func scanRelease(scanner interface{ Scan(...any) error }) (ReleaseOrder, error) {
	var o ReleaseOrder
	var hosts, target string
	var ended sql.NullInt64
	err := scanner.Scan(&o.ID, &o.Service, &o.ArtifactID, &o.Version, &o.Environment, &o.Reason, &o.Strategy, &o.Status, &o.BatchSize, &o.PauseBetweenBatches, &o.FailureThreshold, &hosts, &target, &o.runtimeEnvEncrypted, &o.NextIndex, &o.Total, &o.Succeeded, &o.Failed, &o.Actor, &o.Approver, &o.StartedAt, &ended)
	if err != nil {
		return o, err
	}
	_ = json.Unmarshal([]byte(hosts), &o.Hosts)
	_ = json.Unmarshal([]byte(target), &o.Target)
	o.EndedAt = ended.Int64
	return o, nil
}

const releaseSelect = `SELECT id,service,artifact_id,version,environment,reason,strategy,status,batch_size,pause_between_batches,failure_threshold,hosts,COALESCE(target_snapshot,'{}'),COALESCE(runtime_env_encrypted,''),next_index,total,succeeded,failed,actor,approver,started_at,ended_at FROM release_orders`

func (s *Store) GetReleaseOrder(id int64) *ReleaseOrder {
	o, err := scanRelease(s.db.QueryRow(releaseSelect+` WHERE id=?`, id))
	if err != nil {
		return nil
	}
	o.DeployIDs = s.ReleaseDeployIDs(id)
	o.Recovery = s.saeReleaseRecovery(&o)
	o.ContinueAfter = s.releaseContinueAfter(&o)
	return &o
}
func (s *Store) ReleaseOrders(service string, limit int) []ReleaseOrder {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	q := releaseSelect
	args := []any{}
	if service != "" {
		q += ` WHERE service=?`
		args = append(args, service)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return []ReleaseOrder{}
	}
	out := []ReleaseOrder{}
	for rows.Next() {
		if o, e := scanRelease(rows); e == nil {
			out = append(out, o)
		}
	}
	rows.Close()
	for i := range out {
		out[i].DeployIDs = s.ReleaseDeployIDs(out[i].ID)
		out[i].Recovery = s.saeReleaseRecovery(&out[i])
		out[i].ContinueAfter = s.releaseContinueAfter(&out[i])
	}
	return out
}

func releaseStatusActive(status string) bool {
	return status == "pending_approval" || status == "queued" || status == "running" || status == "waiting_next_batch" || status == "rolling_back"
}

func (s *Store) ActiveReleaseForServiceEnvironment(service, environment string) *ReleaseOrder {
	for _, order := range s.ReleaseOrders(service, 500) {
		if releaseStatusActive(order.Status) && (environment == "" || order.Environment == environment) {
			copy := order
			return &copy
		}
	}
	return nil
}

func (s *Store) ActiveReleaseForHost(host string) *ReleaseOrder {
	rows, err := s.db.Query(releaseSelect + ` WHERE status IN ('pending_approval','queued','running','waiting_next_batch','rolling_back') ORDER BY id DESC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		order, err := scanRelease(rows)
		if err != nil {
			continue
		}
		for _, name := range order.Hosts {
			if name == host {
				return &order
			}
		}
	}
	return nil
}
func (s *Store) ReleaseDeployIDs(id int64) []int64 {
	rows, err := s.db.Query(`SELECT deploy_id FROM release_order_deploys WHERE release_id=? ORDER BY batch_no,deploy_id`, id)
	if err != nil {
		return []int64{}
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var v int64
		if rows.Scan(&v) == nil {
			out = append(out, v)
		}
	}
	return out
}
func (s *Store) AttachReleaseDeploys(id int64, batch int, ids []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, d := range ids {
		if _, err = tx.Exec(`INSERT OR IGNORE INTO release_order_deploys(release_id,deploy_id,batch_no) VALUES(?,?,?)`, id, d, batch); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) SetReleaseStatus(id int64, status, approver string, ended bool) {
	if ended {
		_, _ = s.db.Exec(`UPDATE release_orders SET status=?,approver=CASE WHEN ?='' THEN approver ELSE ? END,ended_at=strftime('%s','now') WHERE id=?`, status, approver, approver, id)
	} else {
		_, _ = s.db.Exec(`UPDATE release_orders SET status=?,approver=CASE WHEN ?='' THEN approver ELSE ? END,ended_at=NULL WHERE id=?`, status, approver, approver, id)
	}
}

func (s *Store) compareReleaseStatus(id int64, from, to, approver string, ended bool) error {
	var end any
	if ended {
		end = time.Now().Unix()
	}
	result, err := s.db.Exec(`UPDATE release_orders SET status=?,approver=CASE WHEN ?<>'' THEN ? ELSE approver END,ended_at=? WHERE id=? AND status=?`, to, approver, approver, end, id, from)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return ErrRunConflict
	}
	return nil
}
func (s *Store) SetReleaseProgress(id int64, next int) {
	_, _ = s.db.Exec(`UPDATE release_orders SET next_index=? WHERE id=?`, next, id)
}
func (s *Store) RefreshReleaseCounts(id int64) (int, int) {
	var ok, failed int
	_ = s.db.QueryRow(`SELECT COALESCE(SUM(CASE WHEN d.status='success' THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN d.status NOT IN ('success','running','waiting_next_batch') THEN 1 ELSE 0 END),0) FROM release_order_deploys rd JOIN deploys d ON d.id=rd.deploy_id WHERE rd.release_id=?`, id).Scan(&ok, &failed)
	_, _ = s.db.Exec(`UPDATE release_orders SET succeeded=?,failed=? WHERE id=?`, ok, failed, id)
	return ok, failed
}

// backfillReleaseOrders 将升级前按主机记录的部署历史收拢为发布单。
// 同一秒、同一应用/版本/环境/操作者发起的记录视为同一次批量发布。
func (s *Store) backfillReleaseOrders() error {
	rows, err := s.db.Query(`SELECT d.id,d.service,d.version,d.host,d.environment,d.artifact_id,d.reason,d.status,d.actor,d.started_at,d.ended_at
		FROM deploys d LEFT JOIN release_order_deploys rd ON rd.deploy_id=d.id
		WHERE rd.deploy_id IS NULL ORDER BY d.id`)
	if err != nil {
		return err
	}
	type legacyDeploy struct {
		id, artifactID, startedAt, endedAt                         int64
		service, version, host, environment, reason, status, actor string
	}
	items := []legacyDeploy{}
	for rows.Next() {
		var d legacyDeploy
		var ended sql.NullInt64
		if err := rows.Scan(&d.id, &d.service, &d.version, &d.host, &d.environment, &d.artifactID, &d.reason, &d.status, &d.actor, &d.startedAt, &ended); err != nil {
			rows.Close()
			return err
		}
		d.endedAt = ended.Int64
		items = append(items, d)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	type group struct {
		key     string
		deploys []legacyDeploy
	}
	groups, positions := []group{}, map[string]int{}
	for _, d := range items {
		key := fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%s\x00%s", d.service, d.version, d.environment, d.startedAt, d.actor, d.reason)
		idx, ok := positions[key]
		if !ok {
			idx = len(groups)
			positions[key] = idx
			groups = append(groups, group{key: key})
		}
		groups[idx].deploys = append(groups[idx].deploys, d)
	}
	for _, g := range groups {
		first := g.deploys[0]
		hosts, ids := make([]string, 0, len(g.deploys)), make([]int64, 0, len(g.deploys))
		succeeded, failed, endedAt := 0, 0, int64(0)
		allRolledBack, allCanceled := true, true
		for _, d := range g.deploys {
			hosts, ids = append(hosts, d.host), append(ids, d.id)
			if d.status == "success" {
				succeeded++
			} else {
				failed++
			}
			allRolledBack = allRolledBack && d.status == "rolled-back"
			allCanceled = allCanceled && d.status == "canceled"
			if d.endedAt > endedAt {
				endedAt = d.endedAt
			}
		}
		status := "failed"
		if succeeded == len(g.deploys) {
			status = "success"
		} else if allRolledBack {
			status = "rolled-back"
		} else if allCanceled {
			status = "canceled"
		}
		o := &ReleaseOrder{Service: first.service, ArtifactID: first.artifactID, Version: first.version, Environment: first.environment, Reason: first.reason, Strategy: "legacy", Status: status, BatchSize: len(hosts), Hosts: hosts, NextIndex: len(hosts), Actor: first.actor, StartedAt: first.startedAt}
		if err := s.CreateReleaseOrder(o); err != nil {
			return err
		}
		if err := s.AttachReleaseDeploys(o.ID, 1, ids); err != nil {
			return err
		}
		_, err = s.db.Exec(`UPDATE release_orders SET succeeded=?,failed=?,ended_at=? WHERE id=?`, succeeded, failed, endedAt, o.ID)
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *ReleaseManager) Create(service, version, environment, reason, actor string) (*ReleaseOrder, error) {
	sv := m.st.GetService(service)
	var candidates []Artifact
	fingerprints := map[string]struct{}{}
	for _, artifact := range m.st.Artifacts(service, 500) {
		if artifact.Version == version {
			candidates = append(candidates, artifact)
			fingerprint := artifact.Digest
			if fingerprint == "" {
				fingerprint = artifact.URI
			}
			fingerprints[fingerprint] = struct{}{}
		}
	}
	if len(fingerprints) > 1 {
		return nil, fmt.Errorf("版本 %s 对应多份不同制品，请改用 artifact_id 指定", version)
	}
	if len(candidates) > 0 {
		return m.createArtifact(service, &candidates[0], environment, reason, actor)
	}
	build := m.st.GetBuildByVersion(service, version)
	if sv == nil || build == nil {
		return nil, fmt.Errorf("应用、制品版本或环境不存在")
	}
	artifact := m.st.ArtifactForBuild(build, sv)
	if artifact == nil {
		return nil, fmt.Errorf("制品不可用")
	}
	return m.createArtifact(service, artifact, environment, reason, actor)
}

// CreateArtifact creates a release from one immutable artifact identity. A
// version is only a human-readable label and is deliberately not used as the
// selector because multiple builds may legitimately produce the same version.
func (m *ReleaseManager) CreateArtifact(service string, artifactID int64, environment, reason, actor string) (*ReleaseOrder, error) {
	artifact := m.st.GetArtifact(artifactID)
	sv := m.st.GetService(service)
	if artifact == nil || sv == nil || artifact.Service != service {
		return nil, fmt.Errorf("应用或制品不存在")
	}
	if artifact.Kind != artifactKindForService(sv.ArtifactType) {
		return nil, fmt.Errorf("制品类型与应用交付类型不匹配")
	}
	return m.createArtifact(service, artifact, environment, reason, actor)
}

// ValidateTarget checks whether a release can be submitted to an application
// environment. It is also used before a one-click build starts so an expensive
// build is not launched for an environment that cannot accept its artifact.
func (m *ReleaseManager) ValidateTarget(service, environment, reason string) (*ServiceEnvironment, error) {
	target := m.st.GetServiceEnvironment(service, environment)
	if target == nil {
		return nil, fmt.Errorf("应用环境不存在")
	}
	if !target.ManagedTarget() && target.HostGroup == "" {
		return nil, fmt.Errorf("应用环境未绑定主机组，不能创建发布单")
	}
	if len(target.ExecutionTargets()) == 0 {
		return nil, fmt.Errorf("环境没有可用执行目标")
	}
	if active := m.st.ActiveReleaseForServiceEnvironment(service, environment); active != nil {
		return nil, fmt.Errorf("%w: 该应用环境已有进行中的发布单 #%d", ErrRunConflict, active.ID)
	}
	if target.RequireReason && strings.TrimSpace(reason) == "" {
		return nil, fmt.Errorf("该环境要求填写发布原因")
	}
	if target.Protected && strings.TrimSpace(target.Approvers) == "" {
		return nil, fmt.Errorf("受保护环境未配置审批人，不能创建发布单")
	}
	if err := validateReleasePolicy(*target, len(target.Hosts)); err != nil {
		return nil, err
	}
	return target, nil
}

func (m *ReleaseManager) createArtifact(service string, artifact *Artifact, environment, reason, actor string) (*ReleaseOrder, error) {
	target, err := m.ValidateTarget(service, environment, reason)
	if err != nil {
		return nil, err
	}
	targets := target.ExecutionTargets()
	status := "queued"
	if target.Protected {
		status = "pending_approval"
	}
	batch := target.BatchSize
	if batch < 1 {
		batch = target.MaxParallel
	}
	if batch < 1 {
		batch = 1
	}
	targetSnapshot := *target
	targetSnapshot.Hosts = append([]string{}, target.Hosts...)
	targetSnapshot.DeployConfig = map[string]string{}
	for key, value := range target.DeployConfig {
		targetSnapshot.DeployConfig[key] = value
	}
	o := &ReleaseOrder{Service: service, ArtifactID: artifact.ID, Version: artifact.Version, Environment: environment, Reason: strings.TrimSpace(reason), Strategy: target.Strategy, Status: status, BatchSize: batch, PauseBetweenBatches: target.PauseBetweenBatches, FailureThreshold: target.FailureThreshold, Hosts: targets, Target: targetSnapshot, runtimeEnvEncrypted: target.runtimeEnvEncrypted, Actor: actor, StartedAt: time.Now().Unix()}
	if err := m.st.CreateReleaseOrder(o); err != nil {
		return nil, err
	}
	m.st.Audit(actor, "create", "release", fmt.Sprint(o.ID), map[string]any{"service": service, "artifact_id": artifact.ID, "version": artifact.Version, "environment": environment})
	if status == "queued" {
		go m.startNextBatch(o.ID)
	}
	return o, nil
}

func allowedApprover(approvers, actor string) bool {
	if strings.TrimSpace(approvers) == "" {
		return false
	}
	for _, v := range strings.Split(approvers, ",") {
		if strings.TrimSpace(v) == actor {
			return true
		}
	}
	return false
}
func (m *ReleaseManager) Approve(id int64, actor string) error {
	o := m.st.GetReleaseOrder(id)
	if o == nil || o.Status != "pending_approval" {
		return fmt.Errorf("发布单不在待审批状态")
	}
	approvers := o.Target.Approvers
	if o.Target.Environment == "" {
		if env := m.st.GetEnvironment(o.Environment); env != nil {
			approvers = env.Approvers
		}
	}
	if !allowedApprover(approvers, actor) {
		return fmt.Errorf("当前用户不在环境审批人列表")
	}
	if strings.TrimSpace(actor) == strings.TrimSpace(o.Actor) {
		return fmt.Errorf("发布发起人不能审批自己的发布单")
	}
	if err := m.st.compareReleaseStatus(id, "pending_approval", "queued", actor, false); err != nil {
		return err
	}
	m.st.Audit(actor, "approve", "release", fmt.Sprint(id), nil)
	go m.startNextBatch(id)
	return nil
}
func (m *ReleaseManager) Reject(id int64, actor string) error {
	o := m.st.GetReleaseOrder(id)
	if o == nil || o.Status != "pending_approval" {
		return fmt.Errorf("发布单不在待审批状态")
	}
	approvers := o.Target.Approvers
	if o.Target.Environment == "" {
		if env := m.st.GetEnvironment(o.Environment); env != nil {
			approvers = env.Approvers
		}
	}
	if !allowedApprover(approvers, actor) {
		return fmt.Errorf("当前用户不在环境审批人列表")
	}
	if strings.TrimSpace(actor) == strings.TrimSpace(o.Actor) {
		return fmt.Errorf("发布发起人不能审批自己的发布单")
	}
	if err := m.st.compareReleaseStatus(id, "pending_approval", "rejected", actor, true); err != nil {
		return err
	}
	m.st.Audit(actor, "reject", "release", fmt.Sprint(id), nil)
	return nil
}
func (m *ReleaseManager) Continue(id int64, actor string) error {
	o := m.st.GetReleaseOrder(id)
	if o == nil || o.Status != "waiting_next_batch" {
		return fmt.Errorf("发布单不在批次暂停状态")
	}
	if o.ContinueAfter > time.Now().Unix() {
		return fmt.Errorf("首批观察期尚未结束，请于 %s 后继续", time.Unix(o.ContinueAfter, 0).Format(time.RFC3339))
	}
	if o.Target.ManagedTarget() {
		return m.continueSAERelease(o, actor)
	}
	if err := m.st.compareReleaseStatus(id, "waiting_next_batch", "queued", "", false); err != nil {
		return err
	}
	m.st.Audit(actor, "continue", "release", fmt.Sprint(id), nil)
	go m.startNextBatch(id)
	return nil
}
func (m *ReleaseManager) Cancel(id int64, actor string) error {
	o := m.st.GetReleaseOrder(id)
	if o != nil && o.Recovery {
		return fmt.Errorf("回退流程不能按普通发布取消；请继续回退批次或在云端核对")
	}
	if o == nil || (o.Status != "pending_approval" && o.Status != "waiting_next_batch" && o.Status != "queued") {
		return fmt.Errorf("仅待审批、排队或批次暂停的发布单可取消")
	}
	if o.Target.ManagedTarget() && o.Status == "waiting_next_batch" {
		return m.cancelSAERelease(o, actor)
	}
	if err := m.st.compareReleaseStatus(id, o.Status, "canceled", "", true); err != nil {
		return err
	}
	m.st.Audit(actor, "cancel", "release", fmt.Sprint(id), nil)
	return nil
}

func (m *ReleaseManager) startNextBatch(id int64) {
	o := m.st.GetReleaseOrder(id)
	if o == nil || o.Status != "queued" {
		return
	}
	sv := m.st.GetService(o.Service)
	target := &o.Target
	if target.Environment == "" {
		target = m.st.GetServiceEnvironment(o.Service, o.Environment)
	}
	artifact := m.st.GetArtifact(o.ArtifactID)
	if sv == nil || target == nil || artifact == nil {
		m.st.SetReleaseStatus(id, "failed", "", true)
		return
	}
	runtimeEnv, err := m.st.decryptRuntimeEnvironment(o.runtimeEnvEncrypted)
	if err != nil {
		m.st.SetReleaseStatus(id, "failed", "", true)
		m.st.Audit(o.Actor, "fail", "release", fmt.Sprint(id), map[string]any{"error": "应用环境变量解密失败"})
		return
	}
	target.RuntimeEnv = runtimeEnv
	if o.NextIndex >= len(o.Hosts) {
		m.st.SetReleaseStatus(id, "success", "", true)
		return
	}
	end := releaseBatchEnd(o)
	hosts := make([]Host, 0, end-o.NextIndex)
	if target.ManagedTarget() {
		hosts = target.ExecutionHosts()
	} else {
		for _, name := range o.Hosts[o.NextIndex:end] {
			h := m.st.GetHost(name)
			if h == nil {
				m.st.SetReleaseStatus(id, "failed", "", true)
				return
			}
			if h.Environment != o.Environment {
				m.st.SetReleaseStatus(id, "failed", "", true)
				m.st.Audit(o.Actor, "fail", "release", fmt.Sprint(id), map[string]any{"error": fmt.Sprintf("主机 %s 属于 %s 环境", h.Name, h.Environment)})
				return
			}
			hosts = append(hosts, *h)
		}
	}
	batchNo := o.NextIndex/o.BatchSize + 1
	if canaryTarget(o.Target) && o.NextIndex > 0 {
		batchNo = 2 + (o.NextIndex-policyInt(o.Target.DeployConfig, "canary_count", 1))/o.BatchSize
	}
	m.st.SetReleaseStatus(id, "running", "", false)
	ids, err := m.dp.StartArtifactReleaseBatch(target.Apply(sv), hosts, *artifact, o.Environment, o.Reason, o.Actor, target.MaxParallel)
	if err != nil {
		m.st.SetReleaseStatus(id, "failed", "", true)
		m.st.Audit(o.Actor, "fail", "release", fmt.Sprint(id), map[string]any{"error": err.Error()})
		return
	}
	_ = m.st.AttachReleaseDeploys(id, batchNo, ids)
	m.st.SetReleaseProgress(id, end)
	go m.monitorBatch(id, ids, end)
}
func (m *ReleaseManager) monitorBatch(id int64, ids []int64, end int) {
	deadline := time.Now().Add(30 * time.Minute)
	completed := false
	for time.Now().Before(deadline) {
		done := true
		for _, d := range ids {
			v := m.st.GetDeploy(d)
			if v != nil && v.Status == "waiting_next_batch" {
				m.st.SetReleaseStatus(id, "waiting_next_batch", "", false)
				return
			}
			if v == nil || v.Status == "running" {
				done = false
				break
			}
		}
		if done {
			completed = true
			break
		}
		time.Sleep(time.Second)
	}
	_, failed := m.st.RefreshReleaseCounts(id)
	o := m.st.GetReleaseOrder(id)
	if o == nil {
		return
	}
	if !completed {
		m.st.SetReleaseStatus(id, "failed", "", true)
		m.st.Audit(o.Actor, "timeout", "release", fmt.Sprint(id), map[string]any{"deploy_ids": ids})
		return
	}
	if failed > o.FailureThreshold || (canaryTarget(o.Target) && end == policyInt(o.Target.DeployConfig, "canary_count", 1) && failed > 0) {
		m.st.SetReleaseStatus(id, "failed", "", true)
		m.st.Audit(o.Actor, "fail", "release", fmt.Sprint(id), map[string]any{"failed": failed})
		return
	}
	if end >= len(o.Hosts) {
		m.st.SetReleaseStatus(id, "success", "", true)
		m.st.Audit(o.Actor, "complete", "release", fmt.Sprint(id), nil)
		return
	}
	if o.PauseBetweenBatches || (canaryTarget(o.Target) && end == policyInt(o.Target.DeployConfig, "canary_count", 1)) {
		m.st.SetReleaseStatus(id, "waiting_next_batch", "", false)
		return
	}
	m.st.SetReleaseStatus(id, "queued", "", false)
	m.startNextBatch(id)
}

func (m *ReleaseManager) Rollback(id int64, reason, actor string) error {
	o := m.st.GetReleaseOrder(id)
	if o == nil || (o.Status != "success" && o.Status != "waiting_next_batch" && o.Status != "failed" && o.Status != "canceled") {
		return fmt.Errorf("仅已发布、观察暂停或失败的发布单可以回滚")
	}
	if o.Recovery {
		return fmt.Errorf("已进入回退流程，不可重复发起回退")
	}
	env := m.st.GetEnvironment(o.Environment)
	if env != nil && env.RequireReason && strings.TrimSpace(reason) == "" {
		return fmt.Errorf("受保护环境回滚必须填写原因")
	}
	if o.Target.ManagedTarget() && o.Status == "waiting_next_batch" {
		return m.rollbackSAERelease(o, reason, actor)
	}
	sourceIDs := m.st.ReleaseDeployIDs(id)
	newIDs := []int64{}
	for _, source := range sourceIDs {
		d := m.st.GetDeploy(source)
		if d == nil || d.Status != "success" {
			continue
		}
		target := &o.Target
		if target.Environment == "" {
			target = nil
		}
		nid, err := m.dp.RollbackWithTarget(source, reason, actor, target)
		if err != nil {
			return err
		}
		newIDs = append(newIDs, nid)
	}
	if len(newIDs) == 0 {
		return fmt.Errorf("没有可回滚的成功目标")
	}
	_ = m.st.AttachReleaseDeploys(id, 999, newIDs)
	m.st.SetReleaseStatus(id, "rolling_back", "", false)
	m.st.Audit(actor, "rollback", "release", fmt.Sprint(id), map[string]any{"reason": reason})
	go func() {
		deadline := time.Now().Add(30 * time.Minute)
		ok := false
		for time.Now().Before(deadline) {
			done, success := true, true
			for _, did := range newIDs {
				d := m.st.GetDeploy(did)
				if d == nil || d.Status == "running" {
					done = false
				}
				if d != nil && d.Status != "running" && d.Status != "success" {
					success = false
				}
			}
			if done {
				ok = success
				break
			}
			time.Sleep(time.Second)
		}
		if ok {
			m.st.SetReleaseStatus(id, "rolled-back", "", true)
		} else {
			m.st.SetReleaseStatus(id, "rollback-failed", "", true)
		}
	}()
	return nil
}

func (m *ReleaseManager) Recover() {
	_, _ = m.st.db.Exec(`UPDATE release_orders SET status='interrupted',ended_at=strftime('%s','now') WHERE status IN ('queued','running','rolling_back')`)
}
