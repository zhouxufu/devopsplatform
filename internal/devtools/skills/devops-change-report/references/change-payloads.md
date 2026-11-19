# 变更载荷（仅在整理上报参数时读取）

客户端当前 MCP 工具 schema 为准。权限：读取需 `changes:read`，登记需 `changes:write` 和本人账号令牌。核心入参是 `report_changes(service, changes, work_item_id?)`；一次 1–50 项、整个请求不超过 2 MiB。可以省略每项 service 以复用顶层应用，不接受混入其他应用、旧变更 ID、owner、执行状态或内部计划。

下面是示例结构，orders、db、config、分支和配置项都是占位示例，必须替换为用户确认及平台真实声明的值，不直接提交示例。

```json
{
  "service": "orders",
  "changes": [
    {
      "kind": "code",
      "title": "订单备注读写",
      "reference": "feature/order-note",
      "summary": "为订单新增备注的读写与校验",
      "risk_level": "low",
      "rollback_plan": "恢复原版本"
    },
    {
      "kind": "mysql",
      "title": "订单表增加备注字段",
      "sql": {
        "resource_alias": "db",
        "content": "ALTER TABLE orders ADD COLUMN note VARCHAR(64);",
        "max_affected_rows": 100
      }
    },
    {
      "kind": "nacos",
      "title": "调整订单超时",
      "nacos": {
        "resource_alias": "config",
        "data_id": "orders.yaml",
        "group": "DEFAULT_GROUP",
        "format": "yaml",
        "entries": [{"key": "timeout", "action": "set", "value": "30"}]
      }
    }
  ]
}
```

- 通用字段：kind、title；可选 summary、risk_level（low/medium/high）、rollback_plan。服务器新建状态为 draft，不在参数里指定。
- code 的 reference 是分支。未提交改动不伪造为已构建 Commit，制品和可信构建证据由构建流程产生。
- mysql 必须使用声明的资源别名，max_affected_rows 为 1–10000。现有引擎支持受限的建表、ADD COLUMN、索引及单表 DML；不支持的 SQL 明确报告，不改写成可执行 SQL或自行在数据库运行。不得伪造 data_sync 或环境执行快照。
- nacos 支持 yaml/properties/json；每项 action 是 set 或 delete，delete 可省略 value。默认 Group 必填，Data ID 可来自环境绑定；执行时环境绑定中的 Data ID/Group 优先。保留原值类型的表达方式，不凭空猜 Namespace、密码或生产地址。
- 关联工作项时顶层增加 work_item_id。默认是待确定交付归属的草稿；不要擅自指定 self/follow。独立草稿不传 delivery_ownership。
- `get_change(service, change_id)` 读取完整内容；`get_work_item_changes(work_item_id)` 读取关联内容。旧 `add_work_item_changes` 仍可批量追加，已有内容由服务端保留，不需要把旧项重交。
