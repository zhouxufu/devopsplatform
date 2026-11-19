package splunkplugin

import (
	"context"
	"encoding/json"
	p "logplatform/pkg/extensionprotocol"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixture(t *testing.T) (*Adapter, *[]url.Values, *[]string) {
	t.Helper()
	var mu sync.Mutex
	forms := []url.Values{}
	deleted := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		r.ParseForm()
		if r.URL.Path == "/services/auth/login" {
			if r.Form.Get("username") != "reader" || r.Form.Get("password") != "secret" {
				t.Error("wrong credentials")
			}
			w.Header().Set("Content-Type", "application/xml")
			w.Write([]byte(`<response><sessionKey>old-session</sessionKey></response>`))
			return
		}
		if r.Header.Get("Authorization") != "Splunk old-session" {
			t.Error("wrong legacy session")
		}
		switch {
		case r.URL.Path == "/services/server/info":
			w.Write([]byte(`{"entry":[{"content":{"version":"6.6.3"}}]}`))
		case r.URL.Path == "/services/search/jobs":
			mu.Lock()
			forms = append(forms, r.Form)
			mu.Unlock()
			w.Write([]byte(`{"sid":"job-1"}`))
		case r.Method == "DELETE":
			mu.Lock()
			deleted = append(deleted, r.URL.Path)
			mu.Unlock()
			w.Write([]byte(`{}`))
		case strings.HasSuffix(r.URL.Path, "/results"):
			if r.URL.Query().Get("offset") == "0" {
				w.Write([]byte(`{"results":[{"__lp_ts":"1792464000000","_raw":"timeout one","host":"node1","severity":"ERROR","traceId":"trace-1"},{"__lp_ts":"1792463999000","_raw":"timeout two"}]}`))
			} else {
				w.Write([]byte(`{"results":[{"__lp_ts":"1792463999000","_raw":"timeout two"}]}`))
			}
		default:
			w.Write([]byte(`{"entry":[{"content":{"isDone":true,"isFinalized":false,"dispatchState":"DONE"}}]}`))
		}
	}))
	t.Cleanup(server.Close)
	a, err := New(Config{Endpoint: server.URL, Username: "reader", Password: "secret", Users: []string{"alice"}, Bindings: []Binding{{Service: "api", Environment: "prod", Index: "business"}}, Fields: map[string]string{"level": "severity", "trace_id": "traceId"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a, &forms, &deleted
}
func query() p.LogSearch {
	return p.LogSearch{Service: "api", Environment: "prod", Start: 1792463000000, End: 1792465000000, Text: "timeout", Limit: 1}
}
func TestLegacySessionQueryPaginationAndScope(t *testing.T) {
	a, forms, deleted := fixture(t)
	actor := p.Actor{User: "alice", Role: "member"}
	if err := a.Test(context.Background()); err != nil {
		t.Fatal(err)
	}
	q := query()
	first, err := a.Search(context.Background(), q, actor)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Hits) != 1 || !first.HasMore || first.Hits[0].Level != "ERROR" || first.Hits[0].TraceID != "trace-1" {
		t.Fatalf("mapping: %+v", first)
	}
	if len(*forms) != 1 || !strings.Contains((*forms)[0].Get("search"), `index="business"`) || (*forms)[0].Get("earliest_time") == "" {
		t.Fatal("missing scope/time")
	}
	if len(*deleted) != 0 {
		t.Fatal("active job prematurely deleted")
	}
	q.Cursor = first.NextCursor
	other := q
	other.Text = "other"
	if _, err = a.Search(context.Background(), other, actor); err == nil {
		t.Fatal("cursor reused for different query")
	}
	if _, err = a.Search(context.Background(), q, p.Actor{User: "mallory"}); err == nil {
		t.Fatal("unauthorized query")
	}
	last, err := a.Search(context.Background(), q, actor)
	if err != nil || last.HasMore || len(last.Hits) != 1 {
		t.Fatal(err)
	}
	if len(*deleted) != 1 {
		t.Fatal("completed job not cleaned")
	}
	if _, err = a.Search(context.Background(), q, actor); err == nil {
		t.Fatal("cursor replay allowed")
	}
}
func TestBoundedQueryAndSPLInjection(t *testing.T) {
	a, _, _ := fixture(t)
	actor := p.Actor{User: "alice"}
	q := query()
	q.Text = `x" | delete | search index=* \\ .*`
	q.Host = `n" OR index=*`
	s, err := a.build(q, actor)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, `index="business"`) || !strings.Contains(s, `\\| delete`) {
		t.Fatal("literal not escaped", s)
	}
	for _, change := range []func(*p.LogSearch){func(q *p.LogSearch) { q.Service = "other" }, func(q *p.LogSearch) { q.Environment = "test" }, func(q *p.LogSearch) { q.End = q.Start + 25*60*60*1000 }, func(q *p.LogSearch) { q.Limit = 1000 }, func(q *p.LogSearch) { q.Levels = []string{"ERROR | delete"} }} {
		bad := query()
		change(&bad)
		if _, err = a.build(bad, actor); err == nil {
			t.Fatal("bad scope accepted")
		}
	}
	a.config.Fields["host"] = "bad | delete"
	config := a.config
	if _, err = New(config); err == nil {
		t.Fatal("bad mapping accepted")
	}
	config = a.config
	config.Endpoint = "http://host:8888/zh-CN/app/search/search"
	if _, err = New(config); err == nil {
		t.Fatal("web UI URL accepted")
	}
}
func TestCanceledSearchAndCredentialRedaction(t *testing.T) {
	a, _, _ := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.Search(ctx, query(), p.Actor{User: "alice"}); err == nil {
		t.Fatal("canceled query succeeded")
	}
	recorder := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/invoke", strings.NewReader(`{}`))
	Handler(a, "private-host-token").ServeHTTP(recorder, r)
	if recorder.Code != 401 {
		t.Fatal("missing host auth")
	}
	r = httptest.NewRequest("POST", "/v1/invoke", strings.NewReader(`{"capability":"unknown","operation":"delete"}`))
	r.Header.Set("Authorization", "Bearer private-host-token")
	recorder = httptest.NewRecorder()
	Handler(a, "private-host-token").ServeHTTP(recorder, r)
	var reply p.Response
	json.Unmarshal(recorder.Body.Bytes(), &reply)
	if reply.Error == nil || reply.Error.Code != "unsupported" {
		t.Fatal("unknown operation accepted")
	}
	if strings.Contains(recorder.Body.String(), "secret") || strings.Contains(recorder.Body.String(), "old-session") {
		t.Fatal("credential leakage")
	}
	a.mu.Lock()
	a.cursors["expired"] = page{SID: "job-1", Hash: digest(query()), User: "alice", Expires: time.Now().Add(-time.Minute)}
	a.mu.Unlock()
	q := query()
	q.Cursor = "expired"
	if _, err := a.Search(context.Background(), q, p.Actor{User: "alice"}); err == nil {
		t.Fatal("expired cursor")
	}
}

func TestBindingFiltersAreAlwaysApplied(t *testing.T) {
	a, _, _ := fixture(t)
	a.config.Bindings[0].Filters = map[string]string{"service": "actual-api", "env": "production"}
	q := query()
	q.Text = `other-api | search index=*`
	spl, err := a.build(q, p.Actor{User: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(spl, `coalesce('service',"")="actual-api"`) || !strings.Contains(spl, `coalesce('env',"")="production"`) {
		t.Fatal("shared-index boundaries lost", spl)
	}
}
func TestBadConfigurationHandshake(t *testing.T) {
	_, err := New(Config{Endpoint: "http://host/zh-CN/app/search/search"})
	r := httptest.NewRequest("GET", "/v1/health", nil)
	r.Header.Set("Authorization", "Bearer boot")
	w := httptest.NewRecorder()
	ConfigurationErrorHandler("boot", err).ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "invalid_argument") || !strings.Contains(w.Body.String(), "REST API") {
		t.Fatal("missing actionable config error")
	}
}
