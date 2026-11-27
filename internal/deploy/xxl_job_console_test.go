package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestXXLJobConsoleNativeContract(t *testing.T) {
	const user = "devops-fixture"
	const password = "console fixture password"
	const root = "/estate-job"
	var mu sync.Mutex
	groups := []XXLJobGroup{{ID: 19, AppName: "orders-jobs", RegistryList: []string{"http://executor:9999"}}, {ID: 50, AppName: "shared-jobs"}}
	tasks := []xxlConsoleTask{{
		XXLJobTask: XXLJobTask{ID: 72, GroupID: 19, Title: "Recount", Handler: "recountOrders", GlueType: "BEAN", ScheduleType: "FIX_RATE", Schedule: "30"},
		Author:     "fixture", Route: "FIRST", Misfire: "DO_NOTHING", Block: "SERIAL_EXECUTION", Parameters: "internal fixture parameter", ChildJobID: "80",
	}}
	var writes, triggers int
	scheduler := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method != http.MethodPost || r.ParseForm() != nil || !strings.HasPrefix(r.URL.Path, root+"/") {
			http.Error(w, "unexpected route", 400)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, root)
		reply := func(content any) { deliveryJSON(w, map[string]any{"code": 200, "content": content}) }
		page := func(data any, length int) { deliveryJSON(w, map[string]any{"data": data, "recordsFiltered": length}) }
		if path == "/login" {
			if r.Form.Get("userName") != user || r.Form.Get("password") != password || r.URL.RawQuery != "" {
				deliveryJSON(w, map[string]any{"code": 500})
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "XXL_JOB_LOGIN_IDENTITY", Value: "fixture", Path: root})
			reply(nil)
			return
		}
		if cookie, _ := r.Cookie("XXL_JOB_LOGIN_IDENTITY"); cookie == nil || cookie.Value != "fixture" || r.Header.Get("Authorization") != "" {
			http.Redirect(w, r, root+"/toLogin", http.StatusFound)
			return
		}
		switch path {
		case "/jobgroup/pageList":
			items := []XXLJobGroup{}
			for _, group := range groups {
				if strings.Contains(group.AppName, r.Form.Get("appname")) {
					items = append(items, group)
				}
			}
			page(items, len(items))
		case "/jobgroup/save":
			groups = append(groups, XXLJobGroup{ID: 61, AppName: r.Form.Get("appname")})
			reply(nil)
		case "/joblog/getJobsByGroup":
			if r.Form.Get("jobGroup") != "19" {
				t.Error("unexpected task scope")
			}
			reply(tasks)
		case "/joblog/pageList":
			if r.Form.Get("jobGroup") != "19" || (r.Form.Get("jobId") != "72" && r.Form.Get("jobId") != "0") {
				t.Error("unexpected log scope")
			}
			page([]xxlConsoleRun{{ID: "9007199254740993", GroupID: 19, TaskID: 72, TriggerCode: 200,
				ExecutorAddress: "http://executor:9999", TriggerTime: json.RawMessage(`"2026-11-27T03:04:05.123+0000"`)}}, 1)
		case "/joblog/logDetailCat":
			if r.Form.Get("logId") != "9007199254740993" || r.Form.Get("executorAddress") != "http://executor:9999" || r.Form.Get("triggerTime") != "1795748645123" {
				t.Errorf("unexpected log request: %v", r.Form)
			}
			reply(map[string]any{"logContent": "hello", "end": true})
		case "/jobinfo/nextTriggerTime":
			reply([]string{"2026-12-01 11:00:00"})
		case "/jobinfo/add":
			writes++
			tasks = append(tasks, xxlConsoleTask{
				XXLJobTask: XXLJobTask{ID: 81, GroupID: 19, Title: r.Form.Get("jobDesc"), Handler: r.Form.Get("executorHandler"), GlueType: "BEAN",
					ScheduleType: r.Form.Get("scheduleType"), Schedule: r.Form.Get("scheduleConf")},
				Author: r.Form.Get("author"), Route: "FIRST", Misfire: "DO_NOTHING", Block: "SERIAL_EXECUTION",
				GlueUpdated: json.RawMessage(`"2026-11-27T03:04:05Z"`),
			})
			reply("81")
		case "/jobinfo/update":
			writes++
			if r.Form.Get("id") != "72" || r.Form.Get("jobGroup") != "19" || r.Form.Get("executorParam") != tasks[0].Parameters || r.Form.Get("childJobId") != "80" {
				t.Error("update must preserve fields outside the change")
			}
			tasks[0].Schedule = r.Form.Get("scheduleConf")
			reply(nil)
		case "/jobinfo/start", "/jobinfo/stop":
			writes++
			if r.Form.Get("id") != "81" {
				t.Error("wrong task for schedule operation")
			}
			if path == "/jobinfo/start" {
				tasks[1].TriggerStatus = 1
			} else {
				tasks[1].TriggerStatus = 0
			}
			reply(nil)
		case "/jobinfo/trigger":
			triggers++
			if r.Form.Get("id") != "72" || r.Form.Get("executorParam") != "" || r.Form.Has("addressList") {
				t.Error("wrong task or parameters for manual trigger")
			}
			reply(nil)
		default:
			t.Errorf("unexpected XXL-JOB route %s", path)
			http.NotFound(w, r)
		}
	}))
	defer scheduler.Close()
	st, _, req := xxlFixture(t, scheduler, "test")
	connection := ResourceConnection{Name: "scheduler", Kind: xxlJobKind, Endpoint: scheduler.URL + root,
		Username: user, Password: password, Config: map[string]string{"environment": "test", "xxl_job_auth": "console"}}
	if err := st.UpsertConnection(connection); err != nil {
		t.Fatal(err)
	}
	stored := st.GetConnection(connection.Name)
	public, _ := json.Marshal(st.Connections())
	if bytesContainAny(string(public), password, user) {
		t.Fatal("console credentials reached connection list")
	}
	ctx := context.Background()
	if err := testIntegrationConnection(ctx, stored); err != nil {
		t.Fatal(err)
	}
	binding, err := st.xxlJobBinding("orders", "test", "jobs")
	if err != nil {
		t.Fatal(err)
	}
	group, err := binding.group(ctx)
	if err != nil || group.ID != 19 {
		t.Fatalf("group: %+v, %v", group, err)
	}
	call := func(operation string, input map[string]any, output any) error {
		input["group_id"], input["app_name"] = group.ID, group.AppName
		return xxlCall(ctx, stored, operation, input, output)
	}
	var found json.RawMessage
	if err := call("tasks", map[string]any{"query": "recountOrders"}, &found); err != nil ||
		!bytesContainAny(string(found), "9007199254740993") || bytesContainAny(string(found), "internal fixture parameter") {
		t.Fatalf("tasks: %s, %v", found, err)
	}
	var chunk map[string]any
	if err := call("log", map[string]any{"task_id": 72, "run_id": int64(9007199254740993)}, &chunk); err != nil || chunk["logContent"] != "hello" {
		t.Fatalf("log: %v, %v", chunk, err)
	}
	if err := call("log", map[string]any{"task_id": 72, "run_id": 200}, &chunk); err == nil {
		t.Fatal("read run outside task scope")
	}
	var before XXLJobTask
	if err := call("task", map[string]any{"task_id": 72}, &before); err != nil || before.Revision == "" {
		t.Fatalf("read task: %v", err)
	}
	change := xxlFixtureChange(t, st, 0, Change{Kind: xxlJobKind, Title: "Update schedule", XXLJob: &XXLJobChange{
		ResourceAlias: "jobs", Action: "update", TargetTitle: "Recount", TargetHandler: "recountOrders",
		Config: map[string]json.RawMessage{"scheduleConf": json.RawMessage(`"45"`)},
	}})
	plan, err := st.prepareXXLJob(ctx, &DeliveryPlan{
		Environment: *st.GetEnvironment(req.Environment), Scope: &DeliveryScope{Changes: []Change{change}},
	}, DeliveryNode{ID: "console-update", Service: "orders", ChangeIDs: []int64{change.ID}})
	if err != nil || plan.After.Schedule != "45" || plan.Before.Schedule != "30" {
		t.Fatalf("preview: %+v, %v", plan, err)
	}
	assertCounts := func(wantWrites, wantTriggers int) {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		if writes != wantWrites || triggers != wantTriggers {
			t.Fatalf("writes/triggers: %d/%d, want %d/%d", writes, triggers, wantWrites, wantTriggers)
		}
	}
	assertCounts(0, 0)
	run := &DeliveryRun{ID: 700, Plan: DeliveryPlan{Scope: &DeliveryScope{Changes: []Change{change}}}}
	if _, err := st.executeXXLJob(ctx, run, plan); err != nil {
		t.Fatal(err)
	}
	effect, err := st.xxlEffect(change.ID, "test")
	if err != nil || effect == nil || effect.RunID != run.ID || effect.Task.Schedule != "45" {
		t.Fatalf("delivery receipt: %+v, %v", effect, err)
	}
	params := map[string]any{"task_id": 72, "revision": before.Revision, "action": "update", "config": change.XXLJob.Config}
	var updated XXLJobTask
	if err := call("apply", params, &updated); !errors.Is(err, errXXLJobRejected) {
		t.Fatalf("stale update: %v", err)
	}
	assertCounts(1, 0)
	create := map[string]any{"action": "create", "config": map[string]json.RawMessage{
		"jobDesc": json.RawMessage(`"New task"`), "executorHandler": json.RawMessage(`"newHandler"`), "author": json.RawMessage(`"fixture"`),
		"scheduleType": json.RawMessage(`"FIX_RATE"`), "scheduleConf": json.RawMessage(`"60"`),
	}}
	var created XXLJobTask
	if err := call("apply", create, &created); err != nil || created.ID != 81 || created.TriggerStatus != 0 {
		t.Fatalf("create: %+v, %v", created, err)
	}
	for _, action := range []string{"enable", "disable"} {
		if err := call("apply", map[string]any{"action": action, "task_id": created.ID, "revision": created.Revision}, &created); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
	}
	if created.TriggerStatus != 0 {
		t.Fatal("task should be disabled after stop")
	}
	var newGroup XXLJobGroup
	if err := xxlCall(ctx, stored, "create-group", map[string]any{"app_name": "new-jobs", "title": "New"}, &newGroup); err != nil || newGroup.ID != 61 {
		t.Fatalf("create group: %+v, %v", newGroup, err)
	}
	for range 2 {
		receipt, err := st.xxlTrigger(ctx, binding, group, 72, "alice", "native-trigger-12345", "")
		if err != nil || !receipt.Accepted || len(receipt.RunIDs) != 0 {
			t.Fatalf("queued trigger: %+v, %v", receipt, err)
		}
	}
	if _, err := st.xxlTrigger(ctx, binding, group, 999, "alice", "native-trigger-54321", ""); err == nil {
		t.Fatal("triggered task outside the bound executor")
	}
	assertCounts(4, 1)
}

func bytesContainAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
