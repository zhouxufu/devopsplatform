package deploy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestReleaseDraftScopePlanning(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a, b := &WorkItem{Title: "需求 A"}, &WorkItem{Title: "需求 B"}
	for _, w := range []*WorkItem{a, b} {
		if err = st.CreateWorkItem(w); err != nil {
			t.Fatal(err)
		}
	}
	v := &ReleaseVersion{Version: "v1", Title: "先规划范围", Owner: "alice"}
	save := func(scope VersionScope) {
		t.Helper()
		if err = st.SaveReleaseDraft(v, scope); err != nil {
			t.Fatal(err)
		}
	}
	save(VersionScope{})
	if _, err = st.SetReleaseVersionReady(v.ID); err == nil {
		t.Fatal("empty version frozen")
	}
	save(VersionScope{WorkItemIDs: []int64{a.ID}})
	if len(st.GetReleaseVersion(v.ID).WorkItems) != 1 {
		t.Fatal("planned demand lost")
	}
	if err = st.DeleteWorkItem(a.ID); err == nil {
		t.Fatal("planned demand deleted")
	}
	own, shared, manual := &Change{Kind: "manual", Title: "独有变更"}, &Change{Kind: "manual", Title: "共享变更"}, &Change{Kind: "manual", Title: "独立变更"}
	for _, c := range []*Change{own, shared, manual} {
		if err = st.CreateChange(c); err != nil {
			t.Fatal(err)
		}
	}
	if err = st.SetChangeWorkItems(own.ID, []int64{a.ID}); err != nil {
		t.Fatal(err)
	}
	if err = st.SetChangeWorkItems(shared.ID, []int64{a.ID, b.ID}); err != nil {
		t.Fatal(err)
	}
	save(VersionScope{WorkItemIDs: []int64{a.ID, b.ID}, ManualChangeIDs: []int64{manual.ID, shared.ID}})
	got := st.GetReleaseVersion(v.ID)
	if len(got.Changes) != 3 || len(got.WorkItems) != 2 {
		t.Fatalf("scope not deduplicated: %#v", got)
	}
	save(VersionScope{WorkItemIDs: []int64{b.ID}, ManualChangeIDs: []int64{manual.ID, shared.ID}})
	got = st.GetReleaseVersion(v.ID)
	if len(got.Changes) != 2 || got.Changes[0].ID != shared.ID || got.Changes[1].ID != manual.ID {
		t.Fatalf("deselect failed: %#v", got.Changes)
	}
	if !reflect.DeepEqual(got.ManualChangeIDs, []int64{manual.ID, shared.ID}) {
		t.Fatal("manual provenance lost")
	}
	if _, err = st.SetReleaseVersionReady(v.ID); err == nil {
		t.Fatal("draft changes frozen")
	}
	v.Title = "should rollback"
	if err = st.SaveReleaseDraft(v, VersionScope{ManualChangeIDs: []int64{999999}}); err == nil {
		t.Fatal("unknown change accepted")
	}
	if st.GetReleaseVersion(v.ID).Title != "先规划范围" || len(st.GetReleaseVersion(v.ID).Changes) != 2 {
		t.Fatal("invalid scope partially saved")
	}
	for _, c := range []*Change{shared, manual} {
		if _, err = st.SetChangeReady(c.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = st.SetChangeReady(shared.ID, false); err != nil {
		t.Fatal("draft scope prevented editing")
	}
	if _, err = st.SetReleaseVersionReady(v.ID); err == nil {
		t.Fatal("reopened change frozen")
	}
	if _, err = st.SetChangeReady(shared.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err = st.SetReleaseVersionReady(v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = st.SetChangeReady(shared.ID, false); err == nil {
		t.Fatal("frozen change reopened")
	}
	if err = st.SaveReleaseDraft(v, VersionScope{}); err == nil {
		t.Fatal("frozen scope edited")
	}
}

func TestReleaseDraftAPIAtomicAndOwned(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	v := &ReleaseVersion{Version: "original", Title: "保留原范围", Owner: "alice"}
	if err = st.SaveReleaseDraft(v, VersionScope{}); err != nil {
		t.Fatal(err)
	}
	h := Routes(st, NewBuilder(st, dir, NewLogHub()), NewLogHub(), "", NewStepLib(dir+"/steps", dir+"/custom"))
	request := func(method, path, actor, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("X-User", actor)
		out := httptest.NewRecorder()
		h.ServeHTTP(out, r)
		return out
	}
	forged := request(http.MethodPost, "/api/release-versions", "bob", fmt.Sprintf(`{"id":%d,"version":"new","title":"新草稿","owner":"alice","manual_change_ids":[]}`, v.ID))
	if forged.Code != 200 {
		t.Fatal(forged.Body.String())
	}
	if st.GetReleaseVersion(v.ID).Title != "保留原范围" || len(st.ReleaseVersions("", 100)) != 2 {
		t.Fatal("POST forged id changed existing version")
	}
	for _, created := range st.ReleaseVersions("", 100) {
		if created.ID != v.ID && created.Owner != "bob" {
			t.Fatal("client owner accepted")
		}
	}
	denied := request(http.MethodPut, fmt.Sprintf("/api/release-versions/%d", v.ID), "bob", `{"version":"original","title":"盗改","manual_change_ids":[]}`)
	if denied.Code != 403 {
		t.Fatalf("ownership bypass: %d", denied.Code)
	}
	invalid := request(http.MethodPut, fmt.Sprintf("/api/release-versions/%d", v.ID), "alice", `{"version":"original","title":"错误范围","manual_change_ids":[99999]}`)
	if invalid.Code != 409 || st.GetReleaseVersion(v.ID).Title != "保留原范围" {
		t.Fatal("invalid scope partially updated metadata")
	}
}

func TestReleaseFreezeRequiresRefreshingNewAssociations(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	w := &WorkItem{Title: "需求"}
	if err = st.CreateWorkItem(w); err != nil {
		t.Fatal(err)
	}
	first := &Change{Kind: "manual", Title: "首个变更"}
	if err = st.CreateChange(first); err != nil {
		t.Fatal(err)
	}
	if err = st.SetChangeWorkItems(first.ID, []int64{w.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = st.SetChangeReady(first.ID, true); err != nil {
		t.Fatal(err)
	}
	v := &ReleaseVersion{Version: "v1", Title: "范围"}
	scope := VersionScope{WorkItemIDs: []int64{w.ID}}
	if err = st.SaveReleaseDraft(v, scope); err != nil {
		t.Fatal(err)
	}
	extra := &Change{Kind: "manual", Title: "后来新增变更"}
	if err = st.CreateChange(extra); err != nil {
		t.Fatal(err)
	}
	if err = st.SetChangeWorkItems(extra.ID, []int64{w.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = st.SetChangeReady(extra.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err = st.SetReleaseVersionReady(v.ID); err == nil {
		t.Fatal("new association silently excluded at freeze")
	}
	if err = st.SaveReleaseDraft(v, scope); err != nil {
		t.Fatal(err)
	}
	if _, err = st.SetReleaseVersionReady(v.ID); err != nil {
		t.Fatal(err)
	}
}

func TestReleaseDraftRequiresImplementationForEveryDemand(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	w := &WorkItem{Title: "尚无变更的需求"}
	if err = st.CreateWorkItem(w); err != nil {
		t.Fatal(err)
	}
	c := &Change{Kind: "manual", Title: "独立变更"}
	if err = st.CreateChange(c); err != nil {
		t.Fatal(err)
	}
	if _, err = st.SetChangeReady(c.ID, true); err != nil {
		t.Fatal(err)
	}
	v := &ReleaseVersion{Version: "v1", Title: "覆盖检查"}
	if err = st.SaveReleaseDraft(v, VersionScope{WorkItemIDs: []int64{w.ID}, ManualChangeIDs: []int64{c.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err = st.SetReleaseVersionReady(v.ID); err == nil {
		t.Fatal("demand without implementation frozen")
	}
	if err = st.SaveReleaseDraft(v, VersionScope{ManualChangeIDs: []int64{c.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err = st.SetReleaseVersionReady(v.ID); err != nil {
		t.Fatal(err)
	}
}
