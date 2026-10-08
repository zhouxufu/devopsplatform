package server

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"logplatform/internal/types"
)

// MetricsAggregator 日志衍生指标：按 service+level 分钟计数，
// 每个整分钟关闭后把聚合值（几十字节）经事件总线推给 monitor。
// 只传聚合值，不传原始日志流。
type MetricsAggregator struct {
	busURL string
	client *http.Client

	mu      sync.Mutex
	buckets map[mkey]int64
}

type mkey struct {
	minute                      int64 // 毫秒时间戳对齐到分钟
	service, environment, level string
}

func NewMetricsAggregator(busURL string) *MetricsAggregator {
	return &MetricsAggregator{busURL: busURL, client: &http.Client{Timeout: 5 * time.Second}, buckets: map[mkey]int64{}}
}

// Add 在 Insert（已 Enrich）之后喂入
func (a *MetricsAggregator) Add(events []types.Event) {
	a.mu.Lock()
	for _, e := range events {
		lv := e.Level
		if lv == "" {
			lv = "OTHER"
		}
		a.buckets[mkey{e.Ts / 60000 * 60000, e.Service, e.Environment, lv}]++
	}
	a.mu.Unlock()
}

// Run 每 10s 检查：已关闭的分钟桶 → 聚合成点 → 推总线
func (a *MetricsAggregator) Run(stop <-chan struct{}) {
	tk := time.NewTicker(10 * time.Second)
	defer tk.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tk.C:
			a.publishClosed()
		}
	}
}

type metricPoint struct {
	Minute      int64            `json:"minute"`
	Service     string           `json:"service"`
	Environment string           `json:"environment,omitempty"`
	Counts      map[string]int64 `json:"counts"`
}

func (a *MetricsAggregator) publishClosed() {
	nowMinute := time.Now().UnixMilli() / 60000 * 60000
	a.mu.Lock()
	pts := map[[3]any]*metricPoint{}
	snapshot := map[mkey]int64{}
	for k, v := range a.buckets {
		if k.minute >= nowMinute {
			continue // 当前分钟未关闭
		}
		key := [3]any{k.minute, k.service, k.environment}
		p := pts[key]
		if p == nil {
			p = &metricPoint{Minute: k.minute, Service: k.service, Environment: k.environment, Counts: map[string]int64{}}
			pts[key] = p
		}
		p.Counts[k.level] += v
		snapshot[k] = v
	}
	a.mu.Unlock()
	if len(pts) == 0 || a.busURL == "" {
		return
	}
	arr := make([]*metricPoint, 0, len(pts))
	for _, p := range pts {
		arr = append(arr, p)
	}
	body, _ := json.Marshal(map[string]any{
		"topic": "logs.metrics.minute", "from": "logs",
		"payload": map[string]any{"points": arr},
	})
	resp, err := a.client.Post(a.busURL+"/api/bus/publish", "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("[metrics] publish to bus: %v", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("[metrics] publish to bus: HTTP %s", resp.Status)
		return
	}
	// 仅在总线接收成功后扣除快照；期间迟到的同分钟日志不会被误删。
	a.mu.Lock()
	for k, sent := range snapshot {
		if current := a.buckets[k]; current <= sent {
			delete(a.buckets, k)
		} else {
			a.buckets[k] = current - sent
		}
	}
	a.mu.Unlock()
}
