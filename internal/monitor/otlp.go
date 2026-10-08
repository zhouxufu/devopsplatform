package monitor

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	collectorv1 "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

// OTLP/HTTP 接收端：protobuf（OTel javaagent 默认，走 http/protobuf）与 JSON（部分 SDK）双编码。
// 支持三类信号（当前消费 metrics）：
//   - gauge/sum(asDouble)：分钟内取最新值（覆盖）
//   - histogram：累计值差分为增量（count/sum 累加到当分钟）→ QPS、平均耗时
//
// jvm_memory 系列按 area 属性拆出 _heap/_non_heap 后缀。
type OtlpReceiver struct {
	db *monitorDB
	mu sync.Mutex
	// histogram 累计值状态（exporter 默认 cumulative tempo，需差分）
	lastCum    map[[4]string][2]float64 // environment|service|instance|name -> {cnt,sum}
	histCum7   map[[7]string]histState  // environment|service|instance|name|iface|meth|status
	counterCum map[[7]string]float64    // environment|service|instance|name|iface|meth|status
}

func NewOtlpReceiver(db *monitorDB) *OtlpReceiver {
	if ensureOtlpMinuteTable(db) != nil || histTable(db) != nil || counterTable(db) != nil {
		return nil
	}
	return &OtlpReceiver{db: db, lastCum: map[[4]string][2]float64{}, histCum7: map[[7]string]histState{}, counterCum: map[[7]string]float64{}}
}

func tableColumns(db *monitorDB, table string) (map[string]bool, error) {
	if db.dialect == "mysql" {
		rows, err := db.Query(`SELECT COLUMN_NAME FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=?`, table)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		cols := map[string]bool{}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				return nil, err
			}
			cols[name] = true
		}
		return cols, rows.Err()
	}
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var cid, notNull, pk int
		var name, ctype string
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, rows.Err()
}

func ensureOtlpMinuteTable(db *monitorDB) error {
	const create = `CREATE TABLE IF NOT EXISTS otlp_minute (
		minute INTEGER NOT NULL, service TEXT NOT NULL, environment TEXT NOT NULL DEFAULT '', instance TEXT NOT NULL DEFAULT '', name TEXT NOT NULL,
		val REAL DEFAULT 0, cnt INTEGER DEFAULT 0,
		PRIMARY KEY(minute, service, environment, instance, name))`
	cols, err := tableColumns(db, "otlp_minute")
	if err != nil {
		return err
	}
	if len(cols) > 0 && (!cols["environment"] || !cols["instance"]) {
		environmentExpr, instanceExpr := `''`, `''`
		if cols["environment"] {
			environmentExpr = "environment"
		}
		if cols["instance"] {
			instanceExpr = "instance"
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err = tx.Exec(`ALTER TABLE otlp_minute RENAME TO otlp_minute_legacy`); err == nil {
			_, err = tx.Exec(create)
		}
		if err == nil {
			_, err = tx.Exec(`INSERT INTO otlp_minute(minute,service,environment,instance,name,val,cnt)
				SELECT minute,service,` + environmentExpr + `,` + instanceExpr + `,name,val,cnt FROM otlp_minute_legacy`)
		}
		if err == nil {
			_, err = tx.Exec(`DROP TABLE otlp_minute_legacy`)
		}
		if err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	} else if _, err := db.Exec(create); err != nil {
		return err
	}
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_otlp_minute_lookup ON otlp_minute(service,environment,instance,name,minute)`)
	return err
}

// otlpAttr OTLP 属性（resource 与 datapoint 通用）
type otlpAttr struct {
	Key   string `json:"key"`
	Value struct {
		StringValue string `json:"stringValue"`
	} `json:"value"`
}

type otlpPayload struct {
	ResourceMetrics []struct {
		Resource struct {
			Attributes []otlpAttr `json:"attributes"`
		} `json:"resource"`
		ScopeMetrics []struct {
			Metrics []otlpMetric `json:"metrics"`
		} `json:"scopeMetrics"`
	} `json:"resourceMetrics"`
}

type otlpMetric struct {
	Name      string     `json:"name"`
	Gauge     *simplePts `json:"gauge"`
	Sum       *simplePts `json:"sum"`
	Histogram *histoPts  `json:"histogram"`
}

type simplePts struct {
	DataPoints []struct {
		AsDouble   float64    `json:"asDouble"`
		Attributes []otlpAttr `json:"attributes"`
	} `json:"dataPoints"`
}

type histoPts struct {
	DataPoints []struct {
		Count string  `json:"count"` // proto3 JSON 数字是字符串
		Sum   float64 `json:"sum"`
	} `json:"dataPoints"`
}

func attrVal(kvs []otlpAttr, key string) string {
	for _, kv := range kvs {
		if kv.Key == key {
			return kv.Value.StringValue
		}
	}
	return ""
}

func jsonResourceIdentity(attrs []otlpAttr) (string, string, string) {
	svc := attrVal(attrs, "service.name")
	if svc == "" {
		svc = "unknown"
	}
	for _, key := range []string{"deployment.environment.name", "deployment.environment", "service.environment"} {
		if env := attrVal(attrs, key); env != "" {
			for _, instanceKey := range []string{"service.instance.id", "host.id", "host.name"} {
				if instance := attrVal(attrs, instanceKey); instance != "" {
					return svc, env, instance
				}
			}
			return svc, env, ""
		}
	}
	for _, key := range []string{"service.instance.id", "host.id", "host.name"} {
		if instance := attrVal(attrs, key); instance != "" {
			return svc, "", instance
		}
	}
	return svc, "", ""
}

func protoResourceIdentity(attrs []*commonv1.KeyValue) (string, string, string) {
	values := map[string]string{}
	for _, kv := range attrs {
		if v := attrStr(kv); v != "" {
			values[kv.GetKey()] = v
		}
	}
	svc := values["service.name"]
	if svc == "" {
		svc = "unknown"
	}
	env, instance := "", ""
	for _, key := range []string{"deployment.environment.name", "deployment.environment", "service.environment"} {
		if values[key] != "" {
			env = values[key]
			break
		}
	}
	for _, key := range []string{"service.instance.id", "host.id", "host.name"} {
		if values[key] != "" {
			instance = values[key]
			break
		}
	}
	return svc, env, instance
}

// IngestProto 处理 protobuf 编码导出（OTel javaagent 默认 http/protobuf 走此路径）
func (o *OtlpReceiver) IngestProto(body []byte) error {
	var req collectorv1.ExportMetricsServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		return err
	}
	minute := time.Now().UnixMilli() / 60000 * 60000
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, rm := range req.ResourceMetrics {
		svc, env, instance := "unknown", "", ""
		if rs := rm.GetResource(); rs != nil {
			svc, env, instance = protoResourceIdentity(rs.Attributes)
		}
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				switch d := m.Data.(type) {
				case *metricsv1.Metric_Gauge:
					o.ingestGaugePts(minute, svc, env, instance, m.Name, len(d.Gauge.DataPoints), func(i int) ([]*commonv1.KeyValue, float64) {
						dp := d.Gauge.DataPoints[i]
						return dp.Attributes, numberVal(dp)
					})
				case *metricsv1.Metric_Sum:
					cum := d.Sum.GetAggregationTemporality() == metricsv1.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE
					for _, dp := range d.Sum.DataPoints {
						o.ingestCounterPt(minute, svc, env, instance, m.Name, dp.Attributes, numberVal(dp), cum)
					}
				case *metricsv1.Metric_Histogram:
					for _, dp := range d.Histogram.DataPoints {
						iface, meth, status := parseHistDims(dp.Attributes)
						o.ingestHistPt(minute, svc, env, instance, m.Name, iface, meth, status,
							float64(dp.GetCount()), dp.GetSum(),
							dp.GetExplicitBounds(), dp.GetBucketCounts())
					}
				}
			}
		}
	}
	return nil
}

// numberVal NumberDataPoint 的双类型取值（OTel 用 asDouble，Micrometer 常用 asInt）
func numberVal(dp *metricsv1.NumberDataPoint) float64 {
	if v, ok := dp.Value.(*metricsv1.NumberDataPoint_AsInt); ok {
		return float64(v.AsInt)
	}
	return dp.GetAsDouble()
}

// ingestGaugePts gauge/sum：单点取值；多点求和（如 Micrometer 按池拆分的内存指标）
func (o *OtlpReceiver) ingestGaugePts(minute int64, svc, env, instance, name string, n int, pt func(i int) ([]*commonv1.KeyValue, float64)) {
	if n == 0 {
		return
	}
	if n == 1 {
		attrs, val := pt(0)
		o.ingestSimple(minute, svc, env, instance, name, attrs, val)
		return
	}
	var sum float64
	for i := 0; i < n; i++ {
		_, v := pt(i)
		sum += v
	}
	o.upsert(minute, svc, env, instance, name, sum, 0, false)
}

// ingestSimple gauge/sum：分钟内取最新值覆盖（jvm_memory 系列按 area 拆分）
func (o *OtlpReceiver) ingestSimple(minute int64, svc, env, instance, name string, attrs []*commonv1.KeyValue, val float64) {
	n := name
	if name == "jvm_memory_used_bytes" || name == "jvm_memory_committed_bytes" {
		for _, kv := range attrs {
			if kv.GetKey() == "area" {
				n = name + "_" + kv.GetValue().GetStringValue()
				break
			}
		}
	}
	o.upsert(minute, svc, env, instance, n, val, 0, false)
}

// ingestHistogram 直方图：累计值差分为增量累加到当分钟
func (o *OtlpReceiver) ingestHistogram(minute int64, svc, env, instance, name string, cnt, sum float64) {
	key := [4]string{env, svc, instance, name}
	last := o.lastCum[key]
	dCnt, dSum := cnt-last[0], sum-last[1]
	if dCnt < 0 || dSum < 0 { // 计数器重置（服务重启）
		dCnt, dSum = cnt, sum
	}
	o.lastCum[key] = [2]float64{cnt, sum}
	if dCnt > 0 || dSum > 0 {
		o.upsert(minute, svc, env, instance, name, dSum, int64(dCnt), true)
	}
}

// Ingest 处理一次导出（JSON 编码）
func (o *OtlpReceiver) Ingest(body []byte) error {
	var p otlpPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return err
	}
	minute := time.Now().UnixMilli() / 60000 * 60000
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, rm := range p.ResourceMetrics {
		svc, env, instance := jsonResourceIdentity(rm.Resource.Attributes)
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				switch {
				case m.Histogram != nil:
					var cnt, sum float64
					for _, dp := range m.Histogram.DataPoints {
						if c, err := strconv.ParseFloat(dp.Count, 64); err == nil {
							cnt += c
						}
						sum += dp.Sum
					}
					key := [4]string{env, svc, instance, m.Name}
					last := o.lastCum[key]
					dCnt, dSum := cnt-last[0], sum-last[1]
					if dCnt < 0 || dSum < 0 { // 计数器重置（服务重启）
						dCnt, dSum = cnt, sum
					}
					o.lastCum[key] = [2]float64{cnt, sum}
					if dCnt > 0 || dSum > 0 {
						o.upsert(minute, svc, env, instance, m.Name, dSum, int64(dCnt), true)
					}
				case m.Gauge != nil:
					o.ingestJSONPoints(minute, svc, env, instance, m.Name, m.Gauge.DataPoints)
				case m.Sum != nil:
					o.ingestJSONPoints(minute, svc, env, instance, m.Name, m.Sum.DataPoints)
				}
			}
		}
	}
	return nil
}

func (o *OtlpReceiver) ingestJSONPoints(minute int64, svc, env, instance, name string, pts []struct {
	AsDouble   float64    `json:"asDouble"`
	Attributes []otlpAttr `json:"attributes"`
}) {
	for _, dp := range pts {
		n := name
		if area := attrVal(dp.Attributes, "area"); area != "" && (name == "jvm_memory_used_bytes" || name == "jvm_memory_committed_bytes") {
			n = name + "_" + area // heap / non_heap
		}
		o.upsert(minute, svc, env, instance, n, dp.AsDouble, 0, false)
	}
}

// upsert accumulate=true（直方图增量累加）或 false（取最新值覆盖）
func (o *OtlpReceiver) upsert(minute int64, svc, env, instance, name string, val float64, cnt int64, accumulate bool) {
	if accumulate {
		o.db.Exec(`INSERT INTO otlp_minute(minute,service,environment,instance,name,val,cnt) VALUES(?,?,?,?,?,?,?)
			ON CONFLICT(minute,service,environment,instance,name) DO UPDATE SET val=val+excluded.val, cnt=cnt+excluded.cnt`,
			minute, svc, env, instance, name, val, cnt)
	} else {
		o.db.Exec(`INSERT INTO otlp_minute(minute,service,environment,instance,name,val,cnt) VALUES(?,?,?,?,?,?,?)
			ON CONFLICT(minute,service,environment,instance,name) DO UPDATE SET val=excluded.val`,
			minute, svc, env, instance, name, val, cnt)
	}
}

// OtlpServices 已上报 OTLP 的服务
func OtlpServices(db *monitorDB) []string {
	rows, err := db.Query(`SELECT DISTINCT service FROM otlp_minute ORDER BY service`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if rows.Scan(&s) == nil {
			out = append(out, s)
		}
	}
	return out
}

// ObservedEnvironments 返回服务已有监控数据中的环境，用于独立监控页筛选。
func ObservedEnvironments(db *monitorDB, svc string) []string {
	seen := map[string]bool{}
	for _, table := range []string{"metrics_minute", "otlp_minute", "otlp_hist", "otlp_counter", "spans"} {
		cols, err := tableColumns(db, table)
		if err != nil || !cols["environment"] || !cols["service"] {
			continue
		}
		rows, err := db.Query(`SELECT DISTINCT environment FROM `+table+`
			WHERE environment<>'' AND (?='' OR service=?)`, svc, svc)
		if err != nil {
			continue
		}
		for rows.Next() {
			var environment string
			if rows.Scan(&environment) == nil && environment != "" {
				seen[environment] = true
			}
		}
		rows.Close()
	}
	out := make([]string, 0, len(seen))
	for environment := range seen {
		out = append(out, environment)
	}
	sort.Strings(out)
	return out
}

// ObservedInstances 返回服务/环境已有 OTLP 数据中的实例标识。
func ObservedInstances(db *monitorDB, svc, environment string) []string {
	seen := map[string]bool{}
	for _, table := range []string{"otlp_minute", "otlp_hist", "otlp_counter", "spans"} {
		cols, err := tableColumns(db, table)
		if err != nil || !cols["instance"] || !cols["service"] {
			continue
		}
		rows, err := db.Query(`SELECT DISTINCT instance FROM `+table+`
			WHERE instance<>'' AND service=? AND (?='' OR environment=?)`, svc, environment, environment)
		if err != nil {
			continue
		}
		for rows.Next() {
			var instance string
			if rows.Scan(&instance) == nil && instance != "" {
				seen[instance] = true
			}
		}
		rows.Close()
	}
	out := make([]string, 0, len(seen))
	for instance := range seen {
		out = append(out, instance)
	}
	sort.Strings(out)
	return out
}

// OtlpNames 某服务全部可用指标名（动态发现）
func OtlpNames(db *monitorDB, svc, environment, instance string) []string {
	rows, err := db.Query(`SELECT DISTINCT name FROM otlp_minute
		WHERE service=? AND (?='' OR environment=?) AND (?='' OR instance=?) ORDER BY name`,
		svc, environment, environment, instance, instance)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if rows.Scan(&s) == nil {
			out = append(out, s)
		}
	}
	return out
}

// ---------- 直方图按接口维度拆分 + 分位数 ----------
// otlp_hist：结构化维度 (minute, service, metric, iface, meth, status) 存 count/sum/bounds/buckets，
// 接口（URI/route）是一级选择，方法/状态是接口下的细分；查询期从 bucket 算 TP50/95/99/max。

type histState struct {
	cnt, sum float64
	buckets  []float64
}

// attrStr 属性值转字符串（OTel AnyValue 支持字符串/整数/布尔——状态码常是整数）
func attrStr(kv *commonv1.KeyValue) string {
	switch x := kv.GetValue().GetValue().(type) {
	case *commonv1.AnyValue_StringValue:
		return x.StringValue
	case *commonv1.AnyValue_IntValue:
		return strconv.FormatInt(x.IntValue, 10)
	case *commonv1.AnyValue_BoolValue:
		return strconv.FormatBool(x.BoolValue)
	}
	return ""
}

// parseHistDims 属性 → (接口, 方法, 状态) 三元组
func parseHistDims(attrs []*commonv1.KeyValue) (iface, meth, status string) {
	for _, kv := range attrs {
		switch kv.GetKey() {
		case "uri", "http.route", "http.target", "rpc.service", "rpc.system": // rpc.system 兜底接口名（如 dubbo）
			if iface == "" {
				iface = attrStr(kv)
			}
		case "http.request.method", "http.method", "method", "rpc.method":
			if meth == "" {
				meth = attrStr(kv)
			}
		case "http.response.status_code", "status", "http.response.status", "rpc.dubbo.status_code":
			if status == "" {
				status = attrStr(kv)
			}
		}
	}
	return
}

func histTable(db *monitorDB) error {
	const create = `CREATE TABLE IF NOT EXISTS otlp_hist (
		minute INTEGER NOT NULL, service TEXT NOT NULL, environment TEXT NOT NULL DEFAULT '', instance TEXT NOT NULL DEFAULT '', name TEXT NOT NULL,
		iface TEXT NOT NULL DEFAULT '', meth TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT '',
		cnt REAL DEFAULT 0, sum REAL DEFAULT 0, bounds TEXT DEFAULT '[]', buckets TEXT DEFAULT '[]',
		PRIMARY KEY(minute, service, environment, instance, name, iface, meth, status))`
	cols, err := tableColumns(db, "otlp_hist")
	if err != nil {
		return err
	}
	// 最早的 dims 拼接结构没有可靠的无损拆分方式，沿用原迁移策略重建。
	if cols["dims"] {
		if _, err := db.Exec(`DROP TABLE otlp_hist`); err != nil {
			return err
		}
		cols = nil
	}
	if len(cols) > 0 && (!cols["environment"] || !cols["instance"]) {
		environmentExpr, instanceExpr := `''`, `''`
		if cols["environment"] {
			environmentExpr = "environment"
		}
		if cols["instance"] {
			instanceExpr = "instance"
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err = tx.Exec(`ALTER TABLE otlp_hist RENAME TO otlp_hist_legacy`); err == nil {
			_, err = tx.Exec(create)
		}
		if err == nil {
			_, err = tx.Exec(`INSERT INTO otlp_hist(minute,service,environment,instance,name,iface,meth,status,cnt,sum,bounds,buckets)
				SELECT minute,service,` + environmentExpr + `,` + instanceExpr + `,name,iface,meth,status,cnt,sum,bounds,buckets FROM otlp_hist_legacy`)
		}
		if err == nil {
			_, err = tx.Exec(`DROP TABLE otlp_hist_legacy`)
		}
		if err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	} else if _, err := db.Exec(create); err != nil {
		return err
	}
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_otlp_hist_lookup ON otlp_hist(service,environment,instance,name,iface,minute)`)
	return err
}

// ingestHistPt 单个直方图数据点：按 (svc,name,iface,meth,status) 差分累计值，增量并入分钟行
func (o *OtlpReceiver) ingestHistPt(minute int64, svc, env, instance, name, iface, meth, status string, cnt, sum float64, bounds []float64, buckets []uint64) {
	key := [7]string{env, svc, instance, name, iface, meth, status}
	st := o.histCum7[key]
	dCnt, dSum := cnt-st.cnt, sum-st.sum
	dB := make([]float64, len(buckets))
	reset := dCnt < 0 || dSum < 0
	for i, b := range buckets {
		dB[i] = float64(b)
		if len(st.buckets) == len(buckets) && !reset {
			dB[i] -= st.buckets[i]
			if dB[i] < 0 {
				reset = true
			}
		}
	}
	if reset { // 服务重启等计数器归零：当次全量
		dCnt, dSum = cnt, sum
		for i, b := range buckets {
			dB[i] = float64(b)
		}
	}
	stB := make([]float64, len(buckets))
	for i, b := range buckets {
		stB[i] = float64(b)
	}
	o.histCum7[key] = histState{cnt, sum, stB}
	if dCnt <= 0 && dSum <= 0 {
		return
	}
	o.mergeHistRow(minute, svc, env, instance, name, iface, meth, status, dCnt, dSum, bounds, dB)
	// 聚合口径（JVM 页请求量卡片）不区分维度
	o.upsert(minute, svc, env, instance, name, dSum, int64(dCnt), true)
}

func (o *OtlpReceiver) mergeHistRow(minute int64, svc, env, instance, name, iface, meth, status string, dCnt, dSum float64, bounds []float64, dB []float64) {
	var oCnt, oSum float64
	var b1, b2 string
	var oBounds, oBuckets []float64
	err := o.db.QueryRow(`SELECT cnt,sum,bounds,buckets FROM otlp_hist
		WHERE minute=? AND service=? AND environment=? AND instance=? AND name=? AND iface=? AND meth=? AND status=?`,
		minute, svc, env, instance, name, iface, meth, status).
		Scan(&oCnt, &oSum, &b1, &b2)
	if err == nil {
		json.Unmarshal([]byte(b1), &oBounds)
		json.Unmarshal([]byte(b2), &oBuckets)
	}
	if len(oBounds) != len(bounds) || len(bounds) == 0 { // bounds 变化（或首见）以新为准
		oBounds, oBuckets = bounds, make([]float64, len(bounds)+1)
	}
	if len(oBuckets) < len(dB) { // 兜底对齐
		nb := make([]float64, len(dB))
		copy(nb, oBuckets)
		oBuckets = nb
	}
	for i, v := range dB {
		oBuckets[i] += v
	}
	bb1, _ := json.Marshal(oBounds)
	bb2, _ := json.Marshal(oBuckets)
	o.db.Exec(`INSERT INTO otlp_hist(minute,service,environment,instance,name,iface,meth,status,cnt,sum,bounds,buckets)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(minute,service,environment,instance,name,iface,meth,status) DO UPDATE SET cnt=cnt+excluded.cnt, sum=sum+excluded.sum,
			bounds=excluded.bounds, buckets=excluded.buckets`,
		minute, svc, env, instance, name, iface, meth, status, dCnt, dSum, string(bb1), string(bb2))
}

// IfaceStat 接口（URI）流量统计
type IfaceStat struct {
	Iface string  `json:"iface"`
	Cnt   float64 `json:"cnt"`
}

// Interfaces 接口列表：近 1h 流量 Top（iface 为空 = 该指标无接口维度，如客户端调用）
func Interfaces(db *monitorDB, svc, environment, instance, name string) []IfaceStat {
	rows, err := db.Query(`SELECT iface, SUM(cnt) c FROM otlp_hist
		WHERE service=? AND (?='' OR environment=?) AND (?='' OR instance=?) AND name=? AND minute > ? GROUP BY iface ORDER BY c DESC LIMIT 100`,
		svc, environment, environment, instance, instance, name, time.Now().Add(-time.Hour).UnixMilli()/60000*60000)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []IfaceStat
	for rows.Next() {
		var d IfaceStat
		if rows.Scan(&d.Iface, &d.Cnt) == nil {
			out = append(out, d)
		}
	}
	return out
}

// IfaceAttr 接口下可选的方法/状态组合
type IfaceAttr struct {
	Meth   string  `json:"meth"`
	Status string  `json:"status"`
	Cnt    float64 `json:"cnt"`
}

func IfaceAttrs(db *monitorDB, svc, environment, instance, name, iface string) []IfaceAttr {
	rows, err := db.Query(`SELECT meth, status, SUM(cnt) c FROM otlp_hist
		WHERE service=? AND (?='' OR environment=?) AND (?='' OR instance=?) AND name=? AND iface=? AND minute > ? GROUP BY meth,status ORDER BY c DESC LIMIT 50`,
		svc, environment, environment, instance, instance, name, iface, time.Now().Add(-time.Hour).UnixMilli()/60000*60000)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []IfaceAttr
	for rows.Next() {
		var d IfaceAttr
		if rows.Scan(&d.Meth, &d.Status, &d.Cnt) == nil {
			out = append(out, d)
		}
	}
	return out
}

// QuantilePoint 分位点（毫秒）
type QuantilePoint struct {
	Minute int64   `json:"minute"`
	Cnt    float64 `json:"cnt"`
	Avg    float64 `json:"avg"`
	P50    float64 `json:"p50"`
	P95    float64 `json:"p95"`
	P99    float64 `json:"p99"`
	Max    float64 `json:"max"`
}

type minuteAgg struct {
	cnt, sum        float64
	bounds, buckets []float64
}

// Quantiles 接口（可细分方法/状态，留空=聚合）的分钟分位序列；单位与指标一致
// AvailPoint 分钟可用率
type AvailPoint struct {
	Minute int64   `json:"minute"`
	Total  float64 `json:"total"`
	OK     float64 `json:"ok"`
	Rate   float64 `json:"rate"` // 0~1；数据无 status 维度时返回空序列
}

// AvailRule 可用率判定规则（服务级）：成功码白名单 / 忽略码（不计入分子分母）
type AvailRule struct {
	Service     string   `json:"service"`
	Environment string   `json:"environment"`         // 必填；"*"=显式全环境兜底规则
	Name        string   `json:"name"`                // 指标名（空=服务默认规则）
	OkCodes     []string `json:"ok_codes"`            // 算成功的 code（空=内置规则 0/空/2xx/OK）
	IgnoreCodes []string `json:"ignore_codes"`        // 不计入可用率的 code（"正常拒绝"）
	Inherited   bool     `json:"inherited,omitempty"` // 命中服务默认规则
	Builtin     bool     `json:"builtin,omitempty"`   // 未配任何规则，走内置语义
}

func ensureAvailRulesTable(db *monitorDB) error {
	const create = `CREATE TABLE IF NOT EXISTS avail_rules (
		service TEXT NOT NULL, environment TEXT NOT NULL DEFAULT '', name TEXT NOT NULL DEFAULT '',
		ok_codes TEXT DEFAULT '[]', ignore_codes TEXT DEFAULT '[]',
		PRIMARY KEY(service, environment, name))`
	cols, err := tableColumns(db, "avail_rules")
	if err != nil {
		return err
	}
	if len(cols) == 0 {
		_, err = db.Exec(create)
		return err
	}
	if cols["environment"] && cols["name"] {
		_, err = db.Exec(`UPDATE avail_rules SET environment='*' WHERE TRIM(COALESCE(environment,''))=''`)
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`ALTER TABLE avail_rules RENAME TO avail_rules_legacy`); err == nil {
		_, err = tx.Exec(create)
	}
	if err == nil {
		switch {
		case cols["name"]:
			_, err = tx.Exec(`INSERT INTO avail_rules(service,environment,name,ok_codes,ignore_codes)
				SELECT service,'*',name,ok_codes,ignore_codes FROM avail_rules_legacy`)
		default:
			_, err = tx.Exec(`INSERT INTO avail_rules(service,environment,name,ok_codes,ignore_codes)
				SELECT service,'*','',ok_codes,ignore_codes FROM avail_rules_legacy`)
		}
	}
	if err == nil {
		_, err = tx.Exec(`DROP TABLE avail_rules_legacy`)
	}
	if err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// codeVerdict 三态判定
const (
	verdictOK     = 0
	verdictFail   = 1
	verdictIgnore = 2
)

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func verdict(s string, rule *AvailRule) int {
	if rule != nil {
		if contains(rule.IgnoreCodes, s) {
			return verdictIgnore
		}
		if len(rule.OkCodes) > 0 {
			if contains(rule.OkCodes, s) {
				return verdictOK
			}
			return verdictFail
		}
	}
	// 默认规则：2xx/3xx/0/OK/空 成功，其余失败
	if s == "" || strings.HasPrefix(s, "2") || strings.HasPrefix(s, "3") || s == "0" || s == "OK" {
		return verdictOK
	}
	return verdictFail
}

// isOKStatus 兼容旧调用
func isOKStatus(s string) bool { return verdict(s, nil) == verdictOK }

// Availability 接口可用率序列（不筛 status，跨全部状态聚合）
func Availability(db *monitorDB, svc, environment, instance, name, iface, meth string, start, end int64, rule *AvailRule) []AvailPoint {
	q := `SELECT minute,status,SUM(cnt) FROM otlp_hist
		WHERE service=? AND (?='' OR environment=?) AND (?='' OR instance=?) AND name=? AND iface=? AND minute BETWEEN ? AND ?`
	args := []any{svc, environment, environment, instance, instance, name, iface, start, end}
	if meth != "" {
		q += ` AND meth=?`
		args = append(args, meth)
	}
	q += ` GROUP BY minute,status`
	// counter 类指标（业务 code）：val 即次数，同样并入可用率口径
	cq := `SELECT minute,status,SUM(val) FROM otlp_counter
		WHERE service=? AND (?='' OR environment=?) AND (?='' OR instance=?) AND name=? AND iface=? AND minute BETWEEN ? AND ?`
	cargs := []any{svc, environment, environment, instance, instance, name, iface, start, end}
	if meth != "" {
		cq += ` AND meth=?`
		cargs = append(cargs, meth)
	}
	cq += ` GROUP BY minute,status`
	rows, err := db.Query(q+` UNION ALL `+cq, append(args, cargs...)...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	type mAgg struct {
		total, ok float64
		hasStatus bool
	}
	byMinute := map[int64]*mAgg{}
	for rows.Next() {
		var m int64
		var status string
		var c float64
		if rows.Scan(&m, &status, &c) != nil {
			continue
		}
		agg := byMinute[m]
		if agg == nil {
			agg = &mAgg{}
			byMinute[m] = agg
		}
		if status != "" {
			agg.hasStatus = true
		}
		switch verdict(status, rule) {
		case verdictIgnore:
			continue // 忽略码：不计入分子分母
		case verdictOK:
			agg.total += c
			agg.ok += c
		default:
			agg.total += c
		}
	}
	var out []AvailPoint
	for m, agg := range byMinute {
		if !agg.hasStatus || agg.total == 0 {
			continue // 无状态维度，可用率不可计算
		}
		out = append(out, AvailPoint{Minute: m, Total: agg.total, OK: agg.ok, Rate: agg.ok / agg.total})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Minute < out[j].Minute })
	return out
}

// counterTable 维度化计数表：业务 code 可用率的存储（code 复用 status 列，成功判定见 isOKStatus）
func counterTable(db *monitorDB) error {
	const create = `CREATE TABLE IF NOT EXISTS otlp_counter (
		minute INTEGER NOT NULL, service TEXT NOT NULL, environment TEXT NOT NULL DEFAULT '', instance TEXT NOT NULL DEFAULT '', name TEXT NOT NULL,
		iface TEXT NOT NULL DEFAULT '', meth TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT '',
		val REAL DEFAULT 0, cnt INTEGER DEFAULT 0,
		PRIMARY KEY(minute, service, environment, instance, name, iface, meth, status))`
	cols, err := tableColumns(db, "otlp_counter")
	if err != nil {
		return err
	}
	if len(cols) > 0 && (!cols["environment"] || !cols["instance"]) {
		environmentExpr, instanceExpr := `''`, `''`
		if cols["environment"] {
			environmentExpr = "environment"
		}
		if cols["instance"] {
			instanceExpr = "instance"
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err = tx.Exec(`ALTER TABLE otlp_counter RENAME TO otlp_counter_legacy`); err == nil {
			_, err = tx.Exec(create)
		}
		if err == nil {
			_, err = tx.Exec(`INSERT INTO otlp_counter(minute,service,environment,instance,name,iface,meth,status,val,cnt)
				SELECT minute,service,` + environmentExpr + `,` + instanceExpr + `,name,iface,meth,status,val,cnt FROM otlp_counter_legacy`)
		}
		if err == nil {
			_, err = tx.Exec(`DROP TABLE otlp_counter_legacy`)
		}
		if err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	} else if _, err := db.Exec(create); err != nil {
		return err
	}
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_otlp_counter_lookup ON otlp_counter(service,environment,instance,name,iface,minute)`)
	return err
}

// parseCounterDims counter 的 tag → 三元组：route/uri→iface、method→meth、code/result→status
func parseCounterDims(attrs []*commonv1.KeyValue) (iface, meth, status string) {
	for _, kv := range attrs {
		switch kv.GetKey() {
		case "uri", "http.route", "http.target", "route", "rpc.service":
			if iface == "" {
				iface = attrStr(kv)
			}
		case "http.request.method", "http.method", "method", "rpc.method":
			if meth == "" {
				meth = attrStr(kv)
			}
		case "code", "biz.code", "biz_code", "result", "outcome", "status", "http.response.status_code":
			if status == "" {
				status = attrStr(kv)
			}
		}
	}
	return
}

// ingestCounterPt counter 数据点：cumulative 差分 / delta 直加，按维度落 otlp_counter
func (o *OtlpReceiver) ingestCounterPt(minute int64, svc, env, instance, name string, attrs []*commonv1.KeyValue, val float64, cumulative bool) {
	iface, meth, status := parseCounterDims(attrs)
	key := [7]string{env, svc, instance, name, iface, meth, status}
	if cumulative { // 累计计数器：差分出本分钟增量
		last := o.counterCum[key]
		d := val - last
		if d < 0 { // 计数器重置（服务重启）
			d = val
		}
		o.counterCum[key] = val
		if d <= 0 {
			return
		}
		val = d
	}
	o.db.Exec(`INSERT INTO otlp_counter(minute,service,environment,instance,name,iface,meth,status,val,cnt)
		VALUES(?,?,?,?,?,?,?,?,?,1)
		ON CONFLICT(minute,service,environment,instance,name,iface,meth,status) DO UPDATE SET val=val+excluded.val, cnt=cnt+1`,
		minute, svc, env, instance, name, iface, meth, status, val)
	o.upsert(minute, svc, env, instance, name, val, 1, true) // 无维度聚合口径
}

func Quantiles(db *monitorDB, svc, environment, instance, name, iface, meth, status string, start, end int64) []QuantilePoint {
	q := `SELECT minute,cnt,sum,bounds,buckets FROM otlp_hist
		WHERE service=? AND (?='' OR environment=?) AND (?='' OR instance=?) AND name=? AND iface=? AND minute BETWEEN ? AND ?`
	args := []any{svc, environment, environment, instance, instance, name, iface, start, end}
	if meth != "" {
		q += ` AND meth=?`
		args = append(args, meth)
	}
	if status != "" {
		q += ` AND status=?`
		args = append(args, status)
	}
	q += ` ORDER BY minute`
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	byMinute := map[int64]*minuteAgg{}
	for rows.Next() {
		var m int64
		var c, s float64
		var b1, b2 string
		var bounds, buckets []float64
		if rows.Scan(&m, &c, &s, &b1, &b2) != nil {
			continue
		}
		json.Unmarshal([]byte(b1), &bounds)
		json.Unmarshal([]byte(b2), &buckets)
		agg := byMinute[m]
		if agg == nil {
			agg = &minuteAgg{}
			byMinute[m] = agg
		}
		agg.cnt += c
		agg.sum += s
		if len(agg.bounds) == 0 {
			agg.bounds = bounds
		}
		if len(agg.buckets) < len(buckets) {
			nb := make([]float64, len(buckets))
			copy(nb, agg.buckets)
			agg.buckets = nb
		}
		for i, v := range buckets {
			agg.buckets[i] += v
		}
	}
	out := make([]QuantilePoint, 0, len(byMinute))
	for m, agg := range byMinute {
		p := QuantilePoint{Minute: m, Cnt: agg.cnt}
		if agg.cnt > 0 {
			p.Avg = agg.sum / agg.cnt
		}
		p.P50 = bucketQuantile(agg.bounds, agg.buckets, 0.50)
		p.P95 = bucketQuantile(agg.bounds, agg.buckets, 0.95)
		p.P99 = bucketQuantile(agg.bounds, agg.buckets, 0.99)
		p.Max = bucketMax(agg.bounds, agg.buckets)
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Minute < out[j].Minute })
	return out
}

// bucketQuantile 线性插值分位（Prometheus histogram_quantile 同款思路）
func bucketQuantile(bounds, buckets []float64, q float64) float64 {
	if len(buckets) == 0 {
		return 0
	}
	total := 0.0
	for _, c := range buckets {
		total += c
	}
	if total == 0 {
		return 0
	}
	target := q * total
	cum := 0.0
	for i, c := range buckets {
		if c <= 0 {
			continue
		}
		if cum+c >= target {
			lo := 0.0
			if i > 0 && i-1 < len(bounds) {
				lo = bounds[i-1]
			}
			hi := lo + 1
			if i < len(bounds) {
				hi = bounds[i]
			}
			if hi <= lo {
				hi = lo + 1
			}
			return lo + (hi-lo)*(target-cum)/c
		}
		cum += c
	}
	return 0
}

// bucketMax 近似最大值：最右非空桶上界（+Inf 桶有量时取最后边界*1.2）
func bucketMax(bounds, buckets []float64) float64 {
	for i := len(buckets) - 1; i >= 0; i-- {
		if buckets[i] > 0 {
			if i < len(bounds) {
				return bounds[i]
			}
			if len(bounds) > 0 {
				return bounds[len(bounds)-1] * 1.2
			}
		}
	}
	return 0
}

type OtlpPoint struct {
	Minute int64   `json:"minute"`
	Val    float64 `json:"val"`
	Cnt    float64 `json:"cnt"`
}

func OtlpSeries(db *monitorDB, svc, environment, instance, name string, start, end int64) []OtlpPoint {
	rows, err := db.Query(`SELECT minute, SUM(val), SUM(cnt) FROM otlp_minute
		WHERE service=? AND (?='' OR environment=?) AND (?='' OR instance=?) AND name=? AND minute BETWEEN ? AND ?
		GROUP BY minute ORDER BY minute`, svc, environment, environment, instance, instance, name, start, end)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []OtlpPoint
	for rows.Next() {
		var p OtlpPoint
		if rows.Scan(&p.Minute, &p.Val, &p.Cnt) == nil {
			out = append(out, p)
		}
	}
	return out
}
