package deploy

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestDocumentSearchAcrossLatestApplicationEnvironments(t *testing.T) {
	st, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	for _, name := range []string{"orders", "payments", "removed"} {
		if e = st.UpsertService(Service{Name: name, ArtifactType: "jar", BuildTool: "maven", DisplayName: name + "显示名"}); e != nil {
			t.Fatal(e)
		}
	}
	for _, env := range []string{"dev", "test"} {
		if e = st.UpsertEnvironment(Environment{Name: env, DisplayName: env}); e != nil {
			t.Fatal(e)
		}
	}
	add := func(app, env, path, summary, tag, description string) int64 {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"openapi": "3.0.3", "info": map[string]string{"title": app, "version": summary}, "paths": map[string]any{path: map[string]any{"post": map[string]any{"summary": summary, "description": description, "x-maintainer": "fixture-maintainer", "tags": []string{tag}, "responses": map[string]any{"200": map[string]string{"description": "response-example-private"}}}}}})
		id, e := st.recordAPIDocument(st.GetService(app), APIDocumentSnapshot{Environment: env, Provider: "fixture"}, raw)
		if e != nil {
			t.Fatal(e)
		}
		return id
	}
	old := add("orders", "test", "/api/old", "已移除功能", "旧接口", "")
	current := add("orders", "test", "/api/orders", "创建订房订单", "订单", "可关联取消记录")
	add("orders", "dev", "/api/houses", "房源查询", "房源", "")
	payment := add("payments", "test", "/api/orders", "创建支付订单", "支付", "")
	add("orders", "", "/api/legacy", "历史功能", "历史", "")
	add("removed", "test", "/removed", "已删除应用功能", "删除", "")
	st.db.Exec(`DELETE FROM services WHERE name='removed'`)
	h := Routes(st, NewBuilder(st, t.TempDir(), NewLogHub()), NewLogHub(), "", NewStepLib("../../steps"))
	request := func(path string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("X-User", "member")
		r.Header.Set("X-Role", "member")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return w
	}
	search := func(q string) documentSearchResult {
		t.Helper()
		w := request("/api/api-documents/search"+q, 200)
		var v documentSearchResult
		if e = json.Unmarshal(w.Body.Bytes(), &v); e != nil {
			t.Fatal(e)
		}
		if strings.Contains(w.Body.String(), "response-example-private") || strings.Contains(w.Body.String(), `"definition"`) || strings.Contains(w.Body.String(), `"document"`) {
			t.Fatal("search exposed full contracts")
		}
		return v
	}
	var before int
	st.db.QueryRow(`SELECT COUNT(*) FROM api_document_versions`).Scan(&before)
	all := search("")
	if all.Total != 4 || all.Snapshots != 4 {
		t.Fatal(all)
	}
	for _, v := range all.Items {
		if v.SnapshotID == old || v.Service == "removed" || v.ApplicationID == "" || v.DisplayName == "" || v.Maintainer != "fixture-maintainer" {
			t.Fatal(v)
		}
	}
	for _, tc := range []struct {
		query string
		count int
	}{{"?q=" + url.QueryEscape("创建 订单"), 2}, {"?q=" + url.QueryEscape("取消"), 1}, {"?q=%25", 0}, {"?q=/API/ORDERS", 2}, {"?group=" + url.QueryEscape("房源"), 1}, {"?service=payments", 1}, {"?service=orders&environment=test", 1}, {"?environment=test", 2}, {"?environment=", 1}, {"?q=" + url.QueryEscape("已移除功能"), 0}} {
		if v := search(tc.query); v.Total != tc.count {
			t.Fatalf("%s: %d != %d", tc.query, v.Total, tc.count)
		}
	}
	one, two := search("?page=1&page_size=1"), search("?page=2&page_size=1")
	if one.Total != 4 || len(one.Items) != 1 || len(two.Items) != 1 || one.Items[0].SnapshotID == two.Items[0].SnapshotID {
		t.Fatal(one, two)
	}
	for _, v := range []struct {
		id  int64
		app string
	}{{current, "orders"}, {payment, "payments"}} {
		request(fmt.Sprintf("/api/api-documents/%d?service=%s", v.id, v.app), 200)
	}
	request(fmt.Sprintf("/api/api-documents/%d?service=payments", current), 404)
	for _, q := range []string{"?service=missing", "?environment=missing", "?page=0", "?page=-1", "?page_size=101", "?page=100001", "?page_size=x", "?q=" + strings.Repeat("a", 257)} {
		request("/api/api-documents/search"+q, 400)
	}
	request("/api/api-documents/source", 400)
	var after int
	st.db.QueryRow(`SELECT COUNT(*) FROM api_document_versions`).Scan(&after)
	if before != after {
		t.Fatal("search changed immutable history")
	}
	st.db.Exec(`UPDATE api_document_versions SET document='invalid-json' WHERE id=?`, current)
	request("/api/api-documents/search", 500)
	if v := search("?service=payments"); v.Total != 1 {
		t.Fatal(v)
	}
}
