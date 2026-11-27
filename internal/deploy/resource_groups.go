package deploy

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// A group is reusable logical resource declarations with per-environment
// connections. It contains no credentials; applications select the group once.
type ResourceGroup struct {
	Name         string                           `json:"name"`
	Description  string                           `json:"description"`
	Revision     int64                            `json:"revision"`
	UpdatedAt    int64                            `json:"updated_at"`
	Requirements []ApplicationResourceRequirement `json:"requirements"`
	Bindings     []ApplicationEnvironmentResource `json:"bindings"`
}

func (s *Store) ResourceGroup(name string) *ResourceGroup {
	if !validResourceName(name) {
		return nil
	}
	var raw string
	if s.db.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, "resource_group:"+name).Scan(&raw) != nil {
		return nil
	}
	var g ResourceGroup
	if json.Unmarshal([]byte(raw), &g) != nil || g.Name != name {
		return nil
	}
	return &g
}
func (s *Store) ResourceGroups() []ResourceGroup {
	out := []ResourceGroup{}
	rows, err := s.db.Query(`SELECT value FROM deploy_meta WHERE key LIKE 'resource_group:%' ORDER BY key`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var g ResourceGroup
		if rows.Scan(&raw) == nil && json.Unmarshal([]byte(raw), &g) == nil {
			out = append(out, g)
		}
	}
	return out
}
func (s *Store) SaveResourceGroup(g *ResourceGroup) error {
	g.Name, g.Description = strings.TrimSpace(g.Name), strings.TrimSpace(g.Description)
	if !validResourceName(g.Name) {
		return fmt.Errorf("资源组标识需以小写字母开头，仅支持小写字母、数字、短横线和下划线，最多 32 位")
	}
	if len(g.Description) > 1000 {
		return fmt.Errorf("资源组说明不能超过 1000 字节")
	}
	if len(g.Requirements) == 0 {
		return fmt.Errorf("请点击“添加资源”，至少添加一项资源后再保存")
	}
	if len(g.Requirements) > 30 {
		return fmt.Errorf("每个资源组最多添加 30 项资源")
	}
	if len(g.Bindings) > 300 {
		return fmt.Errorf("每个资源组最多添加 300 个环境绑定")
	}
	resources := ApplicationResources{Requirements: g.Requirements, Bindings: g.Bindings}
	if _, err := s.normalizeApplicationResources("", &resources); err != nil {
		return err
	}
	g.Requirements, g.Bindings = resources.Requirements, resources.Bindings
	for i := range g.Bindings {
		g.Bindings[i].UpdatedAt = 0
	}
	sort.Slice(g.Requirements, func(i, j int) bool { return g.Requirements[i].Alias < g.Requirements[j].Alias })
	sort.Slice(g.Bindings, func(i, j int) bool {
		a, b := g.Bindings[i], g.Bindings[j]
		return a.Environment+"\x00"+a.Alias < b.Environment+"\x00"+b.Alias
	})
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var stored string
	e := tx.QueryRow(s.resourceGroupLockQuery(), "resource_group:"+g.Name).Scan(&stored)
	var old *ResourceGroup
	if e == nil {
		old = &ResourceGroup{}
		if json.Unmarshal([]byte(stored), old) != nil {
			return fmt.Errorf("资源组记录损坏")
		}
	} else if e != sql.ErrNoRows {
		return e
	}
	if old != nil {
		if old.Revision != g.Revision {
			return fmt.Errorf("资源组已被其他操作修改，请重新读取")
		}
		var refs int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM deploy_meta WHERE key LIKE 'application_resource_group:%' AND value=?`, g.Name).Scan(&refs); err != nil {
			return err
		}
		if refs > 0 {
			types := map[string]string{}
			for _, r := range g.Requirements {
				types[r.Alias] = r.Kind
			}
			for _, r := range old.Requirements {
				if types[r.Alias] != r.Kind {
					return fmt.Errorf("资源组已被应用使用，不能删除或更改逻辑资源类型；请新建资源组后迁移")
				}
			}
		}
	} else if g.Revision != 0 {
		return fmt.Errorf("资源组不存在，请重新读取")
	}
	g.Revision++
	g.UpdatedAt = time.Now().Unix()
	raw, err := json.Marshal(g)
	if err != nil {
		return err
	}
	if len(raw) > 60<<10 {
		return fmt.Errorf("资源组配置过大，请拆分资源组（最多 60 KiB）")
	}
	if old == nil {
		_, err = tx.Exec(`INSERT INTO deploy_meta(key,value) VALUES(?,?)`, "resource_group:"+g.Name, string(raw))
		if err != nil {
			return err
		}
		return tx.Commit()
	}
	oldRaw := []byte(stored)
	r, err := tx.Exec(`UPDATE deploy_meta SET value=? WHERE key=? AND value=?`, string(raw), "resource_group:"+g.Name, string(oldRaw))
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return fmt.Errorf("资源组已变化，请重新读取")
	}
	return tx.Commit()
}
func (s *Store) DeleteResourceGroup(name string) error {
	if s.ResourceGroup(name) == nil {
		return fmt.Errorf("资源组不存在")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists string
	if err = tx.QueryRow(s.resourceGroupLockQuery(), "resource_group:"+name).Scan(&exists); err != nil {
		return fmt.Errorf("资源组已删除")
	}
	var refs int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM deploy_meta WHERE key LIKE 'application_resource_group:%' AND value=?`, name).Scan(&refs); err != nil {
		return err
	}
	if refs > 0 {
		return fmt.Errorf("资源组仍被 %d 个应用使用，请先调整应用绑定", refs)
	}
	historyQuery := `SELECT COUNT(*) FROM delivery_runs r,json_each(r.plan,'$.resource_targets') t WHERE json_extract(t.value,'$.group_name')=?`
	if s.Driver() == "mysql" {
		historyQuery = `SELECT COUNT(*) FROM delivery_runs WHERE JSON_CONTAINS(plan,JSON_OBJECT('group_name',?),'$.resource_targets')`
	}
	if err := tx.QueryRow(historyQuery, name).Scan(&refs); err != nil {
		return err
	}
	if refs > 0 {
		return fmt.Errorf("资源组已有交付记录，请保留用于追溯")
	}
	_, err = tx.Exec(`DELETE FROM deploy_meta WHERE key=?`, "resource_group:"+name)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) resourceGroupLockQuery() string {
	q := `SELECT value FROM deploy_meta WHERE key=?`
	if s.Driver() == "mysql" {
		q += " FOR UPDATE"
	}
	return q
}
func (s *Store) applicationResourceGroup(service string) string {
	var name string
	s.db.QueryRow(`SELECT value FROM deploy_meta WHERE key=?`, "application_resource_group:"+service).Scan(&name)
	return name
}
func (s *Store) groupUses(kind, value string) bool {
	for _, g := range s.ResourceGroups() {
		for _, b := range g.Bindings {
			if (kind == "connection" && b.Connection == value) || (kind == "environment" && b.Environment == value) {
				return true
			}
		}
	}
	return false
}
func registerResourceGroupRoutes(mux *http.ServeMux, s *Store) {
	admin := func(w http.ResponseWriter, r *http.Request) bool {
		if deliveryActor(r) != "local" && r.Header.Get("X-Role") != "admin" {
			deliveryWriteError(w, 403, "资源组配置需要管理员权限")
			return false
		}
		return true
	}
	mux.HandleFunc("GET /api/resource-groups", func(w http.ResponseWriter, r *http.Request) { deliveryJSON(w, s.ResourceGroups()) })
	mux.HandleFunc("POST /api/resource-groups", func(w http.ResponseWriter, r *http.Request) {
		if !admin(w, r) {
			return
		}
		var g ResourceGroup
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&g) != nil {
			deliveryWriteError(w, 400, "资源组参数无效")
			return
		}
		if err := s.SaveResourceGroup(&g); err != nil {
			deliveryWriteError(w, 409, err)
			return
		}
		s.Audit(deliveryActor(r), "save", "resource-group", g.Name, map[string]any{"revision": g.Revision})
		deliveryJSON(w, g)
	})
	mux.HandleFunc("DELETE /api/resource-groups/{name}", func(w http.ResponseWriter, r *http.Request) {
		if !admin(w, r) {
			return
		}
		name := r.PathValue("name")
		if err := s.DeleteResourceGroup(name); err != nil {
			deliveryWriteError(w, 409, err)
			return
		}
		s.Audit(deliveryActor(r), "delete", "resource-group", name, nil)
		deliveryJSON(w, map[string]bool{"ok": true})
	})
}

// Same validator for direct application bindings and reusable group members.
func (s *Store) normalizeApplicationResources(service string, resources *ApplicationResources) (map[string]ApplicationResourceRequirement, error) {
	requirements := map[string]ApplicationResourceRequirement{}
	for i := range resources.Requirements {
		r := &resources.Requirements[i]
		r.Service = service
		r.Alias = strings.TrimSpace(r.Alias)
		r.Kind = strings.TrimSpace(r.Kind)
		r.Description = strings.TrimSpace(r.Description)
		if err := s.validateResourceSources(r.Sources); err != nil {
			return nil, err
		}
		if !validResourceName(r.Alias) || !validApplicationResourceKind(r.Kind) {
			return nil, fmt.Errorf("资源别名需有效标识，类型仅支持 mysql、nacos、redis、xxl_job")
		}
		if _, ok := requirements[r.Alias]; ok {
			return nil, fmt.Errorf("资源别名不能重复")
		}
		requirements[r.Alias] = *r
	}
	seen := map[string]bool{}
	for i := range resources.Bindings {
		b := &resources.Bindings[i]
		b.Service = service
		b.Environment = strings.TrimSpace(b.Environment)
		b.Alias = strings.TrimSpace(b.Alias)
		b.Connection = strings.TrimSpace(b.Connection)
		req, ok := requirements[b.Alias]
		if !ok {
			return nil, fmt.Errorf("资源绑定引用未声明别名 %s", b.Alias)
		}
		if s.GetEnvironment(b.Environment) == nil {
			return nil, fmt.Errorf("资源绑定的环境不存在")
		}
		c := s.GetConnection(b.Connection)
		if c == nil || c.Kind != req.Kind {
			return nil, fmt.Errorf("资源 %s 必须绑定 %s 类型连接", b.Alias, req.Kind)
		}
		if req.Kind == "nacos" {
			if err := normalizeNacosBindingConfig(b.Config); err != nil {
				return nil, err
			}
		} else if req.Kind == "mysql" {
			target, err := mysqlDatabaseTarget(c, b.Config)
			if err != nil {
				return nil, fmt.Errorf("资源 %s：%w", b.Alias, err)
			}
			b.Config = map[string]string{"database": target.Config["database"]}
		} else if req.Kind == xxlJobKind {
			if err := validateXXLBinding(c, *b); err != nil {
				return nil, err
			}
		} else if len(b.Config) > 0 {
			return nil, fmt.Errorf("Redis 目标参数在连接中指定，不支持绑定覆盖")
		}
		key := b.Environment + "\x00" + b.Alias
		if seen[key] {
			return nil, fmt.Errorf("同一环境的资源别名不能重复绑定")
		}
		seen[key] = true
	}
	return requirements, nil
}
