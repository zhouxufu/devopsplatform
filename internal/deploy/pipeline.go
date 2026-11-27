package deploy

import (
	"context"
	"encoding/json"
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
	baseService := sv
	buildEnvironment := strings.TrimSpace(opts.BuildEnvironment)
	if buildEnvironment == "" {
		buildEnvironment = strings.TrimSpace(opts.DeployEnvironment)
	}
	if buildEnvironment == "" && opts.ReplayDelivery == nil {
		_, required, err := pr.st.buildEnvironmentOptions(sv.Name)
		if err != nil {
			return 0, err
		}
		if required {
			return 0, fmt.Errorf("应用配置了环境构建方案，请先选择本次构建的目标环境")
		}
		environments, e := pr.st.automaticDocumentEnvironments(sv)
		if e != nil {
			return 0, e
		}
		if len(environments) > 0 {
			return 0, fmt.Errorf("应用启用了按环境生成接口文档，请选择本次构建目标环境")
		}

	}
	if buildEnvironment != "" {
		if opts.ReplayDelivery != nil {
			return 0, fmt.Errorf("原配置重试不能更换环境构建方案")
		}
		sv, err = pr.st.environmentBuildService(sv, buildEnvironment)
		if err != nil {
			return 0, err
		}
		if target := strings.TrimSpace(opts.DeployEnvironment); target != "" && target != buildEnvironment {
			expected, e := pr.st.environmentBuildService(baseService, target)
			if e != nil {
				return 0, e
			}
			if expected.BuildVariant != sv.BuildVariant || buildVariantDigest(expected, nil) != buildVariantDigest(sv, nil) {
				return 0, fmt.Errorf("构建方案与自动发布目标环境不兼容，未启动构建")
			}
		}
	}
	raw, err := serviceForPipelineRun(sv, opts)
	if err != nil {
		return 0, err
	}
	if buildEnvironment != "" && sv.BuildVariant != "" && buildVariantDigest(raw, nil) != buildVariantDigest(sv, nil) {
		return 0, fmt.Errorf("本次覆盖参数与环境构建方案不一致；请修改应用环境方案后重新构建")
	}
	delivery := pr.st.GetServiceDelivery(sv.Name)
	if opts.ReplayDelivery != nil {
		delivery = opts.ReplayDelivery
	}
	sv, err = pr.st.prepareServiceResources(raw, delivery)
	if err != nil {
		return 0, err
	}
	// A repository binding can supply settings.xml after the initial Maven
	// command was generated. Update the executable steps on the runtime copy.
	if sv.BuildTool == "maven" && sv.MavenSettingsPath != raw.MavenSettingsPath {
		sv, err = serviceForPipelineRun(sv, PipelineRunOptions{})
		if err != nil {
			return 0, err
		}
	}
	if err := pr.st.freezeAPIDocumentation(sv, opts.ReplayDelivery != nil, buildEnvironment); err != nil {
		return 0, err
	}
	raw.APIDocumentation = sv.APIDocumentation
	raw.DocumentEnvironment = sv.DocumentEnvironment
	steps := sv.Steps
	if len(steps) == 0 {
		steps = ExpandTemplate(*sv)
	}
	if err := pr.lib.ValidateBuildPipeline(steps); err != nil {
		return 0, err
	}
	if sv.BuildEnvironment.isolated() {
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
	r := &PipelineRun{Stages: pr.plannedStages(sv), Service: sv.Name, Trigger: trigger, Branch: sv.Branch,
		BuildCommand: sv.BuildCmd, AutoDeploy: autoDeploy, DeployEnvironment: strings.TrimSpace(opts.DeployEnvironment), Actor: actor, StartedAt: time.Now().Unix()}
	if err := pr.st.CreateRun(r); err != nil {
		releaseRunner()
		return 0, err
	}
	scope := opts.replayReasonScope
	if opts.ReplayDelivery == nil {
		scope = &buildReasonScope{Name: buildEnvironment, Protected: buildEnvironment != "" && !pr.st.environmentReasonOptional(buildEnvironment, false)}
	}
	if err := pr.st.saveBuildSnapshot(r.ID, raw, delivery, scope); err != nil {
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
	pr.emit(runID, fmt.Sprintf("▶ 流水线 %s %s（记录 ID %d）\n", pr.st.PipelineRunLabel(runID, sv.Name), sv.Name, runID))

	steps := pr.pipelineSteps(sv)
	planned, _ := json.Marshal(pr.plannedStages(sv))
	pr.st.db.Exec(`UPDATE pipeline_runs SET stages=? WHERE id=?`, string(planned), runID)

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
	compiled := map[string]time.Time{}

	for i, inst := range steps {
		if ctx.Err() != nil {
			break
		}
		if inst.Step == apiDocumentStage {
			name := documentStageName(sv)
			pr.st.UpdateStage(runID, i, Stage{Name: name, Status: "running", StartedAt: time.Now().Unix()})
			pr.emit(runID, fmt.Sprintf("\n━━ %s（%s）━━\n", name, apiDocumentStage))
			var e error
			if artifactRecord == nil {
				e = fmt.Errorf("未生成本次制品，不能关联文档")
			} else {
				e = pr.generateAPIDocument(ctx, runID, sv, artifactRecord, workspace, outputDir)
			}
			status := "success"
			if ctx.Err() != nil {
				status = "canceled"
			} else if e != nil {
				if sv.APIDocumentation.Source.Required {
					status = "failed"
					failed = true
				} else {
					status = "warning"
				}
				pr.emit(runID, "✗ 接口文档阶段："+e.Error()+"\n")
			}
			pr.st.UpdateStage(runID, i, Stage{Name: name, Status: status})
			if failed || ctx.Err() != nil {
				break
			}
			continue
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
		inputMetadata := map[string]string{}
		retainedJar := ""
		cleanupInput := func() {}
		if def.ID == "git-checkout" {
			compiled = map[string]time.Time{}
		}
		if def.ID == "docker-build" {
			params, metadata, cleanup, err := prepareDockerCompilation(def.RenderParams(inst), workspace, outputDir, compiled)
			if err != nil {
				pr.emit(runID, "✗ 镜像编译输入校验失败："+err.Error()+"\n")
				pr.st.UpdateStage(runID, i, Stage{Name: stageName, Status: "failed"})
				failed = true
				break
			}
			inst.Params, inputMetadata, cleanupInput = params, metadata, cleanup
			retainedJar, err = retainDockerJar(params, metadata, outputDir)
			if err != nil {
				cleanupInput()
				pr.emit(runID, "✗ 保留编译制品失败："+err.Error()+"\n")
				pr.st.UpdateStage(runID, i, Stage{Name: stageName, Status: "failed"})
				failed = true
				break
			}
		}
		_ = os.Remove(outputDir + "/.outputs") // 每个步骤独立输出，防止上一步骤的制品泄漏
		env := def.BuildEnv(inst, workspace, sv.Name, version, outputDir)
		for key, value := range sv.RuntimeEnv {
			env = append(env, key+"="+value)
		}
		compileStarted := time.Now()
		code := 0
		if def.ID == javaBundleStep {
			var started time.Time
			for _, at := range compiled {
				if started.IsZero() || at.Before(started) {
					started = at
				}
			}
			file, err := packageJavaBundle(ctx, workspace, outputDir, sv.Name, def.RenderParams(inst), started)
			if err == nil {
				err = os.WriteFile(filepath.Join(outputDir, ".outputs"), []byte("ARTIFACT="+file+"\nARTIFACT_KIND="+ArtifactJavaBundle+"\n"), 0600)
			}
			if err != nil {
				code = 1
				pr.emit(runID, "✗ 完整归档失败: "+err.Error()+"\n")
			}
		} else {
			code = pr.b.execBuildStepContext(ctx, sv, def, env, func(l string) { pr.emit(runID, l) })
		}
		if code == 0 && ctx.Err() == nil && def.ID == "docker-build" && retainedJar == "" && inst.Params["EXPORT_JAR_GLOB"] != "" {
			started := compiled["java-build"]
			if started.IsZero() {
				started = compileStarted
			}
			exportParams, exportMetadata, clean, exportErr := prepareDockerCompilation(map[string]string{"DOCKERFILE_SOURCE": "template", "DOCKERFILE_TEMPLATE": "java21", "CONTEXT": inst.Params["CONTEXT"], "DOCKERFILE_ARTIFACT_PATH": inst.Params["EXPORT_JAR_GLOB"]}, workspace, outputDir, map[string]time.Time{"java-build": started})
			if exportErr == nil {
				retainedJar, exportErr = retainDockerJar(exportParams, exportMetadata, outputDir)
			}
			clean()
			if exportErr != nil {
				cleanupInput()
				pr.emit(runID, "✗ 导出本次 JAR 失败："+exportErr.Error()+"\n")
				pr.st.UpdateStage(runID, i, Stage{Name: stageName, Status: "failed"})
				failed = true
				break
			}
			if inputMetadata == nil {
				inputMetadata = map[string]string{}
			}
			inputMetadata["related_output_digest"], inputMetadata["related_output_path"] = exportMetadata["compiled_input_digest"], exportMetadata["compiled_input_path"]
		}
		cleanupInput()
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
		if def.Kind == "build" {
			compiled[def.ID] = compileStarted
		}
		// Prepared delivery builds must not silently switch source during a custom step.
		if run := pr.st.GetRun(runID); strings.HasPrefix(triggerOfRun(run), "code-preparation:") {
			if err := verifyPreparedWorkspace(ctx, workspace, sv.Branch); err != nil {
				pr.emit(runID, "✗ "+err.Error()+"\n")
				pr.st.UpdateStage(runID, i, Stage{Name: stageName, Status: "failed"})
				failed = true
				break
			}
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
				if described.Metadata == nil {
					described.Metadata = map[string]string{}
				}
				described.Metadata["branch"], described.Metadata["build_command"], described.Metadata["trigger"] = sv.Branch, sv.BuildCmd, triggerOfRun(pr.st.GetRun(runID))
				if def.ID == "docker-build" {
					for key, value := range inputMetadata {
						described.Metadata[key] = value
					}
					for key, value := range dockerfileBuildMetadata(def.RenderParams(inst)) {
						described.Metadata[key] = value
					}
				}
				var jarRecord *Artifact
				if retainedJar != "" {
					jarDigest, jarRole := inputMetadata["compiled_input_digest"], "compilation"
					if jarDigest == "" {
						jarDigest = inputMetadata["related_output_digest"]
						jarRole = "build-output"
					}
					jar, jarErr := describeArtifact(sv.Name, version, ArtifactJar, retainedJar, jarDigest)
					if jarErr == nil {
						jar.Metadata = map[string]string{"artifact_role": jarRole, "branch": sv.Branch}
						jarRecord, jarErr = pr.st.RecordPipelineArtifact(runID, actorOfRun(pr.st.GetRun(runID)), jar)
					}
					if jarErr != nil {
						pr.emit(runID, "✗ 关联 JAR 登记失败："+jarErr.Error()+"\n")
						pr.st.UpdateStage(runID, i, Stage{Name: stageName, Status: "failed"})
						failed = true
						break
					}
					if jarRole == "compilation" {
						described.Metadata["compiled_artifact_id"] = fmt.Sprint(jarRecord.ID)
					} else {
						described.Metadata["related_artifact_id"] = fmt.Sprint(jarRecord.ID)
					}
				}
				artifactRecord, err = pr.st.RecordPipelineArtifact(runID, actorOfRun(pr.st.GetRun(runID)), described)
				if err != nil {
					pr.emit(runID, "✗ 制品入库失败: "+err.Error()+"\n")
					pr.st.UpdateStage(runID, i, Stage{Name: stageName, Status: "failed"})
					failed = true
					break
				}
				if jarRecord != nil {
					if err = pr.st.linkCompilationArtifact(runID, jarRecord, artifactRecord); err != nil {
						pr.emit(runID, "✗ 制品关联失败："+err.Error()+"\n")
						pr.st.UpdateStage(runID, i, Stage{Name: stageName, Status: "failed"})
						failed = true
						break
					}
					pr.emit(runID, fmt.Sprintf("✓ 本次构建保留 JAR #%d 与镜像 #%d，制品关联已核对\n", jarRecord.ID, artifactRecord.ID))
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
	if ctx.Err() == nil && !failed && artifactRecord != nil {
		if _, err := pr.st.db.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, fmt.Sprintf("build-ready:%d", runID), fmt.Sprintf("%d:%s", artifactRecord.ID, artifactRecord.Digest)); err != nil {
			pr.emit(runID, "✗ 构建完成证明保存失败，禁止发布\n")
			failed = true
		}
	}
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
	if target.ManagedTarget() || target.ScriptTarget() {
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
	artifact := pr.st.artifactForEnvironmentRun(r, target.Environment)
	if artifact == nil {
		return fmt.Errorf("本次构建没有与目标环境发布方式匹配的制品")
	}
	if err := pr.st.artifactBuildComplete(artifact); err != nil {
		return err
	}
	if err := pr.st.validateArtifactBuildVariant(artifact, target.Environment); err != nil {
		return err
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
