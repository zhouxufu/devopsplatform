package deploy

import (
	"errors"
	"fmt"
	"logplatform/pkg/deploytask"
	"path"
	"sort"
	"strconv"
	"strings"
)

var errAgentCapacity = errors.New("Agent release concurrency is full")

func (d *Deployer) managedJavaAgentTask(id int64, sv *Service, h Host, a Artifact) (deploytask.Task, error) {
	if e := d.st.validateDeploymentAgent(sv, h); e != nil {
		return deploytask.Task{}, e
	}
	agent, e := deploymentAgentID(sv.DeployConfig, h.Name)
	if e != nil {
		return deploytask.Task{}, e
	}
	root := path.Clean(sv.RemoteDir)
	release := fmt.Sprintf("%s/releases/deploy-%d", root, id)
	taskID := fmt.Sprintf("deploy-%d", id)
	files := map[string]string{"service.sh": managedJavaScript(sv, release)}
	keys := []string{}
	for k := range sv.RuntimeEnv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var env strings.Builder
	secrets := []string{}
	for _, k := range keys {
		env.WriteString(k + "=" + shellQuote(sv.RuntimeEnv[k]) + "\n")
		if sv.RuntimeEnv[k] != "" {
			secrets = append(secrets, sv.RuntimeEnv[k])
		}
	}
	files["runtime.env"] = env.String()
	preamble := "base=" + shellQuote(root) + "; release=" + shellQuote(release) + "; owner=" + shellQuote(sv.Name+"/"+sv.RuntimeEnvironment) + "; "
	lease := `test "$(cat "$base/.deploy-lock/owner")" = ` + shellQuote("agent:"+taskID) + `; `
	owner := `test "$(cat "$base/.devopsplatform-owner")" = "$owner"; `
	prior := `previous=$(cat "$DEPLOY_TASK_DIR/previous"); if [ -n "$previous" ]; then test "$(readlink "$base/current")" = "$previous"; else test ! -e "$base/current" && test ! -L "$base/current"; fi; `
	install := `cp -- "$DEPLOY_ARTIFACT" "$release/app.jar"; chmod 600 "$release/app.jar"; `
	var descriptor *deploytask.BundleDescriptor
	if sv.TargetType == TargetJavaBundle {
		version, _ := strconv.Atoi(sv.DeployConfig["java_version"])
		m, e := inspectJavaBundle(a.URI, sv.Name, version)
		if e != nil {
			return deploytask.Task{}, e
		}
		b, e := m.Descriptor()
		if e != nil {
			return deploytask.Task{}, e
		}
		descriptor = &b
		install = `cp -R -- "$DEPLOY_TASK_DIR/unpacked/bundle" "$release/bundle"; `
	}
	prepare := preamble + lease + `if [ -f "$base/.devopsplatform-owner" ]; then ` + owner + `else test ! -e "$base/current" && test ! -L "$base/current"; printf '%s' "$owner" > "$base/.devopsplatform-owner"; fi; ` + prior + `umask 077
mkdir -p "$base/releases" "$base/logs"
mkdir "$release"
` + install + `cp -- "$DEPLOY_TASK_DIR/files/service.sh" "$release/service.sh"
cp -- "$DEPLOY_TASK_DIR/files/runtime.env" "$release/runtime.env"
chmod 700 "$release/service.sh"
chmod 600 "$release/runtime.env"
bash "$release/service.sh" check`
	stop := preamble + lease + owner + prior + `if [ -n "$previous" ]; then bash "$previous/service.sh" stop; fi`
	start := preamble + lease + owner + prior + `ln -sfn "$release" "$base/current.new"; mv -Tf "$base/current.new" "$base/current"
if bash "$release/service.sh" start; then exit 0; fi
result=0; bash "$release/service.sh" agent-probe || result=$?
if [ "$result" = 20 ] || [ "$result" = 21 ]; then exit "$result"; fi
exit 30`
	probe := preamble + lease + owner + `test "$(readlink "$base/current")" = "$release"; bash "$release/service.sh" agent-probe`
	t := deploytask.Task{Version: deploytask.Version, ID: taskID, DeployID: id, AgentID: agent, Host: h.Name, Service: sv.Name, Environment: sv.RuntimeEnvironment, Root: root, ArtifactDigest: a.Digest, ArtifactSize: a.Size, Files: files, Secrets: secrets, Bundle: descriptor, Steps: []deploytask.Step{{Name: "准备并核验冻结版本", Script: prepare, SyncRelease: true}, {Name: "优雅停止原版本", Script: stop, UncertainOnError: true}, {Name: "检查目标监听端口", Script: preamble + lease + owner + prior + `bash "$release/service.sh" prestart`}, {Name: "切换并启动新版本", Script: start, UncertainOnError: true, RollbackOnExit: true}}, Probe: probe, WarnAfterSeconds: 120}
	if healthFailureAction(sv) == healthFailureRollback {
		guard := preamble + lease + owner + `test "$(readlink "$base/current")" = "$release"; result=0; bash "$release/service.sh" agent-probe || result=$?; test "$result" = 20; previous=$(cat "$DEPLOY_TASK_DIR/previous"); test -n "$previous"; `
		restore := guard + `ln -sfn "$previous" "$base/current.new"; mv -Tf "$base/current.new" "$base/current"; if bash "$previous/service.sh" start; then exit 0; fi; exit 30`
		oldProbe := preamble + lease + owner + `previous=$(cat "$DEPLOY_TASK_DIR/previous"); test -n "$previous"; test "$(readlink "$base/current")" = "$previous"; if grep -q 'agent-probe)' "$previous/service.sh"; then bash "$previous/service.sh" agent-probe; else bash "$previous/service.sh" ready; fi`
		t.Rollback = &deploytask.RollbackPlan{Guard: guard, Steps: []deploytask.Step{{Name: "恢复冻结旧版本和原运行配置", Script: restore, UncertainOnError: true}}, Probe: oldProbe}
	}
	return t, t.Validate()
}
func (d *Deployer) queueManagedJavaAgent(id int64, sv *Service, h Host, a Artifact) string {
	t, e := d.managedJavaAgentTask(id, sv, h, a)
	if e != nil {
		d.emit(id, e.Error()+"\n")
		return "failed"
	}
	if e = d.st.enqueueAgentTask(t); e != nil {
		if errors.Is(e, errAgentCapacity) {
			return "agent_waiting"
		}
		d.emit(id, "Agent 任务保存失败："+e.Error()+"\n")
		return "failed"
	}
	d.emit(id, "✓ 冻结部署任务已持久保存，等待指定 Agent；启动缓慢仅待核对，不停止进程\n")
	return "agent_dispatched"
}
