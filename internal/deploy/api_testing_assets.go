package deploy

import (
	"logplatform/pkg/apitest"
	"net/http"
	"strconv"
	"strings"
)

// Asset preview never creates a case or sends an HTTP request to a business target.
func registerAPITestAssetRoutes(mux *http.ServeMux, st *Store, wj func(http.ResponseWriter, any), fail func(http.ResponseWriter, int, any), actor func(*http.Request) string, admin func(*http.Request) bool, scope func(http.ResponseWriter, *http.Request) *Service, decode func(http.ResponseWriter, *http.Request, any) bool) {
	access := func(w http.ResponseWriter, r *http.Request) *Service {
		if actor(r) == "" || actor(r) == "local" {
			fail(w, 403, "请使用已绑定用户")
			return nil
		}
		sv := scope(w, r)
		if sv != nil && st.GetServiceEnvironment(sv.Name, r.URL.Query().Get("environment")) == nil {
			fail(w, 400, "应用未绑定环境")
			return nil
		}
		return sv
	}
	mux.HandleFunc("GET /api/api-testing/mcp/cases", func(w http.ResponseWriter, r *http.Request) {
		sv := access(w, r)
		if sv == nil {
			return
		}
		if raw := r.URL.Query().Get("case_id"); raw != "" {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || id <= 0 {
				fail(w, 400, "用例编号无效")
				return
			}
			c, err := st.apiTestCase(sv.ApplicationID, actor(r), id)
			if err != nil {
				fail(w, 404, "用例不存在或无权访问")
				return
			}
			c.CanEdit = caseCanEdit(c, actor(r), admin(r))
			for _, step := range c.Steps {
				if err := apitest.ValidateReferences(step.Request); err != nil {
					fail(w, 409, "用例含内联凭据，请由原作者改成受保护变量引用")
					return
				}
			}
			wj(w, map[string]any{"service": sv.Name, "case": c})
			return
		}
		rows, err := st.db.Query(`SELECT c.id FROM api_test_cases c LEFT JOIN api_test_case_access a ON a.case_id=c.id WHERE c.application_id=? AND (c.owner=? OR a.visibility='team') AND c.deleted=0 ORDER BY c.id DESC LIMIT 101`, sv.ApplicationID, actor(r))
		if err != nil {
			fail(w, 500, err)
			return
		}
		ids := []int64{}
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				fail(w, 500, err)
				return
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			fail(w, 500, err)
			return
		}
		truncated := len(ids) > 100
		if truncated {
			ids = ids[:100]
		}
		items := []any{}
		for _, id := range ids {
			if c, err := st.apiTestCase(sv.ApplicationID, actor(r), id); err == nil {
				items = append(items, map[string]any{"id": c.ID, "name": c.Name, "revision": c.Revision, "visibility": c.Visibility, "step_count": len(c.Steps), "can_edit": caseCanEdit(c, actor(r), admin(r))})
			}
		}
		wj(w, map[string]any{"service": sv.Name, "cases": items, "truncated": truncated})
	})
	mux.HandleFunc("POST /api/api-testing/mcp/case-preview", func(w http.ResponseWriter, r *http.Request) {
		sv := access(w, r)
		if sv == nil {
			return
		}
		var c apiTestCase
		if !decode(w, r, &c) {
			return
		}
		if c.Scope != "" {
			fail(w, 400, "跨应用用例请使用业务场景入口")
			return
		}
		if strings.TrimSpace(c.Name) == "" || len(c.Name) > 200 || c.ID < 0 {
			fail(w, 400, "用例名称或编号无效")
			return
		}
		if c.ID > 0 {
			old, err := st.apiTestCase(sv.ApplicationID, actor(r), c.ID)
			if err != nil || !caseCanEdit(old, actor(r), admin(r)) || old.Revision != c.Revision || !compatibleTestDefinition(old.DefinitionVersion, c.DefinitionVersion, c.Graph) {
				fail(w, 409, "用例已变化或无权编辑")
				return
			}
		} else if c.Revision != 0 {
			fail(w, 409, "新用例修订号无效")
			return
		}
		if err := validateAPITestCase(&c); err != nil {
			fail(w, 400, err)
			return
		}
		if _, err := st.apiTestDocument(sv.ApplicationID, c.DocumentID); err != nil {
			fail(w, 400, err)
			return
		}
		if err := st.validateCaseDocumentLinks(sv.ApplicationID, &c); err != nil {
			fail(w, 400, err)
			return
		}
		for _, step := range c.Steps {
			if err := apitest.ValidateReferences(step.Request); err != nil {
				fail(w, 400, err)
				return
			}
		}
		c.Owner = ""
		c.CanEdit = false
		wj(w, map[string]any{"valid": true, "case": c, "execution": map[bool]string{true: "graph", false: "sequential"}[c.Graph != nil], "max_steps": 10})
	})
}
