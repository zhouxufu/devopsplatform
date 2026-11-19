package deploy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCodePreparationGeneratedIDsRemainAddressable(t *testing.T) {
	st, _, _ := deliveryFixture(t)
	mux := http.NewServeMux()
	registerCodePreparationRoutes(mux, st, nil)
	for _, prefix := range "0123456789abcdef" {
		id := string(prefix) + strings.Repeat("a", 31)
		p := &CodePreparation{ID: id, Owner: "local", State: "preview", Protected: true}
		if err := st.saveCodePreparation(p); err != nil {
			t.Fatal(err)
		}
		if got := st.CodePreparation(id); got == nil || got.ID != id || got.State != "preview" {
			t.Fatalf("persisted generated ID cannot be loaded: %s", id)
		}
		for _, call := range []struct {
			method, suffix, body string
			status               int
		}{
			{"GET", "", "", 200},
			{"POST", "/start", `{"confirm":true}`, 400}, // Still requires production scope confirmation.
			{"POST", "/cancel", `{"confirm":true}`, 200},
		} {
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest(call.method, "/api/delivery/code-preparations/"+id+call.suffix, strings.NewReader(call.body)))
			if w.Code != call.status {
				t.Fatalf("%s %s: %d %s", call.method, id+call.suffix, w.Code, w.Body.String())
			}
		}
		if got := st.CodePreparation(id); got == nil || got.State != "canceled" {
			t.Fatal("cancel did not persist")
		}
	}
	for _, id := range []string{"", "../x", " x", "x ", strings.Repeat("a", 33), "a/b", "0?x=1"} {
		if st.CodePreparation(id) != nil || codePreparationIDRE.MatchString(id) {
			t.Fatalf("invalid ID accepted: %q", id)
		}
	}
	if validResourceName("0" + strings.Repeat("a", 31)) {
		t.Fatal("environment/resource rules were broadened")
	}
}

func TestDraftCodePreparationExplainsRepairWithoutChangingReadiness(t *testing.T) {
	st, _, req := deliveryFixture(t)
	w := &WorkItem{Title: "Draft fix", Kind: "bug", Owner: "alice"}
	if err := st.CreateWorkItem(w); err != nil {
		t.Fatal(err)
	}
	c := &Change{Kind: "code", Title: "Draft code", Service: "orders", Reference: "feature/draft", Owner: "alice"}
	if err := st.CreateChange(c); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChangeWorkItems(c.ID, []int64{w.ID}); err != nil {
		t.Fatal(err)
	}
	tpl := &DeliveryTemplate{Name: "Dynamic", Mode: "version", Owner: "alice", Nodes: []DeliveryNode{{ID: "deploy", Kind: "deploy", Name: "Deploy"}}}
	if err := st.SaveDeliveryTemplate(tpl); err != nil {
		t.Fatal(err)
	}
	req.TemplateID, req.SourceType, req.WorkItemIDs = tpl.ID, "work_items", []int64{w.ID}
	req.ArtifactSources = map[string]string{"orders": "build"}
	_, err := st.previewCodePreparation(context.Background(), CodePreparationRequest{Delivery: req}, "alice")
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("代码变更 #%d 仍为草稿", c.ID)) || !strings.Contains(err.Error(), "标记就绪") {
		t.Fatalf("unactionable readiness error: %v", err)
	}
	if st.GetChange(c.ID).Status != ChangeDraft {
		t.Fatal("preview changed source status")
	}
	var count int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM deploy_meta WHERE key LIKE 'code_preparation:%'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("draft created an executable preparation", count, err)
	}
}
