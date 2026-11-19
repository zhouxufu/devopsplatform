package deploy

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"logplatform/pkg/deploytask"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	ArtifactJavaBundle           = "java-bundle"
	TargetJavaBundle             = "ssh-java-bundle"
	javaBundleStep               = "package-java-bundle"
	javaBundleFormat             = "devops-java-bundle/v1"
	javaBundleManifestName       = "java-bundle.json"
	maxBundleFiles               = 100000
	maxBundleSize          int64 = 16 << 30
)

type javaBundleManifest = deploytask.BundleManifest
type javaBundleFile = deploytask.BundleFile

func bundleRelative(value string, allowDot bool) bool {
	if allowDot && value == "." {
		return true
	}
	return value != "" && len(value) <= 4096 && value != "." && !path.IsAbs(value) && path.Clean(value) == value && value != ".." && !strings.HasPrefix(value, "../") && !strings.Contains(value, "\\") && !strings.ContainsFunc(value, unicode.IsControl)
}

func bundlePackageParams(sv *Service) map[string]string {
	params := map[string]string{"ROOT": sv.ArtifactGlob, "MAIN_JAR": "lib/app.jar"}
	for _, step := range sv.Steps {
		if step.Step == javaBundleStep {
			for k, v := range step.Params {
				params[k] = v
			}
			break
		}
	}
	params["ROOT"] = sv.ArtifactGlob
	return params
}

func validateBundlePackageParams(p map[string]string) error {
	if !bundleRelative(p["ROOT"], false) || !bundleRelative(p["MAIN_JAR"], false) || !strings.HasSuffix(p["MAIN_JAR"], ".jar") {
		return fmt.Errorf("Java 整包目录和主 JAR 必须是明确的相对路径，不能使用通配符、根目录或上级目录")
	}
	for _, k := range []string{"ROOT", "MAIN_JAR"} {
		if strings.ContainsAny(p[k], "*?[") {
			return fmt.Errorf("Java 整包路径不能使用通配符")
		}
	}
	if len(p["REQUIRED_PATHS"]) > 32768 {
		return fmt.Errorf("整包必需文件列表过长")
	}
	for _, name := range javaArgumentLines(p["REQUIRED_PATHS"]) {
		if !bundleRelative(name, false) {
			return fmt.Errorf("整包必需路径无效: %s", name)
		}
	}
	return nil
}

// Every archived member has a regular-file identity. Links, devices and sparse
// entries are deliberately unsupported, including links whose targets are inside
// the bundle: a later extraction must never depend on host filesystem state.
func packageJavaBundle(ctx context.Context, workspace, output, service string, p map[string]string, started time.Time) (string, error) {
	if err := validateBundlePackageParams(p); err != nil {
		return "", err
	}
	if started.IsZero() {
		return "", fmt.Errorf("Java 整包归档之前必须有本次成功的构建步骤")
	}
	ws, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", err
	}
	root := filepath.Join(ws, filepath.FromSlash(p["ROOT"]))
	for _, name := range []string{p["ROOT"], p["ROOT"] + "/" + p["MAIN_JAR"]} {
		current := ws
		for _, part := range strings.Split(name, "/") {
			current = filepath.Join(current, part)
			info, e := os.Lstat(current)
			if e != nil {
				return "", e
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("整包路径不能包含符号链接")
			}
		}
	}
	mainInfo, err := os.Stat(filepath.Join(root, filepath.FromSlash(p["MAIN_JAR"])))
	if err != nil || !mainInfo.Mode().IsRegular() {
		return "", fmt.Errorf("整包主 JAR 不存在或不是普通文件")
	}
	if mainInfo.ModTime().Before(started.Add(-time.Second)) {
		return "", fmt.Errorf("整包主 JAR 不是本次构建生成，拒绝归档旧制品")
	}
	for _, name := range javaArgumentLines(p["REQUIRED_PATHS"]) {
		if _, e := os.Lstat(filepath.Join(root, filepath.FromSlash(name))); e != nil {
			return "", fmt.Errorf("整包缺少必需路径: %s", name)
		}
	}
	manifest := javaBundleManifest{Format: javaBundleFormat, Service: service, MainJar: p["MAIN_JAR"]}
	var total int64
	err = filepath.WalkDir(root, func(file string, entry os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		info, e := entry.Info()
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(root, file)
		if e != nil {
			return e
		}
		name := filepath.ToSlash(rel)
		if !bundleRelative(name, true) || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("整包包含不安全的路径或符号链接: %s", name)
		}
		if entry.IsDir() {
			if name != "." {
				if len(manifest.Files) >= maxBundleFiles {
					return fmt.Errorf("整包文件数超过限制")
				}
				manifest.Files = append(manifest.Files, javaBundleFile{Path: name, Mode: int64(info.Mode().Perm()), Directory: true})
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("整包只允许目录和普通文件: %s", name)
		}
		total += info.Size()
		if len(manifest.Files) >= maxBundleFiles || total > maxBundleSize {
			return fmt.Errorf("Java 整包超过文件数或解压大小限制")
		}
		f, e := os.Open(file)
		if e != nil {
			return e
		}
		hash := sha256.New()
		n, e := io.Copy(hash, f)
		f.Close()
		if e != nil || n != info.Size() {
			return fmt.Errorf("整包文件读取失败: %s", name)
		}
		manifest.Files = append(manifest.Files, javaBundleFile{Path: name, Size: n, Mode: int64(info.Mode().Perm() & 0777), SHA256: hex.EncodeToString(hash.Sum(nil))})
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(manifest.Files) == 0 {
		return "", fmt.Errorf("Java 整包为空")
	}
	sort.Slice(manifest.Files, func(i, j int) bool { return manifest.Files[i].Path < manifest.Files[j].Path })
	if err = os.MkdirAll(output, 0755); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(output, "java-bundle-*.tar.gz")
	if err != nil {
		return "", err
	}
	name := f.Name()
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(name)
		}
	}()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	raw, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	if err = tw.WriteHeader(&tar.Header{Name: javaBundleManifestName, Typeflag: tar.TypeReg, Mode: 0600, Size: int64(len(raw))}); err == nil {
		_, err = tw.Write(raw)
	}
	if err != nil {
		return "", err
	}
	for _, item := range manifest.Files {
		if err = ctx.Err(); err != nil {
			return "", err
		}
		file := filepath.Join(root, filepath.FromSlash(item.Path))
		info, e := os.Lstat(file)
		if item.Directory {
			if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("整包目录发生变化")
			}
			if err = tw.WriteHeader(&tar.Header{Name: "bundle/" + item.Path, Typeflag: tar.TypeDir, Mode: item.Mode}); err != nil {
				return "", err
			}
			continue
		}
		if e != nil || !info.Mode().IsRegular() || info.Size() != item.Size {
			return "", fmt.Errorf("整包文件在归档期间发生变化: %s", item.Path)
		}
		in, e := os.Open(file)
		if e != nil {
			return "", e
		}
		opened, e := in.Stat()
		if e != nil || !os.SameFile(info, opened) {
			in.Close()
			return "", fmt.Errorf("整包文件身份发生变化")
		}
		err = tw.WriteHeader(&tar.Header{Name: "bundle/" + item.Path, Typeflag: tar.TypeReg, Mode: item.Mode, Size: item.Size})
		hash := sha256.New()
		var n int64
		if err == nil {
			n, err = io.Copy(tw, io.TeeReader(in, hash))
		}
		in.Close()
		if err != nil || n != item.Size || hex.EncodeToString(hash.Sum(nil)) != item.SHA256 {
			return "", fmt.Errorf("整包内容在归档期间发生变化: %s", item.Path)
		}
	}
	if err = tw.Close(); err == nil {
		err = gz.Close()
	}
	if err == nil {
		err = f.Close()
	}
	if err != nil {
		return "", err
	}
	if err = os.Chmod(name, 0444); err != nil {
		return "", err
	}
	ok = true
	return name, nil
}

// Validate the entire compressed stream, manifest and every file digest before
// any remote action. The JAR is copied to a private temporary file only for ZIP
// bytecode inspection; no repository code or bundled lifecycle script executes.
func inspectJavaBundle(file, service string, javaVersion int) (javaBundleManifest, error) {
	var manifest javaBundleManifest
	f, err := os.Open(file)
	if err != nil {
		return manifest, err
	}
	defer f.Close()
	br := bufio.NewReader(f)
	gz, err := gzip.NewReader(br)
	if err != nil {
		return manifest, err
	}
	defer gz.Close()
	gz.Multistream(false)
	tr := tar.NewReader(gz)
	h, err := tr.Next()
	if err != nil || h.Name != javaBundleManifestName || h.Typeflag != tar.TypeReg || h.Mode != 0600 || h.Linkname != "" || h.Size < 1 || h.Size > 32<<20 {
		return manifest, fmt.Errorf("Java 整包缺少有效的清单")
	}
	raw, err := io.ReadAll(io.LimitReader(tr, 32<<20+1))
	if err != nil || json.Unmarshal(raw, &manifest) != nil || manifest.Format != javaBundleFormat || manifest.Service != service || !bundleRelative(manifest.MainJar, false) || !strings.HasSuffix(manifest.MainJar, ".jar") || len(manifest.Files) == 0 || len(manifest.Files) > maxBundleFiles {
		return manifest, fmt.Errorf("Java 整包清单、应用归属或主 JAR 无效")
	}
	expected := map[string]javaBundleFile{}
	var total int64
	for _, v := range manifest.Files {
		if !bundleRelative(v.Path, false) || v.Size < 0 || v.Size > maxBundleSize || v.Mode < 0 || v.Mode > 0777 || (!v.Directory && len(v.SHA256) != 64) || (v.Directory && (v.Size != 0 || v.SHA256 != "")) {
			return manifest, fmt.Errorf("Java 整包清单包含非法条目")
		}
		if _, e := hex.DecodeString(v.SHA256); e != nil {
			return manifest, fmt.Errorf("整包文件摘要无效")
		}
		if _, duplicate := expected[v.Path]; duplicate {
			return manifest, fmt.Errorf("整包清单包含重复路径")
		}
		total += v.Size
		if total > maxBundleSize {
			return manifest, fmt.Errorf("Java 整包超过解压大小限制")
		}
		expected[v.Path] = v
	}
	for name := range expected {
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if entry, ok := expected[parent]; ok && !entry.Directory {
				return manifest, fmt.Errorf("整包路径的父级不是目录")
			}
		}
	}
	if v, ok := expected[manifest.MainJar]; !ok || v.Directory {
		return manifest, fmt.Errorf("整包缺少主 JAR")
	}
	seen := map[string]bool{}
	var jar *os.File
	defer func() {
		if jar != nil {
			jar.Close()
			os.Remove(jar.Name())
		}
	}()
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return manifest, e
		}
		if (h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir) || h.Linkname != "" || !strings.HasPrefix(h.Name, "bundle/") {

			return manifest, fmt.Errorf("整包包含不允许的条目类型或路径")
		}
		for key := range h.PAXRecords {
			if key != "path" && key != "size" && key != "mtime" && key != "atime" && key != "ctime" {
				return manifest, fmt.Errorf("整包包含不支持的扩展属性")
			}
		}
		name := strings.TrimPrefix(h.Name, "bundle/")
		v, ok := expected[name]
		if !ok || seen[name] || h.Size != v.Size || h.Mode != v.Mode || v.Directory != (h.Typeflag == tar.TypeDir) {
			return manifest, fmt.Errorf("整包内容与清单不一致: %s", name)
		}
		seen[name] = true
		if v.Directory {
			continue
		}
		hash := sha256.New()
		var dest io.Writer = hash
		if name == manifest.MainJar {
			jar, err = os.CreateTemp("", "devops-bundle-*.jar")
			if err != nil {
				return manifest, err
			}
			dest = io.MultiWriter(hash, jar)
		}
		n, e := io.Copy(dest, tr)
		if e != nil || n != v.Size || hex.EncodeToString(hash.Sum(nil)) != v.SHA256 {
			return manifest, fmt.Errorf("整包文件摘要不匹配: %s", name)
		}
	}
	if len(seen) != len(expected) || jar == nil {
		return manifest, fmt.Errorf("Java 整包缺少清单中声明的文件")
	}
	// Read the gzip trailer too: tar EOF alone does not validate gzip CRC.
	n, err := io.Copy(io.Discard, io.LimitReader(gz, (1<<20)+1))
	if err != nil || n > 1<<20 {
		return manifest, fmt.Errorf("Java 整包压缩尾部损坏或超限")
	}
	if _, err = br.Peek(1); err != io.EOF {
		return manifest, fmt.Errorf("Java 整包包含额外压缩流或尾随内容")
	}
	if err = jar.Close(); err != nil {
		return manifest, err
	}
	if javaVersion <= 0 {
		javaVersion = 1000
	}
	if err = validateJavaBytecode(jar.Name(), javaVersion); err != nil {
		return manifest, err
	}
	return manifest, nil
}

type sshJavaBundleDriver struct{}

func (sshJavaBundleDriver) Manifest() DeployDriverManifest {
	return DeployDriverManifest{APIVersion: "deploy.logplatform.io/v1", ID: TargetJavaBundle, Name: "Java 整包 / SSH", Accepts: []string{ArtifactJavaBundle}, Capabilities: []string{"deploy", "verify", "rollback", "immutable-bundle", "managed-java"}}
}
func (sshJavaBundleDriver) Validate(sv *Service, _ Host, a Artifact) error {
	if err := requireLocalArtifact(a, ArtifactJavaBundle); err != nil {
		return err
	}
	if err := validateManagedJava(sv); err != nil {
		return err
	}
	version, _ := strconv.Atoi(sv.DeployConfig["java_version"])
	m, err := inspectJavaBundle(a.URI, sv.Name, version)
	if err != nil {
		return err
	}
	if m.MainJar != sv.DeployConfig["java_bundle_jar"] {
		return fmt.Errorf("整包主 JAR 与环境配置不一致")
	}
	return nil
}
func (sshJavaBundleDriver) Deploy(d *Deployer, id int64, sv *Service, h Host, a Artifact, _ string) string {
	if deploymentAgentTarget(sv.DeployConfig) {
		return d.queueManagedJavaAgent(id, sv, h, a)
	}
	return d.deployJavaBundleRemote(id, sv, h, a, javaRemote{SSHExec, SSHUpload, SSHUploadContent})
}
func (d *Deployer) deployJavaBundleRemote(id int64, sv *Service, h Host, a Artifact, r javaRemote) string {
	// Revalidate at the execution boundary as well as release preflight.
	if err := (sshJavaBundleDriver{}).Validate(sv, h, a); err != nil {
		d.emit(id, "✗ 整包校验失败: "+err.Error()+"\n")
		return "failed"
	}
	return d.deployManagedJavaPreparedRemote(id, sv, h, r, func(release string, step func(string, string) bool) bool {
		archive := release + "/incoming.tar.gz"
		if err := r.upload(h, a.URI, archive); err != nil {
			d.emit(id, "✗ 上传整包失败\n")
			return false
		}
		check := "[ \"$(sha256sum < " + shellQuote(archive) + " | cut -d ' ' -f1)\" = " + shellQuote(strings.TrimPrefix(a.Digest, "sha256:")) + " ]"
		return step("核验并解压完整版本包", check+" && tar -xzf "+shellQuote(archive)+" --no-same-owner --no-same-permissions -C "+shellQuote(release)+" && test -f "+shellQuote(release+"/bundle/"+sv.DeployConfig["java_bundle_jar"])+" && rm -f -- "+shellQuote(archive))
	})
}
