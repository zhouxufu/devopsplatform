package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestReleaseNoticeReadsFeedbackWithoutImportOrSourceWrites(t *testing.T) {
	s, state := researchLiveFixture(t, 2)
	state.rows[0]["feedbackBy"] = "张三,李四"
	state.rows[0]["title"] = "来源现在改过标题"
	target := &noticeTarget{title: "v1", environment: "生产", status: "approved", digest: "frozen", items: []WorkItem{{ID: 1, Source: "zentao:issues", ExternalID: "1", Kind: "bug", Title: "发布时的菜单标题"}, {ID: 1, Source: "zentao:issues", ExternalID: "1", Kind: "bug", Title: "重复关联"}, {ID: 2, Source: "zentao:issues", ExternalID: "2", Kind: "bug", Title: "缺少反馈者"}, {ID: 3, Kind: "requirement", Title: "不是Bug"}}}
	n := s.generateReleaseNotice(context.Background(), target, "platform-alice")
	if len(n.Entries) != 2 || !strings.Contains(n.Text, "#1 发布时的菜单标题 @张三 @李四") || strings.Contains(n.Text, "来源现在改过标题") || strings.Contains(n.Text, "@qa") || strings.Contains(n.Text, "发布成功") {
		t.Fatal(n)
	}
	if len(n.Warnings) == 0 || !strings.Contains(n.Text, "未填写反馈者") {
		t.Fatal("missing warning", n)
	}
	state.mu.Lock()
	writes := state.businessWrites
	state.mu.Unlock()
	if writes != 0 {
		t.Fatal("source writes")
	}
	var count int
	s.db.QueryRow(`SELECT count(*) FROM work_items`).Scan(&count)
	if count != 0 {
		t.Fatal("Bug imported by notice preview")
	}
	state.mu.Lock()
	state.fail = true
	state.mu.Unlock()
	unavailable := s.generateReleaseNotice(context.Background(), target, "another-actor")
	if len(unavailable.Warnings) == 0 || strings.Contains(unavailable.Text, "@qa") {
		t.Fatal("silent fallback", unavailable)
	}
}

func TestReleaseNoticeSaveCASScopeGuardsAndApprovalIsolation(t *testing.T) {
	s, m, req, _, _ := standardManualFixture(t)
	req = standardSign(t, s, m, req)
	run, err := s.CreateDeliveryRun(req, "alice", m)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := fmt.Sprintf("/api/release-notices/delivery/%d", run.ID)
	mux := http.NewServeMux()
	registerReleaseNoticeRoutes(mux, s)
	response := collaborationRequest(mux, "GET", endpoint, "alice", "")
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	var n ReleaseNotice
	json.Unmarshal(response.Body.Bytes(), &n)
	n.Text = "本次发版：菜单修复 @张三"
	body, _ := json.Marshal(n)
	if response = collaborationRequest(mux, "PUT", endpoint, "stranger", string(body)); response.Code != 403 {
		t.Fatal("foreign edit")
	}
	if response = collaborationRequest(mux, "PUT", endpoint, "alice", string(body)); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if response = collaborationRequest(mux, "PUT", endpoint, "alice", string(body)); response.Code != 409 {
		t.Fatal("lost update")
	}
	saved, _ := s.savedReleaseNotice("delivery", run.ID)
	if saved.Revision != 1 || saved.Text != n.Text {
		t.Fatal(saved)
	}
	if current := s.DeliveryRun(run.ID); current.Digest != run.Digest || current.Revision != run.Revision || current.Status != run.Status {
		t.Fatal("notice modified approval snapshot")
	}
	run.Status = "success"
	if err = s.saveDeliveryRun(run); err != nil {
		t.Fatal(err)
	}
	saved.Text = "changed"
	if err = s.saveReleaseNotice("delivery", run.ID, saved, "alice"); err == nil {
		t.Fatal("status drift accepted")
	}
	response = collaborationRequest(mux, "GET", endpoint, "alice", "")
	json.Unmarshal(response.Body.Bytes(), &n)
	if !n.Stale {
		t.Fatal("old status not flagged")
	}
	// Existing notes must be retained after a source outage or failed regenerate.
	still, _ := s.savedReleaseNotice("delivery", run.ID)
	if still.Text != "本次发版：菜单修复 @张三" {
		t.Fatal("saved text lost")
	}
	if response = collaborationRequest(mux, "GET", "/api/release-notices/anything/1", "alice", ""); response.Code != 404 {
		t.Fatal("invalid kind accepted")
	}
}

func TestReleaseNoticeChildUsesFrozenParentAndScopeExclusions(t *testing.T) {
	s, m, req, _, w := standardManualFixture(t)
	req = standardSign(t, s, m, req)
	run, err := s.CreateDeliveryRun(req, "alice", m)
	if err != nil {
		t.Fatal(err)
	}
	child := &ReleaseOrder{Service: "orders", Environment: "test", Version: "v1", Actor: "alice", Status: "success"}
	if err = s.CreateReleaseOrder(child); err != nil {
		t.Fatal(err)
	}
	run.Steps[0].ReleaseID = child.ID
	if err = s.saveDeliveryRun(run); err != nil {
		t.Fatal(err)
	}
	target, err := s.releaseNoticeTarget("release", child.ID)
	if err != nil || len(target.items) != 1 || target.items[0].ID != w.ID {
		t.Fatal(target, err)
	}
	scope := &DeliveryScope{WorkItems: []WorkItem{{ID: 1}, {ID: 2}}, Changes: []Change{{ID: 3, WorkItems: []WorkItem{{ID: 1}}}}, ExcludedChangeIDs: []int64{4}}
	if items := noticeScopeItems(scope); len(items) != 1 || items[0].ID != 1 {
		t.Fatal("excluded Bug included", items)
	}
	scope.WorkItems = nil
	if items := noticeScopeItems(scope); len(items) != 1 {
		t.Fatal("independent change Bug links lost", items)
	}
}
