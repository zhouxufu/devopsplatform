package deploy

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
)

func TestOrdinaryListPaginationSizeValidation(t *testing.T) {
	for _, v := range []struct {
		query   string
		want    int
		invalid bool
	}{{"", 20, false}, {"?page_size=20", 20, false}, {"?page_size=50", 50, false}, {"?page_size=100", 100, false}, {"?page_size=1", 1, false}, {"?page_size=0", 0, true}, {"?page_size=101", 0, true}, {"?page_size=", 0, true}, {"?page_size=abc", 0, true}} {
		size, err := requestListPageSize(httptest.NewRequest("GET", "/"+v.query, nil))
		if (err != nil) != v.invalid || size != v.want {
			t.Fatalf("%s: %d %v", v.query, size, err)
		}
	}
}

func TestOrdinaryReportPaginationReturnsActualRequestedPageSize(t *testing.T) {
	_, h := apiTestingFixture(t)
	for i := 0; i < 107; i++ {
		receiptSubmit(t, h, apiDocumentFixture, fmt.Sprintf("%032x", i+1000), false, "")
	}
	for _, v := range []struct {
		query            string
		size, rows, page int
	}{{"", 20, 20, 1}, {"?page=2&page_size=50", 50, 50, 2}, {"?page=3&page_size=50", 50, 7, 3}, {"?page=1&page_size=100", 100, 100, 1}} {
		w := documentReportRequest(h, "GET", "/api/api-document-reports"+v.query, "alice", "member", nil)
		var out struct {
			Items []DocumentReport `json:"items"`
			Total int              `json:"total"`
			Size  int              `json:"page_size"`
			Page  int              `json:"page"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || out.Total != 107 || out.Size != v.size || len(out.Items) != v.rows || out.Page != v.page {
			t.Fatalf("%s: status=%d size=%d rows=%d total=%d", v.query, w.Code, out.Size, len(out.Items), out.Total)
		}
	}
	for _, v := range []string{"0", "101", "abc"} {
		w := documentReportRequest(h, "GET", "/api/api-document-reports?page_size="+v, "alice", "member", nil)
		if w.Code != 400 {
			t.Fatal(v, w.Code)
		}
	}
}
