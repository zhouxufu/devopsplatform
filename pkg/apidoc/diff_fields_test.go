package apidoc

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRemovedObjectAndArrayDiffUsesLeavesBeforeTruncation(t *testing.T) {
	var before any
	decoder := json.NewDecoder(strings.NewReader(`{"id":210000000000000001,"items":[{"enabled":false,"value":0}],"empty":null,"long":"` + strings.Repeat("x", 3000) + `"}`))
	decoder.UseNumber()
	if e := decoder.Decode(&before); e != nil {
		t.Fatal(e)
	}
	c := OperationChange{}
	collectFields("/example", before, true, nil, false, &c)
	found := map[string]FieldChange{}
	for _, f := range c.Fields {
		found[f.Path] = f
		if !f.BeforePresent || f.AfterPresent {
			t.Fatal(f)
		}
	}
	if len(c.Fields) != 5 || found["/example/id"].Before != "210000000000000001" || found["/example/items/0/enabled"].Before != "false" || found["/example/items/0/value"].Before != "0" || found["/example/empty"].Before != "null" {
		t.Fatalf("not individual exact fields: %+v", c)
	}
	if !strings.Contains(found["/example/long"].Before, "已截断") {
		t.Fatal("long leaf must retain truncation notice")
	}
}

func TestArrayDiffPreservesPresenceAndBound(t *testing.T) {
	c := OperationChange{}
	collectFields("/parameters", []any{map[string]any{"name": "id", "required": false}}, true, []any{map[string]any{"name": "id", "required": true}, map[string]any{"name": "extra"}}, true, &c)
	if len(c.Fields) != 2 || c.Fields[0].Path != "/parameters/0/required" || c.Fields[1].BeforePresent {
		t.Fatal(c)
	}
	large := make([]any, 1000)
	for i := range large {
		large[i] = i
	}
	c = OperationChange{}
	collectFields("/example", nil, false, large, true, &c)
	if len(c.Fields) != 100 || !c.Truncated {
		t.Fatal(c)
	}
}
