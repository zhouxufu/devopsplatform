package deploy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Opt-in real editor + disposable metadata transaction. Every execution route
// and external resource endpoint is blocked, including branch creation.
func TestDeliveryEntryBrowserFixture(t *testing.T) {
	port := os.Getenv("DELIVERY_ENTRY_FIXTURE_PORT")
	if port == "" {
		t.Skip("opt-in entry browser fixture")
	}
	s, _, group := resourceGroupFixture(t)
	if err := s.SaveApplicationResources("orders", ApplicationResources{CompositionVersion: 2, ResourceGroups: []string{group.Name}, GroupRevisions: map[string]int64{group.Name: group.Revision}, OwnRequirements: []ApplicationResourceRequirement{{Alias: "config", Kind: "nacos"}}}); err != nil {
		t.Fatal(err)
	}
	w := &WorkItem{Title: "菜单修复 · 合成验收", Kind: "bug", Source: "manual", Owner: "alice"}
	if err := s.CreateWorkItem(w); err != nil {
		t.Fatal(err)
	}
	base, _ := s.WorkDelivery(w.ID)
	req := WorkDeliverySave{Digest: base.Digest, Changes: []Change{{Kind: "code", Title: "菜单代码", Service: "orders", Reference: "feature/menu"}, {Kind: "mysql", Title: "菜单数据", Service: "orders", SQL: &SQLChange{ResourceAlias: "db", Content: "UPDATE menu SET enabled=1 WHERE id=1", MaxAffectedRows: 1}}}}
	if os.Getenv("DELIVERY_OVERVIEW_FIXTURE") == "1" {
		if err := s.UpsertService(Service{Name: "billing", DisplayName: "账务服务"}); err != nil {
			t.Fatal(err)
		}
		req.Changes = append(req.Changes,
			Change{Kind: "code", Title: "账务适配", Service: "billing", Reference: "feature/menu", Summary: "同分支名的另一应用，不能合并为同一变更"},
			Change{Kind: "nacos", Title: "认领功能开关", Service: "orders", Nacos: &NacosChange{ResourceAlias: "config", DataID: "orders-test.yml", Group: "DEFAULT_GROUP", Format: "yaml", Entries: []NacosEntry{{Key: "owner.claim.enabled", Action: "set", Value: "true"}, {Key: "owner.claim.note", Action: "set", Value: "<img src=x onerror=alert(1)>\n第二行"}, {Key: "owner.claim.old", Action: "delete"}}}},
			Change{Kind: "manual", Title: "XXL 人工核对", Reference: "核对任务配置与调度日志，填写实际回执", RollbackPlan: "恢复原任务配置；此处只是草稿说明"})
	}
	if err := s.SaveWorkDelivery(w.ID, &req, "alice", false); err != nil {
		t.Fatal(err)
	}
	hub := NewLogHub()
	routes := Routes(s, NewBuilder(s, s.dataDir, hub), hub, "", NewStepLib("../../steps"))
	files := http.FileServer(http.Dir("../../web/dist"))
	stop := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	fault := ""
	handler := http.HandlerFunc(func(out http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__fixture/stop" {
			once.Do(func() { close(stop) })
			fmt.Fprint(out, "stopped")
			return
		}
		if r.URL.Path == "/__fixture/state" {
			data, _ := s.WorkDelivery(w.ID)
			deliveryJSON(out, data)
			return
		}
		if r.URL.Path == "/__fixture/fault" {
			mu.Lock()
			fault = r.URL.Query().Get("kind")
			mu.Unlock()
			fmt.Fprint(out, "set")
			return
		}
		r.Header.Set("X-User", "alice")
		r.Header.Set("X-Role", "member")
		if strings.HasPrefix(r.URL.Path, "/api/plugins/research/api/") {
			r.URL.Path = strings.TrimPrefix(r.URL.Path, "/api/plugins/research")
		}
		if strings.HasPrefix(r.URL.Path, "/api/resources/") {
			r.URL.Path = "/api/" + strings.TrimPrefix(r.URL.Path, "/api/resources/")
		}
		if r.URL.Path == "/api/plugins/research/ui" {
			r.URL.Path = "/research.html"
			files.ServeHTTP(out, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/plugins/research/assets/") {
			r.URL.Path = "/assets/" + strings.TrimPrefix(r.URL.Path, "/api/plugins/research/assets/")
			files.ServeHTTP(out, r)
			return
		}
		switch r.URL.Path {
		case "/api/me":
			deliveryJSON(out, map[string]any{"username": "alice", "display_name": "验收开发", "role": "member", "auth_enabled": true})
			return
		case "/api/research/overview":
			deliveryJSON(out, map[string]any{"actor": "alice", "connections": []any{}, "people": map[string]string{"alice": "验收开发", "bob": "验收测试"}, "assignees": map[string]string{"alice": "验收开发", "bob": "验收测试"}})
			return
		case fmt.Sprintf("/api/research/work-items/%d", w.ID):
			activity, _ := s.ResearchActivities(w.ID)
			deliveryJSON(out, map[string]any{"item": s.GetWorkItem(w.ID), "activity": activity})
			return
		case fmt.Sprintf("/api/research/work-items/%d/delivery-records", w.ID):
			deliveryJSON(out, map[string]any{"items": []any{}, "has_more": false})
			return
		case "/api/git/branches":
			deliveryJSON(out, map[string]any{"branches": []any{map[string]string{"name": "feature/menu"}, map[string]string{"name": "main"}}})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			r.Header.Set("X-Reviewer-Directory", `["alice","bob"]`)
			safe := r.Method == "GET" && (r.URL.Path == "/api/services" || r.URL.Path == "/api/environments" || strings.HasSuffix(r.URL.Path, "/resources") || strings.HasSuffix(r.URL.Path, "/delivery-content"))
			safe = safe || r.Method == "GET" && (strings.HasPrefix(r.URL.Path, "/api/release-versions") || strings.HasPrefix(r.URL.Path, "/api/work-items") || r.URL.Path == "/api/changes")
			safe = safe || r.Method == "GET" && (strings.HasPrefix(r.URL.Path, "/api/research/work-items/") || strings.HasPrefix(r.URL.Path, "/api/changes/"))
			write := r.Method == "PUT" && r.URL.Path == fmt.Sprintf("/api/work-items/%d/delivery-content", w.ID)
			write = write || r.Method == "POST" && (r.URL.Path == "/api/work-items" || r.URL.Path == "/api/changes") || r.Method == "PUT" && (strings.HasPrefix(r.URL.Path, "/api/work-items/") && !strings.HasSuffix(r.URL.Path, "/delivery-content") || strings.HasPrefix(r.URL.Path, "/api/changes/"))
			write = write || r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/api/changes/") && strings.HasSuffix(r.URL.Path, "/ready")
			reopen := r.Method == "POST" && (r.URL.Path == fmt.Sprintf("/api/changes/%d/reopen", req.Changes[0].ID) || r.URL.Path == fmt.Sprintf("/api/changes/%d/reopen", req.Changes[1].ID))
			write = write || r.Method == "PATCH" && r.URL.Path == fmt.Sprintf("/api/work-items/%d/status", w.ID)
			if write {
				mu.Lock()
				f := fault
				mu.Unlock()
				if f == "expired" {
					deliveryWriteError(out, 401, "登录已过期，草稿保留")
					return
				}
				if f == "conflict" {
					deliveryWriteError(out, 409, "交付内容已被其他操作修改；草稿已保留，请重新载入后核对")
					return
				}
			}
			if safe || write || reopen {
				routes.ServeHTTP(out, r)
				return
			}
			if r.Method == "GET" {
				deliveryJSON(out, []any{})
				return
			}
			http.Error(out, "fixture blocks execution", 403)
			return
		}
		files.ServeHTTP(out, r)
	})
	listener, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go server.Serve(listener)
	t.Logf("Entry fixture: http://127.0.0.1:%s/research.html#/work/%d?action=changes", port, w.ID)
	select {
	case <-stop:
	case <-time.After(25 * time.Minute):
		t.Error("fixture expired without explicit stop")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	server.Shutdown(ctx)
}
