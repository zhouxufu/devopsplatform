package server

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"logplatform/internal/types"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrLogQueueFull = errors.New("日志投递队列已满，尚未确认本批次，请稍后重试")
var ErrLogIdentityConflict = errors.New("相同日志事件或批次标识携带不同内容")

type queuedLog struct {
	Event    types.Event `json:"event"`
	Key      string      `json:"key"`
	Sequence int64       `json:"sequence"`
}

func hashLog(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func compressLogs(v any) ([]byte, error) {
	var b bytes.Buffer
	z, _ := gzip.NewWriterLevel(&b, gzip.BestSpeed)
	if err := json.NewEncoder(z).Encode(v); err != nil {
		return nil, err
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
func unpackLogs(b []byte) ([]queuedLog, error) {
	z, e := gzip.NewReader(bytes.NewReader(b))
	if e != nil {
		return nil, e
	}
	defer z.Close()
	var out []queuedLog
	e = json.NewDecoder(io.LimitReader(z, 32<<20)).Decode(&out)
	return out, e
}
func newLogID() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}
func invalidLog(e types.Event, retention int) string {
	if e.Service == "" || strings.TrimSpace(e.Message) == "" {
		return "缺少服务或日志正文"
	}
	now := time.Now()
	if e.Ts < now.AddDate(0, 0, -retention).UnixMilli() || e.Ts > now.Add(24*time.Hour).UnixMilli() {
		return "日志时间超出保留期或未来容差"
	}
	for _, s := range []string{e.ID, e.Service, e.Environment, e.Host, e.Path, e.TraceID, e.ApplicationID} {
		if len(s) > 4096 || !utf8.ValidString(s) || strings.ContainsAny(s, "\x00\r\n") {
			return "日志维度非法或过长"
		}
	}
	if len(e.Message) > 256<<10 || !utf8.ValidString(e.Message) {
		return "日志正文无效或超过 256 KiB"
	}
	return ""
}

// AcceptBatch commits receipt identities and compressed payload atomically before
// acknowledging the Agent. The queue is not a second searchable log backend.
func (s *Storage) AcceptBatch(agent, batch, sourceHash string, evs []types.Event) ([]types.Event, int, error) {
	if len(evs) == 0 {
		return []types.Event{}, 0, nil
	}
	if batch == "" {
		batch = newLogID()
	}
	batchKey := hashLog([]string{agent, batch})
	s.mu.Lock()
	defer s.mu.Unlock()
	var existing string
	if err := s.db.QueryRow(`SELECT source_hash FROM log_batch_receipts WHERE id=?`, batchKey).Scan(&existing); err == nil {
		if existing != sourceHash {
			return nil, 0, ErrLogIdentityConflict
		}
		return []types.Event{}, 0, nil
	} else if err != sql.ErrNoRows {
		return nil, 0, err
	}
	if s.DiskUsage() >= s.opts.QueueMaxBytes {
		return nil, 0, ErrLogQueueFull
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	var seq int64
	tx.QueryRow(`SELECT CAST(value AS INTEGER) FROM log_state WHERE key='sequence'`).Scan(&seq)
	now := time.Now().UnixMilli()
	accepted := []types.Event{}
	payload := []queuedLog{}
	quarantined := 0
	var quarantineBytes int64
	for i, original := range evs {
		e := original
		if e.ID == "" {
			e.ID = hashLog([]any{batchKey, i})
		}
		key := hashLog([]string{e.Service, e.Environment, e.ID})
		// App catalog resolution and enrichment are server-side, not source identity.
		identity := original
		identity.ID = e.ID
		identity.ApplicationID = ""
		fingerprint := hashLog(identity)
		var prior string
		err = tx.QueryRow(`SELECT payload_hash FROM log_receipts WHERE event_key=?`, key).Scan(&prior)
		if err == nil {
			if prior != fingerprint {
				return nil, 0, ErrLogIdentityConflict
			}
			continue
		}
		if err != sql.ErrNoRows {
			return nil, 0, err
		}
		reason := invalidLog(e, s.retentionDays)
		if reason == "" && e.Ts < time.Now().Add(-s.opts.RetryWindow).UnixMilli() {
			reason = "超过接收重试窗口；需明确检查后修复/补录"
		}
		if _, err = tx.Exec(`INSERT INTO log_receipts(event_key,payload_hash,batch_id,created_at) VALUES(?,?,?,?)`, key, fingerprint, batchKey, now); err != nil {
			return nil, 0, err
		}
		if reason != "" {
			raw, _ := json.Marshal(original)
			compressed, e2 := compressLogs(json.RawMessage(raw))
			if e2 != nil {
				return nil, 0, e2
			}
			if _, err = tx.Exec(`INSERT INTO log_quarantine(batch_id,event_id,payload,reason,created_at) VALUES(?,?,?,?,?)`, batchKey, e.ID, compressed, reason, now); err != nil {
				return nil, 0, err
			}
			quarantineBytes += int64(len(compressed))
			quarantined++
			continue
		}
		Enrich(&e, 256<<10)
		seq++
		payload = append(payload, queuedLog{e, key, seq})
		accepted = append(accepted, e)
	}
	compressed, err := compressLogs(payload)
	if err != nil {
		return nil, 0, err
	}
	// Account for page/index/WAL overhead and refuse the entire transaction.
	if s.DiskUsage()+(int64(len(compressed))+quarantineBytes)*3+int64(len(evs))*2048 > s.opts.QueueMaxBytes {
		return nil, 0, ErrLogQueueFull
	}
	if _, err = tx.Exec(`INSERT INTO log_batch_receipts(id,source_hash,created_at) VALUES(?,?,?)`, batchKey, sourceHash, now); err != nil {
		return nil, 0, err
	}
	if _, err = tx.Exec(`INSERT INTO log_batches(id,source_hash,payload,created_at) VALUES(?,?,?,?)`, batchKey, sourceHash, compressed, now); err != nil {
		return nil, 0, err
	}
	if _, err = tx.Exec(`INSERT INTO log_state(key,value) VALUES('sequence',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, strconv.FormatInt(seq, 10)); err != nil {
		return nil, 0, err
	}
	if err = tx.Commit(); err != nil {
		return nil, 0, err
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return accepted, quarantined, nil
}
func (s *Storage) InsertUnique(evs []types.Event) ([]types.Event, error) {
	out, _, err := s.AcceptBatch("internal", newLogID(), hashLog(evs), evs)
	return out, err
}
func (s *Storage) Insert(evs []types.Event) (int, error) {
	out, err := s.InsertUnique(evs)
	return len(out), err
}
func logsWire(events []queuedLog) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	for _, r := range events {
		e := r.Event
		if err := enc.Encode(map[string]any{"_time": time.UnixMilli(e.Ts).UTC().Format(time.RFC3339Nano), "ts": e.Ts, "event_id": e.ID, "event_key": r.Key, "ingest_sequence": r.Sequence, "application_id": e.ApplicationID, "service": e.Service, "environment": e.Environment, "host": e.Host, "path": e.Path, "level": e.Level, "trace_id": e.TraceID, "_msg": e.Message}); err != nil {
			return nil, err
		}
	}
	return b.Bytes(), nil
}

// Keep verification queries below the backend's default 16 KiB query limit,
// including a full 2,000-event Agent request. Never use physical counts as ACKs.
func (s *Storage) visibleLogKeys(events []queuedLog) (map[string]bool, error) {
	visible := map[string]bool{}
	for start := 0; start < len(events); start += 100 {
		end := start + 100
		if end > len(events) {
			end = len(events)
		}
		ids := []string{}
		for _, e := range events[start:end] {
			ids = append(ids, vlQuote(e.Key))
		}
		rows, err := s.query(s.ctx, "event_key:in("+strings.Join(ids, ",")+") | uniq event_key", 0, 0)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			visible[r["event_key"]] = true
		}
	}
	return visible, nil
}
func (s *Storage) verifyBatch(events []queuedLog) (bool, error) {
	visible, err := s.visibleLogKeys(events)
	if err != nil {
		return false, err
	}
	for _, e := range events {
		if !visible[e.Key] {
			return false, nil
		}
	}
	return true, nil
}
func (s *Storage) deliverOne() (bool, error) {
	s.deliveryMu.Lock()
	defer s.deliveryMu.Unlock()
	var id string
	var packed []byte
	var sent, created int64
	var attempts int
	// Recently sent payloads are periodically reconciled; unknown outcomes retain
	// their original identities. Old sent data is pruned only after verification.
	err := s.db.QueryRow(`SELECT id,payload,sent_at,created_at,attempts FROM log_batches WHERE (visible_at=0 AND checked_at<?) OR checked_at<? ORDER BY (visible_at=0) DESC,checked_at,created_at LIMIT 1`, time.Now().Add(-time.Second).UnixMilli(), time.Now().Add(-time.Minute).UnixMilli()).Scan(&id, &packed, &sent, &created, &attempts)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	events, err := unpackLogs(packed)
	if err != nil {
		return false, fmt.Errorf("持久队列载荷损坏，保留原记录，未确认投递")
	}
	visible, err := s.verifyBatch(events)
	if err != nil {
		return false, err
	}
	if !visible {
		// A successful HTTP status can omit bad lines. Identify missing keys,
		// retain their original payloads in quarantine, and unblock valid logs.
		if attempts >= 5 {
			if err = s.quarantineUnconfirmed(id, events); err != nil {
				return false, err
			}
			return true, nil
		}
		wire, e := logsWire(events)
		if e != nil {
			return false, e
		}
		resp, e := s.request(s.ctx, "POST", "/insert/jsonline", url.Values{"_stream_fields": {"application_id,environment,host"}}, wire, "application/x-ndjson")
		if e != nil {
			_, _ = s.db.Exec(`UPDATE log_batches SET last_error=? WHERE id=?`, e.Error(), id)
			return false, e
		}
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
	}
	now := time.Now().UnixMilli()
	if !visible {
		sent = now
	}
	if visible && sent > 0 && time.Since(time.UnixMilli(sent)) >= s.opts.ReplayWindow {
		s.mu.Lock()
		_, err = s.db.Exec(`DELETE FROM log_batches WHERE id=? AND sent_at=?`, id, sent)
		s.mu.Unlock()
		return true, err
	}
	if sent == 0 {
		sent = now
	}
	_, err = s.db.Exec(`UPDATE log_batches SET sent_at=?,checked_at=?,visible_at=?,attempts=?,last_error='' WHERE id=?`, sent, now, func() int64 {
		if visible {
			return now
		}
		return 0
	}(), func() int {
		if visible {
			return 0
		}
		return attempts + 1
	}(), id)
	_ = created
	return true, err
}
func (s *Storage) maintainQueue() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := time.Now().Add(-s.opts.RetryWindow).UnixMilli()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Pending/replay batches and unresolved quarantine always retain their keys.
	if _, err = tx.Exec(`DELETE FROM log_receipts WHERE created_at<? AND batch_id NOT IN (SELECT id FROM log_batches) AND batch_id NOT IN (SELECT batch_id FROM log_quarantine WHERE resolved_at=0)`, cutoff); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM log_batch_receipts WHERE created_at<? AND id NOT IN (SELECT id FROM log_batches) AND id NOT IN (SELECT batch_id FROM log_quarantine WHERE resolved_at=0)`, cutoff); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM log_quarantine WHERE resolved_at>0 AND resolved_at<?`, cutoff); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	_, _ = s.db.Exec(`PRAGMA incremental_vacuum(256)`)
	_, _ = s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	return nil
}
func (s *Storage) deliveryLoop() {
	defer close(s.stopped)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	maintenance := time.NewTicker(time.Minute)
	defer maintenance.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-s.wake:
		case <-tick.C:
		case <-maintenance.C:
			_ = s.backendHealth()
			_ = s.maintainQueue()
		}
		for i := 0; i < 100; i++ {
			ok, err := s.deliverOne()
			if err != nil {
				break
			}
			if !ok {
				break
			}
			select {
			case <-s.stop:
				return
			default:
			}
		}
	}
}

type LogStorageStatus struct {
	Backend             string `json:"backend"`
	QueueBytes          int64  `json:"queue_bytes"`
	QueueLimit          int64  `json:"queue_limit_bytes"`
	PendingBatches      int64  `json:"pending_batches"`
	ReplayBatches       int64  `json:"replay_batches"`
	Quarantined         int64  `json:"quarantined"`
	OldestPending       int64  `json:"oldest_pending"`
	LastError           string `json:"last_error,omitempty"`
	ReplayWindowSeconds int64  `json:"replay_window_seconds"`
	RetryWindowSeconds  int64  `json:"retry_window_seconds"`
}

func (s *Storage) StorageStatus() LogStorageStatus {
	out := LogStorageStatus{Backend: "victorialogs", QueueBytes: s.DiskUsage(), QueueLimit: s.opts.QueueMaxBytes, ReplayWindowSeconds: int64(s.opts.ReplayWindow.Seconds()), RetryWindowSeconds: int64(s.opts.RetryWindow.Seconds())}
	_ = s.db.QueryRow(`SELECT COUNT(*),COALESCE(MIN(created_at),0) FROM log_batches WHERE visible_at=0`).Scan(&out.PendingBatches, &out.OldestPending)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM log_batches WHERE visible_at>0`).Scan(&out.ReplayBatches)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM log_quarantine WHERE resolved_at=0`).Scan(&out.Quarantined)
	_ = s.db.QueryRow(`SELECT last_error FROM log_batches WHERE last_error<>'' ORDER BY checked_at LIMIT 1`).Scan(&out.LastError)
	return out
}
