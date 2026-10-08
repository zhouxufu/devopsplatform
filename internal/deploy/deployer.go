package deploy

import (
	"fmt"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

// Deployer 部署状态机：上传→备份→停→换→启→健康检查；任一步失败自动回滚。
// 支持 ssh-jar 与 ssh-static。
type Deployer struct {
	st              *Store
	hub             *logHub
	busURL          string
	mu              sync.Mutex
	logs            map[int64]*logTail
	drivers         map[string]DeployDriver
	saeFactory      saeProviderFactory
	saePollInterval time.Duration
}

func NewDeployer(st *Store, hub *logHub, busURL string) *Deployer {
	d := &Deployer{st: st, hub: hub, busURL: busURL, drivers: map[string]DeployDriver{}, saeFactory: newAliyunSAEProvider, saePollInterval: 5 * time.Second}
	for _, driver := range builtinDeployDrivers() {
		d.drivers[driver.Manifest().ID] = driver
	}
	return d
}

// Deploy 部署执行记录
type Deploy struct {
	ID             int64  `json:"id"`
	Service        string `json:"service"`
	Version        string `json:"version"`
	Host           string `json:"host"`
	Environment    string `json:"environment,omitempty"`
	ArtifactDigest string `json:"artifact_digest,omitempty"`
	ArtifactID     int64  `json:"artifact_id,omitempty"`
	ArtifactKind   string `json:"artifact_kind,omitempty"`
	ArtifactURI    string `json:"artifact_uri,omitempty"`
	ExternalID     string `json:"external_id,omitempty"`
	Reason         string `json:"reason,omitempty"`
	Status         string `json:"status"` // running|success|failed|rolled-back
	Actor          string `json:"actor,omitempty"`
	Steps          string `json:"steps,omitempty"`
	StartedAt      int64  `json:"started_at"`
	EndedAt        int64  `json:"ended_at"`
}

// Start 对一台主机发起部署（异步）
func (d *Deployer) Start(sv *Service, host Host, artifact, version, actor string) (int64, error) {
	return d.StartRelease(sv, host, artifact, version, "", "", "", actor)
}

// StartRelease 发起带环境、制品摘要和发布原因的可审计部署。
func (d *Deployer) StartRelease(sv *Service, host Host, artifact, version, environment, digest, reason, actor string) (int64, error) {
	a, err := describeArtifact(sv.Name, version, artifactKindForService(sv.ArtifactType), artifact, digest)
	if err != nil {
		return 0, err
	}
	return d.StartArtifactRelease(sv, host, a, environment, reason, actor)
}

func (d *Deployer) StartArtifactRelease(sv *Service, host Host, artifact Artifact, environment, reason, actor string) (int64, error) {
	if err := d.validateRelease(sv, host, artifact); err != nil {
		return 0, err
	}
	dp := &Deploy{Service: sv.Name, Version: artifact.Version, Host: host.Name, Environment: environment,
		ArtifactID: artifact.ID, ArtifactKind: artifact.Kind, ArtifactURI: artifact.URI, ArtifactDigest: artifact.Digest,
		Reason: reason, Actor: actor, StartedAt: time.Now().Unix()}
	if err := d.st.CreateDeploy(dp); err != nil {
		return 0, err
	}
	go d.run(dp.ID, sv, host, artifact, artifact.Version)
	return dp.ID, nil
}

func validateRelease(sv *Service, host Host, artifact, digest string) (string, error) {
	artifact = strings.TrimSpace(artifact)
	if artifact == "" {
		return "", fmt.Errorf("制品路径为空")
	}
	info, err := os.Stat(artifact)
	if err != nil || info.IsDir() {
		return "", fmt.Errorf("制品文件不可用: %s", artifact)
	}
	actualDigest, _, err := artifactMetadata(artifact)
	if err != nil {
		return "", fmt.Errorf("读取制品: %w", err)
	}
	if digest != "" && actualDigest != digest {
		return "", fmt.Errorf("制品摘要不匹配，拒绝部署")
	}
	if strings.TrimSpace(host.Name) == "" || strings.TrimSpace(host.Addr) == "" {
		return "", fmt.Errorf("目标主机配置不完整")
	}
	if !path.IsAbs(strings.TrimSpace(sv.RemoteDir)) {
		return "", fmt.Errorf("远端目录必须是绝对路径")
	}
	if sv.TargetType != "ssh-static" && strings.TrimSpace(sv.StartCmd) == "" {
		return "", fmt.Errorf("服务未配置启动命令")
	}
	return actualDigest, nil
}

func (d *Deployer) validateRelease(sv *Service, host Host, artifact Artifact) error {
	if sv.TargetType != TargetAliyunSAE && (strings.TrimSpace(host.Name) == "" || strings.TrimSpace(host.Addr) == "") {
		return fmt.Errorf("目标主机配置不完整")
	}
	if strings.TrimSpace(host.Name) == "" {
		return fmt.Errorf("执行目标配置不完整")
	}
	driver := d.drivers[sv.TargetType]
	if driver == nil {
		return fmt.Errorf("未注册的部署 Driver: %s", sv.TargetType)
	}
	return driver.Validate(sv, host, artifact)
}

// StartReleaseBatch 按环境并发上限分批发布；任一批失败后取消尚未执行的主机。
func (d *Deployer) StartReleaseBatch(sv *Service, hosts []Host, artifact, version, environment, digest, reason, actor string, maxParallel int) ([]int64, error) {
	a, err := describeArtifact(sv.Name, version, artifactKindForService(sv.ArtifactType), artifact, digest)
	if err != nil {
		return nil, err
	}
	return d.StartArtifactReleaseBatch(sv, hosts, a, environment, reason, actor, maxParallel)
}

func (d *Deployer) StartArtifactReleaseBatch(sv *Service, hosts []Host, artifact Artifact, environment, reason, actor string, maxParallel int) ([]int64, error) {
	if len(hosts) == 0 {
		return nil, fmt.Errorf("环境未配置执行目标")
	}
	if maxParallel < 1 {
		maxParallel = 1
	}
	ids := make([]int64, 0, len(hosts))
	for _, host := range hosts {
		if err := d.validateRelease(sv, host, artifact); err != nil {
			return nil, err
		}
		if active := d.st.ActiveDeploy(sv.Name, host.Name); active != nil {
			return nil, fmt.Errorf("%w: %s 在 %s 已有部署 #%d", ErrRunConflict, sv.Name, host.Name, active.ID)
		}
	}
	for _, host := range hosts {
		dp := &Deploy{Service: sv.Name, Version: artifact.Version, Host: host.Name, Environment: environment,
			ArtifactID: artifact.ID, ArtifactKind: artifact.Kind, ArtifactURI: artifact.URI, ArtifactDigest: artifact.Digest,
			Reason: reason, Actor: actor, StartedAt: time.Now().Unix()}
		if err := d.st.CreateDeploy(dp); err != nil {
			for _, id := range ids {
				d.st.FinishDeploy(id, "canceled")
			}
			return nil, err
		}
		ids = append(ids, dp.ID)
	}
	go func() {
		for start := 0; start < len(hosts); start += maxParallel {
			end := start + maxParallel
			if end > len(hosts) {
				end = len(hosts)
			}
			var wg sync.WaitGroup
			for i := start; i < end; i++ {
				wg.Add(1)
				go func(i int) { defer wg.Done(); d.run(ids[i], sv, hosts[i], artifact, artifact.Version) }(i)
			}
			wg.Wait()
			failed := false
			for i := start; i < end; i++ {
				if dp := d.st.GetDeploy(ids[i]); dp == nil || dp.Status != "success" {
					failed = true
				}
			}
			if failed {
				for i := end; i < len(ids); i++ {
					d.st.AppendDeployLog(ids[i], "前序主机发布失败，本次部署已取消")
					d.st.FinishDeploy(ids[i], "canceled")
					d.hub.Close(-ids[i])
				}
				return
			}
		}
	}()
	return ids, nil
}

func (d *Deployer) emit(id int64, line string) { // 部署日志用负数 key 区分构建；同时累积落库
	d.hub.emit(-id, line)
	d.mu.Lock()
	if d.logs == nil {
		d.logs = map[int64]*logTail{}
	}
	lt := d.logs[id]
	if lt == nil {
		lt = &logTail{}
		d.logs[id] = lt
	}
	d.mu.Unlock()
	lt.append(strings.TrimRight(line, "\n"))
}

func (d *Deployer) run(id int64, sv *Service, host Host, artifact Artifact, version string) {
	d.emit(id, fmt.Sprintf("▶ 部署 %s v%s → %s (%s)\n", sv.Name, version, host.Name, host.Addr))
	runtimeService := *sv
	status := "failed"
	if err := d.prepareRuntimeEnvironment(id, &runtimeService, host); err != nil {
		d.emit(id, "✗ 写入应用环境变量失败: "+err.Error()+"\n")
	} else {
		status = d.deploySteps(id, &runtimeService, host, artifact, version)
	}
	d.emit(id, fmt.Sprintf("\n%s 部署结束：%s\n", statusIcon(status), status))
	d.mu.Lock()
	lt := d.logs[id]
	delete(d.logs, id)
	d.mu.Unlock()
	if lt != nil {
		d.st.AppendDeployLog(id, lt.String())
	}
	d.st.FinishDeploy(id, status)
	d.hub.Close(-id)
	// 发布事件进总线（monitor 图表标注发布时刻）
	dp := d.st.GetDeploy(id)
	environment, digest := "", ""
	if dp != nil {
		environment, digest = dp.Environment, dp.ArtifactDigest
	}
	d.publishEvent(sv.Name, version, host.Name, environment, digest, status)
}

func (d *Deployer) prepareRuntimeEnvironment(id int64, sv *Service, host Host) error {
	// Standard Java releases carry their own environment snapshot, so a failed
	// release can restore the previous code AND configuration together.
	if managedJava(sv) {
		return nil
	}
	if sv.TargetType == TargetAliyunSAE || sv.TargetType == "ssh-static" {
		return nil
	}
	file := ""
	shellFormat := sv.TargetType == "ssh-jar"
	if shellFormat {
		file = strings.TrimSuffix(sv.RemoteDir, "/") + "/.devopsplatform.env"
		if _, err := SSHExec(host, "mkdir -p "+shellQuote(sv.RemoteDir), time.Minute); err != nil {
			return err
		}
	} else {
		name := strings.Trim(safeReleaseRE.ReplaceAllString(sv.Name+"-"+sv.RuntimeEnvironment, "-"), "-")
		if name == "" {
			name = "application"
		}
		file = "/tmp/.devopsplatform-" + name + ".env"
	}
	if len(sv.RuntimeEnv) == 0 {
		_, err := SSHExec(host, "rm -f "+shellQuote(file), time.Minute)
		return err
	}
	keys := make([]string, 0, len(sv.RuntimeEnv))
	for key := range sv.RuntimeEnv {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var content strings.Builder
	for _, key := range keys {
		content.WriteString(key)
		content.WriteByte('=')
		if shellFormat {
			content.WriteString(shellQuote(sv.RuntimeEnv[key]))
		} else {
			content.WriteString(sv.RuntimeEnv[key])
		}
		content.WriteByte('\n')
	}
	temporary := file + ".new"
	if err := SSHUploadContent(host, []byte(content.String()), temporary); err != nil {
		return err
	}
	if _, err := SSHExec(host, "chmod 600 "+shellQuote(temporary)+" && mv "+shellQuote(temporary)+" "+shellQuote(file), time.Minute); err != nil {
		return err
	}
	sv.RuntimeEnvFile = file
	d.emit(id, fmt.Sprintf("✓ 已注入 %d 项应用环境变量 → %s\n", len(keys), file))
	return nil
}

func statusIcon(s string) string {
	if s == "success" {
		return "✓"
	}
	return "✗"
}

// deploySteps 按 target_type 分流
func (d *Deployer) deploySteps(id int64, sv *Service, host Host, artifact Artifact, version string) string {
	driver := d.drivers[sv.TargetType]
	if driver == nil {
		d.emit(id, "✗ 未注册的部署 Driver: "+sv.TargetType+"\n")
		return "failed"
	}
	return driver.Deploy(d, id, sv, host, artifact, version)
}

// deployStatic 前端静态部署：上传 tar.gz → 备份旧目录 → 清空 → 解压 → 完成
func (d *Deployer) deployStatic(id int64, sv *Service, host Host, artifact, version string) string {
	remote := strings.TrimSuffix(sv.RemoteDir, "/")
	step := func(name, cmd string) bool {
		d.emit(id, "\n$ "+cmd+"\n")
		out, err := SSHExec(host, cmd, 5*time.Minute)
		if out != "" {
			d.emit(id, out)
		}
		if err != nil {
			d.emit(id, fmt.Sprintf("✗ %s 失败: %v\n", name, err))
			return false
		}
		return true
	}
	if !step("准备目录", "mkdir -p "+remote) {
		return "failed"
	}
	d.emit(id, "⇪ 上传 "+artifact+"\n")
	if err := SSHUpload(host, artifact, remote+"/release.tar.gz"); err != nil {
		d.emit(id, "✗ 上传失败: "+err.Error()+"\n")
		return "failed"
	}
	rollback := fmt.Sprintf("cp -r %[1]s/current %[1]s/backup-%[2]s 2>/dev/null; true", remote, version)
	if !step("备份当前版本", rollback) {
		return "failed"
	}
	if !step("发布新版本", fmt.Sprintf("rm -rf %[1]s/current && mkdir -p %[1]s/current && tar -xzf %[1]s/release.tar.gz -C %[1]s/current --strip-components=1", remote)) {
		// 回滚
		SSHExec(host, fmt.Sprintf("rm -rf %[1]s/current && cp -r %[1]s/backup-%[2]s %[1]s/current", remote, version), 2*time.Minute)
		return "rolled-back"
	}
	d.emit(id, "\n✓ 静态资源发布完成 → "+remote+"/current\n")
	return "success"
}

// deployJar ssh-jar 状态机；返回最终状态（失败已回滚则 rolled-back）
func (d *Deployer) deployJar(id int64, sv *Service, host Host, artifact, version string) string {
	remoteJar := strings.TrimSuffix(sv.RemoteDir, "/") + "/app.jar"
	backup := remoteJar + ".bak-" + version
	startCmd := sv.StartCmd
	if sv.RuntimeEnvFile != "" {
		startCmd = "set -a; . " + shellQuote(sv.RuntimeEnvFile) + "; set +a; " + startCmd
	}
	step := func(name, cmd string) bool {
		d.emit(id, "\n$ "+cmd+"\n")
		out, err := SSHExec(host, cmd, 10*time.Minute)
		if out != "" {
			d.emit(id, out)
		}
		if err != nil {
			d.emit(id, fmt.Sprintf("✗ %s 失败: %v\n", name, err))
			return false
		}
		return true
	}
	fail := func(at string) string {
		d.emit(id, "\n↩ 开始回滚…\n")
		SSHExec(host, fmt.Sprintf("mv %s %s 2>/dev/null; %s", shellQuote(backup), shellQuote(remoteJar), startCmd), 2*time.Minute)
		return "rolled-back"
	}
	// 0. 目标目录
	if !step("准备目录", "mkdir -p "+shellQuote(sv.RemoteDir)) {
		return "failed"
	}
	// 1. 上传
	d.emit(id, "⇪ 上传制品 "+artifact+"\n")
	if err := SSHUpload(host, artifact, remoteJar+".new"); err != nil {
		d.emit(id, "✗ 上传失败: "+err.Error()+"\n")
		return "failed"
	}
	// 2. 备份旧版
	if !step("备份旧版", fmt.Sprintf("cp %s %s 2>/dev/null || echo 'no previous'", shellQuote(remoteJar), shellQuote(backup))) {
		return fail("backup")
	}
	// 3. 停服务（stop 为空时用 pkill 兜底）
	stopCmd := sv.StopCmd
	if stopCmd == "" {
		// 字符类技巧防 pkill 自匹配（自身命令行不含 "[/]" 字面量）
		pat := "[" + remoteJar[:1] + "]" + remoteJar[1:]
		stopCmd = "pkill -f '" + pat + "'; test $? -le 1" // 0=杀掉 1=无进程，143 已被 test 吸收为 0
	}
	if !step("停止服务", stopCmd) {
		return fail("stop")
	}
	// 4. 替换
	if !step("替换制品", fmt.Sprintf("mv %s %s", shellQuote(remoteJar+".new"), shellQuote(remoteJar))) {
		return fail("swap")
	}
	// 5. 启动
	if !step("启动服务", startCmd) {
		return fail("start")
	}
	// 6. 健康检查（轮询 health_url，最长 120s）
	if sv.HealthURL != "" {
		d.emit(id, "\n⏳ 健康检查 "+sv.HealthURL+"\n")
		if !d.healthCheckRemote(id, host, sv.HealthURL, 120*time.Second) {
			return fail("health")
		}
		d.emit(id, "✓ 健康检查通过\n")
	}
	SSHExec(host, "rm -f "+shellQuote(backup), time.Minute) // 清理旧备份（保留最近一次回滚能力由历史提供）
	return "success"
}

func (d *Deployer) healthCheck(id int64, url string, max time.Duration) bool {
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 400 {
				return true
			}
		}
		time.Sleep(3 * time.Second)
	}
	d.emit(id, "✗ 健康检查超时\n")
	return false
}

func (d *Deployer) publishEvent(service, version, host, environment, digest, status string) {
	if d.busURL == "" {
		return
	}
	if status == "success" && environment != "" {
		d.st.httpPost(d.busURL+"/api/registry/deployments", map[string]any{
			"service": service, "environment": environment, "version": version,
			"hosts": []string{host}, "artifact_digest": digest,
		})
	}
	d.st.httpPost(d.busURL+"/api/bus/publish", map[string]any{
		"topic": "deploy." + map[bool]string{true: "finished", false: "rollback"}[status == "success"],
		"from":  "deploy",
		"payload": map[string]any{
			"service": service, "version": version, "host": host, "environment": environment, "artifact_digest": digest, "status": status,
		},
	})
}

// Rollback 从部署历史回滚：取该主机上一成功版本重新部署
func (d *Deployer) Rollback(deployID int64, reason, actor string) (int64, error) {
	return d.RollbackWithTarget(deployID, reason, actor, nil)
}

// RollbackWithTarget 使用发布单固化的环境配置回滚，避免历史发布完成后配置漂移。
func (d *Deployer) RollbackWithTarget(deployID int64, reason, actor string, snapshot *ServiceEnvironment) (int64, error) {
	dp := d.st.GetDeploy(deployID)
	if dp == nil {
		return 0, fmt.Errorf("deploy not found")
	}
	prev := d.st.PrevSuccess(dp.Service, dp.Host, deployID)
	if prev == nil {
		return 0, fmt.Errorf("无更早的成功版本可回滚")
	}
	sv := d.st.GetService(dp.Service)
	host := d.st.GetHost(dp.Host)
	if sv == nil {
		return 0, fmt.Errorf("服务配置缺失")
	}
	if host == nil && snapshot != nil && snapshot.ManagedTarget() {
		managed := snapshot.ExecutionHosts()
		if len(managed) > 0 {
			host = &managed[0]
		}
	}
	if host == nil && dp.Environment != "" {
		if target := d.st.GetServiceEnvironment(dp.Service, dp.Environment); target != nil && target.ManagedTarget() {
			managed := target.ExecutionHosts()
			if len(managed) > 0 {
				host = &managed[0]
			}
		}
	}
	if host == nil {
		return 0, fmt.Errorf("服务或主机配置缺失")
	}
	artifact := d.st.GetArtifact(prev.ArtifactID)
	if artifact == nil {
		b := d.st.GetBuildByVersion(dp.Service, prev.Version)
		artifact = d.st.ArtifactForBuild(b, sv)
	}
	if artifact == nil {
		return 0, fmt.Errorf("该版本制品不存在")
	}
	// 版本会参与远端备份文件名与 shell 命令，必须保留原始、可部署的版本标识。
	auditReason := "回滚部署 #" + fmt.Sprint(deployID)
	if reason != "" {
		auditReason += "：" + reason
	}
	if snapshot != nil {
		sv = snapshot.Apply(sv)
	} else if dp.Environment != "" {
		if target := d.st.GetServiceEnvironment(dp.Service, dp.Environment); target != nil {
			sv = target.Apply(sv)
		}
	}
	return d.StartArtifactRelease(sv, *host, *artifact, dp.Environment, auditReason, actor)
}
