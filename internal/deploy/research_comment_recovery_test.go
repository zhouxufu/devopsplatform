package deploy

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestResearchCommentedHistoryRecoversOriginalOperationsWithoutWrites(t *testing.T) {
	s, w, writes, mu := researchFixture(t)
	actor := "platform-alice"
	ids := []string{strings.Repeat("a", 32), strings.Repeat("b", 32)}
	remote := &ResearchRemote{Bug: map[string]any{"status": "closed", "title": w.Title, "assignedTo": "closed", "product": "1"}, Actions: json.RawMessage(`{}`)}
	histories := map[string]any{}
	for _, id := range ids {
		op := ResearchOperation{ID: id, State: "uncertain", RemoteAccount: "alice", Request: ResearchAction{Action: "comment", Comment: "已经发出的原评论"}}
		raw, _ := json.Marshal(op)
		if _, err := s.db.Exec(`INSERT INTO research_operations(operation_id,work_item_id,actor,request_digest,state,result,created_at) VALUES(?,?,?,'frozen','uncertain',?,1)`, id, w.ID, actor, string(raw)); err != nil {
			t.Fatal(err)
		}
		histories[id] = map[string]any{"actor": "alice", "action": "commented", "comment": "<p>原评论<br/>[delivery-operation:" + id + "]</p>"}
	}
	remote.Actions, _ = json.Marshal(histories)
	before, err := s.researchExceptionTasks(actor)
	if err != nil || len(before) != 2 {
		t.Fatal(before, err)
	}
	for _, id := range ids {
		op, err := s.ResearchOperation(id, actor)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.completeResearchOperation(w.ID, actor, op, remote); err != nil {
			t.Fatal(err)
		}
	}
	after, err := s.researchExceptionTasks(actor)
	if err != nil || len(after) != 0 {
		t.Fatal(after, err)
	}
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM research_activity WHERE work_item_id=? AND kind='comment'`, w.ID).Scan(&count)
	if count != 2 {
		t.Fatal("receipt count", count)
	}
	mux := http.NewServeMux()
	registerResearchRoutes(mux, s, nil, nil)
	for _, id := range ids {
		if r := collaborationRequest(mux, "POST", "/api/research/operations/"+id+"/reconcile", actor, `{}`); r.Code != 200 {
			t.Fatal(r.Code, r.Body.String())
		}
	}
	s.db.QueryRow(`SELECT COUNT(*) FROM research_activity WHERE work_item_id=? AND kind='comment'`, w.ID).Scan(&count)
	if count != 2 {
		t.Fatal("duplicate receipt", count)
	}
	mu.Lock()
	defer mu.Unlock()
	if *writes != 0 {
		t.Fatal("source write replayed")
	}
}
func TestResearchCommentMarkerAliasesRemainBoundToActorIDAndAction(t *testing.T) {
	for _, kind := range []string{"edited", "commented", "COMMENTED"} {
		raw, _ := json.Marshal([]any{map[string]any{"actor": "alice", "action": kind, "comment": "[delivery-operation:abc]"}})
		r := &ResearchRemote{Actions: raw}
		if !researchMarkerFound(r, "abc", "alice", "comment") {
			t.Fatal(kind)
		}
		if researchMarkerFound(r, "abc", "bob", "comment") || researchMarkerFound(r, "abcd", "alice", "comment") || researchMarkerFound(r, "abc", "alice", "resolve") {
			t.Fatal("foreign evidence accepted", kind)
		}
		if kind != "edited" && researchMarkerFound(r, "abc", "alice", "edit") {
			t.Fatal("comment accepted as edit")
		}
	}
}
