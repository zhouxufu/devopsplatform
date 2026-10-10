package deploy

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeliveryRecordPaginationAndMetadata(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for i := 0; i < 605; i++ {
		c := Change{Kind: "manual", Title: fmt.Sprintf("Change %d", i), Service: "app", Owner: "alice", Summary: "BODY NOT IN LIST", Reference: "REF NOT IN LIST"}
		if err = st.CreateChange(&c); err != nil {
			t.Fatal(err)
		}
		v := ReleaseVersion{Version: fmt.Sprintf("v%d", i), Title: fmt.Sprintf("Version %d", i), Owner: "alice", Description: "BODY NOT IN LIST"}
		if err = st.CreateReleaseVersion(&v); err != nil {
			t.Fatal(err)
		}
	}
	for _, kind := range []string{"changes", "versions"} {
		got, e := st.DeliveryRecordsPage(DeliveryRecordFilter{Type: kind, Page: 31, PageSize: 20})
		if e != nil || got.Total != 605 || len(got.Items) != 5 || got.Items[4].ID != 1 {
			t.Fatalf("%s page: %+v %v", kind, got, e)
		}
		raw, _ := json.Marshal(got)
		if strings.Contains(string(raw), "BODY NOT IN LIST") || strings.Contains(string(raw), "REF NOT IN LIST") {
			t.Fatal("list returned detail bodies")
		}
		got, e = st.DeliveryRecordsPage(DeliveryRecordFilter{Type: kind, Owner: "' OR 1=1 --"})
		if e != nil || got.Total != 0 {
			t.Fatal("owner not parameterized", got, e)
		}
		got, e = st.DeliveryRecordsPage(DeliveryRecordFilter{Type: kind, Page: 999})
		if e != nil || got.Page != 31 {
			t.Fatal("page not clamped", got, e)
		}
	}
	w := WorkItem{Title: "100%_literal", Kind: "bug", Description: "SECRET DESCRIPTION"}
	if err = st.CreateWorkItem(&w); err != nil {
		t.Fatal(err)
	}
	c := Change{Kind: "manual", Title: "100%_literal", Owner: "bob", Service: "other", Summary: "PRIVATE"}
	if err = st.CreateChange(&c); err != nil {
		t.Fatal(err)
	}
	if err = st.SetChangeWorkItems(c.ID, []int64{w.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = st.SetChangeReady(c.ID, true); err != nil {
		t.Fatal(err)
	}
	got, err := st.DeliveryRecordsPage(DeliveryRecordFilter{Type: "changes", Query: "%_", Owner: "bob", Service: "other", Status: "ready", Kind: "manual", Association: "linked"})
	if err != nil || got.Total != 1 || len(got.Items[0].WorkItems) != 1 || got.Items[0].WorkItems[0].ID != w.ID {
		t.Fatal("combined filters", got, err)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "SECRET DESCRIPTION") || strings.Contains(string(raw), "PRIVATE") {
		t.Fatal("linked work body leaked")
	}
	got, err = st.DeliveryRecordsPage(DeliveryRecordFilter{Type: "changes", Association: "independent"})
	if err != nil || got.Total != 605 {
		t.Fatal("independent filter", got, err)
	}
	v := ReleaseVersion{Version: "summary", Title: "Summary", Owner: "bob"}
	if err = st.SaveReleaseDraft(&v, VersionScope{WorkItemIDs: []int64{w.ID}, ManualChangeIDs: []int64{c.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err = st.SetReleaseVersionReady(v.ID); err != nil {
		t.Fatal(err)
	}
	got, err = st.DeliveryRecordsPage(DeliveryRecordFilter{Type: "versions", Service: "other", Status: "ready", Owner: "bob"})
	if err != nil || got.Total != 1 || got.Items[0].ApplicationCount != 1 || got.Items[0].ChangeCount != 1 || got.Items[0].WorkItemCount != 1 {
		t.Fatal("version counts", got, err)
	}
	got, err = st.DeliveryRecordsPage(DeliveryRecordFilter{Type: "changes", Owner: "bob"})
	if err != nil || !got.Items[0].Frozen {
		t.Fatal("frozen metadata", got, err)
	}
	if _, err = st.db.Exec(`UPDATE change_payloads SET document=? WHERE change_id=?`, `{"sql":{"content":"SECRET SQL","data_sync":{"task_id":1}}}`, c.ID); err != nil {
		t.Fatal(err)
	}
	got, err = st.DeliveryRecordsPage(DeliveryRecordFilter{Type: "changes", Owner: "bob"})
	if err != nil || !got.Items[0].DataSync {
		t.Fatal("data sync metadata", got, err)
	}
	raw, _ = json.Marshal(got)
	if strings.Contains(string(raw), "SECRET SQL") {
		t.Fatal("SQL in list")
	}
	for _, f := range []DeliveryRecordFilter{{Type: "oops"}, {Type: "changes", Page: -1}, {Type: "changes", PageSize: 500}, {Type: "changes", Status: "completed"}, {Type: "versions", Status: "running"}, {Type: "changes", Kind: "oops"}, {Type: "changes", Association: "oops"}} {
		if _, err = st.DeliveryRecordsPage(f); err == nil {
			t.Fatal("invalid filter accepted", f)
		}
	}
}

func TestDeliveryRecordListRoutes(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	c := Change{Kind: "manual", Title: "Detail", Owner: "alice", Summary: "Detail body"}
	if err = st.CreateChange(&c); err != nil {
		t.Fatal(err)
	}
	v := ReleaseVersion{Version: "v1", Title: "Version", Owner: "bob", Description: "Version body"}
	if err = st.CreateReleaseVersion(&v); err != nil {
		t.Fatal(err)
	}
	h := Routes(st, NewBuilder(st, dir, NewLogHub()), NewLogHub(), "", NewStepLib(dir+"/steps", dir+"/custom"))
	for _, endpoint := range []string{"changes", "release-versions"} {
		for _, q := range []string{"?page=0", "?page=abc", "?page=-1", "?page_size=500", "?status=invalid", "?kind=invalid", "?association=invalid"} {
			out := httptest.NewRecorder()
			h.ServeHTTP(out, httptest.NewRequest("GET", "/api/"+endpoint+"/page"+q, nil))
			if out.Code != 400 {
				t.Fatal(endpoint, q, out.Code, out.Body.String())
			}
		}
	}
	for path, body := range map[string]string{"/api/changes/page": `"total":1`, "/api/changes/owners": `"alice"`, fmt.Sprint("/api/changes/", c.ID): "Detail body", "/api/release-versions/page": `"total":1`, "/api/release-versions/owners": `"bob"`, fmt.Sprint("/api/release-versions/", v.ID): "Version body"} {
		out := httptest.NewRecorder()
		h.ServeHTTP(out, httptest.NewRequest("GET", path, nil))
		if out.Code != 200 || !strings.Contains(out.Body.String(), body) {
			t.Fatal(path, out.Code, out.Body.String())
		}
	}
	for _, endpoint := range []string{"changes", "release-versions"} {
		for id, code := range map[string]int{"0": 400, "bad": 400, "99999": 404} {
			out := httptest.NewRecorder()
			h.ServeHTTP(out, httptest.NewRequest("GET", "/api/"+endpoint+"/"+id, nil))
			if out.Code != code {
				t.Fatal(endpoint, id, out.Code)
			}
		}
	}
}
