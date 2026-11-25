# LogPlatform

面向小团队的轻量可观测与发布平台：统一控制台 + 日志 + 指标/Trace + 构建部署。测试、预发和生产是独立环境；同一不可变制品可以逐级发布，不需要为每个环境重复构建。

## 构建

```bash
./scripts/build-all.sh
# go 不在 PATH 时：GO_BIN=/path/to/go ./scripts/build-all.sh
```

> macOS 开发机如需 agent 的 CPU 指标：`go build`（默认 cgo）重编 agent——gopsutil 的 darwin CPU 路径依赖 cgo，纯 Go 编译时该指标返回未实现（mem/disk/net 不受影响）。Linux 生产环境读 /proc，纯 Go 即全量可用。

## 运行

```bash
# 首次安装唯一日志存储依赖（官方固定版本、校验 SHA-256）
python3 scripts/fetch-victorialogs.py
# 启动本地 VictoriaLogs，platform 托管 logs / monitor / deploy，再启动 agent
AUTH='<strong-admin-password>' ./scripts/start-all.sh

# 停止
./scripts/stop-all.sh
```

首次以 `DEVOPSPLATFORM_AUTH`（或 `AUTH`）启动时，平台会创建用户名 `admin` 的管理员账号，密码就是该配置值。后续在右上角“管理员”入口添加成员；角色固定为“管理员/成员”，不提供自定义权限矩阵。管理员维护用户、环境、主机、凭据和 Runner，成员可进行日常构建、发布、日志和监控操作。生产环境等受保护环境的发布审核人员在环境配置中从已启用用户里单独选择。

### 控制面元数据使用 MySQL（第一期）

生产环境可以把全局应用目录、部署控制面以及监控指标/Trace/告警迁移到 MySQL 8.0.13 及以上版本；SQLite 仍是本地开发默认值。制品文件、源码工作区和凭据加密密钥仍保存在 deploy 数据目录。日志正文统一进入 VictoriaLogs；SQLite 只用于日志投递状态和配置。详见 [VictoriaLogs 配置与切换](deploy/VICTORIALOGS.md)。控制面迁移前保留原有 `registry.db / deploy.db / logs.db / metrics.db` 备份。

三个组件必须使用独立 schema（可以在同一台 MySQL 实例），避免同名表和各组件迁移生命周期互相影响：

```sql
CREATE DATABASE devopsplatform_catalog CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE devopsplatform_deploy CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE devopsplatform_monitor CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;
CREATE USER 'devopsplatform'@'%' IDENTIFIED BY '替换为强密码';
GRANT ALL PRIVILEGES ON devopsplatform_catalog.* TO 'devopsplatform'@'%';
GRANT ALL PRIVILEGES ON devopsplatform_deploy.* TO 'devopsplatform'@'%';
GRANT ALL PRIVILEGES ON devopsplatform_monitor.* TO 'devopsplatform'@'%';
```

首次切换前先停服务并备份整个 `/home/devopsplatform/data`。三个迁移命令都不会修改 SQLite 源库，也不会删除 MySQL 中额外存在的数据，可以安全重试：

```bash
sudo systemctl stop devopsplatform
export DEVOPSPLATFORM_DB_DSN='devopsplatform:密码@tcp(127.0.0.1:3306)/devopsplatform_catalog?charset=utf8mb4&parseTime=true&loc=Local'
/home/devopsplatform/current/bin/migrate-registry \
  -source /home/devopsplatform/data/platform-data/registry.db

export DEVOPSPLATFORM_DEPLOY_DB_DSN='devopsplatform:密码@tcp(127.0.0.1:3306)/devopsplatform_deploy?charset=utf8mb4&parseTime=true&loc=Local'
/home/devopsplatform/current/bin/migrate-deploy \
  -source /home/devopsplatform/data/deploy-data/deploy.db

export DEVOPSPLATFORM_MONITOR_DB_DSN='devopsplatform:密码@tcp(127.0.0.1:3306)/devopsplatform_monitor?charset=utf8mb4&parseTime=true&loc=Local'
/home/devopsplatform/current/bin/migrate-monitor \
  -source /home/devopsplatform/data/monitor-data/metrics.db
```

确认迁移成功后，在 `/home/devopsplatform/config/devopsplatform.env` 增加：

```ini
DEVOPSPLATFORM_DB_DRIVER=mysql
DEVOPSPLATFORM_DB_DSN=devopsplatform:密码@tcp(127.0.0.1:3306)/devopsplatform_catalog?charset=utf8mb4&parseTime=true&loc=Local
DEVOPSPLATFORM_DEPLOY_DB_DRIVER=mysql
DEVOPSPLATFORM_DEPLOY_DB_DSN=devopsplatform:密码@tcp(127.0.0.1:3306)/devopsplatform_deploy?charset=utf8mb4&parseTime=true&loc=Local
DEVOPSPLATFORM_MONITOR_DB_DRIVER=mysql
DEVOPSPLATFORM_MONITOR_DB_DSN=devopsplatform:密码@tcp(127.0.0.1:3306)/devopsplatform_monitor?charset=utf8mb4&parseTime=true&loc=Local
```

然后执行 `sudo devopsplatformctl restart`。`GET /api/health` 的 `catalog_database`、`GET /api/plugins/deploy/api/health` 的 `metadata_database`、`GET /api/plugins/monitor/api/health` 的 `database` 都应返回 `mysql`。迁移完成后抽查应用、环境、构建历史、发布单、监控曲线和告警规则，再发起一次测试环境构建发布和 OTLP 上报。三个迁移命令均按主键幂等写入，可以在停服状态重试，不会删除目标库额外数据。

监控 MySQL 方案适合当前小团队和受控保留周期：机器指标默认保留 7 天、Trace 默认保留 6 小时，分钟指标按现有策略使用。若以后达到持续高基数或亿级时序数据，再把监控数据面替换为专用时序存储，不影响应用、环境和发布模型。DSN 包含 `#`、空格或其他 shell 特殊字符时必须按照 systemd EnvironmentFile 规则加双引号，密码中的保留字符还应进行 URL 转义。迁移期间必须保留 deploy 数据目录中的 `.credential-key`；否则 MySQL 中迁移过去的 SSH、Git 和变量组密文无法解密。

agent 配置示例：

```yaml
server_url: http://log-server:9801
token: <shared-token>
host: my-host                 # 缺省取 hostname
environment: test             # 使用首次向导中创建的环境标识；可在 collect 项覆盖
registry: ./registry.json     # 断点文件
collect:
  - application_id: app_xxxxxxxxxxxxxxxxxxxxxx # 平台生成的稳定应用 ID
    service: order-api                          # 展示名
    paths: [/var/log/order/*.log]
    multiline: '^\d{4}-\d{2}-\d{2}'   # 行首正则，多行合并；缺省单行模式
    environment: prod                  # 可覆盖全局 environment
  - service: user-svc
    paths: [/var/log/user/*.log]
```

日志 Agent 首次启动时在本地 spool 数据库的 `agent_identity` 表生成固定安装 ID。
`agent_name`（未填写时使用旧配置 `agent_id` 或环境/主机名）只作显示名称；改名、改环境和重启不改变安装 ID。
升级必须保留 spool 数据库与 registry，不要把同一份状态库克隆到另一套 Agent。
`logagent -config /etc/logagent/agent.yaml -identity` 只读查看已保存的 ID。
旧版 Agent 仍可上报，升级后才使用固定 ID。此身份用于日志采集；机器指标的环境/主机维度保持原样。

通过控制台安装时不需要手工填写 Token：平台从 `DEVOPSPLATFORM_DATA_TOKEN` 注入上报凭据，数据库只保存每台主机的期望采集配置。进入“资源中心 → 主机 → 采集配置”，从部署应用下拉框选择应用并填写日志路径；可只保存期望配置，也可“保存并安装 Agent / 保存并下发”。修改日志目录不需要重新安装二进制。

Java 异常堆栈被拆成多条时，在该采集项的“日志合并方式”选择 **Java 异常堆栈合并（时间戳首行）**，然后“保存并下发”。支持日期、ISO 时间或时分秒开头的日志，后续异常行并入上一条，保留首行级别；搜索中点击日志可展开完整堆栈。新建 Java JAR 采集项会推荐此模式，已有规则不会被静默修改。每行一个 JSON 的日志请选择“单行日志”，其他格式可填写“自定义首行正则”。配置下发会重启 Agent，沿用原断点；仅影响后续采集，历史拆行记录不会自动重组。

彩色 Spring Boot / p6spy 日志（含 ANSI SGR，如 `ESC[32m`）由新版 Agent 在首行匹配时忽略颜色码，原始文件偏移和事件身份保留；服务端在入库前清除颜色码，再提取级别及 Trace ID。`2026-11-18 08:21:59 943` 这类空格分隔毫秒的首行同样可合并。正文中的原始时间文本保留，平台事件时间仍为采集时间。此能力需更新 Agent 与日志服务；已入库历史不重写。

## API

```
POST /ingest                      # agent 上报（X-Token + gzip 批量）
GET  /api/search?service&environment&host&start&end&q&trace_id&cursor&limit
GET  /api/services                # 今日各服务条数
GET  /api/releases?service=name   # 发布单与各主机执行记录
GET  /api/work-items              # 独立需求/BUG
GET  /api/changes                 # 独立变更
GET  /api/release-versions        # 跨环境发布版本
GET  /api/delivery/templates      # 多应用编排模板
GET  /api/delivery/runs           # 执行与审核快照
GET  /api/connections             # Git/Maven/镜像仓库连接（不返回密文）
GET  /api/host-groups             # 可复用主机组
GET  /api/variable-groups         # 加密变量组（只返回变量名）
GET  /api/runners                 # 构建 Runner
GET  /api/services/summary        # 各应用最新构建、制品与环境摘要
GET  /api/pipeline/runs/page      # 构建记录分页：service/q/status/branch/actor/start/end/page/page_size
GET  /api/artifacts/page          # 制品分页与来源、曾成功发布环境
GET  /api/release-orders/page     # 发布记录分页：额外支持 environment
GET  /api/pipeline/runs/{id}/capabilities # 当前用户可用的重试/取消能力
POST /api/pipeline/runs/{id}/retry # 原 Commit/配置重试，confirm + reason，绝不自动发布
POST /api/pipeline/runs/{id}/stop  # 取消当前构建，reason 必填，执行器退出后释放锁
GET  /api/audit                   # 配置、构建、审批和发布审计
GET  /api/me                      # 当前登录用户和内置角色
GET  /api/users                   # 平台成员列表（写操作仅管理员）
```

## Codex / Vibe Coding MCP

平台内置 `devtools` 插件，通过 Streamable HTTP MCP 提供诊断查询、研发工作项、交付变更、流水线和版本进度；写操作必须先预览，再明确确认。工具清单由令牌权限决定。

1. 打开侧栏「MCP」创建访问令牌；必须显式选择权限、应用、环境和有效期，密钥只显示一次。写权限默认不勾选，只能由本人已登录的平台账号创建；账号停用后绑定令牌拒绝使用。原有令牌不自动增加权限。
2. 在目标代码项目的 `.codex/config.toml` 中加入：

```toml
[mcp_servers.devOps]
url = "http://127.0.0.1:9800/api/plugins/devtools/mcp"
bearer_token_env_var = "LOGPLATFORM_MCP_TOKEN"
enabled = true
default_tools_approval_mode = "writes"
tool_timeout_sec = 120
```

3. 从带有该环境变量的终端启动 Codex：

```bash
export LOGPLATFORM_MCP_TOKEN="lp_dev_..."
codex mcp list
```

查询权限包含 `logs:read / metrics:read / traces:read / builds:read / releases:read / workitems:read / changes:read / delivery:read`。可查实时 Bug、自定义工作项（connection=manual）、本人待办、交付变更、Git 分支、构建日志、制品、资源绑定、数据同步任务定义、版本、编排模板及执行进度。应用与环境使用服务端真实记录校验，不能只传入一个授权应用名就读取其他应用的数据。工作项、交付内容和版本跨应用，当前要求显式授权应用 `*`；来源数据仍受绑定账号在来源平台上的权限约束。

写权限 `workitems:write / changes:write / builds:trigger` 独立选择。操作路径：读取来源和目标 → 必要时 `attach_source_work_item` 关联来源 Bug 引用 → `change_work_item_state` / `add_work_item_changes` / `trigger_build` 生成预览 → 向用户展示完整目标与内容 → 用户同意后 `confirm_platform_action`。确认有效期 10 分钟，绑定令牌、账号、目标摘要与冻结参数；状态或配置变化后须重新预览。并发/重复确认、进程重启和不确定结果不会自动重放写入，使用 `get_platform_action` 查询确认状态。

开发变更统一使用已有 `changes:read / changes:write` 权限，不为 SQL、代码或 Nacos 再增加一套协议：

- `report_changes(service, changes, work_item_id?)`：一次登记 1–50 项开发变更。省略工作项时独立保存为草稿；填写工作项时追加到其交付内容（工作项跨应用，仍需显式授权应用 `*`）。每项可省略 `service` 以复用本次应用，禁止混入其他应用。变更本身不绑定环境；交付时解析应用资源的目标环境绑定。
- `get_change(service, change_id)`：读取单项完整变更，校验真实应用归属；Nacos 敏感配置值沿用 MCP 脱敏。
- `code`：`reference` 填分支，`summary` 填改动说明，可记录风险和恢复方案；代码仍在 Git，登记不提交或推送仓库，也不伪造构建 Commit 证据。
- `mysql`：填写 `sql.resource_alias / content / max_affected_rows`，可登记现有 SQL 引擎支持的建表、加字段、索引及数据变更；平台不把任意 SQL 当作可执行支持。
- `nacos`：填写资源别名、默认 Group、可选 Data ID、格式，以及 `entries` 中的设置/删除项。交付时目标环境的资源绑定优先，MCP 不接收连接密码和 Namespace 的重复配置。

`report_changes` 返回既有操作预览和确认编号；向用户展示目标及内容并确认后，调用 `confirm_platform_action` 原子保存草稿。保存不执行 SQL、不发布配置、不构建或部署。预览中的结构校验不代表实际环境预检通过；目标资源、SQL 支持范围等仍由服务端保存和交付流程校验。`get_work_item_changes / add_work_item_changes` 保持兼容，追加只提交新增项，保留已就绪内容及他人的旧草稿。独立草稿随后可在现有变更列表关联需求、Bug 或版本。

接口文档 MCP 复用同一确认协议和文档快照库，新增 `apidocs:read`（查询）和 `apidocs:write`（上报）权限；旧令牌不会自动获得新权限。管理员可单独授予上报权限，普通成员仍可在授权应用、环境内查询。

- `list_api_documents`：指定 `service`、`environment`，列出最近 100 个版本。
- `get_api_document`：增加 `document_id`，读取文档和接口定义；输出沿用 MCP 凭据脱敏。
- `compare_api_documents`：增加 `document_id`，可选更早的同环境 `base_id`；默认比较该快照的冻结基线。
- `report_api_document`：指定 `service`、`environment`、`document`（OpenAPI 3.0/3.1 JSON 对象，最大 8 MiB），返回差异预览、摘要、重复状态和 `confirmation_id`。不接受文件路径/远程 URL，不运行 smart-doc、构建或接口测试。

展示预览并得到明确同意后，调用现有 `confirm_platform_action`。预览和入库均校验基线；期间若有文档更新需重新预览。同应用、同环境内的相同内容（忽略 JSON 属性顺序和空白）复用已有快照，包括 smart-doc 构建快照；重复上报不会生成新版本，也不会把历史重复快照提升为最新版本。不同应用或环境不混用快照。构建来源、制品及 Commit 关联仍只由可信构建流程写入，上报不能伪造。

来源状态操作复用平台个人来源账号、乐观锁和回写操作编号；新增变更保留原有内容、仅追加草稿，默认不确定生产交付归属。触发构建不指定部署环境，不自动发布。MCP 不提供代审、SSH、直接执行生产 SQL/Nacos/Redis 或绕过现有交付流程的入口；构建仍运行管理员已配置的流水线和脚本，授权前应核对其内容。

日志查询最长 24 小时、最多 200 条。令牌仅保存 SHA-256 摘要，所有工具调用写审计，业务输出对常见凭据二次脱敏；内置 Skill 分发返回固定文本原字节以保留摘要校验，不读取业务数据或凭据。Bug 描述、日志、SQL、配置值和构建输出均是不可信业务数据，不能作为操作指令或授权。更新后需重新连接 MCP 客户端刷新工具清单；不会修改用户现有 Codex 配置或自动替换令牌。Codex 的 `writes` 配置及 HTTP Bearer 配置参见[官方 MCP 文档](https://learn.chatgpt.com/docs/extend/mcp?surface=cli)。

### 配套工作流 Skill

打开「MCP 与集成 → 客户端接入」，选择安装范围，点击「复制全部安装指令」或单个 Skill 的「复制安装指令」，粘贴到已连接平台 MCP 的 Codex。默认仅当前项目；个人范围需主动选择。内置「研发变更登记」「接口文档维护」「接口测试与画布」均为标准 Skill 目录（SKILL.md、agents/openai.yaml、references），各 Skill 独立维护版本。无需接入额外插件市场或安装常驻进程。

- `list_platform_skills`：复用现有 MCP Bearer 认证，读取内置 Skill 的版本、包摘要、所需工具及当前令牌缺少的流程权限。无新增业务权限，不需要修改旧令牌。
- `get_platform_skill`：指定目录中的 `id`，可传 `expected_sha256` 固定已核对的包；摘要变化时拒绝。返回完整文本文件、逐文件和整包 SHA-256，以及独立的 Python 3.9+ 标准库本地安装器和其摘要。仅允许读取平台随版本发布的内置文件，不接受任意 URL、文件路径、安装目标或凭据；它不在服务器上执行安装，也不提供已安装证明。
- Codex 在用户授权范围内核对安装器并暂存包，先运行 `--check`，再用 `--apply` 安装到项目 `.agents/skills/` 或个人 `~/.agents/skills/`。受管安装保存 `.platform-skill.json` 文件清单；同包且内容一致时不重复写入，干净的旧版本更新前保存到同级 `.agents/skill-backups/`，备份不参与 Skill 发现。回读校验后返回本地结果。
- 未管理的相同手动安装只返回 `adoption_required`；说明接管并得到同意后用 `--adopt`，同时备份。存在本地修改、未知文件、符号链接或两个范围重复时返回冲突，不覆盖。安装锁防止同一目标并发更新；更新替换失败恢复旧安装。
- 「复制更新指令」使用 `--update-only`，未安装只报告，不新增。网页显示平台提供版本，不凭 MCP 下载回执冒充客户端安装状态；文件验证不证明当前会话已加载，必要时新开会话或重启客户端。安装路径与发现行为见[官方 Skill 文档](https://learn.chatgpt.com/docs/build-skills)。

保留 ZIP 手动安装：下载解压后将同名 Skill 目录放到上述一个位置。ZIP 不含令牌、固定平台地址或安装脚本；团队自定义内容先比对再保留。复制指令不含密钥；平台不会自动改用户 MCP 配置、令牌、权限或本机目录，安装器只在客户端显式运行。

- `$devops-change-report 整理本次开发变更并预览上报`：识别应用和本次范围，核对已有变更，整理代码、SQL、Nacos 内容，可独立保存或关联工作项。需要 changes:read/write；关联工作项的读取与归属仍按原授权规则处理。
- `$devops-api-docs 比较当前应用测试环境的接口文档`：按应用、环境查询、比较或预览文档；只读使用 apidocs:read，上报需管理员本人及 apidocs:write。
- `$devops-api-testing 根据当前应用接口创建带条件和并行路径的测试画布，先校验并预览保存，不执行`：创建/修改用例、编排图、核对执行报告及维护个人布局。查询/校验需 apitests:read，保存需 apitests:write，执行另需 apitests:execute；读取接口文档按需另需 apidocs:read。

Skill 的自动发现不等于自动写入；普通编码不会因此上传。每次保存仍先展示具体 MCP 预览，再取得用户明确确认；不确定回执只核对同一确认 ID，不重放。Skill 不代替交付审批，也不自动运行 Git、SQL、Nacos、构建或发布。分发沿用既有 MCP 工具协议，后续仍可把相同目录打包为客户端插件。

## 环境与发布模型

- 新安装首次进入控制台时通过三步向导选择“简单流程 / 标准流程 / 完全自定义”，平台只创建确认过的环境，不再强制生成 `default`。旧部署配置仍会自动迁入兼容用的 `default`，不会破坏已有数据。
- 环境没有“内置不可删除”的特殊身份；未被应用配置、主机或主机组引用时都可以删除，但平台至少保留一个环境。是否保护、是否要求发布原因及审批人均由团队配置。
- 应用只定义一次代码、构建方式和制品类型，不为 test/prod 复制应用。每个“应用环境”独立保存环境主机组、远端目录、启停命令、健康检查、批次大小、批次内并发、批间暂停和失败阈值。
- 主机和主机组都归属唯一环境；主机不能跨环境加入主机组，应用环境也不能绑定其他环境的主机组。发布接口不接受临时主机名，只接收“应用 + 制品版本 + 环境”。
- `staging / prod` 默认受保护且发布原因必填。服务端强制校验，不能通过旧的“按主机直发”接口绕过。
- 每次环境发布先从绑定主机组生成不可变主机快照并创建发布单。受保护环境等待审批；执行时先完整预检制品和全部主机，再按批次滚动，支持批间人工继续、阈值熔断、取消和整单回滚。
- 构建产物使用唯一文件名，保存 SHA-256 与大小；部署前重新计算摘要，不一致会拒绝发布。
- 成功部署写入环境维度的服务注册表，控制台所见版本随全局环境切换。
- 应用使用平台生成的稳定 `application_id` 关联部署、日志和 OTel 数据。哪个入口先出现都可以：日志或 OTel 首次上报时先成为“待接入应用”，以后按同名部署应用接入时沿用原 ID；Agent 采集配置直接保存部署应用 ID，不依赖可修改的展示名。
- 回滚继续使用原环境部署参数；受保护环境回滚同样需要确认和原因。

推荐流程：提交一次 → 构建一次 → 发布 `test` → 验证 → 用同一 digest 发布 `prod`。测试与生产可以在不同机器、不同目录，甚至使用不同启动命令。

## 版本与变更管控

部署平台的“变更管控”用于先组织上线范围，再进入发布执行。需求与变更独立存在，变更可不关联需求；发布版本至少选择一项已就绪变更，不绑定环境。需求可以手工创建，也可以记录外部工作项编号与链接。变更可登记代码、制品、SQL、Nacos、数据同步、Redis 和人工操作，外部变更执行能力见下文 V1 边界。

版本先从草稿标记就绪，再交给部署编排选择目标环境；审核作用于具体执行单的不可变快照，而不是环境无关的版本。受保护环境复用指定发布审核人员，发起人不能自审。平台仍只有管理员和成员两个角色，不增加自定义权限矩阵；多应用编排按节点显式推进。

默认推荐“一套控制面、运行环境物理隔离”：平台只负责管理，`test` / `prod` 分别绑定不同主机组、账号、目录和命令，业务流量与数据不共用。若合规要求控制面也隔离，可部署两套平台；此时应再接入共享制品仓库按 SHA-256 提升同一制品，避免在生产控制面重新构建。

### Maven / Gradle 与云效制品仓库

Maven 私服用于依赖解析，以及发布会被其他项目依赖的 `api / sdk / common / starter / bom`。平台不会自动扫描或上传最终应用 JAR；需要发布公共组件时，应由项目自己的 POM 和明确的 `mvn deploy` 流程决定模块范围。

服务构建方式选择“Maven（结构化参数）”后可配置：

- Maven 执行方式：默认 `auto`（优先 `./mvnw`，不存在则使用 Runner PATH 中的 `mvn`），也可指定 `mvn` 或绝对路径；
- goals，例如 `clean package`、`clean verify`，公共组件发布流水线可以明确使用 `deploy`；
- profiles，例如 `ci,release`；
- `-D` 属性，每行一个 `key=value`；
- 云效提供的 `settings.xml` 绝对路径，用于 mirror、repository、server 凭据和 Maven profile。

发起流水线时可以覆盖本次运行的 profiles 和 `-D` 属性，实际生成的 Maven 命令随流水线记录保存，便于审计和复现。云效 Maven 私服地址和账号可在“资源中心 → 连接”统一维护，密码使用 AES-256-GCM 加密且 API 只返回“已配置”状态；自定义 mirror、repository 和 server 仍可通过连接的 `settings_path` 指向 Runner 上的 `settings.xml`。确需把 token 作为属性传入时，只允许使用 `repo.token=${env.YUNXIAO_TOKEN}` 形式引用变量组，流水线记录不会展开真实值。

Java 应用也可选择“Gradle（结构化参数）”：默认 `auto` 会优先使用项目 `./gradlew`，Wrapper 不存在时回退到 Runner PATH 中的 `gradle`，随后执行 `--no-daemon --console=plain clean build`，产物匹配 `build/libs/*.jar`；可配置多模块任务（如 `:app:bootJar`）、排除任务（`test`）、`-P` 项目属性和 `-D` 系统属性，并可在每次构建前覆盖。Gradle 项目使用依赖仓库连接时，平台注入 `GRADLE_REPOSITORY_URL`、`GRADLE_REPOSITORY_USERNAME`、`GRADLE_REPOSITORY_PASSWORD`，由项目的 `build.gradle` / `build.gradle.kts` 明确读取。敏感参数同样只允许 `${env.NAME}` 变量引用。若项目没有 Wrapper 且 Runner 未安装对应工具，流水线会明确失败并提示需要配置 Runner，而不会静默改用其他版本。

测试与生产发布仍然复用同一个应用制品，不能分别使用 `-Ptest`、`-Pprod` 重新构建。Spring Profile、数据库地址等环境差异应在启动命令或外部配置中心注入。跨控制面共享应用交付物时，后续接云效通用制品库/OSS；容器应用使用镜像仓库。

### 制品与部署 Driver

流水线构建完成后会生成统一的不可变 `Artifact`。JAR 和前端包保存本地路径、SHA-256 和大小；Docker 镜像保存 `registry/repository@sha256:...`。流水线产物同时进入构建历史，之后按 artifact ID 发布和回滚，不需要重新构建。

内置部署 Driver：

- `ssh-jar` 接收 `jar`，负责上传、启停、目标主机健康检查和回滚；
- `ssh-static` 接收 `static-archive`，解压到版本目录后原子切换 `current` 软链接；
- `docker-host` 只接收已推送的 `oci-image` digest，通过 SSH 在目标机执行 pull/run/验证，失败恢复旧镜像。

Docker 构建使用 `docker buildx build --push`。应用绑定镜像仓库连接后，Runner 会在推送前登录；目标主机仍需具备该仓库的 pull 权限。镜像 tag 只用于构建和展示，部署阶段强制使用 digest。

无镜像仓库时，在应用设置 → 构建与制品选择“镜像文件”，或将高级流水线 `docker-build` 的 `PUSH` 设为 `false`。此时 buildx 使用 `--load`，随后 `docker image save` 导出本次运行目录下的 `docker-image.tar`，注册为 `docker-image-archive` 制品。文件 SHA-256、大小、镜像 ID、OS/架构随制品保存；标签不是执行身份。Java 项目仍须在 Docker 步骤之前添加 JAR 构建，或由 Dockerfile 完成多阶段构建。

Dockerfile 支持“基础模板、仓库文件、自定义 Dockerfile”三种来源。在应用设置 → 构建与制品中选择；新建镜像应用和新增 Docker 构建步骤默认使用 Java 21 JAR 模板，也可切换 Nginx 静态站点模板。模板只需配置相对构建上下文的产物路径（默认 `app.jar` / `dist`），Java 容器端口为 8888，Nginx 为 80；Maven / Gradle / Node 前置打包仍需配置。特殊依赖才使用自定义内容。既有配置继续默认读取仓库 `Dockerfile`，不会自动改用模板。

高级流水线参数为 `DOCKERFILE_SOURCE=template|repository|platform`；模板参数为 `DOCKERFILE_TEMPLATE=java21|nginx`、`DOCKERFILE_ARTIFACT_PATH`，仓库使用 `DOCKERFILE` 路径，自定义使用 `DOCKERFILE_CONTENT`。自定义内容最多 32 KiB。模板和自定义内容均通过标准输入传给 buildx，保留项目上下文及 `.dockerignore`，不改仓库文件。模板版本 1、参数及具体文本进入加密构建快照；原构建重试使用原模板版本/参数或自定义内容。制品元数据记录来源及内容 SHA-256。该摘要不是基础镜像或远程依赖的锁定：可变基础镜像和网络下载仍需单独固定版本/digest。Dockerfile 不可写入密钥；保存配置不执行构建。切换来源只改变新构建使用的来源，不删除另一来源的草稿。

镜像文件只支持 Docker 主机，不支持 SAE；单次导出一个 Linux 平台，不支持多平台索引及带 variant 的目标。目标机须有 Docker、`sha256sum` 和 `mktemp`，SSH 用户需有执行及上传权限，临时目录须有足够磁盘空间。发布先检查 Client/Server/API 与所需命令参数、目标 Docker 架构，再上传文件、校验 SHA-256、执行 `docker image load`、核对镜像 ID/架构；全部通过后才停止旧容器，按已导入的不可变镜像 ID 启动，不依赖旧 CLI 不支持的 `run --pull` 参数。构建机另须具备可用 Buildx，并提前准备基础镜像和构建依赖。能力要求、验证边界与升级步骤见 [Docker 兼容与升级](deploy/DOCKER-COMPATIBILITY.md)。

Docker 两种交付方式都通过保留原容器完成失败恢复，保留原环境变量、挂载、网络及镜像，不根据新配置重建旧版本。成功后清理已替换的旧容器；旧镜像和本地制品不自动删除。远端临时上传文件会清理；清理失败会明确告警。首次 JAR → Docker 迁移仍须人工停止旧 JAR，平台不会替用户停止未纳管进程。容器健康检查不等于 BGE 模型加载成功，模型须另行验证。

### 交付触发与资源中心

- 应用可绑定 Git/Maven/镜像仓库连接、变量组和构建 Runner；Git 支持 HTTPS/PAT 与 SSH 私钥两种认证，Maven、Registry 凭据与变量也统一加密保存，运行时才注入对应步骤。
- Git SSH 凭据在“资源中心 → 连接”中填写服务地址、SSH 用户（代码托管通常为 `git`）、私钥及可选口令，并通过“获取指纹”核对服务端身份。构建时平台把私钥和 `known_hosts` 写入权限受限的临时目录，强制开启主机校验，拉取结束后立即删除；私钥不会依赖或写入平台账号的 `~/.ssh`，也不会传入后续构建容器。
- 仓库 URL 与凭据模式必须一致：`https://...` 选择 HTTPS/PAT，`git@host:group/repo.git` 或 `ssh://...` 选择 SSH 私钥。认证方式、主机指纹、私钥授权和仓库权限错误会分别给出可操作提示。
- `POST /api/hooks/{service}/{token}` 可触发 webhook 构建；也可按应用配置分钟级定时构建。两者都能在构建成功后自动创建目标环境发布单，受保护环境仍必须审批。
- 构建和发布解耦：JAR、前端压缩包和 OCI 镜像都先形成不可变制品，再由同一套发布单提升到不同环境。
- 插件中心展示构建 Step 与部署 Driver 的契约、输入输出、能力和参数。当前内置 JAR、前端静态包和 Docker 主机链路；Kubernetes 通过新 Driver 扩展，本版不声明已支持。
- 旧版本按主机保存的部署历史会在升级时自动归并为兼容发布单，迁移可重复执行且不会制造重复记录。

OTLP 指标和 Trace 使用资源属性区分环境，服务名可以保持一致：

```text
OTEL_SERVICE_NAME=order-api
OTEL_RESOURCE_ATTRIBUTES=deployment.environment.name=test,service.instance.id=order-api-test-01
```

接收端同时兼容 `deployment.environment` 和 `service.environment`；实例优先读取 `service.instance.id`，并兼容 `host.id` / `host.name`。实例 ID 必须在同一服务环境内唯一且在进程生命周期内稳定，Kubernetes 推荐 Pod UID，虚机推荐主机名加进程名。未携带环境或实例的历史数据分别归入空环境/空实例，只出现在相应的“全部”视图。

## 安全配置

- 建议始终通过 `AUTH=... ./scripts/start-all.sh` 启用控制台登录；首次启动自动创建 `admin`，之后通过成员管理维护账号。Cookie 为 HttpOnly、SameSite=Strict，TLS 下自动启用 Secure。
- 平台只内置“管理员”和“成员”两个角色；公共资源与平台配置仅管理员可修改，成员可构建和发布。发布审核人员按环境单独配置，且不能审批自己发起的发布单。
- 所有浏览器写请求和 WebSocket 都校验同源。
- 主机密码以 AES-256-GCM 加密保存，API 永不返回密码。默认密钥位于 deploy 数据目录的 `.credential-key`（权限 `0600`）。生产建议设置稳定的 `LOGPLATFORM_CREDENTIAL_KEY`，值为 base64 编码的 32 字节随机密钥，并与数据库一起备份。
- 仓库凭据和变量组仅在构建运行时注入；平台会对实时输出和落库构建日志中的已绑定密钥做逐运行脱敏。
- SSH 必须校验主机身份。在“资源中心 → 主机”填写地址后可点“获取指纹”，与服务器侧指纹核对并点“确认并使用”；也可手动填写 `SHA256:...`，或预先维护运行账号的 `~/.ssh/known_hosts`。平台不会自动信任扫描结果。
- 管理面仅监听 `127.0.0.1`，对外只暴露平台网关；数据面使用平台统一的共享 token。Token 不写入 Agent 期望配置数据库，只在安装或下发时写入目标机的 `0600` 运行配置。

生成凭据密钥示例：

```bash
openssl rand -base64 32
```

## 已实现

- 采集：glob 发现（10s）、活跃文件长期持有 FD、EOF 250ms～2s 自适应等待、多行合并（行首正则）、单条 256KB 截断
- 断点：inode+offset+mtime+文件头签名；事件先进入本地 SQLite WAL 再推进断点，Agent 重启优先重放未确认事件
- 轮转：copytruncate 回到文件头；rename 后旧 FD 排空并等待尾部写入，再切换同路径的新 inode
- 发送：500 条/1MB 攒批、快速 gzip、keep-alive 单连接、抖动指数退避、2MB/s 限速；服务端 event_id 幂等并返回批次 ACK
- 背压：内存队列最坏约 16MB；本地 WAL 默认上限 1GB，满后暂停追读而不是静默丢弃（`spool_max_mb` 可调）
- 存储：SQLite WAL、按天分表（logs_YYYYMMDD + FTS5 + 触发器，事务原子建表）、过期 DROP、磁盘水位
- 查询：FTS5 全文（短语转义）、trace_id 点查、level/traceId 入库提取（JSON 优先正则兜底）、游标分页
- 环境与实例：日志按 environment 隔离；OTLP metrics、接口分位/可用率、Trace 和监控告警进一步按 service.instance.id 隔离并支持服务级聚合
- 监控：日志分钟指标、机器指标、OTLP metrics、接口分位/可用率、Trace、报警与 webhook
- 发布：步骤化流水线、资源连接/变量组/Runner、webhook/定时触发、不可变制品库、审批发布单、环境主机组、分批滚动、健康检查、阈值熔断、整单回滚和审计
- 可靠性：单服务并发锁、重启恢复、事件投递重试、总线不可用时分钟指标保留并重试
- 资源：SetMemoryLimit(50MB) + SetGCPercent(30)；Agent 状态上报内存队列深度和 WAL 待发送字节数

## 交付平台 V1（隔离开发分支）

`feature/delivery-platform-v1` 将部署插件分成三个入口：基础构建、部署编排、变更管控。应用与资源仍复用平台公共中心；上下文同步不会把用户从版本或编排编辑页跳回单应用详情。

### 已接通的流程

1. 在「变更管控 → 需求与 BUG」新建自定义需求、手工关联外部编号，或同步禅道开源 16.0 的 Bug。
2. 在「独立变更」登记代码分支、制品、SQL 或人工操作；可关联需求，也可不关联。需求、变更和版本不绑定环境。
3. 将变更标记就绪，在「发布版本」按需求自动加入关联的已就绪变更，或直接选择变更；人工检查最终范围，标记版本就绪。创建版本不会直接合并代码、构建或执行变更。
4. 新建编排模板：固定服务，或者由发布版本中的代码/制品变更推导服务。节点按顺序执行，可增删、排序；某部署节点不指定应用表示发布范围内所有应用。同一应用只能安排一次。
5. 发起交付时选择环境、版本和每应用一个候选制品，并检查范围与配置。候选制品必须显式选择，不自动使用最新构建；多个 BUG 关联同一应用时只发布一次。
6. 提交执行单冻结版本、变更、节点、制品摘要及目标配置；应用环境变量保留加密快照，接口只显示变量名。生产审核人员来自环境配置，发起人不能自审，管理员角色不绕过审核人名单。
7. 审核后显式推进每个节点：应用节点创建真实子发布单，复用现有发布驱动、健康检查和分批策略；人工节点需要显式确认。子发布成功后核对结果再推进下一个应用，最后确认编排完成。失败中止后续节点，不自动回滚其他已成功应用。

提交和审核不会自动部署。执行前重新校验目标配置、环境变量、主机/云资源地址和制品摘要；变更后需重新生成执行单。重复点击使用数据库事务与修订号防重；同一应用环境的普通发布和编排发布共用发布占用检查。页面刷新不会自动恢复或重复执行节点。

### 本版的明确边界

- SQL：结构化配置、表/字段/索引预检、环境资源绑定、审批快照及实际执行已接通；支持范围与限制见下文。敏感数据不要写入公开变更说明或 SQL 字面量。
- 数据同步：表格与树状菜单对比、编辑差异、生成冻结 SQL 草稿并复用 SQL 审批执行已接通，详见下方「数据对比与菜单迁移」。
- 禅道 16.0：Session 登录、手动/定时只读 Bug 同步、日期基线及按连接实例去重已接通。需求/用户同步、自动「未上线」判定及上线回写仍待接通，不将关闭状态误当上线状态。
- 代码：可在交付准备页面按所选 Bug／版本冻结源提交，合并目标分支并按合并提交构建；也保留人工核对既有制品的方式。详见下方「代码准备与冲突恢复」。
- 编排：顺序、逐节点推进；尚无并行 DAG、自动调度、执行中改节点、跨应用自动回滚或全局补偿。
- 数据同步不自动删除目标数据，不同步 Redis，也不自动迁移菜单之外的角色授权关系。
- Splunk 历史日志查询现通过独立能力插件接入，通用协议、打包与热启停见 [插件协议](deploy/PLUGIN-PROTOCOL.md)；真实 6.6.3 实例联调尚未完成。其他工作项来源的插件化仍为后续范围。
- 新增元数据支持 SQLite 开发库和 MySQL 8，并已加入迁移清单；SQL 执行器用独立本地 MySQL 8 验证，不等同于业务库/生产主机联调。

### 数据库工作台只读查询

查询页支持两种目标选择方式：「应用资源」按应用、逻辑资源和环境解析连接；「数据库连接」直接选择资源中心已有的 MySQL 连接，不要求关联应用。目标环境用于读取查询审批设置，实际数据库由所选连接固定，页面会显示实际连接名和数据库。直接连接的查询也支持审批申请；执行前重新校验连接及审核人，连接或凭据变化后需要重新申请。

同一数据库内可手写 `JOIN`、`LEFT JOIN`、`RIGHT JOIN`，支持表别名、限定字段和 `表别名.*`，例如：

```sql
SELECT o.id AS order_id, c.name AS customer_name
FROM orders o LEFT JOIN customers c ON o.customer_id = c.id
ORDER BY o.id DESC LIMIT 200;
```

重名结果列请使用不同 `AS` 别名。查询仍限制为一条只读语句：默认 200 行、最多 1000 行 / 2 MiB、30 秒超时，禁用跨库、子查询、锁及任意函数。支持 HAVING、CASE、CAST、COALESCE / IFNULL、CONCAT、大小写 / 子串、ROUND / ABS、DATE / DATE_FORMAT、NOW / DATE_SUB 等常用只读表达式；未知函数、文件读取和锁函数仍拒绝。`SHOW TABLES`、`DESCRIBE 表名`、`SHOW INDEX FROM 表名` 可读取表列表、结构与索引。SQL 变更与数据同步仍通过应用资源绑定执行；直接连接查询不会授权写操作。

### 数据对比与菜单迁移

入口：「数据库工作台 → 数据对比同步」，交付内容中也可复用同一任务。任务保存查询与字段规则，每次对比单独选择来源、目标环境；两侧仍需绑定应用的 MySQL 资源。旧任务默认按表格处理。

- **表格数据**：按目标主键或唯一键匹配，左侧按行展示新增/更新，右侧逐字段对照目标当前值与迁移后值；可编辑非键字段及 NULL。
- **树状层级（菜单）**：配置 ID、父 ID、名称字段与根节点父 ID（默认 `0`，NULL 也视为根），使用菜单编码、权限标识等稳定字段匹配；目录节点也必须有稳定且非空的匹配值，可组合多个字段。左右数据库中的菜单 ID 可以不同。左侧显示树形差异及祖先、支持折叠，右侧核对字段；已有菜单保留目标 ID，新增菜单从目标整表最大 ID 后分配整数 ID，再转换父 ID。不会复制来源的物理 ID；ID、父 ID 映射不能手改。
- 选择新增子菜单自动带上需要新增的父节点；取消新增父节点同时移除依赖子节点。后端重新验证最终目标树，包括未选菜单，拒绝循环、孤立节点、重复 ID/匹配值及整数溢出。查询范围必须包含完整祖先链。树形模式目前要求 ID 为整数主键/唯一键，父 ID 为整数；不自动调整其他表中引用菜单 ID 的字段。
- 仅支持单表、显式原始字段的 `SELECT ... WHERE ...`；两侧字段及顺序一致，每侧最多 1000 行 / 2 MiB。每份草稿 1–50 行（含依赖父节点）；目标独有数据保留。生成前重新对比，脚本按父先子后排序并冻结；审批后若数据/结构/绑定变化、ID 被并发占用，则阻止或事务回滚，不覆盖冲突或自动重试。
- 草稿不执行 SQL；后续沿用既有 SQL 预检、审核和单库 InnoDB 事务。树形执行会在事务内锁定并核对完整对比范围，防止未选父节点在审批后被改动；提交前再核对实际层级及所选节点是否仍在目标查询范围。目标 SQL 建议按稳定业务范围筛选，避免新分配 ID 超出原 ID 范围而被回滚。

验证：`go test ./internal/deploy -run TestDataSync`；`scripts/test-data-sync-tree-ui.mjs` 为全拦截 UI 验证，无真实业务写入。真实 MySQL 测试需显式设置回环地址的 `DEVOPS_SQL_TEST_ADDR`，只访问可丢弃的 `delivery_qa` / `delivery_sync_source_qa` 测试库。

### 数据库与 Redis 浏览面板

查询页采用固定左右工作区：左侧对象导航，右侧通过「数据浏览 / SQL 查询」或「Key 浏览 / 命令控制台」切换。SQL 与结果在宽屏并排显示，窄屏在工作区内部显示；导航、编辑器、结果分别滚动，不在整页底部堆叠。SQL 草稿与结果和对象浏览状态独立保存；新查询保留同一连接的表目录。两个工作台都支持直接选择资源连接，不要求关联应用；写操作仍需应用资源绑定。

- 数据库：显式加载表列表（最多 1000 张），本地筛选表名；点击表后按需读取结构与 100 行数据，已有主键时按主键排序。结构、索引分别读取；数据区支持字段条件（AND / OR）、直接 WHERE 条件、排序、选择列和每页行数，应用后分页沿用已执行条件；表头点击可切换排序。数字条件保留原始数值文本，字符串在后端绑定参数；不执行全表 COUNT。快捷分页偏移最多 10000，无主键或数据并发变化时分页不是一致性快照，深分页使用 SQL 主键范围查询。
- Redis：打开页面不扫描；搜索 / 加载更多每次只执行一条 `SCAN ... COUNT 50`，自动保留游标、去重，不为填满列表连续扫描。空批次不代表结束，游标 `0` 才结束；最多展示 1000 个 Key / 2 MiB，到上限停止扫描并提示结果不完整。前缀过滤不是索引，界面不查询匹配总数。
- 点击 Key 才读类型、TTL 与内容：String 预览最多 64 KiB；Hash / Set / ZSet 用游标扫描，List 每批最多 50 项，累计最多 1000 项 / 2 MiB。COUNT 不是硬数量上限，后端保留 RESP 响应的数组、单值与总字节限制；超大集合响应拒绝返回。浏览请求串行并间隔至少 250 ms，Redis 查询整体 5 秒超时、不重试。
- 每个页签独立保存浏览状态，切换连接或环境清空结果；过期 Key 显示不存在。新建过期 / 清理草稿不执行 Redis 写命令。查询审批开启时，表浏览、扫描和内容读取也逐次申请；批准后的结果回到原浏览页签（原目标未变化时），不会自动执行下一次查询。

验证：`go test ./internal/deploy -run TestWorkbench`，`scripts/test-resource-browser-ui.mjs`（全部业务请求拦截，不写真实数据库），覆盖空批次、重复项、游标、上限、五种 Redis 类型、结构 / 索引、页签和目标隔离、审批恢复及桌面 / 移动布局。

### 禅道 16.0 与 SQL 首批接通

1. 在资源中心创建 `zentao` 连接：安装根地址（包含实际 `/zentao` 子路径）、专用账号密码、产品 ID 列表、日期基线 `YYYY-MM-DD`，以及同步周期（分钟；`0` 关闭自动同步）。账号只授予目标产品 Bug 查看权限，密码加密保存，平台维护 Cookie Session。现阶段按产品同步 Bug，不按项目同步需求；更高版本接口尚未适配。
2. 在「需求与 BUG → 同步禅道 BUG」选择连接，必要时覆盖本次日期基线，点击同步。按编辑/关闭日期过滤，分页完整读取后在同一事务中更新需求池；失败不保存半份结果。定时同步每分钟检查一次，到期仅执行读取。外部身份为连接实例 + 类型 + Bug ID；同期版本/变更关联不会丢失。
3. 在资源中心创建 `mysql` 连接：地址 `host:port`、数据库、最小权限账号密码、TLS 设置。每个环境使用独立连接；不接受含密码 DSN。TLS `true` 校验证书，内网 `false` 是明文连接。
4. 在「变更管控 → 应用资源绑定」声明 `business-db` 等 MySQL 别名，并为各环境选择实际连接；数据库在连接中固定，绑定不覆盖数据库。此公共配置操作需要管理员权限。
5. 新建 SQL 变更，选择应用，填写资源别名、SQL 内容、允许影响行数（1–10000），先保存草稿，再点卡片上的「SQL 预检」选择目标环境。预检只读结构及 DML 影响范围，不试执行 DDL。
6. 就绪变更加入版本，在版本模式编排中安排一个 SQL 节点。不选择具体变更时取版本内全部 SQL；可显式选择，但每个版本 SQL 只能安排一次。提交和审批不执行 SQL；在执行单详情中显式推进节点才开始执行。

SQL 当前支持 `CREATE TABLE`（简单字段、独立主键/索引，固定 InnoDB/utf8mb4）、每条 `ALTER TABLE ... ADD COLUMN`、`CREATE [UNIQUE] INDEX`、单表 `INSERT ... VALUES`、带字段/字面量比较和 AND/OR 的 `UPDATE` / `DELETE`。字符串用单引号，不支持注释、反斜线转义、函数、子查询、JOIN、跨库、REPLACE、ON DUPLICATE、危险 DDL 或任意管理员 SQL。未支持的语法明确阻止，不悄悄忽略。

预检按语句顺序模拟 DDL 结构变化：先建表再加字段/索引可在一个变更中执行；对象已存在且定义一致才跳过，不一致阻止。审批准入时冻结实际连接身份、资源绑定、SQL 内容、结构基线及 DML 目标数据摘要。执行前检查变化，DML 锁定目标行并在单个事务中执行，失败回滚；DDL 回读验证最终结构，但隐式提交不能统一回滚。每个 SQL 节点一个数据库，DDL/DML 不混用；同一连接只支持一个 SQL 节点，同一表每节点限一条 DML（批量插入可用多行 VALUES）。DDL 与 DML 对同库需要分次交付，待后续实现跨节点预期基线。

执行前保留编排修订号与 SQL 台账，并使用目标 MySQL 会话级锁协调执行。同一个连接/数据库/变更不自动重跑；失败或中断需先人工核对，再创建明确的修复变更。MySQL 外部写入和平台台账没有分布式事务，提交返回异常、DDL 部分成功、台账保存失败不能当作安全重试。保留数据库与 `.credential-key`，不要通过删除执行记录绕过防重。已有 Bug 同步或 SQL 台账的连接不能删除或更换目标地址/数据库，避免旧身份被新服务复用；账号密码仍可轮换。

接口补充：`GET /api/issue-sync`、`POST /api/issue-sync/{connection}`（可带 `baseline`）、`POST /api/changes/{id}/sql-preview`（带 `environment`）、`GET /api/sql-executions`。SQL 执行仅通过已有编排执行单 `advance`，没有绕过审批的直接执行接口。

SQL 实库测试要求本地一次性 MySQL 的 `delivery_qa` 库，指定 `DEVOPS_SQL_TEST_ADDR=127.0.0.1:端口` 与 `DEVOPS_SQL_TEST_PASSWORD`，运行 `go test ./internal/deploy -run TestSQLMySQL -v`。测试拒绝非 loopback 地址，并仅清理自己创建的 `sql_qa_*` 表，不使用业务数据库。

### 资源组与多资源交付

资源中心的「资源组」集中声明逻辑别名（MySQL／Nacos／Redis）及每个环境的实际连接；不保存密码或私钥。管理员在应用资源设置中选择一个可复用资源组，多个应用可以使用同一组。变更继续选择组内逻辑资源别名，不绑定环境；发起交付时按目标环境解析。已有应用独立绑定仍可用，解除资源组时可以保存当前成员为应用独立配置。

组编辑采用修订号及事务保护，避免覆盖并发修改；被应用使用的组不能删除已有逻辑别名或改变类型，被引用的组／连接／环境不能直接删除。组配置变更会影响后续预检；旧执行单保留原目标快照和组修订，执行前核对实际绑定。无有效绑定时阻止，不回退到其他环境或旧应用配置。资源组最多 30 个逻辑资源、300 个绑定、60 KiB 配置；组只负责连接解析，不改变审批权限。

一个 SQL／Nacos／Redis 逻辑编排节点可引用多个应用的变更。预检按实际目标拆成顺序执行节点，并显示「变更、应用、逻辑别名、资源组修订、实际目标、执行节点」：
- SQL 按数据库连接聚合；不同数据库自动拆分。同一个实际数据库（含不同连接别名）仍不允许多个会写入的 SQL 节点，以免前序操作破坏后序预检基线。
- Nacos 按连接＋Group＋Data ID 拆分；环境绑定的 Data ID／Group 优先。对同一配置文档的多项变更须先合成一项，平台不擅自决定冲突键的值。
- Redis 按连接及 DB 拆分。Redis 写操作仍禁止盲目重试。
- 审核与人工卡点的位置不变；目标按原逻辑节点中首次出现的顺序展开，每项变更只分配一次，最多 200 个执行节点。拆分不是跨数据库／配置文档事务：失败停止，已成功节点不会自动撤销。
- SQL 成功跳过仍校验原台账和批准快照；Nacos 成功跳过还需当前配置项一致。DDL／DML 混用、同表复杂连续 DML、缺失绑定、同文档冲突等原安全限制保留，不因拆分而放行。

接口：`GET /api/resource-groups`、`POST /api/resource-groups`（创建或带 `revision` 编辑）、`DELETE /api/resource-groups/{name}`；应用资源接口支持 `resource_group`、`group_revision`。组和应用绑定修改需管理员，查询／变更审核仍沿用各工作台与环境策略。

测试：`TestResourceGroup*`、`TestDeliveryMultipleResource*`、`TestNacosGroup*` 使用临时元数据库和模拟配置中心；`TestSQLMySQLResourceGroupMultipleDatabases` 在一次性 loopback MySQL 上验证两个数据库的 DDL、审批及跳过，须同时设置 `DEVOPS_RESOURCE_GROUP_SQL_TEST=1`，并准备仅用于测试的 `delivery_qa` 和 `delivery_qa_groups` 库。`scripts/test-resource-groups-ui.mjs` 模拟全部业务写请求，验证复用、组编辑和工作台草稿连续性。

### 代码准备与冲突恢复

从工作项直接发起交付，或选择发布版本，进入「核对制品与步骤 → 代码准备」。保存需求、变更和版本不会推送代码；必须先检查来源，再明确点击「合并并构建，不部署」。

- 每个应用指定分支策略：开发／测试可使用已存在的常驻目标分支；受保护环境只能从明确生产基线准备新的 `release/` 或 `hotfix/` 分支。系统不猜测 dev/main 分支。Git 凭据沿用应用构建连接，推送还需要仓库写权限及分支保护授权。
- 同仓库同目标分支聚合源分支；冻结源提交、基线、目标提交和构建配置摘要，一个应用只构建一次。同仓库目标使用持久化互斥锁，不强推，不修改研发本地工作区。
- 冲突会暂停且不推送。研发本地完成真实合并并推送后，点「核对合并结果并恢复」；平台检查冻结基线及所有冻结源提交的祖先关系。squash／cherry-pick 不能替代这项历史证明。独立发布分支还拦截额外未选中的非合并提交。
- 不能自动证明源分支上的所有提交属于所选 Bug；生产操作必须人工核对源范围，混合分支应先整理成独立补丁分支。平台暂停不代表已经撤回推送，取消也不会自动回退远程分支。
- 合并确认后按完整提交 SHA 构建。检查源码、后续步骤工作区 HEAD、构建快照、制品摘要与来源；制品自动关联交付草稿／版本。已锁定版本复用原制品，新增代码需新建版本。
- 任务状态持久化，当前窗口刷新后可恢复。重启或请求结果不确定时不自动重推；明确核对后恢复，已创建的构建通过唯一任务来源关联，不自动重复创建。失败／中断的构建不盲目重跑。
- 自动准备目前支持标准 Git 拉取步骤（自定义流水线须恰有一个 `git-checkout`）、最多 10 个仓库目标、20 个应用和 100 个代码变更。包含源码修改前置命令或混合手工制品引用的范围须人工处理。
- 代码准备不执行部署、SQL、Nacos 或 Redis。准备就绪后仍需交付预检与审核；执行单冻结来源、制品和目标。SQL／Nacos 按各自成功证据与目标状态跳过已执行的变更，不能用代码准备替代配置变更审批。

API：`POST /api/delivery/code-preparations/preview`（`delivery`＋各应用 `policies`），`GET /api/delivery/code-preparations/{id}`，`POST .../{id}/start|resume|cancel`（明确 `confirm`，生产开始／恢复须 `confirm_scope`）。交付请求可携带 `code_preparation_id`，服务端校验来源及每应用制品，不仅信任前端确认。

测试：`go test ./internal/deploy -run TestCodePreparation` 的 Git 推送仅操作一次性临时裸仓库；`scripts/test-code-preparation-ui.mjs` 模拟全部业务 API，验证冲突暂停、刷新恢复、制品关联及提交不部署，不连接真实仓库／数据库／配置中心。

### 本地隔离预览

在该分支工作区执行；不会启动或停止原来的 `9800` 服务，也不复用其数据库。首次预览使用独立空数据。

```bash
cd web
npm ci
cd ..
./scripts/build-web.sh
GO_BIN=/Users/zhouzheng/sdk/go/bin/go ./scripts/preview-delivery.sh
```

访问 `http://127.0.0.1:19813/ui#/changes` 或 `http://127.0.0.1:19813/ui#/orchestration`。该地址是本地独立插件预览，不包含平台壳、完整公共中心或登录；提供资源连接和应用环境资源绑定弹窗。正式联调使用平台网关。默认数据在工作区 `runtime/delivery-preview`，Ctrl+C 仅停止预览。不要把这个管理端口对外暴露。

新接口：`/api/work-items`、`/api/changes`、`/api/release-versions`；编排接口为 `/api/delivery/templates`、`/api/delivery/preview`、`/api/delivery/runs`，执行单支持 `approve`、`reject`、`advance`、`cancel`。审核需提交 `digest`，人工推进需提交 `confirm_manual: true`、当前 `node_id` 和 `digest`，可附 `comment` 留痕；重复确认不会自动确认下一项。运行中的子发布单通过原 `/api/releases/{id}` 接口查询与控制。

研发工作项的“解决”在原列表弹窗内完成，保留筛选和页码；“关联变更”复用交付编辑器，允许一次填写代码、SQL、Nacos、Redis 和自定义记录。提交先保存本地变更草稿，再调用来源解决操作；本地保存失败不会解决 Bug，来源结果不确定只核对回执、不重放写请求。两次保存不是跨系统事务：若草稿保存后来源失败，草稿保留供后续继续。

自定义记录（`manual`）是平台未覆盖操作的纯文本备忘，不作为脚本执行，应用可不选。标准交付和自定义编排按所选变更生成冻结的人工处理清单，未选记录不进入本次清单；提交后当前人工节点进入交付负责人个人待办，显式确认后继续流程，保留处理人、时间及说明。发布快照不随后续新增记录扩张，变更内容请在就绪前修改。

发布版本、交付执行单及单应用发布单的详情提供“发布说明 / 通知文案”。展开后读取已有说明，没有保存过则按当前记录的 Bug 范围生成预览：保留发版快照标题，读取来源 `feedbackBy`，每条带 Bug 编号及 `@反馈者`。不会用创建人、解决人替代缺失反馈者；权限不足、读取失败和缺失字段分别提示。单应用子发布单使用冻结父交付范围，独立发布未关联 Bug 时仅提供手工说明，不反查当前应用的所有 Bug。

说明保存到 `release_notices`，兼容 SQLite / MySQL 及元数据迁移；按记录类型、ID、修订号做并发控制，与审批摘要和执行状态隔离。范围或状态变化后旧文案保留并标记待核对。生成最多读取 100 个 Bug、整体限时 20 秒，无 Bug 写回、无自动通知发送；复制是纯文本 `@`，不保证钉钉／企微／飞书等渠道的真实提醒，后续接渠道时需单独映射账号及设计发送幂等。

## 生产部署建议

Linux 首次安装、版本化升级和自动回滚统一使用 `scripts/release-linux.sh` 与 `deploy/devopsplatformctl`，完整步骤见 [`deploy/README.md`](deploy/README.md)。

- `test` 与 `prod` 使用不同主机账号、目录和最小权限 SSH key；不要复用生产密码。
- 平台数据目录、deploy 数据目录和 `.credential-key` 一起备份；只备份数据库而丢失密钥将无法解密已有密码。
- 平台前置 TLS 反向代理，只开放 `9800`；`19801/19802/19803` 保持 loopback。
- 日志、OTLP 指标、Trace 和告警严格按环境隔离；通用机器指标仍按 agent/主机查看，建议 agent_id/hostname 保持环境内唯一并带环境前缀。
- 不共享管理员密码：为每个人创建独立成员账号，需要维护平台配置时再明确授予管理员角色。后续接入 SSO 时仍映射到这两个内置角色，不引入复杂权限树。

### 研发工作台与版本准备流程

2026-10-13 修订：研发页统一复用公共 PageHeader，个人“我的工作项”是同页页签而非普通链接；增加人员快捷视图及按状态提供的行内操作。级别、优先级和解决方案同时保留原始编码/来源标签，列表与候选支持筛选排序；管理员可在设置中维护已核实的现场字典，未知枚举保留原码。历史遗漏字段仅通过显式“更新本页来源字段”或后续同步补齐，失败不覆盖已有值；没有自动请求或改写真实 Bug。

“准备流程”入口收进设置中的交付流程配置，日常由工作项发起交付进入匹配的前置流程，完成后继续部署；支持单/多工作项范围传递。待上线视图要求选择核对环境，使用不可变成功交付证据；前置确认/构建完成不算已上线。来源账号验证后可返回原工作项。

**禅道数据库直读未接通**：用户要求是直连数据库读取，当前连接不可达。本项已在 [UI 交互验收待办](deploy/UI-INTERACTION-ACCEPTANCE.md) 中明确记录 schema/现场字典、只读账号、分页及权限范围验收，现阶段列表仍使用来源缓存，不能宣称为数据库实时直读；业务写操作继续使用本人原生来源接口。

`research` 是独立可见插件，复用隐藏的 `delivery-core`、工作项、变更与版本数据，不依赖可见部署插件启用。导航包含个人工作台、工作项、准备流程、本人来源账号；详情与交付内容使用全页/内联编辑，返回保留列表筛选，切换详情页签保留交付草稿。

平台自建需求与 Bug 使用统一工作项记录，支持平台成员负责人、优先级（Bug 另含严重程度）、Markdown 描述、图片附件、评论和活动历史。描述模板引导需求填写背景/目标、用户场景和验收标准；Bug 填写环境/版本、前置条件、复现步骤及预期/实际结果。状态为待处理、处理中、已完成（Bug 显示待回归）和已关闭；创建人、负责人或管理员可在详情页流转，使用读取时的更新时间和原状态做并发校验。负责人可更新状态，内容维护仍限创建人或管理员。外部来源工作项继续由各来源平台维护状态，不混入平台状态流转。

“与我相关的待上线”支持直接“标记已上线”：确认后记录人工上线人、时间和可选说明，并从待上线列表移除。“全部”列表显示“已上线（人工）”，可在“更多”中撤销；详情保留活动历史。相关成员和管理员可维护，沿用来源读取权限；人工标记独立于平台交付执行证据，不改来源状态或上线意图，也不执行发布。标记持续保留，后续需要继续跟踪上线时可撤销。

- 人员职能可多选前端、后端、测试、产品，只有标签作用，系统权限仍为管理员/成员。
- 禅道 16.0 的指派、解决、关闭、激活、编辑、评论通过本人登录的原生控制器回写，保留原生历史、附件和钩子。不直接修改禅道数据库，不使用公共同步账号代替操作人。先启用平台登录，再在研发工作台验证本人来源账号；密码加密保存，来源地址变化后需重新验证。
- 解决时必须判定是否需要上线；不需要上线须说明原因。该判定保存在平台，并在本次禅道解决评论中注明。不需要上线的工作项不能纳入版本；历史未判定记录允许作为候选并显示待判定。已入版的工作项需先移出版本才能取消上线。
- 版本可选择准备流程及环境，创建只生成冻结执行记录和待办。可配置代码合入/构建、人工合入核对、解决人确认、指定人员确认、审批节点；顺序及或签/会签由模板决定，不按人员职能硬编码。每个人只确认分配给自己的工作项，可以部分或批量确认。发起人不能审批自己的申请，确认自己的交付内容不等同自审。
- 准备范围和审核内容变化后原确认失效；未完成选定流程的版本不能就绪或交付。代码节点需负责人显式启动，内部复用冻结分支合并/冲突恢复机制；人工审批不会自动部署或执行 SQL/Nacos/Redis。自动准备覆盖全部代码应用后才锁定完整版本制品，混合/人工流程仍需明确关联制品。
- 个人待办聚合准备确认、分阶段审批、代码冲突、现有编排审批/人工卡点、数据库/Redis 查询审批。旧单应用发布的“审批即启动”与批次观察保持跳转原发布单，不在工作台中伪装成无副作用审批。
- 来源回写使用持久化操作编号；响应不确定时仅核对操作历史，不重放写操作。重新打开工作项可读取本人尚未核实的操作。来源图片必须是当前 Bug 的原生附件，显式点击后通过个人会话读取；不按描述中的任意 URL 授权代理。

准备流程沿用现有执行单和审核审计存储，新增元数据与个人凭据表同时支持 SQLite/MySQL。测试通过本地模拟禅道控制器及隔离 MySQL 验证；真实禅道环境需使用个人账号完成联调，平台用户导入并不等同单点登录。

### 标准交付与自定义编排

普通需求／Bug、发布版本和独立变更均可不创建模板直接发起标准交付。三步准备为「范围与环境 → 制品与步骤 → 预检提交」，来源使用服务端分页的搜索选择窗，显式排除变更进入不可变快照。生成草稿不连接外部资源、不合并代码、不执行任何写操作。

服务端生成规则版本为 1：按应用去重发布节点，按应用＋逻辑资源分组 SQL／Redis，Nacos 再按配置文档分组；预检沿用实际目标解析、结构／数据基线、成功台账及严格执行边界。跨类型默认推荐 SQL → Nacos → 应用发布 → Redis → 人工确认，必须由操作者核对业务依赖；节点声明的前置依赖不满足时阻断。只涉及配置／SQL 的交付没有应用制品选择。

环境管理可配置 1–10 个强制审核阶段（或签／会签），标准与自定义交付都继承，模板自身的审核规则额外保留，不能删减或跳阶段。策略保存在现有 deploy_meta，兼容 SQLite 与 MySQL；策略版本及环境保护摘要冻结到执行单，修改后必须重新预检审核。未配置阶段时标准交付沿用必要的单阶段环境审核，SQL／Redis 不免审；平台目录中的不可用人员不能满足审批资格。

本次编辑可以重排步骤、补资源、准备代码和保存模板，兼容的制品及草稿不因辅助操作丢失。明确保存为模板才增加模板记录；保存失败仍可返回原草稿。刷新／退出后的完整草稿恢复尚未提供，关闭前有丢失提醒；已提交执行单不受影响，代码准备任务仍支持原有恢复。

新增只读接口 POST /api/delivery/draft-plan；交付请求 strategy_kind=standard 无 template_id，custom 使用已有模板；缺省字段保持旧模板语义。source_type=changes 明确选择独立变更，source_change_ids 保留其初始范围，selected_change_ids 记录本次选择。预检凭证绑定完整请求与计划；同凭证重试找回原执行单，不能用不同输入复用。提交和审核都不会自动开始执行。

验证脚本：scripts/test-standard-delivery-ui.mjs（业务接口全部模拟），后端 TestStandard* 与可选 TestMySQLStandardDeliveryPolicyAndPartialScope。未进行真实业务库、Git、Nacos、Redis 或生产部署写入。

研发工作项默认查询“全部产品”：禅道 16.0 使用原生产品=all 搜索统一排序、分页，再按连接配置的允许产品范围过滤。平台过滤使用游标；来源候选总数不冒充过滤后的精确总数。选具体产品仍保留原生精确分页。列表查询不会导入整页 Bug，显式打开工作项才登记交付引用。

实时查询性能：每个账号/连接会话只缓存原生搜索初始化与当前 WHERE 条件（5 分钟上限），不缓存 Bug 结果、上线意图或交付状态。重复筛选与翻页直接实时读取列表，换筛选只重建条件；会话/连接变更、其他 Bug 操作页、读取失败与超时都会失效重建。会话内完整搜索与读取串行，避免多窗口串条件。每个来源页的本地关联/上线意图以一次批量查询读取，快速切换筛选时取消旧请求。

生产待上线采用候选编号优先：平台只按显式上线意图、来源关联和冻结的生产成功交付记录筛选候选；不使用登记快照中的标题、人员或状态筛选。无候选不扫描禅道；有候选每批最多 100 个编号，通过固定只读 `api.getModel → bug.getByList` 获取列表字段（不读取描述），刷新当前已认证来源会话的产品/执行可见范围、Bug 列表与 QA 视图权限，再应用实时筛选、排序和分页。平台产品白名单仍生效，越界/重复响应失败关闭；不缓存返回的 Bug，不导入或回写。来源未开启 `apiGetModel`、旧自定义来源缺少可验证权限结构或队列超过 5000 个候选时保留原生实时列表路径。超时、失效会话与其他异常不会回退本地旧数据。候选范围变化后的分页游标要求重新查询第一页。

环境用途 purpose（development/test/staging/production/other）与 protected 保护策略独立。旧环境仅按 dev/test/uat/staging/prod 等标准标识兼容识别用途，不将“受保护”自动认作“生产”；自定义标识在环境管理中指定用途。缺省字段的旧客户端更新保留已配置用途。SQLite 与 MySQL 均保存和迁移用途字段。

“我的待上线”默认本人解决、已关闭、明确 release_required=true 且没有完整生产成功交付证据；不再让研发选择核对环境。按所有 production 用途环境核对，任一生产环境存在完整成功交付即计为已上线，失败、部分交付、测试发布和研发准备流程不计入。多生产域需要分别追踪发布覆盖时仍在交付记录查看各环境，待上线不等同于多地域全部完成。未判定上线意图单独进入“上线待判定”，不默认混入待上线。无生产用途环境时明确提示配置，不猜测保护环境。

工作项“发起交付”直接进入标准交付，不因已存在研发流程模板就强制跳进准备流程。development/test 的新标准交付默认不追加通用 SQL/Redis 审核节点；明确配置的保护策略、审核阶段、人工变更和自定义编排不删除，数据库/Redis 工作台的独立审批开关保持原义。生产按环境保护、审核阶段与编排策略执行。保存、查询、提交执行单都不自动部署或执行变更。
