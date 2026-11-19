package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"logplatform/internal/types"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

func vlQuote(s string) string { return strconv.Quote(s) }
func (q *LogQuery) LogsQL() string {
	if q == nil || q.root == nil {
		return "*"
	}
	return compileLogsQL(q.root, "")
}
func compileLogsQL(e logExpr, field string) string {
	switch n := e.(type) {
	case logAnd:
		return "(" + compileLogsQL(n.left, field) + " AND " + compileLogsQL(n.right, field) + ")"
	case logOr:
		return "(" + compileLogsQL(n.left, field) + " OR " + compileLogsQL(n.right, field) + ")"
	case logNot:
		return "NOT (" + compileLogsQL(n.child, field) + ")"
	case logField:
		return compileLogsQL(n.child, n.name)
	case logTerm:
		if field == "" || field == "message" {
			pattern := regexp.QuoteMeta(n.value)
			if !n.phrase {
				tokens := searchTokens(n.value)
				parts := make([]string, len(tokens))
				for i, t := range tokens {
					parts[i] = regexp.QuoteMeta(t)
				}
				if len(parts) == 0 {
					return "NOT *"
				}
				pattern = `(^|[^\p{L}\p{Nd}])` + strings.Join(parts, `[^\p{L}\p{Nd}]+`)
				if !n.prefix {
					pattern += `($|[^\p{L}\p{Nd}])`
				}
			}
			return "_msg:~" + vlQuote("(?i)"+pattern)
		}
		pattern := "(?i)^" + regexp.QuoteMeta(n.value)
		if !n.prefix {
			pattern += "$"
		}
		return field + ":~" + vlQuote(pattern)
	}
	panic("unknown log expression")
}
func (s *Storage) request(ctx context.Context, method, path string, values url.Values, body []byte, ctype string) (*http.Response, error) {
	u := *s.base
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawQuery = values.Encode()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", ctype)
	if s.opts.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.opts.Token)
	}
	if s.opts.AccountID != "" {
		req.Header.Set("AccountID", s.opts.AccountID)
	}
	if s.opts.ProjectID != "" {
		req.Header.Set("ProjectID", s.opts.ProjectID)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("VictoriaLogs 请求失败")
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, fmt.Errorf("VictoriaLogs 返回 HTTP %d", resp.StatusCode)
	}
	return resp, nil
}
func (s *Storage) query(ctx context.Context, q string, start, end int64) ([]map[string]string, error) {
	v := url.Values{"query": {q}}
	if start > 0 {
		v.Set("start", time.UnixMilli(start).UTC().Format(time.RFC3339Nano))
	}
	if end > 0 {
		v.Set("end", time.UnixMilli(end).Add(time.Millisecond).UTC().Format(time.RFC3339Nano))
	}
	resp, err := s.request(ctx, "POST", "/select/logsql/query", nil, []byte(v.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	reader := &io.LimitedReader{R: resp.Body, N: (32 << 20) + 1}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	rows := []map[string]string{}
	for scanner.Scan() {
		var row map[string]string
		if err = json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return nil, fmt.Errorf("VictoriaLogs 查询结果格式无效")
		}
		rows = append(rows, row)
		if len(rows) > 100000 {
			return nil, fmt.Errorf("查询结果超出安全上限，请缩小时间范围")
		}
	}
	if err = scanner.Err(); err != nil {
		return nil, err
	}
	if reader.N <= 0 {
		return nil, fmt.Errorf("查询结果过大，请缩小范围")
	}
	return rows, nil
}
func baseLogFilter(service, env, host, level, q, trace string) (string, error) {
	parsed, err := ParseLogQuery(q)
	if err != nil {
		return "", err
	}
	parts := []string{parsed.LogsQL()}
	for _, f := range [][2]string{{"service", service}, {"environment", env}, {"host", host}, {"trace_id", trace}} {
		if f[1] != "" {
			parts = append(parts, f[0]+":="+vlQuote(f[1]))
		}
	}
	if levels := parseLevelFilter(level); len(levels) > 0 {
		clauses := []string{}
		for _, l := range levels {
			clauses = append(clauses, "level:="+vlQuote(l))
		}
		parts = append(parts, "("+strings.Join(clauses, " OR ")+")")
	}
	return strings.Join(parts, " AND "), nil
}

type logCursor struct {
	Ts       int64  `json:"t"`
	Sequence int64  `json:"i"`
	Scope    string `json:"s"`
	High     int64  `json:"h"`
	From     int64  `json:"f"`
	To       int64  `json:"u"`
}

func cursorScope(service, env, host, level, q, trace string, start, end int64) string {
	b, _ := json.Marshal([]any{service, env, host, level, q, trace, start, end})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (s *Storage) Search(service, environment, host, level string, start, end int64, q, traceID, cursor string, limit int) (*types.SearchResponse, error) {
	if limit < 1 || limit > 5000 {
		return nil, queryError("每页数量须为 1–5000")
	}
	filter, err := baseLogFilter(service, environment, host, level, q, traceID)
	if err != nil {
		return nil, err
	}
	if start < 0 || end < start {
		return nil, queryError("查询时间范围无效")
	}
	if end == 0 {
		return &types.SearchResponse{Hits: []types.Hit{}}, nil
	}
	scope := cursorScope(service, environment, host, level, q, traceID, start, end)
	c := logCursor{Scope: scope, From: start, To: end}
	if err = s.db.QueryRow(`SELECT COALESCE((SELECT CAST(value AS INTEGER) FROM log_state WHERE key='sequence'),0)`).Scan(&c.High); err != nil {
		return nil, err
	}
	if cursor != "" {
		b, e := base64.RawURLEncoding.DecodeString(cursor)
		if len(cursor) > 4096 || e != nil || json.Unmarshal(b, &c) != nil || c.From != start || c.To != end || c.Scope != scope || c.Sequence <= 0 || c.Ts < 0 || c.High < 0 {
			return nil, queryError("分页游标无效或不属于本次查询")
		}
		filter += " AND (ts:<" + strconv.FormatInt(c.Ts, 10) + " OR (ts:=" + vlQuote(strconv.FormatInt(c.Ts, 10)) + " AND ingest_sequence:<" + strconv.FormatInt(c.Sequence, 10) + "))"
	}
	filter += " AND ingest_sequence:<=" + strconv.FormatInt(c.High, 10)
	// The immutable event_key is the logical identity across at-least-once retries.
	query := filter + " | stats by (event_key) row_any() as sample | unpack_json sample | delete sample | sort by (ts desc, ingest_sequence desc) | limit " + strconv.Itoa(limit+1)
	rows, err := s.query(s.ctx, query, start, end)
	if err != nil {
		return nil, err
	}
	out := &types.SearchResponse{Hits: []types.Hit{}}
	for _, row := range rows {
		h, e := decodeLogHit(row)
		if e != nil {
			return nil, e
		}
		out.Hits = append(out.Hits, h)
	}
	if len(out.Hits) > limit {
		out.Hits = out.Hits[:limit]
		last := rows[limit-1]
		c.Ts, _ = strconv.ParseInt(last["ts"], 10, 64)
		c.Sequence, _ = strconv.ParseInt(last["ingest_sequence"], 10, 64)
		b, _ := json.Marshal(c)
		out.NextCursor = base64.RawURLEncoding.EncodeToString(b)
		out.HasMore = true
	}
	return out, nil
}
func decodeLogHit(row map[string]string) (types.Hit, error) {
	ts, err := strconv.ParseInt(row["ts"], 10, 64)
	if err != nil {
		return types.Hit{}, fmt.Errorf("VictoriaLogs 日志缺少有效时间")
	}
	return types.Hit{EventID: row["event_id"], Ts: ts, ApplicationID: row["application_id"], Service: row["service"], Environment: row["environment"], Host: row["host"], Path: row["path"], Level: row["level"], TraceID: row["trace_id"], Message: row["_msg"]}, nil
}

type MinuteCount struct {
	Minute int64 `json:"minute"`
	Count  int64 `json:"count"`
}

func (s *Storage) CountSeries(service, environment, level, q string, start, end int64) ([]MinuteCount, error) {
	f, err := baseLogFilter(service, environment, "", level, q, "")
	if err != nil {
		return nil, err
	}
	if start < 0 || end < start {
		return nil, queryError("查询时间范围无效")
	}
	if end == 0 {
		return []MinuteCount{}, nil
	}
	rows, err := s.query(s.ctx, f+" | stats by (_time:1m) count_uniq(event_key) as n", start, end)
	if err != nil {
		return nil, err
	}
	out := []MinuteCount{}
	for _, r := range rows {
		t, e := time.Parse(time.RFC3339Nano, r["_time"])
		n, e2 := strconv.ParseInt(r["n"], 10, 64)
		if e != nil || e2 != nil {
			return nil, fmt.Errorf("VictoriaLogs 分钟统计格式无效")
		}
		out = append(out, MinuteCount{t.UnixMilli(), n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Minute < out[j].Minute })
	return out, nil
}
func (s *Storage) ListServices() (map[string]int64, error) {
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).UnixMilli()
	rows, err := s.query(s.ctx, "* | stats by (service) count_uniq(event_key) as n", start, now.UnixMilli())
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for _, r := range rows {
		n, e := strconv.ParseInt(r["n"], 10, 64)
		if e != nil {
			return nil, e
		}
		out[r["service"]] = n
	}
	return out, nil
}
func (s *Storage) ListEnvironments(service string) ([]string, error) {
	f, err := baseLogFilter(service, "", "", "", "", "")
	if err != nil {
		return nil, err
	}
	now := time.Now()
	rows, err := s.query(s.ctx, f+" | uniq environment", now.AddDate(0, 0, -s.retentionDays).UnixMilli(), now.UnixMilli())
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, r := range rows {
		if r["environment"] != "" {
			out = append(out, r["environment"])
		}
	}
	sort.Strings(out)
	return out, nil
}

func logCursorWindow(cursor string) (int64, int64, error) {
	if len(cursor) > 4096 {
		return 0, 0, queryError("分页游标无效")
	}
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	var c logCursor
	if err != nil || json.Unmarshal(b, &c) != nil || c.From < 0 || c.To < c.From || c.To == 0 || c.Sequence <= 0 {
		return 0, 0, queryError("分页游标无效")
	}
	return c.From, c.To, nil
}
