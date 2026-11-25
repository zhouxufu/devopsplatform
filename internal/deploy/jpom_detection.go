package deploy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"reflect"
	"regexp"
	"strings"
	"time"
)

// Detection is read-only. Its result is a configuration draft, never an execution plan.
type jpomDetectionCandidate struct {
	RemoteDir string            `json:"remote_dir"`
	Config    map[string]string `json:"deploy_config"`
}
type jpomDetection struct {
	Status    string                  `json:"status"`
	Message   string                  `json:"message"`
	Candidate *jpomDetectionCandidate `json:"candidate,omitempty"`
}
type jpomProbe func(context.Context, Host, string) (string, error)

func jpomDetectionDirectory(v ServiceEnvironment) (string, error) {
	dir := v.RemoteDir
	if original := v.DeployConfig["jpom_original_dir"]; original != "" {
		dir = original
	}
	if !path.IsAbs(dir) || path.Clean(dir) == "/" || strings.ContainsAny(dir, "\x00\r\n") {
		return "", fmt.Errorf("请先填写应用部署目录")
	}
	dir = path.Clean(dir)
	// This is the exact layout used by the Jpom migration, not a substring or app-name guess.
	suffix := "/.devopsplatform/" + v.Service
	if strings.HasSuffix(dir, suffix) {
		dir = path.Join(strings.TrimSuffix(dir, suffix), "jar")
	}
	return dir, nil
}

func registerJpomDetectionRoutes(mux *http.ServeMux, st *Store, probe jpomProbe) {
	mux.HandleFunc("POST /api/services/{name}/environments/{environment}/java-detection", func(w http.ResponseWriter, r *http.Request) {
		if deliveryActor(r) != "local" && r.Header.Get("X-Role") != "admin" {
			deliveryWriteError(w, 403, "仅管理员可以检测主机上的 Java 部署配置")
			return
		}
		var v ServiceEnvironment
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&v) != nil {
			deliveryWriteError(w, 400, "检测请求无效")
			return
		}
		v.Service, v.Environment = r.PathValue("name"), r.PathValue("environment")
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		result, err := st.detectJpomJava(ctx, v, probe)
		if err != nil {
			deliveryWriteError(w, 400, err)
			return
		}
		deliveryJSON(w, result)
	})
}

func (s *Store) detectJpomJava(ctx context.Context, v ServiceEnvironment, probe jpomProbe) (jpomDetection, error) {
	env := s.GetEnvironment(v.Environment)
	if s.GetService(v.Service) == nil || env == nil || env.Protected || v.Protected || (env.Purpose != "development" && env.Purpose != "test") || v.TargetType != "ssh-jar" {
		return jpomDetection{}, fmt.Errorf("仅支持已登记应用在非受保护开发／测试环境的普通 JAR 检测")
	}
	dir, err := jpomDetectionDirectory(v)
	if err != nil {
		return jpomDetection{}, err
	}
	names := v.Hosts
	if v.HostGroup != "" {
		group := s.GetHostGroup(v.HostGroup)
		if group == nil || group.Environment != v.Environment {
			return jpomDetection{}, fmt.Errorf("请选择当前环境的主机组")
		}
		names = group.Hosts
	}
	if len(names) == 0 || len(names) > 16 {
		return jpomDetection{}, fmt.Errorf("请选择 1 至 16 台当前环境的发布主机")
	}
	seen := map[string]bool{}
	hosts := []Host{}
	for _, name := range names {
		h := s.GetHost(name)
		if seen[name] || h == nil || h.Environment != v.Environment {
			return jpomDetection{}, fmt.Errorf("发布主机重复、失效或不属于当前环境")
		}
		seen[name] = true
		hosts = append(hosts, *h)
	}
	project := v.DeployConfig["jpom_project_id"]
	if project != "" && !jpomProjectRE.MatchString(project) {
		return jpomDetection{}, fmt.Errorf("原 Jpom 项目 ID 格式无效")
	}
	var candidate *jpomDetectionCandidate
	for _, h := range hosts {
		output, e := probe(ctx, h, jpomDetectionCommand(dir, project, "/proc"))
		if e != nil {
			return jpomDetection{Status: "unavailable", Message: h.Name + "：主机检测未完成，请检查 SSH 连接与读取权限后重新识别；原配置未改变"}, nil
		}
		items, complete, e := parseJpomDetection(output, dir, project)
		if e != nil || !complete {
			return jpomDetection{Status: "incomplete", Message: h.Name + "：进程、JDK、日志或参数资料不完整，无法自动填入；请人工核对"}, nil
		}
		if len(items) > 1 {
			return jpomDetection{Status: "ambiguous", Message: h.Name + "：发现多个匹配的 Jpom 实例，请先核对原项目与目录；原配置未改变"}, nil
		}
		if len(items) == 0 {
			return jpomDetection{Status: "not_found", Message: h.Name + "：未发现该目录下运行的 Jpom JAR 实例，保留当前部署方式；停止的应用可手动填写原配置"}, nil
		}
		if candidate != nil && !reflect.DeepEqual(candidate, items[0]) {
			return jpomDetection{Status: "ambiguous", Message: "各主机的 Jpom 目录或运行参数不一致，请人工核对；原配置未改变"}, nil
		}
		candidate = items[0]
	}
	return jpomDetection{Status: "matched", Message: "已识别原 Jpom 实例并填入草稿，保存环境配置后生效", Candidate: candidate}, nil
}

// Only matched processes are emitted. No environment, file contents, or full host inventory is returned.
func jpomDetectionCommand(dir, project, proc string) string {
	script := "set -eu\nroot=" + shellQuote(proc) + "\nscope=" + shellQuote(dir) + "\nproject=" + shellQuote(project) + "\n" + `
enc() { printf '%s' "$1" | base64 | tr -d '\n'; }
count=0; incomplete=0
[ -d "$root" ] || exit 1
for file in "$root"/[0-9]*/cmdline; do
 [ -e "$file" ] || continue
 [ -r "$file" ] || { incomplete=1; continue; }
 args=(); tag=''; base=''; jar=''; after=0
 while IFS= read -r -d '' arg; do
  args+=("$arg")
  if [ "$after" = 0 ]; then
   case "$arg" in -DJpom.application=*) tag=${arg#*=};; -DJpom.basedir=*) base=${arg#*=};; -jar) after=1;; esac
  elif [ "$after" = 1 ]; then jar=$arg; after=2; fi
 done < "$file" 2>/dev/null || { [ ! -e "$file" ] || incomplete=1; continue; }
 [ "$base" = "$scope" ] && [ -n "$tag" ] || continue
 [ -z "$project" ] || [ "$tag" = "$project" ] || continue
 count=$((count+1)); [ "$count" -le 16 ] || { incomplete=1; break; }
 pid=${file%/cmdline}
 cwd=$(readlink "$pid/cwd" 2>/dev/null || true)
 exe=$(readlink "$pid/exe" 2>/dev/null || true)
 log=$(readlink "$pid/fd/1" 2>/dev/null || true)
 java_home=${exe%/bin/java}; release=''
 if [ -r "$java_home/release" ]; then release=$(sed -n '/^JAVA_VERSION=/p' "$java_home/release" | head -n 1); fi
 if [ -z "$release" ] && [ "${java_home%/jre}" != "$java_home" ] && [ -r "${java_home%/jre}/release" ]; then release=$(sed -n '/^JAVA_VERSION=/p' "${java_home%/jre}/release" | head -n 1); fi
 case "$jar" in /*) ;; *) jar="$cwd/$jar";; esac
 [ -f "$jar" ] || incomplete=1
 [ "$(wc -c < "$file")" -le 32768 ] || { incomplete=1; continue; }
 printf 'JPOM1\t'; enc "$cwd"; printf '\t'; enc "$exe"; printf '\t'; enc "$log"; printf '\t'; enc "$release"; printf '\t'
 base64 < "$file" | tr -d '\n'; printf '\n'
done
printf 'JPOM_DONE:%s\n' "$incomplete"
`
	return "bash -c " + shellQuote(script)
}

var jpomSensitiveArgument = regexp.MustCompile(`(?i)(password|passwd|secret|token|credential|access.?key|private.?key)`)
var jpomJavaRelease = regexp.MustCompile(`^JAVA_VERSION="(1\.)?(8|11|17|21|25)([.\-+_"].*)?$`)

func parseJpomDetection(raw, dir, project string) ([]*jpomDetectionCandidate, bool, error) {
	if len(raw) > 1<<20 {
		return nil, false, fmt.Errorf("检测结果超出限制")
	}
	result := []*jpomDetectionCandidate{}
	complete := false
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	for i, line := range lines {
		if line == "JPOM_DONE:0" || line == "JPOM_DONE:1" {
			if i != len(lines)-1 {
				return nil, false, fmt.Errorf("检测结果顺序无效")
			}
			complete = line == "JPOM_DONE:0"
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 6 || parts[0] != "JPOM1" {
			return nil, false, fmt.Errorf("检测结果无效")
		}
		values := make([]string, 5)
		for i, part := range parts[1:] {
			v, e := base64.StdEncoding.DecodeString(part)
			if e != nil {
				return nil, false, e
			}
			values[i] = string(v)
		}
		cwd, exe, log, release := values[0], values[1], values[2], values[3]
		argv := strings.Split(strings.TrimSuffix(values[4], "\x00"), "\x00")
		if len(argv) < 4 || !strings.HasSuffix(exe, "/bin/java") || !path.IsAbs(cwd) || !path.IsAbs(log) || log == "/dev/null" || strings.Contains(log, " (deleted)") {
			return nil, false, fmt.Errorf("实例资料不完整")
		}
		version := jpomJavaRelease.FindStringSubmatch(release)
		if len(version) == 0 {
			return nil, false, fmt.Errorf("JDK 版本未知")
		}
		config := map[string]string{"java_mode": "jpom", "java_version": version[2], "java_home": strings.TrimSuffix(exe, "/bin/java"), "java_workdir": cwd, "jpom_log_file": log}
		jvm := []string{}
		jarIndex := -1
		for j := 1; j < len(argv); j++ {
			arg := argv[j]
			if arg == "-jar" {
				jarIndex = j
				break
			}
			switch {
			case strings.HasPrefix(arg, "-DJpom.application="):
				if config["jpom_project_id"] != "" {
					return nil, false, fmt.Errorf("重复项目标识")
				}
				config["jpom_project_id"] = strings.TrimPrefix(arg, "-DJpom.application=")
			case strings.HasPrefix(arg, "-DJpom.basedir="):
				if config["jpom_original_dir"] != "" {
					return nil, false, fmt.Errorf("重复目录标识")
				}
				config["jpom_original_dir"] = strings.TrimPrefix(arg, "-DJpom.basedir=")
			default:
				jvm = append(jvm, arg)
			}
		}
		if jarIndex < 0 || jarIndex+1 >= len(argv) || config["jpom_original_dir"] != dir || (project != "" && config["jpom_project_id"] != project) {
			return nil, false, fmt.Errorf("实例身份不一致")
		}
		jar := argv[jarIndex+1]
		if !path.IsAbs(jar) {
			jar = path.Join(cwd, jar)
		}
		if path.Dir(path.Clean(jar)) != dir {
			return nil, false, fmt.Errorf("JAR 不在原目录")
		}
		config["jpom_jar_name"] = path.Base(jar)
		args := argv[jarIndex+2:]
		for _, arg := range append(append([]string{}, jvm...), args...) {
			key, _, _ := strings.Cut(arg, "=")
			if strings.ContainsAny(arg, "\n\r") || jpomSensitiveArgument.MatchString(key) {
				return nil, false, fmt.Errorf("参数需要人工核对")
			}
		}
		config["java_jvm_options"], config["java_args"] = strings.Join(jvm, "\n"), strings.Join(args, "\n")
		if e := validateJpomJava(&Service{TargetType: "ssh-jar", RemoteDir: dir, DeployConfig: config}); e != nil {
			return nil, false, e
		}
		result = append(result, &jpomDetectionCandidate{RemoteDir: dir, Config: config})
	}
	return result, complete, nil
}
