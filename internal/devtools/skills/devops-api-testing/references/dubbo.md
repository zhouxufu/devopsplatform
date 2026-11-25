# Dubbo 调试与用例

从 `get_api_test_context` 选 protocol=dubbo 的目标。入口由管理员配置为直连或应用环境 Nacos 别名；Group/Version 指服务身份，与 Nacos 注册分组不同。平台在业务调用前发现并固定提供者，报告保留实际调用地址。缺少 Java 执行器／入口／提供者时先修复配置，不回退到任意 IP 或 HTTP 接口。

请求示例（置于 `execute_api_test.request` 或用例步骤的 request）：

```json
{"mode":"dubbo","rpc":{"interface":"com.example.HouseService","method":"getHouse","group":"house","version":"1.0","parameter_types":["java.lang.Long"]},"body_type":"json","body":"[123]","headers":{"token":"{{var.API_TOKEN}}"},"timeout_seconds":30,"assertions":[{"type":"rpc_success","expected":"true"},{"type":"json","path":"/id","expected":"123"}]}
```

body 是有序 JSON 数组字符串，顺序／数量与 Java 类型一致，无参使用 `[]`。headers 在 Dubbo 中表示 Attachments，不是 HTTP 请求头；不能用它覆盖 group、version、timeout、retries 等调用策略。Dubbo 不使用 HTTP 登录配置。凭据引用受保护环境变量，不写入文档、正文示例或聊天。

节点指定 `kind: "dubbo"`，可选 `target_key: "endpoint:已配置入口标识"`。混合用例没有唯一匹配入口时，明确每步目标；不猜入口。返回断言使用 rpc_success／exception／json／contains／regex／json_regex／json_range／duration／schema，不能写 HTTP status/header/SSE 断言。异常验证可使用 `rpc_success=false` 与精确 `exception`，成功 JSON 提取可供后续 HTTP 或 Dubbo 节点使用。传输超时、进程中断和结果未知不自动重试，可能发生的业务写入需核对原请求。
