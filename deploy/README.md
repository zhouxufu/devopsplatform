# DevOpsPlatform 部署

应用接入、构建工具隔离、Java 标准发布及 estate-ai 配置示例见 [应用构建与发布](APPLICATION-DELIVERY.md)。

插件当前安装与开发方式见 [插件协议 v1](PLUGIN-PROTOCOL.md)；后续逐步统一插件机制的规划见 [Future：全局插件协议与渐进演进](FUTURE-PLUGIN-ARCHITECTURE.md)。Future 是设计草案，尚未实现。

## 最简生产部署

本机只需要执行一次发布脚本。第二个参数是版本号；省略时使用 UTC 时间戳，因此每次升级天然生成不同版本：

```bash
GO_BIN=/Users/zhouzheng/sdk/go/bin/go ./scripts/release-linux.sh amd64 0.2.0
```

生成三个文件：

```text
dist/devopsplatform-0.2.0-linux-amd64.tar.gz
dist/devopsplatform-0.2.0-linux-amd64.tar.gz.sha256
dist/devopsplatformctl
```

服务器架构为 `aarch64` 时把 `amd64` 改为 `arm64`。发布包包含平台、日志、监控、部署、MCP 插件、构建步骤、官方 VictoriaLogs 二进制及两种架构的日志 Agent，不需要在服务器安装 Go、Node.js 或临时下载 VictoriaLogs。目标机需 Linux/systemd、Bash、tar、SHA-256 工具、util-linux 的 flock，以及 curl 或 wget；带认证的外部存储健康检查需要 curl。

首次安装只上传发布包、校验文件和管理命令：

```bash
scp dist/devopsplatform-0.2.0-linux-amd64.tar.gz* dist/devopsplatformctl root@192.168.0.32:/tmp/
ssh root@192.168.0.32
chmod +x /tmp/devopsplatformctl
/tmp/devopsplatformctl install /tmp/devopsplatform-0.2.0-linux-amd64.tar.gz
```

安装程序自动完成以下工作：

- 创建 `devopsplatform` 低权限用户；
- 创建 `/home/devopsplatform/releases`、`data`、`config`；
- 自动生成控制台密码和日志/OTLP 数据面 Token；
- 安装并启动本机 `victorialogs.service`，确认存储健康后再启动 `devopsplatform.service`；
- 检查平台 `/api/health` 与存储 `/health`，失败则明确返回错误；
- 安装 `/usr/local/bin/devopsplatformctl` 日常管理命令。

已使用本版管理命令后，升级仍是一条命令：

```bash
devopsplatformctl upgrade /tmp/devopsplatform-0.3.0-linux-amd64.tar.gz
```

从旧版管理命令首次升级时，一并上传新发布包旁的 `dist/devopsplatformctl`，用 `/tmp/devopsplatformctl upgrade <发布包>` 执行。旧命令不会自动获得新的依赖管理逻辑；新命令后续会从校验通过的发布包原子更新自身。

也可以在开发机一条命令完成上传和安装/升级：

```bash
./scripts/publish-server.sh root@192.168.0.32 dist/devopsplatform-0.3.0-linux-amd64.tar.gz
```

升级使用版本目录和原子软链接切换，保留 `data` 与已有配置值。VictoriaLogs 版本未变且配置未改时不会重启存储；存储发生变化时先停平台，再更换存储程序，依赖健康后切换平台。平台启动/健康检查失败时尝试恢复旧平台；存储升级失败时平台保持停止，不自动降级存储或回退数据，修复后可重试同一发布包。手动管理命令：

```bash
devopsplatformctl status
devopsplatformctl start                 # 存储健康后启动平台
devopsplatformctl stop                  # 先停平台，再停本机存储
devopsplatformctl logs                  # 本机模式跟踪两项服务
devopsplatformctl logs victorialogs
devopsplatformctl restart               # 默认重启平台；存储配置变更也会生效
devopsplatformctl restart victorialogs   # 先停平台，再重启存储并恢复平台
devopsplatformctl version
devopsplatformctl version victorialogs
devopsplatformctl rollback              # 仅回滚平台，不降级存储或回滚数据
```

本机存储配置为 `/home/devopsplatform/config/victorialogs.env`，全新安装默认回环 `9428`、保留 7 天、磁盘目标 `20GiB`、缓存额度 `256MiB`；已有保留期（例如 365 天）优先沿用，不重置为 7 天。参数、旧手工服务接管条件与外部存储见 [VictoriaLogs 运维](VICTORIALOGS.md)。安装/升级串行互斥；平台回滚使用独立的存储版本链接，避免老平台包隐式降低已使用数据目录的引擎版本。

默认配置文件是 `/home/devopsplatform/config/devopsplatform.env`：

```ini
DEVOPSPLATFORM_ADDR=0.0.0.0:9800
DEVOPSPLATFORM_AUTH=自动生成的控制台密码
DEVOPSPLATFORM_DATA_TOKEN=自动生成的日志和OTLP上报Token
```

首次启动会用 `DEVOPSPLATFORM_AUTH` 创建 `admin` 管理员账号。登录后点击控制台右上角管理员入口添加成员；只有“管理员、成员”两个内置角色。环境开启审批保护时，从已启用成员中选择发布审核人员，不再手填 `X-User` 字符串。成员可以构建、发布和查看日志监控，但主机、凭据、环境、Runner 与用户管理仅管理员可修改。

生产环境改用 MySQL 8 时，配置应用目录、部署和监控三组数据库变量；三个组件必须使用独立 schema。首次切换要先停服务并分别执行 `migrate-registry`、`migrate-deploy`、`migrate-monitor`。完整 SQL、迁移与验收命令见项目根目录 [README](../README.md#控制面元数据使用-mysql第一期)。部署插件的源码工作区、制品文件和 `.credential-key` 仍保存在 `/home/devopsplatform/data/deploy-data`，不能因启用 MySQL 而删除。

修改配置后执行 `devopsplatformctl restart`。内网只需开放 `9800`（控制台）、`9801`（日志上报）、`9802`（机器指标和 OTLP）；`19801～19804` 始终只监听回环地址。

## 应用运行环境变量

在“应用 → 环境 → 编辑配置 → 应用环境变量”中按行填写 `KEY=value`。变量按“应用 + 环境”隔离并加密保存，查询接口和发布日志只显示变量名；创建发布单时会生成加密快照，审批期间修改环境配置不会影响该发布单。

- SSH JAR：发布时写入部署目录下的 `.devopsplatform.env`（权限 `0600`），并在普通启动命令前自动加载。如果启动命令是 `systemctl start/restart`，systemd 单元还需配置 `EnvironmentFile=/部署目录/.devopsplatform.env`。
- Docker 主机：平台生成独立 env-file，并追加到 `docker run`；若同时配置了手工 env-file，应用环境变量优先。
- 阿里云 SAE：平台转换成 SAE 环境变量；与旧的 SAE 原生 JSON 重名时，应用环境变量优先。

编辑时留空表示保留已有值；填写内容会整体替换；勾选“清空已保存的环境变量”才会删除。变量值不允许包含换行或 NUL。

服务端目录结构：

```text
/home/devopsplatform/
├── current -> releases/0.3.0
├── previous -> releases/0.2.0
├── releases/
├── storage/
│   ├── current -> releases/<VictoriaLogs二进制SHA256>
│   └── releases/
├── data/victorialogs/
└── config/
    ├── devopsplatform.env
    └── victorialogs.env
```

平台运行本身不依赖构建工具。只有使用本机 Runner 构建业务项目时，服务器才需要按项目安装 Git、JDK、Maven/Gradle、Node.js 或 Docker。

## 本地一键启停

```bash
./scripts/start-all.sh   # platform(9800, 托管 logs/monitor 插件) + agent
./scripts/stop-all.sh
# 数据持久化在 ./runtime/（重启机器不丢）
```

## Java 服务接入监控（OTel javaagent，零代码改动）

1. 下载 [opentelemetry-javaagent.jar](https://github.com/open-telemetry/opentelemetry-java-instrumentation/releases)（如放到 `~/otel/`）
2. IDEA → Run/Debug Configurations → 服务的 VM options 加：

```
-javaagent:/Users/zhouzheng/otel/opentelemetry-javaagent.jar
-Dotel.service.name=estate-ai-seek
-Dotel.resource.attributes=deployment.environment.name=test,service.instance.id=estate-ai-seek-test-01
-Dotel.exporter.otlp.endpoint=http://127.0.0.1:9802
-Dotel.exporter.otlp.headers=X-Token=demo-token
-Dotel.traces.exporter=otlp
-Dotel.metric.export.interval=60000
```

> 每个同时运行的进程必须使用不同且稳定的 `service.instance.id`；Kubernetes 推荐从 Pod UID 注入，虚机可用“主机名+进程名”。javaagent 默认走 http/protobuf（接收端按 Content-Type 自动识别 protobuf/JSON，勿配 `http/json`——Java 的 metrics 导出不支持）。路径必须是绝对路径，JVM 不展开 `~`。不需要 Trace 时可改回 `-Dotel.traces.exporter=none`。

3. 重启服务 → 监控大盘「☕ JVM」页出现该服务的 CPU/堆/线程/QPS/耗时/GC

## systemd（物理机/虚机）

推荐方式：控制台进入「资源 → 主机」，先完成 SSH 测试，再点「采集配置」。日志应用必须从已接入的部署应用中选择，平台保存每台主机的期望状态（接收地址、运行账号、缓冲上限、应用 ID、日志路径和多行规则）。首次点“保存并安装 Agent”，平台会自动识别 Linux amd64/arm64、上传匹配二进制、写入配置并启用 systemd；以后修改日志目录点“保存并下发”即可，不会重复上传 Agent 二进制。

安装页面不要求填写上报 Token。平台从 `/home/devopsplatform/config/devopsplatform.env` 的 `DEVOPSPLATFORM_DATA_TOKEN` 统一注入，Token 不保存到部署数据库或返回给浏览器，只会进入目标机权限为 `0600` 的 `/etc/logagent/agent.yaml`。SSH 用户需为 root 或具备免密 sudo，目标机需能访问日志数据面 `:9801` 和监控数据面 `:9802`。

**异常堆栈合并：** 在“采集配置 → 日志合并方式”选择“Java 异常堆栈合并（时间戳首行）”，点“保存并下发”即可，无需升级或重装 Agent。此预设适用于日期/时间开头的日志，将 `at`、`Caused by`、`Suppressed` 等后续行并入同一条日志。自定义格式可选“自定义首行正则”；JSON 单行日志保留“单行日志”。已有配置不会自动改写，历史拆行数据也不会自动合并。

手工配置时，在对应 `collect` 项中填写以下规则，再重启 `logagent`（不要删除 registry 或 spool）：

```yaml
multiline: '^\s*\[?(\d{4}[-/]\d{2}[-/]\d{2}[ T]|\d{2}:\d{2}:\d{2})'
```

**带颜色的 Spring Boot / p6spy：** 新版 Agent 使用原文优先、去除 ANSI SGR 颜色后补充匹配的方式识别首行，因此已有 Java 时间戳规则仍可使用；原始内容、偏移和事件 ID 保持稳定。日志服务在入库前去掉颜色码，让级别、Trace ID 与正文检索使用干净文本。需更新 Agent 和日志服务后生效，单独下发配置不会更新二进制；历史记录不重写，平台事件时间仍为采集时间。

旧 Agent 暂未升级时，可显式选择“自定义首行正则”使用下面的兼容规则并保存下发，解决彩色首行被合并到上一条的问题；正文颜色清理仍需要更新日志服务：

```yaml
multiline: '^(\s|\x1b\[[0-9;:]*m)*\[?(\d{4}[-/]\d{2}[-/]\d{2}[ T]|\d{2}:\d{2}:\d{2})'
```

多行日志遇到下一首行时立即输出，文件暂时没有新行时会在短暂等待后输出最后一条；在日志搜索中点击该行可展开完整堆栈。合并发生在采集端，以各日志文件为边界，不会把不同主机或文件的堆栈拼在一起。

Agent 本地待发送队列已改为分段顺序文件，原 `spool` 配置不变，实际目录在该路径后追加 `.queue`。升级需先停止旧 Agent；旧 SQLite 积压和安装身份一次性导入，原库保留，不直接删除或用旧二进制降级。容量、重放及坏块边界见 [分段队列说明与实测](AGENT-SEGMENT-QUEUE.md)。

发布平台前先生成两种架构的安装包：

```bash
GO_BIN=/path/to/go AGENT_VERSION=0.4.0 ./scripts/build-agents.sh
# 产物在 dist/agents/，包含 SHA256SUMS
```

手动安装仍然可用：

```bash
# 1. 安装二进制与配置
sudo cp dist/agents/logagent-linux-amd64 /usr/local/bin/logagent # 按目标架构选择
sudo mkdir -p /etc/logagent /var/lib/logagent
sudo cp agent.yaml /etc/logagent/agent.yaml   # 按机器修改 host/environment/collect

# 2. 安装服务单元
sudo cp logagent.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now logagent
journalctl -u logagent -f
```

通过 `devopsplatformctl` 安装平台时会自动准备 VictoriaLogs；独立日志服务、Docker 和开发方式见 [配置与切换](VICTORIALOGS.md)。`logserver.service` 的 `-data` 指向本地投递状态目录。

## K8s（DaemonSet）

```bash
kubectl apply -f logagent-daemonset.yaml
```

## Docker（server）

```bash
export DEVOPSPLATFORM_DATA_TOKEN='替换为实际共享令牌'
docker compose -f deploy/compose.logs.yaml up -d --build
```

## traceId 规则（可选）

server 启动加 `-rules /etc/logplatform/rules.json`，规则文件热加载（30s）：

```json
[
  {"service": "estate-ai-seek", "regex": "traceId=([0-9A-Za-z-]+)"},
  {"service": "*",             "regex": "tid[=:](\\w{8,32})"}
]
```

无规则文件时使用内置规则（JSON trace_id/traceId 字段 + 通用正则）。

## Arthas 诊断控制台

入口：**监控 → 运行诊断 → Arthas 诊断**，复用 monitor 插件和平台管理员身份。

1. 优先点击“接入部署实例”，选择应用、环境对应的已部署 Java 主机或 Docker 容器。平台沿用主机组、容器名称、SSH 凭据和主机指纹，读取实际 JVM；唯一进程自动选中，多个进程必须明确选择。检测不会附加，点击“接入所选 JVM”才上传并启动 Arthas。不会停止或重启业务 JVM。
2. 对于平台之外已有的 Arthas，使用“手动连接”填写名称、应用标识、环境标识和 HTTP 根地址，例如 `http://127.0.0.1:8563`。这里的 localhost 指 monitor 服务所在机器。不要填写 `/api` 子路径，也不要在 URL 中写账号或密码。保存手动配置不会连接或执行命令。
3. 默认进入“常用诊断”。选择实例后点击 JVM 概览、内存使用、CPU 热点线程或阻塞线程即可采样，不必先建立控制台会话；只有一个候选实例时自动选中，但不会自动执行。结果默认展示摘要/指标表格，原始输出按需展开。
4. 目标开启认证时，可在“编辑连接”明确填写认证用户名和密码后保存。凭据使用 AES-GCM 加密，独立于公开配置存放；密码不回显、不传给 AI。密码留空保持原配置，可明确清除；改变地址、应用或环境会清除旧认证，防止把原主机密码发往新目标。备份监控数据库时必须同时备份数据目录下 `arthas-secrets/credentials.key`，其权限为 0600；已有凭据而密钥缺失时拒绝连接和重新生成替代密钥。
5. “高级控制台”支持复用保存的认证，或临时填写仅供当前会话使用的密码。快捷项只填入命令，点击“执行”才发送；自定义命令需明确确认目标。临时密码不会自动保存。高级输出只在当前窗口保留最近 20 次，单次远端响应上限 1 MiB。诊断结果不等于应用持续健康。

首版使用 [Arthas 官方 HTTP API](https://arthas.aliyun.com/doc/http-api.html)，支持有界同步命令、中断和断开；单次执行上限 10 秒，不提供持续流式终端或 Tunnel 自动发现。会话最长 15 分钟、空闲 5 分钟过期，每个管理员最多 4 个、全局最多 20 个。应用/环境切换不会更换已连接 JVM；目标不一致时必须先断开再连接。断线、响应不明或超限不自动重放命令，需中断或断开并核对目标。

连接新增/修改/移除及连接、执行、中断、断开均记录诊断审计；只存操作者、目标、命令类型/摘要、时间与结果状态，不存命令正文或输出。活动连接禁止改址，配置修改有修订冲突保护。管理面必须继续只允许可信平台网关访问；沿用现有平台登录、网关身份注入及管理员/成员模型。

### 部署实例接入条件与运维

- 支持平台有部署记录、仍绑定在应用环境主机组内的 `ssh-jar`、`docker-host`。Linux 目标必须有可读 `/proc`、sh/stat/readlink/sed/awk、Java 8+ 和可用的 Attach / Instrument 能力。JDK 优先使用 Java Attach；JRE 或缺少工具的运行时自动使用随部署插件打包的 jattach v2.2，不再通过是否存在 jcmd 判定可否接入。Java 主机通过部署目录/固定 app.jar 路径确定 JVM，SSH 用户须为 JVM 用户或 root；需要宿主机 curl，root 代运行需 runuser。Docker 容器需 sh，宿主机 SSH 需 root、Docker、nsenter、curl；不自动 sudo、安装 JDK、修改镜像、拉取辅助容器或追加特权。缺少 shell 的 distroless、禁用 Attach / 动态 Agent、裁掉 java.instrument 的运行时、SAE/Kubernetes 暂不支持自动接入。
- 平台下载固定 Arthas 4.0.5 完整发行包，按代码内 SHA-256 校验并重新打包必要文件；目标机不需要出网或安装下载工具。采用官方 core 启动方式，绕过该版 boot 在关闭 Telnet 时的探测错误。临时附加进程使用 32–128 MiB 堆，不改变业务 JVM 参数。
- 轻量路径支持 Linux x64 / arm64：官方 jattach v2.2 静态发行包与 Apache-2.0 许可证随部署插件嵌入（`internal/deploy/assets/arthas`），使用前验证固定 SHA-256、ELF 架构及无动态解释器，兼容 glibc/musl 而不依赖目标安装包管理器或平台访问 GitHub。以 JVM 的 UID/GID 执行唯一固定操作 `load instrument false <受控目录>/arthas-agent.jar`；认证仅由私有属性文件提供，不出现在命令参数中，不把 jattach 变成用户可传任意命令的入口。实现依据 [jattach 官方文档](https://github.com/jattach/jattach) 及 [Arthas 在 JRE 中的用法](https://www.alibabacloud.com/blog/600018)。
- Agent 只监听目标网络命名空间内 127.0.0.1 的随机诊断端口，关闭 Telnet，生成随机认证并强制本地认证；不发布容器端口、不调整网络。主机 curl 通过 SSH stdin 发送 JSON，Docker 使用 nsenter 进入目标网络命名空间；镜像内不需要 curl。认证放在远端 0600 私有文件中，不出现在命令行、审计、日志或浏览器。部署元数据加密保存实例绑定、进程身份及私有配置文件引用。
- 每次诊断重新校验主机绑定、容器 ID、宿主 PID、JVM PID/启动标识。重启或替换后的实例必须重新检测接入，旧句柄不会静默转向新 JVM。已接入实例只重新握手；附加结果不确定时只核对，不自动重放 attach。失败发生在附加之前时允许修复条件后重新操作。
- 接入最长 150 秒，同实例并发互斥、全局最多 4 个；进度与按钮状态留在原弹窗，关闭客户端不会取消服务端已发出的附加。移除连接仅隐藏平台连接，不停止 Arthas/JVM；目标临时目录由实例生命周期/运维清理，不在已加载时自动删除。部署数据库及其凭据密钥需一并备份。
- monitor 通过 `-deploy` 或 `DEVOPSPLATFORM_DEPLOY_INTERNAL` 指定可信内部部署管理地址，默认 `http://127.0.0.1:19803`。必须一并更新 platform/deploy/monitor：`/api/arthas-managed/` 是后端内部协议，平台网关拒绝所有公开插件别名访问；用户统一经过 monitor 管理员校验、确认与诊断审计，不能直接调用 SSH 代理。管理端口继续仅向可信本机后端开放。

### 通过平台 MCP 使用

沿用 `/api/plugins/devtools/mcp`，无需再给 AI 客户端配置每个 JVM 的 MCP 地址或密码。管理员本人在“MCP 访问管理”创建令牌时，单独勾选 `arthas:read` 并限定应用和环境；该权限默认不选中，既有令牌不会自动扩权，成员账号及未绑定本人账号的旧令牌不能执行。

- `list_arthas_instances(service, environment)`：查询明确应用、环境的候选实例，返回 ID、名称和配置修订号，不返回地址或认证信息，不连接目标。
- `diagnose_jvm(service, environment, instance_id, revision, diagnostic)`：`diagnostic` 只接受 `overview`、`memory`、`hot_threads`、`blocked_threads`。结果与实际目标/修订二次核对，使用服务器端认证，单次有界采样结束后关闭独立会话；失败或不确定时不重试。

本轮通过平台统一 MCP 适配现有 Arthas HTTP API，没有另建 MCP 协议栈。官方 Arthas MCP/流式任务、方法追踪表单、堆转储和运行时修改不在这两个常用工具的开放范围；后续接入仍需沿用平台授权、确认与审计。

### 应用资源接入与 MCP 配置

应用设置 → 应用资源绑定可按环境维护实际连接、schema 或 Nacos Data ID/Group；“环境对照”展示声明、已绑定、缺失和核验状态。“识别依赖”只读取受管理工作区或本次上传的 bootstrap/pom、所选环境的 Nacos，先预览候选，选入草稿后再保存。保存与目标核验都不会执行 SQL、Redis 变更或发布配置；核验成功不证明业务写权限。

管理员本人可另建显式包含 `resources:read`／`resources:write` 的 MCP 令牌，旧令牌不自动扩权：

- `list_resource_connections`：只返回公开连接参数、修订及最近核验；指定应用和环境可读取该环境已登记的 Nacos 接入候选，不要求新应用先有绑定。省略范围查询全局连接仍要求应用、环境都授权 `*`。
- `check_application_resources`、`discover_application_resources`：明确一个应用／环境；首次识别须明确选择该环境登记的 Nacos，且来源 profile 的地址和命名空间完整匹配，不能借 profile 或连接名切到其他环境。
- `configure_application_resources`：预览该环境自有声明／绑定，保留其他环境和共享引用。
- `configure_resource_connection`：引用同类型已保存的凭据，服务器复制当前快照；不通过 MCP 传入或回显密码。要求应用、环境都授权 `*`，源凭据后续轮换不会自动修改副本。

写工具先返回 10 分钟有效的冻结预览。展示目标和差异并明确同意后，调用 `confirm_platform_action`；应用／连接变化需重新预览，结果未知只查询 `get_platform_action`，不得自动重放。新凭据仍在管理员连接编辑器维护。

应用 ID 与 Spring 运行名不同时，在“识别依赖”明确填写运行名；受管理源码仍检查当前产物模块，保留原 Nacos prefix，不改源码。固定 Data ID 配置明确指定 Data ID/Group；Flink `common + jobs` 先选一个作业再识别，不展开其他作业或全部租户库。候选别名规范为平台逻辑别名并保留源名，保存来源仓库、产物、文件摘要与 profile；识别条件改变后须重新识别。

环境核验区分“未绑定”“此环境未使用”“源配置待完善”“引用配置缺失”。这些来源观察只解释没有绑定的环境；已绑定目标始终独立核验，不能以来源说明掩盖连接失败。历史未登记来源的资源保持待核对，不猜填开发／测试／生产差异。
