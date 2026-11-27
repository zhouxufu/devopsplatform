package deploy

import (
	"strings"
	"testing"
)

func approvalFixture(t *testing.T) (*Store, *DeliveryRun) {
	t.Helper()
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	if e = s.UpsertEnvironment(Environment{Name: "test", DisplayName: "测试"}); e != nil {
		t.Fatal(e)
	}
	nodes := []DeliveryNode{{ID: "dev-review", Kind: "approval", Name: "开发审核", ApprovalMode: "any", Approvers: []string{"dev", "dev-backup"}}, {ID: "test-review", Kind: "approval", Name: "测试审核", ApprovalMode: "all", Approvers: []string{"qa1", "qa2"}}, {ID: "pm-review", Kind: "approval", Name: "项目审核", ApprovalMode: "any", Approvers: []string{"pm"}}, {ID: "observe", Kind: "manual", Name: "确认", Instructions: "fixture only"}}
	tpl := &DeliveryTemplate{Name: "审核fixture", Mode: "fixed", Nodes: nodes, Owner: "author"}
	if e = s.SaveDeliveryTemplate(tpl); e != nil {
		t.Fatal(e)
	}
	m := NewReleaseManager(s, NewDeployer(s, NewLogHub(), ""))
	v, e := s.CreateDeliveryRun(DeliveryRequest{TemplateID: tpl.ID, Environment: "test"}, "author", m)
	if e != nil {
		t.Fatal(e)
	}
	return s, v
}
func TestThreeStageApprovalAnyAll(t *testing.T) {
	s, v := approvalFixture(t)
	m := NewReleaseManager(s, NewDeployer(s, NewLogHub(), ""))
	if v.Status != "pending_approval" {
		t.Fatal("nodes did not gate execution")
	}
	if _, e := s.AdvanceDeliveryRun(v.ID, "author", true, m); e == nil {
		t.Fatal("executed before review")
	}
	if _, e := s.ReviewDeliveryNode(v.ID, "qa1", v.Digest, "test-review", "", true); e == nil {
		t.Fatal("early signing allowed")
	}
	if _, e := s.ReviewDeliveryNode(v.ID, "author", v.Digest, "dev-review", "", true); e == nil {
		t.Fatal("self approval")
	}
	if _, e := s.ReviewDeliveryRun(v.ID, "dev", v.Digest, true); e == nil {
		t.Fatal("legacy review bypass")
	}
	review := func(actor, node string) {
		t.Helper()
		var e error
		v, e = s.ReviewDeliveryNode(v.ID, actor, v.Digest, node, "reviewed", true)
		if e != nil {
			t.Fatal(e)
		}
	}
	review("dev", "dev-review")
	if v.Steps[0].Status != "success" || v.Status != "pending_approval" {
		t.Fatal("any signing did not advance")
	}
	if _, e := s.ReviewDeliveryNode(v.ID, "dev-backup", v.Digest, "dev-review", "", true); e == nil {
		t.Fatal("replayed old node")
	}
	review("qa1", "test-review")
	if v.Steps[1].Status == "success" {
		t.Fatal("all signing accepted single vote")
	}
	if _, e := s.ReviewDeliveryNode(v.ID, "qa1", v.Digest, "test-review", "", true); e == nil {
		t.Fatal("duplicate vote")
	}
	review("qa2", "test-review")
	review("pm", "pm-review")
	if v.Status != "approved" || len(v.Steps[1].Reviews) != 2 || v.Steps[3].Status != "pending" {
		t.Fatal("review auto-executed or lost votes")
	}
	if _, e := s.AdvanceDeliveryRun(v.ID, "author", true, m); e != nil {
		t.Fatal(e)
	}
}
func TestApprovalRejectAndSnapshotGuard(t *testing.T) {
	s, v := approvalFixture(t)
	if _, e := s.ReviewDeliveryNode(v.ID, "dev", "changed", "dev-review", "", true); e == nil {
		t.Fatal("changed digest accepted")
	}
	rejected, e := s.ReviewDeliveryNode(v.ID, "dev", v.Digest, "dev-review", "", false)
	if e != nil || rejected.Status != "rejected" {
		t.Fatal(e)
	}
	tpl := s.DeliveryTemplate(v.Plan.Template.ID)
	req := DeliveryRequest{TemplateID: tpl.ID, Environment: "test", Nodes: tpl.Nodes[3:]}
	_, _, e = s.ResolveDelivery(req, NewReleaseManager(s, NewDeployer(s, NewLogHub(), "")))
	if e == nil || !strings.Contains(e.Error(), "不能删除") {
		t.Fatal("removed mandatory review", e)
	}
	if e := validateDeliveryNodes([]DeliveryNode{tpl.Nodes[3], tpl.Nodes[0]}); e == nil {
		t.Fatal("review after writes allowed")
	}
}
