package deploy

import (
	"archive/zip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	healthFailureRollback = "rollback"
	healthFailureStop     = "stop"
	healthFailureKeep     = "keep"
)

func healthFailureAction(sv *Service) string {
	if sv != nil && sv.DeployConfig != nil {
		if action := strings.TrimSpace(sv.DeployConfig["health_failure_action"]); action != "" {
			return action
		}
	}
	return healthFailureRollback
}

func managedJava(sv *Service) bool {
	return (sv.TargetType == "ssh-jar" || sv.TargetType == TargetJavaBundle) && sv.DeployConfig["java_mode"] == "managed"
}

func validateManagedJava(sv *Service) error {
	c := sv.DeployConfig
	if sv.TargetType == TargetJavaBundle {
		if c["java_mode"] != "managed" || !bundleRelative(c["java_bundle_jar"], false) || !strings.HasSuffix(c["java_bundle_jar"], ".jar") {
			return fmt.Errorf("Java 整包需要平台托管模式和包内主 JAR 相对路径")
		}
		if strings.TrimSpace(c["java_workdir"]) != "" {
			return fmt.Errorf("Java 整包运行目录固定为当前版本包目录，不能关联旧版本目录")
		}
	}
	if !path.IsAbs(sv.RemoteDir) || path.Clean(sv.RemoteDir) == "/" {
		return fmt.Errorf("标准 Java 部署需要独立的绝对路径目录，不能使用根目录")
	}
	if c["java_home"] != "" && !path.IsAbs(c["java_home"]) {
		return fmt.Errorf("运行 JDK 目录必须是绝对路径；留空使用主机 PATH 中的 java")
	}
	if dir := strings.TrimSpace(c["java_workdir"]); dir != "" && !path.IsAbs(dir) {
		return fmt.Errorf("运行工作目录必须是绝对路径；留空使用本次发布版本目录")
	}
	switch c["java_version"] {
	case "8", "11", "17", "21", "25":
	default:
		return fmt.Errorf("请选择运行 JDK 版本")
	}
	if c["java_port"] != "" {
		port, err := strconv.Atoi(c["java_port"])
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("监听端口必须为 1–65535")
		}
	}
	if c["java_port"] == "" && strings.TrimSpace(sv.HealthURL) == "" {
		return fmt.Errorf("标准 Java 部署至少配置监听端口或健康检查 URL，避免仅凭进程启动判定发布成功")
	}
	for _, key := range []string{"java_home", "java_workdir", "java_args", "java_jvm_options"} {
		if strings.ContainsRune(c[key], 0) {
			return fmt.Errorf("Java 配置不可包含 NUL")
		}
	}
	for _, option := range javaArgumentLines(c["java_jvm_options"]) {
		if !strings.HasPrefix(option, "-") || option == "-jar" || option == "-cp" || option == "-classpath" {
			return fmt.Errorf("JVM 参数每行一项，不能覆盖 -jar 或 classpath")
		}
	}
	switch healthFailureAction(sv) {
	case healthFailureRollback, healthFailureStop, healthFailureKeep:
	default:
		return fmt.Errorf("健康检查失败策略仅支持 rollback、stop 或 keep")
	}
	return nil
}

func javaArgumentLines(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// Inspect application and launcher classes, excluding multi-release overrides.
// Dependency jars may have their own requirements; startup readiness still gates release.
func validateJavaBytecode(file string, javaVersion int) error {
	r, err := zip.OpenReader(file)
	if err != nil {
		return fmt.Errorf("无法读取 JAR: %w", err)
	}
	defer r.Close()
	for _, f := range r.File {
		if !strings.HasSuffix(f.Name, ".class") || strings.Contains(f.Name, "META-INF/versions/") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		var head [8]byte
		_, err = io.ReadFull(rc, head[:])
		rc.Close()
		if err != nil || binary.BigEndian.Uint32(head[:4]) != 0xcafebabe {
			return fmt.Errorf("无效的 Java class: %s", f.Name)
		}
		major := int(binary.BigEndian.Uint16(head[6:]))
		if major > javaVersion+44 {
			return fmt.Errorf("制品 %s 至少需要 Java %d，但运行环境选择了 Java %d", f.Name, major-44, javaVersion)
		}
	}
	return nil
}

func managedJavaScript(sv *Service, release string) string {
	c := sv.DeployConfig
	workdir := strings.TrimSpace(c["java_workdir"])
	if workdir == "" {
		workdir = release
	}
	jarPath := release + "/app.jar"
	if sv.TargetType == TargetJavaBundle {
		workdir = release + "/bundle"
		jarPath = workdir + "/" + c["java_bundle_jar"]
	}
	// The launcher runs on the target host, so the portable {host} placeholder
	// must resolve locally instead of being handed literally to curl.
	healthURL := strings.ReplaceAll(strings.TrimSpace(sv.HealthURL), "{host}", "127.0.0.1")
	java := "java"
	if c["java_home"] != "" {
		java = path.Join(c["java_home"], "bin/java")
	}
	quoteArgs := func(raw string) string {
		values := javaArgumentLines(raw)
		for i := range values {
			values[i] = shellQuote(values[i])
		}
		return strings.Join(values, " ")
	}
	return "#!/usr/bin/env bash\nset -euo pipefail\n" +
		"readonly jar=" + shellQuote(jarPath) + "\nreadonly java=" + shellQuote(java) + "\nreadonly pidfile=" + shellQuote(path.Join(sv.RemoteDir, ".devopsplatform.pid")) + "\n" +
		"readonly startReceipt=" + shellQuote(release+"/.start-state") + "\n" +
		"readonly workdir=" + shellQuote(workdir) + "\n" +
		"readonly envfile=" + shellQuote(release+"/runtime.env") + "\nreadonly logfile=" + shellQuote(path.Join(sv.RemoteDir, "logs/application.log")) + "\n" +
		"readonly port=" + shellQuote(c["java_port"]) + "\nreadonly health=" + shellQuote(healthURL) + "\nreadonly expected=" + shellQuote(c["java_version"]) + "\n" +
		"readonly -a jvm=(" + quoteArgs(c["java_jvm_options"]) + ")\nreadonly -a args=(" + quoteArgs(c["java_args"]) + ")\n" + `
record_start_state() {
  umask 077
  printf '%s\n' "$1" > "$startReceipt.tmp"
  mv -f -- "$startReceipt.tmp" "$startReceipt"
}
owned_pid() {
  [[ -f "$pidfile" ]] || return 1
  read -r pid < "$pidfile"
  [[ "$pid" =~ ^[0-9]+$ ]] || { echo "Invalid PID file; manual review required" >&2; exit 2; }
  kill -0 "$pid" 2>/dev/null || return 1
  [[ $(awk '{print $3}' "/proc/$pid/stat" 2>/dev/null || true) != Z ]] || return 1
  # Never stop a process based on PID alone (PID reuse / another application).
  if ! tr '\0' '\n' < "/proc/$pid/cmdline" | grep -Fqx -- "$jar"; then
    echo "PID belongs to another process; refusing to stop it" >&2; exit 2
  fi
}
listening() { (exec 3<>/dev/tcp/127.0.0.1/"$port") 2>/dev/null; }
health_ready() {
  [[ -z "$health" ]] && return 0
  health_detail=$(curl --fail --silent --show-error --output /dev/null --write-out 'HTTP %{http_code}' --connect-timeout 2 --max-time 3 "$health" 2>&1) && return 0
  return 1
}
diagnose_readiness() {
  [[ -z "$port" ]] || listening || echo "Port $port is not listening on 127.0.0.1" >&2
  [[ -z "$health" ]] || echo "Health check $health failed: ${health_detail:-no response}" >&2
  if [[ -f "$logfile" ]]; then
    echo "--- application.log (last 60 lines) ---" >&2
    tail -n 60 "$logfile" >&2 || true
    echo "--- end application.log ---" >&2
  fi
}
case "${1:-}" in
  check)
    [[ $(uname -s) == Linux ]] || { echo "Standard Java deployment requires Linux"; exit 2; }
    [[ -d "$workdir" && -x "$workdir" ]] || { echo "Runtime working directory is missing or inaccessible"; exit 2; }
    version=$("$java" -version 2>&1) || { echo "Selected Java is not executable"; exit 2; }
    major=$(printf '%s\n' "$version" | sed -n '1s/.*version "\([0-9]*\).*/\1/p')
    if [[ "$major" == 1 ]]; then major=$(printf '%s\n' "$version" | sed -n '1s/.*version "1\.\([0-9]*\).*/\1/p'); fi
    [[ "$major" == "$expected" ]] || { echo "Runtime JDK mismatch: expected $expected, detected $major"; exit 2; }
    [[ -z "$health" ]] || command -v curl >/dev/null || { echo "Health check needs curl"; exit 2; }
    echo "Runtime Java $major ready"
    ;;
  prestart)
    if [[ -n "$port" ]] && listening; then echo "Port $port is in use; new version was not started"; exit 21; fi
    ;;
  start)
    if owned_pid; then echo "Already running"; exit 0; fi
    if [[ -n "$port" ]] && listening; then record_start_state rejected-port; echo "Port $port is in use; new version was not started; will not stop an unmanaged process"; exit 21; fi
    record_start_state launching
    umask 077
    # Bash before 4.4 treats empty arrays as unset under nounset. Expand only
    # populated arrays, preserving each argument literally and adding no empty arg.
    (cd "$workdir"; set -a; source "$envfile"; set +a; exec nohup "$java" ${jvm[@]+"${jvm[@]}"} -jar "$jar" ${args[@]+"${args[@]}"}) >> "$logfile" 2>&1 < /dev/null &
    echo $! > "$pidfile"
    read -r pid < "$pidfile"
    for ((i=0;i<40;i++)); do
      if ! kill -0 "$pid" 2>/dev/null; then
        status=0
        wait "$pid" || status=$?
        echo "Java failed to start (exit status $status)" >&2
        diagnose_readiness
        exit 20
      fi
      if { tr '\0' '\n' < "/proc/$pid/cmdline"; } 2>/dev/null | grep -Fqx -- "$jar"; then exit 0; fi
      sleep .1
    done
    echo "Java did not start within 4 seconds" >&2
    diagnose_readiness
    exit 75
    ;;
  stop)
    if ! owned_pid; then rm -f -- "$pidfile"; exit 0; fi
    kill -TERM "$pid"
    # JDK shutdown hooks may drain multiple pools before the JVM exits.
    deadline=$((SECONDS + 60))
    while kill -0 "$pid" 2>/dev/null && [[ $(awk '{print $3}' "/proc/$pid/stat" 2>/dev/null || true) != Z ]]; do
      if (( SECONDS >= deadline )); then
        echo "Graceful shutdown timed out after 60 seconds; refusing forced kill"; exit 2
      fi
      sleep 1
    done
    rm -f -- "$pidfile"
    ;;
  ready)
    health_detail="not checked"
    for ((i=0;i<60;i++)); do
      [[ -f "$pidfile" ]] || { echo "PID receipt is missing; result needs review"; exit 75; }
      owned_pid || { echo "Java exited before becoming ready" >&2; diagnose_readiness; exit 20; }
      if { [[ -z "$port" ]] || listening; } && health_ready; then exit 0; fi
      sleep 2
    done
    echo "Readiness check timed out after 120 seconds" >&2
    diagnose_readiness
    exit 75
    ;;
  agent-probe)
    if [[ -f "$startReceipt" ]] && [[ $(cat "$startReceipt") == rejected-port ]]; then
      [[ ! -e "$pidfile" ]] || { echo "Rejected-start receipt conflicts with PID receipt; review required"; exit 31; }
      echo "Original start was rejected because its port was occupied; no new process was launched"
      exit 21
    fi
    [[ -f "$pidfile" ]] || { echo "PID receipt is missing; ownership needs review"; exit 30; }
    owned_pid || { echo "Managed application process exited"; exit 20; }
    health_detail="not checked"
    if { [[ -z "$port" ]] || listening; } && health_ready; then echo "Managed process and configured readiness checks passed"; exit 0; fi
    echo "Managed application is still starting"
    exit 10
    ;;
  *) echo "Usage: $0 check|start|stop|ready"; exit 2;;
esac
`
}

func (d *Deployer) deployManagedJava(id int64, sv *Service, host Host, artifact string) string {
	return d.deployManagedJavaRemote(id, sv, host, artifact, javaRemote{SSHExec, SSHUpload, SSHUploadContent})
}

type javaRemote struct {
	exec   func(Host, string, time.Duration) (string, error)
	upload func(Host, string, string) error
	write  func(Host, []byte, string) error
}

// managedJavaLockAcquireCommand uses an owned lease directory instead of a
// bare mkdir lock. Empty locks from older releases are recovered immediately;
// owned locks are only reclaimed after 30 minutes and only when the directory
// contains the two known metadata files.
func managedJavaLockAcquireCommand(remoteDir, owner string) string {
	base := path.Clean(remoteDir)
	return "base=" + shellQuote(base) + "; lock=\"$base/.deploy-lock\"; requested_owner=" + shellQuote(owner) + `; now=$(date +%s); acquired=0; ` +
		`if mkdir "$lock" 2>/dev/null; then acquired=1; else ` +
		`existing_owner=$(cat "$lock/owner" 2>/dev/null || true); started=$(cat "$lock/started_at" 2>/dev/null || stat -c %Y "$lock" 2>/dev/null || echo 0); ` +
		`case "$started" in ''|*[!0-9]*) started=0;; esac; age=$((now-started)); recover=0; ` +
		`if [ -z "$existing_owner" ]; then recover=1; echo 'Recovered legacy empty deployment lock'; ` +
		`elif [ "$age" -gt 1800 ] && [ "${existing_owner#agent:}" = "$existing_owner" ]; then recover=1; echo "Recovered stale deployment lock owned by $existing_owner (${age}s)"; fi; ` +
		`if [ "$recover" -eq 1 ]; then rm -f "$lock/owner" "$lock/started_at"; if rmdir "$lock" 2>/dev/null && mkdir "$lock" 2>/dev/null; then acquired=1; fi; fi; fi; ` +
		`[ "$acquired" -eq 1 ] || { echo "deployment or instance operation is running (owner: ${existing_owner:-unknown}, age: ${age:-unknown}s)" >&2; exit 3; }; ` +
		`umask 077; printf '%s\n' "$requested_owner" > "$lock/owner"; printf '%s\n' "$now" > "$lock/started_at"`
}

func managedJavaLockReleaseCommand(remoteDir, owner string) string {
	base := path.Clean(remoteDir)
	return "base=" + shellQuote(base) + "; lock=\"$base/.deploy-lock\"; requested_owner=" + shellQuote(owner) + `; ` +
		`[ -d "$lock" ] || exit 0; existing_owner=$(cat "$lock/owner" 2>/dev/null || true); ` +
		`[ "$existing_owner" = "$requested_owner" ] || { echo "refusing to remove lock owned by ${existing_owner:-unknown}" >&2; exit 3; }; ` +
		`rm -f "$lock/owner" "$lock/started_at" && rmdir "$lock"`
}

func (d *Deployer) deployManagedJavaRemote(id int64, sv *Service, host Host, artifact string, remote javaRemote) string {
	return d.deployManagedJavaPreparedRemote(id, sv, host, remote, func(release string, _ func(string, string) bool) bool {
		if err := remote.upload(host, artifact, release+"/app.jar"); err != nil {
			d.emit(id, "✗ 上传制品失败: "+err.Error()+"\n")
			return false
		}
		return true
	})
}

func (d *Deployer) deployManagedJavaPreparedRemote(id int64, sv *Service, host Host, remote javaRemote, prepare func(string, func(string, string) bool) bool) string {
	base := path.Clean(sv.RemoteDir)
	release := fmt.Sprintf("%s/releases/deploy-%d", base, id)
	current := base + "/current"
	owner := sv.Name + "/" + sv.RuntimeEnvironment
	lockOwner := fmt.Sprintf("deploy-%d", id)
	var stepError error
	retainLock := false
	step := func(label, command string) bool {
		d.emit(id, "▶ "+label+"\n")
		out, err := remote.exec(host, command, 4*time.Minute)
		stepError = err
		if out != "" {
			d.emit(id, out)
		}
		if err != nil {
			d.emit(id, "✗ "+label+"失败: "+err.Error()+"\n")
			return false
		}
		return true
	}
	if sv.TargetType == TargetJavaBundle {
		guard := "base=" + shellQuote(base) + `; probe="$base"; while [ "$probe" != / ] && [ -n "$probe" ]; do [ ! -L "$probe" ] || { echo 'Managed bundle directory crosses a symbolic link' >&2; exit 2; }; probe="${probe%/*}"; done; `
		for _, rel := range []string{"releases", "logs", ".devopsplatform.pid", ".devopsplatform-owner", ".deploy-lock", ".deploy-lock/owner", ".deploy-lock/started_at"} {
			guard += "[ ! -L " + shellQuote(path.Join(base, rel)) + " ] && "
		}
		guard += "true"
		if !step("检查整包目录边界", guard) {
			return "failed"
		}
	}
	if !step("锁定独立部署目录", "mkdir -p "+shellQuote(base)+" && "+managedJavaLockAcquireCommand(base, lockOwner)) {
		return "failed"
	}
	defer func() {
		if retainLock {
			return
		}
		if out, err := remote.exec(host, managedJavaLockReleaseCommand(base, lockOwner), time.Minute); err != nil {
			d.emit(id, "⚠ 部署锁清理失败："+strings.TrimSpace(out)+" "+err.Error()+"\n")
		}
	}()
	// Reject other owners and non-platform current directories before changing anything.
	guard := "if [ -f " + shellQuote(base+"/.devopsplatform-owner") + " ]; then [ \"$(cat " + shellQuote(base+"/.devopsplatform-owner") + ")\" = " + shellQuote(owner) + " ]; else [ ! -e " + shellQuote(current) + " ] && [ ! -L " + shellQuote(current) + " ] && printf '%s' " + shellQuote(owner) + " > " + shellQuote(base+"/.devopsplatform-owner") + "; fi"
	if !step("检查目录归属", guard) {
		return "failed"
	}

	previous, err := remote.exec(host, "if [ -L "+shellQuote(current)+" ]; then readlink "+shellQuote(current)+"; elif [ -e "+shellQuote(current)+" ]; then exit 2; fi", time.Minute)
	previous = strings.TrimSpace(previous)
	if err != nil || (previous != "" && (!strings.HasPrefix(previous, base+"/releases/deploy-") || path.Dir(previous) != base+"/releases")) {
		d.emit(id, "✗ current 不是平台管理的版本链接\n")
		return "failed"
	}
	if !step("准备新版本", "umask 077; mkdir -p "+shellQuote(base+"/logs")+" "+shellQuote(base+"/releases")+" && mkdir "+shellQuote(release)) {
		return "failed"
	}
	if !prepare(release, step) {
		return "failed"
	}
	if err := remote.write(host, []byte(managedJavaScript(sv, release)), release+"/service.sh"); err != nil {
		d.emit(id, "✗ 上传启动脚本失败\n")
		return "failed"
	}
	var env strings.Builder
	keys := make([]string, 0, len(sv.RuntimeEnv))
	for k := range sv.RuntimeEnv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env.WriteString(k + "=" + shellQuote(sv.RuntimeEnv[k]) + "\n")
	}
	if err := remote.write(host, []byte(env.String()), release+"/runtime.env"); err != nil {
		d.emit(id, "✗ 写入环境变量失败\n")
		return "failed"
	}
	command := func(dir, action string) string { return "bash " + shellQuote(dir+"/service.sh") + " " + action }
	if !step("检查运行 JDK", "chmod 700 "+shellQuote(release+"/service.sh")+" && chmod 600 "+shellQuote(release+"/runtime.env")+" && "+command(release, "check")) {
		return "failed"
	}
	if previous != "" && !step("优雅停止旧版本", command(previous, "stop")) {
		return "failed"
	}
	switchTo := func(dir string) string {
		return "ln -sfn " + shellQuote(dir) + " " + shellQuote(current+".new") + " && mv -Tf " + shellQuote(current+".new") + " " + shellQuote(current)
	}
	rollback := func() string {
		if !step("停止失败版本", command(release, "stop")) {
			return "rollback-failed"
		}
		if previous == "" {
			_, _ = remote.exec(host, "rm -f "+shellQuote(current), time.Minute)
			d.emit(id, "首次发布失败，没有旧版本可回滚\n")
			return "failed"
		}
		if !step("恢复旧制品和旧环境变量", switchTo(previous)+" && "+command(previous, "start")+" && "+command(previous, "ready")) {
			return "rollback-failed"
		}
		return "rolled-back"
	}
	if !step("检查目标监听端口", command(release, "prestart")) {
		d.emit(id, "新版本尚未启动，发布失败；排除端口或连接问题后可重新发布\n")
		return "failed"
	}
	if !step("切换并启动新版本", switchTo(release)+" && "+command(release, "start")) {
		if javaStartRefused(stepError) {
			d.emit(id, "目标端口已被占用，新版本未启动；本次发布失败，不停止占用端口的进程\n")
			return "failed"
		}
		if !knownJavaExit(stepError) {
			retainLock = true
			d.emit(id, "启动结果待核对，保留进程和部署锁，不自动停止或回滚\n")
			return "needs_review"
		}
		return rollback()
	}
	if !step("验证进程及服务就绪", command(release, "ready")) {
		if !knownJavaExit(stepError) {
			retainLock = true
			d.emit(id, "应用仍在启动或结果未知，进入待核对；不自动停止、回滚或重发\n")
			return "needs_review"
		}
		switch healthFailureAction(sv) {
		case healthFailureStop:
			d.emit(id, "健康检查失败；按环境策略停止新版本，不恢复旧版本\n")
			if !step("停止未就绪的新版本", command(release, "stop")) {
				return "failed"
			}
			return "failed"
		case healthFailureKeep:
			d.emit(id, "⚠ 健康检查失败；按环境策略保留新版本运行，请立即排查或手工停止\n")
			return "failed"
		default:
			return rollback()
		}
	}
	d.emit(id, "✓ 标准 Java 发布完成，日志："+base+"/logs/application.log\n")
	return "success"
}

// Exit 21 is reserved for an explicitly rejected start, before spawning Java.
func javaStartRefused(err error) bool {
	var result interface{ ExitStatus() int }
	return errors.As(err, &result) && result.ExitStatus() == 21
}

func knownJavaExit(err error) bool {
	var result interface{ ExitStatus() int }
	return errors.As(err, &result) && result.ExitStatus() == 20
}

// ManagedJavaInstance 是标准 Java 部署在单台主机上的实时运行状态。
// 状态来自目标主机，不以历史发布记录代替进程事实。
type ManagedJavaInstance struct {
	Host      string `json:"host"`
	Address   string `json:"address"`
	State     string `json:"state"`
	PID       int    `json:"pid,omitempty"`
	Release   string `json:"release,omitempty"`
	DeployID  int64  `json:"deploy_id,omitempty"`
	Version   string `json:"version,omitempty"`
	Message   string `json:"message,omitempty"`
	CheckedAt int64  `json:"checked_at"`
}

type javaExec func(Host, string, time.Duration) (string, error)

func managedJavaStatusCommand(remoteDir string) string {
	return managedJavaStatusCommandFor(remoteDir, "app.jar")
}
func managedJavaStatusCommandFor(remoteDir, jar string) string {
	base := path.Clean(remoteDir)
	return "base=" + shellQuote(base) + "; jar_relative=" + shellQuote(jar) + `; current="$base/current"; pidfile="$base/.devopsplatform.pid"; ` +
		`if [ ! -L "$current" ]; then printf 'state=not-deployed\n'; exit 0; fi; ` +
		`release=$(readlink "$current") || exit 2; case "$release" in "$base"/releases/deploy-*) ;; *) printf 'state=unknown\nmessage=current link is not platform managed\n'; exit 0;; esac; ` +
		`printf 'release=%s\n' "${release##*/}"; ` +
		`if [ ! -f "$pidfile" ]; then printf 'state=stopped\n'; exit 0; fi; ` +
		`read -r pid < "$pidfile"; case "$pid" in ''|*[!0-9]*) printf 'state=unknown\nmessage=invalid pid file\n'; exit 0;; esac; ` +
		`printf 'pid=%s\n' "$pid"; ` +
		`if kill -0 "$pid" 2>/dev/null && tr '\0' '\n' < "/proc/$pid/cmdline" 2>/dev/null | grep -Fqx -- "$release/$jar_relative"; then printf 'state=running\n'; ` +
		`elif kill -0 "$pid" 2>/dev/null; then printf 'state=unknown\nmessage=pid belongs to another process\n'; else printf 'state=stopped\nmessage=stale pid file\n'; fi`
}

func inspectManagedJavaInstance(sv *Service, host Host, exec javaExec) ManagedJavaInstance {
	result := ManagedJavaInstance{Host: host.Name, Address: host.Addr, State: "unknown", CheckedAt: time.Now().Unix()}
	jar := "app.jar"
	if sv.TargetType == TargetJavaBundle {
		jar = "bundle/" + sv.DeployConfig["java_bundle_jar"]
	}
	out, err := exec(host, managedJavaStatusCommandFor(sv.RemoteDir, jar), time.Minute)
	if err != nil {
		result.State, result.Message = "unreachable", strings.TrimSpace(err.Error())
		return result
	}
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "state":
			result.State = value
		case "pid":
			result.PID, _ = strconv.Atoi(value)
		case "release":
			result.Release = value
			result.DeployID, _ = strconv.ParseInt(strings.TrimPrefix(value, "deploy-"), 10, 64)
		case "message":
			result.Message = value
		}
	}
	return result
}

func managedJavaControlCommand(remoteDir, action string) (string, error) {
	if action != "start" && action != "stop" && action != "restart" && action != "ready" {
		return "", fmt.Errorf("实例操作仅支持 start、stop、restart 或 ready")
	}
	base := path.Clean(remoteDir)
	operation := map[string]string{
		"start":   `bash "$release/service.sh" start && bash "$release/service.sh" ready`,
		"stop":    `bash "$release/service.sh" stop`,
		"restart": `bash "$release/service.sh" stop && bash "$release/service.sh" start && bash "$release/service.sh" ready`,
		"ready":   `bash "$release/service.sh" ready`,
	}[action]
	lockOwner := "instance-" + action
	releaseLock := managedJavaLockReleaseCommand(base, lockOwner)
	return managedJavaLockAcquireCommand(base, lockOwner) + "; trap " + shellQuote(releaseLock) + ` EXIT; current="$base/current"; [ -L "$current" ] || { echo 'application has not been deployed' >&2; exit 2; }; ` +
		`release=$(readlink "$current") || exit 2; case "$release" in "$base"/releases/deploy-*) ;; *) echo 'current link is not platform managed' >&2; exit 2;; esac; ` + operation, nil
}

func controlManagedJavaInstance(sv *Service, host Host, action string, exec javaExec) (ManagedJavaInstance, error) {
	command, err := managedJavaControlCommand(sv.RemoteDir, action)
	if err != nil {
		return ManagedJavaInstance{}, err
	}
	timeout := 3 * time.Minute
	if action == "stop" {
		timeout = time.Minute
	}
	out, err := exec(host, command, timeout)
	result := inspectManagedJavaInstance(sv, host, exec)
	if err != nil {
		message := strings.TrimSpace(out)
		if message == "" {
			message = err.Error()
		}
		return result, fmt.Errorf("%s", message)
	}
	return result, nil
}

func (d *Deployer) ManagedJavaInstances(sv *Service, hosts []Host) []ManagedJavaInstance {
	results := make([]ManagedJavaInstance, len(hosts))
	var wg sync.WaitGroup
	for i, host := range hosts {
		wg.Add(1)
		go func(i int, host Host) {
			defer wg.Done()
			results[i] = inspectManagedJavaInstance(sv, host, SSHExec)
			if results[i].DeployID > 0 && d.st != nil {
				if deployment := d.st.GetDeploy(results[i].DeployID); deployment != nil && deployment.Service == sv.Name && deployment.Host == host.Name {
					results[i].Version = deployment.Version
				}
			}
		}(i, host)
	}
	wg.Wait()
	return results
}

func (d *Deployer) ControlManagedJavaInstance(sv *Service, host Host, action string) (ManagedJavaInstance, error) {
	result, err := controlManagedJavaInstance(sv, host, action, SSHExec)
	if result.DeployID > 0 && d.st != nil {
		if deployment := d.st.GetDeploy(result.DeployID); deployment != nil && deployment.Service == sv.Name && deployment.Host == host.Name {
			result.Version = deployment.Version
		}
	}
	return result, err
}
