package deploytask

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"
)

const BundleFormat = "devops-java-bundle/v1"
const MaxBundleSize int64 = 16 << 30
const MaxBundleFiles = 100000

type BundleDescriptor struct {
	MainJar        string `json:"main_jar"`
	ManifestDigest string `json:"manifest_digest"`
	ExpandedSize   int64  `json:"expanded_size"`
	FileCount      int    `json:"file_count"`
}
type BundleFile struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Mode      int64  `json:"mode"`
	SHA256    string `json:"sha256,omitempty"`
	Directory bool   `json:"directory,omitempty"`
}
type BundleManifest struct {
	Format  string       `json:"format"`
	Service string       `json:"service"`
	MainJar string       `json:"main_jar"`
	Files   []BundleFile `json:"files"`
}

func BundleRelative(v string) bool {
	return v != "" && len(v) <= 4096 && v != "." && !path.IsAbs(v) && path.Clean(v) == v && v != ".." && !strings.HasPrefix(v, "../") && !strings.Contains(v, "\\") && !strings.ContainsFunc(v, unicode.IsControl)
}
func (b BundleDescriptor) Validate() error {
	if !BundleRelative(b.MainJar) || !strings.HasSuffix(b.MainJar, ".jar") || !digestPattern.MatchString(b.ManifestDigest) || b.ExpandedSize <= 0 || b.ExpandedSize > MaxBundleSize || b.FileCount < 1 || b.FileCount > MaxBundleFiles {
		return fmt.Errorf("invalid frozen bundle descriptor")
	}
	return nil
}
func (m BundleManifest) Descriptor() (BundleDescriptor, error) {
	b := BundleDescriptor{MainJar: m.MainJar, ManifestDigest: Digest(m), FileCount: len(m.Files)}
	expected := map[string]BundleFile{}
	for _, v := range m.Files {
		if !BundleRelative(v.Path) || v.Size < 0 || v.Size > MaxBundleSize || v.Mode < 0 || v.Mode > 0777 || (!v.Directory && len(v.SHA256) != 64) || (v.Directory && (v.Size != 0 || v.SHA256 != "")) {
			return b, fmt.Errorf("invalid bundle member")
		}
		if _, e := hex.DecodeString(v.SHA256); e != nil {
			return b, e
		}
		if _, ok := expected[v.Path]; ok {
			return b, fmt.Errorf("duplicate bundle member")
		}
		expected[v.Path] = v
		b.ExpandedSize += v.Size
		if b.ExpandedSize > MaxBundleSize {
			return b, fmt.Errorf("bundle exceeds expansion limit")
		}
	}
	for name := range expected {
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if v, ok := expected[parent]; ok && !v.Directory {
				return b, fmt.Errorf("bundle parent is a file")
			}
		}
	}
	if v, ok := expected[m.MainJar]; !ok || v.Directory {
		return b, fmt.Errorf("bundle main JAR missing")
	}
	if m.Format != BundleFormat || m.Service == "" {
		return b, fmt.Errorf("invalid bundle format or owner")
	}
	return b, b.Validate()
}

// ExtractBundle verifies every member against the frozen manifest before an
// application is stopped. Destination must be newly created, never a live tree.
func ExtractBundle(archive, destination, service string, want BundleDescriptor) error {
	if e := want.Validate(); e != nil {
		return e
	}
	if e := os.Mkdir(destination, 0700); e != nil {
		return e
	}
	complete := false
	defer func() {
		if !complete {
			os.RemoveAll(destination)
		}
	}()
	f, e := os.Open(archive)
	if e != nil {
		return e
	}
	defer f.Close()
	br := bufio.NewReader(f)
	gz, e := gzip.NewReader(br)
	if e != nil {
		return e
	}
	defer gz.Close()
	gz.Multistream(false)
	tr := tar.NewReader(gz)
	h, e := tr.Next()
	if e != nil || h.Name != "java-bundle.json" || h.Typeflag != tar.TypeReg || h.Mode != 0600 || h.Linkname != "" || h.Size < 1 || h.Size > 32<<20 {
		return fmt.Errorf("invalid bundle manifest header")
	}
	raw, e := io.ReadAll(io.LimitReader(tr, 32<<20+1))
	if e != nil {
		return e
	}
	var m BundleManifest
	if e = json.Unmarshal(raw, &m); e != nil {
		return e
	}
	got, e := m.Descriptor()
	if e != nil {
		return e
	}
	if got != want || m.Service != service {
		return fmt.Errorf("bundle manifest differs from reviewed artifact")
	}
	expected := map[string]BundleFile{}
	for _, v := range m.Files {
		expected[v.Path] = v
	}
	seen := map[string]bool{}
	directories := []BundleFile{}
	for {
		h, e = tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		if (h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir) || h.Linkname != "" || !strings.HasPrefix(h.Name, "bundle/") {
			return fmt.Errorf("unsafe bundle entry")
		}
		for k := range h.PAXRecords {
			if k != "path" && k != "size" && k != "mtime" && k != "atime" && k != "ctime" {
				return fmt.Errorf("unsupported bundle extension")
			}
		}
		name := strings.TrimPrefix(h.Name, "bundle/")
		v, ok := expected[name]
		if !ok || seen[name] || h.Size != v.Size || h.Mode != v.Mode || v.Directory != (h.Typeflag == tar.TypeDir) {
			return fmt.Errorf("bundle member mismatch: %s", name)
		}
		seen[name] = true
		dest := filepath.Join(destination, "bundle", filepath.FromSlash(name))
		if v.Directory {
			if e = os.MkdirAll(dest, 0700); e != nil {
				return e
			}
			directories = append(directories, v)
			continue
		}
		if e = os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
			return e
		}
		file, e := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		hash := sha256.New()
		n, err := io.Copy(io.MultiWriter(file, hash), tr)
		if err == nil {
			err = file.Chmod(os.FileMode(v.Mode))
		}
		if err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if n != v.Size || hex.EncodeToString(hash.Sum(nil)) != v.SHA256 {
			return fmt.Errorf("bundle member checksum mismatch")
		}
	}
	if len(seen) != len(expected) {
		return fmt.Errorf("incomplete bundle")
	}
	n, e := io.Copy(io.Discard, io.LimitReader(gz, (1<<20)+1))
	if e != nil || n > 1<<20 {
		return fmt.Errorf("invalid compressed bundle trailer")
	}
	if _, e = br.Peek(1); e != io.EOF {
		return fmt.Errorf("extra compressed stream or trailing data")
	}
	for i := len(directories) - 1; i >= 0; i-- {
		v := directories[i]
		if e = os.Chmod(filepath.Join(destination, "bundle", filepath.FromSlash(v.Path)), os.FileMode(v.Mode)); e != nil {
			return e
		}
	}
	// The archive itself remains fsynced; an interrupted extraction is never
	// replayed as a deployment step and never reaches the live version tree.
	complete = true
	return nil
}
