# 八项 UI 与交互改动验收

日期：2026-10-10。分支：`feature/delivery-platform-v1`。验收对象：本地 `http://127.0.0.1:9800/`，壳内工作区和独立插件页面。

## 逐项结果

| 项目 | 已实现的结果 | 验收证据 |
| --- | --- | --- |
| 1. 图标 | 部署使用火箭；构建、编排、变更、应用、资源、MCP、查询、同步、历史、设置区分 | `test-console-visual.mjs` 检查实际 SVG 路径无重复、部署标签不是百分号；截图检查 |
| 2. 批量新建 | 单编辑器连续录入多类型、多应用内容；一次事务生成独立草稿；无效项全部回滚，不执行 | `TestIndependentChangeBatchAtomic`、`TestIndependentChangeBatchHTTPMixedPayloads` 覆盖代码/SQL/Nacos/Redis/人工操作、资源校验及结构化内容持久化；`test-compact-delivery-ui.mjs` 覆盖单次批量提交与关闭后恢复 |
| 3. 说明和图片 | Markdown 编辑/预览，多图选择、粘贴、拖入，图片前后切换；兼容来源 HTML 和禅道附件路径 | `TestWorkImagesBindingAndFormats` 验证上传、所有权、格式持久化和安全响应；`test-compact-delivery-ui.mjs` 验证两图上传、预览、切换和需求草稿恢复；`test-rich-description-ui.mjs` 验证富文本、安全清洗、禅道路径、不自动加载外图 |
| 4. 纵向空间 | 公共 `PageHeader` 统一构建、应用详情、编排、变更、插件中心、DBMS/Redis、公共中心与日志/监控管理页；桌面标题、页签和动作同排，窄屏自动换行；说明移至问号 | `test-page-header-ui.mjs` 检查实际页头高度不超过 48px、标题/导航/动作纵向对齐、长应用名、省略后完整提示、壳内和独立页面、公共中心返回；明暗主题及 1440/900/700/390 宽度均检查溢出；配合 `test-console-visual.mjs` 和人员筛选分页用例 |
| 5. 同步入口 | DBMS 独立“数据对比同步”；部署编辑器引用并携带需求关联；任务定义无环境，本次对比再选两侧环境；未启用 DBMS 时保留就地入口 | `TestDataSyncTaskIndependentOfEnvironments`；`test-data-sync-scope-ui.mjs` 验证同一任务跨环境复用、差异编辑、冻结范围和旧选择失效；`test-cross-workspace-continuity-ui.mjs` 验证实际壳内跳转及需求关联 |
| 6. DBMS 操作 | SQL 查询和提交 SQL 为同级显著入口；编辑器直接展示而非叠窗；同步独立；查询和变更审批分别设置 | `test-workbench-ui.mjs` 验证查询/提交/审核全页路径、无嵌套编辑弹窗、独立审批开关；`TestWorkbenchIndependentPoliciesAndReadApproval`、`TestWorkbenchChangePolicySnapshotsAndProtection` 验证服务端策略隔离及保护环境限制 |
| 7. Redis 操作 | 命令查询与命令变更显著；设置和绑定次级化；批准不自动执行，执行仍需明确确认 | `test-workbench-ui.mjs` 验证操作路径与无自动执行；`TestRedisReviewExecutionLedgerAndNoSecrets`、`TestRedisBindingApprovalAndScopeDrift`、`TestRedisSyntaxAndConnectionValidation` 验证审核、审计、范围漂移、风险命令与凭据保护 |
| 8. 连续性 | 模式切换、公共应用/资源中心、应用资源绑定关闭后保持输入；引用同步时原 iframe 保活；新增同步内容合并但不覆盖草稿 | `test-workbench-ui.mjs` 覆盖查询/变更/同步与应用/资源/绑定；`test-platform-utilities-ui.mjs` 覆盖公共中心保活；`test-cross-workspace-continuity-ui.mjs` 验证编辑器打开或关闭期间产生新增内容、原 SQL 保留、最新摘要保存、冻结记录不被改写 |

## 回归与运行核对

- `go test ./...` 通过。浏览器用例在候选产物与更新后的本地服务上验证，无浏览器错误。
- 浏览器业务写入均拦截为测试响应；后端用例使用临时数据库和协议测试服务，不向用户 MySQL、Redis、Nacos 执行命令。
- 本地壳、部署、DBMS、Redis、日志和监控的前端资源与当前构建一致。
- 更新前备份二进制和部署数据库；更新后保留原 19 次构建、6 条发布记录、39 条工作项。审批配置和既有日志保留配置未变。
- 草稿在当前工作区内保活；浏览器刷新或退出不承诺恢复，仍须保存。多人同时修改已有记录时不会自动覆盖，需核对冲突。

## 复测

先启动本地平台，安装 Node、Go 和 Playwright，设置 `PLAYWRIGHT_MODULE` 与 `CHROME_PATH`。使用运行服务产物时不要设置 `CONSOLE_UI_DIST`；候选产物模式可指向 `web/dist`。

```bash
go test ./...
node scripts/test-compact-delivery-ui.mjs
node scripts/test-cross-workspace-continuity-ui.mjs
WORKBENCH_GATEWAY=1 node scripts/test-workbench-ui.mjs
node scripts/test-data-sync-scope-ui.mjs
node scripts/test-console-visual.mjs
node scripts/test-page-header-ui.mjs
node scripts/test-work-items-table-ui.mjs
SHELL_UI_TEST_EMBEDDED=1 node scripts/test-rich-description-ui.mjs
node scripts/test-platform-utilities-ui.mjs
```

这份验收针对八项 UI/交互目标，不代表已完成代码自动合并、版本制品自动映射、通用任意 SQL、生产数据库集成或其他独立规划。

## 页头一致性补验（2026-10-10）

此前视觉用例主要覆盖边距、侧栏、主题与溢出，不能证明各页面标题和页签同排；这一缺口已由公共组件与专用布局断言补齐。

- 构建、编排、变更、应用详情和插件中心使用同一组件；应用代码来源说明转入问号，长名称截断后仍有完整名称提示，环境状态和构建/部署动作保留。
- 数据库 SQL 查询和提交 SQL、Redis 查询和提交命令变更仍是同级入口；没有改变审批策略、执行行为和草稿保活方式。
- 日志订阅、监控报警、规则、机器、JVM 和接口页复用公共页头；接口页筛选条件较多时允许自然换行，不把筛选误作页签。
- 日志搜索/实时日志、Trace 查询和监控总览原本直接展示操作工具栏或数据，不为形式一致额外增加标题占用。资源中心仍保留自身分类导航，避免挤入页头产生拥堵。
- 应用详情验收使用只读模拟应用和环境，覆盖长名称和运行环境徽标，不依赖本地恰好存在应用，也不会创建真实应用或执行部署。
