package deploy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestResearchRuleOptionsNativeAndLegacyProductScope(t *testing.T) {
	for _, format := range []string{"fieldParams", "params"} {
		t.Run(format, func(t *testing.T) {
			current := ""
			browsed := []string{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Error("dictionary must never submit a source action")
					w.WriteHeader(405)
					return
				}
				var data any
				switch r.URL.Query().Get("m") + "/" + r.URL.Query().Get("f") {
				case "bug/browse":
					current = r.URL.Query().Get("productID")
					if r.URL.Query().Get("recPerPage") != "1" {
						t.Error("unbounded Bug loading")
					}
					browsed = append(browsed, current)
					data = map[string]any{"productID": current, "products": map[string]string{"1": "产品一", "2": "产品二", "99": "未配置产品"}}
				case "search/buildForm":
					data = map[string]any{format: map[string]any{
						"project":    map[string]any{"values": map[string]string{current + "70": "项目" + current, "all": "所有项目", "null": "空", "": ""}},
						"resolution": map[string]any{"values": map[string]string{"fixed2": "后端代码修复", "fixed3": "前端代码修复", "null": "空"}},
						"severity":   map[string]any{"values": map[string]any{"1": 1, "2": "严重", "0": ""}},
						"pri":        map[string]any{"values": map[string]string{"1": "紧急"}},
						"assignedTo": map[string]any{"values": "users"},
					}}
				default:
					t.Error("unexpected source request", r.URL)
					w.WriteHeader(400)
					return
				}
				raw, _ := json.Marshal(data)
				json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": string(raw)})
			}))
			defer server.Close()
			z := &zentaoLegacy{root: server.URL, client: server.Client(), searchDigest: "previous-search", searchProduct: 1}
			options, err := z.researchRuleOptions(context.Background(), &ResourceConnection{Config: map[string]string{"product_ids": "1,2"}})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(browsed, []string{"0", "1", "2"}) || options["product"]["99"] != "" {
				t.Fatal("product scope escaped", browsed, options["product"])
			}
			if !reflect.DeepEqual(options["project"], map[string]string{"170": "项目1", "270": "项目2"}) || options["resolution"]["fixed2"] != "后端代码修复" || options["resolution"]["fixed3"] != "前端代码修复" || options["resolution"]["null"] != "" || options["severity"]["1"] != "1" || options["priority"]["1"] != "紧急" {
				t.Fatal("source dictionaries lost or changed", options)
			}
			if z.searchDigest != "" || z.searchProduct != 0 {
				t.Fatal("list search cache was not invalidated")
			}
		})
	}
}

func TestResearchRuleOptionsFailedSourceDoesNotLookEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	z := &zentaoLegacy{root: server.URL, client: server.Client()}
	if options, err := z.researchRuleOptions(context.Background(), &ResourceConnection{}); err == nil || options != nil {
		t.Fatal("source failure must not masquerade as empty dictionaries")
	}
	mux := http.NewServeMux()
	registerResearchRuleEditor(mux, nil)
	if response := policyRequest(t, mux, "GET", "/api/research/connections/issues/release-rules/options", "member", "member", nil); response.Code != 403 {
		t.Fatal("member can read admin rule options", response.Code)
	}
}
