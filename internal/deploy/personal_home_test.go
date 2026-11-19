package deploy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
)

// Exact task navigation still derives visibility from the actor's current
// task/owned projection. A link never supplies approval authority or a digest.
func TestPersonalHomeTaskLinkRevalidatesVisibility(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	v := collaborationRun(t, s, "owner", "pending_approval", false)
	mux := http.NewServeMux()
	registerResearchRoutes(mux, s, nil, nil)
	for _, tc := range []struct {
		path, actor string
		count       int
	}{
		{"/api/research/tasks?view=todo", "qa", 1},
		{"/api/research/tasks?view=todo&id=not-present", "qa", 0},
		{"/api/research/deliveries?id=" + url.QueryEscape(fmt.Sprintf("delivery:%d", v.ID)), "owner", 1},
		{"/api/research/deliveries?id=" + url.QueryEscape(fmt.Sprintf("delivery:%d", v.ID)), "stranger", 0},
	} {
		w := collaborationRequest(mux, "GET", tc.path, tc.actor, "")
		var result struct {
			Items []ResearchTask
			Total int
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Items) != tc.count || result.Total != tc.count {
			t.Fatalf("%s %s: %d %s", tc.actor, tc.path, w.Code, w.Body.String())
		}
		if tc.count > 0 {
			id := result.Items[0].ID
			exact := collaborationRequest(mux, "GET", tc.path+"&id="+url.QueryEscape(id), tc.actor, "")
			if exact.Code != 200 {
				t.Fatal(exact.Body.String())
			}
		}
	}
	if s.DeliveryRun(v.ID).Status != "pending_approval" {
		t.Fatal("homepage read advanced delivery")
	}
}
