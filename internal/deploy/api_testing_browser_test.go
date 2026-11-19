package deploy

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"logplatform/internal/smartdocplugin"
	"logplatform/pkg/apidoc"
	"logplatform/pkg/apitest"
	p "logplatform/pkg/extensionprotocol"
	"logplatform/pkg/workflownode"
)

// Opt-in local browser harness: real handlers/storage/HTTP/SSE engine, synthetic targets only.
func TestAPITestingBrowserFixture(t *testing.T) {
	if os.Getenv("API_TEST_BROWSER_FIXTURE") != "1" {
		t.Skip("opt-in isolated browser fixture")
	}
	st, h := apiTestingFixture(t)
	if os.Getenv("API_TEST_COLLECTION_FIXTURE") == "1" {
		c := saveTeamCase(t, h, "fixture-admin", "admin", apiTestCase{Name: "订单查询回归", Module: "订单/查询", Visibility: "team", Steps: []apiTestStep{{Name: "查询订单并校验成功", Request: apitest.Request{Path: "/orders/{{data.id}}", Assertions: []apitest.Assertion{{Type: "status", Expected: "200"}}}}}, DataRows: []apiTestDataRow{{Name: "已存在订单", Values: map[string]apiTestCell{"id": {Type: "string", Value: "7"}}}, {Name: "另一订单", Values: map[string]apiTestCell{"id": {Type: "string", Value: "8"}}}}})
		w := apiTestingRequest(h, "POST", "/api/api-testing/suites?service=orders", "fixture-admin", "admin", apiTestSuite{Name: "订单核心回归", Visibility: "team", CaseIDs: []int64{c.ID}})
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}

	if os.Getenv("API_TEST_GRAPH_FIXTURE") == "1" {
		c := graphCaseFixture()
		c.Name = "条件与并行回归"
		c.Visibility = "team"
		c.Graph.Entry = "condition"
		c.Graph.Edges = c.Graph.Edges[1:]
		c.Steps = c.Steps[1:]
		c.Steps[0].Condition.Variable = "data.run_parallel"
		for i := range c.Steps {
			if testNodeKind(c.Steps[i]) == "http" {
				c.Steps[i].Request.Path = fmt.Sprintf("/orders/%d", i+7)
				c.Steps[i].Extractions = nil
			}
		}
		c.DataRows = []apiTestDataRow{{Name: "走并行路径", Values: map[string]apiTestCell{"run_parallel": {Type: "boolean", Value: "true"}}}, {Name: "走单接口路径", Values: map[string]apiTestCell{"run_parallel": {Type: "boolean", Value: "false"}}}}
		saveTeamCase(t, h, "fixture-admin", "admin", c)
	}

	var logins, business, streams atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/login" {
			logins.Add(1)
			fmt.Fprint(w, `{"data":{"token":"fixture-auth-token"}}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer fixture-auth-token" {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"error":"missing fixture token"}`)
			return
		}
		if r.URL.Path == "/events" {
			streams.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			ticker := time.NewTicker(180 * time.Millisecond)
			defer ticker.Stop()
			for i := 1; i <= 100; i++ {
				select {
				case <-r.Context().Done():
					return
				case <-ticker.C:
					fmt.Fprintf(w, "id: %d\nevent: order.updated\ndata: {\"progress\":%d,\"token\":\"fixture-auth-token\"}\n\n", i, i)
					w.(http.Flusher).Flush()
				}
			}
			return
		}
		business.Add(1)
		if r.URL.Path == "/orders" && r.Method == "POST" {
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"ok":true,"data":{"id":"fixture-order-42"}}`)
			return
		}
		fmt.Fprintf(w, `{"ok":true,"data":{"id":"%s","status":"confirmed","large":9007199254740993123},"request_id":"%s","token":"fixture-auth-token"}`, strings.TrimPrefix(r.URL.Path, "/orders/"), r.Header.Get("X-Request-ID"))
	}))
	defer target.Close()
	st.apiTests.client = apiTestingClient(target)
	sv := st.GetService("orders")
	for _, env := range []string{"test", "prod"} {
		p, _ := st.apiTestingProfile(sv.ApplicationID, env)
		p.Auth = apitest.Auth{Mode: "login", Path: "/login", Body: `{"password":"{{var.API_PASSWORD}}"}`, TokenPointer: "/data/token", Header: "Authorization", Prefix: "Bearer "}
		if e := st.saveAPITestingProfile(sv.ApplicationID, env, p); e != nil {
			t.Fatal(e)
		}
	}
	if os.Getenv("API_TEST_SCENARIO_FIXTURE") == "1" {
		for _, env := range []string{"test", "prod"} {
			binding := *st.GetServiceEnvironment("orders", env)
			binding.Service = "other"
			binding.RuntimeEnv = map[string]string{"API_PASSWORD": "other-fixture-password"}
			if err := st.UpsertServiceEnvironment(binding); err != nil {
				t.Fatal(err)
			}
			profile, _ := st.apiTestingProfile(sv.ApplicationID, env)
			existing, _ := st.apiTestingProfile(st.GetService("other").ApplicationID, env)
			profile.Revision = existing.Revision
			if err := st.saveAPITestingProfile(st.GetService("other").ApplicationID, env, profile); err != nil {
				t.Fatal(err)
			}
		}
		c := apiTestCase{Name: "跨应用订单查询", Visibility: "team", Steps: []apiTestStep{
			{Name: "订单查询", Service: "orders", Request: apitest.Request{Path: "/orders/7", Assertions: []apitest.Assertion{{Type: "status", Expected: "200"}}}, Extractions: []apitest.Extraction{{Name: "order_id", Source: "json", Path: "/data/id"}}},
			{Name: "其他应用核对", Service: "other", Request: apitest.Request{Path: "/orders/{{step.order_id}}", Assertions: []apitest.Assertion{{Type: "json", Path: "/data/id", Expected: "7"}}}},
		}}
		w := apiTestingRequest(h, "POST", "/api/api-testing/scenario-cases", "fixture-admin", "admin", c)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}

	doc := `{"openapi":"3.0.3","info":{"title":"订单服务接口","version":"v1"},"paths":{"/orders/{id}":{"get":{"summary":"查询订单","responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object"}}}}},"parameters":[{"name":"q","in":"query","required":true,"schema":{"type":"string","default":"operation","enum":["operation","other"],"maxLength":30}}],"tags":["订单"]},"parameters":[{"name":"id","in":"path","required":true,"example":"7","schema":{"type":"string"}},{"name":"q","in":"query","schema":{"type":"string","default":"base"}}]},"/events":{"get":{"summary":"订单状态事件流","responses":{"200":{"description":"stream","content":{"text/event-stream":{"schema":{"type":"string"}}}}}}},"/orders":{"post":{"summary":"创建订单（201）","tags":["订单"],"requestBody":{"content":{"application/json":{"schema":{"type":"object","properties":{"title":{"type":"string","default":"fixture","maxLength":100}}},"example":{"title":"fixture"}}}},"responses":{"201":{"description":"created","content":{"application/json":{"schema":{"type":"object","properties":{"id":{"type":"string"}}}}}}}}}}}`
	if _, e := st.recordAPIDocument(sv, APIDocumentSnapshot{Provider: "fixture"}, []byte(doc)); e != nil {
		t.Fatal(e)
	}
	documentBrokerFixture(t)
	if e := st.UpsertEnvironment(Environment{Name: "dev", DisplayName: "开发环境", Purpose: "development"}); e != nil {
		t.Fatal(e)
	}
	documentRunner, documentService := documentPipelineFixture(t, st)
	source := apidoc.Source{Provider: "smart-doc", OnBuild: true, Settings: json.RawMessage(`{"plugin_version":"2.7.3","config_file":"smart-doc.json","output_file":"target/api-docs/openapi.json"}`)}
	for _, env := range []string{"dev", "test"} {
		if e := st.saveEnvironmentDocumentSource(documentService, env, source, "fixture-admin"); e != nil {
			t.Fatal(e)
		}
	}
	documentBefore := `{"openapi":"3.0.3","info":{"title":"订单服务","version":"v1"},"paths":{"/orders":{"post":{"summary":"创建订单","responses":{"201":{"description":"created","content":{"application/json":{"schema":{"$ref":"#/components/schemas/Order"}}}}}}},"/old":{"get":{"summary":"旧订单查询","responses":{"200":{"description":"ok"}}}}},"components":{"schemas":{"Order":{"type":"object","properties":{"id":{"type":"integer","description":"订单编号"}}}}}}`
	documentAfter := strings.Replace(documentBefore, `"type":"integer"`, `"type":"string"`, 1)
	var after map[string]any
	json.Unmarshal([]byte(documentAfter), &after)
	paths := after["paths"].(map[string]any)
	delete(paths, "/old")
	paths["/orders/{id}"] = map[string]any{"get": map[string]any{"summary": "按编号查询订单", "responses": map[string]any{"200": map[string]any{"description": "ok"}}}}
	rawAfter, _ := json.Marshal(after)
	executeDocumentFixture(t, documentRunner, documentService, "test", documentBefore, true)
	executeDocumentFixture(t, documentRunner, documentService, "test", string(rawAfter), true)
	executeDocumentFixture(t, documentRunner, documentService, "dev", documentBefore, true)
	if os.Getenv("API_DOC_GRADLE_FIXTURE") == "1" {
		v := *documentService
		v.BuildTool = "gradle"
		if e := st.UpsertService(v); e != nil {
			t.Fatal(e)
		}
		documentService = st.GetService(v.Name)
		for _, env := range []string{"dev", "test"} {
			config, _, _ := st.environmentDocumentSource(documentService, env)
			config.Settings = json.RawMessage(`{"config_file":"smart-doc.json","output_file":"build/api-docs/openapi.json","gradle_task":"smartDocOpenApi"}`)
			if e := st.saveEnvironmentDocumentSource(documentService, env, config, "fixture-admin"); e != nil {
				t.Fatal(e)
			}
		}
	}
	if os.Getenv("API_TEST_IMPACT_FIXTURE") == "1" {
		t.Setenv(workflownode.SharedWorkspacesEnv, "api-center,deploy")
		var base int64
		if e := st.db.QueryRow(`SELECT id FROM api_document_versions WHERE environment='test' ORDER BY id LIMIT 1`).Scan(&base); e != nil {
			t.Fatal(e)
		}
		for _, item := range []struct{ name, op, path string }{{"创建订单：文档类型变更", "post /orders", "/orders"}, {"旧查询：文档已移除", "get /old", "/old"}} {
			saveTeamCase(t, h, "fixture-admin", "admin", apiTestCase{Name: item.name, Visibility: "team", DocumentID: base, Steps: []apiTestStep{{Name: item.name, DocumentID: base, OperationID: item.op, Request: apitest.Request{Path: item.path, Assertions: []apitest.Assertion{{Type: "status", Expected: "200"}}}}}})
		}
		p, _ := st.apiTestingProfile(sv.ApplicationID, "test")
		p.RegressionSuiteID = 1
		p.RegressionTargetKey = "entry"
		if e := st.saveAPITestingProfile(sv.ApplicationID, "test", p); e != nil {
			t.Fatal(e)
		}
		var artifactID int64
		if e := st.db.QueryRow(`SELECT artifact_id FROM api_document_versions WHERE environment='test' ORDER BY id DESC LIMIT 1`).Scan(&artifactID); e != nil {
			t.Fatal(e)
		}
		a := st.GetArtifact(artifactID)
		if _, e := st.db.Exec(`INSERT INTO deploys(service,version,host,environment,artifact_id,artifact_digest,status,actor,started_at,ended_at) VALUES('orders',?,'test-host','test',?,?,'success','fixture',100,110)`, a.Version, a.ID, a.Digest); e != nil {
			t.Fatal(e)
		}
		tpl := &DeliveryTemplate{Name: "合成接口回归验收", Mode: "fixed", Services: []string{"orders"}, Nodes: []DeliveryNode{{ID: "deploy", Kind: "deploy", Name: "合成部署占位（不执行）"}, {ID: "test", Kind: "extension", Name: "接口中心回归", Extension: &workflownode.Binding{Provider: workflownode.APITestProvider, NodeKey: "regression"}}}}
		if e := st.SaveDeliveryTemplate(tpl); e != nil {
			t.Fatal(e)
		}
		run, e := st.CreateDeliveryRun(DeliveryRequest{TemplateID: tpl.ID, Environment: "test", Reason: "合成服务验收，禁止真实部署", Artifacts: map[string]int64{"orders": a.ID}}, "fixture-admin", NewReleaseManager(st, nil))
		if e != nil {
			t.Fatal(e)
		}
		run.Steps[0].Status = "success"
		if e = st.saveDeliveryRun(run); e != nil {
			t.Fatal(e)
		}
	}
	quit := make(chan struct{})
	var once sync.Once
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == "/api/workflow-node-providers" {
			d := workflownode.SharedDescriptor(workflownode.APITestProvider, os.Getenv(workflownode.SharedWorkspacesEnv))
			json.NewEncoder(w).Encode([]any{map[string]any{"id": workflownode.APITestProvider, "name": "接口中心", "state": "running", "nodes": []*workflownode.Descriptor{d}}})
			return
		}
		if r.URL.Path == "/api/extensions" {
			json.NewEncoder(w).Encode([]any{map[string]any{"manifest": map[string]any{"id": "smart-doc", "name": "Smart-doc 接口文档", "version": "1.0.0", "capabilities": []string{apidoc.Capability, "workflow.node.v1"}}, "state": "running"}})
			return
		}
		if r.URL.Path == "/api/extensions/smart-doc/invoke" {
			var in p.Request
			if json.NewDecoder(r.Body).Decode(&in) != nil {
				w.WriteHeader(400)
				return
			}
			in.Actor = p.Actor{User: "fixture-admin", Role: "admin"}
			raw, _ := json.Marshal(in)
			req := httptest.NewRequest("POST", "/v1/invoke", strings.NewReader(string(raw)))
			req.Header.Set("Authorization", "Bearer fixture-token")
			smartdocplugin.Handler("fixture-token").ServeHTTP(w, req)
			return
		}
		if r.URL.Path == "/api/me" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"username": "fixture-admin", "role": "admin", "auth_enabled": false})
			return
		}
		if r.URL.Path == "/fixture/state" {
			json.NewEncoder(w).Encode(map[string]any{"logins": logins.Load(), "business": business.Load(), "streams": streams.Load()})
			return
		}
		if r.Method == "POST" && r.URL.Path == "/fixture/quit" {
			once.Do(func() { close(quit) })
			return
		}
		r.Header.Set("X-User", "fixture-admin")
		r.Header.Set("X-Role", "admin")
		h.ServeHTTP(w, r)
	})
	listener, e := net.Listen("tcp", "127.0.0.1:19977")
	if e != nil {
		t.Fatal(e)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go server.Serve(listener)
	defer server.Close()
	fmt.Println("isolated API testing backend ready at 127.0.0.1:19977")
	select {
	case <-quit:
	case <-time.After(15 * time.Minute):
	}
}
