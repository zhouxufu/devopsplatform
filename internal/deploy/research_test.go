package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func researchFixture(t *testing.T, readHooks ...func(string, map[string]any)) (*Store, *WorkItem, *int, *sync.Mutex) {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	bug := map[string]any{"id": "7", "title": "菜单问题", "status": "active", "steps": "<p>描述</p>", "openedBy": "qa", "assignedTo": "alice", "resolvedBy": "", "closedBy": "", "resolution": "", "lastEditedDate": "2026-10-13 10:00:00", "severity": "3", "pri": "2", "type": "codeerror", "product": "1"}
	actions := map[string]any{}
	writes := 0
	mu := &sync.Mutex{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		method := r.URL.Query().Get("f")
		var data any
		switch method {
		case "getSessionID":
			data = map[string]string{"sessionName": "zentaosid", "sessionID": "test-session"}
		case "login":
			r.ParseForm()
			if r.PostForm.Get("account") != "alice" || r.PostForm.Get("password") != "personal-secret" {
				json.NewEncoder(w).Encode(map[string]string{"status": "fail"})
				return
			}
			data = map[string]any{"user": map[string]string{"account": "alice"}}
		default:
			cookie, _ := r.Cookie("zentaosid")
			if cookie == nil {
				t.Error("missing source session")
			}
			if r.Method == http.MethodPost {
				writes++
				if !strings.HasPrefix(r.Header.Get("Referer"), "http://") {
					t.Error("missing same-origin Referer")
				}
				r.ParseMultipartForm(16 << 20)
				form := r.PostForm
				if form.Get("comment") == "" {
					t.Error("missing action marker")
				}
				if form.Get("resolvedBy") != "" && form.Get("resolvedBy") != "alice" {
					t.Error("spoofed source actor")
				}
				kind := "edited"
				switch method {
				case "resolve":
					bug["status"] = "resolved"
					bug["resolution"] = form.Get("resolution")
					bug["resolvedBuild"] = form.Get("resolvedBuild")
					bug["resolvedBy"] = "alice"
					bug["assignedTo"] = form.Get("assignedTo")
					kind = "resolved"
				case "close":
					bug["status"] = "closed"
					bug["closedBy"] = "alice"
					kind = "closed"
				case "activate":
					bug["status"] = "active"
					kind = "activated"
				case "assignTo":
					bug["assignedTo"] = form.Get("assignedTo")
					kind = "assigned"
				case "edit":
					bug["title"] = form.Get("title")
					bug["steps"] = form.Get("steps")
				}
				bug["lastEditedDate"] = fmt.Sprint(writes)
				actions[fmt.Sprint(writes)] = map[string]any{"actor": "alice", "action": kind, "comment": form.Get("comment")}
				http.Redirect(w, r, "/zentao/bug-view-7.html", 302)
				return
			}
			readBug := map[string]any{}
			for k, v := range bug {
				readBug[k] = v
			}
			for _, hook := range readHooks {
				hook(method, readBug)
			}
			data = map[string]any{"bug": readBug, "actions": actions, "users": map[string]string{"alice": "Alice", "qa": "测试"}, "builds": map[string]string{"trunk": "主干", "12": "构建12"}, "assignedTo": "qa"}
		}
		raw, _ := json.Marshal(data)
		json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": string(raw)})
	}))
	t.Cleanup(server.Close)
	if err = s.UpsertConnection(ResourceConnection{Name: "issues", Kind: "zentao", Endpoint: server.URL + "/zentao", Username: "sync", Password: "unused-sync-secret", Config: map[string]string{"product_ids": "1"}}); err != nil {
		t.Fatal(err)
	}
	witem := &WorkItem{Source: "zentao:issues", ExternalID: "7", Kind: "bug", Title: "菜单问题", Status: "open", ExternalStatus: "active", Owner: "sync"}
	if err = s.CreateWorkItem(witem); err != nil {
		t.Fatal(err)
	}
	if err = s.AuthorizeResearch(context.Background(), "issues", "platform-alice", ResearchCredential{Account: "alice", Password: "personal-secret"}); err != nil {
		t.Fatal(err)
	}
	return s, witem, &writes, mu
}

func TestResearchResolveUsesPersonalIdentityAndIdempotentHistory(t *testing.T) {
	s, w, writes, mu := researchFixture(t)
	z, _, err := s.researchClient(context.Background(), w, "platform-alice")
	if err != nil {
		t.Fatal(err)
	}
	before, err := z.researchRead(context.Background(), "7", "view")
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	request := ResearchAction{OperationID: strings.Repeat("a", 32), Action: "resolve", ExpectedDigest: before.Digest, Resolution: "fixed", ResolvedBuild: "trunk", AssignedTo: "qa", Comment: "已修复", Metadata: ResearchMetadata{ReleaseRequired: &yes}}
	op, err := s.ApplyResearchAction(context.Background(), w.ID, "platform-alice", request)
	if err != nil || op.State != "success" {
		t.Fatalf("resolve: %+v %v", op, err)
	}
	item := s.GetWorkItem(w.ID)
	if item.ExternalStatus != "resolved" || item.Status == "closed" || item.ResolvedBy != "alice" || item.ReleaseRequired == nil || !*item.ReleaseRequired {
		t.Fatalf("local result: %+v", item)
	}
	op, err = s.ApplyResearchAction(context.Background(), w.ID, "platform-alice", request)
	if err != nil || op.State != "success" {
		t.Fatal(err)
	}
	mu.Lock()
	count := *writes
	mu.Unlock()
	if count != 1 {
		t.Fatal("repeated source write", count)
	}
	activity, err := s.ResearchActivities(w.ID)
	if err != nil || len(activity) != 1 || activity[0].OperationID != request.OperationID || activity[0].SourceActor != "alice" || op.ActivityID != activity[0].ID {
		t.Fatalf("operation audit association: %+v, %+v, %v", op, activity, err)
	}
	if _, err = s.ResearchOperation(op.ID, "other-user"); err == nil {
		t.Fatal("foreign operation exposed")
	}
	var secret string
	s.db.QueryRow(`SELECT secret FROM research_credentials WHERE actor='platform-alice'`).Scan(&secret)
	if strings.Contains(secret, "personal-secret") {
		t.Fatal("plain credential stored")
	}
	request.Comment = "different"
	if _, err = s.ApplyResearchAction(context.Background(), w.ID, "platform-alice", request); err == nil {
		t.Fatal("operation id reused with new contents")
	}
}

func TestResearchRejectsStaleOrMissingIdentityBeforeWrites(t *testing.T) {
	s, w, writes, mu := researchFixture(t)
	yes := true
	v := ResearchAction{OperationID: strings.Repeat("b", 32), Action: "resolve", ExpectedDigest: "stale", Resolution: "fixed", ResolvedBuild: "trunk", Metadata: ResearchMetadata{ReleaseRequired: &yes}}
	if _, err := s.ApplyResearchAction(context.Background(), w.ID, "platform-alice", v); err == nil {
		t.Fatal("stale write accepted")
	}
	if _, err := s.ApplyResearchAction(context.Background(), w.ID, "another", v); err == nil {
		t.Fatal("sync account reused for another user")
	}
	mu.Lock()
	count := *writes
	mu.Unlock()
	if count != 0 {
		t.Fatal("invalid request wrote remote state")
	}
}

func TestResearchActionDigestUsesCanonicalView(t *testing.T) {
	s, w, writes, mu := researchFixture(t, func(method string, bug map[string]any) {
		bug["steps"] = `<p><img src="/file-read-1.png" /></p>`
		if method == "view" {
			bug["steps"] = `<p><img onload="setImageSize(this,0)" src="/file-read-1.png" /></p>`
		}
	})
	ctx := context.Background()
	z, _, _ := s.researchClient(ctx, w, "platform-alice")
	view, err := z.researchRead(ctx, "7", "view")
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"assignTo", "resolve", "activate", "close", "edit"} {
		options, err := z.researchRead(ctx, "7", method)
		if err != nil || options.Digest != view.Digest {
			t.Fatalf("%s digest mismatch: %+v %v", method, options, err)
		}
		if options.Bug["steps"] == view.Bug["steps"] || options.Users["qa"] == "" {
			t.Fatal("action fields replaced with view")
		}
	}
	options, _ := z.researchRead(ctx, "7", "assignTo")
	op, err := s.ApplyResearchAction(ctx, w.ID, "platform-alice", ResearchAction{OperationID: strings.Repeat("c", 32), Action: "assignTo", ExpectedDigest: options.Digest, AssignedTo: "qa"})
	if err != nil || op.State != "success" {
		t.Fatalf("assign with image: %+v %v", op, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if *writes != 1 {
		t.Fatalf("writes = %d", *writes)
	}
}

func TestResearchRealConflictRefreshAndExplicitResubmit(t *testing.T) {
	changed := false
	s, w, writes, mu := researchFixture(t, func(_ string, bug map[string]any) {
		if changed {
			bug["title"] = "他人修改了标题"
		}
	})
	ctx := context.Background()
	z, _, _ := s.researchClient(ctx, w, "platform-alice")
	options, _ := z.researchRead(ctx, "7", "assignTo")
	mu.Lock()
	changed = true
	mu.Unlock()
	v := ResearchAction{OperationID: strings.Repeat("d", 32), Action: "assignTo", ExpectedDigest: options.Digest, AssignedTo: "qa", Comment: "保留填写"}
	_, err := s.ApplyResearchAction(ctx, w.ID, "platform-alice", v)
	var conflict *researchSourceConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("expected typed conflict: %v", err)
	}
	mu.Lock()
	count := *writes
	mu.Unlock()
	if count != 0 {
		t.Fatal("conflict wrote source")
	}
	options, err = z.researchRead(ctx, "7", "assignTo")
	if err != nil || options.Bug["title"] != "他人修改了标题" {
		t.Fatal("latest data unavailable", err)
	}
	v.ExpectedDigest = options.Digest
	op, err := s.ApplyResearchAction(ctx, w.ID, "platform-alice", v)
	if err != nil || op.State != "success" {
		t.Fatalf("explicit resubmit: %+v %v", op, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if *writes != 1 {
		t.Fatalf("writes = %d", *writes)
	}
}

func TestResearchConflictResponseIsMachineReadable(t *testing.T) {
	s, w, writes, mu := researchFixture(t)
	mux := http.NewServeMux()
	registerResearchRoutes(mux, s, nil, nil)
	body := fmt.Sprintf(`{"operation_id":"%s","action":"assignTo","expected_digest":"stale","assigned_to":"qa"}`, strings.Repeat("e", 32))
	r := httptest.NewRequest("POST", fmt.Sprintf("/api/research/work-items/%d/actions", w.ID), strings.NewReader(body))
	r.Header.Set("X-User", "platform-alice")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, r)
	var result map[string]string
	json.Unmarshal(response.Body.Bytes(), &result)
	if response.Code != 409 || result["code"] != "research_source_conflict" {
		t.Fatal(response.Code, response.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if *writes != 0 {
		t.Fatal("API conflict wrote source")
	}
}

func TestResearchConflictDuringActionReadDoesNotWrite(t *testing.T) {
	views := 0
	s, w, writes, mu := researchFixture(t, func(method string, bug map[string]any) {
		if method == "view" {
			views++
		}
		if views >= 3 {
			bug["assignedTo"] = "qa"
		}
	})
	ctx := context.Background()
	z, _, _ := s.researchClient(ctx, w, "platform-alice")
	options, _ := z.researchRead(ctx, "7", "assignTo")
	_, err := s.ApplyResearchAction(ctx, w.ID, "platform-alice", ResearchAction{OperationID: strings.Repeat("f", 32), Action: "assignTo", ExpectedDigest: options.Digest, AssignedTo: "qa"})
	var conflict *researchSourceConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("expected conflict between reads: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if *writes != 0 {
		t.Fatal("concurrent change was overwritten")
	}
}

func TestResearchReleaseIntentCASAndCandidateFilter(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w := &WorkItem{Title: "custom", Source: "manual", Kind: "bug", Owner: "alice"}
	if err = s.CreateWorkItem(w); err != nil {
		t.Fatal(err)
	}
	no := false
	if err = s.SetResearchMetadata(w.ID, ResearchMetadata{ReleaseRequired: &no}, "alice"); err == nil {
		t.Fatal("missing reason accepted")
	}
	if err = s.SetResearchMetadata(w.ID, ResearchMetadata{ReleaseRequired: &no, NoReleaseReason: "重复问题"}, "alice"); err != nil {
		t.Fatal(err)
	}
	yes := true
	if err = s.SetResearchMetadata(w.ID, ResearchMetadata{ReleaseRequired: &yes}, "alice"); err == nil {
		t.Fatal("stale revision accepted")
	}
	page, err := s.WorkItemsPage(WorkItemFilter{ReleaseRequired: "no"})
	if err != nil || page.Total != 1 {
		t.Fatalf("filter: %+v %v", page, err)
	}
	v := &ReleaseVersion{Version: "v1", Title: "v1", Owner: "alice"}
	if err = s.SaveReleaseDraft(v, VersionScope{WorkItemIDs: []int64{w.ID}}); err == nil {
		t.Fatal("non-release work item planned")
	}
}

func TestResearchWorkflowSequentialTasksAndScopeDrift(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.UpsertEnvironment(Environment{Name: "dev", DisplayName: "开发"}); err != nil {
		t.Fatal(err)
	}
	w := &WorkItem{Title: "custom", Source: "manual", Kind: "bug", Owner: "alice"}
	s.CreateWorkItem(w)
	c := &Change{Title: "operation", Kind: "manual", Summary: "核对", Owner: "alice"}
	s.CreateChange(c)
	if err = s.SetChangeWorkItems(c.ID, []int64{w.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetChangeReady(c.ID, true); err != nil {
		t.Fatal(err)
	}
	template := &DeliveryTemplate{Name: "准备", Mode: "version", Workbench: "research", Owner: "alice", Nodes: []DeliveryNode{{ID: "confirm", Kind: "manual", Name: "解决人确认", Instructions: "确认版本内容", AssigneeSource: "work_item_resolvers", ApprovalMode: "all"}, {ID: "test", Kind: "approval", Name: "测试审批", Approvers: []string{"bob", "carol"}, ApprovalMode: "all"}, {ID: "product", Kind: "approval", Name: "产品审批", Approvers: []string{"dave"}, ApprovalMode: "any"}}}
	if err = saveUnifiedPreparationFixture(s, template); err != nil {
		t.Fatal(err)
	}
	enabled := []string{"alice", "bob", "carol", "dave"}
	run, err := s.CreateResearchFlow(DeliveryRequest{TemplateID: template.ID, SourceType: "work_items", WorkItemIDs: []int64{w.ID}, Environment: "dev"}, nil, enabled, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "waiting_confirmation" {
		t.Fatal(run.Status)
	}
	bobTasks, _ := s.ResearchTasks("bob", "todo")
	if len(bobTasks) != 0 {
		t.Fatal("future stage exposed as actionable")
	}
	if _, err = s.ReviewResearchTask(run.ID, "test", "bob", run.Digest, "", true, enabled); err == nil {
		t.Fatal("skipped confirmation")
	}
	run, err = s.ReviewResearchTask(run.ID, "confirm", "alice", run.Digest, "确认", true, enabled)
	if err != nil || run.Status != "pending_approval" {
		t.Fatalf("confirm %v %+v", err, run)
	}
	bobTasks, _ = s.ResearchTasks("bob", "todo")
	if len(bobTasks) != 1 {
		t.Fatal("current approval missing")
	}
	run, err = s.ReviewResearchTask(run.ID, "test", "bob", run.Digest, "", true, enabled)
	if err != nil || run.Steps[1].Status == "success" {
		t.Fatal("all-sign completed early", err)
	}
	if _, err = s.ReviewResearchTask(run.ID, "test", "bob", run.Digest, "", true, enabled); err == nil {
		t.Fatal("duplicate sign accepted")
	}
	run, err = s.ReviewResearchTask(run.ID, "test", "carol", run.Digest, "", true, enabled)
	if err != nil {
		t.Fatal(err)
	}
	added := &Change{Title: "新增处理项", Kind: "manual", Summary: "changed scope", Owner: "alice"}
	if err = s.CreateChange(added); err != nil {
		t.Fatal(err)
	}
	if err = s.SetChangeWorkItems(added.ID, []int64{w.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReviewResearchTask(run.ID, "product", "dave", run.Digest, "", true, enabled); err == nil {
		t.Fatal("stale confirmation remained valid")
	}
}

func TestResearchPartialConfirmationAndVersionGate(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.UpsertEnvironment(Environment{Name: "dev", DisplayName: "开发"}); err != nil {
		t.Fatal(err)
	}
	ids := []int64{}
	for _, name := range []string{"菜单", "权限"} {
		w := &WorkItem{Title: name, Source: "manual", Kind: "bug", Owner: "alice"}
		if err = s.CreateWorkItem(w); err != nil {
			t.Fatal(err)
		}
		c := &Change{Title: name, Kind: "manual", Summary: "核对", Owner: "alice"}
		if err = s.CreateChange(c); err != nil {
			t.Fatal(err)
		}
		if err = s.SetChangeWorkItems(c.ID, []int64{w.ID}); err != nil {
			t.Fatal(err)
		}
		if _, err = s.SetChangeReady(c.ID, true); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, w.ID)
	}
	tpl := &DeliveryTemplate{Name: "就绪确认", Mode: "version", Workbench: "research", Owner: "alice", Nodes: []DeliveryNode{{ID: "confirm", Kind: "manual", Name: "确认", Instructions: "确认版本内容", AssigneeSource: "work_item_resolvers", ApprovalMode: "all"}}}
	if err = saveUnifiedPreparationFixture(s, tpl); err != nil {
		t.Fatal(err)
	}
	v := &ReleaseVersion{Version: "v-partial", Title: "版本", Owner: "alice"}
	if err = s.SaveReleaseDraft(v, VersionScope{WorkItemIDs: ids}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetReleaseVersionReady(v.ID); err != nil {
		t.Fatal("version readiness must not depend on removed standalone preparation")
	}
	req := DeliveryRequest{ClientRequestID: strings.Repeat("c", 32), TemplateID: tpl.ID, SourceType: "version", VersionID: v.ID, Environment: "dev"}
	run, err := s.CreateResearchFlow(req, nil, []string{"alice"}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	repeat, err := s.CreateResearchFlow(req, nil, []string{"alice"}, "alice")
	if err != nil || repeat.ID != run.ID {
		t.Fatal("duplicate tasks", err)
	}
	if _, err = s.ReviewResearchTask(run.ID, "confirm", "alice", run.Digest, "", true, []string{"alice"}, []int64{9999}); err == nil {
		t.Fatal("confirmed foreign item")
	}
	run, err = s.ReviewResearchTask(run.ID, "confirm", "alice", run.Digest, "", true, []string{"alice"}, ids[:1])
	if err != nil || run.Status != "waiting_confirmation" {
		t.Fatalf("partial: %+v %v", run, err)
	}
	tasks, err := s.ResearchTasks("alice", "todo")
	if err != nil || len(tasks) != 1 {
		t.Fatal("remaining confirmation missing", err)
	}
	if s.GetReleaseVersion(v.ID).Status != ReleaseVersionReady {
		t.Fatal("preparation changed version readiness")
	}
	if _, err = s.ReviewResearchTask(run.ID, "confirm", "alice", run.Digest, "", true, []string{"alice"}, ids[:1]); err == nil {
		t.Fatal("confirmed twice")
	}
	run, err = s.ReviewResearchTask(run.ID, "confirm", "alice", run.Digest, "", true, []string{"alice"}, ids[1:])
	if err != nil || run.Status != "success" {
		t.Fatal("final confirmation", err)
	}
	if s.GetReleaseVersion(v.ID).Status != ReleaseVersionReady {
		t.Fatal("confirmation changed version readiness")
	}
	candidates, err := s.WorkCandidates(WorkCandidateFilter{Environment: "dev", IDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range candidates.Items {
		if candidate.SuccessfulDelivery || candidate.PartialDelivery {
			t.Fatal("preparation completion falsely counted as publication")
		}
	}
}

func TestResearchAttachmentsAndMarkersFailClosed(t *testing.T) {
	remote := &ResearchRemote{Bug: map[string]any{"steps": "<img src='{99.png}'>", "files": map[string]any{"7": map[string]any{"id": "7"}}}, Actions: json.RawMessage(`{"1":{"actor":"other","action":"resolved","comment":"[delivery-operation:abc]"}}`)}
	if researchFileAllowed(remote, "99") || !researchFileAllowed(remote, "7") || researchFileAllowed(nil, "7") {
		t.Fatal("text granted attachment access")
	}
	if researchMarkerFound(remote, "abc", "alice", "resolve") {
		t.Fatal("another actor's marker accepted")
	}
	remote.Actions = json.RawMessage(`{"1":{"actor":"alice","action":"edited","comment":"[delivery-operation:abc]"}}`)
	if researchMarkerFound(remote, "abc", "alice", "resolve") {
		t.Fatal("wrong action accepted")
	}
	if !researchMarkerFound(remote, "abc", "alice", "comment") {
		t.Fatal("actual comment marker not accepted")
	}
}

func TestResearchQueryTodoRequiresConcreteApprovers(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.UpsertEnvironment(Environment{Name: "dev", DisplayName: "开发", Approvers: "bob"}); err != nil {
		t.Fatal(err)
	}
	q := WorkbenchQueryTicket{Kind: "dbms", Owner: "alice", Query: WorkbenchQuery{Environment: "dev", SQL: "select 1"}, Approvers: map[string]string{}, Revision: 0}
	raw, _ := json.Marshal(q)
	if _, err = s.db.Exec(`INSERT INTO workbench_queries(kind,document,status,owner,revision) VALUES('dbms',?,'pending_approval','alice',7)`, string(raw)); err != nil {
		t.Fatal(err)
	}
	tasks, err := s.researchOtherTasks("bob", "todo")
	if err != nil || len(tasks) != 0 {
		t.Fatal("unassigned query approval exposed", err)
	}
	q.Approvers = map[string]string{"dev": "bob"}
	raw, _ = json.Marshal(q)
	s.db.Exec(`UPDATE workbench_queries SET document=?`, string(raw))
	tasks, err = s.researchOtherTasks("bob", "todo")
	if err != nil || len(tasks) != 1 {
		t.Fatal("query approval missing", err)
	}
	q.ID = 1
	q.Status = "pending_approval"
	q.Revision = 7
	if tasks[0].Digest != queryHash(q) {
		t.Fatal("query revision not frozen")
	}
}

func TestResearchCompletedCodeGeneratesNextConfirmationWithoutExecutingIt(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.UpsertEnvironment(Environment{Name: "dev", DisplayName: "开发"})
	w := &WorkItem{Title: "后端变更", Source: "manual", Kind: "bug", Owner: "alice"}
	if err = s.CreateWorkItem(w); err != nil {
		t.Fatal(err)
	}
	c := &Change{Title: "代码", Kind: "code", Service: "orders", Reference: "feature/fix", Owner: "alice"}
	if err = s.CreateChange(c); err != nil {
		t.Fatal(err)
	}
	if err = s.SetChangeWorkItems(c.ID, []int64{w.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetChangeReady(c.ID, true); err != nil {
		t.Fatal(err)
	}
	tpl := &DeliveryTemplate{Name: "代码后确认", Mode: "version", Workbench: "research", Owner: "alice", Nodes: []DeliveryNode{{ID: "code", Kind: "code-prepare", Name: "合入构建", Policies: map[string]CodePolicy{"orders": {Mode: "integration", TargetBranch: "dev"}}}, {ID: "confirm", Kind: "manual", Name: "确认", AssigneeSource: "work_item_resolvers", ApprovalMode: "all", Instructions: "确认本次交付"}}}
	if err = saveUnifiedPreparationFixture(s, tpl); err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateResearchFlow(DeliveryRequest{TemplateID: tpl.ID, SourceType: "work_items", WorkItemIDs: []int64{w.ID}, Environment: "dev"}, nil, []string{"alice"}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	p := &CodePreparation{ID: strings.Repeat("e", 32), State: "ready", Owner: "alice"}
	if err = s.saveCodePreparation(p); err != nil {
		t.Fatal(err)
	}
	run.Status = "running"
	run.Steps[0].Status = "running"
	run.Steps[0].Detail = p.ID
	if err = s.saveDeliveryRun(run); err != nil {
		t.Fatal(err)
	}
	s.reconcileResearchPreparation(p.ID)
	run = s.DeliveryRun(run.ID)
	if run.Status != "waiting_confirmation" || run.Steps[0].Status != "success" || run.Steps[1].Status == "success" {
		t.Fatal("code completion skipped manual confirmation", run)
	}
	tasks, err := s.ResearchTasks("alice", "todo")
	if err != nil || len(tasks) != 1 || tasks[0].NodeID != "confirm" {
		t.Fatal("next confirmation not generated", err)
	}
	s.reconcileResearchPreparation(p.ID)
	if s.DeliveryRun(run.ID).Revision != run.Revision {
		t.Fatal("completion callback applied twice")
	}
}
