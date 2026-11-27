package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"
)

func TestDependencyReleaseRetainsScopeSkipAndManualReceipt(t *testing.T) {
	for _, action := range []string{"skip", "manual_done"} {
		t.Run(action, func(t *testing.T) {
			st, m, req := deliveryFixture(t)
			tpl := st.DeliveryTemplate(req.TemplateID)
			tpl.Nodes = []DeliveryNode{{ID: "gis", Name: "GIS", Kind: "deploy", Service: "orders"}, {ID: "verify", Name: "业务核对", Kind: "manual", Instructions: "核实 GIS 版本", DependsOn: []string{"gis"}}}
			if err := st.SaveDeliveryTemplate(tpl); err != nil {
				t.Fatal(err)
			}
			req.ExecutionMode = dependencyExecution
			if action == "skip" {
				req.SkippedNodes = map[string]string{"gis": ""}
			}
			run, err := st.CreateDeliveryRun(standardSign(t, st, m, req), "alice", m)
			if err != nil {
				t.Fatal(err)
			}
			if len(run.Steps) != 2 || run.Steps[0].NodeID != "gis" || run.Steps[1].NodeID != "verify" || !slices.Equal(run.Plan.Nodes[1].DependsOn, []string{"gis"}) {
				t.Fatalf("plan lost node or dependency: %+v", run.Plan.Nodes)
			}
			if action == "skip" {
				if idx, d, _ := st.deliveryGraphNext(run); idx != -1 || d != "pause" || run.Steps[0].Disposition.Stage != "scope" {
					t.Fatal(idx, d, run.Steps)
				}
			} else {
				r := dispositionRequest{NodeID: "gis", Service: "orders", Action: action, Digest: run.Digest, Revision: run.Revision, Confirm: true}
				stale := st.DeliveryRun(run.ID)
				if err := st.disposeDeliveryStep(run, r, "alice"); err != nil {
					t.Fatal(err)
				}
				if err := st.disposeDeliveryStep(stale, r, "alice"); !errors.Is(err, ErrRunConflict) {
					t.Fatal("stale receipt", err)
				}
				if idx, _, _ := st.deliveryGraphNext(run); idx != 1 {
					t.Fatal("manual completion did not satisfy dependency", idx)
				}
			}
			r := dispositionRequest{NodeID: "verify", Action: "skip", Digest: run.Digest, Revision: run.Revision, Confirm: true}
			if err := st.disposeDeliveryStep(run, r, "alice"); err != nil {
				t.Fatal(err)
			}
			if run.Status != "completed_with_exceptions" || len(st.ReleaseOrders("", 100)) != 0 {
				t.Fatal(run.Status)
			}
			if d, _ := deliveryControlDecision(st, run); d == "complete" {
				t.Fatal("machine success recorded")
			}
		})
	}
}

func TestDependencyRunDispatchesIndependentAppsAndPreservesFailedBranch(t *testing.T) {
	st, m, req := deliveryFixture(t)
	tpl := st.DeliveryTemplate(req.TemplateID)
	for _, name := range []string{"statistics", "gateway"} {
		sv := *st.GetService("orders")
		sv.Name = name
		sv.ApplicationID = ""
		if e := st.UpsertService(sv); e != nil {
			t.Fatal(e)
		}
		target := *st.GetServiceEnvironment("orders", "test")
		target.Service = name
		target.RemoteDir = "/home/" + name
		if e := st.UpsertServiceEnvironment(target); e != nil {
			t.Fatal(e)
		}
		a := &Artifact{Service: name, Kind: "jar", Version: "v1", Digest: "sha256:abc", URI: "/unused.jar"}
		if e := st.RegisterArtifact(a); e != nil {
			t.Fatal(e)
		}
		tpl.Services = append(tpl.Services, name)
		req.Artifacts[name] = a.ID
	}
	tpl.Nodes = []DeliveryNode{{ID: "gis", Name: "GIS", Kind: "deploy", Service: "orders"}, {ID: "gateway", Name: "网关", Kind: "deploy", Service: "gateway", DependsOn: []string{"gis"}}, {ID: "statistics", Name: "统计", Kind: "deploy", Service: "statistics"}}
	if e := st.SaveDeliveryTemplate(tpl); e != nil {
		t.Fatal(e)
	}
	req.ExecutionMode = dependencyExecution
	run, e := st.CreateDeliveryRun(standardSign(t, st, m, req), "alice", m)
	if e != nil {
		t.Fatal(e)
	}
	launches := 0
	launch := func(int64) { launches++ }
	old := st.deliveryRunLedger(run.ID)
	run, e = st.advanceDeliveryRun(run.ID, "alice", false, m, launch)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = st.advanceDeliveryRunState(old, "alice", false, m, launch, nil); e == nil {
		t.Fatal("stale run launched twice")
	}
	run, e = st.advanceDeliveryRun(run.ID, "alice", false, m, launch)
	if e != nil {
		t.Fatal(e)
	}
	if launches != 2 || run.Steps[1].ReleaseID != 0 || run.Steps[2].ReleaseID == 0 {
		t.Fatal(run.Steps)
	}
	if _, e := st.db.Exec(`UPDATE release_orders SET status='failed' WHERE id=?`, run.Steps[0].ReleaseID); e != nil {
		t.Fatal(e)
	}
	run, e = st.advanceDeliveryRun(run.ID, "alice", false, m, launch)
	if e != nil || run.Status != "running" || run.Steps[0].Status != "failed" {
		t.Fatal(run, e)
	}
	if _, e := st.db.Exec(`UPDATE release_orders SET status='success' WHERE id=?`, run.Steps[2].ReleaseID); e != nil {
		t.Fatal(e)
	}
	run, e = st.advanceDeliveryRun(run.ID, "alice", false, m, launch)
	if e != nil || run.Steps[2].Status != "success" || run.Steps[1].StartedAt != 0 {
		t.Fatal(e, run)
	}
}

func TestDependencyMissingProductionTargetAndApproval(t *testing.T) {
	st, m, req := deliveryFixture(t)
	req.Environment = "prod"
	req.ExecutionMode = dependencyExecution
	if _, err := st.db.Exec(`DELETE FROM service_environments WHERE service=? AND environment=?`, "orders", "prod"); err != nil {
		t.Fatal(err)
	}
	run, err := st.CreateDeliveryRun(standardSign(t, st, m, req), "alice", m)
	if err != nil {
		t.Fatal(err)
	}
	if run.Steps[0].Status != "pending_target" || run.Status != "pending_approval" {
		t.Fatal(run.Steps, run.Status)
	}
	r := dispositionRequest{NodeID: "deploy", Service: "orders", Confirm: true, Digest: run.Digest, Revision: run.Revision, Action: "skip", Comment: "由运维处理"}
	if err := st.disposeDeliveryStep(run, r, "alice"); err == nil {
		t.Fatal("approval bypass")
	}
	run, err = st.ReviewDeliveryRun(run.ID, "reviewer", run.Digest, true)
	if err != nil {
		t.Fatal(err)
	}
	r.Revision = run.Revision
	r.Comment = ""
	if err := st.disposeDeliveryStep(run, r, "alice"); err == nil {
		t.Fatal("missing reason accepted")
	}
	r.Comment = "由运维处理"
	if err := st.disposeDeliveryStep(run, r, "alice"); err != nil {
		t.Fatal(err)
	}
}

func TestDependencyResourceReceiptsAcrossRevisions(t *testing.T) {
	st, m, req := deliveryFixture(t)
	run, err := st.CreateDeliveryRun(req, "alice", m)
	if err != nil {
		t.Fatal(err)
	}
	run.Plan.ExecutionMode = dependencyExecution
	run.Plan.StrategyKind = "standard"
	run.Plan.Nodes = []DeliveryNode{{ID: "db", Name: "SQL", Kind: "mysql"}, {ID: "cfg", Name: "配置", Kind: "nacos"}, {ID: "after", Name: "验证", Kind: "manual", DependsOn: []string{"db"}}}
	run.Steps = []DeliveryStep{{NodeID: "db", Kind: "mysql", Status: "pending"}, {NodeID: "cfg", Kind: "nacos", Status: "pending"}, {NodeID: "after", Kind: "manual", Status: "pending"}}
	// Directly seed a frozen ledger to use two bounded, isolated executors.
	raw, _ := json.Marshal(run.Plan)
	if _, err := st.db.Exec(`UPDATE delivery_runs SET plan=? WHERE id=?`, string(raw), run.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	op := func(ctx context.Context, r *DeliveryRun) (string, error) {
		select {
		case <-done:
			return "已登记", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if err := st.dispatchResourceStep(run, 0, "alice", op); err != nil {
		close(done)
		t.Fatal(err)
	}
	index, decision, _ := st.deliveryGraphNext(run)
	if index != 1 || decision != "advance" {
		close(done)
		t.Fatal(index, decision)
	}
	if err := st.dispatchResourceStep(run, 1, "alice", op); err != nil {
		close(done)
		t.Fatal(err)
	}
	if idx, decision, _ := st.deliveryGraphNext(run); idx != -1 || decision != "wait" {
		close(done)
		t.Fatal(idx, decision)
	}
	if err := st.disposeDeliveryStep(run, dispositionRequest{NodeID: "after", Digest: run.Digest, Revision: run.Revision, Action: "skip", Confirm: true}, "alice"); err != nil {
		close(done)
		t.Fatal(err)
	}
	close(done)
	until := time.Now().Add(4 * time.Second)
	for time.Now().Before(until) {
		saved := st.deliveryRunLedger(run.ID)
		if saved.Steps[0].Status == "success" && saved.Steps[1].Status == "success" {
			if saved.Steps[2].Status != "skipped" || saved.Steps[2].Disposition == nil {
				t.Fatal(saved.Steps)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("resource receipts lost", st.deliveryRunLedger(run.ID).Steps)
}

func TestDependencyScopedSQLSkipWithoutTarget(t *testing.T) {
	st, m, _ := deliveryFixture(t)
	change := &Change{Kind: "mysql", Service: "orders", Title: "Update menus", Owner: "alice", SQL: &SQLChange{ResourceAlias: "db", Content: "UPDATE menus SET name='new' WHERE id=1", MaxAffectedRows: 10}}
	if err := st.CreateChange(change); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetChangeReady(change.ID, true); err != nil {
		t.Fatal(err)
	}
	req := DeliveryRequest{StrategyKind: "standard", SourceType: "changes", SelectedChangeIDs: []int64{change.ID}, Environment: "test", ExecutionMode: dependencyExecution}
	draft, err := st.DraftDelivery(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(draft.Nodes) != 1 || draft.Nodes[0].Kind != "mysql" {
		t.Fatal(draft.Nodes)
	}
	id := draft.Nodes[0].ID
	req.SkippedNodes = map[string]string{id: ""}
	run, err := st.CreateDeliveryRun(standardSign(t, st, m, req), "alice", m)
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Steps) != 1 || run.Steps[0].NodeID != id || run.Steps[0].Status != "skipped" || len(run.Plan.SQLPlans) != 0 || len(run.Plan.Services) != 0 {
		t.Fatal(run.Steps, run.Plan.SQLPlans)
	}
}
func TestDependencyScopeWithoutApplicationOmitsWildcard(t *testing.T) {
	nodes := []DeliveryNode{{ID: "approve", Kind: "approval"}, {ID: "apply", Kind: "deploy", DependsOn: []string{"approve"}}, {ID: "sql", Kind: "mysql", DependsOn: []string{"apply"}}}
	filtered := expandScheduleApplications(nodes, &DeliveryTemplate{}, &DeliveryScope{})
	if len(filtered) != 2 || filtered[1].ID != "sql" || !slices.Equal(filtered[1].DependsOn, []string{"approve"}) || nodes[2].DependsOn[0] != "apply" {
		t.Fatal(filtered, nodes)
	}
}

func TestDeliveryInitialDispatchAndFrozenLimit(t *testing.T) {
	p := DeliveryPlan{Nodes: []DeliveryNode{
		{ID: "approval", Kind: "approval"}, {ID: "sql", Kind: "mysql", DependsOn: []string{"approval"}}, {ID: "skip", Kind: "deploy"}, {ID: "gateway", Kind: "deploy", DependsOn: []string{"skip"}}, {ID: "cfg", Kind: "nacos"}, {ID: "cfg2", Kind: "nacos"}, {ID: "gis", Kind: "deploy", Service: "gis"}, {ID: "manual", Kind: "manual"}, {ID: "stats", Kind: "deploy", Service: "stats"}}, SkippedNodes: map[string]string{"skip": ""}, Services: []DeliveryService{{Service: "gis", PendingTarget: true}, {Service: "stats"}}}
	if got := initialDeliveryDispatch(&p); !slices.Equal(got, []string{"sql", "cfg", "stats"}) {
		t.Fatal(got)
	}
	p.MaxActiveNodes = 2
	if got := initialDeliveryDispatch(&p); !slices.Equal(got, []string{"sql", "cfg"}) {
		t.Fatal(got)
	}
	p.Blockers = []string{"invalid"}
	if len(initialDeliveryDispatch(&p)) != 0 {
		t.Fatal("blocked preview advertised executable nodes")
	}
	st, m, req := deliveryFixture(t)
	req.ExecutionMode = dependencyExecution
	req.MaxActiveNodes = 1
	mux := http.NewServeMux()
	registerDeliveryRoutes(mux, st, m)
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/api/delivery/preview", bytes.NewReader(body))
	request.Header.Set("X-User", "alice")
	mux.ServeHTTP(w, request)
	var preview struct {
		DeliveryPlan
		InitialDispatch []string `json:"initial_dispatch"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil || w.Code != http.StatusOK || preview.PreflightToken == "" || preview.MaxActiveNodes != 1 || !slices.Equal(preview.InitialDispatch, []string{"deploy"}) {
		t.Fatal(w.Code, err, preview.InitialDispatch, preview.Blockers)
	}
	signed := standardSign(t, st, m, req)
	modified := signed
	modified.MaxActiveNodes = 2
	if _, err := st.CreateDeliveryRun(modified, "alice", m); err == nil {
		t.Fatal("changed limit accepted")
	}
	run, err := st.CreateDeliveryRun(signed, "alice", m)
	if err != nil {
		t.Fatal(err)
	}
	if run.Plan.MaxActiveNodes != 1 {
		t.Fatal("limit not frozen")
	}
	run.Plan.Nodes = append(run.Plan.Nodes, DeliveryNode{ID: "config", Kind: "nacos"})
	run.Steps = append(run.Steps, DeliveryStep{NodeID: "config", Kind: "nacos", Status: "executing", LeaseUntil: time.Now().Unix() + 60})
	if i, d, _ := st.deliveryGraphNext(run); i != -1 || d != "wait" {
		t.Fatal(i, d)
	}
	run.Steps[len(run.Steps)-1].Status = "success"
	if i, d, _ := st.deliveryGraphNext(run); i != 0 || d != "advance" {
		t.Fatal(i, d)
	}
}
