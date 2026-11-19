package apitest

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDataScopeNamespacePrecisionAndSinglePass(t *testing.T) {
	scope := map[string]any{"data.id": json.Number("9007199254740993123"), "id": "from-step", "data.flag": false, "data.text": "a/b?x=1&y=2#part", "data.literal": "{{step.id}}"}
	r := Request{Path: "/orders/{{data.text}}?id={{data.id}}", Headers: map[string]string{"X-Literal": "{{data.literal}}"}, Body: `{"id":"{{data.id}}","flag":"{{data.flag}}","prior":"{{step.id}}"}`, Assertions: []Assertion{{Type: "json", Path: "/id", Expected: "{{data.id}}"}}}
	out, e := BindStepRequest(r, nil, scope)
	if e != nil {
		t.Fatal(e)
	}
	if out.Body != `{"flag":false,"id":9007199254740993123,"prior":"from-step"}` || out.Headers["X-Literal"] != "{{step.id}}" || out.Assertions[0].Expected != "9007199254740993123" || !strings.Contains(out.Path, "a%2Fb%3Fx=1&y=2%23part") {
		t.Fatalf("%+v", out)
	}
	if _, e = BindStepRequest(r, nil, nil); e == nil {
		t.Fatal("missing data accepted")
	}
	for _, bad := range []string{"{{data.bad-name}}", "{{data.missing}}"} {
		if ValidateDataReferences(Request{Path: "/", Body: bad}, scope) == nil {
			t.Fatal("invalid reference", bad)
		}
	}
	if _, e = BindStepRequest(Request{Path: "/", Body: `{"{{data.id}}":true}`}, nil, scope); e == nil {
		t.Fatal("dynamic key accepted")
	}
	r = Request{Path: "/", Assertions: []Assertion{{Type: "status", Expected: "{{data.code}}"}}}
	if Validate(&r) != nil {
		t.Fatal("data status assertion rejected")
	}
}

func TestDataScopeAllowsEmptyAndNullWithoutWeakeningExtraction(t *testing.T) {
	r := Request{Path: "/orders/{{data.empty}}", Body: `{"a":"{{data.empty}}","b":"{{data.nil}}"}`, Assertions: []Assertion{{Type: "json", Path: "/b", Expected: "{{data.nil}}"}}}
	bound, e := BindStepRequest(r, nil, map[string]any{"data.empty": "", "data.nil": nil})
	if e != nil || bound.Body != `{"a":"","b":null}` || bound.Path != "/orders/" || bound.Assertions[0].Expected != "null" {
		t.Fatal(bound, e)
	}
	if _, ok := ScalarText(""); ok {
		t.Fatal("empty response extraction contract changed")
	}
}
