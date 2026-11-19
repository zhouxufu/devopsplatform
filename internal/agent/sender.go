package agent

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"logplatform/internal/types"
)

// Sender 批量发送：500 条 / 1MB / 1s 先到先触发；gzip + token；
// 指数退避重试；成功后提交 offset 到 registry；2MB/s 简单限速。
type Sender struct {
	cfg     *Config
	in      <-chan PendingEvent
	reg     *Registry
	spool   *Spool
	dropped *atomic.Int64
	client  *http.Client
	// A sender posts serially. Reuse the compression window and buffers instead
	// of allocating a fresh flate encoder for every small batch.
	rawBuffer  bytes.Buffer
	gzipBuffer bytes.Buffer
	gzipWriter *gzip.Writer

	// 简单令牌窗口限速
	mu          sync.Mutex
	windowStart time.Time
	windowBytes int64
}

func NewSender(cfg *Config, in <-chan PendingEvent, reg *Registry, spool *Spool, dropped *atomic.Int64) *Sender {
	return &Sender{
		cfg:     cfg,
		in:      in,
		reg:     reg,
		spool:   spool,
		dropped: dropped,
		client: &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        1, // 单连接复用（keep-alive）
				IdleConnTimeout:     90 * time.Second,
				MaxConnsPerHost:     1,
				MaxIdleConnsPerHost: 1,
			},
		},
	}
}

const (
	batchCount = 500
	batchBytes = 1 << 20
	rateLimit  = 2 << 20 // 2MB/s
	backoffMax = 60 * time.Second
)

func (s *Sender) Run(ctx context.Context) {
	var writers sync.WaitGroup
	writers.Add(1)
	go func() {
		defer writers.Done()
		s.persistLoop(ctx)
	}()
	s.sendLoop(ctx)
	writers.Wait()
}

// persistLoop 与网络发送完全解耦。下游不可用时仍持续把内存事件写入磁盘；
// 达到磁盘上限后不消费 channel，让背压最终停在日志文件 FD 上。
func (s *Sender) persistLoop(ctx context.Context) {
	tk := time.NewTicker(500 * time.Millisecond)
	defer tk.Stop()
	batch := make([]PendingEvent, 0, batchCount)
	payloadBytes := 0
	flush := func(allowRetry bool) bool {
		if len(batch) == 0 {
			return true
		}
		size := len(batch)
		for {
			err := s.spool.EnqueueBatch(batch[:size])
			if err == nil {
				for i := range batch[:size] {
					p := &batch[i]
					s.reg.Commit(p.Key, p.Offset, p.Inode, p.Mtime, p.Head, p.Generation, p.Reset)
				}
				// spool 已是可靠事实源；registry 由 5s SaveLoop 合并写，崩溃时从 spool 修复。
				remaining := copy(batch, batch[size:])
				clear(batch[remaining:]) // Release confirmed log bodies while idle.
				batch = batch[:remaining]
				if len(batch) == 0 {
					payloadBytes = 0
					return true
				}
				size = len(batch)
				continue
			}
			// A larger persistence batch may exceed a small queue's capacity.
			// Admit a prefix and let the independent sender drain it, rather than
			// waiting forever for the entire batch to fit an empty queue.
			if errors.Is(err, ErrSpoolFull) && size > 1 {
				size = (size + 1) / 2
				continue
			}
			if !allowRetry {
				log.Printf("[agent] persist pending batch on shutdown: %v; source offset was not advanced", err)
				return false
			}
			log.Printf("[agent] local spool unavailable/full, pausing readers: %v", err)
			select {
			case <-ctx.Done():
				return false
			case <-time.After(time.Second):
			}
		}
	}
	for {
		select {
		case <-ctx.Done():
			flush(false)
			return
		case p := <-s.in:
			batch = append(batch, p)
			payloadBytes += len(p.Ev.Message)
			if len(batch) >= batchCount || payloadBytes >= batchBytes {
				if !flush(true) {
					return
				}
			}
		case <-tk.C:
			if !flush(true) {
				return
			}
		}
	}
}

func (s *Sender) sendLoop(ctx context.Context) {
	lastHeartbeat := time.Time{}
	for {
		records, err := s.spool.Fetch(batchCount, batchBytes)
		if err != nil {
			log.Printf("[agent] read local spool failed: %v", err)
			if !waitContext(ctx, time.Second) {
				return
			}
			continue
		}
		spoolBytes, _ := s.spool.Bytes()
		if len(records) == 0 {
			if time.Since(lastHeartbeat) >= 10*time.Second {
				req := types.IngestRequest{AgentID: s.cfg.AgentID, Dropped: int(s.dropped.Load()), QueueDepth: len(s.in), SpoolBytes: spoolBytes}
				if err := s.post(ctx, &req); err != nil {
					return
				}
				s.dropped.Store(0)
				lastHeartbeat = time.Now()
			}
			if !waitContext(ctx, time.Second) {
				return
			}
			continue
		}
		events := make([]types.Event, len(records))
		for i := range records {
			events[i] = records[i].Pending.Ev
		}
		req := types.IngestRequest{
			AgentID: s.cfg.AgentID, BatchID: makeBatchID(events), Dropped: int(s.dropped.Load()),
			QueueDepth: len(s.in), SpoolBytes: spoolBytes, Events: events,
		}
		if err := s.post(ctx, &req); err != nil {
			return // 未 ACK 的记录仍在 spool；下次启动继续发送
		}
		if err := s.spool.Ack(records); err != nil {
			log.Printf("[agent] ACK persisted remotely but local cleanup failed (safe retry): %v", err)
			if !waitContext(ctx, time.Second) {
				return
			}
			continue
		}
		s.dropped.Store(0)
		lastHeartbeat = time.Now()
	}
}

func makeBatchID(events []types.Event) string {
	h := sha256.New()
	for i := range events {
		h.Write([]byte(events[i].ID))
		h.Write([]byte{0})
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func (s *Sender) post(ctx context.Context, req *types.IngestRequest) error {
	req.AgentName = s.cfg.AgentName
	req.AgentAliases = s.cfg.AgentAliases
	s.rawBuffer.Reset()
	if err := json.NewEncoder(&s.rawBuffer).Encode(req); err != nil {
		return err
	}
	s.gzipBuffer.Reset()
	if s.gzipWriter == nil {
		zw, err := gzip.NewWriterLevel(&s.gzipBuffer, flate.BestSpeed)
		if err != nil {
			return err
		}
		s.gzipWriter = zw
	} else {
		s.gzipWriter.Reset(&s.gzipBuffer)
	}
	if _, err := s.gzipWriter.Write(s.rawBuffer.Bytes()); err != nil {
		return err
	}
	if err := s.gzipWriter.Close(); err != nil {
		return err
	}

	var attempts int
	for {
		s.throttle(int64(s.gzipBuffer.Len()))
		httpReq, err := http.NewRequestWithContext(ctx, "POST", s.cfg.ServerURL+"/ingest", bytes.NewReader(s.gzipBuffer.Bytes()))
		if err != nil {
			return err
		}
		httpReq.Header.Set("Content-Encoding", "gzip")
		httpReq.Header.Set("Content-Type", "application/json")
		if s.cfg.Token != "" {
			httpReq.Header.Set("X-Token", s.cfg.Token)
		}
		resp, err := s.client.Do(httpReq)
		if err == nil {
			var ack types.IngestResponse
			decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&ack)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK && decodeErr == nil && ack.BatchID == req.BatchID && ack.Acknowledged == len(req.Events) {
				return nil // 完整且匹配的 ACK 才允许清理本地 spool
			}
			err = fmt.Errorf("status %d or invalid ack (batch=%q acknowledged=%d decode=%v)", resp.StatusCode, ack.BatchID, ack.Acknowledged, decodeErr)
		}
		attempts++
		backoff := time.Duration(1<<uint(min(attempts, 6))) * time.Second
		if backoff > backoffMax {
			backoff = backoffMax
		}
		// 约 20% 抖动，避免大量 agent 在中心恢复时同时重试。
		jitter := time.Duration(time.Now().UnixNano() % int64(max(backoff/5, time.Millisecond)))
		backoff = backoff - backoff/10 + jitter
		log.Printf("[agent] ingest not acknowledged, retry in %v: %v", backoff, err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}
}

func waitContext(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// throttle 2MB/s 窗口限速：当前秒超限则睡到下一秒
func (s *Sender) throttle(n int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if s.windowStart.IsZero() || now.Sub(s.windowStart) >= time.Second {
		s.windowStart, s.windowBytes = now, 0
	}
	for s.windowBytes+n > rateLimit {
		sleep := time.Second - time.Since(s.windowStart) + 10*time.Millisecond
		s.mu.Unlock()
		time.Sleep(sleep)
		s.mu.Lock()
		s.windowStart, s.windowBytes = time.Now(), 0
	}
	s.windowBytes += n
}
