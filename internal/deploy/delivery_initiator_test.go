package deploy

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestDeliveryInitiatorConfirmsTeamChanges(t *testing.T) {
	for _, source := range []string{"changes", "work_items"} {
		for _, mode := range []string{"all", "any"} {
			t.Run(source+"/"+mode, func(t *testing.T) {
				st, _, req, change, work := standardManualFixture(t)
				tpl := &DeliveryTemplate{Name: "Team flow", Mode: "version", Owner: "alice",
					Preparation: []DeliveryNode{{ID: "scope", Kind: "manual", Name: "Delivery scope", Instructions: "Check selected team scope", AssigneeSource: "delivery_initiator", ApprovalMode: mode, Approvers: []string{"alice"}}},
					Nodes:       []DeliveryNode{{ID: "publish", Kind: "manual", Name: "Publish", Instructions: "Not executed"}},
				}
				if err := st.SaveDeliveryTemplate(tpl); err != nil {
					t.Fatal(err)
				}
				req.StrategyKind, req.TemplateID = "custom", tpl.ID
				if source == "changes" {
					req.SourceType, req.SourceChangeIDs, req.WorkItemIDs = "changes", []int64{change.ID}, nil
				}
				// The same team content can be delivered by different authenticated members.
				for _, actor := range []string{"bob", "carol"} {
					run, err := st.CreateResearchFlow(req, nil, []string{"alice", "bob", "carol"}, actor)
					if err != nil {
						t.Fatal(err)
					}
					node := run.Plan.Nodes[0]
					if !reflect.DeepEqual(node.Approvers, []string{actor}) || run.Plan.Scope.Changes[0].Owner != "alice" {
						t.Fatalf("initiator or original change author lost: %+v", run)
					}
					if source == "work_items" && !reflect.DeepEqual(node.AssignedWorkItems[actor], []int64{work.ID}) {
						t.Fatal(node)
					}
					if _, err = st.ReviewResearchTask(run.ID, node.ID, "alice", run.Digest, "cannot sign for initiator", true, []string{"alice", "bob", "carol"}); err == nil {
						t.Fatal("change author signed for delivery initiator")
					}
					if _, err = st.ReviewResearchTask(run.ID, node.ID, actor, run.Digest, "checked team scope", true, []string{"alice", "bob", "carol"}); err != nil {
						t.Fatal(err)
					}
					fresh := st.DeliveryRun(run.ID)
					if fresh.Status != "success" || fresh.Digest != run.Digest || fresh.Steps[0].Reviews[0].Actor != actor {
						t.Fatal(fresh)
					}
				}
				if st.GetChange(change.ID).Owner != "alice" || st.DeliveryTemplate(tpl.ID).Preparation[0].Approvers[0] != "alice" {
					t.Fatal("planning rewrote shared definitions")
				}
			})
		}
	}
}

func TestDeliveryInitiatorEnvironmentAndIdentityBoundaries(t *testing.T) {
	for _, scenario := range []string{"disabled", "empty", "protected", "production"} {
		t.Run(scenario, func(t *testing.T) {
			st, _, req, _, _ := standardManualFixture(t)
			tpl := &DeliveryTemplate{Name: "Team flow", Mode: "version", Owner: "alice", Preparation: []DeliveryNode{{ID: "scope", Kind: "manual", Name: "Scope", Instructions: "Check", AssigneeSource: "delivery_initiator", ApprovalMode: "all"}}, Nodes: []DeliveryNode{{ID: "publish", Kind: "manual", Name: "Publish", Instructions: "Not executed"}}}
			if err := st.SaveDeliveryTemplate(tpl); err != nil {
				t.Fatal(err)
			}
			req.StrategyKind, req.TemplateID = "custom", tpl.ID
			actor := "bob"
			enabled := []string{"alice", "bob"}
			if scenario == "empty" {
				actor = ""
			}
			if scenario == "disabled" {
				enabled = []string{"alice"}
			}
			if scenario == "protected" || scenario == "production" {
				env := st.GetEnvironment(req.Environment)
				env.Protected, env.Approvers = scenario == "protected", "alice"
				if scenario == "production" {
					env.Purpose = "production"
				}
				if err := st.UpsertEnvironment(*env); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := st.ResearchFlowPlan(req, nil, enabled, actor); err == nil {
				t.Fatal("invalid initiator policy accepted")
			}
		})
	}
}

func TestDeliveryInitiatorMCPStopsAtFormalApproval(t *testing.T) {
	st, m, run, _ := scopeMCPFixture(t, func(tpl *DeliveryTemplate) { tpl.Preparation[0].AssigneeSource = "delivery_initiator" })
	mux := http.NewServeMux()
	registerDeliveryMCPRoutes(mux, st, m, nil)
	body := map[string]any{"revision": run.Revision, "digest": run.Digest, "node_id": "scope", "comment": "My frozen delivery scope", "confirmation_id": strings.Repeat("b", 32), "work_item_ids": run.Plan.Nodes[0].AssignedWorkItems["alice"]}
	path := fmt.Sprintf("/api/delivery/mcp/runs/%d/confirm-scope", run.ID)
	if out := loopRequest(mux, "POST", path, "bob", body); out.Code != 403 {
		t.Fatal(out.Code, out.Body.String())
	}
	// loopRequest's directory contains alice/reviewer; the following formal
	// approval belongs to bob and must stay untouched by this self-confirmation.
	if out := loopRequest(mux, "POST", path, "alice", body); out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
	fresh := st.DeliveryRun(run.ID)
	if fresh.Status != "pending_approval" || fresh.Steps[1].Status == "success" || len(fresh.Steps[1].Reviews) != 0 {
		t.Fatal(fresh)
	}
	if out := loopRequest(mux, "POST", path, "alice", body); out.Code != 409 {
		t.Fatal("stale replay accepted", out.Code)
	}
}

func TestWorkLoopUsesFrozenDeliveryInitiator(t *testing.T) {
	st, h, task, req := workLoopFixture(t, "bug")
	tpl := st.DeliveryTemplate(req.TemplateID)
	tpl.Preparation[0].AssigneeSource = "delivery_initiator"
	if err := st.SaveDeliveryTemplate(tpl); err != nil {
		t.Fatal(err)
	}
	// Refresh the explicit grant to the modified template before any run starts.
	grant, err := st.previewWorkLoop(context.Background(), task.Grant.Spec, task.Owner)
	if err != nil {
		t.Fatal(err)
	}
	task.Grant = *grant
	if err = st.saveWorkLoop(task); err != nil {
		t.Fatal(err)
	}
	if out := loopRequest(h, "POST", "/api/work-loops/"+task.ID+"/start", task.Owner, map[string]any{"revision": task.Revision, "change_ids": task.current().ChangeIDs, "summary": "checked"}); out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
	for i := 0; i < 2; i++ {
		task, err = st.workLoop(task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if out := loopRequest(h, "POST", "/api/work-loops/"+task.ID+"/advance", task.Owner, map[string]any{"revision": task.Revision}); out.Code != 200 {
			t.Fatal(out.Code, out.Body.String())
		}
	}
	task, _ = st.workLoop(task.ID)
	run := st.DeliveryRun(task.current().PreparationID)
	if run.Steps[0].Status != "success" || run.Steps[1].Status != "pending" {
		t.Fatal("grant failed to confirm, or dispatched code", run)
	}
}
