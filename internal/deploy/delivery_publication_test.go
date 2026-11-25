package deploy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPublicationUsesOneFrozenRunAndStopsAtManual(t *testing.T) {
	s, m, req, change, _ := standardManualFixture(t)
	req.SourceType, req.WorkItemIDs, req.SourceChangeIDs = "changes", nil, []int64{change.ID}
	req = standardSign(t, s, m, req)
	mux := http.NewServeMux()
	registerPublicationRoutes(mux, s, m)
	call := func(path, key string, body any) *httptest.ResponseRecorder {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
		r.Header.Set("X-User", "alice")
		r.Header.Set("X-Work-Loop-Authority", "mcp")
		r.Header.Set("X-Work-Loop-Key-ID", key)
		r.Header.Set("X-Reviewer-Directory", `["alice"]`)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	const base = "/api/delivery/publications"
	post := call(base, "original", req)
	if post.Code != 200 {
		t.Fatal(post.Code, post.Body.String())
	}
	var run DeliveryRun
	if err := json.Unmarshal(post.Body.Bytes(), &run); err != nil || run.ID <= 0 {
		t.Fatal(err)
	}
	var grant string
	if err := s.db.QueryRow(`SELECT id FROM delivery_publications WHERE run_id=?`, run.ID).Scan(&grant); err != nil {
		t.Fatal(err)
	}
	path := base + "/" + grant + "/advance"
	next := map[string]any{"revision": run.Revision}
	if w := call(path, "another", next); w.Code != 403 {
		t.Fatal("accepted other key", w.Code)
	}
	if w := call(path, "original", next); w.Code != 200 || s.DeliveryControl(run.ID).State != "paused" {
		t.Fatal("manual gate", w.Code, w.Body.String())
	}
	if w := call(base, "original", req); w.Code != 200 || s.DeliveryControl(run.ID).State != "paused" {
		t.Fatal("replayed paused run", w.Code)
	}
	if s.DeliveryRun(run.ID).Steps[0].StartedAt != 0 {
		t.Fatal("advanced manual step")
	}
	req = standardSign(t, s, m, req)
	ordinary, err := s.CreateDeliveryRun(req, "alice", m)
	if err != nil {
		t.Fatal(err)
	}
	if w := call(base, "original", req); w.Code != 409 || s.DeliveryControl(ordinary.ID).State != "idle" {
		t.Fatal("converted prior create-only submission", w.Code)
	}
}
