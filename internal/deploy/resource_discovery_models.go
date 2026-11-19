package deploy

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func resourceDiscoveryDocument(raw, format string) (map[string]any, error) {
	if format == "" || format == "yaml" {
		return resourceYAML(raw)
	}
	if format != "properties" || len(raw) > 512<<10 {
		return nil, fmt.Errorf("源文件格式仅支持YAML或properties，且不能超过512KiB")
	}
	out := map[string]any{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		at := strings.IndexAny(line, "=:")
		if at <= 0 || strings.HasSuffix(line, "\\") {
			return nil, fmt.Errorf("properties来源需逐行key=value，不支持续行或未闭合条目")
		}
		parts := strings.Split(strings.TrimSpace(line[:at]), ".")
		if len(parts) > 10 {
			return nil, fmt.Errorf("properties字段层级过深")
		}
		target := out
		for i, key := range parts {
			if key == "" {
				return nil, fmt.Errorf("properties字段名无效")
			}
			if i == len(parts)-1 {
				if _, ok := target[key]; ok {
					return nil, fmt.Errorf("properties字段重复")
				}
				target[key] = strings.TrimSpace(line[at+1:])
				break
			}
			if target[key] == nil {
				target[key] = map[string]any{}
			}
			m, ok := target[key].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("properties字段路径冲突")
			}
			target = m
		}
	}
	return out, nil
}

func resourceDiscoveryProperties(doc map[string]any, pom, profile string) map[string]string {
	base := resourcePOMProperties(pom, profile)
	flat := map[string]string{}
	flattenResourceProperties(doc, "", flat)
	for key, value := range flat {
		flat[key] = resolveResourceProperty(value, base)
	}
	for key, value := range flat {
		base[key] = value
	}
	return base
}

func resourceCanonicalAlias(name string) string {
	if validResourceName(name) && len(name) <= 32 {
		return name
	}
	value := regexp.MustCompile(`([A-Z]+)([A-Z][a-z])`).ReplaceAllString(name, `${1}_${2}`)
	value = regexp.MustCompile(`([a-z0-9])([A-Z])`).ReplaceAllString(value, `${1}_${2}`)
	value = strings.Trim(regexp.MustCompile(`[^a-z0-9_]+`).ReplaceAllString(strings.ToLower(value), "_"), "_")
	if value == "" || value[0] < 'a' || value[0] > 'z' {
		value = "db_" + value
	}
	if len(value) > 32 {
		value = value[:20] + "_" + strings.TrimPrefix(digestJSON(name), "sha256:")[:10]
	}
	return value
}

func resourceDiscoveryCoverage(out *resourceDiscoveryResult, env, profile string, complete bool, origins ...ResourceSourceEvidence) {
	if out.coverage == nil {
		out.coverage = map[string]ResourceSourceEvidence{}
	}
	source := out.Origin
	if len(origins) > 0 {
		source = origins[0]
	}
	source.Profile = profile
	source.State = "not_applicable"
	if !complete {
		source.State = "pending_source"
	}
	out.coverage[env] = source
}

func (s *Store) resourceDiscoveryEnvironmentDocument(service, source, bootstrap string, base map[string]any, req resourceDiscoveryRequest, profile string) (map[string]any, string, string, error) {
	doc := map[string]any{}
	mergeResourceYAML(doc, base)
	if req.BootstrapContent != "" || req.SourceFormat == "properties" {
		return doc, "", "", nil
	}
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`).MatchString(profile) || strings.Contains(profile, "..") {
		return nil, "", "", fmt.Errorf("源码profile不能包含目录或未解析变量")
	}
	root := filepath.Join(s.dataDir, "workspace", service)
	contents, name := "", ""
	for _, extension := range []string{"yml", "yaml"} {
		relative := filepath.Join(filepath.Dir(source), "application-"+profile+"."+extension)
		if filepath.Clean(relative) == filepath.Clean(source) {
			continue
		}
		raw, err := readResourceSourceFile(root, relative)
		if err != nil {
			continue
		}
		if contents != "" {
			return nil, "", "", fmt.Errorf("同一profile存在两份覆盖配置，请明确上传单份源配置核对")
		}
		contents, name = raw, relative
	}
	if contents != "" {
		overlay, err := resourceYAML(contents)
		if err != nil {
			return nil, "", "", err
		}
		mergeResourceYAML(doc, overlay)
	}
	return doc, contents, name, nil
}
func finalizeResourceDiscoveryCoverage(out *resourceDiscoveryResult, environments []string) {
	known := map[string]map[string]ResourceSourceEvidence{}
	for _, c := range out.Items {
		if c.Requirement != nil {
			if known[c.Alias] == nil {
				known[c.Alias] = map[string]ResourceSourceEvidence{}
			}
			for env, s := range c.Requirement.Sources {
				known[c.Alias][env] = s
			}
		}
	}
	for i := range out.Items {
		c := &out.Items[i]
		if c.Requirement == nil {
			continue
		}
		c.Requirement.Sources = map[string]ResourceSourceEvidence{}
		for _, env := range environments {
			source, ok := known[c.Alias][env]
			if !ok {
				source = out.coverage[env]
				if source.State == "" {
					source = out.Origin
					source.State = "pending_source"
					source.Profile = env
				}
			}
			c.Requirement.Sources[env] = source
		}
	}
	// Canonical aliases must not silently merge distinct runtime data sources.
	for i := range out.Items {
		for j := 0; j < i; j++ {
			a, b := &out.Items[i], &out.Items[j]
			if a.Environment == b.Environment && a.Alias == b.Alias && (a.Kind != b.Kind || a.OriginalAlias != b.OriginalAlias) {
				for _, c := range []*resourceCandidate{a, b} {
					c.State = "alias_collision"
					c.Message = "多个源资源规范后使用相同别名，请分别明确命名后重试"
					c.Applyable = false
					c.CanDeclare = false
					c.Requirement = nil
					c.Binding = nil
				}
			}
		}
	}
}

// Fixed Nacos documents and common+jobs are explicit adapters. A Flink query
// selects exactly one existing job; it never expands every job or tenant DB.
func resourceCustomDatasources(doc map[string]any, req resourceDiscoveryRequest) (map[string]any, []string, bool) {
	out := map[string]any{}
	jobs := []string{}
	if req.Adapter == "flink" {
		common := resourceMap(doc["common"])
		all := resourceMap(doc["jobs"])
		for job := range all {
			jobs = append(jobs, job)
		}
		sort.Strings(jobs)
		if len(jobs) > 30 || req.Job == "" || all[req.Job] == nil {
			return out, jobs, false
		}
		job := resourceMap(all[req.Job])
		mysql := map[string]any{}
		mergeResourceYAML(mysql, resourceMap(common["mysql"]))
		mergeResourceYAML(mysql, resourceMap(job["mysql"]))
		add := func(alias string, data map[string]any) {
			copy := map[string]any{}
			mergeResourceYAML(copy, data)
			if copy["url"] == nil {
				copy["url"] = copy["jdbc-url"]
			}
			if copy["url"] != nil && copy["database"] != nil {
				if u, err := url.Parse(strings.TrimPrefix(resourceString(copy["url"]), "jdbc:")); err == nil && u.Scheme == "mysql" {
					u.Path = "/" + resourceString(copy["database"])
					u.RawPath = ""
					copy["url"] = "jdbc:" + u.String()
				}
			}
			if copy["url"] == nil && copy["database"] != nil {
				port := resourceString(copy["port"])
				if port == "" {
					port = "3306"
				}
				copy["url"] = "jdbc:mysql://" + net.JoinHostPort(resourceString(copy["hostname"]), port) + "/" + resourceString(copy["database"])
			}
			if copy["url"] != nil {
				out[alias] = copy
			}
		}
		add("source_mysql", mysql)
		if req.Job == "behavior" {
			data := resourceMap(common["snapshot-mysql"])
			add("snapshot_mysql", data)
			for _, key := range []string{"newhouse-schema", "bus-schema"} {
				if data[key] != nil {
					copy := map[string]any{}
					mergeResourceYAML(copy, data)
					if u, e := url.Parse(strings.TrimPrefix(resourceString(data["jdbc-url"]), "jdbc:")); e == nil {
						u.Path = "/" + resourceString(data[key])
						copy["jdbc-url"] = "jdbc:" + u.String()
						add("snapshot_"+strings.TrimSuffix(key, "-schema"), copy)
					}
				}
			}
		}
		if req.Job == "house-offline-view" {
			for _, key := range []string{"directory-mysql", "customer-mysql"} {
				add(strings.ReplaceAll(key, "-", "_"), resourceMap(common[key]))
			}
			databases := map[string]bool{}
			for _, value := range resourceMap(job["source-overrides"]) {
				database := resourceString(resourceMap(value)["database"])
				if database != "" {
					databases[database] = true
				}
			}
			for database := range databases {
				copy := map[string]any{}
				mergeResourceYAML(copy, mysql)
				copy["database"] = database
				name := "source_mysql"
				if len(databases) > 1 {
					name += "_" + strings.TrimPrefix(digestJSON(database), "sha256:")[:8]
				}
				add(name, copy)
			}
		}
		return out, jobs, true
	}
	if req.Adapter == "custom" {
		primary := resourceMap(resourcePath(doc, "spring", "datasource"))
		if primary["url"] != nil {
			for key, value := range resourceMap(resourcePath(doc, "house-view", "data-source")) {
				if strings.HasSuffix(key, "-schema") {
					copy := map[string]any{}
					mergeResourceYAML(copy, primary)
					if u, e := url.Parse(strings.TrimPrefix(resourceString(primary["url"]), "jdbc:")); e == nil {
						u.Path = "/" + resourceString(value)
						copy["url"] = "jdbc:" + u.String()
						out["view_"+strings.TrimSuffix(key, "-schema")] = copy
					}
				}
			}
		}
	}
	return out, jobs, true
}
