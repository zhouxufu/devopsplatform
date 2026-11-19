package server

import (
	"github.com/gorilla/websocket"
	"logplatform/internal/types"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNativeRealtimeReplayAndNewIngestRemainIndependent(t *testing.T) {
	st, err := openTestLogStorage(t)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	api := NewAPI(st, "secret", "")
	handler := api.Routes()
	ts := time.Now().Add(-time.Second).UnixMilli()
	postIngest(t, api, types.IngestRequest{AgentID: "fixture", BatchID: "replay", Events: []types.Event{{ID: "replay-1", Ts: ts, Service: "api", Environment: "prod", Level: "ERROR", Message: "timeout before connect"}}})
	server := httptest.NewServer(handler)
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/tail?service=api&environment=prod&level=ERROR&q=timeout&replay=200", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var msg wsMsg
	if err = conn.ReadJSON(&msg); err != nil || msg.Type != "log" || msg.Hit.EventID != "replay-1" {
		t.Fatal("history replay lost", err)
	}
	if err = conn.ReadJSON(&msg); err != nil || msg.Type != "replay_done" {
		t.Fatal("replay boundary lost", err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		api.hub.mu.RLock()
		n := len(api.hub.subs)
		api.hub.mu.RUnlock()
		if n > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	postIngest(t, api, types.IngestRequest{AgentID: "fixture", BatchID: "live", Events: []types.Event{{ID: "wrong-env", Ts: ts + 1, Service: "api", Environment: "test", Level: "ERROR", Message: "timeout other env"}, {ID: "wrong-level", Ts: ts + 2, Service: "api", Environment: "prod", Level: "INFO", Message: "timeout informational"}, {ID: "live-1", Ts: ts + 3, Service: "api", Environment: "prod", Level: "ERROR", Message: "timeout new event"}}})
	if err = conn.ReadJSON(&msg); err != nil || msg.Type != "log" || msg.Hit.EventID != "live-1" {
		t.Fatal("real-time ingest or filtering changed", err)
	}
	// End the subscription explicitly so the temporary HTTP test leaves no live workers.
	api.hub.mu.RLock()
	subscriptions := []*Subscriber{}
	for sub := range api.hub.subs {
		subscriptions = append(subscriptions, sub)
	}
	api.hub.mu.RUnlock()
	for _, sub := range subscriptions {
		api.hub.Unsubscribe(sub)
	}
}
