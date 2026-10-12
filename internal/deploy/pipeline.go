package deploy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// PipelineRunner 流水线编排：sync → build → artifact → deploy。
// 阶段状态推进 + 日志广播（hub key = 1e6+runID）；deploy 阶段可自动（auto_deploy+deploy_host）
// 或手动续接（API 收到主机后 ContinueDeploy）。
type PipelineRunner struct {
	st  *Store
	b   *Builder
	hub *logHub
	dp  *Deployer
	lib *StepLib
	mu  sync.Mutex
	// activeByRunner 在单节点部署进程内落实 Runner 的并发上限。
	activeByRunner    map[string]int
	secretValuesByRun map[int64][]string
	cancellations     map[int64]context.CancelFunc
}

func NewPipelineRunner(st *Store, b *Builder, hub *logHub, dp *Deployer, lib *StepLib) *PipelineRunner {
	return &PipelineRunner{st: st, b: b, hub: hub, dp: dp, lib: lib, activeByRunner: map[string]int{}, secretValuesByRun: map[int64][]string{}, cancellations: map[int64]context.CancelFunc{}}
}

func (pr *PipelineRunner) claimRunner(name string, maxParallel int) (func(), bool) {
	if maxParallel < 1 {
		maxParallel = 1
	}
	pr.mu.Lock()
	defer pr.mu.Unlock()
	if pr.activeByRunner[name] >= maxParallel {
		return nil, false
	}
	pr.activeByRunner[name]++
	return func() {
		pr.mu.Lock()
		defer pr.mu.Unlock()
		if pr.activeByRunner[name] <= 1 {
			delete(pr.activeByRunner, name)
		} else {
			pr.activeByRunner[name]--
		}
	}, true
}

// Start 发起流水线（异步）。branch 非空时仅本次运行覆盖服务配置的分支。
func (pr *PipelineRunner) Start(sv *Service, autoDeploy bool, branch, actor string) (int64, error) {
	return pr.StartWithOptions(sv, autoDeploy, PipelineRunOptions{Branch: branch}, actor)
}

func (pr *PipelineRunner) StartWithOptions(sv *Service, autoDeploy bool, opts PipelineRunOptions, actor string) (int64, error) {
	var err error
	raw, err := serviceForPipelineRun(sv, opts)
	if err != nil {
		return 0, err
	}
	delivery := pr.st.GetServiceDelivery(sv.Name)
	if opts.ReplayDelivery != nil {
		delivery = opts.ReplayDelivery
	}
	sv, err = pr.st.prepareServiceResources(raw, delivery)
	if err != nil {
		return 0, err
	}
	steps := sv.Steps
	if len(steps) == 0 {
		steps = ExpandTemplate(*sv)
	}
	if err := pr.lib.ValidateBuildPipeline(steps); err != nil {
		return 0, err
	}
	if sv.BuildEnvironment.isolated() {
		for _, inst := range steps {
			if inst.Step == "docker-build" {
				return 0, fmt.Errorf("隔离环境中不支持 Docker 镜像构建步骤，请使用本机 Runner")
			}
		}
		if err := checkBuildDocker(); err != nil {
			return 0, err
		}
	}
	runnerName := delivery.Runner
	if runnerName == "" {
		runnerName = "local"
	}
	runner := pr.st.GetRunner(runnerName)
	if runner == nil || !runner.Enabled {
		return 0, fmt.Errorf("Runner %s 不可用", runnerName)
	}
	releaseRunner, ok := pr.claimRunner(runnerName, runner.MaxParallel)
	if !ok {
		return 0, fmt.Errorf("%w: Runner %s 已达到最大并发 %d", ErrRunConflict, runnerName, runner.MaxParallel)
	}
	trigger := strings.TrimSpace(opts.Trigger)
	if trigger == "" {
		trigger = "manual"
	}
	r := &PipelineRun{Service: sv.Name, Trigger: trigger, Branch: sv.Branch,
		BuildCommand: sv.BuildCmd, AutoDeploy: autoDeploy, DeployEnvironment: strings.TrimSpace(opts.DeployEnvironment), Actor: actor, StartedAt: time.Now().Unix()}
	if err := pr.st.CreateRun(r); err != nil {
		releaseRunner()
		return 0, err
	}
	if err := pr.st.saveBuildSnapshot(r.ID, raw, delivery); err != nil {
		pr.st.FinishRun(r.ID, "failed")
		releaseRunner()
		return 0, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	pr.mu.Lock()
	pr.cancellations[r.ID] = cancel
	pr.mu.Unlock()
	pr.registerRunSecrets(r.ID, sv.RuntimeSecrets)
	go func() {
		defer cancel()
		defer func() { pr.mu.Lock(); delete(pr.cancellations, r.ID); pr.mu.Unlock() }()
		defer releaseRunner()
		defer pr.registerRunSecrets(r.ID, nil)
		pr.runContext(ctx, r.ID, sv, autoDeploy)
	}()
	return r.ID, nil
}

func (pr *PipelineRunner) key(runID int64) int64 { return 1000000 + runID }

func (pr *PipelineRunner) registerRunSecrets(runID int64, values []string) {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	if len(values) == 0 {
		delete(pr.secretValuesByRun, runID)
		return
	}
	seen, clean := map[string]bool{}, []string{}
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			clean = append(clean, value)
		}
	}
	sort.Slice(clean, func(i, j int) bool { return len(clean[i]) > len(clean[j]) })
	pr.secretValuesByRun[runID] = clean
}

func (pr *PipelineRunner) redactRunLog(runID int64, line string) string {
	pr.mu.Lock()
	values := append([]string{}, pr.secretValuesByRun[runID]...)
	pr.mu.Unlock()
	for _, value := range values {
		line = strings.ReplaceAll(line, value, "******")
	}
	return line
}

func (pr *PipelineRunner) emit(runID int64, line string) {
	line = pr.redactRunLog(runID, line)
	pr.hub.emit(pr.key(runID), line)
	pr.st.AppendRunLog(runID, strings.TrimRight(line, "\n"))
}

// run 阶段推进；任一阶段失败即终止（deploy pending 留给手动/自动）
func (pr *PipelineRunner) run(runID int64, sv *Service, autoDeploy bool) {
	pr.runContext(context.Background(), runID, sv, autoDeploy)
}
func (pr *PipelineRunner) cancelBuild(id int64) error {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	if run := pr.st.GetRun(id); run == nil || run.Status != "running" {
		return fmt.Errorf("该构建不处于运行状态")
	}
	cancel := pr.cancellations[id]
	if cancel == nil {
		return fmt.Errorf("执行器不在当前进程，或已经进入发布阶段；不能直接取消")
	}
	cancel()
	delete(pr.cancellations, id)
	return nil
}
func (pr *PipelineRunner) runContext(ctx context.Context, runID int64, sv *Service, autoDeploy bool) {
	pr.emit(runID, fmt.Sprintf("▶ 流水线 #%d %s\n", runID, sv.Name))

	steps := sv.Steps
	if len(steps) == 0 {
		steps = ExpandTemplate(*sv)
	}
	// 阶段视图 = 步骤序列
	pr.st.SetStages(runID, len(steps))

	version := time.Now().Format("0102-1504")
	workspace := pr.b.buildDir(sv)
	outputDir := pr.b.artifactsDir + "/runs/" + fmt.Sprint(runID)
	// Containers must see the same absolute paths as host-side source/package steps.
	workspace, _ = filepath.Abs(workspace)
	outputDir, _ = filepath.Abs(outputDir)
	os.MkdirAll(outputDir, 0o755)
	artifact := ""
	artifactKind, artifactDigest := "", ""
	var artifactRecord *Artifact
	failed := false
	waitingDeploy := false

	for i, inst := range steps {
		if ctx.Err() != nil {
			break
		}
		def := pr.lib.Get(inst.Step)
		if def == nil {
			pr.emit(runID, "✗ 未知步骤: "+inst.Step+"\n")
			pr.st.UpdateStage(runID, i, Stage{Name: inst.Step, Status: "failed"})
			failed = true
			break
		}
		stageName := def.Name
		pr.st.UpdateStage(runID, i, Stage{Name: stageName, Status: "running", StartedAt: time.Now().Unix()})
		pr.emit(runID, fmt.Sprintf("\n━━ %s（%s）━━\n", stageName, inst.Step))

		// 部署类步骤：手动流水线必须显式确认；自动部署还要求步骤配置了主机。
		if def.Kind == "deploy" {
			pr.mu.Lock()
			delete(pr.cancellations, runID)
			pr.mu.Unlock()
			host := def.RenderParams(inst)["HOST"]
			if artifactRecord == nil {
				pr.emit(runID, "✗ 部署步骤缺少可用制品\n")
				pr.st.UpdateStage(runID, i, Stage{Name: stageName, Status: "failed"})
				failed = true
				break
			}
			if !def.AcceptsArtifact(artifactRecord.Kind) {
				pr.emit(runID, fmt.Sprintf("✗ 步骤 %s 不接受 %s 制品\n", inst.Step, artifactRecord.Kind))
				pr.st.UpdateStage(runID, i, Stage{Name: stageName, Status: "failed"})
				failed = true
				break
			}
			if !autoDeploy || host == "" {
				pr.emit(runID, "⏸ 制品已就绪，等待选择环境并确认部署\n")
				pr.st.UpdateStage(runID, i, Stage{Name: stageName, Status: "waiting"})
				waitingDeploy = true
				break
			}
			pr.st.SetRunDeployHost(runID, host)
			deployService := serviceForDeployStep(sv, def.Driver, def.RenderParams(inst))
			dstatus := pr.deployment(runID, deployService, host, *artifactRecord, i, "")
			if dstatus != "success" {
				failed = true
				pr.st.UpdateStage(runID, i, Stage{Name: stageName, Status: "failed"})
				break
			}
			pr.st.UpdateStage(runID, i, Stage{Name: stageName, Status: "success"})
			continue
		}

		// shell 执行体：run.sh + 环境变量注入（产物契约经 .outputs 文件回传）
		_ = os.Remove(outputDir + "/.outputs") // 每个步骤独立输出，防止上一步骤的制品泄漏
		env := def.BuildEnv(inst, workspace, sv.Name, version, outputDir)
		for key, value := range sv.RuntimeEnv {
			env = append(env, key+"="+value)
		}
		code := pr.b.execBuildStepContext(ctx, sv, def, env, func(l string) { pr.emit(runID, l) })
		if ctx.Err() != nil {
			pr.st.UpdateStage(runID, i, Stage{Name: stageName, Status: "canceled"})
			break
		}
		if code != 0 {
			pr.emit(runID, fmt.Sprintf("✗ %s 失败（退出码 %d）\n", stageName, code))
			pr.st.UpdateStage(runID, i, Stage{Name: stageName, Status: "failed"})
			failed = true
			break
		}
		// 读取步骤输出（COMMIT/ARTIFACT）
		if outs := readOutputs(outputDir); outs != nil {
			if def.ID == "git-checkout" {
				// Trust actual checked-out HEAD, not arbitrary later step output.
				resolve := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
				resolve.Dir = workspace
				if commit, err := resolve.Output(); err == nil && fullBuildCommit.MatchString(strings.TrimSpace(string(commit))) {
					pr.st.db.Exec(`UPDATE build_run_snapshots SET commit_ref=? WHERE run_id=?`, strings.TrimSpace(string(commit)), runID)
				}
			}
			if v := outs["COMMIT"]; v != "" {
				version = v
			}
			if a := outs["ARTIFACT"]; a != "" {
				artifact = a
				artifactKind = outs["ARTIFACT_KIND"]
				if artifactKind == "" {
					artifactKind = def.Produces
				}
				if artifactKind == "" {
					artifactKind = artifactKindForService(sv.ArtifactType)
				}
				artifactDigest = outs["ARTIFACT_DIGEST"]
				described, err := describeArtifact(sv.Name, version, artifactKind, artifact, artifactDigest)
				if err != nil {
					pr.emit(runID, "✗ 制品校验失败: "+err.Error()+"\n")
					pr.st.UpdateStage(runID, i, Stage{Name: stageName, Status: "failed"})
					failed = true
					break
				}
				described.Metadata = map[string]string{"branch": sv.Branch, "build_command": sv.BuildCmd, "trigger": triggerOfRun(pr.st.GetRun(runID))}
				artifactRecord, err = pr.st.RecordPipelineArtifact(runID, actorOfRun(pr.st.GetRun(runID)), described)
				if err != nil {
					pr.emit(runID, "✗ 制品入库失败: "+err.Error()+"\n")
					pr.st.UpdateStage(runID, i, Stage{Name: stageName, Status: "failed"})
					failed = true
					break
				}
			}
		}
		if ctx.Err() != nil {
			pr.st.UpdateStage(runID, i, Stage{Name: stageName, Status: "canceled"})
			break
		} else if failed {
			break
		}
		pr.st.UpdateStage(runID, i, Stage{Name: stageName, Status: "success"})
	}
	pr.st.SetRunVersion(runID, version, artifact)
	if ctx.Err() != nil {
		pr.emit(runID, "⏹ 构建已取消；不会自动发布\n")
		pr.st.FinishRun(runID, "canceled")
	} else if failed {
		pr.st.FinishRun(runID, "failed")
	} else if waitingDeploy {
		pr.st.SetRunStatus(runID, "waiting_deploy")
		pr.emit(runID, "流水线已暂停：请在运行详情确认目标环境\n")
	} else {
		pr.st.FinishRun(runID, "success")
	}
	pr.hub.Close(pr.key(runID))
}

func readOutputs(dir string) map[string]string {
	b, err := os.ReadFile(dir + "/.outputs")
	if err != nil {
		return nil
	}
	m := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		if kv := strings.SplitN(line, "=", 2); len(kv) == 2 {
			m[kv[0]] = kv[1]
		}
	}
	return m
}

// ContinueDeploy 手动续接部署阶段
func (pr *PipelineRunner) ContinueDeploy(runID int64, hostName, actor string) error {
	r := pr.st.GetRun(runID)
	if r == nil || r.Status != "waiting_deploy" || r.Artifact == "" {
		return fmt.Errorf("运行不可续接（需处于等待部署且制品就绪）")
	}
	sv := pr.st.GetService(r.Service)
	host := pr.st.GetHost(hostName)
	if sv == nil || host == nil {
		return fmt.Errorf("服务或主机不存在")
	}
	if active := pr.st.ActiveDeploy(r.Service, hostName); active != nil {
		return fmt.Errorf("%w: %s 在 %s 已有部署 #%d", ErrRunConflict, r.Service, hostName, active.ID)
	}
	if env := pr.st.ProtectedEnvironmentForHost(r.Service, hostName); env != nil {
		return fmt.Errorf("主机属于受保护环境 %s，请按环境续接部署", env.DisplayName)
	}
	stageIdx := waitingDeployStage(r.Stages)
	if stageIdx < 0 {
		return fmt.Errorf("找不到待部署阶段")
	}
	artifact := pr.artifactForRun(r)
	if artifact == nil {
		return fmt.Errorf("流水线制品记录不可用")
	}
	if !pr.st.ClaimRunDeploy(runID, hostName) {
		return fmt.Errorf("%w: 流水线已被其他请求续接", ErrRunConflict)
	}
	stageName := r.Stages[stageIdx].Name
	pr.st.UpdateStage(runID, stageIdx, Stage{Name: stageName, Status: "running", StartedAt: time.Now().Unix()})
	pr.emit(runID, "\n▶ 手动部署 → "+hostName+"\n")
	go func() {
		status := pr.deployment(runID, sv, hostName, *artifact, stageIdx, actor)
		pr.st.FinishRun(runID, status)
		pr.hub.Close(pr.key(runID))
	}()
	return nil
}

// ContinueDeployEnvironment 将待部署流水线续接到服务环境，按环境主机组策略执行。
func (pr *PipelineRunner) ContinueDeployEnvironment(runID int64, target *ServiceEnvironment, reason, actor string) error {
	r := pr.st.GetRun(runID)
	if r == nil || r.Status != "waiting_deploy" || r.Artifact == "" || target == nil || target.Service != r.Service {
		return fmt.Errorf("运行或环境不可续接")
	}
	stageIdx := waitingDeployStage(r.Stages)
	if stageIdx < 0 {
		return fmt.Errorf("找不到待部署阶段")
	}
	hosts := make([]Host, 0, len(target.Hosts))
	if target.ManagedTarget() {
		hosts = target.ExecutionHosts()
	} else {
		for _, name := range target.Hosts {
			host := pr.st.GetHost(name)
			if host == nil {
				return fmt.Errorf("环境主机不存在: %s", name)
			}
			if active := pr.st.ActiveDeploy(r.Service, name); active != nil {
				return fmt.Errorf("%w: 主机 %s 已有部署 #%d", ErrRunConflict, name, active.ID)
			}
			hosts = append(hosts, *host)
		}
	}
	if len(hosts) == 0 {
		return fmt.Errorf("环境未配置执行目标")
	}
	sv := pr.st.GetService(r.Service)
	if sv == nil {
		return fmt.Errorf("服务不存在")
	}
	artifact := pr.artifactForRun(r)
	if artifact == nil {
		return fmt.Errorf("流水线制品记录不可用")
	}
	runtimeEnv, err := pr.st.decryptRuntimeEnvironment(target.runtimeEnvEncrypted)
	if err != nil {
		return fmt.Errorf("应用环境变量解密失败")
	}
	target.RuntimeEnv = runtimeEnv
	if !pr.st.ClaimRunEnvironment(runID, target.Environment) {
		return fmt.Errorf("%w: 流水线已被其他请求续接", ErrRunConflict)
	}
	stageName := r.Stages[stageIdx].Name
	pr.st.UpdateStage(runID, stageIdx, Stage{Name: stageName, Status: "running", StartedAt: time.Now().Unix()})
	pr.emit(runID, fmt.Sprintf("\n▶ 部署到环境 %s → %s\n", target.Environment, strings.Join(target.ExecutionTargets(), ", ")))
	go func() {
		ids, err := pr.dp.StartArtifactReleaseBatch(target.Apply(sv), hosts, *artifact, target.Environment, reason, actor, target.MaxParallel)
		if err != nil {
			pr.emit(runID, "✗ 环境部署发起失败: "+err.Error()+"\n")
			pr.failStage(runID, stageIdx)
			pr.st.FinishRun(runID, "failed")
			pr.hub.Close(pr.key(runID))
			return
		}
		deadline := time.Now().Add(15 * time.Minute)
		status := "success"
		completed := false
		for time.Now().Before(deadline) {
			done, failed := true, false
			for _, id := range ids {
				dp := pr.st.GetDeploy(id)
				if dp == nil || dp.Status == "running" {
					done = false
				}
				if dp != nil && dp.Status != "running" && dp.Status != "success" {
					failed = true
				}
			}
			if done {
				completed = true
				if failed {
					status = "failed"
				}
				break
			}
			time.Sleep(time.Second)
		}
		if !completed {
			status = "failed"
		}
		if status == "success" {
			pr.doneStage(runID, stageIdx)
		} else {
			pr.failStage(runID, stageIdx)
		}
		pr.emit(runID, "环境部署结束："+status+"\n")
		pr.st.FinishRun(runID, status)
		pr.hub.Close(pr.key(runID))
	}()
	return nil
}

func waitingDeployStage(stages []Stage) int {
	for i, stage := range stages {
		if stage.Status == "waiting" {
			return i
		}
	}
	return -1
}

// deployment 委托 Deployer（状态机/回滚复用），其日志转发到本运行
func (pr *PipelineRunner) deployment(runID int64, sv *Service, hostName string, artifact Artifact, stageIdx int, actor string) string {
	host := pr.st.GetHost(hostName)
	if host == nil {
		pr.failStage(runID, stageIdx)
		return "failed"
	}
	if env := pr.st.ProtectedEnvironmentForHost(sv.Name, hostName); env != nil {
		pr.emit(runID, "✗ 主机属于受保护环境 "+env.DisplayName+"，自动直发已阻止，请改为人工按环境确认\n")
		pr.failStage(runID, stageIdx)
		return "failed"
	}
	if actor == "" {
		actor = "local"
		if r := pr.st.GetRun(runID); r != nil && r.Actor != "" {
			actor = r.Actor
		}
	}
	dID, err := pr.dp.StartArtifactRelease(sv, *host, artifact, "", "", actor)
	if err != nil {
		pr.emit(runID, "✗ 部署发起失败: "+err.Error()+"\n")
		pr.failStage(runID, stageIdx)
		return "failed"
	}
	// 转发 deploy 日志（订阅 deployer 的 key，直到部署结束）
	ch, unsub := pr.hub.Subscribe(-dID)
	defer unsub()
	timeout := time.NewTimer(15 * time.Minute)
	defer timeout.Stop()
	poll := time.NewTicker(time.Second)
	defer poll.Stop()
	finish := func() string {
		d := pr.st.GetDeploy(dID)
		status := "success"
		if d == nil || d.Status != "success" {
			status = "failed"
		}
		if status == "success" {
			pr.doneStage(runID, stageIdx)
		} else {
			pr.failStage(runID, stageIdx)
		}
		pr.emit(runID, fmt.Sprintf("部署结束：%s\n", status))
		return status
	}
	for {
		select {
		case <-timeout.C:
			pr.failStage(runID, stageIdx)
			return "failed"
		case <-poll.C:
			if d := pr.st.GetDeploy(dID); d != nil && d.Status != "running" {
				return finish()
			}
		case line, ok := <-ch:
			if !ok { // deployer 关闭通道 = 结束
				return finish()
			}
			pr.emit(runID, line)
		}
	}
}

func (pr *PipelineRunner) doneStage(runID int64, idx int) {
	var dur int64 = 3
	name := "deploy"
	if r := pr.st.GetRun(runID); r != nil && idx < len(r.Stages) {
		name = r.Stages[idx].Name
		dur = time.Now().Unix()*1000 - r.Stages[idx].StartedAt*1000
	}
	pr.st.UpdateStage(runID, idx, Stage{Name: name, Status: "success", DurMs: dur})
}

func (pr *PipelineRunner) failStage(runID int64, idx int) {
	name := "deploy"
	if r := pr.st.GetRun(runID); r != nil && idx < len(r.Stages) {
		name = r.Stages[idx].Name
	}
	pr.st.UpdateStage(runID, idx, Stage{Name: name, Status: "failed"})
}

func actorOfRun(run *PipelineRun) string {
	if run != nil && run.Actor != "" {
		return run.Actor
	}
	return "local"
}

func triggerOfRun(run *PipelineRun) string {
	if run != nil && run.Trigger != "" {
		return run.Trigger
	}
	return "manual"
}

func (pr *PipelineRunner) artifactForRun(run *PipelineRun) *Artifact {
	if run == nil {
		return nil
	}
	if artifact := pr.st.GetArtifact(run.ArtifactID); artifact != nil {
		return artifact
	}
	artifact, err := describeArtifact(run.Service, run.Version, run.ArtifactKind, run.Artifact, run.ArtifactDigest)
	if err != nil {
		return nil
	}
	return &artifact
}

func serviceForDeployStep(base *Service, driver string, params map[string]string) *Service {
	copyService := *base
	copyService.TargetType = driver
	switch driver {
	case "ssh-jar":
		copyService.RemoteDir, copyService.StartCmd = params["REMOTE_DIR"], params["START_CMD"]
		copyService.StopCmd, copyService.HealthURL = params["STOP_CMD"], params["HEALTH_URL"]
	case "ssh-static":
		copyService.RemoteDir, copyService.HealthURL = params["REMOTE_DIR"], params["HEALTH_URL"]
	case "docker-host":
		copyService.HealthURL = params["HEALTH_URL"]
		copyService.DeployConfig = map[string]string{
			"container_name": params["CONTAINER_NAME"], "ports": params["PORTS"],
			"env_file": params["ENV_FILE"], "run_args": params["RUN_ARGS"],
		}
	}
	return &copyService
}
