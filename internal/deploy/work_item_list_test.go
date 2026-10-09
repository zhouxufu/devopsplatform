package deploy

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkItemsServerPaginationAndPeople(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for i := 0; i < 605; i++ {
		w := WorkItem{Source: "zentao:qa", ExternalID: fmt.Sprint(i), Kind: "bug", Title: fmt.Sprintf("Bug %d", i), Owner: "sync-admin"}
		if err := st.CreateWorkItem(&w); err != nil {
			t.Fatal(err)
		}
		if _, err := st.db.Exec(`UPDATE work_items SET assigned_to=?,opened_by=?,resolved_by=?,closed_by=?,external_status=?,status=? WHERE id=?`, "alice", "bob", "charlie", "dave", "closed", "closed", w.ID); err != nil {
			t.Fatal(err)
		}
	}
	manual := WorkItem{Title: "100%_literal", Owner: "eve"}
	if err := st.CreateWorkItem(&manual); err != nil {
		t.Fatal(err)
	}
	got, err := st.WorkItemsPage(WorkItemFilter{Page: 31, PageSize: 20})
	if err != nil || got.Total != 606 || len(got.Items) != 6 || got.Items[5].ExternalID != "0" {
		t.Fatalf("full pagination %+v: %v", got, err)
	}
	for role, person := range map[string]string{"assigned_to": "alice", "opened_by": "bob", "resolved_by": "charlie", "closed_by": "dave", "related": "charlie"} {
		got, err = st.WorkItemsPage(WorkItemFilter{PersonRole: role, Person: person, Kind: "bug", Status: "closed"})
		if err != nil || got.Total != 605 || len(got.Items) != 20 {
			t.Fatalf("%s filter %+v: %v", role, got, err)
		}
	}
	got, err = st.WorkItemsPage(WorkItemFilter{Person: "sync-admin"})
	if err != nil || got.Total != 0 {
		t.Fatal("sync actor incorrectly treated as bug owner", got, err)
	}
	got, err = st.WorkItemsPage(WorkItemFilter{Person: "eve"})
	if err != nil || got.Total != 1 {
		t.Fatal("manual owner filter", got, err)
	}
	got, err = st.WorkItemsPage(WorkItemFilter{Query: "%_"})
	if err != nil || got.Total != 1 {
		t.Fatal("literal keyword", got, err)
	}
	got, err = st.WorkItemsPage(WorkItemFilter{Page: 999})
	if err != nil || got.Page != 31 {
		t.Fatal("out of range page", got, err)
	}
	got, err = st.WorkItemsPage(WorkItemFilter{Person: "' OR 1=1 --"})
	if err != nil || got.Total != 0 {
		t.Fatal("parameterized person", got, err)
	}
	for _, f := range []WorkItemFilter{{Page: -1}, {PageSize: 500}, {Status: "invalid"}, {Kind: "invalid"}, {PersonRole: "owner OR 1=1"}} {
		if _, err = st.WorkItemsPage(f); err == nil {
			t.Fatal("accepted invalid filter", f)
		}
	}
	people, err := st.WorkItemPeople()
	if err != nil || fmt.Sprint(people) != "[alice bob charlie dave eve]" {
		t.Fatal("all persisted people", people, err)
	}
}

func TestWorkItemLegacyDatabaseMigration(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "deploy.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE work_items(id INTEGER PRIMARY KEY AUTOINCREMENT,source TEXT,external_id TEXT,kind TEXT,title TEXT,url TEXT,external_status TEXT,description TEXT,status TEXT,owner TEXT,created_at INTEGER,updated_at INTEGER)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO work_items VALUES(7,'zentao:old','38','bug','Existing bug','','closed','<p>details</p>','closed','sync-admin',1,2)`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	for i := 0; i < 2; i++ {
		st, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		w := st.GetWorkItem(7)
		if w == nil || w.Owner != "sync-admin" || w.Title != "Existing bug" || w.Description != "<p>details</p>" || w.AssignedTo != "" {
			t.Fatal("legacy record changed", w)
		}
		st.Close()
	}
}

func TestWorkItemsPageAPI(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	w := WorkItem{Title: "Page API", Owner: "alice"}
	if err = st.CreateWorkItem(&w); err != nil {
		t.Fatal(err)
	}
	h := Routes(st, NewBuilder(st, dir, NewLogHub()), NewLogHub(), "", NewStepLib(dir+"/steps", dir+"/custom"))
	for _, path := range []string{"/api/work-items/page?page=-1", "/api/work-items/page?page=abc", "/api/work-items/page?page=0", "/api/work-items/page?page_size=500", "/api/work-items/page?person_role=invalid"} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest("GET", path, nil))
		if r.Code != 400 {
			t.Fatalf("%s: %d %s", path, r.Code, r.Body.String())
		}
	}
	for path, body := range map[string]string{"/api/work-items/page?person=alice": `"total":1`, "/api/work-items/people": `"alice"`, fmt.Sprint("/api/work-items/", w.ID): `"title":"Page API"`} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest("GET", path, nil))
		if r.Code != 200 || !strings.Contains(r.Body.String(), body) {
			t.Fatalf("%s: %d %s", path, r.Code, r.Body.String())
		}
	}
	missing := httptest.NewRecorder()
	h.ServeHTTP(missing, httptest.NewRequest("GET", "/api/work-items/99999", nil))
	if missing.Code != 404 {
		t.Fatal(missing.Code)
	}
}

func TestZentaoPersonnelResyncKeepsPlatformOwner(t *testing.T) {
	assigned := "alice"
	st, name := zentaoFixture(t, func(r *http.Request) (any, string) {
		return map[string]any{"bugs": []map[string]any{{"id": "7", "title": "Personal bug", "status": "closed", "assignedTo": assigned, "openedBy": "bob", "resolvedBy": "charlie", "closedBy": "dave"}}}, ""
	})
	if _, err := st.syncZentao(context.Background(), name, "", "sync-admin"); err != nil {
		t.Fatal(err)
	}
	w := st.WorkItems("", "", 100)[0]
	if w.Owner != "sync-admin" || w.AssignedTo != "alice" || w.OpenedBy != "bob" || w.ResolvedBy != "charlie" || w.ClosedBy != "dave" {
		t.Fatal(w)
	}
	change := Change{Kind: "manual", Title: "Fix", Owner: "sync-admin"}
	if err := st.CreateChange(&change); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChangeWorkItems(change.ID, []int64{w.ID}); err != nil {
		t.Fatal(err)
	}
	assigned = "new-assignee"
	if _, err := st.syncZentao(context.Background(), name, "", "scheduler"); err != nil {
		t.Fatal(err)
	}
	got := st.GetChange(change.ID).WorkItems[0]
	if got.ID != w.ID || got.Owner != w.Owner || got.AssignedTo != assigned {
		t.Fatal("identity/authorization lost", got)
	}
	// Manual edits from older clients cannot erase upstream personnel.
	got.AssignedTo = ""
	got.OpenedBy = ""
	got.Title = "Edited title"
	if err := st.UpdateWorkItem(&got); err != nil {
		t.Fatal(err)
	}
	if current := st.GetWorkItem(w.ID); current.AssignedTo != assigned || current.OpenedBy != "bob" {
		t.Fatal("metadata erased", current)
	}
}
