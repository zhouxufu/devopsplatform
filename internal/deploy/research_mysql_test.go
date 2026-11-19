package deploy

import (
	"os"
	"strings"
	"testing"
)

func TestMySQLResearchMetadataWorkflowAndCaseSensitiveActors(t *testing.T) {
	dsn := os.Getenv("DEVOPS_TEST_RESEARCH_MYSQL_DSN")
	if dsn == "" {
		t.Skip("requires an empty disposable MySQL schema")
	}
	s, err := OpenDatabase(t.TempDir(), DatabaseConfig{Driver: "mysql", DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.UpsertEnvironment(Environment{Name: "dev", DisplayName: "开发"}); err != nil {
		t.Fatal(err)
	}
	yes := true
	w := &WorkItem{Title: "MySQL metadata", Source: "manual", Kind: "bug", Owner: "Alice", ReleaseRequired: &yes}
	if err = s.CreateWorkItem(w); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = saveIssueFields(tx, w.ID, ResearchIssueFields{Severity: "1", Priority: "2", Resolution: "backend_custom", ResolutionLabel: "后端代码修复", ReadAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	filtered, err := s.WorkItemsPage(WorkItemFilter{Severity: "1", Priority: "2", Resolution: "backend_custom", Order: "priority_asc"})
	if err != nil || filtered.Total != 1 || filtered.Items[0].ResolutionLabel != "后端代码修复" {
		t.Fatalf("MySQL source fields %+v %v", filtered, err)
	}
	page, err := s.WorkItemsPage(WorkItemFilter{ReleaseRequired: "eligible"})
	if err != nil || page.Total != 1 || page.Items[0].ReleaseRequired == nil {
		t.Fatalf("metadata query %+v %v", page, err)
	}
	c := &Change{Title: "check", Kind: "manual", Summary: "check", Owner: "Alice"}
	s.CreateChange(c)
	s.SetChangeWorkItems(c.ID, []int64{w.ID})
	tpl := &DeliveryTemplate{Name: "MySQL flow", Mode: "version", Workbench: "research", Owner: "Alice", Nodes: []DeliveryNode{{ID: "confirm", Kind: "manual", Name: "confirm", Instructions: "check", AssigneeSource: "work_item_resolvers", ApprovalMode: "all"}}}
	if err = saveUnifiedPreparationFixture(s, tpl); err != nil {
		t.Fatal(err)
	}
	req := DeliveryRequest{ClientRequestID: strings.Repeat("a", 32), TemplateID: tpl.ID, SourceType: "work_items", WorkItemIDs: []int64{w.ID}, Environment: "dev"}
	run, err := s.CreateResearchFlow(req, nil, []string{"Alice"}, "Alice")
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := s.CreateResearchFlow(req, nil, []string{"Alice"}, "Alice")
	if err != nil || repeated.ID != run.ID {
		t.Fatal("submit dedup failed", err)
	}
	if _, err = s.ReviewResearchTask(run.ID, "confirm", "alice", run.Digest, "", true, []string{"Alice"}); err == nil {
		t.Fatal("case-insensitive identity authorized")
	}
	if _, err = s.ReviewResearchTask(run.ID, "confirm", "Alice", run.Digest, "", true, []string{"Alice"}); err != nil {
		t.Fatal(err)
	}
	candidates, err := s.WorkCandidates(WorkCandidateFilter{WorkItemFilter: WorkItemFilter{Severity: "1", Order: "priority_desc"}, Environment: "dev"})
	if err != nil || candidates.Total != 1 || candidates.Items[0].Resolution != "backend_custom" || candidates.Items[0].SuccessfulDelivery {
		t.Fatalf("MySQL preparation is not publication %+v %v", candidates, err)
	}
	for _, actor := range []string{"Alice", "alice"} {
		if _, err = s.db.Exec(`INSERT INTO research_credentials(actor,connection_name,endpoint_digest,account,secret) VALUES(?,'issues','digest',?,'encrypted')`, actor, actor); err != nil {
			t.Fatal("credential identities collided", err)
		}
	}
}
