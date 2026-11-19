package apidoc

import (
	"encoding/json"
	"strings"
	"testing"
)

func fieldDoc(t *testing.T, fields map[string]any, parameters []any) []byte {
	t.Helper()
	v := map[string]any{"openapi": "3.0.3", "info": map[string]string{"title": "fields", "version": "1"}, "paths": map[string]any{"/orders": map[string]any{"get": map[string]any{"parameters": parameters, "responses": map[string]any{"200": map[string]any{"description": "ok", "content": map[string]any{"application/json": map[string]any{"schema": map[string]string{"$ref": "#/components/schemas/Order"}}}}}}}}, "components": map[string]any{"schemas": map[string]any{"Order": map[string]any{"type": "object", "properties": fields}}}}
	raw, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}
func TestAddedDocumentFieldsReferencesArraysAndParameterReorder(t *testing.T) {
	param := func(name string) any {
		return map[string]any{"name": name, "in": "query", "schema": map[string]string{"type": "string"}}
	}
	before := fieldDoc(t, map[string]any{"id": map[string]string{"type": "integer"}}, []any{param("old")})
	after := fieldDoc(t, map[string]any{"id": map[string]string{"type": "string"}, "a/b~c": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"value": map[string]string{"type": "string"}}}}}, []any{param("new"), param("old")})
	added, e := AddedDocumentFields(before, after)
	if e != nil {
		t.Fatal(e)
	}
	if len(added) != 3 {
		t.Fatalf("not only new fields: %+v", added)
	}
	got := map[string]DocumentField{}
	for _, f := range added {
		got[f.Name] = f
		if f.Name == "id" || f.Name == "old" {
			t.Fatal("modification/reorder reported as addition", f)
		}
	}
	if got["a/b~c"].Pointer != "#/components/schemas/Order/properties/a~1b~0c" || got["a/b~c.value"].In != "response" || got["new"].Pointer != "#/paths/~1orders/get/parameters/0" {
		t.Fatal(got)
	}
	again, _ := AddedDocumentFields(before, after)
	a, _ := json.Marshal(added)
	b, _ := json.Marshal(again)
	if string(a) != string(b) {
		t.Fatal("unstable preview")
	}
}
func TestDocumentFieldsCyclesAndExamplesAreBounded(t *testing.T) {
	raw := fieldDoc(t, map[string]any{"node": map[string]string{"$ref": "#/components/schemas/Order"}, "example": map[string]any{"type": "string", "example": map[string]any{"properties": map[string]any{"notAField": true}}}}, nil)
	fields, e := DocumentFields(raw)
	if e != nil {
		t.Fatal(e)
	}
	if len(fields) > 6 {
		t.Fatal(fields)
	}
	for _, f := range fields {
		if strings.Contains(f.Name, "notAField") {
			t.Fatal("example became field", f)
		}
	}
}
