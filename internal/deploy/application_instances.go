package deploy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// A request ID is a durable claim, never permission to repeat an SSH write.
const instanceOperationSchema = `CREATE TABLE IF NOT EXISTS instance_operations (request_id VARCHAR(64) PRIMARY KEY,service VARCHAR(191) NOT NULL,environment VARCHAR(64) NOT NULL,host VARCHAR(191) NOT NULL,action VARCHAR(16) NOT NULL,actor VARCHAR(191) NOT NULL,fingerprint VARCHAR(80) NOT NULL,state VARCHAR(24) NOT NULL,started_at BIGINT NOT NULL,ended_at BIGINT NOT NULL DEFAULT 0)`

type applicationInstance struct {
	ManagedJavaInstance
	Kind           string `json:"kind"`
	ContainerID    string `json:"container_id,omitempty"`
	Container      string `json:"container,omitempty"`
	Image          string `json:"image,omitempty"`
	Health         string `json:"health,omitempty"`
	DiagnosticCode string `json:"diagnostic_code,omitempty"`
	Fingerprint    string `json:"fingerprint,omitempty"`
	Owned          bool   `json:"owned"`
	Controllable   bool   `json:"controllable"`
	JavaMode       string `json:"java_mode,omitempty"`
	ProjectID      string `json:"project_id,omitempty"`
	JavaVersion    string `json:"java_version,omitempty"`
	jpom           *jpomInstanceIdentity
	Restarts       int    `json:"restarts,omitempty"`
	StartedAt      string `json:"started_at,omitempty"`
}

type instanceManager struct {
	st   *Store
	exec javaExec
}
type dockerInstanceInfo struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	ImageID          string `json:"image_id"`
	ImageRef         string `json:"image_ref"`
	State            string `json:"state"`
	PID              int    `json:"pid"`
	Restarts         int    `json:"restarts"`
	StartedAt        string `json:"started_at"`
	Health           string `json:"health"`
	LabelService     string `json:"label_service"`
	LabelEnvironment string `json:"label_environment"`
}

func instanceContainerName(sv *Service) string {
	if n := sv.DeployConfig["container_name"]; n != "" {
		return n
	}
	return sv.Name
}

var instanceContainerNameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)
var instanceIDRE = regexp.MustCompile(`^[a-f0-9]{64}$`)
var instanceRequestRE = regexp.MustCompile(`^[a-zA-Z0-9-]{16,64}$`)

func (m *instanceManager) target(service, environment string) (*ServiceEnvironment, *Service, []Host, error) {
	t := m.st.GetServiceEnvironment(service, environment)
	sv := m.st.GetService(service)
	if t == nil || sv == nil {
		return nil, nil, nil, fmt.Errorf("应用环境不存在")
	}
	sv = t.Apply(sv)
	if t.ManualTarget() || !(managedJava(sv) || jpomJava(sv) || sv.TargetType == "docker-host") {
		return nil, nil, nil, fmt.Errorf("此环境未由平台托管进程或 Docker 容器，不能直接管理实例")
	}
	if jpomJava(sv) {
		env := m.st.GetEnvironment(environment)
		if env == nil || env.Protected || t.Protected || (env.Purpose != "development" && env.Purpose != "test") {
			return nil, nil, nil, fmt.Errorf("Jpom 兼容实例仅支持非受保护开发／测试环境")
		}
		if err := validateJpomJava(sv); err != nil {
			return nil, nil, nil, err
		}
	}
	if sv.TargetType == "docker-host" && !instanceContainerNameRE.MatchString(instanceContainerName(sv)) {
		return nil, nil, nil, fmt.Errorf("配置的容器名无效")
	}
	hosts := []Host{}
	for _, n := range t.Hosts {
		h := m.st.GetHost(n)
		if h == nil || (jpomJava(sv) && h.Environment != t.Environment) {
			return nil, nil, nil, fmt.Errorf("环境主机不存在或归属已变化")
		}
		hosts = append(hosts, *h)
	}
	return t, sv, hosts, nil
}

// Only selected Docker fields leave the host; no Env, command line or inspect dump.
// Docker omits Health and sometimes Labels entirely when they are not configured.
// Docker's formatter uses missingkey=error, so even an `if .State.Health` fails.
func dockerInstanceInspectTemplate() string {
	return `{"id":{{json .Id}},"name":{{json .Name}},"image_id":{{json .Image}},"image_ref":{{json .Config.Image}},"label_service":{{with index .Config "Labels"}}{{json (index . "io.devopsplatform.service")}}{{else}}""{{end}},"label_environment":{{with index .Config "Labels"}}{{json (index . "io.devopsplatform.environment")}}{{else}}""{{end}},"state":{{json .State.Status}},"pid":{{.State.Pid}},"restarts":{{.RestartCount}},"started_at":{{json .State.StartedAt}},"health":{{with index .State "Health"}}{{json (index . "Status")}}{{else}}""{{end}}}`
}
func dockerInstanceStatusCommand(name string) string {
	return `ids=$(docker container ls -a --no-trunc --filter ` + shellQuote("name=^/"+regexp.QuoteMeta(name)+"$") + ` --format '{{.ID}}') || exit 2; if [ -z "$ids" ]; then printf '{}'; exit 0; fi; ` +
		"docker inspect --format " + shellQuote(dockerInstanceInspectTemplate()) + " " + shellQuote(name)
}

func (m *instanceManager) inspect(t *ServiceEnvironment, sv *Service, h Host) applicationInstance {
	r := applicationInstance{ManagedJavaInstance: ManagedJavaInstance{Host: h.Name, Address: h.Addr, State: "unknown", CheckedAt: time.Now().Unix()}, Kind: sv.TargetType}
	if jpomJava(sv) {
		r = inspectJpomInstance(sv, h, m.exec)
		if r.Owned && m.jpomBindingOverlaps(t, sv, h) {
			r.Owned = false
			r.Controllable = false
			r.Message = "同一 Jpom 实例被多个应用或环境引用，请先纠正绑定"
		}
	} else if managedJava(sv) {
		r.ManagedJavaInstance = inspectManagedJavaInstance(sv, h, m.exec)
		if dp := m.st.GetDeploy(r.DeployID); dp != nil && dp.Service == sv.Name && dp.Environment == t.Environment && dp.Host == h.Name {
			r.Version = dp.Version
			r.Owned = true
			r.Controllable = r.State == "running" || r.State == "stopped"
		}
	} else {
		r.Container = instanceContainerName(sv)
		out, err := m.exec(h, dockerInstanceStatusCommand(r.Container), 15*time.Second)
		if err != nil {
			r.State, r.DiagnosticCode, r.Message = classifyInstanceReadError(out, err)
			return r
		}
		var v dockerInstanceInfo
		if len(out) > 16384 || json.Unmarshal([]byte(strings.TrimSpace(out)), &v) != nil {
			r.Message = "容器状态响应无效"
			return r
		}
		if v.ID == "" {
			r.State = "not-deployed"
			r.Message = "未找到已配置的容器，不会自动创建"
			return r
		}
		if !instanceIDRE.MatchString(v.ID) || v.Name != "/"+r.Container || !strings.HasPrefix(v.ImageID, "sha256:") {
			r.Message = "容器身份无法核验"
			return r
		}
		r.ContainerID = v.ID
		r.Image = v.ImageRef
		r.PID = v.PID
		r.Restarts = v.Restarts
		r.StartedAt = v.StartedAt
		r.Health = v.Health
		switch v.State {
		case "running":
			r.State = "running"
		case "exited", "created":
			r.State = "stopped"
		default:
			r.State = v.State
		}
		// A configured name alone never grants authority over an arbitrary container.
		rows, err := m.st.db.Query(`SELECT id,version,artifact_id,artifact_kind,artifact_uri FROM deploys WHERE service=? AND environment=? AND host=? AND status='success' ORDER BY id DESC LIMIT 100`, sv.Name, t.Environment, h.Name)
		type previous struct {
			id, artifact       int64
			version, kind, uri string
		}
		history := []previous{}
		if err == nil {
			for rows.Next() {
				var p previous
				if rows.Scan(&p.id, &p.version, &p.artifact, &p.kind, &p.uri) == nil {
					history = append(history, p)
				}
			}
			rows.Close()
		}
		for _, p := range history {
			match := p.kind == ArtifactOCI && strings.Contains(p.uri, "@sha256:") && v.ImageRef == p.uri
			if p.kind == ArtifactDockerArchive {
				if a := m.st.GetArtifact(p.artifact); a != nil {
					match = a.Metadata["image_id"] == v.ImageID
				}
			}
			if match {
				r.DeployID = p.id
				r.Version = p.version
				break
			}
		}

		r.Owned = r.DeployID > 0
		r.Controllable = r.Owned && (r.State == "running" || r.State == "stopped" || r.State == "restarting")
		if (v.LabelService != "" || v.LabelEnvironment != "") && (v.LabelService != sv.Name || v.LabelEnvironment != t.Environment) {
			r.Owned = false
			r.Controllable = false
			r.Message = "容器标签属于其他应用或环境"
		}
		// A custom container name must not make two application bindings overlap.
		for _, app := range m.st.Services() {
			for _, other := range m.st.ServiceEnvironments(app.Name) {
				if app.Name == sv.Name && other.Environment == t.Environment {
					continue
				}
				if other.TargetType != "docker-host" || instanceContainerName(other.Apply(&app)) != r.Container {
					continue
				}
				for _, n := range other.Hosts {
					peer := m.st.GetHost(n)
					if peer != nil && peer.Addr == h.Addr {
						r.Owned = false
						r.Controllable = false
						r.Message = "同一容器被多个应用或环境引用，请先纠正绑定"
					}
				}
			}
		}

		if !r.Owned && r.Message == "" {
			r.Message = "当前容器与平台成功发布记录不匹配，仅展示状态"
		}
	}
	if r.Owned {
		r.Fingerprint = digestJSON([]any{t, t.runtimeEnvEncrypted, h.Name, h.Addr, h.User, h.HostKey, h.KeyPath, r.ContainerID, r.Image, r.Release, r.PID, r.State, r.Restarts, r.StartedAt, r.DeployID, r.jpom})
	}
	return r
}

func (s *Store) claimInstanceOperation(service, env, host, action, actor, fp, id string) error {
	if !instanceRequestRE.MatchString(id) {
		return fmt.Errorf("操作标识无效，请刷新后重新确认")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockArtifactLifecycle(tx); err != nil {
		return err
	}
	var n int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM instance_operations WHERE request_id=?`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("该操作已受理，不会重复执行；请刷新状态核对结果")
	}
	if err = instanceOperationBusy(tx, service, env, host); err != nil {
		return err
	}
	if err = tx.QueryRow(`SELECT COUNT(*) FROM release_orders WHERE service=? AND environment=? AND status IN ('pending_approval','queued','running','waiting_next_batch','rolling_back','waiting_manual','needs_review')`, service, env).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("该环境正在处理发布单，请完成后再操作实例")
	}
	if err = tx.QueryRow(`SELECT COUNT(*) FROM deploys WHERE service=? AND host=? AND status IN ('running','waiting_next_batch','needs_review')`, service, host).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("该主机正在发布，请稍后再操作")
	}
	_, err = tx.Exec(`INSERT INTO instance_operations(request_id,service,environment,host,action,actor,fingerprint,state,started_at) VALUES(?,?,?,?,?,?,?,'running',?)`, id, service, env, host, action, actor, fp, time.Now().Unix())
	if err != nil {
		return err
	}
	return tx.Commit()
}
func instanceOperationBusy(tx *deployTx, service, env, host string) error {
	var n int
	err := tx.QueryRow(`SELECT COUNT(*) FROM instance_operations WHERE service=? AND ((?='' AND ?='') OR environment=? OR host=?) AND state IN ('running','uncertain') AND started_at>?`, service, env, host, env, host, time.Now().Unix()-600).Scan(&n)
	if err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("%w: 实例操作正在进行或结果待核对，暂不能并发执行", ErrRunConflict)
	}
	return nil
}

func dockerInstanceControlCommand(r applicationInstance, action string) (string, error) {
	if !instanceIDRE.MatchString(r.ContainerID) || !instanceContainerNameRE.MatchString(r.Container) {
		return "", fmt.Errorf("容器身份无效")
	}
	operation := map[string]string{"start": "start", "stop": "stop --time 30", "restart": "restart --time 30"}[action]
	if operation == "" {
		return "", fmt.Errorf("不支持的容器操作")
	}
	guard := `test "$(docker inspect --format '{{.Id}}' ` + shellQuote(r.Container) + `)" = ` + shellQuote(r.ContainerID) + ` || { echo 'container replaced; refresh required' >&2; exit 3; }; `
	return guard + "docker " + operation + " " + shellQuote(r.ContainerID) + " >/dev/null", nil
}

func (m *instanceManager) control(service, environment string, h Host, action, actor string, req instanceActionRequest) (result applicationInstance, controlErr error) {
	var empty applicationInstance
	if err := m.st.claimInstanceOperation(service, environment, h.Name, action, actor, req.Fingerprint, req.RequestID); err != nil {
		return empty, err
	}
	state := "rejected"
	defer func() {
		if _, err := m.st.db.Exec(`UPDATE instance_operations SET state=?,ended_at=? WHERE request_id=?`, state, time.Now().Unix(), req.RequestID); err != nil && controlErr == nil {
			controlErr = fmt.Errorf("操作结果保存失败，请核对实例状态，勿重复提交")
		}
	}()
	t, sv, hosts, err := m.target(service, environment)
	if err != nil {
		return empty, err
	}
	found := false
	for _, current := range hosts {
		if current.Name == h.Name {
			h = current
			found = true
		}
	}
	if !found {
		return empty, fmt.Errorf("主机已移出应用环境")
	}
	if t.Protected && !req.ConfirmProtected || t.Protected && strings.TrimSpace(req.Reason) == "" {
		return empty, fmt.Errorf("环境审批要求已变化，请重新确认")
	}
	r := m.inspect(t, sv, h)
	if !r.Controllable || r.Fingerprint == "" || r.Fingerprint != req.Fingerprint {
		return r, fmt.Errorf("实例或环境已变化，请刷新状态后重新确认")
	}
	if action == "start" && r.State != "stopped" || action == "stop" && r.State != "running" && r.State != "restarting" || action == "restart" && r.State != "running" {
		return r, fmt.Errorf("实例状态已变化，请刷新")
	}
	var command string
	if action == "ready" && sv.TargetType == "docker-host" {
		return r, fmt.Errorf("Docker 健康状态请刷新查看，不执行 Java 就绪检查")
	}
	if jpomJava(sv) {
		env, e := m.st.decryptRuntimeEnvironment(t.runtimeEnvEncrypted)
		if e != nil {
			return r, fmt.Errorf("运行变量不可用，请先核对环境配置")
		}
		command, err = jpomInstanceControlCommand(sv, r, action, env, "/proc")
	} else if managedJava(sv) {
		command, err = managedJavaControlCommand(sv.RemoteDir, action)
	} else {
		command, err = dockerInstanceControlCommand(r, action)
	}
	if err != nil {
		return r, err
	}
	state = "uncertain"
	output, execErr := m.exec(h, command, 3*time.Minute)
	err = execErr
	next := m.inspect(t, sv, h)
	if !next.Owned || (jpomJava(sv) && (next.jpom == nil || jpomInstanceAck(output) != next.jpom.signature())) || (sv.TargetType == "docker-host" && next.ContainerID != r.ContainerID) || (managedJava(sv) && next.DeployID != r.DeployID) {
		return next, fmt.Errorf("操作期间实例已替换，结果待核对，未自动重试")
	}
	if err != nil {
		return next, fmt.Errorf("操作未确认，未自动重试；请刷新实例状态并核对日志")
	}
	if action == "stop" && next.State != "stopped" || action != "stop" && next.State != "running" {
		return next, fmt.Errorf("操作命令已返回，目标状态尚未确认，请刷新核对")
	}
	state = "success"
	return next, nil
}

type instanceActionRequest struct {
	Reason           string `json:"reason"`
	ConfirmProtected bool   `json:"confirm_protected"`
	Fingerprint      string `json:"fingerprint"`
	RequestID        string `json:"request_id"`
}

func registerInstanceRoutes(mux *http.ServeMux, st *Store, exec javaExec) {
	m := &instanceManager{st: st, exec: exec}
	mux.HandleFunc("GET /api/services/{name}/environments/{environment}/instances", func(w http.ResponseWriter, r *http.Request) {
		t, sv, hosts, err := m.target(r.PathValue("name"), r.PathValue("environment"))
		if err != nil {
			deliveryWriteError(w, 409, err)
			return
		}
		out := make([]applicationInstance, len(hosts))
		var wg sync.WaitGroup
		slots := make(chan struct{}, 4)
		for i, h := range hosts {
			wg.Add(1)
			go func(i int, h Host) {
				defer wg.Done()
				slots <- struct{}{}
				defer func() { <-slots }()
				out[i] = m.inspect(t, sv, h)
			}(i, h)
		}
		wg.Wait()
		deliveryJSON(w, out)
	})
	mux.HandleFunc("POST /api/services/{name}/environments/{environment}/instances/{host}/{action}", func(w http.ResponseWriter, r *http.Request) {
		service, env, host, action := r.PathValue("name"), r.PathValue("environment"), r.PathValue("host"), r.PathValue("action")
		t, _, hosts, err := m.target(service, env)
		if err != nil {
			deliveryWriteError(w, 409, err)
			return
		}
		var selected *Host
		for _, h := range hosts {
			if h.Name == host {
				copy := h
				selected = &copy
			}
		}
		if selected == nil {
			deliveryWriteError(w, 400, "主机不属于该应用环境")
			return
		}
		if action != "start" && action != "stop" && action != "restart" && action != "ready" {
			deliveryWriteError(w, 400, "仅支持启动、停止或重启")
			return
		}
		var req instanceActionRequest
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req) != nil {
			deliveryWriteError(w, 400, "操作参数无效")
			return
		}
		if t.Protected && !req.ConfirmProtected {
			deliveryWriteError(w, http.StatusPreconditionRequired, "受保护环境实例操作需要二次确认")
			return
		}
		req.Reason = strings.TrimSpace(req.Reason)
		if t.Protected && req.Reason == "" || len(req.Reason) > 2000 {
			deliveryWriteError(w, 400, "请填写操作原因（最多 2000 字节）")
			return
		}
		if deliveryActor(r) != "local" && r.Header.Get("X-Role") != "admin" {
			deliveryWriteError(w, 403, "实例启停仅管理员可执行")
			return
		}
		out, err := m.control(service, env, *selected, action, deliveryActor(r), req)
		status := "instance-" + action
		if err != nil {
			status += "-unconfirmed"
		}
		st.Audit(deliveryActor(r), status, "application-instance", service+"/"+env+"/"+host, map[string]any{"reason": req.Reason, "request_id": req.RequestID, "state": out.State})
		if err != nil {
			deliveryWriteError(w, 409, err)
			return
		}
		deliveryJSON(w, out)
	})
	registerInstanceLogRoute(mux, m)
}
