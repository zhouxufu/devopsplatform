package types

// Event 一条日志事件（agent → server 的传输单元）
type Event struct {
	ID            string `json:"id,omitempty"` // agent 生成的稳定 ID，服务端据此幂等去重
	Ts            int64  `json:"ts"`           // 毫秒时间戳（agent 侧）
	ApplicationID string `json:"application_id,omitempty"`
	Service       string `json:"service"`
	Environment   string `json:"environment,omitempty"`
	Host          string `json:"host,omitempty"`
	Path          string `json:"path,omitempty"`
	Level         string `json:"level,omitempty"`
	TraceID       string `json:"trace_id,omitempty"`
	Message       string `json:"message"`
}

// IngestRequest agent 批量上报请求体（gzip 压缩传输）
type IngestRequest struct {
	AgentID      string   `json:"agent_id"`
	AgentName    string   `json:"agent_name,omitempty"`
	AgentAliases []string `json:"agent_aliases,omitempty"`
	BatchID      string   `json:"batch_id,omitempty"`
	Dropped      int      `json:"dropped"` // 上一周期明确丢弃数（正常背压不会增加）
	QueueDepth   int      `json:"queue_depth,omitempty"`
	SpoolBytes   int64    `json:"spool_bytes,omitempty"`
	Events       []Event  `json:"events"`
}

// IngestResponse 只有收到匹配 batch_id 的完整 ACK，agent 才删除本地 WAL 记录。
type IngestResponse struct {
	Quarantined  int    `json:"quarantined,omitempty"` // 已可靠隔离的坏记录，原始内容保留供修复
	BatchID      string `json:"batch_id,omitempty"`
	Accepted     int    `json:"accepted"`     // 本次新写入数量（重复事件不计）
	Acknowledged int    `json:"acknowledged"` // 已可靠处理数量（包含幂等重复）
}

// Hit 搜索命中项
type Hit struct {
	EventID       string `json:"event_id,omitempty"`
	Ts            int64  `json:"ts"`
	ApplicationID string `json:"application_id,omitempty"`
	Service       string `json:"service"`
	Environment   string `json:"environment,omitempty"`
	Host          string `json:"host,omitempty"`
	Path          string `json:"path,omitempty"`
	Level         string `json:"level,omitempty"`
	TraceID       string `json:"trace_id,omitempty"`
	Message       string `json:"message"`
	RowID         int64  `json:"rowid,omitempty"` // 游标分页/加载更早使用
}

// SearchResponse 游标分页响应
type SearchResponse struct {
	Hits       []Hit  `json:"hits"`
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}
