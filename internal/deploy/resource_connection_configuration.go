package deploy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

type resourceConnectionConfiguration struct {
	Name                 string            `json:"name"`
	Kind                 string            `json:"kind"`
	Endpoint             string            `json:"endpoint"`
	Config               map[string]string `json:"config"`
	Revision             string            `json:"revision"`
	CredentialConnection string            `json:"credential_connection"`
	CredentialRevision   string            `json:"credential_revision"`
}

func resourceConnectionConfigKeys(kind string) map[string]bool {
	var keys []string
	switch kind {
	case "mysql":
		keys = []string{"tls"}
	case "nacos":
		keys = []string{"namespace", "nacos_auth", "nacos_version", "test_data_id", "test_group"}
	case "redis":
		keys = []string{"db", "tls", "redis_auth", "key_prefix", "key_prefixes"}
	}
	out := map[string]bool{}
	for _, key := range keys {
		out[key] = true
	}
	return out
}
func (s *Store) connectionMetadata(name string) map[string]any {
	c := s.GetConnection(name)
	if c == nil {
		return map[string]any{"name": name, "revision": "missing", "exists": false}
	}
	config := map[string]string{}
	for key := range resourceConnectionConfigKeys(c.Kind) {
		if value, ok := c.Config[key]; ok {
			config[key] = value
		}
	}
	return map[string]any{"name": c.Name, "kind": c.Kind, "endpoint": c.Endpoint, "config": config, "revision": s.connectionCheckFingerprint(c), "exists": true, "secret_configured": c.SecretConfigured}
}
func (s *Store) resourceConnectionImpact(name string) []string {
	rows, err := s.db.Query(`SELECT name FROM resource_applications ORDER BY name`)
	if err != nil {
		return nil
	}
	names := []string{}
	for rows.Next() {
		var name string
		if rows.Scan(&name) == nil {
			names = append(names, name)
		}
	}
	rows.Close()
	out := []string{}
	for _, service := range names {
		for _, binding := range s.ApplicationResources(service).Bindings {
			if binding.Connection == name {
				out = append(out, service)
				break
			}
		}
	}
	return out
}
func (s *Store) prepareResourceConnectionConfiguration(req resourceConnectionConfiguration, verify bool) (ResourceConnection, error) {
	if !validConnectionName(req.Name) || !validApplicationResourceKind(req.Kind) {
		return ResourceConnection{}, fmt.Errorf("连接名称无效或类型暂不支持")
	}
	allowed := resourceConnectionConfigKeys(req.Kind)
	for key := range req.Config {
		if !allowed[key] {
			return ResourceConnection{}, fmt.Errorf("连接参数 %s 不允许通过此入口配置", key)
		}
	}
	current := s.GetConnection(req.Name)
	revision := s.connectionCheckFingerprint(current)
	if req.Revision != revision {
		return ResourceConnection{}, fmt.Errorf("目标连接已变化，请重新预览")
	}
	source := s.GetConnection(req.CredentialConnection)
	if source == nil || source.Kind != req.Kind {
		return ResourceConnection{}, fmt.Errorf("请选择同类型的已有凭据连接；新凭据请在管理员连接编辑器中维护")
	}
	if verify && req.CredentialRevision != s.connectionCheckFingerprint(source) {
		return ResourceConnection{}, fmt.Errorf("凭据来源已变化，请重新预览")
	}
	config := map[string]string{}
	for key, value := range source.Config {
		if allowed[key] {
			config[key] = value
		}
	}
	for key, value := range req.Config {
		config[key] = value
	}
	target := ResourceConnection{Name: req.Name, Kind: req.Kind, Endpoint: strings.TrimSpace(req.Endpoint), Config: config, Username: source.Username, Password: source.Password, PrivateKey: source.PrivateKey, Passphrase: source.Passphrase}
	if current != nil && current.Kind != target.Kind {
		return target, fmt.Errorf("已有连接不能改变类型")
	}
	var err error
	switch target.Kind {
	case "nacos":
		err = validateNacosConnection(&target)
	case "redis":
		err = validateRedisConnection(&target)
	case "mysql":
		err = validateIntegrationConnection(&target)
	}
	return target, err
}
func registerResourceConnectionConfigurationRoutes(mux *http.ServeMux, s *Store) {
	mux.HandleFunc("GET /api/connections/{name}/metadata", func(w http.ResponseWriter, r *http.Request) {
		if deliveryActor(r) != "local" && r.Header.Get("X-Role") != "admin" {
			deliveryWriteError(w, 403, "连接元数据需要管理员权限")
			return
		}
		if !validConnectionName(r.PathValue("name")) {
			deliveryWriteError(w, 400, "连接名称无效")
			return
		}
		deliveryJSON(w, s.connectionMetadata(r.PathValue("name")))
	})
	for _, preview := range []bool{false, true} {
		preview := preview
		path := "POST /api/connections/configure"
		if preview {
			path += "/preview"
		}
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if deliveryActor(r) != "local" && r.Header.Get("X-Role") != "admin" {
				deliveryWriteError(w, 403, "连接接入需要管理员权限")
				return
			}
			var req resourceConnectionConfiguration
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16000))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&req) != nil {
				deliveryWriteError(w, 400, "连接参数无效；不接受密码或任意认证字段")
				return
			}
			s.resourceConnectionMu.Lock()
			defer s.resourceConnectionMu.Unlock()
			target, err := s.prepareResourceConnectionConfiguration(req, !preview)
			if err != nil {
				deliveryWriteError(w, 409, err)
				return
			}
			if preview {
				req.Config = target.Config
				req.CredentialRevision = s.connectionCheckFingerprint(s.GetConnection(req.CredentialConnection))
				impact := s.resourceConnectionImpact(req.Name)
				sort.Strings(impact)
				deliveryJSON(w, map[string]any{"request": req, "before": s.connectionMetadata(req.Name), "credential_source": req.CredentialConnection, "affected_applications": impact, "credential_mode": "复制已保存凭据的当前快照，不返回明文；源凭据轮换不会自动修改此连接", "no_execution": true})
				return
			}
			if err = s.upsertConnection(target); err != nil {
				deliveryWriteError(w, 400, err)
				return
			}
			s.Audit(deliveryActor(r), "upsert", "connection", target.Name, map[string]any{"kind": target.Kind, "credential_source": req.CredentialConnection})
			deliveryJSON(w, s.connectionMetadata(target.Name))
		})
	}
}
