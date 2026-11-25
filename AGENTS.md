# Delivery platform working agreements

## Product invariants confirmed with the user

- Confirmed application identities (2026-11-04): `estate-business` is 商户端; `estate-bus` is 总平台. Use these stable identifiers for configuration association; stale display names are not evidence for remapping historical defaults.

- Delivery workflow definitions belong to deployment orchestration. Environments bind normal/Hotfix workflows; research owns work items and tasks, not a separate daily workflow configuration. Dynamic code nodes resolve selected scope instead of fixed applications. Do not support legacy standalone preparation templates or historical preparations; the user explicitly removed that compatibility. Keep new unified snapshots immutable.
- Bound workflow preparation cannot be bypassed by switching to standard delivery. New combined workflows preview frozen code sources before a separate explicit merge/build confirmation. Keep preparation and execution on the same delivery page; internal engines may remain separate.

- 开发／测试非受保护环境允许本人绑定 Key 通过 `confirm_delivery_scope` 预览冻结范围，再经聊天明确确认写入本人范围确认。只接受当前指定人员、原修订／摘要／节点及本人未确认工作项，管理员不得代签；复用原范围校验与 CAS、会签／或签及审计，不执行后续节点。审批、人工验证及生产／受保护环境仍由页面指定人员处理，不能把普通聊天同意或人工回执当成审核记录。

- Keep administrator/member roles simple; reviewers are configured on approval nodes, not new global roles.
- Work-item lists and delivery selectors query upstream records live, without managed/unmanaged or stored/live modes. Source controls only filter by connection; platform-created items remain available in the unified list, read directly from the platform. Keep stable references, delivery content and frozen audit history; a query never bulk-imports upstream items.
- Work items and independent changes do not belong to an environment. Versions aggregate multi-application delivery scope; execution resolves environment-specific resources. Do not publish one application twice because two Bugs reference it.
- Distinguish source-platform status, delivery intent, platform execution success, and current runtime health. A closed Bug, successful preparation flow, or absent monitoring data is not proof of production delivery or health.
- Keep workspaces independently scoped, allow multiple instances and renaming, and preserve drafts/filters on return. Changing context affects only the current workspace.
- Query and change submission are equally prominent in DBMS/Redis. Data comparison/synchronization is a separate reusable capability. Query and write approval switches remain independent.
- Prefer compact page workflows or a single editor. No stacked long modals, permanent informational/error banners, duplicated configuration entry points, or settings competing with primary actions. Use dismissible toasts; validation switches to and focuses the failing item/field.
- The user selected shadcn-vue (Nova/Neutral) on 2026-10-27. Migrate all built-in business pages as well as the shell and delivery-content editor, using official components/Blocks for navigation, tabs, fields, dialogs, pickers and tables. Do not recreate widgets or implement an Arco/Element compatibility adapter. Keep business contracts and draft/workspace behavior. Keep existing plugin/workspace document boundaries for behavior isolation; every built-in entry uses the same shadcn tokens and must not load legacy theme CSS. Remove obsolete styles from migrated entries, including global native-control overrides.
- Use Chinese date controls in floating panels; opening calendars must not increase page/filter-row height. Align forms with official Field/FieldGroup, consistent control sizes and responsive grids. Keep contextual help in tooltips/popovers, rather than appending permanent explanatory paragraphs for every feature.
- Use generic platform terminology, source-provided enum/person display labels, safe rich text/Markdown and pasteable images. Never guess custom source labels. Missing names retain accounts; Closed is not a person, zero dates are not real dates.
- Async actions need visible loading, duplicate-click prevention, recoverable errors and meaningful empty states. Theme changes must propagate to embedded plugins.
- Keep hosts as a separate resource workspace with direct Agent status, detection and collection configuration actions. Application environments select registered same-environment hosts directly; host groups, shared resources and variable groups are optional reuse tools. Never create hidden host groups to satisfy a UI simplification. Persist explicit host bindings atomically; keep host environment/deletion guards, protected-environment approval and immutable release snapshots.
- Reuse declared resource bindings. Resource connections/bindings are low-frequency setup, not mandatory repeated daily work. Nacos bindings can define environment-specific Data ID/Group.
- 变更录入支持在原编辑器“保存草稿／校验并就绪”；就绪是内容元数据状态，不代表环境预检、审批或执行。保存与就绪必须同一事务，只处理显式提交且有权维护的内容，保留其他人的内容与来源关联。资源声明检查需在事务内解析自有、共享资源组与 Nacos 目录引用，不能只查旧应用资源表。全部内容就绪后的交付／版本入口携带原工作项，原窗口与草稿保持独立。
- SQL 录入体验必须实际保存为 SQL 类型，Nacos 必须实际保存配置项，不能以自定义记录替代后宣称已验收。交付内容编辑器允许保留带说明注释或暂不支持语法的非空有界 SQL 草稿；就绪（包括直接就绪接口）、预检与执行继续严格校验，失败不能部分就绪。保留资源声明、归属、版本冲突、已执行内容及冻结同步计划的保护；不能为通过校验擅自改写业务脚本语义。验收新增配置须明确标注与实际业务变更区分，未经授权不发布。
- 独立变更与工作项编辑器共用 SQL 草稿／就绪契约；不支持执行的语法可保存原文，不能据此就绪。需求说明接收 Textarea 的字符串模型事件；创建成功后才解除本次提交的离开保护并进入已创建记录，保留原列表来源。平台自建工作项的创建人和负责人分开维护，负责人只选启用的平台成员；“指派给我”不能退化为“由我创建”。变更活动在保存事务内记录具体类型、编号和标题，人员用可信显示名、未知仍保留账号，技术原文折叠，不重写历史活动。全屏等显示偏好仅保存白名单字段，并按用户／工作区隔离，不持久化 SQL 或配置内容。
- Resource接入（2026-11-10）：Nacos 目录引用必须携带明确环境与连接，不能自动展开到其他环境；旧引用只冻结一次已有有效作用域，未绑定的保持待配置。应用资源保存使用独立原子修订与 CAS；冲突保留草稿、展示服务器差异并三方合并，不能仅刷新版本号覆盖。连接管理往返和读取差异不得销毁筛选／草稿。核验记录持久保存并随目标／凭据变化过期；已保存、凭据已配置、目标可读、写权限及运行健康分别表达。依赖识别只生成脱敏候选，不自动保存、建连接、执行或发布；资源 MCP 权限单独授权，写入复用冻结预览／确认，不接收明文密码。Redis 前缀是显式非空精确白名单，读写和 SCAN 都不能扩大范围，历史单前缀冻结计划保持有效语义。

- MySQL connections represent instances and credentials; application/resource-group environment bindings select `config.database`. Migrate legacy connection defaults into explicit application/resource-group bindings and remove the connection database field, preserving historical snapshots and direct-query scope policies; isolate query results/caches by database, and verify the effective database again before executing reviewed SQL. Never silently widen legacy direct-query scope or let bindings override addresses/credentials.

- 资源识别支持明确的应用 ID／运行名映射、固定 Data ID 与单个 Flink 作业；受管理源码仍限制当前产物模块，候选保留原别名及来源摘要，不改源码、不枚举其他作业或租户库。受限 Key 首次接入只能使用目标环境已登记、且来源地址／命名空间完整匹配的 Nacos；不扩大全局连接权限。来源状态仅解释未绑定项，不能掩盖已有绑定核验失败；缺源配置不推断为未使用，识别条件变化后旧候选不能直接加入草稿。
- Application branch/artifact defaults are edited in Application → Environment → Configure environment → Code/artifact strategy, not in the shared environment editor. Shared environments retain workflow bindings, approval gates and common defaults. Save application defaults and destination atomically with policy revision checks; keep existing versioned policy readers and frozen snapshots valid. Integration branches do not show an ignored base-branch field.
- Platform runtime Dockerfile templates package compiled inputs; Java images require a preceding successful Java compilation and one fresh, unambiguous JAR. Never silently choose an old/newest JAR. Repository/custom multi-stage Dockerfiles may compile themselves. Compilation containers do not receive the Docker socket; image packaging remains on the Runner host.
- A build may retain multiple immutable outputs while preserving its primary artifact API. Java runtime-image builds keep the exact fresh JAR used by the image; explicit repository/custom exports are labeled as same-build outputs unless image inclusion is proven. Keep authoritative output links and environment build provenance, rather than trusting display metadata. Manual publication is an application/environment target in the existing deploy node: no invented hosts/SSH commands, no automatic execution, and downloading never completes a release. Freeze the artifact/digest/assignee/instructions; record actual time/result with duplicate-safe updates, preserve failed attempts, and keep human confirmation distinct from machine verification.
- Build-tool execution choices use the same selector in image/JAR settings and onboarding. Wrapper download failures must not silently change execution mode. Configuration repair links locate the field; saving/preparing a new build is not execution. Keep current-configuration builds distinct from original Commit/configuration snapshot retries.
- Maven 命令行附加参数在应用公共构建配置中维护，支持 -e/-X 等执行选项并随构建快照冻结；不能填到 Goals 或 -D 属性，也不能在附加参数中覆盖 Profiles、-D、线程、settings 来绕过环境构建契约。锁失败提示应先于仓库 DNS 警告分类，并定位附加参数框；1 为单线程，1C 为每 CPU 核一个线程，不能混淆。
- 构建显示编号为应用内持久流水号，失败构建也占号，删除历史不能重用；全局 pipeline run ID 仍用于 API、路由、重试、制品及审核引用。历史按各应用原记录顺序补号，不修改旧 ID 和原始日志。隔离 Maven 构建只读取明确配置的 settings.xml，使用每次构建私有副本只读挂载；不得 chmod/chown/relabel 原配置、挂载整个配置目录、关闭 SELinux 或改用特权容器来兼容权限。
- 应用发布单同样使用独立的持久流水号，同一应用跨环境连续计数；失败占号，删除不重用。历史按原 release order ID 顺序补号，只新增映射与计数，不改旧发布单、审批、日志、制品或编排引用。全局 release ID 仍是所有读取和执行操作的身份；列表、详情、消息、个人工作台与编排子发布展示应用内编号，未知映射明确显示“记录 ID”，不能把全局 ID 猜作应用内编号。跨应用交付执行单保持原命名空间。
- Single-application publish actions open one in-place selection/confirmation dialog. Keep the originating table mounted; cancel and successful submission preserve the original tab, filters and page. Dialog environment selection must not change the underlying workspace context. Keep artifact identity and all release/approval gates unchanged.
- Environment-dependent Java builds use application/environment BuildVariant contracts (Maven Profiles/-D; Gradle Tasks/-P/-D), not Spring runtime profiles. Derive provenance from encrypted build snapshots, never public artifact labels. Check compatibility in ordinary releases, work-item/version/Hotfix preflight and before ANY resource/script side effect, including lower-level/bare-host paths. Java runtime Docker images inherit compilation provenance. Auto preparation uses the selected environment's contract and freezes it. Version artifact bindings/locks are per environment; never overwrite a reviewed mapping to promote a different environment package. Unconfigured environment-independent legacy apps remain usable, but unknown provenance cannot satisfy a configured build contract.

- Java 完整目录发布使用独立 `java-bundle` / `ssh-java-bundle` 类型；普通 JAR 保留原 Maven/Gradle、产物路径和发布步骤。整包目录、包内主 JAR、必需文件等字段只对整包应用显示，不能给已有 JAR 应用增加必填项、额外页面或发布步骤。

- 接口文档浏览允许跨应用按功能名称、描述、分组、方法和路径检索，应用与环境是可选筛选。默认查询每个应用/环境的最新不可变文档，历史快照可按原归属查看；结果、详情、调试和用例入口必须携带准确的应用/环境/快照身份。同路径接口不能混用归属。文档导入、生成来源维护仍需明确应用及具体环境，查询不触发生成或执行。

- 接口文档只展示一个“维护人”：接口显式 `x-maintainer` 可调整归属；其余已有接口按同应用、同环境、HTTP 方法和路径沿用上一快照的有效维护人（未填仍保留未知），不能因上传人或文档默认联系人改变而转移。首次/新增接口按显式 `x-maintainer`、明确代码作者、已验证 MCP Key 绑定用户依次默认；上报 Skill 只读核对方法/类作者声明，方法优先，并只对无显式归属的新增接口把作者写入候选 `x-maintainer`。不据此重分配已有接口，不以提交人/构建人或全局 `info.contact.name` 代替作者；已验证 Key 优先于文档全局联系人。无 Key 的普通导入才保留联系人兼容默认；普通上传、手动编辑或构建不推断上报账号。新快照逐接口冻结维护人及来源，不修改原始 JSON，不回写历史。重复上报复用原快照及原默认维护人，旧快照不补写。请求/响应中的对象与对象数组使用可独立展开、收起的层级字段表，保留父子归属与完整说明；维护人提取和浏览不改写历史快照。

- 通用脚本发布是应用/环境的 `script` 目标，复用既有 deploy 编排节点、已登记 Runner 与冻结制品，不新增 Flink 专用平台目标。普通 JAR 配置不增加脚本字段或步骤。脚本不继承平台凭据；审批/制品环境契约必须在任何脚本副作用前校验。超时、崩溃及结果未知时保留持久执行保护，不自动重放、回滚或以退出码代替验证；人工核对必须区别人为记录与机器验证。

- 日志正文统一使用 VictoriaLogs，不保留 SQLite 日志查询后端、双写、旧日表/FTS/历史查询回退。SQLite 仅承载告警配置、有限投递/回放队列、隔离和有期去重收据；接收先持久提交再 ACK，未知响应允许至少一次补发，查询/计数按不可变事件身份去重。短期回放窗口不等于无限备份，空间压力必须背压且不能丢弃已接收的待投递载荷。旧日志库只读迁移告警配置，不自动删除旧数据。

- VictoriaLogs 的 Linux 安装、启停、升级统一由 `devopsplatformctl` 管理，先确认存储健康再启动平台。保留已有数据、凭据和保留策略；普通平台升级不重启未变化的引擎。存储使用独立版本链接，平台回滚不能隐式降级已使用数据目录的引擎；外部存储只检查健康、不操作远端进程。无法确认归属/旧引擎兼容性时拒绝接管，不能重置为新装默认值。

- Agent 资源优先级（2026-11-05）：用户允许日志少量重复，不要求端到端完全不重复；优先降低业务机器 CPU、内存和磁盘 IO。允许重读不等于允许丢弃断连积压；不要为了消除所有重复引入高成本机制，也不要直接删除现有恢复队列。以同机同负载前后实测说明收益、内存/延迟等取舍，保留 Java 多行与机器监控能力。

- Agent 未发送队列采用分段顺序文件、批量压缩及校验；完整块同步后才推进源断点，服务端 ACK 后才推进消费游标，崩溃允许重放。满额背压，不淘汰已接收积压；未知格式/完整坏块保留并报错。旧 SQLite spool 仅用于停止旧 Agent 后的一次性只读导入，原文件保留；正式队列不双写。此次切换先做隔离恢复和同机资源对比，不更新业务机器。

- Agent 部署模块默认关闭，使用独立凭据和本地目录授权；不能用日志 ingest Key 执行发布。冻结任务先持久化 intent，再派发到独立 systemd worker，应用不能继承日志 Agent 的内存/cgroup 或随控制进程重启被停止。断连补报按任务/序号/摘要去重；超出启动观察窗口为待核对并保留进程和执行保护，崩溃或收据不明只做原身份的只读核验，不能自动重放启动/停止/回滚。按 2026-11-10 用户要求扩展托管 JAR、Java 整包及多机回执调度；回滚必须在明确进程退出、原目录/制品/变量可核验且策略已冻结时执行，不得以启动耗时、断连或健康等待触发停止/回滚。受保护环境保留审批和回滚约束；批次先原子保存关联，再按持久回执控制并发和推进，已确认的继续不得因重启丢失，成功主机不擅自连带回滚。普通 JAR 不增加必填项或发布步骤；其他类型/真实业务/断电恢复不得凭隔离用例声称通过。

- Agent 自动接入通过管理员冻结预览和确认，使用独立部署凭据及精确应用/目录授权；不把日志令牌扩展为执行令牌。先持久记录接入意图，未知结果只核对原操作，不能自动重装或重复轮换。保留已有采集设置、队列、安装身份及执行 UID，独立 worker 以同 UID/GID 运行；固定 root 管理的启动器只授权受限任务编号。验证 worker 隔离及新凭据回连后才激活，轮换不能提前废弃旧凭据，活动任务阻止变更授权。安装状态与已连接可部署状态不能混淆；授权接入不会自动切换应用通道或发起发布。

## Safety and verification

- 接口文档日常操作保持“编辑 → 保存”，自动建立不可变修订；保留可选差异检查、原应用/环境/快照身份和冻结维护人。接口/字段的首次新增需求与最近修改需求分开记录，工作项反查使用各修订原基线，未知历史不猜填。可视化编辑共享 `$ref` 先明确转为当前接口副本；路径级继承参数不能通过当前接口改名/删除而暗中残留旧参数，名称/位置变更需在完整文档核对。JSON 示例未完成时保留输入并阻止保存/切换，大整数使用无损 JSON。Agent 局部修改复用冻结预览、确认、权限和 CAS，不自动合并或重放；单接口读取也必须应用冻结维护人，新增接口才可使用已验证 Key 默认归属。文档路由加载先捕获目标接口，不能在加载中用旧视图状态覆盖新查询参数。

- 实际配置过程中发现的易用性、布局、错误反馈和功能缺口，持续记录到 `deploy/UI-INTERACTION-ACCEPTANCE.md` 的对应实操待优化清单；记录证据与优先级，区分实测问题、代码确认风险和待验证建议，不把记录或规避方案写成已经修复。

- The user requires regression prevention, not just a new-feature smoke test. Before declaring a development round ready for deployment, run the full backend suite with race detection, all frontend tests, and the production UI build. Recheck the affected historical interaction paths in isolated browser fixtures (workspace ownership/back navigation, draft preservation, duplicate submission/activity, selectors/popups, authentication expiry, and responsive/theme behavior as applicable). Keep regression cases for previously fixed defects aligned with the intended behavior; never restore an obsolete behavior just to satisfy an outdated assertion. Record actual passes, failures and untested external integrations in deploy/UI-INTERACTION-ACCEPTANCE.md; do not deploy with an unresolved failure or present skipped/manual checks as passed.
- Readiness reports and code evidence are read-only observations, not execution/approval tokens. Never equate manual confirmation with machine verification, infer code inclusion from a branch name, or infer exclusive scope from Git ancestry alone.
- Preserve frozen snapshots, artifact digests, approval gates, execution leases and uncertainty states. Never auto-replay SQL/Redis/Nacos or uncertain writes. Do not promise transactional rollback across services/resources.
- No real remote build, merge, deployment, SQL, Redis command, configuration publication, or source Bug mutation just to exercise a UI. Use temporary fixtures/explicit controlled integration tests; report what was not validated.
- Preserve unrelated local test data, templates, existing user changes and configured log retention. Legacy standalone preparation records are out of scope for compatibility; do not delete other business data. Back up runtime binaries before replacing them; update only the required plugin where possible. Do not weaken authentication/approval for testing.
- Personal view memory stores only allowlisted presentation/filter preferences, scoped by user and workspace. Never persist credentials, SQL/config bodies, approval data, execution tokens, cursor tokens or selected execution scope as preferences.
- Browser validation uses the supported computer-use browser tools. Capture meaningful evidence and keep the user session available; do not restart the shell unnecessarily.
- Give opt-in Go browser fixtures an explicit test timeout sufficient for the browser session, then stop them and verify a clean test-process exit after validation; their internal server lifetime does not override Go's default test timeout.

## Current delivery order

1. Release readiness, inspectable code/artifact evidence, and personal view memory.
2. Multi-application environment overview and evidence-based canary observation (no data = inconclusive).
3. Reusable/versioned workflows and adapters/plugins, without making normal operations harder.

Maintain `deploy/UI-INTERACTION-ACCEPTANCE.md` with actual tests and limitations, not assumed completion. Do not mark later phases complete because phase 1 is delivered.

## Application instance controls

- Keep instance status, actions, confirmation and bounded logs in the application environment page, with direct restart/log buttons. Docker controls act on the existing verified container ID and immutable deployment identity, preserving image/runtime settings; do not rebuild or redeploy to restart. Unknown ownership, conflicting bindings, changed fingerprints and active publication block controls. Keep durable duplicate protection, uncertain results without automatic retry, protected-environment confirmation/reason and server-side log redaction. Manual publication must not imply remote process ownership.

## Development/test Jpom coexistence (2026-11-23)

- The user explicitly selected overwrite publishing with manual fallback for development/test: both platforms may publish to the original Jpom JAR directory without a new cross-platform deployment lock. Keep the ordinary managed-JAR flow unchanged. Compatibility uses the registered Jpom project ID plus exact basedir to identify the application, replaces only its named JAR, and preserves other files. Compatibility commands do not infer ownership from a port, force-kill an ambiguous process, or automatically replay/rollback on error. A failed compatibility flow means manual inspection is required, not proof that its JVM exited. This opt-in mode is restricted to unprotected development/test environments and SSH; production and existing Agent/managed uncertainty protections remain unchanged. Saving a compatibility configuration is not authorization to run a business deployment.

- Jpom compatibility detection is read-only and scoped to registered development/test hosts. Exact migration directories or saved Jpom identity may trigger detection when editing an existing environment; ordinary managed apps and explicit manual mode selections are preserved. Auto-fill only a unique, consistent original Java/JAR identity and runtime settings across all selected hosts, keep drafts on failed/stale replies, and persist only through the existing environment save. Never infer ownership from a port, probe on each publication, return obvious credential arguments, or claim stopped applications with missing source metadata were detected.

- Jpom-compatible ordinary JARs keep instance management: bind controls to exact original project/basedir/JAR, reviewed PID generation, boot and current file digest, with existing administrator confirmation, request deduplication and active-publication protection. Never infer a verified platform version from a shared JAR path, signal a foreign/ambiguous process, add a cross-platform file lock, or replay an unconfirmed action. Recheck after execution; native runtime redaction inputs stay inside the backend. Runtime environment variables must not overwrite control-script variables.
