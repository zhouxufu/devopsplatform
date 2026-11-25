package devtools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBuildLogUsesPipelineIdentityBeforeReading(t *testing.T) {
	reads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/pipeline/runs/51":
			w.Write([]byte(`{"service":"gis","id":51,"number":1}`))
		case "/api/pipeline/runs/51/log":
			reads++
			w.Write([]byte(`{"log":"fixture"}`))
		default:
			t.Errorf("unexpected %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service := NewToolService(Upstreams{Deploy: server.URL})
	args := map[string]any{"service": "other", "build_id": int64(51)}
	if _, err := service.buildLog(context.Background(), args); err == nil || reads != 0 {
		t.Fatal("returned another application's logs")
	}
	args["service"] = "gis"
	if _, err := service.buildLog(context.Background(), args); err != nil || reads != 1 {
		t.Fatal(reads, err)
	}
}
