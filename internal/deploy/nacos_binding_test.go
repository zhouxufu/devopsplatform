package deploy

import (
	"context"
	"strings"
	"testing"
)

func TestNacosEnvironmentDataIDAndApprovalDrift(t *testing.T) {
	st, m, mock, original := nacosFixture(t)
	resources := st.ApplicationResources("orders")
	for i := range resources.Bindings {
		if resources.Bindings[i].Environment == "prod" {
			resources.Bindings[i].Config = map[string]string{"data_id": "orders.yaml", "group": "DEFAULT_GROUP"}
		}
	}
	if err := st.SaveApplicationResources("orders", resources); err != nil {
		t.Fatal(err)
	}
	// No override: existing changes still use their original default target.
	legacy, err := st.prepareNacosNode(context.Background(), "legacy", []int64{original.ID}, "test")
	if err != nil || legacy.Configs[0].DataID != "orders.yaml" {
		t.Fatalf("legacy default lost: %v", err)
	}
	change := &Change{Kind: "nacos", Title: "Environment-neutral patch", Service: "orders", Owner: "alice", Nacos: &NacosChange{ResourceAlias: "config", Group: "IGNORED_DEFAULT", Format: "yaml", Entries: []NacosEntry{{Key: "feature.enabled", Action: "set", Value: "true"}}}}
	if err := st.CreateChange(change); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetChangeReady(change.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := st.prepareNacosNode(context.Background(), "missing", []int64{change.ID}, "test"); err == nil || !strings.Contains(err.Error(), "未指定 Nacos Data ID") {
		t.Fatalf("missing environment target not rejected: %v", err)
	}
	run := nacosRun(t, st, m, change, "prod")
	pinned := run.Plan.NacosPlans[0].Configs[0]
	if pinned.DataID != "orders.yaml" || pinned.Group != "DEFAULT_GROUP" {
		t.Fatalf("binding target not used: %+v", pinned)
	}
	if saved := st.GetConnection("qa-nacos"); saved.Config["data_id"] != "" || saved.Config["group"] != "" {
		t.Fatal("shared connection mutated")
	}
	if _, err := st.ReviewDeliveryRun(run.ID, "reviewer", run.Digest, true); err != nil {
		t.Fatal(err)
	}
	resources = st.ApplicationResources("orders")
	for i := range resources.Bindings {
		if resources.Bindings[i].Environment == "prod" {
			resources.Bindings[i].Config["data_id"] = "different.yaml"
		}
	}
	if err := st.SaveApplicationResources("orders", resources); err != nil {
		t.Fatal(err)
	}
	got, err := st.AdvanceDeliveryRun(run.ID, "alice", false, m)
	if err == nil && (got.Steps[0].Status != "failed" || !strings.Contains(got.Steps[0].Detail, "环境绑定变化")) {
		t.Fatalf("approved target drift allowed: %+v", got)
	}
	if _, posts := mock.snapshot(); posts != 0 {
		t.Fatal("target drift published a config")
	}
}

func TestNacosBindingValidationAndResolution(t *testing.T) {
	st, _, _, original := nacosFixture(t)
	for _, config := range []map[string]string{{"namespace": "another"}, {"data_id": "a\nb"}, {"group": strings.Repeat("x", 256)}} {
		resources := st.ApplicationResources("orders")
		resources.Bindings[0].Config = config
		if err := st.SaveApplicationResources("orders", resources); err == nil {
			t.Fatalf("invalid target accepted: %v", config)
		}
	}
	for _, environmentID := range []string{"dev.yaml", "test.yaml", "prod.yaml"} {
		connection := &ResourceConnection{Config: map[string]string{"data_id": environmentID, "group": "APP"}}
		id, group, err := nacosResolvedTarget(connection, original.Nacos)
		if err != nil || id != environmentID || group != "APP" {
			t.Fatal("environment-specific resolution failed", id, group, err)
		}
	}
}

func TestNacosEnvironmentBindingPublishAndRestore(t *testing.T) {
	st, m, mock, change := nacosFixture(t)
	resources := st.ApplicationResources("orders")
	for i := range resources.Bindings {
		resources.Bindings[i].Config = map[string]string{"data_id": "orders.yaml", "group": "DEFAULT_GROUP"}
	}
	if err := st.SaveApplicationResources("orders", resources); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetChangeReady(change.ID, false); err != nil {
		t.Fatal(err)
	}
	change = st.GetChange(change.ID)
	change.Nacos.DataID, change.Nacos.Group = "fallback.yaml", "FALLBACK"
	if err := st.UpdateChange(change); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetChangeReady(change.ID, true); err != nil {
		t.Fatal(err)
	}
	run := nacosRun(t, st, m, change, "test")
	if _, err := st.AdvanceDeliveryRun(run.ID, "alice", false, m); err != nil {
		t.Fatal(err)
	}
	if _, posts := mock.snapshot(); posts != 1 {
		t.Fatal("resolved binding did not publish to mock target")
	}
	pinned := run.Plan.NacosPlans[0].Configs[0]
	recovery := &Change{Kind: "nacos", Title: "Restore override", Service: "orders", Owner: "alice", Nacos: &NacosChange{ResourceAlias: pinned.Alias, DataID: pinned.DataID, Group: pinned.Group, Format: pinned.Format, Entries: nacosInverseEntries(pinned.Diff), Restore: &NacosRestore{RunID: run.ID, ChangeID: change.ID}}}
	if err := st.CreateChange(recovery); err != nil {
		t.Fatal(err)
	}
	if _, err := st.prepareNacosNode(context.Background(), "restore", []int64{recovery.ID}, "test"); err != nil {
		t.Fatal("resolved recovery failed", err)
	}
}
