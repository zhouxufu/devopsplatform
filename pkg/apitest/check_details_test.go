package apitest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAssertionDetailsPreserveTypesMissingFieldsAndBounds(t *testing.T) {
	r := Result{Status: 201, Headers: map[string]string{"X-Version": "v2"}, Body: `{"id":9007199254740993123,"string":"123","null":null}`, Events: []Event{{Data: "a"}}}
	assertions := []Assertion{{Type: "status", Expected: "200"}, {Type: "json", Path: "/id", Expected: "9007199254740993123"}, {Type: "json", Path: "/string", Expected: `"123"`}, {Type: "json", Path: "/missing", Expected: "null"}, {Type: "json", Path: "/null", Expected: "null"}, {Type: "header", Path: "x-version", Expected: "v1"}, {Type: "sse_count", Expected: "2"}}
	checks := Evaluate(assertions, r)
	if checks[0].Passed || checks[0].Actual != "201" || checks[0].Expected != "200" || !checks[0].DetailAvailable {
		t.Fatal(checks[0])
	}
	if !checks[1].Passed || checks[1].Actual != "9007199254740993123" || checks[2].Actual != `"123"` {
		t.Fatal(checks)
	}
	if checks[3].Passed || checks[3].Actual != "字段不存在" || !checks[4].Passed || checks[4].Actual != "null" {
		t.Fatal(checks)
	}
	if checks[5].Actual != "v2" || checks[6].Actual != "1" {
		t.Fatal(checks)
	}
	r.Truncated = true
	if v := Evaluate(assertions[1:2], r)[0]; v.Passed || v.Actual != "响应已截断，无法完整核对" {
		t.Fatal(v)
	}
	secret := strings.Repeat("s", 5000)
	if detail := checkDetail(secret, nil); len([]rune(detail)) > 4120 {
		t.Fatal("unbounded detail")
	}
	if detail := checkDetail(secret, []string{secret}); strings.Contains(detail, "sss") {
		t.Fatal("partial secret leaked")
	}
}

func TestAssertionDetailsAreRedactedBeforeReportPersistence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "session=cookie-value")
		fmt.Fprint(w, `{"token":"inline-token","echo":"variable-secret","nested":{"password":"nested-secret"},"id":7}`)
	}))
	defer server.Close()
	request := Request{Path: "/", Assertions: []Assertion{{Type: "json", Path: "/token", Expected: `"inline-token"`}, {Type: "json", Path: "/echo", Expected: `"{{var.KEY}}"`}, {Type: "json", Path: "/nested", Expected: `{"password":"nested-secret"}`}, {Type: "header", Path: "Set-Cookie", Expected: "session=cookie-value"}}}
	result := Perform(context.Background(), fixtureClient(), server.URL, request, Auth{}, map[string]string{"KEY": "variable-secret"}, nil)
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	raw, _ := json.Marshal(result)
	for _, secret := range []string{"inline-token", "variable-secret", "nested-secret", "cookie-value"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("check leaked secret %s", secret)
		}
	}
	for _, check := range result.Checks {
		if !check.DetailAvailable || !check.Passed {
			t.Fatal(check)
		}
	}
}
