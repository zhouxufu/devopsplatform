package deploy

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDocumentReportsBrowserFixture(t *testing.T) {
	if os.Getenv("DOCUMENT_REPORT_BROWSER_FIXTURE") != "1" {
		t.Skip("opt-in isolated browser fixture")
	}
	s, h := apiTestingFixture(t)
	if os.Getenv("DOCUMENT_REPORT_LINK_FIXTURE") == "1" {
		reportLinkWork(t, s, "requirement", "演示需求：业主媒体管理")
		reportLinkWork(t, s, "bug", "演示Bug：图片地址返回不完整")
	}
	doc := `{"openapi":"3.0.3","info":{"title":"上报记录隔离验证","version":"1"},"paths":{"/images":{"get":{"summary":"图片审核详情","responses":{"200":{"description":"图片列表"}}}},"/videos":{"get":{"summary":"视频审核详情","responses":{"200":{"description":"视频列表"}}}}}}`
	a := receiptSubmit(t, h, doc, strings.Repeat("a", 32), false, "")
	s.db.Exec(`UPDATE api_document_versions SET created_at=? WHERE id=?`, time.Now().Add(-48*time.Hour).Unix(), a.SnapshotID)
	b := receiptSubmit(t, h, strings.Replace(doc, "图片列表", "图片完整地址列表", 1), strings.Repeat("b", 32), false, "")
	for i := 0; i < 23; i++ {
		receiptSubmit(t, h, strings.Replace(doc, "图片列表", "图片完整地址列表", 1), fmt.Sprintf("%032x", i+16), false, "")
	}
	receiptSubmit(t, h, doc, strings.Repeat("c", 32), false, "")
	receiptSubmit(t, h, doc, strings.Repeat("d", 32), true, "")
	s.db.Exec(`UPDATE api_document_reports SET actor='bob' WHERE id=?`, b.ID)
	if os.Getenv("UNIFIED_PAGINATION_BROWSER_FIXTURE") == "1" {
		for i := 0; i < 100; i++ {
			receiptSubmit(t, h, doc, fmt.Sprintf("%032x", i+1000), false, "")
		}
	}
	if os.Getenv("DOCUMENT_REPORT_PAGINATION_FIXTURE") == "1" {
		paths := []string{}
		for i := 1; i <= 45; i++ {
			paths = append(paths, fmt.Sprintf(`"/page-%02d":{"get":{"summary":"分页接口%02d","responses":{"200":{"description":"原说明"}}}}`, i, i))
		}
		before := `{"openapi":"3.0.3","info":{"title":"分页隔离验证","version":"1"},"paths":{` + strings.Join(paths, ",") + `}}`
		receiptSubmit(t, h, before, strings.Repeat("8", 32), false, "")
		receiptSubmit(t, h, strings.ReplaceAll(before, "原说明", "新说明"), strings.Repeat("9", 32), false, "")
	}
	if os.Getenv("DOCUMENT_FIELD_DIFF_FIXTURE") == "1" {
		before := strings.Replace(doc, `"description":"图片列表"`, `"description":"图片列表","content":{"application/json":{"schema":{"type":"object","properties":{"userNick":{"type":"string","description":"业主昵称"}}},"example":{"code":200,"data":{"id":210000000000000001,"userNick":"演示业主","images":[{"url":"demo.jpg","auditStatus":0}],"cover":null}}}}`, 1)
		receiptSubmit(t, h, before, strings.Repeat("e", 32), false, "")
		after := strings.Replace(before, `,"example":{"code":200,"data":{"id":210000000000000001,"userNick":"演示业主","images":[{"url":"demo.jpg","auditStatus":0}],"cover":null}}`, "", 1)
		after = strings.Replace(after, "业主昵称", "业主显示名称", 1)
		receiptSubmit(t, h, after, strings.Repeat("f", 32), false, "")
	}
	stop := make(chan struct{})
	var once sync.Once
	var ledgerMu sync.Mutex
	requests := []string{}
	fault := 0
	root, e := filepath.Abs("../../web/dist")
	if e != nil {
		t.Fatal(e)
	}
	mux := http.NewServeMux()
	if os.Getenv("DOCUMENT_WORKSPACE_FIXTURE") == "1" {
		mux.HandleFunc("GET /api/plugins", func(w http.ResponseWriter, r *http.Request) {
			deliveryJSON(w, []map[string]any{{"name": "api-center", "display": "接口中心", "url": "/api/plugins/api-center/ui"}})
		})
		mux.HandleFunc("GET /api/registry/applications", func(w http.ResponseWriter, r *http.Request) { deliveryJSON(w, []any{}) })
		mux.HandleFunc("GET /api/plugins/api-center/ui", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			http.ServeFile(w, r, filepath.Join(root, "api-center.html"))
		})
		mux.Handle("GET /api/plugins/api-center/assets/", http.StripPrefix("/api/plugins/api-center/", http.FileServer(http.Dir(root))))
		mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir(filepath.Join(root, "assets")))))
	}
	mux.HandleFunc("GET /__fixture/stop", func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(stop) })
		fmt.Fprint(w, "stopping")
	})
	mux.HandleFunc("GET /__fixture/status", func(w http.ResponseWriter, r *http.Request) {
		ledgerMu.Lock()
		defer ledgerMu.Unlock()
		deliveryJSON(w, map[string]any{"requests": requests})
	})
	mux.HandleFunc("POST /__fixture/fault/{code}", func(w http.ResponseWriter, r *http.Request) {
		ledgerMu.Lock()
		defer ledgerMu.Unlock()
		fault = 0
		if r.PathValue("code") == "401" {
			fault = 401
		}
		if r.PathValue("code") == "409" {
			fault = 409
		}
		deliveryJSON(w, map[string]any{"fault": fault})
	})
	mux.HandleFunc("GET /api/me", func(w http.ResponseWriter, r *http.Request) {
		deliveryJSON(w, map[string]any{"username": "alice", "role": "admin", "auth_enabled": true})
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/api/plugins/api-center")
		ledgerMu.Lock()
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		f := fault
		ledgerMu.Unlock()
		if f != 0 {
			deliveryWriteError(w, f, "隔离模拟：请求未完成，原输入保留")
			return
		}
		r.Header.Set("X-User", "alice")
		r.Header.Set("X-Role", "admin")
		h.ServeHTTP(w, r)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == "/" || r.URL.Path == "/api-center.html" {
			if r.URL.Path == "/" && os.Getenv("DOCUMENT_WORKSPACE_FIXTURE") == "1" {
				http.ServeFile(w, r, filepath.Join(root, "shell.html"))
				return
			}
			http.ServeFile(w, r, filepath.Join(root, "api-center.html"))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			http.FileServer(http.Dir(root)).ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})
	listener, e := net.Listen("tcp", "127.0.0.1:19836")
	if e != nil {
		t.Fatal(e)
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	go server.Serve(listener)
	t.Log("Reports browser fixture at http://127.0.0.1:19836/#/reports")
	select {
	case <-stop:
	case <-time.After(25 * time.Minute):
		t.Error("fixture timed out")
	}
}
