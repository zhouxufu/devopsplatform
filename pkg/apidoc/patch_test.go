package apidoc

import (
	"encoding/json"
	"strings"
	"testing"
)

const patchDoc = `{"openapi":"3.0.3","info":{"title":"Orders","version":"1"},"paths":{"/a":{"get":{"summary":"A","responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"#/components/schemas/A"}}}}}},"post":{"responses":{"204":{"description":"done"}}}}},"components":{"schemas":{"A":{"type":"object","properties":{"id":{"$ref":"#/components/schemas/ID"},"nested":{"type":"object","properties":{"old":{"type":"string"}}}}},"ID":{"type":"integer","example":9223372036854775807}}}}`

func TestAtomicDocumentPatchAndPrecision(t *testing.T) {
	out, e := ApplyPatch([]byte(patchDoc), []Patch{{Op: "test", Path: "/paths/~1a/get/summary", Value: json.RawMessage(`"A"`)}, {Op: "replace", Path: "/paths/~1a/get/summary", Value: json.RawMessage(`"B"`)}, {Op: "add", Path: "/paths/~1a/get/tags", Value: json.RawMessage(`["订单"]`)}, {Op: "add", Path: "/paths/~1a/get/tags/-", Value: json.RawMessage(`"查询"`)}})
	if e != nil || !strings.Contains(string(out), "9223372036854775807") || !strings.Contains(string(out), `"summary":"B"`) {
		t.Fatal(string(out), e)
	}
	for _, p := range []Patch{{Op: "replace", Path: "/missing", Value: json.RawMessage(`1`)}, {Op: "add", Path: "/absent/key", Value: json.RawMessage(`1`)}, {Op: "remove", Path: "/paths/~1a/get/responses"}, {Op: "test", Path: "/info/version", Value: json.RawMessage(`2`)}, {Op: "add", Path: "/info/~2bad", Value: json.RawMessage(`1`)}, {Op: "move", Path: "/info/title", Value: json.RawMessage(`1`)}} {
		if _, e := ApplyPatch([]byte(patchDoc), []Patch{p}); e == nil {
			t.Fatal("accepted invalid patch", p)
		}
	}
	array := strings.Replace(patchDoc, `"summary":"A"`, `"summary":"A","tags":["a","b"]`, 1)
	out, e = ApplyPatch([]byte(array), []Patch{{Op: "remove", Path: "/paths/~1a/get/tags/0"}})
	if e != nil || strings.Contains(string(out), `"tags":["a"`) {
		t.Fatal(string(out), e)
	}
}
func TestEntityChangesSharedRefsAndNestedFields(t *testing.T) {
	before, e := DocumentEntities([]byte(patchDoc))
	if e != nil {
		t.Fatal(e)
	}
	raw := strings.Replace(patchDoc, `"old":{"type":"string"}`, `"old":{"type":"string"},"new":{"type":"boolean"}`, 1)
	after, e := DocumentEntities([]byte(raw))
	if e != nil {
		t.Fatal(e)
	}
	for k, v := range before {
		if v.Field != nil && v.Field.Name == "nested" && v.Digest != after[k].Digest {
			t.Fatal("parent claimed child addition", k)
		}
	}
	changed, e := DocumentEntities([]byte(strings.Replace(patchDoc, `"type":"integer","example"`, `"type":"string","example"`, 1)))
	if e != nil {
		t.Fatal(e)
	}
	idChanged := false
	for k, v := range before {
		if v.Field != nil && v.Field.Name == "id" {
			idChanged = v.Digest != changed[k].Digest
		}
	}
	if !idChanged || before["operation:get /a"].Digest == changed["operation:get /a"].Digest || before["operation:post /a"].Digest != changed["operation:post /a"].Digest {
		t.Fatal("shared ref impact not isolated")
	}
}
