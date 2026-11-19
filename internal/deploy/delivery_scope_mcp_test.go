package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func scopeMCPFixture(t *testing.T, configure ...func(*DeliveryTemplate)) (*Store, *ReleaseManager, *DeliveryRun, *Change) {
	t.Helper()
	st, m, req, c, _ := standardManualFixture(t)
	tpl := &DeliveryTemplate{Name: "Scope self confirmation", Mode: "version", Owner: "alice", Preparation: []DeliveryNode{
		{ID: "scope", Kind: "manual", Name: "Scope", Instructions: "Confirm frozen scope", AssigneeSource: "specified", Approvers: []string{"alice", "bob"}, ApprovalMode: "all"},
		{ID: "review", Kind: "approval", Name: "Formal review", Approvers: []string{"bob"}, ApprovalMode: "all"},
	}, Nodes: []DeliveryNode{{ID: "deploy", Kind: "manual", Name: "Publish", Instructions: "Not executed"}}}
	for _, apply := range configure {
		apply(tpl)
	}
	if err := st.SaveDeliveryTemplate(tpl); err != nil {
		t.Fatal(err)
	}
	if err := st.saveDeliveryPolicy("test", DeliveryPolicy{WorkflowTemplateID: tpl.ID}); err != nil {
		t.Fatal(err)
	}
	req.StrategyKind, req.TemplateID = "custom", tpl.ID
	run, err := st.CreateResearchFlow(req, nil, []string{"alice", "bob"}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	return st, m, run, c
}

func TestMCPSelfScopeConfirmationBoundaries(t *testing.T) {
	cases := []string{"success", "other-admin", "stale", "wrong-node", "wrong-digest", "disabled-person", "protected", "production", "changed-scope", "approval", "manual-verification", "release-not-preparation", "empty-comment", "foreign-work-item"}
	for _, kind := range cases {
		t.Run(kind, func(t *testing.T) {
			st, m, run, c := scopeMCPFixture(t)
			if kind == "release-not-preparation" {
				other, manager, req, _, _ := standardManualFixture(t)
				ordinary, err := other.CreateDeliveryRun(standardSign(t, other, manager, req), "alice", manager)
				if err != nil {
					t.Fatal(err)
				}
				st, m, run = other, manager, ordinary
			}
			actor, directory := "alice", `["alice","bob"]`
			body := map[string]any{"revision": run.Revision, "digest": run.Digest, "node_id": "scope", "comment": "Confirmed this frozen test scope", "confirmation_id": strings.Repeat("a", 32), "work_item_ids": []int64{}}
			switch kind {
			case "other-admin":
				actor = "mallory"
			case "stale":
				body["revision"] = run.Revision + 1
			case "wrong-node":
				body["node_id"] = "review"
			case "wrong-digest":
				body["digest"] = "different"
			case "disabled-person":
				directory = `["alice"]`
			case "empty-comment":
				body["comment"] = " "
			case "foreign-work-item":
				body["work_item_ids"] = []int64{9999}
			case "protected", "production":
				env := st.GetEnvironment("test")
				env.Protected = kind == "protected"
				if kind == "production" {
					env.Purpose = "production"
				}
				env.Approvers = "bob"
				if err := st.UpsertEnvironment(*env); err != nil {
					t.Fatal(err)
				}
			case "changed-scope":
				st.SetChangeReady(c.ID, false)
				c = st.GetChange(c.ID)
				c.Summary = "Changed after preview"
				if err := st.UpdateChange(c); err != nil {
					t.Fatal(err)
				}
			case "approval":
				run.Status = "pending_approval"
				run.Steps[0].Kind = "approval"
				run.Plan.Nodes[0].Kind = "approval"
			case "manual-verification":
				run.Steps[0].Kind = "verify"
				run.Plan.Nodes[0].Kind = "verify"
				run.Plan.Nodes[0].Verification = &VerificationSpec{Mode: "manual"}
			}
			if kind == "approval" || kind == "manual-verification" {
				if err := st.saveDeliveryRun(run); err != nil {
					t.Fatal(err)
				}
				body["revision"] = run.Revision
			}
			mux := http.NewServeMux()
			registerDeliveryMCPRoutes(mux, st, m, nil)
			send := func() *httptest.ResponseRecorder {
				raw, _ := json.Marshal(body)
				req := httptest.NewRequest("POST", fmt.Sprintf("/api/delivery/mcp/runs/%d/confirm-scope", run.ID), bytes.NewReader(raw))
				req.Header.Set("X-User", actor)
				req.Header.Set("X-Role", "admin")
				req.Header.Set("X-Reviewer-Directory", directory)
				out := httptest.NewRecorder()
				mux.ServeHTTP(out, req)
				return out
			}
			out := send()
			after := st.DeliveryRun(run.ID)
			if kind != "success" {
				if out.Code == 200 || len(after.Steps[0].Reviews) != 0 {
					t.Fatalf("boundary bypassed %d %s", out.Code, out.Body)
				}
				return
			}
			if out.Code != 200 {
				t.Fatal(out.Code, out.Body)
			}
			if after.Status != "waiting_confirmation" || after.Steps[1].Status == "success" || len(after.Steps[0].Reviews) != 1 || after.Steps[0].Reviews[0].Actor != "alice" || after.Steps[0].Reviews[0].At == 0 {
				t.Fatal("co-sign or audit lost", after)
			}
			if out = send(); out.Code != 409 {
				t.Fatal("replayed confirmation", out.Code)
			}
			// The other named person may confirm their own scope, but formal approval
			// remains pending and is never signed by this endpoint.
			body["revision"], actor = after.Revision, "bob"
			if out = send(); out.Code != 200 {
				t.Fatal(out.Code, out.Body)
			}
			after = st.DeliveryRun(run.ID)
			if after.Status != "pending_approval" || len(after.Steps[1].Reviews) != 0 {
				t.Fatal("auto-approved next stage", after)
			}
		})
	}
}

func TestScopeConfirmationUsesCheckedRevision(t *testing.T) {
	st, _, run, _ := scopeMCPFixture(t)
	stale := st.DeliveryRun(run.ID)
	if _, err := st.reviewResearchTaskState(run, "scope", "alice", run.Digest, "First", true, []string{"alice", "bob"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.reviewResearchTaskState(stale, "scope", "bob", stale.Digest, "Concurrent stale", true, []string{"alice", "bob"}); err == nil {
		t.Fatal("stale co-sign overwrote the newer review")
	}
	after := st.DeliveryRun(run.ID)
	if len(after.Steps[0].Reviews) != 1 || after.Steps[0].Reviews[0].Actor != "alice" {
		t.Fatal(after)
	}
}

func TestMCPScopeAssignedItemsAndAnySign(t *testing.T) {
	for _, mode := range []string{"assigned", "any"} {
		t.Run(mode, func(t *testing.T) {
			st, m, run, _ := scopeMCPFixture(t, func(tpl *DeliveryTemplate) {
				if mode == "assigned" {
					tpl.Preparation[0].AssigneeSource = "work_item_assignees"
				} else {
					tpl.Preparation[0].ApprovalMode = "any"
				}
			})
			mux := http.NewServeMux()
			registerDeliveryMCPRoutes(mux, st, m, nil)
			body := map[string]any{"revision": run.Revision, "digest": run.Digest, "node_id": "scope", "comment": "Confirmed assigned scope", "confirmation_id": strings.Repeat("b", 32)}
			send := func() int {
				raw, _ := json.Marshal(body)
				req := httptest.NewRequest("POST", fmt.Sprintf("/api/delivery/mcp/runs/%d/confirm-scope", run.ID), bytes.NewReader(raw))
				req.Header.Set("X-User", "alice")
				req.Header.Set("X-Reviewer-Directory", `["alice","bob"]`)
				out := httptest.NewRecorder()
				mux.ServeHTTP(out, req)
				return out.Code
			}
			if mode == "assigned" {
				if send() == 200 {
					t.Fatal("omitted frozen assignment accepted")
				}
				body["work_item_ids"] = []int64{99999}
				if send() == 200 {
					t.Fatal("foreign item accepted")
				}
				body["work_item_ids"] = []int64{run.Plan.Scope.WorkItems[0].ID}
			}
			if code := send(); code != 200 {
				t.Fatal(code)
			}
			after := st.DeliveryRun(run.ID)
			if after.Status != "pending_approval" || len(after.Steps[1].Reviews) != 0 {
				t.Fatal("confirmation bypassed approval", after)
			}
			if mode == "assigned" && len(after.Steps[0].Reviews[0].WorkItemIDs) != 1 {
				t.Fatal("assignment receipt missing")
			}
		})
	}
}
