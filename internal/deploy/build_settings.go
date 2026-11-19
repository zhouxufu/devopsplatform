package deploy

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxBuildSettingsSize = 4 << 20

// Copy only the explicitly configured file, using the platform's existing read
// authority. Never chmod/chown/relabel the source or mount its parent directory.
// The private copy belongs to the same UID used by the isolated build.
func stageBuildSettings(path string) (string, func(), error) {
	noop := func() {}
	if path == "" {
		return "", noop, nil
	}
	if !filepath.IsAbs(path) || strings.ContainsAny(path, ",:\r\n") {
		return "", noop, fmt.Errorf("隔离构建的 Maven settings.xml 必须为不含冒号、逗号或换行的绝对路径")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", noop, fmt.Errorf("Maven settings.xml 不存在或不是普通文件")
	}
	source, err := os.Open(path)
	if err != nil {
		return "", noop, fmt.Errorf("平台运行用户无法读取 Maven settings.xml，请检查该文件和父目录的读取权限；未修改文件权限")
	}
	defer source.Close()
	info, err = source.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", noop, fmt.Errorf("Maven settings.xml 不是可读取的普通文件")
	}
	if info.Size() > maxBuildSettingsSize {
		return "", noop, fmt.Errorf("Maven settings.xml 超过 4 MiB 限制")
	}
	dir, err := os.MkdirTemp("", "devops-build-settings-")
	if err != nil {
		return "", noop, fmt.Errorf("无法准备 Maven 临时配置")
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	target := filepath.Join(dir, "settings.xml")
	dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		cleanup()
		return "", noop, fmt.Errorf("无法准备 Maven 临时配置")
	}
	n, copyErr := io.Copy(dst, io.LimitReader(source, maxBuildSettingsSize+1))
	closeErr := dst.Close()
	if copyErr != nil || closeErr != nil || n > maxBuildSettingsSize {
		cleanup()
		return "", noop, fmt.Errorf("读取 Maven settings.xml 失败或文件超过 4 MiB 限制")
	}
	return target, cleanup, nil
}
