package server

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"logplatform/internal/types"
	"net/http"
	"strconv"
	"time"
)

// Unconfirmed per-record evidence is kept, never silently discarded. This only
// runs after successful sink responses and repeated incomplete ID verification.
func (s *Storage) quarantineUnconfirmed(batch string, events []queuedLog) error {
	visible, err := s.visibleLogKeys(events)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	kept := []queuedLog{}
	for _, e := range events {
		if visible[e.Key] {
			kept = append(kept, e)
			continue
		}
		b, e2 := compressLogs(e.Event)
		if e2 != nil {
			return e2
		}
		if _, err = tx.Exec(`INSERT INTO log_quarantine(batch_id,event_id,payload,reason,created_at) VALUES(?,?,?,?,?)`, batch, e.Event.ID, b, "下游多次成功响应但未确认此事件；请核对限制、时间范围后重投", time.Now().UnixMilli()); err != nil {
			return err
		}
	}
	b, err := compressLogs(kept)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE log_batches SET payload=?,visible_at=?,checked_at=?,last_error='' WHERE id=?`, b, time.Now().UnixMilli(), time.Now().UnixMilli(), batch)
	if err != nil {
		return err
	}
	return tx.Commit()
}

type quarantineEntry struct {
	ID         int64  `json:"id"`
	EventID    string `json:"event_id"`
	Reason     string `json:"reason"`
	CreatedAt  int64  `json:"created_at"`
	ResolvedAt int64  `json:"resolved_at"`
}

func (s *Storage) quarantineList() ([]quarantineEntry, error) {
	rows, err := s.db.Query(`SELECT id,event_id,reason,created_at,resolved_at FROM log_quarantine ORDER BY id DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []quarantineEntry{}
	for rows.Next() {
		var e quarantineEntry
		if err = rows.Scan(&e.ID, &e.EventID, &e.Reason, &e.CreatedAt, &e.ResolvedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// A repair creates a new explicitly identified event and retains the rejected
// original. One quarantine item can be resolved once, atomically with enqueue.
func (s *Storage) repairQuarantine(id int64, event types.Event) (string, error) {
	if reason := invalidLog(event, s.retentionDays); reason != "" {
		return "", fmt.Errorf("修复记录无效: %s", reason)
	}
	if event.Ts < time.Now().Add(-s.opts.RetryWindow).UnixMilli() {
		return "", fmt.Errorf("修复记录仍超出接收窗口")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.DiskUsage() >= s.opts.QueueMaxBytes {
		return "", ErrLogQueueFull
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var resolved int64
	if err = tx.QueryRow(`SELECT resolved_at FROM log_quarantine WHERE id=?`, id).Scan(&resolved); err != nil {
		return "", err
	}
	if resolved != 0 {
		return "", fmt.Errorf("隔离记录已处理，未重复投递")
	}
	event.ID = "repair-" + strconv.FormatInt(id, 10) + "-" + hashLog(event)
	Enrich(&event, 256<<10)
	key := hashLog([]string{event.Service, event.Environment, event.ID})
	batch := "repair-" + strconv.FormatInt(id, 10)
	var seq int64
	if err = tx.QueryRow(`SELECT COALESCE((SELECT CAST(value AS INTEGER) FROM log_state WHERE key='sequence'),0)`).Scan(&seq); err != nil {
		return "", err
	}
	seq++
	b, err := compressLogs([]queuedLog{{event, key, seq}})
	if err != nil {
		return "", err
	}
	if s.DiskUsage()+int64(len(b))*3+4096 > s.opts.QueueMaxBytes {
		return "", ErrLogQueueFull
	}
	now := time.Now().UnixMilli()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO log_batches(id,source_hash,payload,created_at) VALUES(?,?,?,?)`, []any{batch, hashLog(event), b, now}},
		{`INSERT INTO log_receipts(event_key,payload_hash,batch_id,created_at) VALUES(?,?,?,?)`, []any{key, hashLog(event), batch, now}},
		{`UPDATE log_quarantine SET resolved_at=? WHERE id=? AND resolved_at=0`, []any{now, id}},
		{`INSERT INTO log_state(key,value) VALUES('sequence',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, []any{strconv.FormatInt(seq, 10)}},
	} {
		if _, err = tx.Exec(q.sql, q.args...); err != nil {
			return "", err
		}
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return event.ID, nil
}
func (a *APIServer) registerQuarantine(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/log-quarantine", func(w http.ResponseWriter, r *http.Request) {
		out, err := a.st.quarantineList()
		if err != nil {
			writeQueryError(w, err)
			return
		}
		writeJSON(w, out)
	})
	mux.HandleFunc("GET /api/log-quarantine/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Role") != "admin" {
			http.Error(w, `{"error":"仅管理员可查看隔离原记录"}`, 403)
			return
		}
		id, e := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if e != nil || id < 1 {
			http.Error(w, `{"error":"隔离编号无效"}`, 400)
			return
		}
		var packed []byte
		var reason string
		if e = a.st.db.QueryRow(`SELECT payload,reason FROM log_quarantine WHERE id=?`, id).Scan(&packed, &reason); e != nil {
			http.Error(w, `{"error":"隔离记录不可用"}`, 404)
			return
		}
		reader, e := gzip.NewReader(bytes.NewReader(packed))
		if e != nil {
			writeQueryError(w, e)
			return
		}
		defer reader.Close()
		var event types.Event
		if e = json.NewDecoder(io.LimitReader(reader, 8<<20)).Decode(&event); e != nil {
			writeQueryError(w, e)
			return
		}
		writeJSON(w, map[string]any{"id": id, "reason": reason, "event": event})
	})
	mux.HandleFunc("POST /api/log-quarantine/{id}/repair", func(w http.ResponseWriter, r *http.Request) {
		// Management only: this route is never exposed on the Agent data listener.
		if r.Header.Get("X-Role") != "admin" {
			http.Error(w, `{"error":"仅管理员可修复隔离日志"}`, 403)
			return
		}
		id, e := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if e != nil || id < 1 {
			http.Error(w, `{"error":"隔离编号无效"}`, 400)
			return
		}
		var body struct {
			Event   types.Event `json:"event"`
			Confirm bool        `json:"confirm"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&body) != nil || !body.Confirm {
			http.Error(w, `{"error":"请明确确认修复内容"}`, 400)
			return
		}
		eventID, err := a.st.repairQuarantine(id, body.Event)
		if err != nil {
			status := 409
			if err == sql.ErrNoRows {
				status = 404
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			writeJSON(w, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, map[string]string{"event_id": eventID})
	})
}
