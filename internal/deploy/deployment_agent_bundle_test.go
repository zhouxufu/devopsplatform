package deploy

import (
	"logplatform/pkg/deploytask"
	"os"
	"path/filepath"
	"testing"
)

func TestDeploymentAgentBundleExtraction(t *testing.T) {
	a, _, _ := packageGisFixture(t)
	m, e := inspectJavaBundle(a.URI, "gis", 21)
	if e != nil {
		t.Fatal(e)
	}
	descriptor, e := m.Descriptor()
	if e != nil {
		t.Fatal(e)
	}
	dest := filepath.Join(t.TempDir(), "unpacked")
	if e = deploytask.ExtractBundle(a.URI, dest, "gis", descriptor); e != nil {
		t.Fatal(e)
	}
	for _, v := range m.Files {
		p := filepath.Join(dest, "bundle", filepath.FromSlash(v.Path))
		info, e := os.Stat(p)
		if e != nil || info.Mode().Perm() != os.FileMode(v.Mode) {
			t.Fatalf("lost bundle file or executable mode %s %v", v.Path, e)
		}
	}
	if deploytask.ExtractBundle(a.URI, dest, "gis", descriptor) == nil {
		t.Fatal("existing tree overwritten")
	}
	wrong := descriptor
	wrong.ManifestDigest = "sha256:" + string(make([]byte, 64))
	if deploytask.ExtractBundle(a.URI, filepath.Join(t.TempDir(), "wrong"), "gis", wrong) == nil {
		t.Fatal("changed manifest accepted")
	}
	if deploytask.ExtractBundle(a.URI, filepath.Join(t.TempDir(), "foreign"), "other", descriptor) == nil {
		t.Fatal("foreign package owner accepted")
	}
	raw, e := os.ReadFile(a.URI)
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"crc", "truncated", "trailing"} {
		t.Run(name, func(t *testing.T) {
			bad := append([]byte(nil), raw...)
			switch name {
			case "crc":
				bad[len(bad)-8] ^= 0xff
			case "truncated":
				bad = bad[:len(bad)-5]
			case "trailing":
				bad = append(bad, []byte("extra")...)
			}
			p := filepath.Join(t.TempDir(), "bad.tar.gz")
			os.WriteFile(p, bad, 0600)
			dest := filepath.Join(t.TempDir(), "out")
			if deploytask.ExtractBundle(p, dest, "gis", descriptor) == nil {
				t.Fatal("corrupt package accepted")
			}
			if _, e := os.Stat(dest); !os.IsNotExist(e) {
				t.Fatal("incomplete extraction left publishable tree")
			}
		})
	}
}
