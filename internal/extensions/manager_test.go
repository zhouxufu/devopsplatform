package extensions

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	p "logplatform/pkg/extensionprotocol"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if token := os.Getenv("PLUGIN_AUTH_TOKEN"); token != "" {
		http.ListenAndServe(os.Getenv("PLUGIN_LISTEN_ADDR"), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+token {
				w.WriteHeader(401)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/v1/describe":
				json.NewEncoder(w).Encode(p.Description{Protocol: 1, ID: "fixture-plugin", Version: "1", Capabilities: []string{"fixture.read.v1"}})
			case "/v1/health":
				w.Write([]byte(`{"ok":true}`))
			case "/v1/test":
				w.Write([]byte(`{"data":{"ok":true}}`))
			case "/v1/invoke":
				var req p.Request
				json.NewDecoder(r.Body).Decode(&req)
				if req.Operation == "paths" {
					_, err := os.Stat(os.Getenv("PLUGIN_CONFIG_PATH"))
					cwd, _ := os.Getwd()
					json.NewEncoder(w).Encode(p.Response{Data: json.RawMessage(fmt.Sprintf(`{"config_readable":%t,"working_directory":%q}`, err == nil, filepath.Base(cwd)))})
					return
				}
				if req.Operation == "wait" {
					<-r.Context().Done()
					return
				}
				data, _ := json.Marshal(req.Actor)
				json.NewEncoder(w).Encode(p.Response{Data: data})
			default:
				w.WriteHeader(404)
			}
		}))
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func testPackage(t *testing.T, mutate func(*p.Manifest), extra string) []byte {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(binary)
	manifest := p.Manifest{ID: "fixture-plugin", Name: "测试能力", Version: "1", Protocol: 1, OS: runtime.GOOS, Arch: runtime.GOARCH, Entrypoint: "bin/plugin", SHA256: hex.EncodeToString(hash[:]), Capabilities: []string{"fixture.read.v1"}, Access: map[string]string{"fixture.read.v1": "member"}, Configuration: []p.Field{{Key: "password", Label: "密码", Type: "secret", Required: true}, {Key: "scope", Label: "范围", Type: "json"}}}
	if mutate != nil {
		mutate(&manifest)
	}
	buf := new(bytes.Buffer)
	z := zip.NewWriter(buf)
	raw, _ := json.Marshal(manifest)
	for name, b := range map[string][]byte{"manifest.json": raw, "bin/plugin": binary} {
		f, _ := z.Create(name)
		f.Write(b)
	}
	if extra != "" {
		f, _ := z.Create(extra)
		f.Write([]byte("bad"))
	}
	z.Close()
	return buf.Bytes()
}
func TestPackageValidationAndSecrets(t *testing.T) {
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	for _, mutate := range []func(*p.Manifest){func(v *p.Manifest) { v.Protocol = 99 }, func(v *p.Manifest) { v.Arch = "bad" }, func(v *p.Manifest) { v.SHA256 = strings.Repeat("0", 64) }, func(v *p.Manifest) { v.Entrypoint = "../escape" }, func(v *p.Manifest) { v.Access["unknown.v1"] = "member" }} {
		if _, err = m.Install(testPackage(t, mutate, "")); err == nil {
			t.Fatal("invalid package installed")
		}
	}
	if _, err = m.Install(testPackage(t, nil, "../escape")); err == nil {
		t.Fatal("zip traversal accepted")
	}
	v, err := m.Install(testPackage(t, nil, ""))
	if err != nil || v.State != "installed" {
		t.Fatal(err)
	}
	if _, err = m.Install(testPackage(t, nil, "")); err == nil {
		t.Fatal("duplicate installed")
	}
	if err = m.Configure("fixture-plugin", map[string]any{"password": "private-secret", "scope": []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	if err = m.Configure("fixture-plugin", map[string]any{"password": "", "scope": []string{"b"}}); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(m.List(true))
	if bytes.Contains(raw, []byte("private-secret")) {
		t.Fatal("secret exposed")
	}
	if m.config("fixture-plugin")["password"] != "private-secret" {
		t.Fatal("blank secret lost")
	}
	stat, _ := os.Stat(filepath.Join(m.root, "fixture-plugin", "config.json"))
	if stat.Mode().Perm() != 0600 {
		t.Fatal("config mode")
	}
	if len(m.List(false)) != 0 {
		t.Fatal("inactive plugin shown")
	}
	if err = m.Remove("../../escape"); err == nil {
		t.Fatal("unknown id removed")
	}
}
func TestRelativeRootStartsPluginAndResolvesConfiguration(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Rel(cwd, filepath.Join(t.TempDir(), "data with spaces", "extensions"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if _, err = m.Install(testPackage(t, nil, "")); err != nil {
		t.Fatal(err)
	}
	if err = m.Configure("fixture-plugin", map[string]any{"password": "fixture"}); err != nil {
		t.Fatal(err)
	}
	if err = m.Enable("fixture-plugin"); err != nil {
		t.Fatalf("start with relative data root: %v", err)
	}
	response, err := m.Invoke(context.Background(), "fixture-plugin", p.Request{Capability: "fixture.read.v1", Operation: "paths", Actor: p.Actor{User: "fixture", Role: "admin"}, Input: json.RawMessage(`{}`)}, false)
	if err != nil {
		t.Fatal(err)
	}
	var paths struct {
		Readable  bool   `json:"config_readable"`
		Directory string `json:"working_directory"`
	}
	if err = json.Unmarshal(response.Data, &paths); err != nil || !paths.Readable || paths.Directory != "fixture-plugin" {
		t.Fatalf("plugin paths: %s (%v)", response.Data, err)
	}
}

func TestProcessHotPlugIsolationAndRestore(t *testing.T) {
	root := t.TempDir()
	m, _ := New(root)
	defer m.Close()
	if _, err := m.Install(testPackage(t, nil, "")); err != nil {
		t.Fatal(err)
	}
	if err := m.Configure("fixture-plugin", map[string]any{"password": "secret"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Enable("fixture-plugin"); err != nil {
		t.Fatal(err)
	}
	if len(m.List(false)) != 1 {
		t.Fatal("healthy plugin undiscovered")
	}
	req := p.Request{Capability: "fixture.read.v1", Operation: "read", Actor: p.Actor{User: "alice", Role: "member"}, Input: json.RawMessage(`{}`)}
	result, err := m.Invoke(context.Background(), "fixture-plugin", req, false)
	if err != nil || !bytes.Contains(result.Data, []byte("alice")) {
		t.Fatal("independent process invocation", err)
	}
	if err = m.Configure("fixture-plugin", map[string]any{}); err == nil {
		t.Fatal("running config mutated")
	}
	if err = m.Remove("fixture-plugin"); err == nil {
		t.Fatal("running plugin removed")
	}
	unknown := req
	unknown.Capability = "unknown.v1"
	if _, err = m.Invoke(context.Background(), "fixture-plugin", unknown, false); err == nil {
		t.Fatal("unsupported capability invoked")
	}
	req.Operation = "wait"
	done := make(chan error, 1)
	go func() { _, err := m.Invoke(context.Background(), "fixture-plugin", req, false); done <- err }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		active := m.items["fixture-plugin"].run
		count := 0
		if active != nil {
			count = len(active.slots)
		}
		m.mu.Unlock()
		if count > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = m.Disable("fixture-plugin"); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err == nil {
			t.Fatal("query survived disable")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("disable did not cancel")
	}
	if _, err = m.Invoke(context.Background(), "fixture-plugin", req, false); err == nil {
		t.Fatal("stopped plugin callable")
	}
	if err = m.Enable("fixture-plugin"); err != nil {
		t.Fatal(err)
	}
	m.Close()
	restored, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	restored.Restore()
	if len(restored.List(false)) != 1 {
		t.Fatal("enabled state not restored")
	}
	if err = restored.Disable("fixture-plugin"); err != nil {
		t.Fatal(err)
	}
	if err = restored.Remove("fixture-plugin"); err != nil {
		t.Fatal(err)
	}
	if len(restored.List(true)) != 0 {
		t.Fatal("uninstall left registry")
	}
}
