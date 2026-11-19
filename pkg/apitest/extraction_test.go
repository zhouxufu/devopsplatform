package apitest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStepBindingTypesEscapingAndSinglePass(t *testing.T) {
	scope := map[string]any{"id": json.Number("9007199254740993"), "ok": false, "text": "a/b?x=1&y=2#fragment", "untrusted": "{{var.PASSWORD}}", "string_id": "123"}
	request := Request{Path: "/orders/{{step.text}}?q={{step.text}}", Headers: map[string]string{"X-Value": "{{step.untrusted}}"}, Body: `{"id":"{{step.id}}","ok":"{{step.ok}}","text":"prefix {{step.text}}","literal":"{{step.untrusted}}"}`, Assertions: []Assertion{{Type: "json", Path: "/id", Expected: "{{step.string_id}}"}}}
	r, e := BindStepRequest(request, map[string]string{"PASSWORD": "must-not-leak"}, scope)
	if e != nil {
		t.Fatal(e)
	}
	if r.Path != "/orders/a%2Fb%3Fx=1&y=2%23fragment?q=a%2Fb%3Fx%3D1%26y%3D2%23fragment" {
		t.Fatal(r.Path)
	}
	if strings.Contains(r.Body, "must-not-leak") || r.Headers["X-Value"] != "{{var.PASSWORD}}" || !strings.Contains(r.Body, `"id":9007199254740993`) || !strings.Contains(r.Body, `"ok":false`) {
		t.Fatal(r)
	}
	if r.Assertions[0].Expected != `"123"` || !Evaluate(r.Assertions, Result{Body: `{"id":"123"}`})[0].Passed {
		t.Fatal(r.Assertions)
	}
	if request.Assertions[0].Expected != "{{step.string_id}}" {
		t.Fatal("mutated template")
	}
	if _, e = BindStepRequest(Request{Path: "/{{step.missing}}"}, nil, scope); e == nil {
		t.Fatal("missing variable accepted")
	}
	if _, e = BindStepRequest(Request{Path: "/", Body: `{"{{step.id}}":true}`}, nil, scope); e == nil {
		t.Fatal("dynamic JSON key accepted")
	}
	bound, _ := BindStepRequest(Request{Path: "/", Headers: map[string]string{"X-Test": "{{step.bad}}"}}, nil, map[string]any{"bad": "value\r\nInjected: true"})
	if Validate(&bound) == nil {
		t.Fatal("header injection accepted")
	}
}

func TestResponseExtractionsBeforeRedactionAndAtomicFailure(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Session", "header-private")
		fmt.Fprint(w, `{"data":{"id":9007199254740993,"zero":0,"ok":false,"token":"body-private"}}`)
	}))
	defer s.Close()
	defs := []Extraction{{Name: "id", Source: "json", Path: "/data/id"}, {Name: "ok", Source: "json", Path: "/data/ok"}, {Name: "zero", Source: "json", Path: "/data/zero"}, {Name: "token", Source: "json", Path: "/data/token"}, {Name: "session", Source: "header", Path: "x-session"}}
	var progress Result
	result, out := PerformScoped(context.Background(), fixtureClient(), s.URL, Request{Path: "/"}, Auth{Mode: "none"}, nil, nil, defs, nil, func(r Result) { progress = r })
	if result.Error != "" || len(out.Values) != 5 || out.Values["id"] != json.Number("9007199254740993") || out.Values["ok"] != false {
		t.Fatalf("%+v %+v", result, out)
	}
	raw, _ := json.Marshal(struct {
		Result   Result
		Outcome  ExtractionOutcome
		Progress Result
	}{result, out, progress})
	for _, secret := range []string{"body-private", "header-private", "9007199254740993"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("private value persisted %s", raw)
		}
	}
	defs = append(defs, Extraction{Name: "missing", Source: "json", Path: "/absent"})
	result, out = PerformScoped(context.Background(), fixtureClient(), s.URL, Request{Path: "/"}, Auth{Mode: "none"}, nil, nil, defs, nil)
	if result.Error == "" || len(out.Values) != 0 || out.Checks[5].Status != "failed" {
		t.Fatal(result, out)
	}
	result, out = PerformScoped(context.Background(), fixtureClient(), s.URL, Request{Path: "/", Assertions: []Assertion{{Type: "status", Expected: "201"}}}, Auth{Mode: "none"}, nil, nil, defs, nil)
	if len(out.Values) != 0 || out.Checks[0].Status != "skipped" {
		t.Fatal(result, out)
	}
}

func TestExtractionValidationAndSSEProgress(t *testing.T) {
	for _, raw := range []string{`{"v":null}`, `{"v":[]}`, `{"v":{}}`, `{"v":""}`, `{"v":"` + strings.Repeat("a", 8193) + `"}`, `not json`} {
		if _, message := decodeScalar(raw, "/v"); message == "" {
			t.Fatal("invalid scalar accepted")
		}
	}
	if _, message := extractionValue(Extraction{Name: "v", Source: "json", Path: "/v"}, Result{Body: `{"v":1}`, Truncated: true}); message == "" {
		t.Fatal("truncated accepted")
	}
	for _, defs := range [][]Extraction{{{Name: "a", Source: "json", Path: "/bad~2"}}, {{Name: "a", Source: "header", Path: ""}}, {{Name: "a", Source: "sse", Path: "/id"}}, {{Name: "a", Source: "json", Path: "/id"}, {Name: "a", Source: "json", Path: "/id"}}} {
		if ValidateExtractions(defs, "http") == nil {
			t.Fatal(defs)
		}
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/follow" {
			if r.URL.Query().Get("id") != "private-last" {
				t.Error("last SSE value not passed")
			}
			fmt.Fprint(w, `{"id":"private-last"}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"id\":\"private-first\"}\n\ndata: {\"id\":\"private-last\"}\n\n")
	}))
	defer s.Close()
	emitted := []Event{}
	result, out := PerformScoped(context.Background(), fixtureClient(), s.URL, Request{Path: "/", Mode: "sse", MaxEvents: 2}, Auth{Mode: "none"}, nil, nil, []Extraction{{Name: "id", Source: "sse", Path: "/id"}}, func(e Event) { emitted = append(emitted, e) })
	if result.Error != "" || out.Values["id"] != "private-last" {
		t.Fatal(result, out)
	}
	raw, _ := json.Marshal(struct {
		Result Result
		Events []Event
	}{result, emitted})
	if strings.Contains(string(raw), "private-") {
		t.Fatal(string(raw))
	}
	follow, _ := PerformScoped(context.Background(), fixtureClient(), s.URL, Request{Path: "/follow?id={{step.id}}", Assertions: []Assertion{{Type: "json", Path: "/id", Expected: "{{step.id}}"}}}, Auth{Mode: "none"}, nil, out.Values, nil, nil)
	if follow.Error != "" || len(follow.Checks) != 1 || !follow.Checks[0].Passed || strings.Contains(follow.Body, "private-last") {
		t.Fatal(follow)
	}
}

func TestScopedRequestSinglePassOverHTTPAndNamespaceIsolation(t *testing.T) {
	var seen int
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen++
		if r.URL.Query().Get("q") != "a/b?x=1&y=2" || r.URL.Query().Get("y") != "" {
			t.Error("query injection", r.URL.RawQuery)
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["literal"] != "{{var.PASSWORD}}" || body["environment"] != "bound-value" || body["step"] != "extracted-value" || r.Header.Get("X-Literal") != "{{var.PASSWORD}}" {
			t.Error("recursive or wrong namespace binding", body, r.Header)
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer s.Close()
	req := Request{Method: "POST", Path: "/use?q={{step.query}}", Headers: map[string]string{"X-Literal": "{{step.literal}}"}, Body: `{"literal":"{{step.literal}}","environment":"{{var.SAME}}","step":"{{step.SAME}}"}`}
	scope := map[string]any{"query": "a/b?x=1&y=2", "literal": "{{var.PASSWORD}}", "SAME": "extracted-value"}
	result, _ := PerformScoped(context.Background(), fixtureClient(), s.URL, req, Auth{Mode: "none"}, map[string]string{"PASSWORD": "must-not-leak", "SAME": "bound-value"}, scope, nil, nil)
	if result.Error != "" || seen != 1 {
		t.Fatal(result, seen)
	}
	// A new execution with an empty private scope cannot reuse an earlier run's values.
	result, _ = PerformScoped(context.Background(), fixtureClient(), s.URL, req, Auth{Mode: "none"}, nil, nil, nil, nil)
	if result.Error == "" || seen != 1 {
		t.Fatal("scope escaped the run", result, seen)
	}
	clean := SanitizeRequest(req, nil)
	if clean.Path != req.Path {
		t.Fatal("request template was URL encoded", clean.Path)
	}
}
