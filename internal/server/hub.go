package server

import (
	"sync"

	"logplatform/internal/types"
)

// Filter tail 订阅的过滤条件（AND 组合，空 = 不限）
type Filter struct {
	Service     string
	Environment string
	Levels      levelFilter
	Query       *LogQuery
}

func (f Filter) Match(h types.Hit) bool {
	return (f.Service == "" || h.Service == f.Service) &&
		(f.Environment == "" || h.Environment == f.Environment) &&
		f.Levels.Match(h.Level) &&
		(f.Query == nil || f.Query.Match(h))
}

// Subscriber 一个 tail 连接
type Subscriber struct {
	filter Filter
	ch     chan types.Hit
	done   chan struct{}
}

// Hub 写入路径上的 fan-out：每连接有界 channel，非阻塞投递；
// 满则踢除慢客户端（由 WS 循环感知后断开，客户端重连补齐）——写入路径永不被阻塞。
type Hub struct {
	mu   sync.RWMutex
	subs map[*Subscriber]struct{}
}

func NewHub() *Hub { return &Hub{subs: map[*Subscriber]struct{}{}} }

const subChanSize = 1000

func (h *Hub) Subscribe(f Filter) *Subscriber {
	s := &Subscriber{filter: f, ch: make(chan types.Hit, subChanSize), done: make(chan struct{})}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	return s
}

func (h *Hub) Unsubscribe(s *Subscriber) {
	h.mu.Lock()
	if _, ok := h.subs[s]; ok {
		delete(h.subs, s)
		close(s.done)
	}
	h.mu.Unlock()
}

// Publish 在 Insert 成功后调用；同步遍历但投递非阻塞，总耗时微秒级
func (h *Hub) Publish(hits []types.Hit) {
	h.mu.RLock()
	for s := range h.subs {
		for _, hit := range hits {
			if !s.filter.Match(hit) {
				continue
			}
			select {
			case s.ch <- hit:
			default: // 慢客户端：踢除，写入路径不等待
				go h.Unsubscribe(s)
			}
		}
	}
	h.mu.RUnlock()
}
