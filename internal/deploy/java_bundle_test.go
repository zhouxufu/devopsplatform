package deploy

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func gisBundleFixture(t *testing.T) (string, string, map[string]string) {
	t.Helper()
	ws := t.TempDir()
	root := filepath.Join(ws, "build/jpom-release")
	for _, dir := range []string{"lib", "bin", "tools/changchun-traffic/scripts", "tools/changchun-traffic/config", "tools/changchun-traffic/data", "empty"} {
		if e := os.MkdirAll(filepath.Join(root, dir), 0755); e != nil {
			t.Fatal(e)
		}
	}
	f, e := os.Create(filepath.Join(root, "lib/estate-gis-service.jar"))
	if e != nil {
		t.Fatal(e)
	}
	z := zip.NewWriter(f)
	c, _ := z.Create("BOOT-INF/classes/Gis.class")
	c.Write([]byte{0xca, 0xfe, 0xba, 0xbe, 0, 0, 0, 65})
	z.Close()
	f.Close()
	for name, data := range map[string]string{"bin/estate-gis-service.sh": "#!/bin/sh\necho THIS_BUNDLED_SCRIPT_MUST_NEVER_RUN\nexit 99\n", "tools/changchun-traffic/scripts/replay.sh": "echo replay", "tools/changchun-traffic/config/default.json": "{\"fixture\":true}", "tools/changchun-traffic/data/edge_features_changchun_full.csv": "edge,feature\n1,fixture\n", "quote ' $(false).txt": "literal"} {
		if e := os.WriteFile(filepath.Join(root, name), []byte(data), 0755); e != nil {
			t.Fatal(e)
		}
	}
	p := map[string]string{"ROOT": "build/jpom-release", "MAIN_JAR": "lib/estate-gis-service.jar", "REQUIRED_PATHS": "bin/estate-gis-service.sh\ntools/changchun-traffic/scripts\ntools/changchun-traffic/config\ntools/changchun-traffic/data/edge_features_changchun_full.csv"}
	return ws, root, p
}
func packageGisFixture(t *testing.T) (Artifact, string, map[string]string) {
	t.Helper()
	ws, root, p := gisBundleFixture(t)
	file, e := packageJavaBundle(context.Background(), ws, t.TempDir(), "gis", p, time.Now().Add(-time.Second))
	if e != nil {
		t.Fatal(e)
	}
	a, e := describeArtifact("gis", "fixture", ArtifactJavaBundle, file, "")
	if e != nil {
		t.Fatal(e)
	}
	return a, root, p
}

func TestJavaBundlePreservesGISReleaseAndValidatesCompatibility(t *testing.T) {
	a, root, _ := packageGisFixture(t)
	m, e := inspectJavaBundle(a.URI, "gis", 21)
	if e != nil {
		t.Fatal(e)
	}
	seen := map[string]javaBundleFile{}
	for _, v := range m.Files {
		seen[v.Path] = v
	}
	for _, name := range []string{"lib/estate-gis-service.jar", "bin/estate-gis-service.sh", "tools/changchun-traffic/scripts/replay.sh", "tools/changchun-traffic/config/default.json", "tools/changchun-traffic/data/edge_features_changchun_full.csv", "empty", "quote ' $(false).txt"} {
		if _, ok := seen[name]; !ok {
			t.Fatal("lost member", name)
		}
	}
	if !seen["empty"].Directory || seen["bin/estate-gis-service.sh"].Mode&0111 == 0 {
		t.Fatal("directory or executable mode lost")
	}
	if _, e = inspectJavaBundle(a.URI, "other", 21); e == nil {
		t.Fatal("foreign application accepted")
	}
	if _, e = inspectJavaBundle(a.URI, "gis", 17); e == nil {
		t.Fatal("wrong runtime JDK accepted")
	}
	sv := managedJavaFixture()
	sv.Name = "gis"
	sv.ArtifactType = ArtifactJavaBundle
	sv.TargetType = TargetJavaBundle
	sv.DeployConfig["java_bundle_jar"] = m.MainJar
	if e = (sshJavaBundleDriver{}).Validate(sv, Host{}, a); e != nil {
		t.Fatal(e)
	}
	if e = (sshJarDriver{}).Validate(sv, Host{}, a); e == nil {
		t.Fatal("JAR-only driver accepted complete bundle")
	}
	script := managedJavaScript(sv, "/isolated/releases/deploy-9")
	if !strings.Contains(script, "/bundle/lib/estate-gis-service.jar") || !strings.Contains(script, "readonly workdir='/isolated/releases/deploy-9/bundle'") || strings.Contains(script, "THIS_BUNDLED_SCRIPT_MUST_NEVER_RUN") {
		t.Fatal("bundle layout or lifecycle isolation lost")
	}
	if e = os.WriteFile(filepath.Join(root, "unchanged"), []byte("source survives packaging"), 0600); e != nil {
		t.Fatal(e)
	}
}

func TestJavaBundleRejectsStaleMissingAndLinkedInputs(t *testing.T) {
	for _, name := range []string{"stale", "missing", "symlink", "root-symlink", "cancel", "no-build", "escape", "wildcard"} {
		t.Run(name, func(t *testing.T) {
			ws, root, p := gisBundleFixture(t)
			ctx := context.Background()
			started := time.Now().Add(-time.Second)
			switch name {
			case "stale":
				old := time.Now().Add(-time.Hour)
				os.Chtimes(filepath.Join(root, p["MAIN_JAR"]), old, old)
			case "missing":
				os.Remove(filepath.Join(root, "bin/estate-gis-service.sh"))
			case "symlink":
				os.Symlink("/etc/passwd", filepath.Join(root, "leak"))
			case "root-symlink":
				os.Rename(root, root+"-real")
				os.Symlink(root+"-real", root)
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "no-build":
				started = time.Time{}
			case "escape":
				p["ROOT"] = "../outside"
			case "wildcard":
				p["MAIN_JAR"] = "lib/*.jar"
			}
			if file, e := packageJavaBundle(ctx, ws, t.TempDir(), "gis", p, started); e == nil {
				t.Fatal("unsafe input accepted", file)
			}
		})
	}
}

func rewriteBundle(t *testing.T, file string, modify func(*tar.Header, []byte) (*tar.Header, []byte), extra bool) string {
	t.Helper()
	in, e := os.Open(file)
	if e != nil {
		t.Fatal(e)
	}
	defer in.Close()
	gz, e := gzip.NewReader(in)
	if e != nil {
		t.Fatal(e)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var buf bytes.Buffer
	out := gzip.NewWriter(&buf)
	tw := tar.NewWriter(out)
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(tr)
		if e != nil {
			t.Fatal(e)
		}
		h, b = modify(h, b)
		if h == nil {
			continue
		}
		if e = tw.WriteHeader(h); e != nil {
			t.Fatal(e)
		}
		if _, e = tw.Write(b); e != nil {
			t.Fatal(e)
		}
	}
	if extra {
		tw.WriteHeader(&tar.Header{Name: "bundle/../../outside", Typeflag: tar.TypeSymlink, Linkname: "/tmp", Mode: 0777})
	}
	tw.Close()
	out.Close()
	dst := filepath.Join(t.TempDir(), "bad.tar.gz")
	if e = os.WriteFile(dst, buf.Bytes(), 0600); e != nil {
		t.Fatal(e)
	}
	return dst
}
func TestJavaBundleRejectsCorruptionTraversalAndManifestTampering(t *testing.T) {
	a, _, _ := packageGisFixture(t)
	for _, name := range []string{"content", "missing", "foreign-main", "escape", "symlink", "extra", "crc", "truncated", "trailing"} {
		t.Run(name, func(t *testing.T) {
			file := rewriteBundle(t, a.URI, func(h *tar.Header, b []byte) (*tar.Header, []byte) {
				if h.Name == javaBundleManifestName && (name == "foreign-main" || name == "escape") {
					var m javaBundleManifest
					json.Unmarshal(b, &m)
					if name == "foreign-main" {
						m.MainJar = "lib/missing.jar"
					} else {
						m.Files[0].Path = "../../outside"
					}
					b, _ = json.Marshal(m)
					h.Size = int64(len(b))
				}
				if h.Name == "bundle/tools/changchun-traffic/config/default.json" {
					switch name {
					case "content":
						b[0] = 'x'
					case "missing":
						return nil, nil
					case "symlink":
						h.Typeflag = tar.TypeSymlink
						h.Linkname = "/etc/passwd"
						h.Size = 0
						b = nil
					}
				}
				return h, b
			}, name == "extra")
			if name == "crc" || name == "truncated" || name == "trailing" {
				raw, _ := os.ReadFile(file)
				switch name {
				case "crc":
					raw[len(raw)-8] ^= 0xff
				case "truncated":
					raw = raw[:len(raw)-5]
				case "trailing":
					raw = append(raw, []byte("extra")...)
				}
				os.WriteFile(file, raw, 0600)
			}
			if _, e := inspectJavaBundle(file, "gis", 21); e == nil {
				t.Fatal("malformed bundle accepted", name)
			}
		})
	}
}

func TestJavaBundleReleasePreflightAndRollbackUseWholeVersion(t *testing.T) {
	a, _, _ := packageGisFixture(t)
	for _, fail := range []string{"", "extract", "check", "ready", "slow", "foreign-current", "wrong-digest"} {
		t.Run(fail, func(t *testing.T) {
			sv := managedJavaFixture()
			sv.Name = "gis"
			sv.ArtifactType = ArtifactJavaBundle
			sv.TargetType = TargetJavaBundle
			sv.DeployConfig["java_bundle_jar"] = "lib/estate-gis-service.jar"
			previous := "/test/app/releases/deploy-1"
			if fail == "foreign-current" {
				previous = "/jpom/gis"
			}
			commands := []string{}
			files := map[string][]byte{}
			remote := javaRemote{
				exec: func(_ Host, c string, _ time.Duration) (string, error) {
					commands = append(commands, c)
					if strings.Contains(c, "then readlink") {
						return previous, nil
					}
					if fail == "extract" && strings.Contains(c, "tar -xzf") {
						return "", fmt.Errorf("extract failed")
					}
					if fail == "check" && strings.HasSuffix(c, " check") {
						return "", fmt.Errorf("JDK mismatch")
					}
					if fail == "slow" && strings.HasSuffix(c, "/deploy-2/service.sh' ready") {
						return "", javaTestExit(75)
					}
					if fail == "ready" && strings.HasSuffix(c, "/deploy-2/service.sh' ready") {
						return "", javaTestExit(20)
					}
					return "", nil
				},
				upload: func(_ Host, source, dest string) error {
					if source != a.URI || !strings.HasSuffix(dest, "/incoming.tar.gz") {
						t.Fatal("bundle not uploaded", source, dest)
					}
					return nil
				},
				write: func(_ Host, b []byte, dest string) error { files[dest] = b; return nil },
			}
			candidate := a
			if fail == "wrong-digest" {
				candidate.Digest = "sha256:" + strings.Repeat("0", 64)
			}
			d := NewDeployer(nil, NewLogHub(), "")
			status := d.deployJavaBundleRemote(2, sv, Host{}, candidate, remote)
			want := "success"
			if fail != "" {
				want = "failed"
			}
			if fail == "ready" {
				want = "rolled-back"
			}
			if fail == "slow" {
				want = "needs_review"
			}
			if status != want {
				t.Fatal(status, want, commands)
			}
			joined := strings.Join(commands, "\n")
			if fail == "slow" && (strings.Contains(joined, "/deploy-2/service.sh' stop") || strings.Contains(joined, "/deploy-1/service.sh' start") || strings.Contains(joined, "refusing to remove lock owned")) {
				t.Fatal("uncertain bundle readiness stopped process or released its lease")
			}
			if fail == "wrong-digest" && len(commands) != 0 {
				t.Fatal("remote effects before digest validation")
			}
			if (fail == "extract" || fail == "check" || fail == "foreign-current") && strings.Contains(joined, "/deploy-1/service.sh' stop") {
				t.Fatal("old version stopped before bundle preflight")
			}
			if fail == "ready" && (!strings.Contains(joined, "/deploy-1/service.sh' start") || !strings.Contains(joined, "/deploy-2/service.sh' stop")) {
				t.Fatal("whole version rollback missing")
			}
			for name := range files {
				if strings.Contains(name, "deploy-1") {
					t.Fatal("old version files overwritten")
				}
			}
		})
	}
}

func TestJavaBundleManagedStatusQuotesJarPath(t *testing.T) {
	c := managedJavaStatusCommandFor("/isolated", "bundle/lib/quoted\" $(false).jar")
	cmd := exec.Command("bash", "-n")
	cmd.Stdin = strings.NewReader(c)
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	if !strings.Contains(c, "grep -Fqx -- \"$release/$jar_relative\"") {
		t.Fatal("jar identity not checked literally")
	}
}
