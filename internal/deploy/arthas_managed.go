package deploy

// Managed onboarding uses existing deployment bindings and SSH trust. No host,
// credentials, command, download URL, or container name is accepted from clients.
import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const arthasDistributionURL = "https://repo.maven.apache.org/maven2/com/taobao/arthas/arthas-packaging/4.0.5/arthas-packaging-4.0.5-bin.zip"
const arthasDistributionSHA = "7dcf453f6986e7bcb5af480ed0cbf5ec36791717cc7d032b58eae5dc16597b5f"

type arthasDeployment struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Service     string `json:"service"`
	Environment string `json:"environment"`
	Host        string `json:"host"`
	Kind        string `json:"kind"`
	Container   string `json:"-"`
	RemoteDir   string `json:"-"`
	Binding     string `json:"-"`
}
type arthasProcess struct {
	PID         int    `json:"pid"`
	UID         int    `json:"-"`
	GID         int    `json:"-"`
	User        string `json:"-"`
	Java        string `json:"-"`
	Start       string `json:"-"`
	Fingerprint string `json:"fingerprint"`
	ContainerID string `json:"-"`
	HostPID     int    `json:"host_pid"`
	AttachMode  string `json:"attach_mode"`
}
type arthasManagedRecord struct {
	Instance    arthasDeployment
	Process     arthasProcess
	Directory   string
	Port        int
	State       string
	Binding     string
	ContainerID string
}
type arthasDeploymentIO struct {
	exec    func(context.Context, Host, string, time.Duration) (string, error)
	upload  func(context.Context, Host, []byte, string) error
	pack    func(context.Context) ([]byte, error)
	request func(context.Context, Host, string, []byte) ([]byte, error)
	native  func(context.Context, string) ([]byte, error)
}
type arthasDeploymentManager struct {
	st   *Store
	io   arthasDeploymentIO
	mu   sync.Mutex
	busy map[string]bool
}

var arthasOpaqueID = regexp.MustCompile(`^[a-f0-9]{40}$`)
var arthasContainerID = regexp.MustCompile(`^[a-f0-9]{64}$`)
var arthasRemoteDir = regexp.MustCompile(`^/tmp/devops-arthas-[a-f0-9]{32}$`)

func arthasHash(values ...string) string {
	h := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return hex.EncodeToString(h[:])[:40]
}
func (st *Store) arthasDeployments(service, environment string) []arthasDeployment {
	out := []arthasDeployment{}
	for _, sv := range st.Services() {
		if service != "" && service != sv.Name {
			continue
		}
		for _, env := range st.ServiceEnvironments(sv.Name) {
			if environment != "" && env.Environment != environment {
				continue
			}
			runtime := env.Apply(&sv)
			kind := runtime.TargetType
			if kind != "ssh-jar" && kind != "docker-host" {
				continue
			}
			for _, name := range env.Hosts {
				h := st.GetHost(name)
				if h == nil {
					continue
				}
				var count int
				// History establishes that this is a deployment target, not an
				// arbitrary host selected through the diagnostic endpoint.
				if st.db.QueryRow(`SELECT COUNT(*) FROM deploys WHERE service=? AND environment=? AND host=?`, sv.Name, env.Environment, name).Scan(&count) != nil || count == 0 {
					continue
				}
				container := runtime.DeployConfig["container_name"]
				if container == "" {
					container = sv.Name
				}
				id := arthasHash(sv.ApplicationID, sv.Name, env.Environment, name)
				label := sv.DisplayName
				if label == "" {
					label = sv.Name
				}
				out = append(out, arthasDeployment{ID: id, Name: label + " · " + name, Service: sv.Name, Environment: env.Environment, Host: name, Kind: kind, Container: container, RemoteDir: runtime.RemoteDir, Binding: arthasHash(h.Addr, h.User, h.HostKey, h.KeyPath, kind, container, runtime.RemoteDir)})
			}
		}
	}
	return out
}
func (m *arthasDeploymentManager) resolve(id string) (*arthasDeployment, *Host, error) {
	if !arthasOpaqueID.MatchString(id) {
		return nil, nil, errors.New("部署实例标识无效")
	}
	for _, v := range m.st.arthasDeployments("", "") {
		if v.ID == id {
			h := m.st.GetHost(v.Host)
			if h != nil {
				return &v, h, nil
			}
		}
	}
	return nil, nil, errors.New("部署实例不存在或已移出应用环境，请刷新")
}

// Only structured process identity leaves the host. Never return cmdline,
// environment, Java arguments or raw SSH output (which may contain secrets).
func arthasProcessScript(remoteDir string) string {
	filter := ""
	if remoteDir != "" {
		filter = `cwd=$(readlink "$p/cwd" 2>/dev/null) || continue
case "$cwd/" in "$root/"*) ;; *) tr '\000' '\n' < "$p/cmdline" | grep -Fx -- "$root/app.jar" >/dev/null || continue;; esac
`
	}
	return `set -eu
test "$(uname -s)" = Linux || exit 41
root=` + shellQuote(strings.TrimRight(remoteDir, "/")) + `
boot=$(cat /proc/sys/kernel/random/boot_id)
for p in /proc/[0-9]*; do
 exe=$(readlink "$p/exe" 2>/dev/null) || continue
 case "$exe" in */bin/java) ;; *) continue;; esac
 ` + filter + `
 pid=${p##*/}
 uid=$(stat -c %u "$p"); gid=$(stat -c %g "$p"); user=$(stat -c %U "$p")
 start=$(sed 's/.*) //' "$p/stat" | awk '{print $20}')
 home=${exe%/bin/java}; home=${home%/jre}
 jdk=no; if test -r "$home/lib/tools.jar" || test -r "$home/jmods/jdk.attach.jmod"; then jdk=yes; fi
 if test "$jdk" = no; then case "$(uname -m)" in x86_64|amd64) jdk=native-x64;; aarch64|arm64) jdk=native-arm64;; *) jdk=unsupported;; esac; fi
 printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$pid" "$uid" "$gid" "$user" "$start" "$boot" "$exe" "$jdk"
done`
}
func parseArthasProcesses(raw string, instance arthasDeployment, cid string, hpid int) ([]arthasProcess, error) {
	out := []arthasProcess{}
	if len(raw) > 64<<10 {
		return nil, errors.New("实例进程信息超出限制")
	}
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 8 {
			return nil, errors.New("无法安全识别实例进程，请检查 Linux /proc 和工具权限")
		}
		pid, e := strconv.Atoi(f[0])
		uid, e2 := strconv.Atoi(f[1])
		gid, e3 := strconv.Atoi(f[2])
		if e != nil || e2 != nil || e3 != nil || pid < 1 || uid < 0 || gid < 0 || (!accountPattern.MatchString(f[3]) && !(cid != "" && f[3] == "UNKNOWN")) || !regexp.MustCompile(`^[0-9]+$`).MatchString(f[4]) || !regexp.MustCompile(`^[a-f0-9-]+$`).MatchString(f[5]) || !strings.HasPrefix(f[6], "/") || strings.ContainsAny(f[6], "\r\n\x00") {
			return nil, errors.New("进程身份信息无效")
		}
		if f[7] != "yes" && f[7] != "native-x64" && f[7] != "native-arm64" {
			return nil, errors.New("目标没有 Java Attach 组件，且当前 CPU 架构暂不支持轻量接入；支持 Linux x64/arm64。平台不会重启业务或替换镜像")
		}
		out = append(out, arthasProcess{PID: pid, UID: uid, GID: gid, User: f[3], Java: f[6], Start: f[4], ContainerID: cid, HostPID: hpid, AttachMode: f[7], Fingerprint: arthasHash(instance.ID, instance.Binding, cid, f[0], f[4], f[5], f[6])})
		if len(out) > 32 {
			return nil, errors.New("JVM 数量超出限制，请缩小部署实例范围")
		}
	}
	if len(out) == 0 {
		return nil, errors.New("没有找到此部署实例的 Java 进程；请确认应用正在运行及部署目录匹配")
	}
	return out, nil
}
func arthasPublicProcesses(ps []arthasProcess) []map[string]any {
	out := []map[string]any{}
	for _, p := range ps {
		out = append(out, map[string]any{"pid": p.PID, "fingerprint": p.Fingerprint, "attach_mode": p.AttachMode})
	}
	return out
}

type arthasDockerIdentity struct {
	ID       string
	PID      int
	Running  bool
	Restarts int
}

func parseArthasDockerIdentity(raw string) (arthasDockerIdentity, error) {
	f := strings.Fields(raw)
	var out arthasDockerIdentity
	if len(f) != 4 || !arthasContainerID.MatchString(f[0]) || (f[2] != "true" && f[2] != "false") {
		return out, errors.New("容器运行身份无法核验，请重新检测")
	}
	var err error
	out.ID, out.Running = f[0], f[2] == "true"
	if out.PID, err = strconv.Atoi(f[1]); err != nil || out.PID < 0 {
		return out, errors.New("容器进程无效")
	}
	if out.Restarts, err = strconv.Atoi(f[3]); err != nil || out.Restarts < 0 {
		return out, errors.New("容器重启计数无法核验")
	}
	return out, nil
}

func arthasContainerUnstable(restarts int) error {
	return fmt.Errorf("容器在检测期间已重启、退出或被替换（累计重启 %d 次）；本次操作已停止。请先查看应用启动日志，待容器稳定后重新检测", restarts)
}

func (m *arthasDeploymentManager) inspect(ctx context.Context, v arthasDeployment, h Host) ([]arthasProcess, error) {
	var cid string
	var hpid int
	var dockerBefore arthasDockerIdentity
	var dockerInspect string
	command := "sh -c " + shellQuote(arthasProcessScript(v.RemoteDir))
	if v.Kind == "docker-host" {
		// nsenter/curl live on the host, so the business image needs no curl,
		// published diagnostic port or changed network configuration.
		if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`).MatchString(v.Container) {
			return nil, errors.New("已配置的容器名无效")
		}
		dockerInspect = `docker inspect --format '{{.Id}} {{.State.Pid}} {{.State.Running}} {{.RestartCount}}' ` + shellQuote(v.Container)
		raw, e := m.io.exec(ctx, h, `test "$(id -u)" = 0 && command -v nsenter >/dev/null && command -v curl >/dev/null && `+dockerInspect, 10*time.Second)
		if e != nil {
			return nil, errors.New("Docker 接入需要已配置 SSH 账号具备 root、docker、nsenter 和 curl 权限；不自动提权")
		}
		dockerBefore, e = parseArthasDockerIdentity(raw)
		if e != nil {
			return nil, e
		}
		if !dockerBefore.Running || dockerBefore.PID == 0 {
			return nil, fmt.Errorf("容器未运行或正在重启（累计重启 %d 次）；请先查看应用启动日志，待容器稳定后重新检测", dockerBefore.Restarts)
		}
		hpid, cid = dockerBefore.PID, dockerBefore.ID
		command = "docker exec " + shellQuote(cid) + " sh -c " + shellQuote(arthasProcessScript(""))
	} else if v.RemoteDir == "" || !strings.HasPrefix(v.RemoteDir, "/") || v.RemoteDir == "/" {
		return nil, errors.New("应用部署目录未配置，无法确定所属 JVM")
	}
	raw, e := m.io.exec(ctx, h, command, 10*time.Second)
	if dockerInspect != "" {
		// A restart can race docker exec or finish before it returns. Recheck
		// even on successful output; never accept an old PID or retry attach.
		afterRaw, afterErr := m.io.exec(ctx, h, dockerInspect, 10*time.Second)
		if afterErr != nil {
			return nil, errors.New("无法复核容器运行状态；本次操作已停止，请重新检测并核对容器是否仍在运行")
		}
		after, parseErr := parseArthasDockerIdentity(afterRaw)
		if parseErr != nil {
			return nil, parseErr
		}
		if after != dockerBefore {
			return nil, arthasContainerUnstable(after.Restarts)
		}
	}
	if e != nil {
		return nil, errors.New("无法读取 JVM 身份；请检查 Linux /proc、sh、stat、readlink、sed、awk 和目录读取权限")
	}
	return parseArthasProcesses(raw, v, cid, hpid)
}
func (m *arthasDeploymentManager) audit(actor, id, state string) error {
	_, err := m.st.db.Exec(`INSERT INTO audit_events(actor,action,resource_type,resource_id,detail,created_at) VALUES(?,?,?,?,?,?)`, actor, "arthas-onboard", "deployment-instance", id, `{"state":`+strconv.Quote(state)+`}`, time.Now().Unix())
	return err
}
func (m *arthasDeploymentManager) save(rec arthasManagedRecord) error {
	raw, e := json.Marshal(rec)
	if e != nil {
		return e
	}
	sealed, e := m.st.encryptSecret(string(raw))
	if e != nil {
		return e
	}
	_, e = m.st.db.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, "arthas-managed:"+rec.Instance.ID, sealed)
	return e
}
func (m *arthasDeploymentManager) load(id string) (*arthasManagedRecord, error) {
	var sealed string
	if e := m.st.db.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, "arthas-managed:"+id).Scan(&sealed); e != nil {
		return nil, e
	}
	raw, e := m.st.decryptSecret(sealed)
	if e != nil {
		return nil, e
	}
	var rec arthasManagedRecord
	if json.Unmarshal([]byte(raw), &rec) != nil || rec.Instance.ID != id || !arthasRemoteDir.MatchString(rec.Directory) || rec.Port < 20000 || rec.Port > 49999 {
		return nil, errors.New("接入记录无效")
	}
	return &rec, nil
}
func (m *arthasDeploymentManager) verify(ctx context.Context, rec *arthasManagedRecord) (*Host, error) {
	v, h, e := m.resolve(rec.Instance.ID)
	if e != nil || v.Binding != rec.Binding {
		return nil, errors.New("实例部署配置已改变，请重新检测接入")
	}
	ps, e := m.inspect(ctx, *v, *h)
	if e != nil {
		return nil, e
	}
	for _, p := range ps {
		if p.Fingerprint == rec.Process.Fingerprint && p.HostPID == rec.Process.HostPID {
			return h, nil
		}
	}
	return nil, errors.New("容器或 JVM 已重启/替换；旧连接不会转接到新进程，请重新接入")
}

func (m *arthasDeploymentManager) remoteAPI(ctx context.Context, rec *arthasManagedRecord, input []byte) ([]byte, error) {
	h, e := m.verify(ctx, rec)
	if e != nil {
		return nil, e
	}
	// Authentication is read from a private file. Request bodies travel on stdin,
	// not the process command line; errors/output are never logged by deployment.
	command := "curl --silent --show-error --fail --noproxy '*' --max-time 13 --max-filesize 1048576 --config " + shellQuote(rec.Directory+"/http.conf") + " --header 'Content-Type: application/json' --data-binary @- " + shellQuote(fmt.Sprintf("http://127.0.0.1:%d/api", rec.Port))
	if rec.ContainerID != "" {
		command = fmt.Sprintf("nsenter -t %d -n -- ", rec.Process.HostPID) + command
	}
	return m.io.request(ctx, *h, command, input)
}
func arthasSSHRequest(ctx context.Context, h Host, command string, input []byte) ([]byte, error) {
	cli, e := sshClientContext(ctx, h, 15*time.Second)
	if e != nil {
		return nil, errors.New("SSH 通道连接失败")
	}
	defer cli.Close()
	stop := context.AfterFunc(ctx, func() { cli.Close() })
	defer stop()
	sess, e := cli.NewSession()
	if e != nil {
		return nil, errors.New("SSH 通道不可用")
	}
	defer sess.Close()
	sess.Stdin = bytes.NewReader(input)
	var output limitedArthasBuffer
	sess.Stdout = &output
	sess.Stderr = io.Discard
	if sess.Run(command) != nil || output.overflow {
		return nil, errors.New("Arthas 未响应、输出超限或认证失败；结果未确认，不自动重试")
	}
	var reply struct {
		State string `json:"state"`
	}
	if json.Unmarshal(output.Bytes(), &reply) != nil {
		return nil, errors.New("Arthas 返回无效响应")
	}
	return output.Bytes(), nil
}

type limitedArthasBuffer struct {
	bytes.Buffer
	overflow bool
}

func (b *limitedArthasBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if b.Len()+n > 1<<20 {
		b.overflow = true
		return 0, errors.New("output limit")
	}
	return b.Buffer.Write(p)
}

func (m *arthasDeploymentManager) onboard(ctx context.Context, actor string, v arthasDeployment, h Host, p arthasProcess) (*arthasManagedRecord, error) {
	m.mu.Lock()
	if m.busy[v.ID] || len(m.busy) >= 4 {
		m.mu.Unlock()
		return nil, errors.New("此实例正在接入，请等待并核对结果")
	}
	m.busy[v.ID] = true
	m.mu.Unlock()
	defer func() { m.mu.Lock(); delete(m.busy, v.ID); m.mu.Unlock() }()
	previous, loadErr := m.load(v.ID)
	if loadErr != nil && !errors.Is(loadErr, sql.ErrNoRows) {
		return nil, errors.New("已有接入记录无法读取，未重复附加，请检查存储与凭据密钥")
	}
	if previous != nil && previous.Process.Fingerprint == p.Fingerprint && previous.State != "failed" && previous.State != "preparing" {
		// Never attach again after an uncertain result. A later explicit request
		// may recover an already-started agent using the frozen credentials.
		if raw, e := m.remoteAPI(ctx, previous, []byte(`{"action":"init_session"}`)); e == nil {
			var reply map[string]any
			json.Unmarshal(raw, &reply)
			if reply["state"] == "SUCCEEDED" && reply["sessionId"] != nil {
				body, _ := json.Marshal(map[string]any{"action": "close_session", "sessionId": reply["sessionId"], "consumerId": reply["consumerId"]})
				m.remoteAPI(ctx, previous, body)
				previous.State = "ready"
				if e = m.save(*previous); e != nil {
					return nil, e
				}
				return previous, nil
			}
		}
		return nil, errors.New("上次接入结果未确认；未重复附加。请核对远端 Arthas 日志和进程，或在实例重启后重新接入")
	}
	pack, e := m.io.pack(ctx)
	if e != nil {
		return nil, e
	}
	var native []byte
	if strings.HasPrefix(p.AttachMode, "native-") {
		if m.io.native == nil {
			return nil, errors.New("轻量接入组件未配置")
		}
		native, e = m.io.native(ctx, strings.TrimPrefix(p.AttachMode, "native-"))
		if e != nil {
			return nil, e
		}
	}
	if e = m.audit(actor, v.ID, "starting"); e != nil {
		return nil, errors.New("无法记录接入审计，未操作目标")
	}
	b := make([]byte, 32)
	if _, e = rand.Read(b); e != nil {
		return nil, e
	}
	secret := hex.EncodeToString(b)
	// Paths and port numbers are public operational metadata, so generate them
	// independently rather than exposing any bytes of the authentication secret.
	nonce := make([]byte, 18)
	if _, e = rand.Read(nonce); e != nil {
		return nil, e
	}
	dir := "/tmp/devops-arthas-" + hex.EncodeToString(nonce[:16])
	port := 20000 + (int(nonce[16])*256+int(nonce[17]))%30000
	rec := arthasManagedRecord{Instance: v, Process: p, Directory: dir, Port: port, State: "preparing", Binding: v.Binding, ContainerID: p.ContainerID}
	started := false
	defer func() {
		if !started {
			rec.State = "failed"
			m.save(rec)
			m.audit(actor, v.ID, "failed")
		}
	}()
	if e = m.save(rec); e != nil {
		return nil, errors.New("无法保存接入状态，未操作目标")
	}
	if _, e = m.io.exec(ctx, h, "umask 077; mkdir -- "+shellQuote(dir), 10*time.Second); e != nil {
		return nil, errors.New("无法创建诊断目录；需要可写的 /tmp")
	}
	if e = m.io.upload(ctx, h, pack, dir+"/arthas.tar.gz"); e != nil {
		return nil, errors.New("Arthas 安装包传输失败，未启动 JVM 诊断")
	}
	props := fmt.Sprintf("arthas.config.overrideAll=true\narthas.ip=127.0.0.1\narthas.telnetPort=-1\narthas.httpPort=%d\narthas.username=arthas\narthas.password=%s\narthas.localConnectionNonAuth=false\narthas.sessionTimeout=900\n", port, secret)
	if e = m.io.upload(ctx, h, []byte("user = \"arthas:"+secret+"\"\n"), dir+"/http.conf"); e != nil {
		return nil, errors.New("诊断认证配置写入失败")
	}
	if _, e = m.io.exec(ctx, h, "mkdir "+shellQuote(dir+"/agent")+" && tar -xzf "+shellQuote(dir+"/arthas.tar.gz")+" -C "+shellQuote(dir+"/agent"), 10*time.Second); e != nil {
		return nil, errors.New("诊断包解压失败，需要宿主机 tar/gzip")
	}
	if e = m.io.upload(ctx, h, []byte(props), dir+"/agent/arthas.properties"); e != nil {
		return nil, errors.New("诊断配置写入失败")
	}
	if len(native) > 0 {
		if e = m.io.upload(ctx, h, native, dir+"/agent/jattach"); e != nil {
			return nil, errors.New("轻量接入组件上传失败，未附加")
		}
		if _, e = m.io.exec(ctx, h, "chmod 700 "+shellQuote(dir+"/agent/jattach"), 10*time.Second); e != nil {
			return nil, errors.New("轻量接入组件权限设置失败，未附加")
		}
	}
	// Revalidate immutable JVM identity after upload and immediately before attach.
	if _, e = m.verify(ctx, &rec); e != nil {
		return nil, e
	}
	// Use the documented core bootstrap directly: arthas-boot 4.0.5 probes
	// Telnet even in attach-only mode and fails when telnet-port is disabled.
	javaHome := strings.TrimSuffix(p.Java, "/bin/java")
	javaHome = strings.TrimSuffix(javaHome, "/jre")
	tools := javaHome + "/lib/tools.jar"
	start := "{ set --; if test -f " + shellQuote(tools) + "; then set -- " + shellQuote("-Xbootclasspath/a:"+tools) + "; fi; " + shellQuote(p.Java) + " \"$@\" -Xms32m -Xmx128m -jar " + shellQuote(dir+"/agent/arthas-core.jar") + " -pid " + strconv.Itoa(p.PID) + " -target-ip 127.0.0.1 -telnet-port -1 -http-port " + strconv.Itoa(port) + " -core " + shellQuote(dir+"/agent/arthas-core.jar") + " -agent " + shellQuote(dir+"/agent/arthas-agent.jar") + "; }"
	if len(native) > 0 {
		start = shellQuote(dir+"/agent/jattach") + " " + strconv.Itoa(p.PID) + " load instrument false " + shellQuote(dir+"/agent/arthas-agent.jar")
	}
	guard := fmt.Sprintf(`test "$(sed 's/.*) //' /proc/%d/stat | awk '{print $20}')" = %s && `, p.PID, shellQuote(p.Start))
	start = guard + start
	// Check the port in the same network namespace; never steal an existing agent.
	check := fmt.Sprintf(`test -z "$(awk '$2 ~ /:%04X$/ && $4 == "0A" {print $2}' /proc/net/tcp /proc/net/tcp6)"`, port)
	if v.Kind == "docker-host" {
		cid := shellQuote(p.ContainerID)
		cmd := "docker exec " + cid + " sh -c " + shellQuote(check) + " && docker cp " + shellQuote(dir+"/agent") + " " + shellQuote(p.ContainerID+":"+dir) + " && docker exec -u 0 " + cid + " chown -R " + strconv.Itoa(p.UID) + ":" + strconv.Itoa(p.GID) + " " + shellQuote(dir)
		if _, e = m.io.exec(ctx, h, cmd, 20*time.Second); e != nil {
			return nil, errors.New("容器诊断目录不可写、端口冲突或 docker cp 不可用；未启动诊断")
		}
		// docker cp source directory becomes dir itself, not dir/agent.
		start = strings.ReplaceAll(start, dir+"/agent", dir)
		start = "docker exec -u " + strconv.Itoa(p.UID) + ":" + strconv.Itoa(p.GID) + " " + cid + " sh -c " + shellQuote(start)
	} else {
		cmd := check + " && command -v curl >/dev/null && test \"$(id -u)\" = " + strconv.Itoa(p.UID)
		if h.User == "root" {
			cmd = check + " && command -v curl >/dev/null && command -v runuser >/dev/null && chmod 711 " + shellQuote(dir) + " && chown -R " + strconv.Itoa(p.UID) + ":" + strconv.Itoa(p.GID) + " " + shellQuote(dir+"/agent")
			start = "runuser -u " + shellQuote(p.User) + " -- sh -c " + shellQuote(start)
		}
		if _, e = m.io.exec(ctx, h, cmd, 10*time.Second); e != nil {
			return nil, errors.New("诊断端口冲突，或 SSH 账号与 JVM 用户不同；请配置同用户或 root SSH 连接及 curl/runuser")
		}
	}
	rec.State = "unknown"
	if e = m.save(rec); e != nil {
		return nil, errors.New("接入状态保存失败，未附加")
	}
	started = true
	_, e = m.io.exec(ctx, h, start+" >"+shellQuote(dir+"/attach.log")+" 2>&1", 45*time.Second)
	if e != nil {
		m.audit(actor, v.ID, "unknown")
		return nil, errors.New("Arthas 附加未确认；可能禁止 Attach、运行时缺少 Instrument 模块、已有诊断 Agent 或权限不足。未自动重试；远端日志：" + dir + "/attach.log")
	}
	raw, e := m.remoteAPI(ctx, &rec, []byte(`{"action":"init_session"}`))
	if e != nil {
		m.audit(actor, v.ID, "unknown")
		return nil, e
	}
	var reply map[string]any
	json.Unmarshal(raw, &reply)
	if reply["state"] != "SUCCEEDED" || reply["sessionId"] == nil {
		return nil, errors.New("Arthas 认证握手失败；未确认接入成功")
	}
	body, _ := json.Marshal(map[string]any{"action": "close_session", "sessionId": reply["sessionId"], "consumerId": reply["consumerId"]})
	m.remoteAPI(ctx, &rec, body)
	rec.State = "ready"
	if e = m.save(rec); e != nil {
		return nil, errors.New("Arthas 已启动但登记失败，请核对接入结果")
	}
	if e = m.audit(actor, v.ID, "ready"); e != nil {
		return nil, errors.New("接入已完成但审计结束状态保存失败，请核对记录")
	}
	return &rec, nil
}

// Fetch one pinned complete release on the platform, verify before extraction,
// and send a tar of required files; target machines need no Internet access.
func arthasManagedPackage(ctx context.Context) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", arthasDistributionURL, nil)
	client := &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect denied") }}
	res, e := client.Do(req)
	if e != nil {
		return nil, errors.New("平台无法下载固定 Arthas 4.0.5 安装包，请检查出网")
	}
	defer res.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if e != nil || res.StatusCode != 200 || fmt.Sprintf("%x", sha256.Sum256(raw)) != arthasDistributionSHA {
		return nil, errors.New("Arthas 安装包下载失败或 SHA-256 校验不匹配，未上传")
	}
	z, e := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if e != nil {
		return nil, e
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	wanted := map[string]bool{"arthas-boot.jar": false, "arthas-core.jar": false, "arthas-agent.jar": false, "arthas-spy.jar": false, "arthas-client.jar": false, "logback.xml": false}
	for _, f := range z.File {
		if _, ok := wanted[f.Name]; !ok {
			continue
		}
		if f.UncompressedSize64 > 24<<20 {
			return nil, errors.New("诊断包文件超限")
		}
		rc, e := f.Open()
		if e != nil {
			return nil, e
		}
		b, e := io.ReadAll(io.LimitReader(rc, 24<<20))
		rc.Close()
		if e != nil {
			return nil, e
		}
		if e = tw.WriteHeader(&tar.Header{Name: f.Name, Mode: 0600, Size: int64(len(b))}); e != nil {
			return nil, e
		}
		if _, e = tw.Write(b); e != nil {
			return nil, e
		}
		wanted[f.Name] = true
	}
	for _, ok := range wanted {
		if !ok {
			return nil, errors.New("Arthas 包内容不完整")
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes(), nil
}

func registerArthasDeploymentRoutes(mux *http.ServeMux, st *Store) {
	m := &arthasDeploymentManager{st: st, io: arthasDeploymentIO{exec: SSHExecContext, upload: SSHUploadContentContext, pack: arthasManagedPackage, request: arthasSSHRequest, native: arthasNativeAttachBinary}, busy: map[string]bool{}}
	registerArthasDeploymentHandler(mux, m)
}
func registerArthasDeploymentHandler(mux *http.ServeMux, m *arthasDeploymentManager) {
	secure := func(fn http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			if r.Header.Get("X-Role") != "admin" || strings.TrimSpace(r.Header.Get("X-User")) == "" || !sameOrigin(r) {
				arthasDeployError(w, 403, "仅管理员可接入部署实例进行 JVM 诊断")
				return
			}
			fn(w, r)
		}
	}
	mux.HandleFunc("GET /api/arthas-managed/instances", secure(func(w http.ResponseWriter, r *http.Request) {
		arthasDeployJSON(w, m.st.arthasDeployments(r.URL.Query().Get("service"), r.URL.Query().Get("environment")))
	}))
	mux.HandleFunc("GET /api/arthas-managed/instances/{id}", secure(func(w http.ResponseWriter, r *http.Request) {
		v, h, e := m.resolve(r.PathValue("id"))
		if e != nil {
			arthasDeployError(w, 404, e.Error())
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		ps, e := m.inspect(ctx, *v, *h)
		if e != nil {
			arthasDeployError(w, 409, e.Error())
			return
		}
		arthasDeployJSON(w, map[string]any{"instance": v, "processes": arthasPublicProcesses(ps)})
	}))
	mux.HandleFunc("POST /api/arthas-managed/instances/{id}/attach", secure(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Fingerprint string `json:"fingerprint"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil || !arthasOpaqueID.MatchString(in.Fingerprint) {
			arthasDeployError(w, 400, "请先检测并选择 JVM")
			return
		}
		v, h, e := m.resolve(r.PathValue("id"))
		if e != nil {
			arthasDeployError(w, 404, e.Error())
			return
		}
		// The operation owns a bounded server deadline. Closing the UI does not
		// abandon an in-flight attach and cause a later automatic replay.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 150*time.Second)
		defer cancel()
		ps, e := m.inspect(ctx, *v, *h)
		if e != nil {
			arthasDeployError(w, 409, e.Error())
			return
		}
		for _, p := range ps {
			if p.Fingerprint == in.Fingerprint {
				rec, e := m.onboard(ctx, r.Header.Get("X-User"), *v, *h, p)
				if e != nil {
					arthasDeployError(w, 409, e.Error())
					return
				}
				arthasDeployJSON(w, map[string]any{"id": rec.Instance.ID, "name": rec.Instance.Name, "service": rec.Instance.Service, "environment": rec.Instance.Environment, "fingerprint": rec.Process.Fingerprint})
				return
			}
		}
		arthasDeployError(w, 409, "JVM 已改变，请重新检测；未附加")
	}))
	mux.HandleFunc("POST /api/arthas-managed/instances/{id}/api", secure(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !arthasOpaqueID.MatchString(id) {
			arthasDeployError(w, 404, "实例不存在")
			return
		}
		rec, e := m.load(id)
		if e != nil || rec.State != "ready" {
			arthasDeployError(w, 409, "实例尚未接入，请先完成接入")
			return
		}
		body, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
		var in map[string]any
		if e != nil || json.Unmarshal(body, &in) != nil {
			arthasDeployError(w, 400, "诊断参数无效")
			return
		}
		// Monitor is the command policy boundary; this backend still rejects
		// unsupported actions and requires an audited administrator identity.
		action, _ := in["action"].(string)
		if action != "init_session" && action != "exec" && action != "close_session" && action != "interrupt_job" {
			arthasDeployError(w, 400, "诊断操作无效")
			return
		}
		if r.Header.Get("X-Arthas-Fingerprint") != rec.Process.Fingerprint {
			arthasDeployError(w, 409, "诊断目标版本不一致，请重新接入")
			return
		}
		if e = m.audit(r.Header.Get("X-User"), id, "api-"+action); e != nil {
			arthasDeployError(w, 500, "诊断审计不可用")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		raw, e := m.remoteAPI(ctx, rec, body)
		if e != nil {
			arthasDeployError(w, 409, e.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(raw)
	}))
}
func arthasDeployError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func arthasDeployJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
