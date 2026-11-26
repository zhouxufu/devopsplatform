# 图与布局契约

仅在编写或修改对应定义时读取。运行前以 `get_agent_capabilities` 和工具输入 Schema 为准；下面的应用名、节点与接口路径是示例，先替换为用户任务中核对过的真实应用/接口。生成示例不会授权执行接口。

## 节点、变量和限制

- 无 `graph` 的旧用例按步骤数组顺序执行；图用例用 `definition_version:2` 和 `graph.entry/concurrency/edges` 表达执行依赖，`steps` 数组仅保存节点定义。修改时保留节点ID，新增节点使用不重复的字母开头ID（字母、数字、下划线、短横线，最多64字符）。
- `kind` 为 `http`（省略亦可）、`condition`、`parallel`、`join`。HTTP/SSE共享请求节点：`request.mode` 为 `http/sse`，`request.path` 必须是相对路径，认证来自环境。响应断言和提取保存在接口节点内。
- 条件含 `variable/operator/expected`。变量为 `step.NAME/data.NAME/case.NAME`；判断为 `equals/not_equals/greater/less/exists`；`expected:{type,value}` 的type支持string/number/boolean/null，value总是字符串，布尔值写 `"true"/"false"`。大小比较使用number；字符串和数字不隐式互转，保留大整数精度。exists判断存在且非null，不代表字符串非空。
- 条件的两条边分别带字符串 `branch:"true"` 与 `branch:"false"`，不是JSON布尔值。parallel分出2–4条边；join接收至少两条输入，等待所有活动前驱完成后继续。HTTP和join最多一个后继，多输入通过join连接。
- 图须有唯一入口、唯一终点、全部节点接入且无环。当前每用例最多20节点、30边，并发1–4；控制节点也计入节点配额。单次执行的全部参数组与用例合计最多100步。
- 登录或分享请求先提取响应字段，后续请求可用 `Authorization: Bearer {{step.loginToken}}` 或 `?shareToken={{step.shareToken}}`。上游节点须在消费路径上保证产生该变量。环境登录配置仍使用 `{{var.NAME}}`。普通分页、业务字段和 `User-Agent` 可直接填写。
- 动态 UUID 在数据组中声明，例如 `{"name":"分享流程","values":{"shareId":{"type":"uuid","value":""}}}`。请求引用 `{{data.shareId}}`。每次执行的每组每列独立生成，同一组内所有节点共享其值。
- 数值提取可指定 `{"source":"json","name":"fee","path":"/data/fee","format":"finite_number_text"}`。它校验有限数值或数值文本并输出文本，适用于后续请求要求字符串的金额字段。
- HTTP返回字段先通过 `extractions:[{name,source:"json",path:"/data/id"}]` 定义输出，再在请求中用 `{{step.NAME}}` 引用。`source:"header"` 用响应头名；SSE提取还需事件选择器。条件引用输出写 `step.NAME`，不加模板括号。
- `{{data.NAME}}` 来自 `data_rows[].values` 的类型化单元格；`{{var.NAME}}` 来自受保护环境变量；`{{case.NAME}}` 仅来自显式测试集映射，不能在单用例执行时假设已绑定。提取变量名在用例内唯一；不能读并行兄弟或只在另一条件路径产生的变量。汇合后仅可读取所有相应活动路径上可保证得到的输出。
- 节点必须满足请求断言及输出提取才能向后发布变量。请求或断言失败会停止调度后继；已经发出的并行请求逐个记录实际结果。条件不能用失败请求的未发布输出来绕过停止策略。

## 条件与并行示例

以下为 `validate_api_test_case` 的完整入参，可将校验返回的规范化case传给 `save_api_test_case`。示例通过data.run_parallel选择两路并行查询或单路查询，最后汇合；两组参数覆盖真和假路径。

```json
{
  "service": "orders",
  "environment": "test",
  "case": {
    "id": 0,
    "revision": 0,
    "definition_version": 2,
    "name": "订单查询条件与并行回归",
    "visibility": "private",
    "priority": "P2",
    "data_rows": [
      {"name": "并行查询", "values": {"run_parallel": {"type": "boolean", "value": "true"}, "order_id": {"type": "string", "value": "example-order-1"}}},
      {"name": "单路查询", "values": {"run_parallel": {"type": "boolean", "value": "false"}, "order_id": {"type": "string", "value": "example-order-1"}}}
    ],
    "graph": {
      "entry": "choose",
      "concurrency": 2,
      "edges": [
        {"from": "choose", "to": "fork", "branch": "true"},
        {"from": "choose", "to": "single", "branch": "false"},
        {"from": "fork", "to": "detail"},
        {"from": "fork", "to": "items"},
        {"from": "detail", "to": "joined"},
        {"from": "items", "to": "joined"},
        {"from": "joined", "to": "end"},
        {"from": "single", "to": "end"}
      ]
    },
    "steps": [
      {"id": "choose", "name": "选择查询方式", "kind": "condition", "condition": {"variable": "data.run_parallel", "operator": "equals", "expected": {"type": "boolean", "value": "true"}}, "request": {}},
      {"id": "fork", "name": "并行查询", "kind": "parallel", "request": {}},
      {"id": "detail", "name": "查询订单", "kind": "http", "request": {"method": "GET", "path": "/orders/{{data.order_id}}", "mode": "http", "timeout_seconds": 30, "assertions": [{"type": "status", "expected": "200"}]}},
      {"id": "items", "name": "查询订单明细", "kind": "http", "request": {"method": "GET", "path": "/orders/{{data.order_id}}/items", "mode": "http", "timeout_seconds": 30, "assertions": [{"type": "status", "expected": "200"}]}},
      {"id": "joined", "name": "等待并行查询", "kind": "join", "request": {}},
      {"id": "single", "name": "查询订单状态", "kind": "http", "request": {"method": "GET", "path": "/orders/{{data.order_id}}/status", "mode": "http", "timeout_seconds": 30, "assertions": [{"type": "status", "expected": "200"}]}},
      {"id": "end", "name": "查询完成", "kind": "join", "request": {}}
    ]
  }
}
```

示例只有HTTP状态码断言，不能据此宣称订单业务正确。根据实际契约补充JSON字段、结构或其他业务断言后再预览。已有用例改为图时先保留读取的修订与节点ID，使用平台校验返回的规范化定义；不从零重建并覆盖旧步骤。

## 布局与执行入参

`get_api_test_layout` 传应用、环境、真实case_id和明确workspace，返回 `revision/positions/zoom/pan`。`save_api_test_layout` 再提交这些字段；positions键是该用例的节点ID，坐标有界，zoom为0.4–1.6。不要在布局中夹带用例内容、参数或执行目标。内容和布局各自预览、各自确认。

下例仅示意布局结构；将case_id、workspace及布局revision替换为实际读取值，positions保留需要的其他节点位置。它不是新用例创建参数。

```json
{
  "service": "orders",
  "environment": "test",
  "case_id": 42,
  "workspace": "standalone",
  "layout": {
    "revision": 0,
    "positions": {"choose": {"x": 0, "y": 0}, "fork": {"x": 270, "y": 0}, "single": {"x": 270, "y": 175}},
    "zoom": 1,
    "pan": {"x": 20, "y": 28}
  }
}
```

执行已保存用例时，`execute_api_test` 使用实际 `service/environment/target_key/target_fingerprint/case_id/case_revision`，不附带临时request替换用例。目标及指纹来自最新 `get_api_test_context`；确认时发生漂移须重新核对。回执 result 中原run ID用于 `get_api_test_run`；报告与布局均不通过其他用户的ID绕过权限读取。
