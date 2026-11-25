package deploy

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	agentcfg "logplatform/internal/agentconfig"
	"logplatform/pkg/deploytask"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"
	"time"
)

type enrollmentInput struct {
	Services []string `json:"services"`
	URL      string   `json:"url"`
	RunUser  string   `json:"run_user"`
	RunGroup string   `json:"run_group"`
}
type enrollmentRemote struct {
	AgentID   string `json:"agent_id"`
	ConfigSHA string `json:"config_sha"`
	BinarySHA string `json:"binary_sha"`
	UnitSHA   string `json:"unit_sha"`
	User      string `json:"user"`
	Group     string `json:"group"`
	Platform  string `json:"platform"`
}
type enrollmentPlan struct {
	Scope           deploytask.EnrollmentScope `json:"scope"`
	Services        []string                   `json:"services"`
	URL             string                     `json:"url"`
	RunUser         string                     `json:"run_user"`
	RunGroup        string                     `json:"run_group"`
	HostDigest      string                     `json:"host_digest"`
	Remote          enrollmentRemote           `json:"remote"`
	BinarySHA       string                     `json:"binary_sha"`
	OldRegistration string                     `json:"old_registration"`
	Actor           string                     `json:"actor"`
	Expires         int64                      `json:"expires"`
}
type enrollmentState struct {
	OperationID string         `json:"operation_id"`
	Phase       string         `json:"phase"`
	Plan        enrollmentPlan `json:"plan"`
	credential  string
}
type enrollmentTransport struct {
	exec   javaExec
	upload func(Host, []byte, string) error
	binary func(string) ([]byte, error)
}

func productionEnrollmentTransport() enrollmentTransport {
	return enrollmentTransport{SSHExec, SSHUploadContent, func(platform string) ([]byte, error) {
		file, e := findAgentBinary(platform)
		if e != nil {
			return nil, e
		}
		return os.ReadFile(file)
	}}
}
func enrollmentID() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}
func enrollmentHash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (s *Store) enrollmentHostHash(h Host) string {
	raw, _ := json.Marshal([]string{h.Name, h.Environment, h.Addr, h.User, h.HostKey, h.KeyPath, h.Password})
	mac := hmac.New(sha256.New, s.secretKey)
	mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil))
}
func (s *Store) enrollment(host string) *enrollmentState {
	v := &enrollmentState{}
	var plan string
	if s.db.QueryRow(`SELECT operation_id,phase,plan,credential FROM deployment_agent_enrollments WHERE host_name=?`, host).Scan(&v.OperationID, &v.Phase, &plan, &v.credential) != nil || json.Unmarshal([]byte(plan), &v.Plan) != nil {
		return nil
	}
	return v
}
func enrollmentBusy(phase string) bool {
	return phase == "applying" || phase == "awaiting_connection" || phase == "needs_review"
}
func (s *Store) enrollmentIdle(agent string) error {
	var n int
	if e := s.db.QueryRow(`SELECT COUNT(*) FROM agent_deployment_tasks WHERE agent_id=? AND status NOT IN ('success','failed','rolled-back','rollback-failed')`, agent).Scan(&n); e != nil {
		return e
	}
	if n > 0 {
		return fmt.Errorf("此 Agent 存在执行中或待核对任务，暂不能更改接入或令牌")
	}
	return nil
}
func (s *Store) enrollmentOptions(h Host) []map[string]string {
	out := []map[string]string{}
	for _, service := range s.Services() {
		target := s.GetServiceEnvironment(service.Name, h.Environment)
		if target == nil {
			continue
		}
		sv := target.Apply(&service)
		if !managedJava(sv) {
			continue
		}
		for _, host := range target.Hosts {
			if host == h.Name {
				out = append(out, map[string]string{"service": service.Name, "root": path.Clean(target.RemoteDir)})
				break
			}
		}
	}
	return out
}
func (s *Store) validateEnrollment(h Host, input enrollmentInput) ([]string, []string, error) {
	if h.HostKey == "" {
		return nil, nil, fmt.Errorf("请先确认资源主机的 SSH 指纹")
	}
	if len(input.Services) == 0 || len(input.Services) > 32 {
		return nil, nil, fmt.Errorf("请选择 1 到 32 个允许部署的应用")
	}
	if e := validateHTTPBaseURL("平台回连地址", input.URL, true); e != nil {
		return nil, nil, e
	}
	services := append([]string{}, input.Services...)
	sort.Strings(services)
	roots := []string{}
	seen := map[string]bool{}
	for i, name := range services {
		if i > 0 && services[i-1] == name {
			return nil, nil, fmt.Errorf("应用重复")
		}
		found := false
		for _, option := range s.enrollmentOptions(h) {
			if option["service"] == name {
				root := option["root"]
				if root == "/" || root == "." || !path.IsAbs(root) {
					return nil, nil, fmt.Errorf("应用部署目录无效")
				}
				if !seen[root] {
					roots = append(roots, root)
					seen[root] = true
				}
				found = true
			}
		}
		if !found {
			return nil, nil, fmt.Errorf("应用 %s 未绑定此主机或不是托管 Java 发布", name)
		}
	}
	sort.Strings(roots)
	return services, roots, nil
}

const enrollmentInspect = `set -eu
hash_file() { if [ -f "$1" ] && [ ! -L "$1" ]; then sha256sum "$1" | cut -d' ' -f1; else printf '%s' '-'; fi; }
command -v systemctl >/dev/null
printf 'DEVOPS_ENROLL platform %s %s systemd\n' "$(uname -s)" "$(uname -m)"
printf 'DEVOPS_ENROLL config %s\n' "$(hash_file /etc/logagent/agent.yaml)"
printf 'DEVOPS_ENROLL binary %s\n' "$(hash_file /usr/local/bin/logagent)"
printf 'DEVOPS_ENROLL unit %s\n' "$(hash_file /etc/systemd/system/logagent.service)"
if [ -x /usr/local/bin/logagent ]; then
 printf 'DEVOPS_ENROLL identity %s\n' "$(/usr/local/bin/logagent -config /etc/logagent/agent.yaml -identity 2>/dev/null || printf '%s' '-')"
 printf 'DEVOPS_ENROLL user %s\n' "$(systemctl show logagent -p User | cut -d= -f2)"
 printf 'DEVOPS_ENROLL group %s\n' "$(systemctl show logagent -p Group | cut -d= -f2)"
fi
`

func inspectEnrollment(h Host, t enrollmentTransport) (enrollmentRemote, error) {
	out, e := t.exec(h, enrollmentInspect, 20*time.Second)
	if e != nil {
		return enrollmentRemote{}, fmt.Errorf("读取 Agent 接入状态失败，未更改目标机器")
	}
	v := enrollmentRemote{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] != "DEVOPS_ENROLL" {
			continue
		}
		value := ""
		if len(f) > 2 && f[2] != "-" {
			value = f[2]
		}
		switch f[1] {
		case "config":
			v.ConfigSHA = value
		case "binary":
			v.BinarySHA = value
		case "unit":
			v.UnitSHA = value
		case "identity":
			v.AgentID = value
		case "user":
			v.User = value
		case "group":
			v.Group = value
		case "platform":
			v.Platform, e = parseAgentPlatform(strings.Join(f[2:], " "))
		}
	}
	if e != nil || v.Platform == "" {
		return v, fmt.Errorf("目标需要受支持的 Linux/systemd")
	}
	if v.BinarySHA != "" && v.AgentID == "" {
		return v, fmt.Errorf("现有 Agent 持久身份不可读取，请先完成采集 Agent 的安全升级")
	}
	if v.BinarySHA != "" && v.User == "" {
		v.User = "root"
	}
	if v.BinarySHA != "" && v.Group == "" {
		v.Group = v.User
	}
	return v, nil
}
func (s *Store) prepareEnrollment(h Host, input enrollmentInput, actor string, t enrollmentTransport) (*enrollmentState, error) {
	if old := s.enrollment(h.Name); old != nil && enrollmentBusy(old.Phase) {
		return nil, fmt.Errorf("此主机存在尚未核对的接入操作，请先刷新接入状态")
	}
	services, roots, e := s.validateEnrollment(h, input)
	if e != nil {
		return nil, e
	}
	remote, e := inspectEnrollment(h, t)
	if e != nil {
		return nil, e
	}
	if remote.AgentID != "" {
		if e = s.enrollmentIdle(remote.AgentID); e != nil {
			return nil, e
		}
	}
	user, group := input.RunUser, input.RunGroup
	if remote.BinarySHA != "" {
		user, group = remote.User, remote.Group
	} else {
		if user == "" {
			user = "logagent"
		}
		if group == "" {
			group = user
		}
	}
	if !accountPattern.MatchString(user) || !accountPattern.MatchString(group) {
		return nil, fmt.Errorf("执行用户或用户组无效")
	}
	binary, e := t.binary(remote.Platform)
	if e != nil {
		return nil, e
	}
	id := enrollmentID()
	agent := remote.AgentID
	if agent == "" {
		agent = "agent_" + enrollmentID()
	}
	if id == "" || len(agent) != 38 {
		return nil, fmt.Errorf("无法创建接入身份")
	}
	if e = s.enrollmentUniqueAgent(h, agent); e != nil {
		return nil, e
	}
	oldRegistration := s.enrollmentRegistrationHash(agent)
	plan := enrollmentPlan{Scope: deploytask.EnrollmentScope{OperationID: id, AgentID: agent, Host: h.Name, Environment: h.Environment, Roots: roots, StateDir: "/var/lib/logagent/deployment-tasks", Launcher: "/usr/local/libexec/logagent-deployment-worker"}, Services: services, URL: strings.TrimRight(input.URL, "/"), RunUser: user, RunGroup: group, Remote: remote, HostDigest: s.enrollmentHostHash(h), BinarySHA: fmt.Sprintf("%x", sha256.Sum256(binary)), OldRegistration: oldRegistration, Actor: actor, Expires: time.Now().Add(10 * time.Minute).Unix()}
	if old := s.deploymentAgent(agent); old != nil && (old.Host != h.Name || old.Environment != h.Environment) {
		return nil, fmt.Errorf("安装身份属于其他主机，拒绝接管")
	}
	raw, _ := json.Marshal(plan)
	now := time.Now().Unix()
	tx, e := s.db.Begin()
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var phase string
	e = tx.QueryRow(enrollmentLockQuery(tx), h.Name).Scan(&phase)
	if e == sql.ErrNoRows {
		_, e = tx.Exec(`INSERT INTO deployment_agent_enrollments(host_name,operation_id,phase,plan,credential,created_at,updated_at) VALUES(?,?,'preview',?,'',?,?)`, h.Name, id, string(raw), now, now)
	} else if e == nil {
		if enrollmentBusy(phase) {
			return nil, fmt.Errorf("接入状态已改变")
		}
		_, e = tx.Exec(`UPDATE deployment_agent_enrollments SET operation_id=?,phase='preview',plan=?,credential='',created_at=?,updated_at=? WHERE host_name=?`, id, string(raw), now, now, h.Name)
	}
	if e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}

	v := s.enrollment(h.Name)
	if v == nil || v.OperationID != id {
		return nil, fmt.Errorf("接入状态已改变，请重新读取")
	}
	return v, nil
}

func (s *Store) applyEnrollment(h Host, id, actor string, t enrollmentTransport) (*enrollmentState, error) {
	v := s.enrollment(h.Name)
	if v == nil || v.OperationID != id || v.Plan.Actor != actor {
		return nil, fmt.Errorf("接入预览不存在或不属于本人")
	}
	if v.Phase != "preview" {
		return v, nil
	}
	p := v.Plan
	if time.Now().Unix() > p.Expires {
		return nil, fmt.Errorf("预览已过期，请重新核对")
	}
	if s.enrollmentHostHash(h) != p.HostDigest {
		return nil, fmt.Errorf("主机连接或环境已改变")
	}
	services, roots, e := s.validateEnrollment(h, enrollmentInput{Services: p.Services, URL: p.URL})
	if e != nil || enrollmentHash(services) != enrollmentHash(p.Services) || enrollmentHash(roots) != enrollmentHash(p.Scope.Roots) {
		return nil, fmt.Errorf("应用绑定或部署目录已改变")
	}
	remote, e := inspectEnrollment(h, t)
	if e != nil || enrollmentHash(remote) != enrollmentHash(p.Remote) {
		return nil, fmt.Errorf("远端 Agent 状态已改变，请重新预览")
	}
	if e = s.enrollmentIdle(p.Scope.AgentID); e != nil {
		return nil, e
	}
	if s.enrollmentRegistrationHash(p.Scope.AgentID) != p.OldRegistration {
		return nil, fmt.Errorf("Agent 授权已改变")
	}
	binary, e := t.binary(remote.Platform)
	if e != nil {
		return nil, e
	}
	if fmt.Sprintf("%x", sha256.Sum256(binary)) != p.BinarySHA {
		return nil, fmt.Errorf("Agent 安装包已改变")
	}
	token := "dpa_" + enrollmentID() + enrollmentID()
	if len(token) != 68 {
		return nil, fmt.Errorf("无法生成部署凭据")
	}
	cipher, e := s.encryptSecret(token)
	if e != nil {
		return nil, e
	}
	tx, e := s.db.Begin()
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var phase string
	if e = tx.QueryRow(enrollmentLockQuery(tx), h.Name).Scan(&phase); e != nil {
		return nil, e
	}
	if e = enrollmentMatches(tx, h.Name, id); e != nil {
		return nil, e
	}
	if phase != "preview" {
		tx.Rollback()
		return s.enrollment(h.Name), nil
	}
	var active int
	if e = tx.QueryRow(`SELECT COUNT(*) FROM agent_deployment_tasks WHERE agent_id=? AND status NOT IN ('success','failed','rolled-back','rollback-failed')`, p.Scope.AgentID).Scan(&active); e != nil {
		return nil, e
	}
	if active > 0 {
		return nil, fmt.Errorf("Agent 存在活动任务")
	}
	res, e := tx.Exec(`UPDATE deployment_agent_enrollments SET phase='applying',credential=?,updated_at=? WHERE host_name=? AND operation_id=? AND phase='preview'`, cipher, time.Now().Unix(), h.Name, id)
	if e != nil {
		return nil, e
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return nil, fmt.Errorf("接入预览已改变")
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}

	stage := "/tmp/devops-agent-enroll-" + id
	// Intent is durable before the first remote mutation. On unknown outcomes,
	// status is inspected; this operation is never automatically replayed.
	updateErr := func() error {
		if _, e = t.exec(h, "umask 077; mkdir "+shellQuote(stage), 15*time.Second); e != nil {
			return e
		}
		request := agentcfg.DeploymentConfigRequest{Scope: p.Scope, Token: token, URL: p.URL, ExpectedConfigSHA: remote.ConfigSHA, Output: "/var/lib/devops-agent-enrollment/" + id + "/result/agent.yaml"}
		raw, _ := json.Marshal(request)
		for _, file := range []struct {
			name string
			data []byte
		}{{"logagent", binary}, {"request.json", raw}, {"worker", []byte(renderEnrollmentLauncher(p))}, {"sudoers", []byte(renderEnrollmentSudoers(p))}, {"unit", []byte(renderAgentUnit(p.RunUser, p.RunGroup))}} {
			if e = t.upload(h, file.data, stage+"/"+file.name); e != nil {
				return e
			}
		}
		_, e = t.exec(h, "bash -c "+shellQuote(renderEnrollmentApply(p, stage)), 3*time.Minute)
		return e
	}()
	phase = "awaiting_connection"
	if updateErr != nil {
		phase = "needs_review"
	}
	_, _ = s.db.Exec(`UPDATE deployment_agent_enrollments SET phase=?,updated_at=? WHERE host_name=? AND operation_id=? AND phase='applying'`, phase, time.Now().Unix(), h.Name, id)
	// Do not propagate SSH output: a process failure may include credential
	// contents. The public status is recoverable and never claims readiness.
	return s.enrollment(h.Name), nil
}

func (s *Store) acceptEnrollment(proof deploytask.EnrollmentProof, token string) error {
	// The already active credential remains usable while a new preview or
	// rotation is pending. A preview must not interrupt the running Agent.
	if a := s.deploymentAgent(proof.AgentID); a != nil && a.Host == proof.Host && a.Environment == proof.Environment && s.authorizeDeploymentAgent(proof.AgentID, token) {
		v := s.enrollment(proof.Host)
		if v == nil || v.OperationID != proof.OperationID {
			return nil
		}
	}
	v := s.enrollment(proof.Host)
	if v == nil || proof.OperationID != v.OperationID || !proof.WorkerVerified || enrollmentHash(proof.EnrollmentScope) != enrollmentHash(v.Plan.Scope) {
		return fmt.Errorf("接入证明不匹配")
	}
	if v.Phase == "ready" {
		if !s.authorizeDeploymentAgent(proof.AgentID, token) {
			return fmt.Errorf("部署授权已撤销")
		}
		return nil
	}
	secret, e := s.decryptSecret(v.credential)
	if e != nil || subtle.ConstantTimeCompare([]byte(secret), []byte(token)) != 1 {
		return fmt.Errorf("接入凭据无效")
	}
	if !enrollmentBusy(v.Phase) {
		return fmt.Errorf("接入状态不允许回连")
	}
	h := s.GetHost(proof.Host)
	if h == nil || s.enrollmentHostHash(*h) != v.Plan.HostDigest {
		return fmt.Errorf("主机归属已改变")
	}
	if e = s.enrollmentUniqueAgent(*h, proof.AgentID); e != nil {
		return e
	}
	if e = s.enrollmentIdle(proof.AgentID); e != nil {
		return e
	}
	if s.enrollmentRegistrationHash(proof.AgentID) != v.Plan.OldRegistration {
		return fmt.Errorf("旧授权已改变")
	}
	_, roots, e := s.validateEnrollment(*h, enrollmentInput{Services: v.Plan.Services, URL: v.Plan.URL})
	if e != nil || enrollmentHash(roots) != enrollmentHash(proof.Roots) {
		return fmt.Errorf("应用目录授权已改变")
	}
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var phase string
	if e = tx.QueryRow(enrollmentLockQuery(tx), proof.Host).Scan(&phase); e != nil {
		return e
	}
	if e = enrollmentMatches(tx, proof.Host, proof.OperationID); e != nil {
		return e
	}
	if phase == "ready" {
		return tx.Commit()
	}
	if !enrollmentBusy(phase) {
		return fmt.Errorf("接入已撤销")
	}
	hash := sha256.Sum256([]byte(token))
	services, _ := json.Marshal(v.Plan.Services)
	_, e = tx.Exec(`INSERT INTO deployment_agents(agent_id,host_name,environment,services,token_hash,enabled,last_seen) VALUES(?,?,?,?,?,1,?) ON CONFLICT(agent_id) DO UPDATE SET services=excluded.services,token_hash=excluded.token_hash,enabled=1,last_seen=excluded.last_seen`, proof.AgentID, proof.Host, proof.Environment, string(services), hex.EncodeToString(hash[:]), time.Now().Unix())
	if e != nil {
		return e
	}
	_, e = tx.Exec(`UPDATE deployment_agent_enrollments SET phase='ready',credential='',updated_at=? WHERE host_name=? AND operation_id=?`, time.Now().Unix(), proof.Host, proof.OperationID)
	if e != nil {
		return e
	}
	return tx.Commit()
}

func registerEnrollmentRoutes(mux *http.ServeMux, s *Store, t enrollmentTransport) {
	admin := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("X-Role") != "admin" {
			deliveryWriteError(w, 403, "仅管理员可管理部署接入")
			return false
		}
		return true
	}
	mux.HandleFunc("GET /api/hosts/{name}/deployment", func(w http.ResponseWriter, r *http.Request) {
		h := s.GetHost(r.PathValue("name"))
		if h == nil {
			deliveryWriteError(w, 404, "主机不存在")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		deliveryJSON(w, map[string]any{"options": s.enrollmentOptions(*h), "enrollment": s.enrollment(h.Name)})
	})
	mux.HandleFunc("POST /api/hosts/{name}/deployment/preview", func(w http.ResponseWriter, r *http.Request) {
		if !admin(w, r) {
			return
		}
		h := s.GetHost(r.PathValue("name"))
		if h == nil {
			deliveryWriteError(w, 404, "主机不存在")
			return
		}
		var input enrollmentInput
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&input) != nil {
			deliveryWriteError(w, 400, "接入参数无效")
			return
		}
		v, e := s.prepareEnrollment(*h, input, deliveryActor(r), t)
		if e != nil {
			deliveryWriteError(w, 409, e)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		deliveryJSON(w, v)
	})
	mux.HandleFunc("POST /api/hosts/{name}/deployment/disable", func(w http.ResponseWriter, r *http.Request) {
		if !admin(w, r) {
			return
		}
		v := s.enrollment(r.PathValue("name"))
		if v == nil {
			deliveryWriteError(w, 404, "此主机尚未接入部署")
			return
		}
		if e := s.enrollmentIdle(v.Plan.Scope.AgentID); e != nil {
			deliveryWriteError(w, 409, e)
			return
		}
		tx, e := s.db.Begin()
		if e != nil {
			deliveryWriteError(w, 500, "授权状态不可用")
			return
		}
		defer tx.Rollback()
		var phase string
		if e = tx.QueryRow(enrollmentLockQuery(tx), r.PathValue("name")).Scan(&phase); e == nil {
			if e = enrollmentMatches(tx, r.PathValue("name"), v.OperationID); e != nil {
				deliveryWriteError(w, 409, "接入记录已改变，请刷新后核对")
				return
			}

			var active int
			e = tx.QueryRow(`SELECT COUNT(*) FROM agent_deployment_tasks WHERE agent_id=? AND status NOT IN ('success','failed','rolled-back','rollback-failed')`, v.Plan.Scope.AgentID).Scan(&active)
			if e != nil || active > 0 {
				deliveryWriteError(w, 409, "Agent 存在活动或待核对任务，授权未停用")
				return
			}

			_, e = tx.Exec(`UPDATE deployment_agent_enrollments SET phase='disabled',credential='',updated_at=? WHERE host_name=?`, time.Now().Unix(), r.PathValue("name"))
		}
		if e == nil {
			_, e = tx.Exec(`UPDATE deployment_agents SET enabled=0 WHERE agent_id=?`, v.Plan.Scope.AgentID)
		}
		if e == nil {
			e = tx.Commit()
		}
		if e != nil {
			deliveryWriteError(w, 409, "授权状态已改变，请刷新核对")
			return
		}
		s.Audit(deliveryActor(r), "disable-deployment-agent", "host", r.PathValue("name"), nil)
		deliveryJSON(w, s.enrollment(r.PathValue("name")))
	})
	mux.HandleFunc("POST /api/hosts/{name}/deployment/confirm", func(w http.ResponseWriter, r *http.Request) {
		if !admin(w, r) {
			return
		}
		h := s.GetHost(r.PathValue("name"))
		if h == nil {
			deliveryWriteError(w, 404, "主机不存在")
			return
		}
		var input struct {
			OperationID string `json:"operation_id"`
			Confirm     bool   `json:"confirm"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&input) != nil || !input.Confirm {
			deliveryWriteError(w, 400, "请明确确认预览中的应用、目录和执行用户")
			return
		}
		v, e := s.applyEnrollment(*h, input.OperationID, deliveryActor(r), t)
		if e != nil {
			deliveryWriteError(w, 409, e)
			return
		}
		s.Audit(deliveryActor(r), "configure-deployment-agent", "host", h.Name, map[string]any{"operation_id": v.OperationID, "phase": v.Phase, "services": v.Plan.Services, "roots": v.Plan.Scope.Roots})
		w.Header().Set("Cache-Control", "no-store")
		deliveryJSON(w, v)
	})
	mux.HandleFunc("POST /api/deployment-agent/enrollment", func(w http.ResponseWriter, r *http.Request) {
		var proof deploytask.EnrollmentProof
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&proof) != nil || proof.AgentID != r.Header.Get("X-Deployment-Agent-ID") {
			deliveryWriteError(w, 401, "接入身份无效")
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if e := s.acceptEnrollment(proof, token); e != nil {
			deliveryWriteError(w, 401, "接入证明未通过")
			return
		}
		deliveryJSON(w, map[string]bool{"ok": true})
	})
}

func enrollmentLockQuery(tx *deployTx) string {
	q := `SELECT phase FROM deployment_agent_enrollments WHERE host_name=?`
	if tx.dialect == "mysql" {
		q += " FOR UPDATE"
	}
	return q
}

func (s *Store) enrollmentRegistrationHash(id string) string {
	var host, env, services, hash string
	var enabled int
	e := s.db.QueryRow(`SELECT host_name,environment,services,token_hash,enabled FROM deployment_agents WHERE agent_id=?`, id).Scan(&host, &env, &services, &hash, &enabled)
	if e == sql.ErrNoRows {
		return enrollmentHash(nil)
	}
	if e != nil {
		return "unreadable"
	}
	return enrollmentHash([]any{host, env, services, hash, enabled})
}

func (s *Store) enrollmentUniqueAgent(h Host, id string) error {
	var n int
	if e := s.db.QueryRow(`SELECT COUNT(*) FROM deployment_agents WHERE host_name=? AND environment=? AND enabled=1 AND agent_id<>?`, h.Name, h.Environment, id).Scan(&n); e != nil {
		return e
	}
	if n > 0 {
		return fmt.Errorf("此主机已有其他部署身份，请核对并停用旧授权后再接入")
	}
	return nil
}

func enrollmentMatches(tx *deployTx, host, id string) error {
	var current string
	if e := tx.QueryRow(`SELECT operation_id FROM deployment_agent_enrollments WHERE host_name=?`, host).Scan(&current); e != nil {
		return e
	}
	if current != id {
		return fmt.Errorf("接入操作已更换，旧操作不能激活或撤销新身份")
	}
	return nil
}
