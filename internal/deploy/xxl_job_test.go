package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

func xxlFixture(t *testing.T, scheduler *httptest.Server, env string) (*Store, *ReleaseManager, DeliveryRequest) {
	t.Helper()
	st, manager, request := deliveryFixture(t)
	const password = "fixture-long-dedicated-token-0123456789"
	connection := ResourceConnection{Name: "scheduler", Kind: xxlJobKind, Endpoint: scheduler.URL,
		Password: password, Config: map[string]string{"environment": env}}
	if err := st.UpsertConnection(connection); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveApplicationResources("orders", ApplicationResources{
		Requirements: []ApplicationResourceRequirement{{Alias: "jobs", Kind: xxlJobKind}},
		Bindings:     []ApplicationEnvironmentResource{{Alias: "jobs", Environment: env, Connection: connection.Name, Config: map[string]string{"app_name": "orders-jobs"}}},
	}); err != nil {
		t.Fatal(err)
	}
	return st, manager, request
}

func xxlFixtureChange(t *testing.T, st *Store, workID int64, change Change) Change {
	t.Helper()
	change.Service, change.Owner = "orders", "alice"
	if err := st.CreateChange(&change); err != nil {
		t.Fatal(err)
	}
	if workID > 0 {
		if err := st.SetChangeWorkItems(change.ID, []int64{workID}); err != nil {
			t.Fatal(err)
		}
	}
	ready, err := st.SetChangeReady(change.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	return *ready
}

func TestXXLJobDeliveryFrozenTaskHandoffAndScope(t *testing.T) {
	var creates, enables atomic.Int32
	scheduler := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-long-dedicated-token-0123456789" {
			http.Error(w, "authentication", 401)
			return
		}
		var input struct {
			Action   string `json:"action"`
			TaskID   int    `json:"task_id"`
			Revision string `json:"revision"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Errorf("decode bridge input: %v", err)
		}
		task := XXLJobTask{Title: "Recount", Handler: "recountOrders", GroupID: 19, ScheduleType: "CRON", Schedule: "0 0 2 * * ?", Revision: "validated"}
		switch r.URL.Path {
		case "/devops/v1/groups":
			deliveryJSON(w, []XXLJobGroup{{ID: 19, AppName: "orders-jobs", RegistryList: []string{"http://executor:9999"}}, {ID: 50, AppName: "other-jobs"}})
		case "/devops/v1/apply":
			switch input.Action {
			case "validate-create":
			case "create":
				creates.Add(1)
				task.ID, task.Revision = 72, "actual-creation-revision"
			case "enable":
				if input.TaskID != 72 || input.Revision != "actual-creation-revision" {
					t.Errorf("enable must use exact create receipt: %+v", input)
					http.Error(w, "wrong task", 409)
					return
				}
				enables.Add(1)
				task.ID, task.Revision, task.TriggerStatus = 72, "enabled-revision", 1
			default:
				t.Errorf("unexpected scheduler action %s", input.Action)
				http.NotFound(w, r)
				return
			}
			deliveryJSON(w, task)
		default:
			t.Errorf("unexpected scheduler endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer scheduler.Close()
	st, manager, request := xxlFixture(t, scheduler, "test")
	first, second := &WorkItem{Title: "Requirement A", Kind: "requirement"}, &WorkItem{Title: "Requirement B", Kind: "requirement"}
	for _, work := range []*WorkItem{first, second} {
		if err := st.CreateWorkItem(work); err != nil {
			t.Fatal(err)
		}
	}
	code := xxlFixtureChange(t, st, first.ID, Change{Kind: "code", Title: "New handler", Reference: "feature/a"})
	other := xxlFixtureChange(t, st, second.ID, Change{Kind: "code", Title: "Unrelated same app", Reference: "feature/b"})
	creation := xxlFixtureChange(t, st, first.ID, Change{Kind: xxlJobKind, Title: "Create recount", XXLJob: &XXLJobChange{
		ResourceAlias: "jobs", Action: "create", Config: map[string]json.RawMessage{
			"jobDesc": json.RawMessage(`"Recount"`), "executorHandler": json.RawMessage(`"recountOrders"`),
			"author": json.RawMessage(`"alice"`), "scheduleType": json.RawMessage(`"CRON"`), "scheduleConf": json.RawMessage(`"0 0 2 * * ?"`),
		},
	}})
	enable := xxlFixtureChange(t, st, first.ID, Change{Kind: xxlJobKind, Title: "Enable recount", XXLJob: &XXLJobChange{
		ResourceAlias: "jobs", Action: "enable", CreatedByChangeID: creation.ID,
	}, Prerequisites: []ChangePrerequisite{{ChangeID: creation.ID}, {ChangeID: code.ID}}})
	template := &DeliveryTemplate{Name: "Demand workflow", Mode: "version", Nodes: []DeliveryNode{{ID: "deploy", Name: "Deploy service", Kind: "deploy"}}}
	if err := st.SaveDeliveryTemplate(template); err != nil {
		t.Fatal(err)
	}
	request.TemplateID, request.SourceType, request.WorkItemIDs = template.ID, "work_items", []int64{first.ID}
	request.CodeConfirmed = true
	resolved, secrets, err := st.ResolveDelivery(request, manager)
	if err != nil || len(resolved.Blockers) > 0 || len(resolved.XXLJobPlans) != 2 {
		t.Fatalf("resolve: %+v %v", resolved, err)
	}
	ids := []string{xxlNodeID("create", creation.ID), "deploy", xxlNodeID("verify", enable.ID), xxlNodeID("enable", enable.ID)}
	var actual []string
	for _, node := range resolved.Nodes {
		actual = append(actual, node.ID)
	}
	if !slices.Equal(ids, actual) || slices.ContainsFunc(resolved.Scope.Changes, func(c Change) bool { return c.ID == other.ID }) {
		t.Fatalf("incorrect steps or unrelated change: %v %+v", actual, resolved.Scope)
	}
	if creates.Load() != 0 || enables.Load() != 0 {
		t.Fatal("preflight wrote to scheduler")
	}
	request.PreflightToken, _ = st.signDeliveryPreflight(request, resolved, secrets, "alice")
	run, err := st.CreateDeliveryRun(request, "alice", manager)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.AdvanceDeliveryRun(run.ID, "alice", false, manager); err != nil {
		t.Fatal(err)
	}
	run = st.deliveryRunLedger(run.ID)
	if makes := creates.Load(); makes != 1 || run.Steps[0].Status != "success" || !strings.Contains(run.Steps[0].Detail, "#72") {
		t.Fatalf("invalid create receipt: creates=%d %+v", makes, run.Steps[0])
	}
	run.Steps[0].Status, run.Steps[0].Detail = "executing", ""
	if err := st.saveDeliveryRun(run); err != nil {
		t.Fatal(err)
	}
	if _, err = st.AdvanceDeliveryRun(run.ID, "alice", false, manager); err != nil {
		t.Fatal("reconcile saved exact receipt", err)
	}
	frozenEnable := resolved.XXLJobPlans[1]
	run = st.deliveryRunLedger(run.ID)
	if creates.Load() != 1 || run.Steps[0].Status != "success" {
		t.Fatal("recovering valid receipt did not reconcile exactly once")
	}
	if _, err = st.executeXXLJob(context.Background(), run, frozenEnable); err == nil || enables.Load() != 0 {
		t.Fatal("enabled without deploy and verification")
	}
	run.Steps[1].ReleaseID, run.Steps[1].Status = 123, "success"
	if err := st.saveDeliveryRun(run); err != nil {
		t.Fatal(err)
	}
	if _, err = st.AdvanceDeliveryRun(run.ID, "alice", true, manager); err == nil {
		t.Fatal("empty executor verification accepted")
	}
	if _, err = st.AdvanceDeliveryRun(run.ID, "alice", true, manager, "Verified artifact and recountOrders on executor"); err != nil {
		t.Fatal(err)
	}
	if _, err = st.AdvanceDeliveryRun(run.ID, "alice", false, manager); err != nil {
		t.Fatal(err)
	}
	run = st.deliveryRunLedger(run.ID)
	if enables.Load() != 1 || run.Steps[3].Status != "success" || !strings.Contains(run.Steps[3].Detail, "#72") {
		t.Fatalf("incorrect enable receipt: %+v, dispatched %d", run.Steps[3], enables.Load())
	}
	if _, err := st.SetChangeReady(creation.ID, false); err == nil {
		t.Fatal("executed create returned to draft")
	}
}

func TestXXLJobUpdateRefusesDriftAndNeverReplaysUncertainWrite(t *testing.T) {
	var count atomic.Int32
	var recoverReceipt atomic.Bool
	var operation atomic.Value
	var ambiguous atomic.Bool
	scheduler := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var fields map[string]any
		json.NewDecoder(r.Body).Decode(&fields)
		task := XXLJobTask{ID: 43, GroupID: 19, Title: "Correct title", Handler: "sharedHandler", Revision: "original", Schedule: "0 0 2 * * ?", ScheduleType: "CRON"}
		switch r.URL.Path {
		case "/devops/v1/groups":
			deliveryJSON(w, []XXLJobGroup{{ID: 19, AppName: "orders-jobs"}})
		case "/devops/v1/resolve":
			if fields["target_title"] != task.Title || fields["target_handler"] != task.Handler {
				t.Errorf("task identity missing: %v", fields)
			}
			found := []XXLJobTask{task}
			if ambiguous.Load() {
				found = append(found, task)
			}
			deliveryJSON(w, found)
		case "/devops/v1/apply":
			if fields["action"] == "validate-update" {
				deliveryJSON(w, task)
				return
			}
			count.Add(1)
			config, _ := fields["config"].(map[string]any)
			if fields["task_id"] != float64(43) || fields["revision"] != "original" || len(config) != 1 || config["executorParam"] != "" {
				t.Errorf("CAS or partial config changed: %v", fields)
			}
			operation.Store(fields["operation_id"].(string))
			http.Error(w, "lost response", 500)
		case "/devops/v1/receipt":
			if !recoverReceipt.Load() {
				http.NotFound(w, r)
				return
			}
			if fields["operation_id"] != operation.Load() {
				t.Errorf("recovery changed the operation identity: %v", fields)
			}
			taskReceipt := task
			taskReceipt.Revision = "committed"
			deliveryJSON(w, taskReceipt)
		}
	}))
	defer scheduler.Close()
	st, manager, _ := xxlFixture(t, scheduler, "test")
	taskChange := xxlFixtureChange(t, st, 0, Change{Kind: xxlJobKind, Title: "Clear executor parameter", XXLJob: &XXLJobChange{
		ResourceAlias: "jobs", Action: "update", TargetTitle: "Correct title", TargetHandler: "sharedHandler",
		Config: map[string]json.RawMessage{"executorParam": json.RawMessage(`""`)},
	}})
	request := DeliveryRequest{StrategyKind: "standard", SourceType: "changes", SelectedChangeIDs: []int64{taskChange.ID}, Environment: "test"}
	ambiguous.Store(true)
	plan, _, err := st.ResolveDelivery(request, manager)
	if err != nil || len(plan.Blockers) < 1 || !strings.Contains(plan.Blockers[0], "唯一") {
		t.Fatalf("ambiguous identity allowed: %+v %v", plan, err)
	}
	ambiguous.Store(false)
	plan, secrets, err := st.ResolveDelivery(request, manager)
	if err != nil || len(plan.Blockers) != 0 {
		t.Fatalf("single change preflight: %+v %v", plan, err)
	}
	request.PreflightToken, _ = st.signDeliveryPreflight(request, plan, secrets, "alice")
	run, err := st.CreateDeliveryRun(request, "alice", manager)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AdvanceDeliveryRun(run.ID, "alice", false, manager); err != nil {
		t.Fatal(err)
	}
	run = st.deliveryRunLedger(run.ID)
	if run.Steps[0].Status != "uncertain" || count.Load() != 1 {
		t.Fatalf("wrong uncertainty: %+v, dispatches %d", run.Steps[0], count.Load())
	}
	if _, err := st.AdvanceDeliveryRun(run.ID, "alice", false, manager); !errors.Is(err, errXXLJobUncertain) || count.Load() != 1 {
		t.Fatalf("uncertain write replayed or result changed: %v", err)
	}
	recoverReceipt.Store(true)
	if _, err := st.AdvanceDeliveryRun(run.ID, "alice", false, manager); err != nil {
		t.Fatal(err)
	}
	run = st.deliveryRunLedger(run.ID)
	if run.Steps[0].Status != "success" || count.Load() != 1 || !strings.Contains(run.Steps[0].Detail, "#43") {
		t.Fatalf("the original committed receipt did not recover the step: %+v", run.Steps[0])
	}
}

func TestXXLJobWorkbenchScopesAndExactManualRunIDs(t *testing.T) {
	var dispatches atomic.Int32
	scheduler := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/devops/v1/groups":
			deliveryJSON(w, []XXLJobGroup{{ID: 19, AppName: "orders-jobs"}})
		case "/devops/v1/trigger":
			dispatches.Add(1)
			deliveryJSON(w, []string{"9007199254740999"})
		case "/devops/v1/run":
			deliveryJSON(w, map[string]any{"id": "9007199254740999", "triggerCode": 200, "handleCode": 500})
		default:
			http.NotFound(w, r)
		}
	}))
	defer scheduler.Close()
	st, _, _ := xxlFixture(t, scheduler, "test")
	router := Routes(st, nil, nil, "", nil)
	post := func(raw string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/xxl-job/trigger", bytes.NewBufferString(raw))
		request.Header.Set("X-User", "alice")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder
	}
	const input = `{"service":"orders","environment":"test","alias":"jobs","task_id":15,"parameters":"","nonce":"trigger-fixture-0001"}`
	for attempt := 0; attempt < 2; attempt++ {
		response := post(input)
		if response.Code != 200 || !strings.Contains(response.Body.String(), `"9007199254740999"`) {
			t.Fatalf("manual run receipt: %d %s", response.Code, response.Body.String())
		}
	}
	if dispatches.Load() != 1 {
		t.Fatal("dedupe dispatched twice")
	}
	if response := post(strings.Replace(input, `"test"`, `"prod"`, 1)); response.Code != 403 || dispatches.Load() != 1 {
		t.Fatal("protected environment triggered")
	}
	if response := post(strings.Replace(input, `"jobs"`, `"other"`, 1)); response.Code != 400 || dispatches.Load() != 1 {
		t.Fatal("unbound executor triggered")
	}
}
