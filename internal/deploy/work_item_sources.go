package deploy

import (
	"context"
	"fmt"
)

// Source capabilities drive the platform UI; vendor-specific authentication and
// pagination remain inside adapters. Only implemented capabilities are exposed.
type WorkItemSourceCapability struct {
	ConnectionKind  string                 `json:"connection_kind"`
	Kinds           []string               `json:"kinds"`
	DefaultStatus   string                 `json:"default_status"`
	StatusConfigKey string                 `json:"status_config_key"`
	Statuses        []WorkItemSourceStatus `json:"statuses"`
}

type WorkItemSourceStatus struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type workItemSourceAdapter struct {
	Capability WorkItemSourceCapability
	Sync       func(*Store, context.Context, string, string, string, string) (IssueSyncState, error)
}

var workItemSources = map[string]workItemSourceAdapter{
	"zentao": {
		Capability: WorkItemSourceCapability{
			ConnectionKind: "zentao", Kinds: []string{"bug"}, DefaultStatus: "closed", StatusConfigKey: "bug_status",
			Statuses: []WorkItemSourceStatus{{"closed", "已关闭"}, {"active", "激活（未解决）"}, {"resolved", "已解决（未关闭）"}, {"unclosed", "未关闭（激活 + 已解决）"}, {"all", "全部状态"}},
		},
		Sync: func(s *Store, ctx context.Context, name, baseline, actor, status string) (IssueSyncState, error) {
			return s.syncZentao(ctx, name, baseline, actor, status)
		},
	},
}

func workItemSourceCapabilities() []WorkItemSourceCapability {
	out := make([]WorkItemSourceCapability, 0, len(workItemSources))
	for _, adapter := range workItemSources {
		out = append(out, adapter.Capability)
	}
	return out
}

func (s *Store) syncWorkItems(ctx context.Context, name, baseline, actor, status string) (IssueSyncState, error) {
	connection := s.GetConnection(name)
	if connection == nil {
		return IssueSyncState{}, fmt.Errorf("来源连接不存在")
	}
	adapter, ok := workItemSources[connection.Kind]
	if !ok {
		return IssueSyncState{}, fmt.Errorf("该连接尚未提供工作项同步能力")
	}
	return adapter.Sync(s, ctx, name, baseline, actor, status)
}
