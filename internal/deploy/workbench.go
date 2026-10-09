package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"logplatform/internal/appcatalog"
)

// Resource-only applications are identities, not fake build/deployment services.
func (s *Store) hasResourceApplication(name string) bool {
	if s.GetService(name) != nil {
		return true
	}
	var found string
	return s.db.QueryRow(`SELECT name FROM resource_applications WHERE name=?`, name).Scan(&found) == nil
}

func (s *Store) resourceApplications() []map[string]string {
	rows, err := s.db.Query(`SELECT name FROM resource_applications UNION SELECT name FROM services ORDER BY name`)
	out := []map[string]string{}
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if rows.Scan(&name) == nil {
			out = append(out, map[string]string{"name": name})
		}
	}
	return out
}

func workbenchChangeKind(kind string) string {
	if kind == "dbms" {
		return "mysql"
	}
	return "redis"
}

func validateWorkbenchTemplate(t DeliveryTemplate) error {
	if (t.Workbench != "dbms" && t.Workbench != "redis") || t.Mode != "fixed" || len(t.Services) != 0 || len(t.Nodes) != 1 {
		return fmt.Errorf("工作台仅支持一个资源操作节点，不包含构建或部署服务")
	}
	want := "mysql"
	if t.Workbench == "redis" {
		want = "redis"
	}
	if t.Nodes[0].Kind != want || t.Nodes[0].Service != "" {
		return fmt.Errorf("工作台节点类型不匹配")
	}
	return nil
}

// A capability-scoped facade reuses the delivery engine, rather than exposing
// all deployment endpoints through the independent plugin aliases.
func registerWorkbenchRoutes(mux *http.ServeMux, st *Store, busURL string) {
	observer := appcatalog.NewObserver(busURL)
	for _, kind := range []string{"dbms", "redis"} {
		kind := kind
		prefix := "/api/workbench/" + kind + "/"
		mux.HandleFunc(prefix, func(w http.ResponseWriter, r *http.Request) {
			rest := strings.TrimPrefix(r.URL.Path, prefix)
			parts := strings.Split(rest, "/")
			isGet := r.Method == http.MethodGet
			fail := func(message string) { writeIntegrationJSON(w, nil, fmt.Errorf("%s", message)) }
			var raw []byte
			if !isGet {
				var err error
				raw, err = io.ReadAll(http.MaxBytesReader(w, r.Body, 256<<10))
				if err != nil || !json.Valid(raw) {
					fail("操作参数格式无效或过大")
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(raw))
			}
			if rest == "services" && isGet {
				writeIntegrationJSON(w, st.resourceApplications(), nil)
				return
			}
			if rest == "applications" && r.Method == http.MethodPost {
				if r.Header.Get("X-User") != "" && r.Header.Get("X-Role") != "admin" {
					http.Error(w, "administrator required", 403)
					return
				}
				var v struct {
					Name string `json:"name"`
				}
				if json.Unmarshal(raw, &v) != nil || !validResourceName(v.Name) {
					fail("应用名称格式无效")
					return
				}
				if _, err := observer.ResolveWithSession(v.Name, "manual", "", r.Header.Get("Cookie")); err != nil {
					fail("应用中心不可用，未接入资源应用")
					return
				}
				{
					if _, err := st.db.Exec(`INSERT OR IGNORE INTO resource_applications(name) VALUES(?)`, v.Name); err != nil {
						fail("保存资源应用失败，请刷新重试")
						return
					}
				}
				st.Audit(r.Header.Get("X-User"), "register", "resource-application", v.Name, nil)
				writeIntegrationJSON(w, map[string]string{"name": v.Name}, nil)
				return
			}
			if rest == "connections" && isGet {
				out := []ResourceConnection{}
				for _, c := range st.Connections() {
					if c.Kind == workbenchChangeKind(kind) {
						out = append(out, c)
					}
				}
				writeIntegrationJSON(w, out, nil)
				return
			}
			if st.handleWorkbenchQueries(w, r, kind, rest, raw) {
				return
			}
			allowed := rest == "environments" && isGet
			if len(parts) == 3 && parts[0] == "services" && parts[2] == "resources" && (isGet || r.Method == http.MethodPut) {
				if !isGet && r.Header.Get("X-User") != "" && r.Header.Get("X-Role") != "admin" {
					http.Error(w, "administrator required", 403)
					return
				}
				allowed = true
			}
			if parts[0] == "changes" {
				if len(parts) == 1 && isGet {
					out := []Change{}
					for _, c := range st.Changes(r.URL.Query().Get("status"), r.URL.Query().Get("service"), r.URL.Query().Get("q"), 500) {
						if c.Kind == workbenchChangeKind(kind) {
							out = append(out, c)
						}
					}
					writeIntegrationJSON(w, out, nil)
					return
				}
				if len(parts) >= 2 {
					id, _ := strconv.ParseInt(parts[1], 10, 64)
					c := st.GetChange(id)
					if c == nil || c.Kind != workbenchChangeKind(kind) {
						fail("变更不存在或不属于该工作台")
						return
					}
				}
				if (len(parts) == 1 && r.Method == http.MethodPost) || (len(parts) == 2 && r.Method == http.MethodPut) {
					var c Change
					if json.Unmarshal(raw, &c) != nil || c.Kind != workbenchChangeKind(kind) || (kind == "dbms" && c.SQL == nil) || (kind == "redis" && c.Redis == nil) || !st.hasResourceApplication(c.Service) {
						fail("请填写本工作台的结构化操作及已接入应用")
						return
					}
					allowed = true
				}
				if len(parts) == 3 && r.Method == http.MethodPost {
					preview := "sql-preview"
					if kind == "redis" {
						preview = "redis-preview"
					}
					allowed = parts[2] == "ready" || parts[2] == "draft" || parts[2] == preview
				}
			}
			if len(parts) >= 2 && parts[0] == "data-sync" && parts[1] == "tasks" && kind == "dbms" {
				if len(parts) == 4 && r.Method == http.MethodPost && (parts[3] == "compare" || parts[3] == "freeze") {
					id, _ := strconv.ParseInt(parts[2], 10, 64)
					if err := st.authorizeWorkbenchComparison(r, raw, id); err != nil {
						writeIntegrationJSON(w, nil, err)
						return
					}
				}
				allowed = (len(parts) == 2 && (isGet || r.Method == http.MethodPost)) || (len(parts) == 3 && r.Method == http.MethodPut) || (len(parts) == 4 && r.Method == http.MethodPost && (parts[3] == "compare" || parts[3] == "freeze"))
			}
			if rest == "delivery/templates" && r.Method == http.MethodPost {
				var t DeliveryTemplate
				if json.Unmarshal(raw, &t) != nil || t.Workbench != kind || validateWorkbenchTemplate(t) != nil {
					fail("工作台模板类型不匹配")
					return
				}
				actor := strings.TrimSpace(r.Header.Get("X-User"))
				if actor == "" {
					actor = "local"
				}
				for _, existing := range st.DeliveryTemplates() {
					if existing.Workbench == kind && existing.Owner == actor {
						writeIntegrationJSON(w, existing, nil)
						return
					}
				}
				t.ID, t.Owner = 0, actor
				t.Nodes = []DeliveryNode{{ID: "operation", Kind: t.Nodes[0].Kind, Name: t.Name}}
				if err := st.SaveDeliveryTemplate(&t); err != nil {
					writeIntegrationJSON(w, nil, err)
					return
				}
				writeIntegrationJSON(w, t, nil)
				return
			}
			if (rest == "delivery/preview" || rest == "delivery/runs") && r.Method == http.MethodPost {
				var req DeliveryRequest
				if json.Unmarshal(raw, &req) != nil {
					fail("执行单参数无效")
					return
				}
				t := st.DeliveryTemplate(req.TemplateID)
				if t == nil || t.Workbench != kind || req.VersionID != 0 || len(req.Artifacts) != 0 {
					fail("请选择本工作台的资源模板，不允许部署制品")
					return
				}
				nodes := t.Nodes
				if req.Nodes != nil {
					nodes = req.Nodes
				}
				copy := *t
				copy.Nodes = nodes
				if validateWorkbenchTemplate(copy) != nil {
					fail("节点范围无效")
					return
				}
				for _, id := range nodes[0].ChangeIDs {
					c := st.GetChange(id)
					if c == nil || c.Kind != workbenchChangeKind(kind) {
						fail("不能执行其他工作台的变更")
						return
					}
				}
				allowed = true
			}
			if rest == "delivery/runs" && isGet {
				out := []DeliveryRun{}
				for _, v := range st.DeliveryRuns() {
					if v.Plan.Template.Workbench == kind {
						out = append(out, v)
					}
				}
				writeIntegrationJSON(w, out, nil)
				return
			}
			if len(parts) >= 3 && parts[0] == "delivery" && parts[1] == "runs" {
				id, _ := strconv.ParseInt(parts[2], 10, 64)
				v := st.DeliveryRun(id)
				if v == nil || v.Plan.Template.Workbench != kind {
					fail("执行单不存在或不属于该工作台")
					return
				}
				allowed = (len(parts) == 3 && isGet) || (len(parts) == 4 && r.Method == http.MethodPost && (parts[3] == "approve" || parts[3] == "reject" || parts[3] == "advance" || parts[3] == "cancel"))
			}
			if !allowed {
				http.NotFound(w, r)
				return
			}
			forward := r.Clone(r.Context())
			forward.URL.Path = "/api/" + rest
			forward.URL.RawPath = ""
			mux.ServeHTTP(w, forward)
		})
	}
}
