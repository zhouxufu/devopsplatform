package extensions

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	p "logplatform/pkg/extensionprotocol"
)

var identifier = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
var configKey = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
var capability = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,100}\.v[1-9][0-9]*$`)

type instance struct {
	Manifest p.Manifest
	Enabled  bool
	State    string
	run      *process
}
type process struct {
	cmd        *exec.Cmd
	done       chan struct{}
	ctx        context.Context
	cancel     context.CancelFunc
	token, url string
	client     *http.Client
	slots      chan struct{}
}
type View struct {
	Manifest          p.Manifest     `json:"manifest"`
	Enabled           bool           `json:"enabled"`
	State             string         `json:"state"`
	Config            map[string]any `json:"config,omitempty"`
	ConfiguredSecrets []string       `json:"configured_secrets,omitempty"`
}
type Manager struct {
	root   string
	mu     sync.Mutex
	ops    sync.Mutex
	items  map[string]*instance
	closed bool
}

func fail(code, message string) *p.Error { return &p.Error{Code: code, Message: message} }
func random() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func New(root string) (*Manager, error) {
	// The child runs from its own installation directory. Resolve the root once
	// so its executable and configuration do not become relative to that directory.
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	root = absoluteRoot
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(root, 0700); err != nil {
		return nil, err
	}
	m := &Manager{root: root, items: map[string]*instance{}}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() || !identifier.MatchString(e.Name()) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(root, e.Name(), "manifest.json"))
		if err != nil {
			continue
		}
		var manifest p.Manifest
		if json.Unmarshal(raw, &manifest) != nil || validateManifest(manifest) != nil || manifest.ID != e.Name() {
			continue
		}
		var enabled bool
		raw, _ = os.ReadFile(filepath.Join(root, e.Name(), "enabled.json"))
		json.Unmarshal(raw, &enabled)
		m.items[e.Name()] = &instance{Manifest: manifest, Enabled: enabled, State: "installed"}
	}
	return m, nil
}
func (m *Manager) Restore() {
	m.mu.Lock()
	ids := []string{}
	for id, v := range m.items {
		if v.Enabled {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.Enable(id)
	}
}
func validateManifest(v p.Manifest) error {
	if !identifier.MatchString(v.ID) || len(v.Name) == 0 || len(v.Name) > 100 || len(v.Version) == 0 || len(v.Version) > 60 || v.Protocol != p.Major || v.OS != runtime.GOOS || v.Arch != runtime.GOARCH {
		return fail("invalid_argument", "插件身份、协议版本或操作系统架构不兼容")
	}
	if !safePath(v.Entrypoint) || v.Entrypoint == "manifest.json" || len(v.SHA256) != 64 {
		return fail("invalid_argument", "插件入口或摘要无效")
	}
	if _, err := hex.DecodeString(v.SHA256); err != nil {
		return fail("invalid_argument", "插件摘要无效")
	}
	seen := map[string]bool{}
	if len(v.Capabilities) == 0 || len(v.Capabilities) > 20 || len(v.Configuration) > 40 {
		return fail("invalid_argument", "插件能力或配置字段数量无效")
	}
	for _, c := range v.Capabilities {
		if !capability.MatchString(c) || seen[c] {
			return fail("invalid_argument", "插件能力声明无效")
		}
		seen[c] = true
	}
	for cap, role := range v.Access {
		if !seen[cap] || (role != "admin" && role != "member") {
			return fail("invalid_argument", "插件能力访问声明无效")
		}
	}
	seen = map[string]bool{}
	for _, f := range v.Configuration {
		if !configKey.MatchString(f.Key) || seen[f.Key] || len(f.Label) > 256 || (f.Type != "string" && f.Type != "secret" && f.Type != "json") {
			return fail("invalid_argument", "插件配置描述无效")
		}
		seen[f.Key] = true
	}
	return nil
}
func safePath(s string) bool {
	return s != "" && !strings.Contains(s, "\\") && !strings.Contains(s, ":") && !strings.HasPrefix(s, "/") && filepath.ToSlash(filepath.Clean(s)) == s && s != ".." && !strings.HasPrefix(s, "../")
}
func atomicFile(path string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp := path + ".new"
	if err = os.WriteFile(tmp, raw, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
func (m *Manager) Install(raw []byte) (*View, error) {
	m.ops.Lock()
	defer m.ops.Unlock()
	if len(raw) > 64<<20 {
		return nil, fail("resource_exhausted", "安装包超过 64 MiB")
	}
	z, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil || len(z.File) > 32 {
		return nil, fail("invalid_argument", "插件 ZIP 格式无效或文件过多")
	}
	files := map[string][]byte{}
	total := int64(0)
	for _, f := range z.File {
		name := strings.TrimSuffix(f.Name, "/")
		if !safePath(name) || f.Mode()&os.ModeSymlink != 0 || (!f.Mode().IsRegular() && !f.FileInfo().IsDir()) {
			return nil, fail("invalid_argument", "安装包包含不安全路径或特殊文件")
		}
		if f.FileInfo().IsDir() {
			continue
		}
		if _, ok := files[name]; ok {
			return nil, fail("invalid_argument", "安装包包含重复文件")
		}
		if f.UncompressedSize64 > 96<<20 {
			return nil, fail("resource_exhausted", "插件文件过大")
		}
		r, e := f.Open()
		if e != nil {
			return nil, fail("invalid_argument", "读取安装包失败")
		}
		b, e := io.ReadAll(io.LimitReader(r, 96<<20+1))
		r.Close()
		total += int64(len(b))
		if e != nil || total > 96<<20 {
			return nil, fail("resource_exhausted", "插件解压内容过大")
		}
		files[name] = b
	}
	var manifest p.Manifest
	if len(files["manifest.json"]) > 64<<10 || json.Unmarshal(files["manifest.json"], &manifest) != nil {
		return nil, fail("invalid_argument", "缺少有效 manifest.json")
	}
	if err = validateManifest(manifest); err != nil {
		return nil, err
	}
	binary, ok := files[manifest.Entrypoint]
	sum := sha256.Sum256(binary)
	if !ok || hex.EncodeToString(sum[:]) != manifest.SHA256 {
		return nil, fail("invalid_argument", "插件可执行文件摘要不匹配")
	}
	m.mu.Lock()
	exists := m.items[manifest.ID] != nil
	closed := m.closed
	m.mu.Unlock()
	if exists || closed {
		return nil, fail("invalid_argument", "插件已安装或宿主正在停止，请先停用并卸载旧版")
	}
	stage, err := os.MkdirTemp(m.root, ".install-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	for name, b := range files {
		target := filepath.Join(stage, name)
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return nil, err
		}
		mode := os.FileMode(0600)
		if name == manifest.Entrypoint {
			mode = 0700
		}
		if err = os.WriteFile(target, b, mode); err != nil {
			return nil, err
		}
	}
	if err = atomicFile(filepath.Join(stage, "config.json"), map[string]any{}); err != nil {
		return nil, err
	}
	if err = atomicFile(filepath.Join(stage, "enabled.json"), false); err != nil {
		return nil, err
	}
	if err = os.Rename(stage, filepath.Join(m.root, manifest.ID)); err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.items[manifest.ID] = &instance{Manifest: manifest, State: "installed"}
	m.mu.Unlock()
	v := m.view(manifest.ID, true)
	return &v, nil
}
func (m *Manager) config(id string) map[string]any {
	var c map[string]any
	raw, _ := os.ReadFile(filepath.Join(m.root, id, "config.json"))
	json.Unmarshal(raw, &c)
	if c == nil {
		c = map[string]any{}
	}
	return c
}
func (m *Manager) view(id string, admin bool) View {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.items[id]
	if r == nil {
		return View{}
	}
	v := View{Manifest: r.Manifest, Enabled: r.Enabled, State: r.State}
	if admin {
		v.Config = m.config(id)
		for _, f := range r.Manifest.Configuration {
			if f.Type == "secret" {
				if s, _ := v.Config[f.Key].(string); s != "" {
					v.ConfiguredSecrets = append(v.ConfiguredSecrets, f.Key)
				}
				delete(v.Config, f.Key)
			}
		}
	}
	return v
}
func (m *Manager) List(admin bool) []View {
	m.mu.Lock()
	ids := []string{}
	for id, v := range m.items {
		if admin || v.State == "running" {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	out := []View{}
	for _, id := range ids {
		v := m.view(id, admin)
		if v.Manifest.ID != "" {
			out = append(out, v)
		}
	}
	return out
}
func (m *Manager) Configure(id string, config map[string]any) error {
	m.ops.Lock()
	defer m.ops.Unlock()
	m.mu.Lock()
	r := m.items[id]
	if r == nil || r.run != nil || r.Enabled {
		m.mu.Unlock()
		return fail("invalid_argument", "插件不存在或仍在运行，请先停用")
	}
	fields := r.Manifest.Configuration
	m.mu.Unlock()
	old := m.config(id)
	allowed := map[string]bool{}
	for _, f := range fields {
		allowed[f.Key] = true
		v := config[f.Key]
		if f.Type == "secret" && (v == nil || v == "") {
			config[f.Key] = old[f.Key]
			v = config[f.Key]
		}
		if f.Required && (v == nil || v == "") {
			return fail("invalid_argument", f.Label+"必填")
		}
		if f.Type != "json" && v != nil {
			if _, ok := v.(string); !ok {
				return fail("invalid_argument", "配置字段类型错误")
			}
		}
	}
	for key := range config {
		if !allowed[key] {
			return fail("invalid_argument", "包含未声明配置字段")
		}
	}
	return atomicFile(filepath.Join(m.root, id, "config.json"), config)
}
func (m *Manager) Enable(id string) error {
	m.ops.Lock()
	defer m.ops.Unlock()
	m.mu.Lock()
	r := m.items[id]
	if r == nil || m.closed {
		m.mu.Unlock()
		return fail("invalid_argument", "插件不存在或宿主已关闭")
	}
	if r.run != nil {
		m.mu.Unlock()
		return fail("invalid_argument", "插件正在运行或启动")
	}
	r.State = "starting"
	manifest := r.Manifest
	m.mu.Unlock()
	bootError := func(err error) error { m.mu.Lock(); r.State = "error"; m.mu.Unlock(); return err }
	binary := filepath.Join(m.root, id, manifest.Entrypoint)
	raw, err := os.ReadFile(binary)
	if err != nil {
		return bootError(fail("invalid_argument", "插件入口不可读"))
	}
	hash := sha256.Sum256(raw)
	if hex.EncodeToString(hash[:]) != manifest.SHA256 {
		return bootError(fail("invalid_argument", "插件入口摘要已变化"))
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return bootError(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	run := &process{ctx: ctx, cancel: cancel, token: random(), url: "http://" + addr, done: make(chan struct{}), slots: make(chan struct{}, 4), client: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	run.cmd = exec.Command(binary)
	run.cmd.Dir = filepath.Join(m.root, id)
	run.cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "PLUGIN_LISTEN_ADDR=" + addr, "PLUGIN_AUTH_TOKEN=" + run.token, "PLUGIN_CONFIG_PATH=" + filepath.Join(m.root, id, "config.json")}
	run.cmd.Stdout = io.Discard
	run.cmd.Stderr = io.Discard
	if err = run.cmd.Start(); err != nil {
		cancel()
		return bootError(fail("unavailable", "插件进程启动失败"))
	}
	m.mu.Lock()
	r.run = run
	m.mu.Unlock()
	go func() {
		run.cmd.Wait()
		close(run.done)
		cancel()
		m.mu.Lock()
		if r.run == run {
			r.run = nil
			r.State = "error"
		}
		m.mu.Unlock()
	}()
	bootCtx, bootCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer bootCancel()
	ready := false
	var configurationError *p.Error
	for i := 0; i < 50; i++ {
		select {
		case <-run.done:
			i = 50
		default:
			var d p.Description
			body, e := run.call(bootCtx, "GET", "/v1/describe", nil)
			if e == nil && json.Unmarshal(body, &d) == nil && d.Protocol == p.Major && d.ID == id && d.Version == manifest.Version && reflect.DeepEqual(d.Capabilities, manifest.Capabilities) {
				var health struct {
					OK    bool     `json:"ok"`
					Error *p.Error `json:"error"`
				}
				b, e := run.call(bootCtx, "GET", "/v1/health", nil)
				if e == nil && json.Unmarshal(b, &health) == nil {
					if health.OK {
						ready = true
					} else if health.Error != nil && health.Error.Code == "invalid_argument" && len(health.Error.Message) <= 500 {
						configurationError = health.Error
					}
				}
			}
		}
		if ready || configurationError != nil || bootCtx.Err() != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		m.stop(r)
		if configurationError != nil {
			return bootError(configurationError)
		}
		return bootError(fail("unavailable", "插件未就绪或协议声明不匹配"))
	}
	if err = atomicFile(filepath.Join(m.root, id, "enabled.json"), true); err != nil {
		m.stop(r)
		return bootError(err)
	}
	m.mu.Lock()
	if r.run != run {
		m.mu.Unlock()
		return bootError(fail("unavailable", "插件就绪后异常退出"))
	}
	r.State = "running"
	r.Enabled = true
	m.mu.Unlock()
	return nil
}
func (r *process) call(ctx context.Context, method, path string, input []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	stop := context.AfterFunc(r.ctx, cancel)
	defer stop()
	req, err := http.NewRequestWithContext(ctx, method, r.url+path, bytes.NewReader(input))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fail("unavailable", "插件连接不可用或查询已取消")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20+1))
	if err != nil || len(raw) > 2<<20 {
		return nil, fail("resource_exhausted", "插件响应超过限制")
	}
	if resp.StatusCode != 200 {
		return nil, fail("unavailable", "插件返回异常状态")
	}
	return raw, nil
}
func (m *Manager) stop(r *instance) {
	m.mu.Lock()
	run := r.run
	r.run = nil
	r.State = "stopping"
	m.mu.Unlock()
	if run != nil {
		run.cancel()
		run.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-run.done:
		case <-time.After(5 * time.Second):
			run.cmd.Process.Kill()
			<-run.done
		}
		run.client.CloseIdleConnections()
	}
	m.mu.Lock()
	r.State = "stopped"
	m.mu.Unlock()
}
func (m *Manager) Disable(id string) error {
	m.ops.Lock()
	defer m.ops.Unlock()
	m.mu.Lock()
	r := m.items[id]
	m.mu.Unlock()
	if r == nil {
		return fail("invalid_argument", "插件不存在")
	}
	if err := atomicFile(filepath.Join(m.root, id, "enabled.json"), false); err != nil {
		return err
	}
	m.stop(r)
	m.mu.Lock()
	r.Enabled = false
	m.mu.Unlock()
	return nil
}
func (m *Manager) Remove(id string) error {
	m.ops.Lock()
	defer m.ops.Unlock()
	m.mu.Lock()
	r := m.items[id]
	if r == nil || r.run != nil || r.Enabled {
		m.mu.Unlock()
		return fail("invalid_argument", "请先停用插件")
	}
	m.mu.Unlock()
	if err := os.RemoveAll(filepath.Join(m.root, id)); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.items, id)
	m.mu.Unlock()
	return nil
}
func (m *Manager) Close() {
	m.ops.Lock()
	defer m.ops.Unlock()
	m.mu.Lock()
	m.closed = true
	items := []*instance{}
	for _, r := range m.items {
		items = append(items, r)
	}
	m.mu.Unlock()
	for _, r := range items {
		m.stop(r)
	}
}
func (m *Manager) Invoke(ctx context.Context, id string, request p.Request, test bool) (*p.Response, error) {
	m.mu.Lock()
	r := m.items[id]
	if r == nil || r.State != "running" || r.run == nil {
		m.mu.Unlock()
		return nil, fail("unavailable", "插件尚未启用或已停止")
	}
	run := r.run
	identity := &p.ProviderInfo{ID: r.Manifest.ID, Version: r.Manifest.Version, Digest: r.Manifest.SHA256}
	if request.ExpectedVersion != "" && request.ExpectedVersion != identity.Version || request.ExpectedDigest != "" && request.ExpectedDigest != identity.Digest {
		m.mu.Unlock()
		return nil, fail("version_mismatch", "插件版本或安装包已变化，请重新预览并审核")
	}
	if request.Actor.Role != "admin" && r.Manifest.Access[request.Capability] != "member" {
		m.mu.Unlock()
		return nil, fail("permission_denied", "无权调用该插件能力")
	}
	supported := false
	for _, cap := range r.Manifest.Capabilities {
		if request.Capability == cap {
			supported = true
		}
	}
	m.mu.Unlock()
	if !test && !supported {
		return nil, fail("unsupported", "插件未声明该能力")
	}
	select {
	case run.slots <- struct{}{}:
		defer func() { <-run.slots }()
	default:
		return nil, fail("resource_exhausted", "插件查询并发已满，请稍后重试")
	}
	request.RequestID = random()
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	path := "/v1/invoke"
	if test {
		path = "/v1/test"
	}
	body, err := run.call(ctx, "POST", path, raw)
	if err != nil {
		return nil, err
	}
	var response p.Response
	if json.Unmarshal(body, &response) != nil || (response.Error == nil && len(response.Data) == 0) {
		return nil, fail("internal", "插件响应格式无效")
	}
	response.Provider = identity
	return &response, nil
}
func (m *Manager) String() string { return fmt.Sprintf("capability plugins in %s", m.root) }
