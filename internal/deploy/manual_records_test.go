package deploy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestSelectedManualRecordsBecomeFrozenChecklistAndPersonalTasks(t *testing.T) {
	st, m, req, first, w := standardManualFixture(t)
	// No network executor is installed or invoked: these are text records.
	second := &Change{Kind: "manual", Title: "外部系统更新", Summary: "上线后人工处理", Reference: "1. 在外部系统更新菜单\n2. 核对结果\n<script>not executable</script>", Owner: "alice"}
	excluded := &Change{Kind: "manual", Title: "下次再处理", Reference: "不在本次范围", Owner: "alice"}
	for _, c := range []*Change{second, excluded} {
		if err := st.CreateChange(c); err != nil {
			t.Fatal(err)
		}
		if err := st.SetChangeWorkItems(c.ID, []int64{w.ID}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.SetChangeReady(c.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	req.SelectedChangeIDs = []int64{first.ID, second.ID}
	draft, err := st.DraftDelivery(req)
	if err != nil || len(draft.Nodes) != 2 {
		t.Fatal(draft, err)
	}
	if draft.Nodes[1].Instructions != second.Summary+"\n\n"+second.Reference {
		t.Fatal("memo truncated", draft.Nodes[1])
	}
	tampered := append([]DeliveryNode{}, draft.Nodes...)
	tampered[1].Instructions = "different text"
	req.Nodes = tampered
	if _, _, err := st.ResolveDelivery(req, m); err == nil {
		t.Fatal("memo override accepted")
	}
	req.Nodes = nil
	req = standardSign(t, st, m, req)
	run, err := st.CreateDeliveryRun(req, "alice", m)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := st.ResearchTasks("alice", "todo")
	if err != nil || len(pending) != 1 || pending[0].NodeID != draft.Nodes[0].ID {
		t.Fatal(pending, err)
	}
	if other, _ := st.ResearchTasks("bob", "todo"); len(other) != 0 {
		t.Fatal("task leaked", other)
	}
	// Confirm only this step. No other record or application is executed.
	mux := http.NewServeMux()
	registerDeliveryRoutes(mux, st, m)
	body, _ := json.Marshal(map[string]any{"digest": run.Digest, "node_id": draft.Nodes[0].ID, "confirm_manual": true, "comment": "已核对业务结果"})
	endpoint := fmt.Sprintf("/api/delivery/runs/%d/advance", run.ID)
	if response := collaborationRequest(mux, "POST", endpoint, "alice", string(body)); response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	// Replaying the same confirmation cannot complete the next memo.
	if response := collaborationRequest(mux, "POST", endpoint, "alice", string(body)); response.Code != 409 {
		t.Fatal("replayed manual confirmation", response.Code)
	}
	run = st.DeliveryRun(run.ID)
	if err != nil || run.Steps[0].Status != "success" || run.Steps[1].Status != "pending" {
		t.Fatal(run, err)
	}
	if len(run.Steps[0].Reviews) != 1 || run.Steps[0].Reviews[0].Comment != "已核对业务结果" {
		t.Fatal("missing handling evidence")
	}
	done, err := st.ResearchTasks("alice", "done")
	if err != nil || len(done) != 1 {
		t.Fatal(done, err)
	}
	pending, err = st.ResearchTasks("alice", "todo")
	if err != nil || len(pending) != 1 || pending[0].NodeID != draft.Nodes[1].ID || !strings.Contains(pending[0].Instructions, "not executable") {
		t.Fatal(pending, err)
	}
	if exceptions, err := st.researchExceptionTasks("alice"); err != nil || len(exceptions) != 0 {
		t.Fatal("manual record mislabeled batch observation exception", exceptions, err)
	}
	// An existing run remains frozen if another draft is attached later.
	if len(st.DeliveryRun(run.ID).Plan.Scope.Changes) != 2 {
		t.Fatal("scope expanded")
	}
}

func TestCustomDeliveryIncludesSelectedManualRecordsAndCannotDropThem(t *testing.T) {
	st, m, req, c, _ := standardManualFixture(t)
	tpl := &DeliveryTemplate{Name: "Custom", Mode: "version", Owner: "alice", Nodes: []DeliveryNode{{ID: "gate", Kind: "manual", Name: "通用卡点", Instructions: "核对目标"}}}
	if err := st.SaveDeliveryTemplate(tpl); err != nil {
		t.Fatal(err)
	}
	req.StrategyKind = "custom"
	req.TemplateID = tpl.ID
	draft, err := st.DraftDelivery(req)
	if err != nil || len(draft.Nodes) != 2 || draft.Nodes[1].ChangeIDs[0] != c.ID {
		t.Fatal(draft, err)
	}
	req.Nodes = draft.Nodes[:1]
	if _, _, err := st.ResolveDelivery(req, m); err == nil {
		t.Fatal("selected record removed")
	}
	req.Nodes = []DeliveryNode{draft.Nodes[1], draft.Nodes[0]}
	if _, _, err := st.ResolveDelivery(req, m); err != nil {
		t.Fatal("valid reordered checklist blocked", err)
	}
	covered := includeManualRecords([]DeliveryNode{{ID: "mapped", Kind: "manual", Name: "已有节点", Instructions: "核对", ChangeIDs: []int64{c.ID}}}, draft.Scope)
	if len(covered) != 1 || !strings.Contains(covered[0].Instructions, c.Reference) {
		t.Fatal("mapped record duplicated or omitted", covered)
	}
	if err := normalizeChange(&Change{Kind: "manual", Title: "large", Reference: strings.Repeat("x", 65537)}); err == nil {
		t.Fatal("unbounded memo accepted")
	}
}
