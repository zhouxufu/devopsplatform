package apidoc

import (
	"encoding/json"
	"strings"
	"testing"
)

func contractFixture() map[string]any {
	var v map[string]any
	json.Unmarshal([]byte(`{"openapi":"3.0.3","info":{"title":"订单","version":"1"},"paths":{"/orders":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"#/components/schemas/Order"}}}}}}},"/old":{"delete":{"responses":{"200":{"description":"ok"}}}}},"components":{"schemas":{"Order":{"type":"object","properties":{"id":{"type":"integer"},"next":{"$ref":"#/components/schemas/Order"}}}}}}`), &v)
	return v
}
func docBytes(v any) []byte { b, _ := json.Marshal(v); return b }
func TestDocumentComparisonTracksSharedSchemaCyclesAndEndpoints(t *testing.T) {
	before := contractFixture()
	after := contractFixture()
	paths := after["paths"].(map[string]any)
	delete(paths, "/old")
	paths["/new"] = map[string]any{"post": map[string]any{"responses": map[string]any{"201": map[string]any{"description": "created"}}}}
	order := after["components"].(map[string]any)["schemas"].(map[string]any)["Order"].(map[string]any)
	order["properties"].(map[string]any)["id"].(map[string]any)["type"] = "string"
	diff, e := Compare(docBytes(before), docBytes(after))
	if e != nil || diff.Summary != (ChangeSummary{Added: 1, Removed: 1, Modified: 1}) {
		t.Fatal(diff, e)
	}
	found := false
	for _, c := range diff.Changes {
		if c.ID == "get /orders" {
			for _, f := range c.Fields {
				if f.Path == "#/components/schemas/Order/properties/id/type" && f.Before == `"integer"` && f.After == `"string"` {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal(diff)
	}
	same, e := Compare(docBytes(after), append([]byte(" \n"), docBytes(after)...))
	if e != nil || same.Summary.Unchanged != 2 || len(same.Changes) != 0 {
		t.Fatal(same, e)
	}
	first, e := Compare(nil, docBytes(after))
	if e != nil || first.Summary.Added != 2 {
		t.Fatal(first, e)
	}
}
func TestDocumentComparisonIncludesInheritedParametersSecurityAndLargeNumbers(t *testing.T) {
	b := contractFixture()
	a := contractFixture()
	for _, root := range []map[string]any{b, a} {
		root["security"] = []any{map[string]any{"token": []any{}}}
		root["components"].(map[string]any)["securitySchemes"] = map[string]any{"token": map[string]any{"type": "apiKey", "in": "header", "name": "X-Key"}}
	}
	a["components"].(map[string]any)["securitySchemes"].(map[string]any)["token"].(map[string]any)["name"] = "Authorization"
	diff, e := Compare(docBytes(b), docBytes(a))
	if e != nil || diff.Summary.Modified != 2 {
		t.Fatal(diff, e)
	}
	braw := strings.Replace(string(docBytes(b)), `"type":"integer"`, `"type":"integer","example":9007199254740992`, 1)
	araw := strings.Replace(braw, "9007199254740992", "9007199254740993", 1)
	diff, e = Compare([]byte(braw), []byte(araw))
	if e != nil || diff.Summary.Modified != 1 {
		t.Fatal(diff, e)
	}
}
func TestDocumentComparisonBoundsFieldDisplay(t *testing.T) {
	b := contractFixture()
	a := contractFixture()
	v := a["paths"].(map[string]any)["/orders"].(map[string]any)["get"].(map[string]any)
	v["description"] = strings.Repeat("long", 5000)
	diff, e := Compare(docBytes(b), docBytes(a))
	if e != nil || len(diff.Changes) != 1 || len(diff.Changes[0].Fields[0].After) > 2150 {
		t.Fatal(diff, e)
	}
}
