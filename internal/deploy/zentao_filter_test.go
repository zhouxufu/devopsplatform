package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestZentaoDateBoundaryAndStatusSelection(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("browseType") != "all" {
			t.Error("16.0 has no closed native browse type")
		}
		field := r.URL.Query().Get("orderBy")
		page := r.URL.Query().Get("pageID")
		rows := []zentaoBug{}
		for i := 1; i <= 100; i++ {
			bug := zentaoBug{ID: json.Number(fmt.Sprint(i)), Title: "bug", Status: "closed", LastEditedDate: "2025-01-01", ClosedDate: "2025-01-01"}
			if field == "lastEditedDate_desc" && page == "1" && i <= 3 {
				bug.LastEditedDate = "2026-10-06"
			}
			if field == "closedDate_desc" && page == "1" && i <= 2 {
				bug.ClosedDate = "2026-10-07"
			}
			if i == 2 {
				bug.Status = "active"
			}
			if page == "2" {
				bug.ID = json.Number(fmt.Sprint(i + 100))
			}
			rows = append(rows, bug)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"bugs": rows, "orderBy": field}})
	}))
	defer server.Close()
	z := &zentaoLegacy{root: server.URL, client: server.Client()}
	rows, err := z.filteredBugs(context.Background(), []int{14}, "2026-10-06", "closed")
	if err != nil || len(rows) != 2 || rows["1"].ID == "" || rows["3"].ID == "" {
		t.Fatalf("date union/default closed: %+v %v", rows, err)
	}
	if calls != 4 {
		t.Fatalf("did not stop old history: %d", calls)
	}
}

func TestZentaoStateOverrideAndConnectionDefault(t *testing.T) {
	st, name := zentaoFixture(t, func(r *http.Request) (any, string) {
		return map[string]any{"bugs": []zentaoBug{{ID: "1", Title: "closed", Status: "closed"}, {ID: "2", Title: "active", Status: "active"}, {ID: "3", Title: "resolved", Status: "resolved"}}}, ""
	})
	if _, err := st.syncZentao(context.Background(), name, "", "local"); err != nil {
		t.Fatal(err)
	}
	if len(st.WorkItems("", "", 100)) != 1 {
		t.Fatal("default must only import closed")
	}
	c := st.GetConnection(name)
	c.Config["bug_status"] = "active"
	if err := st.UpsertConnection(*c); err != nil {
		t.Fatal(err)
	}
	if _, err := st.syncZentao(context.Background(), name, "", "local"); err != nil {
		t.Fatal(err)
	}
	if len(st.WorkItems("", "", 100)) != 2 {
		t.Fatal("connection default or preservation failed")
	}
	if _, err := st.syncZentao(context.Background(), name, "", "local", "resolved"); err != nil {
		t.Fatal(err)
	}
	if len(st.WorkItems("", "", 100)) != 3 {
		t.Fatal("manual override failed")
	}
	if st.GetConnection(name).Config["bug_status"] != "active" {
		t.Fatal("manual override rewrote default")
	}
	if _, err := st.syncZentao(context.Background(), name, "", "local", "untrusted"); err == nil {
		t.Fatal("invalid state accepted")
	}
	for _, selection := range []string{"unclosed", "all"} {
		if _, err := st.syncZentao(context.Background(), name, "", "local", selection); err != nil {
			t.Fatal(err)
		}
	}
}

func TestZentaoNativeStatusQueriesAndUnknownOrder(t *testing.T) {
	for selected, browse := range map[string]string{"active": "unresolved", "resolved": "toclosed", "unclosed": "unclosed"} {
		t.Run(selected, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Query().Get("browseType") != browse {
					t.Error("wrong native browseType")
				}
				rows := []zentaoBug{}
				if r.URL.Query().Get("pageID") == "1" {
					for i := 1; i <= 100; i++ {
						rows = append(rows, zentaoBug{ID: json.Number(fmt.Sprint(i)), Title: "old", Status: "active", LastEditedDate: "2020-01-01"})
					}
				} else {
					rows = append(rows, zentaoBug{ID: "101", Title: "recent", Status: "active", LastEditedDate: "2026-10-09"})
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"bugs": rows}})
			}))
			defer server.Close()
			z := &zentaoLegacy{root: server.URL, client: server.Client()}
			rows, err := z.filteredBugs(context.Background(), []int{14}, "2026-10-06", selected)
			if err != nil || calls != 4 {
				t.Fatalf("missing order cannot early-stop: %d %v", calls, err)
			}
			if selected != "resolved" && len(rows) != 1 {
				t.Fatal("missed later matching row")
			}
		})
	}
}

func TestZentaoTenThousandBoundaryAndAtomicLimit(t *testing.T) {
	for _, overflow := range []bool{false, true} {
		t.Run(fmt.Sprint(overflow), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				page := 1
				fmt.Sscan(r.URL.Query().Get("pageID"), &page)
				rows := []zentaoBug{}
				if page <= 100 || overflow {
					for i := 1; i <= 100; i++ {
						rows = append(rows, zentaoBug{ID: json.Number(fmt.Sprint((page-1)*100 + i)), Title: "bug", Status: "closed"})
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"bugs": rows}})
			}))
			defer server.Close()
			z := &zentaoLegacy{root: server.URL, client: server.Client()}
			rows, err := z.filteredBugs(context.Background(), []int{14}, "", "closed")
			if overflow {
				if err == nil || rows != nil || !strings.Contains(err.Error(), "不是日期范围内命中数") {
					t.Fatalf("partial/unclear limit: %d %v", len(rows), err)
				}
			} else if err != nil || len(rows) != 10000 {
				t.Fatalf("exact 10000 falsely rejected: %d %v", len(rows), err)
			}
		})
	}
}

func TestZentaoDateOrderInstabilityFailsClosed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("pageID")
		rows := []zentaoBug{}
		for i := 1; i <= 100; i++ {
			id := i
			date := "2026-10-06"
			if page == "2" {
				id += 100
				date = "2026-10-08"
			}
			rows = append(rows, zentaoBug{ID: json.Number(fmt.Sprint(id)), Title: "bug", Status: "closed", LastEditedDate: date})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"bugs": rows, "orderBy": "lastEditedDate_desc"}})
	}))
	defer server.Close()
	z := &zentaoLegacy{root: server.URL, client: server.Client()}
	if rows, err := z.filteredBugs(context.Background(), []int{14}, "2026-10-06", "closed"); err == nil || rows != nil {
		t.Fatal("accepted unstable date pagination")
	}
	z.pathInfo = true
	got, err := z.requestURL("bug", "browse", url.Values{"productID": {"14"}, "orderBy": {"closedDate_desc"}, "browseType": {"all"}, "pageID": {"3"}})
	if err != nil || !strings.HasSuffix(got, "bug-browse-14-0-all-0-closedDate_desc-0-100-3.json") {
		t.Fatal(got, err)
	}
}
