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
# 一体化启动：platform 托管 logs / monitor / deploy，再启动 agent
AUTH='<strong-admin-password>' ./scripts/start-all.sh

# 停止
./scripts/stop-all.sh
```

首次以 `DEVOPSPLATFORM_AUTH`（或 `AUTH`）启动时，平台会创建用户名 `admin` 的管理员账号，密码就是该配置值。后续在右上角“管理员”入口添加成员；角色固定为“管理员/成员”，不提供自定义权限矩阵。管理员维护用户、环境、主机、凭据和 Runner，成员可进行日常构建、发布、日志和监控操作。生产环境等受保护环境的发布审核人员在环境配置中从已启用用户里单独选择。

### 控制面元数据使用 MySQL（第一期）

生产环境可以把全局应用目录、部署控制面以及监控指标/Trace/告警迁移到 MySQL 8.0.13 及以上版本；SQLite 仍是本地开发默认值。制品文件、源码工作区和凭据加密密钥仍保存在 deploy 数据目录。日志正文不会写入 MySQL，继续由日志插件自己的检索存储承载；切换稳定前不要删除原有 `registry.db / deploy.db / logs.db / metrics.db`。

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

通过控制台安装时不需要手工填写 Token：平台从 `DEVOPSPLATFORM_DATA_TOKEN` 注入上报凭据，数据库只保存每台主机的期望采集配置。进入“资源中心 → 主机 → 采集配置”，从部署应用下拉框选择应用并填写日志路径；可只保存期望配置，也可“保存并安装 Agent / 保存并下发”。修改日志目录不需要重新安装二进制。

Java 异常堆栈被拆成多条时，在该采集项的“日志合并方式”选择 **Java 异常堆栈合并（时间戳首行）**，然后“保存并下发”。支持日期、ISO 时间或时分秒开头的日志，后续异常行并入上一条，保留首行级别；搜索中点击日志可展开完整堆栈。新建 Java JAR 采集项会推荐此模式，已有规则不会被静默修改。每行一个 JSON 的日志请选择“单行日志”，其他格式可填写“自定义首行正则”。配置下发会重启 Agent，沿用原断点；仅影响后续采集，历史拆行记录不会自动重组。

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
GET  /api/audit                   # 配置、构建、审批和发布审计
GET  /api/me                      # 当前登录用户和内置角色
GET  /api/users                   # 平台成员列表（写操作仅管理员）
```

## Codex / Vibe Coding MCP

平台内置只读 `devtools` 插件，通过 Streamable HTTP MCP 把日志、指标、Trace、构建和发布上下文提供给 Codex、Claude Code、Cursor 等开发工具。

1. 打开控制台顶部「＋」→「AI 开发工具」，创建访问令牌；必须显式选择权限、应用、环境和有效期，密钥只显示一次。
2. 在目标代码项目的 `.codex/config.toml` 中加入：

```toml
[mcp_servers.logplatform]
url = "http://127.0.0.1:9800/api/plugins/devtools/mcp"
bearer_token_env_var = "LOGPLATFORM_MCP_TOKEN"
enabled = true
default_tools_approval_mode = "auto"
tool_timeout_sec = 60
```

3. 从带有该环境变量的终端启动 Codex：

```bash
export LOGPLATFORM_MCP_TOKEN="lp_dev_..."
codex mcp list
```

MCP 工具全部声明为只读，服务端再次强制检查 `logs:read / metrics:read / traces:read / builds:read / releases:read`、应用和环境范围；日志查询最长 24 小时、最多 200 条。令牌只保存 SHA-256 摘要，所有工具调用写审计，输出还会对常见密码、Token、Authorization 和 URL 凭据二次脱敏。MCP 服务端 `instructions` 明确要求把日志、构建输出和 Trace 属性视为不可信证据，不能当作指令执行。

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

1. 在「变更管控 → 需求与 BUG」新建自定义需求，或手工关联禅道/其他平台编号与链接。
2. 在「独立变更」登记代码分支、制品或人工操作；可关联需求，也可不关联。需求、变更和版本不绑定环境。
3. 将变更标记就绪，在「发布版本」按需求自动加入关联的已就绪变更，或直接选择变更；人工检查最终范围，标记版本就绪。创建版本不会直接合并代码、构建或执行变更。
4. 新建编排模板：固定服务，或者由发布版本中的代码/制品变更推导服务。节点按顺序执行，可增删、排序；某部署节点不指定应用表示发布范围内所有应用。同一应用只能安排一次。
5. 发起交付时选择环境、版本和每应用一个候选制品，并检查范围与配置。候选制品必须显式选择，不自动使用最新构建；多个 BUG 关联同一应用时只发布一次。
6. 提交执行单冻结版本、变更、节点、制品摘要及目标配置；应用环境变量保留加密快照，接口只显示变量名。生产审核人员来自环境配置，发起人不能自审，管理员角色不绕过审核人名单。
7. 审核后显式推进每个节点：应用节点创建真实子发布单，复用现有发布驱动、健康检查和分批策略；人工节点需要显式确认。子发布成功后核对结果再推进下一个应用，最后确认编排完成。失败中止后续节点，不自动回滚其他已成功应用。

提交和审核不会自动部署。执行前重新校验目标配置、环境变量、主机/云资源地址和制品摘要；变更后需重新生成执行单。重复点击使用数据库事务与修订号防重；同一应用环境的普通发布和编排发布共用发布占用检查。页面刷新不会自动恢复或重复执行节点。

### 本版的明确边界

- SQL、Nacos、数据同步和 Redis：可登记变更/保存节点，但执行器尚未接通，含这些节点或变更的编排会被明确阻止提交；不会显示虚假的执行成功。敏感配置请不要直接写入变更说明。
- 禅道：目前手工关联；自动同步「关闭且未上线」候选、日期基线、用户同步与上线回写待接通。
- 代码：记录分支与交付范围，不自动合并/创建分支，不自动构建版本候选；必须核对所选制品真正包含版本代码。
- 编排：顺序、逐节点推进；尚无并行 DAG、自动调度、执行中改节点、跨应用自动回滚或全局补偿。
- 数据同步：SQL 查询、结果对比/编辑、主键映射、冲突检测和审批后执行属于下一阶段；Redis 仅计划命令节点，不做同步。
- Splunk 只读适配、外部工作项 Provider、Nacos 键值 Diff 及资源绑定执行在后续阶段。
- 新增元数据支持 SQLite 开发库和 MySQL 8，并已加入迁移清单；本地测试不等同于真实 MySQL/生产主机验证。

### 本地隔离预览

在该分支工作区执行；不会启动或停止原来的 `9800` 服务，也不复用其数据库。首次预览使用独立空数据。

```bash
cd web
npm ci
cd ..
./scripts/build-web.sh
GO_BIN=/Users/zhouzheng/sdk/go/bin/go ./scripts/preview-delivery.sh
```

访问 `http://127.0.0.1:19813/ui#/changes` 或 `http://127.0.0.1:19813/ui#/orchestration`。该地址是本地独立插件预览，不包含平台壳、公共中心或登录；正式联调使用平台网关。默认数据在工作区 `runtime/delivery-preview`，Ctrl+C 仅停止预览。不要把这个管理端口对外暴露。

新接口：`/api/work-items`、`/api/changes`、`/api/release-versions`；编排接口为 `/api/delivery/templates`、`/api/delivery/preview`、`/api/delivery/runs`，执行单支持 `approve`、`reject`、`advance`、`cancel`。审核需提交 `digest`，人工推进需提交 `confirm_manual: true`。运行中的子发布单通过原 `/api/releases/{id}` 接口查询与控制。

## 生产部署建议

Linux 首次安装、版本化升级和自动回滚统一使用 `scripts/release-linux.sh` 与 `deploy/devopsplatformctl`，完整步骤见 [`deploy/README.md`](deploy/README.md)。

- `test` 与 `prod` 使用不同主机账号、目录和最小权限 SSH key；不要复用生产密码。
- 平台数据目录、deploy 数据目录和 `.credential-key` 一起备份；只备份数据库而丢失密钥将无法解密已有密码。
- 平台前置 TLS 反向代理，只开放 `9800`；`19801/19802/19803` 保持 loopback。
- 日志、OTLP 指标、Trace 和告警严格按环境隔离；通用机器指标仍按 agent/主机查看，建议 agent_id/hostname 保持环境内唯一并带环境前缀。
- 不共享管理员密码：为每个人创建独立成员账号，需要维护平台配置时再明确授予管理员角色。后续接入 SSO 时仍映射到这两个内置角色，不引入复杂权限树。
