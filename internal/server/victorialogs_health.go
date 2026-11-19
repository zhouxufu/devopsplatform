package server

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

type vlHealth struct {
	Bytes *int64
	Start string
	Error string
}

func (s *Storage) backendHealth() vlHealth {
	s.healthMu.Lock()
	defer s.healthMu.Unlock()
	if time.Since(s.healthAt) < 10*time.Second {
		return s.health
	}
	s.healthAt = time.Now()
	ctx, cancel := context.WithTimeout(s.ctx, 3*time.Second)
	defer cancel()
	resp, err := s.request(ctx, "GET", "/metrics", nil, nil, "")
	if err != nil {
		s.health = vlHealth{Error: err.Error()}
		return s.health
	}
	defer resp.Body.Close()
	scanner := bufio.NewScanner(io.LimitReader(resp.Body, 4<<20))
	var n int64
	found := false
	start := ""
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "vl_data_size_bytes{") {
			parts := strings.Fields(line)
			if len(parts) == 2 {
				v, e := strconv.ParseFloat(parts[1], 64)
				if e == nil {
					n += int64(v)
					found = true
				}
			}
		}
		if strings.HasPrefix(line, "process_start_time_seconds ") {
			start = strings.TrimPrefix(line, "process_start_time_seconds ")
		}
	}
	if scanner.Err() != nil || !found {
		s.health = vlHealth{Error: "VictoriaLogs 存储指标不可用"}
		return s.health
	}
	s.health = vlHealth{Bytes: &n, Start: start}
	// A restarted backend may have lost an acknowledged in-memory tail. Replay
	// retained identities promptly; query visibility alone never removes a tail.
	if start != "" {
		var old string
		_ = s.db.QueryRow(`SELECT value FROM log_state WHERE key='backend_start'`).Scan(&old)
		if old != "" && old != start {
			_, err = s.db.Exec(`UPDATE log_batches SET checked_at=0,visible_at=0`)
			if err != nil {
				s.health.Error = fmt.Sprint("后端重启回放调度失败")
			}
		}
		_, _ = s.db.Exec(`INSERT INTO log_state(key,value) VALUES('backend_start',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, start)
	}
	return s.health
}
