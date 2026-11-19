package deploy

import (
	"reflect"
	"strings"
	"testing"
)

func TestOmitEmptyDeliveryResourceNodesPreservesGatesAndDependencies(t *testing.T) {
	nodes := []DeliveryNode{
		{ID: "review", Kind: "approval", Name: "Release review"},
		{ID: "nacos", Kind: "nacos", Name: "Nacos", DependsOn: []string{"review"}},
		{ID: "sql", Kind: "mysql", Name: "SQL", DependsOn: []string{"nacos"}},
		{ID: "redis", Kind: "redis", Name: "Redis", DependsOn: []string{"review"}},
		{ID: "deploy", Kind: "deploy", Name: "Deploy", DependsOn: []string{"sql", "redis"}},
	}
	before := digestJSON(nodes)
	out, warnings := omitEmptyDeliveryResourceNodes(nodes, &DeliveryScope{Type: "changes", Changes: []Change{{ID: 1, Kind: "code", Service: "orders"}}})
	if len(out) != 2 || len(warnings) != 3 || out[0].ID != "review" || !reflect.DeepEqual(out[1].DependsOn, []string{"review"}) {
		t.Fatalf("lost gate/dependencies: %+v, %v", out, warnings)
	}
	if err := validateNodeDependencies(out); err != nil {
		t.Fatal(err)
	}
	if digestJSON(nodes) != before {
		t.Fatal("template was mutated")
	}
	if !hasApprovalNodes(&DeliveryPlan{Nodes: out}) {
		t.Fatal("explicit approval was removed")
	}
	if deliveryReviewRequired(&DeliveryPlan{Nodes: out}) {
		t.Fatal("empty SQL caused write review")
	}
	if !deliveryReviewRequired(&DeliveryPlan{Nodes: out, Environment: Environment{Protected: true}}) {
		t.Fatal("protected review was bypassed")
	}
}

func TestOmitEmptyDeliveryResourceNodesKeepsActualAndExplicitChanges(t *testing.T) {
	for _, kind := range []string{"mysql", "nacos", "redis"} {
		t.Run(kind, func(t *testing.T) {
			nodes := []DeliveryNode{{ID: "auto", Name: "Auto", Kind: kind}, {ID: "orders", Name: "Orders", Kind: kind, Service: "orders"}, {ID: "billing", Name: "Billing", Kind: kind, Service: "billing"}, {ID: "explicit", Name: "Explicit", Kind: kind, ChangeIDs: []int64{999}}}
			scope := &DeliveryScope{Type: "changes", Changes: []Change{{ID: 1, Kind: kind, Service: "orders"}}}
			out, warnings := omitEmptyDeliveryResourceNodes(nodes, scope)
			if len(out) != 3 || len(warnings) != 1 || out[0].ID != "auto" || out[1].ID != "orders" || out[2].ID != "explicit" {
				t.Fatalf("real or explicit reference removed: %+v %v", out, warnings)
			}
			if kind != "nacos" && !deliveryReviewRequired(&DeliveryPlan{Nodes: out}) {
				t.Fatal("write review bypassed")
			}
			unchanged, w := omitEmptyDeliveryResourceNodes(nodes, nil)
			if !reflect.DeepEqual(unchanged, nodes) || len(w) != 0 {
				t.Fatal("unscoped/fixed node skipped")
			}
		})
	}
}

func TestDeliveryEmptyResourceNodesCodeOnlyRelease(t *testing.T) {
	st, manager, req := deliveryFixture(t)
	c := &Change{Kind: "code", Title: "Code-only fix", Service: "orders", Reference: "feature/fix", Owner: "alice"}
	if err := st.CreateChange(c); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetChangeReady(c.ID, true); err != nil {
		t.Fatal(err)
	}
	v := &ReleaseVersion{Version: "code-only", Title: "No resource changes", Owner: "alice"}
	if err := st.CreateReleaseVersion(v); err != nil {
		t.Fatal(err)
	}
	if err := st.SetReleaseVersionChanges(v.ID, []int64{c.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetReleaseVersionReady(v.ID); err != nil {
		t.Fatal(err)
	}
	tpl := &DeliveryTemplate{Name: "Combined workflow", Mode: "version", Owner: "alice", Nodes: []DeliveryNode{{ID: "nacos", Kind: "nacos", Name: "Nacos"}, {ID: "sql", Kind: "mysql", Name: "SQL", DependsOn: []string{"nacos"}}, {ID: "deploy", Kind: "deploy", Name: "Deploy", DependsOn: []string{"sql"}}}}
	if err := st.SaveDeliveryTemplate(tpl); err != nil {
		t.Fatal(err)
	}
	req.TemplateID, req.VersionID = tpl.ID, v.ID
	plan, _, err := st.ResolveDelivery(req, manager)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Blockers) != 0 || len(plan.Nodes) != 1 || plan.Nodes[0].Kind != "deploy" || strings.Count(strings.Join(plan.Warnings, "\n"), "本次交付范围无匹配变更") != 2 || len(plan.SQLPlans) != 0 || len(plan.NacosPlans) != 0 {
		t.Fatalf("code-only blocked or acquired resource work: %+v", plan)
	}
	run, err := st.CreateDeliveryRun(req, "alice", manager)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "approved" || len(run.Steps) != 1 || run.Steps[0].StartedAt != 0 {
		t.Fatalf("wrong run or execution triggered: %+v", run)
	}
	if len(st.DeliveryTemplate(tpl.ID).Nodes) != 3 || len(run.Plan.Template.Nodes) != 3 {
		t.Fatal("template snapshot was rewritten")
	}
	req.Environment = "prod"
	protected, err := st.CreateDeliveryRun(req, "alice", manager)
	if err != nil {
		t.Fatal(err)
	}
	if protected.Status != "pending_approval" {
		t.Fatal("protected approval bypassed", protected.Status)
	}
	if len(st.ReleaseOrders("", 100)) != 0 {
		t.Fatal("preview/create started a release")
	}
	// An explicit invalid reference remains an error, not an empty optional node.
	tpl.Nodes[1].ChangeIDs = []int64{999999}
	if err := st.SaveDeliveryTemplate(tpl); err != nil {
		t.Fatal(err)
	}
	req.Environment = "test"
	invalid, _, err := st.ResolveDelivery(req, manager)
	if err == nil && len(invalid.Blockers) == 0 {
		t.Fatal("explicit outside-scope reference was skipped")
	}
	if err == nil && !strings.Contains(strings.Join(invalid.Blockers, " "), "范围外") {
		t.Fatal(invalid.Blockers)
	}
}
