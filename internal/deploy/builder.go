package deploy

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Builder 构建执行器：本地步骤执行，支持 jar/static 与仅构建的 image 产物。
// 构建日志实时广播（fan-out 模式，同 logs 的 tail）+ 落库。
type Builder struct {
	st           *Store
	artifactsDir string
	workspaceDir string // git 模式的托管源码目录
	hub          *logHub
}

func NewBuilder(st *Store, dataDir string, hub *logHub) *Builder {
	artifacts := filepath.Join(dataDir, "artifacts")
	workspace := filepath.Join(dataDir, "workspace")
	os.MkdirAll(artifacts, 0o755)
	os.MkdirAll(workspace, 0o755)
	return &Builder{st: st, artifactsDir: artifacts, workspaceDir: workspace, hub: hub}
}

// Subscribe 构建日志订阅（实时）
func (b *Builder) Subscribe(buildID int64) (chan string, func()) { return b.hub.Subscribe(buildID) }

func (b *Builder) emit(buildID int64, line string) { b.hub.emit(buildID, line) }

// buildDir 构建目录：插件托管 workspace（git 模式唯一；workdir 仅旧数据兼容）
func (b *Builder) buildDir(sv *Service) string {
	if sv.GitURL == "" && sv.Workdir != "" {
		return sv.Workdir
	}
	return filepath.Join(b.workspaceDir, sv.Name)
}

// syncGit 同步源码：clone 或 fetch+reset（干净可重复）
func (b *Builder) syncGit(buildID int64, sv *Service) error {
	dir := b.buildDir(sv)
	branch := sv.Branch
	branchArg := ""
	if branch != "" {
		branchArg = " -b " + branch
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		b.emit(buildID, "⬇ clone "+sv.GitURL+branchArg+"\n")
		cmd := exec.Command("git", "clone", sv.GitURL, dir)
		if branch != "" {
			cmd = exec.Command("git", "clone", sv.GitURL, dir, "-b", branch)
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("clone 失败: %v\n%s", err, out)
		}
		return nil
	}
	ref := "origin/HEAD"
	if branch != "" {
		ref = "origin/" + branch
	}
	b.emit(buildID, "⬇ 更新 "+ref+"\n")
	for _, args := range [][]string{
		{"git", "-C", dir, "fetch", "--all", "--prune"},
		{"git", "-C", dir, "reset", "--hard", ref},
		{"git", "-C", dir, "clean", "-fd"},
	} {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			return fmt.Errorf("%v 失败: %v\n%s", args, err, out)
		}
	}
	return nil
}

// Start 触发一次构建（异步执行，立即返回 buildID）
func (b *Builder) Start(sv *Service, actor string) (int64, error) {
	if sv.ArtifactType == ArtifactJavaBundle {
		return 0, fmt.Errorf("Java 整包请使用应用构建入口 POST /api/pipeline/runs，旧版接口不支持完整归档")
	}
	environments, err := b.st.automaticDocumentEnvironments(sv)
	if err != nil {
		return 0, err
	}
	if len(environments) > 0 {
		return 0, fmt.Errorf("已启用按环境生成接口文档，请使用应用构建入口（POST /api/pipeline/runs）并指定 build_environment；旧版 builds 接口无法保存环境和文档阶段")
	}

	if b.st.GetServiceDelivery(sv.Name).BuildEnvironment.isolated() {
		return 0, fmt.Errorf("隔离构建请使用应用的构建入口（POST /api/pipeline/runs），旧版 builds 接口不支持容器环境")
	}
	dir := b.buildDir(sv)
	version, err := gitVersion(dir)
	if err != nil {
		version = time.Now().Format("0102-1504")
	}
	build := &Build{Service: sv.Name, Version: version, Actor: actor, StartedAt: time.Now().Unix()}
	if err := b.st.CreateBuild(build); err != nil {
		return 0, err
	}
	go b.run(build.ID, sv, version)
	return build.ID, nil
}

// run 构建状态机：前置(git pull) → 构建命令 → 产物归档（jar 拷贝 / image 记 tag）
func (b *Builder) run(buildID int64, sv *Service, version string) {
	var logBuf logTail
	status, artifact := "success", ""
	if sv.GitURL != "" {
		if err := b.syncGit(buildID, sv); err != nil {
			b.emit(buildID, "✗ 源码同步失败: "+err.Error()+"\n")
			b.st.FinishBuild(buildID, "failed", "", "", 0, logBuf.String()+"\n源码同步失败: "+err.Error())
			b.hub.Close(buildID)
			return
		}
		if v, err := gitVersion(b.buildDir(sv)); err == nil {
			version = v // 以同步后的真实 commit 为准
			b.st.SetBuildVersion(buildID, version)
		}
	}
	dir := b.buildDir(sv)

	steps := []struct {
		name string
		cmd  string
	}{
		{"前置", sv.PreCmd},
		{"构建", sv.BuildCmd},
	}
	for _, st := range steps {
		if strings.TrimSpace(st.cmd) == "" {
			continue
		}
		b.emit(buildID, fmt.Sprintf("\n$ %s\n", st.cmd))
		code := b.exec(buildID, dir, st.cmd, &logBuf)
		if code != 0 {
			status = "failed"
			b.emit(buildID, fmt.Sprintf("\n✗ %s 失败（退出码 %d）\n", st.name, code))
			break
		}
	}
	if status == "success" {
		var err error
		artifact, err = b.archive(sv, dir, version)
		if err != nil {
			status = "failed"
			b.emit(buildID, "\n✗ 产物处理失败: "+err.Error()+"\n")
		} else {
			b.emit(buildID, "\n✓ 构建成功 产物: "+artifact+"\n")
		}
	}
	digest, size := "", int64(0)
	if status == "success" && sv.ArtifactType != "image" {
		var err error
		digest, size, err = artifactMetadata(artifact)
		if err != nil {
			status = "failed"
			b.emit(buildID, "\n✗ 制品校验失败: "+err.Error()+"\n")
		}
	} else if status == "success" {
		out, err := exec.Command("docker", "image", "inspect", artifact, "--format", "{{.Id}}").Output()
		if err != nil {
			status = "failed"
			b.emit(buildID, "\n✗ 镜像校验失败: "+err.Error()+"\n")
		} else {
			digest = strings.TrimSpace(string(out))
		}
	}
	b.st.FinishBuild(buildID, status, artifact, digest, size, logBuf.String())
	b.hub.Close(buildID)
}

func artifactMetadata(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), n, nil
}

// execStream 流式执行：输出同时进 hub 广播
func (b *Builder) execStream(hub *logHub, key int64, dir, cmdStr string, emit func(string)) int {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", cmdStr)
	cmd.Dir = dir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		emit("✗ 创建 stdout 管道失败: " + err.Error() + "\n")
		return -1
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		emit("✗ 创建 stderr 管道失败: " + err.Error() + "\n")
		return -1
	}
	if err := cmd.Start(); err != nil {
		emit("✗ 启动命令失败: " + err.Error() + "\n")
		return -1
	}
	var wg sync.WaitGroup
	scan := func(r interface{ Read([]byte) (int, error) }) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64<<10), 256<<10)
		for sc.Scan() {
			emit(sc.Text() + "\n")
		}
	}
	wg.Add(2)
	go scan(stdout)
	go scan(stderr)
	wg.Wait()
	err = cmd.Wait()
	if ctx.Err() == context.DeadlineExceeded {
		return -1
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		return -1
	}
	return 0
}

// exec 执行本地命令，输出实时广播。
func (b *Builder) exec(buildID int64, dir, cmdStr string, logBuf *logTail) int {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", cmdStr)
	cmd.Dir = dir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		b.emit(buildID, "✗ 创建 stdout 管道失败: "+err.Error()+"\n")
		return -1
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		b.emit(buildID, "✗ 创建 stderr 管道失败: "+err.Error()+"\n")
		return -1
	}
	if err := cmd.Start(); err != nil {
		b.emit(buildID, "✗ 启动命令失败: "+err.Error()+"\n")
		return -1
	}
	var wg sync.WaitGroup
	scan := func(r interface{ Read([]byte) (int, error) }) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64<<10), 256<<10)
		for sc.Scan() {
			line := sc.Text()
			b.emit(buildID, line+"\n")
			logBuf.append(line)
		}
	}
	wg.Add(2)
	go scan(stdout)
	go scan(stderr)
	wg.Wait()
	err = cmd.Wait()
	if ctx.Err() == context.DeadlineExceeded {
		return -1
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		return -1
	}
	return 0
}

func (b *Builder) archive(sv *Service, dir, version string) (string, error) {
	switch sv.ArtifactType {
	case "image":
		return b.imageArtifact(sv, version, 0)
	case "static": // 前端产物：glob 目录打成 tar.gz
		return b.staticArtifact(sv, dir, version)
	default:
		return b.jarArtifactAt(sv, dir, version)
	}
}

// staticArtifact 前端静态产物：glob 匹配目录（如 dist）→ tar.gz 归档
func (b *Builder) staticArtifact(sv *Service, dir, version string) (string, error) {
	matches, _ := filepath.Glob(filepath.Join(dir, sv.ArtifactGlob))
	if len(matches) == 0 {
		return "", fmt.Errorf("未匹配到产物目录 %s", sv.ArtifactGlob)
	}
	srcDir := matches[len(matches)-1]
	if info, err := os.Stat(srcDir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("静态制品必须是目录: %s", srcDir)
	}
	dst := filepath.Join(b.artifactsDir, sv.Name, fmt.Sprintf("%s-%d.tar.gz", version, time.Now().UnixNano()))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	cmd := exec.Command("tar", "-czf", dst, "-C", filepath.Dir(srcDir), filepath.Base(srcDir))
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("打包失败: %v\n%s", err, out)
	}
	return dst, nil
}

// jarArtifact 归档 jar 产物：glob 匹配 → 拷贝到 artifacts/{svc}/{version}.jar
func (b *Builder) jarArtifact(sv *Service, version string, buildID int64) (string, error) {
	return b.jarArtifactAt(sv, b.buildDir(sv), version)
}

func (b *Builder) jarArtifactAt(sv *Service, dir, version string) (string, error) {
	matches, _ := filepath.Glob(filepath.Join(dir, sv.ArtifactGlob))
	if len(matches) == 0 {
		return "", fmt.Errorf("未匹配到产物 %s", sv.ArtifactGlob)
	}
	src := newestArtifact(matches)
	dst := filepath.Join(b.artifactsDir, sv.Name, fmt.Sprintf("%s-%d%s", version, time.Now().UnixNano(), filepath.Ext(src)))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return dst, nil
}

func newestArtifact(matches []string) string {
	selected := matches[0]
	var selectedMod time.Time
	for _, candidate := range matches {
		base := strings.ToLower(filepath.Base(candidate))
		if strings.Contains(base, "-sources.") || strings.Contains(base, "-javadoc.") || strings.Contains(base, "-plain.") {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && (selectedMod.IsZero() || info.ModTime().After(selectedMod)) {
			selected, selectedMod = candidate, info.ModTime()
		}
	}
	return selected
}

// imageArtifact 镜像产物：按约定 tag 重打（docker tag {image_name}:{version}）并记录
func (b *Builder) imageArtifact(sv *Service, version string, buildID int64) (string, error) {
	if sv.ImageName == "" {
		return "", fmt.Errorf("image 类型需配置 image_name")
	}
	tag := sv.ImageName + ":" + version
	cmd := exec.Command("docker", "tag", sv.ImageName+":latest", tag)
	if out, err := cmd.CombinedOutput(); err != nil {
		b.emit(buildID, "docker tag: "+string(out))
		if inspectErr := exec.Command("docker", "image", "inspect", tag).Run(); inspectErr != nil {
			return "", fmt.Errorf("构建后未找到镜像 %s", tag)
		}
	}
	return tag, nil
}

// gitVersion 工作目录的版本标识（git 短 hash）
func gitVersion(workdir string) (string, error) {
	out, err := exec.Command("git", "-C", workdir, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// logTail 构建日志尾部缓存（落库用，防止无限制内存）
type logTail struct {
	mu  sync.Mutex
	buf []string
}

func (l *logTail) append(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = append(l.buf, line)
	if len(l.buf) > 2000 { // 保留最后 2000 行
		l.buf = l.buf[len(l.buf)-2000:]
	}
}

func (l *logTail) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.buf, "\n")
}

// ---------- 流水线复用的细粒度方法 ----------

// RunGitSteps 导出：git 同步（clone 或 fetch+reset+clean）
func (b *Builder) RunGitSteps(sv *Service, emit func(string)) (string, error) {
	return runGitSteps(b.buildDir(sv), sv, emit)
}

// ExecInDir 导出：在目录执行命令，输出经 emit 广播
func (b *Builder) ExecInDir(hub *logHub, key int64, dir, cmdStr string, emit func(string)) int {
	return b.execStream(hub, key, dir, cmdStr, emit)
}

// ArchiveArtifact 导出：产物归档
func (b *Builder) ArchiveArtifact(sv *Service, dir, version string) (string, error) {
	return b.archive(sv, dir, version)
}

func (b *Builder) WorkspaceDir() string { return b.workspaceDir }

// runGitSteps git 同步（返回分支说明）
func runGitSteps(dir string, sv *Service, emit func(string)) (string, error) {
	branch := sv.Branch
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		args := []string{"clone", sv.GitURL, dir}
		if branch != "" {
			args = append(args, "-b", branch)
		}
		emit("⬇ git clone " + sv.GitURL + "\n")
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			return "", fmt.Errorf("%v\n%s", err, out)
		}
		return branch, nil
	}
	ref := "origin/HEAD"
	if branch != "" {
		ref = "origin/" + branch
	}
	emit("⬇ git fetch + reset " + ref + "\n")
	for _, args := range [][]string{
		{"git", "-C", dir, "fetch", "--all", "--prune"},
		{"git", "-C", dir, "reset", "--hard", ref},
		{"git", "-C", dir, "clean", "-fd"},
	} {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			return "", fmt.Errorf("%v 失败: %v\n%s", args, err, out)
		}
	}
	return branch, nil
}

// ExecStep 执行步骤包（bash run.sh + 注入环境变量），输出流式回调
func (b *Builder) ExecStep(def *StepDef, env []string, emit func(string)) int {
	return b.execStepContext(context.Background(), def, env, emit)
}
func (b *Builder) execStepContext(parent context.Context, def *StepDef, env []string, emit func(string)) int {
	ctx, cancel := context.WithTimeout(parent, 40*time.Minute)
	defer cancel()
	script, err := filepath.Abs(filepath.Join(def.Dir, "run.sh"))
	if err != nil {
		emit("✗ 无法解析构建步骤路径\n")
		return -1
	}
	cmd := exec.CommandContext(ctx, "bash", script)
	cmd.Env = env
	cmd.Dir = def.Dir
	return runBuildCommand(cmd, emit)
}

func runBuildCommand(cmd *exec.Cmd, emit func(string)) int {
	configureBuildCancellation(cmd)
	// Let os/exec own the pipes: WaitDelay can then close descriptors held by
	// descendants after cancellation. External scanners before Wait deadlock.
	var mu sync.Mutex
	output := func(line string) {
		mu.Lock()
		defer mu.Unlock()
		emit(line)
	}
	stdout, stderr := &buildLineWriter{emit: output}, &buildLineWriter{emit: output}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if err != nil {
		finishBuildCancellation(cmd)
	}
	stdout.flush()
	stderr.flush()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		return -1
	}
	return 0
}

// Drop oversized lines whole, never split secrets across redaction boundaries.
type buildLineWriter struct {
	emit     func(string)
	buf      []byte
	dropping bool
}

func (w *buildLineWriter) Write(p []byte) (int, error) {
	for _, c := range p {
		if c == '\n' {
			w.flush()
			continue
		}
		if w.dropping {
			continue
		}
		if len(w.buf) >= 256<<10 {
			w.buf = nil
			w.dropping = true
			continue
		}
		w.buf = append(w.buf, c)
	}
	return len(p), nil
}
func (w *buildLineWriter) flush() {
	if w.dropping {
		w.emit("⚠ 已省略超过 256 KiB 的单行构建输出\n")
	} else if len(w.buf) > 0 {
		w.emit(string(w.buf) + "\n")
	}
	w.buf = nil
	w.dropping = false
}
