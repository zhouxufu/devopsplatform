package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestResearchLiveNativeResolutionLabelsAndRawFilter(t *testing.T) {
	s, state := researchLiveFixture(t, 40)
	state.resolutionOptions = map[string]string{"fixed2": "服务端修复", "fixed3": "客户端修复"}
	for i, row := range state.rows {
		row["resolution"] = []string{"fixed2", "fixed3"}[i%2]
	}
	f := researchLiveFilter{Connection: "issues", Product: 1, WorkItemFilter: WorkItemFilter{PageSize: 20}}
	for i := 0; i < 2; i++ {
		out, err := s.ResearchLivePage(context.Background(), "alice", f)
		if err != nil || len(out.Items) != 20 {
			t.Fatal(err, len(out.Items))
		}
		for _, item := range out.Items {
			if item.ResolutionLabel != state.resolutionOptions[item.Resolution] || item.ResolutionLabel == "" {
				t.Fatal("custom enum not translated", item)
			}
		}
		if out.FieldLabels["resolution"]["fixed3"] != "客户端修复" {
			t.Fatal("live filter dictionary missing", out.FieldLabels)
		}
	}
	f.Resolution = "fixed2"
	out, err := s.ResearchLivePage(context.Background(), "alice", f)
	if err != nil || out.Total != 20 {
		t.Fatal("raw-code filter failed", err, out.Total)
	}
	for _, item := range out.Items {
		if item.Resolution != "fixed2" || item.ResolutionLabel != "服务端修复" {
			t.Fatal("filter changed enum code", item)
		}
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.routes["bug/resolve"] != 1 || state.businessWrites != 0 {
		t.Fatal("dictionary should use one cached GET, never a write", state.routes, state.businessWrites)
	}
}

func TestResearchResolutionLabelsFailureOverrideAndIdentity(t *testing.T) {
	s, state := researchLiveFixture(t, 2)
	f := researchLiveFilter{Connection: "issues", Product: 1, WorkItemFilter: WorkItemFilter{PageSize: 20}}
	for i := 0; i < 2; i++ {
		out, err := s.ResearchLivePage(context.Background(), "alice", f)
		if err != nil || len(out.Items) != 2 || out.Items[0].Resolution != "backend_custom" || out.Items[0].ResolutionLabel != "" {
			t.Fatal("dictionary failure changed/blocked source data", err, out)
		}
	}
	state.mu.Lock()
	if state.routes["bug/resolve"] != 1 {
		t.Fatal("failed dictionaries were not briefly cached", state.routes)
	}
	state.resolutionOptions = map[string]string{"backend_custom": "来源自定义方案"}
	state.mu.Unlock()
	// A newly authorized personal session must not inherit the public session's
	// failed dictionary. Neither session exposes another account's data.
	if err := s.AuthorizeResearch(context.Background(), "issues", "alice", ResearchCredential{Account: "alice", Password: "alice-secret"}); err != nil {
		t.Fatal(err)
	}
	out, err := s.ResearchLivePage(context.Background(), "alice", f)
	if err != nil || out.Items[0].ResolutionLabel != "来源自定义方案" {
		t.Fatal("personal dictionary was not independently read", err, out)
	}
	c := s.GetConnection("issues")
	c.Config["resolution_labels"] = `{"backend_custom":"管理员校正名称"}`
	if err := s.UpsertConnection(*c); err != nil {
		t.Fatal(err)
	}
	out, err = s.ResearchLivePage(context.Background(), "alice", f)
	if err != nil || out.Items[0].ResolutionLabel != "管理员校正名称" || out.FieldLabels["resolution"]["backend_custom"] != "管理员校正名称" {
		t.Fatal("configured override lost", err, out)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.businessWrites != 0 {
		t.Fatal("presentation modified source")
	}
}

func TestResearchResolutionHTMLFallbackReadsReal16Form(t *testing.T) {
	for _, pathInfo := range []bool{false, true} {
		t.Run(fmt.Sprint("pathInfo=", pathInfo), func(t *testing.T) {
			var jsonReads, htmlReads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Error("dictionary submitted a native form")
					w.WriteHeader(400)
					return
				}
				isHTML := r.URL.Query().Get("t") == "html" || strings.HasSuffix(r.URL.Path, ".html")
				if isHTML {
					htmlReads.Add(1)
					if pathInfo && r.URL.Path != "/zentao/bug-resolve-7.html" {
						t.Error("wrong PATHINFO form", r.URL.Path)
					}
					if !pathInfo && (r.URL.Query().Get("m") != "bug" || r.URL.Query().Get("f") != "resolve" || r.URL.Query().Get("bugID") != "7") {
						t.Error("wrong GET form", r.URL)
					}
					fmt.Fprint(w, `<html><script>location='https://do-not-follow.invalid/';</script><form method="post"><select name="assignedTo"><option value="alice">不应读此值</option></select><select name="resolution" id="resolution"><option value="">请选择</option><option value="fixed">代码错误修复</option><option value="fixed2">后端代码修复</option><option value="fixed3">前端代码修复</option><option value="fixed4">非本项目代码修复</option></select></form></html>`)
					return
				}
				jsonReads.Add(1)
				data, _ := json.Marshal(map[string]any{"bug": map[string]string{"id": "7", "product": "1", "resolution": "fixed2"}, "builds": map[string]string{"trunk": "主干"}})
				json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": string(data)})
			}))
			defer server.Close()
			z := &zentaoLegacy{root: server.URL + "/zentao", client: server.Client(), pathInfo: pathInfo}
			for i := 0; i < 2; i++ {
				labels := z.researchResolutionLabels(context.Background(), "1", "7", nil)
				if labels["fixed2"] != "后端代码修复" || labels["fixed3"] != "前端代码修复" || labels["fixed4"] != "非本项目代码修复" || labels["alice"] != "" {
					t.Fatal("HTML source enum not respected", labels)
				}
			}
			if jsonReads.Load() != 1 || htmlReads.Load() != 1 {
				t.Fatal("GET forms should be cached", jsonReads.Load(), htmlReads.Load())
			}
		})
	}
}

func TestResearchResolutionHTMLParserIsSpecificAndInert(t *testing.T) {
	labels, err := parseResearchResolutionHTML(`<select name="resolution"><optgroup label="custom"><option value="fixed2">后端 &amp; 接口修复</option></optgroup></select><script>window.post()</script>`)
	if err != nil || labels["fixed2"] != "后端 & 接口修复" {
		t.Fatal(err, labels)
	}
	for _, raw := range []string{
		`<form id="loginForm"><input name="account"></form>`,
		`<script>const resolutionList={fixed2:'不得执行脚本'};</script>`,
		`<select name="other"><option value="fixed2">不得猜测</option></select>`,
		`<select name="resolution"><option value="">请选择</option></select>`,
		`<select name="resolution"><option value="fixed2">A</option></select><select name="resolution"><option value="fixed2">B</option></select>`,
	} {
		if _, err := parseResearchResolutionHTML(raw); err == nil {
			t.Fatal("accepted a non-dictionary/ambiguous document")
		}
	}
}

func TestResearchResolutionCacheCopiesAndProductIsolation(t *testing.T) {
	z := &zentaoLegacy{}
	ctx := context.Background()
	labels := z.researchResolutionLabels(ctx, "1", "7", map[string]string{"fixed2": "产品一方案"})
	labels["fixed2"] = "modified"
	if got := z.researchResolutionLabels(ctx, "1", "8", nil)["fixed2"]; got != "产品一方案" {
		t.Fatal("caller mutated cached dictionary", got)
	}
	z.researchResolutionLabels(ctx, "2", "9", map[string]string{"fixed2": "产品二方案"})
	if got := z.researchResolutionLabels(ctx, "1", "7", nil)["fixed2"]; got != "产品一方案" {
		t.Fatal("product dictionary leaked", got)
	}
	entry := z.resolutionDictionaries["1"]
	entry.expires = time.Now().Add(-time.Second)
	z.resolutionDictionaries["1"] = entry
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if len(z.researchResolutionLabels(canceled, "1", "7", nil)) != 0 {
		t.Fatal("expired labels returned as fresh")
	}
}
