package deploy

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestResearchExceptionIgnorePreservesFailureAndNewEvents(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	v := collaborationRun(t, s, "owner", "failed", false)
	items, err := s.unresolvedResearchExceptions("owner")
	if err != nil || len(items) != 1 {
		t.Fatal(err, items)
	}
	item := items[0]
	mux := http.NewServeMux()
	registerResearchCollaboration(mux, s)
	request := func(actor, action, comment, digest string, want int) {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"id": item.ID, "digest": digest, "comment": comment, "action": action})
		w := collaborationRequest(mux, "POST", "/api/research/exceptions/resolve", actor, string(body))
		if w.Code != want {
			t.Fatalf("status=%d want=%d %s", w.Code, want, w.Body.String())
		}
	}
	request("owner", "ignore", "1111", item.ResolutionDigest, 400)
	request("owner", "success", "故障已由其他发布处理", item.ResolutionDigest, 400)
	request("other", "ignore", "故障已由其他发布处理", item.ResolutionDigest, 409)
	request("owner", "ignore", "故障已由其他发布处理", "stale", 409)
	request("owner", "ignore", "故障已由其他发布处理", item.ResolutionDigest, 200)
	request("owner", "ignore", "故障已由其他发布处理", item.ResolutionDigest, 409)
	if s.DeliveryRun(v.ID).Status != "failed" {
		t.Fatal("ignore changed execution state")
	}
	items, err = s.unresolvedResearchExceptions("owner")
	if err != nil || len(items) != 0 {
		t.Fatal("ignore did not close todo", err)
	}
	history, err := s.researchExceptionHistory("owner")
	if err != nil || len(history) != 1 || history[0].ResolutionAction != "ignore" || !strings.Contains(history[0].Stage, "忽略") || !strings.Contains(history[0].NextAction, "故障已由其他发布处理") {
		t.Fatal("missing ignore history", err, history)
	}
	var audits int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='research-exception-ignored'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatal("missing audit", err, audits)
	}
	v.Revision = s.DeliveryRun(v.ID).Revision
	v.Steps[0].Detail = "another failure"
	if err = s.saveDeliveryRun(v); err != nil {
		t.Fatal(err)
	}
	items, err = s.unresolvedResearchExceptions("owner")
	if err != nil || len(items) != 1 || items[0].ResolutionDigest == item.ResolutionDigest {
		t.Fatal("old ignore hid new event", err)
	}
}

func TestResearchIgnoreFailedReleaseDoesNotRewriteExecution(t *testing.T) {
	s, _, _ := deliveryFixture(t)
	o := &ReleaseOrder{Service: "orders", Environment: "test", Status: "failed", Actor: "owner", Hosts: []string{"test-1"}, Version: "frozen", StartedAt: 100, Target: *s.GetServiceEnvironment("orders", "test")}
	if err := s.CreateReleaseOrder(o); err != nil {
		t.Fatal(err)
	}
	before := digestJSON(s.GetReleaseOrder(o.ID))
	items, err := s.unresolvedResearchExceptions("owner")
	if err != nil || len(items) != 1 || items[0].SourceKind != "release" {
		t.Fatal(err, items)
	}
	mux := http.NewServeMux()
	registerResearchCollaboration(mux, s)
	body, _ := json.Marshal(map[string]string{"id": items[0].ID, "digest": items[0].ResolutionDigest, "comment": "已由新发布处理此故障", "action": "ignore"})
	if w := collaborationRequest(mux, "POST", "/api/research/exceptions/resolve", "owner", string(body)); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if digestJSON(s.GetReleaseOrder(o.ID)) != before || len(s.Deploys("orders", 10)) != 0 {
		t.Fatal("ignore modified or restarted original release")
	}
}
