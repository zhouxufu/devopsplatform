package deploy

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// References only. Configuration content and credentials never live here.
type ApplicationNacosConfig struct {
	Environment string `json:"environment,omitempty"`
	Connection  string `json:"connection,omitempty"`
	Alias       string `json:"alias"`
	DataID      string `json:"data_id"`
	Group       string `json:"group"`
}

func (s *Store) environmentNacos(environment string) string {
	var name string
	_ = s.db.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, "environment_nacos:"+environment).Scan(&name)
	return name
}

func (s *Store) composeResources(service string, in ApplicationResources) (ApplicationResources, error) {
	out := in
	out.ResourceGroup, out.GroupRevision, out.Error = "", 0, ""
	out.Requirements = append([]ApplicationResourceRequirement{}, in.OwnRequirements...)
	out.OwnBindings = append([]ApplicationEnvironmentResource{}, in.OwnBindings...)
	out.Bindings = append([]ApplicationEnvironmentResource{}, out.OwnBindings...)
	out.GroupRevisions = map[string]int64{}
	seenGroups := map[string]bool{}
	for _, name := range in.ResourceGroups {
		if seenGroups[name] {
			return out, fmt.Errorf("资源组不能重复选择")
		}
		seenGroups[name] = true
		g := s.ResourceGroup(name)
		if g == nil {
			return out, fmt.Errorf("资源组 %s 不存在，请重新选择", name)
		}
		out.GroupRevisions[name] = g.Revision
		for _, r := range g.Requirements {
			r.GroupName, r.GroupRevision = name, g.Revision
			out.Requirements = append(out.Requirements, r)
		}
		out.Bindings = append(out.Bindings, g.Bindings...)
	}
	seen, refAliases := map[string]bool{}, map[string]bool{}
	for _, ref := range in.NacosConfigs {
		if !validResourceName(ref.Alias) || strings.TrimSpace(ref.DataID) == "" || strings.TrimSpace(ref.Group) == "" || len(ref.DataID) > 255 || len(ref.Group) > 255 || strings.ContainsAny(ref.DataID+ref.Group, "\r\n\x00") {
			return out, fmt.Errorf("Nacos 配置引用无效")
		}
		if ref.Environment == "" || s.GetEnvironment(ref.Environment) == nil {
			return out, fmt.Errorf("请选择 Nacos 配置的具体环境，不会自动绑定其他环境")
		}
		connection := ref.Connection
		if connection == "" {
			connection = s.environmentNacos(ref.Environment)
		}
		c := s.GetConnection(connection)
		if c == nil || c.Kind != "nacos" {
			return out, fmt.Errorf("请为 %s 环境选择 Nacos 连接", ref.Environment)
		}
		key := ref.Environment + "\x00" + ref.Alias
		target := ref.Environment + "\x00" + connection + "\x00" + ref.Group + "\x00" + ref.DataID
		if seen[key] || seen[target] {
			return out, fmt.Errorf("同一环境的 Nacos 逻辑资源或配置不能重复选择")
		}
		seen[key], seen[target] = true, true
		if !refAliases[ref.Alias] {
			out.Requirements = append(out.Requirements, ApplicationResourceRequirement{Service: service, Alias: ref.Alias, Kind: "nacos", Description: "按环境选择的 Nacos 配置"})
			refAliases[ref.Alias] = true
		}
		out.Bindings = append(out.Bindings, ApplicationEnvironmentResource{Service: service, Environment: ref.Environment, Alias: ref.Alias, Connection: connection, Config: map[string]string{"data_id": ref.DataID, "group": ref.Group}})
	}
	aliases := map[string]bool{}
	for _, r := range out.Requirements {
		if aliases[r.Alias] {
			return out, fmt.Errorf("资源别名 %s 冲突，请调整资源组或自有资源别名", r.Alias)
		}
		aliases[r.Alias] = true
	}
	_, err := s.normalizeApplicationResources(service, &out)
	return out, err
}

func (s *Store) composedApplicationResources(service string) (ApplicationResources, bool) {
	var raw string
	err := s.db.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, "application_resources_v2:"+service).Scan(&raw)
	if err != nil {
		return ApplicationResources{}, false
	}
	var in ApplicationResources
	if json.Unmarshal([]byte(raw), &in) != nil {
		return ApplicationResources{CompositionVersion: 2, Error: "应用资源配置损坏"}, true
	}
	out, err := s.composeResources(service, in)
	if err != nil {
		out.Error = err.Error()
		out.Requirements = nil
		out.Bindings = nil
	}
	return out, true
}

func (s *Store) saveComposedResources(service string, in ApplicationResources) error {
	in.NacosScopeVersion = 1
	if !s.hasResourceApplication(service) {
		return fmt.Errorf("应用不存在")
	}
	if len(in.ResourceGroups) > 20 || len(in.OwnRequirements) > 100 || len(in.OwnBindings) > 300 || len(in.NacosConfigs) > 100 {
		return fmt.Errorf("资源选择数量超限")
	}
	out, err := s.composeResources(service, in)
	if err != nil {
		return err
	}
	for name, revision := range out.GroupRevisions {
		if in.GroupRevisions[name] != revision {
			return fmt.Errorf("资源组 %s 已变化，请刷新资源组后重新确认", name)
		}
	}
	// Save only own resources and references, never a flattened copy of shared resources.
	in.OwnBindings = out.OwnBindings
	in.ResourceGroup = ""
	in.GroupRevision = 0
	in.Requirements = nil
	in.Bindings = nil
	in.Error = ""
	in.Checks = nil
	for i := range in.OwnRequirements {
		in.OwnRequirements[i].GroupName = ""
		in.OwnRequirements[i].GroupRevision = 0
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	if len(raw) > 60<<10 {
		return fmt.Errorf("应用资源配置过大")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = s.advanceApplicationResourceRevision(tx, service, in.ExpectedRevision); err != nil {
		return err
	}
	names := append([]string{}, in.ResourceGroups...)
	sort.Strings(names)
	for _, name := range names {
		var value string
		var g ResourceGroup
		if tx.QueryRow(s.resourceGroupLockQuery(), "resource_group:"+name).Scan(&value) != nil || json.Unmarshal([]byte(value), &g) != nil || g.Revision != in.GroupRevisions[name] {
			return fmt.Errorf("资源组已变化，请刷新后重试")
		}
	}
	// Exact prefix comparisons avoid LIKE wildcard collisions in application names.
	rows, err := tx.Query(`SELECT key FROM deploy_meta WHERE key LIKE 'application_resource_group:%'`)
	if err != nil {
		return err
	}
	keys := []string{}
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			rows.Close()
			return err
		}
		if key == "application_resource_group:"+service || strings.HasPrefix(key, "application_resource_group:"+service+"/") {
			keys = append(keys, key)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, key := range keys {
		if _, err = tx.Exec(`DELETE FROM deploy_meta WHERE key=?`, key); err != nil {
			return err
		}
	}
	for _, name := range names {
		if _, err = tx.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?)`, "application_resource_group:"+service+"/"+name, name); err != nil {
			return err
		}
	}
	// These rows are superseded by the own-resource document in this same transaction.
	if _, err = tx.Exec(`DELETE FROM application_environment_resources WHERE service=?`, service); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM application_resource_requirements WHERE service=?`, service); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, "application_resources_v2:"+service, string(raw)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) compositionUses(kind, value string) bool {
	rows, err := s.db.Query(`SELECT key,value FROM deploy_meta WHERE key LIKE 'application_resources_v2:%' OR key LIKE 'environment_nacos:%'`)
	if err != nil {
		return true
	}
	defer rows.Close()
	for rows.Next() {
		var key, raw string
		if rows.Scan(&key, &raw) != nil {
			return true
		}
		if strings.HasPrefix(key, "environment_nacos:") {
			if kind == "connection" && raw == value || kind == "environment" && key == "environment_nacos:"+value {
				return true
			}
			continue
		}
		var v ApplicationResources
		if json.Unmarshal([]byte(raw), &v) != nil {
			return true
		}
		for _, ref := range v.NacosConfigs {
			if kind == "connection" && ref.Connection == value || kind == "environment" && ref.Environment == value {
				return true
			}
		}
		for _, b := range v.OwnBindings {
			if kind == "connection" && b.Connection == value || kind == "environment" && b.Environment == value {
				return true
			}
		}
	}
	return rows.Err() != nil
}
