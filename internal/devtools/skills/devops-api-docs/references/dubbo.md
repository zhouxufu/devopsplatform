# 原生 Dubbo 文档

`report_api_document` 的 `document` 可使用以下格式；service/environment 从已确认的应用和环境填写。整个文档快照只包含一种协议；保存 Dubbo 不覆盖 HTTP 快照。

```json
{"dubbo":"1.0","info":{"title":"房源服务","version":"1"},"methods":[{"interface":"com.example.HouseService","method":"getHouse","group":"house","version":"1.0","summary":"查询房源","tags":["房源查询"],"parameters":[{"name":"id","java_type":"java.lang.Long","description":"房源编号","schema":{"type":"integer"},"example":123}],"returns":{"java_type":"com.example.House","schema":{"type":"object","properties":{"id":{"type":"integer"}}}},"exceptions":["java.lang.IllegalArgumentException"]}]}
```

方法身份由 interface、group、version、method、有序 Java 参数类型组成。重载不可合并；泛型填写擦除后的 Java 类型，泛型内容在 schema 中表达。数组使用 `java.lang.String[]` 等 Java 类型。参数／返回字段可引用 `#/components/schemas/...`。示例 Long 不经浮点解析；保存原始数值精度。

维护人沿用同一身份的冻结归属。改身份会形成删除／新增，预览必须展示。`patch_api_document` 使用 `/methods/0/...` 等原始 JSON Pointer，先读精确修订，不能依据另一次检索的数组位置盲改。发布文档不会发现或调用业务提供者。
