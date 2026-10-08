package deploy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// logHub 构建/部署共用的实时日志广播（订阅 key：构建=buildID，部署=-deployID）
type logHub struct {
	mu   sync.RWMutex
	subs map[int64]map[chan string]struct{}
}

// NewLogHub 导出构造（构建/部署共用日志中枢）
func NewLogHub() *logHub { return &logHub{subs: map[int64]map[chan string]struct{}{}} }

func (h *logHub) Subscribe(key int64) (chan string, func()) {
	ch := make(chan string, 256)
	h.mu.Lock()
	if h.subs[key] == nil {
		h.subs[key] = map[chan string]struct{}{}
	}
	h.subs[key][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs[key], ch)
		h.mu.Unlock()
	}
}

func (h *logHub) emit(key int64, line string) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs[key] {
		select {
		case ch <- line:
		default: // 慢消费者丢行（历史日志仍完整）
		}
	}
}

func (h *logHub) Close(key int64) {
	h.mu.Lock()
	for ch := range h.subs[key] {
		close(ch)
	}
	delete(h.subs, key)
	h.mu.Unlock()
}

func (s *Store) httpPost(url string, body any) bool {
	b, _ := json.Marshal(body)
	client := &http.Client{Timeout: 5 * time.Second}
	for attempt := 0; attempt < 3; attempt++ {
		resp, err := client.Post(url, "application/json", bytes.NewReader(b))
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return true
			}
		}
		if attempt < 2 {
			time.Sleep(time.Duration(attempt+1) * 250 * time.Millisecond)
		}
	}
	return false
}
