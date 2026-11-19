# 文档上报参数（上报或候选预览时读取）

以当前 MCP 工具 schema 为准，示例中的 orders/test 是占位值，必须使用真实平台记录。

```json
{
  "service": "orders",
  "environment": "test",
  "document": {
    "openapi": "3.0.3",
    "info": {"title": "订单接口", "version": "1.0", "contact": {"name": "示例维护人"}},
    "tags": [{"name": "订单管理"}],
    "paths": {
      "/orders": {
        "get": {
          "tags": ["订单管理"],
          "summary": "查询订单列表",
          "description": "查询当前用户可见的订单列表。",
          "responses": {"200": {"description": "成功"}}
        }
      }
    }
  }
}
```

调用 `report_api_document` 只生成预览，读取 preview 中的 info、operation_count、baseline、digest、summary、changes、truncated、duplicate、existing_document_id、default_maintainer、maintainers。maintainers 按接口 ID 给出最终维护人；已有接口除显式 x-maintainer 修改外沿用上一同环境快照的归属，首次/新增接口依次用显式 x-maintainer、上报端从源码确认并写入 x-maintainer 的作者、服务端验证的 Key 用户；不使用全局 info.contact.name 覆盖 Key 用户。普通非 MCP 导入没有已验证 Key 时才保留文档联系人作为兼容默认，不推断当前登录用户。代码作者由 Skill 按主文档规则核对并作为接口声明发送，服务端不扫描源码，来源会显示为 explicit；预览说明中另附作者依据。maintainer_sources 标明 explicit / inherited / document / mcp-key / unspecified，maintainer_changes 显示旧值与新值。maintainer_digest 由确认请求冻结并在提交时校验，不能挪用到另一份预览。重复预览返回原快照的默认维护人，不改历史；不能用新 Key 替换。客户端须完整发送用户指定的文档，不用裁剪版代替，也不要将查询工具返回的脱敏文档原样覆盖为新来源。

先完成 SKILL.md 中的内容质量检查，发送最终整理后的候选副本；获得 confirmation_id 后不要继续改正文并沿用旧确认号。需要修改时重新预览。上例的业务名称与规则仅为示例，不作为真实接口的默认内容。

确认有效期为 10 分钟。展示具体预览后取得用户明确同意，再调用：

```json
{"confirmation_id": "预览返回的实际ID", "confirm": true, "reason": "用户确认的此次上报目的"}
```

`get_platform_action` 核对状态：success 是文档保存成功，executing/uncertain 不能重试写入；stale 或过期需要重新预览。重复成功确认只返回原回执。

`list_api_documents(service, environment)` 列出最近 100 个版本，不代表全部历史。`get_api_document(service, environment, document_id)` 读取完整定义；`compare_api_documents` 增加 document_id，可选更早的 base_id。接口文档中的差异不等于运行实例已发布或测试已通过。

## 可选需求关联

`change_context` 是与 `document` 平级的可选 MCP 参数。`work_item: {"id": 实际平台引用编号}` 设置本次新增字段默认需求；`fields: {"完整字段 JSON Pointer": {"id": 实际平台引用编号}}` 覆盖具体新增字段的需求。编号必须来自已核对的平台记录，不是猜测的来源编号。字段指针使用预览 `field_additions[].pointer`，例如路径中的 `/` 编码为 `~1`、`~` 编码为 `~0`。一次最多 500 个字段覆盖。

预览同时返回 `change_context`、`field_additions`（含 work_item 的真实标题/来源）及可能的 `field_additions_error`。确认前向用户展示默认需求、各字段例外和未关联项。改变正文、关联需求或字段映射均需重新预览及确认。原始 document 的完整性、维护人优先级和冻结基线保持原规则。
