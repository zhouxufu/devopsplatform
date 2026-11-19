package smartdocplugin

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"logplatform/internal/extensions"
	"logplatform/internal/platform"
	"logplatform/pkg/apidoc"
	p "logplatform/pkg/extensionprotocol"
	"logplatform/pkg/workflownode"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	if token := os.Getenv("PLUGIN_AUTH_TOKEN"); token != "" {
		_ = http.ListenAndServe(os.Getenv("PLUGIN_LISTEN_ADDR"), Handler(token))
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func TestInstalledPluginIsDiscoveredByOrchestrationAndPinned(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(binary)
	manifest := p.Manifest{ID: ID, Name: "Smart-doc 接口文档", Version: Version, Protocol: p.Major, OS: runtime.GOOS, Arch: runtime.GOARCH, Entrypoint: "bin/plugin", SHA256: hex.EncodeToString(sum[:]), Capabilities: []string{apidoc.Capability, workflownode.Capability}, Access: map[string]string{workflownode.Capability: "member", apidoc.Capability: "admin"}}
	buffer := new(bytes.Buffer)
	z := zip.NewWriter(buffer)
	raw, _ := json.Marshal(manifest)
	for name, body := range map[string][]byte{"manifest.json": raw, "bin/plugin": binary} {
		f, _ := z.Create(name)
		_, _ = f.Write(body)
	}
	_ = z.Close()
	manager, err := extensions.New(filepath.Join(t.TempDir(), "extensions"))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if _, err := manager.Install(buffer.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Configure(ID, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Enable(ID); err != nil {
		t.Fatal(err)
	}
	reg, err := platform.OpenRegistryDatabase(platform.RegistryConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "registry.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	g := platform.NewGateway("")
	g.SetExtensions(manager)
	h := g.Routes(reg, platform.NewBus())
	request := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/workflow-node-providers", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	visible := request()
	if visible.Code != 200 || !strings.Contains(visible.Body.String(), `"key":"api-documentation"`) {
		t.Fatal(visible.Body.String())
	}
	input, _ := json.Marshal(apidoc.PlanInput{Tool: "maven", Settings: json.RawMessage(`{"plugin_version":"2.7.3"}`)})
	out, err := manager.Invoke(context.Background(), ID, p.Request{Capability: apidoc.Capability, Operation: "plan", Actor: p.Actor{Role: "admin"}, Input: input}, false)
	if err != nil || out.Provider == nil || out.Provider.Digest != manifest.SHA256 || out.Provider.Version != Version {
		t.Fatalf("identity was not host supplied: %+v %v", out, err)
	}
	if _, err := manager.Invoke(context.Background(), ID, p.Request{Capability: apidoc.Capability, Operation: "plan", ExpectedVersion: "other", Actor: p.Actor{Role: "admin"}, Input: input}, false); err == nil {
		t.Fatal("changed version accepted")
	}
	if _, err := manager.Invoke(context.Background(), ID, p.Request{Capability: apidoc.Capability, Operation: "plan", ExpectedDigest: strings.Repeat("a", 64), Actor: p.Actor{Role: "admin"}, Input: input}, false); err == nil {
		t.Fatal("changed package accepted")
	}
	if err := manager.Disable(ID); err != nil {
		t.Fatal(err)
	}
	hidden := request()
	if hidden.Code != 200 || strings.Contains(hidden.Body.String(), `"key":"api-documentation"`) {
		t.Fatal("disabled node still selectable")
	}
}
