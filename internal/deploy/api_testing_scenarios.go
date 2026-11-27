package deploy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"logplatform/pkg/apitest"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Business scenarios have no owning application (the existing tables use an
// empty application_id). Every HTTP node names a real, stable application.
// Legacy application-owned cases and reports keep their original namespaces.
type apiScenarioSelection struct {
	Key             string `json:"key"`
	Fingerprint     string `json:"fingerprint"`
	VariablesDigest string `json:"variables_digest"`
}
type apiScenarioInput struct {
	RequestID        string                          `json:"request_id"`
	Environment      string                          `json:"environment"`
	CaseID           int64                           `json:"case_id"`
	CaseRevision     int64                           `json:"case_revision"`
	Targets          map[string]apiScenarioSelection `json:"targets"`
	ConfirmProtected bool                            `json:"confirm_protected"`
	Reason           string                          `json:"reason"`
}
type apiScenarioRunTarget struct {
	Service       string        `json:"service"`
	ApplicationID string        `json:"application_id"`
	Environment   string        `json:"environment"`
	Target        apiTestTarget `json:"target"`
}
type apiScenarioRuntime struct {
	apiScenarioRunTarget
	Auth       apitest.Auth
	Variables  map[string]string
	Redactions map[string]string
}

func scenarioServices(c *apiTestCase) []string {
	seen := map[string]bool{}
	for _, step := range c.Steps {
		if isTestRequest(step) {
			seen[step.Service] = true
		}
	}
	services := []string{}
	for name := range seen {
		services = append(services, name)
	}
	sort.Strings(services)
	return services
}
func (s *Store) validateScenario(c *apiTestCase) error {
	c.Scope = "scenario"
	if c.DocumentID != 0 {
		return fmt.Errorf("业务场景的接口文档请关联到具体节点")
	}
	if err := validateAPITestCase(c); err != nil {
		return err
	}
	if err := validateAPITestBindings(nil, []*apiTestCase{c}); err != nil {
		return err
	}
	for i := range c.Steps {
		step := &c.Steps[i]
		if !isTestRequest(*step) {
			if step.Service != "" || step.ApplicationID != "" {
				return fmt.Errorf("控制节点不绑定应用")
			}
			continue
		}
		sv := s.GetService(step.Service)
		if sv == nil {
			return fmt.Errorf("步骤 %d 请选择已登记的目标应用", i+1)
		}
		if step.ApplicationID != "" && step.ApplicationID != sv.ApplicationID {
			return fmt.Errorf("步骤 %d 的应用身份已变化，请重新选择并核对目标", i+1)
		}
		step.ApplicationID = sv.ApplicationID
		if err := apitest.ValidateReferences(step.Request); err != nil {
			return fmt.Errorf("步骤 %d：%w", i+1, err)
		}
		copy := apiTestCase{Steps: []apiTestStep{*step}}
		if err := s.validateCaseDocumentLinks(sv.ApplicationID, &copy); err != nil {
			return fmt.Errorf("步骤 %d：%w", i+1, err)
		}
	}
	if len(scenarioServices(c)) == 0 {
		return fmt.Errorf("业务场景至少需要一个接口节点")
	}
	return nil
}
func scenarioTargetAuth(p apiTestProfile, key string) apitest.Auth {
	for _, entry := range p.Entries {
		if key == "endpoint:"+entry.Key {
			return entry.Auth
		}
	}
	return p.Auth
}
func scenarioVariablePreflight(c *apiTestCase, service string, auth apitest.Auth, variables map[string]string) error {
	for _, step := range c.Steps {
		if isTestRequest(step) && step.Service == service {
			stepAuth := auth
			if step.Request.Mode == "dubbo" {
				stepAuth = apitest.Auth{Mode: "none"}
			}
			if err := apitest.ValidateVariableBindings(step.Request, stepAuth, variables); err != nil {
				return fmt.Errorf("应用 %s：%w", service, err)
			}
		}
	}
	return nil
}
func scenarioSummary(c *apiTestCase) map[string]any {
	return map[string]any{"scope": "scenario", "id": c.ID, "name": c.Name, "module": c.Module, "priority": c.Priority, "revision": c.Revision, "visibility": c.Visibility, "owner": c.Owner, "can_edit": c.CanEdit, "step_count": len(c.Steps), "data_count": len(c.DataRows), "services": scenarioServices(c)}
}
func (s *Store) scenarioContext(c *apiTestCase, environment string) (map[string]any, error) {
	if err := s.validateScenario(c); err != nil {
		return nil, err
	}
	services := scenarioServices(c)
	environments := []map[string]any{}
	for _, env := range s.Environments() {
		available, protected := true, false
		for _, name := range services {
			binding := s.GetServiceEnvironment(name, env.Name)
			if binding == nil {
				available = false
				break
			}
			protected = protected || binding.Protected
		}
		if available {
			environments = append(environments, map[string]any{"name": env.Name, "display_name": env.DisplayName, "protected": protected})
		}
	}
	out := map[string]any{"case": c, "services": services, "environments": environments, "applications": []any{}, "environment": environment, "protected": false}
	if environment == "" {
		return out, nil
	}
	applications := []any{}
	protected := false
	for _, name := range services {
		sv := s.GetService(name)
		env := s.GetServiceEnvironment(name, environment)
		if env == nil {
			return nil, fmt.Errorf("应用 %s 未绑定所选环境", name)
		}
		p, err := s.apiTestingProfile(sv.ApplicationID, environment)
		if err != nil {
			return nil, err
		}
		variables, err := s.apiTestVariables(sv, env)
		if err != nil {
			return nil, fmt.Errorf("应用 %s 的绑定变量不可用", name)
		}
		keys := []string{}
		for key := range variables {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		targets := s.apiTestTargets(sv, env, p)
		for i := range targets {
			if scenarioVariablePreflight(c, name, scenarioTargetAuth(p, targets[i].Key), variables) != nil {
				targets[i].CredentialReferencesValid = false
			}
		}
		applications = append(applications, map[string]any{"service": name, "application_id": sv.ApplicationID, "display_name": sv.DisplayName, "enabled": p.Enabled, "targets": targets, "variables": keys, "variables_digest": s.privateOperationDigest(variables), "revision": p.Revision, "protected": env.Protected})
		protected = protected || env.Protected
	}
	out["applications"], out["protected"] = applications, protected
	return out, nil
}

func (m *apiTestManager) startScenario(owner string, admin bool, in apiScenarioInput) (*apiTestRun, error) {
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`).MatchString(in.RequestID) || in.CaseID < 1 || in.CaseRevision < 1 || in.Environment == "" || in.Environment == "*" || len(in.Reason) > 1000 {
		return nil, fmt.Errorf("场景执行标识、环境或修订无效")
	}
	raw, _ := json.Marshal(in)
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	m.mu.Lock()
	defer m.mu.Unlock()
	var previous int64
	var oldDigest string
	if m.st.db.QueryRow(`SELECT id,request_digest FROM api_test_runs WHERE application_id='' AND owner=? AND request_key=?`, owner, in.RequestID).Scan(&previous, &oldDigest) == nil {
		if digest != oldDigest {
			return nil, fmt.Errorf("请求标识已用于不同场景内容")
		}
		return m.load("", owner, previous)
	}
	if m.closing || len(m.jobs) >= 4 || m.owners[owner] >= 1 {
		return nil, fmt.Errorf("已有测试执行或服务正在停止，请先等待或核对原记录")
	}
	c, err := m.st.apiTestCase("", owner, in.CaseID)
	if err != nil || c.Revision != in.CaseRevision {
		return nil, fmt.Errorf("业务场景已变化或无权访问，请重新读取")
	}
	if err = m.st.validateScenario(c); err != nil {
		return nil, err
	}
	services := scenarioServices(c)
	if len(in.Targets) != len(services) {
		return nil, fmt.Errorf("请逐项确认所有应用的执行入口")
	}
	runtimes := map[string]apiScenarioRuntime{}
	redactions := map[string]string{}
	public := []apiScenarioRunTarget{}
	for _, name := range services {
		sv := m.st.GetService(name)
		env := m.st.GetServiceEnvironment(name, in.Environment)
		if env == nil {
			return nil, fmt.Errorf("应用 %s 未绑定目标环境", name)
		}
		if env.Protected && (!admin || !in.ConfirmProtected || strings.TrimSpace(in.Reason) == "") {
			return nil, fmt.Errorf("受保护环境必须管理员确认全部应用目标并填写原因")
		}
		p, e := m.st.apiTestingProfile(sv.ApplicationID, in.Environment)
		if e != nil {
			return nil, e
		}
		if !p.Enabled {
			return nil, fmt.Errorf("应用 %s 的接口测试已停用", name)
		}
		selection, ok := in.Targets[name]
		if !ok {
			return nil, fmt.Errorf("缺少应用 %s 的目标确认", name)
		}
		var target *apiTestTarget
		for _, item := range m.st.apiTestTargets(sv, env, p) {
			if item.Key == selection.Key {
				copy := item
				target = &copy
				break
			}
		}
		if target == nil || !target.CredentialReferencesValid || target.Fingerprint != selection.Fingerprint {
			return nil, fmt.Errorf("应用 %s 的入口、认证或部署已变化，请重新预览", name)
		}
		if strings.HasPrefix(target.Key, "endpoint:") {
			for _, entry := range p.Entries {
				if target.Key == "endpoint:"+entry.Key {
					p.Auth = entry.Auth
					break
				}
			}
		}
		variables, e := m.st.apiTestVariables(sv, env)
		if e != nil {
			return nil, e
		}
		if selection.VariablesDigest == "" || selection.VariablesDigest != m.st.privateOperationDigest(variables) {
			return nil, fmt.Errorf("应用 %s 的变量已变化，请重新预览", name)
		}
		if e = scenarioVariablePreflight(c, name, p.Auth, variables); e != nil {
			return nil, e
		}
		for key, value := range variables {
			redactions[name+"."+key] = value
		}
		identity := apiScenarioRunTarget{Service: name, ApplicationID: sv.ApplicationID, Environment: in.Environment, Target: *target}
		public = append(public, identity)
		runtimes[name] = apiScenarioRuntime{apiScenarioRunTarget: identity, Auth: p.Auth, Variables: variables}
		relevant := []apiTestStep{}
		for _, step := range c.Steps {
			if step.Service == name {
				relevant = append(relevant, step)
			}
		}
		nodeTargets, e := m.st.prepareProtocolTargets(name, in.Environment, p, *target, relevant, variables)
		if e != nil {
			return nil, e
		}
		for key, value := range nodeTargets {
			runtimes[key] = value
		}

	}
	for name, runtime := range runtimes {
		runtime.Redactions = redactions
		runtimes[name] = runtime
	}
	rows := c.DataRows
	if len(rows) == 0 {
		rows = []apiTestDataRow{{Name: "默认数据"}}
	}
	if len(rows) > 20 || len(rows)*len(c.Steps) > 100 {
		return nil, fmt.Errorf("单次场景最多20组数据、100步")
	}
	v := apiTestRun{Scenario: true, Targets: public, Owner: owner, Name: c.Name, Environment: in.Environment, Status: "queued", CaseID: c.ID, CaseRevision: c.Revision, Source: "case", StartedAt: time.Now().Unix(), Result: apitest.Result{Events: []apitest.Event{}, Checks: []apitest.Check{}}}
	planned := []apiTestPlannedUnit{}
	for _, row := range rows {
		data := map[string]any{}
		if len(c.DataRows) > 0 {
			data, err = runAPITestDataScope(row)
			if err != nil {
				return nil, err
			}
		}
		snapshot := *c
		snapshot.DataRows = nil
		snapshot.Steps = append([]apiTestStep(nil), c.Steps...)
		unit := apiTestUnit{RowName: row.Name, Start: len(v.Steps), End: len(v.Steps) + len(c.Steps), Status: "pending"}
		for i, step := range c.Steps {
			snapshot.Steps[i].Request = apitest.SanitizeRequest(step.Request, redactions)
			r := apiTestStepResult{ID: step.ID, Kind: step.Kind, Service: step.Service, ApplicationID: step.ApplicationID, Environment: in.Environment, DocumentID: step.DocumentID, Name: step.Name, Action: step.Action, Expected: step.Expected, Request: snapshot.Steps[i].Request, Status: "pending"}
			if runtime, ok := scenarioRuntimeFor(runtimes, step); ok {
				target := runtime.Target
				r.Target = &target
				r.DocumentDigest, _ = m.st.apiTestDocument(runtime.ApplicationID, step.DocumentID)
			}
			v.Steps = append(v.Steps, r)
		}
		snapshot.Request = apitest.SanitizeRequest(snapshot.Request, redactions)
		unit.CaseSnapshot = snapshot
		v.Units = append(v.Units, unit)
		planned = append(planned, apiTestPlannedUnit{Case: *c, Data: data, RowName: row.Name})
	}
	snapshot := v.Units[0].CaseSnapshot
	v.CaseSnapshot = &snapshot
	raw, _ = json.Marshal(v)
	enc, err := m.st.encryptSecret(string(raw))
	if err != nil {
		return nil, err
	}
	res, err := m.st.db.Exec(`INSERT INTO api_test_runs(application_id,service,owner,request_key,request_digest,document,status,created_at,updated_at) VALUES('','',?,?,?,?,?,?,?)`, owner, in.RequestID, digest, enc, v.Status, v.StartedAt, v.StartedAt)
	if err != nil {
		return nil, err
	}
	v.ID, _ = res.LastInsertId()
	if err = m.indexRun(&v, ""); err != nil {
		v.Status = "failed"
		_ = m.persist(v)
		return nil, err
	}
	raw, _ = json.Marshal(v)
	var queued apiTestRun
	_ = json.Unmarshal(raw, &queued)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	m.jobs[v.ID] = cancel
	m.owners[owner]++
	m.wg.Add(1)
	m.st.Audit(owner, "start", "api-test-scenario", fmt.Sprint(v.ID), map[string]any{"services": services, "environment": in.Environment, "case_id": c.ID, "case_revision": c.Revision})
	go func(v apiTestRun) {
		defer m.wg.Done()
		defer func() { cancel(); m.mu.Lock(); delete(m.jobs, v.ID); m.owners[owner]--; m.mu.Unlock() }()
		v.Status = "running"
		if m.persist(v) != nil {
			return
		}
		m.runGraphCases(ctx, &v, planned, apiTestTarget{}, apiTestProfile{}, nil, nil, runtimes)
	}(v)
	return &queued, nil
}

func redactScenarioResult(v apitest.Result, values map[string]string) apitest.Result {
	secrets := []string{}
	for _, value := range values {
		if value != "" {
			secrets = append(secrets, value)
		}
	}
	v.Body = apitest.Redact(v.Body, secrets)
	v.Error = apitest.Redact(v.Error, secrets)
	v.URL = apitest.SafeURL(v.URL, values)
	headers := map[string]string{}
	for k, value := range v.Headers {
		if apitest.SecretKey(k) {
			headers[k] = "[REDACTED]"
		} else {
			headers[k] = apitest.Redact(value, secrets)
		}
	}
	v.Headers = headers
	v.Events = append([]apitest.Event(nil), v.Events...)
	for i := range v.Events {
		v.Events[i].Data = apitest.Redact(v.Events[i].Data, secrets)
		v.Events[i].ID = apitest.Redact(v.Events[i].ID, secrets)
	}
	v.Checks = append([]apitest.Check(nil), v.Checks...)
	for i := range v.Checks {
		v.Checks[i].Actual = apitest.Redact(v.Checks[i].Actual, secrets)
		v.Checks[i].Expected = apitest.Redact(v.Checks[i].Expected, secrets)
	}
	if v.Authentication != nil {
		copy := *v.Authentication
		copy.Identity = apitest.Redact(copy.Identity, secrets)
		copy.LoginURL = apitest.SafeURL(copy.LoginURL, values)
		v.Authentication = &copy
	}
	return v
}

func registerAPIScenarioRoutes(mux *http.ServeMux, st *Store, wj func(http.ResponseWriter, any), fail func(http.ResponseWriter, int, any), actor func(*http.Request) string, admin func(*http.Request) bool, decode func(http.ResponseWriter, *http.Request, any) bool) {
	getCase := func(w http.ResponseWriter, r *http.Request) *apiTestCase {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		c, err := st.apiTestCase("", actor(r), id)
		if err != nil {
			fail(w, 404, "业务场景不存在或无权访问")
			return nil
		}
		c.CanEdit = caseCanEdit(c, actor(r), admin(r))
		return c
	}
	mux.HandleFunc("GET /api/api-testing/scenario-cases", func(w http.ResponseWriter, r *http.Request) {
		rows, err := st.db.Query(`SELECT c.id FROM api_test_cases c LEFT JOIN api_test_case_access a ON a.case_id=c.id WHERE c.application_id='' AND c.deleted=0 AND (c.owner=? OR a.visibility='team') ORDER BY c.id DESC LIMIT 101`, actor(r))
		if err != nil {
			fail(w, 500, err)
			return
		}
		ids := []int64{}
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				break
			}
			ids = append(ids, id)
		}
		if err == nil {
			err = rows.Err()
		}
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
			if c, e := st.apiTestCase("", actor(r), id); e == nil {
				c.CanEdit = caseCanEdit(c, actor(r), admin(r))
				items = append(items, scenarioSummary(c))
			}
		}
		wj(w, map[string]any{"cases": items, "truncated": truncated})
	})
	mux.HandleFunc("GET /api/api-testing/scenario-cases/{id}", func(w http.ResponseWriter, r *http.Request) {
		if c := getCase(w, r); c != nil {
			wj(w, c)
		}
	})
	save := func(w http.ResponseWriter, r *http.Request) {
		var c apiTestCase
		if !decode(w, r, &c) {
			return
		}
		if strings.TrimSpace(c.Name) == "" || len(c.Name) > 200 {
			fail(w, 400, "请填写用例名称")
			return
		}
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if id > 0 {
			old := getCase(w, r)
			if old == nil {
				return
			}
			if !old.CanEdit || old.Revision != c.Revision || !compatibleTestDefinition(old.DefinitionVersion, c.DefinitionVersion, c.Graph) {
				fail(w, 409, "场景已变化或无权编辑")
				return
			}
			c.Owner = old.Owner
		} else {
			if c.Revision != 0 {
				fail(w, 409, "新场景修订无效")
				return
			}
			c.Owner = actor(r)
		}
		if err := st.validateScenario(&c); err != nil {
			fail(w, 400, err)
			return
		}
		oldRevision := c.Revision
		c.ID = id
		c.Revision++
		c.CanEdit = true
		raw, _ := json.Marshal(c)
		enc, err := st.encryptSecret(string(raw))
		if err != nil {
			fail(w, 500, err)
			return
		}
		tx, err := st.db.Begin()
		if err != nil {
			fail(w, 500, err)
			return
		}
		defer tx.Rollback()
		if id == 0 {
			res, e := tx.Exec(`INSERT INTO api_test_cases(application_id,owner,document,revision,deleted,updated_at) VALUES('',?,?,?,0,?)`, c.Owner, enc, c.Revision, time.Now().Unix())
			err = e
			if e == nil {
				c.ID, _ = res.LastInsertId()
			}
		} else {
			res, e := tx.Exec(`UPDATE api_test_cases SET document=?,revision=?,updated_at=? WHERE id=? AND application_id='' AND owner=? AND revision=? AND deleted=0`, enc, c.Revision, time.Now().Unix(), id, c.Owner, oldRevision)
			err = e
			if e == nil {
				n, _ := res.RowsAffected()
				if n != 1 {
					err = fmt.Errorf("场景已变化")
				}
			}
		}
		if err == nil {
			_, err = tx.Exec(`INSERT INTO api_test_case_access(case_id,visibility) VALUES(?,?) ON CONFLICT(case_id) DO UPDATE SET visibility=excluded.visibility`, c.ID, c.Visibility)
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			fail(w, 409, err)
			return
		}
		wj(w, c)
	}
	mux.HandleFunc("POST /api/api-testing/scenario-cases", save)
	mux.HandleFunc("PUT /api/api-testing/scenario-cases/{id}", save)
	mux.HandleFunc("DELETE /api/api-testing/scenario-cases/{id}", func(w http.ResponseWriter, r *http.Request) {
		c := getCase(w, r)
		if c == nil {
			return
		}
		if !c.CanEdit {
			fail(w, 403, "无权删除业务场景")
			return
		}
		res, err := st.db.Exec(`UPDATE api_test_cases SET deleted=1 WHERE id=? AND application_id='' AND revision=? AND deleted=0`, c.ID, c.Revision)
		if err == nil {
			n, _ := res.RowsAffected()
			if n != 1 {
				err = fmt.Errorf("场景已变化")
			}
		}
		if err != nil {
			fail(w, 409, err)
			return
		}
		wj(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/api-testing/scenario-cases/validate", func(w http.ResponseWriter, r *http.Request) {
		var c apiTestCase
		if !decode(w, r, &c) {
			return
		}
		if c.ID > 0 {
			old, err := st.apiTestCase("", actor(r), c.ID)
			if err != nil || !caseCanEdit(old, actor(r), admin(r)) || old.Revision != c.Revision {
				fail(w, 409, "场景已变化或无权编辑")
				return
			}
		} else if c.Revision != 0 {
			fail(w, 409, "新场景修订无效")
			return
		}
		if strings.TrimSpace(c.Name) == "" || len(c.Name) > 200 {
			fail(w, 400, "请填写用例名称")
			return
		}
		if err := st.validateScenario(&c); err != nil {
			fail(w, 400, err)
			return
		}
		c.Owner = ""
		c.CanEdit = false
		wj(w, map[string]any{"valid": true, "case": c, "sends_requests": false})
	})
	mux.HandleFunc("GET /api/api-testing/scenario-cases/{id}/context", func(w http.ResponseWriter, r *http.Request) {
		c := getCase(w, r)
		if c == nil {
			return
		}
		value, err := st.scenarioContext(c, r.URL.Query().Get("environment"))
		if err != nil {
			fail(w, 409, err)
			return
		}
		wj(w, value)
	})
	mux.HandleFunc("POST /api/api-testing/scenario-runs", func(w http.ResponseWriter, r *http.Request) {
		var in apiScenarioInput
		if !decode(w, r, &in) {
			return
		}
		v, err := st.apiTests.startScenario(actor(r), admin(r), in)
		if err != nil {
			fail(w, 409, err)
			return
		}
		wj(w, v)
	})
	mux.HandleFunc("GET /api/api-testing/scenario-runs", func(w http.ResponseWriter, r *http.Request) {
		if err := st.apiTests.history(w, r, "", actor(r), wj); err != nil {
			fail(w, 400, err)
		}
	})
	mux.HandleFunc("GET /api/api-testing/scenario-runs/{run}", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("run"), 10, 64)
		v, err := st.apiTests.load("", actor(r), id)
		if err != nil {
			fail(w, 404, err)
			return
		}
		wj(w, v)
	})
	mux.HandleFunc("POST /api/api-testing/scenario-runs/{run}/stop", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("run"), 10, 64)
		if _, err := st.apiTests.load("", actor(r), id); err != nil {
			fail(w, 404, err)
			return
		}
		st.apiTests.mu.Lock()
		cancel := st.apiTests.jobs[id]
		st.apiTests.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		wj(w, map[string]bool{"stop_requested": true})
	})
}
