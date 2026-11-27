package deploy

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func attentionTestItem(t *testing.T, s *Store, actor, key string) researchAttentionItem {
	t.Helper()
	target, e := s.attentionTarget(actor, key)
	if e != nil {
		t.Fatal(e)
	}
	return researchAttentionItem{ID: key, Event: target.Task.AttentionEvent}
}
func TestResearchAttentionLifecycleAndEventIsolation(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	v := collaborationRun(t, s, "alice", "failed", false)
	key := fmt.Sprint("delivery:", v.ID)
	item := attentionTestItem(t, s, "alice", key)
	before := digestJSON(s.DeliveryRun(v.ID))
	raw, _ := s.unresolvedResearchExceptions("alice")
	tasks, e := s.attentionTasks("alice", "todo", raw)
	if e != nil || len(tasks) != 1 || !tasks[0].ReadOnly || !tasks[0].CanIgnore {
		t.Fatalf("readonly execution must allow personal ignore: %+v %v", tasks, e)
	}
	if _, e = s.setResearchAttention("Alice", item, "ignore", "暂不跟进此异常", "request-foreign"); e == nil {
		t.Fatal("foreign case actor accepted")
	}
	state, e := s.setResearchAttention("alice", item, "ignore", "暂不跟进此异常", "request-ignore")
	if e != nil {
		t.Fatal(e)
	}
	repeated, e := s.setResearchAttention("alice", item, "ignore", "暂不跟进此异常", "request-ignore")
	if e != nil || repeated.Revision != 1 {
		t.Fatal("not idempotent", repeated, e)
	}
	tasks, e = s.attentionTasks("alice", "todo", raw)
	if e != nil || len(tasks) != 0 {
		t.Fatal("ignored task remains", tasks, e)
	}
	active, e := s.attentionDeliveries("alice", "active")
	if e != nil || len(active) != 0 {
		t.Fatal("ignored tracking remains", active, e)
	}
	done, e := s.attentionTasks("alice", "done", nil)
	if e != nil || len(done) != 1 || !done[0].CanRestore || done[0].Status != "failed" {
		t.Fatal("missing honest history", done, e)
	}
	if digestJSON(s.DeliveryRun(v.ID)) != before {
		t.Fatal("personal action changed execution")
	}
	s.Close()
	s, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	archived, e := s.attentionDeliveries("alice", "archived")
	if e != nil || len(archived) != 1 || archived[0].AttentionReason != "暂不跟进此异常" {
		t.Fatal("not persistent", archived, e)
	}
	if _, e = s.setResearchAttention("alice", item, "restore", "继续跟进此异常", "request-stale"); e == nil {
		t.Fatal("CAS bypass")
	}
	item.Revision = state.Revision
	state, e = s.setResearchAttention("alice", item, "restore", "继续跟进此异常", "request-restore")
	if e != nil {
		t.Fatal(e)
	}
	tasks, e = s.attentionTasks("alice", "todo", raw)
	if e != nil || len(tasks) != 1 {
		t.Fatal("restore did not reopen", tasks, e)
	}
	item.Revision = state.Revision
	if _, e = s.setResearchAttention("alice", item, "ignore", "再次暂停跟进", "request-ignore-2"); e != nil {
		t.Fatal(e)
	}
	same := attentionTestItem(t, s, "alice", key)
	if same.Event != item.Event {
		t.Fatal("reading changed event")
	}
	v = s.DeliveryRun(v.ID)
	v.Steps[0].Detail = "new failure after retry"
	if e = s.saveDeliveryRun(v); e != nil {
		t.Fatal(e)
	}
	fresh := attentionTestItem(t, s, "alice", key)
	if fresh.Event == item.Event {
		t.Fatal("new failure not new event")
	}
	active, e = s.attentionDeliveries("alice", "active")
	if e != nil || len(active) != 1 {
		t.Fatal("old ignore hid new failure", active, e)
	}
	if _, e = s.setResearchAttention("alice", item, "restore", "继续跟进原异常", "request-old-event"); e == nil {
		t.Fatal("restored stale event")
	}
}
func TestResearchAttentionTerminalMandatoryAndUncertain(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	for _, status := range []string{"success", "canceled", "rejected", "rolled_back"} {
		v := collaborationRun(t, s, "alice", status, false)
		item := attentionTestItem(t, s, "alice", fmt.Sprint("delivery:", v.ID))
		rows, e := s.attentionDeliveries("alice", "active")
		if e != nil || len(rows) != 0 {
			t.Fatal("terminal active", rows, e)
		}
		if _, e = s.setResearchAttention("alice", item, "restore", "手动恢复交付关注", "request-restore-"+status); e != nil {
			t.Fatal(e)
		}
		rows, e = s.attentionDeliveries("alice", "active")
		if e != nil || len(rows) != 1 || rows[0].Status != status {
			t.Fatal("restore falsified state", rows, e)
		}
		item.Revision = 1
		if _, e = s.setResearchAttention("alice", item, "archive", "核对完成归档关注", "request-archive-"+status); e != nil {
			t.Fatal(e)
		}
	}
	v := collaborationRun(t, s, "alice", "pending_approval", false)
	item := attentionTestItem(t, s, "alice", fmt.Sprint("delivery:", v.ID))
	if _, e = s.setResearchAttention("alice", item, "ignore", "审批还未核对完成", "request-deny"); e == nil {
		t.Fatal("ignored mandatory approval")
	}
	if _, e = s.setResearchAttention("alice", item, "archive", "归档个人跟踪记录", "request-archive"); e != nil {
		t.Fatal(e)
	}
	tasks, e := s.ResearchTasks("qa", "todo")
	if e != nil || len(tasks) != 1 {
		t.Fatal("archive closed reviewer task", tasks, e)
	}
	v = collaborationRun(t, s, "alice", "failed", false)
	v.Steps[0].Status = "uncertain"
	if e = s.saveDeliveryRun(v); e != nil {
		t.Fatal(e)
	}
	item = attentionTestItem(t, s, "alice", fmt.Sprint("delivery:", v.ID))
	if _, e = s.setResearchAttention("alice", item, "ignore", "尚有未知执行结果", "request-uncertain"); e == nil {
		t.Fatal("ignored unknown outcome")
	}
}
func TestResearchAttentionLegacyRestoreAndBatchPartial(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	mux := http.NewServeMux()
	registerResearchCollaboration(mux, s)
	v := collaborationRun(t, s, "alice", "failed", false)
	raw, e := s.unresolvedResearchExceptions("alice")
	if e != nil || len(raw) != 1 {
		t.Fatal(e, raw)
	}
	body, _ := json.Marshal(map[string]string{"id": raw[0].ID, "digest": raw[0].ResolutionDigest, "action": "ignore", "comment": "旧版忽略暂不处理"})
	w := collaborationRequest(mux, "POST", "/api/research/exceptions/resolve", "alice", string(body))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	item := attentionTestItem(t, s, "alice", fmt.Sprint("delivery:", v.ID))
	target, e := s.attentionTarget("alice", item.ID)
	if e != nil || !target.LegacyClosed {
		t.Fatalf("legacy digest mismatch %v %+v vs %+v", e, target, raw[0])
	}
	if _, e = s.setResearchAttention("alice", item, "restore", "恢复旧版忽略记录", "request-legacy"); e != nil {
		t.Fatal(e)
	}
	raw, e = s.unresolvedResearchExceptions("alice")
	if e != nil || len(raw) != 1 {
		t.Fatal("legacy restore suppressed", raw, e)
	}
	item.Revision = 1
	request := researchAttentionRequest{Action: "ignore", Reason: "批量暂停跟进异常", RequestID: "request-batch", Items: []researchAttentionItem{item, {ID: item.ID, Event: "stale"}}}
	payload, _ := json.Marshal(request)
	w = collaborationRequest(mux, "POST", "/api/research/attention", "alice", string(payload))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var result struct {
		Results []struct {
			Success bool
			Error   string
		}
		ExecutionStateUnchanged bool `json:"execution_state_unchanged"`
	}
	json.Unmarshal(w.Body.Bytes(), &result)
	if len(result.Results) != 2 || !result.Results[0].Success || result.Results[1].Success || !result.ExecutionStateUnchanged {
		t.Fatal(w.Body.String())
	}
	request.Reason = strings.Repeat("长", 667)
	payload, _ = json.Marshal(request)
	w = collaborationRequest(mux, "POST", "/api/research/attention", "alice", string(payload))
	if w.Code != 400 || !strings.Contains(w.Body.String(), "2000字节") {
		t.Fatal(w.Body.String())
	}
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action LIKE 'attention-%'`).Scan(&count)
	if count != 2 {
		t.Fatal("missing audit or duplicate write", count)
	}
}

func TestAttentionReasonByEnvironment(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, env := range []Environment{{Name: "dev", DisplayName: "开发"}, {Name: "guarded", DisplayName: "保护测试", Protected: true, Approvers: "qa"}} {
		if err := s.UpsertEnvironment(env); err != nil {
			t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	registerResearchCollaboration(mux, s)
	var items []researchAttentionItem
	for _, env := range []Environment{{Name: "dev"}, {Name: "guarded"}, {Name: "dev", Protected: true}, {Name: "removed"}} {
		run := collaborationRun(t, s, "alice", "failed", false)
		run.Plan.Environment = env
		// Seed the frozen plan itself; runtime status updates preserve the original plan.
		plan, err := json.Marshal(run.Plan)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`UPDATE delivery_runs SET plan=?,digest=? WHERE id=?`, string(plan), digestJSON(run.Plan), run.ID); err != nil {
			t.Fatal(err)
		}
		items = append(items, attentionTestItem(t, s, "alice", fmt.Sprint("delivery:", run.ID)))
	}
	body, _ := json.Marshal(researchAttentionRequest{Action: "ignore", Items: items, RequestID: "mixed-attention"})
	w := collaborationRequest(mux, "POST", "/api/research/attention", "alice", string(body))
	var result struct {
		Results []struct {
			Success bool
			Error   string
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || len(result.Results) != 4 {
		t.Fatal(w.Body.String())
	}
	for i, r := range result.Results {
		if r.Success != (i == 0) || i > 0 && !strings.Contains(r.Error, "至少6字") {
			t.Fatal(w.Body.String())
		}
	}
}

// Opt-in local browser fixture: only personal-disposition writes are permitted.
func TestResearchAttentionBrowserFixture(t *testing.T) {
	port := os.Getenv("ATTENTION_FIXTURE_PORT")
	if port == "" {
		t.Skip("opt-in browser fixture")
	}
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	for i := 0; i < 7; i++ {
		v := collaborationRun(t, s, "alice", "failed", false)
		v.Plan.Template.Name = fmt.Sprintf("异常核对 %d · 开发测试 SQL 与 Nacos 综合交付", i+1)
		v.Steps[0].Detail = "代码变更仍为草稿，请核对变更内容与当前实例；原执行结果保留"
		if e = s.saveDeliveryRun(v); e != nil {
			t.Fatal(e)
		}
	}
	collaborationRun(t, s, "alice", "success", false)
	collaborationRun(t, s, "alice", "canceled", false)
	mux := http.NewServeMux()
	registerResearchRoutes(mux, s, nil, nil)
	stop := make(chan struct{}, 1)
	mux.HandleFunc("GET /__fixture/stop", func(w http.ResponseWriter, r *http.Request) {
		deliveryJSON(w, map[string]bool{"stopped": true})
		stop <- struct{}{}
	})
	mux.HandleFunc("GET /__fixture/status", func(w http.ResponseWriter, r *http.Request) {
		rows, _ := s.attentionStates("alice")
		deliveryJSON(w, rows)
	})
	listener, e := net.Listen("tcp", "127.0.0.1:"+port)
	if e != nil {
		t.Fatal(e)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("X-User", "alice")
		if r.Method != "GET" && r.URL.Path != "/api/research/attention" {
			deliveryWriteError(w, 405, "Fixture permits personal disposition only")
			return
		}
		mux.ServeHTTP(w, r)
	})}
	go server.Serve(listener)
	defer server.Close()
	t.Log("attention fixture on", port)
	select {
	case <-stop:
	case <-time.After(45 * time.Minute):
	}
}

func TestResearchAttentionReleaseUnknownCannotBeIgnored(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	o := &ReleaseOrder{Service: "fixture", Actor: "alice", Status: "failed", Environment: "test", Hosts: []string{"synthetic"}, Strategy: "all"}
	if e = s.CreateReleaseOrder(o); e != nil {
		t.Fatal(e)
	}
	d := &Deploy{Service: "fixture", Host: "synthetic", Environment: "test", Actor: "alice"}
	if e = s.CreateDeploy(d); e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec(`INSERT INTO release_order_deploys(release_id,deploy_id,batch_no) VALUES(?,?,1)`, o.ID, d.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.FinishDeploy(d.ID, "timed_out"); e != nil {
		t.Fatal(e)
	}
	item := attentionTestItem(t, s, "alice", fmt.Sprint("release:", o.ID))
	if _, e = s.setResearchAttention("alice", item, "ignore", "实例结果尚未核对", "request-release-unknown"); e == nil {
		t.Fatal("timeout hidden")
	}
	// Synthetic receipt update only; the production reconciliation gate is not exercised here.
	if _, e = s.db.Exec(`UPDATE deploys SET status='failed' WHERE id=?`, d.ID); e != nil {
		t.Fatal(e)
	}
	fresh := attentionTestItem(t, s, "alice", item.ID)
	if fresh.Event == item.Event {
		t.Fatal("new reconciliation not new event")
	}
	if _, e = s.setResearchAttention("alice", fresh, "ignore", "已经核对实例失败", "request-release-settled"); e != nil {
		t.Fatal(e)
	}
}

func TestResearchAttentionArchiveKeepsRestoredLegacyTodo(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	mux := http.NewServeMux()
	registerResearchCollaboration(mux, s)
	v := collaborationRun(t, s, "alice", "failed", false)
	tasks, _ := s.unresolvedResearchExceptions("alice")
	body, _ := json.Marshal(map[string]string{"id": tasks[0].ID, "digest": tasks[0].ResolutionDigest, "action": "ignore", "comment": "旧版忽略暂不处理"})
	if w := collaborationRequest(mux, "POST", "/api/research/exceptions/resolve", "alice", string(body)); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	item := attentionTestItem(t, s, "alice", fmt.Sprint("delivery:", v.ID))
	if _, e = s.setResearchAttention("alice", item, "restore", "恢复处理旧版异常", "legacy-restore"); e != nil {
		t.Fatal(e)
	}
	item.Revision = 1
	if _, e = s.setResearchAttention("alice", item, "archive", "仅归档交付关注", "legacy-archive"); e != nil {
		t.Fatal(e)
	}
	tasks, e = s.unresolvedResearchExceptions("alice")
	if e != nil || len(tasks) != 1 {
		t.Fatal("archiving watch closed restored todo", tasks, e)
	}
	tasks, e = s.attentionTasks("alice", "todo", tasks)
	if e != nil || len(tasks) != 1 || !tasks[0].CanIgnore {
		t.Fatal("archived watch blocked todo maintenance", tasks, e)
	}
}
