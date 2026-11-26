package deploy

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJointCodeAndNacosPreparationKeepsScopeUntilReady(t *testing.T) {
	store, _, nacos, config := nacosFixture(t)
	if _, err := store.SetChangeReady(config.ID, false); err != nil {
		t.Fatal(err)
	}
	code := &Change{Service: "orders", Kind: "code", Owner: "alice", Title: "使用新配置", Reference: "feature/orders"}
	if err := store.CreateChange(code); err != nil {
		t.Fatal(err)
	}
	template := &DeliveryTemplate{
		Name: "配置与服务联合发布", Mode: "version", Owner: "alice",
		Preparation: []DeliveryNode{
			{ID: "scope", Kind: "manual", Name: "核对范围", Instructions: "核对", AssigneeSource: "change_submitters", ApprovalMode: "all"},
			{ID: "code", Kind: "code-prepare", Name: "准备代码", ApplicationScope: "delivery", DefaultCodePolicy: &CodePolicy{Mode: "integration", TargetBranch: "test-release"}},
		},
		Nodes: []DeliveryNode{{ID: "config", Kind: "nacos", Name: "更新配置"}, {ID: "deploy", Kind: "deploy", Name: "部署"}},
	}
	if err := store.SaveDeliveryTemplate(template); err != nil {
		t.Fatal(err)
	}
	policy, err := store.deliveryPolicy("test")
	if err != nil {
		t.Fatal(err)
	}
	policy.WorkflowTemplateID = template.ID
	if err := store.saveDeliveryPolicy("test", policy); err != nil {
		t.Fatal(err)
	}
	ids := []int64{config.ID, code.ID}
	request := DeliveryRequest{SourceType: "changes", SourceChangeIDs: ids, SelectedChangeIDs: ids, TemplateID: template.ID, Environment: "test", StrategyKind: "custom"}
	for pending := 2; pending >= 0; pending-- {
		plan, err := store.ResearchFlowPlan(request, nil, []string{"alice"}, "alice")
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Scope.Changes) != 2 || plan.Scope.Changes[0].ID != config.ID || plan.Scope.Changes[1].ID != code.ID || len(plan.Blockers) != pending {
			t.Fatal(plan.Scope, plan.Blockers)
		}
		guidance := strings.Join(plan.Warnings, " ")
		if strings.Contains(guidance, fmt.Sprintf("#%d、#%d", config.ID, code.ID)) != (pending > 0) {
			t.Fatal("unexpected joint readiness guidance", guidance)
		}
		if pending > 0 {
			if _, err := store.SetChangeReady(ids[pending-1], true); err != nil {
				t.Fatal(err)
			}
		}
	}
	run, err := store.CreateResearchFlow(request, nil, []string{"alice"}, "alice")
	if err != nil || len(run.Plan.Scope.Changes) != 2 {
		t.Fatal("joint preparation", run, err)
	}
	if _, writes := nacos.snapshot(); writes != 0 {
		t.Fatal("preview published configuration")
	}
}

func TestChangeDeploymentOneConfirmation(t *testing.T) {
	for _, authority := range []string{"page", "mcp", "drift", "draft", "linked", "direct", "mixed", "conflict", "sql"} {
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
			if authority == "linked" || authority == "direct" {
				w := &WorkItem{Title: "isolated", Kind: "bug", Source: "manual", Owner: "alice"}
				if e := s.CreateWorkItem(w); e != nil {
					t.Fatal(e)
				}
				if e := s.SetChangeWorkItems(c.ID, []int64{w.ID}); e != nil {
					t.Fatal(e)
				}
			}
			if authority != "draft" {
				if _, e := s.SetChangeReady(c.ID, true); e != nil {
					t.Fatal(e)
				}
			}
			tpl := &DeliveryTemplate{Name: "Development delivery", Mode: "version", Owner: "alice", Preparation: []DeliveryNode{{ID: "scope", Kind: "manual", Name: "范围", Instructions: "核对范围", AssigneeSource: "change_submitters", ApprovalMode: "all"}, {ID: "code", Kind: "code-prepare", Name: "合并构建", ApplicationScope: "delivery", DefaultCodePolicy: &CodePolicy{Mode: "integration", TargetBranch: "shared-test"}}}, Nodes: []DeliveryNode{{ID: "deploy", Kind: "deploy", Name: "部署"}}}
			var db *sql.DB
			var table string
			var before, after *Change
			if authority == "sql" {
				var src *Store
				src, _, db, table = sqlTargetFixture(t)
				if err := s.UpsertConnection(*src.GetConnection("qa-mysql")); err != nil {
					t.Fatal(err)
				}
				if err := s.SaveApplicationResources("orders", src.ApplicationResources("orders")); err != nil {
					t.Fatal(err)
				}
				env := *s.GetEnvironment("test")
				env.Approvers = "reviewer"
				if err := s.UpsertEnvironment(env); err != nil {
					t.Fatal(err)
				}
				before = sqlChangeFixture(t, s, fmt.Sprintf("CREATE TABLE %s (id bigint NOT NULL, PRIMARY KEY(id))", table))
				after = sqlChangeFixture(t, s, fmt.Sprintf("INSERT INTO %s (id) VALUES (7)", table))
				tpl.Nodes = append([]DeliveryNode{{ID: "sql", Name: "SQL", Kind: "mysql", Service: "orders"}}, tpl.Nodes...)
			}
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
			if authority == "mcp" || authority == "draft" {
				r.Header.Set("X-Work-Loop-Authority", "mcp")
				r.Header.Set("X-Work-Loop-Key-ID", "fixture")
			}
			spec := ServiceDeploymentSpec{Mode: "change", Service: "orders", Environment: "test", Reason: "fixture", ChangeID: c.ID}
			if authority == "sql" {
				r.Header.Set("X-Reviewer-Directory", `["alice","reviewer"]`)
				spec.Service = ""
				spec.ChangeID = 0
				a, b := tpl.Nodes[0], tpl.Nodes[0]
				a.ChangeIDs = []int64{before.ID}
				b.ID = "after"
				b.SQLGroup = "sql"
				b.ChangeIDs = []int64{after.ID}
				spec.Delivery = &DeliveryRequest{SourceType: "changes", Environment: "test", SourceChangeIDs: []int64{c.ID, before.ID, after.ID}, Nodes: []DeliveryNode{a, tpl.Nodes[1], b}}
			}
			if authority == "conflict" {
				git("-C", work, "checkout", "shared-test")
				if err := os.WriteFile(filepath.Join(work, "feature"), []byte("different"), 0600); err != nil {
					t.Fatal(err)
				}
				git("-C", work, "add", "feature")
				git("-C", work, "-c", "user.email=fixture@example.test", "-c", "user.name=Fixture", "commit", "-m", "other edit")
				git("-C", work, "push", bare, "shared-test")
				spec.Delivery = &DeliveryRequest{SourceType: "changes", Environment: "test", SourceChangeIDs: []int64{c.ID}}
			}
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
			if authority == "direct" {
				git("-C", work, "push", bare, "HEAD:shared-test")
				spec.Delivery = &DeliveryRequest{Environment: "test", SourceType: "work_items", WorkItemIDs: []int64{s.GetChange(c.ID).WorkItems[0].ID}, SelectedChangeIDs: []int64{c.ID}, CodeModes: map[string]string{"orders": "direct"}}
			}
			if authority == "mixed" {
				sv := *s.GetService("orders")
				sv.Name = "billing"
				sv.ApplicationID = ""
				if err := s.UpsertService(sv); err != nil {
					t.Fatal(err)
				}
				target := *s.GetServiceEnvironment("orders", "test")
				target.Service = "billing"
				target.DeployConfig["script"] = strings.ReplaceAll(target.DeployConfig["script"], "= orders", "= billing")
				if err := s.UpsertServiceEnvironment(target); err != nil {
					t.Fatal(err)
				}
				cb := &Change{Service: "billing", Kind: "code", Owner: "alice", Title: "billing", Reference: "shared-test", Summary: "fixture", RiskLevel: "low", RollbackPlan: "restore"}
				if err := s.CreateChange(cb); err != nil {
					t.Fatal(err)
				}
				if _, err := s.SetChangeReady(cb.ID, true); err != nil {
					t.Fatal(err)
				}
				spec.Service = ""
				spec.ChangeID = 0
				spec.Delivery = &DeliveryRequest{Environment: "test", SourceType: "changes", SourceChangeIDs: []int64{c.ID, cb.ID}, CodeModes: map[string]string{"orders": "merge", "billing": "direct"}}
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
			if authority == "sql" && (!grant.Change.Batch.RequiresReview || len(grant.Change.Batch.EnvironmentReviewers) != 1 || grant.Change.Batch.EnvironmentReviewers[0] != "reviewer") {
				t.Fatal("missing SQL reviewer in preview")
			}
			if grant.Spec.ChangeID != spec.ChangeID || grant.Spec.PreparationID != spec.PreparationID {
				t.Fatal("original independent scope not recovered", grant.Spec)
			}
			if authority == "draft" {
				if grant.Change.MarkReadyDraftDigest == "" || s.GetChange(c.ID).Status != ChangeDraft {
					t.Fatal("preview did not freeze original draft")
				}
				other := r.Clone(r.Context())
				other.Header.Set("X-User", "bob")
				if err := loopCall(mux, other, "/api/service-deployments/preview", spec, &ServiceDeploymentGrant{}); err == nil {
					t.Fatal("accepted foreign draft")
				}
			}
			id := strings.Repeat("c", 32)
			var task ServiceDeployment
			submit := map[string]any{"id": id, "spec": grant.Spec, "expected_digest": grant.Digest}
			if authority == "draft" {
				c.Summary = "updated after preview"
				if err := s.UpdateChange(c); err != nil {
					t.Fatal(err)
				}
				if err := loopCall(mux, r, "/api/service-deployments", submit, &task); err == nil || s.GetChange(c.ID).Status != ChangeDraft {
					t.Fatal("stale confirmation promoted draft")
				}
				if err := loopCall(mux, r, "/api/service-deployments/preview", spec, &grant); err != nil {
					t.Fatal(err)
				}
				submit["expected_digest"] = grant.Digest
			}
			if e := loopCall(mux, r, "/api/service-deployments", submit, &task); e != nil {
				t.Fatal(e)
			}
			if e := loopCall(mux, r, "/api/service-deployments", submit, &task); e != nil {
				t.Fatal("lost submit receipt", e)
			}
			if s.GetChange(c.ID).Status != ChangeReady {
				t.Fatal("original change not ready after confirmation")
			}
			if authority == "mixed" {
				var recovered struct {
					OriginalTaskID string `json:"original_task_id"`
				}
				if err := loopCall(mux, r, "/api/service-deployments/preview", spec, &recovered); err != nil || recovered.OriginalTaskID != id {
					t.Fatal("repeat preview did not recover original", err, recovered)
				}
			}
			expectedCommit := ""
			if authority == "direct" {
				expectedCommit = git("-C", work, "rev-parse", "HEAD")
				git("-C", work, "-c", "user.email=fixture@example.test", "-c", "user.name=Fixture", "commit", "--allow-empty", "-m", "later change")
				git("-C", work, "push", bare, "HEAD:shared-test")
			}
			if authority == "drift" {
				git("-C", work, "push", bare, "HEAD:shared-test")
			}
			recovered := false
			reviewed := false
			deadline := time.Now().Add(65 * time.Second)
			for task.State == "running" && time.Now().Before(deadline) {
				if e := loopCall(mux, r, "/api/service-deployments/"+id+"/advance", map[string]any{"revision": task.Revision}, &task); e != nil {
					t.Fatal(e)
				}
				if task.Stage == "waiting_conflict" && authority == "conflict" && !recovered {
					if task.RunID != 0 || task.ReleaseID != 0 {
						t.Fatal("conflict dispatched work", task)
					}
					original := s.CodePreparation(task.CodeID)
					if original == nil || original.PreparationRunID != task.PreparationID {
						t.Fatal("lost conflict task")
					}
					git("-C", work, "-c", "user.email=fixture@example.test", "-c", "user.name=Fixture", "merge", "--no-ff", "-Xtheirs", "feature/commute", "-m", "reviewed resolution")
					sha := git("-C", work, "rev-parse", "HEAD")
					git("-C", work, "push", bare, "HEAD:shared-test")
					if err := loopCall(mux, r, "/api/delivery/mcp/code-preparations/"+task.CodeID+"/resume", map[string]any{"confirm": true, "expected_revision": original.Revision, "target_commits": []string{sha}}, nil); err != nil {
						t.Fatal(err)
					}
					recovered = true
				}
				if authority == "sql" && task.DeliveryID > 0 && !reviewed {
					run := s.DeliveryRun(task.DeliveryID)
					if run.Status == "pending_approval" {
						if _, err := s.ReviewDeliveryRun(run.ID, "reviewer", run.Digest, true); err != nil {
							t.Fatal(err)
						}
						reviewed = true
					}
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
			if authority == "mixed" {
				if len(task.Artifacts) != 2 || len(task.Releases) != 2 {
					t.Fatal("incomplete receipts", task)
				}
				for svc, id := range task.Releases {
					o := s.GetReleaseOrder(id)
					if o.Service != svc || o.ArtifactID != task.Artifacts[svc] || o.Status != "success" {
						t.Fatal("wrong receipt", svc, o)
					}
				}
				return
			}
			if authority == "conflict" && !recovered {
				t.Fatal("conflict path not reached")
			}
			if authority == "sql" {
				run := s.DeliveryRun(task.DeliveryID)
				var value int
				if !reviewed || len(run.Steps) != 3 || run.Steps[0].NodeID != "sql" || run.Steps[1].Kind != "deploy" || run.Steps[2].NodeID != "after" || db.QueryRow("SELECT id FROM `"+table+"`").Scan(&value) != nil || value != 7 {
					t.Fatal("ordered release incomplete", run.Steps, value)
				}
			}
			o := s.GetReleaseOrder(task.ReleaseID)
			if o.ArtifactID != task.ArtifactID || len(s.ReleaseOrders("orders", 8)) != 1 {
				t.Fatal("unexpected release", o)
			}
			if expectedCommit == "" {
				expectedCommit = git("--git-dir="+bare, "rev-parse", "shared-test")
			}
			if r := s.GetRun(task.RunID); r.Branch != expectedCommit {
				t.Fatalf("wrong build commit %s", r.Branch)
			}
		})
	}
}
