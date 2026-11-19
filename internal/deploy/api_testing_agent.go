package deploy

import (
	"encoding/json"
	"fmt"
	"logplatform/pkg/apitest"
	"math"
	"net/http"
	"strconv"
	"strings"
)

type apiTestSuiteMutation struct {
	Suite         apiTestSuite     `json:"suite"`
	CaseRevisions map[string]int64 `json:"case_revisions"`
}

// Cursor pages scan a bounded number of candidates. Filtering by visibility,
// application authorization and search happens before an item reaches a page.
// An empty page may still have a continuation; no unauthorized totals leak.
func (s *Store) apiTestAgentCatalog(r *http.Request, app, owner string, isAdmin bool) (any, error) {
	q := r.URL.Query()
	kind := q.Get("kind")
	limit, before := 20, int64(math.MaxInt64)
	if raw := q.Get("limit"); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || n < 1 || n > 100 {
			return nil, fmt.Errorf("每页需要1–100条")
		}
		limit = n
	}
	if raw := q.Get("before_id"); raw != "" {
		n, e := strconv.ParseInt(raw, 10, 64)
		if e != nil || n < 1 {
			return nil, fmt.Errorf("游标无效")
		}
		before = n
	}
	search := strings.ToLower(strings.TrimSpace(q.Get("q")))
	if len(search) > 200 {
		return nil, fmt.Errorf("搜索词过长")
	}
	allowed := map[string]bool{}
	if app == "" {
		var names []string
		if json.Unmarshal([]byte(q.Get("application_scope")), &names) != nil || len(names) == 0 {
			return nil, fmt.Errorf("业务场景需要明确应用授权范围")
		}
		for _, name := range names {
			allowed[name] = true
		}
	}
	visible := func(c *apiTestCase) bool {
		if app != "" {
			return true
		}
		services := scenarioServices(c)
		if len(services) == 0 {
			return false
		}
		for _, name := range services {
			if name == "" || !allowed["*"] && !allowed[name] {
				return false
			}
		}
		return true
	}
	table, where := "", ""
	args := []any{app, owner}
	switch kind {
	case "cases":
		table = "api_test_cases c LEFT JOIN api_test_case_access a ON a.case_id=c.id"
		where = "c.application_id=? AND (c.owner=? OR a.visibility='team') AND c.deleted=0"
	case "suites":
		if app == "" {
			return nil, fmt.Errorf("测试集需要明确应用")
		}
		table = "api_test_suites c"
		where = "c.application_id=? AND (c.owner=? OR c.visibility='team') AND c.deleted=0"
	case "runs":
		if e := s.apiTests.backfillRunIndex(app, owner); e != nil {
			return nil, e
		}
		table = "api_test_runs c JOIN api_test_run_index i ON i.run_id=c.id"
		where = "c.application_id=? AND c.owner=?"
		for _, filter := range []struct{ key, col string }{{"environment", "i.environment"}, {"status", "c.status"}, {"source", "i.source"}} {
			if v := q.Get(filter.key); v != "" {
				if filter.key == "status" && !map[string]bool{"queued": true, "running": true, "success": true, "failed": true, "unknown": true, "canceled": true}[v] {
					return nil, fmt.Errorf("记录状态无效")
				}
				if filter.key == "source" && v != "manual" && v != "case" {
					return nil, fmt.Errorf("记录来源无效")
				}
				where += " AND " + filter.col + "=?"
				args = append(args, v)
			}
		}
		for _, filter := range []struct{ key, col, op string }{{"suite_id", "i.suite_id", "="}, {"from", "c.created_at", ">="}, {"to", "c.created_at", "<="}} {
			if raw := q.Get(filter.key); raw != "" {
				n, e := strconv.ParseInt(raw, 10, 64)
				if e != nil || n < 0 {
					return nil, fmt.Errorf("历史筛选参数无效")
				}
				where += " AND " + filter.col + filter.op + "?"
				args = append(args, n)
			}
		}
		if raw := q.Get("case_id"); raw != "" {
			n, e := strconv.ParseInt(raw, 10, 64)
			if e != nil || n < 1 {
				return nil, fmt.Errorf("用例编号无效")
			}
			where += " AND EXISTS (SELECT 1 FROM api_test_run_cases rc WHERE rc.run_id=c.id AND rc.case_id=?)"
			args = append(args, n)
		}
	default:
		return nil, fmt.Errorf("目录类型无效")
	}
	items := []any{}
	next, hasMore := int64(0), false
	// Bounded scans also make sparse authorization/search results resumable.
	for scanned := 0; scanned < 1000; {
		queryArgs := append(append([]any{}, args...), before)
		rows, e := s.db.Query("SELECT c.id FROM "+table+" WHERE "+where+" AND c.id<? ORDER BY c.id DESC LIMIT 100", queryArgs...)
		if e != nil {
			return nil, e
		}
		ids := []int64{}
		for rows.Next() {
			var id int64
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return nil, e
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
		if len(ids) == 0 {
			hasMore = false
			break
		}
		for index, id := range ids {
			before, next = id, id
			scanned++
			var item any
			var name string
			switch kind {
			case "cases":
				c, e := s.apiTestCase(app, owner, id)
				if e != nil {
					return nil, e
				}
				if visible(c) {
					c.CanEdit = caseCanEdit(c, owner, isAdmin)
					item = scenarioSummary(c)
					if app != "" {
						delete(item.(map[string]any), "scope")
					}
					name = c.Name + " " + c.Module + " " + strconv.FormatInt(c.ID, 10)
				}
			case "suites":
				v, e := s.apiTestSuite(app, owner, id, isAdmin)
				if e != nil {
					return nil, e
				}
				item = v
				name = v.Name + " " + strconv.FormatInt(v.ID, 10)
			case "runs":
				v, e := s.apiTests.load(app, owner, id)
				if e != nil {
					return nil, e
				}
				if app != "" || v.CaseSnapshot != nil && visible(v.CaseSnapshot) {
					item = apiTestRunSummary(v)
					if app == "" {
						item.(map[string]any)["services"] = scenarioServices(v.CaseSnapshot)
					}
					name = v.Name + " " + strconv.FormatInt(v.ID, 10)
				}
			}
			if item != nil && (search == "" || strings.Contains(strings.ToLower(name), search)) {
				items = append(items, item)
			}
			hasMore = index < len(ids)-1 || len(ids) == 100
			if len(items) == limit {
				break
			}
		}
		if len(items) == limit || !hasMore {
			break
		}
	}
	out := map[string]any{"items": items, "has_more": hasMore, "next_before_id": nil, "limit": limit}
	if hasMore {
		out["next_before_id"] = next
	}
	if kind == "cases" {
		out["cases"] = items
		out["truncated"] = hasMore
	}
	if kind == "suites" {
		out["suites"] = items
	}
	return out, nil
}

func apiTestRunSummary(v *apiTestRun) map[string]any {
	return map[string]any{"source": v.Source, "case_id": v.CaseID, "case_revision": v.CaseRevision, "suite_id": v.SuiteID, "suite_revision": v.SuiteRevision, "unit_count": len(v.Units), "step_count": len(v.Steps), "id": v.ID, "name": v.Name, "status": v.Status, "environment": v.Environment, "method": v.Method, "path": v.Path, "mode": v.Mode, "started_at": v.StartedAt, "duration_ms": v.DurationMS, "trace_id": v.Result.TraceID}
}

func (s *Store) apiTestSuiteContext(app, owner string, isAdmin bool, id int64, ids []int64) (map[string]any, error) {
	var suite *apiTestSuite
	var err error
	if id > 0 {
		suite, err = s.apiTestSuite(app, owner, id, isAdmin)
		if err != nil {
			return nil, err
		}
		if ids == nil {
			ids = suite.CaseIDs
		}
	}
	if len(ids) == 0 || len(ids) > 20 {
		return nil, fmt.Errorf("请选择1–20个用例")
	}
	cases := []*apiTestCase{}
	revisions := map[string]int64{}
	for _, id := range ids {
		c, e := s.apiTestCase(app, owner, id)
		if e != nil {
			return nil, e
		}
		for _, step := range c.Steps {
			if e = apitest.ValidateReferences(step.Request); e != nil {
				return nil, fmt.Errorf("用例含内联凭据，请先改为受保护变量引用")
			}
		}
		key := strconv.FormatInt(id, 10)
		if _, exists := revisions[key]; exists {
			return nil, fmt.Errorf("用例重复")
		}
		revisions[key] = c.Revision
		cases = append(cases, c)
	}
	return map[string]any{"suite": suite, "cases": cases, "case_revisions": revisions}, nil
}

func registerAPITestAgentRoutes(mux *http.ServeMux, st *Store, wj func(http.ResponseWriter, any), fail func(http.ResponseWriter, int, any), actor func(*http.Request) string, admin func(*http.Request) bool, scope func(http.ResponseWriter, *http.Request) *Service, decode func(http.ResponseWriter, *http.Request, any) bool) {
	access := func(w http.ResponseWriter, r *http.Request) (string, bool) {
		if actor(r) == "" || actor(r) == "local" || r.URL.Query().Get("environment") == "" {
			fail(w, 403, "需要已绑定用户和具体环境")
			return "", false
		}
		if r.URL.Query().Get("service") == "" {
			return "", true
		}
		sv := scope(w, r)
		if sv == nil {
			return "", false
		}
		if st.GetServiceEnvironment(sv.Name, r.URL.Query().Get("environment")) == nil {
			fail(w, 400, "应用未绑定环境")
			return "", false
		}
		return sv.ApplicationID, true
	}
	mux.HandleFunc("GET /api/api-testing/mcp/catalog", func(w http.ResponseWriter, r *http.Request) {
		app, ok := access(w, r)
		if !ok {
			return
		}
		out, e := st.apiTestAgentCatalog(r, app, actor(r), admin(r))
		if e != nil {
			fail(w, 400, e)
			return
		}
		wj(w, out)
	})
	mux.HandleFunc("GET /api/api-testing/mcp/suite-context", func(w http.ResponseWriter, r *http.Request) {
		app, ok := access(w, r)
		if !ok {
			return
		}
		if app == "" {
			fail(w, 400, "测试集需要明确应用")
			return
		}
		var id int64
		if raw := r.URL.Query().Get("suite_id"); raw != "" {
			var e error
			id, e = strconv.ParseInt(raw, 10, 64)
			if e != nil || id < 1 {
				fail(w, 400, "测试集编号无效")
				return
			}
		}
		var ids []int64
		if raw := r.URL.Query().Get("case_ids"); raw != "" {
			if json.Unmarshal([]byte(raw), &ids) != nil {
				fail(w, 400, "用例编号无效")
				return
			}
		}
		out, e := st.apiTestSuiteContext(app, actor(r), admin(r), id, ids)
		if e != nil {
			fail(w, 409, e)
			return
		}
		wj(w, out)
	})
	mux.HandleFunc("POST /api/api-testing/mcp/suite-preview", func(w http.ResponseWriter, r *http.Request) {
		app, ok := access(w, r)
		if !ok {
			return
		}
		if app == "" {
			fail(w, 400, "测试集需要明确应用")
			return
		}
		var v apiTestSuite
		if !decode(w, r, &v) {
			return
		}
		if v.ID < 0 {
			fail(w, 400, "测试集编号无效")
			return
		}
		if _, e := st.validateAPITestSuite(&v, app, actor(r), admin(r)); e != nil {
			fail(w, apiTestSuiteErrorStatus(e), e)
			return
		}
		ctx, e := st.apiTestSuiteContext(app, actor(r), admin(r), v.ID, v.CaseIDs)
		if e != nil {
			fail(w, 409, e)
			return
		}
		v.CanEdit = false
		v.Owner = ""
		ctx["after"] = v
		ctx["valid"] = true
		ctx["sends_requests"] = false
		wj(w, ctx)
	})
	control := func(w http.ResponseWriter, r *http.Request) (*apiTestRun, string, bool) {
		app, ok := access(w, r)
		if !ok {
			return nil, "", false
		}
		id, e := strconv.ParseInt(r.URL.Query().Get("run_id"), 10, 64)
		if e != nil || id < 1 {
			fail(w, 400, "运行编号无效")
			return nil, "", false
		}
		v, e := st.apiTests.load(app, actor(r), id)
		if e != nil || v.Environment != r.URL.Query().Get("environment") {
			fail(w, 404, "运行不存在或不属于指定环境")
			return nil, "", false
		}
		// Never include changing status/progress: stopping is bound to immutable run identity.
		identity := st.privateOperationDigest(map[string]any{"id": v.ID, "owner": v.Owner, "environment": v.Environment, "service": v.Service, "started_at": v.StartedAt, "case_id": v.CaseID, "case_revision": v.CaseRevision, "suite_id": v.SuiteID, "suite_revision": v.SuiteRevision, "targets": v.Targets, "target": v.Target})
		return v, identity, true
	}
	mux.HandleFunc("GET /api/api-testing/mcp/run-control", func(w http.ResponseWriter, r *http.Request) {
		v, identity, ok := control(w, r)
		if !ok {
			return
		}
		out := map[string]any{"identity": identity, "run": apiTestRunSummary(v), "service": v.Service, "environment": v.Environment}
		if v.Scenario && v.CaseSnapshot != nil {
			out["services"] = scenarioServices(v.CaseSnapshot)
		}
		wj(w, out)
	})
	mux.HandleFunc("POST /api/api-testing/mcp/stop", func(w http.ResponseWriter, r *http.Request) {
		v, identity, ok := control(w, r)
		if !ok {
			return
		}
		var in struct {
			ExpectedIdentity string `json:"expected_identity"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.ExpectedIdentity != identity {
			fail(w, 409, "运行身份已变化，请重新核对")
			return
		}
		st.apiTests.mu.Lock()
		cancel := st.apiTests.jobs[v.ID]
		st.apiTests.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		wj(w, map[string]any{"id": v.ID, "stop_requested": cancel != nil, "status": v.Status, "requires_result_check": true})
	})
}
