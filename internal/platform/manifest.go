// Package platform 平台内核：插件 manifest 发现、进程守护、网关、服务注册表、事件总线。
// 插件可独立运行或复用 internal 后台；管理面只听 127.0.0.1 供网关转发。
// 数据面（如 agent 上报）由插件自行监听内网并鉴权。
package platform

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Manifest 插件声明（plugins/<name>/manifest.yaml）
type Manifest struct {
	Name      string   `yaml:"name" json:"name"`
	Display   string   `yaml:"display" json:"display"`
	Version   string   `yaml:"version" json:"version"`
	Binary    string   `yaml:"binary" json:"-"` // 相对 manifest 目录
	Args      []string `yaml:"args" json:"-"`
	AdminAddr string   `yaml:"admin_addr" json:"admin_addr"` // 如 127.0.0.1:19801，网关转发目标
	NavIcon   string   `yaml:"nav_icon" json:"nav_icon"`
	Enabled   *bool    `yaml:"enabled" json:"enabled"` // 缺省 true
	Dir       string   `yaml:"-" json:"-"`
	URL       string   `yaml:"-" json:"url"` // 壳 iframe 地址（发现时填充）
	Backend   string   `yaml:"backend" json:"-"`
	UIPath    string   `yaml:"ui_path" json:"-"`
	Internal  bool     `yaml:"internal" json:"-"`
}

// DiscoverPlugins 扫描插件目录，返回启用的 manifest 列表
func DiscoverPlugins(root string) ([]*Manifest, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("plugins dir: %w", err)
	}
	var out []*Manifest
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		mp := filepath.Join(root, e.Name(), "manifest.yaml")
		raw, err := os.ReadFile(mp)
		if err != nil {
			continue
		}
		m := &Manifest{Dir: filepath.Join(root, e.Name())}
		if err := yaml.Unmarshal(raw, m); err != nil {
			return nil, fmt.Errorf("parse %s: %w", mp, err)
		}
		if m.Name == "" || (m.Backend == "" && (m.Binary == "" || m.AdminAddr == "")) {
			continue // 契约不全，跳过
		}
		if m.Enabled != nil && !*m.Enabled {
			continue
		}
		if m.Backend == "" && !filepath.IsAbs(m.Binary) {
			// 转绝对路径：插件进程的 cwd 是其目录，相对路径需按 platform 的 cwd 解析
			if abs, err := filepath.Abs(filepath.Join(m.Dir, m.Binary)); err == nil {
				m.Binary = abs
			}
		}
		m.URL = "/api/plugins/" + m.Name + "/ui"
		out = append(out, m)
	}
	byName := map[string]*Manifest{}
	for _, m := range out {
		byName[m.Name] = m
	}
	for _, m := range out {
		if m.Backend == "" {
			continue
		}
		backend := byName[m.Backend]
		if backend == nil || !backend.Internal || backend.Backend != "" {
			return nil, fmt.Errorf("plugin %s requires enabled internal backend %s", m.Name, m.Backend)
		}
		if m.UIPath != "/ui" && m.UIPath != "/ui/dbms" && m.UIPath != "/ui/redis" && m.UIPath != "/ui/research" && m.UIPath != "/ui/api-center" {
			return nil, fmt.Errorf("plugin %s: unsupported ui_path", m.Name)
		}
		m.AdminAddr = backend.AdminAddr
	}
	return out, nil
}
