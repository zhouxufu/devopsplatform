package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"logplatform/internal/types"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		u, err := url.Parse(origin)
		return err == nil && u.Host == r.Host
	},
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
}

// handleTail GET /api/tail?service&q&level&replay=500&since=<ms>
// 先回放最近 N 条（ts 升序），再实时推送增量；断线重连带 since 补齐。
func (a *APIServer) handleTail(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer c.Close()

	q := r.URL.Query()
	parsedQuery, err := ParseLogQuery(q.Get("q"))
	if err != nil {
		a.wsWrite(c, wsMsg{Type: "error", Error: err.Error()})
		return
	}
	level := strings.Join(q["level"], ",")
	filter := Filter{Service: q.Get("service"), Environment: q.Get("environment"), Levels: parseLevelFilter(level), Query: parsedQuery}
	replay := 500
	if v := q.Get("replay"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 5000 {
			replay = n
		}
	}
	since := int64(0)
	if v := q.Get("since"); v != "" {
		since, _ = strconv.ParseInt(v, 10, 64)
	}

	// 1) 回放（或按 since 补齐）：Search 返回 DESC，反转成升序
	end := time.Now().UnixMilli()
	start := end - 15*60*1000
	if since > 0 {
		start = since + 1
	}
	sub := a.hub.Subscribe(filter)
	defer a.hub.Unsubscribe(sub)
	seen := map[string]bool{}
	var resp *types.SearchResponse
	if replay > 0 {
		resp, err = a.st.Search(filter.Service, filter.Environment, "", level, start, end, q.Get("q"), "", "", replay)
	}
	if err != nil {
		a.wsWrite(c, wsMsg{Type: "error", Error: err.Error()})
		return
	}
	if resp != nil && len(resp.Hits) > 0 {
		for i := len(resp.Hits) - 1; i >= 0; i-- {
			seen[resp.Hits[i].EventID] = true
			if !a.wsWrite(c, wsMsg{Type: "log", Hit: &resp.Hits[i]}) {
				return
			}
		}
	}
	a.wsWrite(c, wsMsg{Type: "replay_done"})

	// 2) 订阅增量
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-sub.done: // 被 hub 踢除（慢客户端）
			a.wsWrite(c, wsMsg{Type: "slow_client"})
			return
		case hit := <-sub.ch:
			if hit.EventID != "" && seen[hit.EventID] {
				continue
			}
			if !a.wsWrite(c, wsMsg{Type: "log", Hit: &hit}) {
				return
			}
		case <-ping.C:
			if err := c.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
				return
			}
		}
	}
}

type wsMsg struct {
	Type  string     `json:"type"`
	Hit   *types.Hit `json:"hit,omitempty"`
	Error string     `json:"error,omitempty"`
}

func (a *APIServer) wsWrite(c *websocket.Conn, m wsMsg) bool {
	c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	b, err := json.Marshal(m)
	if err != nil {
		return false
	}
	return c.WriteMessage(websocket.TextMessage, b) == nil
}
