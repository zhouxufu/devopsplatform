package deploy

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Runtime templates consume this run's compilation, not an arbitrary JAR left
// in a reused checkout. Copy the unique output into a private minimal context;
// never overwrite repository files, guess a newest JAR or expose other sources.
func prepareDockerCompilation(params map[string]string, workspace, output string, compiled map[string]time.Time) (map[string]string, map[string]string, func(), error) {
	cleanup := func() {}
	if params["DOCKERFILE_SOURCE"] != "template" {
		return params, nil, cleanup, nil
	}
	kind, input, destination := "java-build", params["DOCKERFILE_ARTIFACT_PATH"], "app.jar"
	if params["DOCKERFILE_TEMPLATE"] == "nginx" {
		kind, destination = "npm-build", "dist"
	}
	if input == "" {
		input = destination
	}
	started := compiled[kind]
	if started.IsZero() {
		return nil, nil, cleanup, fmt.Errorf("运行时镜像模板缺少本次成功完成的源码编译步骤")
	}
	context := params["CONTEXT"]
	if context == "" {
		context = "."
	}
	if filepath.IsAbs(context) || filepath.IsAbs(input) {
		return nil, nil, cleanup, fmt.Errorf("基础模板的构建上下文和产物必须位于源码工作区内")
	}
	root, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return nil, nil, cleanup, err
	}
	pattern := filepath.Join(root, context, input)
	if rel, e := filepath.Rel(root, pattern); e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, nil, cleanup, fmt.Errorf("镜像编译产物不能越出源码工作区")
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, nil, cleanup, fmt.Errorf("编译产物匹配规则无效")
	}
	selected := []string{}
	for _, name := range matches {
		base := filepath.Base(name)
		if kind == "java-build" && (strings.HasSuffix(base, "-plain.jar") || strings.HasSuffix(base, "-sources.jar") || strings.HasSuffix(base, "-javadoc.jar")) {
			continue
		}
		selected = append(selected, name)
	}
	if len(selected) != 1 {
		return nil, nil, cleanup, fmt.Errorf("编译产物 %s 匹配到 %d 项，必须明确匹配一个可运行 JAR 或静态目录；请调整产物路径，不能自动选择旧文件", input, len(selected))
	}
	source, err := filepath.EvalSymlinks(selected[0])
	if err != nil {
		return nil, nil, cleanup, err
	}
	rel, err := filepath.Rel(root, source)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, nil, cleanup, fmt.Errorf("编译产物不能通过符号链接越出工作区")
	}
	info, err := os.Stat(source)
	if err != nil {
		return nil, nil, cleanup, err
	}
	if kind == "java-build" && (!info.Mode().IsRegular() || !strings.HasSuffix(source, ".jar") || info.Size() == 0) || kind == "npm-build" && !info.IsDir() {
		return nil, nil, cleanup, fmt.Errorf("Java 模板要求非空 .jar 文件，Nginx 模板要求静态产物目录")
	}
	dir, err := os.MkdirTemp(output, "compiled-image-")
	if err != nil {
		return nil, nil, cleanup, err
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	digest := sha256.New()
	files := 0
	err = filepath.Walk(source, func(name string, item os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if item.Mode()&os.ModeSymlink != 0 || !item.IsDir() && !item.Mode().IsRegular() {
			return fmt.Errorf("编译产物目录不能包含符号链接或特殊文件")
		}
		part, e := filepath.Rel(source, name)
		if e != nil {
			return e
		}
		target := filepath.Join(dir, destination, part)
		if item.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		if item.ModTime().Before(started) {
			return fmt.Errorf("产物包含本次编译前的文件，请使用 clean package / clean build，或在编译命令中清理输出目录；不会把遗留产物打进镜像")
		}
		if e = os.MkdirAll(filepath.Dir(target), 0755); e != nil {
			return e
		}
		in, e := os.Open(name)
		if e != nil {
			return e
		}
		defer in.Close()
		out, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if e != nil {
			return e
		}
		if info.IsDir() {
			fmt.Fprintf(digest, "%s\x00%d\x00", filepath.ToSlash(part), item.Size())
		}
		_, e = io.Copy(io.MultiWriter(out, digest), in)
		closeErr := out.Close()
		files++
		if e != nil {
			return e
		}
		return closeErr
	})
	if err != nil || files == 0 {
		cleanup()
		if err == nil {
			err = fmt.Errorf("静态编译产物目录为空")
		}
		return nil, nil, func() {}, err
	}
	resolved := map[string]string{}
	for key, value := range params {
		resolved[key] = value
	}
	resolved["CONTEXT"], resolved["DOCKERFILE_ARTIFACT_PATH"] = dir, destination
	metadata := map[string]string{"compiled_input_path": filepath.ToSlash(rel), "compiled_input_digest": fmt.Sprintf("sha256:%x", digest.Sum(nil)), "compiled_input_step": kind}
	return resolved, metadata, cleanup, nil
}
