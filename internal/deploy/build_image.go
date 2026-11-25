package deploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

func validateBuildImageSource(e *BuildEnvironment) error {
	if e.ImageSource == "" || e.ImageSource == "registry" {
		return nil
	}
	if e.ImageSource != "dockerfile" || e.Preset != "custom" {
		return fmt.Errorf("仅自定义构建镜像支持仓库 Dockerfile 来源")
	}
	if strings.Contains(e.Image, "@") {
		return fmt.Errorf("Dockerfile 构建镜像须填写版本标签，不能使用只读 digest 地址")
	}
	if !buildImageRelativePath(e.Dockerfile, false) {
		return fmt.Errorf("构建镜像 Dockerfile 必须填写源码目录内的相对文件路径")
	}
	if e.Context != "" && !buildImageRelativePath(e.Context, true) {
		return fmt.Errorf("构建镜像上下文必须是源码目录内的相对目录，默认 .")
	}
	return nil
}

func buildImageRelativePath(value string, allowRoot bool) bool {
	if value == "" || strings.TrimSpace(value) != value || len(value) > 1024 ||
		filepath.IsAbs(value) || strings.ContainsAny(value, ":\\\x00\r\n") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." || part == ".git" {
			return false
		}
	}
	return allowRoot || filepath.Clean(value) != "."
}

func buildImageDockerfileArgs(e *BuildEnvironment, workspace string) ([]string, error) {
	if err := validateBuildImageSource(e); err != nil {
		return nil, err
	}
	root, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return nil, fmt.Errorf("构建镜像源码目录不可读")
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	resolve := func(relative string, directory bool) (string, error) {
		name, err := filepath.EvalSymlinks(filepath.Join(root, relative))
		if err != nil {
			return "", fmt.Errorf("构建镜像输入 %s 不存在或不可读", relative)
		}
		rel, err := filepath.Rel(root, name)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("构建镜像输入不能通过符号链接越出源码目录")
		}
		info, err := os.Stat(name)
		if err != nil || directory && !info.IsDir() || !directory && (!info.Mode().IsRegular() || info.Size() == 0) {
			return "", fmt.Errorf("构建镜像上下文须为目录，Dockerfile 须为非空普通文件")
		}
		return name, nil
	}
	file, err := resolve(e.Dockerfile, false)
	if err != nil {
		return nil, err
	}
	contextPath, err := resolve(e.Context, true)
	if err != nil {
		return nil, err
	}
	args := []string{"build", "--file", file, "--tag", e.image()}
	if e.Network == "host" {
		args = append(args, "--network", "host")
	}
	// Only the checked-out source is sent as context. Application variables,
	// host credentials, Docker socket mounts and arbitrary build args are not
	// forwarded into the image. Docker applies the repository's .dockerignore.
	return append(args, contextPath), nil
}

var buildImageID = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var buildImageGates sync.Map // image reference -> context-aware per-process gate

// Resolve once, then run the immutable local ID, even if a tag changes later.
// Missing Dockerfile-built images never fall back to pulling that custom tag.
func prepareBuildImage(ctx context.Context, e *BuildEnvironment, workspace string, emit func(string), inspect func() (string, error), run func([]string) error) (string, error) {
	if e == nil || !e.isolated() {
		return "", fmt.Errorf("构建镜像准备仅用于容器构建环境")
	}
	if err := validateBuildEnvironment(e, nil); err != nil {
		return "", err
	}
	gate, _ := buildImageGates.LoadOrStore(e.image(), make(chan struct{}, 1))
	ch := gate.(chan struct{})
	select {
	case ch <- struct{}{}:
		defer func() { <-ch }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	id, err := inspect()
	if err == nil {
		if !buildImageID.MatchString(id) {
			return "", fmt.Errorf("无法解析本地构建镜像 ID")
		}
		emit("▶ 复用本地构建镜像：" + e.image() + " · " + id + "\n")
		return id, nil
	}
	var args []string
	if e.ImageSource == "dockerfile" {
		args, err = buildImageDockerfileArgs(e, workspace)
		if err != nil {
			return "", err
		}
		emit("▶ 本地缺少构建镜像，按仓库 Dockerfile 构建：" + e.Dockerfile + " → " + e.image() + "\n")
	} else {
		args = []string{"pull", e.image()}
		emit("⇣ 首次准备构建镜像：" + e.image() + "（需要镜像仓库网络；自定义镜像可配置仓库 Dockerfile，离线可预先 docker load）\n")
	}
	if err := run(args); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("构建镜像准备失败，源码编译尚未开始；请核对上方 Docker 日志、基础镜像及依赖仓库网络")
	}
	id, err = inspect()
	if err != nil || !buildImageID.MatchString(id) {
		return "", fmt.Errorf("构建镜像准备后无法解析本地镜像 ID，未开始源码编译")
	}
	return id, nil
}
