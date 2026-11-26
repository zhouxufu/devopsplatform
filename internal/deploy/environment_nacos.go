package deploy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func registerEnvironmentNacosRoutes(mux *http.ServeMux, s *Store) {
	mux.HandleFunc("GET /api/environments/{environment}/nacos", func(w http.ResponseWriter, r *http.Request) {
		env := r.PathValue("environment")
		if s.GetEnvironment(env) == nil {
			deliveryWriteError(w, 404, "环境不存在")
			return
		}
		deliveryJSON(w, map[string]string{"connection": s.environmentNacos(env)})
	})
	mux.HandleFunc("PUT /api/environments/{environment}/nacos", func(w http.ResponseWriter, r *http.Request) {
		if deliveryActor(r) != "local" && r.Header.Get("X-Role") != "admin" {
			deliveryWriteError(w, 403, "配置环境 Nacos 需要管理员权限")
			return
		}
		env := r.PathValue("environment")
		if s.GetEnvironment(env) == nil {
			deliveryWriteError(w, 404, "环境不存在")
			return
		}
		var in struct {
			Connection string `json:"connection"`
			Previous   string `json:"previous"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
			deliveryWriteError(w, 400, "参数无效")
			return
		}
		if in.Connection != "" {
			c := s.GetConnection(in.Connection)
			if c == nil || c.Kind != "nacos" {
				deliveryWriteError(w, 400, "请选择 Nacos 连接")
				return
			}
		}
		tx, err := s.db.Begin()
		if err != nil {
			deliveryWriteError(w, 500, "保存失败")
			return
		}
		defer tx.Rollback()
		_, err = tx.Exec(`INSERT OR IGNORE INTO deploy_meta(key,value) VALUES(?,'')`, "environment_nacos:"+env)
		if err == nil {
			var current string
			err = tx.QueryRow(s.resourceGroupLockQuery(), "environment_nacos:"+env).Scan(&current)
			if err == nil && current != in.Previous {
				err = fmt.Errorf("配置已变化，请重新读取")
			}
			if err == nil {
				_, err = tx.Exec(`UPDATE deploy_meta SET value=? WHERE key=?`, in.Connection, "environment_nacos:"+env)
			}
		}
		if err == nil && in.Connection == "" {
			_, err = tx.Exec(`DELETE FROM deploy_meta WHERE key=?`, "environment_nacos:"+env)
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			deliveryWriteError(w, 409, "配置已变化或保存失败，请重新读取后重试")
			return
		}
		s.Audit(deliveryActor(r), "update", "environment-nacos", env, map[string]string{"connection": in.Connection})
		deliveryJSON(w, map[string]string{"connection": in.Connection})
	})
	mux.HandleFunc("GET /api/environments/{environment}/nacos/configs", func(w http.ResponseWriter, r *http.Request) {
		env := r.PathValue("environment")
		if s.GetEnvironment(env) == nil {
			deliveryWriteError(w, 404, "环境不存在")
			return
		}
		connection := strings.TrimSpace(r.URL.Query().Get("connection"))
		if connection == "" {
			connection = s.environmentNacos(env)
		}
		c := s.GetConnection(connection)
		if c == nil || c.Kind != "nacos" {
			deliveryWriteError(w, 409, "请先在资源中心 → 环境中配置该环境的 Nacos 接入")
			return
		}
		q, group := strings.TrimSpace(r.URL.Query().Get("q")), strings.TrimSpace(r.URL.Query().Get("group"))
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		if page > 10000 || len(q) > 200 || len(group) > 200 {
			deliveryWriteError(w, 400, "搜索参数无效")
			return
		}
		size, err := requestListPageSize(r)
		if err != nil {
			deliveryWriteError(w, 400, err)
			return
		}
		client, err := newNacosClient(r.Context(), c)
		if err != nil {
			deliveryWriteError(w, 502, err)
			return
		}
		if q != "" && !strings.Contains(q, "*") {
			q = "*" + q + "*"
		}
		args := url.Values{"search": {"blur"}, "tenant": {c.Config["namespace"]}, "dataId": {q}, "group": {group}, "pageNo": {strconv.Itoa(page)}, "pageSize": {strconv.Itoa(size)}}
		body, _, err := client.request(r.Context(), http.MethodGet, "/v1/cs/configs", args, "")
		if err != nil {
			deliveryWriteError(w, 502, err)
			return
		}
		// Intentionally whitelist metadata, discarding upstream content/md5/credentials.
		var response struct {
			Total int `json:"totalCount"`
			Items []struct {
				DataID string `json:"dataId"`
				Group  string `json:"group"`
				Type   string `json:"type"`
			} `json:"pageItems"`
		}
		if json.Unmarshal(body, &response) != nil || response.Items == nil {
			deliveryWriteError(w, 502, "Nacos 配置列表响应无效，请核对兼容版本")
			return
		}
		items := []ApplicationNacosConfig{}
		for _, item := range response.Items {
			if len(items) >= 20 {
				break
			}
			hash := strings.TrimPrefix(digestJSON([]string{item.Group, item.DataID}), "sha256:")
			items = append(items, ApplicationNacosConfig{Alias: "nacos-" + hash[:16], Environment: env, Connection: c.Name, DataID: item.DataID, Group: item.Group})
		}
		deliveryJSON(w, map[string]any{"items": items, "total": response.Total, "page": page, "namespace": c.Config["namespace"], "connection": c.Name})
	})
}
