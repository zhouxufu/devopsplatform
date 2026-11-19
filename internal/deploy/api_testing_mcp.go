package deploy

import (
	"logplatform/pkg/apitest"
	"net/http"
	"sort"
	"strconv"
)

func referenceTestProfile(p apiTestProfile) (apiTestProfile, error) {
	var first error
	p.Auth, first = apitest.ReferenceAuth(p.Auth)
	p.Entries = append([]apiTestEntry(nil), p.Entries...)
	for i := range p.Entries {
		var err error
		p.Entries[i].Auth, err = apitest.ReferenceAuth(p.Entries[i].Auth)
		if first == nil {
			first = err
		}
	}
	return p, first
}

func registerAPITestMCPRoutes(mux *http.ServeMux, st *Store, wj func(http.ResponseWriter, any), fail func(http.ResponseWriter, int, any), actor func(*http.Request) string, admin func(*http.Request) bool, scope func(http.ResponseWriter, *http.Request) *Service, decode func(http.ResponseWriter, *http.Request, any) bool) {
	registerAPITestAssetRoutes(mux, st, wj, fail, actor, admin, scope, decode)
	registerAPITestAgentRoutes(mux, st, wj, fail, actor, admin, scope, decode)
	// Read only context never returns variable values, unredacted legacy login
	// bodies or bearer tokens. The digest is keyed by the platform secret key.
	context := func(w http.ResponseWriter, r *http.Request) {
		if actor(r) == "" || actor(r) == "local" {
			fail(w, 403, "请使用已绑定用户")
			return
		}
		sv := scope(w, r)
		if sv == nil {
			return
		}
		env := st.GetServiceEnvironment(sv.Name, r.URL.Query().Get("environment"))
		if env == nil {
			fail(w, 400, "应用未绑定环境")
			return
		}
		p, err := st.apiTestingProfile(sv.ApplicationID, env.Environment)
		if err != nil {
			fail(w, 500, "配置读取失败")
			return
		}
		vars, err := st.apiTestVariables(sv, env)
		if err != nil {
			fail(w, 409, "绑定变量不可用")
			return
		}
		keys := []string{}
		for k := range vars {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		safe, referenceErr := referenceTestProfile(p)
		out := map[string]any{"service": sv.Name, "environment": env.Environment, "protected": env.Protected, "enabled": p.Enabled, "targets": st.apiTestTargets(sv, env, p), "variables": keys, "variables_digest": st.privateOperationDigest(vars), "revision": p.Revision}
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
			for _, step := range c.Steps {
				if err := apitest.ValidateReferences(step.Request); err != nil {
					fail(w, 400, err)
					return
				}
			}
			out["case"] = c
		}
		if raw := r.URL.Query().Get("suite_id"); raw != "" {
			id, e := strconv.ParseInt(raw, 10, 64)
			if e != nil || id < 1 || r.URL.Query().Get("case_id") != "" {
				fail(w, 400, "测试集编号无效或同时选择用例")
				return
			}
			ctx, e := st.apiTestSuiteContext(sv.ApplicationID, actor(r), admin(r), id, nil)
			if e != nil {
				fail(w, 409, e)
				return
			}
			for k, v := range ctx {
				out[k] = v
			}
		}
		if admin(r) {
			out["profile"] = safe
			if referenceErr != nil {
				out["configuration_warning"] = referenceErr.Error()
			}
		}
		wj(w, out)
	}
	mux.HandleFunc("GET /api/api-testing/mcp/context", context)
	mux.HandleFunc("PUT /api/api-testing/mcp/profile", func(w http.ResponseWriter, r *http.Request) {
		if !admin(r) {
			fail(w, 403, "仅管理员可配置测试入口与认证")
			return
		}
		sv := scope(w, r)
		if sv == nil {
			return
		}
		env := r.URL.Query().Get("environment")
		if st.GetServiceEnvironment(sv.Name, env) == nil {
			fail(w, 400, "应用未绑定环境")
			return
		}
		var p apiTestProfile
		if !decode(w, r, &p) {
			return
		}
		if _, err := referenceTestProfile(p); err != nil {
			fail(w, 400, err)
			return
		}
		if err := st.saveAPITestingProfile(sv.ApplicationID, env, p); err != nil {
			fail(w, 409, err)
			return
		}
		st.Audit(actor(r), "configure", "api-test-profile", sv.ApplicationID+"/"+env, map[string]any{"source": "mcp", "revision": p.Revision + 1})
		wj(w, map[string]any{"service": sv.Name, "environment": env, "revision": p.Revision + 1, "saved": true})
	})
	mux.HandleFunc("POST /api/api-testing/mcp/runs", func(w http.ResponseWriter, r *http.Request) {
		sv := scope(w, r)
		if sv == nil {
			return
		}
		var in apiTestInput
		if !decode(w, r, &in) {
			return
		}
		if actor(r) == "" || actor(r) == "local" || in.Environment != r.URL.Query().Get("environment") || in.ExpectedVariablesDigest == "" {
			fail(w, 400, "执行必须携带已确认的应用环境和凭据摘要")
			return
		}
		env := st.GetServiceEnvironment(sv.Name, in.Environment)
		if env == nil {
			fail(w, 400, "应用未绑定环境")
			return
		}
		p, err := st.apiTestingProfile(sv.ApplicationID, in.Environment)
		if err != nil {
			fail(w, 500, "认证配置读取失败")
			return
		}
		for _, target := range st.apiTestTargets(sv, env, p) {
			if target.Key == in.TargetKey && !target.CredentialReferencesValid {
				fail(w, 400, "入口凭据请先改为受保护变量引用")
				return
			}
		}
		if err := apitest.ValidateReferences(in.Request); err != nil {
			fail(w, 400, err)
			return
		}
		run, err := st.apiTests.start(sv, actor(r), admin(r), in)
		if err != nil {
			fail(w, 409, err)
			return
		}
		wj(w, run)
	})
}
