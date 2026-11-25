package apitest

import (
	"logplatform/pkg/dubborpc"
	"testing"
)

func TestDubboRequestAssertionsAndBindings(t *testing.T) {
	r := Request{Mode: "dubbo", RPC: &dubborpc.Method{Interface: "example.House", Method: "get", ParameterTypes: []string{"java.lang.Long"}}, Body: `[9223372036854775806]`, Assertions: []Assertion{{Type: "rpc_success", Expected: "true"}}}
	if e := Validate(&r); e != nil {
		t.Fatal(e)
	}
	checks := Evaluate(r.Assertions, Result{Protocol: "dubbo", RPCSuccess: true})
	if len(checks) != 1 || !checks[0].Passed {
		t.Fatal(checks)
	}
	bad := r
	bad.Headers = map[string]string{"retries": "2"}
	if Validate(&bad) == nil {
		t.Fatal("retry override allowed")
	}
	bad = r
	bad.Assertions = []Assertion{{Type: "status", Expected: "200"}}
	if Validate(&bad) == nil {
		t.Fatal("HTTP assertion allowed")
	}
	bad = r
	bad.Body = `[]`
	if Validate(&bad) == nil {
		t.Fatal("arity mismatch allowed")
	}
	r.Headers = map[string]string{"customToken": "{{var.token}}"}
	bound, e := BindStepRequest(r, map[string]string{"token": "secret"}, nil)
	if e != nil || bound.Headers["customToken"] != "secret" {
		t.Fatal(e, bound.Headers)
	}
	if ValidateExtractions([]Extraction{{Name: "id", Source: "header", Path: "X-ID"}}, "dubbo") == nil {
		t.Fatal("header extraction allowed")
	}
}

func TestDubboSanitizedSignature(t *testing.T) {
	r := Request{Mode: "dubbo", Path: "ignored#fragment", RPC: &dubborpc.Method{Interface: "com.example.Service", Method: "find", ParameterTypes: []string{"java.lang.Long"}}}
	got := SanitizeRequest(r, nil)
	if got.Path != "com.example.Service#find(java.lang.Long)" {
		t.Fatalf("signature lost: %s", got.Path)
	}
}
