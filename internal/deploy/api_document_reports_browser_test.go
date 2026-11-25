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
		if r.URL.Path == "/" || r.URL.Path == "/api-center.html" {
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
