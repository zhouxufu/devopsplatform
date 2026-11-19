package apitest

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestExtendedBodiesWireEncodingAndReportRedaction(t *testing.T) {
	payload := []byte{0, 1, 2, 255, 'x'}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/form":
			if err := r.ParseForm(); err != nil || r.FormValue("q") != "a&b=中文" || r.Form["many"][1] != "second" || r.FormValue("id") != "9007199254740993" {
				t.Error("form", r.Form, err)
			}
		case "/multipart":
			if err := r.ParseMultipartForm(256 << 10); err != nil {
				t.Error(err)
				return
			}
			if r.FormValue("id") != "a/b" {
				t.Error(r.Form)
			}
			f, header, err := r.FormFile("file")
			if err != nil {
				t.Error(err)
				return
			}
			defer f.Close()
			body, _ := io.ReadAll(f)
			if string(body) != string(payload) || header.Filename != "file.bin" {
				t.Error(body, header)
			}
		case "/binary":
			raw, _ := io.ReadAll(r.Body)
			if string(raw) != string(payload) || r.Header.Get("Content-Type") != "application/octet-stream" {
				t.Error(raw, r.Header)
			}
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()
	file := &FileContent{Name: "file.bin", Data: base64.StdEncoding.EncodeToString(payload)}
	for _, r := range []Request{
		{Path: "/form", Method: "POST", BodyType: "urlencoded", Form: []FormField{{Name: "q", Value: "a&b=中文"}, {Name: "many", Value: "first"}, {Name: "many", Value: "second"}, {Name: "id", Value: "{{case.id}}"}}},
		{Path: "/multipart", Method: "POST", BodyType: "multipart", Headers: map[string]string{"Content-Type": "wrong/boundary"}, Form: []FormField{{Name: "id", Value: "{{step.id}}"}, {Name: "file", File: file}}},
		{Path: "/binary", Method: "POST", BodyType: "binary", File: file},
	} {
		result, _ := PerformScoped(context.Background(), fixtureClient(), server.URL, r, Auth{}, nil, map[string]any{"id": "a/b", "case.id": json.Number("9007199254740993")}, nil, nil)
		if result.Error != "" || result.Status != 200 {
			t.Fatal(result)
		}
		raw, _ := json.Marshal(SanitizeRequest(r, nil))
		if strings.Contains(string(raw), file.Data) {
			t.Fatal("file leaked", string(raw))
		}
	}
	for _, r := range []Request{{Path: "/", BodyType: "binary", File: &FileContent{Name: "server-file", Data: "", Pending: true}}, {Path: "/", BodyType: "binary", File: &FileContent{Name: "../file", Data: "YQ=="}}, {Path: "/", BodyType: "json", Body: "bad"}, {Path: "/", BodyType: "urlencoded", Form: []FormField{{Name: "file", File: file}}}, {Path: "/", BodyType: "binary", File: &FileContent{Name: "huge", Data: strings.Repeat("YQ==", MaxRequestBody)}}} {
		if Validate(&r) == nil {
			t.Fatal("invalid body accepted", r.BodyType)
		}
	}
}
func TestExtendedAssertionsExactNumbersSchemaAndValidation(t *testing.T) {
	r := Result{Status: 200, Body: `{"id":9007199254740993123,"name":"order-123","ok":true}`, DurationMS: 25}
	checks := []Assertion{{Type: "json_range", Path: "/id", Expected: "[9007199254740993123,9007199254740993123]"}, {Type: "json_regex", Path: "/name", Expected: "^order-[0-9]+$"}, {Type: "regex", Expected: "order-"}, {Type: "duration", Expected: "25"}, {Type: "schema", Expected: `{"type":"object","required":["id"],"properties":{"id":{"type":"integer","minimum":9007199254740993123}}}`}}
	for _, a := range checks {
		q := Request{Path: "/", Assertions: []Assertion{a}}
		if err := Validate(&q); err != nil {
			t.Fatal(a, err)
		}
	}
	for _, c := range Evaluate(checks, r) {
		if !c.Passed {
			t.Fatal(c)
		}
	}
	for _, a := range []Assertion{{Type: "json_range", Path: "/id", Expected: "[0,9007199254740993122]"}, {Type: "duration", Expected: "24"}, {Type: "schema", Expected: `{"required":["absent"]}`}, {Type: "json_regex", Path: "/id", Expected: ".+"}} {
		if Evaluate([]Assertion{a}, r)[0].Passed {
			t.Fatal("false success", a)
		}
	}
	for _, a := range []Assertion{{Type: "regex", Expected: "("}, {Type: "schema", Expected: `{"$ref":"http://127.0.0.1/secret"}`}, {Type: "duration", Expected: "-1"}, {Type: "json_range", Expected: "[9,1]"}} {
		q := Request{Path: "/", Assertions: []Assertion{a}}
		if Validate(&q) == nil {
			t.Fatal("invalid accepted", a)
		}
	}
	r.Truncated = true
	for _, c := range Evaluate(checks[:3], r) {
		if c.Passed {
			t.Fatal("truncated false success", c)
		}
	}
}
func TestSSESelectedEventAssertionExtractionAndRedaction(t *testing.T) {
	events := []Event{{Event: "ready", Data: `{"id":"first-private"}`}, {Event: "delta", Data: `{"text":"progress"}`}, {Event: "ready", Data: `{"id":"second-private"}`}, {Event: "done", Data: `{"ok":true}`}}
	result := Result{Status: 200, Events: events}
	assertions := []Assertion{{Type: "sse_json", Event: "ready", Select: "first", Path: "/id", Expected: `"first-private"`}, {Type: "sse_contains", Event: "ready", Select: "index:1", Expected: "second-private"}}
	for _, c := range Evaluate(assertions, result) {
		if !c.Passed {
			t.Fatal(c)
		}
	}
	if Evaluate([]Assertion{{Type: "sse_json", Event: "absent", Path: "/id", Expected: "x"}}, result)[0].Passed {
		t.Fatal("missing event accepted")
	}
	defs := []Extraction{{Name: "id", Source: "sse", Event: "ready", Select: "index:1", Path: "/id"}}
	out, secrets := extractResponse(defs, result)
	if out.Values["id"] != "second-private" || len(secrets) != 1 {
		t.Fatal(out, secrets)
	}
	if _, ok := SelectEvent(events, "ready", "index:-1"); ok {
		t.Fatal("invalid selector")
	}
	q := Request{Path: "/", Mode: "http", Assertions: assertions}
	if Validate(&q) == nil {
		t.Fatal("HTTP SSE assertions accepted")
	}
	// Duration is populated before evaluating rather than only when finishing.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"ok":true}`) }))
	defer server.Close()
	rr := Perform(context.Background(), fixtureClient(), server.URL, Request{Path: "/", Assertions: []Assertion{{Type: "duration", Expected: "120000"}}}, Auth{}, nil, nil)
	if !rr.Checks[0].Passed {
		t.Fatal(rr)
	}
}

func TestExtendedFileEchoIsRedactedAndScopedInputCannotInject(t *testing.T) {
	raw := `{"password":"private-file-password"}`
	file := &FileContent{Name: "file.json", Data: base64.StdEncoding.EncodeToString([]byte(raw))}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"raw":%q,"encoded":%q,"password":"private-file-password"}`, raw, file.Data)
	}))
	defer server.Close()
	result := Perform(context.Background(), fixtureClient(), server.URL, Request{Path: "/", Method: "POST", BodyType: "binary", File: file}, Auth{}, nil, nil)
	serialized, _ := json.Marshal(result)
	if strings.Contains(string(serialized), "private-file-password") || strings.Contains(string(serialized), file.Data) {
		t.Fatal("file echoed in report", string(serialized))
	}
	bound, err := BindStepRequest(Request{Path: "/x/{{case.id}}?id={{case.id}}", Body: `{"id":"{{case.id}}"}`}, nil, map[string]any{"case.id": `a/b?escape=1&x={{var.SECRET}}`})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(bound.Path)
	if err != nil || len(parsed.Query()) != 1 || parsed.Query().Get("id") != `a/b?escape=1&x={{var.SECRET}}` {
		t.Fatal(bound.Path, err)
	}
	if ValidateCaseReferences(Request{Path: "/{{case.id}}"}, nil, true) == nil {
		t.Fatal("missing binding accepted")
	}
}
