package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeliveryMCPAdvanceExactRevisionAndHumanEvidence(t *testing.T) {
	st, m, req, _, _ := standardManualFixture(t)
	run, err := st.CreateDeliveryRun(standardSign(t, st, m, req), "alice", m)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	registerDeliveryMCPRoutes(mux, st, m, nil)
	send := func(body map[string]any) int {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", fmt.Sprintf("/api/delivery/mcp/runs/%d/advance", run.ID), bytes.NewReader(b))
		r.Header.Set("X-User", "alice")
		r.Header.Set("X-Role", "member")
		r.Header.Set("X-Reviewer-Directory", `["alice"]`)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w.Code
	}
	body := map[string]any{"revision": run.Revision, "digest": run.Digest, "node_id": run.Steps[0].NodeID, "service": run.Steps[0].Service, "manual": true, "comment": ""}
	if code := send(body); code != 400 {
		t.Fatal("empty receipt", code)
	}
	body["comment"] = "人工已核对，凭据 fixture-1"
	body["revision"] = run.Revision + 1
	if code := send(body); code != 409 {
		t.Fatal("stale allowed", code)
	}
	body["revision"] = run.Revision
	if code := send(body); code != 200 {
		t.Fatal("valid receipt", code)
	}
	if code := send(body); code != 409 {
		t.Fatal("replayed node", code)
	}
	after := st.DeliveryRun(run.ID)
	if len(after.Steps[0].Reviews) != 1 || after.Steps[0].Reviews[0].Comment != "人工已核对，凭据 fixture-1" {
		t.Fatal(after.Steps)
	}
}
