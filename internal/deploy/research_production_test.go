package deploy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestEnvironmentPurposeIndependentOfProtectionAndLegacyClients(t *testing.T) {
	s, _ := researchLiveFixture(t, 2)
	for _, env := range []Environment{
		{Name: "customer-east", DisplayName: "华东客户运行区", Purpose: "production"},
		{Name: "verify", DisplayName: "验证区", Purpose: "test", Protected: true, Approvers: "qa"},
	} {
		if err := s.UpsertEnvironment(env); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UpsertEnvironment(Environment{Name: "customer-east", DisplayName: "客户运行区（更名）"}); err != nil {
		t.Fatal(err)
	}
	if env := s.GetEnvironment("customer-east"); env.Purpose != "production" || env.Protected {
		t.Fatal("legacy update lost purpose or forced protection", env)
	}
	if env := s.GetEnvironment("verify"); env.Purpose != "test" || !env.Protected {
		t.Fatal("protected test misclassified", env)
	}
	if err := s.UpsertEnvironment(Environment{Name: "oops", DisplayName: "Invalid", Purpose: "prod"}); err == nil {
		t.Fatal("invalid purpose accepted")
	}
	if _, err := s.db.Exec(`UPDATE environments SET purpose='' WHERE name='prod'`); err != nil {
		t.Fatal(err)
	}
	if s.GetEnvironment("prod").Purpose != "production" {
		t.Fatal("legacy prod not compatible")
	}
}

func TestResearchLiveAllProductsRetainsAllowlistAndCursorOrder(t *testing.T) {
	s, state := researchLiveFixture(t, 60)
	state.mu.Lock()
	for i, row := range state.rows {
		if i%3 == 0 {
			row["product"] = "2"
		}
		if i%7 == 0 {
			row["product"] = "99"
		}
	}
	state.mu.Unlock()
	f := researchLiveFilter{Connection: "issues", WorkItemFilter: WorkItemFilter{PageSize: 20}}
	seen := map[string]bool{}
	for page := 1; page <= 2; page++ {
		f.Page = page
		out, err := s.ResearchLivePage(context.Background(), "member", f)
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Items) != 20 || out.TotalExact {
			t.Fatal("incorrect scoped page/total", out)
		}
		for _, item := range out.Items {
			if seen[item.ExternalID] {
				t.Fatal("cursor duplicate")
			}
			seen[item.ExternalID] = true
			for _, row := range state.rows {
				if row["id"] == item.ExternalID && row["product"] == "99" {
					t.Fatal("product scope escaped")
				}
			}
		}
		f.Cursor = out.NextCursor
	}
	state.mu.Lock()
	found := false
	for _, p := range state.predicates["sync"] {
		if p.Field == "product" && p.Value == "all" {
			found = true
		}
	}
	state.mu.Unlock()
	if !found {
		t.Fatal("did not use native all-product search")
	}
	if _, err := s.ResearchLivePage(context.Background(), "member", researchLiveFilter{Connection: "issues", WorkItemFilter: WorkItemFilter{Page: 2}}); err == nil {
		t.Fatal("scoped cursor omitted")
	}
	if out, err := s.ResearchLivePage(context.Background(), "member", researchLiveFilter{Connection: "issues", Product: 2}); err != nil || len(out.Items) == 0 || !out.TotalExact {
		t.Fatal("single product regression", err)
	}
}

func TestResearchPendingReleaseProductionOnlyAndExplicitIntent(t *testing.T) {
	s, _ := researchLiveFixture(t, 12)
	if err := s.DeleteEnvironment("prod"); err != nil { // Store retains at least one environment.
		if err = s.UpsertEnvironment(Environment{Name: "verify", DisplayName: "测试", Purpose: "test", Protected: true, Approvers: "qa"}); err != nil {
			t.Fatal(err)
		}
		if err = s.DeleteEnvironment("prod"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UpsertEnvironment(Environment{Name: "customer-east", DisplayName: "正式区", Purpose: "production"}); err != nil {
		t.Fatal(err)
	}
	yes, no := true, false
	items := map[string]*WorkItem{}
	for id, value := range map[string]*bool{"1": &yes, "2": &yes, "3": &yes, "4": &no, "5": nil} {
		item, err := s.AttachResearchBug(context.Background(), "alice", "issues", id)
		if err != nil {
			t.Fatal(err)
		}
		items[id] = item
		if value != nil {
			meta := ResearchMetadata{ReleaseRequired: value}
			if !*value {
				meta.NoReleaseReason = "无需上线"
			}
			if err = s.SetResearchMetadata(item.ID, meta, "alice"); err != nil {
				t.Fatal(err)
			}
		}
	}
	record := func(item *WorkItem, env, status, workbench string) {
		plan, _ := json.Marshal(DeliveryPlan{Environment: Environment{Name: env}, Template: DeliveryTemplate{Workbench: workbench}, Scope: &DeliveryScope{WorkItems: []WorkItem{{ID: item.ID}}}})
		if _, err := s.db.Exec(`INSERT INTO delivery_runs(plan,digest,status,execution,env_secrets,owner,approver,created_at,revision) VALUES(?,'frozen',?,'[]','{}','alice','',1,1)`, string(plan), status); err != nil {
			t.Fatal(err)
		}
	}
	record(items["1"], "verify", "success", "") // Even a protected test is not上线.
	record(items["1"], "customer-east", "failed", "")
	record(items["2"], "customer-east", "success", "")
	record(items["3"], "customer-east", "success", "research") // Preparation is not deployment.
	out, err := s.ResearchLivePage(context.Background(), "member", researchLiveFilter{Connection: "issues", Delivery: "pending_release", Environment: "verify", WorkItemFilter: WorkItemFilter{ReleaseRequired: "eligible"}})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, item := range out.Items {
		ids[item.ExternalID] = true
	}
	if len(ids) != 2 || !ids["1"] || !ids["3"] || strings.Join(out.ReleaseEnvironments, ",") != "customer-east" {
		t.Fatal("pending release mixed intent or test success", ids, out.ReleaseEnvironments)
	}
	unknown, err := s.ResearchLivePage(context.Background(), "member", researchLiveFilter{Connection: "issues", WorkItemFilter: WorkItemFilter{ReleaseRequired: "unknown"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range unknown.Items {
		if item.ReleaseRequired != nil {
			t.Fatal("undecided filter mixed explicit intent")
		}
	}
	if err = s.DeleteEnvironment("customer-east"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResearchLivePage(context.Background(), "member", researchLiveFilter{Connection: "issues", Delivery: "pending_release"}); err == nil {
		t.Fatal("guessed protected test as production")
	}
}

func TestStandardNonProductionDefaultAndExplicitGatePreserved(t *testing.T) {
	for _, purpose := range []string{"development", "test"} {
		plan := DeliveryPlan{StrategyKind: "standard", Environment: Environment{Purpose: purpose}, Nodes: []DeliveryNode{{Kind: "mysql"}, {Kind: "redis"}}}
		if deliveryReviewRequired(&plan) {
			t.Fatal("non-production forced into generic approval")
		}
		plan.Environment.Protected = true
		if !deliveryReviewRequired(&plan) {
			t.Fatal("explicit protection bypassed")
		}
		plan.Environment.Protected = false
		plan.StrategyKind = "custom"
		if !deliveryReviewRequired(&plan) {
			t.Fatal("existing custom review changed")
		}
		plan.Template.Workbench = "dbms"
		if !deliveryReviewRequired(&plan) {
			t.Fatal("separate workbench approval disabled")
		}
	}
}

func TestStandardTestDeliveryRedisExecutesOnlyAfterExplicitStart(t *testing.T) {
	s, m, server, change, _ := redisFixture(t)
	env := *s.GetEnvironment("test")
	env.Purpose = "test"
	env.Protected = false
	if err := s.UpsertEnvironment(env); err != nil {
		t.Fatal(err)
	}
	req := DeliveryRequest{StrategyKind: "standard", SourceType: "changes", SelectedChangeIDs: []int64{change.ID}, Environment: "test", Reason: "fixture only"}
	req = standardSign(t, s, m, req)
	run, err := s.CreateDeliveryRun(req, "alice", m)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "approved" || server.count("UNLINK") != 0 {
		t.Fatal("default review or automatic write", run.Status)
	}
	for _, n := range run.Plan.Nodes {
		if n.Kind == "approval" {
			t.Fatal("unexpected default approval")
		}
	}
	run, err = s.AdvanceDeliveryRun(run.ID, "alice", false, m)
	if err != nil || run.Steps[0].Status != "success" || server.count("UNLINK") != 1 {
		t.Fatal("explicit execution failed", err)
	}
}
