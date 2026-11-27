package deploy

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type resourceDiscoveryRequest struct {
	RuntimeName             string            `json:"runtime_name,omitempty"`
	SourceName              string            `json:"source_name,omitempty"`
	SourceFormat            string            `json:"source_format,omitempty"`
	Adapter                 string            `json:"adapter,omitempty"`
	Job                     string            `json:"job,omitempty"`
	ConfigDataID            string            `json:"config_data_id,omitempty"`
	ConfigGroup             string            `json:"config_group,omitempty"`
	XXLJobAddressKey        string            `json:"xxl_job_address_key,omitempty"`
	RequireEnvironmentScope bool              `json:"require_environment_scope,omitempty"`
	Environments            []string          `json:"environments"`
	Connections             map[string]string `json:"connections,omitempty"`
	Profiles                map[string]string `json:"profiles,omitempty"`
	BootstrapPath           string            `json:"bootstrap_path,omitempty"`
	BootstrapContent        string            `json:"bootstrap_content,omitempty"`
	PomContent              string            `json:"pom_content,omitempty"`
}
type resourceCandidate struct {
	OriginalAlias string                          `json:"original_alias,omitempty"`
	CanDeclare    bool                            `json:"can_declare,omitempty"`
	ID            string                          `json:"id"`
	Environment   string                          `json:"environment"`
	Alias         string                          `json:"alias"`
	Kind          string                          `json:"kind"`
	Source        string                          `json:"source"`
	State         string                          `json:"state"`
	Message       string                          `json:"message,omitempty"`
	Endpoint      string                          `json:"endpoint,omitempty"`
	Database      string                          `json:"database,omitempty"`
	DataID        string                          `json:"data_id,omitempty"`
	Group         string                          `json:"group,omitempty"`
	Connection    string                          `json:"connection,omitempty"`
	AppName       string                          `json:"app_name,omitempty"`
	Supported     bool                            `json:"supported"`
	Applyable     bool                            `json:"applyable"`
	Requirement   *ApplicationResourceRequirement `json:"requirement,omitempty"`
	Binding       *ApplicationEnvironmentResource `json:"binding,omitempty"`
}
type resourceDiscoveryResult struct {
	coverage     map[string]ResourceSourceEvidence
	Origin       ResourceSourceEvidence `json:"origin"`
	Jobs         []string               `json:"jobs,omitempty"`
	Items        []resourceCandidate    `json:"items"`
	Source       string                 `json:"source"`
	SourceDigest string                 `json:"source_digest"`
	Warnings     []string               `json:"warnings"`
}

var mavenYAMLPlaceholder = regexp.MustCompile(`(?m)(:\s*)@([^@\r\n]+)@(\s*(?:#.*)?$)`)
var springProperty = regexp.MustCompile(`\$\{([^{}]+)\}|@([^@\r\n]+)@`)

func resourceYAML(raw string) (map[string]any, error) {
	if len(raw) > 512<<10 {
		return nil, fmt.Errorf("单份配置不能超过 512 KiB")
	}
	raw = mavenYAMLPlaceholder.ReplaceAllString(raw, `${1}"@${2}@"${3}`)
	out := map[string]any{}
	if yaml.Unmarshal([]byte(raw), &out) != nil {
		return nil, fmt.Errorf("配置 YAML 格式无效，请核对源文件；错误正文不会回显")
	}
	return out, nil
}
func resourceMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
func resourcePath(m map[string]any, keys ...string) any {
	var value any = m
	for _, key := range keys {
		value = resourceMap(value)[key]
	}
	return value
}
func resourceString(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}
func flattenResourceProperties(value map[string]any, prefix string, out map[string]string) {
	for key, v := range value {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		if child, ok := v.(map[string]any); ok {
			flattenResourceProperties(child, path, out)
		} else if _, ok := v.([]any); !ok {
			out[path] = resourceString(v)
		}
	}
}
func resolveResourceProperty(value string, properties map[string]string) string {
	for i := 0; i < 8; i++ {
		next := springProperty.ReplaceAllStringFunc(value, func(token string) string {
			parts := springProperty.FindStringSubmatch(token)
			key := parts[1]
			if key == "" {
				key = parts[2]
			}
			pair := strings.SplitN(key, ":", 2)
			if v, ok := properties[pair[0]]; ok {
				return v
			}
			if len(pair) == 2 {
				return pair[1]
			}
			return token
		})
		if next == value {
			break
		}
		value = next
	}
	return value
}
func resourcePOMProperties(raw, profile string) map[string]string {
	type properties struct {
		Values []struct {
			XMLName xml.Name
			Value   string `xml:",chardata"`
		} `xml:",any"`
	}
	var document struct {
		Properties properties `xml:"properties"`
		Profiles   []struct {
			ID         string     `xml:"id"`
			Properties properties `xml:"properties"`
		} `xml:"profiles>profile"`
	}
	out := map[string]string{}
	if len(raw) > 512<<10 || xml.Unmarshal([]byte(raw), &document) != nil {
		return out
	}
	for _, p := range document.Properties.Values {
		out[p.XMLName.Local] = strings.TrimSpace(p.Value)
	}
	for _, p := range document.Profiles {
		if p.ID == profile {
			for _, property := range p.Properties.Values {
				out[property.XMLName.Local] = strings.TrimSpace(property.Value)
			}
		}
	}
	return out
}
func readResourceSourceFile(root, path string) (string, error) {
	if path == "" || filepath.IsAbs(path) || strings.Contains(path, "\\") {
		return "", fmt.Errorf("源码路径必须是应用工作区内的相对路径")
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("应用源码目录不存在")
	}
	root = canonicalRoot
	resolved, err := filepath.EvalSymlinks(filepath.Join(root, path))
	if err != nil {
		return "", fmt.Errorf("源码文件不存在")
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("源码文件越出应用工作区")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 512<<10 {
		return "", fmt.Errorf("源码必须是 512 KiB 内的普通文件")
	}
	b, err := os.ReadFile(resolved)
	if err != nil {
		return "", fmt.Errorf("源码读取失败")
	}
	return string(b), nil
}
func (s *Store) resourceDiscoverySource(service string, req resourceDiscoveryRequest) (string, string, string, error) {
	if req.BootstrapContent != "" {
		if len(req.BootstrapContent) > 512<<10 || len(req.PomContent) > 512<<10 {
			return "", "", "", fmt.Errorf("上传的源文件过大")
		}
		name := strings.TrimSpace(req.SourceName)
		if name == "" {
			name = "上传源文件"
		}
		if len(name) > 255 || strings.ContainsAny(name, "/\\\r\n\x00") {
			return "", "", "", fmt.Errorf("上传来源请填写不含路径的文件名")
		}
		return req.BootstrapContent, req.PomContent, name, nil
	}
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`).MatchString(service) {
		return "", "", "", fmt.Errorf("应用标识无效")
	}
	parent, err := filepath.EvalSymlinks(filepath.Join(s.dataDir, "workspace"))
	if err != nil {
		return "", "", "", fmt.Errorf("尚无已拉取源码，可在识别区上传 bootstrap 与 pom.xml；不会自动构建或拉取")
	}
	root, err := filepath.EvalSymlinks(filepath.Join(parent, service))
	if err != nil {
		return "", "", "", fmt.Errorf("应用尚无已拉取源码，请上传源文件或稍后重试")
	}
	relative, err := filepath.Rel(parent, root)
	if err != nil || relative != service {
		return "", "", "", fmt.Errorf("应用源码目录归属无法确认")
	}
	path := req.BootstrapPath
	if path == "" {
		count := 0
		_ = filepath.WalkDir(root, func(pathName string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return filepath.SkipDir
			}
			count++
			if count > 6000 {
				return filepath.SkipAll
			}
			if entry.IsDir() {
				switch entry.Name() {
				case ".git", "node_modules", "target", "build", "java", "test", ".gradle":
					return filepath.SkipDir
				}
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if entry.Name() != "bootstrap.yml" && entry.Name() != "bootstrap.yaml" && entry.Name() != "application.yml" && entry.Name() != "application.yaml" {
				return nil
			}
			rel, e := filepath.Rel(root, pathName)
			if e != nil {
				return nil
			}
			body, e := readResourceSourceFile(root, rel)
			if e != nil {
				return nil
			}
			doc, e := resourceYAML(body)
			expected := service
			if req.RuntimeName != "" {
				expected = req.RuntimeName
			}
			if e == nil && resourceString(resourcePath(doc, "spring", "application", "name")) == expected {
				if strings.HasPrefix(filepath.Base(path), "bootstrap.") && strings.HasPrefix(entry.Name(), "application.") {
					return nil
				}
				if strings.HasPrefix(filepath.Base(path), "application.") && strings.HasPrefix(entry.Name(), "bootstrap.") {
					path = ""
				}
				if path != "" {
					path = "AMBIGUOUS"
					return filepath.SkipAll
				}
				path = rel
			}
			return nil
		})
	}
	if path == "" || path == "AMBIGUOUS" {
		return "", "", "", fmt.Errorf("无法唯一识别应用 bootstrap，请指定相对路径或上传源码")
	}
	body, err := readResourceSourceFile(root, path)
	if err != nil {
		return "", "", "", err
	}
	if req.RuntimeName != "" && req.RuntimeName != service {
		svc := s.GetService(service)
		module := ""
		if svc != nil {
			for _, marker := range []string{"/target/", "/build/"} {
				if at := strings.Index(filepath.ToSlash(svc.ArtifactGlob), marker); at >= 0 {
					module = filepath.ToSlash(svc.ArtifactGlob)[:at]
					break
				}
			}
		}
		if module == "" || !strings.HasPrefix(filepath.ToSlash(path), module+"/") {
			return "", "", "", fmt.Errorf("运行名映射的源码路径与该应用配置的产物模块不一致；请核对应用配置")
		}
	}
	pom := req.PomContent
	if pom == "" {
		pom, _ = readResourceSourceFile(root, "pom.xml")
	}
	return body, pom, path, nil
}
func (s *Store) discoverApplicationResources(ctx context.Context, service string, req resourceDiscoveryRequest) (resourceDiscoveryResult, error) {
	out := resourceDiscoveryResult{Items: []resourceCandidate{}, Warnings: []string{}}
	if len(req.Environments) == 0 || len(req.Environments) > 10 {
		return out, fmt.Errorf("请显式选择要识别的环境，最多 10 个")
	}
	req.XXLJobAddressKey = strings.TrimSpace(req.XXLJobAddressKey)
	if req.XXLJobAddressKey != "" && !xxlJobPropertyKey.MatchString(req.XXLJobAddressKey) {
		return out, fmt.Errorf("XXL-JOB 调度中心地址配置键格式无效，请填写属性名")
	}
	bootstrap, pom, source, err := s.resourceDiscoverySource(service, req)
	if err != nil {
		return out, err
	}
	doc, err := resourceDiscoveryDocument(bootstrap, req.SourceFormat)
	if err != nil {
		return out, err
	}
	runtimeName := service
	if req.RuntimeName != "" {
		runtimeName = req.RuntimeName
	}
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`).MatchString(runtimeName) {
		return out, fmt.Errorf("运行名格式无效")
	}
	if name := resourceString(resourcePath(doc, "spring", "application", "name")); name != "" && name != runtimeName {
		return out, fmt.Errorf("源码中的运行名与当前映射不一致；请显式填写Spring运行名，平台应用标识保持不变")
	}
	if req.Adapter != "" && req.Adapter != "spring" && req.Adapter != "custom" && req.Adapter != "flink" {
		return out, fmt.Errorf("不支持的依赖配置格式")
	}
	if req.Adapter == "custom" || req.Adapter == "flink" {
		if strings.TrimSpace(req.ConfigDataID) == "" || strings.TrimSpace(req.ConfigGroup) == "" || len(req.ConfigDataID) > 255 || len(req.ConfigGroup) > 255 || strings.ContainsAny(req.ConfigDataID+req.ConfigGroup, "\r\n\x00") {
			return out, fmt.Errorf("固定配置识别须明确Data ID与Group")
		}
	}
	out.Source = source
	out.SourceDigest = "hmac:" + s.privateResourceDigest([]string{bootstrap, pom})
	out.Origin = ResourceSourceEvidence{File: source, Digest: out.SourceDigest, RuntimeName: runtimeName}
	if svc := s.GetService(service); svc != nil {
		out.Origin.Artifact = svc.ArtifactGlob
		if u, e := url.Parse(svc.GitURL); e == nil {
			u.User = nil
			u.RawQuery = ""
			u.Fragment = ""
			out.Origin.Repository = u.String()
		}
	}
	connections := s.Connections()
	saved := s.ApplicationResources(service)
	environments := map[string]bool{}
	currentOrigin := out.Origin
	appendItem := func(c resourceCandidate) {
		identity := []any{c.Environment, c.Alias, c.Kind, c.Connection, c.Endpoint, c.Database, c.Group, c.DataID}
		if c.Kind == xxlJobKind {
			identity = append(identity, c.AppName)
		}
		c.ID = strings.TrimPrefix(digestJSON(identity), "sha256:")
		if c.Applyable || c.CanDeclare {
			evidence := currentOrigin
			evidence.State = "declared"
			evidence.Profile = req.Profiles[c.Environment]
			if evidence.Profile == "" {
				evidence.Profile = c.Environment
			}
			if !c.Applyable && c.Kind == "nacos" {
				evidence.State = "pending_source"
				if c.State == "configuration_missing" {
					evidence.State = "missing_config"
				}
			}
			c.Requirement = &ApplicationResourceRequirement{Service: service, Alias: c.Alias, Kind: c.Kind, Description: c.Source, Sources: map[string]ResourceSourceEvidence{c.Environment: evidence}}
		}
		if c.Applyable {
			config := map[string]string{}
			if c.Kind == "mysql" {
				config["database"] = c.Database
			}
			if c.Kind == "nacos" {
				config["data_id"] = c.DataID
				config["group"] = c.Group
			}
			if c.Kind == xxlJobKind {
				config["app_name"] = c.AppName
			}
			c.Binding = &ApplicationEnvironmentResource{Service: service, Environment: c.Environment, Alias: c.Alias, Connection: c.Connection, Config: config}
		}
		out.Items = append(out.Items, c)
	}
	for _, environment := range req.Environments {
		if environments[environment] {
			continue
		}
		environments[environment] = true
		if s.GetEnvironment(environment) == nil {
			return out, fmt.Errorf("所选环境不存在")
		}
		profile := req.Profiles[environment]
		if profile == "" {
			profile = environment
		}
		envDoc, overlay, overlayName, e := s.resourceDiscoveryEnvironmentDocument(service, source, bootstrap, doc, req, profile)
		if e != nil {
			return out, e
		}
		currentOrigin = out.Origin
		if overlay != "" {
			currentOrigin.File = source + " + " + overlayName
			currentOrigin.Digest = "hmac:" + s.privateResourceDigest([]string{bootstrap, pom, overlay})
		}
		resourceDiscoveryCoverage(&out, environment, profile, false, currentOrigin)
		properties := resourceDiscoveryProperties(envDoc, pom, profile)
		properties["spring.application.name"] = runtimeName
		config := resourceMap(resourcePath(envDoc, "spring", "cloud", "nacos", "config"))
		namespace := resolveResourceProperty(resourceString(config["namespace"]), properties)
		serverAddress := resolveResourceProperty(resourceString(config["server-addr"]), properties)
		if namespace == "" {
			namespace = resolveResourceProperty(properties["nacos.namespace"], properties)
		}
		if serverAddress == "" {
			serverAddress = resolveResourceProperty(properties["nacos.server"], properties)
		}
		connection := req.Connections[environment]
		if connection == "" {
			for _, b := range saved.Bindings {
				if b.Environment == environment && s.GetConnection(b.Connection) != nil && s.GetConnection(b.Connection).Kind == "nacos" {
					if connection != "" && connection != b.Connection {
						return out, fmt.Errorf("环境有多个 Nacos，请显式选择识别连接")
					}
					connection = b.Connection
				}
			}
		}
		if connection == "" {
			connection = s.environmentNacos(environment)
		}
		if connection == "" {
			matches := []string{}
			for _, c := range connections {
				if c.Kind == "nacos" && c.Config["namespace"] == namespace && resourceNacosAddressMatches(serverAddress, c.Endpoint) {
					matches = append(matches, c.Name)
				}
			}
			if len(matches) == 1 {
				connection = matches[0]
			}
		}
		c := s.GetConnection(connection)
		if c == nil || c.Kind != "nacos" {
			appendItem(resourceCandidate{Environment: environment, Alias: "nacos_app", Kind: "nacos", Source: source, State: "needs_connection", Message: "请为该环境选择已有 Nacos 连接", Supported: true})
			continue
		}
		if namespace != "" && !strings.Contains(namespace, "@") && !strings.Contains(namespace, "${") && namespace != c.Config["namespace"] {
			return out, fmt.Errorf("所选 Nacos 命名空间与该环境源码不一致，请核对连接")
		}
		if req.RequireEnvironmentScope && (!s.resourceConnectionApprovedForEnvironment(connection, environment) || strings.ContainsAny(namespace, "@$") || namespace != c.Config["namespace"] || !resourceNacosAddressMatches(serverAddress, c.Endpoint)) {
			return out, fmt.Errorf("首次识别仅允许已登记到该环境的Nacos，且源码profile的地址与命名空间必须完整匹配")
		}
		client, e := newNacosClient(ctx, c)
		if e != nil {
			check := resourceFailure(e)
			out.Warnings = append(out.Warnings, environment+"："+check.Message)
			continue
		}
		group := resolveResourceProperty(resourceString(config["group"]), properties)
		if group == "" {
			group = "DEFAULT_GROUP"
		}
		if strings.ContainsAny(group, "@$") {
			return out, fmt.Errorf("Nacos Group 尚未解析，请提供对应 Maven profile 的 pom.xml")
		}
		ext := resourceString(config["file-extension"])
		if ext == "" {
			ext = "properties"
		}
		prefix := resolveResourceProperty(resourceString(config["prefix"]), properties)
		if prefix == "" {
			prefix = runtimeName
		}
		active := resolveResourceProperty(resourceString(resourcePath(envDoc, "spring", "profiles", "active")), properties)
		if active == "" || strings.ContainsAny(active, "@$") {
			active = profile
		}
		refs := []ApplicationNacosConfig{}
		for _, name := range []string{"shared-configs", "extension-configs"} {
			if list, ok := config[name].([]any); ok {
				for _, item := range list {
					m := resourceMap(item)
					dataID := resolveResourceProperty(resourceString(m["data-id"]), properties)
					g := resolveResourceProperty(resourceString(m["group"]), properties)
					if g == "" {
						g = "DEFAULT_GROUP"
					}
					alias := "nacos_" + strings.NewReplacer("-", "_", ".", "_").Replace(strings.TrimSuffix(strings.TrimSuffix(dataID, ".yaml"), ".yml"))
					if len(alias) > 32 || !validResourceName(alias) {
						alias = "nacos_" + strings.TrimPrefix(digestJSON([]string{g, dataID}), "sha256:")[:16]
					}
					refs = append(refs, ApplicationNacosConfig{Alias: alias, DataID: dataID, Group: g})
				}
			}
		}
		refs = append(refs, ApplicationNacosConfig{Alias: "nacos_base", DataID: prefix + "." + ext, Group: group}, ApplicationNacosConfig{Alias: "nacos_app", DataID: prefix + "-" + active + "." + ext, Group: group})
		if req.Adapter == "custom" || req.Adapter == "flink" {
			refs = []ApplicationNacosConfig{{Alias: "nacos_app", DataID: resolveResourceProperty(req.ConfigDataID, properties), Group: resolveResourceProperty(req.ConfigGroup, properties)}}
		}
		merged := map[string]any{}
		mergeResourceYAML(merged, envDoc)
		complete := true
		seen := map[string]bool{}
		for _, ref := range refs {
			if ref.DataID == "" || ref.Group == "" || len(ref.DataID) > 255 || len(ref.Group) > 255 || strings.ContainsAny(ref.DataID+ref.Group, "\r\n\x00") || springProperty.MatchString(ref.DataID+ref.Group) {
				return out, fmt.Errorf("Nacos引用尚未完整解析，请核对所选profile、Data ID与Group")
			}
			target := ref.Group + "\x00" + ref.DataID
			if seen[target] {
				continue
			}
			seen[target] = true
			content, readErr := client.read(ctx, ref.DataID, ref.Group)
			if readErr != nil {
				check := resourceFailure(readErr)
				if ref.Alias != "nacos_base" {
					complete = false
					appendItem(resourceCandidate{Environment: environment, Alias: ref.Alias, Kind: "nacos", Source: source, DataID: ref.DataID, Group: ref.Group, Connection: c.Name, Supported: true, CanDeclare: true, State: check.Code, Message: check.Message})
				}
				continue
			}
			appendItem(resourceCandidate{Environment: environment, Alias: ref.Alias, Kind: "nacos", Source: source, DataID: ref.DataID, Group: ref.Group, Connection: c.Name, Supported: true, State: "matched", Applyable: true})
			format := "yaml"
			if strings.HasSuffix(strings.ToLower(ref.DataID), ".properties") {
				format = "properties"
			}
			parsed, parseErr := resourceDiscoveryDocument(content, format)
			if parseErr == nil {
				mergeResourceYAML(merged, parsed)
			} else {
				complete = false
				out.Warnings = append(out.Warnings, environment+" / "+ref.DataID+"：配置引用可用，但内容未能按 "+format+" 识别")
			}
		}
		flattenResourceProperties(merged, "", properties)
		if candidate, ok := discoverXXLJobBinding(environment, req.XXLJobAddressKey, properties, connections); ok {
			if candidate.Applyable && !complete {
				candidate.Applyable = false
				candidate.State = "incomplete_source"
				candidate.Message = "有配置未能读取或解析，请核对来源后重新识别"
			}
			for _, binding := range saved.Bindings {
				if binding.Environment == environment && binding.Alias == candidate.Alias &&
					(binding.Connection != candidate.Connection || binding.Config["app_name"] != candidate.AppName) {
					candidate.Applyable = false
					candidate.State = "existing_binding"
					candidate.Message = "此环境已有其他绑定，请在资源配置中核对"
				}
			}
			appendItem(candidate)
		}
		sources := resourceMap(resourcePath(merged, "spring", "datasource", "dynamic", "datasource"))
		if len(sources) == 0 {
			if basic := resourceMap(resourcePath(merged, "spring", "datasource")); basic["url"] != nil {
				sources = map[string]any{"database": basic}
			}
		}
		custom, jobs, customComplete := resourceCustomDatasources(merged, req)
		if req.Adapter == "flink" {
			sources = custom
			if len(jobs) > 30 {
				return out, fmt.Errorf("固定配置作业超过30项，请缩小配置范围")
			}
			for _, job := range jobs {
				found := false
				for _, existing := range out.Jobs {
					found = found || existing == job
				}
				if !found {
					out.Jobs = append(out.Jobs, job)
				}
			}
			complete = complete && customComplete
			if req.Job == "" {
				out.Warnings = append(out.Warnings, environment+"：请选择本次Flink作业后重新识别数据源")
			} else if !customComplete {
				out.Warnings = append(out.Warnings, environment+"：所选Flink作业在此环境不存在，未展开其他作业")
			}
		} else {
			if sources == nil {
				sources = map[string]any{}
			}
			for name, data := range custom {
				sources[name] = data
			}
		}
		for alias, raw := range sources {
			originalAlias := alias
			alias = resourceCanonicalAlias(alias)
			data := resourceMap(raw)
			rawURL := resolveResourceProperty(resourceString(data["url"]), properties)
			u, e := url.Parse(strings.TrimPrefix(rawURL, "jdbc:"))
			candidate := resourceCandidate{Environment: environment, Alias: alias, OriginalAlias: originalAlias, Kind: "mysql", Source: source + " · 数据源 " + originalAlias, Supported: true, State: "needs_connection", Message: "没有凭据匹配的已保存连接，请在连接管理中创建后重新识别"}
			if e != nil || u.Scheme != "mysql" || u.Hostname() == "" || u.User != nil || strings.Contains(rawURL, "${") {
				complete = false
				candidate.State = "needs_review"
				candidate.Message = "数据库地址含未解析变量或不支持的格式"
				appendItem(candidate)
				continue
			}
			port := u.Port()
			if port == "" {
				port = "3306"
			}
			candidate.Endpoint = net.JoinHostPort(u.Hostname(), port)
			candidate.Database = strings.TrimPrefix(u.Path, "/")
			if !sqlIdentifierRE.MatchString(candidate.Database) {
				complete = false
				candidate.State = "invalid_database"
				candidate.Message = "数据库名称无效或含未解析范围，不能作为固定库绑定"
				appendItem(candidate)
				continue
			}
			engineHint := strings.ToLower(candidate.Endpoint + " " + originalAlias)
			if port == "9030" && (strings.Contains(engineHint, "selectdb") || strings.Contains(engineHint, "doris")) {
				candidate.State = "unsupported_engine"
				candidate.Supported = false
				candidate.Message = "此地址使用SelectDB/Doris端口，不作为MySQL自动接入"
				appendItem(candidate)
				continue
			}
			candidate.CanDeclare = true
			username := resolveResourceProperty(resourceString(data["username"]), properties)
			password := resolveResourceProperty(resourceString(data["password"]), properties)
			matches := []string{}
			for _, public := range connections {
				if public.Kind == "mysql" && public.Endpoint == candidate.Endpoint {
					private := s.GetConnection(public.Name)
					if private != nil && private.Username == username && private.Password == password {
						matches = append(matches, private.Name)
					}
				}
			}
			if len(matches) == 1 {
				candidate.Connection = matches[0]
				candidate.State = "matched"
				candidate.Message = "匹配已有实例与凭据；保存后请核验目标库"
				candidate.Applyable = true
				if alias != originalAlias {
					candidate.Message = "源别名已规范为平台逻辑别名，原运行配置保持不变；请核对后加入草稿"
				}
			} else if len(matches) > 1 {
				candidate.State = "ambiguous"
				candidate.Message = "多个已保存连接匹配，请明确选择连接后维护绑定"
			}
			appendItem(candidate)
		}
		resourceDiscoveryCoverage(&out, environment, profile, complete, currentOrigin)
		for _, dependency := range []struct {
			kind string
			path []string
		}{{"redis", []string{"spring", "redis"}}, {"redis", []string{"spring", "data", "redis"}}, {"elasticsearch", []string{"spring", "elasticsearch"}}, {"elasticsearch", []string{"easy-es"}}, {"kafka", []string{"spring", "kafka"}}} {
			if resourcePath(merged, dependency.path...) != nil {
				appendItem(resourceCandidate{Environment: environment, Alias: dependency.kind, Kind: dependency.kind, Source: "Nacos 依赖识别", Supported: dependency.kind == "redis", State: "needs_scope", Message: map[bool]string{true: "Redis 需另选 DB 和精确 Key 范围，不自动开放操作", false: "由 Nacos 引用管理，尚不支持独立连接执行"}[dependency.kind == "redis"]})
			}
		}
	}
	finalizeResourceDiscoveryCoverage(&out, req.Environments)
	sort.Strings(out.Jobs)
	sort.SliceStable(out.Items, func(i, j int) bool {
		return out.Items[i].Environment+out.Items[i].Alias < out.Items[j].Environment+out.Items[j].Alias
	})
	out.Warnings = append(out.Warnings, "动态租户数据源需显式选择范围，固定配置识别不会自动开放全部租户库")
	if len(out.Items) > 150 {
		return out, fmt.Errorf("识别结果超过 150 项，请缩小环境或源文件范围")
	}
	return out, nil
}
func mergeResourceYAML(target, source map[string]any) {
	for key, value := range source {
		if child, ok := value.(map[string]any); ok {
			existing := resourceMap(target[key])
			if existing == nil {
				existing = map[string]any{}
				target[key] = existing
			}
			mergeResourceYAML(existing, child)
		} else {
			target[key] = value
		}
	}
}
func registerResourceDiscoveryRoutes(mux *http.ServeMux, s *Store) {
	registerResourceDiscoveryAccessRoutes(mux, s)
	mux.HandleFunc("POST /api/services/{name}/resources/discover", func(w http.ResponseWriter, r *http.Request) {
		if deliveryActor(r) != "local" && r.Header.Get("X-Role") != "admin" {
			deliveryWriteError(w, 403, "依赖识别需要管理员权限")
			return
		}
		service := r.PathValue("name")
		if !s.hasResourceApplication(service) {
			deliveryWriteError(w, 404, "应用不存在")
			return
		}
		var req resourceDiscoveryRequest
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1100<<10)).Decode(&req) != nil {
			deliveryWriteError(w, 400, "识别参数无效")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		result, err := s.discoverApplicationResources(ctx, service, req)
		if err != nil {
			deliveryWriteError(w, 400, err)
			return
		}
		deliveryJSON(w, result)
	})
}

// Auto-matching needs both namespace and endpoint evidence. An unresolved address
// requires an explicit connection selection rather than guessing a cluster.
func resourceNacosAddressMatches(source, endpoint string) bool {
	if source == "" || strings.ContainsAny(source, "@$,") {
		return false
	}
	normalize := func(value string) string {
		if !strings.Contains(value, "://") {
			value = "http://" + value
		}
		u, err := url.Parse(value)
		if err != nil {
			return ""
		}
		return strings.ToLower(u.Host)
	}
	return normalize(source) != "" && normalize(source) == normalize(endpoint)
}
