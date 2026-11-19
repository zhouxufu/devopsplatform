# 测试集契约

测试集属于明确应用，与环境复用同一份定义；执行仍需具体环境和准确入口。最多20个用例、40条跨用例变量绑定，展开参数组最多20个运行单元、100步；保留现有用例图/请求时限、并发和响应限制。

## 保存定义

先发现真实用例编号，读取完整定义。以下为 `validate_api_test_suite` 入参形状；101/102为示例编号，不可直接调用。`case_ids` 顺序决定执行顺序。新建 id/revision 为0；修改使用读取的原值，保留未涉及的成员和绑定。

```json
{
  "service": "orders",
  "environment": "test",
  "suite": {
    "id": 0,
    "revision": 0,
    "name": "创建后查询订单",
    "visibility": "private",
    "case_ids": [101, 102],
    "bindings": [
      {"target_case_id": 102, "name": "order_id", "source_case_id": 101, "source_name": "created_id"}
    ]
  }
}
```

101须定义输出提取 `created_id`；102请求引用 `{{case.order_id}}`。来源用例必须排在目标之前，来源有多组数据时通过 `source_row` 指定准确数据组名。图的来源输出需在所有分支保证存在。映射只在运行内存中传值，不将响应值改写为定义默认值。团队测试集的每个成员也须为团队用例。

校验返回规范化 `after`、完整 `cases` 和 `case_revisions`。将 `after` 作为 suite 交给 `save_api_test_suite`；确认时平台重新检查测试集及每个成员修订/权限。不得在冲突后只换 revision 覆盖。

## 执行

`get_api_test_context(service,environment,suite_id)` 返回当前 suite、cases、case_revisions、targets、variables_digest。以此构造 `execute_api_test`：

```json
{
  "service": "orders",
  "environment": "test",
  "suite_id": 9,
  "suite_revision": 2,
  "case_revisions": {"101": 3, "102": 1},
  "target_key": "从上下文选择",
  "target_fingerprint": "从上下文原样使用",
  "reason": "验证创建后查询的业务契约"
}
```

不同时提交case_id或request。预览冻结目标、认证/变量摘要及全部用例修订；明确确认后仍需查询原run_id到终态。报告按数据组/用例和节点核对，任一失败停止调度后继，未执行和分支跳过分别报告。编辑定义不会改写历史运行快照。

列表使用 `items/has_more/next_before_id`，返回空页仍检查 has_more。报告的停止/结果不确定处理见主 Skill；不提供跨应用分布式回滚或自动清理保证。
