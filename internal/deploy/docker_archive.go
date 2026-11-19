package deploy

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
)

// Read only small metadata entries; never unpack a caller supplied archive.
func dockerArchiveEntry(file, entry string, limit int64) ([]byte, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := tar.NewReader(f)
	for {
		h, err := r.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("镜像文件缺少 %s", entry)
		}
		if err != nil {
			return nil, fmt.Errorf("镜像文件不是有效 docker save tar: %w", err)
		}
		if h.Name != entry {
			continue
		}
		if h.Typeflag != tar.TypeReg || h.Size < 1 || h.Size > limit {
			return nil, fmt.Errorf("镜像元数据 %s 无效或过大", entry)
		}
		return io.ReadAll(io.LimitReader(r, limit+1))
	}
}

func dockerArchiveMetadata(file string) (map[string]string, error) {
	manifest, err := dockerArchiveEntry(file, "manifest.json", 64<<10)
	if err != nil {
		return nil, err
	}
	var images []struct {
		Config   string
		RepoTags []string
		Layers   []string
	}
	if json.Unmarshal(manifest, &images) != nil || len(images) != 1 || len(images[0].Layers) == 0 {
		return nil, fmt.Errorf("镜像文件必须只包含一个完整镜像")
	}
	configPath := images[0].Config
	if !regexp.MustCompile(`^(?:[a-f0-9]{64}\.json|blobs/sha256/[a-f0-9]{64})$`).MatchString(configPath) {
		return nil, fmt.Errorf("镜像 config 路径无效")
	}
	config, err := dockerArchiveEntry(file, configPath, 4<<20)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(config)
	hexID := hex.EncodeToString(hash[:])
	if configPath != hexID+".json" && configPath != "blobs/sha256/"+hexID {
		return nil, fmt.Errorf("镜像 config 摘要不匹配")
	}
	var platform struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
		Variant      string `json:"variant"`
	}
	if json.Unmarshal(config, &platform) != nil || platform.OS != "linux" || !regexp.MustCompile(`^(amd64|arm64|arm|386|ppc64le|s390x|riscv64)$`).MatchString(platform.Architecture) || !regexp.MustCompile(`^[a-zA-Z0-9]*$`).MatchString(platform.Variant) {
		return nil, fmt.Errorf("镜像 Linux 平台信息无效")
	}
	return map[string]string{"image_id": "sha256:" + hexID, "os": platform.OS, "architecture": platform.Architecture, "variant": platform.Variant, "transport": "archive"}, nil
}

func validateDockerArchive(a Artifact) error {
	if !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(a.Digest) {
		return fmt.Errorf("镜像文件缺少有效的冻结 SHA-256")
	}
	if err := requireLocalArtifact(a, ArtifactDockerArchive); err != nil {
		return err
	}
	metadata, err := dockerArchiveMetadata(a.URI)
	if err != nil {
		return err
	}
	for _, key := range []string{"image_id", "os", "architecture", "variant"} {
		if a.Metadata[key] != metadata[key] {
			return fmt.Errorf("镜像文件 %s 与冻结制品不一致", key)
		}
	}
	return nil
}
