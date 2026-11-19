# Agent 交付能力补齐（2026-11-16）

本轮实现：环境流程诊断、代码/构建策略读取、已有本人草稿维护、令牌编辑删除、已绑定资源的数据库结构与配置键只读检查。没有修改需求 #24 或实际应用的配置，没有执行业务合并、构建、迁移、配置发布或部署。ES、XXL、Kafka/CDC 和 Git 文件/diff 不在本轮实现范围。

## 工具与权限

| 工具 | 权限 | 行为 |
|---|---|---|
| `diagnose_delivery_binding` | `delivery:read` | 返回命中的环境、策略 revision、正常/Hotfix 流程 ID、模板摘要、准备/发布节点数、缺失阶段 |
| `get_application_code_policy` | `changes:read` | 明确应用+环境，读取固定公共/独立分支策略、节点覆盖优先级、构建方案、参数名及制品规则；不读取 Git 或执行构建 |
| `get_change_maintenance` | `changes:read` | 本人已验证 Key、全部应用授权，读取原记录与 digest |
| `maintain_change` | `changes:write` | 同一原 ID 的 edit/archive 冻结预览，明确确认后调用 `confirm_platform_action`；不会新增替代记录 |
| `check_database_schema` | `resources:read` | 管理员本人已验证 Key，在授权的具体应用/环境/别名内核对 MySQL 元数据 |
| `check_configuration_keys` | `resources:read` | 同上，按已绑定 Nacos Data ID/Group 检查显式白名单键的存在性/类型 |
| `trigger_build`（扩展） | `builds:trigger` | 可选 `environment`，映射既有 `build_environment`，冻结策略摘要并校验目标分支；不自动合并源分支或发布 |

`confirm_delivery_scope` 继续复用已实现的本人范围确认与 CAS。生产/受保护环境、他人范围、审核及验证节点仍不能由此工具代签。

## 工具发现

`get_agent_capabilities.tools` 与 MCP `tools/list` 使用同一个按令牌过滤的注册表；新增 `tool_manifest_digest` 可用于对比清单。未授权 `delivery:prepare` 时，不再宣称当前 Key 可做范围确认。

本轮对真实连接的只读核对证明：服务端清单已有 `confirm_delivery_scope`，当前客户端 `ALL_TOOLS` 没有；证据在 `artifacts/mcp-capability-completion-20261116/live-discovery.json`。这证明连接发现不一致，不能由此判定后端缺实现，也未证明客户端缓存的具体原因。发布/修改令牌权限后需重新连接，再核对工具列表；服务端无法替客户端刷新已缓存的 schema。

## 策略与构建

代码策略的优先级与既有准备流程一致：代码节点应用覆盖 > 应用环境策略 > 代码节点通用策略。返回 `code_nodes`；多个代码节点或分支占位符必须结合明确交付范围解析。应用默认 branch 与需求变更 reference、环境构建目标分别表达。

查询不会改写任何分支配置。`integration` 表示固定公共目标，`isolated` 表示独立交付分支；保留真实模式，不把需求源当作目标。指定环境直接构建时，分支必须符合已配置目标；依赖工作项/版本占位符或多个代码节点时，应走统一交付代码准备。

代码准备原有冻结证据为 `repositories[].sources[].commit`、`target_commit`、`result_commit`。合并尚未发生时不能声称已有合并后的 SHA；合并完成后的 `result_commit` 才是结果证据。

## 草稿维护

先读取 `get_change_maintenance(service, change_id)`，再提交原 digest 和明确修改字段：

```json
{
  "service": "orders",
  "change_id": 8,
  "digest": "sha256:原摘要",
  "action": "edit",
  "reason": "修正原草稿的分支和描述",
  "patch": {"reference": "feature/actual-source", "summary": "当前变更说明"}
}
```

`patch` 未提供的字段保留，避免把读取结果中的脱敏值重新写回 SQL/配置。SQL/Nacos/Redis 载荷只有明确提供时才替换；不接受注入同步冻结计划或恢复身份。

可用 `work_item_ids` 调整旧式关联（空数组表示移除），或通过 `patch.delivery_ownership` 调整 self/follow/draft。已有来源归属不能覆盖关联或更换原始来源；首次设置归属时显式给出已关联的 `origin_work_item_id`。内容、关联、归属及审计在同一事务内完成，失败全部回滚。

只处理本人草稿，保留状态/摘要冲突、版本引用、活动交付单及 SQL/Nacos/Redis 台账保护。归档使用 `action: "archive"` 与原因，保留原记录、来源关联和审计；不再加入新的交付内容、候选范围或版本，不可再标记就绪。独立变更列表可筛选“已归档”。

## 受限只读检查

数据库示例：

```json
{
  "service": "orders", "environment": "test", "alias": "db",
  "tables": [{"name": "orders", "columns": [
    {"name": "id", "type": "bigint", "nullable": false},
    {"name": "total", "generated": true}
  ], "indexes": [{"name": "PRIMARY", "unique": true, "columns": ["id"]}]}]
}
```

最多 20 张精确表、每表 100 项字段/索引期望，实际字段与索引元数据各最多 500 项；单表返回最多 128 KiB。查询固定为当前绑定库的 `information_schema` 元数据，并核对 `SELECT DATABASE()`；不接受 SQL、连接地址、库名、跨库/通配符或业务数据查询。

可比较 `nullable`、`default`（只返回匹配结果）、`default_is_null`、`generated`、索引顺序及唯一性。实际默认值、生成表达式和注释不返回；默认值为空的元数据不能区分“显式 DEFAULT NULL”与“未声明默认值”。复杂/函数/前缀索引明确标记，不把它们当作普通完整列索引。缺失只表示当前凭据元数据不可见或对象不存在，不代表获得了全库权限。

配置示例：

```json
{
  "service": "orders", "environment": "test", "alias": "app_config",
  "format": "yaml", "keys": ["feature.enabled", "spring.datasource.url"]
}
```

每次显式白名单 1–50 个精确键，支持 YAML、JSON、简单 properties；只返回请求键的存在性和类型，不枚举其他键，不返回任何值。重复键、层级/点键歧义、锚点/别名、多文档及不支持的 properties 转义/续行会返回“未判定”，不误报键缺失。

两类检查均复用自有/共享/目录引用的有效绑定，不回退到其他环境或任意全局连接；10 秒超时，读取后复核目标/凭据摘要。检查结果是观察证据，不是执行授权或迁移已完成的证明。未接入资源时明确失败，不把空清单等同“无依赖”。

## 令牌维护

页面新增编辑和删除。`PUT /api/tokens/{id}` 携带 revision，可调整名称、权限、应用/环境范围和有效期；0 天保留原到期时间。原 Key 和 owner 保持不变，不能恢复已撤销/过期 Key。独立资源/文档/诊断授权仍由管理员授予。

`DELETE /api/tokens/{id}/record` 携带 revision，立即撤销并从列表隐藏，保留审计和操作身份。既有 DELETE 撤销路径继续有效。编辑/删除使该 Key 的未确认预览作废；有执行中操作时阻止修改/删除，须先核对原操作。页面失败保留输入，不自动重试。
