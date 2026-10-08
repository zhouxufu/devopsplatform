package server

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"logplatform/internal/types"
)

func postIngest(t *testing.T, h http.Handler, req types.IngestRequest) types.IngestResponse {
	t.Helper()
	var body bytes.Buffer
	zw := gzip.NewWriter(&body)
	if err := json.NewEncoder(zw).Encode(req); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	httpReq := httptest.NewRequest(http.MethodPost, "/ingest", &body)
	httpReq.Header.Set("Content-Encoding", "gzip")
	httpReq.Header.Set("X-Token", "secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httpReq)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var ack types.IngestResponse
	if err := json.NewDecoder(w.Body).Decode(&ack); err != nil {
		t.Fatal(err)
	}
	return ack
}

func TestIngestReturnsMatchingAckAndDeduplicatesRetry(t *testing.T) {
	st, err := Open(t.TempDir(), 7, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	api := NewAPI(st, "secret", "")
	req := types.IngestRequest{
		AgentID: "prod/node-a", BatchID: "batch-1", QueueDepth: 3, SpoolBytes: 1024,
		Events: []types.Event{{ID: "event-1", Ts: time.Now().UnixMilli(), Service: "api", Environment: "prod", Message: "INFO ok"}},
	}
	ack := postIngest(t, api.Routes(), req)
	if ack.BatchID != req.BatchID || ack.Accepted != 1 || ack.Acknowledged != 1 {
		t.Fatalf("first ack = %#v", ack)
	}
	ack = postIngest(t, api.Routes(), req)
	if ack.BatchID != req.BatchID || ack.Accepted != 0 || ack.Acknowledged != 1 {
		t.Fatalf("retry ack = %#v", ack)
	}
	api.agentsMu.Lock()
	stat := *api.agents[req.AgentID]
	api.agentsMu.Unlock()
	if stat.Events != 1 || stat.QueueDepth != 3 || stat.SpoolBytes != 1024 {
		t.Fatalf("agent stat = %#v", stat)
	}
}

func TestIngestKeepsMergedJavaStackAsOneError(t *testing.T) {
	st, err := Open(t.TempDir(), 7, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	api := NewAPI(st, "secret", "")
	message := "2026-09-16 10:24:37.025 [Dubbo] ERROR Failed to get metadata\norg.apache.dubbo.rpc.RpcException: Failed to invoke remote method\n\tat org.apache.dubbo.rpc.protocol.dubbo.DubboInvoker.doInvoke(DubboInvoker.java:158)\nCaused by: java.net.ConnectException: Connection refused"
	ack := postIngest(t, api.Routes(), types.IngestRequest{
		AgentID: "test/node-a", BatchID: "stack-batch",
		Events: []types.Event{{ID: "stack-event", Ts: time.Now().UnixMilli(), Service: "orders", Environment: "test", Message: message}},
	})
	if ack.Accepted != 1 {
		t.Fatalf("accepted %d events, want 1", ack.Accepted)
	}
	rec := httptest.NewRecorder()
	api.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/search?service=orders&environment=test&level=ERROR,WARN&q=ConnectException", nil))
	var result struct {
		Hits []struct {
			Level   string `json:"level"`
			Message string `json:"message"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || len(result.Hits) != 1 || result.Hits[0].Level != "ERROR" || result.Hits[0].Message != message {
		t.Fatalf("merged error should remain searchable by its stack: status=%d body=%s", rec.Code, rec.Body.String())
	}
}
