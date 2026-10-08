package monitor

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"logplatform/internal/appcatalog"
)

// MachineSample 与 agent 的上报单元一致
type MachineSample struct {
	AgentID     string  `json:"agent_id"`
	Environment string  `json:"environment"`
	Ts          int64   `json:"ts"`
	CPU         float64 `json:"cpu"`
	MemUsed     float64 `json:"mem_used"`
	MemTotal    float64 `json:"mem_total"`
	DiskUsed    float64 `json:"disk_used"`
	DiskTotal   float64 `json:"disk_total"`
	NetRxKBs    float64 `json:"net_rx_kbs"`
	NetTxKBs    float64 `json:"net_tx_kbs"`
}

func machineTable(db *monitorDB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS machine (
		ts INTEGER NOT NULL, agent TEXT NOT NULL, environment TEXT NOT NULL DEFAULT '',
		cpu REAL, mem_used REAL, mem_total REAL, disk_used REAL, disk_total REAL,
		net_rx REAL, net_tx REAL)`)
	if err != nil {
		return err
	}
	cols, _ := tableColumns(db, "machine")
	if !cols["environment"] {
		if _, err := db.Exec(`ALTER TABLE machine ADD COLUMN environment TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_machine_environment ON machine(environment, agent, ts)`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_machine ON machine(agent, ts)`); err != nil {
		return err
	}
	return nil
}

// IngestMachine 数据面：写入 + 每小时清理 7 天前样本
func IngestMachine(db *monitorDB) (func([]MachineSample) error, error) {
	if err := machineTable(db); err != nil {
		return nil, err
	}
	stmt, err := db.Prepare(`INSERT INTO machine(ts,agent,environment,cpu,mem_used,mem_total,disk_used,disk_total,net_rx,net_tx)
		VALUES(?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return nil, err
	}
	go func() {
		for range time.Tick(time.Hour) {
			db.Exec(`DELETE FROM machine WHERE ts < ?`, time.Now().Add(-7*24*time.Hour).UnixMilli())
		}
	}()
	return func(ss []MachineSample) error {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		for _, s := range ss {
			if _, err := tx.Stmt(stmt).Exec(s.Ts, s.AgentID, s.Environment, s.CPU, s.MemUsed, s.MemTotal,
				s.DiskUsed, s.DiskTotal, s.NetRxKBs, s.NetTxKBs); err != nil {
				tx.Rollback()
				return err
			}
		}
		return tx.Commit()
	}, nil
}

// DataRoutes 数据面：POST /api/machine（机器指标）+ POST /v1/metrics（OTLP JSON，OTel javaagent）
func DataRoutes(token string, st *Store, traces *TraceStore, busURL string) *http.ServeMux {
	mux := http.NewServeMux()
	applications := appcatalog.NewObserver(busURL)
	ingest, err := IngestMachine(st.db)
	if err != nil {
		return mux
	}
	otlp := NewOtlpReceiver(st.db)

	mux.HandleFunc("POST /api/machine", func(w http.ResponseWriter, r *http.Request) {
		if token != "" && r.Header.Get("X-Token") != token {
			http.Error(w, `{"error":"unauthorized"}`, 401)
			return
		}
		var s MachineSample
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&s) != nil || s.AgentID == "" || strings.TrimSpace(s.Environment) == "" {
			http.Error(w, `{"error":"bad request"}`, 400)
			return
		}
		s.Environment = strings.TrimSpace(s.Environment)
		if err := ingest([]MachineSample{s}); err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})

	otlpHandler := func(w http.ResponseWriter, r *http.Request) {
		if token != "" && r.Header.Get("X-Token") != token {
			http.Error(w, `{"error":"unauthorized"}`, 401)
			return
		}
		if otlp == nil {
			http.Error(w, `{"error":"otlp store unavailable"}`, http.StatusServiceUnavailable)
			return
		}
		body := readAll(http.MaxBytesReader(w, r.Body, 8<<20))
		// javaagent 默认 http/protobuf；部分 SDK 用 http/json —— 按 Content-Type 分流
		var err error
		jsonMode := strings.Contains(r.Header.Get("Content-Type"), "json")
		if jsonMode {
			err = otlp.Ingest(body)
		} else {
			err = otlp.IngestProto(body)
		}
		if err != nil {
			http.Error(w, `{"error":"bad payload"}`, 400)
			return
		}
		for _, service := range observedMetricServices(body, jsonMode) {
			applications.Submit(service, "otel", "")
		}
		// OTel 规范：部分成功也返回 200，否则 exporter 会整批重发
		writeJSON(w, map[string]any{"partialSuccess": map[string]int{"rejectedDataPoints": 0}})
	}
	mux.HandleFunc("POST /v1/metrics", otlpHandler)
	mux.HandleFunc("POST /v1/metrics/", otlpHandler) // 兼容尾斜杠
	// 不消费的信号返回 200（OTel 容错惯例）
	noopOK := func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		writeJSON(w, map[string]any{"partialSuccess": map[string]int{}})
	}
	mux.HandleFunc("POST /v1/logs", noopOK)
	// traces：存 span（日志关联排查用）
	mux.HandleFunc("POST /v1/traces", func(w http.ResponseWriter, r *http.Request) {
		if token != "" && r.Header.Get("X-Token") != token {
			http.Error(w, `{"error":"unauthorized"}`, 401)
			return
		}
		if traces == nil {
			http.Error(w, `{"error":"trace store unavailable"}`, http.StatusServiceUnavailable)
			return
		}
		body := readAll(http.MaxBytesReader(w, r.Body, 16<<20))
		if err := traces.IngestTraces(body); err != nil {
			http.Error(w, `{"error":"bad payload"}`, 400)
			return
		}
		for _, service := range observedTraceServices(body) {
			applications.Submit(service, "otel", "")
		}
		writeJSON(w, map[string]any{"partialSuccess": map[string]int{"rejectedSpans": 0}})
	})
	return mux
}

func readAll(r io.Reader) []byte {
	b, _ := io.ReadAll(r)
	return b
}

// MachinePoint 分钟均值序列
type MachinePoint struct {
	Minute   int64   `json:"minute"`
	CPU      float64 `json:"cpu"`
	MemUsed  float64 `json:"mem_used"`
	NetRxKBs float64 `json:"net_rx_kbs"`
	NetTxKBs float64 `json:"net_tx_kbs"`
}

func queryMachineSeries(db *monitorDB, agent, environment string, start, end int64) []MachinePoint {
	rows, err := db.Query(`SELECT (ts/60000)*60000, AVG(cpu), AVG(mem_used), AVG(net_rx), AVG(net_tx)
		FROM machine WHERE agent=? AND (?='' OR environment=?) AND ts BETWEEN ? AND ?
		GROUP BY 1 ORDER BY 1`, agent, environment, environment, start, end)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []MachinePoint{}
	for rows.Next() {
		var p MachinePoint
		if rows.Scan(&p.Minute, &p.CPU, &p.MemUsed, &p.NetRxKBs, &p.NetTxKBs) == nil {
			out = append(out, p)
		}
	}
	return out
}

func queryMachineLatest(db *monitorDB, environment string) []MachineSample {
	rows, err := db.Query(`SELECT ts,agent,environment,cpu,mem_used,mem_total,disk_used,disk_total,net_rx,net_tx
		FROM machine WHERE ts > ? AND (?='' OR environment=?)
		AND ts = (SELECT MAX(ts) FROM machine m2 WHERE m2.agent = machine.agent AND m2.environment = machine.environment)
		ORDER BY environment,agent`, time.Now().Add(-5*time.Minute).UnixMilli(), environment, environment)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []MachineSample{}
	for rows.Next() {
		var s MachineSample
		if rows.Scan(&s.Ts, &s.AgentID, &s.Environment, &s.CPU, &s.MemUsed, &s.MemTotal, &s.DiskUsed, &s.DiskTotal, &s.NetRxKBs, &s.NetTxKBs) == nil {
			out = append(out, s)
		}
	}
	return out
}
