# Dubbo 接口文档与测试

接口文档首页默认 HTTP，按协议切换 Dubbo；URL 的 protocol=dubbo 可直达，普通首页不记成全局 Dubbo 默认。两个协议分别保留最新快照、历史、比较及维护人；不能以 Dubbo 上传覆盖 HTTP 基线。

## 文档契约

使用原生 JSON 文档，非伪造的 OpenAPI 路径：

```json
{
  "dubbo": "1.0",
  "info": {"title": "房源服务", "version": "1.0"},
  "methods": [{
    "interface": "com.example.HouseService",
    "method": "getHouse",
    "group": "house",
    "version": "1.0.0",
    "summary": "查询房源",
    "parameters": [{"name": "id", "java_type": "java.lang.Long", "example": 123}],
    "returns": {"java_type": "com.example.House", "schema": {"$ref": "#/components/schemas/House"}}
  }],
  "components": {"schemas": {"House": {"type": "object", "properties": {"id": {"type": "integer"}}}}}
}
```

方法身份包含接口、Group、Version、方法及有序 Java 类型，因此重载互不覆盖。页面支持导入、详情、JSON 编辑并保存新修订；MCP 复用原文档上报工具，查询可指定 protocol=dubbo。不自动扫描源码，也不将 Nacos 服务列表冒充完整文档；需提供参数/返回字段契约。

## 调试与用例

从方法详情点击调试，带入应用、环境、文档快照、方法签名及示例。按 Java 类型顺序填写 JSON 数组参数；Long 示例和响应使用无损 JSON。Attachments 可引用环境变量，不能覆盖路由、序列化、重试等调用策略。

保存为用例后，可在画布混排 HTTP、Dubbo、条件、并行和汇合节点。JSON 响应提取、数据驱动、跨步骤变量及原测试集复用。每个节点可指定入口标识；有多个匹配入口时必须明确选择。开始执行前预检所有节点的目标，缺少 Dubbo 入口不会先执行前面的 HTTP 请求。

Dubbo 断言支持 RPC 成功、异常类型、JSON、正文、耗时和 schema；不提供虚构 HTTP 状态码、响应头或 SSE 事件。报告区分 RPC 成功、业务异常、未发送和结果未知，并保留原方法与冻结目标。保留原测试权限、受保护环境确认、停止及审计规则；停止不能撤销已进入业务服务的操作。

安装及兼容范围见 [执行器说明](../tools/dubbo-executor/README.md)。每次调用启动 JVM，适用于功能验证而非高并发压测。当前不支持流式 RPC、自定义序列化、业务 JAR 上传或自动生成源码文档。

内置 Skill：devops-api-docs 1.2.0、devops-api-testing 1.3.0 已加入 Dubbo 契约；个人目录不会随平台源码更新自动安装。
