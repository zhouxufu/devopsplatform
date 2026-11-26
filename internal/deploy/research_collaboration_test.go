package deploy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func collaborationRun(t *testing.T, s *Store, owner, status string, research bool) *DeliveryRun {
	t.Helper()
	v := &DeliveryRun{Owner: owner, Status: status, CreatedAt: 100, Revision: 1, Plan: DeliveryPlan{Environment: Environment{Name: "prod", DisplayName: "生产", Approvers: "qa,pm"}, Template: DeliveryTemplate{Name: "菜单交付"}, Scope: &DeliveryScope{Type: "work_items", WorkItems: []WorkItem{{ID: 7, Title: "菜单 Bug"}}, Changes: []Change{{ID: 1, Kind: "mysql", Service: "menus", Title: "修复菜单", RollbackPlan: "回退原菜单", SQL: &SQLChange{ResourceAlias: "db", Content: "UPDATE menus SET name='new' WHERE id=1", MaxAffectedRows: 1}}}}, Nodes: []DeliveryNode{{ID: "qa", Kind: "approval", Name: "测试审批", Approvers: []string{"qa", "qa2"}, ApprovalMode: "all"}, {ID: "pm", Kind: "approval", Name: "项目管理审批", Approvers: []string{"pm"}, ApprovalMode: "any"}}, NacosPlans: []*NacosExecutionPlan{{NodeID: "config", Configs: []NacosConfigPlan{{BeforeEncrypted: "DO_NOT_EXPOSE", AfterEncrypted: "DO_NOT_EXPOSE"}}}}}, Steps: []DeliveryStep{{NodeID: "qa", Name: "测试审批", Kind: "approval", Status: "pending"}, {NodeID: "pm", Name: "项目管理审批", Kind: "approval", Status: "pending"}}}
	if research {
		v.Plan.Template.Workbench = "research"
		v.Plan.PreparationRequest = &DeliveryRequest{TemplateID: 1}
	}
	if status == "failed" {
		v.Steps[0].Status = "failed"
		v.Steps[0].Detail = "Readiness check timed out"
	}
	v.Digest = digestJSON(v.Plan)
	plan, _ := json.Marshal(v.Plan)
	steps, _ := json.Marshal(v.Steps)
	r, err := s.db.Exec(`INSERT INTO delivery_runs(plan,digest,status,execution,env_secrets,owner,approver,created_at,revision) VALUES(?,?,?,?,?,?,'',?,?)`, string(plan), v.Digest, status, string(steps), "{}", owner, v.CreatedAt, v.Revision)
	if err != nil {
		t.Fatal(err)
	}
	v.ID, _ = r.LastInsertId()
	return v
}

func collaborationRequest(mux *http.ServeMux, method, path, actor, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("X-User", actor)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestResearchCollaborationHandoffAndPreparationIsNotPublication(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	v := collaborationRun(t, s, "owner", "pending_approval", true)
	v.Steps[0].Reviews = []ApprovalDecision{{Actor: "qa", Approved: true, At: 120}}
	summary := researchRunSummary(v)
	if strings.Join(summary.Assignees, ",") != "qa2" || summary.Stage != "测试审批" || summary.UpdatedAt != 120 {
		t.Fatalf("wrong all-sign handoff %+v", summary)
	}
	v.Steps[0].Status = "success"
	summary = researchRunSummary(v)
	if strings.Join(summary.Assignees, ",") != "pm" || summary.Progress != "1 / 2" {
		t.Fatalf("wrong next stage %+v", summary)
	}
	v.Status = "success"
	v.Steps[1].Status = "success"
	summary = researchRunSummary(v)
	if !strings.Contains(summary.Stage, "尚未发布") || summary.Assignees[0] != "owner" {
		t.Fatalf("preparation mislabeled publication %+v", summary)
	}
	v.Plan.Template.Workbench = ""
	summary = researchRunSummary(v)
	if summary.Stage != "交付完成" || len(summary.Assignees) != 0 {
		t.Fatalf("actual result %+v", summary)
	}
}

func TestResearchCollaborationOwnedPaginationVisibilityAndSnapshot(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	mux := http.NewServeMux()
	registerResearchCollaboration(mux, s)
	v := collaborationRun(t, s, "owner", "pending_approval", false)
	for i := 0; i < 105; i++ {
		collaborationRun(t, s, "owner", "approved", true)
	}
	collaborationRun(t, s, "Owner", "approved", true)
	w := collaborationRequest(mux, "GET", "/api/research/deliveries?page=6", "owner", "")
	var page struct {
		Total int
		Items []ResearchTask
	}
	json.Unmarshal(w.Body.Bytes(), &page)
	if w.Code != 200 || page.Total != 106 || len(page.Items) != 6 {
		t.Fatalf("pagination %d %s", w.Code, w.Body.String())
	}
	for _, i := range page.Items {
		if i.Owner != "owner" {
			t.Fatal("case-fold owner leak")
		}
	}
	for _, size := range []int{50, 100} {
		w = collaborationRequest(mux, "GET", fmt.Sprintf("/api/research/deliveries?page=2&page_size=%d", size), "owner", "")
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || page.Total != 106 || len(page.Items) != min(size, 106-size) {
			t.Fatal("actual research page size mismatch", size, w.Body.String())
		}
	}
	path := fmt.Sprintf("/api/research/deliveries/delivery/%d", v.ID)
	if w = collaborationRequest(mux, "GET", path, "stranger", ""); w.Code != 403 {
		t.Fatal("foreign snapshot visible", w.Code)
	}
	w = collaborationRequest(mux, "GET", path, "qa", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "DO_NOT_EXPOSE") || !strings.Contains(w.Body.String(), "回退原菜单") {
		t.Fatal("review snapshot incomplete or encrypted backup leaked", w.Body.String())
	}
	if s.DeliveryRun(v.ID).Status != "pending_approval" {
		t.Fatal("reading advanced workflow")
	}
}

func TestResearchCollaborationExceptionsCloseWithoutReplayingOrFakingSuccess(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	mux := http.NewServeMux()
	registerResearchCollaboration(mux, s)
	v := collaborationRun(t, s, "owner", "failed", false)
	items, e := s.unresolvedResearchExceptions("owner")
	if e != nil || len(items) != 1 || items[0].ResolutionDigest == "" {
		t.Fatalf("exceptions %+v %v", items, e)
	}
	item := items[0]
	body, _ := json.Marshal(map[string]string{"id": item.ID, "digest": item.ResolutionDigest, "comment": "已核对发布日志与实例，创建恢复交付 #123"})
	if w := collaborationRequest(mux, "POST", "/api/research/exceptions/resolve", "stranger", string(body)); w.Code != 409 {
		t.Fatal("foreign exception writable")
	}
	if w := collaborationRequest(mux, "POST", "/api/research/exceptions/resolve", "owner", string(body)); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	items, e = s.unresolvedResearchExceptions("owner")
	if e != nil || len(items) != 0 {
		t.Fatal("handled exception remained", e, items)
	}
	if s.DeliveryRun(v.ID).Status != "failed" {
		t.Fatal("exception review forged successful deployment")
	}
	history, e := s.researchExceptionHistory("owner")
	if e != nil || len(history) != 1 || !strings.Contains(history[0].NextAction, "#123") {
		t.Fatal("missing handling evidence", e, history)
	}
	v.Revision = s.DeliveryRun(v.ID).Revision
	v.Steps[0].Detail = "new failure"
	if e = s.saveDeliveryRun(v); e != nil {
		t.Fatal(e)
	}
	items, e = s.unresolvedResearchExceptions("owner")
	if e != nil || len(items) != 1 || items[0].ResolutionDigest == item.ResolutionDigest {
		t.Fatal("old resolution hid new failure", e, items)
	}
	if w := collaborationRequest(mux, "POST", "/api/research/exceptions/resolve", "owner", string(body)); w.Code != 409 {
		t.Fatal("stale exception digest accepted")
	}
}

func TestResearchCollaborationUncertainSourceCannotBeDismissed(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	mux := http.NewServeMux()
	registerResearchCollaboration(mux, s)
	op := ResearchOperation{ID: "uncertain-op", State: "uncertain", Message: "结果待核对"}
	raw, _ := json.Marshal(op)
	_, e = s.db.Exec(`INSERT INTO research_operations(operation_id,work_item_id,actor,request_digest,state,result,created_at) VALUES(?,7,'owner','digest','uncertain',?,100)`, op.ID, string(raw))
	if e != nil {
		t.Fatal(e)
	}
	items, e := s.unresolvedResearchExceptions("owner")
	if e != nil || len(items) != 1 || items[0].SourceKind != "operation" {
		t.Fatal(e, items)
	}
	body, _ := json.Marshal(map[string]string{"id": items[0].ID, "digest": "fake", "comment": "忽略来源状态不再核对"})
	if w := collaborationRequest(mux, "POST", "/api/research/exceptions/resolve", "owner", string(body)); w.Code != 409 {
		t.Fatal("unconfirmed source operation dismissed")
	}
	ignoreBody, _ := json.Marshal(map[string]string{"id": items[0].ID, "digest": "fake", "comment": "不能忽略结果未知的回写", "action": "ignore"})
	if w := collaborationRequest(mux, "POST", "/api/research/exceptions/resolve", "owner", string(ignoreBody)); w.Code != 409 {
		t.Fatal("ignore bypassed uncertain source verification")
	}
	if _, e = s.db.Exec(`UPDATE research_operations SET state='success' WHERE operation_id=?`, op.ID); e != nil {
		t.Fatal(e)
	}
	items, e = s.unresolvedResearchExceptions("owner")
	if e != nil || len(items) != 0 {
		t.Fatal("verified source did not leave inbox", e, items)
	}
}

func TestResearchCollaborationLinksOnlyFrozenReferences(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	prep := collaborationRun(t, s, "owner", "success", true)
	actual := collaborationRun(t, s, "owner", "success", false)
	actual.Plan.Version = &ReleaseVersion{ID: 1, Version: "v1"}
	actual.Plan.PreparationRunID = prep.ID
	raw, _ := json.Marshal(actual.Plan)
	if _, e = s.db.Exec(`UPDATE delivery_runs SET plan=? WHERE id=?`, string(raw), actual.ID); e != nil {
		t.Fatal(e)
	}
	collaborationRun(t, s, "owner", "success", false) // Same Bug is NOT association proof.
	linked, e := s.researchLinkedDeliveries(prep, "owner")
	if e != nil || len(linked) != 1 || linked[0].RunID != actual.ID {
		t.Fatal("false or missing handoff", e, linked)
	}
	linked, e = s.researchLinkedDeliveries(actual, "owner")
	if e != nil || len(linked) != 1 || linked[0].RunID != prep.ID {
		t.Fatal("reverse handoff missing", e, linked)
	}
	linked, e = s.researchLinkedDeliveries(prep, "stranger")
	if e != nil || len(linked) != 0 {
		t.Fatal("foreign linked delivery leak", e, linked)
	}
}

func TestResearchCollaborationRepeatedCoordinatorInterruptionIsNewTask(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	v := collaborationRun(t, s, "owner", "running", false)
	_, e = s.db.Exec(`INSERT INTO delivery_controls(run_id,state,actor,lease,lease_until,message) VALUES(?,'running','owner','old-lease',1,'stopped')`, v.ID)
	if e != nil {
		t.Fatal(e)
	}
	items, e := s.unresolvedResearchExceptions("owner")
	if e != nil || len(items) != 1 || items[0].EventDigest == "" {
		t.Fatal("missing coordinator task", e, items)
	}
	mux := http.NewServeMux()
	registerResearchCollaboration(mux, s)
	body, _ := json.Marshal(map[string]string{"id": items[0].ID, "digest": items[0].ResolutionDigest, "comment": "已核对中断现场并记录处理结果"})
	if w := collaborationRequest(mux, "POST", "/api/research/exceptions/resolve", "owner", string(body)); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	_, e = s.db.Exec(`UPDATE delivery_controls SET lease='new-lease',lease_until=2 WHERE run_id=?`, v.ID)
	if e != nil {
		t.Fatal(e)
	}
	newItems, e := s.unresolvedResearchExceptions("owner")
	if e != nil || len(newItems) != 1 || newItems[0].ResolutionDigest == items[0].ResolutionDigest {
		t.Fatal("old review hid repeated interruption", e, newItems)
	}
}

func TestMySQLResearchCollaboration(t *testing.T) {
	dsn := os.Getenv("DEVOPS_TEST_COLLAB_MYSQL_DSN")
	if dsn == "" {
		t.Skip("requires disposable MySQL schema")
	}
	s, e := OpenDatabase(t.TempDir(), DatabaseConfig{Driver: "mysql", DSN: dsn})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	collaborationRun(t, s, "Alice", "failed", false)
	collaborationRun(t, s, "alice", "approved", true)
	a, e := s.researchOwnedDeliveries("Alice")
	if e != nil || len(a) != 1 || a[0].Owner != "Alice" {
		t.Fatal("case sensitive owner", e, a)
	}
	items, e := s.unresolvedResearchExceptions("Alice")
	if e != nil || len(items) != 1 {
		t.Fatal("mysql exceptions", e, items)
	}
	mux := http.NewServeMux()
	registerResearchCollaboration(mux, s)
	body, _ := json.Marshal(map[string]string{"id": items[0].ID, "digest": items[0].ResolutionDigest, "comment": "已核对实例并记录恢复结果"})
	if w := collaborationRequest(mux, "POST", "/api/research/exceptions/resolve", "Alice", string(body)); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	h, e := s.researchExceptionHistory("alice")
	if e != nil || len(h) != 0 {
		t.Fatal("case sensitive exception history", e, h)
	}
}
