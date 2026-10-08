package deploy

import (
	"strings"
	"testing"
)

func TestIndependentChangeAndReleasePromotionLifecycle(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.InitializeEnvironments([]Environment{
		{Name: "test", DisplayName: "测试"},
		{Name: "prod", DisplayName: "生产", Protected: true, Approvers: "reviewer"},
	}); err != nil {
		t.Fatal(err)
	}

	work := &WorkItem{Source: "zentao", ExternalID: "BUG-100", Kind: "bug", Title: "修复发布异常", Owner: "alice"}
	if err := st.CreateWorkItem(work); err != nil {
		t.Fatal(err)
	}
	change := &Change{Kind: "code", Title: "修复部署状态", Service: "estate-ai-seek", Reference: "feature/BUG-100", RiskLevel: "medium", Owner: "alice"}
	if err := st.CreateChange(change); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChangeWorkItems(change.ID, []int64{work.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetChangeReady(change.ID, true); err != nil {
		t.Fatal(err)
	}

	version := &ReleaseVersion{Version: "2026.10", Title: "十月发布", Owner: "alice"}
	if err := st.CreateReleaseVersion(version); err != nil {
		t.Fatal(err)
	}
	if err := st.SetReleaseVersionChanges(version.ID, []int64{change.ID}); err != nil {
		t.Fatal(err)
	}
	ready, err := st.SetReleaseVersionReady(version.ID)
	if err != nil || ready.Status != ReleaseVersionReady {
		t.Fatalf("ready release: %#v %v", ready, err)
	}

	testPromotion, err := st.CreateReleasePromotion(version.ID, "test", "alice")
	if err != nil || testPromotion.Status != PromotionApproved {
		t.Fatalf("test promotion: %#v %v", testPromotion, err)
	}
	if _, err := st.SetReleasePromotionStatus(testPromotion.ID, []string{PromotionApproved}, PromotionSuccess); err != nil {
		t.Fatal(err)
	}
	prodPromotion, err := st.CreateReleasePromotion(version.ID, "prod", "alice")
	if err != nil || prodPromotion.Status != PromotionPendingApproval {
		t.Fatalf("prod promotion: %#v %v", prodPromotion, err)
	}
	if _, err := st.ReviewReleasePromotion(prodPromotion.ID, "alice", true); err == nil || !strings.Contains(err.Error(), "不能审核") {
		t.Fatalf("owner unexpectedly approved promotion: %v", err)
	}
	approved, err := st.ReviewReleasePromotion(prodPromotion.ID, "reviewer", true)
	if err != nil || approved.Status != PromotionApproved {
		t.Fatalf("approve production: %#v %v", approved, err)
	}
}

func TestChangeAllowsIndependentWorkAndRequiresReadyChange(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	change := &Change{Kind: "nacos", Title: "调整超时时间", RiskLevel: "low", Owner: "alice"}
	if err := st.CreateChange(change); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetChangeReady(change.ID, true); err != nil {
		t.Fatalf("independent change cannot become ready: %v", err)
	}
	if _, err := st.SetChangeReady(change.ID, false); err != nil {
		t.Fatal(err)
	}
	work := &WorkItem{Title: "调整超时时间", Owner: "alice"}
	if err := st.CreateWorkItem(work); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChangeWorkItems(change.ID, []int64{work.ID}); err != nil {
		t.Fatal(err)
	}

	version := &ReleaseVersion{Version: "v1", Title: "版本一", Owner: "alice"}
	if err := st.CreateReleaseVersion(version); err != nil {
		t.Fatal(err)
	}
	if err := st.SetReleaseVersionChanges(version.ID, []int64{change.ID}); err == nil {
		t.Fatal("draft change was added to release")
	}
	if _, err := st.SetChangeReady(change.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetReleaseVersionChanges(version.ID, []int64{change.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetReleaseVersionReady(version.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetChangeReady(change.ID, false); err == nil {
		t.Fatal("versioned change was reopened")
	}
	if len(st.Changes("", "", "", 100)) != 1 || len(st.ReleaseVersions("", 100)) != 1 {
		t.Fatal("list associations are incomplete")
	}
}
