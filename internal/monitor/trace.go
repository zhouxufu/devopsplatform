package monitor

import (
	"encoding/hex"
	"encoding/json"
	"log"
	"strconv"
	"time"

	collectortracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

// OTLP traces 接收：存 span（保留 6h，采样由业务侧控制），按 traceId 出调用链。
// 设计为"日志关联排查"用：配合 logs 插件的 traceId 点击跳转，不是全量 APM。

// Span 一条 span 的存储视图
type Span struct {
	TraceID     string            `json:"trace_id"`
	SpanID      string            `json:"span_id"`
	ParentID    string            `json:"parent_id,omitempty"`
	Service     string            `json:"service"`
	Environment string            `json:"environment,omitempty"`
	Instance    string            `json:"instance,omitempty"`
	Name        string            `json:"name"`
	StartMs     int64             `json:"start_ms"`
	DurMs       int64             `json:"dur_ms"`
	Status      int32             `json:"status"`          // 0=UNSET 1=OK 2=ERROR
	Attrs       map[string]string `json:"attrs,omitempty"` // 属性（http.url/db.statement/rpc.method…，值截断）
	Err         string            `json:"err,omitempty"`   // 异常摘要（exception.type: message）
}

// TraceStore spans 存储与查询；storeAttrs=false 丢弃属性（极端流量防御开关）
type TraceStore struct {
	db         *monitorDB
	storeAttrs bool
}

func NewTraceStore(db *monitorDB, storeAttrs bool) (*TraceStore, error) {
	const create = `CREATE TABLE IF NOT EXISTS spans (
		trace_id TEXT NOT NULL, span_id TEXT NOT NULL, parent_id TEXT DEFAULT '',
		service TEXT, environment TEXT NOT NULL DEFAULT '', instance TEXT NOT NULL DEFAULT '', name TEXT, start_ms INTEGER, dur_ms INTEGER, status INTEGER DEFAULT 0,
		attrs TEXT DEFAULT '', err TEXT DEFAULT '',
		PRIMARY KEY(environment, trace_id, span_id))`
	if _, err := db.Exec(create); err != nil {
		return nil, err
	}
	cols, err := tableColumns(db, "spans")
	if err != nil {
		return nil, err
	}
	for col, ddl := range map[string]string{
		"environment": `ALTER TABLE spans ADD COLUMN environment TEXT NOT NULL DEFAULT ''`,
		"instance":    `ALTER TABLE spans ADD COLUMN instance TEXT NOT NULL DEFAULT ''`,
		"attrs":       `ALTER TABLE spans ADD COLUMN attrs TEXT DEFAULT ''`,
		"err":         `ALTER TABLE spans ADD COLUMN err TEXT DEFAULT ''`,
	} {
		if !cols[col] {
			if _, err := db.Exec(ddl); err != nil {
				return nil, err
			}
		}
	}
	pkHasEnvironment := db.dialect == "mysql"
	if db.dialect == "sqlite" {
		rows, err := db.Query(`PRAGMA table_info(spans)`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var cid, notNull, pk int
			var name, ctype string
			var dflt any
			if rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk) == nil && name == "environment" && pk > 0 {
				pkHasEnvironment = true
			}
		}
		rows.Close()
	}
	if !pkHasEnvironment {
		tx, err := db.Begin()
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec(`ALTER TABLE spans RENAME TO spans_legacy`); err == nil {
			_, err = tx.Exec(create)
		}
		if err == nil {
			_, err = tx.Exec(`INSERT INTO spans(trace_id,span_id,parent_id,service,environment,instance,name,start_ms,dur_ms,status,attrs,err)
				SELECT trace_id,span_id,parent_id,service,environment,instance,name,start_ms,dur_ms,status,attrs,err FROM spans_legacy`)
		}
		if err == nil {
			_, err = tx.Exec(`DROP TABLE spans_legacy`)
		}
		if err != nil {
			tx.Rollback()
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
	}
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_spans_trace ON spans(trace_id)`)
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_spans_start ON spans(start_ms)`)
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_spans_environment_start ON spans(environment,start_ms)`)
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_spans_instance_start ON spans(environment,instance,start_ms)`)
	go func() { // 保留 6 小时
		for range time.Tick(30 * time.Minute) {
			if _, err := db.Exec(`DELETE FROM spans WHERE start_ms < ?`,
				time.Now().Add(-6*time.Hour).UnixMilli()); err != nil {
				log.Printf("[trace] cleanup: %v", err)
			}
		}
	}()
	return &TraceStore{db: db, storeAttrs: storeAttrs}, nil
}

// IngestTraces 处理 protobuf 编码的 traces 导出
func (t *TraceStore) IngestTraces(body []byte) error {
	var req collectortracev1.ExportTraceServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		return err
	}
	tx, err := t.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO spans(trace_id,span_id,parent_id,service,environment,instance,name,start_ms,dur_ms,status,attrs,err)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(environment,trace_id,span_id) DO UPDATE SET parent_id=excluded.parent_id,service=excluded.service,instance=excluded.instance,
		name=excluded.name,start_ms=excluded.start_ms,dur_ms=excluded.dur_ms,status=excluded.status,attrs=excluded.attrs,err=excluded.err`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, rs := range req.ResourceSpans {
		svc, env, instance := "unknown", "", ""
		if r := rs.GetResource(); r != nil {
			svc, env, instance = protoResourceIdentity(r.Attributes)
		}
		for _, ss := range rs.ScopeSpans {
			for _, sp := range ss.Spans {
				if len(sp.GetTraceId()) == 0 || len(sp.GetSpanId()) == 0 {
					continue
				}
				startMs := int64(sp.GetStartTimeUnixNano() / 1e6)
				durMs := int64((sp.GetEndTimeUnixNano() - sp.GetStartTimeUnixNano()) / 1e6)
				attrsJSON, errSum := "", ""
				if t.storeAttrs {
					attrs, errSum2 := extractSpanDetail(sp)
					attrsJSON, errSum = attrs, errSum2
				}
				if _, err := stmt.Exec(
					hex.EncodeToString(sp.GetTraceId()),
					hex.EncodeToString(sp.GetSpanId()),
					hex.EncodeToString(sp.GetParentSpanId()),
					svc, env, instance, sp.GetName(), startMs, durMs,
					int32(sp.GetStatus().GetCode()),
					attrsJSON, errSum); err != nil {
					tx.Rollback()
					return err
				}
			}
		}
	}
	return tx.Commit()
}

// ByTrace 按 traceId 取全部 span（时间序）
func (t *TraceStore) ByTrace(traceID, environment, instance string) ([]Span, error) {
	rows, err := t.db.Query(`SELECT trace_id,span_id,parent_id,service,environment,instance,name,start_ms,dur_ms,status,attrs,err
		FROM spans s WHERE trace_id=? AND (?='' OR environment=?)
		AND (?='' OR EXISTS (SELECT 1 FROM spans si WHERE si.trace_id=s.trace_id AND si.environment=s.environment AND si.instance=?))
		ORDER BY start_ms`, traceID, environment, environment, instance, instance)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Span{}
	for rows.Next() {
		var s Span
		var attrs, errStr string
		if rows.Scan(&s.TraceID, &s.SpanID, &s.ParentID, &s.Service, &s.Environment, &s.Instance, &s.Name, &s.StartMs, &s.DurMs, &s.Status, &attrs, &errStr) == nil {
			if attrs != "" {
				json.Unmarshal([]byte(attrs), &s.Attrs)
			}
			s.Err = errStr
			out = append(out, s)
		}
	}
	return out, nil
}

// extractSpanDetail 属性（值截断 512B、单 span 总量 2KB、黑名单）+ 异常事件摘要
var spanAttrBlacklist = map[string]bool{"user_agent": true, "http.user_agent": true, "user.id": true}

func extractSpanDetail(sp *tracev1.Span) (string, string) {
	attrs := map[string]string{}
	total := 0
	for _, kv := range sp.GetAttributes() {
		k := kv.GetKey()
		if spanAttrBlacklist[k] {
			continue
		}
		var v string
		switch x := kv.GetValue().GetValue().(type) {
		case *commonv1.AnyValue_StringValue:
			v = x.StringValue
		case *commonv1.AnyValue_IntValue:
			v = strconv.FormatInt(x.IntValue, 10)
		case *commonv1.AnyValue_BoolValue:
			v = strconv.FormatBool(x.BoolValue)
		default:
			continue
		}
		if len(v) > 512 {
			v = v[:512] + "…"
		}
		if total += len(k) + len(v); total > 2048 {
			break // 单 span 属性预算
		}
		attrs[k] = v
	}
	// 异常事件（exception.type/message/stacktrace）
	errSum := ""
	for _, ev := range sp.GetEvents() {
		if ev.GetName() != "exception" {
			continue
		}
		et, em := "", ""
		for _, kv := range ev.GetAttributes() {
			switch kv.GetKey() {
			case "exception.type":
				et = kv.GetValue().GetStringValue()
			case "exception.message":
				em = kv.GetValue().GetStringValue()
			}
		}
		if et != "" || em != "" {
			errSum = et + ": " + em
			if len(errSum) > 512 {
				errSum = errSum[:512] + "…"
			}
			break
		}
	}
	b, _ := json.Marshal(attrs)
	return string(b), errSum
}

// RecentTraces 最近 trace（供浏览）：取最近窗口内有 ERROR 的优先，再补正常
func (t *TraceStore) RecentTraces(service, environment, instance string, limit int) []map[string]any {
	since := time.Now().Add(-2 * time.Hour).UnixMilli()
	rows, err := t.db.Query(`SELECT trace_id, environment, COUNT(*) n, SUM(status=2) errs, MIN(start_ms) st, MAX(start_ms+dur_ms) en
		FROM spans s WHERE start_ms > ? AND (?='' OR environment=?)
		AND ((?='' AND ?='') OR EXISTS (
			SELECT 1 FROM spans si WHERE si.trace_id=s.trace_id AND si.environment=s.environment
			AND (?='' OR si.service=?) AND (?='' OR si.instance=?)))
		GROUP BY environment,trace_id ORDER BY errs DESC, en DESC LIMIT ?`,
		since, environment, environment, service, instance, service, service, instance, instance, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, environment string
		var n, errs int64
		var st, en int64
		if rows.Scan(&id, &environment, &n, &errs, &st, &en) == nil {
			out = append(out, map[string]any{"trace_id": id, "environment": environment, "spans": n, "errors": errs, "start_ms": st, "total_ms": en - st})
		}
	}
	return out
}
