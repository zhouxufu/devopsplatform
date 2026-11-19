package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Opt-in disposable local fixture. Database and Nacos targets are synthetic;
// only the temporary application's configuration can be saved. No execution route.
func TestResourceExperienceBrowserFixture(t *testing.T) {
	port := os.Getenv("RESOURCE_EXPERIENCE_FIXTURE_PORT")
	if port == "" {
		t.Skip("opt-in isolated resource browser fixture")
	}
	s, _, _ := deliveryFixture(t)
	discoveryMode := os.Getenv("RESOURCE_DISCOVERY_FIXTURE") == "1"
	for _, env := range []string{"dev", "test"} {
		if err := s.UpsertEnvironment(Environment{Name: env, DisplayName: map[string]string{"dev": "开发", "test": "测试"}[env]}); err != nil {
			t.Fatal(err)
		}
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.Error(w, "read only", 403)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("search") != "" {
			fmt.Fprint(w, `{"totalCount":2,"pageItems":[{"dataId":"app-dev.yaml","group":"DEV_GROUP"},{"dataId":"app-test.yaml","group":"TEST_GROUP"}]}`)
		} else {
			if discoveryMode {
				if r.URL.Query().Get("dataId") == "fixed.yaml" {
					fmt.Fprint(w, "common:\n  mysql:\n    hostname: 127.0.0.1\n    username: fixture\n    password: fixture\njobs:\n  chosen:\n    mysql:\n      database: chosen_test\n  other:\n    mysql:\n      database: other_database\n")
					return
				}
				if r.URL.Query().Get("dataId") != "runtime-orders-test.yaml" {
					http.NotFound(w, r)
					return
				}
				fmt.Fprint(w, discoveryDatabase("dataIntelligenceMysql", "orders_test"))
				return
			}
			fmt.Fprint(w, "spring:\n  application:\n    name: orders\nfeature: true\n")
		}
	}))
	defer upstream.Close()
	for _, c := range []ResourceConnection{{Name: "开发数据库", Kind: "mysql", Endpoint: "127.0.0.1:1", Username: "fixture", Config: map[string]string{"tls": "false"}}, {Name: "测试数据库", Kind: "mysql", Endpoint: "127.0.0.1:1", Username: "fixture", Config: map[string]string{"tls": "false"}}, {Name: "开发配置", Kind: "nacos", Endpoint: upstream.URL, Config: map[string]string{"nacos_auth": "none", "namespace": "dev-space"}}, {Name: "测试配置", Kind: "nacos", Endpoint: upstream.URL, Config: map[string]string{"nacos_auth": "none", "namespace": "test-space"}}} {
		if err := s.UpsertConnection(c); err != nil {
			t.Fatal(err)
		}
	}
	if discoveryMode {
		svc := s.GetService("orders")
		svc.ArtifactGlob = "module/target/orders.jar"
		svc.GitURL = "https://code.example.invalid/orders.git"
		if err := s.UpsertService(*svc); err != nil {
			t.Fatal(err)
		}
		root := filepath.Join(s.dataDir, "workspace", "orders")
		os.MkdirAll(filepath.Join(root, "module/src/main/resources"), 0700)
		os.WriteFile(filepath.Join(root, "module/src/main/resources/bootstrap.yml"), []byte(discoverySource("runtime-orders", upstream.URL)), 0600)
		os.WriteFile(filepath.Join(root, "pom.xml"), []byte(discoveryProfiles()), 0600)
	}
	value := ApplicationResources{CompositionVersion: 2}
	for i := 0; i < 28; i++ {
		kind := "nacos"
		if i < 9 {
			kind = "mysql"
		}
		alias := fmt.Sprintf("resource_%02d", i)
		value.OwnRequirements = append(value.OwnRequirements, ApplicationResourceRequirement{Alias: alias, Kind: kind, Description: "合成资源，不连接业务系统"})
		for _, env := range []string{"dev", "test"} {
			if i == 8 && env == "dev" {
				continue
			}
			connection := map[string]string{"dev": "开发配置", "test": "测试配置"}[env]
			config := map[string]string{"data_id": alias + "-" + env + ".yaml", "group": strings.ToUpper(env) + "_GROUP"}
			if kind == "mysql" {
				connection = map[string]string{"dev": "开发数据库", "test": "测试数据库"}[env]
				config = map[string]string{"database": fmt.Sprintf("schema_%02d", i)}
			}
			value.OwnBindings = append(value.OwnBindings, ApplicationEnvironmentResource{Alias: alias, Environment: env, Connection: connection, Config: config})
		}
	}
	if discoveryMode {
		for _, state := range []string{"not_applicable", "pending_source", "missing_config"} {
			value.OwnRequirements = append(value.OwnRequirements, ApplicationResourceRequirement{Alias: state, Kind: "mysql", Sources: map[string]ResourceSourceEvidence{"test": {State: state, File: "module/bootstrap.yml"}}})
		}
		value.OwnRequirements[0].Sources = map[string]ResourceSourceEvidence{"test": {State: "not_applicable", File: "module/bootstrap.yml"}}
	}
	if err := s.SaveApplicationResources("orders", value); err != nil {
		t.Fatal(err)
	}
	hub := NewLogHub()
	routes := Routes(s, NewBuilder(s, s.dataDir, hub), hub, "", NewStepLib("../../steps"))
	files := http.FileServer(http.Dir("../../web/dist"))
	stop := make(chan struct{})
	var once sync.Once
	var fault atomic.Int32
	server := &http.Server{Addr: "127.0.0.1:" + port, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__fixture/stop" {
			once.Do(func() { close(stop) })
			fmt.Fprint(w, "stopped")
			return
		}
		if discoveryMode && r.URL.Path == "/__fixture/fault" {
			if r.URL.Query().Get("auth") == "1" {
				fault.Store(401)
			} else if r.URL.Query().Get("save") == "1" {
				fault.Store(409)
			} else {
				fault.Store(0)
			}
			fmt.Fprint(w, "ok")
			return
		}
		if discoveryMode && fault.Load() == 401 && strings.HasSuffix(r.URL.Path, "/resources/discover") {
			deliveryWriteError(w, 401, "模拟会话过期，请重新登录后重试；草稿应保留")
			return
		}
		if discoveryMode && fault.Load() == 409 && r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/resources") {
			deliveryWriteError(w, 409, "资源已被其他窗口修改，请核对差异")
			return
		}
		if r.URL.Path == "/__fixture/conflict" {
			v := s.ApplicationResources("orders")
			v.OwnBindings[0].Config = map[string]string{"database": "remote_changed"}
			if err := s.SaveApplicationResources("orders", v); err != nil {
				http.Error(w, err.Error(), 409)
				return
			}
			fmt.Fprint(w, "remote changed")
			return
		}
		if r.URL.Path == "/__fixture/state" {
			deliveryJSON(w, s.ApplicationResources("orders"))
			return
		}
		if r.URL.Path == "/api/me" {
			deliveryJSON(w, map[string]any{"username": "fixture-admin", "role": "admin", "auth_enabled": false})
			return
		}
		if r.URL.Path == "/api/plugins" {
			deliveryJSON(w, []any{})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/resources/check") {
			var req resourceCheckRequest
			json.NewDecoder(r.Body).Decode(&req)
			v := s.ApplicationResources("orders")
			if req.Resources != nil {
				v = *req.Resources
			}
			items, err := s.checkApplicationResources(r.Context(), "orders", "fixture-admin", v, req.Environments, nil, func(_ context.Context, kind string, b ApplicationEnvironmentResource) ResourceCheck {
				if b.Alias == "resource_00" {
					return ResourceCheck{Status: "failed", Code: "database_missing", Message: "目标数据库不存在（1049）", Suggestion: "核对 schema 名称"}
				}
				if b.Alias == "resource_01" {
					return ResourceCheck{Status: "failed", Code: "connection_refused", Message: "目标端口拒绝连接", Suggestion: "检查服务监听与网络"}
				}
				return ResourceCheck{Status: "passed", Code: "target_readable", Message: "只读目标核验通过，未验证写权限"}
			})
			if err != nil {
				deliveryWriteError(w, 400, err)
				return
			}
			counts := map[string]int{}
			for _, c := range items {
				counts[c.Status]++
			}
			deliveryJSON(w, map[string]any{"items": items, "counts": counts, "permission_verified": false})
			return
		}
		if r.Method != "GET" && !(r.Method == "PUT" && r.URL.Path == "/api/services/orders/resources") && !(r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/resources/discover")) {
			http.Error(w, "fixture blocks execution", 403)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			r.Header.Set("X-User", "fixture-admin")
			r.Header.Set("X-Role", "admin")
			routes.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/" {
			r.URL.Path = "/deploy.html"
		}
		files.ServeHTTP(w, r)
	})}
	ln, err := net.Listen("tcp", server.Addr)
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve(ln)
	defer server.Close()
	t.Log("isolated resource fixture http://" + server.Addr + "/deploy.html#/svc/orders/overview")
	select {
	case <-stop:
	case <-time.After(25 * time.Minute):
		t.Error("fixture timed out")
	}
}
