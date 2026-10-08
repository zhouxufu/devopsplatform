# DevOpsPlatform 部署

应用接入、构建工具隔离、Java 标准发布及 estate-ai 配置示例见 [应用构建与发布](APPLICATION-DELIVERY.md)。

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

服务器架构为 `aarch64` 时把 `amd64` 改为 `arm64`。发布包包含平台、日志、监控、部署、MCP 插件、构建步骤和两种架构的日志 Agent，不需要在服务器安装 Go 或 Node.js。

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
- 安装并启动 `devopsplatform.service`；
- 请求 `/api/health` 验证服务，失败时输出最近日志；
- 安装 `/usr/local/bin/devopsplatformctl` 日常管理命令。

以后升级：

```bash
devopsplatformctl upgrade /tmp/devopsplatform-0.3.0-linux-amd64.tar.gz
```

也可以在开发机一条命令完成上传和安装/升级：

```bash
./scripts/publish-server.sh root@192.168.0.32 dist/devopsplatform-0.3.0-linux-amd64.tar.gz
```

升级使用版本目录和原子软链接切换，`data` 与 `config` 永不覆盖；新版本 30 秒内未通过健康检查会自动恢复上一版本。手动管理命令：

```bash
devopsplatformctl status
devopsplatformctl logs
devopsplatformctl restart
devopsplatformctl version
devopsplatformctl rollback
```

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
├── data/
└── config/devopsplatform.env
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

多行日志遇到下一首行时立即输出，文件暂时没有新行时会在短暂等待后输出最后一条；在日志搜索中点击该行可展开完整堆栈。合并发生在采集端，以各日志文件为边界，不会把不同主机或文件的堆栈拼在一起。

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

server 同理：`logserver.service`，注意 `-data` 指向数据盘目录。

## K8s（DaemonSet）

```bash
kubectl apply -f logagent-daemonset.yaml
```

## Docker（server）

```bash
docker build -f Dockerfile.server -t logplatform-server .
docker run -d -p 9801:9801 -v /data/logplatform:/data logplatform-server \
  -token <shared-token> -data /data -retention-days 7 -disk-gb 200
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
