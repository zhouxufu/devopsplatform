package deploy

import (
	"strings"
	"testing"
)

func TestWorkDeliveryNacosValidationHasExactItemAndEntry(t *testing.T) {
	st, _, remote, _ := nacosFixture(t)
	before := len(st.Changes("", "", "", 100))
	for _, key := range []string{"", "feature.enabled"} {
		req := &WorkDeliverySave{Changes: []Change{
			{Kind: "manual", Title: "valid first row", Reference: "verify"},
			{Kind: "nacos", Title: "invalid second row", Service: "orders", Nacos: &NacosChange{ResourceAlias: "config", Group: "DEFAULT_GROUP", Format: "yaml", Entries: []NacosEntry{{Key: "feature.enabled", Action: "set", Value: "true"}, {Key: key, Action: "set", Value: "false"}}}},
		}}
		err := st.CreateChangeBatch(req, "alice")
		if err == nil || !strings.Contains(err.Error(), "第 2 项：第 2 个配置项") {
			t.Fatalf("missing exact location: %v", err)
		}
		if len(st.Changes("", "", "", 100)) != before {
			t.Fatal("failed validation partially saved other row")
		}
	}
	if _, posts := remote.snapshot(); posts != 0 {
		t.Fatal("validation published configuration")
	}
}
