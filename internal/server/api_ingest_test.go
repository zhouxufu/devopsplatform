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

func postIngest(t *testing.T, a *APIServer, req types.IngestRequest) types.IngestResponse {
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
	a.Routes().ServeHTTP(w, httpReq)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var ack types.IngestResponse
	if err := json.NewDecoder(w.Body).Decode(&ack); err != nil {
		t.Fatal(err)
	}
	awaitTestLogDelivery(t, a.st)
	return ack
}

func TestIngestReturnsMatchingAckAndDeduplicatesRetry(t *testing.T) {
	st, err := openTestLogStorage(t)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	api := NewAPI(st, "secret", "")
	req := types.IngestRequest{
		AgentID: "prod/node-a", BatchID: "batch-1", QueueDepth: 3, SpoolBytes: 1024,
		Events: []types.Event{{ID: "event-1", Ts: time.Now().UnixMilli(), Service: "api", Environment: "prod", Message: "INFO ok"}},
	}
	ack := postIngest(t, api, req)
	if ack.BatchID != req.BatchID || ack.Accepted != 1 || ack.Acknowledged != 1 {
		t.Fatalf("first ack = %#v", ack)
	}
	ack = postIngest(t, api, req)
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
	st, err := openTestLogStorage(t)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	api := NewAPI(st, "secret", "")
	message := "2026-09-16 10:24:37.025 [Dubbo] ERROR Failed to get metadata\norg.apache.dubbo.rpc.RpcException: Failed to invoke remote method\n\tat org.apache.dubbo.rpc.protocol.dubbo.DubboInvoker.doInvoke(DubboInvoker.java:158)\nCaused by: java.net.ConnectException: Connection refused"
	ack := postIngest(t, api, types.IngestRequest{
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

func TestIngestStableIdentityRenameKeepsAckAndStoredHistory(t *testing.T) {
	st, err := openTestLogStorage(t)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	api := NewAPI(st, "secret", "")
	req := types.IngestRequest{AgentID: "agent_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", AgentName: "test/before", AgentAliases: []string{"test/before"}, BatchID: "identity-batch", Events: []types.Event{{ID: "stable-event", Ts: time.Now().UnixMilli(), Service: "api", Environment: "test", Host: "before", Message: "preserved"}}}
	if ack := postIngest(t, api, req); ack.Accepted != 1 {
		t.Fatal(ack)
	}
	req.AgentName = "test/after"
	req.AgentAliases = append(req.AgentAliases, req.AgentName)
	if ack := postIngest(t, api, req); ack.Accepted != 0 || ack.Acknowledged != 1 {
		t.Fatal("retry dedup changed", ack)
	}
	if len(api.agents) != 1 || api.agents[req.AgentID].Name != "test/after" || api.agents[req.AgentID].Events != 1 {
		t.Fatal("identity/metadata did not survive transport")
	}
	rec := httptest.NewRecorder()
	api.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/search?service=api&environment=test", nil))
	if rec.Code != 200 || !bytes.Contains(rec.Body.Bytes(), []byte(`"host":"before"`)) {
		t.Fatalf("historical host rewritten: %s", rec.Body.String())
	}
}

func TestIngestColoredJavaIsSearchableAndRetryRemainsIdempotent(t *testing.T) {
	st, err := openTestLogStorage(t)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	api := NewAPI(st, "secret", "")
	raw := "\x1b[32m2026-11-18 08:21:59 943\x1b[0;39m \x1b[31mERROR\x1b[0m [io-4010-exec-10] p6spy : query failed\n\x1b[31mjava.net.ConnectException\x1b[0m\n\tat Example.run(Example.java:7)"
	clean := "2026-11-18 08:21:59 943 ERROR [io-4010-exec-10] p6spy : query failed\njava.net.ConnectException\n\tat Example.run(Example.java:7)"
	now := time.Now().UnixMilli()
	req := types.IngestRequest{AgentID: "colored-agent", BatchID: "colored-batch", Events: []types.Event{{ID: "colored-event", Ts: now, Service: "orders", Environment: "test", Message: raw}}}
	if ack := postIngest(t, api, req); ack.Accepted != 1 || ack.Acknowledged != 1 {
		t.Fatal(ack)
	}
	req.BatchID = "colored-retry"
	if ack := postIngest(t, api, req); ack.Accepted != 0 || ack.Acknowledged != 1 {
		t.Fatal("raw payload retry changed", ack)
	}
	got, err := st.Search("orders", "test", "", "ERROR", now-1000, now+1000, "ConnectException", "", "", 10)
	if err != nil || len(got.Hits) != 1 {
		t.Fatal(got, err)
	}
	if got.Hits[0].Message != clean || got.Hits[0].Ts != now {
		t.Fatal("message, stack or timestamp changed", got.Hits[0])
	}
}
