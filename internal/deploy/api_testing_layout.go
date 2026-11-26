package deploy

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"

	"logplatform/pkg/apitest"
)

type apiTestPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}
type apiTestLayout struct {
	Revision  int64                   `json:"revision"`
	Positions map[string]apiTestPoint `json:"positions"`
	Zoom      float64                 `json:"zoom"`
	Pan       apiTestPoint            `json:"pan"`
}

func validTestPoint(p apiTestPoint) bool {
	return !math.IsNaN(p.X) && !math.IsNaN(p.Y) && !math.IsInf(p.X, 0) && !math.IsInf(p.Y, 0) && math.Abs(p.X) <= 1e6 && math.Abs(p.Y) <= 1e6
}
func (s *Store) apiTestLayout(app, owner, workspace string, id int64) (apiTestLayout, error) {
	v := apiTestLayout{Positions: map[string]apiTestPoint{}, Zoom: 1, Pan: apiTestPoint{20, 28}}
	var raw string
	err := s.db.QueryRow(`SELECT document,revision FROM api_test_layouts WHERE application_id=? AND case_id=? AND owner=? AND workspace=?`, app, id, owner, workspace).Scan(&raw, &v.Revision)
	if err == sql.ErrNoRows {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	revision := v.Revision
	if err = json.Unmarshal([]byte(raw), &v); err != nil {
		return v, err
	}
	v.Revision = revision
	return v, nil
}
func registerAPITestLayoutRoutes(mux *http.ServeMux, st *Store, wj func(http.ResponseWriter, any), fail func(http.ResponseWriter, int, any), actor func(*http.Request) string, scope func(http.ResponseWriter, *http.Request) *Service, decode func(http.ResponseWriter, *http.Request, any) bool, groups ...string) {
	group := "cases"
	if len(groups) > 0 {
		group = groups[0]
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		app, service := "", ""
		if group == "cases" {
			sv := scope(w, r)
			if sv == nil {
				return
			}
			app, service = sv.ApplicationID, sv.Name
		}
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			fail(w, 400, "用例编号无效")
			return
		}
		if env := r.URL.Query().Get("environment"); env != "" && group == "cases" && st.GetServiceEnvironment(service, env) == nil {
			fail(w, 400, "应用未绑定环境")
			return
		}
		c, err := st.apiTestCase(app, actor(r), id)
		if err != nil {
			fail(w, 404, "用例不存在或无权访问")
			return
		}
		workspace := r.URL.Query().Get("workspace")
		if workspace == "" {
			workspace = "standalone"
		}
		if len(workspace) > 191 {
			fail(w, 400, "工作区标识过长")
			return
		}
		current, err := st.apiTestLayout(app, actor(r), workspace, id)
		if err != nil {
			fail(w, 500, err)
			return
		}
		if r.Method == "GET" {
			wj(w, current)
			return
		}
		var next apiTestLayout
		if !decode(w, r, &next) {
			return
		}
		if next.Revision != current.Revision {
			w.WriteHeader(409)
			wj(w, map[string]any{"error": "画布布局已变化，请保留本地布局并重新读取", "current": current})
			return
		}
		if next.Zoom < .4 || next.Zoom > 1.6 || !validTestPoint(next.Pan) || len(next.Positions) > apitest.MaxCaseSteps {
			fail(w, 400, "布局范围无效")
			return
		}
		ids := map[string]bool{}
		for _, step := range c.Steps {
			ids[step.ID] = true
		}
		for id, p := range next.Positions {
			if !ids[id] || !validTestPoint(p) {
				fail(w, 400, "布局包含不存在节点或无效坐标")
				return
			}
		}
		if r.Method == "POST" {
			wj(w, map[string]any{"valid": true, "layout": next})
			return
		}
		next.Revision++
		raw, _ := json.Marshal(next)
		if current.Revision == 0 {
			_, err = st.db.Exec(`INSERT INTO api_test_layouts(application_id,case_id,owner,workspace,document,revision) VALUES(?,?,?,?,?,?)`, app, id, actor(r), workspace, string(raw), next.Revision)
		} else {
			var result sql.Result
			result, err = st.db.Exec(`UPDATE api_test_layouts SET document=?,revision=? WHERE application_id=? AND case_id=? AND owner=? AND workspace=? AND revision=?`, string(raw), next.Revision, app, id, actor(r), workspace, current.Revision)
			if err == nil {
				n, _ := result.RowsAffected()
				if n != 1 {
					err = fmt.Errorf("布局修订冲突")
				}
			}
		}
		if err != nil {
			fail(w, 409, "布局修订冲突，请重新读取")
			return
		}
		wj(w, next)
	}
	mux.HandleFunc("POST /api/api-testing/"+group+"/{id}/layout/validate", handler)
	mux.HandleFunc("GET /api/api-testing/"+group+"/{id}/layout", handler)
	mux.HandleFunc("PUT /api/api-testing/"+group+"/{id}/layout", handler)
}
