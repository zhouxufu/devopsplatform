package deploy

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestChangeDeploymentOneConfirmation(t *testing.T) {
	for _, authority := range []string{"page", "mcp", "drift"} {
		t.Run(authority, func(t *testing.T) {
			s, m, pr, git, work, bare, _ := serviceBuildFixture(t, true)
			git("-C", work, "checkout", "-b", "feature/commute")
			if e := os.WriteFile(filepath.Join(work, "feature"), []byte("commute"), 0600); e != nil {
				t.Fatal(e)
			}
			git("-C", work, "add", "feature")
			git("-C", work, "-c", "user.email=fixture@example.test", "-c", "user.name=Fixture", "commit", "-m", "feature")
			git("-C", work, "push", bare, "feature/commute")
			c := &Change{Service: "orders", Kind: "code", Owner: "alice", Title: "commute", Reference: "feature/commute", Summary: "fixture", RiskLevel: "low", RollbackPlan: "restore artifact"}
			if e := s.CreateChange(c); e != nil {
				t.Fatal(e)
			}
			if _, e := s.SetChangeReady(c.ID, true); e != nil {
				t.Fatal(e)
			}
			tpl := &DeliveryTemplate{Name: "Development delivery", Mode: "version", Owner: "alice", Preparation: []DeliveryNode{{ID: "scope", Kind: "manual", Name: "范围", Instructions: "核对范围", AssigneeSource: "change_submitters", ApprovalMode: "all"}, {ID: "code", Kind: "code-prepare", Name: "合并构建", ApplicationScope: "delivery", DefaultCodePolicy: &CodePolicy{Mode: "integration", TargetBranch: "shared-test"}}}, Nodes: []DeliveryNode{{ID: "deploy", Kind: "deploy", Name: "部署"}}}
			if e := s.SaveDeliveryTemplate(tpl); e != nil {
				t.Fatal(e)
			}
			policy, _ := s.deliveryPolicy("test")
			policy.WorkflowTemplateID = tpl.ID
			if e := s.saveDeliveryPolicy("test", policy); e != nil {
				t.Fatal(e)
			}
			mux := http.NewServeMux()
			registerDeliveryMCPRoutes(mux, s, m, pr)
			registerCodePreparationRoutes(mux, s, pr)
			registerServiceDeploymentRoutes(mux, s, pr, m)
			r := httptest.NewRequest("POST", "/", nil)
			r.Header.Set("X-User", "alice")
			r.Header.Set("X-Reviewer-Directory", `["alice"]`)
			r.Header.Set("X-Work-Loop-Authority", "page")
			if authority == "mcp" {
				r.Header.Set("X-Work-Loop-Authority", "mcp")
				r.Header.Set("X-Work-Loop-Key-ID", "fixture")
			}
			spec := ServiceDeploymentSpec{Mode: "change", Service: "orders", Environment: "test", Reason: "fixture", ChangeID: c.ID}
			if authority == "mcp" {
				req := DeliveryRequest{StrategyKind: "custom", TemplateID: tpl.ID, SourceType: "changes", SourceChangeIDs: []int64{c.ID}, Environment: "test", Reason: "fixture"}
				run, e := s.CreateResearchFlow(req, nil, []string{"alice"}, "alice")
				if e != nil {
					t.Fatal(e)
				}
				if _, e = s.ReviewResearchTask(run.ID, "scope", "alice", run.Digest, "scope only", true, []string{"alice"}); e != nil {
					t.Fatal(e)
				}
				if _, e = s.AdvanceResearchFlow(r.Context(), run.ID, "alice", pr); e != nil {
					t.Fatal(e)
				}
				spec.PreparationID = run.ID
			}
			previewSpec := spec
			if authority == "mcp" {
				previewSpec.Mode = "build" // Exact request shape used by older MCP clients.
				previewSpec.ChangeID = 0
				previewSpec.PreparationID = 0
				previewSpec.Branch = "feature/commute"
			}
			var grant ServiceDeploymentGrant
			if e := loopCall(mux, r, "/api/service-deployments/preview", previewSpec, &grant); e != nil {
				t.Fatal(e)
			}
			if grant.Spec.ChangeID != c.ID || grant.Spec.PreparationID != spec.PreparationID {
				t.Fatal("original independent scope not recovered", grant.Spec)
			}
			id := strings.Repeat("c", 32)
			var task ServiceDeployment
			submit := map[string]any{"id": id, "spec": spec, "expected_digest": grant.Digest}
			if e := loopCall(mux, r, "/api/service-deployments", submit, &task); e != nil {
				t.Fatal(e)
			}
			if e := loopCall(mux, r, "/api/service-deployments", submit, &task); e != nil {
				t.Fatal("lost submit receipt", e)
			}
			if authority == "drift" {
				git("-C", work, "push", bare, "HEAD:shared-test")
			}
			deadline := time.Now().Add(40 * time.Second)
			for task.State == "running" && time.Now().Before(deadline) {
				if e := loopCall(mux, r, "/api/service-deployments/"+id+"/advance", map[string]any{"revision": task.Revision}, &task); e != nil {
					t.Fatal(e)
				}
				time.Sleep(50 * time.Millisecond)
			}
			if authority == "drift" {
				if task.State != "blocked" || task.RunID != 0 || task.ReleaseID != 0 || len(s.ReleaseOrders("orders", 8)) != 0 {
					t.Fatal("baseline drift did not stop before build", task)
				}
				return
			}
			if task.State != "success" {
				t.Fatalf("%s: %s %s", task.State, task.Stage, task.Message)
			}
			if task.RunID == 0 || task.ArtifactID == 0 || task.DeliveryID == 0 || task.ReleaseID == 0 || spec.PreparationID > 0 && task.PreparationID != spec.PreparationID {
				t.Fatal("lost original receipts", task)
			}
			o := s.GetReleaseOrder(task.ReleaseID)
			if o.ArtifactID != task.ArtifactID || len(s.ReleaseOrders("orders", 8)) != 1 {
				t.Fatal("unexpected release", o)
			}
			if r := s.GetRun(task.RunID); r.Branch != git("--git-dir="+bare, "rev-parse", "shared-test") {
				t.Fatalf("wrong build commit %s", r.Branch)
			}
		})
	}
}
