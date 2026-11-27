package devtools

import "testing"

func TestDeliveryMCPOrdersOnlyFrozenNodesWithDependencies(t *testing.T) {
	generated := []any{map[string]any{"id": "gis", "kind": "deploy"}, map[string]any{"id": "gateway", "kind": "deploy", "service": "gateway"}}
	requested := []any{map[string]any{"id": "gis"}, map[string]any{"id": "gateway", "depends_on": []any{"gis"}}}
	nodes, e := deliveryOrderedNodes(generated, requested)
	if e != nil || mapValue(nodes[1])["service"] != "gateway" || mapValue(generated[1])["depends_on"] != nil {
		t.Fatal(nodes, e)
	}
	for _, bad := range []map[string]any{{"id": "new"}, {"id": "gis", "depends_on": []any{1}}, {"id": "gis", "service": "other"}} {
		if _, e := deliveryOrderedNodes(generated, []any{bad}); e == nil {
			t.Fatal(bad)
		}
	}
}
