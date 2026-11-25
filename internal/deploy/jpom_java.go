package deploy

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

func jpomJava(sv *Service) bool {
	return sv != nil && sv.TargetType == "ssh-jar" && sv.DeployConfig["java_mode"] == "jpom"
}

var jpomProjectRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,191}$`)

func validateJpomJava(sv *Service) error {
	c := sv.DeployConfig
	if !path.IsAbs(sv.RemoteDir) || path.Clean(sv.RemoteDir) == "/" || strings.ContainsRune(sv.RemoteDir, 0) {
		return fmt.Errorf("Jpom 兼容发布需要应用 JAR 所在的绝对目录")
	}
	if !jpomProjectRE.MatchString(c["jpom_project_id"]) {
		return fmt.Errorf("请填写原 Jpom 项目 ID")
	}
	jar := c["jpom_jar_name"]
	if jar == "" || path.Base(jar) != jar || !strings.HasSuffix(strings.ToLower(jar), ".jar") || strings.ContainsAny(jar, "\x00\r\n") || jar == ".jar" {
		return fmt.Errorf("请填写原 Jpom JAR 文件名，不包含目录")
	}
	for _, key := range []string{"java_home", "java_workdir", "jpom_log_file"} {
		if v := c[key]; v != "" && (!path.IsAbs(v) || strings.ContainsAny(v, "\x00\r\n")) {
			return fmt.Errorf("%s 必须为绝对路径", key)
		}
	}
	switch c["java_version"] {
	case "8", "11", "17", "21", "25":
	default:
		return fmt.Errorf("请选择运行 JDK 版本")
	}
	for _, key := range []string{"java_args", "java_jvm_options"} {
		if strings.ContainsRune(c[key], 0) {
			return fmt.Errorf("Java 参数不可包含 NUL")
		}
	}
	for _, v := range javaArgumentLines(c["java_jvm_options"]) {
		if !strings.HasPrefix(v, "-") || v == "-jar" || v == "-cp" || v == "-classpath" {
			return fmt.Errorf("JVM 参数不能覆盖 JAR 或 classpath")
		}
	}
	if deploymentAgentTarget(c) {
		return fmt.Errorf("Jpom 兼容覆盖发布使用 SSH 通道，请切换执行通道")
	}
	return nil
}

func jpomJavaCommands(sv *Service) (string, string) { return jpomJavaCommandsFor(sv, "/proc") }
func jpomJavaCommandsFor(sv *Service, proc string) (string, string) {
	return jpomJavaCommandsForEnv(sv, proc, "")
}
func jpomJavaCommandsForEnv(sv *Service, proc, launchEnv string) (string, string) {
	c := sv.DeployConfig
	dir := path.Clean(sv.RemoteDir)
	project := c["jpom_project_id"]
	log := c["jpom_log_file"]
	if log == "" {
		log = path.Join(path.Dir(dir), project, project+".log")
	}
	work := c["java_workdir"]
	if work == "" {
		work = dir
	}
	java := "java"
	if c["java_home"] != "" {
		java = path.Join(c["java_home"], "bin/java")
	}
	jvm := javaArgumentLines(c["java_jvm_options"])
	jvm = append(jvm, "-DJpom.application="+project, "-DJpom.basedir="+dir)
	quote := func(v []string) string {
		for i := range v {
			v[i] = shellQuote(v[i])
		}
		return strings.Join(v, " ")
	}
	prefix := "set -eu\nproc=" + shellQuote(proc) + "\ntag=" + shellQuote("-DJpom.application="+project) + "\nbasedir=" + shellQuote("-DJpom.basedir="+dir) + "\n" + `
find_process() {
  pid=''; count=0
  test -d "$proc" || { echo 'Process directory is unavailable'; return 1; }
  for file in "$proc"/[0-9]*/cmdline; do
    [ -e "$file" ] || continue
    [ -r "$file" ] || { echo 'Cannot verify process ownership'; return 1; }
    have_tag=0; have_dir=0
    while IFS= read -r -d '' arg; do
      [ "$arg" != "$tag" ] || have_tag=1
      [ "$arg" != "$basedir" ] || have_dir=1
    done < "$file" 2>/dev/null || { [ ! -e "$file" ] || return 1; }
    if [ "$have_tag" = 1 ] && [ "$have_dir" = 1 ]; then
      candidate=${file%/cmdline}; candidate=${candidate##*/}
      kill -0 "$candidate" 2>/dev/null || continue
      state=$(awk '{print $3}' "$proc/$candidate/stat" 2>/dev/null || true)
      [ "$state" != Z ] || continue
      pid=$candidate; count=$((count+1))
    fi
  done
  [ "$count" -le 1 ] || { echo 'Multiple processes match this Jpom project; check manually'; return 1; }
}
`
	stop := prefix + `find_process || exit 21
[ -n "$pid" ] || { echo 'No matching application process'; exit 0; }
kill -TERM "$pid"
for ((i=0;i<30;i++)); do
  if ! kill -0 "$pid" 2>/dev/null || [ "$(awk '{print $3}' "$proc/$pid/stat" 2>/dev/null || true)" = Z ]; then echo 'Original application stopped'; exit 0; fi
  sleep 1
done
echo 'Application did not stop gracefully; check manually'; exit 21`
	start := prefix + "find_process || exit 21\n[ -z \"$pid\" ] || { echo 'Application is already running; check the concurrent publication manually'; exit 21; }\n" +
		"mkdir -p " + shellQuote(path.Dir(log)) + "\ncd " + shellQuote(work) + "\numask 077\nnohup " + launchEnv + shellQuote(java) + " " + quote(jvm) + " -jar " + shellQuote(path.Join(dir, c["jpom_jar_name"])) + " " + quote(javaArgumentLines(c["java_args"])) + " >> " + shellQuote(log) + " 2>&1 < /dev/null &\nstarted=$!\n" + `
# This mode verifies process launch only; application acceptance remains manual.
for ((i=0;i<40;i++)); do
  if ! kill -0 "$started" 2>/dev/null; then wait "$started" || true; echo 'Java exited before launch was confirmed'; exit 20; fi
  find_process || exit 21
  if [ "$pid" = "$started" ]; then echo "Application launched, pid=$started; verify the application manually"; exit 0; fi
  sleep .1
done
echo 'Launch not confirmed; process left untouched, check manually before another publication'; exit 75`
	return "bash -c " + shellQuote(start), "bash -c " + shellQuote(stop)
}
