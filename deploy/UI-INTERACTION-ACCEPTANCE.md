# 八项 UI 与交互改动验收

## 工作台研发操作补验（2026-10-10）

- 查询与变更提交保持同级，侧栏只保留公共中心入口；审批设置放进“设置”。实际目标只展示连接名、库/DB 和 Key 前缀，不返回凭据，也不在打开页面时查询业务数据。
- 查询页签分别保留环境、内容和结果，最多 10 个；Ctrl/⌘ + Enter 执行选中内容或全文。SQL 高亮、格式化、查表、结构查看及结构字段插入可用。近期查询与对照结果仅在当前页面保留，不承诺刷新恢复。
- 交互查询支持单表 `SELECT *`、别名、WHERE、排序、分组、COUNT/MIN/MAX/SUM/AVG、LIMIT，以及 `SHOW TABLES`、`DESCRIBE 表名`。默认 200、最多 1000 行，30 秒超时，结果最多 2 MiB；不支持 JOIN、子查询、任意函数、序列、锁与多语句。数据同步仍使用独立的严格语法。
- Redis 使用带准确资源前缀、游标与 COUNT 1–100 的 SCAN；结果可点选查看类型/TTL、载入下一游标、生成准确 Key 的清理或过期草稿。Hash/List/Set/Sorted Set 支持有限范围读取，禁用 KEYS、全量集合返回；在 RESP 响应头阶段限制大小，GET 最多显示 64 KiB。所有辅助操作只载入命令或草稿，不自动执行。
- 查询结果支持复制、详情、JSON 格式显示及基于非空唯一键的两次结果数量对照；仅对比已经返回的范围，不等同于完整数据同步，不自动生成写操作。
- “以此新建变更”保留核对查询，不把 SELECT/只读命令复制到写编辑器。预检保存草稿但不标记就绪；编辑会使旧预检失效。实际提交才标记就绪，审核与执行仍分开。当前会话中提交的执行单可返回原核对查询，不自动重查。
- 记录可按我的操作、待我审核和关键词筛选；Redis 执行单显示逐条命令台账（成功/尚未开始/结果不确定），失败不自动重试。仅展示服务端最近返回的记录，不声称完整历史分页。
- 后端用例：`TestWorkbenchSQLIndependentGrammar`、`TestWorkbenchRedisReadBoundsAndScope`、`TestWorkbenchBoundedRedisScanProtocol`、`TestWorkbenchDraftPreviewNeverCreatesExecutionPermit`、`TestWorkbenchTargetAndLedgerScope`。浏览器用例 `test-workbench-ui.mjs` 覆盖页签和结果保留、选中执行、格式化字面量、结构查看、Key 点选/游标、核对查询、预检可编辑及失效、审批、无自动执行、公共中心连续性和响应式布局。
- 后端仅使用临时数据库与回环 Redis 协议测试服务；浏览器业务请求全部模拟。真实 MySQL SQL 执行集成需要可丢弃的本地测试库，本次不访问用户业务数据。

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

## 变更与版本表格补验（2026-10-10）

- 两类列表改为摘要表格；长 SQL、Nacos 配置值、Redis 命令和说明仅在详情展示。操作列保留查看/编辑，预检、纳入版本、状态调整和删除草稿收进“更多”；冻结版本保留发起交付入口。
- 服务端分页与创建人、应用、状态、类型及关联关系筛选。`TestDeliveryRecordPaginationAndMetadata` 使用两类各超过 500 条记录验证总数、最后一页、组合条件、参数安全、冻结和同步标志及正文不混入摘要。`TestDeliveryRecordListRoutes` 验证参数错误和详情/人员路由。
- `test-delivery-records-table-ui.mjs` 使用 621 条模拟记录验证分页、筛选、创建人维度、版本范围统计、详情与页签返回、表格滚动恢复、一次批量纳入版本和明暗主题/1440、900、700、390 宽度。所有写请求被拦截，只允许模拟的草稿范围 PUT，不执行用户变更。
- 批量操作仅选择当前页；筛选、翻页和刷新清空选择。草稿版本支持远程搜索和加载更多，避免 500 条历史截断。版本规划状态与各环境交付结果分开展示。
- 复测命令：`node scripts/test-delivery-records-table-ui.mjs`，配合原有工作项、批量编辑、跨插件连续性和公共页头回归。

## 页头一致性补验（2026-10-10）

此前视觉用例主要覆盖边距、侧栏、主题与溢出，不能证明各页面标题和页签同排；这一缺口已由公共组件与专用布局断言补齐。

- 构建、编排、变更、应用详情和插件中心使用同一组件；应用代码来源说明转入问号，长名称截断后仍有完整名称提示，环境状态和构建/部署动作保留。
- 数据库 SQL 查询和提交 SQL、Redis 查询和提交命令变更仍是同级入口；没有改变审批策略、执行行为和草稿保活方式。
- 日志订阅、监控报警、规则、机器、JVM 和接口页复用公共页头；接口页筛选条件较多时允许自然换行，不把筛选误作页签。
- 日志搜索/实时日志、Trace 查询和监控总览原本直接展示操作工具栏或数据，不为形式一致额外增加标题占用。资源中心仍保留自身分类导航，避免挤入页头产生拥堵。
- 应用详情验收使用只读模拟应用和环境，覆盖长名称和运行环境徽标，不依赖本地恰好存在应用，也不会创建真实应用或执行部署。

## 明暗主题实时同步补验（2026-10-10）

- 原因：壳已发送 `theme.sync`，但 Vue 插件的公共主题模块只读取启动偏好，没有监听后续消息；MCP 初始偏好也与壳不一致。
- 公共主题模块实时应用来自同源父窗口的布尔主题消息，统一原生控件的 `color-scheme`；独立页面通过 `storage` 事件同步偏好。壳的切换图标和标签使用响应式主题状态，MCP 采用相同规则。
- `test-theme-sync.mjs` 验证可信消息、错误来源/类型拒绝、重复初始化不重复监听、跨窗口同步以及存储不可用时的降级。
- `test-theme-sync-ui.mjs` 实际点击壳的主题按钮，覆盖部署、数据库、Redis、日志、监控、MCP 的当前和后台工作区、新开/重新加载文档、独立页面及第二个壳窗口；检查原生控件和 Element Plus 色板同步、页面不重新加载或导航。禁止业务写请求，不修改用户数据库和审批配置。
- 复测：`node scripts/test-theme-sync.mjs`、`node scripts/test-theme-sync-ui.mjs`。浏览器测试使用独立上下文，不改变用户浏览器中的主题偏好。

## 同步人员关联已有账号补验（2026-10-10）

- 成员管理 → 从禅道同步 → 读取用户，每行可搜索并选择未关联的已有平台账号；选择后自动勾选来源人员。默认仍按原规则新增或跳过，不因账号同名自动合并。
- 首次手动关联只保存来源用户编号与平台用户名的映射，不修改本地姓名、密码、角色、启用状态；后续常规同步按既有规则仅更新姓名。来源账号改名仍由稳定编号关联，不重复创建平台账号。这不是单点登录。
- 每个平台账号仅关联一个来源身份；已有关联在同步页显示且不可改绑。服务端拦截重复目标、未知/未选来源编号、失效目标、跨来源占用和来源变更；确认时重新读取来源，不信任浏览器提交的姓名、角色、密码。整批事务保存，冲突不部分写入。
- `TestDirectoryManualAssociationIsAtomicAndPreservesAccounts` 覆盖预览无写入、幂等关联、管理员/停用账号资料保留、改名和备份恢复；网关用例验证管理员授权、确认重新读取和伪造字段拒绝。MySQL 专项集成测试需配置一次性测试库，本次未提供测试库。
- `node scripts/test-user-sync-ui.mjs` 使用临时服务和模拟用户验证新增、设置密码、关联、自动选择、筛选、已绑定只读、关闭取消及三种宽度，无真实人员同步或外部服务写入。

## 工作台紧凑布局补验（2026-10-10）

- DBMS/Redis 的查询、变更提交、执行记录移入左侧功能导航，沿用悬停展开和键盘聚焦展开；DBMS 保留独立数据对比入口。目标环境与审批状态放在页头，不再额外占一行。
- 去掉重复查询标题；查询页签与执行按钮同排。应用和逻辑资源的标签与下拉框同行，窄屏自动换行，不强行挤在一行。
- 不再显示全页常驻登录警告、请求失败或审批策略读取失败横幅，改成可关闭、8 秒后消失的通知。审批策略未加载时仍禁止查询；受保护环境规则、预检阻断项和执行结果仍保留在对应业务区域，不因关闭提醒绕过安全校验。
- 按用户要求移除刷新按钮；进入执行记录自动重读记录和审批状态，不执行查询或变更、不清空查询内容或变更草稿。
- `test-workbench-ui.mjs` 补充侧栏位置、页头环境、去重标题、同行选择框、提示关闭与刷新按钮移除断言，验证进入记录只读取元数据；沿用完整查询/变更/审批、公共中心和草稿连续性回归。`test-page-header-ui.mjs` 验证明暗模式和 1440/900/700/390 宽度；浏览器业务请求使用模拟响应，不写入真实数据库或 Redis。

## 时间范围快捷按钮主题补验（2026-10-12）

- 壳中“最近15分钟（动态）”的选中背景原为硬编码深色，文字却跟随主题，导致浅色下深底深字。改用 `--brand-soft` 和 `--brand-ink`，保持与工作区页签相同的主题选中色。
- 补充 `aria-pressed` 表达动态窗口选中状态；键盘聚焦边框向内绘制，不被组合控件的裁剪边界遮住。未改时间窗口、快捷项、上下文同步或查询逻辑。
- `test-time-range-theme-ui.mjs` 使用真实壳与日期组件，检查日志/监控、选中/未选中、悬停/键盘聚焦、连续明暗切换和打开插件菜单时的文字对比度（至少 4.5:1），验证中文日期弹层和四种宽度。测试独立浏览器上下文、禁止业务写入，不更改用户浏览器偏好或数据库。
- 配合 `test-theme-sync-ui.mjs` 验证插件实时主题同步；`node --test web/src/shared/timeRange.test.js` 验证动态时间推进与固定窗口语义未变。

## Agent 与资源检测等待反馈补验（2026-10-12）

- Agent 检测、SSH 测试、采集配置读取、列表安装/升级以及资源连接测试显示真实请求期间的转圈、阶段文字和完成/失败提示。同一主机的相关操作互斥，不影响其他主机检测；连接测试也拦截重复点击。
- 采集配置立即打开读取中窗口；关闭读取窗口或切换主机后，旧响应不覆盖当前配置。保存、安装、下发分阶段提示，整条链路结束才解除 loading。提交期间禁止编辑或关闭该配置窗口，避免改写提交目标；读取期间仍可关闭。
- 配置窗口操作区固定在可见底部；失败恢复按钮并保留采集内容。下发成功后的原始配置重新转成编辑器结构，避免日志路径编辑值丢失，支持后续继续修改。
- `test-agent-loading-ui.mjs` 拦截所有业务请求，使用可控延迟与失败响应覆盖加载状态、禁止重复、主机隔离、读请求过期、保存/安装/下发阶段、失败重试、输入与窗口保护、路径保留和四种宽度；没有真实 SSH、安装或配置下发。配合采集合并规则单测、工作台与公共中心回归，保持其他弹窗默认关闭/取消行为。
