package deploy

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/elf"
	"errors"
	"fmt"
	"io"
)

// jattach v2.2 (Apache-2.0), official static Linux releases. This is a
// server-selected implementation of attach, never an arbitrary command tool.
var arthasNativeReleases = map[string]struct {
	URL, SHA string
	Machine  elf.Machine
}{
	"x64":   {"https://github.com/jattach/jattach/releases/download/v2.2/jattach-linux-x64.tgz", "acd9e17f15749306be843df392063893e97bfecc5260eef73ee98f06e5cfe02f", elf.EM_X86_64},
	"arm64": {"https://github.com/jattach/jattach/releases/download/v2.2/jattach-linux-arm64.tgz", "288ae5ed87ee7fe0e608c06db5a23a096a6217c9878ede53c4e33710bdcaab51", elf.EM_AARCH64},
}

func arthasNativeAttachBinary(ctx context.Context, arch string) ([]byte, error) {
	if _, ok := arthasNativeReleases[arch]; !ok {
		return nil, errors.New("轻量接入仅支持 Linux x64/arm64")
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	// Bundle the tiny official binaries so production onboarding does not
	// depend on target Internet access or GitHub availability in the platform.
	raw, e := uiFS.ReadFile("assets/arthas/jattach-linux-" + arch + ".tgz")
	if e != nil {
		return nil, errors.New("平台安装包缺少轻量接入组件，请更新部署插件")
	}
	return decodeArthasNativeBinary(raw, arch)
}
func decodeArthasNativeBinary(raw []byte, arch string) ([]byte, error) {
	release, ok := arthasNativeReleases[arch]
	if !ok || fmt.Sprintf("%x", sha256.Sum256(raw)) != release.SHA {
		return nil, errors.New("轻量接入组件 SHA-256 校验失败，未上传")
	}
	gz, e := gzip.NewReader(bytes.NewReader(raw))
	if e != nil {
		return nil, e
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	header, e := tr.Next()
	if e != nil || header.Name != "jattach" || header.Typeflag != tar.TypeReg || header.Size < 1 || header.Size > 1<<20 {
		return nil, errors.New("轻量接入包内容无效")
	}
	bin, e := io.ReadAll(io.LimitReader(tr, (1<<20)+1))
	if e != nil || int64(len(bin)) != header.Size {
		return nil, errors.New("轻量接入组件不完整")
	}
	f, e := elf.NewFile(bytes.NewReader(bin))
	if e != nil {
		return nil, errors.New("轻量接入组件不是 ELF 文件")
	}
	defer f.Close()
	if f.Machine != release.Machine || f.Class != elf.ELFCLASS64 || f.Data != elf.ELFDATA2LSB {
		return nil, errors.New("轻量接入组件架构不匹配")
	}
	for _, prog := range f.Progs {
		if prog.Type == elf.PT_INTERP {
			return nil, errors.New("轻量接入组件必须静态链接")
		}
	}
	return bin, nil
}
