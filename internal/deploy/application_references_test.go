package deploy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestApplicationReferencesAreAuthoritativeAndReadOnly(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.UpsertService(Service{Name: "orders", GitURL: "secret-repository", ArtifactType: "jar"})
	var calls atomic.Int32
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.Error(w, "must not observe", 500) }))
	defer catalog.Close()
	hub := NewLogHub()
	h := Routes(st, NewBuilder(st, dir, hub), hub, catalog.URL, NewStepLib("../../steps"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/application-references", nil))
	if w.Code != 200 || calls.Load() != 0 || strings.Contains(w.Body.String(), "secret-repository") {
		t.Fatal(w.Code, w.Body.String(), calls.Load())
	}
	var data struct {
		Complete     bool
		Applications []struct {
			Name          string
			ApplicationID string `json:"application_id"`
		}
	}
	if json.Unmarshal(w.Body.Bytes(), &data) != nil || !data.Complete || len(data.Applications) != 1 || data.Applications[0].ApplicationID != st.GetService("orders").ApplicationID {
		t.Fatal(w.Body.String())
	}
	st.db.Exec(`DROP TABLE services`)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/application-references", nil))
	if w.Code != 503 {
		t.Fatal("database failure treated as empty", w.Code, w.Body.String())
	}
}

func TestDeleteApplicationForwardsSessionAndKeepsHistory(t *testing.T) {
	for _, offline := range []bool{false, true} {
		dir := t.TempDir()
		st, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		st.UpsertService(Service{Name: "orders", ArtifactType: "jar"})
		id := st.GetService("orders").ApplicationID
		st.db.Exec(`INSERT INTO builds(service,version,status,artifact) VALUES('orders','original','success','original.jar')`)
		var calls atomic.Int32
		catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			if r.Method != "DELETE" || r.URL.Path != "/api/registry/applications/source" || r.Header.Get("Cookie") != "lp_session=fixture" {
				t.Error("session or route lost", r.Method, r.URL.Path)
			}
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["application_id"] != id || body["name"] != "orders" || body["source"] != "deploy" {
				t.Error(body)
			}
			if offline {
				http.Error(w, "offline", 503)
				return
			}
			json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		}))
		hub := NewLogHub()
		h := Routes(st, NewBuilder(st, dir, hub), hub, catalog.URL, NewStepLib("../../steps"))
		req := httptest.NewRequest("DELETE", "/api/services/orders", nil)
		req.Header.Set("Cookie", "lp_session=fixture")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		var data map[string]bool
		json.Unmarshal(w.Body.Bytes(), &data)
		var count int
		st.db.QueryRow(`SELECT COUNT(*) FROM builds WHERE service='orders'`).Scan(&count)
		if w.Code != 200 || !data["ok"] || data["catalog_detached"] == offline || st.GetService("orders") != nil || count != 1 || calls.Load() != 1 {
			t.Fatal(w.Code, w.Body.String(), count, calls.Load())
		}
		catalog.Close()
		st.Close()
	}
}
