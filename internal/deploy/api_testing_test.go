package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"logplatform/pkg/apitest"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type apiTestRoundTrip func(*http.Request) (*http.Response, error)

func (f apiTestRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func apiTestingFixture(t *testing.T) (*Store, *http.ServeMux) {
	t.Helper()
	st, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { st.Close() })
	for _, name := range []string{"orders", "other"} {
		if e = st.UpsertService(Service{Name: name, ArtifactType: "jar"}); e != nil {
			t.Fatal(e)
		}
	}
	for _, env := range []Environment{{Name: "test", DisplayName: "测试环境"}, {Name: "prod", DisplayName: "生产环境", Protected: true, Approvers: "fixture-admin"}} {
		if e = st.UpsertEnvironment(env); e != nil {
			t.Fatal(e)
		}
		if e = st.UpsertHost(Host{Name: env.Name + "-host", Environment: env.Name, Addr: "192.0.2.55:22", User: "fixture"}); e != nil {
			t.Fatal(e)
		}
		if e = st.UpsertHostGroup(HostGroup{Name: env.Name + "-group", Environment: env.Name, Hosts: []string{env.Name + "-host"}}); e != nil {
			t.Fatal(e)
		}
		if e = st.UpsertServiceEnvironment(ServiceEnvironment{HostGroup: env.Name + "-group", Service: "orders", Environment: env.Name, TargetType: "ssh-jar", RemoteDir: "/srv/orders", StartCmd: "java -jar app.jar", DeployConfig: map[string]string{"java_port": "8080"}, RuntimeEnv: map[string]string{"API_PASSWORD": "fixture-password"}}); e != nil {
			t.Fatal(e)
		}
	}
	sv := st.GetService("orders")
	for _, env := range []string{"test", "prod"} {
		if e = st.saveAPITestingProfile(sv.ApplicationID, env, apiTestProfile{Enabled: true, BaseURL: "http://192.0.2.55:8080", Auth: apitest.Auth{Mode: "none"}}); e != nil {
			t.Fatal(e)
		}
	}
	h := Routes(st, NewBuilder(st, st.dataDir, NewLogHub()), NewLogHub(), "", NewStepLib("../../steps"))
	return st, h
}
func apiTestingRequest(h http.Handler, method, path, user, role string, input any) *httptest.ResponseRecorder {
	var raw []byte
	if input != nil {
		raw, _ = json.Marshal(input)
	}
	r := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
	r.Header.Set("X-User", user)
	r.Header.Set("X-Role", role)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func apiTestingInput(t *testing.T, st *Store, key string) apiTestInput {
	t.Helper()
	sv := st.GetService("orders")
	env := st.GetServiceEnvironment("orders", "test")
	p, _ := st.apiTestingProfile(sv.ApplicationID, "test")
	targets := st.apiTestTargets(sv, env, p)
	if len(targets) == 0 {
		t.Fatal("no target")
	}
	return apiTestInput{RequestID: key, Name: "合成测试", Environment: "test", TargetKey: targets[0].Key, TargetFingerprint: targets[0].Fingerprint, Request: apitest.Request{Method: "POST", Path: "/orders?access_token=private-query", Body: `{"password":"{{var.API_PASSWORD}}"}`, Assertions: []apitest.Assertion{{Type: "status", Expected: "200"}, {Type: "json", Path: "/ok", Expected: "true"}}}}
}
func apiTestingClient(server *httptest.Server) *http.Client {
	return &http.Client{Transport: apiTestRoundTrip(func(r *http.Request) (*http.Response, error) {
		copy := r.Clone(r.Context())
		u, _ := url.Parse(server.URL + r.URL.RequestURI())
		copy.URL = u
		return http.DefaultTransport.RoundTrip(copy)
	}), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func waitAPITest(t *testing.T, st *Store, owner string, id int64) *apiTestRun {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		v, e := st.apiTests.load(st.GetService("orders").ApplicationID, owner, id)
		if e != nil {
			t.Fatal(e)
		}
		if v.Status != "running" && v.Status != "queued" {
			return v
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("test job did not finish")
	return nil
}
func TestAPITestingRoutesIdempotencyIsolationAndEncryption(t *testing.T) {
	st, h := apiTestingFixture(t)
	var calls atomic.Int32
	gate := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/orders" {
			t.Error(r.URL)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "fixture-password") {
			t.Errorf("bound variable missing: %s", body)
		}
		<-gate
		fmt.Fprint(w, `{"ok":true,"password":"fixture-password"}`)
	}))
	defer server.Close()
	st.apiTests.client = apiTestingClient(server)
	in := apiTestingInput(t, st, "fixture-key-000001")
	w := apiTestingRequest(h, "POST", "/api/api-testing/runs?service=orders", "alice", "member", in)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var run apiTestRun
	json.Unmarshal(w.Body.Bytes(), &run)
	duplicate := apiTestingRequest(h, "POST", "/api/api-testing/runs?service=orders", "alice", "member", in)
	var again apiTestRun
	json.Unmarshal(duplicate.Body.Bytes(), &again)
	if duplicate.Code != 200 || again.ID != run.ID {
		t.Fatal(duplicate.Body.String())
	}
	changed := in
	changed.Name = "changed"
	if v := apiTestingRequest(h, "POST", "/api/api-testing/runs?service=orders", "alice", "member", changed); v.Code != 409 {
		t.Fatal(v.Code)
	}
	if v := apiTestingRequest(h, "GET", fmt.Sprintf("/api/api-testing/runs/%d?service=orders", run.ID), "bob", "member", nil); v.Code != 404 {
		t.Fatal("cross-user read", v.Code)
	}
	if v := apiTestingRequest(h, "GET", fmt.Sprintf("/api/api-testing/runs/%d?service=other", run.ID), "alice", "member", nil); v.Code != 404 {
		t.Fatal("cross-app read", v.Code)
	}
	close(gate)
	finished := waitAPITest(t, st, "alice", run.ID)
	if finished.Status != "success" || calls.Load() != 1 || !strings.Contains(finished.Result.Body, "[REDACTED]") {
		t.Fatal(finished, calls.Load())
	}
	if strings.Contains(finished.Path, "private-query") {
		t.Fatal("query credential leaked")
	}
	var encrypted string
	if e := st.db.QueryRow(`SELECT document FROM api_test_runs WHERE id=?`, run.ID).Scan(&encrypted); e != nil || !strings.HasPrefix(encrypted, "enc:v1:") || strings.Contains(encrypted, "fixture-password") {
		t.Fatal(encrypted, e)
	}
}
func TestAPITestProfileCaseRevisionAndProtectedEnvironment(t *testing.T) {
	st, h := apiTestingFixture(t)
	sv := st.GetService("orders")
	if w := apiTestingRequest(h, "GET", "/api/api-testing/profile?service=orders&environment=test", "local", "member", nil); w.Code != 403 {
		t.Fatal(w.Code)
	}
	p, _ := st.apiTestingProfile(sv.ApplicationID, "test")
	if e := st.saveAPITestingProfile(sv.ApplicationID, "test", p); e != nil {
		t.Fatal(e)
	}
	if e := st.saveAPITestingProfile(sv.ApplicationID, "test", p); e == nil {
		t.Fatal("stale profile accepted")
	}
	c := apiTestCase{Name: "检查订单", Request: apitest.Request{Path: "/orders"}}
	w := apiTestingRequest(h, "POST", "/api/api-testing/cases?service=orders", "alice", "member", c)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &c)
	old := c
	c.Name = "更新"
	w = apiTestingRequest(h, "PUT", fmt.Sprintf("/api/api-testing/cases/%d?service=orders", c.ID), "alice", "member", c)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w = apiTestingRequest(h, "PUT", fmt.Sprintf("/api/api-testing/cases/%d?service=orders", c.ID), "alice", "member", old); w.Code != 409 {
		t.Fatal("stale case", w.Code)
	}
	if w = apiTestingRequest(h, "GET", "/api/api-testing/cases?service=orders", "bob", "member", nil); w.Body.String() != "[]\n" {
		t.Fatal(w.Body.String())
	}
	in := apiTestingInput(t, st, "protected-key-00001")
	in.Environment = "prod"
	env := st.GetServiceEnvironment("orders", "prod")
	p, _ = st.apiTestingProfile(sv.ApplicationID, "prod")
	in.TargetFingerprint = st.apiTestTargets(sv, env, p)[0].Fingerprint
	for _, role := range []string{"member", "admin"} {
		w = apiTestingRequest(h, "POST", "/api/api-testing/runs?service=orders", "alice", role, in)
		if w.Code != 409 {
			t.Fatal("protected guard", role, w.Code)
		}
	}
	in.ConfirmProtected = true
	in.Reason = "合成验证"
	w = apiTestingRequest(h, "POST", "/api/api-testing/runs?service=orders", "alice", "member", in)
	if w.Code != 409 {
		t.Fatal("member bypassed protected guard")
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, `{"ok":true}`) }))
	defer server.Close()
	st.apiTests.client = apiTestingClient(server)
	w = apiTestingRequest(h, "POST", "/api/api-testing/runs?service=orders", "alice", "admin", in)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var run apiTestRun
	json.Unmarshal(w.Body.Bytes(), &run)
	if v := waitAPITest(t, st, "alice", run.ID); v.Status != "success" || calls.Load() != 1 {
		t.Fatal(v, calls.Load())
	}
}
func TestAPITestTargetChangesDocumentScopeAndStoppedStream(t *testing.T) {
	st, h := apiTestingFixture(t)
	in := apiTestingInput(t, st, "target-key-0000001")
	in.TargetFingerprint = "stale"
	if w := apiTestingRequest(h, "POST", "/api/api-testing/runs?service=orders", "alice", "member", in); w.Code != 409 {
		t.Fatal(w.Code)
	}
	sv := st.GetService("orders")
	doc, e := st.recordAPIDocument(st.GetService("other"), APIDocumentSnapshot{Provider: "fixture"}, []byte(apiDocumentFixture))
	if e != nil {
		t.Fatal(e)
	}
	in = apiTestingInput(t, st, "target-key-0000002")
	in.DocumentID = doc
	if w := apiTestingRequest(h, "POST", "/api/api-testing/runs?service=orders", "alice", "member", in); w.Code != 409 {
		t.Fatal(w.Code)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"token\":\"fixture-stream-token\"}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	st.apiTests.client = apiTestingClient(server)
	in = apiTestingInput(t, st, "stream-key-0000001")
	in.Request.Mode = "sse"
	in.Request.Assertions = []apitest.Assertion{{Type: "sse_count", Expected: "1"}}
	w := apiTestingRequest(h, "POST", "/api/api-testing/runs?service=orders", "alice", "member", in)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var run apiTestRun
	json.Unmarshal(w.Body.Bytes(), &run)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		v, _ := st.apiTests.load(sv.ApplicationID, "alice", run.ID)
		if len(v.Result.Events) > 0 {
			if strings.Contains(v.Result.Events[0].Data, "fixture-stream-token") {
				t.Fatal("progress token leaked")
			}
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	w = apiTestingRequest(h, "POST", fmt.Sprintf("/api/api-testing/runs/%d/stop?service=orders", run.ID), "bob", "member", nil)
	if w.Code != 404 {
		t.Fatal("cross-owner stop")
	}
	w = apiTestingRequest(h, "POST", fmt.Sprintf("/api/api-testing/runs/%d/stop?service=orders", run.ID), "alice", "member", nil)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	v := waitAPITest(t, st, "alice", run.ID)
	if v.Status != "canceled" || calls.Load() != 1 {
		t.Fatal(v, calls.Load())
	}
}
func TestAPITestUnknownWriteIsNotResumed(t *testing.T) {
	st, _ := apiTestingFixture(t)
	var calls atomic.Int32
	st.apiTests.client = &http.Client{Transport: apiTestRoundTrip(func(r *http.Request) (*http.Response, error) { calls.Add(1); return nil, context.DeadlineExceeded })}
	in := apiTestingInput(t, st, "unknown-key-000001")
	sv := st.GetService("orders")
	v, e := st.apiTests.start(sv, "alice", false, in)
	if e != nil {
		t.Fatal(e)
	}
	result := waitAPITest(t, st, "alice", v.ID)
	if result.Status != "unknown" || !result.Result.OutcomeUnknown {
		t.Fatal(result)
	}
	again, e := st.apiTests.start(sv, "alice", false, in)
	if e != nil || again.ID != v.ID || calls.Load() != 1 {
		t.Fatal(again, e, calls.Load())
	}
	_, e = st.db.Exec(`UPDATE api_test_runs SET status='running' WHERE id=?`, v.ID)
	if e != nil {
		t.Fatal(e)
	}
	recovered := newAPITestManager(st)
	result, e = recovered.load(sv.ApplicationID, "alice", v.ID)
	if e != nil || result.Status != "unknown" || calls.Load() != 1 {
		t.Fatal(result, e)
	}
}

func TestAPITestTargetsReuseHostGroupsAndPublishedPorts(t *testing.T) {
	st, _ := apiTestingFixture(t)
	sv := st.GetService("orders")
	env := st.GetServiceEnvironment("orders", "test")
	targets := st.apiTestTargets(sv, env, apiTestProfile{})
	if len(targets) != 1 || targets[0].Host != "test-host" || targets[0].BaseURL != "http://192.0.2.55:8080" {
		t.Fatal(targets)
	}
	env.TargetType = "docker-host"
	env.HealthURL = "http://127.0.0.1:8080/health"
	env.DeployConfig = map[string]string{"ports": "9090:8080,5353:53/udp"}
	targets = st.apiTestTargets(sv, env, apiTestProfile{})
	if len(targets) != 1 || targets[0].BaseURL != "http://192.0.2.55:9090" {
		t.Fatal("must use published TCP port, not container/UDP port", targets)
	}
}

func TestAPICaseStepsSnapshotAndRecordOrigins(t *testing.T) {
	st, h := apiTestingFixture(t)
	var paths []string
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()
	st.apiTests.client = apiTestingClient(server)
	step := func(name, path string) apiTestStep {
		return apiTestStep{Name: name, Action: "按给定数据请求", Expected: "返回成功", Request: apitest.Request{Method: "GET", Path: path, Assertions: []apitest.Assertion{{Type: "status", Expected: "200"}}}}
	}
	c := apiTestCase{Name: "订单场景", Module: "订单管理", Priority: "P1", Preconditions: "订单已存在", TestData: "订单 7", Steps: []apiTestStep{step("查询订单", "/first"), step("核对状态", "/second")}}
	w := apiTestingRequest(h, "POST", "/api/api-testing/cases?service=orders", "alice", "member", c)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &c)
	in := apiTestingInput(t, st, "case-sequence-00001")
	in.CaseID = c.ID
	in.CaseRevision = c.Revision
	in.Request.Path = "/client-draft-must-not-execute"
	in.DocumentID = 9999
	w = apiTestingRequest(h, "POST", "/api/api-testing/runs?service=orders", "alice", "member", in)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var queued apiTestRun
	json.Unmarshal(w.Body.Bytes(), &queued)
	run := waitAPITest(t, st, "alice", queued.ID)
	if run.Status != "success" || run.Source != "case" || len(run.Steps) != 2 || run.Steps[1].Status != "success" || run.CaseSnapshot.Module != "订单管理" {
		t.Fatalf("bad case report: %+v", run)
	}
	mu.Lock()
	got := strings.Join(paths, ",")
	mu.Unlock()
	if got != "/first,/second" {
		t.Fatal(got)
	}
	c.Name = "后来修改的用例"
	c.Steps[0].Expected = "新预期"
	w = apiTestingRequest(h, "PUT", fmt.Sprintf("/api/api-testing/cases/%d?service=orders", c.ID), "alice", "member", c)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	run, _ = st.apiTests.load(st.GetService("orders").ApplicationID, "alice", run.ID)
	if run.CaseSnapshot.Name != "订单场景" || run.Steps[0].Expected != "返回成功" || run.CaseRevision != 1 {
		t.Fatal("frozen case changed")
	}
	in.RequestID = "stale-case-0000001"
	if w = apiTestingRequest(h, "POST", "/api/api-testing/runs?service=orders", "alice", "member", in); w.Code != 409 {
		t.Fatal("stale case accepted")
	}
	// Manual execution of the same request remains manual, even if a case with it exists.
	manual := apiTestingInput(t, st, "manual-sequence-01")
	w = apiTestingRequest(h, "POST", "/api/api-testing/runs?service=orders", "alice", "member", manual)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &queued)
	waitAPITest(t, st, "alice", queued.ID)
	for _, source := range []string{"manual", "case"} {
		w = apiTestingRequest(h, "GET", "/api/api-testing/runs?service=orders&source="+source, "alice", "member", nil)
		var list []map[string]any
		json.Unmarshal(w.Body.Bytes(), &list)
		if len(list) != 1 || list[0]["source"] != source {
			t.Fatal(w.Body.String())
		}
	}
	// Deleting a case does not delete or reclassify its frozen execution record.
	apiTestingRequest(h, "DELETE", fmt.Sprintf("/api/api-testing/cases/%d?service=orders", c.ID), "alice", "member", nil)
	w = apiTestingRequest(h, "GET", "/api/api-testing/runs?service=orders&source=case", "alice", "member", nil)
	if !strings.Contains(w.Body.String(), `"case_revision":1`) {
		t.Fatal(w.Body.String())
	}
}

func TestAPICaseFailureAndCancellationSkipRemainingSteps(t *testing.T) {
	for _, mode := range []string{"failed", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			st, h := apiTestingFixture(t)
			var calls atomic.Int32
			entered := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if mode == "cancel" {
					close(entered)
					<-r.Context().Done()
					return
				}
				w.WriteHeader(500)
			}))
			defer server.Close()
			st.apiTests.client = apiTestingClient(server)
			c := apiTestCase{Name: "中止场景", Steps: []apiTestStep{{Name: "第一步", Request: apitest.Request{Path: "/first"}}, {Name: "第二步", Request: apitest.Request{Path: "/must-not-send"}}}}
			w := apiTestingRequest(h, "POST", "/api/api-testing/cases?service=orders", "alice", "member", c)
			json.Unmarshal(w.Body.Bytes(), &c)
			in := apiTestingInput(t, st, "stop-sequence-0001")
			in.CaseID = c.ID
			in.CaseRevision = c.Revision
			w = apiTestingRequest(h, "POST", "/api/api-testing/runs?service=orders", "alice", "member", in)
			if w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			var queued apiTestRun
			json.Unmarshal(w.Body.Bytes(), &queued)
			if mode == "cancel" {
				select {
				case <-entered:
				case <-time.After(time.Second):
					t.Fatal("not started")
				}
				apiTestingRequest(h, "POST", fmt.Sprintf("/api/api-testing/runs/%d/stop?service=orders", queued.ID), "alice", "member", nil)
			}
			run := waitAPITest(t, st, "alice", queued.ID)
			if calls.Load() != 1 || run.Steps[1].Status != "skipped" || (mode == "failed" && run.Status != "failed") || (mode == "cancel" && run.Status != "canceled") {
				t.Fatalf("unexpected execution %+v calls=%d", run, calls.Load())
			}
		})
	}
}

func TestAPILegacyCasesNormalizeWithoutRevisionChange(t *testing.T) {
	st, h := apiTestingFixture(t)
	sv := st.GetService("orders")
	raw, _ := json.Marshal(apiTestCase{Name: "旧请求用例", Request: apitest.Request{Path: "/legacy"}})
	enc, _ := st.encryptSecret(string(raw))
	res, e := st.db.Exec(`INSERT INTO api_test_cases(application_id,owner,document,revision,deleted,updated_at) VALUES(?,?,?,7,0,?)`, sv.ApplicationID, "alice", enc, time.Now().Unix())
	if e != nil {
		t.Fatal(e)
	}
	id, _ := res.LastInsertId()
	c, e := st.apiTestCase(sv.ApplicationID, "alice", id)
	if e != nil || c.Revision != 7 || len(c.Steps) != 1 || c.Steps[0].Request.Path != "/legacy" {
		t.Fatalf("%+v %v", c, e)
	}
	w := apiTestingRequest(h, "GET", "/api/api-testing/runs?service=orders&source=other", "alice", "member", nil)
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
	c.Steps = make([]apiTestStep, 11)
	if validateAPITestCase(c) == nil {
		t.Fatal("unbounded steps")
	}
}

func TestAPIRecordOriginFilterBeforeLimitAndRecoveredStepStates(t *testing.T) {
	st, h := apiTestingFixture(t)
	sv := st.GetService("orders")
	for i := 0; i < 102; i++ {
		v := apiTestRun{Name: "历史", Owner: "alice", Status: "success", Service: "orders", StartedAt: time.Now().Unix()}
		if i == 0 {
			v.CaseID = 17
			v.CaseRevision = 3
			v.Status = "running"
			v.Steps = []apiTestStepResult{{Name: "已开始", Status: "running"}, {Name: "未开始", Status: "pending"}}
		}
		raw, _ := json.Marshal(v)
		enc, _ := st.encryptSecret(string(raw))
		_, e := st.db.Exec(`INSERT INTO api_test_runs(application_id,service,owner,request_key,request_digest,document,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, sv.ApplicationID, "orders", "alice", fmt.Sprintf("old-origin-%d", i), "digest", enc, v.Status, v.StartedAt, v.StartedAt)
		if e != nil {
			t.Fatal(e)
		}
	}
	w := apiTestingRequest(h, "GET", "/api/api-testing/runs?service=orders&source=case", "alice", "member", nil)
	var list []map[string]any
	json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) != 1 || list[0]["case_id"] != float64(17) {
		t.Fatal("older case record lost behind manual limit", w.Body.String())
	}
	_, e := st.db.Exec(`UPDATE api_test_runs SET status='unknown' WHERE status='running'`)
	if e != nil {
		t.Fatal(e)
	}
	run, e := st.apiTests.load(sv.ApplicationID, "alice", int64(list[0]["id"].(float64)))
	if e != nil || run.Steps[0].Status != "unknown" || run.Steps[1].Status != "skipped" {
		t.Fatalf("%+v %v", run, e)
	}
}

func TestAPICaseResponseVariableChainAndNoPersistedValues(t *testing.T) {
	st, h := apiTestingFixture(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		switch r.URL.Path {
		case "/create":
			fmt.Fprintf(w, `{"id":"private-order-%d","number":9007199254740993}`, n)
		default:
			id := strings.TrimPrefix(r.URL.Path, "/orders/")
			if !strings.HasPrefix(id, "private-order-") || r.Header.Get("X-Order") != id {
				t.Error("variable was not passed", r.URL.Path, r.Header)
			}
			raw, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(raw), `"number":9007199254740993`) {
				t.Error("numeric type lost", string(raw))
			}
			fmt.Fprintf(w, `{"id":%q}`, id)
		}
	}))
	defer server.Close()
	st.apiTests.client = apiTestingClient(server)
	c := apiTestCase{Name: "创建后查询", Steps: []apiTestStep{
		{Name: "创建", Request: apitest.Request{Method: "POST", Path: "/create"}, Extractions: []apitest.Extraction{{Name: "id", Source: "json", Path: "/id"}, {Name: "number", Source: "json", Path: "/number"}}},
		{Name: "查询", Request: apitest.Request{Method: "POST", Path: "/orders/{{step.id}}", Headers: map[string]string{"X-Order": "{{step.id}}"}, Body: `{"number":"{{step.number}}"}`, Assertions: []apitest.Assertion{{Type: "json", Path: "/id", Expected: "{{step.id}}"}}}},
	}}
	w := apiTestingRequest(h, "POST", "/api/api-testing/cases?service=orders", "alice", "member", c)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &c)
	for n := 0; n < 2; n++ {
		in := apiTestingInput(t, st, fmt.Sprintf("chain-scope-run-%d", n))
		in.CaseID = c.ID
		in.CaseRevision = c.Revision
		w = apiTestingRequest(h, "POST", "/api/api-testing/runs?service=orders", "alice", "member", in)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		var queued apiTestRun
		json.Unmarshal(w.Body.Bytes(), &queued)
		run := waitAPITest(t, st, "alice", queued.ID)
		if run.Status != "success" || run.Steps[0].Extractions[0].Status != "success" || len(run.CaseSnapshot.Steps[0].Extractions) != 2 {
			t.Fatalf("%+v", run)
		}
		raw, _ := json.Marshal(run)
		if strings.Contains(string(raw), "private-order-") || strings.Contains(string(raw), "9007199254740993") {
			t.Fatalf("values reached persisted report %s", raw)
		}
		// Exact same request key resolves the original record and never sends another request.
		before := calls.Load()
		w = apiTestingRequest(h, "POST", "/api/api-testing/runs?service=orders", "alice", "member", in)
		if w.Code != 200 || calls.Load() != before {
			t.Fatal("replayed writes")
		}
	}
	if calls.Load() != 4 {
		t.Fatal(calls.Load())
	}
	c.ID = 0
	c.Revision = 0
	c.Steps[0].Extractions[0].Path = "/absent"
	w = apiTestingRequest(h, "POST", "/api/api-testing/cases?service=orders", "alice", "member", c)
	json.Unmarshal(w.Body.Bytes(), &c)
	in := apiTestingInput(t, st, "chain-missing-stop")
	in.CaseID = c.ID
	in.CaseRevision = c.Revision
	w = apiTestingRequest(h, "POST", "/api/api-testing/runs?service=orders", "alice", "member", in)
	var queued apiTestRun
	json.Unmarshal(w.Body.Bytes(), &queued)
	run := waitAPITest(t, st, "alice", queued.ID)
	if run.Status != "failed" || run.Steps[1].Status != "skipped" || calls.Load() != 5 {
		t.Fatal(run, calls.Load())
	}
	c.Steps[0].Request.Path = "/{{step.id}}"
	if validateAPITestCase(&c) == nil {
		t.Fatal("self reference accepted")
	}
	c.Steps[0].Request.Path = "/"
	c.Steps[1].Extractions = []apitest.Extraction{{Name: "id", Source: "json", Path: "/id"}}
	if validateAPITestCase(&c) == nil {
		t.Fatal("duplicate variable accepted")
	}
}
