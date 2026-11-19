package deploy

import (
	"reflect"
	"testing"
	"time"
)

func TestResearchPeopleCacheIsActorEndpointScopedAndCopied(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := ResourceConnection{Name: "source", Kind: "zentao", Endpoint: "http://example.test/zentao"}
	if err := s.UpsertConnection(c); err != nil {
		t.Fatal(err)
	}
	people := map[string]string{"alice": "张三", "closed": "已关闭"}
	s.rememberResearchPeople("admin", &c, people)
	people["alice"] = "changed"
	labels := s.cachedResearchPeople("admin")
	if labels["zentao:source"]["alice"] != "张三" || labels["zentao:source"]["closed"] != "" || len(s.cachedResearchPeople("other")) != 0 {
		t.Fatalf("unsafe cache: %+v", labels)
	}
	labels["zentao:source"]["alice"] = "changed"
	if s.cachedResearchPeople("admin")["zentao:source"]["alice"] != "张三" {
		t.Fatal("cache returned mutable map")
	}
	key := zentaoSessionKey{"admin", "source"}
	snapshot := s.researchDisplayPeople[key]
	snapshot.expires = time.Now().Add(-time.Second)
	s.researchDisplayPeople[key] = snapshot
	if len(s.cachedResearchPeople("admin")) != 0 {
		t.Fatal("expired labels exposed")
	}
	s.rememberResearchPeople("admin", &c, people)
	c.Endpoint = "http://other.test/zentao"
	if err := s.UpsertConnection(c); err != nil {
		t.Fatal(err)
	}
	if len(s.cachedResearchPeople("admin")) != 0 {
		t.Fatal("labels crossed endpoint boundary")
	}
}

func TestWorkPersonnelIdentityIsSourceScoped(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, v := range []WorkItem{{Source: "manual", Title: "本人自定义", Owner: "admin"}, {Source: "zentao:a", ExternalID: "1", Kind: "bug", Title: "来源本人"}, {Source: "zentao:b", ExternalID: "1", Kind: "bug", Title: "另一来源同名账号"}} {
		w := v
		if err := s.CreateWorkItem(&w); err != nil {
			t.Fatal(err)
		}
		if w.Source != "manual" {
			if _, err := s.db.Exec(`UPDATE work_items SET assigned_to='alice' WHERE id=?`, w.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	f := WorkItemFilter{PersonRole: "related", PersonAccounts: map[string]string{"manual": "admin", "zentao:a": "alice"}}
	p, err := s.WorkItemsPage(f)
	if err != nil || p.Total != 2 {
		t.Fatalf("mine leaked or lost source identity: %+v %v", p, err)
	}
	c, err := s.WorkCandidates(WorkCandidateFilter{WorkItemFilter: f})
	if err != nil || c.Total != 2 {
		t.Fatalf("candidate differs from list: %+v %v", c, err)
	}
}

func TestWorkCandidateDisplaySnapshotsAndSourceDateValidation(t *testing.T) {
	labels := issuePersonLabels(map[string]string{"alice": "张三", "closed": "已关闭", "secret": "不相关用户"}, "alice", "closed")
	if !reflect.DeepEqual(labels, map[string]string{"alice": "张三"}) {
		t.Fatalf("unsafe labels: %+v", labels)
	}
	p, err := livePredicates(WorkItemFilter{SourceStart: "2026-10-01", SourceEnd: "2026-10-15"})
	if err != nil || len(p) != 2 || p[0].Field != "lastEditedDate" || p[0].Operator != ">=" || p[1].Value != "2026-10-15 23:59:59" {
		t.Fatalf("date predicates %+v %v", p, err)
	}
	for _, f := range []WorkItemFilter{{SourceStart: "2026-13-01"}, {SourceStart: "2026-10-16", SourceEnd: "2026-10-15"}, {SourceStart: "2026-10-01' OR 1=1"}} {
		if _, err := livePredicates(f); err == nil {
			t.Fatal("invalid date accepted", f)
		}
	}
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w := WorkItem{Source: "manual", Title: "display snapshot", Owner: "alice"}
	if err := s.CreateWorkItem(&w); err != nil {
		t.Fatal(err)
	}
	tx, _ := s.db.Begin()
	if err := saveIssueFields(tx, w.ID, ResearchIssueFields{PersonLabels: labels}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	c, err := s.WorkCandidates(WorkCandidateFilter{})
	if err != nil || len(c.Items) != 1 || c.Items[0].PersonLabels["alice"] != "张三" {
		t.Fatalf("candidate lost labels %+v %v", c, err)
	}
}
