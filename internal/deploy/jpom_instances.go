package deploy

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type jpomInstanceIdentity struct {
	PID                                    int
	Ticks, Boot, JarHash, Directory, State string
}

func (v *jpomInstanceIdentity) signature() string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s\n%s\n%s\n%d\n%s\n%s\n", v.Boot, v.JarHash, v.Directory, v.PID, v.Ticks, v.State))))
}

var jpomHashRE = regexp.MustCompile(`^[a-f0-9]{64}$`)
var jpomTicksRE = regexp.MustCompile(`^[0-9]+$`)
var jpomBootRE = regexp.MustCompile(`^[a-fA-F0-9-]{16,64}$`)

// Shared inspection/guard: exact project + basedir + main JAR, PID generation,
// boot identity and current JAR bytes. No port lookup, process command dump or lock.
func jpomInstanceProbe(sv *Service, proc string) string {
	return "set -euo pipefail\nproc=" + shellQuote(proc) + "\ndir=" + shellQuote(path.Clean(sv.RemoteDir)) + "\njar=" + shellQuote(path.Join(sv.RemoteDir, sv.DeployConfig["jpom_jar_name"])) + "\ntag=" + shellQuote("-DJpom.application="+sv.DeployConfig["jpom_project_id"]) + "\nbasedir=" + shellQuote("-DJpom.basedir="+path.Clean(sv.RemoteDir)) + "\n" + `
probe() {
 state=unknown; pid=0; ticks=0; real_dir=''; jar_hash=''; boot=''; java_version=''
 [ -d "$proc" ] && [ -d "$dir" ] || return 1
 real_dir=$(cd -P "$dir" && pwd) || return 1
 [ -f "$jar" ] && [ ! -L "$jar" ] || return 1
 jar_hash=$(sha256sum -- "$jar") || return 1; jar_hash=${jar_hash%% *}
 boot=$(cat "$proc/sys/kernel/random/boot_id") || return 1
 count=0
 for file in "$proc"/[0-9]*/cmdline; do
  [ -e "$file" ] || continue
  [ -r "$file" ] || return 1
  have_tag=0; have_dir=0; tag_count=0; dir_count=0; main=''; after=0
  while IFS= read -r -d '' arg; do
   if [ "$after" = 0 ]; then
    case "$arg" in -DJpom.application=*) tag_count=$((tag_count+1));; -DJpom.basedir=*) dir_count=$((dir_count+1));; esac
    [ "$arg" != "$tag" ] || have_tag=$((have_tag+1))
    [ "$arg" != "$basedir" ] || have_dir=$((have_dir+1))
    [ "$arg" != '-jar' ] || after=1
   elif [ "$after" = 1 ]; then main=$arg; after=2; fi
  done < "$file" 2>/dev/null || { [ ! -e "$file" ] || return 1; continue; }
  if [ "$have_tag" -eq 0 ] || [ "$have_dir" -eq 0 ]; then
   # A process already using this JAR is a conflict even when its tags differ.
   if [ -n "$main" ]; then
    case "$main" in /*) other_main=$main;; *) other_cwd=$(readlink "${file%/cmdline}/cwd") || return 1; other_main="$other_cwd/$main";; esac
    [ "$other_main" != "$jar" ] || return 1
   fi
   continue
  fi
  [ "$tag_count" -eq 1 ] && [ "$dir_count" -eq 1 ] || return 1
  candidate=${file%/cmdline}; candidate=${candidate##*/}
  kill -0 "$candidate" 2>/dev/null || continue
  line=$(cat "$proc/$candidate/stat" 2>/dev/null) || return 1
  rest=${line##*) }; read -r -a fields <<< "$rest"
  [ "${fields[0]:-}" != Z ] || continue
  count=$((count+1)); [ "$count" -eq 1 ] && [ "$have_tag" -eq 1 ] && [ "$have_dir" -eq 1 ] || return 1
  cwd=$(readlink "$proc/$candidate/cwd") || return 1
  case "$main" in /*) ;; *) main="$cwd/$main";; esac
  [ "$main" = "$jar" ] || return 1
  exe=$(readlink "$proc/$candidate/exe") || return 1
  case "$exe" in */bin/java) ;; *) return 1;; esac
  ticks=${fields[19]:-}; case "$ticks" in ''|*[!0-9]*) return 1;; esac
  again=$(cat "$proc/$candidate/stat" 2>/dev/null) || return 1
  [ "$again" = "$line" ] || {
   again=${again##*) }; read -r -a next_fields <<< "$again"
   [ "${next_fields[19]:-}" = "$ticks" ] && [ "${next_fields[0]:-}" != Z ] || return 1
  }
  pid=$candidate
  home=${exe%/bin/java}
  for release in "$home/release" "${home%/jre}/release"; do
   [ -r "$release" ] || continue
   java_version=$(sed -n 's/^JAVA_VERSION="\([0-9A-Za-z._+-]*\)"$/\1/p' "$release" | head -n 1)
   [ -z "$java_version" ] || break
  done
 done
 state=stopped; [ "$count" -eq 0 ] || state=running
}
signature() { printf '%s\n' "$boot" "$jar_hash" "$real_dir" "$pid" "$ticks" "$state" | sha256sum | cut -d ' ' -f 1; }
`
}
func jpomInstanceStatusCommand(sv *Service, proc string) string {
	return "bash -c " + shellQuote(jpomInstanceProbe(sv, proc)+`
if ! probe; then printf 'state=unknown\n'; exit 0; fi
printf 'state=%s\npid=%s\nticks=%s\nboot=%s\njar_hash=%s\ndirectory=%s\njava_version=%s\n' "$state" "$pid" "$ticks" "$boot" "$jar_hash" "$real_dir" "$java_version"
`)
}
func inspectJpomInstance(sv *Service, h Host, exec javaExec) applicationInstance {
	r := applicationInstance{ManagedJavaInstance: ManagedJavaInstance{Host: h.Name, Address: h.Addr, State: "unknown", CheckedAt: time.Now().Unix()}, Kind: sv.TargetType, JavaMode: "jpom", ProjectID: sv.DeployConfig["jpom_project_id"]}
	if e := validateJpomJava(sv); e != nil || strings.ContainsAny(sv.RemoteDir, "\r\n") {
		r.Message = "Jpom 实例配置无效，请核对原目录和项目"
		return r
	}
	out, e := exec(h, jpomInstanceStatusCommand(sv, "/proc"), 20*time.Second)
	if e != nil {
		r.State, r.DiagnosticCode, r.Message = classifyInstanceReadError(out, e)
		return r
	}
	fields := map[string]string{}
	if len(out) > 8192 {
		r.Message = "实例状态响应无效"
		return r
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok || fields[k] != "" {
			r.Message = "实例状态响应无效"
			return r
		}
		fields[k] = v
	}
	pid, err := strconv.Atoi(fields["pid"])
	if err != nil || pid < 0 || !jpomHashRE.MatchString(fields["jar_hash"]) || !jpomBootRE.MatchString(fields["boot"]) || !jpomTicksRE.MatchString(fields["ticks"]) || !path.IsAbs(fields["directory"]) || (fields["state"] != "running" && fields["state"] != "stopped") || (fields["state"] == "running" && (pid == 0 || fields["ticks"] == "0")) || (fields["state"] == "stopped" && (pid != 0 || fields["ticks"] != "0")) {
		r.Message = "原目录、JAR 或进程归属无法唯一核验，请人工检查"
		return r
	}
	r.State, r.PID = fields["state"], pid
	r.JavaVersion = fields["java_version"]
	r.jpom = &jpomInstanceIdentity{PID: pid, Ticks: fields["ticks"], Boot: fields["boot"], JarHash: fields["jar_hash"], Directory: fields["directory"], State: r.State}
	r.Owned, r.Controllable = true, true
	r.Message = "Jpom 共享实例；当前 JAR 版本未由平台发布记录核验"
	return r
}
func sameInstanceHost(a, b Host) bool {
	x, e := normalizeSSHAddress(a.Addr)
	y, f := normalizeSSHAddress(b.Addr)
	return e == nil && f == nil && x == y
}
func (m *instanceManager) jpomBindingOverlaps(t *ServiceEnvironment, sv *Service, h Host) bool {
	for _, app := range m.st.Services() {
		for _, other := range m.st.ServiceEnvironments(app.Name) {
			if app.Name == sv.Name && other.Environment == t.Environment {
				continue
			}
			if other.TargetType != "ssh-jar" || other.DeployConfig["java_mode"] != "jpom" || path.Clean(other.RemoteDir) != path.Clean(sv.RemoteDir) {
				continue
			}
			if other.DeployConfig["jpom_project_id"] != sv.DeployConfig["jpom_project_id"] && other.DeployConfig["jpom_jar_name"] != sv.DeployConfig["jpom_jar_name"] {
				continue
			}
			for _, n := range other.Hosts {
				if peer := m.st.GetHost(n); peer != nil && sameInstanceHost(*peer, h) {
					return true
				}
			}
		}
	}
	return false
}
func jpomIdentityGuard(sv *Service, r applicationInstance, proc string) (string, error) {
	if r.jpom == nil || !r.Owned || !jpomHashRE.MatchString(r.jpom.JarHash) {
		return "", fmt.Errorf("Jpom 实例归属未核验")
	}
	return jpomInstanceProbe(sv, proc) + "expected=" + shellQuote(r.jpom.signature()) + "\ncheck_original() { probe && [ \"$(signature)\" = \"$expected\" ]; }\ncheck_original || { echo 'Instance changed; refresh required' >&2; exit 21; }\n", nil
}
func jpomInstanceControlCommand(sv *Service, r applicationInstance, action string, env map[string]string, proc string) (string, error) {
	if action != "start" && action != "stop" && action != "restart" {
		return "", fmt.Errorf("Jpom 兼容模式支持启停或重启；业务就绪请核对健康接口")
	}
	script, e := jpomIdentityGuard(sv, r, proc)
	if e != nil {
		return "", e
	}
	script += "expected_jar=" + shellQuote(r.jpom.JarHash) + "\nexpected_dir=" + shellQuote(r.jpom.Directory) + "\nexpected_boot=" + shellQuote(r.jpom.Boot) + "\n"
	if action == "stop" || action == "restart" {
		script += `original_pid=$pid; original_ticks=$ticks
# Signal only the reviewed PID generation, never a newly discovered replacement.
check_original || exit 21
kill -TERM "$original_pid"
stopped=0
for ((i=0;i<30;i++)); do
 if ! kill -0 "$original_pid" 2>/dev/null; then stopped=1; break; fi
 line=$(cat "$proc/$original_pid/stat" 2>/dev/null) || { stopped=1; break; }
 rest=${line##*) }; read -r -a fields <<< "$rest"
 [ "${fields[19]:-}" = "$original_ticks" ] || exit 21
 if [ "${fields[0]:-}" = Z ]; then stopped=1; break; fi
 sleep 1
done
[ "$stopped" = 1 ] || { echo 'Stop not confirmed; inspect manually' >&2; exit 75; }
`
	}
	script += `probe && [ "$state" = stopped ] && [ "$jar_hash" = "$expected_jar" ] && [ "$real_dir" = "$expected_dir" ] && [ "$boot" = "$expected_boot" ] || { echo 'Instance or JAR changed; inspect manually' >&2; exit 21; }
`
	if action == "start" || action == "restart" {
		assignments, e := runtimeEnvironmentArguments(env)
		if e != nil {
			return "", e
		}
		prefix := ""
		if assignments != "" {
			prefix = "/usr/bin/env " + assignments + " "
		}
		start, _ := jpomJavaCommandsForEnv(sv, proc, prefix)
		script += start + "\nprobe && [ \"$state\" = running ] || exit 75\n"
	}
	script += `[ "$jar_hash" = "$expected_jar" ] && [ "$real_dir" = "$expected_dir" ] && [ "$boot" = "$expected_boot" ] || exit 21
printf 'JPOM_INSTANCE_ACK:%s\n' "$(signature)"
`
	return "bash -c " + shellQuote(script), nil
}
func jpomInstanceAck(out string) string {
	ack := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "JPOM_INSTANCE_ACK:") {
			v := strings.TrimPrefix(line, "JPOM_INSTANCE_ACK:")
			if ack != "" || !jpomHashRE.MatchString(v) {
				return ""
			}
			ack = v
		}
	}
	return ack
}

func runtimeEnvironmentArguments(values map[string]string) (string, error) {
	keys := make([]string, 0, len(values))
	for key, value := range values {
		if !runtimeEnvironmentKeyRE.MatchString(key) || strings.ContainsRune(value, 0) {
			return "", fmt.Errorf("运行变量格式无效")
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := []string{}
	for _, key := range keys {
		lines = append(lines, shellQuote(key+"="+values[key]))
	}
	return strings.Join(lines, " "), nil
}

// Runtime redaction inputs stay inside the backend. Only the bounded, redacted
// log body is returned by the route; no cmdline/environment dump reaches the UI.
func jpomInstanceLogCommand(sv *Service, r applicationInstance, proc string) (string, error) {
	script, e := jpomIdentityGuard(sv, r, proc)
	if e != nil {
		return "", e
	}
	log := sv.DeployConfig["jpom_log_file"]
	if log == "" {
		id := sv.DeployConfig["jpom_project_id"]
		log = path.Join(path.Dir(sv.RemoteDir), id, id+".log")
	}
	script += "log=" + shellQuote(log) + "\n" + `
if [ "$state" = running ]; then
 file="$proc/$pid/fd/1"
 target=$(readlink "$file") || exit 21
 [ "$target" = "$log" ] || [ "$target" = "$log (deleted)" ] || { echo 'Log destination differs; inspect configuration' >&2; exit 21; }
 [ -f "$file" ] || exit 21
else
 file="$log"; [ -f "$file" ] && [ ! -L "$file" ] || exit 21
fi
printf 'JPOM_LOG1\n'
if [ "$state" = running ]; then head -c 131073 -- "$proc/$pid/cmdline" | base64 | tr -d '\n'; fi
printf '\n'
if [ "$state" = running ]; then head -c 131073 -- "$proc/$pid/environ" | base64 | tr -d '\n'; fi
printf '\n'
tail -n 200 -- "$file" | tail -c 65536 | base64 | tr -d '\n'
printf '\n'
check_original || exit 21
`
	return "bash -c " + shellQuote(script), nil
}
func parseJpomInstanceLog(out string) (string, []string, error) {
	fail := func() (string, []string, error) {
		return "", nil, fmt.Errorf("Jpom 实例日志或脱敏信息无效，请刷新后核对")
	}
	if len(out) > 512<<10 {
		return fail()
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 4 || lines[0] != "JPOM_LOG1" {
		return fail()
	}
	raw := make([][]byte, 3)
	for i := range raw {
		v, e := base64.StdEncoding.DecodeString(lines[i+1])
		if e != nil {
			return fail()
		}
		raw[i] = v
	}
	if len(raw[0]) > 128<<10 || len(raw[1]) > 128<<10 || len(raw[2]) > 65536 {
		return fail()
	}
	secrets := []string{}
	for _, source := range raw[:2] {
		next := false
		for _, arg := range strings.Split(string(source), "\x00") {
			if next && arg != "" {
				secrets = append(secrets, arg)
				next = false
			}
			key, value, ok := strings.Cut(arg, "=")
			if jpomSensitiveArgument.MatchString(key) || strings.Contains(strings.ToLower(key), "pwd") {
				if ok && value != "" {
					secrets = append(secrets, value)
				} else if !ok {
					next = true
				}
			}
		}
	}
	return string(raw[2]), secrets, nil
}
