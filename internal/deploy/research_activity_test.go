package deploy

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResearchActivitiesLinkVerifiedOperationsAndConservativeLegacy(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		count    int
		explicit bool
		state    string
		linked   bool
	}{
		{"legacy unique", 1, false, "success", true},
		{"legacy repeated text remains ambiguous", 2, false, "success", false},
		{"explicit repeated comments keep distinct IDs", 2, true, "success", true},
		{"unknown write is not merged", 1, false, "unknown", false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			s, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			item := &WorkItem{Source: "manual", Kind: "bug", Title: "fixture", Owner: "platform-user"}
			if err = s.CreateWorkItem(item); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < scenario.count; i++ {
				r, err := s.db.Exec(`INSERT INTO research_activity(work_item_id,actor,kind,content,created_at) VALUES(?,'platform-user','comment','same comment',501)`, item.ID)
				if err != nil {
					t.Fatal(err)
				}
				id, _ := r.LastInsertId()
				opID := strings.Repeat(string(rune('a'+i)), 32)
				op := ResearchOperation{ID: opID, State: scenario.state, RemoteAccount: "source-user", Request: ResearchAction{OperationID: opID, Action: "comment", Comment: "same comment"}}
				if scenario.explicit {
					op.ActivityID = id
				}
				raw, _ := json.Marshal(op)
				if _, err = s.db.Exec(`INSERT INTO research_operations(operation_id,work_item_id,actor,request_digest,state,result,created_at) VALUES(?,?,'platform-user','fixture',?,?,500)`, opID, item.ID, scenario.state, string(raw)); err != nil {
					t.Fatal(err)
				}
			}
			rows, err := s.ResearchActivities(item.ID)
			if err != nil || len(rows) != scenario.count+1 {
				t.Fatal(rows, err)
			}
			for _, row := range rows {
				if row.Kind == "created" {
					continue
				}
				if (row.OperationID != "") != scenario.linked {
					t.Fatal(row)
				}
				if scenario.linked && row.SourceActor != "source-user" {
					t.Fatal(row)
				}
			}
			var count int
			s.db.QueryRow(`SELECT count(*) FROM research_activity WHERE work_item_id=?`, item.ID).Scan(&count)
			if count != scenario.count+1 {
				t.Fatal("audit rows changed", count)
			}
		})
	}
}
